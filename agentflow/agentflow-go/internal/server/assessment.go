package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
)

// draftAssessmentResponse is the LLM's assessment of whether we can draft
type draftAssessmentResponse struct {
	CanGenerate       bool     `json:"can_generate"`
	Confidence        string   `json:"confidence"`    // "high" | "medium" | "low"
	Reasoning         string   `json:"reasoning"`
	MissingInfo       []string `json:"missing_info"`
	SuggestedSearches  []string `json:"suggested_searches"` // RAG queries to fill gaps
	QuestionsForUser   []string `json:"questions_for_user"`
	ProceedAnyway     bool     `json:"proceed_anyway"` // LLM recommends proceeding with caveats
}

// structuredSummaryResponse is the structured case summary with confidence
type structuredSummaryResponse struct {
	Plaintiffs     []string `json:"plaintiffs"`
	Defendants     []string `json:"defendants"`
	MatterType     string   `json:"matter_type"`
	CaseDescription string   `json:"case_description"`
	KeyFacts       []string `json:"key_facts"`
	KeyAmounts     []string `json:"key_amounts"`
	KeyDates       []string `json:"key_dates"`
	Confidence     string   `json:"confidence"`
	Reasoning      string   `json:"reasoning"`
	GapsIdentified []string `json:"gaps_identified"`
}

// iterativeDraftRefinement handles the multi-step draft generation process
type iterativeDraftRefinement struct {
	Assessment    draftAssessmentResponse    `json:"assessment"`
	SearchResults []map[string]interface{}     `json:"search_results"`
	Summary       structuredSummaryResponse  `json:"summary"`
	Draft         map[string]interface{}      `json:"draft,omitempty"`
	NeedsUserInput bool                         `json:"needs_user_input"`
}

// assessDraftReadiness asks LLM if we have enough info to generate a draft
func (s *Server) assessDraftReadiness(docContext string, uploadedFiles []string) draftAssessmentResponse {
	if docContext == "" {
		return draftAssessmentResponse{
			CanGenerate: false,
			Confidence:  "low",
			Reasoning:   "No documents have been processed for this case.",
			MissingInfo: []string{"any case documents"},
			QuestionsForUser: []string{"Please upload at least one case document (complaint, contract, evidence)"},
		}
	}

	// Check if we only have images (screenshots)
	hasNonImage := false
	imageCount := 0
	for _, fn := range uploadedFiles {
		lower := strings.ToLower(fn)
		if strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") ||
			strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".gif") {
			imageCount++
		} else {
			hasNonImage = true
		}
	}
	isImageOnly := !hasNonImage && imageCount > 0

	fileListInfo := ""
	if isImageOnly {
		fileListInfo = "\n注意：当前只有图片文件（截图），OCR可能不完整。建议补充原始文档或文字材料。"
	}

	prompt := fmt.Sprintf(`你是智能法律文书助手。请评估当前案件材料是否足够生成一份有意义的法律文书草稿。

【已上传文件】%s
%s

【材料摘录】
%s

请分析并返回JSON（不要使用markdown代码块）：
{
  "can_generate": true/false,
  "confidence": "high/medium/low",
  "reasoning": "简要说明判断理由",
  "missing_info": ["缺失的关键信息，如：被告明确信息、合同金额、具体日期等"],
  "suggested_searches": ["可以在材料中搜索补充信息的关键词"],
  "questions_for_user": ["应该询问用户的具体问题"],
  "proceed_anyway": true/false
}

评估标准：
1. high confidence: 有明确的原告/被告信息、争议事实、金额或诉求
2. medium confidence: 有基本当事人信息和部分事实，但细节待完善
3. low confidence: 当事人信息不全、事实模糊、或仅有单方陈述
4. 图片/截图为主的情况应标记为low confidence，建议补充文字材料`, strings.Join(uploadedFiles, ", "), fileListInfo, truncateRunesStr(docContext, 4000))

	result, err := s.llm.Generate(prompt, "", llm.GenerationConfig{
		MaxTokens: 1024,
		Temp:      0.1,
	})
	if err != nil {
		log.Printf("[assessment] LLM error: %v", err)
		// Fail open - allow generation but with warning
		return draftAssessmentResponse{
			CanGenerate:   true,
			Confidence:    "low",
			Reasoning:     "评估服务暂时不可用，可以尝试生成，但请仔细审核结果",
			ProceedAnyway: true,
		}
	}

	result = llmutil.ExtractJSONObject(result)
	var assessment draftAssessmentResponse
	if err := json.Unmarshal([]byte(result), &assessment); err != nil {
		log.Printf("[assessment] JSON parse error: %v, raw: %s", err, result)
		// Parse failure - allow generation but with warning
		return draftAssessmentResponse{
			CanGenerate:   true,
			Confidence:    "low",
			Reasoning:     "无法解析评估结果，建议手动检查材料完整性",
			ProceedAnyway: true,
		}
	}

	// Enhance reasoning for image-only cases
	if isImageOnly && assessment.Confidence == "high" {
		assessment.Confidence = "medium"
		assessment.QuestionsForUser = append(assessment.QuestionsForUser,
			"注意：当前只有图片材料，文字可能不完整，建议上传原始文档")
		}

	return assessment
}

// extractStructuredSummary uses LLM to extract structured case info
func (s *Server) extractStructuredSummary(docContext string, caseMeta map[string]interface{}) structuredSummaryResponse {
	clientName, _ := caseMeta["client_name"].(string)
	matterType, _ := caseMeta["matter_type"].(string)

	prompt := fmt.Sprintf(`你是专业的法律案件分析助手。请从以下材料中提取结构化的案件信息。

【当事人】%s
【案由】%s

【材料摘录】
%s

请返回JSON（不要使用markdown代码块）：
{
  "plaintiffs": ["原告姓名"],
  "defendants": ["被告姓名"],
  "matter_type": "标准化案由名称",
  "case_description": "案件简要描述（100-200字）",
  "key_facts": ["关键事实1", "关键事实2"],
  "key_amounts": ["争议金额XXX元"],
  "key_dates": ["重要日期"],
  "confidence": "high/medium/low - 根据材料完整度判断",
  "reasoning": "说明信息完整度判断依据",
  "gaps_identified": ["信息不足之处"]
}

要求：
1. 如果材料中某个当事人信息不明确，在对应数组中留空或标注"待确认"
2. confidence评估基于：当事人信息是否明确、争议事实是否清晰、证据是否充分
3. 日期格式：YYYY-MM-DD或中文描述（如"2024年3月"）`, clientName, matterType, truncateRunesStr(docContext, 6000))

	result, err := s.llm.Generate(prompt, "", llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.1,
	})
	if err != nil {
		log.Printf("[summary] LLM error: %v", err)
		return structuredSummaryResponse{
			Confidence: "low",
			Reasoning:  "摘要生成失败，请重试",
		}
	}

	result = llmutil.ExtractJSONObject(result)
	var summary structuredSummaryResponse
	if err := json.Unmarshal([]byte(result), &summary); err != nil {
		log.Printf("[summary] JSON parse error: %v", err)
		return structuredSummaryResponse{
			Confidence: "low",
			Reasoning:  "摘要解析失败，材料可能格式异常",
		}
	}

	return summary
}

// ragSearchOnDemand performs RAG searches based on LLM suggestions
func (s *Server) ragSearchOnDemand(caseID string, queries []string, maxResults int) []map[string]interface{} {
	if len(queries) == 0 {
		return nil
	}

	var allResults []map[string]interface{}
	seen := make(map[string]bool)

	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}

		// Search RAG
		results := s.rag.Search(query, maxResults)
		for _, r := range results {
			key := r.Filename + ":" + r.Chunk[:20] // use filename + chunk start as unique key
			if seen[key] {
				continue
			}
			seen[key] = true

			allResults = append(allResults, map[string]interface{}{
				"filename":   r.Filename,
				"chunk":      r.Chunk,
				"score":      r.Score,
				"query_used": query,
			})
		}
	}

	return allResults
}

// prepareForDraftWithAssessment runs the intelligent assessment and gathers info
func (s *Server) prepareForDraftWithAssessment(caseID string) iterativeDraftRefinement {
	var result iterativeDraftRefinement

	// 1. Gather document context
	docContext, uploadedFiles, ok := s.draftGatherEvidence(caseID)
	if !ok {
		result.Assessment = draftAssessmentResponse{
			CanGenerate: false,
			Confidence:  "low",
			Reasoning:   "Case not found",
		}
		return result
	}

	// 2. Assess draft readiness with LLM
	result.Assessment = s.assessDraftReadiness(docContext, uploadedFiles)

	// 3. If LLM suggested searches, run them and re-assess
	if len(result.Assessment.SuggestedSearches) > 0 {
		log.Printf("[draft] Running suggested searches: %v", result.Assessment.SuggestedSearches)
		result.SearchResults = s.ragSearchOnDemand(caseID, result.Assessment.SuggestedSearches, 5)

		// If we found relevant info, append it to context and re-assess
		if len(result.SearchResults) > 0 {
			log.Printf("[draft] Found %d additional context pieces", len(result.SearchResults))
			// Could optionally re-run assessment with enhanced context
		}
	}

	// 4. Extract structured summary
	caseSnap, _ := s.workflow.GetCaseSnapshot(caseID)
	result.Summary = s.extractStructuredSummary(docContext, map[string]interface{}{
		"client_name": caseSnap.ClientName,
		"matter_type": caseSnap.MatterType,
	})

	// 5. Determine if user input is needed
	result.NeedsUserInput = !result.Assessment.CanGenerate && !result.Assessment.ProceedAnyway

	return result
}
