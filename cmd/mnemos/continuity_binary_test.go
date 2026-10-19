package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// This test drives actual subprocesses and independent SQLite connections. A
// reserved writer makes the killed mutation deterministically uncommitted.
func TestContinuityRealBinarySharedHandoffAndIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	bin := os.Getenv("MNEMOS_TEST_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "mnemos.exe")
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build real fixture binary: %v %s", err, out)
		}
	}
	fixture := t.TempDir()
	left, right, worktree := filepath.Join(fixture, "left", "api"), filepath.Join(fixture, "right", "api"), filepath.Join(fixture, "linked")
	for _, root := range []string{left, right} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var childEnv []string
	for _, entry := range os.Environ() {
		name := strings.SplitN(entry, "=", 2)[0]
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") && !strings.EqualFold(name, "MNEMOS_DISABLED") {
			childEnv = append(childEnv, entry)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = childEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture %v: %v %s", args, err, out)
		}
	}
	git("init", "--quiet", left)
	git("-C", left, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+fixture, "commit", "--quiet", "--allow-empty", "-m", "fixture")
	git("init", "--quiet", right)
	git("-C", left, "worktree", "add", "--quiet", "--detach", worktree, "HEAD")
	dbPath := filepath.Join(fixture, "memory.db")
	cfg := filepath.Join(fixture, "config.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("[storage]\npath = %q\n[embedding]\nprovider = \"none\"\n", dbPath)), 0o600); err != nil {
		t.Fatal(err)
	}
	command := func(operation string, input any) *exec.Cmd {
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, bin, "continuity", operation, "--config", cfg, "--payload", string(data))
		cmd.Env = childEnv
		return cmd
	}
	call := func(operation string, input, out any) {
		t.Helper()
		cmd := command(operation, input)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s failed: %v %s", operation, err, raw)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("invalid real response: %v %s", err, raw)
		}
	}
	open := func(root, agent, key, source string) continuityflow.OpenResult {
		t.Helper()
		var out continuityflow.OpenResult
		call("open", continuity.OpenInput{ProtocolVersion: 1, CWD: root, AgentID: agent, HarnessKind: "binary-test", StartKey: key, SourceSessionID: source}, &out)
		return out
	}
	a := open(left, "a", "start-a", "")
	zero := int64(0)
	save := continuity.CheckpointInput{ProtocolVersion: 1, CWD: left, AgentID: "a", SessionID: a.SessionID, Action: "save", ExpectedRevision: &zero, RequestID: "first", Visibility: "workspace", Payload: &continuity.Payload{Task: "migration", State: "blocked", Summary: "Schema is implemented; one compatibility check remains.", NextSteps: []string{"Verify current files and run compatibility test."}, Blockers: []string{"The full regression has not run."}}}
	var receipt continuity.CheckpointResult
	call("checkpoint", save, &receipt)
	var replay continuity.CheckpointResult
	call("checkpoint", save, &replay)
	if replay.Status != "replayed" || replay.Revision != receipt.Revision || !replay.UpdatedAt.Equal(receipt.UpdatedAt) {
		t.Fatal("lost ACK retry did not confirm same commit")
	}
	b := open(left, "b", "start-b", a.SessionID)
	c := open(left, "c", "start-c", a.SessionID)
	for _, receiver := range []continuityflow.OpenResult{b, c} {
		if receiver.SessionID == a.SessionID || receiver.ContextStatus != "available" || !strings.Contains(receiver.Prewarm, "one compatibility check remains") || !strings.Contains(receiver.Prewarm, "mnemos_session_id: "+receiver.SessionID) || len(receiver.ContinuityRefs) != 1 || receiver.ContinuityRefs[0].SessionID != a.SessionID {
			t.Fatalf("handoff failed: %#v", receiver)
		}
	}
	for _, receiver := range []continuityflow.OpenResult{b, c} {
		var out continuity.ContextResult
		call("context", continuity.ContextInput{ProtocolVersion: 1, CWD: left, AgentID: receiver.AgentID, SessionID: receiver.SessionID, SourceSessionID: a.SessionID}, &out)
		if out.Source == nil || out.Source.Revision != 1 {
			t.Fatal("source was claimed/consumed")
		}
	}
	for index, root := range []string{right, worktree} {
		receiver := open(root, "b", fmt.Sprintf("isolated-%d", index), "")
		var out continuity.ContextResult
		call("context", continuity.ContextInput{ProtocolVersion: 1, CWD: root, AgentID: "b", SessionID: receiver.SessionID}, &out)
		if out.Status != "none" || len(out.Candidates) != 0 {
			t.Fatalf("workspace data leaked: %#v", out)
		}
		cmd := command("context", continuity.ContextInput{ProtocolVersion: 1, CWD: root, AgentID: "b", SessionID: receiver.SessionID, SourceSessionID: a.SessionID})
		raw, err := cmd.Output()
		if err == nil || !strings.Contains(string(raw), `"code":"unavailable"`) {
			t.Fatalf("explicit source fell back across root: %v %s", err, raw)
		}
	}
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.SQL().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	one := int64(1)
	uncommitted := save
	body := *save.Payload
	body.Summary = "UNCOMMITTED MUST NOT APPEAR"
	uncommitted.Payload = &body
	uncommitted.ExpectedRevision = &one
	uncommitted.RequestID = "killed-before-commit"
	blocked := command("checkpoint", uncommitted)
	if err := blocked.Start(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	// The independent writer is held until the subprocess is confirmed dead.
	time.Sleep(50 * time.Millisecond)
	if err := blocked.Process.Kill(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("mutation was not alive behind the reserved writer: %v", err)
	}
	if err := blocked.Wait(); err == nil {
		_ = tx.Rollback()
		t.Fatal("interrupted mutation exited successfully")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var after continuity.ContextResult
	call("context", continuity.ContextInput{ProtocolVersion: 1, CWD: left, AgentID: "b", SessionID: b.SessionID, SourceSessionID: a.SessionID}, &after)
	if after.Source == nil || after.Source.Revision != 1 || strings.Contains(after.Source.Payload.Summary, "UNCOMMITTED") {
		t.Fatalf("uncommitted progress recovered: %#v", after)
	}
	var rows int
	if err := db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM session_checkpoints").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("receiver writes or checkpoint history appeared: %d %v", rows, err)
	}
}
