package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// TestSkillStoreDelete covers the store-level delete contract: one row goes, the
// skills_fts mirror follows through the table triggers, a repeated or empty
// delete reports a domain error instead of silently succeeding, and every error
// the store returns is prefixed with the storage subsystem name.
func TestSkillStoreDelete(t *testing.T) {
	cases := []struct {
		name     string
		id       func(sk *skills.Skill) string
		want     error
		wantText string
	}{
		{
			name: "existing row is removed",
			id:   func(sk *skills.Skill) string { return sk.ID },
		},
		{
			name:     "unknown id reports not found",
			id:       func(*skills.Skill) string { return "01nosuchskill" },
			want:     skills.ErrNotFound,
			wantText: "storage: delete skill",
		},
		{
			name:     "empty id is rejected",
			id:       func(*skills.Skill) string { return "" },
			wantText: "storage: delete skill: id required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := Open(ctx, filepath.Join(t.TempDir(), "skills.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			store := db.Skills()

			sk, err := store.Upsert(ctx, skills.SaveInput{
				Name: "deploy-checklist", Description: "deploy runbook", Procedure: "kubectl apply",
			})
			if err != nil {
				t.Fatal(err)
			}

			err = store.Delete(ctx, c.id(sk))
			if c.wantText != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantText) {
					t.Fatalf("delete error = %v, want one containing %q", err, c.wantText)
				}
			}
			if c.want != nil {
				if !errors.Is(err, c.want) {
					t.Fatalf("delete error = %v, want %v", err, c.want)
				}
				return
			}
			if c.id(sk) == "" {
				if err == nil {
					t.Fatal("deleting an empty id should fail")
				}
				return
			}
			if err != nil {
				t.Fatalf("delete: %v", err)
			}

			if _, err := store.Get(ctx, sk.ID); !errors.Is(err, skills.ErrNotFound) {
				t.Errorf("skill still readable after delete: %v", err)
			}
			var fts, base int
			if err := db.sql.QueryRowContext(ctx, `SELECT count(*) FROM skills_fts`).Scan(&fts); err != nil {
				t.Fatal(err)
			}
			if err := db.sql.QueryRowContext(ctx, `SELECT count(*) FROM skills`).Scan(&base); err != nil {
				t.Fatal(err)
			}
			if fts != base {
				t.Errorf("skills_fts holds %d rows but skills holds %d: mirror drift", fts, base)
			}
		})
	}
}
