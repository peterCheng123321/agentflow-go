package processor

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-go/internal/model"
)

// Mocks
type mockOCR struct {
	fn func(string) (string, error)
}
func (m *mockOCR) ScanFile(p string) (string, error) { return m.fn(p) }

type mockClassifier struct {
	fn func(string, string) (map[string]interface{}, error)
}
func (m *mockClassifier) Classify(ctx context.Context, t, f string) (map[string]interface{}, error) {
	return m.fn(t, f)
}

type mockAnalyzer struct {
	fn func(map[string]string) (BatchMeta, error)
}
func (m *mockAnalyzer) AnalyzeBatch(ctx context.Context, docs map[string]string) (BatchMeta, error) {
	return m.fn(docs)
}

type mockRAG struct {
	fn func(string, string, map[string]interface{}) error
}
func (m *mockRAG) IngestFile(p, t string, me map[string]interface{}) error { return m.fn(p, t, me) }

type mockWorkflow struct {
	mu     sync.Mutex
	attached []string
}
func (m *mockWorkflow) AttachDocument(c, f string, ex ...map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attached = append(m.attached, f)
}

type mockUpdater struct {
	mu       sync.Mutex
	progress map[string]int
}
func (m *mockUpdater) UpdateJob(id string, fn func(*model.Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := &model.Job{ID: id}
	fn(j)
	m.progress[id] = j.Progress
}

func TestProcessBatchSuccess(t *testing.T) {
	ocr := &mockOCR{fn: func(p string) (string, error) { return "text for " + p, nil }}
	cls := &mockClassifier{fn: func(t, f string) (map[string]interface{}, error) {
		return map[string]interface{}{"type": "doc"}, nil
	}}
	ana := &mockAnalyzer{fn: func(docs map[string]string) (BatchMeta, error) {
		return BatchMeta{ClientName: "Test Client"}, nil
	}}
	rag := &mockRAG{fn: func(p, t string, m map[string]interface{}) error { return nil }}
	wf := &mockWorkflow{}
	upd := &mockUpdater{progress: make(map[string]int)}

	p := NewBatchProcessor(ocr, cls, ana, rag, wf, upd, 2)
	files := []string{"f1.txt", "f2.txt"}
	res, err := p.ProcessBatch(context.Background(), "job1", files, Options{CaseID: "case1"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := res.(map[string]interface{})
	if m["count"] != 2 {
		t.Errorf("expected 2 uploaded, got %v", m["count"])
	}
	if m["client_name"] != "Test Client" {
		t.Errorf("expected Test Client, got %v", m["client_name"])
	}
	if len(wf.attached) != 2 {
		t.Errorf("expected 2 attached, got %d", len(wf.attached))
	}
}

func TestProcessBatchEmpty(t *testing.T) {
	p := NewBatchProcessor(nil, nil, nil, nil, nil, nil, 2)
	res, err := p.ProcessBatch(context.Background(), "job1", nil, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := res.(map[string]interface{})
	if m["count"] != 0 {
		t.Errorf("expected 0, got %v", m["count"])
	}
}

func TestProcessBatchPartialOCRFailure(t *testing.T) {
	ocr := &mockOCR{fn: func(p string) (string, error) {
		if p == "fail.txt" {
			return "", errors.New("ocr error")
		}
		return "ok", nil
	}}
	cls := &mockClassifier{fn: func(t, f string) (map[string]interface{}, error) { return nil, nil }}
	ana := &mockAnalyzer{fn: func(docs map[string]string) (BatchMeta, error) { return BatchMeta{}, nil }}
	rag := &mockRAG{fn: func(p, t string, m map[string]interface{}) error { return nil }}
	wf := &mockWorkflow{}
	upd := &mockUpdater{progress: make(map[string]int)}

	p := NewBatchProcessor(ocr, cls, ana, rag, wf, upd, 2)
	files := []string{"f1.txt", "fail.txt", "f2.txt"}
	res, err := p.ProcessBatch(context.Background(), "job1", files, Options{CaseID: "case1"})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := res.(map[string]interface{})
	if m["count"] != 2 {
		t.Errorf("expected 2 successes, got %v", m["count"])
	}
	failed := m["failed"].([]string)
	if len(failed) != 1 || failed[0] != "fail.txt" {
		t.Errorf("expected fail.txt in failed list, got %v", failed)
	}
}

func TestProcessBatchAllOCRFailureReachesFullProgress(t *testing.T) {
	ocr := &mockOCR{fn: func(p string) (string, error) {
		return "", errors.New("ocr failed")
	}}
	cls := &mockClassifier{fn: func(t, f string) (map[string]interface{}, error) { return nil, nil }}
	ana := &mockAnalyzer{fn: func(docs map[string]string) (BatchMeta, error) { return BatchMeta{}, nil }}
	rag := &mockRAG{fn: func(p, t string, m map[string]interface{}) error { return nil }}
	wf := &mockWorkflow{}
	upd := &mockUpdater{progress: make(map[string]int)}

	p := NewBatchProcessor(ocr, cls, ana, rag, wf, upd, 2)
	files := []string{"a.txt", "b.txt", "c.txt"}
	_, err := p.ProcessBatch(context.Background(), "job1", files, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if upd.progress["job1"] != 100 {
		t.Errorf("expected progress 100 after all OCR failures, got %d", upd.progress["job1"])
	}
}

func TestProcessBatchBatchAnalysisUsesUniquePaths(t *testing.T) {
	var docLen int
	ocr := &mockOCR{fn: func(p string) (string, error) { return "body", nil }}
	cls := &mockClassifier{fn: func(t, f string) (map[string]interface{}, error) { return nil, nil }}
	ana := &mockAnalyzer{fn: func(docs map[string]string) (BatchMeta, error) {
		docLen = len(docs)
		return BatchMeta{}, nil
	}}
	rag := &mockRAG{fn: func(p, t string, m map[string]interface{}) error { return nil }}
	wf := &mockWorkflow{}
	upd := &mockUpdater{progress: make(map[string]int)}

	p := NewBatchProcessor(ocr, cls, ana, rag, wf, upd, 4)
	// Same display name, different paths — must not collapse in batch map.
	files := []string{"/tmp/foo/x.txt", "/tmp/bar/x.txt"}
	_, err := p.ProcessBatch(context.Background(), "job1", files, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if docLen != 2 {
		t.Errorf("expected 2 distinct batch-analysis keys, got %d", docLen)
	}
}

func TestProcessBatchBatchAnalysisErrorSurfaced(t *testing.T) {
	ocr := &mockOCR{fn: func(p string) (string, error) { return "ok", nil }}
	cls := &mockClassifier{fn: func(t, f string) (map[string]interface{}, error) { return nil, nil }}
	ana := &mockAnalyzer{fn: func(docs map[string]string) (BatchMeta, error) {
		return BatchMeta{}, errors.New("analyzer down")
	}}
	rag := &mockRAG{fn: func(p, t string, m map[string]interface{}) error { return nil }}
	wf := &mockWorkflow{}
	upd := &mockUpdater{progress: make(map[string]int)}

	p := NewBatchProcessor(ocr, cls, ana, rag, wf, upd, 2)
	res, err := p.ProcessBatch(context.Background(), "job1", []string{"only.txt"}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := res.(map[string]interface{})
	if m["batch_analysis_error"] != "analyzer down" {
		t.Errorf("expected batch_analysis_error, got %v", m["batch_analysis_error"])
	}
}

func TestProcessBatchContextCancellation(t *testing.T) {
	ocr := &mockOCR{fn: func(p string) (string, error) {
		time.Sleep(100 * time.Millisecond)
		return "text", nil
	}}
	p := NewBatchProcessor(ocr, nil, nil, nil, nil, &mockUpdater{progress: make(map[string]int)}, 2)
	
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	files := []string{"f1.txt", "f2.txt", "f3.txt"}
	_, err := p.ProcessBatch(ctx, "job1", files, Options{})

	if err == nil {
		t.Fatal("expected error due to cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
