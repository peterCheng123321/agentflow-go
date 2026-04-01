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
	dataDir := getEnv("AGENTFLOW_DATA_DIR", "./data")
	dashKey := resolveDashScopeAPIKey(dataDir)
	log.Printf("[config] DashScope Key found: %v (len=%d)", dashKey != "", len(dashKey))
	dashBase := strings.TrimSuffix(getEnv("AGENTFLOW_DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), "/")

	// Determine backend early to set correct concurrency defaults
	llmBackend := "dashscope" 

	maxConcurrent := 8
	if mc := os.Getenv("AGENTFLOW_MAX_CONCURRENT"); mc != "" {
		if v, err := strconv.Atoi(mc); err == nil {
			maxConcurrent = v
		}
	} else if llmBackend == "mlx" {
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

	if modelName == "" {
		modelName = "qwen-max" 
	}
	if ocrModelID == "" {
		ocrModelID = "qwen-vl-plus"
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
