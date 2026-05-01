package embedrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DashScopeEmbedder calls DashScope's OpenAI-compatible /embeddings
// endpoint. Used for the cloud spike of the embedding router; the
// long-term target is local MLX so latency drops to ~10-30ms.
//
// text-embedding-v3 is multilingual (zh+en), 1024-dim by default.
type DashScopeEmbedder struct {
	BaseURL string
	APIKey  string
	Model   string
	Client  *http.Client
}

// NewDashScopeEmbedder constructs an embedder. baseURL should NOT include
// /embeddings; this method appends it.
func NewDashScopeEmbedder(baseURL, apiKey, model string) *DashScopeEmbedder {
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	if model == "" {
		model = "text-embedding-v3"
	}
	return &DashScopeEmbedder{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		Client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed sends one request per batch and returns one vector per input.
// DashScope caps batch size at 25 inputs, so we chunk.
func (d *DashScopeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const batchSize = 25
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := d.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (d *DashScopeEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	payload := map[string]any{
		"model":           d.Model,
		"input":           texts,
		"encoding_format": "float",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.BaseURL+"/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dashscope /embeddings status=%d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal: %w (body=%s)", err, string(body))
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("dashscope returned %d embeddings for %d inputs", len(parsed.Data), len(texts))
	}
	// Sort by index so output order matches input order.
	out := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("dashscope returned out-of-range index %d", item.Index)
		}
		out[item.Index] = item.Embedding
	}
	return out, nil
}
