package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// Backend selects the HTTP protocol for text generation.
type Backend string

const (
	BackendOllama        Backend = "ollama"
	BackendOpenAICompat  Backend = "openai_compat" // Alibaba DashScope compatible-mode, OpenAI-style /chat/completions
)

type Provider struct {
	modelName  string
	baseURL    string
	apiKey     string
	backend    Backend
	client     *http.Client
	mu         sync.Mutex
	isReady    bool
	lastUsed   time.Time
	ttl        time.Duration
	reqCount   atomic.Int64
	errCount   atomic.Int64
	maxRetries int

	disk        *diskCache
	cacheHits   atomic.Uint64
	cacheMisses atomic.Uint64
	sf          singleflight.Group
}

// Option configures Provider construction.
type Option func(*Provider)

// WithResponseCache enables a disk-backed cache of LLM responses (same prompt+context+model+params → no API call).
func WithResponseCache(dir string, enabled bool) Option {
	return func(p *Provider) {
		if !enabled || dir == "" {
			return
		}
		_ = os.MkdirAll(dir, 0755)
		p.disk = newDiskCache(dir)
	}
}

type GenerationConfig struct {
	MaxTokens int
	Temp      float64
	Model     string // Optional model override for this specific request
}

func NewProvider(modelName, baseURL, apiKey string, backend Backend, opts ...Option) *Provider {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	p := &Provider{
		modelName:  modelName,
		baseURL:    baseURL,
		apiKey:     apiKey,
		backend:    backend,
		maxRetries: 3,
		ttl:        30 * time.Minute,
		client: &http.Client{
			Timeout:   300 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	for _, o := range opts {
		o(p)
	}

	go func() {
		if err := p.checkReady(); err != nil {
			log.Printf("[LLM] Warmup check: %v (will retry on first request)", err)
		} else {
			p.isReady = true
			log.Printf("[LLM] Backend=%s model=%s base=%s", backend, modelName, baseURL)
		}
	}()

	return p
}

func (p *Provider) checkReady() error {
	switch p.backend {
	case BackendOpenAICompat:
		if p.apiKey == "" {
			return fmt.Errorf("missing DashScope API key")
		}
		return nil
	default: // Ollama
		req, err := http.NewRequest("GET", p.baseURL+"/api/tags", nil)
		if err != nil {
			return err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			return fmt.Errorf("cannot reach model server: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("model server returned %d", resp.StatusCode)
		}
		return nil
	}
}

// polishAssistantResponse trims whitespace and removes a single outer markdown code fence if the model wrapped prose in ```.
func polishAssistantResponse(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, "```"))
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		first := strings.TrimSpace(rest[:nl])
		if first == "json" || first == "markdown" || first == "md" || first == "txt" {
			rest = rest[nl+1:]
		}
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = strings.TrimSpace(rest[:i])
	}
	return strings.TrimSpace(rest)
}

func (p *Provider) SetBaseURL(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.baseURL = url
}

func (p *Provider) Generate(prompt, context string, config GenerationConfig) (string, error) {
	p.mu.Lock()
	p.lastUsed = time.Now()
	p.mu.Unlock()

	p.reqCount.Add(1)

	if p.disk != nil {
		key := p.cacheKeyHex(prompt, context, config)
		if b, ok := p.disk.get(key); ok {
			p.cacheHits.Add(1)
			return string(b), nil
		}
		p.cacheMisses.Add(1)

		v, err, _ := p.sf.Do(key, func() (interface{}, error) {
			if b, ok := p.disk.get(key); ok {
				return string(b), nil
			}
			s, err := p.generateUncached(prompt, context, config)
			if err != nil {
				return "", err
			}
			if err := p.disk.set(key, []byte(s)); err != nil {
				log.Printf("[LLM] cache write: %v", err)
			}
			return s, nil
		})
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}

	return p.generateUncached(prompt, context, config)
}

func (p *Provider) generateUncached(prompt, context string, config GenerationConfig) (string, error) {
	switch p.backend {
	case BackendOpenAICompat:
		return p.generateOpenAICompatChat(prompt, context, config)
	}

	if !p.isReady {
		if err := p.checkReady(); err != nil {
			return "", fmt.Errorf("model server unavailable: %w", err)
		}
		p.isReady = true
	}

	messages := []map[string]string{
		{"role": "user", "content": buildOllamaUserContentForKey(prompt, context)},
	}

	modelToUse := p.modelName
	if config.Model != "" {
		modelToUse = config.Model
	}

	payload := map[string]interface{}{
		"model":    modelToUse,
		"messages": messages,
		"stream":   false,
		"options": map[string]interface{}{
			"num_predict": config.MaxTokens,
			"temperature": config.Temp,
		},
	}

	var lastErr error
	for attempt := 0; attempt < p.maxRetries; attempt++ {
		result, err := p.doGenerateOllama(payload)
		if err == nil {
			return polishAssistantResponse(result), nil
		}

		lastErr = err
		p.errCount.Add(1)
		log.Printf("[LLM] Request failed (attempt %d/%d): %v", attempt+1, p.maxRetries, err)

		if attempt < p.maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}

		p.isReady = false
		if checkErr := p.checkReady(); checkErr != nil {
			log.Printf("[LLM] Model server health check failed: %v", checkErr)
		} else {
			p.isReady = true
		}
	}

	return "", fmt.Errorf("all %d attempts failed, last error: %w", p.maxRetries, lastErr)
}

func (p *Provider) generateOpenAICompatChat(prompt, context string, config GenerationConfig) (string, error) {
	maxTok := config.MaxTokens
	if maxTok <= 0 {
		maxTok = 2048
	}
	temp := config.Temp
	if temp <= 0 {
		temp = 0.1
	}

	modelToUse := p.modelName
	if config.Model != "" {
		modelToUse = config.Model
	}

	userContent := buildOpenAICompatUserContentForKey(prompt, context)

	messages := []map[string]interface{}{
		{"role": "system", "content": openAISystemMessage},
		{"role": "user", "content": userContent},
	}

	payload := map[string]interface{}{
		"model":       modelToUse,
		"messages":    messages,
		"max_tokens":  maxTok,
		"temperature": temp,
	}

	var lastErr error
	for attempt := 0; attempt < p.maxRetries; attempt++ {
		result, err := p.doGenerateOpenAICompat(payload)
		if err == nil {
			return polishAssistantResponse(result), nil
		}
		lastErr = err
		p.errCount.Add(1)
		log.Printf("[LLM] DashScope request failed (attempt %d/%d): %v", attempt+1, p.maxRetries, err)
		if attempt < p.maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return "", fmt.Errorf("DashScope: all %d attempts failed, last error: %w", p.maxRetries, lastErr)
}

func (p *Provider) doGenerateOpenAICompat(payload map[string]interface{}) (string, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal failed: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal failed: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response: %s", string(body))
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if out == "" {
		return "", fmt.Errorf("empty assistant content: %s", string(body))
	}
	return out, nil
}

func (p *Provider) doGenerateOllama(payload map[string]interface{}) (string, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal failed: %w", err)
	}
	
	req, err := http.NewRequest("POST", p.baseURL+"/api/chat", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("request creation failed: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response failed: %w", err)
	}
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	
	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unmarshal failed: %w", err)
	}
	
	return result.Message.Content, nil
}

func (p *Provider) GenerateJSON(prompt, context string, config GenerationConfig, defaultResult interface{}) (interface{}, error) {
	result, err := p.Generate(prompt, context, config)
	if err != nil {
		return defaultResult, err
	}
	
	var parsed interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return defaultResult, fmt.Errorf("JSON parse failed: %w", err)
	}
	
	return parsed, nil
}

func (p *Provider) Unload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isReady = false
}

func (p *Provider) Stats() map[string]interface{} {
	out := map[string]interface{}{
		"model":          p.modelName,
		"base_url":       p.baseURL,
		"backend":        string(p.backend),
		"is_ready":       p.isReady,
		"req_count":      p.reqCount.Load(),
		"err_count":      p.errCount.Load(),
		"last_used":      p.lastUsed.Format(time.RFC3339),
		"max_retries":    p.maxRetries,
		"cache_enabled":  p.disk != nil,
		"cache_hits":     p.cacheHits.Load(),
		"cache_misses":   p.cacheMisses.Load(),
	}
	if p.disk != nil {
		out["cache_dir"] = p.disk.dir
	}
	return out
}
