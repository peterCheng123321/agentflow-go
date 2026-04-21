package agent

import (
	"fmt"
	"strings"
	"sync"
)

// Registry holds all registered tools indexed by name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. Panics on duplicate name.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic(fmt.Sprintf("agent: duplicate tool name %q", t.Name()))
	}
	r.tools[t.Name()] = t
}

// Get looks up a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools in sorted order.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// PromptBlock returns the tool listing used in the agent system prompt.
func (r *Registry) PromptBlock() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder
	for _, t := range r.tools {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", t.Name(), t.Description()))
		for param, schema := range t.Params() {
			req := ""
			if schema.Required {
				req = " (required)"
			}
			sb.WriteString(fmt.Sprintf("    - %s%s: %s\n", param, req, schema.Description))
		}
	}
	return sb.String()
}
