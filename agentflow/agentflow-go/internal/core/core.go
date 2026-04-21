package core

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agentflow-go/internal/agent"
	"agentflow-go/internal/config"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
	"agentflow-go/internal/model"
	"agentflow-go/internal/ocr"
	"agentflow-go/internal/processor"
	"agentflow-go/internal/rag"
	"agentflow-go/internal/tools"
	"agentflow-go/internal/worker"
	"agentflow-go/internal/workflow"

	"github.com/google/uuid"
)

// App owns all core services and exposes high-level operations for the native UI.
// It has no HTTP dependencies.
type App struct {
	Cfg      *config.Config
	LLM      *llm.Provider
	OCR      *ocr.Engine
	Agent    *agent.Executor // pre-wired agent executor with all built-in tools
	RAG      *rag.Manager
	Workflow *workflow.Engine
	Proc     *processor.BatchProcessor

	workerPool *worker.Pool

	jobsMu sync.RWMutex
	jobs   map[string]*model.Job

	mu        sync.RWMutex
	listeners []func()
}

func New(cfg *config.Config) *App {
	if cfg.LLMBackend == "dashscope" && cfg.DashScopeAPIKey == "" {
		log.Fatal("LLM backend is dashscope but no API key: set AGENTFLOW_DASHSCOPE_API_KEY or DASHSCOPE_API_KEY")
	}

	a := &App{
		Cfg:  cfg,
		jobs: make(map[string]*model.Job),
	}

	llmOpts := []llm.Option{}
	if cfg.LLMCacheEnabled && cfg.LLMCacheDir != "" {
		llmOpts = append(llmOpts, llm.WithResponseCache(cfg.LLMCacheDir, true))
	}
	switch cfg.LLMBackend {
	case "dashscope":
		a.LLM = llm.NewProvider(cfg.ModelName, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, llm.BackendOpenAICompat, llmOpts...)
		a.OCR = ocr.NewEngine(cfg.OCRModelID, cfg.DashScopeBaseURL, cfg.DashScopeAPIKey, ocr.BackendOpenAICompat, cfg.MaxConcurrent, 10*time.Minute)
	default:
		a.LLM = llm.NewProvider(cfg.ModelName, cfg.OllamaURL, "", llm.BackendOllama, llmOpts...)
		a.OCR = ocr.NewEngine(cfg.OCRModelID, cfg.OllamaURL, "", ocr.BackendOllama, cfg.MaxConcurrent, 5*time.Minute)
	}

	os.MkdirAll(filepath.Join(cfg.DataDir, "vector_store"), 0755)
	a.RAG = rag.NewManager(filepath.Join(cfg.DataDir, "vector_store"))

	maxCases := cfg.MaxCases
	if maxCases < 1 {
		maxCases = 200
	}
	a.Workflow = workflow.NewEngine(maxCases, cfg.DataDir, func() {
		a.notify()
	})

	a.Proc = processor.NewBatchProcessor(a.OCR, a, a, a.RAG, a.Workflow, a, cfg.MaxConcurrent)

	workers := cfg.MaxConcurrent
	if workers < 1 {
		workers = 1
	}
	wp := worker.New(workers)
	wp.SetJobUpdater(func(id string, upd func(*model.Job)) {
		a.updateJob(id, upd)
	})
	wp.SetAfterTerminal(func(j *model.Job) {
		a.notify()
		jid := j.ID
		go func() {
			time.Sleep(5 * time.Second)
			a.jobsMu.Lock()
			delete(a.jobs, jid)
			a.jobsMu.Unlock()
			a.notify()
		}()
	})
	a.workerPool = wp

	// Build the agent tool registry and executor
	reg := agent.NewRegistry()
	reg.Register(tools.NewRAGSearch(a.RAG))
	reg.Register(tools.NewEntityExtraction(a.LLM, cfg.ModelMedium))
	reg.Register(tools.NewDeadlineCalc())
	reg.Register(tools.NewCaseSummary(a.RAG, a.LLM, cfg.ModelComplex))
	reg.Register(tools.NewClassifyDoc(a.LLM))
	a.Agent = agent.NewExecutor(reg, a.LLM, agent.Config{
		MaxSteps:         10,
		Model:            cfg.ModelComplex,
		MaxTokensPerStep: 1024,
	})

	return a
}

// AgentRun submits an agent task as a background job and returns the job ID.
// The job result will contain the agent.RunResult when complete.
func (a *App) AgentRun(goal string, caseID string) string {
	return a.SubmitJob("agent", func(j *model.Job) (any, error) {
		result := a.Agent.Run(context.Background(), goal)
		// Persist final answer as an AI case summary if a case ID was provided
		if caseID != "" && result.Answer != "" {
			_ = a.Workflow.SetAICaseSummary(caseID, result.Answer)
		}
		return result, nil
	})
}

// Subscribe registers a callback that fires whenever any state changes.
func (a *App) Subscribe(fn func()) {
	a.mu.Lock()
	a.listeners = append(a.listeners, fn)
	a.mu.Unlock()
}

func (a *App) notify() {
	a.mu.RLock()
	listeners := make([]func(), len(a.listeners))
	copy(listeners, a.listeners)
	a.mu.RUnlock()
	for _, fn := range listeners {
		fn()
	}
}

// SubmitJob enqueues a background job and returns its ID.
func (a *App) SubmitJob(jobType string, fn func(*model.Job) (any, error)) string {
	id := uuid.New().String()
	job := &model.Job{
		ID:        id,
		Type:      model.JobType(jobType),
		Status:    model.JobStatusPending,
		Progress:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	a.jobsMu.Lock()
	a.jobs[id] = job
	a.jobsMu.Unlock()
	a.notify()

	if err := a.workerPool.Enqueue(context.Background(), job, 0, fn); err != nil {
		a.updateJob(id, func(j *model.Job) {
			j.Status = model.JobStatusFailed
			j.Error = fmt.Sprintf("enqueue: %v", err)
		})
	}
	return id
}

func (a *App) updateJob(id string, upd func(*model.Job)) {
	a.jobsMu.Lock()
	if j, ok := a.jobs[id]; ok {
		upd(j)
		j.UpdatedAt = time.Now()
	}
	a.jobsMu.Unlock()
}

// GetJobs returns a snapshot of all active jobs.
func (a *App) GetJobs() []model.Job {
	a.jobsMu.RLock()
	defer a.jobsMu.RUnlock()
	out := make([]model.Job, 0, len(a.jobs))
	for _, j := range a.jobs {
		out = append(out, *j)
	}
	return out
}

// Shutdown cleanly stops all services.
func (a *App) Shutdown() {
	a.workerPool.Shutdown()
	a.LLM.Unload()
	a.OCR.Unload()
	a.Workflow.Close()
}

// --- processor.Classifier interface ---

func (a *App) Classify(ctx context.Context, text, filename string) (map[string]interface{}, error) {
	res := llmutil.ClassifyDocument(a.LLM, text, filename)
	if res == nil {
		return nil, fmt.Errorf("classification returned nil")
	}
	return res, nil
}

// --- processor.BatchAnalyzer interface ---

func (a *App) AnalyzeBatch(ctx context.Context, docs map[string]string) (processor.BatchMeta, error) {
	raw := llmutil.AnalyzeBatchFromOCR(a.LLM, docs)
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

// --- processor.JobUpdater interface ---

func (a *App) UpdateJob(id string, upd func(*model.Job)) {
	a.updateJob(id, upd)
}

// ResolveCaseForDocument finds or creates a case for an uploaded document.
func (a *App) ResolveCaseForDocument(clientName, matterType, logicalName string) string {
	if clientName == "" || clientName == "Unknown Client" {
		clientName = "Unknown Client"
	}
	for _, c := range a.Workflow.ListCases() {
		if c.ClientName == clientName {
			return c.CaseID
		}
	}
	c := a.Workflow.CreateCase(clientName, matterType, "Upload", "")
	return c.CaseID
}

// InferIntakeFromOCR extracts client/matter info from OCR text.
func (a *App) InferIntakeFromOCR(text, logicalName string) (clientName, matterType, source string) {
	return llmutil.InferIntakeFromOCR(a.LLM, text, logicalName)
}

// Search queries the RAG index.
func (a *App) Search(query string, topK int) []model.SearchResult {
	return a.RAG.Search(query, topK)
}

// WorkerStats returns worker pool stats.
func (a *App) WorkerStats() map[string]interface{} {
	return a.workerPool.Stats()
}
