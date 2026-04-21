// Package tools provides concrete Tool implementations for legal document processing.
package tools

import (
	"context"
	"fmt"
	"strings"

	"agentflow-go/internal/agent"
	"agentflow-go/internal/rag"
)

// RAGSearch searches the in-memory BM25 index for document chunks relevant
// to a query.  Results are fed back to the agent as text.
type RAGSearch struct {
	mgr *rag.Manager
}

func NewRAGSearch(mgr *rag.Manager) *RAGSearch { return &RAGSearch{mgr: mgr} }

func (t *RAGSearch) Name() string { return "rag_search" }
func (t *RAGSearch) Description() string {
	return "Search uploaded case documents for relevant text. Use this to find evidence, facts, amounts, parties, or dates mentioned in the documents."
}
func (t *RAGSearch) Params() map[string]agent.ParamSchema {
	return map[string]agent.ParamSchema{
		"query": {Description: "Natural-language search query (Chinese or English)", Required: true},
		"top_k": {Description: "Number of results to return (default 5, max 10)"},
	}
}

func (t *RAGSearch) Execute(_ context.Context, input agent.ToolInput) agent.ToolOutput {
	query, _ := input["query"].(string)
	if query == "" {
		return agent.ToolOutput{Error: "query is required"}
	}
	k := 5
	if v, ok := input["top_k"].(float64); ok && v > 0 {
		k = int(v)
		if k > 10 {
			k = 10
		}
	}

	results := t.mgr.Search(query, k)
	if len(results) == 0 {
		return agent.ToolOutput{Text: "No matching documents found.", Data: results}
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s (score %.2f)\n%s\n\n", i+1, r.Filename, r.Score, r.Chunk))
	}
	return agent.ToolOutput{Text: sb.String(), Data: results}
}
