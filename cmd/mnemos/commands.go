package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/installer"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// deps bundles the services a CLI subcommand typically needs. Returned by
// loadDeps so callers take exactly what they use.
type deps struct {
	db       *storage.DB
	mem      *memory.Service
	sess     *session.Service
	skl      *skills.Service
	rum      *rumination.Service
	embedder memory.Embedder
	cfg      config.Config
}

func (d *deps) close() { _ = d.db.Close() }

func loadDeps(ctx context.Context) (*deps, error) {
	cfg, err := loadOperationConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}
	skl := skills.NewService(skills.Config{Store: db.Skills()})
	// Resolve the embedder here rather than at each call site. When this was
	// omitted the memory service came up with a nil embedder, so every CLI
	// command silently ran FTS-only while `mnemos serve` ran hybrid — and the
	// two failure modes, "no embedder wired" and "no provider reachable", look
	// identical from the outside. Only `serve` had it.
	embedder := selectEmbedder(ctx, cfg.Embedding)
	return &deps{
		db:       db,
		embedder: embedder,
		mem:      memory.NewService(memoryServiceConfig(cfg.Search, db.Observations(), embedder, injection.NewLogger(db.Injections(), nil))),
		sess:     session.NewService(session.Config{Store: db.Sessions()}),
		skl:      skl,
		rum:      newRumination(cfg, db, skl),
		cfg:      cfg,
	}, nil
}

// newRumination composes the rumination service with whatever monitors
// the config enables. When `[rumination].enabled = false` we return nil
// so callers (dream, MCP surface) quietly skip the feature — same guarded
// pattern as skill promotion.
func newRumination(cfg config.Config, db *storage.DB, skl *skills.Service) *rumination.Service {
	if !cfg.Rumination.Enabled {
		return nil
	}
	monitors := []rumination.Monitor{
		&rumination.SkillEffectivenessMonitor{
			Skills:  skl,
			Floor:   cfg.Rumination.SkillEffectivenessFloor,
			MinUses: cfg.Rumination.SkillMinUses,
		},
		&rumination.StaleSkillMonitor{
			Skills:           skl,
			StaleDays:        cfg.Rumination.StaleSkillDays,
			MaxEffectiveness: cfg.Rumination.StaleSkillFloor,
		},
		&rumination.CorrectionRepeatUnderSkillMonitor{
			Corrections:     db.Observations(),
			Skills:          skl,
			RepeatThreshold: cfg.Rumination.CorrectionRepeatN,
		},
		&rumination.ContradictionDetectedMonitor{
			Links:     db.Observations(),
			Threshold: cfg.Rumination.ContradictionThreshold,
		},
		&rumination.StaleRawObservationMonitor{
			Observations: db.Observations(),
			StaleDays:    cfg.Rumination.StaleRawDays,
			RawPromote:   rumination.RawPromotePolicy(cfg.Rumination.RawPromote),
		},
	}
	return rumination.NewService(rumination.Config{
		Monitors: monitors,
		Skills:   skl,
		Memory:   db.Observations(),
		Store:    db.Rumination(),
	})
}

func runSearch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: mnemos search <query>")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	results, mode, err := d.mem.SearchWithMode(ctx, memory.SearchInput{Query: args[0]})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		fmt.Println("no matches")
		return nil
	}
	for _, r := range results {
		fmt.Printf("  %s  %-8s  %s\n", r.Observation.ID, r.Observation.Type, r.Observation.Title)
		fmt.Printf("    score %.3f = relevance %.3f × policy %.3f\n", r.Score, r.Relevance, r.PolicyFactor)
		fmt.Printf("    %s\n\n", r.Snippet)
	}
	fmt.Printf("%d result(s) · retrieval: %s\n", len(results), mode)
	return nil
}

func runStats(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("stats")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	st, err := d.mem.Stats(ctx)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	skillList, _ := d.skl.List(ctx, "")
	promotedCount := 0
	for _, s := range skillList {
		for _, tag := range s.Tags {
			if tag == "auto-promoted" {
				promotedCount++
				break
			}
		}
	}
	// storage_bytes comes from the typed stats value, the same number the MCP
	// stats surfaces render, so the CLI cannot disagree with them by stat'ing
	// a different file set.
	fmt.Printf("database:          %s (%d bytes)\n", d.cfg.Storage.Path, st.StorageBytes)
	fmt.Printf("observations:      %d (%d live)\n", st.Observations, st.LiveObservations)
	fmt.Printf("sessions:          %d\n", st.Sessions)
	// The backlog line is one session-service read (session.Service.OpenBacklog)
	// so mnemos stats and the MCP stats surfaces describe the same backlog.
	backlog, err := d.sess.OpenBacklog(ctx)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	fmt.Printf("open sessions: %d%s\n", backlog.Count, openBacklogSuffix(backlog))
	fmt.Printf("skills:            %d (%d auto-promoted from corrections)\n",
		len(skillList), promotedCount)
	if d.rum != nil {
		if c, err := d.rum.Counts(ctx); err == nil && (c.Pending+c.Resolved+c.Dismissed) > 0 {
			fmt.Printf("rumination:        %d pending, %d resolved, %d dismissed\n",
				c.Pending, c.Resolved, c.Dismissed)
		}
	}
	if len(st.TopTags) > 0 {
		fmt.Println("top tags:")
		for _, t := range st.TopTags {
			fmt.Printf("  %-16s %d\n", t.Tag, t.Count)
		}
	}
	return nil
}

func runExport(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("export")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return usageErrorf("expected at most one output file")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	out := io.Writer(os.Stdout)
	if fs.NArg() == 1 {
		name := fs.Arg(0)
		f, err := os.Create(name)
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		defer f.Close()
		out = f
	}
	warnContinuityOmission(ctx, d.db, os.Stderr)

	snap, err := dumpAll(ctx, d.db)
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func warnContinuityOmission(ctx context.Context, db *storage.DB, w io.Writer) {
	present, err := db.HasContinuity(ctx)
	if err != nil {
		fmt.Fprintln(w, "warning: continuity backup scope unknown (state detection failed); logical JSON export excludes continuity bindings/checkpoints; use a consistent same-machine SQLite backup — run `mnemos backup <path>` — to preserve them")
	} else if present {
		fmt.Fprintln(w, "warning: logical JSON export omits continuity bindings/checkpoints, revisions and retry identities; use a consistent same-machine SQLite backup — run `mnemos backup <path>` — to preserve them")
	}
}

func runImport(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErrorf("usage: mnemos import <file>")
	}
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open %s: %w", args[0], err)
	}
	defer f.Close()

	var snap snapshot
	if err := json.NewDecoder(f).Decode(&snap); err != nil {
		return fmt.Errorf("decode %s: %w", args[0], err)
	}

	if err := prepareSnapshot(&snap); err != nil {
		return fmt.Errorf("import snapshot: %w", err)
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	// Fidelity restore: every record keeps its original ID, timestamps,
	// provenance, and (for observations) bi-temporal validity. Re-importing a
	// record already present is skipped, not duplicated or re-versioned. This
	// is a trusted operation, the JSON equivalent of restoring a database
	// backup, so it preserves trust tiers verbatim rather than quarantining.
	//
	// Order matters for the observation -> session foreign key: sessions
	// restore first (with their original IDs), then observations resolve.
	var (
		obsAdded, obsSkipped     int
		sessAdded, sessSkipped   int
		skillAdded, skillSkipped int
		repointed                int
		knownSessions            = make(map[string]bool, len(snap.Sessions))
	)
	// Normalize legacy dangling session links before both identity preflight
	// and restore, so matching repeated imports compare the same owner identity.
	for _, session := range snap.Sessions {
		knownSessions[session.ID] = true
	}
	for i := range snap.Observations {
		if id := snap.Observations[i].SessionID; id != "" && !knownSessions[id] {
			snap.Observations[i].SessionID = ""
			repointed++
		}
	}
	if err := d.mem.ValidateRestoreMetadata(ctx, snap.Observations, snap.Links); err != nil {
		return fmt.Errorf("import metadata preflight: %w", err)
	}
	for i := range snap.Sessions {
		s := snap.Sessions[i]
		knownSessions[s.ID] = true
		added, err := d.sess.Restore(ctx, &s)
		if err != nil {
			return fmt.Errorf("import session %s: %w", s.ID, err)
		}
		countAddSkip(added, &sessAdded, &sessSkipped)
	}
	for i := range snap.Skills {
		sk := snap.Skills[i]
		added, err := d.skl.Restore(ctx, &sk)
		if err != nil {
			return fmt.Errorf("import skill %s: %w", sk.Name, err)
		}
		countAddSkip(added, &skillAdded, &skillSkipped)
	}
	var restoreErr error
	obsAdded, restoreErr = d.mem.RestoreEvolutionSnapshot(ctx, snap.Observations, snap.Links)
	if restoreErr != nil {
		return fmt.Errorf("import observations and metadata: %w", restoreErr)
	}
	obsSkipped = len(snap.Observations) - obsAdded
	fmt.Printf("imported from %s: observations %d added / %d skipped, sessions %d / %d, skills %d / %d\n",
		args[0], obsAdded, obsSkipped, sessAdded, sessSkipped, skillAdded, skillSkipped)
	if repointed > 0 {
		fmt.Printf("  %d observation(s) had a dangling session link cleared\n", repointed)
	}
	return nil
}

func countAddSkip(added bool, addCount, skipCount *int) {
	if added {
		*addCount++
	} else {
		*skipCount++
	}
}

func runPrune(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("prune")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected positional arguments")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		return err
	}
	defer d.close()

	n, err := d.mem.Prune(ctx)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	fmt.Printf("pruned %d expired observations\n", n)
	return nil
}

func runConfig(ctx context.Context, args []string) error {
	fs := newCommandFlagSet("config")
	cfgPath := fs.String("path", "", "explicit config path")
	client := fs.String("client", "", "nonsecret client projection (pi)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*client != "" && *client != "pi") {
		return usageErrorf("expected --client pi and no positional arguments")
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "path" })
	path, err := selectConfigPath(ctx, *cfgPath, explicit)
	if err != nil {
		return err
	}
	if explicit {
		ctx = config.ContextWithPath(ctx, path)
	}
	var cfg config.Config
	if *client == "pi" {
		cfg, err = readOperationConfig(ctx)
	} else {
		cfg, err = loadOperationConfig(ctx)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *client == "pi" {
		return enc.Encode(struct {
			ProtocolVersion int  `json:"protocol_version"`
			Disabled        bool `json:"disabled"`
			Continuity      struct {
				Enabled bool `json:"enabled"`
			} `json:"continuity"`
		}{ProtocolVersion: 1, Disabled: cfg.Integration.Disabled, Continuity: struct {
			Enabled bool `json:"enabled"`
		}{Enabled: cfg.Continuity.Enabled}})
	}
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

// labeledHook pairs a Claude Code hook entry with the name init and doctor
// print for it.
type labeledHook struct {
	label string
	entry installer.HookEntry
}

// claudeHooks is the Claude Code hook set mnemos installs. init wires it
// and doctor checks it, so the two cannot drift apart.
func claudeHooks(selfPath string) []labeledHook {
	return []labeledHook{
		{
			// resume and clear start a conversation with a context the old
			// session never saw, so they get a session and a prewarm too.
			label: "Claude Code SessionStart hook",
			entry: installer.HookEntry{
				Event:   "SessionStart",
				Matcher: "startup|resume|clear",
				Command: fmt.Sprintf("%s prewarm", selfPath),
				Timeout: 10,
			},
		},
		{
			// SessionStart output is added to the model's context, and the
			// compact source fires once the compacted conversation resumes,
			// so this is where recovery state has to be delivered.
			label: "Claude Code SessionStart compaction recovery hook",
			entry: installer.HookEntry{
				Event:   "SessionStart",
				Matcher: "compact",
				Command: fmt.Sprintf("%s prewarm --mode compaction_recovery", selfPath),
				Timeout: 10,
			},
		},
		{
			label: "Claude Code UserPromptSubmit hook",
			entry: installer.HookEntry{
				Event:   "UserPromptSubmit",
				Command: fmt.Sprintf("%s hook user-prompt", selfPath),
				Timeout: 5,
			},
		},
		{
			label: "Claude Code PostToolUse hook",
			entry: installer.HookEntry{
				Event:   "PostToolUse",
				Matcher: "Edit|Write|MultiEdit|NotebookEdit",
				Command: fmt.Sprintf("%s hook post-tool", selfPath),
				Timeout: 5,
			},
		},
		{
			label: "Claude Code SessionEnd hook",
			entry: installer.HookEntry{
				Event:   "SessionEnd",
				Command: fmt.Sprintf("%s hook session-end", selfPath),
				Timeout: 5,
			},
		},
		{
			label: "Claude Code PreToolUse guardrail",
			entry: installer.HookEntry{
				Event:   "PreToolUse",
				Matcher: "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention|mcp__mnemos__mnemos_checkpoint",
				Command: fmt.Sprintf("%s hook pre-tool", selfPath),
				Timeout: 5,
			},
		},
		{
			label: "Claude Code PreToolUse just-in-time memory",
			entry: installer.HookEntry{
				Event:   "PreToolUse",
				Matcher: "Edit|Write|MultiEdit|NotebookEdit",
				Command: fmt.Sprintf("%s hook pre-tool", selfPath),
				Timeout: 5,
			},
		},
	}
}

// obsoleteClaudeHooks are entries earlier releases installed that init now
// removes. The PreCompact and PostCompact hooks wrote their recovery block
// to stderr, which never appeared in a single transcript, while every run
// logged its memories as surfaced. The startup-only SessionStart entry is
// superseded by the startup|resume|clear one; leaving both would fire
// prewarm twice on every launch.
func obsoleteClaudeHooks(selfPath string) []labeledHook {
	return []labeledHook{
		{
			label: "startup-only SessionStart hook",
			entry: installer.HookEntry{
				Event: "SessionStart", Matcher: "startup",
				Command: fmt.Sprintf("%s prewarm", selfPath),
			},
		},
		{
			label: "PreCompact hook",
			entry: installer.HookEntry{
				Event: "PreCompact", Command: fmt.Sprintf("%s hook pre-compact", selfPath),
			},
		},
		{
			label: "PostCompact hook",
			entry: installer.HookEntry{
				Event: "PostCompact", Command: fmt.Sprintf("%s hook post-compact", selfPath),
			},
		},
		{
			label: "three-tool PreToolUse guardrail",
			entry: installer.HookEntry{
				Event: "PreToolUse", Matcher: "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention",
				Command: fmt.Sprintf("%s hook pre-tool", selfPath),
			},
		},
	}
}

func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: mnemos init\nPlan and install detected agent integrations; independent successes remain if another target fails.")
	}
	// flag stops at help/the first positional. Validate the entire argument
	// list first so trailing garbage never hides behind a help request.
	for _, arg := range args {
		if arg != "-h" && arg != "--help" {
			return usageErrorf("init: unexpected argument %q", arg)
		}
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return usageErrorf("init: %v", err)
	}
	if fs.NArg() != 0 {
		return usageErrorf("init: unexpected positional arguments")
	}
	// Resolve every route before the first install writes user configuration.
	cfg, err := readOperationConfig(ctx)
	if err != nil {
		return fmt.Errorf("init: load config: %w", err)
	}
	opts := installerOptions(cfg)
	plans := installer.PlanTargets(opts)
	selfPath, err := os.Executable()
	if err != nil {
		selfPath = "mnemos"
	}
	entry := installer.ServerEntry{Command: selfPath, Args: []string{"serve"}}
	if config.HasExplicitPath(ctx) {
		entry.Args = []string{"--config", config.PathFromContext(ctx), "serve"}
	}
	hookPath, err := configuredCommand(ctx, selfPath)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}

	if len(plans) == 0 {
		fmt.Println("no agent clients detected (Claude Code, Claude Desktop, Cursor, Windsurf, Codex CLI, pi).")
		fmt.Println("install one of them first, then run `mnemos init` again.")
		return nil
	}
	var failures []error
	hasClaudeCode := false
	for _, plan := range plans {
		t := plan.Target
		if plan.Err != nil {
			fmt.Printf("  ✗ %s (%s): %v\n", t.Name, t.Path, plan.Err)
			failures = append(failures, fmt.Errorf("init: plan %s (%s): %w", t.Name, t.Path, plan.Err))
			continue
		}
		changed, err := installer.Install(t, entry)
		if err != nil {
			fmt.Printf("  ✗ %s (%s): %v\n", t.Name, t.Path, err)
			failures = append(failures, fmt.Errorf("init: install %s (%s): %w", t.Name, t.Path, err))
			continue
		}
		if changed {
			fmt.Printf("  ✓ %s registered at %s\n", t.Name, t.Path)
		} else {
			fmt.Printf("  ○ %s already up to date\n", t.Name)
		}
		if t.Name == "Claude Code (user)" {
			hasClaudeCode = true
		}
	}

	// Claude Code specific hooks. Without them, mnemos tool calls are
	// optional to the agent, and agents skip optional calls when the task
	// looks like plain editing — the failure this wiring exists to fix.
	if hasClaudeCode {
		settings := installer.ClaudeSettingsPath(opts)
		for _, old := range obsoleteClaudeHooks(selfPath) {
			changed, err := installer.UninstallHook(settings, old.entry)
			switch {
			case err != nil:
				fmt.Printf("  ✗ removing %s (%s): %v\n", old.label, settings, err)
				failures = append(failures, fmt.Errorf("init: remove %s (%s): %w", old.label, settings, err))
			case changed:
				fmt.Printf("  ✓ removed %s from %s\n", old.label, settings)
			}
		}
		for _, h := range claudeHooks(hookPath) {
			if _, err := installer.UninstallHookVariants(settings, h.entry, selfPath); err != nil {
				failures = append(failures, fmt.Errorf("init: migrate %s (%s): %w", h.label, settings, err))
			}
			changed, err := installer.InstallHook(settings, h.entry)
			switch {
			case err != nil:
				fmt.Printf("  ✗ %s (%s): %v\n", h.label, settings, err)
				failures = append(failures, fmt.Errorf("init: install %s (%s): %w", h.label, settings, err))
			case changed:
				fmt.Printf("  ✓ %s wired at %s\n", h.label, settings)
			default:
				fmt.Printf("  ○ %s already up to date\n", h.label)
			}
		}
	}

	fmt.Println()
	if err := errors.Join(failures...); err != nil {
		fmt.Println("installation incomplete; successful steps were retained. Resolve the reported errors and rerun mnemos init.")
		return err
	}
	fmt.Println("restart your agent. the mnemos_* tools will appear next session.")
	return nil
}

func runDoctor(ctx context.Context, args []string) error {
	if handled, err := doctorWorkspaceMode(ctx, args, os.Stdout); handled {
		return err
	}
	ok := true
	check := func(pass bool, format string, args ...any) {
		prefix := "  ✓ "
		if !pass {
			prefix = "  ✗ "
			ok = false
		}
		fmt.Printf(prefix+format+"\n", args...)
	}

	selfPath, _ := os.Executable()
	check(selfPath != "", "binary path: %s", selfPath)

	cfg, err := loadOperationConfig(ctx)
	if err != nil {
		check(false, "config load: %v", err)
		return fmt.Errorf("doctor: %w", err)
	}
	check(true, "config: %s", config.PathFromContext(ctx))
	opts := installerOptions(cfg)

	db, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		check(false, "storage open: %v", err)
		return fmt.Errorf("doctor: %w", err)
	}
	defer db.Close()
	st, _ := db.Observations().Stats(ctx)
	check(true, "storage: %s (%d observations)", cfg.Storage.Path, st.Observations)

	targets := installer.DetectTargets(opts)
	if len(targets) == 0 {
		check(false, "no agent clients detected")
	}
	hasClaudeCode := false
	hasPi := false
	for _, t := range targets {
		installed := installer.IsInstalled(t)
		if t.Name == "pi" && !installed {
			// A package manifest can supply the same server without an
			// adapter-global entry. Verify its actual files, not its name.
			if ok, path := installer.PiPackageMCPInstalled(opts); ok {
				check(true, "pi package MCP %s", path)
			} else {
				check(false, "%s %s", t.Name, t.Path)
			}
		} else {
			check(installed, "%s %s", t.Name, t.Path)
		}
		switch t.Name {
		case "Claude Code (user)":
			hasClaudeCode = true
		case "pi":
			hasPi = true
		}
	}

	// pi's hooks live in the mnemos pi package, not in a settings file
	// mnemos writes, so the check is whether that package is registered with
	// pi. It is reported rather than failed on: MCP-only is a legitimate
	// configuration (it is the Codex parity level), and failing the whole
	// health check over an optional capability would be misleading.
	if hasPi {
		installed, path := installer.PiPackageInstalled(opts)
		if installed {
			fmt.Printf("  • pi hooks: mnemos package present in %s\n", path)
		} else {
			fmt.Printf("  • pi hooks: not installed — mnemos MCP tools work, but the pi package adds prewarm, prompt recall, the capture directive, and the write guardrail\n")
		}
	}

	if hasClaudeCode {
		settings := installer.ClaudeSettingsPath(opts)
		hookPath, err := configuredCommand(ctx, selfPath)
		if err != nil {
			return fmt.Errorf("doctor: %w", err)
		}
		for _, h := range claudeHooks(hookPath) {
			check(installer.IsHookInstalled(settings, h.entry), "%s %s", h.label, settings)
		}
		for _, old := range obsoleteClaudeHooks(selfPath) {
			if installer.IsHookInstalled(settings, old.entry) {
				check(false, "obsolete %s still installed in %s (run `mnemos init` to remove it)", old.label, settings)
			}
		}
	}
	if !ok {
		return errors.New("doctor found issues")
	}
	fmt.Println("all checks passed.")
	return nil
}

type snapshot struct {
	Observations  []memory.Observation `json:"observations"`
	Sessions      []session.Session    `json:"sessions"`
	Skills        []skills.Skill       `json:"skills"`
	SchemaVersion int                  `json:"schema_version,omitempty"`
	Evidence      []snapshotEvidence   `json:"evidence,omitempty"`
	Links         []memory.LinkEdge    `json:"supersession_links,omitempty"`
}

func dumpAll(ctx context.Context, db *storage.DB) (*snapshot, error) {
	out := &snapshot{SchemaVersion: 2}
	// Coherent observations/evidence/links snapshot, no per-id Get. The previous loop held a SELECT
	// cursor open while calling Get (which itself queries and writes
	// access_count) for each row, which deadlocks the single-connection pool
	// on any populated store and bumps access counts as a side effect of
	// merely exporting.
	obs, links, err := memory.NewService(memory.Config{Store: db.Observations()}).ExportEvolutionSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("export observations: %w", err)
	}
	out.Observations = obs
	out.Links = links
	for i := range out.Observations {
		for _, e := range out.Observations[i].Evidence {
			out.Evidence = append(out.Evidence, snapshotEvidence{ObservationID: out.Observations[i].ID, Evidence: e})
		}
		out.Observations[i].Evidence = nil
	}
	if list, err := db.Sessions().Recent(ctx, "", 10000); err == nil {
		out.Sessions = list
	}
	if list, err := db.Skills().List(ctx, ""); err == nil {
		out.Skills = list
	}
	return out, nil
}
