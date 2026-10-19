package continuity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/safety"
)

// Config supplies persistence and optional deterministic workspace, safety and clock dependencies.
type Config struct {
	Store    Store
	Resolver Resolver
	Scanner  *safety.Scanner
	Now      func() time.Time
}

// Service manages latest-only checkpoints without embedding or execution dependencies.
type Service struct {
	store    Store
	resolver Resolver
	scanner  *safety.Scanner
	now      func() time.Time
}

// New constructs a continuity service. Defaults perform local read-only root resolution.
func New(cfg Config) *Service {
	if cfg.Resolver == nil {
		cfg.Resolver = LocalResolver{}
	}
	if cfg.Scanner == nil {
		cfg.Scanner = safety.NewScanner()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{store: cfg.Store, resolver: cfg.Resolver, scanner: cfg.Scanner, now: cfg.Now}
}

func requestHash(v any) string {
	data, _ := json.Marshal(v)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Open atomically establishes or confirms one invocation's independent session.
func (s *Service) Open(ctx context.Context, in OpenInput) (*OpenResult, error) {
	agent, err := normalizeScope(in.ProtocolVersion, in.AgentID, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	in.AgentID = agent
	if in.StartKey == "" || !bounded(in.StartKey, 128) || in.HarnessKind == "" || !bounded(in.HarnessKind, 64) || !bounded(in.HostSessionID, 256) || !bounded(in.Project, 4096) || !bounded(in.Goal, 4096) {
		return nil, invalid("invalid startup identity or goal")
	}
	if err := validateContextOptions(in.SourceSessionID, in.Task, in.Limit, in.MaxBytes); err != nil {
		return nil, err
	}
	w, err := s.resolver.Resolve(ctx, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if !bounded(w.Root, 4096) || w.Key == "" {
		return nil, invalid("invalid resolved workspace")
	}
	if in.Project == "" {
		in.Project = filepath.Base(w.Root)
	}
	// Equivalent cwd/subdirectory spellings bind to the canonical workspace identity.
	hashInput := in
	hashInput.CWD = ""
	hashInput.WorkspaceRoot = w.Root
	return s.store.Open(ctx, OpenRecord{Input: in, Workspace: w, RequestHash: requestHash(struct {
		Version   int
		Workspace string
		Input     OpenInput
	}{1, w.Key, hashInput}), SessionID: ulid.Make().String(), Now: s.now().UTC()})
}

// Checkpoint replaces or clears one exact owned checkpoint with optimistic concurrency.
func (s *Service) Checkpoint(ctx context.Context, in CheckpointInput) (*CheckpointResult, error) {
	agent, err := normalizeScope(in.ProtocolVersion, in.AgentID, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	in.AgentID = agent
	if in.SessionID == "" || !bounded(in.SessionID, 256) || in.RequestID == "" || !bounded(in.RequestID, 128) || in.ExpectedRevision == nil || *in.ExpectedRevision < 0 || *in.ExpectedRevision == math.MaxInt64 {
		return nil, invalid("invalid checkpoint identity or revision")
	}
	if in.Visibility == "" {
		in.Visibility = "owner"
	}
	if in.Visibility != "owner" && in.Visibility != "workspace" {
		return nil, invalid("invalid visibility")
	}
	if in.Action == "clear" {
		if in.Payload != nil || in.Visibility != "owner" {
			return nil, invalid("clear accepts no payload or shared visibility")
		}
	} else if in.Action == "save" {
		if err := validatePayload(in.Payload); err != nil {
			return nil, err
		}
		if in.Payload.Workspace != (WorkspaceState{}) {
			return nil, invalid("workspace metadata is server sampled")
		}
		data, _ := json.Marshal(in.Payload)
		var clone Payload
		_ = json.Unmarshal(data, &clone)
		in.Payload = &clone
		if err := s.sanitizePayload(in.Payload); err != nil {
			return nil, err
		}
		if encodedSize(in.Payload) > MaxPayloadBytes {
			return nil, invalid("checkpoint payload exceeds byte budget")
		}
	} else {
		return nil, invalid("invalid checkpoint action")
	}
	w, err := s.resolver.Resolve(ctx, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	hashInput := in
	hashInput.CWD = ""
	hashInput.WorkspaceRoot = w.Root
	hashInput.RequestID = ""
	return s.store.Checkpoint(ctx, Mutation{Input: in, Workspace: w, RequestHash: requestHash(struct {
		Version   int
		Workspace string
		Input     CheckpointInput
	}{1, w.Key, hashInput}), Now: s.now().UTC(), Sample: func(ctx context.Context) WorkspaceState { return s.sample(ctx, w) }})
}

func (s *Service) sanitizePayload(p *Payload) error {
	fields := []*string{&p.Task, &p.Summary}
	for i := range p.NextSteps {
		fields = append(fields, &p.NextSteps[i])
	}
	for i := range p.Blockers {
		fields = append(fields, &p.Blockers[i])
	}
	for i := range p.References {
		fields = append(fields, &p.References[i].ID)
	}
	var rawText []string
	for _, field := range fields {
		rawText = append(rawText, *field)
	}
	rawReport := s.scanner.Scan(strings.Join(rawText, "\n"))
	if rawReport.MaxRisk >= safety.RiskHigh || s.scanner.Scan(rawReport.Sanitised).MaxRisk >= safety.RiskHigh {
		return invalid("checkpoint rejected by write-boundary scanner")
	}
	for _, field := range fields {
		raw := s.scanner.Scan(*field)
		clean := s.scanner.Scan(raw.Sanitised)
		if raw.MaxRisk >= safety.RiskHigh || clean.MaxRisk >= safety.RiskHigh {
			return invalid("checkpoint rejected by write-boundary scanner")
		}
		*field = clean.Sanitised
	}
	// JSON punctuation and field names must not hide instruction fragments.
	var cleanText []string
	for _, field := range fields {
		cleanText = append(cleanText, *field)
	}
	if s.scanner.Scan(strings.Join(cleanText, "\n")).MaxRisk >= safety.RiskHigh {
		return invalid("checkpoint rejected by write-boundary scanner")
	}
	return validatePayload(p)
}

func (s *Service) sample(ctx context.Context, w Workspace) WorkspaceState {
	if sampler, ok := s.resolver.(WorkspaceSampler); ok {
		return sampler.Sample(ctx, w.Root)
	}
	return w.State
}

// Context reads visible candidates from one SQLite snapshot; it never consumes or executes them.
func (s *Service) Context(ctx context.Context, in ContextInput) (*ContextResult, error) {
	agent, err := normalizeScope(in.ProtocolVersion, in.AgentID, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if in.SessionID == "" || !bounded(in.SessionID, 256) {
		return nil, invalid("exact current session required")
	}
	if err := validateContextOptions(in.SourceSessionID, in.Task, in.Limit, in.MaxBytes); err != nil {
		return nil, err
	}
	if in.Limit == 0 {
		in.Limit = 5
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = DefaultResponseBytes
	}
	w, err := s.resolver.Resolve(ctx, in.CWD, in.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	snap, err := s.store.Read(ctx, ReadScope{AgentID: agent, WorkspaceKey: w.Key, SessionID: in.SessionID, SourceSessionID: in.SourceSessionID})
	if err != nil {
		return nil, err
	}
	// Sample the recorded binding root, never an unrelated caller directory.
	w.Root = snap.Root
	current := s.sample(ctx, w)
	out := &ContextResult{ProtocolVersion: 1, Status: "none", MaxBytes: in.MaxBytes}
	var sources []Source
	for _, source := range snap.Sources {
		if in.SourceSessionID == "" && in.Task != "" && (source.Payload == nil || source.Payload.Task != in.Task) {
			continue
		}
		source.Stale = s.now().UTC().Sub(source.RecordedAt) > 24*time.Hour
		source.WorkspaceUnknown = source.Payload == nil || !source.Payload.Workspace.Known || !current.Known
		if !source.WorkspaceUnknown {
			source.WorkspaceChanged = source.Payload.Workspace.Branch != current.Branch || source.Payload.Workspace.HEAD != current.HEAD
		}
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].RecordedAt.Equal(sources[j].RecordedAt) {
			return sources[i].SessionID < sources[j].SessionID
		}
		return sources[i].RecordedAt.After(sources[j].RecordedAt)
	})
	if in.SourceSessionID != "" {
		if len(sources) == 0 {
			return nil, ErrUnavailable
		}
		out.Source = &sources[0]
		out.Status = "selected"
		if sources[0].SourceKind == "cleared" {
			out.Status = "cleared"
		}
	} else if len(sources) > 0 {
		out.Status = "candidates"
		out.Ambiguous = len(sources) > 1
		if len(sources) == 1 && in.ResumeIntent && !sources[0].RecordedAt.After(s.now().UTC()) && !sources[0].Stale && !sources[0].WorkspaceChanged && !sources[0].WorkspaceUnknown && (sources[0].State == "active" || sources[0].State == "blocked") {
			out.Status = "selected"
			out.Source = &sources[0]
		} else {
			if len(sources) > in.Limit {
				out.HasMore = true
				sources = sources[:in.Limit]
			}
			out.Candidates = sources
		}
	}
	// Account for the whole JSON envelope, including its own decimal size field.
	for {
		settleSize(out)
		if out.BytesReturned <= in.MaxBytes {
			return out, nil
		}
		out.HasMore = true
		out.OmittedForSize++
		if out.Source != nil {
			out.Source = nil
			out.Status = "candidates"
			continue
		}
		if len(out.Candidates) > 0 {
			out.Candidates = out.Candidates[:len(out.Candidates)-1]
			continue
		}
		return nil, invalid("response budget cannot fit status envelope")
	}
}

func settleSize(out *ContextResult) {
	for {
		n := encodedSize(out)
		if n == out.BytesReturned {
			return
		}
		out.BytesReturned = n
	}
}
