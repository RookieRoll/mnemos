package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

func TestOwnedHookRemovalPreservesSiblingsAndMatcherlessMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	user := map[string]any{"type": "command", "command": "user-hook", "timeout": float64(20)}
	cfg := map[string]any{"outside": true, "hooks": map[string]any{"PreCompact": []any{
		map[string]any{"keep": "sibling-group", "hooks": []any{map[string]any{"type": "command", "command": "mnemos hook pre-compact"}, user}},
		map[string]any{"keep": "empty-group", "hooks": []any{map[string]any{"type": "command", "command": "mnemos hook pre-compact"}}},
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "mnemos hook pre-compact"}}},
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "other-exe hook pre-compact"}}},
	}}}
	writePlanJSON(t, path, cfg)
	entry := installer.HookEntry{Event: "PreCompact", Command: "mnemos hook pre-compact"}
	changed, err := installer.UninstallHook(path, entry)
	if err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	data, _ := os.ReadFile(path)
	var actual map[string]any
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatal(err)
	}
	groups := actual["hooks"].(map[string]any)["PreCompact"].([]any)
	if len(groups) != 3 || actual["outside"] != true {
		t.Fatalf("unexpected groups: %s", data)
	}
	first := groups[0].(map[string]any)
	if first["keep"] != "sibling-group" || !reflect.DeepEqual(first["hooks"], []any{user}) {
		t.Fatalf("sibling changed: %s", data)
	}
	second := groups[1].(map[string]any)
	if second["keep"] != "empty-group" || len(second["hooks"].([]any)) != 0 {
		t.Fatalf("matcherless metadata lost: %s", data)
	}
	changed, err = installer.UninstallHook(path, entry)
	if err != nil || changed {
		t.Fatalf("repeat: %v %v", changed, err)
	}
}
