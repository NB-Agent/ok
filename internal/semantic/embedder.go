package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/NB-Agent/ok/internal/log"
)

// Embedder talks to a local Ollama instance to generate text embeddings.
type Embedder struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewEmbedder creates an embedder. baseURL defaults to "http://localhost:11434",
// model defaults to "nomic-embed-text".
func NewEmbedder(baseURL, model string) *Embedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "nomic-embed-text"
	}
	return &Embedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Healthy reports whether Ollama is reachable and the model is available.
func (e *Embedder) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", e.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return false
	}
	defer log.Close("embedder response", resp.Body)
	if resp.StatusCode != 200 {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	return strings.Contains(string(body), e.model)
}

// Embed converts text to a vector. Returns nil on any error.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	payload := map[string]string{
		"model":  e.model,
		"prompt": text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("embedder marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx,
		"POST", e.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedder request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedder request failed: %w", err)
	}
	defer log.Close("embedder response", resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("embedder HTTP %d", resp.StatusCode)
	}

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embedder decode: %w", err)
	}
	return result.Embedding, nil
}

// InstallInstructions returns a human-readable string telling how to install Ollama.
func InstallInstructions() string {
	return fmt.Sprintf("To enable semantic search:\n" +
		"  1. Install Ollama: https://ollama.com/download\n" +
		"  2. Pull the embedding model: ollama pull nomic-embed-text\n" +
		"  3. Run ollama serve")
}
