package continuity

import (
	"context"
	"os/exec"
	"testing"
)

func TestGitQueryPreservesEarlierCallerCancellation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if data, err := exec.Command("git", "-C", root, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("initialize real Git fixture: %v %s", err, data)
	}
	if _, ok := gitOutput(t.Context(), root, "rev-parse", "--show-toplevel"); !ok {
		t.Fatal("control Git query failed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, ok := gitOutput(ctx, root, "rev-parse", "--show-toplevel"); ok {
		t.Fatal("Git query ignored caller cancellation")
	}
}
