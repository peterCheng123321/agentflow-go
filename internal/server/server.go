package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentflow-go/internal/config"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/model"
	"agentflow-go/internal/ocr"
	"agentflow-go/internal/rag"
	"agentflow-go/internal/workflow"

	"github.com/gorilla/websocket"
)

type Server struct {
	cfg        *config.Config
	llm        *llm.Provider
	ocr        *ocr.Engine
	rag        *rag.Manager
	workflow   *workflow.Engine
	mux        *http.ServeMux
	upgrader   websocket.Upgrader
	clients    map[*websocket.Conn]bool
	clientsMu  sync.Mutex
	jobs       map[string]*model.Job
	jobsMu     sync.RWMutex
	wsWriteMu  sync.Mutex // one writer at a time per connection (Gorilla WS); serializes all client writes
	workerPool chan struct{}
	startTime  time.Time
}

func New(cfg *config.Config) *Server {
	workers := cfg.MaxConcurrent
	if workers < 1 {
		workers = 1
	}
	s := &Server{
		cfg:        cfg,
		mux:        http.NewServeMux(),
		startTime:  time.Now(),
		clients:    make(map[*websocket.Conn]bool),
		jobs:       make(map[string]*model.Job),
		workerPool: make(chan struct{}, workers),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	if cfg.LLMBackend == "dashscope" && cfg.DashScopeAPIKey == "" {
		log.Fatal("LLM backend is dashscope but no API key: set AGENTFLOW_DASHSCOPE_API_KEY or DASHSCOPE_API_KEY, AGENTFLOW_DASHSCOPE_API_KEY_FILE, or create data/secrets/dashscope_api_key.txt (see data/secrets/dashscope_api_key.txt.example)")
	}

	switch cfg.LLMBackend {
	case "dashscope":
		s.llm = llm.NewProvider(cfg.ModelName, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, llm.BackendOpenAICompat)
		s.ocr = ocr.NewEngine(cfg.OCRModelID, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, ocr.BackendOpenAICompat, cfg.MaxConcurrent, 10*time.Minute)
	default:
		s.llm = llm.NewProvider(cfg.ModelName, cfg.OllamaURL, "", llm.BackendOllama)
		s.ocr = ocr.NewEngine(cfg.OCRModelID, cfg.OllamaURL, "", ocr.BackendOllama, cfg.MaxConcurrent, 5*time.Minute)
	}

	os.MkdirAll(filepath.Join(cfg.DataDir, "vector_store"), 0755)
	s.rag = rag.NewManager(filepath.Join(cfg.DataDir, "vector_store"))

	maxCases := cfg.MaxCases
	if maxCases < 1 {
		maxCases = 200
	}
	s.workflow = workflow.NewEngine(maxCases, func() {
		go s.broadcastStatus()
	})

	s.workflow.CreateCase("ClientX", "Commercial Lease Dispute", "Demo", "")

	s.setupRoutes()
	return s
}

func (s *Server) Router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[http] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) Shutdown() {
	s.llm.Unload()
	s.ocr.Unload()

	s.clientsMu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.clientsMu.Unlock()
}

func (s *Server) wsWriteJSON(conn *websocket.Conn, v interface{}) error {
	s.wsWriteMu.Lock()
	defer s.wsWriteMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(25 * time.Second))
	return conn.WriteJSON(v)
}

func (s *Server) wsWriteMessage(conn *websocket.Conn, messageType int, data []byte) error {
	s.wsWriteMu.Lock()
	defer s.wsWriteMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(25 * time.Second))
	return conn.WriteMessage(messageType, data)
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/status", s.handleStatus)
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/v1/cases", s.handleListCases)
	s.mux.HandleFunc("/v1/cases/create", s.handleCreateCase)
	s.mux.HandleFunc("/v1/upload/directory", s.handleUploadDirectory)
	s.mux.HandleFunc("/v1/upload/batch", s.handleUploadBatch)
	s.mux.HandleFunc("/v1/upload", s.handleUpload)
	s.mux.HandleFunc("/v1/rag/search", s.handleSearch)
	s.mux.HandleFunc("/v1/rag/summary", s.handleRAGSummary)
	s.mux.HandleFunc("/v1/documents", s.handleListDocuments)
	s.mux.HandleFunc("/v1/device", s.handleDeviceStatus)

	// Prefix handlers LAST
	s.mux.HandleFunc("/v1/jobs/", s.handleJobs)
	s.mux.HandleFunc("/v1/cases/", s.handleCases)
	s.mux.HandleFunc("/v1/documents/", s.handleDocuments)

	s.mux.Handle("/", http.FileServer(http.Dir("./frontend")))
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	s.jobsMu.RLock()
	job, ok := s.jobs[jobID]
	var snap model.Job
	if ok {
		snap = *job
	}
	s.jobsMu.RUnlock()

	if !ok {
		s.writeError(w, http.StatusNotFound, "Job not found")
		return
	}
	s.writeJSON(w, http.StatusOK, snap)
}

func (s *Server) submitJob(jobType string, fn func(job *model.Job) (any, error)) string {
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	job := &model.Job{
		ID:        jobID,
		Type:      jobType,
		Status:    model.JobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.jobsMu.Lock()
	s.jobs[jobID] = job
	s.jobsMu.Unlock()

	s.broadcastStatus()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("job %s panic: %v", jobID, r)
				s.updateJob(jobID, func(j *model.Job) {
					j.Status = model.JobStatusFailed
					j.Error = fmt.Sprintf("internal error: %v", r)
					j.UpdatedAt = time.Now()
				})
				s.broadcastStatus()
			}
		}()

		// Wait for slot in worker pool
		s.workerPool <- struct{}{}
		defer func() { <-s.workerPool }()

		s.updateJob(jobID, func(j *model.Job) {
			j.Status = model.JobStatusProcessing
			j.UpdatedAt = time.Now()
		})

		result, err := fn(job)

		s.updateJob(jobID, func(j *model.Job) {
			if err != nil {
				j.Status = model.JobStatusFailed
				j.Error = err.Error()
			} else {
				j.Status = model.JobStatusCompleted
				j.Result = result
				j.Progress = 100
			}
			j.UpdatedAt = time.Now()
		})
		s.broadcastStatus()

		// Cleanup job after 5 seconds so it fades out of the UI
		go func() {
			time.Sleep(5 * time.Second)
			s.jobsMu.Lock()
			delete(s.jobs, jobID)
			s.jobsMu.Unlock()
			s.broadcastStatus()
		}()
	}()

	return jobID
}

func (s *Server) updateJob(id string, fn func(*model.Job)) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "v2-go",
		"uptime":  time.Since(s.startTime).String(),
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cases := s.workflow.ListCases()
	ragSummary := s.rag.GetSummary()

	s.clientsMu.Lock()
	wsN := len(s.clients)
	s.clientsMu.Unlock()

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":        "v2-go",
		"cases":          cases,
		"case_count":     len(cases),
		"rag":            ragSummary,
		"max_cases":      s.cfg.MaxCases,
		"max_concurrent": s.cfg.MaxConcurrent,
		"uptime":         time.Since(s.startTime).String(),
		"active_ws":      wsN,
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	})

	s.clientsMu.Lock()
	s.clients[conn] = true
	connCount := len(s.clients)
	s.clientsMu.Unlock()

	log.Printf("WebSocket connected, total: %d", connCount)

	cases := s.workflow.ListCases()
	data := map[string]interface{}{
		"type":       "status_update",
		"cases":      cases,
		"case_count": len(cases),
		"rag":        s.rag.GetSummary(),
	}
	if err := s.wsWriteJSON(conn, data); err != nil {
		log.Printf("WebSocket initial write: %v", err)
		_ = conn.Close()
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
		return
	}

	pingStop := make(chan struct{})
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := s.wsWriteMessage(conn, websocket.PingMessage, nil); err != nil {
					return
				}
			case <-pingStop:
				return
			}
		}
	}()
	defer close(pingStop)

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		connCount := len(s.clients)
		s.clientsMu.Unlock()
		_ = conn.Close()
		log.Printf("WebSocket disconnected, total: %d", connCount)
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (s *Server) broadcastStatus() {
	cases := s.workflow.ListCases()

	s.jobsMu.RLock()
	allJobs := make([]model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		allJobs = append(allJobs, *j)
	}
	s.jobsMu.RUnlock()

	data := map[string]interface{}{
		"type":       "status_update",
		"cases":      cases,
		"case_count": len(cases),
		"rag":        s.rag.GetSummary(),
		"jobs":       allJobs,
	}

	s.clientsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.clients))
	for c := range s.clients {
		conns = append(conns, c)
	}
	s.clientsMu.Unlock()

	for _, conn := range conns {
		if err := s.wsWriteJSON(conn, data); err != nil {
			s.clientsMu.Lock()
			delete(s.clients, conn)
			s.clientsMu.Unlock()
			_ = conn.Close()
		}
	}
}

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
	case "documents":
		if len(parts) == 3 && parts[2] == "reassign" && r.Method == http.MethodPost {
			s.handleReassignDocument(w, r, caseID)
			return
		}
		if len(parts) >= 3 && (r.Method == http.MethodDelete || r.Method == http.MethodPost) {
			filename := strings.Join(parts[2:], "/")
			s.handleDeleteDocumentFromCase(w, r, caseID, filename)
			return
		}
		s.writeError(w, http.StatusNotFound, "Action not found")
	default:
		// Check if it's a PUT request to update the case itself
		if r.Method == http.MethodPut {
			s.handleUpdateCaseByID(w, r, caseID)
			return
		}
		s.writeError(w, http.StatusNotFound, "Action not found")
	}
}

func (s *Server) handleReassignDocument(w http.ResponseWriter, r *http.Request, sourceCaseID string) {
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

	// Also remove from RAG (optional, but good for cleanup)
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

	// Clean up documents in RAG
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

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	if err := r.ParseMultipartForm(50 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	reqCaseID := strings.TrimSpace(r.FormValue("case_id"))

	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "No file uploaded")
		return
	}
	defer file.Close()

	logicalName := sanitizeUploadedBasename(header.Filename)
	if logicalName == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	os.MkdirAll(filepath.Join(s.cfg.DataDir, "docs"), 0755)
	savePath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), logicalName))

	dst, err := os.Create(savePath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to save file")
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to write file")
		return
	}
	fileSize := header.Size
	if fileSize <= 0 && written > 0 {
		fileSize = written
	}

	jobID := s.submitJob("upload", func(j *model.Job) (any, error) {
		s.updateJob(j.ID, func(job *model.Job) { job.Progress = 12 })
		text, err := s.ocr.ScanFile(savePath)
		if err != nil {
			log.Printf("OCR error: %v", err)
			text = fmt.Sprintf("[OCR Error] %v", err)
		}

		s.updateJob(j.ID, func(job *model.Job) { job.Progress = 38 })
		meta := map[string]interface{}{
			"filename": logicalName,
			"size":     fileSize,
		}
		var classification map[string]interface{}
		if cls := s.classifyLegalDocumentFromOCR(text, logicalName); cls != nil {
			meta["classification"] = cls
			classification = cls
		}
		if err := s.rag.IngestFile(savePath, text, meta); err != nil {
			return nil, fmt.Errorf("ingestion failed: %v", err)
		}

		var finalCaseID string
		var clientName, matterType, intakeSrc string

		if reqCaseID != "" {
			// Explicit target case, skip heavy Intake inference
			finalCaseID = reqCaseID
			snap, ok := s.workflow.GetCaseSnapshot(finalCaseID)
			if ok {
				clientName = snap.ClientName
				matterType = snap.MatterType
				intakeSrc = "explicit_ui"
			}
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 70 })
		} else {
			// Infer from document to create/route to a case
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 48 })
			clientName, matterType, intakeSrc = s.inferIntakeFromOCR(text, logicalName)
			finalCaseID = s.resolveCaseForDocument(clientName, matterType, logicalName)
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 90 })
		}

		if classification != nil {
			s.workflow.AttachDocument(finalCaseID, logicalName, map[string]interface{}{
				"classification": classification,
			})
		} else {
			s.workflow.AttachDocument(finalCaseID, logicalName)
		}

		out := map[string]interface{}{
			"status":        "uploaded",
			"filename":      logicalName,
			"case_id":       finalCaseID,
			"client_name":   clientName,
			"matter_type":   matterType,
			"intake_source": intakeSrc,
			"text_length":   len(text),
		}
		if classification != nil {
			out["classification"] = classification
		}
		return out, nil
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":   jobID,
		"filename": logicalName,
	})
}

func (s *Server) handleUploadBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)

	if err := r.ParseMultipartForm(500 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to parse form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	reqCaseID := strings.TrimSpace(r.FormValue("case_id"))
	fileHeaders := r.MultipartForm.File["files"]

	if len(fileHeaders) == 0 {
		s.writeError(w, http.StatusBadRequest, "No files uploaded")
		return
	}

	os.MkdirAll(filepath.Join(s.cfg.DataDir, "docs"), 0755)

	var savedFiles []string
	for _, header := range fileHeaders {
		file, err := header.Open()
		if err != nil {
			continue
		}

		logicalName := sanitizeUploadedBasename(header.Filename)
		if logicalName == "" {
			file.Close()
			continue
		}

		if strings.HasSuffix(strings.ToLower(logicalName), ".zip") {
			tmpZipPath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("tmp-%d-%s", time.Now().UnixNano(), logicalName))
			tmpZip, _ := os.Create(tmpZipPath)
			io.Copy(tmpZip, file)
			tmpZip.Close()
			file.Close()

			reader, err := zip.OpenReader(tmpZipPath)
			if err == nil {
				for _, f := range reader.File {
					if f.FileInfo().IsDir() {
						continue
					}
					rc, err := f.Open()
					if err != nil {
						continue
					}
					fName := sanitizeUploadedBasename(filepath.Base(f.Name))
					if fName != "" {
						outPath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), fName))
						dst, err := os.Create(outPath)
						if err == nil {
							io.Copy(dst, rc)
							dst.Close()
							savedFiles = append(savedFiles, outPath)
						}
					}
					rc.Close()
				}
				reader.Close()
			}
			os.Remove(tmpZipPath)
			continue
		}

		savePath := filepath.Join(s.cfg.DataDir, "docs", fmt.Sprintf("%d-%s", time.Now().UnixNano(), logicalName))
		dst, err := os.Create(savePath)
		if err == nil {
			io.Copy(dst, file)
			dst.Close()
			savedFiles = append(savedFiles, savePath)
		}
		file.Close()
	}

	jobID := s.submitJob("batch_upload", func(j *model.Job) (any, error) {
		return s.processBatchUpload(j, savedFiles, reqCaseID)
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id": jobID,
	})
}

func (s *Server) processBatchUpload(j *model.Job, files []string, reqCaseID string) (any, error) {
	var uploaded []string
	var failed []string
	var mu sync.Mutex
	var targetCaseID string

	total := len(files)
	var completed int

	ocrResults := make(map[string]string)

	var wg sync.WaitGroup
	concurrencyLimit := s.cfg.MaxConcurrent * 2
	if concurrencyLimit < 4 {
		concurrencyLimit = 4
	}
	sem := make(chan struct{}, concurrencyLimit)

	s.updateJob(j.ID, func(job *model.Job) { job.Progress = 5 })

	for _, filePath := range files {
		wg.Add(1)
		sem <- struct{}{}

		go func(path string) {
			defer wg.Done()
			defer func() { <-sem }()

			fileName := filepath.Base(path)
			// Remove the timestamp prefix for display purposes if present
			displayFileName := fileName
			if parts := strings.SplitN(fileName, "-", 2); len(parts) == 2 && len(parts[0]) > 10 {
				displayFileName = parts[1]
			}

			text, err := s.ocr.ScanFile(path)

			mu.Lock()
			completed++
			s.updateJob(j.ID, func(job *model.Job) {
				job.Progress = 5 + int(float64(completed)/float64(total)*45) // First 50% is OCR
			})
			if err != nil {
				log.Printf("OCR failed for %s: %v", displayFileName, err)
				failed = append(failed, displayFileName)
			} else {
				ocrResults[displayFileName] = text
			}
			mu.Unlock()
		}(filePath)
	}

	wg.Wait()
	log.Printf("[batch] OCR complete for %d files. Starting analysis...", len(ocrResults))

	s.updateJob(j.ID, func(job *model.Job) { job.Progress = 60 })

	// --- Optimized Batch Context Selection ---
	// Instead of sending snippets of all 45 files, rank them and pick the "Golden Files"
	type rankedFile struct {
		name  string
		text  string
		score int
	}
	var ranked []rankedFile
	for name, text := range ocrResults {
		score := len(text) / 100 // Base score on length
		lower := strings.ToLower(name)
		if strings.Contains(lower, "起诉") || strings.Contains(lower, "complaint") {
			score += 500
		}
		if strings.Contains(lower, "身份证") || strings.Contains(lower, "id_card") {
			score += 400
		}
		if strings.Contains(lower, "合同") || strings.Contains(lower, "contract") {
			score += 300
		}
		if strings.Contains(lower, "欠条") || strings.Contains(lower, "iou") {
			score += 300
		}
		if strings.Contains(lower, "律师函") || strings.Contains(lower, "lawyer") {
			score += 300
		}
		if strings.HasSuffix(lower, ".docx") {
			score += 200
		}

		ranked = append(ranked, rankedFile{name, text, score})
	}
	// Sort by score descending
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	// Select top 12 files for the LLM to analyze the case context
	selectedDocs := make(map[string]string)
	limit := 12
	if len(ranked) < limit {
		limit = len(ranked)
	}
	for i := 0; i < limit; i++ {
		selectedDocs[ranked[i].name] = ranked[i].text
	}

	log.Printf("[batch] Selected %d/%d top files for case synthesis", limit, len(ocrResults))

	// Run single LLM call for the entire batch context
	batchMeta := s.analyzeBatchFromOCR(selectedDocs)
	log.Printf("[batch] Batch analysis complete. Clusters: %d", len(batchMeta.Files))

	s.updateJob(j.ID, func(job *model.Job) { job.Progress = 80 })

	// --- Multi-Case Resolution ---
	fileToCaseID := make(map[string]string)
	if reqCaseID != "" {
		for fileName := range ocrResults {
			fileToCaseID[fileName] = reqCaseID
		}
		targetCaseID = reqCaseID
	} else {
		// batchMeta.ClientName is the primary/root case
		defaultCaseID := s.resolveCaseForDocument(batchMeta.ClientName, batchMeta.MatterType, "Batch Root")
		targetCaseID = defaultCaseID
		for _, f := range batchMeta.Files {
			// If LLM found a specific human client for this file, use it
			if f.ClientName != "" && f.ClientName != "Unknown Client" && f.ClientName != batchMeta.ClientName {
				fileToCaseID[f.Filename] = s.resolveCaseForDocument(f.ClientName, f.MatterType, f.Filename)
			} else {
				fileToCaseID[f.Filename] = defaultCaseID
			}
		}
	}

	// Map classifications
	classMap := make(map[string]map[string]interface{})
	for _, f := range batchMeta.Files {
		entities := map[string]interface{}{}
		p := f.Plaintiffs
		if len(p) == 0 {
			p = batchMeta.Plaintiffs
		}
		d := f.Defendants
		if len(d) == 0 {
			d = batchMeta.Defendants
		}

		if len(p) > 0 {
			entities["plaintiffs"] = p
		}
		if len(d) > 0 {
			entities["defendants"] = d
		}

		classMap[f.Filename] = map[string]interface{}{
			"document_type":   f.DocumentType,
			"display_name_zh": f.DisplayNameZH,
			"summary_zh":      f.SummaryZH,
			"entities":        entities,
			"source":          "llm_batch",
		}
	}

	// Ingest into RAG and Attach to Case
	for i, filePath := range files {
		fileName := filepath.Base(filePath)
		displayFileName := fileName
		if parts := strings.SplitN(fileName, "-", 2); len(parts) == 2 && len(parts[0]) > 10 {
			displayFileName = parts[1]
		}

		if text, ok := ocrResults[displayFileName]; ok {
			meta := map[string]interface{}{
				"filename": displayFileName,
			}
			if cls, exists := classMap[displayFileName]; exists {
				meta["classification"] = cls
			}

			if err := s.rag.IngestFile(filePath, text, meta); err != nil {
				failed = append(failed, displayFileName)
			} else {
				uploaded = append(uploaded, displayFileName)
				targetCaseID := fileToCaseID[displayFileName]
				if targetCaseID == "" {
					targetCaseID = s.resolveCaseForDocument(batchMeta.ClientName, batchMeta.MatterType, displayFileName)
				}

				if cls, exists := classMap[displayFileName]; exists {
					s.workflow.AttachDocument(targetCaseID, displayFileName, map[string]interface{}{
						"classification": cls,
					})
				} else {
					s.workflow.AttachDocument(targetCaseID, displayFileName)
				}
			}
		}
		s.updateJob(j.ID, func(job *model.Job) {
			job.Progress = 80 + int(float64(i)/float64(total)*20)
		})
	}

	return map[string]interface{}{
		"uploaded": uploaded,
		"failed":   failed,
		"count":    len(uploaded),
		"case_id":  targetCaseID,
	}, nil
}

func (s *Server) handleUploadDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		DirectoryPath string `json:"directory_path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var files []string
	err := filepath.WalkDir(req.DirectoryPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Cannot read directory: %v", err))
		return
	}

	jobID := s.submitJob("directory_upload", func(j *model.Job) (any, error) {
		var uploaded []string
		var failed []string
		var mu sync.Mutex

		total := len(files)
		var completed int

		var primaryCaseID string
		var primaryClientName string
		var primaryMatterType string

		// Pre-scan to find a primary file (e.g. a docx or a named file) to establish case context
		var bestFile string
		for _, path := range files {
			name := filepath.Base(path)
			if strings.HasSuffix(strings.ToLower(name), ".docx") {
				bestFile = path
				break
			}
			if bestFile == "" {
				ext := strings.ToLower(filepath.Ext(name))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
					bestFile = path
				}
			}
		}

		if bestFile != "" {
			s.updateJob(j.ID, func(job *model.Job) { job.Progress = 5 })
			text, _ := s.ocr.ScanFile(bestFile)
			baseName := filepath.Base(bestFile)
			primaryClientName, primaryMatterType, _ = s.inferIntakeFromOCR(text, baseName)
			primaryCaseID = s.resolveCaseForDocument(primaryClientName, primaryMatterType, baseName)
		}

		var wg sync.WaitGroup
		concurrencyLimit := s.cfg.MaxConcurrent * 2
		if concurrencyLimit < 4 {
			concurrencyLimit = 4
		}
		sem := make(chan struct{}, concurrencyLimit)

		for _, filePath := range files {
			wg.Add(1)
			sem <- struct{}{}

			go func(path string) {
				defer wg.Done()
				defer func() { <-sem }()

				fileName := filepath.Base(path)
				text, err := s.ocr.ScanFile(path)

				mu.Lock()
				completed++
				s.updateJob(j.ID, func(job *model.Job) {
					job.Progress = int(float64(completed) / float64(total) * 100)
				})
				mu.Unlock()

				if err != nil {
					log.Printf("OCR failed for %s: %v", fileName, err)
					mu.Lock()
					failed = append(failed, fileName)
					mu.Unlock()
					return
				}

				meta := map[string]interface{}{
					"filename": fileName,
				}
				var classification map[string]interface{}
				if cls := s.classifyLegalDocumentFromOCR(text, fileName); cls != nil {
					meta["classification"] = cls
					classification = cls
				}
				if err := s.rag.IngestFile(path, text, meta); err != nil {
					mu.Lock()
					failed = append(failed, fileName)
					mu.Unlock()
					return
				}

				mu.Lock()
				uploaded = append(uploaded, fileName)
				targetCaseID := primaryCaseID
				if targetCaseID == "" {
					clientName, matterType, _ := s.inferIntakeFromOCR(text, fileName)
					targetCaseID = s.resolveCaseForDocument(clientName, matterType, fileName)
				}

				if classification != nil {
					s.workflow.AttachDocument(targetCaseID, fileName, map[string]interface{}{
						"classification": classification,
					})
				} else {
					s.workflow.AttachDocument(targetCaseID, fileName)
				}
				mu.Unlock()
			}(filePath)
		}

		wg.Wait()

		return map[string]interface{}{
			"uploaded": uploaded,
			"failed":   failed,
			"count":    len(uploaded),
			"case_id":  primaryCaseID,
		}, nil
	})

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id": jobID,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.K <= 0 {
		req.K = 5
	}

	results := s.rag.Search(req.Query, req.K)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   req.Query,
		"results": results,
		"count":   len(results),
	})
}

func (s *Server) handleRAGSummary(w http.ResponseWriter, r *http.Request) {
	summary := s.rag.GetSummary()
	s.writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleDocuments(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/documents/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		s.handleListDocuments(w, r)
		return
	}

	filename := parts[0]
	if len(parts) == 1 {
		// Default to metadata if no action
		s.handleDocumentMetadataByID(w, r, filename)
		return
	}

	action := parts[1]
	switch action {
	case "view":
		s.handleViewDocumentByID(w, r, filename)
	case "content":
		s.handleDocumentContentByID(w, r, filename)
	case "metadata":
		s.handleDocumentMetadataByID(w, r, filename)
	default:
		s.writeError(w, http.StatusNotFound, "Action not found")
	}
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	summary := s.rag.GetSummary()
	s.writeJSON(w, http.StatusOK, summary)
}

func unescapeURLPathSegment(seg string) string {
	out := seg
	for i := 0; i < 3; i++ {
		dec, err := url.PathUnescape(out)
		if err != nil || dec == out {
			break
		}
		out = dec
	}
	return out
}

func truncateRunesStr(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes])
}

// resolveDocDiskPath finds bytes on disk if the RAG-stored path is stale (cwd/data dir changed).
func (s *Server) resolveDocDiskPath(doc model.DocumentRecord) (abs string, ok bool) {
	candidates := []string{
		doc.Path,
		filepath.Join(s.cfg.DataDir, "docs", filepath.Base(doc.Path)),
	}
	if wd, err := os.Getwd(); err == nil {
		if !filepath.IsAbs(doc.Path) {
			candidates = append(candidates, filepath.Join(wd, doc.Path))
		}
		candidates = append(candidates, filepath.Join(wd, s.cfg.DataDir, "docs", filepath.Base(doc.Path)))
	}
	seen := map[string]bool{}
	for _, p := range candidates {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return doc.Path, false
}

func (s *Server) handleViewDocumentByID(w http.ResponseWriter, r *http.Request, filename string) {
	filename = unescapeURLPathSegment(filename)

	doc, ok := s.rag.GetDocumentFlex(filename)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Document not found")
		return
	}

	diskPath, found := s.resolveDocDiskPath(doc)
	if !found {
		log.Printf("[view] missing file for %q tried path %q", doc.Filename, doc.Path)
		s.writeError(w, http.StatusNotFound, "File not found on disk")
		return
	}

	// Help browsers embed PDFs/images in iframes on same origin
	w.Header().Set("Content-Disposition", "inline; filename=\""+filepath.Base(doc.Filename)+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, diskPath)
}

func (s *Server) handleDocumentContentByID(w http.ResponseWriter, r *http.Request, filename string) {
	decoded, err := url.QueryUnescape(filename)
	if err == nil {
		filename = decoded
	}

	doc, ok := s.rag.GetDocumentFlex(filename)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Document not found")
		return
	}

	content := strings.Join(doc.Chunks, "\n\n")
	s.writeJSON(w, http.StatusOK, map[string]string{
		"content": content,
	})
}

func (s *Server) handleDocumentMetadataByID(w http.ResponseWriter, r *http.Request, filename string) {
	filename = unescapeURLPathSegment(filename)

	doc, ok := s.rag.GetDocumentFlex(filename)
	if !ok {
		s.writeError(w, http.StatusNotFound, "Document not found")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"filename":  doc.Filename,
		"path":      doc.Path,
		"file_type": doc.FileType,
		"size":      doc.FileSizeBytes,
		"chunks":    len(doc.Chunks),
		"metadata":  doc.AIMetadata,
	})
}

func (s *Server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]interface{}{
		"platform_id":        "macOS",
		"chip_name":          "Apple Silicon",
		"memory_mb":          s.cfg.MaxMemoryMB,
		"is_apple_silicon":   s.cfg.IsAppleSilicon,
		"max_concurrent":     s.cfg.MaxConcurrent,
		"m_series_optimized": s.cfg.IsAppleSilicon,
		"llm_backend":        s.cfg.LLMBackend,
		"inference_backend":  "ollama",
		"llm_model":          s.cfg.ModelName,
		"ocr_model":          s.cfg.OCRModelID,
	}
	switch s.cfg.LLMBackend {
	case "dashscope":
		out["inference_backend"] = "dashscope"
		out["dashscope_base_url"] = s.cfg.DashScopeBaseURL
		if s.cfg.IsAppleSilicon {
			out["sidecar_url"] = "http://127.0.0.1:8081"
			out["notes"] = "Text LLM: Alibaba DashScope API (OpenAI-compatible). Vision OCR still uses local MLX sidecar when on Apple Silicon."
		} else {
			out["notes"] = "Text LLM: Alibaba DashScope API. OCR uses Ollama endpoint (AGENTFLOW_OCR_MODEL / OLLAMA_URL)."
		}
	case "mlx":
		out["inference_backend"] = "mlx-lm"
		out["sidecar_url"] = "http://127.0.0.1:8081"
		out["notes"] = "MLX sidecar + local models; default max_concurrent=1 on arm64 to protect unified memory (set AGENTFLOW_MAX_CONCURRENT to scale)."
	default:
		out["ollama_url"] = s.cfg.OllamaURL
		if !s.cfg.IsAppleSilicon {
			out["m_series_optimized"] = false
		}
	}
	s.writeJSON(w, http.StatusOK, out)
}

// clipContext limits context length to prevent OOM/sidecar crash
func clipContext(context string, maxLength int) string {
	if len(context) > maxLength {
		return context[:maxLength]
	}
	return context
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

	// MLX local models: keep context rune-bounded (byte slice used to break UTF-8) and prompt size modest to avoid sidecar failures.
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

func sanitizeUploadedBasename(name string) string {
	b := filepath.Base(name)
	b = strings.ReplaceAll(b, string(os.PathSeparator), "_")
	b = strings.ReplaceAll(b, "/", "_")
	b = strings.ReplaceAll(b, "..", "_")
	b = strings.TrimSpace(b)
	if b == "" || b == "." {
		return ""
	}
	return b
}

func extractMatterType(filename string) string {
	hints := map[string]string{
		"买卖": "Sales Contract Dispute",
		"合同": "Contract Dispute",
		"欠款": "Debt Dispute",
		"借贷": "Loan Dispute",
		"租赁": "Lease Dispute",
		"劳务": "Labor Dispute",
		"劳动": "Labor Dispute",
		"起诉": "Civil Litigation",
		"诉讼": "Civil Litigation",
	}

	for keyword, matter := range hints {
		if strings.Contains(filename, keyword) {
			return matter
		}
	}

	return "Civil Litigation"
}
