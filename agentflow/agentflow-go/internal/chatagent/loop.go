package chatagent

import (
	"encoding/json"
	"fmt"
	"log"

	"agentflow-go/internal/llm"
)

// Step is one iteration of the agent loop, captured for the UI to render
// inline ("the assistant called create_case with these args, got this back").
type Step struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id"`
	Arguments  string `json:"arguments"`              // raw JSON string from the model
	Result     string `json:"result,omitempty"`       // marshalled tool result, if ok
	Error      string `json:"error,omitempty"`        // tool error, if not ok
}

// Result — final outcome of a Run. FinalText is the assistant's prose answer
// (may be empty if the loop hit max iterations). Steps trace every tool call
// in order, so the UI can show the agent's work.
type Result struct {
	FinalText string `json:"final_text"`
	Steps     []Step `json:"steps"`
	Stopped   string `json:"stopped"` // "complete" | "max_iterations" | "error"
	Error     string `json:"error,omitempty"`
}

// Config controls the agent loop.
type Config struct {
	MaxIterations int               // hard ceiling; default 6
	Model         string            // overrides provider default
	System        string            // optional system prompt prepended to history
	Temp          float64           // generation temperature; default 0.1
	MaxTokens     int               // per-call output cap; default 1024
}

// Run executes the agent loop: alternate LLM call (with tools) → tool exec →
// feed result back → repeat until the model stops calling tools or we hit
// the iteration ceiling. The caller's `messages` are the conversation
// history; we DO NOT mutate them. We return all the steps that happened so
// the UI can render the agent's work.
func Run(provider *llm.Provider, registry *Registry, history []llm.ChatTurn, cfg Config) Result {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 6
	}
	if cfg.Temp == 0 {
		cfg.Temp = 0.1
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}

	// Compose the working history: optional system prompt + caller history.
	working := make([]llm.ChatTurn, 0, len(history)+1+cfg.MaxIterations*2)
	if cfg.System != "" {
		working = append(working, llm.ChatTurn{Role: "system", Content: cfg.System})
	}
	working = append(working, history...)

	tools := registry.Defs()
	out := Result{Steps: []Step{}}

	for iter := 0; iter < cfg.MaxIterations; iter++ {
		resp, err := provider.GenerateWithTools(working, tools, llm.GenerationConfig{
			Model:     cfg.Model,
			Temp:      cfg.Temp,
			MaxTokens: cfg.MaxTokens,
		})
		if err != nil {
			out.Stopped = "error"
			out.Error = err.Error()
			return out
		}

		// Terminal: the model gave a final text answer.
		if len(resp.ToolCalls) == 0 {
			out.FinalText = resp.Content
			out.Stopped = "complete"
			return out
		}

		// Append the assistant's tool-call message to the working history so
		// the next round sees it.
		working = append(working, llm.ChatTurn{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call, append a "tool" role message with the result.
		for _, tc := range resp.ToolCalls {
			step := Step{
				ToolName:   tc.Function.Name,
				ToolCallID: tc.ID,
				Arguments:  tc.Function.Arguments,
			}

			result, runErr := registry.Invoke(tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if runErr != nil {
				step.Error = runErr.Error()
				log.Printf("[agent] tool %s error: %v", tc.Function.Name, runErr)
				working = append(working, llm.ChatTurn{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    fmt.Sprintf(`{"error": %q}`, runErr.Error()),
				})
			} else {
				marshalled, _ := json.Marshal(result)
				step.Result = string(marshalled)
				working = append(working, llm.ChatTurn{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    string(marshalled),
				})
			}
			out.Steps = append(out.Steps, step)
		}
	}

	out.Stopped = "max_iterations"
	out.Error = fmt.Sprintf("agent did not converge within %d iterations", cfg.MaxIterations)
	return out
}
