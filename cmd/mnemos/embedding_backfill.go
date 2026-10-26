package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/polyxmedia/mnemos/internal/memory"
)

type embeddingBackfillStore interface {
	EmbeddingBackfillUpperBound(context.Context) (string, error)
	ListEmbeddingBackfill(context.Context, memory.EmbeddingBackfillInput) ([]memory.Observation, error)
	UpdateEmbedding(context.Context, string, string, []float32) error
}
type embeddingBackfillOptions struct{ RebuildIncompatible bool }

// embeddingBackfillReport is the outcome of one offline backfill run.
//
// Attempted, Succeeded and Failed describe the provider work. Skipped and
// Remaining describe the rows the work deliberately left alone: a permanent
// skip (superseded, incompatible model, blank text) is counted by reason so it
// cannot be mistaken for a stalled backfill, and Remaining is the number of
// eligible observations that still carry no usable vector.
type embeddingBackfillReport struct {
	Attempted int
	Succeeded int
	Failed    int
	Skipped   embeddingSkipCounts
	Remaining int
}

// runEmbeddingBackfill performs no retry: success and failure both advance the
// ID cursor, while a later invocation can revisit still-missing vectors.
func runEmbeddingBackfill(ctx context.Context, store embeddingBackfillStore, embedder memory.Embedder, options embeddingBackfillOptions) (embeddingBackfillReport, error) {
	var report embeddingBackfillReport
	var firstFailure error
	rememberFailure := func(id, stage string, cause error) {
		report.Failed++
		if firstFailure == nil {
			firstFailure = fmt.Errorf("observation %q %s: %w", id, stage, cause)
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if embedder == nil || embedder.Dimension() <= 0 || embedder.Model() == "" {
		return report, fmt.Errorf("no embedding provider available — check `embed status`")
	}
	upper, err := store.EmbeddingBackfillUpperBound(ctx)
	if err != nil {
		return report, fmt.Errorf("backfill horizon: %w", err)
	}
	cursor := ""
	for upper != "" {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		batch, err := store.ListEmbeddingBackfill(ctx, memory.EmbeddingBackfillInput{AfterID: cursor, ThroughID: upper, Limit: 100, Model: embedder.Model(), Dimension: embedder.Dimension(), RebuildIncompatible: options.RebuildIncompatible})
		if err != nil {
			return report, fmt.Errorf("backfill page: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		for _, o := range batch {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if o.ID <= cursor || o.ID > upper {
				return report, fmt.Errorf("backfill page did not advance within its horizon")
			}
			cursor = o.ID
			text := embeddingDocument(o)
			if strings.TrimSpace(text) == "" {
				// Nothing to embed. The post-run coverage scan counts this row
				// as Skipped(empty_content); attempting it would only store a
				// meaningless vector or burn a provider call.
				continue
			}
			report.Attempted++
			vec, err := embedder.Embed(ctx, text)
			if ctx.Err() != nil {
				report.Failed++
				return report, ctx.Err()
			}
			if err != nil || !memory.ValidEmbeddingVector(vec, embedder.Dimension()) {
				if err == nil {
					err = fmt.Errorf("invalid vector shape/signal (dimension %d, expected %d)", len(vec), embedder.Dimension())
				}
				rememberFailure(o.ID, "embed", err)
				continue
			}
			if err := store.UpdateEmbedding(ctx, o.ID, embedder.Model(), vec); err != nil {
				if ctx.Err() != nil {
					report.Failed++
					return report, ctx.Err()
				}
				rememberFailure(o.ID, "update", err)
				continue
			}
			report.Succeeded++
		}
		if len(batch) < 100 {
			break
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("embedding backfill: %d of %d attempts failed; first failure: %w", report.Failed, report.Attempted, firstFailure)
	}
	return report, nil
}
