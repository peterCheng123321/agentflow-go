package rag

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    "徐克林",
			expected: []string{"徐", "克", "林"},
		},
		{
			input:    "Luo Haixia 罗海霞",
			expected: []string{"luo", "haixia", "罗", "海", "霞"},
		},
		{
			input:    "123-456",
			expected: []string{"123", "456"},
		},
		{
			input:    "追索劳动报酬纠纷",
			expected: []string{"追", "索", "劳", "动", "报", "酬", "纠", "纷"},
		},
	}

	for _, tc := range tests {
		got := tokenize(tc.input)
		if !reflect.DeepEqual(got, tc.expected) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestRAGPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "rag-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager(tempDir)

	// Ingest a document
	docPath := filepath.Join(tempDir, "test.txt")
	os.WriteFile(docPath, []byte("罗海霞律师函"), 0644)

	err = m.IngestFile(docPath, "罗海霞律师函", nil)
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	// Verify search works before restart
	results := m.Search("罗海霞", 1)
	if len(results) == 0 {
		t.Fatal("Search failed before restart")
	}

	// Create new manager instance to simulate restart
	m2 := NewManager(tempDir)

	if len(m2.documents) != 1 {
		t.Errorf("Expected 1 document after reload, got %d", len(m2.documents))
	}

	// Verify BM25 stats are loaded
	if m2.bm25AvgDocLen == 0 {
		t.Error("BM25 stats not loaded (AvgDocLen is 0)")
	}

	// Verify search still works
	results2 := m2.Search("罗海霞", 1)
	if len(results2) == 0 {
		t.Fatal("Search failed after restart")
	}

	if results2[0].Score == 0 {
		t.Error("Search score is 0 after restart")
	}
}

func TestIngestEmptyText(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	err := m.IngestFile("empty.txt", "", nil)
	if err != nil {
		t.Fatalf("IngestFile with empty text should not error: %v", err)
	}
	if len(m.documents) != 1 {
		t.Errorf("expected 1 document, got %d", len(m.documents))
	}
}

func TestIngestDuplicateFilename(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("test.txt", "first content", nil)
	m.IngestFile("test.txt", "second content", nil)

	if len(m.documents) != 2 {
		t.Errorf("expected 2 documents (duplicates allowed), got %d", len(m.documents))
	}
}

func TestDeleteDocument(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc1.txt", "content one", nil)
	m.IngestFile("doc2.txt", "content two", nil)

	if len(m.documents) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(m.documents))
	}

	m.DeleteDocument("doc1.txt")

	if len(m.documents) != 1 {
		t.Errorf("expected 1 document after delete, got %d", len(m.documents))
	}
	if m.documents[0].Filename != "doc2.txt" {
		t.Errorf("expected remaining doc 'doc2.txt', got %q", m.documents[0].Filename)
	}
}

func TestDeleteNonExistentDocument(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc1.txt", "content", nil)
	m.DeleteDocument("nonexistent.txt")

	if len(m.documents) != 1 {
		t.Errorf("expected 1 document (no change), got %d", len(m.documents))
	}
}

func TestDeleteDocumentRebuildsIndex(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc1.txt", "apple banana", nil)
	m.IngestFile("doc2.txt", "cherry date", nil)

	results := m.Search("apple", 10)
	if len(results) == 0 {
		t.Fatal("expected results for 'apple' before delete")
	}

	m.DeleteDocument("doc1.txt")

	results = m.Search("apple", 10)
	if len(results) != 0 {
		t.Errorf("expected no results for 'apple' after deleting doc1, got %d", len(results))
	}
}

func TestGetDocument(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("test.txt", "content", nil)

	doc, ok := m.GetDocument("test.txt")
	if !ok {
		t.Fatal("expected to find document")
	}
	if doc.Filename != "test.txt" {
		t.Errorf("expected filename 'test.txt', got %q", doc.Filename)
	}
}

func TestGetDocumentNotFound(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	_, ok := m.GetDocument("nonexistent.txt")
	if ok {
		t.Error("expected not found")
	}
}

func TestGetDocumentFlex(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("test_file.txt", "content", nil)

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"exact match", "test_file.txt", true},
		{"case insensitive", "TEST_FILE.TXT", true},
		{"basename only", "test_file.txt", true},
		{"not found", "other.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := m.GetDocumentFlex(tt.query)
			if ok != tt.want {
				t.Errorf("GetDocumentFlex(%q) ok = %v, want %v", tt.query, ok, tt.want)
			}
		})
	}
}

func TestGetSummary(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc1.txt", "content one", nil)
	m.IngestFile("doc2.txt", "content two with more words", nil)

	summary := m.GetSummary()
	if summary["document_count"] != 2 {
		t.Errorf("expected 2 documents, got %v", summary["document_count"])
	}
	if summary["backend_mode"] != "bm25" {
		t.Errorf("expected backend_mode 'bm25', got %v", summary["backend_mode"])
	}
}

func TestSearchEmptyIndex(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	results := m.Search("anything", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results on empty index, got %d", len(results))
	}
}

func TestSearchKZero(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc.txt", "hello world", nil)
	results := m.Search("hello", 0)
	if len(results) != 0 {
		t.Errorf("expected 0 results for k=0, got %d", len(results))
	}
}

func TestSearchReturnsTopK(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc1.txt", "apple banana cherry", nil)
	m.IngestFile("doc2.txt", "apple apple apple", nil)
	m.IngestFile("doc3.txt", "banana cherry date", nil)

	results := m.Search("apple", 1)
	if len(results) != 1 {
		t.Errorf("expected 1 result for k=1, got %d", len(results))
	}
	if results[0].Filename != "doc2.txt" {
		t.Errorf("expected 'doc2.txt' (most apple terms), got %q", results[0].Filename)
	}
}

func TestSearchCrossDocument(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("legal.txt", "民事起诉状 原告张三", nil)
	m.IngestFile("evidence.txt", "微信聊天记录 张三 李四", nil)

	results := m.Search("张三", 10)
	if len(results) < 2 {
		t.Errorf("expected results from both documents, got %d", len(results))
	}
}

func TestChunkTextEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		minChunks int
	}{
		{"empty text", "", 0},
		{"single word", "hello", 1},
		{"under chunk size", "short text", 1},
		{"no sentence boundaries", "a very long string without any periods semicolons or newlines that exceeds five hundred twelve bytes " +
			"and continues for a while because we need to make sure it gets split into multiple chunks even though there are no natural " +
			"sentence boundaries anywhere in this text at all which is quite unusual but possible in some documents like base64 encoded data", 1},
		{"many sentences", "a. b. c. d. e. f. g. h. i. j.", 1},
		{"chinese sentences", "你好。世界。测试。内容。", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkText(tt.text, 512)
			if len(chunks) < tt.minChunks {
				t.Errorf("expected at least %d chunks, got %d", tt.minChunks, len(chunks))
			}
		})
	}
}

func TestChunkTextRespectsBoundary(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence."
	chunks := chunkText(text, 30)

	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for small chunk size, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 512 {
			t.Errorf("chunk exceeds max size: %d bytes", len(chunk))
		}
	}
}

func TestConcurrentReadsDuringIngest(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			m.IngestFile("doc.txt", "content", nil)
		}(i)
		go func(n int) {
			defer wg.Done()
			m.Search("content", 10)
		}(i)
	}
	wg.Wait()
}

func TestSearchCacheInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc.txt", "hello world", nil)
	r1 := m.Search("hello", 10)

	m.IngestFile("doc2.txt", "hello again", nil)
	r2 := m.Search("hello", 10)

	if len(r2) <= len(r1) {
		t.Errorf("expected more results after second ingest, got %d vs %d", len(r2), len(r1))
	}
}

func TestNormalizeLogicalName(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"simple", "test.txt"},
		{"with directory", "/path/to/file.txt"},
		{"chinese", "中文文件.txt"},
		{"spaces", "my file name.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeLogicalName(tt.path)
			if result == "" {
				t.Error("NormalizeLogicalName returned empty string")
			}
		})
	}
}

func TestCorruptedPersistFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "rag_store.json")
	os.WriteFile(storePath, []byte("not valid json"), 0644)

	m := NewManager(tempDir)

	if len(m.documents) != 0 {
		t.Errorf("expected 0 documents from corrupted store, got %d", len(m.documents))
	}
}

func TestBM25Score(t *testing.T) {
	tempDir := t.TempDir()
	m := NewManager(tempDir)

	m.IngestFile("doc.txt", "the quick brown fox jumps over the lazy dog", nil)

	results := m.Search("quick fox", 10)
	if len(results) == 0 {
		t.Fatal("expected results for 'quick fox'")
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", results[0].Score)
	}
	if results[0].MatchMode != "bm25" {
		t.Errorf("expected match mode 'bm25', got %q", results[0].MatchMode)
	}
}
