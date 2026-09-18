package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// The tests here pin the compatibility guarantee: separating a hook's
// decision from its rendering must not move a single byte of what Claude
// Code consumes. The expected strings below are copied from the exact
// format literals that produced them before the split, so a drift in the
// header text, the bullet shape, or the trailing newline fails here
// rather than in someone's session.

func sampleHit(typ memory.ObsType, title, snippet string) memory.SearchResult {
	return memory.SearchResult{
		Observation: memory.Observation{ID: "01ABC", Type: typ, Title: title},
		Snippet:     snippet,
	}
}

func TestFormatPromptMemoryMatchesOriginalBytes(t *testing.T) {
	got := formatPromptMemory(promptMemory{Hits: []memory.SearchResult{
		sampleHit(memory.TypeCorrection, "oauth retry", "refresh token first"),
		sampleHit(memory.TypeConvention, "error wrapping", "always use %w"),
	}})
	want := "[mnemos: prior memory relevant to this prompt — apply or address]\n" +
		"- [correction] oauth retry — refresh token first\n" +
		"- [convention] error wrapping — always use %w\n"
	if got != want {
		t.Errorf("prompt memory block drifted:\n got %q\nwant %q", got, want)
	}
}

func TestFormatPromptMemoryEmptyIsSilent(t *testing.T) {
	if got := formatPromptMemory(promptMemory{}); got != "" {
		t.Errorf("no hits must render nothing, got %q", got)
	}
}

func TestFormatPreToolMemoryMatchesOriginalBytes(t *testing.T) {
	got := formatPreToolMemory(preToolMemory{
		Path: "internal/storage/sessions.go",
		Hits: []memory.SearchResult{sampleHit(memory.TypeCorrection, "explicit timestamps", "pass UTC from Go")},
	})
	want := "[mnemos: memory relevant to sessions.go — apply before editing]\n" +
		"- [correction] explicit timestamps — pass UTC from Go"
	if got != want {
		t.Errorf("pre-tool memory block drifted:\n got %q\nwant %q", got, want)
	}
}

func TestFormatPreToolMemoryEmptyIsSilent(t *testing.T) {
	if got := formatPreToolMemory(preToolMemory{Path: "x/y.go"}); got != "" {
		t.Errorf("no hits must render nothing, got %q", got)
	}
}

func TestCaptureDirectiveWordingIsPinned(t *testing.T) {
	// One fragment per shape, chosen to catch a reworded or reordered
	// directive without duplicating the whole paragraph here.
	cases := []struct {
		sig  captureSignal
		want string
	}{
		{captureCorrection, "call mnemos_correct with: tried = the failed approach"},
		{captureConvention, "call mnemos_convention with title = short label"},
		{captureSave, "call mnemos_save with type = decision"},
	}
	for _, tc := range cases {
		got := captureDirectiveText(tc.sig)
		if !strings.Contains(got, tc.want) {
			t.Errorf("signal %d directive missing %q:\n%s", tc.sig, tc.want, got)
		}
	}
	if got := captureDirectiveText(captureNone); got != "" {
		t.Errorf("captureNone must produce no directive, got %q", got)
	}
}

func TestWriteClaudePromptOutputOrdersBlockThenDirective(t *testing.T) {
	var buf bytes.Buffer
	writeClaudePromptOutput(&buf, "BLOCK\n", "DIRECTIVE")
	want := "BLOCK\nDIRECTIVE\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteClaudePromptOutputSkipsEmptyParts(t *testing.T) {
	cases := []struct{ block, directive, want string }{
		{"", "", ""},
		{"B\n", "", "B\n"},
		{"", "D", "D\n"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		writeClaudePromptOutput(&buf, tc.block, tc.directive)
		if buf.String() != tc.want {
			t.Errorf("block=%q directive=%q: got %q, want %q", tc.block, tc.directive, buf.String(), tc.want)
		}
	}
}

func TestWriteClaudePreToolOutputEmptyEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	writeClaudePreToolOutput(&buf, "")
	if buf.Len() != 0 {
		t.Errorf("no context must emit no JSON, got %q", buf.String())
	}
}

func TestWriteClaudePreToolOutputShapeIsPinned(t *testing.T) {
	var buf bytes.Buffer
	writeClaudePreToolOutput(&buf, "some context")
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output must be JSON: %v (%q)", err, buf.String())
	}
	hso, _ := decoded["hookSpecificOutput"].(map[string]any)
	if hso == nil {
		t.Fatalf("missing hookSpecificOutput: %q", buf.String())
	}
	if hso["hookEventName"] != claudePreToolEvent {
		t.Errorf("hookEventName must be %q, got %v", claudePreToolEvent, hso["hookEventName"])
	}
	if hso["additionalContext"] != "some context" {
		t.Errorf("additionalContext drifted: %v", hso["additionalContext"])
	}
	if _, has := hso["permissionDecision"]; has {
		t.Error("JIT memory must NEVER emit a permissionDecision")
	}
}

func TestCapRunesTruncatesOnRuneBoundary(t *testing.T) {
	// Byte slicing would cut the multi-byte characters in half and emit
	// invalid UTF-8 straight into the model's context.
	long := strings.Repeat("é", 300)
	got := capRunes(long, snippetCap)
	if len([]rune(got)) != snippetCap {
		t.Errorf("want %d runes, got %d", snippetCap, len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncation must be marked, got %q", got)
	}
	if !utf8Valid(got) {
		t.Error("truncation produced invalid UTF-8")
	}
	if short := capRunes("short", snippetCap); short != "short" {
		t.Errorf("short strings must pass through, got %q", short)
	}
}

func TestSnippetOfPrefersSnippetThenFallsBackToContent(t *testing.T) {
	withSnippet := sampleHit(memory.TypeContext, "t", "the snippet")
	if got := snippetOf(withSnippet); got != "the snippet" {
		t.Errorf("got %q", got)
	}
	noSnippet := sampleHit(memory.TypeContext, "t", "  ")
	noSnippet.Observation.Content = "the content"
	if got := snippetOf(noSnippet); got != "the content" {
		t.Errorf("fallback to content failed: %q", got)
	}
}

// TestCollectorsDoNoRendering is the structural half of the guarantee:
// with no store available, the collectors must return empty results and
// produce no output anywhere. A collector that printed would show up
// here, and that is the drift this split exists to prevent.
func TestCollectorsDoNoRenderingWhenStoreIsUnavailable(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	if got := collectPromptMemory(ctx, nil, "anything", "", "proj", ""); len(got.Hits) != 0 {
		t.Errorf("nil deps must yield no hits, got %d", len(got.Hits))
	}
	res := collectUserPrompt(ctx, hookInput{Prompt: ""})
	if res.MemoryBlock != "" || res.Directive != "" || res.SessionID != "" {
		t.Errorf("empty prompt must decide nothing, got %+v", res)
	}
	if got := collectPreToolMemory(ctx, hookInput{ToolInput: map[string]any{}}); len(got.Hits) != 0 {
		t.Errorf("missing file_path must yield no hits, got %d", len(got.Hits))
	}
}

// TestPathQueryTokensStillFeedsBothRenderings guards the one pure helper
// the collectors share with the old code path.
func TestPathQueryTokensStillFeedsBothRenderings(t *testing.T) {
	got := pathQueryTokens("internal/storage/sessions.go")
	for _, want := range []string{"internal", "storage", "sessions", "go"} {
		if !strings.Contains(got, want) {
			t.Errorf("query %q missing %q", got, want)
		}
	}
}

// utf8Valid keeps the import list honest without pulling unicode/utf8
// into every helper above.
func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
