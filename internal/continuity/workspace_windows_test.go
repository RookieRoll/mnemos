package continuity_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

func TestWindowsShortPathResolvesAgainstGitLongRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := filepath.Join(t.TempDir(), "Workspace with a long Unicode name 中文")
	sub := filepath.Join(repo, "nested directory")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init")
	wide, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := syscall.GetShortPathName(wide, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("invalid short-path response %d", n)
	}
	short := syscall.UTF16ToString(buffer[:n])
	if strings.EqualFold(short, sub) {
		t.Skip("8.3 aliases disabled on test volume")
	}
	// Git deliberately prints the long worktree root while cwd retains aliases.
	gitRoot := git(t, short, "rev-parse", "--show-toplevel")
	r := continuity.LocalResolver{}
	want, err := r.Resolve(context.Background(), gitRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := r.Resolve(context.Background(), short, "")
	if err != nil || actual.Key != want.Key || actual.Root != want.Root {
		t.Fatalf("short cwd %q versus Git root %q: got %+v want %+v err=%v", short, gitRoot, actual, want, err)
	}
	asserted, err := r.Resolve(context.Background(), short, repo)
	if err != nil || asserted.Key != want.Key {
		t.Fatalf("explicit long root disagreed with short cwd: %+v %v", asserted, err)
	}
}
