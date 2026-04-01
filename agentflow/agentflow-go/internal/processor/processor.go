package processor

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"agentflow-go/internal/model"
	"golang.org/x/sync/errgroup"
)

// OCREngine defines the interface for text extraction from files.
type OCREngine interface {
	ScanFile(filePath string) (string, error)
}

// Classifier defines the interface for document classification.
type Classifier interface {
	Classify(ctx context.Context, text, filename string) (map[string]interface{}, error)
}

// BatchMeta represents the summary of a batch of files.
type BatchMeta struct {
	ClientName string
	MatterType string
	Files      []FileMeta
}

// FileMeta represents metadata for a single file in a batch analysis.
type FileMeta struct {
	Filename     string
	DocumentType string
}

// BatchAnalyzer defines the interface for analyzing a batch of documents together.
type BatchAnalyzer interface {
	AnalyzeBatch(ctx context.Context, docs map[string]string) (BatchMeta, error)
}

// RAGIngestor defines the interface for RAG ingestion.
type RAGIngestor interface {
	IngestFile(filePath string, text string, meta map[string]interface{}) error
}

// WorkflowAttacher defines the interface for attaching documents to cases.
type WorkflowAttacher interface {
	AttachDocument(caseID, filename string, extras ...map[string]interface{})
}

// JobUpdater defines the interface for updating job progress.
type JobUpdater interface {
	UpdateJob(id string, fn func(*model.Job))
}

// BatchProcessor manages the end-to-end processing of a batch of files.
type BatchProcessor struct {
	ocr           OCREngine
	classifier    Classifier
	batchAnalyzer BatchAnalyzer
	rag           RAGIngestor
	workflow      WorkflowAttacher
	jobUpdater    JobUpdater
	
	concurrencyLimit int
}

// Options provides configuration for a batch processing job.
type Options struct {
	CaseID string
}

// NewBatchProcessor creates a new instance of BatchProcessor.
func NewBatchProcessor(ocr OCREngine, classifier Classifier, analyzer BatchAnalyzer, rag RAGIngestor, workflow WorkflowAttacher, updater JobUpdater, concurrency int) *BatchProcessor {
	if concurrency < 1 {
		concurrency = 4
	}
	return &BatchProcessor{
		ocr:              ocr,
		classifier:       classifier,
		batchAnalyzer:    analyzer,
		rag:              rag,
		workflow:         workflow,
		jobUpdater:       updater,
		concurrencyLimit: concurrency,
	}
}

type fileState struct {
	filePath        string
	filename        string
	displayFilename string
	ocrText         string
	classification  map[string]interface{}
	ocrErr          error
	classifyErr     error
	ingestErr       error
	score           int
}

// ProcessBatch processes a list of files through OCR, classification, and ingestion.
func (p *BatchProcessor) ProcessBatch(ctx context.Context, jobID string, filePaths []string, opts Options) (any, error) {
	total := len(filePaths)
	if total == 0 {
		return map[string]interface{}{"count": 0}, nil
	}

	states := make([]*fileState, total)
	for i, path := range filePaths {
		fname := filepath.Base(path)
		displayFname := fname
		if parts := strings.SplitN(fname, "-", 2); len(parts) == 2 && len(parts[0]) > 10 {
			displayFname = parts[1]
		}
		states[i] = &fileState{
			filePath:        path,
			filename:        fname,
			displayFilename: displayFname,
		}
	}

	var completedCount int32
	var mu sync.Mutex

	// 1. OCR Stage
	g, gCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, p.concurrencyLimit)

	log.Printf("[Processor] Starting OCR for %d files with concurrency %d", total, p.concurrencyLimit)
	for _, s := range states {
		s := s // capture loop variable
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-gCtx.Done():
				return gCtx.Err()
			}

			text, err := p.ocr.ScanFile(s.filePath)
			s.ocrText = text
			s.ocrErr = err
			
			// Simple scoring for ranking
			if err == nil {
				s.score = len(text) / 100
				lower := strings.ToLower(s.displayFilename)
				if strings.Contains(lower, "起诉") || strings.Contains(lower, "complaint") {
					s.score += 500
				}
				if strings.Contains(lower, "身份证") || strings.Contains(lower, "id_card") {
					s.score += 400
				}
				if strings.Contains(lower, "合同") || strings.Contains(lower, "contract") {
					s.score += 300
				}
			}

			mu.Lock()
			completedCount++
			p.jobUpdater.UpdateJob(jobID, func(j *model.Job) {
				j.Progress = 5 + int(float64(completedCount)/float64(total)*40) // 5-45% is OCR
			})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("OCR batch failed: %w", err)
	}

	// 2. Batch Analysis Stage
	log.Printf("[Processor] OCR complete. Running batch analysis...")
	p.jobUpdater.UpdateJob(jobID, func(j *model.Job) { j.Progress = 45 })

	selectedDocs := make(map[string]string)
	// Rank and select top 12 for batch analysis
	sortedStates := make([]*fileState, len(states))
	copy(sortedStates, states)
	sort.Slice(sortedStates, func(i, j int) bool { return sortedStates[i].score > sortedStates[j].score })
	
	limit := 12
	if len(sortedStates) < limit {
		limit = len(sortedStates)
	}
	for i := 0; i < limit; i++ {
		if sortedStates[i].ocrErr == nil {
			selectedDocs[sortedStates[i].displayFilename] = sortedStates[i].ocrText
		}
	}

	var batchMeta BatchMeta
	if len(selectedDocs) > 0 {
		var bErr error
		batchMeta, bErr = p.batchAnalyzer.AnalyzeBatch(ctx, selectedDocs)
		if bErr != nil {
			log.Printf("[Processor] Batch analysis failed: %v", bErr)
		}
	}
	p.jobUpdater.UpdateJob(jobID, func(j *model.Job) { j.Progress = 55 })

	// 3. Classification Stage
	log.Printf("[Processor] Starting individual classification...")
	g, gCtx = errgroup.WithContext(ctx)
	for _, s := range states {
		if s.ocrErr != nil {
			continue
		}
		s := s
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-gCtx.Done():
				return gCtx.Err()
			}

			cls, err := p.classifier.Classify(gCtx, s.ocrText, s.displayFilename)
			s.classification = cls
			s.classifyErr = err
			return nil
		})
	}
	_ = g.Wait() // Classification failures are tolerated per file

	p.jobUpdater.UpdateJob(jobID, func(j *model.Job) { j.Progress = 75 })

	// 4. Ingestion Stage
	log.Printf("[Processor] Classification complete. Starting ingestion...")
	var uploaded []string
	var failed []string

	targetCaseID := opts.CaseID
	// In V2, if CaseID is empty, we could use batchMeta.ClientName to resolve it.

	for i, s := range states {
		if s.ocrErr != nil {
			failed = append(failed, s.displayFilename)
			continue
		}

		meta := map[string]interface{}{
			"filename": s.displayFilename,
		}
		if s.classification != nil {
			meta["classification"] = s.classification
		}

		if err := p.rag.IngestFile(s.filePath, s.ocrText, meta); err != nil {
			log.Printf("[Processor] RAG ingest failed for %s: %v", s.displayFilename, err)
			s.ingestErr = err
			failed = append(failed, s.displayFilename)
		} else {
			uploaded = append(uploaded, s.displayFilename)
			
			// Attach to workflow
			if targetCaseID != "" {
				if s.classification != nil {
					p.workflow.AttachDocument(targetCaseID, s.displayFilename, map[string]interface{}{
						"classification": s.classification,
					})
				} else {
					p.workflow.AttachDocument(targetCaseID, s.displayFilename)
				}
			}
		}

		p.jobUpdater.UpdateJob(jobID, func(j *model.Job) {
			j.Progress = 75 + int(float64(i+1)/float64(total)*25)
		})
	}

	return map[string]interface{}{
		"uploaded":    uploaded,
		"failed":      failed,
		"count":       len(uploaded),
		"case_id":     targetCaseID,
		"client_name": batchMeta.ClientName,
		"matter_type": batchMeta.MatterType,
	}, nil
}
