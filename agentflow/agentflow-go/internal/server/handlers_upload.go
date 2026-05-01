package server

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow-go/internal/llmutil"
	"agentflow-go/internal/model"
	"agentflow-go/internal/processor"
)

// allowedDirectoryUnderDataDir returns an absolute path only if dir is contained within DataDir (no path escape).
func (s *Server) allowedDirectoryUnderDataDir(dir string) (string, error) {
	absBase, err := filepath.Abs(s.cfg.DataDir)
	if err != nil {
		return "", err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absDir)
	if err != nil {
		return "", fmt.Errorf("invalid directory path")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("directory must be under server data directory %s", absBase)
	}
	return absDir, nil
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	reqCaseID := strings.TrimSpace(r.FormValue("case_id"))

	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "No file uploaded")
		return
	}
	defer file.Close()

	logicalName := sanitizeUploadedBasename(header.Filename)
	if logicalName == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	os.MkdirAll(filepath.Join(s.cfg.DataDir, "docs"), 0755)
	savePath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), logicalName))

	dst, err := os.Create(savePath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save file")
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to write file")
		return
	}
	fileSize := header.Size
	if fileSize <= 0 && written > 0 {
		fileSize = written
	}

	jobID := s.submitJob("upload", func(j *model.Job) (any, error) {
		s.updateJob(j.ID, func(job *model.Job) { job.Progress = 12 })
		text, err := s.ocr.ScanFile(savePath)
		if err != nil {
			log.Printf("OCR error: %v", err)
			text = fmt.Sprintf("[OCR Error] %v", err)
		}

		s.updateJob(j.ID, func(job *model.Job) { job.Progress = 38 })
		meta := map[string]interface{}{
			"filename": logicalName,
			"size":     fileSize,
		}
		var classification map[string]interface{}
		if cls := s.classifyLegalDocumentFromOCR(text, logicalName); cls != nil {
			meta["classification"] = cls
			classification = cls
		}
		if err := s.rag.IngestFile(savePath, text, meta); err != nil {
			return nil, fmt.Errorf("ingestion failed: %v", err)
		}

		var finalCaseID string
		var clientName, matterType, intakeSrc string

		if reqCaseID != "" {
			finalCaseID = reqCaseID
			snap, ok := s.workflow.GetCaseSnapshot(finalCaseID)
			if ok {
				clientName = snap.ClientName
				matterType = snap.MatterType
				intakeSrc = "explicit_ui"
			}
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 70 })
		} else {
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 48 })
			clientName, matterType, intakeSrc = s.inferIntakeFromOCR(text, logicalName)
			finalCaseID = s.resolveCaseForDocument(clientName, matterType, logicalName)
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 90 })
		}

		if classification != nil {
			extras := map[string]interface{}{
				"classification": classification,
			}
			if dt := llmutil.DoctypeFromClassification(classification); dt != "" {
				extras["doctype"] = dt
			}
			s.workflow.AttachDocument(finalCaseID, logicalName, extras)
		} else {
			s.workflow.AttachDocument(finalCaseID, logicalName)
		}

		out := map[string]interface{}{
			"status":        "uploaded",
			"filename":      logicalName,
			"case_id":       finalCaseID,
			"client_name":   clientName,
			"matter_type":   matterType,
			"intake_source": intakeSrc,
			"text_length":   len(text),
		}
		if classification != nil {
			out["classification"] = classification
		}
		return out, nil
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":   "queued",
		"job_id":   jobID,
		"filename": logicalName,
	})
}

func (s *Server) handleUploadBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)

	if err := r.ParseMultipartForm(500 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	reqCaseID := strings.TrimSpace(r.FormValue("case_id"))
	fileHeaders := r.MultipartForm.File["files"]

	if len(fileHeaders) == 0 {
		s.writeError(w, http.StatusBadRequest, "No files uploaded")
		return
	}

	os.MkdirAll(filepath.Join(s.cfg.DataDir, "docs"), 0755)

	var savedFiles []string
	for _, header := range fileHeaders {
		file, err := header.Open()
		if err != nil {
			continue
		}

		logicalName := sanitizeUploadedBasename(header.Filename)
		if logicalName == "" {
			file.Close()
			continue
		}

		if strings.HasSuffix(strings.ToLower(logicalName), ".zip") {
			tmpZipPath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("tmp-%d-%s", time.Now().UnixNano(), logicalName))
			tmpZip, _ := os.Create(tmpZipPath)
			io.Copy(tmpZip, file)
			tmpZip.Close()
			file.Close()

			reader, err := zip.OpenReader(tmpZipPath)
			if err == nil {
				for _, f := range reader.File {
					if f.FileInfo().IsDir() {
						continue
					}
					rc, err := f.Open()
					if err != nil {
						continue
					}
					fName := sanitizeUploadedBasename(filepath.Base(f.Name))
					if fName != "" {
						outPath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), fName))
						dst, err := os.Create(outPath)
						if err == nil {
							io.Copy(dst, rc)
							dst.Close()
							savedFiles = append(savedFiles, outPath)
						}
					}
					rc.Close()
				}
				reader.Close()
			}
			os.Remove(tmpZipPath)
			continue
		}

		savePath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), logicalName))
		dst, err := os.Create(savePath)
		if err == nil {
			io.Copy(dst, file)
			dst.Close()
			savedFiles = append(savedFiles, savePath)
		}
		file.Close()
	}

	jobID := s.submitJob("batch_upload", func(j *model.Job) (any, error) {
		return s.processBatchUpload(j, savedFiles, reqCaseID)
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id": jobID,
	})
}

func (s *Server) processBatchUpload(j *model.Job, files []string, reqCaseID string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	opts := processor.Options{
		CaseID: reqCaseID,
	}

	result, err := s.processor.ProcessBatch(ctx, j.ID, files, opts)
	if err != nil {
		return nil, err
	}

	if reqCaseID == "" {
		res := result.(map[string]interface{})
		clientName, _ := res["client_name"].(string)
		matterType, _ := res["matter_type"].(string)

		targetCaseID := s.resolveCaseForDocument(clientName, matterType, "Batch Root")
		res["case_id"] = targetCaseID
		s.attachBatchUploadsToCase(targetCaseID, res["uploaded"])
	}

	return result, nil
}

func (s *Server) handleUploadDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		DirectoryPath string `json:"directory_path"`
	}

	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	allowedDir, err := s.allowedDirectoryUnderDataDir(req.DirectoryPath)
	if err != nil {
		s.writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var files []string
	err = filepath.WalkDir(allowedDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Cannot read directory: %v", err))
		return
	}

	jobID := s.submitJob("directory_upload", func(j *model.Job) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		result, err := s.processor.ProcessBatch(ctx, j.ID, files, processor.Options{})
		if err != nil {
			return nil, err
		}

		res := result.(map[string]interface{})
		clientName, _ := res["client_name"].(string)
		matterType, _ := res["matter_type"].(string)

		if clientName != "" && clientName != "Unknown Client" {
			targetCaseID := s.resolveCaseForDocument(clientName, matterType, "Directory Root")
			res["case_id"] = targetCaseID
			s.attachBatchUploadsToCase(targetCaseID, res["uploaded"])
		}

		return result, nil
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id": jobID,
	})
}

func sanitizeUploadedBasename(name string) string {
	b := filepath.Base(name)
	b = strings.ReplaceAll(b, string(os.PathSeparator), "_")
	b = strings.ReplaceAll(b, "/", "_")
	b = strings.ReplaceAll(b, "..", "_")
	b = strings.TrimSpace(b)
	if b == "" || b == "." {
		return ""
	}
	return b
}

func extractMatterType(filename string) string {
	type hint struct {
		keyword string
		matter  string
	}
	hints := []hint{
		{"买卖", "Sales Contract Dispute"},
		{"租赁", "Lease Dispute"},
		{"劳务", "Labor Dispute"},
		{"劳动", "Labor Dispute"},
		{"借贷", "Loan Dispute"},
		{"欠款", "Debt Dispute"},
		{"合同", "Contract Dispute"},
		{"起诉", "Civil Litigation"},
		{"诉讼", "Civil Litigation"},
	}

	for _, h := range hints {
		if strings.Contains(filename, h.keyword) {
			return h.matter
		}
	}

	return "Civil Litigation"
}
