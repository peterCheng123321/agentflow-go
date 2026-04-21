package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentflow_io "agentflow-go/internal/io"
)

const (
	baseURL = "http://localhost:8000"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	testDir := "/Users/peter/Downloads/10.15 罗海霞"

	// Show Apple M-series info
	info := agentflow_io.GetAppleInfo()
	log.Printf("=== Apple M-Series Performance Test ===")
	log.Printf("Model: %s | Cores: %dP+%dE | Memory: %dGB | AMX: %v", 
		info.Model, info.Cores, info.EfficiencyCores, info.MemoryGB, info.SupportsAMX)
	log.Printf("Optimal Buffer: %d KB | Optimal Chunk: %d MB | Workers: %d",
		info.OptimalBufferSize/1024, info.OptimalChunkSize/(1024*1024), agentflow_io.OptimalWorkerCount())
	log.Printf("")

	// Scan all files
	var files []string
	filepath.Walk(testDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".pdf" || ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".docx" {
			files = append(files, path)
		}
		return nil
	})

	var totalSize int64
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			totalSize += info.Size()
		}
	}

	log.Printf("Files: %d | Total Size: %.1f MB", len(files), float64(totalSize)/(1024*1024))
	log.Printf("")

	// Create case first
	caseID, err := createCase("罗海霞", "劳动争议")
	if err != nil {
		log.Fatalf("Failed to create case: %v", err)
	}
	log.Printf("Created case: %s", caseID)
	log.Printf("")

	// Test 1: Measure API response time (just upload, don't wait for OCR)
	log.Printf("=== Test 1: Upload Response Time ===")
	start := time.Now()

	// Upload all files concurrently
	jobs := uploadFilesConcurrent(caseID, files, agentflow_io.OptimalWorkerCount())
	
	uploadTime := time.Since(start)
	log.Printf("Upload API response time: %v (%.2f sec)", uploadTime, uploadTime.Seconds())
	log.Printf("Throughput: %.2f MB/sec", float64(totalSize)/(1024*1024)/uploadTime.Seconds())
	log.Printf("Jobs submitted: %d", len(jobs))
	log.Printf("")

	// Test 2: Wait for first few jobs to complete
	log.Printf("=== Test 2: Processing Time (first 3 jobs) ===")
	start = time.Now()
	completed := 0
	for _, jobID := range jobs[:min(3, len(jobs))] {
		if waitForJob(jobID, 60*time.Second) {
			completed++
		}
	}
	processTime := time.Since(start)
	log.Printf("First %d jobs completed in: %v (%.2f sec)", completed, processTime, processTime.Seconds())
	log.Printf("Avg per job: %.2f sec", processTime.Seconds()/float64(completed))
	log.Printf("")

	// Test 3: Summary generation
	log.Printf("=== Test 3: Summary Generation ===")
	start = time.Now()
	summarizeCase(caseID)
	summaryTime := time.Since(start)
	log.Printf("Summary time: %v (%.2f sec)", summaryTime, summaryTime.Seconds())
	log.Printf("")

	// Summary
	log.Printf("=== Summary ===")
	log.Printf("Total files: %d (%.1f MB)", len(files), float64(totalSize)/(1024*1024))
	log.Printf("Upload time: %.2f sec", uploadTime.Seconds())
	log.Printf("Processing time: %.2f sec", processTime.Seconds())
	log.Printf("Summary time: %.2f sec", summaryTime.Seconds())
	totalEndToEnd := uploadTime + processTime + summaryTime
	log.Printf("End-to-end: %.2f sec", totalEndToEnd.Seconds())
}

func createCase(clientName, matterType string) (string, error) {
	payload := map[string]string{
		"client_name": clientName,
		"matter_type": matterType,
	}
	data, _ := json.Marshal(payload)

	resp, err := http.Post(baseURL+"/v1/cases/create", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create case failed: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	caseID, _ := result["case_id"].(string)
	return caseID, nil
}

func uploadFilesConcurrent(caseID string, files []string, workers int) []string {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var jobs []string
	sem := make(chan struct{}, workers)

	completedUploads := 0
	var uploadMu sync.Mutex

	for i, file := range files {
		wg.Add(1)
		go func(index int, filePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			filename := filepath.Base(filePath)
			file, _ := os.Open(filePath)
			defer file.Close()

			// Get file size
			stat, _ := file.Stat()
			fileSize := stat.Size()

			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, _ := writer.CreateFormFile("file", filename)
			// Copy file content
			io.Copy(part, file)
			writer.WriteField("case_id", caseID)
			writer.Close()

			req, _ := http.NewRequest("POST", baseURL+"/v1/upload", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())

			uploadStart := time.Now()
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				uploadLatency := time.Since(uploadStart)
				
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
					var result map[string]interface{}
					json.Unmarshal(body, &result)
					if jobID, ok := result["job_id"].(string); ok {
						mu.Lock()
						jobs = append(jobs, jobID)
						mu.Unlock()
					}
					uploadMu.Lock()
					completedUploads++
					uploadMu.Unlock()
					log.Printf("[%.3fs] Upload #%d: %s (%.1f KB)", 
						uploadLatency.Seconds(), index+1, filename, float64(fileSize)/1024)
				}
			} else {
				log.Printf("Failed: %s (error: %v)", filename, err)
			}
		}(i, file)
	}

	wg.Wait()
	return jobs
}

func waitForJob(jobID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/jobs/" + jobID)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var result map[string]interface{}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		json.Unmarshal(body, &result)

		status, _ := result["status"].(string)
		if status == "completed" {
			log.Printf("Job %s: completed", jobID)
			return true
		} else if status == "failed" {
			log.Printf("Job %s: failed", jobID)
			return false
		}

		time.Sleep(500 * time.Millisecond)
	}
	log.Printf("Job %s: timeout", jobID)
	return false
}

func summarizeCase(caseID string) {
	resp, err := http.Post(baseURL+"/v1/cases/"+caseID+"/summarize", "application/json", nil)
	if err != nil {
		log.Printf("Summary failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		if summary, ok := result["summary"].(string); ok {
			log.Printf("Summary generated: %d chars", len(summary))
			if len(summary) > 200 {
				log.Printf("Preview: %.200s...", summary)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
