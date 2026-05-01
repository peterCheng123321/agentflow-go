package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-go/internal/llm"
	"agentflow-go/internal/model"
)

func clipContext(context string, maxLength int) string {
	if len(context) > maxLength {
		return context[:maxLength]
	}
	return context
}

func truncateRunesStr(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes])
}

func (s *Server) handleOrchestrateByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Objective string `json:"objective"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	results := s.rag.Search(c.MatterType+" "+c.ClientName, 5)
	context := ""
	for _, r := range results {
		context += r.Chunk + "\n\n"
	}

	context = clipContext(context, 12000)

	prompt := fmt.Sprintf(
		"你是中国法律业务助手。请结合「检索摘录」仅为当事人 %s、案由类型 %s 撰写关于「%s」的书面要点。"+
			"要求：中文；区分事实摘录与你的法律分析；材料不足处明确写「检索材料不足」；不要编造案号或未出现的当事人。",
		c.ClientName,
		c.MatterType,
		req.Objective,
	)

	synthesis, err := s.llm.Generate(prompt, context, llm.GenerationConfig{
		MaxTokens: 8192,
		Temp:      0.08,
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}

	result := model.OrchestrationResult{
		Objective: req.Objective,
		Synthesis: synthesis,
		RanAt:     time.Now().Format(time.RFC3339),
	}

	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSummarizeByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	docContext := ""
	for _, fn := range c.UploadedDocuments {
		doc, ok := s.rag.GetDocumentFlex(fn)
		if ok {
			for _, chunk := range doc.Chunks {
				docContext += chunk + "\n"
			}
		}
	}

	if docContext == "" {
		s.writeJSON(w, http.StatusOK, map[string]string{
			"summary": "暂无可用文档材料进行总结。",
		})
		return
	}

	docContext = truncateRunesStr(docContext, 10000)

	caseMeta := fmt.Sprintf(
		"【案件元信息】当事人/案件名称（系统）: %s\n案由类型: %s\n当前工作流阶段: %s\n材料来自以下文件: %s\n\n",
		c.ClientName,
		c.MatterType,
		c.State,
		strings.Join(c.UploadedDocuments, ", "),
	)
	context := caseMeta + "【材料摘录】\n" + docContext + "\n【材料摘录结束】"

	prompt := `你是一名严谨的中国执业律师助理。只能根据上方【材料摘录】与【案件元信息】中已出现的内容作答；材料未明确记载的事项请写「材料未载明」，禁止臆测、编造法院案号或未出现的日期/金额。

请用中文输出一份结构化案件摘要，总字数不超过650字。使用 Markdown 二级标题：

## 一、案件概述
## 二、当事人与请求
## 三、关键事实与证据（标注是否材料中已载明）
## 四、风险与后续建议（区分事实与法律意见）

写作要求：客观、短句、可核查；关键数字与日期务必与原文一致；若材料仅为扫描件识别结果，可在末尾一句提示核实原件。`

	ctxLLM := truncateRunesStr(context, 4800)
	summary, err := s.llm.Generate(prompt, ctxLLM, llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.08,
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}

	if err := s.workflow.SetAICaseSummary(caseID, summary); err != nil {
		log.Printf("SetAICaseSummary: %v", err)
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"case_id": caseID,
		"summary": summary,
	})
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	var backend llm.Backend
	var baseURL string
	switch s.cfg.LLMBackend {
	case "dashscope":
		backend = llm.BackendOpenAICompat
		baseURL = s.cfg.DashScopeBaseURL
	case "deepseek":
		backend = llm.BackendOpenAICompat
		baseURL = s.cfg.DeepSeekBaseURL
	default:
		backend = llm.BackendOllama
		baseURL = s.cfg.OllamaURL
	}

	models, err := llm.ListModels(backend, baseURL)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list models: %v", err))
		return
	}

	for i := range models {
		if models[i].ID == s.cfg.ModelName {
			models[i].IsDefault = true
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"models":  models,
		"backend": s.cfg.LLMBackend,
		"current": s.cfg.ModelName,
	})
}

func (s *Server) handleBenchmarkModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		ModelID string  `json:"model_id"`
		Prompt  string  `json:"prompt,omitempty"`
		Context string  `json:"context,omitempty"`
		MaxTok  int     `json:"max_tokens,omitempty"`
		Temp    float64 `json:"temperature,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	modelID := req.ModelID
	if modelID == "" {
		modelID = s.cfg.ModelName
	}

	var result llm.BenchmarkResult
	if req.Prompt != "" {
		maxTok := req.MaxTok
		if maxTok <= 0 {
			maxTok = 500
		}
		temp := req.Temp
		if temp <= 0 {
			temp = 0.1
		}
		result = s.llm.BenchmarkWithPrompt(modelID, req.Prompt, req.Context, maxTok, temp)
	} else {
		result = s.llm.Benchmark(modelID)
	}

	s.writeJSON(w, http.StatusOK, result)
}
