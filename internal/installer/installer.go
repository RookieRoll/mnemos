// Package installer wires Mnemos into agent clients (Claude Code, Claude
// Desktop, Cursor, Windsurf, Codex CLI, pi) by editing their MCP config
// files idempotently. It also powers `mnemos doctor` for self-diagnosis.
package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Format identifies the on-disk encoding of a target's config file.
const (
	FormatJSON = "json"
	FormatTOML = "toml"
)

// Target describes an MCP client config location and how to patch it. The
// Mnemos entry is keyed under Group[Key] inside the file. We never rewrite
// unrelated keys.
type Target struct {
	Name   string // human-readable (Claude Code, Cursor, ...)
	Path   string // absolute path to the config file
	Group  string // parent map key (default: "mcpServers")
	Key    string // server key under Group (default: "mnemos")
	Format string // "json" or "toml" (default: "json")
}

// ServerEntry is the value we write under Group[Key].
type ServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// piAgentDirEnv overrides the pi agent directory, mirroring how
// CLAUDE_CONFIG_DIR relocates Claude Code's. pi itself resolves its agent
// dir from a package manifest's `piConfig.configDir` plus an uppercased
// app-name env var, and only the host knows its own app name, so mnemos
// honours the documented generic override and the default path rather
// than reimplementing that rebranding logic.
const piAgentDirEnv = "PI_CODING_AGENT_DIR"

// piTarget describes pi's MCP config location. pi reads MCP servers from
// several files with a defined precedence; the agent-dir one is the
// Pi-owned global override, which makes it the right target for a tool
// that must not rewrite a user's shared or project config.
//
// piMCPConfigPath returns the path to pi's agent-dir MCP config.
func piMCPConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if d := os.Getenv(piAgentDirEnv); d != "" {
		return filepath.Join(d, "mcp.json")
	}
	return filepath.Join(home, ".pi", "agent", "mcp.json")
}

// piTarget returns pi's MCP config target. The server key stays "mnemos":
// the tool names pi ends up exposing do not depend on choosing a key that
// spells a particular prefix, because the pi-side guardrail matches on the
// tool-name suffix. See the change's design.md, D6.
func piTarget() (Target, bool) {
	p := piMCPConfigPath()
	if p == "" {
		return Target{}, false
	}
	return Target{Name: "pi", Path: p, Key: "mnemos"}, true
}

// DetectTargets returns the MCP client config files that exist for the
// current user. Non-existent files are still returned if their parent
// directory is present, so `init` can create them idempotently.
func DetectTargets() []Target {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	candidates := []Target{
		{Name: "Claude Code (user)", Path: filepath.Join(claudeConfigDir(home), ".claude.json"), Key: "mnemos"},
		{Name: "Cursor", Path: filepath.Join(home, ".cursor", "mcp.json"), Key: "mnemos"},
		{Name: "Windsurf", Path: filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), Key: "mnemos"},
		{Name: "OpenAI Codex CLI", Path: filepath.Join(home, ".codex", "config.toml"), Group: "mcp_servers", Key: "mnemos", Format: FormatTOML},
	}
	if t, ok := piTarget(); ok {
		candidates = append(candidates, t)
	}
	if p := claudeDesktopPath(); p != "" {
		candidates = append(candidates, Target{Name: "Claude Desktop", Path: p, Key: "mnemos"})
	}
	out := make([]Target, 0, len(candidates))
	for _, c := range candidates {
		// Include the target if the file or its parent directory exists.
		// Parent existence means the client is installed; we can create the
		// file if needed.
		if _, err := os.Stat(c.Path); err == nil {
			out = append(out, c)
			continue
		}
		if _, err := os.Stat(filepath.Dir(c.Path)); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// claudeConfigDir returns the directory that hosts Claude Code's user-scope
// config (the one containing .claude.json). Claude Code honours the
// CLAUDE_CONFIG_DIR env var to relocate this dir; when unset, it falls back
// to $HOME. The installer must mirror that, otherwise mnemos gets wired into
// ~/.claude.json while the running agent reads from $CLAUDE_CONFIG_DIR
// and never sees the entry.
func claudeConfigDir(home string) string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return home
}

// claudeDesktopPath returns the per-OS Claude Desktop config path, or "" if
// the platform doesn't ship Claude Desktop.
func claudeDesktopPath() string {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Claude", "claude_desktop_config.json")
		}
	}
	// No official Claude Desktop on Linux. Skip.
	return ""
}

// Install patches the given target, adding (or updating) the mnemos entry
// under Group. Unrelated keys are preserved. Returns true if the file
// was written (false if it was already up to date).
func Install(t Target, entry ServerEntry) (bool, error) {
	t = normalise(t)
	if err := ensureDir(filepath.Dir(t.Path)); err != nil {
		return false, err
	}
	cfg, err := readConfig(t)
	if err != nil {
		return false, err
	}

	servers := asStringMap(cfg[t.Group])
	desired := desiredEntry(entry)

	// Merge over the existing entry instead of replacing it. A pi user
	// routinely adds adapter-only keys to this entry — `directTools`,
	// `toolPrefix`, `lifecycle` — and a later `mnemos init` must not strip
	// them. Only the fields mnemos owns are overwritten; anything else the
	// client or the user put there is left in place.
	existing, _ := servers[t.Key].(map[string]any)
	merged := mergeEntry(existing, desired)
	if equalMaps(existing, merged) {
		return false, nil
	}
	servers[t.Key] = merged
	cfg[t.Group] = servers

	data, err := encodeConfig(t.Format, cfg)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(t.Path, data); err != nil {
		return false, err
	}
	return true, nil
}

// Uninstall removes the mnemos entry from the target. Returns true if the
// file was changed.
func Uninstall(t Target) (bool, error) {
	t = normalise(t)
	data, err := os.ReadFile(t.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", t.Path, err)
	}
	cfg, err := decodeConfig(t.Format, data)
	if err != nil {
		return false, err
	}
	servers, _ := cfg[t.Group].(map[string]any)
	if _, ok := servers[t.Key]; !ok {
		return false, nil
	}
	delete(servers, t.Key)
	cfg[t.Group] = servers
	out, err := encodeConfig(t.Format, cfg)
	if err != nil {
		return false, err
	}
	return true, writeAtomic(t.Path, out)
}

// IsInstalled reports whether the target already has a mnemos entry.
func IsInstalled(t Target) bool {
	t = normalise(t)
	data, err := os.ReadFile(t.Path)
	if err != nil {
		return false
	}
	cfg, err := decodeConfig(t.Format, data)
	if err != nil {
		return false
	}
	servers, _ := cfg[t.Group].(map[string]any)
	_, ok := servers[t.Key]
	return ok
}

func normalise(t Target) Target {
	if t.Group == "" {
		t.Group = "mcpServers"
	}
	if t.Key == "" {
		t.Key = "mnemos"
	}
	if t.Format == "" {
		t.Format = FormatJSON
	}
	return t
}

func readConfig(t Target) (map[string]any, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", t.Path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	return decodeConfig(t.Format, data)
}

func decodeConfig(format string, data []byte) (map[string]any, error) {
	cfg := map[string]any{}
	switch format {
	case FormatJSON:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse json: %w", err)
		}
	case FormatTOML:
		if _, err := toml.Decode(string(data), &cfg); err != nil {
			return nil, fmt.Errorf("parse toml: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
	return cfg, nil
}

func encodeConfig(format string, cfg map[string]any) ([]byte, error) {
	switch format {
	case FormatJSON:
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal json: %w", err)
		}
		return append(data, '\n'), nil
	case FormatTOML:
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
			return nil, fmt.Errorf("marshal toml: %w", err)
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown format: %s", format)
	}
}

func asStringMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func desiredEntry(entry ServerEntry) map[string]any {	out := map[string]any{"command": entry.Command}
	if len(entry.Args) > 0 {
		args := make([]any, len(entry.Args))
		for i, a := range entry.Args {
			args[i] = a
		}
		out["args"] = args
	}
	if len(entry.Env) > 0 {
		env := make(map[string]any, len(entry.Env))
		for k, v := range entry.Env {
			env[k] = v
		}
		out["env"] = env
	}
	return out
}

// mergeEntry overlays the keys mnemos owns onto whatever the entry
// already held, so client- or user-added keys survive an install. A nil
// existing entry yields exactly the desired entry.
func mergeEntry(existing, desired map[string]any) map[string]any {
	if len(existing) == 0 {
		return desired
	}
	out := make(map[string]any, len(existing)+len(desired))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range desired {
		out[k] = v
	}
	// A field mnemos owns but no longer needs must not linger: if the
	// previous install wrote args and this one does not, the stale args
	// would keep pointing at an argument list we no longer intend.
	for _, owned := range []string{"command", "args", "env"} {
		if _, keep := desired[owned]; !keep {
			delete(out, owned)
		}
	}
	return out
}

func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func equalMaps(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// PiPackageInstalled reports whether the mnemos pi package is registered
// with pi, and the settings file it was found in.
//
// pi package registration lives in pi's own settings, not in the MCP
// config, so this is a read-only observation rather than something mnemos
// writes: the package is installed with pi's own package command. Both pi
// settings scopes are checked, since a project-scope install is as valid
// as a global one.
func PiPackageInstalled() (bool, string) {
	candidates := []string{}
	if d := os.Getenv(piAgentDirEnv); d != "" {
		candidates = append(candidates, filepath.Join(d, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".pi", "agent", "settings.json"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, ".pi", "settings.json"))
	}
	for _, path := range candidates {
		if piSettingsReferenceMnemos(path) {
			return true, path
		}
	}
	return false, ""
}

// piSettingsReferenceMnemos reports whether a pi settings file lists a
// mnemos package. Matching is on the package source string containing
// "mnemos", which covers a local path, a git URL, and an npm name without
// pinning any of them.
func piSettingsReferenceMnemos(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var cfg struct {
		Packages []any `json:"packages"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	for _, entry := range cfg.Packages {
		switch v := entry.(type) {
		case string:
			if strings.Contains(v, "mnemos") {
				return true
			}
		case map[string]any:
			if s, ok := v["source"].(string); ok && strings.Contains(s, "mnemos") {
				return true
			}
		}
	}
	return false
}
