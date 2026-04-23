package rag

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestSearchCacheInvalidation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "rag-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	m := NewManager(tempDir)

	if err := m.IngestFile("purple.txt", "purple elephant in the room", nil); err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	results := m.Search("purple", 5)
	if len(results) == 0 || results[0].Filename != "purple.txt" {
		t.Fatalf("expected purple.txt in initial search results, got %+v", results)
	}

	if err := m.IngestFile("orange.txt", "orange tiger", nil); err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}
	results = m.Search("orange", 5)
	if len(results) == 0 || results[0].Filename != "orange.txt" {
		t.Fatalf("expected orange.txt surfaced after second ingest, got %+v", results)
	}

	m.DeleteDocument("purple.txt")

	results = m.Search("purple", 5)
	for _, r := range results {
		if r.Filename == "purple.txt" {
			t.Fatalf("deleted document purple.txt still surfaced in search: %+v", results)
		}
	}
}
