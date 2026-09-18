package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	// The full-endpoint spelling is the mistake worth absorbing: Embed appends
	// "/embeddings" itself, so leaving it in produces a doubled path whose only
	// symptom is that vector search never turns on.
	cases := map[string]string{
		"http://host/v1":                 "http://host/v1",
		"http://host/v1/":                "http://host/v1",
		"http://host/v1/embeddings":      "http://host/v1",
		"http://host/v1/embeddings/":     "http://host/v1",
		"http://host:8090/v1/embeddings": "http://host:8090/v1",
		"  http://host/v1  ":             "http://host/v1",
		"https://api.openai.com/v1":      "https://api.openai.com/v1",
		"":                               "",
		"   ":                            "",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewOpenAIStripsTheEndpointSuffix(t *testing.T) {
	// The constructor is where an operator's config lands, so the normalization
	// has to happen by the time Embed builds its URL.
	o := NewOpenAI(OpenAIConfig{BaseURL: "http://host:8090/v1/embeddings"})
	if o.baseURL != "http://host:8090/v1" {
		t.Fatalf("baseURL = %q, want the /v1 root", o.baseURL)
	}
}

func TestEmbedAppendsTheEndpointExactlyOnce(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	// Configured with the full endpoint on purpose: this is the shape that
	// produced /v1/embeddings/embeddings before.
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1/embeddings", Dimension: 2})
	vec, err := o.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/v1/embeddings" {
		t.Errorf("requested %q, want /v1/embeddings", gotPath)
	}
	if len(vec) != 2 {
		t.Errorf("got %d values, want 2", len(vec))
	}
}

func TestProbeOpenAIDistinguishesTheThreeCases(t *testing.T) {
	// A GET is what the probe sends, so the handler answers GET; the status is
	// the whole input to the decision.
	cases := []struct {
		name string
		code int
		want bool
	}{
		{"route exists", http.StatusOK, true},
		// OpenAI answers a GET on /v1/embeddings with 405; a keyless-but-working
		// server must not read as absent.
		{"method not allowed", http.StatusMethodNotAllowed, true},
		// A key that is missing or wrong is a first-Embed problem, not an
		// "is anything there" problem.
		{"unauthorized", http.StatusUnauthorized, true},
		{"forbidden", http.StatusForbidden, true},
		{"route missing", http.StatusNotFound, false},
		{"server error", http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/embeddings" {
					t.Errorf("probe hit %q, want /v1/embeddings", r.URL.Path)
				}
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()

			if got := ProbeOpenAI(context.Background(), srv.URL+"/v1", ""); got != tc.want {
				t.Errorf("code %d: ProbeOpenAI = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestProbeOpenAIFailsOnAnUnreachableEndpoint(t *testing.T) {
	// The whole point of the probe is to not enable hybrid search against
	// nothing, so a transport error has to read as absent rather than panic or
	// hang.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // now nothing is listening

	if ProbeOpenAI(context.Background(), srv.URL+"/v1", "") {
		t.Error("a closed server must probe false")
	}
}

func TestProbeOpenAIWithoutABaseURLDoesNothing(t *testing.T) {
	if ProbeOpenAI(context.Background(), "", "") {
		t.Error("an empty base_url must probe false")
	}
}

func TestProbeOpenAISendsTheAPIKey(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ProbeOpenAI(context.Background(), srv.URL+"/v1", "secret-key")
	if seen != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want the bearer key", seen)
	}
}
