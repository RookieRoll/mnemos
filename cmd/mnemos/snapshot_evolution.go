package main

import (
	"fmt"
	"reflect"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// snapshotEvidence is a version-owned record, never a separately searchable memory.
type snapshotEvidence struct {
	ObservationID string                   `json:"observation_id"`
	Evidence      memory.EvidenceReference `json:"evidence"`
}

// prepareSnapshot validates ownership before any import writes and restores inline
// owner metadata for the domain fidelity operations. Old dumps remain valid.
func prepareSnapshot(snap *snapshot) error {
	if snap.SchemaVersion < 0 || snap.SchemaVersion > 2 {
		return fmt.Errorf("unsupported schema_version %d; import with a compatible newer binary", snap.SchemaVersion)
	}
	owners := make(map[string]int, len(snap.Observations))
	for i, o := range snap.Observations {
		if o.ID == "" {
			return fmt.Errorf("observation at index %d has no ID", i)
		}
		if _, exists := owners[o.ID]; exists {
			return fmt.Errorf("duplicate observation ID %s", o.ID)
		}
		refs, err := memory.NormalizeEvidence(o.Evidence, true)
		if err != nil {
			return fmt.Errorf("observation %s evidence: %w", o.ID, err)
		}
		snap.Observations[i].Evidence = refs
		owners[o.ID] = i
	}
	for _, row := range snap.Evidence {
		i, ok := owners[row.ObservationID]
		if !ok {
			return fmt.Errorf("orphan evidence for observation %s", row.ObservationID)
		}
		refs, err := memory.NormalizeEvidence([]memory.EvidenceReference{row.Evidence}, true)
		if err != nil {
			return fmt.Errorf("observation %s evidence: %w", row.ObservationID, err)
		}
		row.Evidence = refs[0]
		duplicate := false
		for _, existing := range snap.Observations[i].Evidence {
			if existing.Key == row.Evidence.Key {
				if !reflect.DeepEqual(existing, row.Evidence) {
					return fmt.Errorf("conflicting evidence key %s for observation %s", row.Evidence.Key, row.ObservationID)
				}
				duplicate = true
				break
			}
		}
		if !duplicate {
			snap.Observations[i].Evidence = append(snap.Observations[i].Evidence, row.Evidence)
		}
	}
	for _, edge := range snap.Links {
		if edge.LinkType != memory.LinkSupersedes {
			return fmt.Errorf("snapshot supersession section contains link type %s", edge.LinkType)
		}
		if _, ok := owners[edge.SourceID]; !ok {
			return fmt.Errorf("orphan supersession source %s", edge.SourceID)
		}
		if _, ok := owners[edge.TargetID]; !ok {
			return fmt.Errorf("orphan supersession target %s", edge.TargetID)
		}
	}
	return nil
}
