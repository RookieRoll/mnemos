package workspacediagnostics

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

// LocalProbe inspects existing roots using filesystem identity and local Git.
type LocalProbe struct{}

// Inspect never reads source contents, branch names, HEAD or remote metadata.
func (LocalProbe) Inspect(ctx context.Context, root string) PathState {
	out := PathState{Status: "unknown", Git: "unknown"}
	if ctx.Err() != nil {
		out.Reason = "probe_budget_exhausted"
		return out
	}
	if !filepath.IsAbs(root) {
		out.Reason = "recorded_root_is_not_absolute"
		return out
	}
	canonical, err := continuity.CanonicalDirectory(ctx, root)
	if err != nil {
		if os.IsNotExist(err) {
			out.Status, out.Reason = "missing", "recorded_directory_missing"
		} else {
			out.Reason = "directory_metadata_unavailable"
		}
		return out
	}
	out.Status, out.Canonical = "live", canonical
	out.Info, err = os.Stat(canonical)
	if err != nil {
		out.Status, out.Reason = "unknown", "directory_identity_unavailable"
		return out
	}
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(child, "git", "--no-optional-locks", "-c", "core.fsmonitor=false", "-C", canonical, "rev-parse", "--show-toplevel")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(strings.SplitN(entry, "=", 2)[0]), "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	output := &gitBuffer{}
	cmd.Stdout = output
	err = cmd.Run()
	if err == nil && !output.overflow && child.Err() == nil {
		gitRoot, canonicalErr := continuity.CanonicalDirectory(child, strings.TrimSpace(string(output.data)))
		if canonicalErr == nil && (gitRoot == canonical || strictlyContains(gitRoot, canonical)) {
			out.Git, out.GitRoot = "git", gitRoot
			return out
		}
		out.Reason = "git_root_metadata_unavailable"
		return out
	}
	if child.Err() != nil {
		out.Reason = "git_probe_budget_exhausted"
		return out
	}
	if _, ok := err.(*exec.ExitError); !ok || output.overflow {
		out.Reason = "git_probe_unavailable"
		return out
	}
	// Failure is not proof of non-Git: broken .git files, permissions and safe
	// directory errors must remain unknown instead of becoming merge evidence.
	for p := canonical; ; p = filepath.Dir(p) {
		_, statErr := os.Lstat(filepath.Join(p, ".git"))
		if statErr == nil || !os.IsNotExist(statErr) {
			out.Reason = "git_metadata_unavailable"
			return out
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	out.Git = "non_git"
	return out
}

type gitBuffer struct {
	data     []byte
	overflow bool
}

func (b *gitBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := 8192 - len(b.data)
	if len(data) > remaining {
		data = data[:remaining]
		b.overflow = true
	}
	b.data = append(b.data, data...)
	return n, nil
}
