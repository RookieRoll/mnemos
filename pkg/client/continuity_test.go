package client_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestContinuityMutationNeverConfusesMalformedReceiptWithFailureToCommit(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"legacy", `{"session_id":"old"}`},
		{"version", `{"protocol_version":2,"status":"applied","session_id":"s","started_at":"2026-10-19T00:00:00Z","context_status":"available"}`},
		{"empty_identity", `{"protocol_version":1,"status":"applied","session_id":" ","started_at":"2026-10-19T00:00:00Z","context_status":"available"}`},
		{"truncated", `{"protocol_version":1,"status":`},
		{"missing_context", `{"protocol_version":1,"status":"applied","session_id":"s","started_at":"2026-10-19T00:00:00Z","context_status":"available"}`},
		{"null_context", `{"protocol_version":1,"status":"applied","session_id":"s","started_at":"2026-10-19T00:00:00Z","context_status":"available","prewarm":null}`},
		{"trailing_json", `{"protocol_version":1,"status":"applied","session_id":"s","started_at":"2026-10-19T00:00:00Z","context_status":"available","prewarm":""} {}`},
		{"trailing_garbage", `{"protocol_version":1,"status":"applied","session_id":"s","started_at":"2026-10-19T00:00:00Z","context_status":"available","prewarm":""} garbage`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; _, _ = io.WriteString(w, tc.body) }))
			defer srv.Close()
			_, err := client.New(srv.URL).ContinuityOpen(t.Context(), client.ContinuityOpenInput{ProtocolVersion: 1, HarnessKind: "test", StartKey: "same"})
			var unknown *client.OutcomeUnknownError
			if !errors.As(err, &unknown) || calls != 1 {
				t.Fatalf("must preserve unknown outcome without implicit retry: calls=%d err=%v", calls, err)
			}
		})
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("response lost after dispatch")
}

func TestContinuityNetworkFailureIsUnknownAndServerConflictIsExplicit(t *testing.T) {
	c := client.New("http://example.invalid", client.WithHTTPClient(&http.Client{Transport: failingRoundTripper{}}))
	_, err := c.ContinuityCheckpoint(context.Background(), client.ContinuityCheckpointInput{ProtocolVersion: 1, SessionID: "s"})
	var unknown *client.OutcomeUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("network failure became a persistence verdict: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"protocol_version":1,"code":"conflict","current_revision":2}`)
	}))
	defer srv.Close()
	_, err = client.New(srv.URL).ContinuityCheckpoint(t.Context(), client.ContinuityCheckpointInput{})
	if !client.IsConflict(err) || errors.As(err, &unknown) {
		t.Fatalf("server rejection lost semantics: %v", err)
	}
}

func TestContinuityExplicitRetryPreservesWireRequest(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		status := "applied"
		if len(bodies) > 1 {
			status = "replayed"
		}
		_, _ = io.WriteString(w, `{"protocol_version":1,"status":"`+status+`","session_id":"session","revision":1,"updated_at":"2026-10-19T00:00:00Z"}`)
	}))
	defer srv.Close()
	zero := int64(0)
	in := client.ContinuityCheckpointInput{ProtocolVersion: 1, SessionID: "session", Action: "save", ExpectedRevision: &zero, RequestID: "stable", Visibility: "workspace", Payload: &client.ContinuityPayload{Task: "t", State: "active", Summary: "进度"}}
	c := client.New(srv.URL)
	first, err := c.ContinuityCheckpoint(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.ContinuityCheckpoint(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[0], `"expected_revision":0`) || first.Status != "applied" || second.Status != "replayed" || first.UpdatedAt != second.UpdatedAt {
		t.Fatalf("retry identity lost: %#v %#v %v", first, second, bodies)
	}
}

func TestContinuityProxyFailureAndWrongRevisionRemainUnknown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"proxy", 502, "upstream disconnected"},
		{"storage", 500, `{"protocol_version":1,"code":"storage_failure"}`},
		{"invalid_rejection", 409, `{"code":"conflict"}`},
		{"wrong_revision", 200, `{"protocol_version":1,"status":"applied","session_id":"s","revision":99,"updated_at":"2026-10-19T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			zero := int64(0)
			_, err := client.New(srv.URL).ContinuityCheckpoint(t.Context(), client.ContinuityCheckpointInput{ProtocolVersion: 1, SessionID: "s", ExpectedRevision: &zero})
			var unknown *client.OutcomeUnknownError
			if !errors.As(err, &unknown) {
				t.Fatalf("post-dispatch uncertainty lost: %v", err)
			}
		})
	}
}

func TestContinuityCapabilitiesRequiresExplicitSupportBoolean(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"protocol_version":1,"supported":false}`, true},
		{`{"protocol_version":1}`, false},
		{`{"protocol_version":1,"supported":null}`, false},
		{`{"protocol_version":1,"supported":true} {}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer srv.Close()
			out, err := client.New(srv.URL).ContinuityCapabilities(t.Context())
			if tc.valid && (err != nil || out.Supported) {
				t.Fatalf("explicit unsupported lost: %#v %v", out, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("malformed discovery treated as definite support: %#v", out)
			}
		})
	}
}
