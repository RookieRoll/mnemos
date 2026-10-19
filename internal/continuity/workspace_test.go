package continuity_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = os.Environ()
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}

func TestWorkspaceIdentityIsolationSubdirectoriesAndWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	base := t.TempDir()
	repo := filepath.Join(base, "one", "api")
	other := filepath.Join(base, "two", "api")
	os.MkdirAll(repo, 0755)
	os.MkdirAll(other, 0755)
	git(t, repo, "-c", "init.defaultBranch=main", "init")
	git(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	git(t, other, "init")
	sub := filepath.Join(repo, "src")
	os.MkdirAll(sub, 0755)
	r := continuity.LocalResolver{}
	root, err := r.Resolve(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	nested, err := r.Resolve(ctx, sub, "")
	if err != nil || nested.Key != root.Key {
		t.Fatal("subdirectory split", err)
	}
	second, err := r.Resolve(ctx, other, "")
	if err != nil || second.Key == root.Key {
		t.Fatal("same basename merged", err)
	}
	linked := filepath.Join(base, "linked")
	git(t, repo, "worktree", "add", "-b", "linked", linked)
	worktree, err := r.Resolve(ctx, linked, "")
	if err != nil || worktree.Key == root.Key {
		t.Fatal("linked worktree merged", err)
	}
	if state := r.Sample(ctx, root.Root); !state.Known || state.Branch != "main" || state.HEAD == "" {
		t.Fatalf("metadata %+v", state)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", other)
	protected, err := r.Resolve(ctx, sub, "")
	if err != nil || protected.Key != root.Key {
		t.Fatal("Git environment hijacked root", err)
	}
}

func TestExplicitNonGitRootsLinksAndCaseEquivalence(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "CaseRoot")
	os.Mkdir(root, 0755)
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0755)
	r := continuity.LocalResolver{}
	if _, err := r.Resolve(ctx, root, ""); err == nil {
		t.Fatal("non-Git inferred root")
	}
	w, err := r.Resolve(ctx, sub, root)
	if err != nil {
		t.Fatal(err)
	}
	if w.State.Known {
		t.Fatal("non-Git known metadata")
	}
	for _, test := range []struct{ cwd, explicit string }{{filepath.Join(root, "missing"), root}, {root, sub}, {"", ""}} {
		if _, err := r.Resolve(ctx, test.cwd, test.explicit); err == nil {
			t.Fatalf("invalid path accepted %+v", test)
		}
	}
	upper := filepath.Join(filepath.Dir(root), strings.ToUpper(filepath.Base(root)))
	if _, err := os.Stat(upper); err == nil {
		equiv, err := r.Resolve(ctx, upper, upper)
		if err != nil || equiv.Key != w.Key {
			t.Fatal("case-equivalent path split", err)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(filepath.Dir(root), "link")
		if err := os.Symlink(root, link); err != nil {
			t.Skip("symlink unavailable", err)
		}
		linked, err := r.Resolve(ctx, link, link)
		if err != nil || linked.Key != w.Key {
			t.Fatal("symlink split", err)
		}
	})
}

func TestCaseSensitiveSiblingRootsStaySeparate(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "CaseSibling")
	second := filepath.Join(base, "casesibling")
	if err := os.Mkdir(first, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0755); os.IsExist(err) {
		t.Skip("case-insensitive test filesystem")
	} else if err != nil {
		t.Fatal(err)
	}
	a, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(a, b) {
		t.Skip("case-equivalent test filesystem")
	}
	r := continuity.LocalResolver{}
	left, err := r.Resolve(context.Background(), first, first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := r.Resolve(context.Background(), second, second)
	if err != nil || left.Key == right.Key {
		t.Fatalf("distinct case-sensitive roots merged %+v %+v %v", left, right, err)
	}
}
