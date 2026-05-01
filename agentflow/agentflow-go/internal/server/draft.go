package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"agentflow-go/internal/llm"
)

// handleDraftRoutes dispatches /v1/cases/{id}/draft, /draft/generate, /draft/save, /draft/ai, /draft/export
func (s *Server) handleDraftRoutes(w http.ResponseWriter, r *http.Request, caseID string, tail []string) {
	if len(tail) == 0 {
		if r.Method == http.MethodGet {
			s.handleDraftGet(w, r, caseID)
			return
		}
		s.writeError(w, http.StatusMethodNotAllowed, "GET only for /draft")
		return
	}
	switch tail[0] {
	case "generate":
		if r.Method == http.MethodPost {
			s.handleDraftGenerate(w, r, caseID)
			return
		}
	case "save":
		if r.Method == http.MethodPost {
			s.handleDraftSave(w, r, caseID)
			return
		}
	case "ai":
		if r.Method == http.MethodPost {
			s.handleDraftAI(w, r, caseID)
			return
		}
	case "export":
		if r.Method == http.MethodGet {
			s.handleDraftExportDocx(w, r, caseID)
			return
		}
	case "assess":
		if r.Method == http.MethodPost {
			s.handleDraftAssess(w, r, caseID)
			return
		}
	}
	s.writeError(w, http.StatusNotFound, "Draft action not found")
}

func (s *Server) handleDraftGet(w http.ResponseWriter, r *http.Request, caseID string) {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"case_id":        c.CaseID,
		"draft_preview":  c.DraftPreview,
		"document_draft": c.DocumentDraft,
	})
}

func (s *Server) handleDraftAssess(w http.ResponseWriter, r *http.Request, caseID string) {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	// Run intelligent assessment
	prep := s.prepareForDraftWithAssessment(caseID)
	assessment := prep.Assessment

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"can_generate":        assessment.CanGenerate,
		"confidence":          assessment.Confidence,
		"reasoning":           assessment.Reasoning,
		"missing_info":        assessment.MissingInfo,
		"suggested_searches":  assessment.SuggestedSearches,
		"questions":           assessment.QuestionsForUser,
		"proceed_anyway":      assessment.ProceedAnyway,
		"extracted_info": map[string]interface{}{
			"plaintiffs": prep.Summary.Plaintiffs,
			"defendants": prep.Summary.Defendants,
			"key_facts":  prep.Summary.KeyFacts,
			"key_amounts": prep.Summary.KeyAmounts,
			"key_dates":   prep.Summary.KeyDates,
		},
		"document_count": len(c.UploadedDocuments),
	})
}

func (s *Server) draftGatherEvidence(caseID string) (docContext string, fileList []string, ok bool) {
	c, snapOk := s.workflow.GetCaseSnapshot(caseID)
	if !snapOk {
		return "", nil, false
	}
	var parts []string
	for _, fn := range c.UploadedDocuments {
		doc, dok := s.rag.GetDocumentFlex(fn)
		if !dok {
			continue
		}
		fileList = append(fileList, fn)
		for i, chunk := range doc.Chunks {
			if chunk == "" {
				continue
			}
			parts = append(parts, fmt.Sprintf("[来源: %s #%d]\n%s", fn, i+1, chunk))
		}
	}
	if len(parts) == 0 {
		return "", fileList, true
	}
	docContext = strings.Join(parts, "\n\n")
	docContext = truncateRunesStr(docContext, 8000)
	return docContext, fileList, true
}

func (s *Server) handleDraftGenerate(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	// Parse request body - allow user to force generation or provide extra context
	var req struct {
		Force            bool              `json:"force"`             // User confirmed to proceed anyway
		UserContext      string            `json:"user_context"`      // Additional info from user
		AnsweredQuestions map[string]string `json:"answered_questions"` // User's answers to questions
	}
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req)
		}
	}

	// Step 1: Intelligent assessment
	prep := s.prepareForDraftWithAssessment(caseID)
	assessment := prep.Assessment

	// Step 2: Check if we should proceed
	if !assessment.CanGenerate && !assessment.ProceedAnyway && !req.Force {
		// Not enough info - return assessment with questions for user
		s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"needs_assessment":     true,
			"can_generate":         false,
			"confidence":           assessment.Confidence,
			"reasoning":            assessment.Reasoning,
			"missing_info":         assessment.MissingInfo,
			"questions":            assessment.QuestionsForUser,
			"suggested_searches":   assessment.SuggestedSearches,
			"search_results":       prep.SearchResults,
			"extracted_parties":    map[string]interface{}{"plaintiffs": prep.Summary.Plaintiffs, "defendants": prep.Summary.Defendants},
		})
		return
	}

	// Step 3: Gather document context
	docContext, uploadedFiles, evOk := s.draftGatherEvidence(caseID)
	if !evOk {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	if docContext == "" {
		s.writeError(w, http.StatusBadRequest, "No documents found for this case")
		return
	}

	// Enhance context with search results from LLM suggestions
	if len(prep.SearchResults) > 0 {
		docContext += "\n\n【补充检索结果】\n"
		for _, sr := range prep.SearchResults {
			query, _ := sr["query_used"].(string)
			chunk, _ := sr["chunk"].(string)
			docContext += fmt.Sprintf("检索「%s」发现：\n%s\n---\n", query, truncateRunesStr(chunk, 500))
		}
	}

	// Add user-provided context if any
	if req.UserContext != "" {
		docContext += fmt.Sprintf("\n\n【用户补充信息】\n%s\n", req.UserContext)
	}

	// Build guidance from structured summary
	summaryGuidance := ""
	if prep.Summary.Plaintiffs != nil && len(prep.Summary.Plaintiffs) > 0 {
		summaryGuidance += fmt.Sprintf("原告：%s\n", strings.Join(prep.Summary.Plaintiffs, "、"))
	}
	if prep.Summary.Defendants != nil && len(prep.Summary.Defendants) > 0 {
		summaryGuidance += fmt.Sprintf("被告：%s\n", strings.Join(prep.Summary.Defendants, "、"))
	}
	if prep.Summary.KeyFacts != nil && len(prep.Summary.KeyFacts) > 0 {
		summaryGuidance += fmt.Sprintf("关键事实：%s\n", strings.Join(prep.Summary.KeyFacts, "；"))
	}

	caseMeta := fmt.Sprintf("案件: %s\n案由: %s\n阶段: %s\n文件列表: %s\n%s",
		c.ClientName, c.MatterType, c.State, strings.Join(uploadedFiles, ", "), summaryGuidance)

	// Step 4: Enhanced draft prompt with confidence-aware instructions
	confidenceNote := ""
	if assessment.Confidence == "low" {
		confidenceNote = "注意：当前材料信息有限，请在文书中明确标注「待确认」的内容，并在需人工核实处添加更多highlights。"
	} else if assessment.Confidence == "medium" {
		confidenceNote = "注意：部分信息可能需要进一步确认，请在文书中适当标注不确定之处。"
	}

	prompt := fmt.Sprintf(`你是一名中国执业律师助理。根据所提供的【材料摘录】，生成一份结构化法律文书草稿。

%s

严格按以下JSON格式输出，不要输出其他任何内容：
{
  "title": "文书标题",
  "sections": [
    {
      "id": "s1",
      "title": "章节标题",
      "content": "正文内容（可包含多个段落）",
      "highlights": [
        {
          "text": "需要人工核实的具体文字（必须是content中的原文片段）",
          "reason": "为什么需要核实（如：金额待确认、日期需核对、当事人信息需确认）",
          "category": "amount|date|party|clause|fact",
          "source_file": "来源文件名",
          "source_ref": "来源文件中的相关原文片段（作为证据引用）"
        }
      ]
    }
  ]
}

要求：
1. 文书应包含：案件概述、当事人信息、事实与证据、法律分析、诉讼请求/法律意见 等章节
2. highlights中每个条目的source_file和source_ref必须指向材料中的具体来源
3. 需要人工核实的内容包括：关键金额、重要日期、当事人姓名/身份、法律条款引用、关键事实认定
4. content使用完整的法律文书语言，专业且准确
5. 对于不确定的信息（如被告信息不全、金额模糊等），在文中标注「待确认」并在highlights中说明
6. 控制总字数在2500字以内`, confidenceNote)

	fullContext := caseMeta + "\n\n【材料摘录】\n" + docContext + "\n【材料摘录结束】"
	fullContext = truncateRunesStr(fullContext, 8000)

	result, err := s.llm.Generate(prompt, fullContext, llm.GenerationConfig{
		MaxTokens: 4096,
		Temp:      0.1,
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}

	result = strings.TrimSpace(result)
	if strings.HasPrefix(result, "```") {
		if idx := strings.Index(result, "\n"); idx >= 0 {
			result = result[idx+1:]
		}
		if idx := strings.LastIndex(result, "```"); idx >= 0 {
			result = result[:idx]
		}
		result = strings.TrimSpace(result)
	}

	var draft map[string]interface{}
	if err := json.Unmarshal([]byte(result), &draft); err != nil {
		log.Printf("[draft] JSON parse failed, using raw text: %v", err)
		draft = map[string]interface{}{
			"title": fmt.Sprintf("%s — %s", c.ClientName, c.MatterType),
			"sections": []interface{}{
				map[string]interface{}{
					"id":         "s1",
					"title":      "AI 生成草稿",
					"content":    result,
					"highlights": []interface{}{},
				},
			},
		}
	}

	// Embed assessment info in draft for UI display
	draft["_assessment"] = map[string]interface{}{
		"confidence": assessment.Confidence,
		"reasoning":  assessment.Reasoning,
	}
	if prep.Summary.Plaintiffs != nil {
		draft["_extracted_parties"] = map[string]interface{}{
			"plaintiffs": prep.Summary.Plaintiffs,
			"defendants": prep.Summary.Defendants,
		}
	}

	var preview strings.Builder
	if title, ok := draft["title"].(string); ok {
		preview.WriteString("# " + title + "\n\n")
	}
	if assessment.Confidence != "high" {
		preview.WriteString(fmt.Sprintf("> **⚠️ 生成置信度：%s** - %s\n\n", assessment.Confidence, assessment.Reasoning))
	}
	if sections, ok := draft["sections"].([]interface{}); ok {
		for _, sec := range sections {
			if sm, ok := sec.(map[string]interface{}); ok {
				if t, ok := sm["title"].(string); ok {
					preview.WriteString("## " + t + "\n\n")
				}
				if ct, ok := sm["content"].(string); ok {
					preview.WriteString(ct + "\n\n")
				}
			}
		}
	}

	if err := s.workflow.SetDocumentDraft(caseID, draft); err != nil {
		log.Printf("[draft] SetDocumentDraft: %v", err)
	}
	if err := s.workflow.SetDraftPreview(caseID, preview.String()); err != nil {
		log.Printf("[draft] SetDraftPreview: %v", err)
	}

	// Store the structured summary for the case
	if prep.Summary.CaseDescription != "" {
		summaryText := fmt.Sprintf("**案件摘要**\n\n%s\n\n**置信度**: %s\n\n**分析**: %s",
			prep.Summary.CaseDescription, prep.Summary.Confidence, prep.Summary.Reasoning)
		if len(prep.Summary.GapsIdentified) > 0 {
			summaryText += fmt.Sprintf("\n\n**信息缺口**: %s", strings.Join(prep.Summary.GapsIdentified, "、"))
		}
		if err := s.workflow.SetAICaseSummary(caseID, summaryText); err != nil {
			log.Printf("[draft] SetAICaseSummary: %v", err)
		}
	}

	s.broadcastStatus()
	s.writeJSON(w, http.StatusOK, draft)
}

func (s *Server) handleDraftSave(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		Draft   map[string]interface{} `json:"draft"`
		Preview string                 `json:"preview"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if body.Draft != nil {
		if err := s.workflow.SetDocumentDraft(caseID, body.Draft); err != nil {
			s.writeError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	if body.Preview != "" {
		if err := s.workflow.SetDraftPreview(caseID, body.Preview); err != nil {
			log.Printf("[draft] SetDraftPreview: %v", err)
		}
	}
	s.broadcastStatus()
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleDraftAI(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body struct {
		Action      string   `json:"action"`
		Text        string   `json:"text"`
		Context     string   `json:"context"`
		Instruction string   `json:"instruction"`
		SourceFiles []string `json:"source_files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		s.writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	var evidence strings.Builder
	if len(body.SourceFiles) > 0 {
		for _, fn := range body.SourceFiles {
			doc, dok := s.rag.GetDocumentFlex(fn)
			if !dok {
				continue
			}
			for i, ch := range doc.Chunks {
				if ch == "" {
					continue
				}
				evidence.WriteString(fmt.Sprintf("[来源: %s #%d]\n%s\n\n", fn, i+1, ch))
			}
		}
	} else {
		for _, fn := range c.UploadedDocuments {
			doc, dok := s.rag.GetDocumentFlex(fn)
			if !dok {
				continue
			}
			for i, ch := range doc.Chunks {
				if ch == "" {
					continue
				}
				evidence.WriteString(fmt.Sprintf("[来源: %s #%d]\n%s\n\n", fn, i+1, ch))
			}
		}
	}
	ev := truncateRunesStr(evidence.String(), 6000)

	action := strings.TrimSpace(body.Action)
	if action == "" {
		action = "suggest"
	}
	instr := body.Instruction
	if instr == "" {
		switch action {
		case "expand":
			instr = "将下列文字扩展为更完整、严谨的法律表述，保持与材料一致，不编造事实。"
		case "formalize":
			instr = "将下列文字改写为更正式的法律文书用语，保持原意。"
		default:
			instr = "根据【材料摘录】审阅下列选段，提出改进后的完整替换文本（可直接替换选段）。要求：法律用语严谨、与证据一致；若材料不足以支持某论断，明确标注「材料未载明」。只输出改进后的正文，不要前缀说明。"
		}
	}

	meta := fmt.Sprintf("案件: %s\n案由: %s\n", c.ClientName, c.MatterType)
	userCtx := body.Context
	if userCtx != "" {
		userCtx = truncateRunesStr(userCtx, 2000)
	}
	prompt := fmt.Sprintf("%s\n\n任务类型: %s\n\n【用户指令】\n%s\n\n【上下文段落】\n%s\n\n【待处理选段】\n%s",
		meta, action, instr, userCtx, body.Text)

	full := "【材料摘录】\n" + ev + "\n【材料摘录结束】\n\n" + prompt
	full = truncateRunesStr(full, 12000)

	out, err := s.llm.Generate(
		"你是中国法律助理。只输出最终建议文本（或JSON中单一字段），不要解释过程。",
		full,
		llm.GenerationConfig{MaxTokens: 2048, Temp: 0.15},
	)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}
	out = strings.TrimSpace(out)
	s.writeJSON(w, http.StatusOK, map[string]string{"suggestion": out, "action": action})
}

func (s *Server) handleDraftExportDocx(w http.ResponseWriter, r *http.Request, caseID string) {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	draft := c.DocumentDraft
	if draft == nil || len(draft) == 0 {
		s.writeError(w, http.StatusBadRequest, "No draft to export")
		return
	}

	title, _ := draft["title"].(string)
	if title == "" {
		title = "Legal Draft"
	}
	var bodyXML strings.Builder
	bodyXML.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	bodyXML.WriteString(`<w:body>`)
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	bodyXML.WriteString(fmt.Sprintf(`<w:p><w:r><w:rPr><w:b/><w:sz w:val="32"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, esc(title)))

	if secs, ok := draft["sections"].([]interface{}); ok {
		for _, sec := range secs {
			sm, ok := sec.(map[string]interface{})
			if !ok {
				continue
			}
			st, _ := sm["title"].(string)
			ct, _ := sm["content"].(string)
			if st != "" {
				bodyXML.WriteString(fmt.Sprintf(`<w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, esc(st)))
			}
			for _, para := range strings.Split(ct, "\n\n") {
				para = strings.TrimSpace(para)
				if para == "" {
					continue
				}
				bodyXML.WriteString(fmt.Sprintf(`<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, esc(para)))
			}
			if hls, ok := sm["highlights"].([]interface{}); ok {
				for _, h := range hls {
					hm, ok := h.(map[string]interface{})
					if !ok {
						continue
					}
					tx, _ := hm["text"].(string)
					rs, _ := hm["reason"].(string)
					sf, _ := hm["source_file"].(string)
					sr, _ := hm["source_ref"].(string)
					line := fmt.Sprintf("【核实】%s | 原因: %s | 来源: %s | 摘录: %s", tx, rs, sf, sr)
					bodyXML.WriteString(fmt.Sprintf(`<w:p><w:r><w:rPr><w:color w:val="C0504D"/></w:rPr><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, esc(line)))
				}
			}
		}
	}
	bodyXML.WriteString(`</w:body></w:document>`)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	add := func(name, content string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write([]byte(content))
		return err
	}
	_ = add("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)
	_ = add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`)
	_ = add("word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`)
	_ = add("word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+bodyXML.String())
	if err := zw.Close(); err != nil {
		s.writeError(w, http.StatusInternalServerError, "export failed")
		return
	}

	fn := fmt.Sprintf("%s_%s_draft.docx", sanitizeUploadedBasename(c.ClientName), c.CaseID)
	if utf8.RuneCountInString(fn) > 180 {
		fn = c.CaseID + "_draft.docx"
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fn))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, buf)
}
