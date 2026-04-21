package worker

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-go/internal/model"
)

var benchJobSeq atomic.Int64

func newBenchPool(b *testing.B, workers int) *Pool {
	b.Helper()
	p := New(workers)
	p.SetJobUpdater(func(_ string, _ func(*model.Job)) {})
	p.SetAfterTerminal(func(_ *model.Job) {})
	return p
}

func makeJob(id string) *model.Job {
	return &model.Job{
		ID:     id,
		Type:   model.JobType("bench"),
		Status: model.JobStatusPending,
	}
}

func BenchmarkWorkerPool_Throughput(b *testing.B) {
	p := newBenchPool(b, 4)
	defer p.Shutdown()

	var done atomic.Int64
	ctx := context.Background()
	fn := func(j *model.Job) (any, error) {
		done.Add(1)
		return nil, nil
	}

	b.ResetTimer()
	for i := range b.N {
		job := makeJob(fmt.Sprintf("bench-%d", benchJobSeq.Add(1)))
		_ = p.Enqueue(ctx, job, i%5, fn)
	}
	// drain
	deadline := time.Now().Add(10 * time.Second)
	for done.Load() < int64(b.N) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func BenchmarkWorkerPool_1worker(b *testing.B) {
	p := newBenchPool(b, 1)
	defer p.Shutdown()

	ctx := context.Background()
	noop := func(j *model.Job) (any, error) { return nil, nil }

	b.ResetTimer()
	for range b.N {
		job := makeJob(fmt.Sprintf("bench-%d", benchJobSeq.Add(1)))
		_ = p.Enqueue(ctx, job, 0, noop)
	}
}

func BenchmarkWorkerPool_8workers(b *testing.B) {
	p := newBenchPool(b, 8)
	defer p.Shutdown()

	var done atomic.Int64
	ctx := context.Background()
	fn := func(j *model.Job) (any, error) {
		done.Add(1)
		return nil, nil
	}

	b.ResetTimer()
	for i := range b.N {
		job := makeJob(fmt.Sprintf("bench-%d", benchJobSeq.Add(1)))
		_ = p.Enqueue(ctx, job, i%10, fn)
	}
	deadline := time.Now().Add(10 * time.Second)
	for done.Load() < int64(b.N) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func BenchmarkWorkerPool_Stats(b *testing.B) {
	p := newBenchPool(b, 2)
	defer p.Shutdown()
	b.ResetTimer()
	for range b.N {
		_ = p.Stats()
	}
}
