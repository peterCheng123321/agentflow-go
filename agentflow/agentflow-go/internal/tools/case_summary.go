package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"agentflow-go/internal/agent"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
	"agentflow-go/internal/rag"
)

// CaseSummary generates a structured AI summary of a case by pulling together
// its metadata, uploaded documents (via RAG), and notes.
type CaseSummary struct {
	ragMgr   *rag.Manager
	provider *llm.Provider
	model    string
}

func NewCaseSummary(ragMgr *rag.Manager, provider *llm.Provider, model string) *CaseSummary {
	if model == "" {
		model = "qwen-plus"
	}
	return &CaseSummary{ragMgr: ragMgr, provider: provider, model: model}
}

func (t *CaseSummary) Name() string { return "case_summary" }
func (t *CaseSummary) Description() string {
	return "Generate a comprehensive structured summary of a legal case including parties, facts, evidence strength, legal analysis, and recommended next actions."
}
func (t *CaseSummary) Params() map[string]agent.ParamSchema {
	return map[string]agent.ParamSchema{
		"client_name": {Description: "Client / plaintiff name", Required: true},
		"matter_type": {Description: "Type of legal matter", Required: true},
		"documents":   {Description: "Comma-separated list of uploaded document names"},
		"notes":       {Description: "Case notes to include in the analysis"},
		"focus":       {Description: "Optional focus area, e.g. 'evidence strength' or 'risk assessment'"},
	}
}

type CaseSummaryResult struct {
	Parties       PartiesSummary `json:"parties"`
	KeyFacts      []string       `json:"key_facts"`
	Evidence      []EvidenceItem `json:"evidence"`
	LegalAnalysis string         `json:"legal_analysis"`
	Risks         []string       `json:"risks"`
	NextActions   []string       `json:"next_actions"`
	Confidence    string         `json:"confidence"`
}

type PartiesSummary struct {
	Plaintiff string `json:"plaintiff"`
	Defendant string `json:"defendant"`
}

type EvidenceItem struct {
	Document string `json:"document"`
	Type     string `json:"type"`
	Strength string `json:"strength"` // strong / medium / weak
	Notes    string `json:"notes"`
}

func (t *CaseSummary) Execute(ctx context.Context, input agent.ToolInput) agent.ToolOutput {
	clientName, _ := input["client_name"].(string)
	matterType, _ := input["matter_type"].(string)
	if clientName == "" || matterType == "" {
		return agent.ToolOutput{Error: "client_name and matter_type are required"}
	}

	ragResults := t.ragMgr.Search(clientName+" "+matterType, 8)
	var ragContext strings.Builder
	for _, r := range ragResults {
		ragContext.WriteString(fmt.Sprintf("【%s】%s\n\n", r.Filename, r.Chunk))
	}

	docList, _ := input["documents"].(string)
	notes, _ := input["notes"].(string)
	focus, _ := input["focus"].(string)

	caseInfo := fmt.Sprintf("委托人: %s\n案件类型: %s", clientName, matterType)
	if docList != "" {
		caseInfo += "\n已上传文件: " + docList
	}
	if notes != "" {
		caseInfo += "\n案件备注:\n" + notes
	}
	if focus != "" {
		caseInfo += "\n分析重点: " + focus
	}

	prompt := `你是资深中国民事案件律师助理。根据以下案件信息和文件摘录，生成结构化案件分析报告。

输出严格的JSON对象（不要markdown代码块）：
{
  "parties": {"plaintiff": "原告姓名", "defendant": "被告姓名"},
  "key_facts": ["事实1", "事实2"],
  "evidence": [{"document": "文件名", "type": "证据类型", "strength": "strong/medium/weak", "notes": "说明"}],
  "legal_analysis": "法律分析（2-3段）",
  "risks": ["风险1"],
  "next_actions": ["建议行动1"],
  "confidence": "high/medium/low"
}

案件信息:
` + caseInfo

	ragCtx := llmutil.TruncateRunes(ragContext.String(), 8000)

	raw, err := t.provider.GenerateWithTimeout(ctx, prompt, ragCtx, llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.1,
		Model:     t.model,
	}, 0)
	if err != nil {
		log.Printf("[case_summary] LLM error: %v", err)
		return agent.ToolOutput{Error: fmt.Sprintf("LLM call failed: %v", err)}
	}

	payload := llmutil.ExtractJSONObject(raw)
	var result CaseSummaryResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return agent.ToolOutput{Text: raw, Data: map[string]string{"raw": raw}}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**当事人**: 原告 %s vs 被告 %s\n\n", result.Parties.Plaintiff, result.Parties.Defendant))
	if len(result.KeyFacts) > 0 {
		sb.WriteString("**关键事实**:\n")
		for _, f := range result.KeyFacts {
			sb.WriteString("  • " + f + "\n")
		}
		sb.WriteString("\n")
	}
	if result.LegalAnalysis != "" {
		sb.WriteString("**法律分析**: " + result.LegalAnalysis + "\n\n")
	}
	if len(result.Risks) > 0 {
		sb.WriteString("**风险**:\n")
		for _, r := range result.Risks {
			sb.WriteString("  ⚠ " + r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(result.NextActions) > 0 {
		sb.WriteString("**建议行动**:\n")
		for _, a := range result.NextActions {
			sb.WriteString("  → " + a + "\n")
		}
	}
	sb.WriteString(fmt.Sprintf("\n*置信度: %s*", result.Confidence))

	return agent.ToolOutput{Text: sb.String(), Data: result}
}
