package continuity

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Access to a Temp descendant does not imply permission to enumerate the user
// profile. This fixture intentionally uses the real user Temp (not repo .tmp),
// matching the pi.exec host environment that exposed the ancestor ACL failure.
func TestWindowsUserTempWorkspaceWithoutAncestorEnumeration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "Temp")
	home, err := os.MkdirTemp(base, "mnemos-continuity-user-temp-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	root := filepath.Join(home, "api")
	sub := filepath.Join(root, "nested directory")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	init := exec.Command("git", "-C", root, "init", "--quiet")
	if data, err := init.CombinedOutput(); err != nil {
		t.Fatalf("initialize real Git fixture: %v %s", err, data)
	}
	r := LocalResolver{}
	ctx := context.Background()
	long, err := r.Resolve(ctx, sub, root)
	if err != nil {
		t.Fatalf("traversable user Temp workspace rejected: %v", err)
	}
	wide, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := syscall.GetShortPathName(wide, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("get real short path: %d %v", n, err)
	}
	short := syscall.UTF16ToString(buffer[:n])
	actual, err := r.Resolve(ctx, short, "")
	if err != nil || actual.Key != long.Key || actual.Root != long.Root {
		t.Fatalf("user Temp aliases diverged: %v", err)
	}
	// Prove returned canonical spelling denotes the same target directory.
	a, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(actual.Root)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("canonical root changed filesystem identity: %v", err)
	}
}
