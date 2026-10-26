package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// TestDispatchJSONEnvelopeExitStatus pins the two machine-readable surfaces that
// do not use the plain usage taxonomy: continuity answers a malformed request
// in its v1 envelope and exits 2, while maintenance keeps its long-standing
// exit 1 because its envelope already carries the failure.
func TestDispatchJSONEnvelopeExitStatus(t *testing.T) {
	t.Run("continuity usage is 2", func(t *testing.T) {
		home := isolatedHome(t)
		var stdout, stderr bytes.Buffer
		var code int
		written := captureStdout(t, func() {
			code = dispatch(context.Background(), []string{"continuity"}, &stdout, &stderr)
		})
		if code != exitUsage {
			t.Fatalf("exit = %d, want %d", code, exitUsage)
		}
		var envelope map[string]any
		if err := json.Unmarshal([]byte(written), &envelope); err != nil {
			t.Fatalf("continuity must answer in its JSON envelope: %q: %v", written, err)
		}
		if envelope["protocol_version"] != float64(1) {
			t.Fatalf("envelope = %v", envelope)
		}
		if stderr.Len() != 0 {
			t.Fatalf("a written envelope must not add stderr prose: %q", stderr.String())
		}
		assertHomeEmpty(t, home)
	})

	t.Run("maintenance usage stays 1", func(t *testing.T) {
		home := isolatedHome(t)
		var stdout, stderr bytes.Buffer
		var code int
		written := captureStdout(t, func() {
			code = dispatch(context.Background(), []string{"maintenance", "nope"}, &stdout, &stderr)
		})
		if code != exitFailure {
			t.Fatalf("exit = %d, want %d", code, exitFailure)
		}
		var envelope maintenanceErrorEnvelope
		if err := json.Unmarshal([]byte(written), &envelope); err != nil {
			t.Fatalf("maintenance must answer in its JSON envelope: %q: %v", written, err)
		}
		if envelope.Error.Code != "unknown_command" {
			t.Fatalf("envelope = %+v", envelope)
		}
		if stderr.Len() != 0 {
			t.Fatalf("a written envelope must not add stderr prose: %q", stderr.String())
		}
		assertHomeEmpty(t, home)
	})
}
