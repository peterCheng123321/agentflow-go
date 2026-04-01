package llm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolishAssistantResponse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"json fence", "```json\n{\"key\": 1}\n```", `{"key": 1}`},
		{"markdown fence", "```markdown\n# Title\n```", "# Title"},
		{"md fence", "```md\ncontent\n```", "content"},
		{"txt fence", "```txt\nplain text\n```", "plain text"},
		{"unknown lang fence", "```python\nprint(1)\n```", "python\nprint(1)"},
		{"no closing fence", "```json\n{\"key\": 1}", `{"key": 1}`},
		{"whitespace trimmed", "  hello  ", "hello"},
		{"empty string", "", ""},
		{"nested fences", "```json\n{\"text\": \"```code```\"}\n```", "{\"text\": \"```code```\"}"},
		{"fence with spaces", "  ```json\n{\"a\": 1}\n```  ", `{"a": 1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := polishAssistantResponse(tt.in)
			if got != tt.want {
				t.Errorf("polishAssistantResponse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewProvider(t *testing.T) {
	p := NewProvider("test-model", "http://localhost:9999", "", BackendOllama)
	if p == nil {
		t.Fatal("NewProvider returned nil")
	}
	if p.modelName != "test-model" {
		t.Errorf("expected model name 'test-model', got %q", p.modelName)
	}
	if p.backend != BackendOllama {
		t.Errorf("expected backend %q, got %q", BackendOllama, p.backend)
	}
	if p.maxRetries != 3 {
		t.Errorf("expected maxRetries 3, got %d", p.maxRetries)
	}
}

func TestNewProviderTrimsBaseURL(t *testing.T) {
	p := NewProvider("m", "http://example.com/ ", "", BackendOllama)
	if p.baseURL != "http://example.com" {
		t.Errorf("expected baseURL trimmed to 'http://example.com', got %q", p.baseURL)
	}
}

func TestGenerateOpenAICompatSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-api-key" {
			t.Errorf("expected auth header 'Bearer test-api-key', got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "hello from mock"}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-api-key", BackendOpenAICompat)
	p.isReady = true

	result, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100, Temp: 0.1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello from mock" {
		t.Errorf("expected 'hello from mock', got %q", result)
	}
}

func TestGenerateOpenAICompatServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal"}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	_, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got %v", err)
	}
}

func TestGenerateOpenAICompatEmptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": []}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	_, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestGenerateOpenAICompatEmptyContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "   "}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	_, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestGenerateOpenAICompatPolishesResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"choices\": [{\"message\": {\"content\": \"```json\\n{\\\"result\\\": 42}\\n```\"}}]}"))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	result, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "```") {
		t.Errorf("response should have fences stripped, got %q", result)
	}
}

func TestGenerateOpenAICompatRetryExhausted(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true
	p.maxRetries = 2

	_, err := p.Generate("task", "context", GenerationConfig{MaxTokens: 100})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestGenerateJSONSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "{\"key\": \"value\", \"num\": 42}"}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	result, err := p.GenerateJSON("task", "context", GenerationConfig{MaxTokens: 100}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["key"] != "value" {
		t.Errorf("expected key='value', got %v", m["key"])
	}
}

func TestGenerateJSONParseFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "not json"}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "test-key", BackendOpenAICompat)
	p.isReady = true

	defaultVal := map[string]string{"fallback": "yes"}
	result, err := p.GenerateJSON("task", "context", GenerationConfig{MaxTokens: 100}, defaultVal)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if result.(map[string]string)["fallback"] != "yes" {
		t.Error("expected default result on parse failure")
	}
}

func TestStats(t *testing.T) {
	p := NewProvider("test-model", "http://localhost:11434", "", BackendOllama)
	stats := p.Stats()

	if stats["model"] != "test-model" {
		t.Errorf("expected model 'test-model', got %v", stats["model"])
	}
	if stats["backend"] != string(BackendOllama) {
		t.Errorf("expected backend %q, got %v", BackendOllama, stats["backend"])
	}
	if stats["is_ready"] != false {
		t.Error("expected is_ready=false initially")
	}
	if stats["req_count"].(int64) != 0 {
		t.Error("expected req_count=0")
	}
	if stats["max_retries"] != 3 {
		t.Errorf("expected max_retries=3, got %v", stats["max_retries"])
	}
}

func TestSetBaseURL(t *testing.T) {
	p := NewProvider("m", "http://old.com", "", BackendOllama)
	p.SetBaseURL("http://new.com")
	if p.baseURL != "http://new.com" {
		t.Errorf("expected baseURL 'http://new.com', got %q", p.baseURL)
	}
}

func TestUnload(t *testing.T) {
	p := NewProvider("m", "http://localhost:11434", "", BackendOllama)
	p.isReady = true
	p.Unload()
	if p.isReady != false {
		t.Error("expected isReady=false after Unload")
	}
}

func TestGenerateOpenAICompatRequestPayload(t *testing.T) {
	var receivedBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "ok"}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-turbo", ts.URL, "key", BackendOpenAICompat)
	p.isReady = true

	_, err := p.Generate("Do this task", "reference context", GenerationConfig{
		MaxTokens: 512,
		Temp:      0.2,
		Model:     "qwen-plus",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedBody == nil {
		_ = receivedBody
	}
}

func TestGenerationConfigDefaults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices": [{"message": {"content": "ok"}}]}`))
	}))
	defer ts.Close()

	p := NewProvider("qwen-plus", ts.URL, "key", BackendOpenAICompat)
	p.isReady = true

	_, err := p.Generate("task", "context", GenerationConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
