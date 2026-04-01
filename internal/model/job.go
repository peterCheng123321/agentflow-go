package model

import (
	"time"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

type Job struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // e.g., "ocr", "ingestion"
	Status    JobStatus `json:"status"`
	Progress  int       `json:"progress"` // 0-100
	Error     string    `json:"error,omitempty"`
	Result    any       `json:"result,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
