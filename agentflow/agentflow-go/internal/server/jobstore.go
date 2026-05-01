package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agentflow-go/internal/model"

	_ "modernc.org/sqlite"
)

// JobStore persists jobs to storage (SQLite)
type JobStore struct {
	db   *sql.DB
	mu   sync.RWMutex
	path string
}

// JobFilter for querying jobs
type JobFilter struct {
	Status     model.JobStatus
	OlderThan  time.Duration
	Limit      int
	CaseID     string
}

// NewJobStore creates a new persistent job store
func NewJobStore(dataDir string) (*JobStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "jobs.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Set SQLite pragmas for performance
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return nil, fmt.Errorf("set pragmas: %w", err)
	}

	js := &JobStore{
		db:   db,
		path: dbPath,
	}

	if err := js.init(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return js, nil
}

func (js *JobStore) init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		progress INTEGER DEFAULT 0,
		result TEXT,
		error TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		case_id TEXT,
		file_count INTEGER DEFAULT 0,
		metadata TEXT
	);

	CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status);
	CREATE INDEX IF NOT EXISTS jobs_case_id ON jobs(case_id);
	CREATE INDEX IF NOT EXISTS jobs_created_at ON jobs(created_at);
	`

	if _, err := js.db.Exec(schema); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}

	return nil
}

// Create stores a new job
func (js *JobStore) Create(job *model.Job) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	metaJSON, _ := json.Marshal(job.Metadata)
	resultJSON, _ := json.Marshal(job.Result)

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)
	_, err := js.db.Exec(`
		INSERT INTO jobs (id, type, status, progress, result, error, created_at, updated_at, case_id, file_count, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.Type, job.Status, job.Progress, resultJSON, job.Error, nowStr, nowStr, job.CaseID, job.FileCount, metaJSON)

	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	job.CreatedAt = now
	job.UpdatedAt = now
	return nil
}

// Get retrieves a job by ID
func (js *JobStore) Get(id string) (*model.Job, error) {
	js.mu.RLock()
	defer js.mu.RUnlock()

	var job model.Job
	var resultJSON, metaJSON []byte
	var createdAtStr, updatedAtStr string

	err := js.db.QueryRow(`
		SELECT id, type, status, progress, result, error, created_at, updated_at, case_id, file_count, metadata
		FROM jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.Type, &job.Status, &job.Progress,
		&resultJSON, &job.Error, &createdAtStr, &updatedAtStr,
		&job.CaseID, &job.FileCount, &metaJSON,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("query job: %w", err)
	}

	// Parse time strings
	if createdAtStr != "" {
		job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	}
	if updatedAtStr != "" {
		job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAtStr)
	}

	if resultJSON != nil {
		_ = json.Unmarshal(resultJSON, &job.Result)
	}
	if metaJSON != nil {
		_ = json.Unmarshal(metaJSON, &job.Metadata)
	}

	return &job, nil
}

// Update modifies a job with the given function
func (js *JobStore) Update(id string, fn func(*model.Job) error) error {
	// First, get the current job without holding the lock
	var job model.Job
	var resultJSON, metaJSON []byte
	var createdAtStr, updatedAtStr string

	err := js.db.QueryRow(`
		SELECT id, type, status, progress, result, error, created_at, updated_at, case_id, file_count, metadata
		FROM jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.Type, &job.Status, &job.Progress,
		&resultJSON, &job.Error, &createdAtStr, &updatedAtStr,
		&job.CaseID, &job.FileCount, &metaJSON,
	)

	if err == sql.ErrNoRows {
		return fmt.Errorf("job not found: %s", id)
	}
	if err != nil {
		return fmt.Errorf("query job: %w", err)
	}

	// Parse time strings
	if createdAtStr != "" {
		job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	}
	if updatedAtStr != "" {
		job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAtStr)
	}

	if resultJSON != nil {
		_ = json.Unmarshal(resultJSON, &job.Result)
	}
	if metaJSON != nil {
		_ = json.Unmarshal(metaJSON, &job.Metadata)
	}

	// Apply mutation
	if err := fn(&job); err != nil {
		return err
	}

	// Serialize updates
	metaJSON, _ = json.Marshal(job.Metadata)
	resultJSON, _ = json.Marshal(job.Result)
	now := time.Now().UTC()
	job.UpdatedAt = now
	updatedAtStr = now.Format(time.RFC3339Nano)

	// Update database
	_, err = js.db.Exec(`
		UPDATE jobs
		SET status = ?, progress = ?, result = ?, error = ?, updated_at = ?, case_id = ?, metadata = ?
		WHERE id = ?
	`, job.Status, job.Progress, resultJSON, job.Error, updatedAtStr, job.CaseID, metaJSON, job.ID)

	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}

	return nil
}

// List retrieves jobs matching the filter
func (js *JobStore) List(filter JobFilter) ([]*model.Job, error) {
	js.mu.RLock()
	defer js.mu.RUnlock()

	query := `SELECT id, type, status, progress, result, error, created_at, updated_at, case_id, file_count, metadata FROM jobs WHERE 1=1`
	args := []interface{}{}

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.CaseID != "" {
		query += " AND case_id = ?"
		args = append(args, filter.CaseID)
	}
	if filter.OlderThan > 0 {
		cutoff := time.Now().Add(-filter.OlderThan).UTC().Format(time.RFC3339Nano)
		query += " AND created_at < ?"
		args = append(args, cutoff)
	}
	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := js.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		var job model.Job
		var resultJSON, metaJSON []byte
		var createdAtStr, updatedAtStr string

		if err := rows.Scan(
			&job.ID, &job.Type, &job.Status, &job.Progress,
			&resultJSON, &job.Error, &createdAtStr, &updatedAtStr,
			&job.CaseID, &job.FileCount, &metaJSON,
		); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}

		// Parse time strings
		if createdAtStr != "" {
			job.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		}
		if updatedAtStr != "" {
			job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAtStr)
		}

		if resultJSON != nil {
			_ = json.Unmarshal(resultJSON, &job.Result)
		}
		if metaJSON != nil {
			_ = json.Unmarshal(metaJSON, &job.Metadata)
		}

		jobs = append(jobs, &job)
	}

	return jobs, nil
}

// DeleteOlderThan removes jobs older than the given duration
func (js *JobStore) DeleteOlderThan(age time.Duration) (int64, error) {
	js.mu.Lock()
	defer js.mu.Unlock()

	cutoff := time.Now().Add(-age).UTC().Format(time.RFC3339Nano)

	result, err := js.db.Exec(`DELETE FROM jobs WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old jobs: %w", err)
	}

	return result.RowsAffected()
}

// Delete removes a job by ID
func (js *JobStore) Delete(id string) error {
	js.mu.Lock()
	defer js.mu.Unlock()

	_, err := js.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}

	return nil
}

// Stats returns job statistics
func (js *JobStore) Stats() (map[string]int64, error) {
	js.mu.RLock()
	defer js.mu.RUnlock()

	stats := make(map[string]int64)

	rows, err := js.db.Query(`
		SELECT status, COUNT(*) as count FROM jobs GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		stats[status] = count
	}

	// Get total count
	var total int64
	if err := js.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&total); err == nil {
		stats["total"] = total
	}

	return stats, nil
}

// Close closes the database connection
func (js *JobStore) Close() error {
	return js.db.Close()
}

// Vacuum optimizes the database
func (js *JobStore) Vacuum() error {
	js.mu.Lock()
	defer js.mu.Unlock()

	_, err := js.db.Exec(`VACUUM`)
	return err
}
