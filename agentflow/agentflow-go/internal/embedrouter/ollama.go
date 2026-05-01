package embedrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaEmbedder calls Ollama's /api/embed endpoint (newer batch form).
// Default URL is http://localhost:11434. Default model is "bge-m3" — a
// 568M-param multilingual model that handles zh+en mixed input cleanly.
type OllamaEmbedder struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

// NewOllamaEmbedder returns an embedder with sensible defaults.
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if model == "" {
		model = "bge-m3"
	}
	return &OllamaEmbedder{
		BaseURL: baseURL,
		Model:   model,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed implements Embedder. Sends one request with the whole batch;
// Ollama's /api/embed accepts a string array.
func (o *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	payload := map[string]any{
		"model": o.Model,
		"input": texts,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", o.BaseURL+"/api/embed", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/embed status=%d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal: %w (body=%s)", err, string(body))
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs", len(parsed.Embeddings), len(texts))
	}
	return parsed.Embeddings, nil
}
