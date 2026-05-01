package server

import (
	"context"
	"fmt"
	"time"
)

// ensureDocumentsProcessed checks if all documents in a case are processed (OCR'd and in RAG)
func (s *Server) ensureDocumentsProcessed(caseID string) error {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		return fmt.Errorf("case not found")
	}

	if len(c.UploadedDocuments) == 0 {
		return fmt.Errorf("no documents in case")
	}

	// Wait up to 2 minutes for documents to be processed
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for documents to be processed")
		case <-ticker.C:
			allProcessed := true
			for _, docName := range c.UploadedDocuments {
				doc, exists := s.rag.GetDocumentFlex(docName)
				if !exists || len(doc.Chunks) == 0 {
					allProcessed = false
					break
				}
			}

			if allProcessed {
				return nil
			}
		}
	}
}
