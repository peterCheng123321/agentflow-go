package tools

import (
	"context"
	"fmt"
	"strings"

	"agentflow-go/internal/agent"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
)

// ClassifyDoc runs LLM-based document classification on raw text.
type ClassifyDoc struct {
	provider *llm.Provider
}

func NewClassifyDoc(provider *llm.Provider) *ClassifyDoc {
	return &ClassifyDoc{provider: provider}
}

func (t *ClassifyDoc) Name() string { return "classify_document" }
func (t *ClassifyDoc) Description() string {
	return "Classify a legal document by type (civil_complaint, iou_debt_note, power_of_attorney, etc.) and extract key metadata. Use when you need to identify what a document is."
}
func (t *ClassifyDoc) Params() map[string]agent.ParamSchema {
	return map[string]agent.ParamSchema{
		"text":     {Description: "OCR text content of the document", Required: true},
		"filename": {Description: "Original filename (used as a hint, not for classification)"},
	}
}

func (t *ClassifyDoc) Execute(_ context.Context, input agent.ToolInput) agent.ToolOutput {
	text, _ := input["text"].(string)
	if strings.TrimSpace(text) == "" {
		return agent.ToolOutput{Error: "text is required"}
	}
	filename, _ := input["filename"].(string)

	result := llmutil.ClassifyDocument(t.provider, text, filename)
	if result == nil {
		return agent.ToolOutput{Error: "classification returned nil (text may be too short or unreadable)"}
	}

	docType, _ := result["document_type"].(string)
	displayName, _ := result["display_name_zh"].(string)
	confidence, _ := result["confidence"].(string)
	summary, _ := result["summary_zh"].(string)

	text2 := fmt.Sprintf("文档类型: %s (%s)\n置信度: %s\n摘要: %s", docType, displayName, confidence, summary)

	return agent.ToolOutput{Text: text2, Data: result}
}
