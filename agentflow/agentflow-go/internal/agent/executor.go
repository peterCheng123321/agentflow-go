package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"agentflow-go/internal/llm"
	"agentflow-go/internal/llmutil"
)

// Config controls agent execution behaviour.
type Config struct {
	// MaxSteps is the maximum number of tool calls before forcing a final answer.
	MaxSteps int
	// Model is the LLM model name for agent reasoning.
	Model string
	// MaxTokensPerStep is the token budget for each LLM call.
	MaxTokensPerStep int
}

func (c *Config) setDefaults() {
	if c.MaxSteps <= 0 {
		c.MaxSteps = 8
	}
	if c.Model == "" {
		c.Model = "qwen-plus"
	}
	if c.MaxTokensPerStep <= 0 {
		c.MaxTokensPerStep = 1024
	}
}

// Executor runs the ReAct (Reason + Act) agentic loop.
type Executor struct {
	registry *Registry
	llm      *llm.Provider
	cfg      Config
}

// NewExecutor creates an Executor backed by the given registry and LLM provider.
func NewExecutor(reg *Registry, provider *llm.Provider, cfg Config) *Executor {
	cfg.setDefaults()
	return &Executor{registry: reg, llm: provider, cfg: cfg}
}

// Run executes the agentic loop for the given goal, returning the final answer
// and a log of all steps taken.
func (e *Executor) Run(ctx context.Context, goal string) RunResult {
	var steps []AgentStep
	history := ""

	for i := 0; i < e.cfg.MaxSteps; i++ {
		prompt := e.buildPrompt(goal, history)

		raw, err := e.llm.GenerateWithTimeout(ctx, prompt, "", llm.GenerationConfig{
			MaxTokens: e.cfg.MaxTokensPerStep,
			Temp:      0.1,
			Model:     e.cfg.Model,
		}, 0)
		if err != nil {
			log.Printf("[agent] LLM error at step %d: %v", i, err)
			break
		}

		action, err := parseAction(raw)
		if err != nil {
			log.Printf("[agent] parse error at step %d: %v (raw: %.80s)", i, err, raw)
			// Give the LLM one chance to recover
			history += fmt.Sprintf("\nParsing your last response failed (%v). Please respond with valid JSON.", err)
			continue
		}

		if action.Action == "final_answer" {
			step := AgentStep{FinalAnswer: action.Answer}
			steps = append(steps, step)
			return RunResult{Answer: action.Answer, Steps: steps}
		}

		// Execute the tool
		tool, ok := e.registry.Get(action.Action)
		if !ok {
			errMsg := fmt.Sprintf("unknown tool %q — choose from: %s",
				action.Action, strings.Join(e.toolNames(), ", "))
			history += fmt.Sprintf("\nObservation: error — %s", errMsg)
			steps = append(steps, AgentStep{
				ToolName: action.Action,
				Input:    action.Input,
				Output:   &ToolOutput{Error: errMsg},
			})
			continue
		}

		out := tool.Execute(ctx, action.Input)
		step := AgentStep{ToolName: action.Action, Input: action.Input, Output: &out}
		steps = append(steps, step)

		obs := out.Text
		if out.Error != "" {
			obs = "error — " + out.Error
		}
		history += fmt.Sprintf("\nAction: %s(%s)\nObservation: %s", action.Action, inputSummary(action.Input), obs)
	}

	// Ran out of steps — ask for a final answer from accumulated history
	prompt := e.buildPrompt(goal, history+"\n\nYou have reached the step limit. Provide your final_answer now based on what you have found.")
	raw, _ := e.llm.GenerateWithTimeout(ctx, prompt, "", llm.GenerationConfig{
		MaxTokens: e.cfg.MaxTokensPerStep,
		Temp:      0.1,
		Model:     e.cfg.Model,
	}, 0)
	finalAnswer := raw
	if a, err := parseAction(raw); err == nil && a.Answer != "" {
		finalAnswer = a.Answer
	}
	steps = append(steps, AgentStep{FinalAnswer: finalAnswer})
	return RunResult{Answer: finalAnswer, Steps: steps, Truncated: true}
}

// ─── Internal helpers ────────────────────────────────────────────────────────

type agentAction struct {
	Action string    `json:"action"`
	Input  ToolInput `json:"input"`
	Answer string    `json:"answer"`
}

func parseAction(raw string) (agentAction, error) {
	payload := llmutil.ExtractJSONObject(raw)
	var a agentAction
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		return a, fmt.Errorf("JSON parse: %w", err)
	}
	if a.Action == "" {
		return a, fmt.Errorf("missing 'action' field")
	}
	return a, nil
}

func (e *Executor) buildPrompt(goal, history string) string {
	return fmt.Sprintf(`You are an expert Chinese legal AI assistant. Achieve the following goal by reasoning step-by-step and calling tools as needed.

GOAL: %s

AVAILABLE TOOLS:
%s

INSTRUCTIONS:
- Respond with exactly one JSON object per turn (no markdown fences, no explanation outside the JSON).
- To call a tool: {"action": "<tool_name>", "input": {<params>}}
- To give the final answer: {"action": "final_answer", "answer": "<your complete answer>"}
- The final answer should be in Chinese if the goal involves Chinese legal documents, otherwise in English.
- Be concise but complete.

HISTORY:%s

Your next JSON response:`, goal, e.registry.PromptBlock(), history)
}

func (e *Executor) toolNames() []string {
	tools := e.registry.All()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

func inputSummary(inp ToolInput) string {
	if len(inp) == 0 {
		return ""
	}
	b, _ := json.Marshal(inp)
	s := string(b)
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
