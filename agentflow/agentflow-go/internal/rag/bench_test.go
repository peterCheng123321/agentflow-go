package rag

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func makeManager(b *testing.B) (*Manager, string) {
	b.Helper()
	dir := b.TempDir()
	return NewManager(dir), dir
}

func ingestDocs(b *testing.B, m *Manager, n int) {
	b.Helper()
	for i := range n {
		text := strings.Repeat(fmt.Sprintf("原告张三诉被告李四借款纠纷案件编号%05d。债务金额人民币十万元整，借款日期二零二三年一月一日，还款期限一年。合同签订地北京市朝阳区。 ", i), 20)
		_ = m.IngestFile(fmt.Sprintf("/tmp/doc%05d.txt", i), text, map[string]interface{}{
			"filename": fmt.Sprintf("doc%05d.txt", i),
		})
	}
}

func BenchmarkRAGIngest_10docs(b *testing.B) {
	for range b.N {
		dir := b.TempDir()
		m := NewManager(dir)
		ingestDocs(b, m, 10)
	}
}

func BenchmarkRAGIngest_100docs(b *testing.B) {
	dir, _ := os.MkdirTemp("", "rag-bench-*")
	defer os.RemoveAll(dir)

	b.ResetTimer()
	for range b.N {
		m := NewManager(dir)
		ingestDocs(b, m, 100)
	}
}

func BenchmarkRAGSearch_10docs(b *testing.B) {
	m, _ := makeManager(b)
	ingestDocs(b, m, 10)
	b.ResetTimer()
	for range b.N {
		_ = m.Search("借款合同违约金", 5)
	}
}

func BenchmarkRAGSearch_100docs(b *testing.B) {
	m, _ := makeManager(b)
	ingestDocs(b, m, 100)
	b.ResetTimer()
	for range b.N {
		_ = m.Search("原告债务金额合同签订", 10)
	}
}

func BenchmarkRAGSearch_cached(b *testing.B) {
	m, _ := makeManager(b)
	ingestDocs(b, m, 50)
	_ = m.Search("借款合同", 5) // warm cache
	b.ResetTimer()
	for range b.N {
		_ = m.Search("借款合同", 5)
	}
}

func BenchmarkRAGChunking(b *testing.B) {
	text := strings.Repeat("原告张三诉被告李四借款纠纷案件。债务金额人民币十万元整。", 200)
	b.ResetTimer()
	for range b.N {
		_ = chunkText(text, 512)
	}
}

func BenchmarkRAGTokenize(b *testing.B) {
	text := "原告张三诉被告李四借款纠纷案件编号A2023001，债务金额人民币十万元整，借款日期2023年1月1日，还款期限一年，合同签订地北京市朝阳区法院。"
	b.ResetTimer()
	for range b.N {
		_ = tokenize(text)
	}
}
