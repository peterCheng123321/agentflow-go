package server

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"agentflow-go/internal/rag"
)

func (s *Server) handleCases(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/cases/")
	parts := strings.Split(path, "/")
	log.Printf("handleCases: path=[%s] parts=%v", path, parts)

	if len(parts) == 0 || parts[0] == "" {
		s.handleListCases(w, r)
		return
	}

	caseID := parts[0]
	if len(parts) == 1 {
		s.handleGetCaseByID(w, r, caseID)
		return
	}

	action := parts[1]
	switch action {
	case "advance":
		s.handleAdvanceCaseByID(w, r, caseID)
	case "approve":
		s.handleApproveHITLByID(w, r, caseID)
	case "delete":
		s.handleDeleteCaseByID(w, r, caseID)
	case "notes":
		s.handleAddNoteByID(w, r, caseID)
	case "orchestrate":
		s.handleOrchestrateByID(w, r, caseID)
	case "summarize":
		s.handleSummarizeByID(w, r, caseID)
	case "draft":
		s.handleDraftRoutes(w, r, caseID, parts[2:])
	case "documents":
		// Two namespaces share /documents/...:
		//   1. Legacy file-attachment ops (reassign, delete-by-filename)
		//   2. New AI-generated-doc ops (generate, get/approve/export/refine
		//      keyed by doc IDs that start with "gen-")
		// Plus GET with no tail → list generated docs.
		if len(parts) == 2 && r.Method == http.MethodGet {
			s.handleDocumentsRoutes(w, r, caseID, nil)
			return
		}
		if len(parts) >= 3 {
			next := parts[2]
			if next == "generate" || strings.HasPrefix(next, "gen-") {
				s.handleDocumentsRoutes(w, r, caseID, parts[2:])
				return
			}
			if next == "reassign" && r.Method == http.MethodPost {
				s.handleReassignDocument(w, r, caseID, strings.Join(parts[3:], "/"))
				return
			}
			if r.Method == http.MethodDelete || r.Method == http.MethodPost {
				filename := strings.Join(parts[2:], "/")
				s.handleDeleteDocumentFromCase(w, r, caseID, filename)
				return
			}
		}
		s.writeError(w, http.StatusNotFound, "Action not found")
	default:
		if r.Method == http.MethodPut {
			s.handleUpdateCaseByID(w, r, caseID)
			return
		}
		s.writeError(w, http.StatusNotFound, "Action not found")
	}
}

func (s *Server) handleReassignDocument(w http.ResponseWriter, r *http.Request, sourceCaseID, pathFilename string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Filename     string `json:"filename"`
		TargetCaseID string `json:"target_case_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Filename == "" && pathFilename != "" {
		if decoded, err := url.QueryUnescape(pathFilename); err == nil {
			req.Filename = decoded
		} else {
			req.Filename = pathFilename
		}
	}
	if req.Filename == "" || req.TargetCaseID == "" {
		s.writeError(w, http.StatusBadRequest, "filename and target_case_id are required")
		return
	}
	if sourceCaseID == req.TargetCaseID {
		s.writeError(w, http.StatusBadRequest, "source and target case must differ")
		return
	}

	fn := rag.NormalizeLogicalName(req.Filename)

	srcSnap, ok := s.workflow.GetCaseSnapshot(sourceCaseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	tgtSnap, ok := s.workflow.GetCaseSnapshot(req.TargetCaseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Target case not found")
		return
	}

	onSource := false
	for _, d := range srcSnap.UploadedDocuments {
		if rag.NormalizeLogicalName(d) == fn {
			onSource = true
			break
		}
	}
	if !onSource {
		s.writeError(w, http.StatusBadRequest, "Document not on this case")
		return
	}

	for _, f := range tgtSnap.UploadedDocuments {
		if rag.NormalizeLogicalName(f) == fn {
			s.writeError(w, http.StatusConflict, "Target case already has this document")
			return
		}
	}

	var merge map[string]interface{}
	for _, row := range srcSnap.AIFileSummaries {
		if row == nil {
			continue
		}
		sfn, _ := row["filename"].(string)
		if rag.NormalizeLogicalName(sfn) != fn {
			continue
		}
		merge = make(map[string]interface{})
		for k, v := range row {
			if k == "filename" {
				continue
			}
			merge[k] = v
		}
		break
	}

	if err := s.workflow.DetachDocument(sourceCaseID, fn); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(merge) > 0 {
		s.workflow.AttachDocument(req.TargetCaseID, fn, merge)
	} else {
		s.workflow.AttachDocument(req.TargetCaseID, fn)
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":         "reassigned",
		"source_case_id": sourceCaseID,
		"target_case_id": req.TargetCaseID,
		"filename":       fn,
	})
}

func (s *Server) handleDeleteDocumentFromCase(w http.ResponseWriter, r *http.Request, caseID string, filename string) {
	decoded, err := url.QueryUnescape(filename)
	if err == nil {
		filename = decoded
	}

	if err := s.workflow.DetachDocument(caseID, filename); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.rag.DeleteDocument(filename)

	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleUpdateCaseByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPut {
		s.writeError(w, http.StatusMethodNotAllowed, "PUT required")
		return
	}

	var req struct {
		ClientName string `json:"client_name"`
		MatterType string `json:"matter_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.workflow.UpdateCase(caseID, req.ClientName, req.MatterType); err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) handleListCases(w http.ResponseWriter, r *http.Request) {
	cases := s.workflow.ListCases()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"cases": cases,
		"count": len(cases),
	})
}

func (s *Server) handleCreateCase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		ClientName    string `json:"client_name"`
		MatterType    string `json:"matter_type"`
		SourceChannel string `json:"source_channel"`
		InitialMsg    string `json:"initial_msg"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ClientName == "" {
		req.ClientName = "Unknown Client"
	}
	if req.MatterType == "" {
		req.MatterType = "Civil Litigation"
	}

	c := s.workflow.CreateCase(req.ClientName, req.MatterType, req.SourceChannel, req.InitialMsg)
	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"case_id": c.CaseID,
		"case":    c,
	})
}

func (s *Server) handleGetCaseByID(w http.ResponseWriter, r *http.Request, caseID string) {
	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}
	s.writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleAdvanceCaseByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	if err := s.workflow.AdvanceState(caseID); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":  "advanced",
		"case_id": caseID,
	})
}

func (s *Server) handleDeleteCaseByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "DELETE or POST required")
		return
	}

	c, ok := s.workflow.GetCaseSnapshot(caseID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Case not found")
		return
	}

	for _, docName := range c.UploadedDocuments {
		s.rag.DeleteDocument(docName)
	}

	if err := s.workflow.DeleteCase(caseID); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleApproveHITLByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		State    string `json:"state"`
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := s.workflow.ApproveHITL(caseID, req.State, req.Approved, req.Reason); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":  "approved",
		"case_id": caseID,
	})
}

func (s *Server) handleAddNoteByID(w http.ResponseWriter, r *http.Request, caseID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Text string `json:"text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	s.workflow.AddNote(caseID, req.Text)
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// resolveCaseForDocument finds a case by extracted client name, or creates one.
// Uploads that resolve to "Unknown Client" never merge with each other; each gets an English
// intake label derived from the file stem (no legacy 待分类 bucket).
func (s *Server) resolveCaseForDocument(clientName, matterType, logicalName string) string {
	var caseID string
	if clientName != "Unknown Client" {
		for _, c := range s.workflow.ListCases() {
			if c.ClientName == clientName {
				caseID = c.CaseID
				break
			}
		}
	}
	if caseID != "" {
		return caseID
	}

	displayName := clientName
	if clientName == "Unknown Client" {
		stem := strings.TrimSuffix(logicalName, filepath.Ext(logicalName))
		if stem == "" {
			stem = strings.TrimSpace(logicalName)
		}
		rr := []rune(stem)
		if len(rr) > 48 {
			stem = string(rr[:48]) + "…"
		}
		if stem == "" {
			stem = "upload"
		}
		displayName = "Intake matter — " + stem
	}

	c := s.workflow.CreateCase(displayName, matterType, "Upload", "")
	return c.CaseID
}

// attachBatchUploadsToCase records workflow attachments for uploads that were ingested without a case_id.
func (s *Server) attachBatchUploadsToCase(caseID string, uploaded interface{}) {
	if caseID == "" {
		return
	}
	names, ok := uploaded.([]string)
	if !ok || len(names) == 0 {
		return
	}
	for _, name := range names {
		s.workflow.AttachDocument(caseID, name)
	}
}
