package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-go/internal/doctype"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
	"agentflow-go/internal/model"
)

// handleDocumentsRoutes is the dispatcher for /v1/cases/{id}/documents/...
//
// Sub-routes:
//   POST /v1/cases/{id}/documents/generate                       → generate a typed doc
//   GET  /v1/cases/{id}/documents                                → list all generated docs
//   GET  /v1/cases/{id}/documents/{doc_id}                       → fetch one doc
//   POST /v1/cases/{id}/documents/{doc_id}/approve               → HITL gate
//   POST /v1/cases/{id}/documents/{doc_id}/export                → .docx
//   POST /v1/cases/{id}/documents/{doc_id}/refine                → AI refine one section
//   GET  /v1/document-types                                      → list registered DocTypes
func (s *Server) handleDocumentsRoutes(w http.ResponseWriter, r *http.Request, caseID string, tail []string) {
	// "documents" already consumed by the caller; tail is what remains.
	if len(tail) == 0 {
		switch r.Method {
		case http.MethodGet:
			s.handleDocumentList(w, r, caseID)
		default:
			s.writeError(w, http.StatusMethodNotAllowed, "GET expected")
		}
		return
	}

	if tail[0] == "generate" && len(tail) == 1 {
		s.handleDocumentGenerate(w, r, caseID)
		return
	}

	docID := tail[0]
	rest := tail[1:]

	if len(rest) == 0 {
		s.handleDocumentGet(w, r, caseID, docID)
		return
	}

	switch rest[0] {
	case "approve":
		s.handleDocumentApprove(w, r, caseID, docID)
	case "export":
		s.handleDocumentExport(w, r, caseID, docID)
	case "refine":
		s.handleDocumentRefine(w, r, caseID, docID)
	case "section":
		// Direct user edit of a section — no LLM round trip. Used by the
		// inline TextEditor in DocumentsPane so the lawyer can hand-tune
		// the draft before approval.
		s.handleDocumentSaveSection(w, r, caseID, docID)
	default:
		s.writeError(w, http.StatusNotFound, "unknown document sub-route")
	}
}

// ─────────────────── direct section save (user edit) ───────────────────

type saveSectionReq struct {
	SectionID string `json:"section_id"`
	Content   string `json:"content"`
}

func (s *Server) handleDocumentSaveSection(w http.ResponseWriter, r *http.Request, caseID, docID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		s.writeError(w, http.StatusMethodNotAllowed, "POST or PUT required")
		return
	}
	var req saveSectionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SectionID == "" {
		s.writeError(w, http.StatusBadRequest, "section_id required")
		return
	}
	// Empty content allowed — the user might want to clear a section.
	// UpdateGeneratedDocSection enforces the "draft-only" rule.
	if err := s.workflow.UpdateGeneratedDocSection(caseID, docID, req.SectionID, req.Content); err != nil {
		// Most common errors are status conflicts ("doc is approved; not editable")
		// or section-not-found. Both deserve 409 / 404; 500 is wrong here.
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not found"):
			s.writeError(w, http.StatusNotFound, msg)
		case strings.Contains(msg, "only draft"):
			s.writeError(w, http.StatusConflict, msg)
		default:
			s.writeError(w, http.StatusInternalServerError, msg)
		}
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"section_id": req.SectionID,
	})
}

// handleDocumentTypesList returns the catalogue for the UI's type picker.
// Doesn't depend on a case — just enumerates the registered DocTypes.
func (s *Server) handleDocumentTypesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	types := doctype.All()
	out := make([]map[string]any, len(types))
	for i, t := range types {
		out[i] = map[string]any{
			"id":              t.ID,
			"label_zh":        t.LabelZH,
			"label_en":        t.LabelEN,
			"icon":            t.Icon,
			"description":     t.Description,
			"required_fields": t.RequiredFields,
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"types": out})
}

// ─────────────────── generate ───────────────────

type generateDocReq struct {
	DocType string `json:"doc_type"`
	// UserContext lets the lawyer add free-text guidance to the prompt
	// (e.g. "emphasize the unpaid wages claim").
	UserContext string `json:"user_context,omitempty"`
}

func (s *Server) handleDocumentGenerate(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req generateDocReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	gd, modelUsed, status, err := s.generateDocumentCore(caseID, req.DocType, req.UserContext)
	if err != nil {
		s.writeError(w, status, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"doc":        gd,
		"model_used": modelUsed,
	})
}

// generateDocumentCore is the shared implementation behind the HTTP endpoint
// AND the agent tool. Returns the persisted GeneratedDoc, the model used,
// and on error a suitable HTTP status.
//
// Extracted so the chatagent's `generate_document` tool can drive the same
// pipeline without re-implementing prompt building / persistence.
func (s *Server) generateDocumentCore(caseID, docTypeID, userContext string) (model.GeneratedDoc, string, int, error) {
	t, ok := doctype.Get(docTypeID)
	if !ok {
		return model.GeneratedDoc{}, "", http.StatusBadRequest,
			fmt.Errorf("unknown doc_type %q (have: %v)", docTypeID, doctype.IDs())
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		return model.GeneratedDoc{}, "", http.StatusNotFound, fmt.Errorf("case not found")
	}

	docContext, fileList, _ := s.draftGatherEvidence(caseID)
	plaintiffs, defendants := extractPartiesFromCase(c)

	prompt := t.Prompt(doctype.PromptContext{
		CaseID:          c.CaseID,
		ClientName:      c.ClientName,
		MatterType:      c.MatterType,
		State:           c.State,
		Plaintiffs:      plaintiffs,
		Defendants:      defendants,
		EvidenceContext: docContext,
		EvidenceFiles:   fileList,
		InitialMsg:      c.InitialMsg,
	})
	if userContext != "" {
		prompt += "\n\n【用户附加要求】\n" + userContext
	}

	modelUsed := s.modelForTask("synth")
	raw, err := s.llm.Generate(prompt, "", llm.GenerationConfig{
		MaxTokens: 4096,
		Temp:      0.1,
		Model:     modelUsed,
	})
	if err != nil {
		return model.GeneratedDoc{}, "", http.StatusInternalServerError,
			fmt.Errorf("LLM error: %w", err)
	}

	parsed, parseErr := parseGeneratedDoc(raw, t)
	if parseErr != nil {
		return model.GeneratedDoc{}, "", http.StatusInternalServerError,
			fmt.Errorf("could not parse LLM output: %w", parseErr)
	}

	gd := buildGeneratedDoc(docTypeID, parsed)
	version, err := s.workflow.AppendGeneratedDoc(caseID, gd)
	if err != nil {
		return model.GeneratedDoc{}, "", http.StatusInternalServerError, err
	}
	gd.Version = version
	return gd, modelUsed, http.StatusOK, nil
}

// ─────────────────── list / get ───────────────────

func (s *Server) handleDocumentList(w http.ResponseWriter, r *http.Request, caseID string) {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "case not found")
		return
	}
	docs := c.GeneratedDocs
	if docs == nil {
		docs = []model.GeneratedDoc{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"case_id": caseID,
		"docs":    docs,
		"count":   len(docs),
	})
}

func (s *Server) handleDocumentGet(w http.ResponseWriter, r *http.Request, caseID, docID string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "case not found")
		return
	}
	for _, d := range c.GeneratedDocs {
		if d.ID == docID {
			s.writeJSON(w, http.StatusOK, d)
			return
		}
	}
	s.writeError(w, http.StatusNotFound, "doc not found")
}

// ─────────────────── approve ───────────────────

func (s *Server) handleDocumentApprove(w http.ResponseWriter, r *http.Request, caseID, docID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	if err := s.workflow.UpdateGeneratedDocStatus(caseID, docID, "approved"); err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "approved"})
}

// ─────────────────── export (.docx) ───────────────────

func (s *Server) handleDocumentExport(w http.ResponseWriter, r *http.Request, caseID, docID string) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "POST or GET required")
		return
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "case not found")
		return
	}
	var doc *model.GeneratedDoc
	for i := range c.GeneratedDocs {
		if c.GeneratedDocs[i].ID == docID {
			doc = &c.GeneratedDocs[i]
			break
		}
	}
	if doc == nil {
		s.writeError(w, http.StatusNotFound, "doc not found")
		return
	}
	// HITL gate: only approved docs may be exported.
	if doc.Status != "approved" && doc.Status != "exported" {
		s.writeError(w, http.StatusConflict,
			fmt.Sprintf("doc is %q; approve before exporting", doc.Status))
		return
	}

	fname := fmt.Sprintf("%s_%s_v%d.docx",
		sanitizeUploadedBasename(c.ClientName),
		doc.DocType, doc.Version)
	if utf8.RuneCountInString(fname) > 180 {
		fname = fmt.Sprintf("%s_v%d.docx", doc.DocType, doc.Version)
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fname))
	w.WriteHeader(http.StatusOK)
	if err := writeDocx(w, doc.Title, doc.Sections); err != nil {
		// We've already started writing; can't change the status code.
		return
	}
	// Mark exported on the case (won't affect the response that's already streamed).
	_ = s.workflow.MarkGeneratedDocExported(caseID, docID, fname)
}

// ─────────────────── refine ───────────────────

type refineDocReq struct {
	SectionID   string `json:"section_id"`
	Instruction string `json:"instruction"` // e.g. "make this more formal", "expand on the unpaid wages claim"
}

func (s *Server) handleDocumentRefine(w http.ResponseWriter, r *http.Request, caseID, docID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req refineDocReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SectionID == "" || req.Instruction == "" {
		s.writeError(w, http.StatusBadRequest, "section_id and instruction required")
		return
	}
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "case not found")
		return
	}
	var doc *model.GeneratedDoc
	for i := range c.GeneratedDocs {
		if c.GeneratedDocs[i].ID == docID {
			doc = &c.GeneratedDocs[i]
			break
		}
	}
	if doc == nil {
		s.writeError(w, http.StatusNotFound, "doc not found")
		return
	}
	if doc.Status != "draft" {
		s.writeError(w, http.StatusConflict, "only draft status is editable")
		return
	}

	var section *model.DocSection
	for i := range doc.Sections {
		if doc.Sections[i].ID == req.SectionID {
			section = &doc.Sections[i]
			break
		}
	}
	if section == nil {
		s.writeError(w, http.StatusNotFound, "section not found")
		return
	}

	prompt := fmt.Sprintf(
		`You are a Chinese-law legal-drafting assistant. Refine the following section per the user's instruction. Output ONLY the revised section text in Chinese — no JSON, no markdown, no commentary.

Document type: %s
Section: %s
Current content:
%s

User instruction: %s

Revised section:`,
		doc.DocType, section.Title, section.Content, req.Instruction,
	)
	revised, err := s.llm.Generate(prompt, "", llm.GenerationConfig{
		MaxTokens: 1500,
		Temp:      0.2,
		Model:     s.modelForTask("synth"),
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}
	revised = strings.TrimSpace(revised)
	if revised == "" {
		s.writeError(w, http.StatusInternalServerError, "LLM returned empty revision")
		return
	}

	if err := s.workflow.UpdateGeneratedDocSection(caseID, docID, req.SectionID, revised); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"section_id": req.SectionID,
		"content":    revised,
	})
}

// ─────────────────── helpers ───────────────────

// extractPartiesFromCase pulls plaintiffs/defendants from the case's batch
// analysis JSON (set during folder intake) if present. Returns empty slices
// when not available — the prompt will instruct the LLM to extract from
// evidence as a fallback.
func extractPartiesFromCase(c model.Case) (plaintiffs, defendants []string) {
	if c.EvaluationDetail == nil {
		return nil, nil
	}
	if v, ok := c.EvaluationDetail["plaintiffs"].([]interface{}); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				plaintiffs = append(plaintiffs, s)
			}
		}
	}
	if v, ok := c.EvaluationDetail["defendants"].([]interface{}); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				defendants = append(defendants, s)
			}
		}
	}
	return
}

// parseGeneratedDoc unmarshals the LLM's JSON output into a strongly-typed
// shape. Any sections the LLM omitted that are listed as Required in the
// DocType are inserted with placeholder content so downstream code never
// has to special-case missing keys.
func parseGeneratedDoc(raw string, t doctype.DocType) (parsedDoc, error) {
	payload := llmutil.ExtractJSONObject(raw)
	var out parsedDoc
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return out, fmt.Errorf("json: %w (raw: %.200q)", err, raw)
	}
	// Backfill any required sections the LLM forgot.
	have := make(map[string]bool, len(out.Sections))
	for _, s := range out.Sections {
		have[s.ID] = true
	}
	for _, spec := range t.Sections {
		if !spec.Required || have[spec.ID] {
			continue
		}
		out.Sections = append(out.Sections, parsedSection{
			ID:      spec.ID,
			Title:   spec.TitleZH,
			Content: "材料未载明",
		})
	}
	return out, nil
}

type parsedDoc struct {
	Title    string          `json:"title"`
	Sections []parsedSection `json:"sections"`
}

type parsedSection struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	Highlights []parsedHighlight `json:"highlights,omitempty"`
}

type parsedHighlight struct {
	Text       string `json:"text"`
	Reason     string `json:"reason"`
	Category   string `json:"category"`
	SourceFile string `json:"source_file"`
	SourceRef  string `json:"source_ref"`
}

// buildGeneratedDoc converts the parsed shape into a model.GeneratedDoc
// with a fresh ID and draft status. Caller still needs to call
// AppendGeneratedDoc to persist.
func buildGeneratedDoc(docType string, p parsedDoc) model.GeneratedDoc {
	gd := model.GeneratedDoc{
		ID:        fmt.Sprintf("gen-%d", time.Now().UnixNano()),
		DocType:   docType,
		CreatedAt: time.Now(),
		Title:     p.Title,
		Status:    "draft",
	}
	for _, ps := range p.Sections {
		ds := model.DocSection{
			ID:      ps.ID,
			Title:   ps.Title,
			Content: ps.Content,
		}
		for _, h := range ps.Highlights {
			ds.Highlights = append(ds.Highlights, model.DocHighlight{
				Text:       h.Text,
				Reason:     h.Reason,
				Category:   h.Category,
				SourceFile: h.SourceFile,
				SourceRef:  h.SourceRef,
			})
			gd.Highlights = append(gd.Highlights, model.DocHighlight{
				Text:       h.Text,
				Reason:     h.Reason,
				Category:   h.Category,
				SourceFile: h.SourceFile,
				SourceRef:  h.SourceRef,
			})
		}
		gd.Sections = append(gd.Sections, ds)
	}
	return gd
}
