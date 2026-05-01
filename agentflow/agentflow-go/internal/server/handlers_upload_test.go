package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"agentflow-go/internal/config"
	"agentflow-go/testutil"
)

// TestUploadPersistsDoctype simulates what the single-file upload worker does
// after OCR + classification: it calls workflow.AttachDocument with a
// classification map AND a top-level "doctype" slug. We verify the resulting
// AIFileSummaries entry contains both `filename` and `doctype`.
func TestUploadPersistsDoctype(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "upload-doctype-test-*")
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)
	defer s.Shutdown()

	c := s.workflow.CreateCase("DocTypeClient", "Civil Litigation", "Test", "")
	caseID := c.CaseID

	// Simulate the post-classification AttachDocument call from handlers_upload.go.
	classification := map[string]interface{}{
		"document_type":   "civil_complaint",
		"display_name_zh": "民事起诉状",
		"confidence":      "high",
	}
	extras := map[string]interface{}{
		"classification": classification,
	}
	if dt, _ := classification["document_type"].(string); dt != "" {
		extras["doctype"] = dt
	}
	s.workflow.AttachDocument(caseID, "complaint.pdf", extras)

	snap, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		t.Fatalf("case %s missing", caseID)
	}
	if len(snap.AIFileSummaries) != 1 {
		t.Fatalf("expected 1 AIFileSummaries row, got %d", len(snap.AIFileSummaries))
	}
	row := snap.AIFileSummaries[0]
	if fn, _ := row["filename"].(string); fn != "complaint.pdf" {
		t.Errorf("expected filename 'complaint.pdf', got %v", row["filename"])
	}
	if dt, _ := row["doctype"].(string); dt != "civil_complaint" {
		t.Errorf("expected doctype 'civil_complaint', got %v", row["doctype"])
	}
}

// TestUploadAttachWithoutClassification ensures that when classification is
// skipped (e.g. unusable OCR text), AttachDocument still records the file but
// without a doctype field.
func TestUploadAttachWithoutClassification(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "upload-no-doctype-test-*")
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)
	defer s.Shutdown()

	c := s.workflow.CreateCase("NoDocTypeClient", "Civil Litigation", "Test", "")
	caseID := c.CaseID

	s.workflow.AttachDocument(caseID, "blurry.pdf")

	snap, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		t.Fatalf("case %s missing", caseID)
	}
	if len(snap.AIFileSummaries) != 1 {
		t.Fatalf("expected 1 AIFileSummaries row, got %d", len(snap.AIFileSummaries))
	}
	row := snap.AIFileSummaries[0]
	if fn, _ := row["filename"].(string); fn != "blurry.pdf" {
		t.Errorf("expected filename 'blurry.pdf', got %v", row["filename"])
	}
	if _, present := row["doctype"]; present {
		t.Errorf("expected doctype to be absent when classification is skipped, got %v", row["doctype"])
	}
}

// TestUploadDoctypeMergePreservesExistingKeys verifies that when a doctype
// is recorded for a file that already has an AIFileSummaries row, the merge
// preserves any pre-existing keys (e.g. earlier ai_metadata) instead of
// overwriting the whole row.
func TestUploadDoctypeMergePreservesExistingKeys(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "upload-merge-test-*")
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)
	defer s.Shutdown()

	c := s.workflow.CreateCase("MergeClient", "Civil Litigation", "Test", "")
	caseID := c.CaseID

	// First attach with some pre-existing extra metadata.
	s.workflow.AttachDocument(caseID, "evidence.pdf", map[string]interface{}{
		"existing_key": "preserve-me",
	})

	// Second attach simulates the post-classification update.
	s.workflow.AttachDocument(caseID, "evidence.pdf", map[string]interface{}{
		"doctype": "iou_debt_note",
	})

	snap, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		t.Fatalf("case %s missing", caseID)
	}
	if len(snap.AIFileSummaries) != 1 {
		t.Fatalf("expected 1 row (merged), got %d", len(snap.AIFileSummaries))
	}
	row := snap.AIFileSummaries[0]
	if got, _ := row["existing_key"].(string); got != "preserve-me" {
		t.Errorf("expected existing_key preserved, got %v", row["existing_key"])
	}
	if got, _ := row["doctype"].(string); got != "iou_debt_note" {
		t.Errorf("expected doctype 'iou_debt_note', got %v", row["doctype"])
	}
}

// TestUploadEndpointPersistsDoctype drives the HTTP /v1/upload endpoint with a
// mocked OCR + classification LLM and asserts that the resulting case has a
// {filename, doctype} entry in AIFileSummaries.
func TestUploadEndpointPersistsDoctype(t *testing.T) {
	tempDir := t.TempDir()

	mockLLM := testutil.NewMockLLMServer()
	defer mockLLM.Close()

	// The mock server is hit for both OCR (vision) and classification (text).
	// OCR responses must contain enough Chinese characters to pass IntakeTextUsable.
	// Classification responses must be a JSON object with document_type.
	mockLLM.CustomHandler = func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw := string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var payload string
		switch {
		case bytes.Contains([]byte(raw), []byte("document_type")):
			// Classification prompt — return a valid civil_complaint slug.
			cls := `{"document_type":"civil_complaint","display_name_zh":"民事起诉状","confidence":"high","summary_zh":"原告诉被告","entities":{}}`
			payload = fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, cls)
		case bytes.Contains([]byte(raw), []byte("client_name")):
			payload = `{"choices":[{"message":{"content":"{\"client_name\":\"\",\"matter_type\":\"Civil Litigation\",\"confidence\":\"low\"}"}}]}`
		default:
			// OCR or other text generation — return Chinese filler text long
			// enough to satisfy IntakeTextUsable's minimum rune length.
			payload = `{"choices":[{"message":{"content":"民事起诉状原告张三被告李四诉讼请求金额壹万元事实与理由如下双方签订买卖合同被告未付款"}}]}`
		}
		fmt.Fprint(w, payload)
	}

	cfg := &config.Config{
		DataDir:          tempDir,
		IsAppleSilicon:   false,
		LLMBackend:       "dashscope",
		ModelName:        "qwen-plus",
		OCRModelID:       "qwen-vl",
		MaxConcurrent:    2,
		MaxCases:         10,
		DashScopeBaseURL: mockLLM.URL(),
		DashScopeAPIKey:  "test-key",
	}
	s := New(cfg)
	defer s.Shutdown()

	httpSrv := httptest.NewServer(s.Router())
	defer httpSrv.Close()

	c := s.workflow.CreateCase("Endpoint Client", "Civil Litigation", "Test", "")
	caseID := c.CaseID

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("file", "endpoint_complaint.pdf")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("dummy bytes — OCR is mocked so content does not matter")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.WriteField("case_id", caseID); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/v1/upload", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202 Accepted, got %d: %s", resp.StatusCode, respBody)
	}

	var ack struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap, ok := s.workflow.GetCaseSnapshot(caseID)
		if ok {
			for _, row := range snap.AIFileSummaries {
				fn, _ := row["filename"].(string)
				if fn != "endpoint_complaint.pdf" {
					continue
				}
				if dt, _ := row["doctype"].(string); dt == "civil_complaint" {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	snap, _ := s.workflow.GetCaseSnapshot(caseID)
	t.Fatalf("did not find {filename:endpoint_complaint.pdf, doctype:civil_complaint} after upload; got AIFileSummaries=%+v", snap.AIFileSummaries)
}
