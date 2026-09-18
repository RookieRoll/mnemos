package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// This file separates the *decision* half of a hook from its *rendering*
// half.
//
// A hook subcommand has always done three things in one place: read the
// payload, decide what to surface or block, and print bytes in whatever
// shape the calling harness expects. The first two are harness-neutral;
// only the third is not. Splitting them is what lets a second harness
// consume the same decisions without a parallel implementation that would
// drift — the capture patterns, the relevance floor, the suppression
// window, and the safety threshold are all test-covered behaviour and stay
// in exactly one place.
//
// The collectors below own the decisions and their side effects (the
// injection log write and the surfaced counter). The formatters turn a
// decision into text without touching the store. The Claude writers turn
// that text into the bytes Claude Code consumes today, byte for byte.
//
// Renderers never write to the store. That is what keeps "the injection
// log records a surfaced set exactly once per invocation" true by
// construction rather than by convention: the collector runs once, and
// formatting it any number of ways costs nothing.

// promptMemory is the set of memories the prompt hook decided to surface.
// Empty Hits means nothing cleared the relevance floor, everything was
// already suppressed, or the store had no match — all of which render as
// no output.
type promptMemory struct {
	Hits []memory.SearchResult
}

// preToolMemory is the set of memories the pre-tool hook decided to
// surface for the file about to be edited. Path is kept alongside the
// hits because the header names the file, and a formatter must not need
// the original payload to produce that.
type preToolMemory struct {
	Path string
	Hits []memory.SearchResult
}

// promptMemoryHeader opens the prompt-time memory block. Claude Code
// concatenates a UserPromptSubmit hook's stdout straight into context,
// so the block has to be self-describing and say what to do with it.
const promptMemoryHeader = "[mnemos: prior memory relevant to this prompt — apply or address]"

// snippetCap bounds one memory's contribution to an injected block. The
// cap keeps the push invisible on routine prompts: a three-hit block is
// meant to be read, not skimmed past.
const snippetCap = 240

// snippetOf returns the text a block shows for one hit, preferring the
// search snippet and falling back to the observation content. Truncation
// keeps whole characters by slicing on a rune boundary.
func snippetOf(h memory.SearchResult) string {
	s := strings.TrimSpace(h.Snippet)
	if s == "" {
		s = strings.TrimSpace(h.Observation.Content)
	}
	return capRunes(s, snippetCap)
}

// capRunes truncates s to at most n runes, appending an ellipsis when it
// had to cut. Slicing bytes here would split a multi-byte character and
// emit invalid UTF-8 into the agent's context.
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// formatPromptMemory renders the prompt-time memory block, or "" when
// there is nothing to surface. Byte-identical to what the hook printed
// before the decision/rendering split.
func formatPromptMemory(m promptMemory) string {
	if len(m.Hits) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintln(&b, promptMemoryHeader)
	for _, h := range m.Hits {
		title := h.Observation.Title
		if title == "" {
			title = h.Observation.ID
		}
		fmt.Fprintf(&b, "- [%s] %s — %s\n", h.Observation.Type, title, snippetOf(h))
	}
	return b.String()
}

// formatPreToolMemory renders the pre-tool memory block, or "" when
// there is nothing to surface. The header names the file because the
// memory arrives at the decision point rather than at session start, so
// the model needs to know what it is about to touch.
func formatPreToolMemory(m preToolMemory) string {
	if len(m.Hits) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[mnemos: memory relevant to %s — apply before editing]\n", filepath.Base(m.Path))
	for _, h := range m.Hits {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", h.Observation.Type, h.Observation.Title, snippetOf(h))
	}
	return strings.TrimRight(b.String(), "\n")
}

// captureDirectiveText returns the directive for a detected capture
// shape, or "" for captureNone.
//
// We do NOT call the tool ourselves — the agent owns the structured
// arguments (tried/wrong_because/fix), and we want the agent to learn the
// pattern, not be replaced by it. The directive raises capture from
// "optional" to "non-optional in this turn."
func captureDirectiveText(sig captureSignal) string {
	switch sig {
	case captureCorrection:
		return "[mnemos: capture required] The user's prompt contains correction-shaped phrasing (\"we tried X\", \"going forward\", \"don't do Y\", or similar). BEFORE answering the rest of the prompt, call mnemos_correct with: tried = the failed approach, wrong_because = why it failed, fix = what to do instead, trigger_context = a phrase that should resurface this in future. Do this even if it interrupts the task — the next session will repeat the mistake otherwise."
	case captureConvention:
		return "[mnemos: capture required] The user's prompt declares a project convention (\"we always X\", \"the rule here is Y\", or similar). BEFORE answering the rest of the prompt, call mnemos_convention with title = short label, rule = the rule itself, rationale = why. The next agent on this project should not have to be told this twice."
	case captureSave:
		return "[mnemos: capture required] The user explicitly asked you to remember or save something, OR narrated an architectural decision (\"we just decided X\", \"going with Y\"). BEFORE answering the rest of the prompt, search mnemos first; if not already recorded, call mnemos_save with type = decision (or convention if it's a project rule), title = short label, content = the substance."
	}
	return ""
}

// claudePreToolEvent is the hook event name Claude Code expects inside a
// PreToolUse payload.
const claudePreToolEvent = "PreToolUse"

// writeClaudePromptOutput renders the prompt hook's stdout for Claude
// Code: the memory block first, then the capture directive, in that
// order. Claude Code concatenates this into the model's context.
func writeClaudePromptOutput(w io.Writer, memoryBlock, directive string) {
	if memoryBlock != "" {
		fmt.Fprint(w, memoryBlock)
	}
	if directive != "" {
		fmt.Fprintln(w, directive)
	}
}

// writeClaudePreToolOutput renders the pre-tool hook's stdout for Claude
// Code as hookSpecificOutput JSON carrying additionalContext.
//
// No permissionDecision is ever emitted — this surface must never
// auto-approve or block a write, only inform it. Blocking is decided
// separately by the guardrail and signalled by exit status.
func writeClaudePreToolOutput(w io.Writer, context string) {
	if context == "" {
		return
	}
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     claudePreToolEvent,
			"additionalContext": context,
		},
	}
	_ = json.NewEncoder(w).Encode(out)
}

// ---------------------------------------------------------------------
// Neutral hook contract: payload in, result out.
//
// A harness other than Claude Code has no way to pipe a payload to a hook
// subcommand, so the payload can also arrive as an argument. And it has no
// use for Claude's stdout shapes, so a second format reports the outcome
// as data. Both are additive: with neither flag present, every hook
// behaves exactly as before.
// ---------------------------------------------------------------------

// neutralFormat is the value that selects the transport-neutral result.
const neutralFormat = "json"

// hookResult is the transport-neutral outcome of one hook invocation.
//
// It is deliberately narrow. Every harness that consumes it needs the same
// four things and nothing more: what to inject into context, whether the
// triggering action should be refused, why, and which mnemos session the
// invocation was scoped to. Anything richer would be an internal contract
// leaking across a process boundary that other tools will depend on.
type hookResult struct {
	SessionID   string `json:"session_id,omitempty"`
	Context     string `json:"context,omitempty"`
	Block       bool   `json:"block"`
	BlockReason string `json:"block_reason,omitempty"`
}

// hookFlags holds the two flags every hook subcommand accepts.
type hookFlags struct {
	payload *string
	format  *string
}

// newHookFlags registers the neutral transport flags on fs. Callers pass
// the subcommand's args so a hook can be invoked as
//
//	mnemos hook user-prompt --payload '{"prompt":"..."}' --format json
//
// without disturbing stdin for the Claude Code path.
func newHookFlags(fs *flag.FlagSet) hookFlags {
	return hookFlags{
		payload: fs.String("payload", "", "hook payload as JSON (overrides stdin)"),
		format:  fs.String("format", "", "output format: text (default, Claude Code) | json (neutral)"),
	}
}

// neutral reports whether the result format was selected.
func (f hookFlags) neutral() bool { return *f.format == neutralFormat }

// resolve applies the payload flag, falling back to stdin. An unusable
// argument yields the zero payload rather than an error: a hook must
// never fail the turn it runs inside, and the contract says a malformed
// argument produces no context and no block.
func (f hookFlags) resolve() hookInput { return resolveHookPayload(*f.payload) }

// write emits the result in the selected format. The neutral format is a
// single JSON object on stdout; the default format is whatever the caller
// decides, since only the caller knows which harness is listening.
func (f hookFlags) write(res hookResult, claude func()) {
	if !f.neutral() {
		claude()
		return
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "mnemos hook: encode result:", err)
	}
}

// writeBlocked emits a guardrail decision.
//
// The reason is identical in both formats by construction — it is the
// string passed in — but the exit status deliberately is not, because
// only one of the two consumers can read it. A harness that parses the
// envelope already has the decision as data and would be forced to invent
// a meaning for a non-zero status it did not ask for; Claude Code has no
// other channel besides stderr plus exit 2. The neutral path therefore
// keeps a reason on stderr as well, so a human debugging from a terminal
// still sees why a call was refused.
func (f hookFlags) writeBlocked(reason string) {
	if !f.neutral() {
		fmt.Fprintln(os.Stderr, reason)
		os.Exit(2)
	}
	if reason != "" {
		fmt.Fprintln(os.Stderr, reason)
	}
	f.write(hookResult{Block: true, BlockReason: reason}, func() {})
}

// parseHookPayload decodes a payload argument. Errors are swallowed on
// purpose for the same reason readHookStdin swallows them: the CLI also
// runs interactively, and a hook must not fail loudly.
func parseHookPayload(raw string) hookInput {
	var in hookInput
	_ = json.Unmarshal([]byte(raw), &in)
	return in
}

// resolveHookPayload reads the payload from the argument when it is
// present and usable, and from stdin otherwise. Shared by the hook
// subcommands and by prewarm so both accept a payload the same way.
func resolveHookPayload(raw string) hookInput {
	if s := strings.TrimSpace(raw); s != "" {
		return parseHookPayload(s)
	}
	return readHookStdin(os.Stdin)
}
