package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ToolDef is the OpenAI-compatible function tool definition. DeepSeek,
// DashScope, and OpenAI all accept this exact wire shape.
type ToolDef struct {
	Type     string         `json:"type"`     // always "function"
	Function ToolDefFn      `json:"function"`
}

type ToolDefFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall is what the model returns when it wants to invoke a function.
type ToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function ToolCallFn     `json:"function"`
}

type ToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string — tool decides how to parse
}

// ChatTurn is a single message in the conversation. Used both as input
// (history) and output (the assistant's tool-call or final text).
//
// Fields are pointers/slices so JSON omits them when empty — the OpenAI
// shape uses different fields per role:
//   user:      { role, content }
//   assistant: { role, content?, tool_calls? }
//   tool:      { role, tool_call_id, content }
type ChatTurn struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolChatResponse — what GenerateWithTools returns. Either ToolCalls is
// non-empty (model wants to invoke functions) or Content is non-empty
// (model gave a final text answer). Never both.
type ToolChatResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// GenerateWithTools is the OpenAI-compatible tool-calling chat call. It does
// NOT loop — the caller (the agent) drives the loop. This keeps the loop
// concerns (max iterations, observability, cancellation) out of the LLM
// transport layer.
//
// Only OpenAI-compat backends (DeepSeek, DashScope, OpenAI) support tools.
// Ollama is not in scope; we return an explicit error.
func (p *Provider) GenerateWithTools(history []ChatTurn, tools []ToolDef, config GenerationConfig) (ToolChatResponse, error) {
	if p.backend != BackendOpenAICompat {
		return ToolChatResponse{}, fmt.Errorf("tool-calling requires an OpenAI-compatible backend; got %s", p.backend)
	}

	maxTok := config.MaxTokens
	if maxTok <= 0 {
		maxTok = 2048
	}
	temp := config.Temp
	if temp <= 0 {
		temp = 0.1
	}
	model := p.modelName
	if config.Model != "" {
		model = config.Model
	}

	payload := map[string]any{
		"model":       model,
		"messages":    history,
		"max_tokens":  maxTok,
		"temperature": temp,
	}
	if len(tools) > 0 {
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return ToolChatResponse{}, err
	}
	req, err := http.NewRequest("POST", p.baseURL+"/chat/completions", bytes.NewBuffer(data))
	if err != nil {
		return ToolChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return ToolChatResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ToolChatResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return ToolChatResponse{}, fmt.Errorf("LLM tool-call %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ToolChatResponse{}, fmt.Errorf("LLM response parse: %w (body: %.200q)", err, string(body))
	}
	if len(parsed.Choices) == 0 {
		return ToolChatResponse{}, fmt.Errorf("LLM returned no choices: %s", string(body))
	}
	msg := parsed.Choices[0].Message
	return ToolChatResponse{
		Content:   msg.Content,
		ToolCalls: msg.ToolCalls,
	}, nil
}
