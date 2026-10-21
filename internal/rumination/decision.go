package rumination

import (
	"context"
	"fmt"
	"unicode/utf8"
)

// ResolveDecision validates and records an agent-authored semantic resolution.
// whyBetter must name a concrete prediction the revision makes that the old
// version did not; Mnemos validates the shape but does not judge its meaning.
func (s *Service) ResolveDecision(ctx context.Context, id, resolvedBy, whyBetter string) error {
	if err := ValidateResolveDecision(id, resolvedBy, whyBetter); err != nil {
		return err
	}
	return s.Resolve(ctx, id, resolvedBy)
}

// ValidateResolveDecision applies the shared structural checks used before a
// semantic resolution. It does not assess whether the prediction is sound.
func ValidateResolveDecision(id, resolvedBy, whyBetter string) error {
	if id == "" {
		return fmt.Errorf("rumination: id required")
	}
	if resolvedBy == "" {
		return fmt.Errorf("rumination: resolved_by required — the revision must carry provenance")
	}
	if n := utf8.RuneCountInString(whyBetter); n < 16 {
		return fmt.Errorf("rumination: why_better must state a new prediction (min 16 chars), got %d", n)
	}
	return nil
}

// DismissDecision validates and records an agent-authored dismissal. The
// reason is preserved so later maintenance does not silently repeat the same
// review request.
func (s *Service) DismissDecision(ctx context.Context, id, reason string) error {
	if err := ValidateDismissDecision(id, reason); err != nil {
		return err
	}
	return s.Dismiss(ctx, id, reason)
}

// ValidateDismissDecision applies the shared structural checks used before a
// semantic dismissal. It does not assess whether the reason is persuasive.
func ValidateDismissDecision(id, reason string) error {
	if id == "" {
		return fmt.Errorf("rumination: id required")
	}
	if utf8.RuneCountInString(reason) < 8 {
		return fmt.Errorf("rumination: reason must be at least 8 chars")
	}
	return nil
}
