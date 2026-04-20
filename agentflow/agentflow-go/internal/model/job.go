package model

import (
	"time"
)

type JobType string

const (
	JobTypeOCR        JobType = "ocr"
	JobTypeIngestion  JobType = "ingestion"
	JobTypeClassification JobType = "classification"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

type Job struct {
	ID        string                 `json:"id"`
	Type      JobType                `json:"type"`
	Status    JobStatus              `json:"status"`
	Progress  int                    `json:"progress"` // 0-100
	Error     string                 `json:"error,omitempty"`
	Result    any                    `json:"result,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	CaseID    string                 `json:"case_id,omitempty"`
	FileCount int                    `json:"file_count,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}
