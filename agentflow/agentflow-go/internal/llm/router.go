package llm

import (
	"context"
	"fmt"
	"log"
	"time"
)

// TaskType defines the complexity/category of an LLM task
type TaskType string

const (
	TaskTypeOCR       TaskType = "ocr"        // Vision/OCR tasks
	TaskTypeIntake    TaskType = "intake"     // Initial case intake/classification
	TaskTypeSummary   TaskType = "summary"    // Document summarization
	TaskTypeComplex   TaskType = "complex"    // Complex reasoning, legal analysis
	TaskTypeDraft     TaskType = "draft"      // Document generation
	TaskTypeChat      TaskType = "chat"       // Simple chat/questions
	TaskTypeDefault   TaskType = "default"    // Use default model
)

// RouterConfig holds model IDs for different task types
type RouterConfig struct {
	OCRModel     string
	ComplexModel string
	MediumModel  string
	DefaultModel string
}

// Router intelligently selects models based on task type
type Router struct {
	provider *Provider
	config   RouterConfig
	enabled  bool
}

// NewRouter creates a model router with the given provider and configuration
func NewRouter(p *Provider, cfg RouterConfig) *Router {
	return &Router{
		provider: p,
		config:   cfg,
		enabled:  true,
	}
}

// SelectModel returns the appropriate model ID for the given task type
func (r *Router) SelectModel(task TaskType) string {
	if !r.enabled {
		return r.config.DefaultModel
	}

	switch task {
	case TaskTypeOCR:
		return r.config.OCRModel
	case TaskTypeIntake, TaskTypeSummary, TaskTypeChat:
		return r.config.MediumModel
	case TaskTypeComplex, TaskTypeDraft:
		return r.config.ComplexModel
	default:
		return r.config.DefaultModel
	}
}

// Generate routes a generation request to the appropriate model
func (r *Router) Generate(task TaskType, prompt, context string, config GenerationConfig) (string, error) {
	model := r.SelectModel(task)
	config.Model = model
	log.Printf("[Router] Task=%s -> Model=%s", task, model)
	return r.provider.Generate(prompt, context, config)
}

// GenerateWithTimeout routes a generation request with timeout
func (r *Router) GenerateWithTimeout(ctx context.Context, task TaskType, prompt, context string, config GenerationConfig, timeout time.Duration) (string, error) {
	model := r.SelectModel(task)
	config.Model = model
	log.Printf("[Router] Task=%s -> Model=%s (timeout=%v)", task, model, timeout)
	return r.provider.GenerateWithTimeout(ctx, prompt, context, config, timeout)
}

// GenerateJSON routes a JSON generation request
func (r *Router) GenerateJSON(task TaskType, prompt, context string, config GenerationConfig, defaultResult interface{}) (interface{}, error) {
	model := r.SelectModel(task)
	config.Model = model
	log.Printf("[Router] Task=%s -> Model=%s (JSON)", task, model)
	return r.provider.GenerateJSON(prompt, context, config, defaultResult)
}

// Benchmark compares models for a given task type
func (r *Router) Benchmark(task TaskType, prompt string) (map[string]BenchmarkResult, error) {
	results := make(map[string]BenchmarkResult)

	// Test the model that would be used for this task
	modelID := r.SelectModel(task)
	result := r.provider.Benchmark(modelID)
	results[modelID] = result

	return results, nil
}

// BenchmarkAll tests all models in the router with the same prompt
func (r *Router) BenchmarkAll(prompt string) map[string]BenchmarkResult {
	results := make(map[string]BenchmarkResult)

	models := []string{
		r.config.OCRModel,
		r.config.MediumModel,
		r.config.ComplexModel,
	}

	for _, model := range models {
		if model == "" {
			continue
		}
		log.Printf("[Router] Benchmarking model: %s", model)
		result := r.provider.Benchmark(model)
		results[model] = result
	}

	return results
}

// SetEnabled enables or disables the router (when disabled, always uses default model)
func (r *Router) SetEnabled(enabled bool) {
	r.enabled = enabled
}

// Stats returns router statistics
func (r *Router) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":        r.enabled,
		"ocr_model":      r.config.OCRModel,
		"medium_model":   r.config.MediumModel,
		"complex_model":  r.config.ComplexModel,
		"default_model":  r.config.DefaultModel,
		"provider_stats": r.provider.Stats(),
	}
}

// String returns a string representation of the router config
func (r *Router) String() string {
	return fmt.Sprintf("Router{OCR=%s, Medium=%s, Complex=%s, Default=%s, Enabled=%v}",
		r.config.OCRModel, r.config.MediumModel, r.config.ComplexModel, r.config.DefaultModel, r.enabled)
}
