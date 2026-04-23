package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentflow-go/internal/config"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/model"
	"agentflow-go/internal/ocr"
	"agentflow-go/internal/processor"
	"agentflow-go/internal/rag"
	"agentflow-go/internal/worker"
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
	wsWriteMu  sync.Mutex
	workerPool *worker.Pool
	processor  *processor.BatchProcessor
	startTime  time.Time
}

func New(cfg *config.Config) *Server {
	workers := cfg.MaxConcurrent
	if workers < 1 {
		workers = 1
	}
	s := &Server{
		cfg:       cfg,
		mux:       http.NewServeMux(),
		startTime: time.Now(),
		clients:   make(map[*websocket.Conn]bool),
		jobs:      make(map[string]*model.Job),
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

	llmOpts := []llm.Option{}
	if cfg.LLMCacheEnabled && cfg.LLMCacheDir != "" {
		llmOpts = append(llmOpts, llm.WithResponseCache(cfg.LLMCacheDir, true))
	}
	switch cfg.LLMBackend {
	case "dashscope":
		s.llm = llm.NewProvider(cfg.ModelName, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, llm.BackendOpenAICompat, llmOpts...)
		s.ocr = ocr.NewEngine(cfg.OCRModelID, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, ocr.BackendOpenAICompat, cfg.MaxConcurrent, 10*time.Minute)
	default:
		s.llm = llm.NewProvider(cfg.ModelName, cfg.OllamaURL, "", llm.BackendOllama, llmOpts...)
		s.ocr = ocr.NewEngine(cfg.OCRModelID, cfg.OllamaURL, "", ocr.BackendOllama, cfg.MaxConcurrent, 5*time.Minute)
	}

	os.MkdirAll(filepath.Join(cfg.DataDir, "vector_store"), 0755)
	s.rag = rag.NewManager(filepath.Join(cfg.DataDir, "vector_store"))

	maxCases := cfg.MaxCases
	if maxCases < 1 {
		maxCases = 200
	}
	s.workflow = workflow.NewEngine(maxCases, cfg.DataDir, func() {
		go s.broadcastStatus()
	})

	s.processor = processor.NewBatchProcessor(s.ocr, s, s, s.rag, s.workflow, s, cfg.MaxConcurrent)

	wp := worker.New(workers)
	wp.SetJobUpdater(func(id string, upd func(*model.Job)) {
		s.updateJob(id, upd)
	})
	wp.SetAfterTerminal(func(j *model.Job) {
		s.broadcastStatus()
		jid := j.ID
		go func() {
			time.Sleep(5 * time.Second)
			s.jobsMu.Lock()
			delete(s.jobs, jid)
			s.jobsMu.Unlock()
			s.broadcastStatus()
		}()
	})
	s.workerPool = wp

	// Only seed the demo case when the store is empty (prevents duplicates on restart)
	if existing := s.workflow.ListCases(); len(existing) == 0 {
		s.workflow.CreateCase("ClientX", "Commercial Lease Dispute", "Demo", "")
	}

	s.setupRoutes()
	return s
}

func (s *Server) Router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[http] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) BatchProcessor() *processor.BatchProcessor {
	return s.processor
}

func (s *Server) Shutdown() {
	if s.workerPool != nil {
		s.workerPool.Shutdown()
	}
	s.llm.Unload()
	s.ocr.Unload()

	s.clientsMu.Lock()
	for conn := range s.clients {
		conn.Close()
	}
	s.clientsMu.Unlock()

	s.workflow.Close()
}

func (s *Server) wsWriteJSON(conn *websocket.Conn, v interface{}) error {
	s.wsWriteMu.Lock()
	defer s.wsWriteMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(25 * time.Second)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	return conn.WriteJSON(v)
}

func (s *Server) wsWriteMessage(conn *websocket.Conn, messageType int, data []byte) error {
	s.wsWriteMu.Lock()
	defer s.wsWriteMu.Unlock()
	if err := conn.SetWriteDeadline(time.Now().Add(25 * time.Second)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
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
	s.mux.HandleFunc("/api/models", s.handleListModels)
	s.mux.HandleFunc("/api/models/benchmark", s.handleBenchmarkModel)
	s.mux.HandleFunc("/v1/chat", s.handleChat)

	// Prefix handlers LAST
	s.mux.HandleFunc("/v1/jobs/", s.handleJobs)
	s.mux.HandleFunc("/v1/cases/", s.handleCases)
	s.mux.HandleFunc("/v1/documents/", s.handleDocuments)

	s.mux.Handle("/", http.FileServer(http.Dir(resolveFrontendDir())))
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

// decodeJSON is a convenience wrapper for parsing request bodies.
func (s *Server) decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
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
		Type:      model.JobType(jobType),
		Status:    model.JobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.jobsMu.Lock()
	s.jobs[jobID] = job
	s.jobsMu.Unlock()

	s.broadcastStatus()

	if err := s.workerPool.Enqueue(context.Background(), job, 0, fn); err != nil {
		s.updateJob(jobID, func(j *model.Job) {
			j.Status = model.JobStatusFailed
			j.Error = err.Error()
			j.UpdatedAt = time.Now()
		})
		s.broadcastStatus()
	}

	return jobID
}

func (s *Server) updateJob(id string, fn func(*model.Job)) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if j, ok := s.jobs[id]; ok {
		fn(j)
	}
}

func (s *Server) UpdateJob(id string, fn func(*model.Job)) {
	s.updateJob(id, fn)
}

func (s *Server) Classify(ctx context.Context, text, filename string) (map[string]interface{}, error) {
	res := s.classifyLegalDocumentFromOCR(text, filename)
	if res == nil {
		return nil, fmt.Errorf("classification returned nil")
	}
	return res, nil
}

func (s *Server) AnalyzeBatch(ctx context.Context, docs map[string]string) (processor.BatchMeta, error) {
	raw := s.analyzeBatchFromOCR(docs)
	meta := processor.BatchMeta{
		ClientName: raw.ClientName,
		MatterType: raw.MatterType,
		LLMError:   raw.LLMError,
		Files:      make([]processor.FileMeta, len(raw.Files)),
	}
	for i, f := range raw.Files {
		meta.Files[i] = processor.FileMeta{
			Filename:     f.Filename,
			DocumentType: f.DocumentType,
		}
	}
	return meta, nil
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

	s.jobsMu.RLock()
	allJobs := make([]model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		allJobs = append(allJobs, *j)
	}
	s.jobsMu.RUnlock()

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"version":        "v2-go",
		"cases":          cases,
		"case_count":     len(cases),
		"jobs":           allJobs,
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
		log.Printf("[WebSocket] Upgrade failed: %v", err)
		return
	}

	if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
		log.Printf("[WebSocket] SetReadDeadline failed: %v", err)
		_ = conn.Close()
		return
	}
	conn.SetPongHandler(func(string) error {
		if err := conn.SetReadDeadline(time.Now().Add(120 * time.Second)); err != nil {
			log.Printf("[WebSocket] Pong handler SetReadDeadline failed: %v", err)
			return err
		}
		return nil
	})

	s.clientsMu.Lock()
	s.clients[conn] = true
	connCount := len(s.clients)
	s.clientsMu.Unlock()

	log.Printf("WebSocket connected, total: %d", connCount)

	cases := s.workflow.ListCases()
	s.jobsMu.RLock()
	initJobs := make([]model.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		initJobs = append(initJobs, *j)
	}
	s.jobsMu.RUnlock()
	data := map[string]interface{}{
		"type":       "status_update",
		"cases":      cases,
		"case_count": len(cases),
		"rag":        s.rag.GetSummary(),
		"jobs":       initJobs,
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

// resolveFrontendDir finds the frontend directory whether running from source or .app bundle.
func resolveFrontendDir() string {
	if exe, err := os.Executable(); err == nil {
		// macOS .app bundle: server binary (e.g. agentflow-serve) under Contents/MacOS or Resources; static UI in Contents/Resources/frontend
		bundleFrontend := filepath.Join(filepath.Dir(exe), "..", "Resources", "frontend")
		if fi, err := os.Stat(bundleFrontend); err == nil && fi.IsDir() {
			return bundleFrontend
		}
	}
	for _, dir := range []string{"frontend"} {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return "frontend"
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
