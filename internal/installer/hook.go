package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HookEntry describes a Claude Code hook to wire into settings.json. One
// entry per (event, matcher, command) tuple. Event defaults to SessionStart
// for backward compatibility with the original single-hook API.
type HookEntry struct {
	Event   string // "SessionStart" | "UserPromptSubmit" | "Stop" | "PostToolUse" | ...
	Matcher string // event-specific: SessionStart source, PostToolUse tool_name; may be empty
	Command string // full command line, e.g. "/usr/local/bin/mnemos prewarm"
	Timeout int    // seconds; zero means omit the field
}

// eventName returns the Claude Code event slot Entry belongs under. Empty
// Event defaults to SessionStart to preserve the legacy behavior of the
// original single-hook API.
func (e HookEntry) eventName() string {
	if e.Event == "" {
		return "SessionStart"
	}
	return e.Event
}

// ClaudeSettingsPath returns the path to the Claude Code user-scope
// settings.json. An explicit client path overrides ~/.claude/settings.json.
func ClaudeSettingsPath(options ...Options) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(claudeSettingsDir(home, clientOptions(options)), "settings.json")
}

func claudeSettingsDir(home string, options Options) string {
	if options.ClaudeConfigDir != "" {
		return options.ClaudeConfigDir
	}
	return filepath.Join(home, ".claude")
}

// InstallHook adds entry to settings.json under the event slot (default
// SessionStart). It is idempotent: if an entry with the same (event,
// matcher, command) already exists, it returns changed=false. Other hooks
// (for other events or other commands) are preserved untouched.
func InstallHook(path string, entry HookEntry) (bool, error) {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	cfg, err := readSettings(path)
	if err != nil {
		return false, err
	}

	event := entry.eventName()
	hooks := asStringMap(cfg["hooks"])
	groups := asAnyList(hooks[event])
	desired := hookGroupFor(entry)

	if groupIndex(groups, entry) >= 0 {
		return false, nil
	}
	groups = append(groups, desired)
	hooks[event] = groups
	cfg["hooks"] = hooks

	data, err := encodeJSON(cfg)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, data)
}

// UninstallHook removes only matching command hooks under entry's event/matcher.
// Sibling hooks and unrelated group fields survive owned integration upgrades.
// Returns changed=true if the file was rewritten.
func UninstallHook(path string, entry HookEntry) (bool, error) {
	cfg, err := readSettings(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	event := entry.eventName()
	hooks := asStringMap(cfg["hooks"])
	groups := asAnyList(hooks[event])

	changed := false
	keptGroups := make([]any, 0, len(groups))
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok || str(group["matcher"]) != entry.Matcher {
			keptGroups = append(keptGroups, raw)
			continue
		}
		inner := asAnyList(group["hooks"])
		kept := make([]any, 0, len(inner))
		removed := false
		for _, hook := range inner {
			h, ok := hook.(map[string]any)
			if ok && str(h["command"]) == entry.Command && (str(h["type"]) == "command" || str(h["type"]) == "") {
				changed, removed = true, true
			} else {
				kept = append(kept, hook)
			}
		}
		if !removed {
			keptGroups = append(keptGroups, raw)
			continue
		}
		group["hooks"] = kept
		// A plain owned group disappears. Custom group metadata is retained
		// even if its last owned hook was removed, rather than silently lost.
		hasExtra := false
		for key := range group {
			hasExtra = hasExtra || (key != "hooks" && key != "matcher")
		}
		if len(kept) > 0 || hasExtra {
			keptGroups = append(keptGroups, group)
		}
	}
	if !changed {
		return false, nil
	}
	groups = keptGroups
	if len(groups) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = groups
	}
	if len(hooks) == 0 {
		delete(cfg, "hooks")
	} else {
		cfg["hooks"] = hooks
	}

	data, err := encodeJSON(cfg)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(path, data)
}

// UninstallHookVariants removes selector variants of the owned hook command.
// Older installations could inherit their configuration route from process
// environment, while file-based installations carry --config in argv. During
// init, remove all forms of the same executable and hook arguments before
// installing the selected route.
func UninstallHookVariants(path string, entry HookEntry, executable string) (bool, error) {
	cfg, err := readSettings(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	wanted, ok := hookCommandIdentity(entry.Command, executable)
	if !ok {
		return false, fmt.Errorf("installer: parse owned hook command %q", entry.Command)
	}
	hooks := asStringMap(cfg["hooks"])
	groups := asAnyList(hooks[entry.eventName()])
	var variants []HookEntry
	seen := make(map[string]bool)
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok || str(group["matcher"]) != entry.Matcher {
			continue
		}
		for _, rawHook := range asAnyList(group["hooks"]) {
			hook, ok := rawHook.(map[string]any)
			if !ok || (str(hook["type"]) != "command" && str(hook["type"]) != "") {
				continue
			}
			command := str(hook["command"])
			if command == entry.Command {
				continue
			}
			identity, valid := hookCommandIdentity(command, executable)
			if !valid || !equalCommandIdentity(identity, wanted) || seen[command] {
				continue
			}
			seen[command] = true
			variants = append(variants, HookEntry{Event: entry.Event, Matcher: entry.Matcher, Command: command})
		}
	}
	changed := false
	for _, variant := range variants {
		removed, err := UninstallHook(path, variant)
		if err != nil {
			return changed, err
		}
		changed = changed || removed
	}
	return changed, nil
}

func hookCommandIdentity(command, executable string) ([]string, bool) {
	words, ok := commandWords(command)
	if !ok || len(words) == 0 {
		return nil, false
	}
	exe := filepath.Clean(words[0])
	if !samePath(exe, filepath.Clean(executable)) {
		return nil, false
	}
	args := make([]string, 0, len(words)-1)
	for i := 1; i < len(words); i++ {
		arg := words[i]
		switch {
		case arg == "--config":
			if i+1 >= len(words) || words[i+1] == "" {
				return nil, false
			}
			i++
		case strings.HasPrefix(arg, "--config="):
			if strings.TrimPrefix(arg, "--config=") == "" {
				return nil, false
			}
		default:
			args = append(args, arg)
		}
	}
	return args, true
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func equalCommandIdentity(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// commandWords splits the quoted argv forms emitted by mnemos init. It
// accepts single- and double-quoted words and backslash escapes inside them.
func commandWords(command string) ([]string, bool) {
	return commandWordsForOS(command, runtime.GOOS == "windows")
}

func commandWordsForOS(command string, windows bool) ([]string, bool) {
	var words []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			word.WriteRune(r)
			escaped, started = false, true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(runes) && runes[i+1] == '"' {
				word.WriteRune(runes[i+1])
				i++
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		switch {
		case r == '"' || r == '\'':
			quote, started = r, true
		case r == '\\' && !windows:
			escaped, started = true, true
		case r == ' ' || r == '\t' || r == '\r' || r == '\n':
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	if started {
		words = append(words, word.String())
	}
	return words, len(words) > 0
}

// IsHookInstalled reports whether settings.json has a hook group under the
// event slot matching entry.
func IsHookInstalled(path string, entry HookEntry) bool {
	cfg, err := readSettings(path)
	if err != nil {
		return false
	}
	hooks := asStringMap(cfg["hooks"])
	return groupIndex(asAnyList(hooks[entry.eventName()]), entry) >= 0
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func encodeJSON(cfg map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	return append(data, '\n'), nil
}

func asAnyList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// hookGroupFor builds the JSON shape a single matcher-group takes in
// settings.json: { "matcher": "...", "hooks": [ {"type": "command", ...} ] }.
func hookGroupFor(entry HookEntry) map[string]any {
	cmd := map[string]any{
		"type":    "command",
		"command": entry.Command,
	}
	if entry.Timeout > 0 {
		cmd["timeout"] = entry.Timeout
	}
	group := map[string]any{"hooks": []any{cmd}}
	if entry.Matcher != "" {
		group["matcher"] = entry.Matcher
	}
	return group
}

// groupIndex returns the index of the first hook group whose matcher and
// embedded command match entry, or -1 if none. We dedupe on (matcher, command)
// so a user reinstalling with a new binary path replaces the old entry.
func groupIndex(list []any, entry HookEntry) int {
	for i, raw := range list {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if str(group["matcher"]) != entry.Matcher {
			continue
		}
		inner := asAnyList(group["hooks"])
		for _, r := range inner {
			h, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if str(h["command"]) == entry.Command {
				return i
			}
		}
	}
	return -1
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
