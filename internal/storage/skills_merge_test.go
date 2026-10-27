package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// mergeFixture is one database plus its transactional skill store, seeded with
// the duplicate-name pair a merge folds together: a keeper at v2 under "alpha"
// and the superseded row at v1 under "beta". (agent_id, name) is unique, so a
// same-name pair can only span agents — exactly the shape Merge exists for.
type mergeFixture struct {
	db     *DB
	store  skills.Store
	merger skills.MergingStore
	keeper *skills.Skill
	loser  *skills.Skill
}

func newMergeFixture(t *testing.T) *mergeFixture {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := db.Skills()
	merger, ok := store.(skills.MergingStore)
	if !ok {
		t.Fatal("the storage skill store must implement skills.MergingStore")
	}
	f := &mergeFixture{db: db, store: store, merger: merger}
	f.keeper = f.seed(t, skills.SaveInput{
		AgentID: "alpha", Name: "deploy-checklist",
		Description: "alpha deploy runbook", Procedure: "alpha steps: kubectl apply",
		Tags: []string{"team:alpha", "project:api"}, SourceSessions: []string{"sess-alpha"},
	}, 2)
	f.loser = f.seed(t, skills.SaveInput{
		AgentID: "beta", Name: "deploy-checklist",
		Description: "beta deploy runbook", Procedure: "beta steps: helm upgrade",
		Tags: []string{"team:beta", "project:api"}, SourceSessions: []string{"sess-beta"},
	}, 1)
	return f
}

// seed upserts in versions times so the row lands at a known version.
func (f *mergeFixture) seed(t *testing.T, in skills.SaveInput, versions int) *skills.Skill {
	t.Helper()
	var sk *skills.Skill
	for i := 0; i < versions; i++ {
		var err error
		if sk, err = f.store.Upsert(context.Background(), in); err != nil {
			t.Fatalf("seed %q: %v", in.Name, err)
		}
	}
	return sk
}

// fold builds the payload the skill service computes for this pair: the keeper's
// content with procedure as the merged body and extra tags/provenance appended.
func (f *mergeFixture) fold(procedure string, extraTags, extraSources []string) skills.MergeWriteInput {
	return skills.MergeWriteInput{
		KeeperID:       f.keeper.ID,
		KeeperVersion:  f.keeper.Version,
		Description:    f.keeper.Description,
		Procedure:      procedure,
		Pitfalls:       f.keeper.Pitfalls,
		Tags:           append(append([]string(nil), f.keeper.Tags...), extraTags...),
		SourceSessions: append(append([]string(nil), f.keeper.SourceSessions...), extraSources...),
		Losers:         []skills.SkillRef{{ID: f.loser.ID, Version: f.loser.Version}},
	}
}

// skillRowCounts reports the skills table and its full-text mirror row counts: a
// transactional merge must keep the two in step through the table triggers.
func (f *mergeFixture) skillRowCounts(t *testing.T) (base, fts int) {
	t.Helper()
	if err := f.db.sql.QueryRow(`SELECT count(*) FROM skills`).Scan(&base); err != nil {
		t.Fatalf("count skills: %v", err)
	}
	if err := f.db.sql.QueryRow(`SELECT count(*) FROM skills_fts`).Scan(&fts); err != nil {
		t.Fatalf("count skills_fts: %v", err)
	}
	return base, fts
}

// TestSkillStoreMergeCommitsBothHalves covers the happy path and its idempotent
// twin: a content-changing merge rewrites the keeper and removes the superseded
// row in one transaction, while a merge whose content already matches the keeper
// removes the row without a version bump.
func TestSkillStoreMergeCommitsBothHalves(t *testing.T) {
	const mergedBody = "alpha steps: kubectl apply\n\n## Merged from beta v1\n\nbeta steps: helm upgrade"

	cases := []struct {
		name          string
		in            func(*mergeFixture) skills.MergeWriteInput
		wantVersion   func(*mergeFixture) int
		wantProcedure string
		wantTags      []string
	}{
		{
			name: "content change versions the keeper and drops the superseded row",
			in: func(f *mergeFixture) skills.MergeWriteInput {
				return f.fold(mergedBody, []string{"team:beta"}, []string{"sess-beta"})
			},
			wantVersion:   func(f *mergeFixture) int { return f.keeper.Version + 1 },
			wantProcedure: mergedBody,
			wantTags:      []string{"team:alpha", "team:beta", "project:api"},
		},
		{
			name: "unchanged content drops the superseded row without a version bump",
			in: func(f *mergeFixture) skills.MergeWriteInput {
				return f.fold(f.keeper.Procedure, nil, nil)
			},
			wantVersion:   func(f *mergeFixture) int { return f.keeper.Version },
			wantProcedure: "alpha steps: kubectl apply",
			wantTags:      []string{"team:alpha", "project:api"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMergeFixture(t)
			ctx := context.Background()

			merged, err := f.merger.Merge(ctx, c.in(f))
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if merged.ID != f.keeper.ID {
				t.Fatalf("merge returned row %q, want the keeper %q", merged.ID, f.keeper.ID)
			}
			if want := c.wantVersion(f); merged.Version != want {
				t.Errorf("keeper version = %d, want %d", merged.Version, want)
			}
			if merged.Procedure != c.wantProcedure {
				t.Errorf("keeper procedure = %q, want %q", merged.Procedure, c.wantProcedure)
			}
			for _, tag := range c.wantTags {
				if !slices.Contains(merged.Tags, tag) {
					t.Errorf("keeper tags %v missing %q", merged.Tags, tag)
				}
			}
			if _, err := f.store.Get(ctx, f.loser.ID); !errors.Is(err, skills.ErrNotFound) {
				t.Errorf("superseded row still readable: %v", err)
			}
			if base, fts := f.skillRowCounts(t); base != 1 || fts != 1 {
				t.Errorf("skills=%d skills_fts=%d, want one row in each", base, fts)
			}
		})
	}
}

// TestSkillStoreMergeRejectsStaleSelection pins the concurrency semantics: the
// versions the caller observed at selection are a precondition, so a row another
// writer changed or removed between selection and write aborts the merge with
// ErrMergeConflict and leaves every row exactly as that writer left it.
func TestSkillStoreMergeRejectsStaleSelection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*skills.MergeWriteInput)
	}{
		{"keeper version moved on", func(in *skills.MergeWriteInput) { in.KeeperVersion-- }},
		{"keeper gone", func(in *skills.MergeWriteInput) { in.KeeperID = "01nosuchkeeper" }},
		{"superseded version moved on", func(in *skills.MergeWriteInput) { in.Losers[0].Version = 7 }},
		{"superseded row gone", func(in *skills.MergeWriteInput) { in.Losers[0].ID = "01nosuchloser" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMergeFixture(t)
			ctx := context.Background()
			in := f.fold("merged body", []string{"team:beta"}, []string{"sess-beta"})
			c.mutate(&in)

			if _, err := f.merger.Merge(ctx, in); !errors.Is(err, skills.ErrMergeConflict) {
				t.Fatalf("merge error = %v, want skills.ErrMergeConflict", err)
			}
			kept, err := f.store.Get(ctx, f.keeper.ID)
			if err != nil {
				t.Fatalf("keeper unreadable: %v", err)
			}
			if kept.Version != f.keeper.Version || kept.Procedure != f.keeper.Procedure {
				t.Errorf("rejected merge changed the keeper: v%d %q", kept.Version, kept.Procedure)
			}
			if _, err := f.store.Get(ctx, f.loser.ID); err != nil {
				t.Errorf("rejected merge removed the superseded row: %v", err)
			}
			if base, _ := f.skillRowCounts(t); base != 2 {
				t.Errorf("rejected merge changed the table: %d skills", base)
			}
		})
	}
}

// TestSkillStoreMergeLosesToConcurrentWriter walks the interleaving that made the
// old multi-step merge unsafe: another writer versions the keeper after the
// selection. The stale payload is refused, the concurrent content survives, and
// retrying with the re-read version lands the merge exactly once.
func TestSkillStoreMergeLosesToConcurrentWriter(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	in := f.fold("merged body", []string{"team:beta"}, []string{"sess-beta"})

	concurrent, err := f.store.Upsert(ctx, skills.SaveInput{
		AgentID: "alpha", Name: "deploy-checklist",
		Description: "concurrent runbook", Procedure: "concurrent steps",
		Tags: []string{"team:alpha", "project:api"}, SourceSessions: []string{"sess-concurrent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.merger.Merge(ctx, in); !errors.Is(err, skills.ErrMergeConflict) {
		t.Fatalf("stale merge error = %v, want skills.ErrMergeConflict", err)
	}
	kept, err := f.store.Get(ctx, f.keeper.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Version != concurrent.Version || kept.Procedure != "concurrent steps" {
		t.Fatalf("stale merge overwrote the concurrent write: v%d %q", kept.Version, kept.Procedure)
	}

	// Re-selecting (as the caller would on a conflict) and retrying succeeds and
	// versions the row that is now current exactly once.
	in.KeeperVersion = kept.Version
	in.Description = kept.Description
	in.Tags = append(append([]string(nil), kept.Tags...), "team:beta")
	in.SourceSessions = append(append([]string(nil), kept.SourceSessions...), "sess-beta")
	merged, err := f.merger.Merge(ctx, in)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if want := concurrent.Version + 1; merged.Version != want {
		t.Errorf("retry version = %d, want %d", merged.Version, want)
	}
	if merged.Procedure != "merged body" {
		t.Errorf("retry procedure = %q, want the merged body", merged.Procedure)
	}
	if _, err := f.store.Get(ctx, f.loser.ID); !errors.Is(err, skills.ErrNotFound) {
		t.Errorf("superseded row still readable after the retry: %v", err)
	}
}

// TestSkillStoreMergeRollsBackOnDeleteFailure proves the two halves share one
// transaction: when the superseded-row delete fails the keeper rewrite is rolled
// back with it, and a retry once the obstacle clears versions the keeper once.
func TestSkillStoreMergeRollsBackOnDeleteFailure(t *testing.T) {
	f := newMergeFixture(t)
	ctx := context.Background()
	block := fmt.Sprintf(
		"CREATE TRIGGER test_block_merge_delete BEFORE DELETE ON skills WHEN OLD.id = '%s' BEGIN SELECT RAISE(ABORT, 'merge delete blocked'); END",
		f.loser.ID)
	if _, err := f.db.SQL().Exec(block); err != nil {
		t.Fatalf("install delete blocker: %v", err)
	}

	in := f.fold("merged body", []string{"team:beta"}, []string{"sess-beta"})
	if _, err := f.merger.Merge(ctx, in); err == nil {
		t.Fatal("merge must fail when the superseded row cannot be deleted")
	}

	kept, err := f.store.Get(ctx, f.keeper.ID)
	if err != nil {
		t.Fatalf("keeper unreadable: %v", err)
	}
	if kept.Version != f.keeper.Version || kept.Procedure != f.keeper.Procedure {
		t.Errorf("failed merge kept the keeper rewrite: v%d %q", kept.Version, kept.Procedure)
	}
	if _, err := f.store.Get(ctx, f.loser.ID); err != nil {
		t.Errorf("failed merge removed the superseded row: %v", err)
	}
	if base, fts := f.skillRowCounts(t); base != 2 || fts != 2 {
		t.Errorf("failed merge changed the store: skills=%d skills_fts=%d", base, fts)
	}

	if _, err := f.db.SQL().Exec("DROP TRIGGER test_block_merge_delete"); err != nil {
		t.Fatalf("drop delete blocker: %v", err)
	}
	merged, err := f.merger.Merge(ctx, in)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if want := f.keeper.Version + 1; merged.Version != want {
		t.Errorf("retry version = %d, want %d (the failed attempt must not have bumped anything)", merged.Version, want)
	}
	if base, fts := f.skillRowCounts(t); base != 1 || fts != 1 {
		t.Errorf("skills=%d skills_fts=%d after the retry, want one row in each", base, fts)
	}
}

// TestSkillStoreMergeRejectsIncompleteInput keeps the store's contract explicit:
// a payload missing its keeper, its body, or its superseded rows is a
// programming error, not a silent no-op.
func TestSkillStoreMergeRejectsIncompleteInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*skills.MergeWriteInput)
	}{
		{"keeper required", func(in *skills.MergeWriteInput) { in.KeeperID = "" }},
		{"procedure required", func(in *skills.MergeWriteInput) { in.Procedure = "" }},
		{"superseded rows required", func(in *skills.MergeWriteInput) { in.Losers = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMergeFixture(t)
			in := f.fold("merged body", nil, nil)
			c.mutate(&in)

			_, err := f.merger.Merge(context.Background(), in)
			if err == nil || !strings.Contains(err.Error(), "storage: merge skills") {
				t.Fatalf("merge error = %v, want a storage: merge skills validation error", err)
			}
			if base, _ := f.skillRowCounts(t); base != 2 {
				t.Errorf("rejected merge changed the table: %d skills", base)
			}
		})
	}
}
