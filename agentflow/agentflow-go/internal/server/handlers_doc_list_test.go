package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"agentflow-go/internal/config"
)

// TestListCaseDocumentsJSONShape verifies that GET /v1/cases/{id}/documents/list
// returns a JSON array of objects, each carrying the six spec fields with the
// correct types: filename(string), doctype(string), ocr_indexed(bool),
// rag_indexed(bool), size_bytes(int64), modified_at(string).
func TestListCaseDocumentsJSONShape(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "doc-list-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
		MaxCases:       10,
		MaxConcurrent:  1,
	}
	s := New(cfg)

	// Create a case and attach a document. Drop matching bytes on disk so the
	// handler can stat them and report a nonzero size + mtime.
	c := s.workflow.CreateCase("Test Client", "Civil", "Test", "")
	docsDir := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	docName := "evidence.pdf"
	docPath := filepath.Join(docsDir, docName)
	docBody := []byte("hello world")
	if err := os.WriteFile(docPath, docBody, 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	s.workflow.AttachDocument(c.CaseID, docName)

	req := httptest.NewRequest(http.MethodGet, "/v1/cases/"+c.CaseID+"/documents/list", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got []map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d (body=%s)", len(got), rr.Body.String())
	}

	entry := got[0]
	for _, field := range []string{"filename", "doctype", "ocr_indexed", "rag_indexed", "size_bytes", "modified_at"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("missing field %q in entry: %v", field, entry)
		}
	}

	if fn, _ := entry["filename"].(string); fn != docName {
		t.Errorf("filename = %q, want %q", fn, docName)
	}
	// doctype is a string (empty when AIFileSummaries.doctype is unset).
	if _, ok := entry["doctype"].(string); !ok {
		t.Errorf("doctype is not a string: %T", entry["doctype"])
	}
	if _, ok := entry["ocr_indexed"].(bool); !ok {
		t.Errorf("ocr_indexed is not a bool: %T", entry["ocr_indexed"])
	}
	if _, ok := entry["rag_indexed"].(bool); !ok {
		t.Errorf("rag_indexed is not a bool: %T", entry["rag_indexed"])
	}
	// size_bytes round-trips through JSON as a float64 — verify it's the file size.
	size, ok := entry["size_bytes"].(float64)
	if !ok {
		t.Errorf("size_bytes is not a number: %T", entry["size_bytes"])
	} else if int(size) != len(docBody) {
		t.Errorf("size_bytes = %v, want %d", size, len(docBody))
	}
	if mt, _ := entry["modified_at"].(string); mt == "" {
		t.Errorf("modified_at is empty for an existing file")
	}
}

// TestListCaseDocumentsCaseNotFound verifies the endpoint 404s on an unknown case.
func TestListCaseDocumentsCaseNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "doc-list-test-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	cfg := &config.Config{
		DataDir:       tempDir,
		OllamaURL:     "http://localhost:11434",
		MaxCases:      10,
		MaxConcurrent: 1,
	}
	s := New(cfg)

	req := httptest.NewRequest(http.MethodGet, "/v1/cases/nonexistent/documents/list", nil)
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rr.Code, rr.Body.String())
	}
}
