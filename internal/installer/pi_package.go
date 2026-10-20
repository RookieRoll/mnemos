package installer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// PiPackageMCPInstalled verifies a local pi package's manifest-backed mnemos
// server. Remote package names alone are insufficient evidence of installation.
// It is read-only; doctor must not create a duplicate adapter-global entry.
func PiPackageMCPInstalled() (bool, string) {
	routes := piPackageRoutes()
	if len(routes) > 0 {
		return true, routes[0].settings
	}
	return false, ""
}

type piPackageRoute struct {
	target   Target
	settings string
}

func piPackageRoutes() []piPackageRoute {
	var routes []piPackageRoute
	seen := make(map[string]bool)
	var settings []string
	if d := os.Getenv(piAgentDirEnv); d != "" {
		settings = append(settings, filepath.Join(d, "settings.json"))
	} else if home, err := os.UserHomeDir(); err == nil {
		settings = append(settings, filepath.Join(home, ".pi", "agent", "settings.json"))
	}
	if wd, err := os.Getwd(); err == nil {
		settings = append(settings, filepath.Join(wd, ".pi", "settings.json"))
	}
	for _, path := range settings {
		var cfg struct {
			Packages []json.RawMessage `json:"packages"`
		}
		if !readPiJSON(path, &cfg) {
			continue
		}
		for _, entry := range cfg.Packages {
			var source string
			if json.Unmarshal(entry, &source) != nil {
				var object struct {
					Source string `json:"source"`
				}
				if json.Unmarshal(entry, &object) != nil {
					continue
				}
				source = object.Source
			}
			// Match the adapter's local-path resolution. npm/git resolution is
			// intentionally not guessed, since registration does not prove files exist.
			if source == "" || strings.HasPrefix(source, "npm:") || strings.HasPrefix(source, "git:") || strings.Contains(source, "://") {
				continue
			}
			root := source
			if !filepath.IsAbs(root) {
				root = filepath.Join(filepath.Dir(path), root)
			}
			for _, target := range verifiedPiPackageTargets(root) {
				identity := target.Path + "\x00" + target.Key
				if !seen[identity] {
					seen[identity] = true
					routes = append(routes, piPackageRoute{target: target, settings: path})
				}
			}
		}
	}
	return routes
}

func readPiJSON(path string, value any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, value) == nil
}

func verifiedPiPackageTargets(root string) []Target {
	var targets []Target
	var manifest struct {
		Name string `json:"name"`
		Pi   struct {
			MCP json.RawMessage `json:"mcp"`
		} `json:"pi"`
	}
	if !readPiJSON(filepath.Join(root, "package.json"), &manifest) || manifest.Name == "" {
		return nil
	}
	var paths []string
	var single string
	if json.Unmarshal(manifest.Pi.MCP, &single) == nil {
		paths = []string{single}
	} else if json.Unmarshal(manifest.Pi.MCP, &paths) != nil {
		return nil
	}
	for _, path := range paths {
		if path == "" || filepath.IsAbs(path) {
			continue
		}
		joined := filepath.Join(root, path)
		// The adapter rejects both lexical and symlink escapes from a package.
		lexical, err := filepath.Rel(root, joined)
		if err != nil || lexical == ".." || strings.HasPrefix(lexical, ".."+string(filepath.Separator)) || filepath.IsAbs(lexical) {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		realPath, err := filepath.EvalSymlinks(joined)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(realRoot, realPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		var cfg struct {
			Servers map[string]struct {
				Command  string   `json:"command"`
				Args     []string `json:"args"`
				Disabled bool     `json:"disabled"`
			} `json:"mcpServers"`
		}
		if !readPiJSON(realPath, &cfg) {
			continue
		}
		keys := make([]string, 0, len(cfg.Servers))
		for key := range cfg.Servers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			server := cfg.Servers[key]
			if !server.Disabled && verifiedMnemosCommand(server.Command) && len(server.Args) == 1 && server.Args[0] == "serve" {
				targets = append(targets, Target{Name: "pi (local package)", Path: realPath, Key: key})
			}
		}
	}
	return targets
}

func verifiedMnemosCommand(command string) bool {
	resolved, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	if strings.TrimSuffix(filepath.Base(command), ".exe") == "mnemos" {
		return true
	}
	// init writes its own executable path. A locally renamed development
	// binary is still this process, and must remain recognizable on rerun.
	self, err := os.Executable()
	if err != nil {
		return false
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return false
	}
	commandInfo, err := os.Stat(resolved)
	return err == nil && os.SameFile(selfInfo, commandInfo)
}
