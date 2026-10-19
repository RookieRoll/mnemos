package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/safety"
	"github.com/polyxmedia/mnemos/internal/session"
)

// projectFromHook derives the project name from the hook payload's cwd,
// falling back to the process working directory. Same derivation the
// prewarm command uses, so SessionStart and SessionEnd agree on which
// project a session belongs to.
func projectFromHook(in hookInput) string {
	cwd := in.CWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// resolveHookSession keeps the two payload ID contracts distinct. Claude sends
// its host conversation ID; neutral consumers send the Mnemos ID prewarm returned.
// An explicit neutral ID must never fall back to another terminal's session.
func resolveHookSession(ctx context.Context, d *deps, in hookInput, neutral bool) (*session.Session, error) {
	project := projectFromHook(in)
	if neutral && in.SessionID != "" {
		sess, err := d.sess.Get(ctx, in.SessionID)
		if err != nil {
			return nil, err
		}
		if sess.EndedAt != nil || sess.Project != project {
			return nil, session.ErrNotFound
		}
		return sess, nil
	}
	return d.sess.Resolve(ctx, in.SessionID, project)
}

// runHook is the parent dispatcher for harness-side hook subcommands. Each
// leaf command reads a Claude Code hook payload from stdin (shape documented
// at https://code.claude.com/docs/en/hooks) and performs one invisible,
// best-effort side effect against the mnemos store. Failure is always silent
// at exit 0 — hooks must never block the user or spam the transcript.
//
// Honors MNEMOS_DISABLED=1 as a global kill-switch: the verify harness sets
// this in the off-arm so the user's globally-installed mnemos hooks (which
// fire regardless of --strict-mcp-config) cannot contaminate the off
// transcript with prewarm-injected memory or auto-search blocks.
func runHook(ctx context.Context, args []string) error {
	if os.Getenv("MNEMOS_DISABLED") != "" {
		return nil
	}
	if len(args) == 0 {
		return errors.New("usage: mnemos hook <user-prompt|post-tool|session-end|pre-tool|pre-compact|post-compact> [--payload JSON] [--format json]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "user-prompt":
		return runHookUserPrompt(ctx, rest)
	case "post-tool":
		return runHookPostTool(ctx, rest)
	case "session-end":
		return runHookSessionEnd(ctx, rest)
	case "pre-tool":
		return runHookPreTool(ctx, rest)
	case "pre-compact":
		return runHookPreCompact(ctx, rest)
	case "post-compact":
		return runHookPostCompact(ctx, rest)
	default:
		return fmt.Errorf("unknown hook: %s", sub)
	}
}

// hookFlagSet builds the flag set every hook leaf shares.
func hookFlagSet(sub string) (*flag.FlagSet, hookFlags) {
	fs := flag.NewFlagSet("hook "+sub, flag.ContinueOnError)
	return fs, newHookFlags(fs)
}

// runHookUserPrompt handles Claude Code's UserPromptSubmit event. Two
// invisible side effects:
//  1. Backfill the open session's goal if empty (one-shot, idempotent).
//  2. Search mnemos for relevant prior memory and inject the top hits to
//     stdout so Claude Code adds them to the model's context — bypassing
//     the agent's choice to (or not to) call mnemos_search itself. The
//     correction `agent skipped mnemos_session_start on editing tasks`
//     was saved precisely because LLMs skip optional tool calls; this
//     promotes mnemos from optional to ambient.
//
// Turns the user did not type (see isSyntheticPrompt) get neither.
//
// Both effects degrade silently. UserPromptSubmit stdout is injected into
// the model context, so a failed search must produce zero stdout.
func runHookUserPrompt(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("user-prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := flags.resolve()
	result := collectUserPrompt(ctx, in, flags.neutral())
	flags.write(hookResult{
		SessionID: result.SessionID,
		Context:   result.context(),
	}, func() {
		writeClaudePromptOutput(os.Stdout, result.MemoryBlock, result.Directive)
	})
	return nil
}

// promptResultText joins the memory block and the capture directive into
// the single context blob the neutral envelope carries. They are two
// paragraphs of one injection, so a harness that consumes the envelope
// does not need to reassemble them.
func (r userPromptResult) context() string {
	parts := make([]string, 0, 2)
	if r.MemoryBlock != "" {
		parts = append(parts, strings.TrimRight(r.MemoryBlock, "\n"))
	}
	if r.Directive != "" {
		parts = append(parts, r.Directive)
	}
	return strings.Join(parts, "\n")
}

// userPromptResult is the decided outcome of a prompt hook. Everything
// that goes to the store happened during the decision; rendering only
// turns these strings into bytes.
//
// SessionID is carried even when no session is open, so the envelope
// reports nothing rather than an empty string.
type userPromptResult struct {
	SessionID   string
	MemoryBlock string
	Directive   string
}

// collectUserPrompt performs every prompt-time decision and side effect:
// goal backfill, memory retrieval with the relevance floor and the
// suppression window, the injection-log write, and the surfaced counter.
// It holds no writer, so a second harness can consume the same decisions
// without reimplementing any of them.
func collectUserPrompt(ctx context.Context, in hookInput, neutral ...bool) userPromptResult {
	if in.Prompt == "" || isSyntheticPrompt(in.Prompt) {
		return userPromptResult{}
	}

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		return userPromptResult{}
	}
	defer d.close()

	project := projectFromHook(in)
	sess, err := resolveHookSession(ctx, d, in, len(neutral) > 0 && neutral[0])
	switch {
	case err != nil && !errors.Is(err, session.ErrNotFound):
		fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
	case sess != nil && sess.Goal == "":
		if err := d.sess.SetGoalIfEmpty(ctx, sess.ID, truncateGoal(in.Prompt)); err != nil {
			fmt.Fprintln(os.Stderr, "mnemos hook user-prompt:", err)
		}
	}

	var sessID, agentID string
	if sess != nil {
		sessID, agentID = sess.ID, sess.AgentID
	}
	m := collectPromptMemory(ctx, d, in.Prompt, agentID, project, sessID)
	return userPromptResult{
		SessionID:   sessID,
		MemoryBlock: formatPromptMemory(m),
		Directive:   captureDirectiveText(detectCaptureShape(in.Prompt)),
	}
}

// maxAutoGoalChars caps the goal we backfill from a user prompt. Goals are
// shown in prewarm output and session lists, so we keep them one-line.
const maxAutoGoalChars = 120

// isSyntheticPrompt reports whether a UserPromptSubmit prompt is a turn the
// harness generated rather than one the user typed: a background task
// notification, a report from a subagent or another session, the summary
// Claude Code writes after a compaction, or a slash-command echo. The hook
// payload carries no field that tells these apart, so the check reads the
// markers they open with.
//
// Between 2026-07-27 and 2026-09-30 these were 17% of prompt_hook events
// and 24 of the 30 capture directives. A subagent's report is full of
// "we tried" and "going forward", which the capture patterns read as the
// user correcting the agent.
func isSyntheticPrompt(prompt string) bool {
	p := strings.TrimSpace(prompt)
	prefixes := []string{
		"<task-notification>",
		"<agent-message",
		"Another Claude session sent a message",
		"This session is being continued from a previous conversation",
		"[SYSTEM NOTIFICATION",
		"<command-name>",
		"<command-message>",
		"<local-command-stdout>",
		"[Request interrupted by user",
	}
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	// Notifications can arrive behind a short harness banner, so the
	// structural tags also count when they appear near the top.
	head := p
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.Contains(head, "<task-notification>") || strings.Contains(head, "<agent-message")
}

// minPromptContentWords is how many content words a prompt needs before
// its text is worth searching on. Every prompt token becomes an OR'd
// prefix term in the FTS query, so "yes", "show me" or "so we good?"
// matched stored memories on filler alone. Replaying 70 such prompts from
// real sessions injected memories into 67 of them.
const minPromptContentWords = 2

// hasSearchableContent reports whether prompt carries at least
// minPromptContentWords words of three or more letters (any script) that
// are not English conversational filler. Scripts commonly written without
// spaces also qualify with four CJK letters; treating a whole Chinese task
// as one word would suppress meaningful prompts while retaining English ones.
func hasSearchableContent(prompt string) bool {
	cjk := 0
	for _, r := range prompt {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			cjk++
			if cjk >= 4 {
				return true
			}
		}
	}
	filler := map[string]bool{
		"yes": true, "yeah": true, "yep": true, "nope": true, "okay": true,
		"please": true, "thanks": true, "thank": true, "sure": true, "cool": true,
		"nice": true, "great": true, "good": true, "fine": true, "done": true,
		"the": true, "and": true, "but": true, "for": true, "with": true,
		"you": true, "your": true, "that": true, "this": true, "then": true,
		"now": true, "can": true, "lets": true, "let's": true, "what": true,
		"how": true, "why": true, "show": true, "just": true, "ahead": true,
		"keep": true, "going": true, "continue": true, "carry": true, "all": true,
		"are": true, "was": true, "did": true, "does": true, "got": true,
		"get": true, "not": true, "it's": true, "its": true, "too": true,
	}
	n := 0
	for _, w := range strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	}) {
		if utf8.RuneCountInString(w) < 3 || filler[w] {
			continue
		}
		n++
		if n >= minPromptContentWords {
			return true
		}
	}
	return false
}

// captureSignal names what kind of write tool the user's phrasing is
// asking for. Returned by detectCaptureShape; consumed by
// captureDirectiveText.
type captureSignal int

const (
	captureNone captureSignal = iota
	captureCorrection
	captureConvention
	captureSave
)

// detectCaptureShape pattern-matches the user's prompt for phrasing that
// should fire a write tool. Description-level hints (in the MCP tool
// docs) ceiling out around 13% capture in claude -p — the agent stays
// task-focused and skips optional tool calls. This adds a directive
// block to the prompt context that's much harder to skip.
//
// Patterns are intentionally conservative — we want low false-positive
// rate, not maximal recall. Better to miss a borderline correction than
// nag the agent on every routine prompt.
func detectCaptureShape(prompt string) captureSignal {
	p := strings.ToLower(prompt)

	// Convention: explicit project rules. Highest precedence — "we always
	// do X" is a rule, not a one-off correction.
	conventionPatterns := []string{
		"we always", "we never",
		"in this codebase we", "in this repo we", "in this project we",
		"the convention here is", "the convention is",
		"by convention", "the rule is", "the rule here is",
		"every error", "every commit", "every migration",
	}
	for _, pat := range conventionPatterns {
		if strings.Contains(p, pat) {
			return captureConvention
		}
	}

	// Correction: tried/wrong/fix shape, including past-failure references.
	correctionPatterns := []string{
		"we tried", "we were wrong",
		"going forward", "from now on",
		"don't do", "do not do", "don't use", "do not use",
		"that's wrong", "that is wrong",
		"actually wait", "hold on",
		"caused the bug", "caused a bug",
		"last time we", "last quarter we", "last week we",
		"don't ever", "never do",
	}
	for _, pat := range correctionPatterns {
		if strings.Contains(p, pat) {
			return captureCorrection
		}
	}

	// Save: explicit memory request or architectural-decision narration.
	savePatterns := []string{
		"save this", "remember this", "remember that",
		"note this", "note that", "for future sessions",
		"keep this for next", "we just decided",
		"we're going with", "we are going with", "going with",
	}
	for _, pat := range savePatterns {
		if strings.Contains(p, pat) {
			return captureSave
		}
	}

	return captureNone
}

// promptMemoryMinScore is the composite-score-contract floor under which
// we suppress injection. The contract scale is Score = relevance x policy,
// ceiling 1.2 (see docs/ARCHITECTURE.md) — the same scale in keyword and
// hybrid retrieval, which is what keeps this floor meaningful in both.
// Calibrated by the gate sweep (mnemos verify calibrate) over the seeded
// calibration corpus, corroborated by translating the old empirically
// tuned BM25-scale floor of 1.5 onto the contract scale (~0.15). Below the
// floor we'd be force-feeding weak-batch noise into every prompt's context
// window; the suppression window and promptMemoryMaxHits handle repeats.
const promptMemoryMinScore = 0.10

// promptMemoryMaxHits caps how many memories we inject per prompt. Three
// is small enough to stay invisible on routine prompts but large enough to
// catch the "convention + correction + decision" cluster around a topic.
const promptMemoryMaxHits = 3

// collectPromptMemory searches mnemos for the user's prompt and keeps the
// hits worth surfacing. Best-effort: every error path yields no hits, so
// a hook bug never poisons the agent's context. Kept hits are recorded
// in the injection log (channel prompt_hook) so surfaced-vs-used ratios
// cover this path too.
func collectPromptMemory(ctx context.Context, d *deps, prompt, agentID, project, sessionID string) promptMemory {
	return collectPromptMemoryAt(ctx, d, prompt, agentID, project, sessionID, time.Now(), true)
}

// collectPromptMemoryAt shares the actual gate with read-only evaluation.
func collectPromptMemoryAt(ctx context.Context, d *deps, prompt, agentID, project, sessionID string, now time.Time, record bool) promptMemory {
	if d == nil || !hasSearchableContent(prompt) {
		return promptMemory{}
	}
	// PreferProject, not Project: a hard filter would drop project-less
	// global conventions. Soft affinity downranks other projects' memories
	// so they only surface when strongly on-topic.
	hits, err := d.mem.Search(ctx, memory.SearchInput{
		Query:         prompt,
		PreferProject: project,
		Limit:         promptMemoryMaxHits,
	})
	if err != nil || len(hits) == 0 {
		return promptMemory{}
	}
	// Suppress memories already surfaced into this project's context in the
	// last window. Without this the same three memories ride along on every
	// single prompt for as long as the topic holds — 6879 prompt_hook
	// injections drew on 61 distinct memories before this existed.
	injStore := d.db.Injections()
	suppressed := injection.Suppressed(ctx, injStore, injection.KindObservation, project, now)

	kept := hits[:0]
	for _, h := range hits {
		if h.Score < promptMemoryMinScore {
			continue
		}
		if _, dup := suppressed[h.Observation.ID]; dup {
			continue
		}
		kept = append(kept, h)
	}
	if len(kept) == 0 {
		return promptMemory{}
	}
	refs := make([]injection.Ref, 0, len(kept))
	for _, h := range kept {
		refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
	}
	if record {
		_ = injection.NewLogger(injStore, nil).
			Log(ctx, injection.ChannelPromptHook, agentID, project, sessionID, refs)
		_ = d.mem.RecordSurfaced(ctx, refIDs(refs))
	}
	return promptMemory{Hits: kept}
}

// refIDs projects injection refs down to their bare IDs for RecordSurfaced.
func refIDs(refs []injection.Ref) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ID)
	}
	return out
}

// truncateGoal folds whitespace and caps to maxAutoGoalChars, appending an
// ellipsis when truncated. Pure helper; no I/O.
func truncateGoal(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= maxAutoGoalChars {
		return s
	}
	return s[:maxAutoGoalChars-1] + "…"
}

// runHookPostTool handles Claude Code's PostToolUse event. For file-editing
// tools (Edit / Write / MultiEdit / NotebookEdit), it records a passive
// touch against the heat map so the store stays populated even when the
// agent never calls mnemos_touch. Matcher filtering happens at the Claude
// Code level; this command is defensive and no-ops on any other tool.
//
// The matcher we install (`Edit|Write|MultiEdit|NotebookEdit`) is an exact
// alternation — the schema documents this as the format when the string has
// no regex metacharacters but contains pipes.
func runHookPostTool(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("post-tool")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := flags.resolve()
	// Side-effect only: the envelope is emitted for transport symmetry so a
	// harness can parse one shape for every hook, and always reports "no
	// context, no block".
	flags.write(hookResult{}, func() {})
	if !isFileEditToolFor(in.ToolName, flags.neutral()) {
		return nil
	}
	path := filePathFromToolInput(in.ToolInput)
	if path == "" {
		return nil
	}

	proj := projectFromHook(in)

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
		return nil
	}
	defer d.close()

	// Best-effort session stamp. If nothing is open we still record the
	// touch so the heat map does not lose data; SessionID just stays empty.
	var sessID string
	if sess, err := resolveHookSession(ctx, d, in, flags.neutral()); err == nil && sess != nil {
		sessID = sess.ID
	} else if err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
	}

	if err := d.db.Touches().Record(ctx, memory.TouchInput{
		Project:   proj,
		Path:      path,
		SessionID: sessID,
		Note:      "auto:" + in.ToolName,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook post-tool:", err)
	}
	return nil
}

// isFileEditTool reports whether the Claude Code tool name is one we want
// to record as a file touch. Kept as a set so adding new editing tools is
// a one-liner.
func isFileEditTool(name string) bool {
	return isFileEditToolFor(name, false)
}

// isFileEditToolFor is isFileEditTool with control over case sensitivity.
//
// The strict form matches the Claude Code matcher's names exactly, which is
// correct there because the installed matcher already dispatched only
// those. A neutral-format caller matches case-insensitively because pi
// registers its built-in editing tools lowercase (`edit`, `write`) while
// the Claude Code matcher uses `Edit` and `Write`. Without this the
// pre-tool hook returns no context for every pi edit — the same silent
// failure the write-tool match had, one branch over.
func isFileEditToolFor(name string, foldCase bool) bool {
	if !foldCase {
		switch name {
		case "Edit", "Write", "MultiEdit", "NotebookEdit":
			return true
		}
		return false
	}
	lower := strings.ToLower(name)
	for _, base := range []string{"edit", "write", "multiedit", "notebookedit"} {
		if lower == base || strings.HasSuffix(lower, "_"+base) {
			return true
		}
	}
	return false
}

// filePathFromToolInput extracts the file path from a tool_input map.
// Returns "" if absent or not a string.
//
// Two field names are accepted deliberately. Claude Code's editing tools
// and mnemos' own MCP tools use `file_path`; pi's built-in `edit` and
// `write` use `path`. Reading only one of them would make every edit on the
// other harness query for an empty path and surface nothing.
func filePathFromToolInput(m map[string]any) string {
	if m == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "notebook_path"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// runHookSessionEnd handles Claude Code's SessionEnd event. Closes the
// session(s) this conversation opened, found by the payload's session_id
// (duplicate SessionStart installs can leave twins carrying the same one),
// with a summary of that session's own activity and a status derived from
// the reason Claude Code supplied. Another terminal's session in the same
// project stays open. No-ops when the agent already called
// mnemos_session_end properly — session.Close guards on ended_at IS NULL.
// Finishes with a stale sweep so sessions orphaned by killed terminals
// (no SessionEnd ever fires) don't stay open forever.
func runHookSessionEnd(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("session-end")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := flags.resolve()
	flags.write(hookResult{}, func() {})

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return nil
	}
	defer d.close()

	proj := projectFromHook(in)
	closeIn := session.CloseInput{
		Status:      sessionStatusFromReason(in.Reason),
		OutcomeTags: []string{"auto-closed:" + sanitizeReason(in.Reason)},
	}
	// The summary is per session, so it has to be derived before the
	// close; the resolved session is the one CloseForHost will pick.
	if sess, err := resolveHookSession(ctx, d, in, flags.neutral()); err == nil && sess != nil {
		closeIn.Summary = deriveSessionSummary(ctx, d, sess.ID)
	}
	var closeErr error
	if flags.neutral() && in.SessionID != "" {
		if sess, err := resolveHookSession(ctx, d, in, true); err == nil {
			closeIn.ID = sess.ID
			closeErr = d.sess.Close(ctx, closeIn)
		} else if !errors.Is(err, session.ErrNotFound) {
			closeErr = err
		}
	} else {
		_, closeErr = d.sess.CloseForHost(ctx, in.SessionID, proj, closeIn)
	}
	if closeErr != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", closeErr)
	}

	sweepStaleSessions(ctx, d)
	return nil
}

// closeSessionQuietly closes one session, logging anything except the
// benign already-closed race to stderr. Hooks never fail loudly.
func closeSessionQuietly(ctx context.Context, d *deps, in session.CloseInput) {
	if err := d.sess.Close(ctx, in); err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
	}
}

// staleSessionMaxAge is how long a session may stay open before the sweep
// declares it abandoned. SessionEnd never fires for killed terminals or
// crashed hosts, so without the sweep those sessions hold ended_at NULL
// forever and pollute Current()/ListOpen() for every later hook.
const staleSessionMaxAge = 24 * time.Hour

// sweepStaleSessions closes open sessions older than staleSessionMaxAge
// across all projects as abandoned. Runs piggybacked on SessionEnd — the
// next clean exit anywhere tidies the whole store.
func sweepStaleSessions(ctx context.Context, d *deps) {
	open, err := d.sess.ListLegacyOpen(ctx, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook session-end:", err)
		return
	}
	cutoff := time.Now().UTC().Add(-staleSessionMaxAge)
	for _, sess := range open {
		if !sess.StartedAt.Before(cutoff) {
			continue
		}
		closeSessionQuietly(ctx, d, session.CloseInput{
			ID:          sess.ID,
			Summary:     "auto-closed as stale: open longer than 24h with no SessionEnd (terminal killed or hook never fired)",
			Status:      session.StatusAbandoned,
			OutcomeTags: []string{"auto-closed:stale"},
		})
	}
}

// deriveSessionSummary builds a one-line recap from the session's own
// activity: counts its observations and names the files it touched most.
// Best effort — returns "" if the observation query fails. A small,
// deterministic summary beats an empty close.
func deriveSessionSummary(ctx context.Context, d *deps, sessID string) string {
	obs, err := d.db.Observations().ListBySession(ctx, sessID)
	if err != nil {
		return ""
	}
	parts := []string{}
	if n := len(obs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d observation(s)", n))
	}
	if hot, err := d.db.Touches().SessionHot(ctx, sessID, 5); err == nil && len(hot) > 0 {
		files := make([]string, 0, len(hot))
		for _, h := range hot {
			files = append(files, filepath.Base(h.Path))
		}
		parts = append(parts, "touched "+strings.Join(files, ", "))
	}
	if len(parts) == 0 {
		return "auto-closed on SessionEnd (no activity recorded)"
	}
	return "auto-closed on SessionEnd: " + strings.Join(parts, "; ")
}

// sessionStatusFromReason maps Claude Code's SessionEnd `reason` to a
// mnemos session status. Ctrl+C style exits get StatusAbandoned so
// replay/learning can weight them differently from clean closes.
func sessionStatusFromReason(reason string) session.Status {
	switch reason {
	case "prompt_input_exit":
		return session.StatusAbandoned
	case "bypass_permissions_disabled":
		return session.StatusBlocked
	default:
		return session.StatusOK
	}
}

// sanitizeReason defaults to "unknown" when Claude Code omitted the field
// so outcome tags stay queryable.
func sanitizeReason(r string) string {
	if r == "" {
		return "unknown"
	}
	return r
}

// runHookPreTool handles Claude Code's PreToolUse event. Two surfaces in
// one subcommand, dispatched on tool_name:
//
//  1. Guardrail on mnemos write tools: runs the safety scanner over the
//     tool_input payload; an elevated-risk prompt-injection pattern exits
//     2 with stderr fed back to the model, and the save never lands.
//  2. Just-in-time memory on file-edit tools: searches corrections and
//     conventions relevant to the file about to be written and returns
//     them as PreToolUse additionalContext — the memory arrives at the
//     decision point, not 40 turns earlier in the session-start block.
func runHookPreTool(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("pre-tool")
	guardrailOnly := fs.Bool("guardrail-only", false, "scan writes without resolving a session or retrieving memory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := flags.resolve()
	// The prefixed match is enabled only for the neutral format: that
	// caller has no dispatch matcher narrowing the call, so the tool name
	// is the only signal it can offer. The default path keeps the strict
	// Claude Code match.
	msg, block := decidePreToolFor(in, flags.neutral())
	if block {
		flags.writeBlocked(msg)
		return nil
	}
	if *guardrailOnly {
		flags.write(hookResult{}, func() {})
		return nil
	}
	// The guardrail ran first and let the call through, so a refused edit
	// never reaches the memory push and a blocked invocation carries no
	// context.
	if !isFileEditToolFor(in.ToolName, flags.neutral()) {
		flags.write(hookResult{}, func() {})
		return nil
	}
	context := formatPreToolMemory(collectPreToolMemory(ctx, in, flags.neutral()))
	// No session_id in this envelope: a pre-tool invocation neither
	// establishes nor reuses a session, it only observes one. The session
	// identifier a harness scopes its writes to comes from prewarm.
	flags.write(hookResult{Context: context}, func() {
		writeClaudePreToolOutput(os.Stdout, context)
	})
	return nil
}

// preToolMemoryMaxHits caps how many memories ride along on one edit.
// Same rationale as the prompt hook's cap: invisible on routine edits,
// enough to carry the correction + convention cluster around a file.
const preToolMemoryMaxHits = 3

// collectPreToolMemory searches the store for corrections and conventions
// relevant to the file a PreToolUse(Edit|Write) event is about to touch.
// Memories already surfaced in the current session (any channel, read
// from the injection log) are skipped, so repeated edits to the same file
// don't spam the context with the same warning. Best-effort: every
// failure path yields no hits.
//
// This never blocks and never auto-approves — blocking is the guardrail's
// job, decided separately in decidePreTool. The caller only reaches here
// when the guardrail let the call through, which is why a blocked edit
// carries no memory: the call was refused, so there is nothing to inform.
func collectPreToolMemory(ctx context.Context, in hookInput, neutral ...bool) preToolMemory {
	path := filePathFromToolInput(in.ToolInput)
	if path == "" {
		return preToolMemory{}
	}
	root := in.CWD
	if root == "" {
		root, _ = os.Getwd()
	}
	query := pathQueryTokens(path, root)
	if query == "" {
		return preToolMemory{}
	}

	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook pre-tool:", err)
		return preToolMemory{}
	}
	defer d.close()

	proj := projectFromHook(in)
	hits, err := d.mem.Search(ctx, memory.SearchInput{
		Query:   query,
		Project: proj,
		Limit:   preToolMemoryMaxHits * 2,
	})
	if err != nil || len(hits) == 0 {
		return preToolMemory{}
	}

	var sessID, agentID string
	if sess, err := resolveHookSession(ctx, d, in, len(neutral) > 0 && neutral[0]); err == nil && sess != nil {
		sessID, agentID = sess.ID, sess.AgentID
	}

	injStore := d.db.Injections()
	suppressed := injection.Suppressed(ctx, injStore, injection.KindObservation, proj, time.Now())

	var kept []memory.SearchResult
	for _, h := range hits {
		if h.Observation.Type != memory.TypeCorrection && h.Observation.Type != memory.TypeConvention {
			continue
		}
		if h.Score < promptMemoryMinScore {
			continue
		}
		if _, dup := suppressed[h.Observation.ID]; dup {
			continue
		}
		kept = append(kept, h)
		if len(kept) >= preToolMemoryMaxHits {
			break
		}
	}
	if len(kept) == 0 {
		return preToolMemory{}
	}

	refs := make([]injection.Ref, 0, len(kept))
	for _, h := range kept {
		refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
	}
	_ = injection.NewLogger(injStore, nil).
		Log(ctx, injection.ChannelPreTool, agentID, proj, sessID, refs)
	_ = d.mem.RecordSurfaced(ctx, refIDs(refs))
	return preToolMemory{Path: path, Hits: kept}
}

// pathQueryTokens turns the file an edit is about to write into a search
// query: its path relative to the project root, last few segments split
// on separators and punctuation, lowercased, with the extension and
// generic layout directories dropped. "/Users/x/Code/repo/internal/
// storage/sessions.go" under root "/Users/x/Code/repo" → "storage
// sessions". The FTS layer ORs the tokens, so any segment matching a
// correction's trigger context or content surfaces it.
//
// It used to keep the project directory and the extension, so the query
// for any wayframer PHP file contained "wayframer" and "php" and matched
// every memory mentioning either: "use DB_DATABASE=wayframer" fired on
// controllers, models and markdown notes alike.
func pathQueryTokens(p, root string) string {
	if root != "" && filepath.IsAbs(p) {
		if rel, err := filepath.Rel(root, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			p = rel
		}
	}
	segs := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(segs) == 0 {
		return ""
	}
	if len(segs) > 4 {
		segs = segs[len(segs)-4:]
	}
	last := len(segs) - 1
	segs[last] = strings.TrimSuffix(segs[last], filepath.Ext(segs[last]))
	// Layout directories say where a file lives in any project of its
	// kind, not what it is about.
	generic := map[string]bool{
		"src": true, "internal": true, "cmd": true, "pkg": true, "lib": true, "app": true,
		"resources": true, "js": true, "views": true, "components": true,
		"composables": true, "public": true, "assets": true,
	}
	var toks []string
	for _, s := range segs {
		for _, t := range strings.FieldsFunc(s, func(r rune) bool {
			return r == '.' || r == '_' || r == '-'
		}) {
			t = strings.ToLower(t)
			if len(t) > 1 && !generic[t] {
				toks = append(toks, t)
			}
		}
	}
	return strings.Join(toks, " ")
}

// decidePreTool is the pure-function core of the PreToolUse guardrail.
// Returns the stderr message to emit and whether the harness should be
// told to block. Extracted so tests can assert both paths without
// spawning the binary just to observe an exit status.
func decidePreTool(in hookInput) (string, bool) {
	return decidePreToolFor(in, false)
}

// decidePreToolFor is decidePreTool with control over how the tool name is
// matched.
//
// strict matches only the exact Claude Code names, which is correct on
// that path because the installed PreToolUse matcher already narrowed the
// dispatch to mnemos' write tools. It is also the safe default: widening a
// guardrail's match is a safety-relevant change and should not happen
// incidentally.
//
// allowPrefixed additionally accepts any `<something>_<tool>` spelling.
// A neutral-format caller asks for this because it has no matcher: pi's
// MCP adapter derives tool names from the configured server key, the
// prefix mode, and — through a package manifest — the package name, so
// the Claude literal is only one of several spellings the same tool can
// arrive under. Without this the guardrail silently stops scanning every
// pi write, which is the failure this whole change exists to prevent.
func decidePreToolFor(in hookInput, allowPrefixed bool) (string, bool) {
	if !matchesMnemosWriteTool(in.ToolName, allowPrefixed) {
		return "", false
	}
	text := gatherStringsFromToolInput(in.ToolInput)
	if text == "" {
		return "", false
	}
	report := safety.NewScanner().Scan(text)
	if report.MaxRisk < safety.RiskHigh {
		return "", false
	}
	rules := uniqueRuleNames(report.Findings)
	return fmt.Sprintf(
		"mnemos: blocked %s — detected prompt-injection pattern (risk=%s; rules: %s). Remove the flagged text or rephrase before retrying.",
		shortToolName(in.ToolName), report.MaxRisk.String(), strings.Join(rules, ", "),
	), true
}

// mnemosWriteToolNames are mnemos' write tools without any prefix. The
// bare names are the matching unit for the prefixed path below, and the
// Claude-literal table is derived from them so the two spellings cannot
// drift apart.
var mnemosWriteToolNames = []string{"mnemos_save", "mnemos_correct", "mnemos_convention", "mnemos_checkpoint"}

// isMnemosWriteTool matches the MCP-namespaced names Claude Code assigns
// to our write tools. The PreToolUse matcher we install already narrows
// Claude's invocation, but the defensive check keeps this command safe
// to invoke from elsewhere (tests, future shared guardrails).
func isMnemosWriteTool(name string) bool { return matchesMnemosWriteTool(name, false) }

// matchesMnemosWriteTool reports whether a tool name identifies one of
// mnemos' write tools. With allowPrefixed it accepts any
// `<prefix>_<tool>` form, which is what a harness without a dispatch
// matcher needs; otherwise only the exact Claude Code spellings match.
func matchesMnemosWriteTool(name string, allowPrefixed bool) bool {
	for _, base := range mnemosWriteToolNames {
		if name == "mcp__mnemos__"+base {
			return true
		}
		if allowPrefixed && hasToolSuffix(name, base) {
			return true
		}
	}
	return false
}

// hasToolSuffix reports whether name ends in `_base` (case-insensitive),
// or is base itself.
//
// The cost of matching on a suffix is that a foreign server exposing the
// same trailing name — `other_mnemos_save` — is also treated as a mnemos
// write. That is accepted deliberately rather than overlooked: the two
// failure modes are not symmetric. Over-matching scans, and possibly
// refuses, a foreign write whose content already tripped a high-risk
// injection pattern; under-matching stops scanning every real mnemos
// write. The scanner only blocks on high-risk patterns, so the practical
// cost is a mnemos-flavoured reason on a refused foreign write. See the
// change's design.md, D6.
func hasToolSuffix(name, base string) bool {
	lower := strings.ToLower(name)
	base = strings.ToLower(base)
	return lower == base || strings.HasSuffix(lower, "_"+base)
}

// shortToolName strips any harness prefix for user-facing messages, so a
// reason reads "blocked mnemos_save" whatever the adapter named the tool.
func shortToolName(name string) string {
	lower := strings.ToLower(name)
	for _, base := range mnemosWriteToolNames {
		if hasToolSuffix(lower, base) {
			return base
		}
	}
	return name
}

// gatherStringsFromToolInput flattens every string value in the tool_input
// map (including strings inside list values) into one scannable blob. The
// scanner looks for structural patterns, not natural language, so which
// field a match came from does not matter — we want total coverage and
// zero need to update this when the MCP tool schema gains a field.
func gatherStringsFromToolInput(m map[string]any) string {
	if m == nil {
		return ""
	}
	var out []string
	// Stable order for deterministic tests.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		walkStrings(m[k], &out)
	}
	return strings.Join(out, "\n")
}

// walkStrings appends every string found in v (a scalar, a slice, or a
// nested map) to dst. Non-string scalars are ignored.
func walkStrings(v any, dst *[]string) {
	switch x := v.(type) {
	case string:
		if x != "" {
			*dst = append(*dst, x)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, dst)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkStrings(x[k], dst)
		}
	}
}

// runHookPreCompact keeps legacy Claude hooks silent: recovery now reaches
// Claude through SessionStart(compact). Explicit neutral consumers may still
// request a recovery block as data, without pretending stderr was surfaced.
func runHookPreCompact(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("pre-compact")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !flags.neutral() {
		return nil
	}
	in := flags.resolve()
	block := compactionRecoveryBlock(ctx, in, "PreCompact", true)
	flags.write(hookResult{Context: block}, func() {
		if block != "" {
			fmt.Fprintln(os.Stderr, block)
		}
	})
	return nil
}

// runHookPostCompact mirrors the neutral-only recovery path above.
func runHookPostCompact(ctx context.Context, args []string) error {
	fs, flags := hookFlagSet("post-compact")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !flags.neutral() {
		return nil
	}
	in := flags.resolve()
	block := compactionRecoveryBlock(ctx, in, "PostCompact", true)
	flags.write(hookResult{Context: block}, func() {
		if block != "" {
			fmt.Fprintln(os.Stderr, block)
		}
	})
	return nil
}

// compactionRecoveryBlock composes a prewarm block in the compaction-
// recovery mode and returns it with a header naming the event. It returns
// "" when there is nothing to say. Failure is always silent at the
// caller: a hook must not fail loudly.
//
// It takes the already-resolved payload so the payload flag and stdin
// share one path, and it holds no writer so the neutral format can carry
// the same text as data.
func compactionRecoveryBlock(ctx context.Context, in hookInput, event string, neutral ...bool) string {
	d, err := loadDeps(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook "+strings.ToLower(event)+":", err)
		return ""
	}
	defer d.close()

	proj := projectFromHook(in)

	var sessID string
	if sess, err := resolveHookSession(ctx, d, in, len(neutral) > 0 && neutral[0]); err == nil && sess != nil {
		sessID = sess.ID
	} else if err != nil && !errors.Is(err, session.ErrNotFound) {
		fmt.Fprintln(os.Stderr, "mnemos hook "+strings.ToLower(event)+":", err)
	}

	pw := prewarm.NewService(prewarm.Config{
		Observations: d.db.Observations(),
		Sessions:     d.db.Sessions(),
		Skills:       d.db.Skills(),
		Touches:      d.db.Touches(),
		Rumination:   d.rum,
		Injections:   injection.NewLogger(d.db.Injections(), nil),
	})
	block, err := pw.Build(ctx, prewarm.Request{
		Mode:      prewarm.ModeCompactionRecovery,
		Project:   proj,
		SessionID: sessID,
	})
	if err != nil || block == nil || block.Text == "" {
		return ""
	}

	return fmt.Sprintf("[mnemos %s — recovery block]\n%s", event, block.Text)
}

// uniqueRuleNames deduplicates the rule names from the findings while
// keeping order stable for reproducible stderr messages.
func uniqueRuleNames(findings []safety.Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range findings {
		if seen[f.Rule] {
			continue
		}
		seen[f.Rule] = true
		out = append(out, f.Rule)
	}
	return out
}
