package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/polyxmedia/mnemos/internal/safety"
)

// ErrConflict means a named version or imported identity no longer agrees.
var ErrConflict = errors.New("memory version conflict")

// ErrInvalidEvolutionInput identifies invalid topic, evidence, or lifecycle input.
var ErrInvalidEvolutionInput = errors.New("invalid evolution input")

// EvidenceReference locates source material; statuses are caller-reported and
// never certify truth, independent proof, trust, or successful agent use.
type EvidenceReference struct {
	Key         string     `json:"key"`
	Locator     string     `json:"locator"`
	Relation    string     `json:"relation"`
	Revision    string     `json:"revision,omitempty"`
	Selector    string     `json:"selector,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Status      string     `json:"status"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
}

// EvidenceCheckInput records one explicit caller result without fetching sources.
type EvidenceCheckInput struct {
	ObservationID string `json:"observation_id"`
	EvidenceKey   string `json:"evidence_key"`
	Status        string `json:"status"`
}

// EvolutionSave carries a normalized request and original retry identity.
type EvolutionSave struct {
	Observation      *Observation
	ReplacesID       string
	UnpinReplacement bool
	TopicExplicit    bool
	RequestHash      string
	Now              time.Time
}

// EvolutionStore is the transaction boundary consumed by evolution services.
type EvolutionStore interface {
	SaveEvolution(ctx context.Context, in EvolutionSave) (*SaveResult, error)
	SupersedeAtomic(ctx context.Context, newID, oldID string, at time.Time) error
	CheckEvidenceState(ctx context.Context, in EvidenceCheckInput, at time.Time) (*EvidenceReference, error)
	ExportHistoricalLinks(ctx context.Context) ([]LinkEdge, error)
	ExportEvolutionSnapshot(ctx context.Context) ([]Observation, []LinkEdge, error)
	ValidateRestoreMetadata(ctx context.Context, owners []Observation, links []LinkEdge) error
	RestoreEvolutionSnapshot(ctx context.Context, owners []Observation, links []LinkEdge) (int, error)
}

// NormalizeRestoreObservation applies fidelity validation shared by single-row
// and full-snapshot restores, without trust clamping, safety rescans, or I/O.
func NormalizeRestoreObservation(o Observation) (Observation, error) {
	if strings.TrimSpace(o.Title) == "" {
		return o, fmt.Errorf("restore: title is required")
	}
	if !o.Type.Valid() {
		return o, fmt.Errorf("restore: invalid type %q", o.Type)
	}
	if o.SourceKind != "" && !o.SourceKind.Valid() {
		return o, fmt.Errorf("restore: invalid source_kind %q", o.SourceKind)
	}
	if o.TrustTier != "" && !o.TrustTier.Valid() {
		return o, fmt.Errorf("restore: invalid trust_tier %q", o.TrustTier)
	}
	if o.AgentID == "" {
		o.AgentID = "default"
	}
	if o.SourceKind == "" {
		o.SourceKind = SourceUser
	}
	if o.TrustTier == "" {
		o.TrustTier = TrustCurated
	}
	var err error
	o.TopicKey, err = NormalizeTopic(o.TopicKey)
	if err != nil {
		return o, err
	}
	o.Evidence, err = NormalizeEvidence(o.Evidence, true)
	if err != nil {
		return o, err
	}
	if _, err := json.Marshal([]any{o.CreatedAt, o.ValidFrom, o.ValidUntil, o.InvalidatedAt, o.ExpiresAt}); err != nil {
		return o, fmt.Errorf("restore: invalid lifecycle timestamp: %w", ErrInvalidEvolutionInput)
	}
	return o, nil
}

// NormalizeTopic validates a trimmed, case-sensitive optional grouping key.
func NormalizeTopic(key string) (string, error) {
	key = strings.TrimSpace(key)
	if len(key) > 256 || !utf8.ValidString(key) {
		return "", fmt.Errorf("memory: topic must be valid UTF-8 up to 256 bytes: %w", ErrInvalidEvolutionInput)
	}
	return key, nil
}

func scanTopicWrite(topic string) error {
	scanner := safety.NewScanner()
	raw := scanner.Scan(topic)
	sanitised := scanner.Scan(raw.Sanitised)
	if raw.MaxRisk >= safety.RiskHigh || sanitised.MaxRisk >= safety.RiskHigh {
		return fmt.Errorf("memory: unsafe topic metadata: %w", ErrInvalidEvolutionInput)
	}
	return nil
}

// NormalizeEvidence validates and canonicalizes bounded version metadata. New
// writes reset checks; trusted restores preserve a valid latest check marker.
func NormalizeEvidence(refs []EvidenceReference, restore bool) ([]EvidenceReference, error) {
	if len(refs) > 16 {
		return nil, fmt.Errorf("memory: at most 16 evidence references: %w", ErrInvalidEvolutionInput)
	}
	seen := map[string]bool{}
	var out []EvidenceReference
	for _, ref := range refs {
		ref.Locator = strings.TrimSpace(ref.Locator)
		ref.Revision = strings.TrimSpace(ref.Revision)
		ref.Selector = strings.TrimSpace(ref.Selector)
		ref.Fingerprint = strings.ToLower(ref.Fingerprint)
		for _, field := range []struct {
			text string
			max  int
		}{{ref.Locator, 2048}, {ref.Revision, 256}, {ref.Selector, 512}} {
			if !utf8.ValidString(field.text) || len(field.text) > field.max {
				return nil, fmt.Errorf("memory: invalid evidence metadata length/UTF-8: %w", ErrInvalidEvolutionInput)
			}
		}
		if ref.Locator == "" || (ref.Relation != "supports" && ref.Relation != "refutes") {
			return nil, fmt.Errorf("memory: evidence requires locator and supports/refutes relation: %w", ErrInvalidEvolutionInput)
		}
		if ref.Fingerprint != "" {
			if decoded, err := hex.DecodeString(ref.Fingerprint); err != nil || len(decoded) != sha256.Size {
				return nil, fmt.Errorf("memory: fingerprint must be SHA-256 hex: %w", ErrInvalidEvolutionInput)
			}
		}
		identity, _ := json.Marshal([]string{ref.Locator, ref.Relation, ref.Revision, ref.Selector, ref.Fingerprint})
		hash := sha256.Sum256(identity)
		key := hex.EncodeToString(hash[:])
		if restore && ref.Key != "" && ref.Key != key {
			return nil, fmt.Errorf("memory: evidence key disagrees with identity: %w", ErrConflict)
		}
		ref.Key = key
		if !restore {
			scanner := safety.NewScanner()
			rep := scanner.Scan(strings.Join([]string{ref.Locator, ref.Revision, ref.Selector}, "\n"))
			sanitised := scanner.Scan(rep.Sanitised)
			if rep.MaxRisk >= safety.RiskHigh || sanitised.MaxRisk >= safety.RiskHigh {
				return nil, fmt.Errorf("memory: unsafe evidence metadata: %w", ErrInvalidEvolutionInput)
			}
			ref.Status = "unverified"
			ref.CheckedAt = nil
		} else {
			if ref.CheckedAt != nil {
				if _, err := json.Marshal(ref.CheckedAt); err != nil {
					return nil, fmt.Errorf("memory: invalid restored check time: %w", ErrInvalidEvolutionInput)
				}
			}
			if ref.Status == "" {
				ref.Status = "unverified"
			}
			if !validEvidenceStatus(ref.Status) || ref.Status == "matched" && ref.Fingerprint == "" {
				return nil, fmt.Errorf("memory: invalid restored evidence check: %w", ErrInvalidEvolutionInput)
			}
		}
		if !seen[key] {
			out = append(out, ref)
			seen[key] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func validEvidenceStatus(status string) bool {
	return status == "unverified" || status == "matched" || status == "changed" || status == "unavailable"
}

// ReplacementPolicy applies domain safeguards to versions re-read inside the
// write transaction. It does not authorize a replacement using source locators.
func ReplacementPolicy(next, old *Observation, now time.Time, unpin, topicExplicit bool) error {
	if next.ID == old.ID || next.AgentID != old.AgentID || next.Project != old.Project {
		return fmt.Errorf("memory: replacement scope/self mismatch: %w", ErrConflict)
	}
	if !old.Live(now) || old.ValidFrom.After(now) {
		return fmt.Errorf("memory: expected replacement target is not live: %w", ErrConflict)
	}
	if next.ValidFrom.After(now) {
		return fmt.Errorf("memory: future-effective replacement unsupported: %w", ErrInvalidEvolutionInput)
	}
	if next.TrustTier == TrustRaw && old.TrustTier != TrustRaw {
		return fmt.Errorf("memory: raw cannot replace trusted observation: %w", ErrConflict)
	}
	if topicExplicit && next.TopicKey != old.TopicKey {
		return fmt.Errorf("memory: replacement cannot change topic: %w", ErrConflict)
	}
	if !topicExplicit {
		next.TopicKey = old.TopicKey
	}
	if old.Pinned && !unpin {
		next.Pinned = true
	}
	if next.Pinned {
		next.ExpiresAt = nil
	}
	return nil
}

func replacementRequestHash(in SaveInput, source SourceKind, trust TrustTier, agent, topic string, evidence []EvidenceReference) (string, error) {
	keys := make([]string, len(evidence))
	for i, ref := range evidence {
		keys[i] = ref.Key
	}
	sort.Strings(keys)
	tags := append([]string{}, in.Tags...)
	parents := append([]string{}, in.DerivedFrom...)
	sort.Strings(tags)
	sort.Strings(parents)
	// Requested lifecycle options are stable across retries. Generated timestamps,
	// inherited mutable pin/trust, and current evidence checks are not identity.
	identity := struct {
		ContentHash, Agent, Project, Session, Topic, Replaces string
		Evidence, Tags, Parents                               []string
		Importance, TTL                                       int
		Pinned, Unpin                                         bool
		ValidFrom, ValidUntil                                 *time.Time
		Source                                                SourceKind
		Trust                                                 TrustTier
	}{
		hashContent(in.Type, in.Title, in.Content, in.Rationale, in.Structured), agent, in.Project, in.SessionID, topic, in.ReplacesID, keys, tags, parents, in.Importance, in.TTLDays, in.Pinned, in.UnpinReplacement, in.ValidFrom, in.ValidUntil, source, trust}
	if in.ValidFrom != nil {
		v := in.ValidFrom.UTC()
		identity.ValidFrom = &v
	}
	if in.ValidUntil != nil {
		v := in.ValidUntil.UTC()
		identity.ValidUntil = &v
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("memory: invalid replacement lifecycle: %w: %v", ErrInvalidEvolutionInput, err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

// CheckEvidence stores only the latest explicit caller result with service time.
func (s *Service) CheckEvidence(ctx context.Context, in EvidenceCheckInput) (*EvidenceReference, error) {
	if strings.TrimSpace(in.ObservationID) == "" || len(in.EvidenceKey) != 64 || !validEvidenceStatus(in.Status) {
		return nil, fmt.Errorf("memory: invalid evidence check input: %w", ErrInvalidEvolutionInput)
	}
	store, ok := s.store.(EvolutionStore)
	if !ok {
		return nil, fmt.Errorf("memory: store does not support evidence checks")
	}
	return store.CheckEvidenceState(ctx, in, s.clock().UTC())
}

// ValidateRestoreMetadata preflights full-store owners, identity, and link orphans.
func (s *Service) ValidateRestoreMetadata(ctx context.Context, owners []Observation, links []LinkEdge) error {
	store, ok := s.store.(EvolutionStore)
	if !ok {
		return fmt.Errorf("memory: store does not support metadata restore")
	}
	return store.ValidateRestoreMetadata(ctx, owners, links)
}

// RestoreEvolutionSnapshot atomically restores owners, then evidence and links.
func (s *Service) RestoreEvolutionSnapshot(ctx context.Context, owners []Observation, links []LinkEdge) (int, error) {
	store, ok := s.store.(EvolutionStore)
	if !ok {
		return 0, fmt.Errorf("memory: store does not support fidelity restore")
	}
	return store.RestoreEvolutionSnapshot(ctx, owners, links)
}

// ExportEvolutionSnapshot exports observations/evidence and historical links in
// one non-mutating transaction snapshot.
func (s *Service) ExportEvolutionSnapshot(ctx context.Context) ([]Observation, []LinkEdge, error) {
	store, ok := s.store.(EvolutionStore)
	if !ok {
		return nil, nil, fmt.Errorf("memory: store does not support fidelity export")
	}
	return store.ExportEvolutionSnapshot(ctx)
}
