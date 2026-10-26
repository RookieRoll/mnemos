package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
)

func TestParsePrewarmMode(t *testing.T) {
	cases := []struct {
		in      string
		want    prewarm.Mode
		wantErr bool
	}{
		{"session_start", prewarm.ModeSessionStart, false},
		{"", prewarm.ModeSessionStart, false},
		{"compaction_recovery", prewarm.ModeCompactionRecovery, false},
		{"garbage", 0, true},
	}
	for _, c := range cases {
		got, err := parsePrewarmMode(c.in)
		if c.wantErr && err == nil {
			t.Errorf("%q: expected error, got none", c.in)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestReadHookStdinParsesClaudePayload(t *testing.T) {
	// A realistic Claude Code SessionStart payload.
	raw := `{
		"session_id": "abc123",
		"cwd": "/Users/demo/Code/mnemos",
		"hook_event_name": "SessionStart",
		"source": "startup"
	}`
	in := readHookStdin(strings.NewReader(raw))
	if in.SessionID != "abc123" {
		t.Errorf("session_id: got %q", in.SessionID)
	}
	if in.CWD != "/Users/demo/Code/mnemos" {
		t.Errorf("cwd: got %q", in.CWD)
	}
	if in.Source != "startup" {
		t.Errorf("source: got %q", in.Source)
	}
}

func TestReadHookStdinHandlesEmpty(t *testing.T) {
	in := readHookStdin(strings.NewReader(""))
	if in.CWD != "" || in.SessionID != "" {
		t.Errorf("expected zero value, got %+v", in)
	}
}

func TestReadHookStdinHandlesNonJSON(t *testing.T) {
	// A non-JSON stdin means the command was invoked interactively or
	// piped garbage. We must not panic or error — just return zero.
	in := readHookStdin(strings.NewReader("not json at all"))
	if in.CWD != "" {
		t.Errorf("non-json stdin should produce zero value, got %+v", in)
	}
}

func TestRunPrewarmTextOutput(t *testing.T) {
	home := homeWithConfig(t, "")
	_ = home

	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(), []string{"--project", "mnemos", "--open-session"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	// A fresh DB has no observations, so the block text may be empty; but
	// the session_id must be printed when the session is forced open.
	if !strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("expected session_id line in output, got: %q", out)
	}
}

func TestRunPrewarmJSONOutput(t *testing.T) {
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--format", "json", "--open-session"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("json output did not parse: %v\n%s", err, out)
	}
	if parsed["project"] != "mnemos" {
		t.Errorf("project missing or wrong: %v", parsed["project"])
	}
	if _, ok := parsed["session_id"].(string); !ok {
		t.Errorf("session_id missing: %v", parsed)
	}
	if _, ok := parsed["safety_risk"].(string); !ok {
		t.Errorf("safety_risk missing: %v", parsed)
	}
}

func TestRunPrewarmRejectsInvalidMode(t *testing.T) {
	homeWithConfig(t, "")
	err := runPrewarm(context.Background(), []string{"--mode", "wat"})
	if err == nil {
		t.Error("expected error on invalid mode")
	}
}

func TestRunPrewarmRejectsInvalidFormat(t *testing.T) {
	homeWithConfig(t, "")
	err := runPrewarm(context.Background(), []string{"--format", "xml"})
	if err == nil {
		t.Error("expected error on invalid format")
	}
}

func TestRunPrewarmNoSessionFlag(t *testing.T) {
	// With --open-session=false, the output should still render (empty
	// prewarm for a fresh DB is fine) but must not contain a session_id
	// line since we never opened one.
	homeWithConfig(t, "")
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--open-session=false"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	if strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("session_id should be absent when --open-session=false: %q", out)
	}
}

func TestRunPrewarmCompactionRecoveryMode(t *testing.T) {
	homeWithConfig(t, "")
	// Compaction recovery without any prior session is a no-op but must
	// not error. We also must NOT open a new session in this mode.
	out := captureStdout(t, func() {
		if err := runPrewarm(context.Background(),
			[]string{"--project", "mnemos", "--mode", "compaction_recovery"}); err != nil {
			t.Fatalf("prewarm: %v", err)
		}
	})
	if strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("compaction_recovery should not open a new session when none exists: %q", out)
	}
}

func TestRunPrewarmAdoptsRecentOpenSession(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	// SessionStart hooks installed at user AND project scope both fire
	// within milliseconds. The second firing must adopt the session the
	// first one opened, not create a twin that never gets closed.
	first := captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "mnemos", "--open-session"}); err != nil {
			t.Fatalf("first prewarm: %v", err)
		}
	})
	second := captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "mnemos", "--open-session"}); err != nil {
			t.Fatalf("second prewarm: %v", err)
		}
	})

	firstID := sessionIDFromOutput(t, first)
	secondID := sessionIDFromOutput(t, second)
	if firstID != secondID {
		t.Errorf("second firing must adopt the open session: %s != %s", firstID, secondID)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	open, err := d.sess.ListOpen(ctx, "mnemos")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 {
		t.Errorf("want exactly 1 open session after double firing, got %d", len(open))
	}
}

func TestRunPrewarmDoesNotAdoptAcrossProjects(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "alpha", "--open-session"}); err != nil {
			t.Fatalf("prewarm alpha: %v", err)
		}
	})
	captureStdout(t, func() {
		if err := runPrewarm(ctx, []string{"--project", "beta", "--open-session"}); err != nil {
			t.Fatalf("prewarm beta: %v", err)
		}
	})

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	for _, proj := range []string{"alpha", "beta"} {
		open, err := d.sess.ListOpen(ctx, proj)
		if err != nil {
			t.Fatalf("list open %s: %v", proj, err)
		}
		if len(open) != 1 {
			t.Errorf("project %s: want its own session, got %d", proj, len(open))
		}
	}
}

// sessionIDFromOutput extracts the mnemos_session_id line from prewarm
// text output.
func sessionIDFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "mnemos_session_id: ") {
			return strings.TrimPrefix(line, "mnemos_session_id: ")
		}
	}
	t.Fatalf("no mnemos_session_id in output: %q", out)
	return ""
}

func TestRunPrewarmRecordsHostSessionAndAdoptsOnlyItsOwn(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	fire := func(host string) string {
		t.Helper()
		payload := hookPayload(t, map[string]any{
			"hook_event_name": "SessionStart",
			"source":          "startup",
			"session_id":      host,
		})
		var out string
		withStdin(t, payload, func() {
			out = captureStdout(t, func() {
				if err := runPrewarm(ctx, []string{"--project", "mnemos"}); err != nil {
					t.Fatalf("prewarm: %v", err)
				}
			})
		})
		return sessionIDFromOutput(t, out)
	}

	first := fire("claude-a")
	twin := fire("claude-a")
	// A second terminal in the same project inside the adoption window.
	second := fire("claude-b")

	if twin != first {
		t.Errorf("a duplicate SessionStart for the same conversation must adopt %s, got %s", first, twin)
	}
	if second == first {
		t.Error("another conversation in the same project must get its own session")
	}

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	got, err := d.sess.Get(ctx, first)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HostSessionID != "claude-a" {
		t.Errorf("SessionStart must record the host session id, got %q", got.HostSessionID)
	}
}

func TestRunPrewarmRecoveryRestoresTheHostsSession(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	mine, _ := d.sess.Open(ctx, session.OpenInput{
		Project: "mnemos", Goal: "ship compaction recovery", HostSessionID: "claude-a",
	})
	if _, err := d.mem.Save(ctx, memory.SaveInput{
		Title: "recovery runs from SessionStart compact", Content: "stderr from PreCompact never reached the model",
		Project: "mnemos", Type: memory.TypeDecision, SessionID: mine.ID,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Another terminal in the same project, newer, so the old lookup took it.
	_, _ = d.sess.Open(ctx, session.OpenInput{
		Project: "mnemos", Goal: "unrelated review", HostSessionID: "claude-b",
	})
	d.close()

	payload := hookPayload(t, map[string]any{
		"hook_event_name": "SessionStart",
		"source":          "compact",
		"session_id":      "claude-a",
	})
	var out string
	withStdin(t, payload, func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(ctx, []string{"--project", "mnemos", "--mode", "compaction_recovery"}); err != nil {
				t.Fatalf("prewarm: %v", err)
			}
		})
	})
	if !strings.Contains(out, "ship compaction recovery") {
		t.Errorf("recovery must restore the compacted conversation's goal, got: %s", out)
	}
	if !strings.Contains(out, "recovery runs from SessionStart compact") {
		t.Errorf("recovery must list the session's observations, got: %s", out)
	}
	if strings.Contains(out, "unrelated review") {
		t.Errorf("recovery must not restore another terminal's session, got: %s", out)
	}
	if got := sessionIDFromOutput(t, out); got != mine.ID {
		t.Errorf("recovery must report session %s, got %s", mine.ID, got)
	}
}

// sessionTotal is the store's session count, the observable form of "this
// invocation opened a session".
func sessionTotal(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	st, err := d.mem.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	return st.Sessions
}

func TestPrewarm_NoPayloadIsReadOnly(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	before := sessionTotal(t, ctx)

	// The hand-run shape: stdin is a pipe, not a terminal, but it carries no
	// hook payload. Prewarm must compose and print, and write nothing.
	var out string
	withStdin(t, "", func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(ctx, []string{"--project", "mnemos"}); err != nil {
				t.Fatalf("prewarm: %v", err)
			}
		})
	})
	if strings.Contains(out, "mnemos_session_id:") {
		t.Errorf("a payload-less prewarm must not report a session: %q", out)
	}
	if after := sessionTotal(t, ctx); after != before {
		t.Fatalf("payload-less prewarm changed the session count: %d -> %d", before, after)
	}
}

func TestPrewarm_PayloadFromStdinOpensSession(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	payload := hookPayload(t, map[string]any{"hook_event_name": "SessionStart", "session_id": "claude-stdin"})
	var out string
	withStdin(t, payload, func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(ctx, []string{"--project", "mnemos"}); err != nil {
				t.Fatalf("prewarm: %v", err)
			}
		})
	})
	id := sessionIDFromOutput(t, out)
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	got, err := d.sess.Get(ctx, id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.HostSessionID != "claude-stdin" {
		t.Errorf("stdin payload must open a session for the host conversation, got %q", got.HostSessionID)
	}
}

func TestPrewarm_PayloadArgOpensSession(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	payload := hookPayload(t, map[string]any{"hook_event_name": "SessionStart", "session_id": "pi-argv"})
	var out string
	withStdin(t, "", func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(ctx, []string{"--project", "mnemos", "--payload", payload}); err != nil {
				t.Fatalf("prewarm: %v", err)
			}
		})
	})
	id := sessionIDFromOutput(t, out)
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	got, err := d.sess.Get(ctx, id)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.HostSessionID != "pi-argv" {
		t.Errorf("an argv payload must open a session for the host conversation, got %q", got.HostSessionID)
	}
}

func TestPrewarm_OpenSessionFlagForces(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	before := sessionTotal(t, ctx)
	var out string
	withStdin(t, "", func() {
		out = captureStdout(t, func() {
			if err := runPrewarm(ctx, []string{"--project", "mnemos", "--open-session"}); err != nil {
				t.Fatalf("prewarm: %v", err)
			}
		})
	})
	_ = sessionIDFromOutput(t, out)
	if after := sessionTotal(t, ctx); after != before+1 {
		t.Fatalf("--open-session must open exactly one session: %d -> %d", before, after)
	}
}

func TestHookPayloadFrom(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		stdin    string
		nilRead  bool
		want     bool
	}{
		{"explicit payload", `{"hook_event_name":"SessionStart"}`, "", false, true},
		{"explicit payload without hook fields", `{"cwd":"/tmp"}`, "", false, true},
		{"stdin hook payload", "", `{"session_id":"s1"}`, false, true},
		{"stdin unrelated json", "", `{"cwd":"/tmp"}`, false, false},
		{"stdin not json", "", "hello", false, false},
		{"empty stdin", "", "", false, false},
		{"nil reader", "", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdin io.Reader = strings.NewReader(tc.stdin)
			if tc.nilRead {
				stdin = nil
			}
			in, detected := hookPayloadFrom(tc.explicit, stdin)
			if detected != tc.want {
				t.Fatalf("detected = %v, want %v", detected, tc.want)
			}
			if !detected && (in.HookEventName != "" || in.SessionID != "" || in.CWD != "") {
				t.Fatalf("an undetected payload must be zero, got %+v", in)
			}
		})
	}
}
