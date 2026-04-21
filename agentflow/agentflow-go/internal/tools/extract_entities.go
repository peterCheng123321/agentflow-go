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
)

// EntityExtraction runs an LLM extraction pass over text to pull out
// structured legal entities: parties, amounts, dates, courts, claims.
type EntityExtraction struct {
	provider *llm.Provider
	model    string
}

func NewEntityExtraction(provider *llm.Provider, model string) *EntityExtraction {
	if model == "" {
		model = "qwen-plus"
	}
	return &EntityExtraction{provider: provider, model: model}
}

func (t *EntityExtraction) Name() string { return "extract_entities" }
func (t *EntityExtraction) Description() string {
	return "Extract structured legal entities from text: plaintiffs, defendants, amounts, dates, courts, legal claims. Use on OCR text or case summaries."
}
func (t *EntityExtraction) Params() map[string]agent.ParamSchema {
	return map[string]agent.ParamSchema{
		"text": {Description: "The text to extract entities from (OCR output or document content)", Required: true},
	}
}

type LegalEntities struct {
	Plaintiffs    []string `json:"plaintiffs"`
	Defendants    []string `json:"defendants"`
	Amounts       []string `json:"amounts_cny"`
	Dates         []string `json:"dates"`
	Courts        []string `json:"courts"`
	Claims        []string `json:"legal_claims"`
	IDNumbers     []string `json:"id_numbers"`
	Phones        []string `json:"phones"`
	ContractRefs  []string `json:"contract_refs"`
	KeyFacts      []string `json:"key_facts"`
}

func (t *EntityExtraction) Execute(ctx context.Context, input agent.ToolInput) agent.ToolOutput {
	text, _ := input["text"].(string)
	if strings.TrimSpace(text) == "" {
		return agent.ToolOutput{Error: "text is required"}
	}

	prompt := `你是中国法律文书信息提取专家。从以下文本中提取所有相关实体，输出严格的JSON对象（不要markdown）。

字段说明：
- plaintiffs: 原告/申请人姓名列表
- defendants: 被告/被申请人姓名列表
- amounts_cny: 所有涉案金额（包含单位，如"50000元"）
- dates: 关键日期（格式：YYYY-MM-DD 或原始中文描述）
- courts: 涉及的法院名称
- legal_claims: 诉讼请求列表（如"要求偿还借款50000元"）
- id_numbers: 身份证号码、统一社会信用代码等
- phones: 电话号码
- contract_refs: 合同编号、案号等引用编号
- key_facts: 3-5个关键事实（每条不超过30字）

对无法提取的字段填空数组 []。`

	ctx2 := llmutil.TruncateRunes(text, 6000)
	raw, err := t.provider.GenerateWithTimeout(ctx, prompt, ctx2, llm.GenerationConfig{
		MaxTokens: 1024,
		Temp:      0.05,
		Model:     t.model,
	}, 0)
	if err != nil {
		log.Printf("[extract_entities] LLM error: %v", err)
		return agent.ToolOutput{Error: fmt.Sprintf("LLM call failed: %v", err)}
	}

	payload := llmutil.ExtractJSONObject(raw)
	var entities LegalEntities
	if err := json.Unmarshal([]byte(payload), &entities); err != nil {
		log.Printf("[extract_entities] JSON parse error: %v", err)
		return agent.ToolOutput{Error: fmt.Sprintf("parse failed: %v", err), Text: raw}
	}

	// Build human-readable summary
	var sb strings.Builder
	if len(entities.Plaintiffs) > 0 {
		sb.WriteString("原告: " + strings.Join(entities.Plaintiffs, ", ") + "\n")
	}
	if len(entities.Defendants) > 0 {
		sb.WriteString("被告: " + strings.Join(entities.Defendants, ", ") + "\n")
	}
	if len(entities.Amounts) > 0 {
		sb.WriteString("涉案金额: " + strings.Join(entities.Amounts, ", ") + "\n")
	}
	if len(entities.Dates) > 0 {
		sb.WriteString("关键日期: " + strings.Join(entities.Dates, ", ") + "\n")
	}
	if len(entities.Courts) > 0 {
		sb.WriteString("法院: " + strings.Join(entities.Courts, ", ") + "\n")
	}
	if len(entities.Claims) > 0 {
		sb.WriteString("诉讼请求:\n")
		for _, c := range entities.Claims {
			sb.WriteString("  - " + c + "\n")
		}
	}
	if len(entities.KeyFacts) > 0 {
		sb.WriteString("关键事实:\n")
		for _, f := range entities.KeyFacts {
			sb.WriteString("  - " + f + "\n")
		}
	}

	return agent.ToolOutput{Text: sb.String(), Data: entities}
}
