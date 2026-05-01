package io

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetAppleInfo(t *testing.T) {
	info := GetAppleInfo()

	if info == nil {
		t.Fatal("GetAppleInfo returned nil")
	}

	t.Logf("Apple Silicon: %v", info.IsAppleSilicon)
	t.Logf("Model: %s", info.Model)
	t.Logf("Cores: %d (performance), %d (efficiency)", info.Cores, info.EfficiencyCores)
	t.Logf("Memory: %d GB", info.MemoryGB)
	t.Logf("NEON: %v, AMX: %v", info.SupportsNEON, info.SupportsAMX)
}

func TestFastFileReader(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.txt")
	content := []byte("Hello, Apple M-series!")

	err := os.WriteFile(testFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	reader, err := NewFastFileReader(testFile)
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	data, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read all: %v", err)
	}

	if string(data) != string(content) {
		t.Errorf("Expected %q, got %q", string(content), string(data))
	}
}

func TestFastFileReaderLargeFile(t *testing.T) {
	tempDir := t.TempDir()
	largeFile := filepath.Join(tempDir, "large.bin")

	// Create a 5MB file
	largeContent := make([]byte, 5*1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	err := os.WriteFile(largeFile, largeContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create large file: %v", err)
	}

	reader, err := NewFastFileReader(largeFile)
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	data, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read all: %v", err)
	}

	if len(data) != len(largeContent) {
		t.Errorf("Expected %d bytes, got %d", len(largeContent), len(data))
	}
}

func TestFastFileWriter(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "output.txt")

	writer, err := NewFastFileWriter(testFile, 0644)
	if err != nil {
		t.Fatalf("Failed to create writer: %v", err)
	}

	testData := [][]byte{
		[]byte("Line 1\n"),
		[]byte("Line 2\n"),
		[]byte("Line 3\n"),
		[]byte(strings.Repeat("X", 100000)), // Large write
	}

	var written int
	for _, data := range testData {
		n, err := writer.Write(data)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if n != len(data) {
			t.Errorf("Expected to write %d bytes, wrote %d", len(data), n)
		}
		written += n
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify written content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	if len(content) != written {
		t.Errorf("Expected %d bytes, got %d", written, len(content))
	}
}

func TestCopyFile(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "src.txt")
	dstFile := filepath.Join(tempDir, "dst.txt")

	content := []byte("Test content for copy")

	err := os.WriteFile(srcFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	copied, err := CopyFile(srcFile, dstFile)
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	if copied != int64(len(content)) {
		t.Errorf("Expected to copy %d bytes, copied %d", len(content), copied)
	}

	// Verify content
	dstContent, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("Failed to read destination: %v", err)
	}

	if string(dstContent) != string(content) {
		t.Errorf("Content mismatch")
	}
}

func TestCopyFileLarge(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "large_src.bin")
	dstFile := filepath.Join(tempDir, "large_dst.bin")

	// Create 10MB file
	largeContent := make([]byte, 10*1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	err := os.WriteFile(srcFile, largeContent, 0644)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	copied, err := CopyFile(srcFile, dstFile)
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	if copied != int64(len(largeContent)) {
		t.Errorf("Expected to copy %d bytes, copied %d", len(largeContent), copied)
	}
}

func TestMMapReadOnly(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "mmap_test.txt")
	content := []byte("Memory mapped content")

	err := os.WriteFile(testFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	data, f, err := MMapReadOnly(testFile)
	if err != nil {
		t.Fatalf("MMapReadOnly failed: %v", err)
	}
	defer Munmap(data, f)

	if string(data) != string(content) {
		t.Errorf("Mmap content mismatch")
	}
}


func TestParallelChunkSize(t *testing.T) {
	chunk := ParallelChunkSize()
	t.Logf("Parallel chunk size: %d bytes", chunk)

	if chunk < 256*1024 {
		t.Errorf("Chunk size too small: %d", chunk)
	}
	if chunk > 2*1024*1024 {
		t.Errorf("Chunk size too large: %d", chunk)
	}
}

func TestOptimalWorkerCount(t *testing.T) {
	count := OptimalWorkerCount()
	t.Logf("Optimal worker count: %d", count)

	if count < 1 {
		t.Errorf("Worker count must be at least 1, got %d", count)
	}
	if count > 16 {
		t.Errorf("Worker count suspiciously high: %d", count)
	}
}


// Benchmark comparisons
func BenchmarkFastFileReader(b *testing.B) {
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "bench.txt")

	content := make([]byte, 1024*1024) // 1MB
	os.WriteFile(testFile, content, 0644)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		reader, _ := NewFastFileReader(testFile)
		reader.ReadAll()
		reader.Close()
	}
}

func BenchmarkOSReadAll(b *testing.B) {
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "bench.txt")

	content := make([]byte, 1024*1024) // 1MB
	os.WriteFile(testFile, content, 0644)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		os.ReadFile(testFile)
	}
}

func BenchmarkFastFileWriter(b *testing.B) {
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "bench_out.txt")

	content := make([]byte, 1024*1024) // 1MB

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		writer, _ := NewFastFileWriter(testFile, 0644)
		writer.Write(content)
		writer.Close()
		os.Remove(testFile)
	}
}

func BenchmarkOSWriteFile(b *testing.B) {
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "bench_out.txt")

	content := make([]byte, 1024*1024) // 1MB

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		os.WriteFile(testFile, content, 0644)
		os.Remove(testFile)
	}
}

func BenchmarkCopyFile(b *testing.B) {
	tempDir := b.TempDir()
	srcFile := filepath.Join(tempDir, "src.bin")
	dstFile := filepath.Join(tempDir, "dst.bin")

	content := make([]byte, 5*1024*1024) // 5MB
	os.WriteFile(srcFile, content, 0644)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		CopyFile(srcFile, dstFile)
		os.Remove(dstFile)
	}
}

