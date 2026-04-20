package config

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type Config struct {
	Port              int
	ModelName         string
	MaxCases          int
	DataDir           string
	MaxConcurrent     int
	OCRModelID        string
	MaxMemoryMB       int
	OllamaURL         string
	IsAppleSilicon    bool
	LLMBackend        string // dashscope | mlx | ollama
	DashScopeBaseURL  string
	DashScopeAPIKey   string // never log; pass only to LLM provider
	LLMCacheEnabled   bool
	LLMCacheDir       string
	// Model routing for different task types
	ModelOCR          string // Vision model for OCR (default: qwen-vl-ocr-latest)
	ModelComplex      string // High-end model for complex reasoning (default: qwen-plus)
	ModelMedium       string // Mid-tier model for summaries, classification (default: qwen-turbo)
}

func Load() *Config {
	port := 8000
	if p := os.Getenv("AGENTFLOW_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	maxCases := 200
	if mc := os.Getenv("AGENTFLOW_MAX_CASES"); mc != "" {
		if v, err := strconv.Atoi(mc); err == nil {
			maxCases = v
		}
	}

	isAS := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"

	// Default to proper macOS data directory if running as app, otherwise ./data
	defaultDataDir := "./data"
	if runtime.GOOS == "darwin" {
		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			defaultDataDir = filepath.Join(homeDir, "Library", "Application Support", "AgentFlow")
		}
	}
	dataDir := getEnv("AGENTFLOW_DATA_DIR", defaultDataDir)
	dashKey := resolveDashScopeAPIKey(dataDir)
	log.Printf("[config] DashScope Key found: %v (len=%d)", dashKey != "", len(dashKey))
	dashBase := strings.TrimSuffix(getEnv("AGENTFLOW_DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), "/")

	// AGENTFLOW_LLM_BACKEND: dashscope | ollama | mlx (default dashscope)
	llmBackend := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTFLOW_LLM_BACKEND")))
	switch llmBackend {
	case "dashscope", "ollama", "mlx":
	default:
		if llmBackend != "" {
			log.Printf("[config] unknown AGENTFLOW_LLM_BACKEND %q, using dashscope", os.Getenv("AGENTFLOW_LLM_BACKEND"))
		}
		llmBackend = "dashscope"
	}

	// Calculate MaxConcurrent based on CPU cores for optimal M-Series performance
	// Scale slightly lower than NumCPU to prevent GC thrashing and context switching
	maxConcurrent := 8
	if mc := os.Getenv("AGENTFLOW_MAX_CONCURRENT"); mc != "" {
		if v, err := strconv.Atoi(mc); err == nil {
			maxConcurrent = v
		}
	} else {
		// Auto-tune based on available CPU cores (Apple Silicon optimization)
		numCores := runtime.NumCPU()
		// Use 75% of available cores, minimum 4, maximum 32
		autoTuned := numCores * 3 / 4
		if autoTuned < 4 {
			autoTuned = 4
		} else if autoTuned > 32 {
			autoTuned = 32
		}
		maxConcurrent = autoTuned
		log.Printf("[config] Auto-tuned MaxConcurrent to %d based on %d CPU cores", maxConcurrent, numCores)
	}

	// MLX backend runs single-threaded
	if llmBackend == "mlx" && maxConcurrent > 1 {
		maxConcurrent = 1
	}

	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	
	maxMem := detectPhysicalMemory()
	if maxMem == 0 {
		maxMem = 4096 
	}

	modelName := strings.TrimSpace(os.Getenv("AGENTFLOW_MODEL"))
	ocrModelID := strings.TrimSpace(os.Getenv("AGENTFLOW_OCR_MODEL"))

	// Model routing defaults - user specified models
	modelOCR := strings.TrimSpace(os.Getenv("AGENTFLOW_MODEL_OCR"))
	if modelOCR == "" {
		modelOCR = "qwen-vl-ocr-latest" // OCR model
	}

	modelComplex := strings.TrimSpace(os.Getenv("AGENTFLOW_MODEL_COMPLEX"))
	if modelComplex == "" {
		modelComplex = "qwen-plus" // Complex tasks (qwen3.6-plus equivalent)
	}

	modelMedium := strings.TrimSpace(os.Getenv("AGENTFLOW_MODEL_MEDIUM"))
	if modelMedium == "" {
		modelMedium = "qwen-plus" // Medium/summary tasks (qwen3.5-27b equivalent)
	}

	if modelName == "" {
		modelName = "qwen-plus" // Default
	}
	if ocrModelID == "" {
		ocrModelID = modelOCR // Use OCR model if not explicitly set
	}

	llmCacheEnabled := true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENTFLOW_LLM_CACHE"))) {
	case "0", "false", "off", "no":
		llmCacheEnabled = false
	}
	llmCacheDir := filepath.Join(dataDir, "llm_cache")
	if v := strings.TrimSpace(os.Getenv("AGENTFLOW_LLM_CACHE_DIR")); v != "" {
		llmCacheDir = v
	}

	return &Config{
		Port:             port,
		ModelName:        modelName,
		MaxCases:         maxCases,
		DataDir:          dataDir,
		MaxConcurrent:    maxConcurrent,
		OCRModelID:       ocrModelID,
		MaxMemoryMB:      maxMem,
		OllamaURL:        getEnv("OLLAMA_URL", "http://localhost:11434"),
		IsAppleSilicon:   isAS,
		LLMBackend:       llmBackend,
		DashScopeBaseURL: dashBase,
		DashScopeAPIKey:  dashKey,
		LLMCacheEnabled:  llmCacheEnabled,
		LLMCacheDir:      llmCacheDir,
		ModelOCR:         modelOCR,
		ModelComplex:     modelComplex,
		ModelMedium:      modelMedium,
	}
}

func resolveDashScopeAPIKey(dataDir string) string {
	if k := os.Getenv("AGENTFLOW_DASHSCOPE_API_KEY"); k != "" {
		return k
	}
	if k := os.Getenv("DASHSCOPE_API_KEY"); k != "" {
		return k
	}
	// Check file
	file := os.Getenv("AGENTFLOW_DASHSCOPE_API_KEY_FILE")
	if file == "" {
		file = filepath.Join(dataDir, "secrets", "dashscope_api_key.txt")
	}
	b, err := os.ReadFile(file)
	if err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func detectPhysicalMemory() int {
	if runtime.GOOS != "darwin" {
		return 0
	}
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return int(size / 1024 / 1024)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
