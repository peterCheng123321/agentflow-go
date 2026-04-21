package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-go/internal/model"
)

// --- stubs ---

type benchOCR struct{ text string }

func (b *benchOCR) ScanFile(_ string) (string, error) { return b.text, nil }

type benchClassifier struct{}

func (benchClassifier) Classify(_ context.Context, _, _ string) (map[string]interface{}, error) {
	return map[string]interface{}{
		"document_type":   "civil_complaint",
		"display_name_zh": "民事起诉状",
		"confidence":      "high",
	}, nil
}

type benchAnalyzer struct{}

func (benchAnalyzer) AnalyzeBatch(_ context.Context, _ map[string]string) (BatchMeta, error) {
	return BatchMeta{ClientName: "张三", MatterType: "债务纠纷"}, nil
}

type benchRAG struct{}

func (benchRAG) IngestFile(_ string, _ string, _ map[string]interface{}) error { return nil }

type benchWorkflow struct{}

func (benchWorkflow) AttachDocument(_, _ string, _ ...map[string]interface{}) {}

type benchUpdater struct{}

func (benchUpdater) UpdateJob(_ string, _ func(*model.Job)) {}

// --- helpers ---

var benchDocText = strings.Repeat(
	"原告张三诉被告李四借款纠纷案件债务金额人民币十万元整借款日期二零二三年一月一日还款期限一年合同签订地北京市朝阳区。",
	30,
)

func makeBenchProcessor(concurrency int) *BatchProcessor {
	return NewBatchProcessor(
		&benchOCR{text: benchDocText},
		benchClassifier{},
		benchAnalyzer{},
		benchRAG{},
		benchWorkflow{},
		benchUpdater{},
		concurrency,
	)
}

func writeTmpFiles(tb testing.TB, dir string, n int) []string {
	tb.Helper()
	paths := make([]string, n)
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("doc%04d.txt", i))
		if err := os.WriteFile(p, []byte(benchDocText), 0644); err != nil {
			tb.Fatal(err)
		}
		paths[i] = p
	}
	return paths
}

// --- benchmarks ---

func BenchmarkBatchProcess_10files(b *testing.B) {
	p := makeBenchProcessor(4)
	dir := b.TempDir()
	files := writeTmpFiles(b, dir, 10)
	b.ResetTimer()
	for range b.N {
		_, _ = p.ProcessBatch(context.Background(), "bench-job", files, Options{})
	}
}

func BenchmarkBatchProcess_50files(b *testing.B) {
	p := makeBenchProcessor(4)
	dir := b.TempDir()
	files := writeTmpFiles(b, dir, 50)
	b.ResetTimer()
	for range b.N {
		_, _ = p.ProcessBatch(context.Background(), "bench-job", files, Options{})
	}
}

func BenchmarkBatchProcess_concurrency1(b *testing.B) {
	p := makeBenchProcessor(1)
	dir := b.TempDir()
	files := writeTmpFiles(b, dir, 20)
	b.ResetTimer()
	for range b.N {
		_, _ = p.ProcessBatch(context.Background(), "bench-job", files, Options{})
	}
}

func BenchmarkBatchProcess_concurrency8(b *testing.B) {
	p := makeBenchProcessor(8)
	dir := b.TempDir()
	files := writeTmpFiles(b, dir, 20)
	b.ResetTimer()
	for range b.N {
		_, _ = p.ProcessBatch(context.Background(), "bench-job", files, Options{})
	}
}
