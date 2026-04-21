package server

import "agentflow-go/internal/llmutil"

// classifyLegalDocumentFromOCR returns a map suitable for RAG ai_metadata and workflow summaries, or nil.
func (s *Server) classifyLegalDocumentFromOCR(text, logicalName string) map[string]interface{} {
	return llmutil.ClassifyDocument(s.llm, text, logicalName)
}

// Thin package-level wrappers retained for unit tests in this package.
func canonicalDocumentType(s string) string { return llmutil.CanonicalDocumentType(s) }

func parseClassificationResponse(raw string) (llmutil.ClassificationLLMJSON, bool) {
	return llmutil.ParseClassificationResponse(raw)
}

func buildClassificationPrompt(logicalName string) string {
	return llmutil.BuildClassificationPrompt(logicalName)
}
