package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

type diagnosticBackfillStore struct {
	updateErr error
	writes    []string
}

func (*diagnosticBackfillStore) EmbeddingBackfillUpperBound(context.Context) (string, error) {
	return "c", nil
}
func (*diagnosticBackfillStore) ListEmbeddingBackfill(context.Context, memory.EmbeddingBackfillInput) ([]memory.Observation, error) {
	return []memory.Observation{{ID: "a", Title: "a", Content: "SYNTHETIC_PRIVATE_BODY"}, {ID: "b", Title: "b"}, {ID: "c", Title: "c"}}, nil
}
func (s *diagnosticBackfillStore) UpdateEmbedding(_ context.Context, id, _ string, _ []float32) error {
	if id != "c" && s.updateErr != nil {
		return s.updateErr
	}
	s.writes = append(s.writes, id)
	return nil
}

type diagnosticBackfillEmbedder struct {
	cause   error
	invalid bool
}

func (e diagnosticBackfillEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if !strings.HasPrefix(text, "c\n") {
		if e.cause != nil {
			return nil, e.cause
		}
		if e.invalid {
			return []float32{12345}, nil
		}
	}
	return []float32{1, 0}, nil
}
func (diagnosticBackfillEmbedder) Dimension() int { return 2 }
func (diagnosticBackfillEmbedder) Model() string  { return "fixture" }

func TestEmbeddingBackfillPreservesFirstFailureDiagnostic(t *testing.T) {
	providerErr := errors.New("synthetic provider unavailable")
	updateErr := errors.New("synthetic database write failed")
	for _, tc := range []struct {
		name, stage string
		provider    error
		update      error
		invalid     bool
		wantCause   error
	}{
		{"provider", "embed", providerErr, nil, false, providerErr},
		{"update", "update", nil, updateErr, false, updateErr},
		{"invalid vector", "embed", nil, nil, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &diagnosticBackfillStore{updateErr: tc.update}
			report, err := runEmbeddingBackfill(context.Background(), store, diagnosticBackfillEmbedder{cause: tc.provider, invalid: tc.invalid}, embeddingBackfillOptions{})
			if err == nil || report.Attempted != 3 || report.Failed != 2 || report.Succeeded != 1 {
				t.Fatalf("counts/cause lost: report=%+v err=%v", report, err)
			}
			if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
				t.Fatalf("error chain lost cause: %v", err)
			}
			if !strings.Contains(err.Error(), `observation "a" `+tc.stage+":") || strings.Contains(err.Error(), `observation "b"`) {
				t.Fatalf("not the first bounded diagnostic: %v", err)
			}
			if tc.invalid && !strings.Contains(err.Error(), "invalid vector shape/signal (dimension 1, expected 2)") {
				t.Fatalf("safe shape diagnostic missing: %v", err)
			}
			for _, sensitive := range []string{"SYNTHETIC_PRIVATE_BODY", "12345"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("diagnostic exposed observation/vector content: %v", err)
				}
			}
			if !reflect.DeepEqual(store.writes, []string{"c"}) {
				t.Fatalf("later successful row was not retained: %v", store.writes)
			}
		})
	}
}
