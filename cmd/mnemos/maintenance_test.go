package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestDecodeMaintenancePayloadPreservesUnicodeAndEscapes(t *testing.T) {
	payload := `{"candidate_id":"候选 \"甲\"\n第二行","resolved_by":"revision-1","why_better":"新预测说明中文结果"}`
	var got resolveDecisionPayload
	if err := decodeMaintenancePayload(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.CandidateID != "候选 \"甲\"\n第二行" {
		t.Fatalf("candidate_id = %q", got.CandidateID)
	}
	if got.WhyBetter != "新预测说明中文结果" {
		t.Fatalf("why_better = %q", got.WhyBetter)
	}
}

func TestMaintenanceCLIResolveUsesSharedValidationAndStableJSON(t *testing.T) {
	tests := []struct {
		name       string
		whyBetter  string
		wantStatus rumination.Status
		wantError  string
	}{
		{name: "accepted", whyBetter: "新预测会在后续任务中稳定覆盖这个具体反例", wantStatus: rumination.StatusResolved},
		{name: "rejected", whyBetter: "太短", wantStatus: rumination.StatusPending, wantError: "min 16 chars"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, configPath, dbPath := newMaintenanceCLITestConfig(t)
			seedMaintenanceCandidate(t, ctx, dbPath)
			payload, err := json.Marshal(resolveDecisionPayload{
				CandidateID: "candidate-1", ResolvedBy: "revision-1", WhyBetter: tc.whyBetter,
			})
			if err != nil {
				t.Fatal(err)
			}

			output, commandErr := captureMaintenanceStdout(t, func() error {
				return runMaintenance(ctx, []string{"resolve", "--payload", string(payload)})
			})
			var envelope struct {
				ProtocolVersion int    `json:"protocol_version"`
				Command         string `json:"command"`
				ID              string `json:"id"`
				Status          string `json:"status"`
				Error           *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(output), &envelope); err != nil {
				t.Fatalf("invalid JSON response %q: %v", output, err)
			}
			if tc.wantError == "" {
				if envelope.ProtocolVersion != 1 || envelope.Command != "resolve" || envelope.ID != "candidate-1" || commandErr != nil || envelope.Status != string(tc.wantStatus) || envelope.Error != nil {
					t.Fatalf("success response=%+v error=%v", envelope, commandErr)
				}
			} else if envelope.ProtocolVersion != 1 || commandErr == nil || envelope.Error == nil || !strings.Contains(envelope.Error.Message, tc.wantError) {
				t.Fatalf("rejected response=%+v error=%v", envelope, commandErr)
			}

			db, err := storage.Open(context.Background(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			candidate, err := db.Rumination().Get(context.Background(), "candidate-1")
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Status != tc.wantStatus {
				t.Fatalf("candidate status = %q, want %q (config %s)", candidate.Status, tc.wantStatus, configPath)
			}
		})
	}
}

func TestMaintenanceCLIListAndEvidenceReturnJSON(t *testing.T) {
	ctx, _, dbPath := newMaintenanceCLITestConfig(t)
	seedMaintenanceCandidate(t, ctx, dbPath)

	listOutput, err := captureMaintenanceStdout(t, func() error {
		return runMaintenance(ctx, []string{"list", "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listed maintenanceListResponse
	if err := json.Unmarshal([]byte(listOutput), &listed); err != nil {
		t.Fatalf("list output is invalid JSON %q: %v", listOutput, err)
	}
	if listed.ProtocolVersion != 1 || listed.Command != "list" || len(listed.Candidates) != 1 || listed.Candidates[0].ID != "candidate-1" {
		t.Fatalf("list response = %+v", listed)
	}

	evidenceOutput, err := captureMaintenanceStdout(t, func() error {
		return runMaintenance(ctx, []string{"evidence", "--id", "candidate-1"})
	})
	if err != nil {
		t.Fatalf("evidence: %v", err)
	}
	var evidence maintenanceEvidenceResponse
	if err := json.Unmarshal([]byte(evidenceOutput), &evidence); err != nil {
		t.Fatalf("evidence output is invalid JSON %q: %v", evidenceOutput, err)
	}
	if evidence.ProtocolVersion != 1 || evidence.Command != "evidence" || evidence.Candidate.ID != "candidate-1" || evidence.Target.ID == "" || !strings.Contains(evidence.Text, "Fixture skill") {
		t.Fatalf("evidence response = %+v", evidence)
	}
}

func TestMaintenanceCLIDismissUsesSharedValidationAndPersistsReason(t *testing.T) {
	tests := []struct {
		name      string
		reason    string
		wantState rumination.Status
		wantError string
	}{
		{name: "accepted", reason: "证据只是一次性环境异常", wantState: rumination.StatusDismissed},
		{name: "rejected", reason: "短", wantState: rumination.StatusPending, wantError: "at least 8 chars"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, dbPath := newMaintenanceCLITestConfig(t)
			seedMaintenanceCandidate(t, ctx, dbPath)
			payload, err := json.Marshal(dismissDecisionPayload{CandidateID: "candidate-1", Reason: tc.reason})
			if err != nil {
				t.Fatal(err)
			}
			output, commandErr := captureMaintenanceStdout(t, func() error {
				return runMaintenance(ctx, []string{"dismiss", "--payload", string(payload)})
			})
			if tc.wantError == "" {
				var response maintenanceDecisionResponse
				if commandErr != nil || json.Unmarshal([]byte(output), &response) != nil || response.Status != string(tc.wantState) || response.Reason != tc.reason {
					t.Fatalf("response=%q parsed=%+v error=%v", output, response, commandErr)
				}
			} else {
				var envelope maintenanceErrorEnvelope
				if commandErr == nil || json.Unmarshal([]byte(output), &envelope) != nil || !strings.Contains(envelope.Error.Message, tc.wantError) {
					t.Fatalf("error response=%q parsed=%+v error=%v", output, envelope, commandErr)
				}
			}

			db, err := storage.Open(context.Background(), dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			candidate, err := db.Rumination().Get(context.Background(), "candidate-1")
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Status != tc.wantState || (tc.wantError == "" && candidate.DismissedReason != tc.reason) {
				t.Fatalf("dismissed candidate = %+v", candidate)
			}
		})
	}
}

func newMaintenanceCLITestConfig(t *testing.T) (context.Context, string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	dbPath := filepath.Join(dir, "mnemos.db")
	cfg := config.Default()
	cfg.Storage.Path = dbPath
	cfg.Embedding.Provider = "none"
	cfg.MCP.Profile = "core"
	cfg.Rumination.Enabled = false
	f, err := os.Create(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return config.ContextWithPath(context.Background(), configPath), configPath, dbPath
}

func seedMaintenanceCandidate(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	skill, err := db.Skills().Upsert(ctx, skills.SaveInput{Name: "Fixture skill", Procedure: "Do the fixture operation."})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Rumination().Upsert(ctx, rumination.Candidate{
		ID: "candidate-1", MonitorName: "fixture", Severity: rumination.SeverityMedium, TargetKind: rumination.TargetSkill,
		TargetID: skill.ID, Reason: "fixture evidence", Evidence: []rumination.Evidence{{Label: "test", Content: "detail"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func captureMaintenanceStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := run()
	_ = w.Close()
	os.Stdout = old
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), runErr
}

func TestDecodeMaintenancePayloadIsStrictAndBounded(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "empty", want: "required"},
		{name: "unknown field", payload: `{"candidate_id":"x","resolved_by":"r","why_better":"a sufficiently long prediction","extra":true}`, want: "unknown field"},
		{name: "trailing object", payload: `{"candidate_id":"x","resolved_by":"r","why_better":"a sufficiently long prediction"} {}`, want: "exactly one"},
		{name: "null is not an object", payload: `null`, want: "object"},
		{name: "oversized", payload: strings.Repeat(" ", maintenancePayloadLimit+1), want: "exceeds"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got resolveDecisionPayload
			err := decodeMaintenancePayload(tc.payload, &got)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decode error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestMaintenanceJSONErrorsHaveNonzeroExitWithoutTextStderr(t *testing.T) {
	output, err := captureMaintenanceStdout(t, func() error {
		return runMaintenance(context.Background(), []string{"not-a-command"})
	})
	if err == nil {
		t.Fatal("unknown command should return an error")
	}
	var envelope maintenanceErrorEnvelope
	if jsonErr := json.Unmarshal([]byte(output), &envelope); jsonErr != nil {
		t.Fatalf("error response is not one JSON object: %q: %v", output, jsonErr)
	}
	if envelope.ProtocolVersion != 1 || envelope.Error.Code != "unknown_command" {
		t.Fatalf("error response = %+v", envelope)
	}
	var stderr bytes.Buffer
	if code := commandFailure("maintenance", err, &stderr); code == 0 || stderr.Len() != 0 {
		t.Fatalf("exit code=%d stderr=%q; want nonzero without appended text", code, stderr.String())
	}
}

func TestMaintenanceCLIRejectsInputsBeforeOpeningStorage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing payload fields", args: []string{"resolve", "--payload", `{}`}},
		{name: "invalid payload shape", args: []string{"dismiss", "--payload", `{"candidate_id":"x","reason":"long enough reason","extra":1}`}},
		{name: "wrong action flag", args: []string{"evidence", "--id", "x", "--limit", "100"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, dbPath := newMaintenanceCLITestConfig(t)
			output, err := captureMaintenanceStdout(t, func() error { return runMaintenance(ctx, tc.args) })
			if err == nil {
				t.Fatal("invalid command should fail")
			}
			var envelope maintenanceErrorEnvelope
			if decodeErr := json.Unmarshal([]byte(output), &envelope); decodeErr != nil {
				t.Fatalf("error output is not one JSON object: %q: %v", output, decodeErr)
			}
			if envelope.ProtocolVersion != 1 || envelope.Error.Code == "" {
				t.Fatalf("error envelope = %+v", envelope)
			}
			if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
				t.Fatalf("invalid input opened or created storage at %q (stat err %v)", dbPath, statErr)
			}
		})
	}
}

func TestMaintenanceOversizedResponseReturnsSmallJSONError(t *testing.T) {
	output, err := captureMaintenanceStdout(t, func() error {
		return writeMaintenanceJSON(struct {
			Value string `json:"value"`
		}{Value: strings.Repeat("x", maintenanceOutputLimit)})
	})
	if err == nil {
		t.Fatal("oversized response should fail")
	}
	if len(output) > 1024 {
		t.Fatalf("oversize error response is %d bytes", len(output))
	}
	var envelope maintenanceErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(output), &envelope); decodeErr != nil {
		t.Fatalf("oversize response is not JSON: %q: %v", output, decodeErr)
	}
	if envelope.Error.Code != "response_too_large" {
		t.Fatalf("error code = %q", envelope.Error.Code)
	}
}
