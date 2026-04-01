package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-go/internal/config"
	"agentflow-go/internal/model"
)

func TestHandleHealth(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleStatus(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/status", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["rag"]; !ok {
		t.Error("expected 'rag' in status response")
	}
}

func TestHandleListCases(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	s.workflow.CreateCase("TestClient", "Test", "Source", "")

	req := httptest.NewRequest("GET", "/v1/cases", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Cases []map[string]interface{} `json:"cases"`
		Count int                      `json:"count"`
	}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Count < 1 {
		t.Error("expected at least 1 case")
	}
}

func TestHandleCreateCaseInvalidMethod(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/cases/create", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleCreateCaseInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("POST", "/v1/cases/create", bytes.NewBuffer([]byte("not json")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleGetCaseNotFound(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/cases/nonexistent", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleAdvanceCaseNotFound(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("POST", "/v1/cases/nonexistent/advance", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleDeleteCase(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	payload := map[string]string{"client_name": "DeleteMe", "matter_type": "Test"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/cases/create", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	var created struct {
		CaseID string `json:"case_id"`
	}
	json.NewDecoder(rr.Body).Decode(&created)

	req = httptest.NewRequest("DELETE", "/v1/cases/"+created.CaseID+"/delete", nil)
	rr = httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleAddNote(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("NoteClient", "Test", "Source", "")

	payload := map[string]string{"text": "Important note"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/cases/"+c.CaseID+"/notes", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	snap, _ := s.workflow.GetCaseSnapshot(c.CaseID)
	if len(snap.Notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(snap.Notes))
	}
}

func TestHandleAddNoteInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("Client", "Test", "Source", "")

	req := httptest.NewRequest("POST", "/v1/cases/"+c.CaseID+"/notes", bytes.NewBuffer([]byte("bad")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleApproveHITL(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("Client", "Test", "Source", "")

	payload := map[string]interface{}{"state": "CASE_EVALUATION", "approved": true, "reason": "looks good"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/v1/cases/"+c.CaseID+"/approve", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleApproveHITLInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("Client", "Test", "Source", "")

	req := httptest.NewRequest("POST", "/v1/cases/"+c.CaseID+"/approve", bytes.NewBuffer([]byte("bad")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleListDocuments(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/documents", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleDeviceStatus(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/device", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["platform_id"]; !ok {
		t.Error("expected 'platform_id' in device status")
	}
	if _, ok := resp["llm_model"]; !ok {
		t.Error("expected 'llm_model' in device status")
	}
}

func TestHandleJobNotFound(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/jobs/nonexistent", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleUploadInvalidMethod(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/upload", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleUploadBatchInvalidMethod(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/upload/batch", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleUploadDirectoryInvalidMethod(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/upload/directory", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleUploadDirectoryInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("POST", "/v1/upload/directory", bytes.NewBuffer([]byte("bad")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleUploadDirectoryRejectsPathOutsideDataDir(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	body := `{"directory_path":"/tmp"}`
	req := httptest.NewRequest("POST", "/v1/upload/directory", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestHandleSearchInvalidMethod(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/rag/search", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleSearchInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("POST", "/v1/rag/search", bytes.NewBuffer([]byte("bad")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleUpdateCase(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("OldName", "OldType", "Source", "")

	payload := map[string]string{"client_name": "NewName", "matter_type": "NewType"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("PUT", "/v1/cases/"+c.CaseID+"/update", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", rr.Code, rr.Body.String())
	}

	snap, _ := s.workflow.GetCaseSnapshot(c.CaseID)
	if snap.ClientName != "NewName" {
		t.Errorf("expected 'NewName', got %q", snap.ClientName)
	}
}

func TestHandleUpdateCaseNotFound(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	payload := map[string]string{"client_name": "X", "matter_type": "Y"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest("PUT", "/v1/cases/nonexistent/update", bytes.NewBuffer(b))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleUpdateCaseInvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	c := s.workflow.CreateCase("Client", "Type", "Source", "")

	req := httptest.NewRequest("PUT", "/v1/cases/"+c.CaseID+"/update", bytes.NewBuffer([]byte("bad")))
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestSanitizeUploadedBasename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal", "document.pdf", "document.pdf"},
		{"with path strips basename", "/some/path/document.pdf", "document.pdf"},
		{"double dots becomes underscore", "..", "_"},
		{"dot", ".", ""},
		{"empty", "", ""},
		{"whitespace trimmed", "  file.txt  ", "file.txt"},
		{"basename extracted from path", "foo/bar.txt", "bar.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUploadedBasename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeUploadedBasename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUnescapeURLPathSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no encoding", "file.txt", "file.txt"},
		{"encoded space", "my%20file.txt", "my file.txt"},
		{"encoded chinese", "%E4%B8%AD%E6%96%87.pdf", "中文.pdf"},
		{"invalid encoding", "%ZZ", "%ZZ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unescapeURLPathSegment(tt.input)
			if got != tt.want {
				t.Errorf("unescapeURLPathSegment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSubmitJobPanicRecovery(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
		MaxConcurrent:  1,
	}
	s := New(cfg)

	jobID := s.submitJob("panic_test", func(job *model.Job) (any, error) {
		panic("intentional panic")
	})

	time.Sleep(200 * time.Millisecond)

	s.jobsMu.RLock()
	job, ok := s.jobs[jobID]
	s.jobsMu.RUnlock()

	if !ok {
		t.Fatal("job should still exist after panic recovery")
	}
	if job.Status != model.JobStatusFailed {
		t.Errorf("expected job status 'failed', got %q", job.Status)
	}
	if !strings.Contains(job.Error, "panic") {
		t.Errorf("expected error to contain 'panic', got %q", job.Error)
	}
}

func TestSubmitJobCompletion(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
		MaxConcurrent:  1,
	}
	s := New(cfg)

	done := make(chan string)
	jobID := s.submitJob("simple", func(job *model.Job) (any, error) {
		return map[string]string{"result": "ok"}, nil
	})

	go func() {
		for {
			s.jobsMu.RLock()
			job, ok := s.jobs[jobID]
			s.jobsMu.RUnlock()
			if ok && job.Status == model.JobStatusCompleted {
				done <- jobID
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not complete within timeout")
	}
}

func TestResolveCaseForDocumentNewClient(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	caseID := s.resolveCaseForDocument("Unknown Client", "Civil Litigation", "test.pdf")
	if caseID == "" {
		t.Fatal("expected non-empty case ID")
	}

	snap, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		t.Fatal("expected case to exist")
	}
	if snap.MatterType != "Civil Litigation" {
		t.Errorf("expected matter 'Civil Litigation', got %q", snap.MatterType)
	}
	if !strings.Contains(snap.ClientName, "Intake") && snap.ClientName != "Unknown Client" {
		t.Errorf("expected intake client name, got %q", snap.ClientName)
	}
}

func TestResolveCaseForDocumentExistingClient(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	s.workflow.CreateCase("ExistingClient", "Civil Litigation", "Source", "")

	caseID := s.resolveCaseForDocument("ExistingClient", "Civil Litigation", "test.pdf")
	if caseID == "" {
		t.Fatal("expected non-empty case ID")
	}

	snap, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		t.Fatal("expected case to exist")
	}
	if snap.ClientName != "ExistingClient" {
		t.Errorf("expected 'ExistingClient', got %q", snap.ClientName)
	}
}

func TestHandleRAGSummary(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	req := httptest.NewRequest("GET", "/v1/rag/summary", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
