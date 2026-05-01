package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentflow-go/internal/embedrouter"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
)

// FolderIntakeFileResult — per-file outcome from a folder intake run.
type FolderIntakeFileResult struct {
	StagedPath    string `json:"staged_path"`     // absolute path on disk inside the staging dir
	OriginalName  string `json:"original_name"`   // basename the user uploaded
	RelativePath  string `json:"relative_path"`   // path inside the source folder (with separators)
	SizeBytes     int64  `json:"size_bytes"`
	DocumentType  string `json:"document_type"`   // LLM-inferred slug, e.g. "civil_complaint"
	DisplayNameZH string `json:"display_name_zh"` // 微信聊天记录, 起诉状, etc.
	SummaryZH     string `json:"summary_zh"`
	OCRError      string `json:"ocr_error,omitempty"`
}

// FolderIntakeResult — what the UI gets back to populate the confirmation sheet.
type FolderIntakeResult struct {
	StagingID     string                   `json:"staging_id"`     // dir name under data/staging/<id>; used by /v1/intake/folder/commit
	FolderName    string                   `json:"folder_name"`    // original user-supplied folder name (e.g. "10.15 罗海霞")
	ClientName    string                   `json:"client_name"`
	MatterType    string                   `json:"matter_type"`
	Plaintiffs    []string                 `json:"plaintiffs"`
	Defendants    []string                 `json:"defendants"`
	Files         []FolderIntakeFileResult `json:"files"`
	ModelUsed     string                   `json:"model_used"`
	ElapsedMillis int64                    `json:"elapsed_ms"`
	LLMError      string                   `json:"llm_error,omitempty"`
}

// modelForTask returns the configured model ID for a given pipeline task,
// implementing the "multi-LLM" routing the user asked for: fast model for
// classification/intake, complex model for synthesis, OCR model for vision,
// router for live intent + step labels.
//
// All are configured via AGENTFLOW_MODEL_* / AGENTFLOW_ROUTER_* env vars in
// internal/config/config.go and fall back to ModelName if unset.
func (s *Server) modelForTask(task string) string {
	switch task {
	case "intake": // filename hints, fast classification
		if s.cfg.ModelMedium != "" {
			return s.cfg.ModelMedium
		}
	case "synth": // multi-doc synthesis (this is where accuracy matters most)
		if s.cfg.ModelComplex != "" {
			return s.cfg.ModelComplex
		}
	case "ocr": // images
		if s.cfg.ModelOCR != "" {
			return s.cfg.ModelOCR
		}
	case "router", "labeler": // tiny local model, live work
		if s.router != nil && s.routerMgr != nil && s.routerMgr.Ready() {
			return s.cfg.RouterModel
		}
		// Fall through to ModelMedium/ModelName when the local router is
		// disabled or still warming up — better than failing the request.
		if s.cfg.ModelMedium != "" {
			return s.cfg.ModelMedium
		}
	}
	return s.cfg.ModelName
}

// providerForTask returns the LLM Provider that should service a given task.
// Router and labeler tasks use the local MLX provider when it's ready;
// everything else uses the main s.llm.
func (s *Server) providerForTask(task string) *llm.Provider {
	switch task {
	case "router", "labeler":
		if s.router != nil && s.routerMgr != nil && s.routerMgr.Ready() {
			return s.router
		}
	}
	return s.llm
}

// Intent labels emitted by the local router. Callers should treat the
// router's reply as a *prefix* — e.g. "NEEDS_T" classifies as IntentTools.
const (
	IntentTools          = "NEEDS_TOOLS"
	IntentRAG            = "NEEDS_RAG"
	IntentConversational = "CONVERSATIONAL"
	IntentUnknown        = "UNKNOWN"
)

// classifyIntent picks one of the Intent* constants for a short user
// message. Prefers the local MLX embedding router (cosine over labeled
// exemplars, ~11ms warm). Falls back to the LLM router (~110ms) when the
// embed sidecar isn't ready. Returns IntentUnknown when both are out —
// callers treat UNKNOWN as "use the safe slow path".
//
// Confidence gate: if the embed router's top1−top2 margin falls below
// cfg.EmbedRouterMargin, we *also* return IntentUnknown rather than the
// guessed label. This addresses the asymmetric-error gap from the
// research: routing TOOLS/RAG → CONV by mistake silently hallucinates
// without retrieval; better to pay the slow-path cost on borderline calls.
func (s *Server) classifyIntent(ctx context.Context, userMessage string) string {
	// Primary: embedding router.
	if s.embedRouter != nil && s.embedMgr != nil && s.embedMgr.Ready() {
		if !s.ensureEmbedCorpus(ctx) {
			// init failed — fall through to LLM router below
		} else {
			ctx2, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
			res, err := s.embedRouter.Classify(ctx2, userMessage)
			cancel()
			if err == nil {
				if res.Margin < s.cfg.EmbedRouterMargin {
					log.Printf("[embed-router] margin %.3f < %.3f for %q — escalating to safe path",
						res.Margin, s.cfg.EmbedRouterMargin, truncForLog(userMessage))
					return IntentUnknown
				}
				return res.Label
			}
			log.Printf("[embed-router] classify error: %v (falling back to LLM router)", err)
		}
	}
	// Fallback: legacy LLM router.
	if s.router != nil && s.routerMgr != nil && s.routerMgr.Ready() {
		ctx2, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		out, err := s.router.Classify(ctx2, RouterSystemPrompt, userMessage, RouterMaxTokens)
		if err != nil {
			log.Printf("[router] classify error: %v", err)
			return IntentUnknown
		}
		up := strings.ToUpper(strings.TrimSpace(out))
		switch {
		case strings.Contains(up, "NEEDS_T"):
			return IntentTools
		case strings.Contains(up, "NEEDS_R"):
			return IntentRAG
		case strings.Contains(up, "CONV"):
			return IntentConversational
		}
	}
	return IntentUnknown
}

// ensureEmbedCorpus initialises the embedding router's corpus on first use.
// Runs once per server lifetime; subsequent calls are O(1). Holds a brief
// timeout so a wedged sidecar doesn't block requests forever.
func (s *Server) ensureEmbedCorpus(parent context.Context) bool {
	s.embedReadyMu.Lock()
	defer s.embedReadyMu.Unlock()
	if s.embedReady {
		return true
	}
	if s.embedRouter == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if err := s.embedRouter.Init(ctx, embedrouter.DefaultCorpus); err != nil {
		log.Printf("[embed-router] corpus init failed: %v", err)
		return false
	}
	s.embedReady = true
	log.Printf("[embed-router] corpus ready (%d utterances)", len(embedrouter.DefaultCorpus))
	return true
}

func truncForLog(s string) string {
	const n = 60
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RouterMaxTokens caps the router completion length. The 3 valid labels
// fully disambiguate within 4 tokens (NEEDS_TOOLS / NEEDS_RAG / CONVERS…);
// going below 4 collapses TOOLS+RAG into a "NEEDS" stub and breaks the
// classifier. Going above 4 just spends extra ms generating noise.
//
// Callers must treat the response as a *prefix*: e.g. accept "NEEDS_T" as
// NEEDS_TOOLS. cmd/routereval has the canonical normalize() helper.
const RouterMaxTokens = 4

// RouterSystemPrompt is the canonical system prompt for the local router.
// Tuned on testdata/router_eval.json against Qwen3-1.7B-4bit with
// max_tokens=4, temperature=0, seed=42, chat_template_kwargs.enable_thinking=false:
// 100% accuracy at ~101ms avg latency. Without these examples the same
// model collapses to ~25% (defaults NEEDS_TOOLS for everything). Any
// caller of providerForTask("router") MUST send this as the system message.
const RouterSystemPrompt = `You are a router. Reply with exactly one of: NEEDS_TOOLS, NEEDS_RAG, CONVERSATIONAL. No prose, no other text.

Definitions:
- NEEDS_TOOLS: structured action or lookup (case status, deadlines, scheduling, sending email, marking records).
- NEEDS_RAG: synthesize an answer from the user's documents (summaries, clause lookup, comparisons, evidence).
- CONVERSATIONAL: greetings, thanks, capability questions, smalltalk — no data needed.

Examples:
User: What's the deadline for case 8421?
Assistant: NEEDS_TOOLS
User: Summarize the indemnification clauses in this contract.
Assistant: NEEDS_RAG
User: What does the loan agreement say about prepayment penalties?
Assistant: NEEDS_RAG
User: Email the signed NDA to alice@firm.com
Assistant: NEEDS_TOOLS
User: Hi, how are you?
Assistant: CONVERSATIONAL
User: What can you help me with?
Assistant: CONVERSATIONAL
User: 案件123的状态是什么？
Assistant: NEEDS_TOOLS
User: 总结这份合同的保密条款。
Assistant: NEEDS_RAG
User: 你好
Assistant: CONVERSATIONAL`

// handleIntakeFolder ingests a whole folder upload and runs the multi-LLM
// pipeline to suggest case metadata. Does NOT create the case yet — the UI
// shows the result for the user to confirm/edit, then calls
// /v1/intake/folder/commit to materialise the case.
//
// Multipart fields:
//   folder_name : user-supplied folder name (required, used as a hint)
//   files       : repeated; per-file part name "files"
//   relpaths    : repeated string parts, one per file, in the same order;
//                 carry the relative path of each file inside the folder
//                 (so subfolders like "起诉立案/起诉状.pdf" survive the upload).
func (s *Server) handleIntakeFolder(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	// 200 MB cap — a typical Chinese legal folder with WeChat screenshots
	// + a few PDFs sits comfortably under this. Adjust if needed.
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	folderName := strings.TrimSpace(r.FormValue("folder_name"))
	if folderName == "" {
		folderName = "Unnamed folder"
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		s.writeError(w, http.StatusBadRequest, "no files in upload")
		return
	}
	relpaths := r.MultipartForm.Value["relpaths"]

	// Stage all files under data/staging/<id>/
	stagingID := fmt.Sprintf("intake-%d", time.Now().UnixNano())
	stagingDir := filepath.Join(s.cfg.DataDir, "staging", stagingID)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to create staging dir")
		return
	}

	// 1. Save each upload to staging, recording original + relative paths.
	staged := make([]FolderIntakeFileResult, 0, len(files))
	for i, fh := range files {
		rel := ""
		if i < len(relpaths) {
			rel = strings.TrimSpace(relpaths[i])
		}
		if rel == "" {
			rel = fh.Filename
		}
		// Sanitise relpath: strip absolute, prevent traversal.
		rel = filepath.Clean(strings.TrimPrefix(rel, "/"))
		if strings.Contains(rel, "..") {
			rel = filepath.Base(rel)
		}

		dstPath := filepath.Join(stagingDir, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			log.Printf("[intake] mkdir %s: %v", filepath.Dir(dstPath), err)
			continue
		}

		if err := saveMultipart(fh, dstPath); err != nil {
			log.Printf("[intake] save %s: %v", dstPath, err)
			continue
		}
		staged = append(staged, FolderIntakeFileResult{
			StagedPath:   dstPath,
			OriginalName: fh.Filename,
			RelativePath: rel,
			SizeBytes:    fh.Size,
		})
	}

	if len(staged) == 0 {
		s.writeError(w, http.StatusInternalServerError, "all files failed to stage")
		return
	}

	// 2. Extract text from each in parallel (OCR for images, text fast-path
	//    for searchable PDFs, XML parse for DOCX). Bounded concurrency so we
	//    don't overwhelm the OCR provider.
	docs := extractTextParallel(s, staged, nil)

	// 3. Multi-LLM synthesis call — uses the COMPLEX model for accuracy.
	//    The folder name is passed as a hint so e.g. "10.15 罗海霞" yields
	//    a confident client_name even before any document text is read.
	synthModel := s.modelForTask("synth")
	analysis := llmutil.AnalyzeBatchFromOCRWithModel(s.llm, synthModel, folderName, docs)

	// Merge the LLM file classifications back into the staged file results.
	classByName := make(map[string]int, len(analysis.Files))
	for i, f := range analysis.Files {
		classByName[f.Filename] = i
	}
	for i := range staged {
		if idx, ok := classByName[staged[i].OriginalName]; ok {
			f := analysis.Files[idx]
			staged[i].DocumentType = f.DocumentType
			staged[i].DisplayNameZH = f.DisplayNameZH
			staged[i].SummaryZH = f.SummaryZH
		}
		if text, hadText := docs[staged[i].OriginalName]; !hadText || text == "" {
			staged[i].OCRError = "no extractable text"
		}
	}

	// Stable order for predictable rendering.
	sort.Slice(staged, func(i, j int) bool { return staged[i].RelativePath < staged[j].RelativePath })

	out := FolderIntakeResult{
		StagingID:     stagingID,
		FolderName:    folderName,
		ClientName:    analysis.ClientName,
		MatterType:    analysis.MatterType,
		Plaintiffs:    analysis.Plaintiffs,
		Defendants:    analysis.Defendants,
		Files:         staged,
		ModelUsed:     synthModel,
		ElapsedMillis: time.Since(started).Milliseconds(),
		LLMError:      analysis.LLMError,
	}
	s.writeJSON(w, http.StatusOK, out)
}

// handleIntakeCommit materialises a staged intake into a real Case, copying
// the staged files into the case's docs directory so the user can edit/delete
// metadata in the confirmation sheet without duplicating the upload.
//
// Body JSON:
//   { "staging_id": "...", "client_name": "...", "matter_type": "...",
//     "initial_msg": "...", "source_channel": "Folder upload" }
func (s *Server) handleIntakeCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		StagingID     string `json:"staging_id"`
		ClientName    string `json:"client_name"`
		MatterType    string `json:"matter_type"`
		InitialMsg    string `json:"initial_msg"`
		SourceChannel string `json:"source_channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.StagingID == "" {
		s.writeError(w, http.StatusBadRequest, "staging_id required")
		return
	}

	stagingDir := filepath.Join(s.cfg.DataDir, "staging", req.StagingID)
	if _, err := s.allowedDirectoryUnderDataDir(stagingDir); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid staging id")
		return
	}
	if _, err := os.Stat(stagingDir); err != nil {
		s.writeError(w, http.StatusNotFound, "staging dir not found")
		return
	}

	clientName := strings.TrimSpace(req.ClientName)
	if clientName == "" {
		clientName = "Unknown Client"
	}
	matterType := strings.TrimSpace(req.MatterType)
	if matterType == "" {
		matterType = "Civil Litigation"
	}
	source := req.SourceChannel
	if source == "" {
		source = "Folder upload"
	}

	c := s.workflow.CreateCase(clientName, matterType, source, req.InitialMsg)

	// Copy every staged file into the case's docs directory.
	docsDir := filepath.Join(s.cfg.DataDir, "docs", c.CaseID)
	if err := os.MkdirAll(docsDir, 0755); err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to create docs dir")
		return
	}

	var moved []string
	_ = filepath.WalkDir(stagingDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(stagingDir, p)
		dst := filepath.Join(docsDir, filepath.Base(rel))
		if err := copyFile(p, dst); err != nil {
			log.Printf("[intake-commit] copy %s -> %s: %v", p, dst, err)
			return nil
		}
		s.workflow.AttachDocument(c.CaseID, filepath.Base(rel))
		moved = append(moved, filepath.Base(rel))
		return nil
	})

	// Best-effort cleanup of staging.
	_ = os.RemoveAll(stagingDir)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"case_id":         c.CaseID,
		"attached":        moved,
		"attached_count":  len(moved),
	})
}

// extractTextParallel walks the staged files and runs the OCR engine's
// universal `ScanFile` on each. Vision-OCR is network-bound, so we crank
// the worker pool to 12 — wall-clock drops roughly linearly until the
// upstream rate-limits.
//
// Each file goes through the content-addressed OCR cache first: re-running
// intake on the same folder pays zero OCR cost on identical bytes.
//
// `onDone` (if non-nil) fires once per file the moment its text is in hand,
// regardless of cache or fresh-OCR, so streaming endpoints can emit progress.
// It runs on the worker goroutine — must be cheap and non-blocking.
func extractTextParallel(s *Server, staged []FolderIntakeFileResult, onDone func(name string, cached bool, ok bool)) map[string]string {
	type kv struct {
		k, v string
	}
	const maxWorkers = 12
	sem := make(chan struct{}, maxWorkers)
	resCh := make(chan kv, len(staged))

	var wg sync.WaitGroup
	for _, f := range staged {
		wg.Add(1)
		sem <- struct{}{}
		go func(f FolderIntakeFileResult) {
			defer wg.Done()
			defer func() { <-sem }()

			// 1. Cache lookup by sha256(file).
			if s.ocrCache != nil {
				if text, ok := s.ocrCache.Get(f.StagedPath); ok {
					if onDone != nil {
						onDone(f.OriginalName, true, true)
					}
					resCh <- kv{f.OriginalName, text}
					return
				}
			}

			// 2. Real OCR.
			text, err := s.ocr.ScanFile(f.StagedPath)
			if err != nil {
				log.Printf("[intake] ocr %s: %v", f.OriginalName, err)
				if onDone != nil {
					onDone(f.OriginalName, false, false)
				}
				resCh <- kv{f.OriginalName, ""}
				return
			}

			// 3. Cache the result for next time.
			if s.ocrCache != nil && text != "" {
				s.ocrCache.Set(f.StagedPath, text)
			}
			if onDone != nil {
				onDone(f.OriginalName, false, true)
			}
			resCh <- kv{f.OriginalName, text}
		}(f)
	}
	go func() { wg.Wait(); close(resCh) }()

	out := make(map[string]string, len(staged))
	for r := range resCh {
		out[r.k] = r.v
	}
	return out
}

// QuickIntakeRequest — body for POST /v1/intake/folder/quick.
type QuickIntakeRequest struct {
	FolderName string   `json:"folder_name"`
	Filenames  []string `json:"filenames"`
}

// QuickIntakeResponse — fast-path metadata returned in ~1–2s.
type QuickIntakeResponse struct {
	ClientName    string   `json:"client_name"`
	MatterType    string   `json:"matter_type"`
	Plaintiffs    []string `json:"plaintiffs"`
	Defendants    []string `json:"defendants"`
	ModelUsed     string   `json:"model_used"`
	ElapsedMillis int64    `json:"elapsed_ms"`
	LLMError      string   `json:"llm_error,omitempty"`
}

// handleIntakeFolderStream is the streaming variant of handleIntakeFolder.
// Same multipart input shape — but instead of one big response at the end,
// the server emits SSE events as the pipeline progresses, so the UI can
// render a real determinate progress bar.
//
// Event shapes:
//   data: {"phase": "staged",      "total": 22}
//   data: {"phase": "extract",     "completed": 7, "total": 22, "current_file": "...", "cached": false}
//   data: {"phase": "synthesizing"}
//   data: {"phase": "done",        "result": {...IntakeResult...}}
//   data: {"phase": "error",       "error": "..."}
func (s *Server) handleIntakeFolderStream(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Generous cap — folders with many WeChat screenshots are common.
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	folderName := strings.TrimSpace(r.FormValue("folder_name"))
	if folderName == "" {
		folderName = "Unnamed folder"
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		s.writeError(w, http.StatusBadRequest, "no files in upload")
		return
	}
	relpaths := r.MultipartForm.Value["relpaths"]

	// SSE headers BEFORE first write — once we start streaming, errors must
	// also be streamed (any further w.WriteHeader call is ignored).
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// All emissions go through `send`. Write+flush is mutex-protected because
	// the OCR progress callback fires from worker goroutines.
	var sendMu sync.Mutex
	send := func(obj map[string]interface{}) {
		sendMu.Lock()
		defer sendMu.Unlock()
		b, _ := json.Marshal(obj)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	bail := func(reason string) {
		send(map[string]interface{}{"phase": "error", "error": reason})
	}

	// 1. Stage every uploaded part to disk.
	stagingID := fmt.Sprintf("intake-%d", time.Now().UnixNano())
	stagingDir := filepath.Join(s.cfg.DataDir, "staging", stagingID)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		bail("failed to create staging dir")
		return
	}
	staged := make([]FolderIntakeFileResult, 0, len(files))
	for i, fh := range files {
		rel := ""
		if i < len(relpaths) {
			rel = strings.TrimSpace(relpaths[i])
		}
		if rel == "" {
			rel = fh.Filename
		}
		rel = filepath.Clean(strings.TrimPrefix(rel, "/"))
		if strings.Contains(rel, "..") {
			rel = filepath.Base(rel)
		}
		dstPath := filepath.Join(stagingDir, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			log.Printf("[intake-stream] mkdir %s: %v", filepath.Dir(dstPath), err)
			continue
		}
		if err := saveMultipart(fh, dstPath); err != nil {
			log.Printf("[intake-stream] save %s: %v", dstPath, err)
			continue
		}
		staged = append(staged, FolderIntakeFileResult{
			StagedPath:   dstPath,
			OriginalName: fh.Filename,
			RelativePath: rel,
			SizeBytes:    fh.Size,
		})
	}
	if len(staged) == 0 {
		bail("all files failed to stage")
		return
	}
	send(map[string]interface{}{"phase": "staged", "total": len(staged)})

	// 2. Parallel OCR — emit progress as each file completes.
	var completed atomic.Int64
	total := len(staged)
	docs := extractTextParallel(s, staged, func(name string, cached bool, ok bool) {
		n := completed.Add(1)
		send(map[string]interface{}{
			"phase":        "extract",
			"completed":    n,
			"total":        total,
			"current_file": name,
			"cached":       cached,
			"ok":           ok,
		})
	})

	// 3. Synthesis — single LLM round trip, no internal progress.
	send(map[string]interface{}{"phase": "synthesizing"})
	synthModel := s.modelForTask("synth")
	analysis := llmutil.AnalyzeBatchFromOCRWithModel(s.llm, synthModel, folderName, docs)

	// Merge classifications back into staged entries.
	classByName := make(map[string]int, len(analysis.Files))
	for i, f := range analysis.Files {
		classByName[f.Filename] = i
	}
	for i := range staged {
		if idx, ok := classByName[staged[i].OriginalName]; ok {
			f := analysis.Files[idx]
			staged[i].DocumentType = f.DocumentType
			staged[i].DisplayNameZH = f.DisplayNameZH
			staged[i].SummaryZH = f.SummaryZH
		}
		if text, hadText := docs[staged[i].OriginalName]; !hadText || text == "" {
			staged[i].OCRError = "no extractable text"
		}
	}
	sort.Slice(staged, func(i, j int) bool { return staged[i].RelativePath < staged[j].RelativePath })

	result := FolderIntakeResult{
		StagingID:     stagingID,
		FolderName:    folderName,
		ClientName:    analysis.ClientName,
		MatterType:    analysis.MatterType,
		Plaintiffs:    analysis.Plaintiffs,
		Defendants:    analysis.Defendants,
		Files:         staged,
		ModelUsed:     synthModel,
		ElapsedMillis: time.Since(started).Milliseconds(),
		LLMError:      analysis.LLMError,
	}
	send(map[string]interface{}{"phase": "done", "result": result})
}

// handleIntakeQuick is the FAST PATH: filename-only metadata extraction.
// No file uploads, no OCR — just folder name + filename list → fast model.
// Lets the UI populate the review form immediately while the full intake
// pipeline runs in parallel against /v1/intake/folder.
func (s *Server) handleIntakeQuick(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req QuickIntakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Filenames) == 0 {
		s.writeError(w, http.StatusBadRequest, "filenames required")
		return
	}

	// Use the MEDIUM (fast) model. Falls back to default if unset.
	model := s.modelForTask("intake")
	res := llmutil.QuickIntakeFromFilenames(s.llm, model, req.FolderName, req.Filenames)

	out := QuickIntakeResponse{
		ClientName:    res.ClientName,
		MatterType:    res.MatterType,
		Plaintiffs:    res.Plaintiffs,
		Defendants:    res.Defendants,
		ModelUsed:     model,
		ElapsedMillis: time.Since(started).Milliseconds(),
		LLMError:      res.LLMError,
	}
	s.writeJSON(w, http.StatusOK, out)
}

// saveMultipart streams a multipart file part to disk.
func saveMultipart(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
