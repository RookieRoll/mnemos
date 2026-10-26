package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// This file derives the two embedding reports the CLI publishes:
//
//	mnemos embed status    — embedded N/M coverage plus the per-model mix
//	mnemos embed backfill  — skipped rows by closed reason, and what remains
//
// Both are computed from one full observation read (the store's Export) so the
// numbers a preview prints and the numbers a later status prints describe the
// same rows under the same policy. The predicates live here, in the CLI, only
// because the service layer exposes no coverage query; the eligibility rule is
// the store's backfill predicate: live rows (not invalidated, not retired, not
// expired) whose text is not blank.

// embeddingCoverage summarises the vectors a store holds against the active
// embedding model.
type embeddingCoverage struct {
	// Eligible is the number of live observations the policy would embed.
	Eligible int
	// Embedded is how many of those carry a vector the active model can use.
	Embedded int
	// Models is the per-embedding_model distribution, ordered by model name.
	Models []embeddingModelUsage
}

// embeddingModelUsage is one row of the per-embedding_model distribution.
type embeddingModelUsage struct {
	// Model is the stored embedding_model value, or "(unset)" when empty.
	Model string
	// Count is how many live rows carry a vector from this model.
	Count int
	// Dimension is the stored vector width. Vectors from one model share it.
	Dimension int
	// Mismatch reports that Dimension differs from the active model's
	// dimension, which makes the row unusable for hybrid retrieval.
	Mismatch bool
}

// embeddingSkipCounts is the closed reason breakdown of rows a backfill
// deliberately passes over (design D9).
type embeddingSkipCounts struct {
	// Superseded counts rows outside the live window — invalidated, retired, or
	// expired — that therefore carry no vector and are never re-embedded.
	Superseded int
	// IncompatibleModel counts live rows holding a vector from another model or
	// dimension while --rebuild-incompatible was not requested.
	IncompatibleModel int
	// EmptyContent counts live rows whose title, content and rationale are all
	// blank, so there is nothing to embed.
	EmptyContent int
}

// total is the number of rows the backfill passed over.
func (c embeddingSkipCounts) total() int {
	return c.Superseded + c.IncompatibleModel + c.EmptyContent
}

// String renders the closed reason breakdown for the backfill report line.
func (c embeddingSkipCounts) String() string {
	return fmt.Sprintf("superseded:%d incompatible_model:%d empty_content:%d",
		c.Superseded, c.IncompatibleModel, c.EmptyContent)
}

// measureEmbeddingCoverage derives the embed status numbers from one
// observation snapshot.
func measureEmbeddingCoverage(observations []memory.Observation, model string, dimension int, now time.Time) embeddingCoverage {
	cov := embeddingCoverage{}
	byModel := map[string]*embeddingModelUsage{}
	for _, o := range observations {
		if !observationLive(o, now) {
			continue
		}
		if len(o.Embedding) > 0 {
			name := o.EmbeddingModel
			if name == "" {
				name = "(unset)"
			}
			row, ok := byModel[name]
			if !ok {
				// The first row for a model names its width; a later row from
				// the same model with a different width is still reported under
				// that model with the mismatch flag set.
				row = &embeddingModelUsage{Model: name, Dimension: len(o.Embedding)}
				byModel[name] = row
			}
			row.Count++
			if len(o.Embedding) != dimension {
				row.Mismatch = true
			}
		}
		if strings.TrimSpace(embeddingDocument(o)) == "" {
			// Blank text is not embeddable, so it is not part of the coverage
			// denominator; the backfill report counts it as a skip instead.
			continue
		}
		cov.Eligible++
		if embeddingUsable(o, model, dimension) {
			cov.Embedded++
		}
	}
	cov.Models = make([]embeddingModelUsage, 0, len(byModel))
	for _, row := range byModel {
		cov.Models = append(cov.Models, *row)
	}
	sort.Slice(cov.Models, func(i, j int) bool { return cov.Models[i].Model < cov.Models[j].Model })
	return cov
}

// embeddingBackfillGap derives the backfill report's Skipped breakdown and the
// Remaining count from one observation snapshot taken after the run. Skipped
// rows are counted rather than omitted, so a permanent skip is distinguishable
// from a stalled backfill.
func embeddingBackfillGap(observations []memory.Observation, model string, dimension int, rebuild bool, now time.Time) (embeddingSkipCounts, int) {
	var skipped embeddingSkipCounts
	remaining := 0
	for _, o := range observations {
		usable := embeddingUsable(o, model, dimension)
		if !observationLive(o, now) {
			if !usable {
				skipped.Superseded++
			}
			continue
		}
		if usable {
			continue
		}
		if len(o.Embedding) > 0 && !rebuild {
			skipped.IncompatibleModel++
			continue
		}
		if strings.TrimSpace(embeddingDocument(o)) == "" {
			skipped.EmptyContent++
			continue
		}
		remaining++
	}
	return skipped, remaining
}

// observationLive reports whether o is inside the live window the embedding
// policy reads. It mirrors the store's live predicate: an observation is live
// while it is not system-invalidated, its fact-time validity has not passed,
// and it has not expired.
func observationLive(o memory.Observation, now time.Time) bool {
	if o.InvalidatedAt != nil {
		return false
	}
	if o.ValidUntil != nil && !o.ValidUntil.After(now) {
		return false
	}
	if o.ExpiresAt != nil && !o.ExpiresAt.After(now) {
		return false
	}
	return true
}

// embeddingUsable reports whether o carries a vector the active model can use:
// the model identifier must match and the vector must have the model's
// dimension and a finite, nonzero signal.
func embeddingUsable(o memory.Observation, model string, dimension int) bool {
	if model == "" || dimension <= 0 || o.EmbeddingModel != model {
		return false
	}
	return memory.ValidEmbeddingVector(o.Embedding, dimension)
}

// embeddingDocument is the text an observation is embedded from. It is the
// same composition the backfill sends to the provider, so "blank text" means
// blank here and there.
func embeddingDocument(o memory.Observation) string {
	text := o.Title + "\n" + o.Content
	if o.Rationale != "" {
		text += "\n" + o.Rationale
	}
	return text
}
