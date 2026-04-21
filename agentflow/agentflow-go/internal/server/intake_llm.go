package server

import "agentflow-go/internal/llmutil"

func (s *Server) analyzeBatchFromOCR(docs map[string]string) llmutil.BatchAnalysisJSON {
	return llmutil.AnalyzeBatchFromOCR(s.llm, docs)
}

func (s *Server) inferIntakeFromOCR(text, logicalName string) (clientName, matterType, source string) {
	return llmutil.InferIntakeFromOCR(s.llm, text, logicalName)
}

// Package-level aliases retained for unit tests in this package.
type intakeLLMJSON = llmutil.IntakeLLMJSON

func intakeTextUsable(text string) bool           { return llmutil.IntakeTextUsable(text) }
func truncateRunes(s string, max int) string       { return llmutil.TruncateRunes(s, max) }
func extractJSONObject(s string) string            { return llmutil.ExtractJSONObject(s) }
func canonicalMatter(m string) string              { return llmutil.CanonicalMatter(m) }
func parseIntakeResponse(r string) (llmutil.IntakeLLMJSON, bool) { return llmutil.ParseIntakeResponse(r) }
func buildIntakePrompt(n string) string            { return llmutil.BuildIntakePrompt(n) }
