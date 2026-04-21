package core

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"agentflow-go/internal/llmutil"
	"agentflow-go/internal/model"
	"agentflow-go/internal/processor"
)

// UploadResult holds the outcome of a single-file upload job.
type UploadResult struct {
	Filename     string
	CaseID       string
	ClientName   string
	MatterType   string
	IntakeSource string
	TextLength   int
	Classification map[string]interface{}
}

// UploadFile copies src to the data/docs directory, then enqueues OCR + ingestion.
// Returns the job ID immediately; subscribe to state changes to track progress.
func (a *App) UploadFile(src string, reqCaseID string) (jobID string, err error) {
	logicalName := filepath.Base(src)

	os.MkdirAll(filepath.Join(a.Cfg.DataDir, "docs"), 0755)
	savePath := filepath.Join(a.Cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), logicalName))

	if err := copyFile(src, savePath); err != nil {
		return "", fmt.Errorf("copy file: %w", err)
	}

	jobID = a.SubmitJob("upload", func(j *model.Job) (any, error) {
		a.updateJob(j.ID, func(job *model.Job) { job.Progress = 12 })

		text, err := a.OCR.ScanFile(savePath)
		if err != nil {
			log.Printf("OCR error: %v", err)
			text = fmt.Sprintf("[OCR Error] %v", err)
		}

		a.updateJob(j.ID, func(job *model.Job) { job.Progress = 38 })

		meta := map[string]interface{}{
			"filename": logicalName,
		}
		var classification map[string]interface{}
		if cls := llmutil.ClassifyDocument(a.LLM, text, logicalName); cls != nil {
			meta["classification"] = cls
			classification = cls
		}
		if err := a.RAG.IngestFile(savePath, text, meta); err != nil {
			return nil, fmt.Errorf("ingestion failed: %v", err)
		}

		var finalCaseID, clientName, matterType, intakeSrc string

		if reqCaseID != "" {
			finalCaseID = reqCaseID
			snap, ok := a.Workflow.GetCaseSnapshot(finalCaseID)
			if ok {
				clientName = snap.ClientName
				matterType = snap.MatterType
				intakeSrc = "explicit_ui"
			}
			a.updateJob(j.ID, func(job *model.Job) { job.Progress = 70 })
		} else {
			a.updateJob(j.ID, func(job *model.Job) { job.Progress = 48 })
			clientName, matterType, intakeSrc = llmutil.InferIntakeFromOCR(a.LLM, text, logicalName)
			finalCaseID = a.ResolveCaseForDocument(clientName, matterType, logicalName)
			a.updateJob(j.ID, func(job *model.Job) { job.Progress = 90 })
		}

		if classification != nil {
			a.Workflow.AttachDocument(finalCaseID, logicalName, map[string]interface{}{
				"classification": classification,
			})
		} else {
			a.Workflow.AttachDocument(finalCaseID, logicalName)
		}

		out := UploadResult{
			Filename:       logicalName,
			CaseID:         finalCaseID,
			ClientName:     clientName,
			MatterType:     matterType,
			IntakeSource:   intakeSrc,
			TextLength:     len(text),
			Classification: classification,
		}
		return out, nil
	})

	return jobID, nil
}

// UploadBatch processes multiple files as a batch job.
func (a *App) UploadBatch(files []string, reqCaseID string) string {
	return a.SubmitJob("batch_upload", func(j *model.Job) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		result, err := a.Proc.ProcessBatch(ctx, j.ID, files, processor.Options{CaseID: reqCaseID})
		if err != nil {
			return nil, err
		}

		if reqCaseID == "" {
			res, ok := result.(map[string]interface{})
			if ok {
				clientName, _ := res["client_name"].(string)
				matterType, _ := res["matter_type"].(string)
				targetCaseID := a.ResolveCaseForDocument(clientName, matterType, "Batch Root")
				res["case_id"] = targetCaseID
			}
		}

		return result, nil
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
