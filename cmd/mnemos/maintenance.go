package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/polyxmedia/mnemos/internal/rumination"
)

const maintenancePayloadLimit = 32 << 10
const maintenanceOutputLimit = 256 << 10

// maintenanceJSONError marks an error whose stable JSON representation has
// already been written to stdout. main uses it to set a non-zero exit status
// without appending human-oriented text to the machine-readable response.
type maintenanceJSONError struct{ cause error }

func (e maintenanceJSONError) Error() string { return e.cause.Error() }
func (e maintenanceJSONError) Unwrap() error { return e.cause }

type maintenanceErrorEnvelope struct {
	ProtocolVersion int                      `json:"protocol_version"`
	Error           maintenanceErrorResponse `json:"error"`
}

type maintenanceErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type maintenanceCandidate struct {
	ID         string                `json:"id"`
	Monitor    string                `json:"monitor"`
	Severity   string                `json:"severity"`
	Reason     string                `json:"reason"`
	TargetKind string                `json:"target_kind"`
	TargetID   string                `json:"target_id"`
	DetectedAt string                `json:"detected_at"`
	Evidence   []maintenanceEvidence `json:"evidence"`
	Status     string                `json:"status"`
}

type maintenanceEvidence struct {
	Label   string `json:"label"`
	Content string `json:"content"`
	Source  string `json:"source,omitempty"`
}

type maintenanceListResponse struct {
	ProtocolVersion int                    `json:"protocol_version"`
	Command         string                 `json:"command"`
	Candidates      []maintenanceCandidate `json:"candidates"`
}

type maintenanceEvidenceResponse struct {
	ProtocolVersion int                  `json:"protocol_version"`
	Command         string               `json:"command"`
	Candidate       maintenanceCandidate `json:"candidate"`
	Target          maintenanceTarget    `json:"target"`
	Text            string               `json:"text"`
	TokenEstimate   int                  `json:"token_estimate"`
}

type maintenanceTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type maintenanceDecisionResponse struct {
	ProtocolVersion int    `json:"protocol_version"`
	Command         string `json:"command"`
	ID              string `json:"id"`
	Status          string `json:"status"`
	ResolvedBy      string `json:"resolved_by,omitempty"`
	WhyBetter       string `json:"why_better,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type resolveDecisionPayload struct {
	CandidateID string `json:"candidate_id"`
	ResolvedBy  string `json:"resolved_by"`
	WhyBetter   string `json:"why_better"`
}

type dismissDecisionPayload struct {
	CandidateID string `json:"candidate_id"`
	Reason      string `json:"reason"`
}

// runMaintenance exposes bounded candidate evidence and explicit semantic
// decisions to the active host agent. All decisions pass through rumination's
// same validation methods as the MCP tools.
func runMaintenance(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return writeMaintenanceError("usage", "usage: mnemos maintenance <list|evidence|resolve|dismiss>")
	}
	subcommand := args[0]
	rest := args[1:]
	switch subcommand {
	case "list", "evidence", "resolve", "dismiss":
	default:
		return writeMaintenanceError("unknown_command", "unknown maintenance command: "+subcommand)
	}

	var payload string
	var limit int
	var candidateID string
	fs := flag.NewFlagSet("maintenance "+subcommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&limit, "limit", 100, "maximum pending candidates (1..1000)")
	fs.StringVar(&candidateID, "id", "", "candidate ID")
	fs.StringVar(&payload, "payload", "", "bounded JSON decision payload")
	if err := fs.Parse(rest); err != nil {
		return writeMaintenanceError("invalid_arguments", err.Error())
	}
	if fs.NArg() != 0 {
		return writeMaintenanceError("invalid_arguments", "unexpected positional arguments")
	}
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	var resolveInput resolveDecisionPayload
	var dismissInput dismissDecisionPayload
	switch subcommand {
	case "list":
		if seen["id"] || seen["payload"] {
			return writeMaintenanceError("invalid_arguments", "list accepts only --limit")
		}
		if limit < 1 || limit > 1000 {
			return writeMaintenanceError("invalid_arguments", "limit must be between 1 and 1000")
		}
	case "evidence":
		if seen["payload"] || seen["limit"] {
			return writeMaintenanceError("invalid_arguments", "evidence accepts only --id")
		}
		if candidateID == "" {
			return writeMaintenanceError("invalid_arguments", "--id is required")
		}
	case "resolve":
		if seen["id"] || seen["limit"] {
			return writeMaintenanceError("invalid_arguments", "resolve accepts only --payload")
		}
		if err := decodeMaintenancePayload(payload, &resolveInput); err != nil {
			return writeMaintenanceError("invalid_payload", err.Error())
		}
		if err := rumination.ValidateResolveDecision(resolveInput.CandidateID, resolveInput.ResolvedBy, resolveInput.WhyBetter); err != nil {
			return writeMaintenanceError("invalid_decision", err.Error())
		}
	case "dismiss":
		if seen["id"] || seen["limit"] {
			return writeMaintenanceError("invalid_arguments", "dismiss accepts only --payload")
		}
		if err := decodeMaintenancePayload(payload, &dismissInput); err != nil {
			return writeMaintenanceError("invalid_payload", err.Error())
		}
		if err := rumination.ValidateDismissDecision(dismissInput.CandidateID, dismissInput.Reason); err != nil {
			return writeMaintenanceError("invalid_decision", err.Error())
		}
	}

	d, err := loadDeps(ctx)
	if err != nil {
		return writeMaintenanceError("storage_unavailable", err.Error())
	}
	defer d.close()
	ruminationService := d.rum
	if ruminationService == nil {
		// The CLI is an explicit review surface. A disabled detector must not
		// make already-persisted candidates inaccessible to an authorized task.
		ruminationService = rumination.NewService(rumination.Config{
			Skills: d.skl, Memory: d.db.Observations(), Store: d.db.Rumination(),
		})
	}

	switch subcommand {
	case "list":
		candidates, err := ruminationService.Pending(ctx, limit)
		if err != nil {
			return writeMaintenanceError("maintenance_failed", err.Error())
		}
		out := maintenanceListResponse{ProtocolVersion: 1, Command: "list", Candidates: make([]maintenanceCandidate, 0, len(candidates))}
		for _, c := range candidates {
			out.Candidates = append(out.Candidates, toMaintenanceCandidate(c))
		}
		return writeMaintenanceJSON(out)
	case "evidence":
		c, err := ruminationService.Get(ctx, candidateID)
		if err != nil {
			return writeMaintenanceServiceError(err)
		}
		block, err := ruminationService.Pack(ctx, *c)
		if err != nil {
			return writeMaintenanceServiceError(err)
		}
		return writeMaintenanceJSON(maintenanceEvidenceResponse{
			ProtocolVersion: 1, Command: "evidence", Candidate: toMaintenanceCandidate(*c),
			Target: maintenanceTarget{Kind: string(block.Target.Kind), ID: block.Target.ID, Name: block.Target.Name},
			Text:   block.Text, TokenEstimate: block.TokenEstimate,
		})
	case "resolve":
		if err := ruminationService.ResolveDecision(ctx, resolveInput.CandidateID, resolveInput.ResolvedBy, resolveInput.WhyBetter); err != nil {
			return writeMaintenanceServiceError(err)
		}
		return writeMaintenanceJSON(maintenanceDecisionResponse{
			ProtocolVersion: 1, Command: "resolve", ID: resolveInput.CandidateID,
			Status: string(rumination.StatusResolved), ResolvedBy: resolveInput.ResolvedBy, WhyBetter: resolveInput.WhyBetter,
		})
	case "dismiss":
		if err := ruminationService.DismissDecision(ctx, dismissInput.CandidateID, dismissInput.Reason); err != nil {
			return writeMaintenanceServiceError(err)
		}
		return writeMaintenanceJSON(maintenanceDecisionResponse{
			ProtocolVersion: 1, Command: "dismiss", ID: dismissInput.CandidateID,
			Status: string(rumination.StatusDismissed), Reason: dismissInput.Reason,
		})
	}
	return writeMaintenanceError("unknown_command", "unknown maintenance command: "+subcommand)
}

func decodeMaintenancePayload(payload string, dst any) error {
	if payload == "" {
		return fmt.Errorf("--payload is required")
	}
	if len(payload) > maintenancePayloadLimit {
		return fmt.Errorf("payload exceeds %d bytes", maintenancePayloadLimit)
	}
	if strings.TrimSpace(payload) == "" || strings.TrimSpace(payload)[0] != '{' {
		return fmt.Errorf("payload must be a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("payload must contain exactly one JSON object")
		}
		return fmt.Errorf("decode trailing payload: %w", err)
	}
	return nil
}

func toMaintenanceCandidate(c rumination.Candidate) maintenanceCandidate {
	evidence := make([]maintenanceEvidence, 0, len(c.Evidence))
	for _, e := range c.Evidence {
		evidence = append(evidence, maintenanceEvidence{Label: e.Label, Content: e.Content, Source: e.Source})
	}
	return maintenanceCandidate{
		ID: c.ID, Monitor: c.MonitorName, Severity: c.Severity.String(), Reason: c.Reason,
		TargetKind: string(c.TargetKind), TargetID: c.TargetID, DetectedAt: c.DetectedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Evidence: evidence, Status: string(c.Status),
	}
}

func writeMaintenanceServiceError(err error) error {
	if errors.Is(err, rumination.ErrNotFound) {
		return writeMaintenanceError("candidate_not_found", "rumination candidate not found")
	}
	return writeMaintenanceError("maintenance_failed", err.Error())
}

func writeMaintenanceError(code, message string) error {
	err := writeMaintenanceJSONDirect(maintenanceErrorEnvelope{
		ProtocolVersion: 1, Error: maintenanceErrorResponse{Code: code, Message: message},
	})
	if err != nil {
		return err
	}
	return maintenanceJSONError{cause: fmt.Errorf("%s: %s", code, message)}
}

func writeMaintenanceJSON(value any) error {
	var buf bytes.Buffer
	if err := encodeMaintenanceJSON(&buf, value); err != nil {
		return err
	}
	if buf.Len() > maintenanceOutputLimit {
		return writeMaintenanceError("response_too_large", "maintenance JSON response exceeds output limit")
	}
	_, err := os.Stdout.Write(buf.Bytes())
	return err
}

func writeMaintenanceJSONDirect(value any) error {
	var buf bytes.Buffer
	if err := encodeMaintenanceJSON(&buf, value); err != nil {
		return err
	}
	_, err := os.Stdout.Write(buf.Bytes())
	return err
}

func encodeMaintenanceJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}
