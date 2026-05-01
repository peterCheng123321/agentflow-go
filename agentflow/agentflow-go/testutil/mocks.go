// Package testutil provides testing utilities for agentflow-go tests.
// It includes mock servers, random file generators, and test helpers.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// MockLLMServer creates a mock LLM server for testing.
type MockLLMServer struct {
	Server      *httptest.Server
	Response    string
	Latency     time.Duration
	RequestCount atomic.Int64
	CustomHandler http.HandlerFunc
}

// NewMockLLMServer creates a new mock LLM server.
func NewMockLLMServer() *MockLLMServer {
	m := &MockLLMServer{
		Response: `{"choices": [{"message": {"content": "Mock response"}}]}`,
		Latency:  0,
	}

	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.RequestCount.Add(1)
		if m.Latency > 0 {
			time.Sleep(m.Latency)
		}

		if m.CustomHandler != nil {
			m.CustomHandler(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/chat/completions", "/v1/chat/completions":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, m.Response)
		case "/api/tags":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"models": []}`)
		case "/ocr":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"text": "Mock OCR result"}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, m.Response)
		}
	}))

	return m
}

// Close closes the mock server.
func (m *MockLLMServer) Close() {
	m.Server.Close()
}

// URL returns the mock server URL.
func (m *MockLLMServer) URL() string {
	return m.Server.URL
}

// SetResponse sets a custom response for the mock server.
func (m *MockLLMServer) SetResponse(response string) {
	m.Response = response
}

// MockOCRServer creates a mock OCR server for testing.
type MockOCRServer struct {
	Server      *httptest.Server
	OCRText     string
	Latency     time.Duration
	RequestCount atomic.Int64
}

// NewMockOCRServer creates a new mock OCR server.
func NewMockOCRServer() *MockOCRServer {
	m := &MockOCRServer{
		OCRText: "Sample OCR text from document",
		Latency: 0,
	}

	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.RequestCount.Add(1)
		if m.Latency > 0 {
			time.Sleep(m.Latency)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"text": m.OCRText,
		})
	}))

	return m
}

// Close closes the mock server.
func (m *MockOCRServer) Close() {
	m.Server.Close()
}

// URL returns the mock server URL.
func (m *MockOCRServer) URL() string {
	return m.Server.URL
}

// FileGenerator generates random test files.
type FileGenerator struct {
	tempDir string
}

// NewFileGenerator creates a new file generator.
func NewFileGenerator(t *testing.T) *FileGenerator {
	tempDir := t.TempDir()
	return &FileGenerator{tempDir: tempDir}
}

// TempDir returns the temporary directory.
func (fg *FileGenerator) TempDir() string {
	return fg.tempDir
}

// TextFile creates a random text file.
func (fg *FileGenerator) TextFile(name string, content string) string {
	if name == "" {
		name = fmt.Sprintf("test-%d.txt", time.Now().UnixNano())
	}
	path := filepath.Join(fg.tempDir, name)
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		panic(fmt.Sprintf("failed to create text file: %v", err))
	}
	return path
}

// RandomTextFile creates a text file with random content.
func (fg *FileGenerator) RandomTextFile(name string, size int) string {
	content := strings.Repeat("Random test content. ", size/21+1)
	if name == "" {
		name = fmt.Sprintf("random-%d.txt", time.Now().UnixNano())
	}
	return fg.TextFile(name, content)
}

// LargeTextFile creates a large text file for testing performance.
func (fg *FileGenerator) LargeTextFile(name string, sizeKB int) string {
	size := sizeKB * 1024
	return fg.RandomTextFile(name, size)
}

// ChineseTextFile creates a text file with Chinese content.
func (fg *FileGenerator) ChineseTextFile(name string) string {
	content := `这是一份测试文件。
内容包括合同、起诉书、身份证等法律文档的模拟文本。
客户：张三
案件类型：民事纠纷
争议金额：50000元
`
	if name == "" {
		name = fmt.Sprintf("chinese-%d.txt", time.Now().UnixNano())
	}
	return fg.TextFile(name, content)
}

// LegalDocumentFile creates a file with legal document content.
func (fg *FileGenerator) LegalDocumentFile(docType, name string) string {
	content := fmt.Sprintf(`LEGAL DOCUMENT - %s
==========================
Document Number: %d
Date: %s

PARTIES INVOLVED:
- Plaintiff: Test Plaintiff Inc.
- Defendant: Test Defendant LLC

CASE DETAILS:
Type: %s
Jurisdiction: Test Court
Case Number: TEST-2024-%d

CONTENT:
This is a sample legal document for testing purposes.
The document contains various legal terms and conditions
that simulate real-world legal documents.

AI_CLASSIFICATION_HINT: %s
`, strings.ToUpper(docType), time.Now().UnixNano(), time.Now().Format("2006-01-02"),
		docType, time.Now().UnixNano()%10000, docType)

	if name == "" {
		name = fmt.Sprintf("%s-%d.txt", strings.ToLower(docType), time.Now().UnixNano())
	}
	return fg.TextFile(name, content)
}

// MultiFile creates multiple test files.
func (fg *FileGenerator) MultiFile(count int) []string {
	files := make([]string, count)
	docTypes := []string{"contract", "complaint", "id_card", "evidence", "judgment"}

	for i := 0; i < count; i++ {
		docType := docTypes[i%len(docTypes)]
		files[i] = fg.LegalDocumentFile(docType, "")
	}
	return files
}

// MultipartRequest creates a multipart form request for file uploads.
func MultipartRequest(uri string, fieldName, filename string, fileContent []byte, additionalFields map[string]string) (*http.Request, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return nil, err
	}
	_, err = part.Write(fileContent)
	if err != nil {
		return nil, err
	}

	// Add additional fields
	for key, value := range additionalFields {
		err = writer.WriteField(key, value)
		if err != nil {
			return nil, err
		}
	}

	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", uri, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

// AssertJSON asserts that the response contains valid JSON.
func AssertJSON(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	err := json.Unmarshal(body, &result)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v\nBody: %s", err, string(body))
	}
	return result
}

// AssertStatus asserts the HTTP status code.
func AssertStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("Status code = %d, want %d", got, want)
	}
}

// AssertContains asserts that a string contains a substring.
func AssertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("String does not contain %q:\n%s", substr, s)
	}
}

// Poll polls a condition until it returns true or timeout is reached.
func Poll(t *testing.T, condition func() bool, timeout time.Duration, interval time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// WaitForJob waits for a job to complete.
func WaitForJob(t *testing.T, client *http.Client, baseURL, jobID string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/v1/jobs/" + jobID)
		if err != nil {
			t.Logf("Error polling job: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		result := AssertJSON(t, body)

		status, _ := result["status"].(string)
		if status == "completed" || status == "failed" {
			return result
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Job %s did not complete within %v", jobID, timeout)
	return nil
}

// SetupTestServer creates a test server with all dependencies.
func SetupTestServer(t *testing.T, dataDir string) *httptest.Server {
	// This is a placeholder - the actual implementation would
	// create a full server instance with mocked dependencies.
	// Each service test file should implement its own setup.
	return nil
}

// CleanupTempDir cleans up a temporary directory.
func CleanupTempDir(t *testing.T, dir string) {
	t.Helper()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// WithRetry runs a function with retries.
func WithRetry(t *testing.T, maxRetries int, fn func() error) {
	t.Helper()
	var err error
	for i := 0; i < maxRetries; i++ {
		if err = fn(); err == nil {
			return
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("After %d retries, last error: %v", maxRetries, err)
	}
}

// ContextWithTimeout returns a context with timeout.
func ContextWithTimeout(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}
