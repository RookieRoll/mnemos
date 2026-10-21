package rumination

import (
	"context"
	"strings"
	"testing"
)

func TestResolveDecisionValidationAndPersistence(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	_, err := store.Upsert(ctx, Candidate{ID: "candidate", MonitorName: "monitor", TargetID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(Config{Store: store})

	for _, tc := range []struct {
		name, id, resolvedBy, whyBetter, wantError string
	}{
		{name: "missing id", resolvedBy: "revision", whyBetter: "A new concrete prediction", wantError: "id required"},
		{name: "missing provenance", id: "candidate", whyBetter: "A new concrete prediction", wantError: "resolved_by required"},
		{name: "short prediction", id: "candidate", resolvedBy: "revision", whyBetter: "too short", wantError: "min 16 chars"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.ResolveDecision(ctx, tc.id, tc.resolvedBy, tc.whyBetter)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("ResolveDecision error = %v, want containing %q", err, tc.wantError)
			}
		})
	}

	if err := svc.ResolveDecision(ctx, "candidate", "revision", "新的预测将在后续输入中稳定覆盖既有反例"); err != nil {
		t.Fatalf("ResolveDecision valid Unicode rationale: %v", err)
	}
	got, err := store.Get(ctx, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusResolved || got.ResolvedBy != "revision" {
		t.Fatalf("resolved candidate = %+v", got)
	}
}

func TestDismissDecisionValidationAndPersistence(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	_, err := store.Upsert(ctx, Candidate{ID: "candidate", MonitorName: "monitor", TargetID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(Config{Store: store})

	for _, tc := range []struct {
		name, id, reason, wantError string
	}{
		{name: "missing id", reason: "evidence was noise", wantError: "id required"},
		{name: "short reason", id: "candidate", reason: "noise", wantError: "at least 8 chars"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.DismissDecision(ctx, tc.id, tc.reason)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("DismissDecision error = %v, want containing %q", err, tc.wantError)
			}
		})
	}

	if err := svc.DismissDecision(ctx, "candidate", "证据已确认是噪声"); err != nil {
		t.Fatalf("DismissDecision valid Unicode rationale: %v", err)
	}
	got, err := store.Get(ctx, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDismissed || got.DismissedReason != "证据已确认是噪声" {
		t.Fatalf("dismissed candidate = %+v", got)
	}
}
