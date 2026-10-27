package session

import (
	"context"
	"errors"
	"fmt"
)

// ErrSessionNotOwned reports that a session-end request named a session the
// invocation is not entitled to close. Transports map it to a stable error
// code; hooks and the local `mnemos sessions close` maintenance channel stay
// outside this guard by construction.
var ErrSessionNotOwned = errors.New("session not owned")

// NotOwnedError carries the rejected session id and the invocation workspace
// that did not authorise the close. It unwraps to ErrSessionNotOwned so a
// transport can route on the sentinel while still reporting the rejection.
type NotOwnedError struct {
	SessionID     string
	WorkspaceRoot string
}

// Error renders the subsystem-prefixed rejection with its identity.
func (e *NotOwnedError) Error() string {
	if e.WorkspaceRoot == "" {
		return fmt.Sprintf("session: %v (id %s)", ErrSessionNotOwned, e.SessionID)
	}
	return fmt.Sprintf("session: %v (id %s, workspace %s)", ErrSessionNotOwned, e.SessionID, e.WorkspaceRoot)
}

// Unwrap supports errors.Is(err, ErrSessionNotOwned).
func (e *NotOwnedError) Unwrap() error { return ErrSessionNotOwned }

// EndReceipts authorises a close for a session the invocation did not open.
// Implementations MUST decide by workspace binding, never by agent identity:
// a different agent in the same workspace may close a receipt-backed session,
// and the same agent in another workspace may not.
//
// This is the named receipt seam from the session-end ownership design. The
// MCP transport implements it over continuity's read path today; a dedicated
// receipt accessor can replace that implementation without changing
// EndOwned's guard logic.
type EndReceipts interface {
	// AuthorizeSessionEnd reports whether a continuity receipt carried by
	// target binds that session to the invocation's workspace. A plain
	// refusal is (false, nil); an operational failure returns an error.
	AuthorizeSessionEnd(ctx context.Context, target *Session) (bool, error)
}

// EndInvocation is the caller context of one ownership-checked session end.
// The transport supplies the exact session established on this connection.
// Project labels and recency do not establish ownership between terminals.
type EndInvocation struct {
	// CurrentSessionID is the session opened by this invocation's MCP connection.
	CurrentSessionID string
	// HostSessionID is the host conversation id, when the transport has one.
	HostSessionID string
	// Project is the invocation's project label; it does not authorize a close.
	Project string
	// WorkspaceRoot names the invocation workspace for the rejection record.
	// Workspace comparison itself belongs to EndReceipts.
	WorkspaceRoot string
	// Receipts optionally authorises closing a continuity-bound session.
	// Nil means only the bound session may be closed.
	Receipts EndReceipts
}

// EndOwned closes the session named by in only when the invocation is
// entitled to close it: in.ID is the invocation's bound session, or Receipts
// confirms a continuity receipt binding that session to the invocation's
// workspace. Any other id returns a *NotOwnedError and leaves the session
// untouched. Hooks keep using CloseForHost/EndForHost and the local CLI keeps
// using CloseEach, so neither passes through this guard.
func (s *Service) EndOwned(ctx context.Context, in CloseInput, inv EndInvocation) error {
	if in.ID == "" {
		return fmt.Errorf("session close: id required")
	}
	bound, err := s.boundSession(ctx, inv)
	if err != nil {
		return fmt.Errorf("session close: resolve bound session: %w", err)
	}
	if bound != nil && bound.ID == in.ID {
		return s.Close(ctx, in)
	}
	if inv.Receipts != nil {
		target, err := s.Get(ctx, in.ID)
		switch {
		case err == nil:
			// Only a continuity-bound row can carry a receipt; a legacy row
			// stays rejected without consulting the receipt seam.
			if target.ContinuityBound {
				ok, authErr := inv.Receipts.AuthorizeSessionEnd(ctx, target)
				if authErr != nil {
					return fmt.Errorf("session close: verify continuity receipt: %w", authErr)
				}
				if ok {
					return s.Close(ctx, in)
				}
			}
		case !errors.Is(err, ErrNotFound):
			return fmt.Errorf("session close: read target session: %w", err)
		}
	}
	return &NotOwnedError{SessionID: in.ID, WorkspaceRoot: inv.WorkspaceRoot}
}

// boundSession uses explicit identities only. Hook callers may supply a host
// conversation, while MCP supplies its exact connection-bound session.
func (s *Service) boundSession(ctx context.Context, inv EndInvocation) (*Session, error) {
	if inv.CurrentSessionID != "" {
		current, err := s.Get(ctx, inv.CurrentSessionID)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return current, err
	}
	if inv.HostSessionID == "" {
		return nil, nil
	}
	owned, err := s.ListLegacyOpenByHost(ctx, inv.HostSessionID)
	if err != nil {
		return nil, err
	}
	if len(owned) == 0 {
		return nil, nil
	}
	return &owned[0], nil
}
