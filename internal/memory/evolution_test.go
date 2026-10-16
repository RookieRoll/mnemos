package memory_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func evolutionInput(title string) memory.SaveInput {
	return memory.SaveInput{Title: title, Content: "assertion " + title, Type: memory.TypePattern, Project: "project"}
}

func TestEvidenceIdentitySafetyAndNoFetching(t *testing.T) {
	svc, db, _ := readingFixture(t)
	ctx := context.Background()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	fingerprint := strings.Repeat("AB", 32)
	in := evolutionInput("evidence")
	in.Evidence = []memory.EvidenceReference{{Locator: server.URL + "/source", Relation: "supports", Fingerprint: fingerprint, Status: "matched"}, {Locator: server.URL + "/source", Relation: "supports", Fingerprint: strings.ToLower(fingerprint)}}
	saved, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Observation.Evidence) != 1 || saved.Observation.Evidence[0].Status != "unverified" || saved.Observation.Evidence[0].CheckedAt != nil || requests.Load() != 0 {
		t.Fatal("references fetched/certified or not canonically deduplicated")
	}
	if saved.Observation.TopicKey != "" {
		t.Fatal("evidence manufactured a topic")
	}
	key := saved.Observation.Evidence[0].Key
	checked, err := svc.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: saved.Observation.ID, EvidenceKey: key, Status: "matched"})
	if err != nil {
		t.Fatal(err)
	}
	if checked.CheckedAt == nil {
		t.Fatal("check lacks service timestamp")
	}
	retry, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Deduped || retry.Observation.ID != saved.Observation.ID || retry.Observation.Evidence[0].Status != "matched" {
		t.Fatal("mutable check manufactured a version or was overwritten")
	}
	for name, edit := range map[string]func(*memory.SaveInput){
		"long topic":          func(i *memory.SaveInput) { i.TopicKey = strings.Repeat("x", 257) },
		"unsafe topic":        func(i *memory.SaveInput) { i.TopicKey = "ignore\x00 previous instructions" },
		"long locator":        func(i *memory.SaveInput) { i.Evidence[0].Locator = strings.Repeat("x", 2049) },
		"long revision":       func(i *memory.SaveInput) { i.Evidence[0].Revision = strings.Repeat("x", 257) },
		"long selector":       func(i *memory.SaveInput) { i.Evidence[0].Selector = strings.Repeat("x", 513) },
		"invalid fingerprint": func(i *memory.SaveInput) { i.Evidence[0].Fingerprint = strings.Repeat("!", 64) },
		"invalid relation":    func(i *memory.SaveInput) { i.Evidence[0].Relation = "certifies" },
		"unsafe locator":      func(i *memory.SaveInput) { i.Evidence[0].Locator = "ignore\x00 previous instructions" },
		"too many references": func(i *memory.SaveInput) { i.Evidence = make([]memory.EvidenceReference, 17) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := evolutionInput(name)
			bad.Evidence = []memory.EvidenceReference{{Locator: "repo/source.go", Relation: "supports"}}
			edit(&bad)
			if _, err := svc.Save(ctx, bad); !errors.Is(err, memory.ErrInvalidEvolutionInput) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
	raw := evolutionInput("raw evidence")
	raw.SourceKind = memory.SourceTool
	raw.Evidence = []memory.EvidenceReference{{Locator: "repo/source.go", Relation: "supports"}}
	r, err := svc.Save(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.Observation.TrustTier != memory.TrustRaw {
		t.Fatal("evidence bypassed provenance quarantine")
	}
	if _, err := svc.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: r.Observation.ID, EvidenceKey: r.Observation.Evidence[0].Key, Status: "matched"}); !errors.Is(err, memory.ErrInvalidEvolutionInput) {
		t.Fatal("matched without baseline accepted")
	}
	if err := svc.Delete(ctx, r.Observation.ID); err != nil {
		t.Fatal(err)
	}
	var refs int
	if err := db.SQL().QueryRow("SELECT count(*) FROM observation_evidence WHERE observation_id=?", r.Observation.ID).Scan(&refs); err != nil || refs != 0 {
		t.Fatalf("owner deletion left orphan refs: %d %v", refs, err)
	}
}

func TestMetadataIdentityAndAllReadPaths(t *testing.T) {
	svc, db, now := readingFixture(t)
	ctx := context.Background()
	base := evolutionInput("same prose")
	a := base
	a.TopicKey = "  architecture/auth  "
	a.Evidence = []memory.EvidenceReference{{Locator: "repo/auth.go", Relation: "supports"}}
	first, err := svc.Save(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Save(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Evidence = []memory.EvidenceReference{{Locator: "repo/exception.go", Relation: "refutes"}}
	third, err := svc.Save(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if first.Observation.ID == second.Observation.ID || third.Observation.ID == first.Observation.ID || first.Observation.ContentHash != second.Observation.ContentHash {
		t.Fatal("metadata identity lost or legacy content hashes rewritten")
	}
	looked, err := db.Observations().FindByContentHash(ctx, "default", "project", first.Observation.ContentHash)
	if err != nil || looked == nil || looked.ID != second.Observation.ID {
		t.Fatalf("legacy lookup selected metadata-bearing row: %+v %v", looked, err)
	}
	hits, _, err := svc.SearchWithMode(ctx, memory.SearchInput{Query: "prose", Project: "project", TopicKey: " architecture/auth "})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("exact topic search count=%d", len(hits))
	}
	for _, hit := range hits {
		if len(hit.Observation.Evidence) != 1 {
			t.Fatal("search omitted evidence")
		}
	}
	got, err := svc.Get(ctx, first.Observation.ID)
	if err != nil || got.TopicKey != "architecture/auth" || len(got.Evidence) != 1 {
		t.Fatal("get omitted metadata")
	}
	batch, err := svc.GetMany(ctx, memory.GetManyInput{IDs: []string{first.Observation.ID}, Project: "project"})
	if err != nil || len(batch.Items[0].Observation.Evidence) != 1 {
		t.Fatalf("batch omitted metadata: %v", err)
	}
	window, err := svc.Neighborhood(ctx, memory.NeighborhoodInput{ID: first.Observation.ID, Project: "project"})
	if err != nil || len(window.Items[0].Summary.Evidence) != 1 {
		t.Fatalf("neighborhood omitted metadata: %v", err)
	}
	before, err := db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: first.Observation.ID, EvidenceKey: first.Observation.Evidence[0].Key, Status: "unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		before[i].Evidence = nil
		after[i].Evidence = nil
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("check changed owner lifecycle/trust/popularity")
	}
	// No project remains a real legacy namespace, not an all-project selector.
	global := base
	global.Project = ""
	one, err := svc.Save(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	two, err := svc.Save(ctx, global)
	if err != nil || !two.Deduped || two.Observation.ID != one.Observation.ID || one.Observation.ID == second.Observation.ID {
		t.Fatal("empty project dedup scope broken")
	}
	_ = now
}

func TestReplacementPoliciesReplayAndHistory(t *testing.T) {
	svc, db, now := readingFixture(t)
	ctx := context.Background()
	oldInput := evolutionInput("rule")
	oldInput.Pinned = true
	oldInput.TopicKey = "rules"
	oldInput.Evidence = []memory.EvidenceReference{{Locator: "repo/old.go", Relation: "supports"}}
	old, err := svc.Save(ctx, oldInput)
	if err != nil {
		t.Fatal(err)
	}
	nextInput := evolutionInput("revised rule")
	nextInput.ReplacesID = old.Observation.ID
	nextInput.TTLDays = 1
	nextInput.ValidFrom = &now
	next, err := svc.Save(ctx, nextInput)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Observation.Pinned || next.Observation.ExpiresAt != nil || next.Observation.TopicKey != "rules" || len(next.Observation.Evidence) != 0 {
		t.Fatal("pin/topic inheritance or explicit-only evidence broken")
	}
	storedOld, err := svc.Get(ctx, old.Observation.ID)
	if err != nil || storedOld.InvalidatedAt == nil || len(storedOld.Evidence) != 1 {
		t.Fatal("old version/evidence not preserved")
	}
	// The successor's mutable state and later supersession cannot change the
	// original request identity stored on its incoming supersession edge.
	if err := db.Observations().SetPinned(ctx, next.Observation.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := db.Observations().SetTrustTier(ctx, next.Observation.ID, memory.TrustSkill); err != nil {
		t.Fatal(err)
	}
	later := evolutionInput("third version")
	later.ReplacesID = next.Observation.ID
	if _, err := svc.Save(ctx, later); err != nil {
		t.Fatal(err)
	}
	laterSvc := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now.Add(time.Hour) }})
	offset := now.In(time.FixedZone("offset", 8*3600))
	nextInput.ValidFrom = &offset
	replay, err := laterSvc.Save(ctx, nextInput)
	if err != nil || !replay.Deduped || replay.Observation.ID != next.Observation.ID {
		t.Fatalf("stable retry after lifecycle change failed: %+v %v", replay, err)
	}
	changed := nextInput
	changed.TTLDays = 2
	if _, err := laterSvc.Save(ctx, changed); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("different requested lifecycle not conflict: %v", err)
	}
	for name, change := range map[string]func(*memory.SaveInput){
		"raw":     func(i *memory.SaveInput) { i.SourceKind = memory.SourceTool },
		"project": func(i *memory.SaveInput) { i.Project = "different" },
		"agent":   func(i *memory.SaveInput) { i.AgentID = "other" },
		"topic":   func(i *memory.SaveInput) { i.TopicKey = "other" },
		"future":  func(i *memory.SaveInput) { v := now.Add(time.Hour); i.ValidFrom = &v },
	} {
		t.Run(name, func(t *testing.T) {
			target := evolutionInput(name + " target")
			target.TopicKey = "original"
			saved, err := svc.Save(ctx, target)
			if err != nil {
				t.Fatal(err)
			}
			candidate := evolutionInput(name + " successor")
			candidate.ReplacesID = saved.Observation.ID
			change(&candidate)
			if _, err := svc.Save(ctx, candidate); err == nil {
				t.Fatal("invalid replacement accepted")
			}
			state, err := svc.Get(ctx, saved.Observation.ID)
			if err != nil || state.InvalidatedAt != nil {
				t.Fatal("rejected replacement invalidated target")
			}
		})
	}
	ackTarget := evolutionInput("pinned ack")
	ackTarget.Pinned = true
	target, err := svc.Save(ctx, ackTarget)
	if err != nil {
		t.Fatal(err)
	}
	ack := evolutionInput("unpinned successor")
	ack.ReplacesID = target.Observation.ID
	ack.UnpinReplacement = true
	ack.TTLDays = 1
	result, err := svc.Save(ctx, ack)
	if err != nil || result.Observation.Pinned || result.Observation.ExpiresAt == nil {
		t.Fatal("explicit unpin acknowledgement ignored")
	}
}

func TestConcurrentReplacementAcrossIndependentSQLiteHandles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	one := memory.NewService(memory.Config{Store: a.Observations(), Clock: clock})
	two := memory.NewService(memory.Config{Store: b.Observations(), Clock: clock})
	old, err := one.Save(ctx, evolutionInput("concurrent target"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*memory.Service{one, two} {
		wg.Add(1)
		go func(i int, svc *memory.Service) {
			defer wg.Done()
			<-start
			in := evolutionInput([]string{"writer one", "writer two"}[i])
			in.ReplacesID = old.Observation.ID
			_, err := svc.Save(ctx, in)
			errs <- err
		}(i, svc)
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, memory.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	var owners, links int
	if err := a.SQL().QueryRow("SELECT count(*) FROM observations").Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := a.SQL().QueryRow("SELECT count(*) FROM observation_links").Scan(&links); err != nil {
		t.Fatal(err)
	}
	if owners != 2 || links != 1 {
		t.Fatal("losing writer left partial version state")
	}
}

func TestReplacementAndExistingLinkRollback(t *testing.T) {
	for _, stage := range []string{"evidence", "link", "invalidation"} {
		t.Run(stage, func(t *testing.T) {
			svc, db, _ := readingFixture(t)
			ctx := context.Background()
			old, err := svc.Save(ctx, evolutionInput("rollback target"))
			if err != nil {
				t.Fatal(err)
			}
			trigger := map[string]string{"evidence": `CREATE TRIGGER fail_evolution BEFORE INSERT ON observation_evidence BEGIN SELECT RAISE(ABORT,'evidence failed'); END`, "link": `CREATE TRIGGER fail_evolution BEFORE INSERT ON observation_links BEGIN SELECT RAISE(ABORT,'link failed'); END`, "invalidation": `CREATE TRIGGER fail_evolution BEFORE UPDATE OF invalidated_at ON observations BEGIN SELECT RAISE(ABORT,'invalidation failed'); END`}[stage]
			if _, err := db.SQL().Exec(trigger); err != nil {
				t.Fatal(err)
			}
			in := evolutionInput("rollback successor")
			in.ReplacesID = old.Observation.ID
			in.Evidence = []memory.EvidenceReference{{Locator: "repo/source.go", Relation: "supports"}}
			if _, err := svc.Save(ctx, in); err == nil {
				t.Fatal("failure injection did not fail")
			}
			var owners, edges, refs int
			_ = db.SQL().QueryRow("SELECT count(*) FROM observations").Scan(&owners)
			_ = db.SQL().QueryRow("SELECT count(*) FROM observation_links").Scan(&edges)
			_ = db.SQL().QueryRow("SELECT count(*) FROM observation_evidence").Scan(&refs)
			if owners != 1 || edges != 0 || refs != 0 {
				t.Fatalf("partial rollback owners=%d edges=%d refs=%d", owners, edges, refs)
			}
			state, err := svc.Get(ctx, old.Observation.ID)
			if err != nil || state.InvalidatedAt != nil {
				t.Fatal("failure invalidated original")
			}
		})
	}
	svc, db, _ := readingFixture(t)
	ctx := context.Background()
	old, _ := svc.Save(ctx, evolutionInput("existing old"))
	next, _ := svc.Save(ctx, evolutionInput("existing next"))
	if _, err := db.SQL().Exec(`CREATE TRIGGER fail_existing BEFORE UPDATE OF invalidated_at ON observations BEGIN SELECT RAISE(ABORT,'existing invalidation failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, next.Observation.ID, old.Observation.ID, memory.LinkSupersedes); err == nil {
		t.Fatal("existing link failure ignored")
	}
	var links int
	_ = db.SQL().QueryRow("SELECT count(*) FROM observation_links").Scan(&links)
	if links != 0 {
		t.Fatal("existing supersedes left a partial link")
	}
}

func TestFidelityOwnerEvidenceIdentity(t *testing.T) {
	svc, db, _ := readingFixture(t)
	ctx := context.Background()
	in := evolutionInput("fidelity")
	in.TopicKey = "topic"
	in.Evidence = []memory.EvidenceReference{{Locator: "repo/original.go", Relation: "supports"}}
	saved, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	owners, links, err := svc.ExportEvolutionSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bad := owners[0]
	bad.Evidence = []memory.EvidenceReference{{Locator: "repo/different.go", Relation: "supports"}}
	if _, err := svc.Restore(ctx, &bad); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("direct restore appended different evidence: %v", err)
	}
	if err := svc.ValidateRestoreMetadata(ctx, []memory.Observation{bad}, links); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("snapshot preflight ignored evidence identity: %v", err)
	}
	legacy := owners[0]
	legacy.TopicKey = ""
	legacy.Evidence = nil
	if inserted, err := svc.Restore(ctx, &legacy); err != nil || inserted {
		t.Fatalf("old dump skip failed: %v", err)
	}
	rows, err := db.Observations().Export(ctx)
	if err != nil || len(rows[0].Evidence) != 1 || rows[0].TopicKey != "topic" {
		t.Fatal("legacy skip lost metadata")
	}
	copySvc, _, _ := readingFixture(t)
	if n, err := copySvc.RestoreEvolutionSnapshot(ctx, owners, links); err != nil || n != 1 {
		t.Fatalf("fresh fidelity restore failed: %d %v", n, err)
	}
	if n, err := copySvc.RestoreEvolutionSnapshot(ctx, owners, links); err != nil || n != 0 {
		t.Fatalf("matching repeated restore failed: %d %v", n, err)
	}
	if _, err := svc.Get(ctx, saved.Observation.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultTimeReplacementRetryAndExistingLinkGuards(t *testing.T) {
	svc, db, now := readingFixture(t)
	ctx := context.Background()
	old, err := svc.Save(ctx, evolutionInput("implicit clock target"))
	if err != nil {
		t.Fatal(err)
	}
	in := evolutionInput("implicit clock successor")
	in.ReplacesID = old.Observation.ID
	in.TTLDays = 3
	in.Evidence = []memory.EvidenceReference{{Locator: "repo/b.go", Relation: "supports"}, {Locator: "repo/a.go", Relation: "refutes"}}
	saved, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	advanced := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now.Add(time.Hour) }})
	in.Evidence[0], in.Evidence[1] = in.Evidence[1], in.Evidence[0]
	replay, err := advanced.Save(ctx, in)
	if err != nil || !replay.Deduped || replay.Observation.ID != saved.Observation.ID {
		t.Fatalf("default clock/canonical evidence retry failed: %v", err)
	}
	invalid := in
	badTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	invalid.ValidUntil = &badTime
	if _, err := advanced.Save(ctx, invalid); !errors.Is(err, memory.ErrInvalidEvolutionInput) {
		t.Fatalf("malformed explicit time was hashed without error: %v", err)
	}
	if err := svc.Supersede(ctx, saved.Observation.ID, saved.Observation.ID); !errors.Is(err, memory.ErrConflict) {
		t.Fatal("self supersession allowed")
	}
	if err := svc.Supersede(ctx, old.Observation.ID, saved.Observation.ID); !errors.Is(err, memory.ErrConflict) {
		t.Fatal("supersession cycle allowed")
	}
	for name, edit := range map[string]func(*memory.SaveInput){
		"raw":    func(i *memory.SaveInput) { i.SourceKind = memory.SourceTool },
		"scope":  func(i *memory.SaveInput) { i.Project = "other" },
		"future": func(i *memory.SaveInput) { v := now.Add(time.Hour); i.ValidFrom = &v },
	} {
		t.Run(name, func(t *testing.T) {
			target, err := svc.Save(ctx, evolutionInput("link "+name+" target"))
			if err != nil {
				t.Fatal(err)
			}
			candidate := evolutionInput("link " + name + " candidate")
			edit(&candidate)
			source, err := svc.Save(ctx, candidate)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Link(ctx, source.Observation.ID, target.Observation.ID, memory.LinkSupersedes); err == nil {
				t.Fatal("unsafe existing-version link accepted")
			}
		})
	}
	pinnedInput := evolutionInput("existing pinned target")
	pinnedInput.Pinned = true
	pinnedInput.TopicKey = "core"
	pinned, err := svc.Save(ctx, pinnedInput)
	if err != nil {
		t.Fatal(err)
	}
	sourceInput := evolutionInput("existing pinned successor")
	sourceInput.TTLDays = 1
	source, err := svc.Save(ctx, sourceInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Supersede(ctx, source.Observation.ID, pinned.Observation.ID); err != nil {
		t.Fatal(err)
	}
	current, err := svc.Get(ctx, source.Observation.ID)
	if err != nil || !current.Pinned || current.ExpiresAt != nil || current.TopicKey != "core" {
		t.Fatal("existing supersession lost pin/topic policy")
	}
}

func TestSnapshotRejectsSecondSuccessorWithoutPartialOwners(t *testing.T) {
	svc, db, now := readingFixture(t)
	ctx := context.Background()
	seedReading(t, db, now, "a", "b", "c")
	owners, err := db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	links := []memory.LinkEdge{{SourceID: "b", TargetID: "a", LinkType: memory.LinkSupersedes, LinkedAt: now}, {SourceID: "c", TargetID: "a", LinkType: memory.LinkSupersedes, LinkedAt: now}}
	fresh, freshDB, _ := readingFixture(t)
	if _, err := fresh.RestoreEvolutionSnapshot(ctx, owners, links); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("competing imported successors accepted: %v", err)
	}
	var count int
	if err := freshDB.SQL().QueryRow("SELECT count(*) FROM observations").Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid graph partially restored owners")
	}
	if err := svc.Supersede(ctx, "b", "a"); err != nil {
		t.Fatal(err)
	}
	owners, err = db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RestoreEvolutionSnapshot(ctx, owners, links[1:]); !errors.Is(err, memory.ErrConflict) {
		t.Fatalf("incoming successor bypassed existing successor: %v", err)
	}
}

type evolutionEmbedder struct{}

func (evolutionEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}
func (evolutionEmbedder) Dimension() int { return 2 }
func (evolutionEmbedder) Model() string  { return "evolution-fixture" }

func TestExactTopicFilterCoversVectorRecallAndGraphExpansion(t *testing.T) {
	plain, db, now := readingFixture(t)
	ctx := context.Background()
	hybrid := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now }, Embedder: evolutionEmbedder{}, Hybrid: memory.HybridParams{Alpha: 0.01}})
	anchorInput := evolutionInput("vector anchor")
	anchorInput.TopicKey = "allowed"
	anchorInput.Importance = 10
	anchor, err := hybrid.Save(ctx, anchorInput)
	if err != nil {
		t.Fatal(err)
	}
	wrongVector := evolutionInput("wrong vector")
	wrongVector.TopicKey = "other"
	wrongVector.Importance = 10
	if _, err := hybrid.Save(ctx, wrongVector); err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{"allowed", "other"} {
		in := evolutionInput("graph " + topic)
		in.TopicKey = topic
		in.Importance = 10
		leaf, err := plain.Save(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if err := plain.Link(ctx, anchor.Observation.ID, leaf.Observation.ID, memory.LinkRefines); err != nil {
			t.Fatal(err)
		}
	}
	hits, mode, err := hybrid.SearchWithMode(ctx, memory.SearchInput{Query: "unmatchedkeyword", Project: "project", TopicKey: " allowed ", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if mode != memory.RetrievalHybrid || len(hits) != 2 {
		t.Fatalf("expected vector anchor plus eligible graph leaf: mode=%s hits=%+v", mode, hits)
	}
	expanded := false
	for _, hit := range hits {
		if hit.Observation.TopicKey != "allowed" {
			t.Fatal("topic leaked through vector or graph recall")
		}
		if hit.ExpandedFrom != "" {
			expanded = true
		}
	}
	if !expanded {
		t.Fatal("fixture did not exercise graph expansion")
	}
}
