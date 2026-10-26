package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

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
func runPrewarm(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("prewarm")
	var (
		project     = fs.String("project", "", "project name (default: basename of cwd)")
		goal        = fs.String("goal", "", "optional goal")
		mode        = fs.String("mode", "session_start", "session_start | compaction_recovery")
		format      = fs.String("format", "text", "text | json")
		openSession = fs.Bool("open-session", true, "open a mnemos session (session_start mode only)")
		// payload mirrors the hook subcommands' flag so a harness that
		// cannot pipe stdin can still supply cwd and source.
		payload = fs.String("payload", "", "hook payload as JSON (overrides stdin)")
	)
	// Parse before reading config so --help is answered without touching any
	// user state, and a bad flag cannot be masked by an unreadable config.
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos prewarm:", err)
		return nil
	}
	if cfg.Integration.Disabled {
		return nil
	}

	pwMode, err := parsePrewarmMode(*mode)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if *format != "text" && *format != "json" {
		return usageErrorf("invalid format: %s (want text | json)", *format)
	}

	in := resolveHookPayload(*payload)
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

	pw := prewarm.NewService(prewarm.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		Touches:      d.db.Touches(),
		Rumination:   d.rum,
		Injections:   injection.NewLogger(d.db.Injections(), nil),
		MaxTokens:    d.cfg.Search.PrewarmMaxTokens,
	})

	flowConfig := continuityflow.Config{
		LegacySessions: d.sess, Composer: pw, PrewarmMaxTokens: d.cfg.Search.PrewarmMaxTokens,
	}
	if *openSession && pwMode == prewarm.ModeSessionStart {
		if maintenance, buildErr := newMaintenanceRuntime(d.cfg, d.db, d.mem, d.skl, d.rum, d.db.Sessions(), d.db.Injections(), slog.Default()); buildErr == nil {
			flowConfig.Maintenance = maintenance
		}
	}
	flow := continuityflow.New(flowConfig)
	var sessID string
	var block *prewarm.Block
	startedSession := false
	if *openSession && pwMode == prewarm.ModeSessionStart {
		started, startErr := flow.Start(ctx, continuityflow.TaskSessionInput{
			Project: proj, Goal: *goal, HostSessionID: in.SessionID, AdoptLegacy: true,
		})
		if startErr == nil && started.Receipt.LegacySession != nil {
			startedSession = true
			sessID = started.Receipt.LegacySession.ID
			block = started.LegacyBlock
		}
	} else if pwMode == prewarm.ModeCompactionRecovery {
		// Recovery restores the state of the session that was compacted:
		// the one SessionStart opened for this host conversation.
		if sess, err := flow.ResolveLegacy(ctx, in.SessionID, proj); err == nil && sess != nil {
			sessID = sess.ID
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
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	case "text":
		if block.Text != "" {
			fmt.Println(block.Text)
		}
		if sessID != "" {
			fmt.Printf("\nmnemos_session_id: %s\n", sessID)
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

// readHookStdin best-effort parses a Claude Code hook payload from stdin.
// Returns a zero value if stdin is a TTY, empty, or not valid JSON. Errors
// are swallowed on purpose: the CLI also runs interactively.
func readHookStdin(r io.Reader) hookInput {
	var in hookInput
	f, ok := r.(*os.File)
	if ok {
		fi, err := f.Stat()
		if err != nil {
			return in
		}
		if (fi.Mode() & os.ModeCharDevice) != 0 {
			// Interactive TTY — nothing to read.
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
