package llmutil

import (
	"encoding/json"
	"log"
	"strings"
	"unicode/utf8"

	"agentflow-go/internal/llm"
)

const maxClassificationContextRunes = 10000

var AllowedDocumentTypes = map[string]struct{}{
	"resident_id_card":                        {},
	"wechat_chat_screenshot":                  {},
	"printed_chat_evidence":                   {},
	"wechat_pay_receipt":                      {},
	"online_case_filing_confirmation":         {},
	"civil_complaint":                         {},
	"civil_petition_fragment":                 {},
	"power_of_attorney":                       {},
	"iou_debt_note":                           {},
	"household_registration_query_result":     {},
	"litigation_service_address_confirmation": {},
	"litigation_fee_refund_account_form":      {},
	"legal_statute_excerpt":                   {},
	"spreadsheet_shipment_ledger":             {},
	"court_form_other":                        {},
	"other":                                   {},
}

type ClassificationLLMJSON struct {
	DocumentType  string                 `json:"document_type"`
	DisplayNameZH string                 `json:"display_name_zh"`
	Confidence    string                 `json:"confidence"`
	SummaryZH     string                 `json:"summary_zh"`
	Entities      map[string]interface{} `json:"entities"`
}

func BuildClassificationPrompt(logicalName string) string {
	return `你是中国法院立案与证据材料分类专家。你只能根据「材料正文」(OCR 文本)判断文档类型，**禁止**仅凭文件名或路径推断。若正文不足以判断，用 other。

仅输出一个 JSON 对象（不要 markdown、不要解释）。字段：
- document_type: 必须从下列英文 slug 中选一个（完全一致）：
  resident_id_card — 中华人民共和国居民身份证（正/反面，含姓名、公民身份号码、住址等版式）
  wechat_chat_screenshot — 微信等即时通讯聊天界面截图（气泡、语音条、联系人顶栏等）
  printed_chat_evidence — 纸质打印页上拼图/多张聊天截图（诉讼证据常见）
  wechat_pay_receipt — 微信支付/财付通等成功账单详情（负号金额、交易单号、支付时间等）
  online_case_filing_confirmation — 人民法院在线服务类「在线立案」流程页、「提交成功」提示及步骤条
  civil_complaint — 完整的民事起诉状（原告被告、诉讼请求、事实与理由等结构化栏目）
  civil_petition_fragment — 诉状片段/具状页等（叙事+致某某法院+具状人捺印，但缺少完整当事人信息表）
  power_of_attorney — 授权委托书（委托人、受委托人、律所、代理权限、案件类型）
  iou_debt_note — 欠款条/借条（欠款人、债权人、金额大小写、还款期限、违约责任等）
  household_registration_query_result — 外省市户籍人员信息查询表（公安/查询编号、户籍地址、查询单位律师事务所）
  litigation_service_address_confirmation — 诉讼文书送达地址确认书（电子送达、邮寄地址、法院名称）
  litigation_fee_refund_account_form — 诉讼费退费账号确认书（收款账号、开户行、法院抬头）
  legal_statute_excerpt — 法律法规或司法解释条文摘录打印页（民事诉讼法「送达」等援引）
  spreadsheet_shipment_ledger — 表格/出货日期与金额汇总（如「出货日期」「金额」「总计」类）
  court_form_other — 明显为法院诉讼文书样式但不属于上面具体表单的空白/其它表格
  other — 以上皆不符合
- display_name_zh: 简短中文类型名（4–12 字）
- confidence: 仅允许 "high" 或 "low"
- summary_zh: 一句中文概括材料要点（当事人、案由、金额、法院等，无则写「材料信息不足」），不超过 80 字
- entities: 对象，尽量抽取；无法抽取则省略键或给空数组。可用键示例：
  plaintiffs (string[] - 原告/上诉人/申请人等), defendants (string[] - 被告/被上诉人/被申请人等),
  amounts_cny (string[]), dates (string[]), id_numbers (string[]),
  phones (string[]), courts (string[]), app_or_platform (string)

材料来源标签（不得单独作为分类依据）：` + logicalName
}

// DoctypeFromClassification returns the canonical document_type slug from a
// classification map produced by ClassifyDocument, or "" if absent.
func DoctypeFromClassification(cls map[string]interface{}) string {
	if cls == nil {
		return ""
	}
	dt, _ := cls["document_type"].(string)
	return dt
}

func CanonicalDocumentType(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, ok := AllowedDocumentTypes[s]; ok {
		return s
	}
	return ""
}

func ParseClassificationResponse(raw string) (ClassificationLLMJSON, bool) {
	payload := ExtractJSONObject(raw)
	var out ClassificationLLMJSON
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		log.Printf("[classify] JSON parse: %v (raw snippet: %.120q)", err, raw)
		return out, false
	}
	out.DocumentType = strings.TrimSpace(out.DocumentType)
	out.DisplayNameZH = strings.TrimSpace(out.DisplayNameZH)
	out.SummaryZH = strings.TrimSpace(out.SummaryZH)
	if out.Entities == nil {
		out.Entities = map[string]interface{}{}
	}
	return out, true
}

// ClassifyDocument returns a classification map for a document, or nil on failure.
func ClassifyDocument(provider *llm.Provider, text, logicalName string) map[string]interface{} {
	if !IntakeTextUsable(text) {
		return nil
	}

	ctx := TruncateRunes(text, maxClassificationContextRunes)
	prompt := BuildClassificationPrompt(logicalName)

	raw, err := provider.Generate(prompt, ctx, llm.GenerationConfig{
		MaxTokens: 768,
		Temp:      0.05,
		Model:     "qwen-plus",
	})
	if err != nil {
		log.Printf("[classify] LLM: %v", err)
		return nil
	}

	inf, ok := ParseClassificationResponse(raw)
	if !ok {
		return nil
	}

	dt := CanonicalDocumentType(inf.DocumentType)
	if dt == "" {
		dt = "other"
	}
	conf := strings.ToLower(strings.TrimSpace(inf.Confidence))
	if conf != "high" && conf != "low" {
		conf = "low"
	}

	if rr := []rune(inf.DisplayNameZH); len(rr) > 32 {
		inf.DisplayNameZH = string(rr[:32]) + "…"
	}
	if rr := []rune(inf.SummaryZH); len(rr) > 120 {
		inf.SummaryZH = string(rr[:120]) + "…"
	}

	return map[string]interface{}{
		"document_type":     dt,
		"display_name_zh":   inf.DisplayNameZH,
		"confidence":        conf,
		"summary_zh":        inf.SummaryZH,
		"entities":          inf.Entities,
		"source":            "llm_ocr",
		"context_rune_hint": utf8.RuneCountInString(ctx),
	}
}
