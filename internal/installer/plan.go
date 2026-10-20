package installer

import (
	"fmt"
	"path/filepath"
	"strings"
)

// TargetPlan records a read-only routing decision or a target-specific conflict.
// Execution of independent plans may continue when one plan has an error.
type TargetPlan struct {
	Target Target
	Err    error
}

// PlanTargets discovers integrations without writing files. A verified pi
// package owns its existing route; init must not add an adapter-global duplicate.
func PlanTargets() []TargetPlan {
	targets := DetectTargets()
	routes := piPackageRoutes()
	hasPi := false
	for _, target := range targets {
		hasPi = hasPi || target.Name == "pi"
	}
	// Project-local package registration is sufficient even when no global
	// pi agent directory has been created yet.
	if !hasPi && len(routes) > 0 {
		if target, ok := piTarget(); ok {
			targets = append(targets, target)
		}
	}
	plans := make([]TargetPlan, 0, len(targets))
	for _, target := range targets {
		plan := TargetPlan{Target: target}
		if target.Name == "pi" {
			switch {
			case len(routes) > 1:
				plan.Err = fmt.Errorf("pi: multiple validated package routes; reconcile registrations before init")
			case len(routes) == 1:
				// Even an identical command under another name is a second
				// adapter route. Never guess which existing entry to delete.
				cfg, err := readConfig(normalise(target))
				if err != nil {
					plan.Err = fmt.Errorf("pi: inspect global route: %w", err)
				} else if conflictingPiGlobal(asStringMap(cfg["mcpServers"]), target.Key) {
					plan.Err = fmt.Errorf("pi: package and global routes conflict; reconcile registrations before init")
				} else {
					plan.Target = routes[0].target
				}
			}
		}
		plans = append(plans, plan)
	}
	return plans
}

func conflictingPiGlobal(servers map[string]any, ownedKey string) bool {
	for key, raw := range servers {
		if key == ownedKey {
			return true
		}
		server, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		command := str(server["command"])
		standardName := strings.TrimSuffix(filepath.Base(command), ".exe") == "mnemos"
		args := asAnyList(server["args"])
		if (standardName || verifiedMnemosCommand(command)) && len(args) == 1 && args[0] == "serve" {
			return true
		}
	}
	return false
}
