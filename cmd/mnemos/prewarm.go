package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/prewarm"
)

// hookInput mirrors the JSON payload Claude Code writes to hook stdin.
// Fields are a superset across SessionStart, UserPromptSubmit, Stop, and
// PostToolUse — missing fields are tolerated, since each hook populates
// only what its event carries, and the command also runs standalone from
// a regular shell where stdin carries nothing.
type hookInput struct {
	SessionID     string         `json:"session_id,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	HookEventName string         `json:"hook_event_name,omitempty"`
	Source        string         `json:"source,omitempty"`
	Prompt        string         `json:"prompt,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	ToolInput     map[string]any `json:"tool_input,omitempty"`
	Reason        string         `json:"reason,omitempty"`
}

// runPrewarm composes a prewarm block and prints it. Designed to be wired as
// a Claude Code SessionStart hook so session_start context gets pushed without
// the agent having to call mnemos_session_start explicitly.
//
// Runtime failure is intentionally quiet: if anything goes wrong (no config,
// DB unreachable, scanner error) we exit 0 with empty stdout. The hook must
// not block or spam the user; degraded-silent is the right failure mode.
// A malformed invocation is different: bad flags and invalid values are usage
// errors (exit 2), because the caller — not the store — is what needs fixing.
// That classification is decided before configuration is read, so it holds even
// when the integration is disabled or the config is unreadable.
func runPrewarm(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("prewarm")
	var (
		project = fs.String("project", "", "project name (default: basename of cwd)")
		goal    = fs.String("goal", "", "optional goal")
		mode    = fs.String("mode", "session_start", "session_start | compaction_recovery")
		format  = fs.String("format", "text", "text | json")
		// --open-session forces the session open. It is no longer the default:
		// only a hook invocation opens a session automatically (design D3), so
		// a hand-run prewarm stays read-only unless this flag says otherwise.
		// It is three-state: unset follows the payload, and an explicit value
		// wins over one in both directions (--open-session=false keeps a
		// payload-carrying invocation read-only).
		forceSession = fs.Bool("open-session", false, "force a mnemos session open even without a hook payload (session_start mode only)")
		// payload mirrors the hook subcommands' flag so a harness that
		// cannot pipe stdin can still supply cwd and source. It must be a JSON
		// object: passing it is what marks the invocation as an explicit hook.
		payload = fs.String("payload", "", "hook payload as a JSON object (overrides stdin)")
	)
	// Parse before reading config so --help is answered without touching any
	// user state, and a bad flag cannot be masked by an unreadable config.
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}

	// Everything the invocation itself can get wrong is decided here, before
	// configuration or storage is touched (CR c4#3). An invalid --mode or
	// --format used to be reported only after readOperationConfig, so a
	// disabled integration — or an unreadable config — answered a malformed
	// request with success. A usage error stays a usage error no matter what
	// the config says, and deciding it here also means it writes nothing.
	pwMode, err := parsePrewarmMode(*mode)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if *format != "text" && *format != "json" {
		return usageErrorf("invalid format: %s (want text | json)", *format)
	}
	// An explicit --payload is validated here too (CR c4#1): it must be a JSON
	// object, so --payload garbage is a usage error instead of a truthy "hook
	// payload present" signal that silently opened a session.
	var explicitPayload *hookInput
	if s := strings.TrimSpace(*payload); s != "" {
		in, err := parseExplicitHookPayload(s)
		if err != nil {
			return usageErrorf("invalid --payload: %v", err)
		}
		explicitPayload = &in
	}
	// --open-session is three-state (CR c4#2): unset defers to the payload,
	// while an explicit value wins in either direction. Visit walks only the
	// flags actually given on the command line, which is exactly the difference
	// between "unset" and "explicitly false" that a bool flag cannot express.
	openSessionSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "open-session" {
			openSessionSet = true
		}
	})

	cfg, err := readOperationConfig(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos prewarm:", err)
		return nil
	}
	if cfg.Integration.Disabled {
		return nil
	}

	// A payload is what marks an invocation as hook-driven: argv on pi, stdin
	// on Claude Code. Without one, prewarm composes and prints and writes
	// nothing, so a human running it cannot leak a never-ended session into the
	// store. stdin is never read from a terminal, so an interactive run cannot
	// block (design D3).
	in, hasPayload, err := resolvePrewarmPayload(ctx, explicitPayload, os.Stdin)
	switch {
	case errors.Is(err, errHookStdinTooLarge):
		// A stream that long is nobody's hook payload; report it as the caller
		// mistake it is rather than silently reading nothing (CR c4#5).
		return usageErrorf("%v", err)
	case err != nil:
		// Any other stdin failure is degraded-silent, like every other runtime
		// failure here: a hook must not fail because a pipe broke.
		fmt.Fprintln(os.Stderr, "mnemos prewarm:", err)
		return nil
	}
	openSession := hasPayload
	if openSessionSet {
		openSession = *forceSession
	}
	cwd := in.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	proj := *project
	if proj == "" && cwd != "" {
		proj = filepath.Base(cwd)
	}

	d, err := loadDeps(ctx)
	if err != nil {
		// Degraded silent: hook must not fail loudly.
		fmt.Fprintln(os.Stderr, "mnemos prewarm:", err)
		return nil
	}
	defer d.close()

	pwConfig := prewarm.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		Touches:      d.db.Touches(),
		Rumination:   d.rum,
		MaxTokens:    d.cfg.Search.PrewarmMaxTokens,
	}
	if openSession {
		pwConfig.Injections = injection.NewLogger(d.db.Injections(), nil)
	}
	pw := prewarm.NewService(pwConfig)

	flowConfig := continuityflow.Config{
		LegacySessions: d.sess, Composer: pw, PrewarmMaxTokens: d.cfg.Search.PrewarmMaxTokens,
	}
	if openSession && pwMode == prewarm.ModeSessionStart {
		if maintenance, buildErr := newMaintenanceRuntime(d.cfg, d.db, d.mem, d.skl, d.rum, d.db.Sessions(), d.db.Injections(), slog.Default()); buildErr == nil {
			flowConfig.Maintenance = maintenance
		}
	}
	flow := continuityflow.New(flowConfig)
	var sessID string
	var hostID string
	var block *prewarm.Block
	startedSession := false
	if openSession && pwMode == prewarm.ModeSessionStart {
		host := in.SessionID
		if host == "" {
			// An explicit unhosted invocation owns a fresh identity; a project
			// label cannot tell which terminal's session is safe to adopt.
			host = "prewarm:" + ulid.Make().String()
		}
		started, startErr := flow.Start(ctx, continuityflow.TaskSessionInput{
			Project: proj, Goal: *goal, HostSessionID: host, AdoptLegacy: true,
		})
		if startErr == nil && started.Receipt.LegacySession != nil {
			startedSession = true
			sessID = started.Receipt.LegacySession.ID
			hostID = started.Receipt.LegacySession.HostSessionID
			block = started.LegacyBlock
		}
	} else if pwMode == prewarm.ModeCompactionRecovery {
		// Recovery restores the state of the session that was compacted:
		// the one SessionStart opened for this host conversation.
		if sess, err := flow.ResolveLegacy(ctx, in.SessionID, proj); err == nil && sess != nil {
			sessID = sess.ID
			hostID = sess.HostSessionID
		}
	}

	if !startedSession {
		block, err = flow.Compose(ctx, prewarm.Request{
			Mode: pwMode, Project: proj, Goal: *goal, SessionID: sessID,
		})
	}
	if err != nil || block == nil {
		return nil
	}

	switch *format {
	case "json":
		out := map[string]any{
			"session_id":     sessID,
			"project":        proj,
			"mode":           *mode,
			"text":           block.Text,
			"token_estimate": block.TokenEstimate,
			"section_count":  len(block.Sections),
			"safety_risk":    block.SafetyReport.MaxRisk.String(),
		}
		if hostID != "" {
			out["host_session_id"] = hostID
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "text":
		if block.Text != "" {
			fmt.Println(block.Text)
		}
		if sessID != "" {
			fmt.Printf("\nmnemos_session_id: %s\n", sessID)
			if hostID != "" {
				fmt.Printf("mnemos_host_session_id: %s\n", hostID)
			}
		}
	}
	return nil
}

func parsePrewarmMode(s string) (prewarm.Mode, error) {
	switch s {
	case "session_start", "":
		return prewarm.ModeSessionStart, nil
	case "compaction_recovery":
		return prewarm.ModeCompactionRecovery, nil
	default:
		return 0, fmt.Errorf("invalid mode: %s (want session_start | compaction_recovery)", s)
	}
}

// parseExplicitHookPayload validates and decodes an explicit --payload value.
//
// The value must be a JSON object. A bare token — the `--payload garbage` of CR
// c4#1 — used to count as "a payload is present", which silently opened a
// session. Requiring an object makes a malformed invocation a usage error the
// caller has to fix instead of a hidden state change. Passing --payload is
// itself the "this is a deliberate hook call" signal, so unlike the stdin path
// the hook_event_name/session_id markers are not required: {} is enough.
func parseExplicitHookPayload(raw string) (hookInput, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return hookInput{}, fmt.Errorf("payload must be a JSON object: %w", err)
	}
	if fields == nil {
		// JSON null unmarshals into a nil map without an error.
		return hookInput{}, errors.New("payload must be a JSON object, got null")
	}
	var in hookInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return hookInput{}, fmt.Errorf("decode hook payload: %w", err)
	}
	return in, nil
}

// resolvePrewarmPayload reports the hook payload an invocation carries and
// whether it carried one at all.
//
// An explicit payload always counts: a harness without stdin still supplies cwd
// and source that way. Otherwise stdin is read only when it is not a character
// device, and only a JSON object carrying hook_event_name or session_id counts
// as a payload. A terminal is never read, so an interactive `mnemos prewarm`
// cannot block (design D3).
func resolvePrewarmPayload(ctx context.Context, explicit *hookInput, stdin io.Reader) (hookInput, bool, error) {
	if explicit != nil {
		return *explicit, true, nil
	}
	data, err := readNonTerminalStdin(ctx, stdin)
	if err != nil {
		return hookInput{}, false, err
	}
	if len(data) == 0 {
		return hookInput{}, false, nil
	}
	var probe struct {
		HookEventName string `json:"hook_event_name"`
		SessionID     string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return hookInput{}, false, nil
	}
	if probe.HookEventName == "" && probe.SessionID == "" {
		// A pipe carrying something that is not a hook payload (a script
		// redirecting a file, say) is still a read-only invocation.
		return hookInput{}, false, nil
	}
	var in hookInput
	_ = json.Unmarshal(data, &in)
	return in, true, nil
}

// hookStdinLimit caps how many bytes of a non-terminal stdin the CLI reads for a
// hook payload. A real harness payload is a few KiB; the cap turns a runaway
// producer — a redirected multi-gigabyte file, say — into a bounded failure
// instead of an unbounded allocation (CR c4#5).
const hookStdinLimit = 256 << 10 // 256 KiB

// hookStdinTimeout bounds how long an open stdin may stay silent. A hook
// harness writes its payload and closes; a writer that does not must not hang
// the CLI. A variable so tests can shorten it.
var hookStdinTimeout = 2 * time.Second

// errHookStdinTooLarge reports a stdin stream longer than hookStdinLimit.
var errHookStdinTooLarge = errors.New("hook payload on stdin exceeds 256 KiB")

// readNonTerminalStdin reads a hook payload from r unless r is a character
// device (an interactive terminal) or absent.
//
// The read is bounded twice over (CR c4#5): at most hookStdinLimit bytes are
// accepted, so a redirected file cannot exhaust the heap, and a writer that
// never closes its end is abandoned after hookStdinTimeout, so a dangling pipe
// cannot hang the process. A timed-out stream is discarded because a valid JSON
// prefix does not establish that the entire payload has arrived.
//
// A nil result with a nil error means "nothing to read"; the bytes read
// otherwise; errHookStdinTooLarge when the stream is longer than the cap.
func readNonTerminalStdin(ctx context.Context, r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("inspect stdin: %w", err)
		}
		if (fi.Mode() & os.ModeCharDevice) != 0 {
			// Interactive TTY — nothing to read.
			return nil, nil
		}
	}
	collected := &boundedStdinBuffer{}
	done := make(chan error, 1)
	go func() {
		// The copy stops on its own once the cap is crossed: Write returns
		// errHookStdinTooLarge and io.Copy reports it here.
		_, err := io.Copy(collected, r)
		done <- err
	}()
	timer := time.NewTimer(hookStdinTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, errHookStdinTooLarge) {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	case <-ctx.Done():
		return nil, fmt.Errorf("read stdin: %w", ctx.Err())
	case <-timer.C:
		// Without EOF the payload may be incomplete, even if a prefix is JSON.
		return nil, errors.New("read stdin: timed out waiting for EOF")
	}
	data := collected.bytes()
	if len(data) > hookStdinLimit {
		return nil, errHookStdinTooLarge
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// boundedStdinBuffer collects at most hookStdinLimit+1 bytes and reports
// errHookStdinTooLarge once that bound is crossed. The extra byte is what
// distinguishes a stream of exactly hookStdinLimit bytes from a longer one
// without a further read, and returning the error from Write makes io.Copy stop
// instead of draining the rest of the producer.
type boundedStdinBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer for io.Copy.
func (b *boundedStdinBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := hookStdinLimit + 1 - b.buf.Len()
	if room <= 0 {
		return 0, errHookStdinTooLarge
	}
	if len(p) > room {
		_, _ = b.buf.Write(p[:room])
		return room, errHookStdinTooLarge
	}
	return b.buf.Write(p)
}

// bytes returns a copy of what has been collected so far. The copy is taken
// under the lock because the reader goroutine may still be appending after
// readNonTerminalStdin gave up on a writer that never closed.
func (b *boundedStdinBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// readHookStdin best-effort parses a Claude Code hook payload from stdin.
// Hook events can contain large tool inputs, so they retain the unbounded input
// behavior of existing harnesses; the prewarm-specific cap is not used here.
func readHookStdin(r io.Reader) hookInput {
	var in hookInput
	if r == nil {
		return in
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return in
		}
	}
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return in
	}
	// Ignore unmarshal errors: a non-JSON stdin is a legitimate shell use, and
	// whatever did parse is kept. The error is deliberately not inspected —
	// every failure mode lands on the same "use what we got" answer.
	_ = json.Unmarshal(data, &in)
	return in
}
