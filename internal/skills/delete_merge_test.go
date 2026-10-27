package skills_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// skillFixture is one fresh store plus the service under test. Delete, merge,
// and promotion consistency all depend on the real schema (the skills triggers
// and the skills_fts mirror), so these tests always run against SQLite rather
// than a fake store.
type skillFixture struct {
	db  *storage.DB
	svc *skills.Service
	ctx context.Context
}

func newSkillFixture(t *testing.T) *skillFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &skillFixture{
		db:  db,
		svc: skills.NewService(skills.Config{Store: db.Skills()}),
		ctx: context.Background(),
	}
}

// save writes one skill version and returns the stored row.
func (f *skillFixture) save(t *testing.T, in skills.SaveInput) *skills.Skill {
	t.Helper()
	sk, err := f.svc.Save(f.ctx, in)
	if err != nil {
		t.Fatalf("save %q: %v", in.Name, err)
	}
	return sk
}

// bump saves the same (agent, name) versions times, adding a distinct step each
// time so every version carries distinguishable content. Returns the final row.
func (f *skillFixture) bump(t *testing.T, versions int, in skills.SaveInput) *skills.Skill {
	t.Helper()
	var sk *skills.Skill
	for i := 0; i < versions; i++ {
		in.Procedure += " step" + strconv.Itoa(i+1)
		sk = f.save(t, in)
	}
	return sk
}

// ftsRowCount reports how many rows the skills full-text mirror and the skills
// table each hold. The mirror is an external-content FTS5 table fed by the
// skills triggers, so the two counts must always agree: a mismatch means the
// mirror and the table have drifted apart.
func (f *skillFixture) ftsRowCount(t *testing.T) (fts, base int) {
	t.Helper()
	if err := f.db.SQL().QueryRow(`SELECT count(*) FROM skills_fts`).Scan(&fts); err != nil {
		t.Fatalf("count skills_fts: %v", err)
	}
	if err := f.db.SQL().QueryRow(`SELECT count(*) FROM skills`).Scan(&base); err != nil {
		t.Fatalf("count skills: %v", err)
	}
	return fts, base
}

// seedDuplicateName creates two skills named deploy-checklist at versions 2 and
// 3. (agent_id, name) is unique, so a same-name pair can only span agents, which
// is exactly the duplicate shape the merge command folds together.
func (f *skillFixture) seedDuplicateName(t *testing.T) (v2, v3 *skills.Skill) {
	t.Helper()
	v2 = f.bump(t, 2, skills.SaveInput{
		AgentID:        "alpha",
		Name:           "deploy-checklist",
		Description:    "alpha deploy runbook",
		Procedure:      "alpha steps: kubectl apply alphaonly",
		Tags:           []string{"team:alpha", "project:api"},
		SourceSessions: []string{"sess-alpha"},
	})
	v3 = f.bump(t, 3, skills.SaveInput{
		AgentID:        "beta",
		Name:           "deploy-checklist",
		Description:    "beta deploy runbook",
		Procedure:      "beta steps: helm upgrade betaonly",
		Tags:           []string{"team:beta", "project:api"},
		SourceSessions: []string{"sess-beta"},
	})
	return v2, v3
}

func skillNames(list []skills.Skill) []string {
	out := make([]string, 0, len(list))
	for _, sk := range list {
		out = append(out, sk.Name)
	}
	return out
}

func hasString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// TestSkillDeleteRemovesAndKeepsFTSInSync is the skill-lifecycle delete
// contract: the row goes, the full-text mirror stays in step with the table, and
// a search stops returning the deleted skill.
func TestSkillDeleteRemovesAndKeepsFTSInSync(t *testing.T) {
	cases := []struct {
		name        string
		seed        func(*skillFixture)
		in          skills.DeleteInput
		wantRemoved int
		wantLeft    []string
	}{
		{
			name: "single name removes only its match",
			seed: func(f *skillFixture) {
				f.save(t, skills.SaveInput{Name: "deploy-checklist", Description: "deploy runbook", Procedure: "kubectl apply"})
				f.save(t, skills.SaveInput{Name: "release-notes", Description: "notes", Procedure: "write the notes"})
			},
			in:          skills.DeleteInput{Name: "deploy-checklist"},
			wantRemoved: 1,
			wantLeft:    []string{"release-notes"},
		},
		{
			name: "project scope keeps the other project's skill",
			seed: func(f *skillFixture) {
				f.save(t, skills.SaveInput{AgentID: "alpha", Name: "deploy-checklist", Description: "api deploy", Procedure: "kubectl apply", Tags: []string{"project:api"}})
				f.save(t, skills.SaveInput{AgentID: "beta", Name: "deploy-checklist", Description: "web deploy", Procedure: "kubectl apply", Tags: []string{"project:web"}})
			},
			in:          skills.DeleteInput{Name: "deploy-checklist", Scope: skills.Scope{Project: "api"}},
			wantRemoved: 1,
			wantLeft:    []string{"deploy-checklist"},
		},
		{
			name: "no match removes nothing",
			seed: func(f *skillFixture) {
				f.save(t, skills.SaveInput{Name: "deploy-checklist", Description: "deploy runbook", Procedure: "kubectl apply"})
			},
			in:          skills.DeleteInput{Name: "ghost-skill"},
			wantRemoved: 0,
			wantLeft:    []string{"deploy-checklist"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSkillFixture(t)
			c.seed(f)

			before, err := f.svc.Match(f.ctx, skills.MatchInput{Query: "deploy", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(before) < c.wantRemoved {
				t.Fatalf("setup: want at least %d searchable rows, got %d", c.wantRemoved, len(before))
			}

			res, err := f.svc.Delete(f.ctx, c.in)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if len(res.Rows) != c.wantRemoved {
				t.Fatalf("affected rows = %d, want %d", len(res.Rows), c.wantRemoved)
			}

			removed := make(map[string]bool, len(res.Rows))
			for _, sk := range res.Rows {
				removed[sk.ID] = true
				if _, err := f.svc.Get(f.ctx, sk.ID); !errors.Is(err, skills.ErrNotFound) {
					t.Errorf("skill %q still readable after delete: %v", sk.Name, err)
				}
			}

			list, err := f.svc.List(f.ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := len(list); got != len(c.wantLeft) {
				t.Errorf("skills left = %d, want %d (%v)", got, len(c.wantLeft), skillNames(list))
			}

			after, err := f.svc.Match(f.ctx, skills.MatchInput{Query: "deploy", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if want := len(before) - c.wantRemoved; len(after) != want {
				t.Errorf("search results = %d, want %d", len(after), want)
			}
			for _, m := range after {
				if removed[m.Skill.ID] {
					t.Errorf("deleted skill %q is still returned by search", m.Skill.Name)
				}
			}

			fts, base := f.ftsRowCount(t)
			if fts != base {
				t.Errorf("skills_fts holds %d rows but skills holds %d: orphan index rows after delete", fts, base)
			}
		})
	}
}

// TestSkillMergeKeepsHighestVersion is the default merge contract: the highest
// version survives, the other match is absorbed and removed, and the keeper
// carries both rows' content, tags, and provenance.
func TestSkillMergeKeepsHighestVersion(t *testing.T) {
	f := newSkillFixture(t)
	v2, v3 := f.seedDuplicateName(t)
	if v2.Version != 2 || v3.Version != 3 {
		t.Fatalf("setup versions = %d/%d, want 2/3", v2.Version, v3.Version)
	}

	before, err := f.svc.Match(f.ctx, skills.MatchInput{Query: "deploy", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("setup: want 2 searchable duplicates, got %d", len(before))
	}

	res, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Keeper == nil {
		t.Fatal("merge returned no keeper")
	}
	if res.Keeper.ID != v3.ID {
		t.Errorf("keeper = %q (v%d), want the v3 row %q", res.Keeper.ID, res.Keeper.Version, v3.ID)
	}
	if want := v3.Version + 1; res.Keeper.Version != want {
		t.Errorf("keeper version = %d, want %d (the merge writes through the versioning path)", res.Keeper.Version, want)
	}
	if len(res.Superseded) != 1 || res.Superseded[0].ID != v2.ID {
		t.Fatalf("superseded = %v, want the v2 row %q", res.Superseded, v2.ID)
	}

	// Both bodies survive in the keeper; the absorbed one is labelled.
	for _, want := range []string{"betaonly", "alphaonly", "## Merged from alpha v2"} {
		if !strings.Contains(res.Keeper.Procedure, want) {
			t.Errorf("keeper procedure missing %q:\n%s", want, res.Keeper.Procedure)
		}
	}
	for _, want := range []string{"team:alpha", "team:beta", "project:api"} {
		if !hasString(res.Keeper.Tags, want) {
			t.Errorf("keeper tags %v missing %q", res.Keeper.Tags, want)
		}
	}
	for _, want := range []string{"sess-alpha", "sess-beta"} {
		if !hasString(res.Keeper.SourceSessions, want) {
			t.Errorf("keeper source sessions %v missing %q", res.Keeper.SourceSessions, want)
		}
	}

	list, err := f.svc.List(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != v3.ID {
		t.Fatalf("skills after merge = %v, want only the keeper", skillNames(list))
	}

	// The superseded row is gone from the store and from the index, while its
	// content is still findable through the keeper.
	if _, err := f.svc.Get(f.ctx, v2.ID); !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("superseded skill %q still readable: %v", v2.ID, err)
	}
	hits, err := f.svc.Match(f.ctx, skills.MatchInput{Query: "alphaonly", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Skill.ID != v3.ID {
		t.Errorf("search for absorbed content = %v, want the keeper only", hits)
	}
	if fts, base := f.ftsRowCount(t); fts != base {
		t.Errorf("skills_fts holds %d rows but skills holds %d: orphan index rows after merge", fts, base)
	}

	// A second merge selects one row and writes nothing.
	again, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist"})
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if len(again.Superseded) != 0 {
		t.Errorf("second merge superseded %d rows, want 0", len(again.Superseded))
	}
	if again.Keeper == nil || again.Keeper.Version != res.Keeper.Version {
		t.Errorf("second merge bumped the version: %v -> %d", again.Keeper, res.Keeper.Version)
	}
}

// TestSkillMergeKeepsExplicitVersion covers --keep-version: the named version is
// the keeper even when a higher version exists, and an unmatched version is an
// error that writes nothing.
func TestSkillMergeKeepsExplicitVersion(t *testing.T) {
	f := newSkillFixture(t)
	v2, v3 := f.seedDuplicateName(t)

	res, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist", KeepVersion: 2})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Keeper == nil || res.Keeper.ID != v2.ID {
		t.Fatalf("keeper = %v, want the v2 row %q", res.Keeper, v2.ID)
	}
	if want := v2.Version + 1; res.Keeper.Version != want {
		t.Errorf("keeper version = %d, want %d", res.Keeper.Version, want)
	}
	if len(res.Superseded) != 1 || res.Superseded[0].ID != v3.ID {
		t.Fatalf("superseded = %v, want the v3 row %q", res.Superseded, v3.ID)
	}
	list, err := f.svc.List(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != v2.ID {
		t.Fatalf("skills after merge = %v, want only the v2 keeper", skillNames(list))
	}

	before := list[0].Version
	if _, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist", KeepVersion: 99}); err == nil {
		t.Error("merging with an unknown version should fail")
	}
	list, err = f.svc.List(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Version != before {
		t.Errorf("failed merge wrote to the store: %v", skillNames(list))
	}
}

// TestSkillMergeNoMatch: a name that matches nothing reports zero affected rows
// and no error, so the CLI can exit successfully.
func TestSkillMergeNoMatch(t *testing.T) {
	f := newSkillFixture(t)
	res, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "ghost-skill"})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if res.Keeper != nil || len(res.Superseded) != 0 {
		t.Errorf("empty match should report nothing: keeper=%v superseded=%v", res.Keeper, res.Superseded)
	}
	list, err := f.svc.List(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("empty match wrote %d rows", len(list))
	}
}

// TestSkillMaintenanceDryRunWritesNothing: --dry-run uses the same selection as
// the real run and writes nothing — no row, version, or index change.
func TestSkillMaintenanceDryRunWritesNothing(t *testing.T) {
	cases := []struct {
		name string
		run  func(*skillFixture) (int, error)
		want int
	}{
		{
			name: "delete",
			run: func(f *skillFixture) (int, error) {
				res, err := f.svc.Delete(f.ctx, skills.DeleteInput{Name: "deploy-checklist", DryRun: true})
				return len(res.Rows), err
			},
			want: 2, // both same-name rows, one per agent
		},
		{
			name: "merge",
			run: func(f *skillFixture) (int, error) {
				res, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist", DryRun: true})
				return len(res.Superseded), err
			},
			want: 1, // one keeper survives, one row folds into it
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSkillFixture(t)
			f.seedDuplicateName(t)
			affected, err := c.run(f)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if affected != c.want {
				t.Errorf("dry run reported %d affected rows, want %d", affected, c.want)
			}
			list, err := f.svc.List(f.ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 {
				t.Fatalf("dry run changed the store: %d skills left", len(list))
			}
			for _, sk := range list {
				if sk.Version != 2 && sk.Version != 3 {
					t.Errorf("dry run bumped %q to v%d", sk.Name, sk.Version)
				}
			}
			hits, err := f.svc.Match(f.ctx, skills.MatchInput{Query: "deploy", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 2 {
				t.Errorf("dry run changed the index: %d hits, want 2", len(hits))
			}
		})
	}
}

// TestSkillSelectScopesSelection pins the shared selection rules Delete and
// Merge both rely on, including the direction that matters for a destructive
// command: a project filter never selects a skill that carries no project tag.
func TestSkillSelectScopesSelection(t *testing.T) {
	f := newSkillFixture(t)
	f.save(t, skills.SaveInput{AgentID: "alpha", Name: "deploy-checklist", Description: "api", Procedure: "kubectl apply", Tags: []string{"project:api"}})
	f.save(t, skills.SaveInput{AgentID: "beta", Name: "deploy-checklist", Description: "web", Procedure: "kubectl apply", Tags: []string{"project:web"}})
	f.save(t, skills.SaveInput{AgentID: "gamma", Name: "deploy-checklist", Description: "untagged", Procedure: "kubectl apply"})
	f.save(t, skills.SaveInput{AgentID: "alpha", Name: "release-notes", Description: "notes", Procedure: "write notes"})

	cases := []struct {
		name  string
		scope skills.Scope
		want  int
	}{
		{"unscoped sees every agent", skills.Scope{}, 3},
		{"agent scope", skills.Scope{AgentID: "alpha"}, 1},
		{"project scope", skills.Scope{Project: "api"}, 1},
		{"project filter skips untagged skills", skills.Scope{Project: "untagged"}, 0},
		{"agent and project must both match", skills.Scope{AgentID: "beta", Project: "api"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, err := f.svc.Select(f.ctx, "deploy-checklist", c.scope)
			if err != nil {
				t.Fatalf("select: %v", err)
			}
			if len(rows) != c.want {
				t.Fatalf("selected %d rows, want %d", len(rows), c.want)
			}
			for _, sk := range rows {
				if sk.Name != "deploy-checklist" {
					t.Errorf("selection returned unrelated skill %q", sk.Name)
				}
			}
		})
	}

	if _, err := f.svc.Select(f.ctx, "   ", skills.Scope{}); err == nil {
		t.Error("selecting an empty name should fail")
	}
}

// TestSkillSelectOrdersByVersion makes the keeper choice deterministic: the
// highest version comes first, so the default merge keeper is the same on every
// run and the printed preview is stable.
func TestSkillSelectOrdersByVersion(t *testing.T) {
	f := newSkillFixture(t)
	v2, v3 := f.seedDuplicateName(t)
	rows, err := f.svc.Select(f.ctx, "deploy-checklist", skills.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("selected %d rows, want 2", len(rows))
	}
	if rows[0].ID != v3.ID || rows[1].ID != v2.ID {
		t.Errorf("selection order = [%d, %d], want the v3 row first", rows[0].Version, rows[1].Version)
	}
}

// TestSkillMergeValidatesKeepVersion is the c3#5 contract: KeepVersion is
// validated before and independently of the selection. A negative value is a
// usage error and a version that cannot match is a no-match error, so a caller
// that asked for a specific version never gets a silent success — including when
// the name itself matched nothing.
func TestSkillMergeValidatesKeepVersion(t *testing.T) {
	cases := []struct {
		name    string
		seed    bool
		in      skills.MergeInput
		wantErr string
	}{
		{
			name:    "negative keep version is rejected before selecting",
			in:      skills.MergeInput{Name: "deploy-checklist", KeepVersion: -1},
			wantErr: "keep version must be positive",
		},
		{
			name:    "negative keep version is rejected with matches present",
			seed:    true,
			in:      skills.MergeInput{Name: "deploy-checklist", KeepVersion: -3},
			wantErr: "keep version must be positive",
		},
		{
			name:    "named version with no match is an error",
			in:      skills.MergeInput{Name: "ghost-skill", KeepVersion: 2},
			wantErr: "no match at version 2",
		},
		{
			name:    "named version missing from the matches is an error",
			seed:    true,
			in:      skills.MergeInput{Name: "deploy-checklist", KeepVersion: 99},
			wantErr: "no match at version 99",
		},
		{
			name: "no match without a named version stays a no-op",
			in:   skills.MergeInput{Name: "ghost-skill"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSkillFixture(t)
			if c.seed {
				f.seedDuplicateName(t)
			}
			res, err := f.svc.Merge(f.ctx, c.in)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("merge error = %v, want one containing %q", err, c.wantErr)
				}
				if res != nil {
					t.Errorf("failed merge returned a result: %+v", res)
				}
			} else if err != nil {
				t.Fatalf("merge: %v", err)
			}

			// Validation happens before any write: the store is exactly as seeded.
			list, err := f.svc.List(f.ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if c.seed {
				want = 2
			}
			if len(list) != want {
				t.Fatalf("store holds %d skills, want %d", len(list), want)
			}
			for _, sk := range list {
				if sk.Version != 2 && sk.Version != 3 {
					t.Errorf("failed merge bumped %q to v%d", sk.Name, sk.Version)
				}
			}
		})
	}
}

// TestSkillMergeRollsBackWhenDeleteFails is the atomicity contract c3#3 asked
// for: the keeper rewrite and the superseded-row deletes commit together, so a
// delete that fails leaves the store exactly as it was — no bumped keeper, no
// missing row — and the retry still versions the keeper exactly once.
func TestSkillMergeRollsBackWhenDeleteFails(t *testing.T) {
	f := newSkillFixture(t)
	v2, v3 := f.seedDuplicateName(t)

	// Abort the delete of the superseded row: the merge cannot finish, and with a
	// multi-step write it would have left the keeper already bumped.
	block := fmt.Sprintf(
		"CREATE TRIGGER test_block_merge_delete BEFORE DELETE ON skills WHEN OLD.id = '%s' BEGIN SELECT RAISE(ABORT, 'merge delete blocked'); END",
		v2.ID)
	if _, err := f.db.SQL().Exec(block); err != nil {
		t.Fatalf("install delete blocker: %v", err)
	}

	if _, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist"}); err == nil {
		t.Fatal("merge must fail when the superseded row cannot be deleted")
	}

	kept, err := f.svc.Get(f.ctx, v3.ID)
	if err != nil {
		t.Fatalf("keeper unreadable after a failed merge: %v", err)
	}
	if kept.Version != v3.Version {
		t.Errorf("failed merge bumped the keeper to v%d, want v%d", kept.Version, v3.Version)
	}
	if kept.Procedure != v3.Procedure {
		t.Errorf("failed merge rewrote the keeper body:\n%s", kept.Procedure)
	}
	if _, err := f.svc.Get(f.ctx, v2.ID); err != nil {
		t.Errorf("failed merge removed the superseded row: %v", err)
	}

	// With the obstacle gone the retry lands, and it versions the keeper once —
	// a rollback leaves no bump behind for the retry to repeat.
	if _, err := f.db.SQL().Exec("DROP TRIGGER test_block_merge_delete"); err != nil {
		t.Fatalf("drop delete blocker: %v", err)
	}
	res, err := f.svc.Merge(f.ctx, skills.MergeInput{Name: "deploy-checklist"})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res.Keeper == nil || res.Keeper.ID != v3.ID {
		t.Fatalf("retry keeper = %v, want %q", res.Keeper, v3.ID)
	}
	if want := v3.Version + 1; res.Keeper.Version != want {
		t.Errorf("retry produced v%d, want v%d (the failed attempt must not have bumped anything)",
			res.Keeper.Version, want)
	}
	list, err := f.svc.List(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != v3.ID {
		t.Fatalf("skills after the retry = %v, want only the keeper", skillNames(list))
	}
	if fts, base := f.ftsRowCount(t); fts != base {
		t.Errorf("skills_fts holds %d rows but skills holds %d after merge", fts, base)
	}
}
