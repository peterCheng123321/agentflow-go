package ocr

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Backend string

const (
	BackendOllama       Backend = "ollama"
	BackendOpenAICompat Backend = "openai_compat"
)

type Engine struct {
	modelID        string
	baseURL        string
	apiKey         string
	backend        Backend
	client         *http.Client
	mu             sync.Mutex
	isReady        bool
	lastUsed       time.Time
	ttl            time.Duration
	semaphore      chan struct{}
	reqCount       atomic.Int64
	errCount       atomic.Int64
	maxRetries     int
	isAppleSilicon bool
}

func NewEngine(modelID, baseURL, apiKey string, backend Backend, maxConcurrent int, ttl time.Duration) *Engine {
	isAS := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	
	e := &Engine{
		modelID:        modelID,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		apiKey:         apiKey,
		backend:        backend,
		ttl:            ttl,
		maxRetries:     2,
		semaphore:      make(chan struct{}, maxConcurrent),
		isAppleSilicon: isAS,
		client: &http.Client{
			Timeout: 300 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        5,
				MaxIdleConnsPerHost: 3,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	
	// Test connection on startup
	go func() {
		if err := e.checkReady(); err != nil {
			log.Printf("[OCR] Model server not ready: %v (will retry on first request)", err)
		} else {
			e.isReady = true
			log.Printf("[OCR] Connected to model server at %s", baseURL)
		}
	}()
	
	return e
}

func (e *Engine) checkReady() error {
	switch e.backend {
	case BackendOpenAICompat:
		if e.apiKey == "" {
			return fmt.Errorf("missing API key for Cloud OCR")
		}
		return nil
	default: // Ollama
		req, _ := http.NewRequest("GET", e.baseURL+"/api/tags", nil)
		resp, err := e.client.Do(req)
		if err != nil {
			return fmt.Errorf("Ollama: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("Ollama returned %d", resp.StatusCode)
		}
		return nil
	}
}

func (e *Engine) ScanFile(filePath string) (string, error) {
	// Check TTL and unload if idle
	e.checkTTL()
	
	// Acquire semaphore for concurrency control
	e.semaphore <- struct{}{}
	defer func() { <-e.semaphore }()
	
	ext := strings.ToLower(filepath.Ext(filePath))
	
	// Handle text files directly
	if ext == ".txt" || ext == ".md" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read text file: %w", err)
		}
		e.reqCount.Add(1)
		e.mu.Lock()
		e.lastUsed = time.Now()
		e.mu.Unlock()
		return string(data), nil
	}
	
	// Handle DOCX files
	if ext == ".docx" {
		e.reqCount.Add(1)
		e.mu.Lock()
		e.lastUsed = time.Now()
		e.mu.Unlock()
		return e.extractDOCX(filePath)
	}

	// Fast-path for searchable PDFs
	if ext == ".pdf" {
		text, err := e.extractPDFText(filePath)
		if err == nil && len(strings.TrimSpace(text)) > 10 {
			e.reqCount.Add(1)
			e.mu.Lock()
			e.lastUsed = time.Now()
			e.mu.Unlock()
			return text, nil
		}
		log.Printf("[OCR] PDF text extraction failed or empty, falling back to vision: %v", err)
	}
	
	// For images and PDFs, send to OCR model server
	e.mu.Lock()
	e.lastUsed = time.Now()
	e.mu.Unlock()
	
	return e.scanWithModel(filePath)
}

func isRasterImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".heic", ".heif":
		return true
	default:
		return false
	}
}

// tryTesseractRaster uses local tesseract when mlx-vlm returns nothing (common for ID cards / dense text).
func (e *Engine) tryTesseractRaster(filePath string) (string, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", fmt.Errorf("tesseract not in PATH (brew install tesseract tesseract-lang)")
	}

	workPath := filePath
	var cleanup func()
	cleanup = func() {}
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".heic" || ext == ".heif" {
		if runtime.GOOS != "darwin" {
			return "", fmt.Errorf("HEIC not supported without macOS sips; convert to JPEG first")
		}
		tmp, err := os.CreateTemp("", "agentflow-heic-*.jpg")
		if err != nil {
			return "", err
		}
		tmpName := tmp.Name()
		_ = tmp.Close()
		out, err := exec.Command("sips", "-s", "format", "jpeg", filePath, "--out", tmpName).CombinedOutput()
		if err != nil {
			_ = os.Remove(tmpName)
			return "", fmt.Errorf("sips HEIC→JPEG: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		workPath = tmpName
		cleanup = func() { _ = os.Remove(tmpName) }
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var lastErr error
	for _, lang := range []string{"chi_sim+eng", "eng"} {
		cmd := exec.CommandContext(ctx, "tesseract", workPath, "stdout", "-l", lang)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("tesseract -l %s: %w — %s", lang, err, strings.TrimSpace(stderr.String()))
			continue
		}
		s := strings.TrimSpace(stdout.String())
		if len(s) >= 4 {
			return s, nil
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("tesseract produced no usable text")
}

func (e *Engine) scanWithModel(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Cloud API preference
	if e.backend == BackendOpenAICompat {
		return e.scanWithOpenAI(filePath)
	}

	if !e.isReady {
		if err := e.checkReady(); err != nil {
			return "", err
		}
		e.isReady = true
	}
	
	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	
	e.reqCount.Add(1)
	
	var lastErr error
	for attempt := 0; attempt < e.maxRetries; attempt++ {
		result, err := e.doOCR(data, filepath.Base(filePath))
		if err == nil {
			return result, nil
		}
		
		lastErr = err
		e.errCount.Add(1)
		log.Printf("[OCR] Request failed (attempt %d/%d): %v", attempt+1, e.maxRetries, err)
		
		if attempt < e.maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
		
		// Re-check readiness
		e.isReady = false
		if checkErr := e.checkReady(); checkErr != nil {
			log.Printf("[OCR] Model server health check failed: %v", checkErr)
		} else {
			e.isReady = true
		}
	}
	
	// If all attempts failed, try Tesseract as a final fallback for images
	if isRasterImageExt(ext) {
		log.Printf("[OCR] Vision model failed for %s — trying Tesseract fallback", filepath.Base(filePath))
		if t, terr := e.tryTesseractRaster(filePath); terr == nil {
			return t, nil
		} else {
			log.Printf("[OCR] Tesseract fallback failed: %v", terr)
		}
	}
	
	return "", fmt.Errorf("all %d OCR attempts failed, last error: %w", e.maxRetries, lastErr)
}

func (e *Engine) doOCR(data []byte, filename string) (string, error) {
	// Use Ollama's vision API or DashScope
	encoded := base64.StdEncoding.EncodeToString(data)
	
	prompt := "OCR: Extract all text from this document accurately."
	lower := strings.ToLower(filename)
	if strings.Contains(lower, "id_card") || strings.Contains(lower, "身份证") {
		prompt = "OCR: This is an ID Card. Extract Name, ID Number, Address, and Date of Birth. Format clearly."
	} else if strings.Contains(lower, "screenshot") || strings.Contains(lower, "微信") || strings.Contains(lower, "chat") {
		prompt = "OCR: This is a Chat Screenshot. Extract the chat participants, timestamps, and the content of messages. Pay special attention to any mentions of money, dates, or agreements."
	}
	
	payload := map[string]interface{}{
		"model":  e.modelID,
		"prompt": prompt,
		"stream": false,
		"images": []string{encoded},
	}
	
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal failed: %w", err)
	}
	
	req, err := http.NewRequest("POST", e.baseURL+"/api/generate", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("request creation failed: %w", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response failed: %w", err)
	}
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OCR error %d: %s", resp.StatusCode, string(body))
	}
	
	var result struct {
		Response string `json:"response"`
	}
	
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("unmarshal failed: %w", err)
	}
	
	return result.Response, nil
}

func (e *Engine) extractPDFText(filePath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use pdftotext (part of poppler) which is very fast
	// -nopgbrk: don't insert page breaks
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", "-nopgbrk", filePath, "-")
	
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("pdftotext failed: %v (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

func (e *Engine) extractDOCX(filePath string) (string, error) {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open DOCX: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			data, err := io.ReadAll(rc)
			if err != nil {
				return "", err
			}

			dataStr := string(data)
			var finalContent strings.Builder
			
			// This regex picks up tags and text in between
			tagRe := regexp.MustCompile(`(<w:p[ >]|<w:t[ >]|</w:p>|>)`)
			tokens := tagRe.Split(dataStr, -1)
			tags := tagRe.FindAllString(dataStr, -1)
			
			for i, tag := range tags {
				if strings.HasPrefix(tag, "<w:t") {
					if i < len(tokens)-1 {
						finalContent.WriteString(tokens[i+1])
					}
				} else if tag == "</w:p>" {
					finalContent.WriteString("\n")
				}
			}
			
			return finalContent.String(), nil
		}
	}

	return "", fmt.Errorf("word/document.xml not found in DOCX")
}

func (e *Engine) checkTTL() {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if e.isReady && time.Since(e.lastUsed) > e.ttl {
		e.isReady = false
		log.Printf("[OCR] Model unloaded after %v idle", e.ttl)
	}
}

func (e *Engine) Unload() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.isReady = false
}

func (e *Engine) scanWithOpenAI(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	mime := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(filePath), ".png") {
		mime = "image/png"
	}

	payload := map[string]interface{}{
		"model": e.modelID,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "OCR: Extract all text from this document accurately. Output plain text only."},
					{"type": "image_url", "image_url": map[string]string{"url": "data:" + mime + ";base64," + encoded}},
				},
			},
		},
		"max_tokens": 2048,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", e.baseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Cloud OCR error %d: %s", resp.StatusCode, string(b))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no choice in response")
}

func (e *Engine) Stats() map[string]interface{} {
	return map[string]interface{}{
		"model":        e.modelID,
		"base_url":     e.baseURL,
		"is_ready":     e.isReady,
		"req_count":    e.reqCount.Load(),
		"err_count":    e.errCount.Load(),
		"last_used":    e.lastUsed.Format(time.RFC3339),
		"max_concurrent": cap(e.semaphore),
	}
}
