package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// Embedder turns text into dense vectors for semantic memory retrieval/dedup.
type Embedder interface {
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model returns the embedding model id (for cache invalidation).
	Model() string
}

// HTTPEmbedder calls an OpenAI-compatible /embeddings endpoint (e.g. DashScope
// compatible-mode, OpenAI, SiliconFlow). coden's memory is small, so vectors are
// stored as SQLite blobs and compared by brute-force cosine — no vector DB dep.
type HTTPEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	httpCli *http.Client
}

// NewHTTPEmbedder builds an embedder. baseURL is the OpenAI-compatible base
// (…/v1); "/embeddings" is appended.
func NewHTTPEmbedder(baseURL, apiKey, model string) *HTTPEmbedder {
	return &HTTPEmbedder{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		httpCli: &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *HTTPEmbedder) Model() string { return e.model }

// IsConfigured reports whether the embedder has a usable endpoint + key.
func (e *HTTPEmbedder) IsConfigured() bool {
	return e.baseURL != "" && e.apiKey != "" && e.model != ""
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(embedRequest{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed: decode: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embed: got %d vectors for %d inputs", len(out.Data), len(texts))
	}
	// Order by index to be safe.
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

// Cosine returns the cosine similarity of two equal-length vectors in [-1, 1].
// Returns 0 for mismatched/empty vectors.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
