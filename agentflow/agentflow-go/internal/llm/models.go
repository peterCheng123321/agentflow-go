package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ModelInfo describes an available model
type ModelInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Backend     string  `json:"backend"`
	Size        int64   `json:"size_bytes,omitempty"`
	Modified    string  `json:"modified,omitempty"`
	IsDefault   bool    `json:"is_default"`
	Description string  `json:"description,omitempty"`
}

// BenchmarkResult contains timing and metadata from a benchmark run
type BenchmarkResult struct {
	Model        string        `json:"model"`
	Backend      string        `json:"backend"`
	Success      bool          `json:"success"`
	LatencyMs    float64       `json:"latency_ms"`
	TokensPerSec float64       `json:"tokens_per_sec"`
	Error        string        `json:"error,omitempty"`
	Timestamp    string        `json:"timestamp"`
}

// predefinedDashScopeModels lists the known DashScope (Alibaba Cloud) models.
// DashScope does not provide a models list API; these are hardcoded based on documentation.
var predefinedDashScopeModels = []ModelInfo{
	// Text models - user specified
	{
		ID:          "qwen-plus",
		Name:        "Qwen Plus (qwen3.6-plus)",
		Backend:     "dashscope",
		IsDefault:   true,
		Description: "Balanced capability - use for COMPLEX tasks (legal analysis, drafting)",
	},
	{
		ID:          "qwen-turbo",
		Name:        "Qwen Turbo",
		Backend:     "dashscope",
		Description: "Fast responses for simple tasks",
	},
	{
		ID:          "qwen-max",
		Name:        "Qwen Max",
		Backend:     "dashscope",
		Description: "Highest capability for complex reasoning (slower, more expensive)",
	},
	{
		ID:          "qwen-long",
		Name:        "Qwen Long",
		Backend:     "dashscope",
		Description: "Extended context window (up to 1M tokens)",
	},
	// OCR/Vision models - user specified
	{
		ID:          "qwen-vl-ocr-latest",
		Name:        "Qwen VL OCR Latest",
		Backend:     "dashscope",
		Description: "OCR-optimized vision model - use for OCR tasks",
	},
	{
		ID:          "qwen-vl-plus",
		Name:        "Qwen VL Plus",
		Backend:     "dashscope",
		Description: "Vision-language model for image understanding",
	},
	{
		ID:          "qwen-vl-max",
		Name:        "Qwen VL Max",
		Backend:     "dashscope",
		Description: "Highest capability vision-language model",
	},
}

// predefinedDeepSeekModels lists the current DeepSeek API models.
// DeepSeek's API is OpenAI-compatible at https://api.deepseek.com/v1.
var predefinedDeepSeekModels = []ModelInfo{
	{
		ID:          "deepseek-chat",
		Name:        "DeepSeek Chat",
		Backend:     "deepseek",
		IsDefault:   true,
		Description: "Fast, general-purpose chat — no chain-of-thought (non-thinking).",
	},
	{
		ID:          "deepseek-reasoner",
		Name:        "DeepSeek Reasoner",
		Backend:     "deepseek",
		Description: "Reasoning model with visible chain-of-thought. Slower but better at hard problems.",
	},
}

// ListModels returns available models for the given backend.
// For Ollama, it queries the server's /api/tags endpoint.
// For DashScope, it returns the predefined list.
// For DeepSeek, it returns the predefined list (both share the OpenAI-compatible wire format).
func ListModels(backend Backend, baseURL string) ([]ModelInfo, error) {
	switch backend {
	case BackendOllama:
		return listOllamaModels(baseURL)
	case BackendOpenAICompat:
		// Disambiguate by base URL: DeepSeek shares the OpenAI-compat protocol.
		if strings.Contains(baseURL, "deepseek.com") {
			return predefinedDeepSeekModels, nil
		}
		return predefinedDashScopeModels, nil
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}

// listOllamaModels queries the Ollama server for installed models.
func listOllamaModels(baseURL string) ([]ModelInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name     string `json:"name"`
			Modified string `json:"modified"`
			Size     int64  `json:"size"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode ollama response: %w", err)
	}

	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		models = append(models, ModelInfo{
			ID:       m.Name,
			Name:     m.Name,
			Backend:  "ollama",
			Size:     m.Size,
			Modified: m.Modified,
		})
	}

	return models, nil
}

// Benchmark tests a model with a simple prompt and returns timing metrics.
func (p *Provider) Benchmark(modelID string) BenchmarkResult {
	start := time.Now()
	result := BenchmarkResult{
		Model:     modelID,
		Backend:   string(p.backend),
		Timestamp: start.UTC().Format(time.RFC3339),
	}

	// Use a simple prompt that should generate ~50-100 tokens
	prompt := "Count from 1 to 10, one number per line."
	config := GenerationConfig{
		Model:     modelID,
		MaxTokens: 100,
		Temp:      0.1,
	}

	response, err := p.generateUncached(prompt, "", config)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	latency := time.Since(start).Milliseconds()
	result.LatencyMs = float64(latency)

	// Rough token count estimation (1 token ≈ 4 chars for English)
	estimatedTokens := float64(len(response)) / 4.0
	if latency > 0 {
		result.TokensPerSec = (estimatedTokens / float64(latency)) * 1000
	}

	result.Success = true
	return result
}

// BenchmarkWithPrompt tests a model with a custom prompt and returns timing metrics.
func (p *Provider) BenchmarkWithPrompt(modelID, prompt, context string, maxTokens int, temp float64) BenchmarkResult {
	start := time.Now()
	result := BenchmarkResult{
		Model:     modelID,
		Backend:   string(p.backend),
		Timestamp: start.UTC().Format(time.RFC3339),
	}

	config := GenerationConfig{
		Model:     modelID,
		MaxTokens: maxTokens,
		Temp:      temp,
	}

	response, err := p.generateUncached(prompt, context, config)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	latency := time.Since(start).Milliseconds()
	result.LatencyMs = float64(latency)

	// Rough token count estimation
	estimatedTokens := float64(len(response)) / 4.0
	if latency > 0 {
		result.TokensPerSec = (estimatedTokens / float64(latency)) * 1000
	}

	result.Success = true
	return result
}
