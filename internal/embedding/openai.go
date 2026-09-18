package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAI is an embedder for OpenAI-compatible /v1/embeddings endpoints.
// Works with OpenAI proper, Together.ai, vLLM, LM Studio, etc.
type OpenAI struct {
	client    *http.Client
	baseURL   string
	apiKey    string
	model     string
	dimension int
}

// OpenAIConfig bundles OpenAI-compatible provider settings.
type OpenAIConfig struct {
	BaseURL   string // e.g. "https://api.openai.com/v1"
	APIKey    string // Authorization: Bearer <key>
	Model     string // e.g. "text-embedding-3-small"
	Dimension int    // e.g. 1536
	Timeout   time.Duration
}

// normalizeBaseURL makes base_url mean the same thing whether the operator
// wrote the API root ("http://host/v1") or the full endpoint
// ("http://host/v1/embeddings").
//
// Embed POSTs to baseURL + "/embeddings", so the full-endpoint spelling would
// otherwise become ".../embeddings/embeddings" and 404. That mistake is worth
// absorbing here rather than documenting away: its only symptom is that
// vector search never turns on, with nothing in the output to say why.
func normalizeBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	u = strings.TrimSuffix(u, "/embeddings")
	return strings.TrimRight(u, "/")
}

// NewOpenAI constructs an OpenAI-compatible embedder.
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	cfg.BaseURL = normalizeBaseURL(cfg.BaseURL)
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}
	if cfg.Dimension == 0 {
		cfg.Dimension = 1536
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &OpenAI{
		client:    &http.Client{Timeout: cfg.Timeout},
		baseURL:   cfg.BaseURL,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		dimension: cfg.Dimension,
	}
}

// Embed POSTs text to /v1/embeddings and returns the first vector.
func (o *OpenAI) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"model":      o.model,
		"input":      text,
		"dimensions": o.dimension,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openai: status %d", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("openai: empty data array")
	}
	return out.Data[0].Embedding, nil
}

// Dimension returns the configured vector length.
func (o *OpenAI) Dimension() int { return o.dimension }

// Model returns a qualified identifier.
func (o *OpenAI) Model() string { return "openai/" + o.model }

// probeTimeout bounds the reachability probe. Short on purpose: this runs on
// every CLI invocation, so a slow endpoint must not make the whole command
// feel slow.
const probeTimeout = 1500 * time.Millisecond

// ProbeOpenAI reports whether an OpenAI-compatible embedding endpoint looks
// reachable at baseURL.
//
// It sends a GET, not the POST that would settle the question definitively,
// because of where this runs: selectEmbedder calls it for provider="auto" on
// every command, and a real embedding POST costs an inference. The status code
// is enough to separate the three cases that matter — the route exists (200,
// and 401/403/405, which is what OpenAI itself answers a GET with, auth
// problems included), the route does not exist (404), or nothing is listening
// (transport error).
//
// The cost of accepting 401/403 as "reachable" is that a wrong API key is
// discovered at the first real Embed rather than here; the cost of not doing so
// is that a keyless-but-working endpoint reads as absent.
func ProbeOpenAI(ctx context.Context, baseURL, apiKey string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	url := normalizeBaseURL(baseURL)
	if url == "" {
		return false
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url+"/embeddings", nil)
	if err != nil {
		return false
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := (&http.Client{Timeout: probeTimeout}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false
	case resp.StatusCode >= 500:
		return false
	default:
		return true
	}
}
