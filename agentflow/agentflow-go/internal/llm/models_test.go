package llm

import (
	"encoding/json"
	"testing"
)

func TestListModelsDashScope(t *testing.T) {
	models, err := ListModels(BackendOpenAICompat, "https://dashscope.aliyuncs.com/compatible-mode/v1")
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if len(models) == 0 {
		t.Fatal("Expected at least one model, got none")
	}

	// Check for expected models
	expectedModels := []string{"qwen-max", "qwen-plus", "qwen-turbo", "qwen-long"}
	modelIDs := make(map[string]bool)
	for _, m := range models {
		modelIDs[m.ID] = true
		if m.Backend != "dashscope" {
			t.Errorf("Expected backend 'dashscope', got '%s'", m.Backend)
		}
	}

	for _, exp := range expectedModels {
		if !modelIDs[exp] {
			t.Errorf("Expected model '%s' not found in list", exp)
		}
	}

	t.Logf("Found %d models: %v", len(models), modelIDs)
}

func TestModelInfoJSON(t *testing.T) {
	m := ModelInfo{
		ID:          "qwen-turbo",
		Name:        "Qwen Turbo",
		Backend:     "dashscope",
		IsDefault:   false,
		Description: "Fast responses for simple tasks",
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	var unmarshaled ModelInfo
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if unmarshaled.ID != m.ID {
		t.Errorf("Expected ID %s, got %s", m.ID, unmarshaled.ID)
	}
}

func TestBenchmarkResultJSON(t *testing.T) {
	r := BenchmarkResult{
		Model:        "qwen-turbo",
		Backend:      "dashscope",
		Success:      true,
		LatencyMs:    1250.5,
		TokensPerSec: 45.2,
		Timestamp:    "2024-01-01T00:00:00Z",
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	t.Logf("BenchmarkResult JSON: %s", string(data))

	var unmarshaled BenchmarkResult
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if !unmarshaled.Success {
		t.Error("Expected Success=true")
	}
}
