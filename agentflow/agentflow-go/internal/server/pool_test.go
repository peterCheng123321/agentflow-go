package server

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"agentflow-go/internal/model"
)

func TestWorkerPoolBasicExecution(t *testing.T) {
	wp := NewWorkerPool(2)
	defer wp.Shutdown()

	var mu sync.Mutex
	var results []string

	for i := 0; i < 5; i++ {
		job := &model.Job{ID: fmt.Sprintf("job-%d", i)}
		i := i
		err := wp.Enqueue(context.Background(), job, 0, func(j *model.Job) (any, error) {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			results = append(results, fmt.Sprintf("done-%d", i))
			mu.Unlock()
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	count := len(results)
	mu.Unlock()

	if count != 5 {
		t.Errorf("expected 5 results, got %d", count)
	}
}

func TestWorkerPoolPriorityOrder(t *testing.T) {
	wp := NewWorkerPool(1)
	defer wp.Shutdown()

	var order []int
	var mu sync.Mutex
	block := make(chan struct{})

	job0 := &model.Job{ID: "job-0"}
	wp.Enqueue(context.Background(), job0, 5, func(j *model.Job) (any, error) {
		<-block
		mu.Lock()
		order = append(order, 5)
		mu.Unlock()
		return "ok", nil
	})

	time.Sleep(20 * time.Millisecond)

	for i := 1; i < 5; i++ {
		job := &model.Job{ID: fmt.Sprintf("job-%d", i)}
		priority := 5 - i
		err := wp.Enqueue(context.Background(), job, priority, func(j *model.Job) (any, error) {
			mu.Lock()
			order = append(order, priority)
			mu.Unlock()
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	time.Sleep(50 * time.Millisecond)
	close(block)
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 5 {
		t.Fatalf("expected 5 executions, got %d", len(order))
	}

	expected := []int{5, 4, 3, 2, 1}
	for i := range expected {
		if order[i] != expected[i] {
			t.Errorf("index %d: expected priority %d, got %d", i, expected[i], order[i])
		}
	}
}

func TestWorkerPoolContextCancellation(t *testing.T) {
	wp := NewWorkerPool(1)
	defer wp.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())

	job := &model.Job{ID: "job-cancel"}
	err := wp.Enqueue(ctx, job, 0, func(j *model.Job) (any, error) {
		time.Sleep(500 * time.Millisecond)
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	time.Sleep(100 * time.Millisecond)

	stats := wp.Stats()
	if stats["queue_length"] != 0 {
		t.Logf("queue should be drained after cancel, got %v", stats["queue_length"])
	}
}

func TestWorkerPoolCancelledContext(t *testing.T) {
	wp := NewWorkerPool(1)
	defer wp.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	job := &model.Job{ID: "job-pre-cancel"}
	err := wp.Enqueue(ctx, job, 0, func(j *model.Job) (any, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestWorkerPoolShutdown(t *testing.T) {
	wp := NewWorkerPool(2)

	var completed int
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		job := &model.Job{ID: fmt.Sprintf("job-%d", i)}
		err := wp.Enqueue(context.Background(), job, 0, func(j *model.Job) (any, error) {
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			completed++
			mu.Unlock()
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	wp.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	if completed == 0 {
		t.Error("expected some jobs to complete before shutdown")
	}
}

func TestWorkerPoolStats(t *testing.T) {
	wp := NewWorkerPool(3)
	defer wp.Shutdown()

	for i := 0; i < 5; i++ {
		job := &model.Job{ID: fmt.Sprintf("job-%d", i)}
		err := wp.Enqueue(context.Background(), job, 0, func(j *model.Job) (any, error) {
			time.Sleep(20 * time.Millisecond)
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	time.Sleep(200 * time.Millisecond)

	stats := wp.Stats()

	if stats["num_workers"] != 3 {
		t.Errorf("expected 3 workers, got %v", stats["num_workers"])
	}
	if stats["enqueued"] != int64(5) {
		t.Errorf("expected 5 enqueued, got %v", stats["enqueued"])
	}
	if stats["completed"] != int64(5) {
		t.Errorf("expected 5 completed, got %v", stats["completed"])
	}
}

func TestWorkerPoolPanicRecovery(t *testing.T) {
	wp := NewWorkerPool(1)
	defer wp.Shutdown()

	job := &model.Job{ID: "job-panic"}
	err := wp.Enqueue(context.Background(), job, 0, func(j *model.Job) (any, error) {
		panic("intentional panic")
	})
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if job.Status != model.JobStatusFailed {
		t.Errorf("expected job status 'failed' after panic, got %q", job.Status)
	}

	stats := wp.Stats()
	if stats["failed"] != int64(1) {
		t.Errorf("expected 1 failed job, got %v", stats["failed"])
	}

	job2 := &model.Job{ID: "job-after-panic"}
	err = wp.Enqueue(context.Background(), job2, 0, func(j *model.Job) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Enqueue after panic failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if job2.Status != model.JobStatusCompleted {
		t.Errorf("expected job2 to complete after panic recovery, got status %q", job2.Status)
	}
}

func TestWorkerPoolConcurrencyLimit(t *testing.T) {
	wp := NewWorkerPool(2)
	defer wp.Shutdown()

	var maxConcurrent int
	var currentConcurrent int
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		job := &model.Job{ID: fmt.Sprintf("job-%d", i)}
		err := wp.Enqueue(context.Background(), job, 0, func(j *model.Job) (any, error) {
			mu.Lock()
			currentConcurrent++
			if currentConcurrent > maxConcurrent {
				maxConcurrent = currentConcurrent
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			currentConcurrent--
			mu.Unlock()
			return "ok", nil
		})
		if err != nil {
			t.Fatalf("Enqueue failed: %v", err)
		}
	}

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if maxConcurrent > 2 {
		t.Errorf("max concurrent exceeded worker count: got %d, limit 2", maxConcurrent)
	}
}
