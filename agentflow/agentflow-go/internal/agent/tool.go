// Package agent provides a ReAct-style agentic loop and typed tool registry
// for legal document processing tasks.
package agent

import "context"

// ToolInput is a free-form parameter bag passed to a tool.
type ToolInput map[string]interface{}

// ToolOutput is the result returned by a tool.
type ToolOutput struct {
	// Data holds the structured result (anything JSON-serialisable).
	Data interface{} `json:"data,omitempty"`
	// Text is a human-readable summary of the result, used when feeding
	// the output back into the agent prompt.
	Text string `json:"text"`
	// Error is non-empty when the tool failed; the executor treats this as
	// a soft error and continues the loop.
	Error string `json:"error,omitempty"`
}

// ParamSchema describes a single input parameter for a Tool.
type ParamSchema struct {
	Description string
	Required    bool
}

// Tool is the interface every agent tool must implement.
type Tool interface {
	// Name returns the unique snake_case identifier used by the LLM to call this tool.
	Name() string
	// Description explains what the tool does and when to use it (shown to LLM).
	Description() string
	// Params returns the input schema: param name → schema.
	Params() map[string]ParamSchema
	// Execute runs the tool with the provided input.
	Execute(ctx context.Context, input ToolInput) ToolOutput
}

// AgentStep records one action taken by the agent.
type AgentStep struct {
	// ToolName is empty for the final answer step.
	ToolName string      `json:"tool_name,omitempty"`
	Input    ToolInput   `json:"input,omitempty"`
	Output   *ToolOutput `json:"output,omitempty"`
	// FinalAnswer is set only on the last step.
	FinalAnswer string `json:"final_answer,omitempty"`
}

// RunResult is the complete output of one agent run.
type RunResult struct {
	Answer string      `json:"answer"`
	Steps  []AgentStep `json:"steps"`
	// Truncated is true when the run hit MaxSteps without a final answer.
	Truncated bool `json:"truncated,omitempty"`
}
