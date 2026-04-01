package ocr

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewEngine(t *testing.T) {
	e := NewEngine("qwen-vl-plus", "http://localhost:11434", "", BackendOllama, 4, 30*time.Minute)
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.modelID != "qwen-vl-plus" {
		t.Errorf("expected modelID 'qwen-vl-plus', got %q", e.modelID)
	}
	if e.backend != BackendOllama {
		t.Errorf("expected backend %q, got %q", BackendOllama, e.backend)
	}
	if cap(e.semaphore) != 4 {
		t.Errorf("expected semaphore capacity 4, got %d", cap(e.semaphore))
	}
	if e.maxRetries != 2 {
		t.Errorf("expected maxRetries 2, got %d", e.maxRetries)
	}
}

func TestNewEngineMinConcurrent(t *testing.T) {
	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 0, 30*time.Minute)
	if cap(e.semaphore) != 1 {
		t.Errorf("expected min concurrent 1, got %d", cap(e.semaphore))
	}
}

func TestNewEngineTrimsBaseURL(t *testing.T) {
	e := NewEngine("m", "http://example.com/", "", BackendOllama, 1, 30*time.Minute)
	if e.baseURL != "http://example.com" {
		t.Errorf("expected baseURL trimmed, got %q", e.baseURL)
	}
}

func TestScanFileTXT(t *testing.T) {
	tmpDir := t.TempDir()
	content := "这是一份测试文件内容。\n第二行文字。"
	path := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	result, err := e.ScanFile(path)
	if err != nil {
		t.Fatalf("ScanFile failed: %v", err)
	}
	if result != content {
		t.Errorf("expected %q, got %q", content, result)
	}
}

func TestScanFileMD(t *testing.T) {
	tmpDir := t.TempDir()
	content := "# Title\n\nSome markdown content."
	path := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	result, err := e.ScanFile(path)
	if err != nil {
		t.Fatalf("ScanFile failed: %v", err)
	}
	if result != content {
		t.Errorf("expected %q, got %q", content, result)
	}
}

func TestScanFileNotFound(t *testing.T) {
	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	_, err := e.ScanFile("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestExtractDOCX(t *testing.T) {
	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "test.docx")

	createTestDOCX(t, docxPath, `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Hello World</w:t></w:r></w:p>
<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
</w:body>
</w:document>`)

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	result, err := e.extractDOCX(docxPath)
	if err != nil {
		t.Fatalf("extractDOCX failed: %v", err)
	}
	if !strings.Contains(result, "Hello World") {
		t.Errorf("expected 'Hello World' in result, got %q", result)
	}
	if !strings.Contains(result, "Second paragraph") {
		t.Errorf("expected 'Second paragraph' in result, got %q", result)
	}
}

func TestExtractDOCXMissingXML(t *testing.T) {
	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "empty.docx")

	createZipWithFiles(t, docxPath, nil)

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	_, err := e.extractDOCX(docxPath)
	if err == nil {
		t.Fatal("expected error for DOCX without document.xml")
	}
}

func TestExtractDOCXNotAZip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "notdocx.docx")
	os.WriteFile(path, []byte("not a zip file"), 0644)

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	_, err := e.extractDOCX(path)
	if err == nil {
		t.Fatal("expected error for invalid DOCX")
	}
}

func TestExtractDOCXEmptyText(t *testing.T) {
	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "empty.docx")

	createTestDOCX(t, docxPath, `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t></w:t></w:r></w:p>
</w:body>
</w:document>`)

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	result, err := e.extractDOCX(docxPath)
	if err != nil {
		t.Fatalf("extractDOCX failed: %v", err)
	}
	if !strings.Contains(result, "\n") {
		t.Errorf("expected newline in result, got %q", result)
	}
}

func TestIsRasterImageExt(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".jpg", true},
		{".jpeg", true},
		{".png", true},
		{".gif", true},
		{".webp", true},
		{".bmp", true},
		{".tiff", true},
		{".tif", true},
		{".heic", true},
		{".heif", true},
		{".JPG", true},
		{".PNG", true},
		{".txt", false},
		{".pdf", false},
		{".docx", false},
		{".md", false},
		{".json", false},
	}

	for _, tt := range tests {
		got := isRasterImageExt(tt.ext)
		if got != tt.want {
			t.Errorf("isRasterImageExt(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestCheckTTL(t *testing.T) {
	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 100*time.Millisecond)
	e.isReady = true
	e.lastUsed = time.Now().Add(-200 * time.Millisecond)

	e.checkTTL()
	if e.isReady != false {
		t.Error("expected isReady=false after TTL expired")
	}
}

func TestCheckTTLNotExpired(t *testing.T) {
	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	e.isReady = true
	e.lastUsed = time.Now()

	e.checkTTL()
	if e.isReady != true {
		t.Error("expected isReady=true when TTL not expired")
	}
}

func TestUnload(t *testing.T) {
	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	e.isReady = true
	e.Unload()
	if e.isReady != false {
		t.Error("expected isReady=false after Unload")
	}
}

func TestStats(t *testing.T) {
	e := NewEngine("qwen-vl-plus", "http://localhost:11434", "", BackendOllama, 4, 30*time.Minute)
	stats := e.Stats()

	if stats["model"] != "qwen-vl-plus" {
		t.Errorf("expected model 'qwen-vl-plus', got %v", stats["model"])
	}
	if stats["max_concurrent"] != 4 {
		t.Errorf("expected max_concurrent 4, got %v", stats["max_concurrent"])
	}
	if stats["is_ready"] != false {
		t.Error("expected is_ready=false initially")
	}
	if stats["req_count"].(int64) != 0 {
		t.Error("expected req_count=0")
	}
}

func TestScanFileIncrementsReqCount(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0644)

	e := NewEngine("m", "http://localhost:11434", "", BackendOllama, 1, 30*time.Minute)
	e.ScanFile(path)

	if e.reqCount.Load() != 1 {
		t.Errorf("expected reqCount=1, got %d", e.reqCount.Load())
	}
}

func TestCheckReadyOpenAICompatMissingKey(t *testing.T) {
	e := NewEngine("m", "http://example.com", "", BackendOpenAICompat, 1, 30*time.Minute)
	err := e.checkReady()
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestCheckReadyOpenAICompatHasKey(t *testing.T) {
	e := NewEngine("m", "http://example.com", "some-key", BackendOpenAICompat, 1, 30*time.Minute)
	err := e.checkReady()
	if err != nil {
		t.Errorf("expected no error when API key is set, got %v", err)
	}
}

// Helpers

func createTestDOCX(t *testing.T, path, xmlContent string) {
	createZipWithFiles(t, path, map[string]string{
		"word/document.xml": xmlContent,
	})
}

func createZipWithFiles(t *testing.T, path string, files map[string]string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("failed to create zip entry: %v", err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("failed to write zip entry: %v", err)
		}
	}
	w.Close()
}
