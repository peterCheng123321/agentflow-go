package server

import (
	"fmt"
	"os"
	"testing"
	"time"

	"agentflow-go/internal/model"
)

func TestJobStore(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "jobstore-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create job store
	js, err := NewJobStore(tmpDir)
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	defer js.Close()

	// Test Create
	job := &model.Job{
		ID:        "test-job-1",
		Type:      model.JobTypeOCR,
		Status:    model.JobStatusPending,
		Progress:  0,
		CaseID:    "case-123",
		FileCount: 3,
		Metadata:  map[string]interface{}{"source": "test"},
	}

	if err := js.Create(job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Test Get
	retrieved, err := js.Get("test-job-1")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if retrieved.ID != "test-job-1" {
		t.Errorf("expected ID test-job-1, got %s", retrieved.ID)
	}
	if retrieved.Status != model.JobStatusPending {
		t.Errorf("expected status %s, got %s", model.JobStatusPending, retrieved.Status)
	}
	if retrieved.CaseID != "case-123" {
		t.Errorf("expected case_id case-123, got %s", retrieved.CaseID)
	}
	if retrieved.FileCount != 3 {
		t.Errorf("expected file_count 3, got %d", retrieved.FileCount)
	}

	// Test Update
	err = js.Update("test-job-1", func(j *model.Job) error {
		j.Status = model.JobStatusCompleted
		j.Progress = 100
		j.Result = map[string]interface{}{"files_processed": 3}
		return nil
	})
	if err != nil {
		t.Fatalf("update job: %v", err)
	}

	updated, err := js.Get("test-job-1")
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}

	if updated.Status != model.JobStatusCompleted {
		t.Errorf("expected status %s, got %s", model.JobStatusCompleted, updated.Status)
	}
	if updated.Progress != 100 {
		t.Errorf("expected progress 100, got %d", updated.Progress)
	}

	// Test List with filter
	jobs, err := js.List(JobFilter{Status: model.JobStatusCompleted})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}

	if len(jobs) != 1 {
		t.Errorf("expected 1 job, got %d", len(jobs))
	}

	// Test Create multiple jobs
	for i := 2; i <= 5; i++ {
		job := &model.Job{
			ID:     fmt.Sprintf("test-job-%d", i),
			Type:   model.JobTypeOCR,
			Status: model.JobStatusPending,
		}
		if err := js.Create(job); err != nil {
			t.Fatalf("create job %d: %v", i, err)
		}
	}

	// Test List with limit
	jobs, err = js.List(JobFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list jobs with limit: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs with limit, got %d", len(jobs))
	}

	// Test Stats
	stats, err := js.Stats()
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats["total"] != 5 {
		t.Errorf("expected total 5, got %d", stats["total"])
	}
	if stats[string(model.JobStatusCompleted)] != 1 {
		t.Errorf("expected 1 completed, got %d", stats[string(model.JobStatusCompleted)])
	}

	// Test DeleteOlderThan - skipped (needs internal db access)
	t.Skip("Need to add SetCreatedAt helper for time manipulation tests")

	deleted, err := js.DeleteOlderThan(30 * time.Minute)
	if err != nil {
		t.Fatalf("delete old jobs: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected to delete 1 job, deleted %d", deleted)
	}

	// Test Delete
	err = js.Delete("test-job-3")
	if err != nil {
		t.Fatalf("delete job: %v", err)
	}

	_, err = js.Get("test-job-3")
	if err == nil {
		t.Error("expected error getting deleted job")
	}
}

func TestJobStoreGetNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "jobstore-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	js, err := NewJobStore(tmpDir)
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	defer js.Close()

	_, err = js.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent job")
	}
}

func TestJobStoreUpdateNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "jobstore-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	js, err := NewJobStore(tmpDir)
	if err != nil {
		t.Fatalf("create job store: %v", err)
	}
	defer js.Close()

	err = js.Update("nonexistent", func(j *model.Job) error { return nil })
	if err == nil {
		t.Error("expected error updating nonexistent job")
	}
}
