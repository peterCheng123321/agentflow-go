package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentflow-go/internal/config"
)

func TestClipContext(t *testing.T) {
	input := strings.Repeat("A", 20000)
	limit := 12000
	output := clipContext(input, limit)
	
	if len(output) != limit {
		t.Errorf("Expected length %d, got %d", limit, len(output))
	}
}

func TestManualRouting(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)
	
	// 1. Create a case
	payload := map[string]string{
		"client_name": "Test Client",
		"matter_type": "Civil",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/cases/create", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("Create case failed: %d", rr.Code)
	}

	var createdCase struct {
		CaseID string `json:"case_id"`
	}
	json.NewDecoder(rr.Body).Decode(&createdCase)
	caseID := createdCase.CaseID

	// 2. Test Get Case
	req = httptest.NewRequest("GET", "/v1/cases/"+caseID, nil)
	rr = httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Get case failed: %d", rr.Code)
	}

	// 3. Test Advance Case
	req = httptest.NewRequest("POST", "/v1/cases/"+caseID+"/advance", nil)
	rr = httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Advance case failed: %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestMockSidecar(t *testing.T) {
	// Mock the OpenAI-compatible API
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/chat/completions" {
			fmt.Fprint(w, `{"choices": [{"message": {"content": "AI Summary Result"}}]}`)
		} else if r.URL.Path == "/ocr" {
			// Keeping legacy OCR endpoint if needed, but OpenAI compat uses /chat/completions
			fmt.Fprint(w, `{"choices": [{"message": {"content": "OCR Result"}}]}`)
		} else if r.URL.Path == "/api/tags" {
			// For Ollama fallback testing
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	tempDir, _ := os.MkdirTemp("", "sidecar-test-*")
	defer os.RemoveAll(tempDir)

	// We'll test with the DashScope backend explicitly since we removed MLX
	os.Setenv("AGENTFLOW_LLM_BACKEND", "dashscope")
	os.Setenv("AGENTFLOW_DASHSCOPE_API_KEY", "test-key")
	defer os.Unsetenv("AGENTFLOW_LLM_BACKEND")
	defer os.Unsetenv("AGENTFLOW_DASHSCOPE_API_KEY")

	cfg := config.Load()
	cfg.DataDir = tempDir
	cfg.DashScopeBaseURL = ts.URL
	
	s := New(cfg)
	// Create case and docs to test summarization
	c := s.workflow.CreateCase("SummarizeMe", "Civil", "Test", "")
	s.workflow.AttachDocument(c.CaseID, "test.txt")
	s.rag.IngestFile("test.txt", "Content to summarize", nil)

	req := httptest.NewRequest("POST", "/v1/cases/"+c.CaseID+"/summarize", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Summarize failed with mock sidecar: %d body: %s", rr.Code, rr.Body.String())
	}
	
	if !strings.Contains(rr.Body.String(), "AI Summary Result") {
		t.Errorf("Expected summary result in body, got: %s", rr.Body.String())
	}
}
