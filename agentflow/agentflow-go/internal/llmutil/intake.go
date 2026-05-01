package llmutil

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	"agentflow-go/internal/llm"
)

const maxIntakeContextRunes = 8000

var AllowedMatterTypes = map[string]struct{}{
	"Civil Litigation":         {},
	"Contract Dispute":         {},
	"Sales Contract Dispute":   {},
	"Debt Dispute":             {},
	"Loan Dispute":             {},
	"Lease Dispute":            {},
	"Labor Dispute":            {},
	"Commercial Lease Dispute": {},
}

var matterSynonyms = map[string]string{
	"民事诉讼":   "Civil Litigation",
	"民事纠纷":   "Civil Litigation",
	"合同纠纷":   "Contract Dispute",
	"买卖合同":   "Sales Contract Dispute",
	"买卖合同纠纷": "Sales Contract Dispute",
	"欠款":     "Debt Dispute",
	"借贷":     "Loan Dispute",
	"租赁":     "Lease Dispute",
	"商业租赁":   "Commercial Lease Dispute",
	"劳务":     "Labor Dispute",
	"劳动":     "Labor Dispute",
}

type IntakeLLMJSON struct {
	ClientName string `json:"client_name"`
	MatterType string `json:"matter_type"`
	Confidence string `json:"confidence"`
}

type BatchAnalysisJSON struct {
	ClientName string   `json:"client_name"`
	MatterType string   `json:"matter_type"`
	LLMError   string   `json:"-"`
	Plaintiffs []string `json:"plaintiffs"`
	Defendants []string `json:"defendants"`
	Files      []struct {
		Filename      string   `json:"filename"`
		DocumentType  string   `json:"document_type"`
		DisplayNameZH string   `json:"display_name_zh"`
		SummaryZH     string   `json:"summary_zh"`
		ClientName    string   `json:"client_name"`
		MatterType    string   `json:"matter_type"`
		Plaintiffs    []string `json:"plaintiffs"`
		Defendants    []string `json:"defendants"`
	} `json:"files"`
}

func IntakeTextUsable(text string) bool {
	if text == "" || strings.HasPrefix(strings.TrimSpace(text), "[OCR Error]") {
		return false
	}
	return utf8.RuneCountInString(text) >= 24
}

func TruncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

func ExtractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSpace(s)
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return s
	}
	return s[start : end+1]
}

func ParseIntakeResponse(raw string) (IntakeLLMJSON, bool) {
	payload := ExtractJSONObject(raw)
	var out IntakeLLMJSON
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		log.Printf("[intake] JSON parse: %v (raw snippet: %.120q)", err, raw)
		return out, false
	}
	out.ClientName = strings.TrimSpace(out.ClientName)
	out.MatterType = strings.TrimSpace(out.MatterType)
	return out, true
}

func CanonicalMatter(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return ""
	}
	if _, ok := AllowedMatterTypes[m]; ok {
		return m
	}
	for k := range AllowedMatterTypes {
		if strings.EqualFold(m, k) {
			return k
		}
	}
	if canon, ok := matterSynonyms[m]; ok {
		return canon
	}
	return ""
}

func BuildIntakePrompt(logicalName string) string {
	return `你是中国法律材料信息抽取助手。你的任务是阅读「材料正文」(OCR)，提取立案归档所需信息。

硬性规则：
- client_name **只能**根据正文中的当事人表述填写：原告诉称中的「原告」「申请人」「上诉人」「委托人」等后所附姓名/名称；起诉状首部当事人栏；法定代表人之前的单位名称等。**禁止**根据文件名、证据目录标题、或文件题名猜测姓名；文件名可能故意与正文不一致，不得使用。
- 若正文无法可靠识别一方当事人，client_name 必须为空字符串 ""。自然人姓名一般为2–4个汉字；不要把「新」「证据目录」等非姓名字样塞进 client_name。
- 禁止在任意字段使用「待分类」「未知客户」等占位词。

只输出一个JSON对象，不要markdown、不要解释。
字段：
- client_name: 见上；无法判断则 "" 。
- matter_type: 必须从下列英文短语中选一个（完全一致）：Civil Litigation, Contract Dispute, Sales Contract Dispute, Debt Dispute, Loan Dispute, Lease Dispute, Labor Dispute, Commercial Lease Dispute。无法判断则填 Civil Litigation。
- confidence: 仅允许 "high" 或 "low"

下列仅为材料来源标签（不得用于推断 client_name）：` + logicalName
}

// InferIntakeFromOCR extracts client name and matter type from OCR text.
func InferIntakeFromOCR(provider *llm.Provider, text, logicalName string) (clientName, matterType, source string) {
	matterType = ExtractMatterType(logicalName)
	clientName = ""
	source = "default"

	if !IntakeTextUsable(text) {
		clientName = "Unknown Client"
		source = "default"
		return
	}

	ctx := TruncateRunes(text, maxIntakeContextRunes)
	prompt := BuildIntakePrompt(logicalName)

	raw, err := provider.Generate(prompt, ctx, llm.GenerationConfig{
		MaxTokens: 512,
		Temp:      0.05,
		Model:     "qwen-plus",
	})
	if err != nil {
		log.Printf("[intake] LLM: %v", err)
		clientName = "Unknown Client"
		source = "default"
		return
	}

	inf, ok := ParseIntakeResponse(raw)
	if !ok {
		clientName = "Unknown Client"
		source = "default"
		return
	}

	source = "llm"
	if inf.ClientName != "" && !strings.EqualFold(inf.ClientName, "unknown") &&
		!strings.Contains(inf.ClientName, "待分类") {
		rr := []rune(inf.ClientName)
		if len(rr) > 64 {
			inf.ClientName = string(rr[:64]) + "…"
		}
		clientName = inf.ClientName
	} else {
		clientName = "Unknown Client"
	}

	if m := CanonicalMatter(inf.MatterType); m != "" {
		matterType = m
	} else {
		matterType = ExtractMatterType(logicalName)
	}
	return
}

// AnalyzeBatchFromOCR runs a batch LLM analysis across multiple documents.
func AnalyzeBatchFromOCR(provider *llm.Provider, docs map[string]string) BatchAnalysisJSON {
	return AnalyzeBatchFromOCRWithModel(provider, "qwen-plus", "", docs)
}

func AnalyzeBatchFromOCRWithModel(provider *llm.Provider, modelName, folderName string, docs map[string]string) BatchAnalysisJSON {
	var out BatchAnalysisJSON
	out.ClientName = "Unknown Client"
	out.MatterType = "Civil Litigation"

	if len(docs) == 0 {
		return out
	}

	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)

	var ctxBuilder strings.Builder
	if strings.TrimSpace(folderName) != "" {
		ctxBuilder.WriteString(fmt.Sprintf("Folder hint: %s\n\n", strings.TrimSpace(folderName)))
	}
	for _, name := range names {
		text := docs[name]
		if !IntakeTextUsable(text) {
			continue
		}
		ctxBuilder.WriteString(fmt.Sprintf("--- File: %s ---\n", name))
		ctxBuilder.WriteString(TruncateRunes(text, 1000))
		ctxBuilder.WriteString("\n\n")
	}

	ctx := TruncateRunes(ctxBuilder.String(), 20000)

	prompt := `你是专业的中国法律材料分析专家。我将提供一批用户同时上传的案件材料（含文件名和部分OCR文本）。
请综合分析这些材料，提取以下案件元数据，并为每个有效文件进行分类和摘要。

输出一个严格的 JSON 对象，不要包含 markdown 代码块。格式如下：
{
  "client_name": "委托人/原告的姓名（通常是提交这些材料维权的人，必填，如无法确定填 'Unknown Client'）",
  "matter_type": "必须从下列短语选一：Civil Litigation, Contract Dispute, Sales Contract Dispute, Debt Dispute, Loan Dispute, Lease Dispute, Labor Dispute, Commercial Lease Dispute",
  "plaintiffs": ["原告1", "原告2"],
  "defendants": ["被告1", "被告2"],
  "files": [
    {
      "filename": "必须与输入的文件名完全一致",
      "document_type": "文件类型slug（如 civil_complaint, resident_id_card, wechat_chat_screenshot, iou_debt_note, spreadsheet_shipment_ledger, court_form_other, other）",
      "display_name_zh": "简短中文类型名（如 微信聊天记录、身份证、起诉状）",
      "summary_zh": "一句中文概括该文件要点（如：涉及金额XXXX元的聊天记录）"
    }
  ]
}
`

	raw, err := provider.Generate(prompt, ctx, llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.1,
		Model:     modelName,
	})
	if err != nil {
		log.Printf("[batch-analyze] LLM: %v", err)
		out.LLMError = err.Error()
		return out
	}

	payload := ExtractJSONObject(raw)
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		log.Printf("[batch-analyze] JSON parse: %v (raw: %.120q)", err, raw)
	}

	if out.ClientName == "" {
		out.ClientName = "Unknown Client"
	}
	if out.MatterType == "" {
		out.MatterType = "Civil Litigation"
	}

	return out
}

func QuickIntakeFromFilenames(provider *llm.Provider, modelName, folderName string, filenames []string) BatchAnalysisJSON {
	var out BatchAnalysisJSON
	out.ClientName = "Unknown Client"
	out.MatterType = "Civil Litigation"
	if len(filenames) == 0 {
		return out
	}

	names := append([]string(nil), filenames...)
	sort.Strings(names)
	for _, name := range names {
		if label, margin, ok := extractMatterByEmbed(name); ok && margin >= MatterMargin {
			out.MatterType = label
			break
		}
		if out.MatterType == "Civil Litigation" {
			out.MatterType = ExtractMatterType(name)
		}
	}

	prompt := `你是中国法律材料快速归档助手。只根据文件夹名称和文件名，提取案件初始元数据。
禁止编造姓名；如果无法从文件夹名或文件名判断委托人/原告，client_name 填 "Unknown Client"。
matter_type 必须从下列英文短语选一：Civil Litigation, Contract Dispute, Sales Contract Dispute, Debt Dispute, Loan Dispute, Lease Dispute, Labor Dispute, Commercial Lease Dispute。
输出严格 JSON：
{
  "client_name": "Unknown Client",
  "matter_type": "Civil Litigation",
  "plaintiffs": [],
  "defendants": []
}`
	ctx := fmt.Sprintf("Folder: %s\nFiles:\n- %s", strings.TrimSpace(folderName), strings.Join(names, "\n- "))
	raw, err := provider.Generate(prompt, ctx, llm.GenerationConfig{
		MaxTokens: 512,
		Temp:      0.05,
		Model:     modelName,
	})
	if err != nil {
		out.LLMError = err.Error()
		return out
	}
	payload := ExtractJSONObject(raw)
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		out.LLMError = err.Error()
	}
	if out.ClientName == "" {
		out.ClientName = "Unknown Client"
	}
	if m := CanonicalMatter(out.MatterType); m != "" {
		out.MatterType = m
	} else {
		out.MatterType = "Civil Litigation"
	}
	return out
}

// ExtractMatterType guesses matter type from filename keywords.
func ExtractMatterType(filename string) string {
	if label, margin, ok := extractMatterByEmbed(filename); ok && margin >= MatterMargin {
		log.Printf("[matter-router] %q -> %s (margin %.3f)", filename, label, margin)
		return label
	}
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
