package continuity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// LocalResolver uses canonical filesystem roots and read-only Git metadata.
type LocalResolver struct{}

// Resolve verifies the current worktree, or an explicit non-Git workspace root.
func (LocalResolver) Resolve(ctx context.Context, cwd, explicitRoot string) (Workspace, error) {
	if cwd == "" {
		cwd = explicitRoot
	}
	if cwd == "" {
		return Workspace{}, invalid("cwd or explicit workspace root required")
	}
	dir, err := canonicalDirectory(cwd)
	if err != nil {
		return Workspace{}, invalid("workspace directory unavailable")
	}
	root := ""
	if out, ok := gitOutput(ctx, dir, "rev-parse", "--show-toplevel"); ok {
		root, err = canonicalDirectory(out)
		if err != nil || !withinRoot(root, dir) {
			return Workspace{}, invalid("invalid Git workspace root")
		}
	}
	if explicitRoot != "" {
		explicit, err := canonicalDirectory(explicitRoot)
		if err != nil || !withinRoot(explicit, dir) {
			return Workspace{}, invalid("invalid explicit workspace root")
		}
		if root != "" && root != explicit {
			return Workspace{}, invalid("explicit root differs from worktree root")
		}
		if root == "" {
			// An inaccessible/broken repository must not be silently classified as non-Git.
			for p := dir; withinRoot(explicit, p); p = filepath.Dir(p) {
				if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
					return Workspace{}, invalid("Git workspace metadata unavailable")
				}
				if p == explicit {
					break
				}
			}
		}
		root = explicit
	}
	if root == "" {
		return Workspace{}, invalid("non-Git workspace requires explicit root")
	}
	hash := sha256.Sum256([]byte("mnemos-workspace-v1\x00" + root))
	w := Workspace{Key: "workspace-v1:" + hex.EncodeToString(hash[:]), Root: root}
	return w, nil
}

// Sample reads current branch and HEAD; unavailable metadata stays explicitly unknown.
func (LocalResolver) Sample(ctx context.Context, root string) WorkspaceState {
	if head, ok := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD"); ok {
		branch, named := gitOutput(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
		if !named {
			branch = "detached"
		}
		if len(branch) <= 256 && len(head) <= 256 {
			return WorkspaceState{Branch: branch, HEAD: head, Known: true}
		}
	}
	return WorkspaceState{}
}

// CanonicalDirectory verifies the physical directory using the same identity
// normalization as continuity binding. Context cancellation bounds new probes;
// an individual operating-system filesystem call may not be interruptible.
func CanonicalDirectory(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := canonicalDirectory(path)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return root, err
}

func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	original, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	abs, err = canonicalFilesystemPath(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", os.ErrInvalid
	}
	if !os.SameFile(original, info) {
		return "", os.ErrInvalid
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		return abs, nil
	}
	{
		// Resolve actual spelling on every filesystem (including Darwin's optional
		// case-insensitive volumes), retaining distinct entries on sensitive ones.
		originalVolume := filepath.VolumeName(abs)
		vol := originalVolume
		if runtime.GOOS == "windows" {
			vol = strings.ToUpper(vol)
		}
		base := vol + string(filepath.Separator)
		for _, part := range strings.Split(strings.TrimPrefix(abs, originalVolume+string(filepath.Separator)), string(filepath.Separator)) {
			if part == "" {
				continue
			}
			entries, err := os.ReadDir(base)
			if err != nil {
				return "", err
			}
			chosen := part
			exact := false
			for _, entry := range entries {
				if entry.Name() == part {
					chosen = entry.Name()
					exact = true
					break
				}
			}
			if !exact {
				want, err := os.Stat(filepath.Join(base, part))
				if err != nil {
					return "", err
				}
				for _, entry := range entries {
					// Windows 8.3 names and filesystem Unicode aliases need not
					// resemble the directory entry. File identity is authoritative;
					// an exact entry above keeps case-sensitive siblings distinct.
					actual, err := os.Stat(filepath.Join(base, entry.Name()))
					if err == nil && os.SameFile(want, actual) {
						chosen = entry.Name()
						break
					}
				}
			}
			base = filepath.Join(base, chosen)
		}
		abs = base
	}
	return abs, nil
}

func withinRoot(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, bool) {
	// Process startup and filesystem scanning can exceed two seconds on a busy
	// workstation (especially Windows antivirus/parallel builds). Keep a bounded
	// local query budget without overriding the host's shorter total deadline.
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(child, "git", argv...)
	// Never inherit GIT_DIR/WORK_TREE/CONFIG_* or discovery overrides from a host.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(strings.SplitN(entry, "=", 2)[0]), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output := &limitedGitOutput{}
	cmd.Stdout = output
	err := cmd.Run()
	if err != nil || output.overflow {
		return "", false
	}
	return strings.TrimSpace(string(output.data)), true
}

type limitedGitOutput struct {
	data     []byte
	overflow bool
}

func (w *limitedGitOutput) Write(data []byte) (int, error) {
	remaining := 8192 - len(w.data)
	if len(data) > remaining {
		w.data = append(w.data, data[:remaining]...)
		w.overflow = true
	} else {
		w.data = append(w.data, data...)
	}
	return len(data), nil
}
