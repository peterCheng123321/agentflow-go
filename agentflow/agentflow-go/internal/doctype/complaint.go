package doctype

import (
	"fmt"
	"strings"
)

// 起诉状 — Civil Complaint. The headline filing in any civil action under
// Chinese procedure law. Mandates four canonical sections in fixed order:
//   当事人 (Parties)         — plaintiff + defendant identifiers
//   诉讼请求 (Claims)        — numbered list of demands the plaintiff makes
//   事实与理由 (Facts)       — narrative grounding the claims in evidence
//   证据 (Evidence)          — list of attached documents
//
// The prompt below instructs the LLM to produce exactly this structure, in
// Chinese, citing the source filename + evidence reference for every claim
// of fact. Any field the evidence doesn't cover gets "材料未载明" — never
// fabricated.
func init() {
	MustRegister(DocType{
		ID:          "complaint",
		LabelZH:     "起诉状",
		LabelEN:     "Civil Complaint",
		Icon:        "doc.text",
		Description: "Civil complaint filing — opens a lawsuit in court.",
		RequiredFields: []string{
			"client_name",
			"matter_type",
			// plaintiffs/defendants are nice-to-have but the LLM can extract
			// them from evidence if missing, so don't gate on them.
		},
		Sections: []SectionSpec{
			{
				ID:          "parties",
				TitleZH:     "当事人",
				Required:    true,
				Description: "原告、被告的姓名、身份证号、住址、联系方式（如材料中有）。",
			},
			{
				ID:          "claims",
				TitleZH:     "诉讼请求",
				Required:    true,
				Description: "列明原告的具体诉讼请求，逐项编号；包括标的金额、利息、违约金、案件受理费等。",
			},
			{
				ID:          "facts",
				TitleZH:     "事实与理由",
				Required:    true,
				Description: "陈述案件事实，按时间顺序展开；引用证据时标注来源文件名与具体位置。",
			},
			{
				ID:          "evidence",
				TitleZH:     "证据",
				Required:    true,
				Description: "已附证据清单（编号、名称、证明对象）。",
			},
		},
		Prompt: buildComplaintPrompt,
	})
}

func buildComplaintPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(`你是一名严谨的中国执业律师助理。请基于以下案件信息和证据材料，起草一份《民事起诉状》。

【输出格式】严格的 JSON 对象，禁止使用 Markdown 代码块。结构如下：
{
  "title": "民事起诉状",
  "sections": [
    {
      "id": "parties",
      "title": "当事人",
      "content": "（中文段落，包含原告与被告的身份信息）",
      "highlights": [{"text":"被引用的关键事实片段","reason":"为何重要","category":"party","source_file":"证据文件名","source_ref":"位置如p.3 §2"}]
    },
    {
      "id": "claims",
      "title": "诉讼请求",
      "content": "（编号列出每一项请求）",
      "highlights": []
    },
    {
      "id": "facts",
      "title": "事实与理由",
      "content": "（按时间顺序陈述案件事实，引用证据）",
      "highlights": [{"text":"...","reason":"...","category":"fact|amount|date","source_file":"...","source_ref":"..."}]
    },
    {
      "id": "evidence",
      "title": "证据",
      "content": "（编号列出所附证据：1.证据名称——证明对象）",
      "highlights": []
    }
  ]
}

【输出规则】
1. sections 数组必须正好包含上述四个 id，按上述顺序输出，不得增减、不得重命名。
2. content 字段为中文。所有金额、日期、人名应与证据保持一致，不一致或不确定时写 "材料未载明"，禁止臆造。
3. highlights 数组用于记录关键事实的来源；category 必须是 "party" | "claim" | "fact" | "amount" | "date" | "clause" 之一；source_file 必须与下方"证据材料"中提供的文件名完全一致；source_ref 例如 "p.3 §2"。
4. 诉讼请求一栏需逐项编号（一、二、三……），最后单列"诉讼费用由被告承担"。
5. 事实与理由部分使用客观叙述，避免情绪化语言。
6. 如证据不足以支持某项请求，请在事实与理由中明确指出"现有证据不足以证明……"。

`)

	// Case meta block
	b.WriteString("【案件元信息】\n")
	if ctx.ClientName != "" {
		b.WriteString(fmt.Sprintf("当事人/原告（系统记录）: %s\n", ctx.ClientName))
	}
	if ctx.MatterType != "" {
		b.WriteString(fmt.Sprintf("案由类型: %s\n", ctx.MatterType))
	}
	if len(ctx.Plaintiffs) > 0 {
		b.WriteString(fmt.Sprintf("原告（已知）: %s\n", strings.Join(ctx.Plaintiffs, "、")))
	}
	if len(ctx.Defendants) > 0 {
		b.WriteString(fmt.Sprintf("被告（已知）: %s\n", strings.Join(ctx.Defendants, "、")))
	}
	if ctx.State != "" {
		b.WriteString(fmt.Sprintf("当前工作流阶段: %s\n", ctx.State))
	}
	if ctx.InitialMsg != "" {
		b.WriteString("\n【受理时记录】\n")
		b.WriteString(ctx.InitialMsg)
		b.WriteString("\n")
	}

	// Attached evidence file inventory — gives the LLM the canonical
	// source_file values to cite. Even if a file produced no extractable
	// text, listing it here lets the LLM include it in the 证据 section.
	if len(ctx.EvidenceFiles) > 0 {
		b.WriteString("\n【已附证据文件清单（可作为 source_file 引用）】\n")
		for _, name := range ctx.EvidenceFiles {
			b.WriteString("- ")
			b.WriteString(name)
			b.WriteString("\n")
		}
	}

	// Evidence text corpus (RAG-retrieved or full-text). The caller is
	// responsible for keeping this within the model's context window.
	if ctx.EvidenceContext != "" {
		b.WriteString("\n【证据材料摘录】\n")
		b.WriteString(ctx.EvidenceContext)
		b.WriteString("\n【证据材料摘录结束】\n")
	}

	b.WriteString("\n请现在输出 JSON。")
	return b.String()
}
