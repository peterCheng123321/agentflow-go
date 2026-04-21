package workflow_test

import (
	"fmt"
	"testing"

	"agentflow-go/internal/workflow"
)

func newBenchEngine(b *testing.B) *workflow.Engine {
	b.Helper()
	return workflow.NewEngine(500, b.TempDir(), func() {})
}

func BenchmarkCreateCase(b *testing.B) {
	e := newBenchEngine(b)
	defer e.Close()
	b.ResetTimer()
	for i := range b.N {
		_ = e.CreateCase(fmt.Sprintf("张三%d", i), "债务纠纷", "Upload", "")
	}
}

func BenchmarkListCases_100(b *testing.B) {
	e := newBenchEngine(b)
	defer e.Close()
	for i := range 100 {
		_ = e.CreateCase(fmt.Sprintf("当事人%04d", i), "劳动争议", "Upload", "")
	}
	b.ResetTimer()
	for range b.N {
		_ = e.ListCases()
	}
}

func BenchmarkListCases_500(b *testing.B) {
	e := newBenchEngine(b)
	defer e.Close()
	for i := range 500 {
		_ = e.CreateCase(fmt.Sprintf("当事人%04d", i), "合同纠纷", "Upload", "")
	}
	b.ResetTimer()
	for range b.N {
		_ = e.ListCases()
	}
}

func BenchmarkAttachDocument(b *testing.B) {
	e := newBenchEngine(b)
	defer e.Close()
	c := e.CreateCase("张三", "债务纠纷", "Upload", "")
	b.ResetTimer()
	for i := range b.N {
		e.AttachDocument(c.CaseID, fmt.Sprintf("doc%06d.pdf", i), map[string]interface{}{
			"document_type": "civil_complaint",
			"confidence":    "high",
			"summary_zh":    "民事起诉状",
		})
	}
}

func BenchmarkGetCaseSnapshot(b *testing.B) {
	e := newBenchEngine(b)
	defer e.Close()
	c := e.CreateCase("李四", "合同纠纷", "Upload", "")
	id := c.CaseID
	b.ResetTimer()
	for range b.N {
		_, _ = e.GetCaseSnapshot(id)
	}
}
