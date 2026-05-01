package chatagent

import (
	"encoding/json"
	"fmt"

	"agentflow-go/internal/llm"
	"agentflow-go/internal/model"
	"agentflow-go/internal/workflow"
)

// Tool binds a JSON-schema-defined tool to a Go handler. The handler receives
// the raw JSON arguments string from the LLM and returns a JSON-serialisable
// result that gets fed back into the conversation.
type Tool struct {
	Def     llm.ToolDef
	Handler func(args json.RawMessage) (any, error)
}

// Registry is an ordered map of tool name → Tool. Order is preserved so the
// LLM sees a deterministic tool list (some models behave better that way).
type Registry struct {
	tools []Tool
	index map[string]int
}

func NewRegistry() *Registry { return &Registry{index: make(map[string]int)} }

func (r *Registry) Register(t Tool) {
	r.index[t.Def.Function.Name] = len(r.tools)
	r.tools = append(r.tools, t)
}

func (r *Registry) Defs() []llm.ToolDef {
	out := make([]llm.ToolDef, len(r.tools))
	for i, t := range r.tools {
		out[i] = t.Def
	}
	return out
}

// Invoke dispatches a tool call to its handler. Errors from the handler are
// returned as JSON-serialisable error objects so the LLM can read them.
func (r *Registry) Invoke(name string, args json.RawMessage) (any, error) {
	idx, ok := r.index[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return r.tools[idx].Handler(args)
}

// DocumentGenerator is the callback used by the `generate_document` tool.
// Wired by the server when it builds the agent registry, this lets the tool
// reuse the exact same pipeline as the HTTP endpoint without depending on
// internals of the server package.
type DocumentGenerator func(caseID, docType, userContext string) (doc model.GeneratedDoc, modelUsed string, err error)

// BuildCaseTools returns the standard set of case-management tools backed by
// the live workflow.Engine. The chatbot can use these to drive every CRUD
// surface in AgentFlow.
//
// Read-only tools are safe to call autonomously. Mutating tools (create_case,
// delete_case, advance_case, approve_hitl) all return enough metadata that
// the LLM can describe what happened in its final answer.
//
// `gen` is optional — pass nil to skip registering `generate_document`.
func BuildCaseTools(eng *workflow.Engine, gen DocumentGenerator, allowedDocTypes []string) *Registry {
	r := NewRegistry()

	// ───────────────── list_cases ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "list_cases",
				Description: "List all matters in the firm. Use this when the user asks about their book of business or refers to a client by name.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"filter": map[string]any{
							"type":        "string",
							"enum":        []string{"all", "needs_attention", "active", "closed"},
							"description": "Subset to return. 'needs_attention' = matters awaiting lawyer sign-off; 'active' = not closed; 'closed' = case_closed; 'all' (default).",
						},
					},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				Filter string `json:"filter"`
			}
			_ = json.Unmarshal(args, &p)
			cases := eng.ListCases()
			out := make([]map[string]any, 0, len(cases))
			for _, c := range cases {
				if !filterMatch(p.Filter, c) {
					continue
				}
				out = append(out, summariseCase(c))
			}
			return map[string]any{"cases": out, "count": len(out)}, nil
		},
	})

	// ───────────────── get_case ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "get_case",
				Description: "Get full details for a specific matter by case_id.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id": map[string]any{"type": "string"},
					},
					"required": []string{"case_id"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID string `json:"case_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			c, ok := eng.GetCaseSnapshot(p.CaseID)
			if !ok {
				return nil, fmt.Errorf("case %q not found", p.CaseID)
			}
			return summariseCase(c), nil
		},
	})

	// ───────────────── create_case ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "create_case",
				Description: "Create a new client matter. Use this when the user describes a new case to take on, or asks you to start a matter for someone.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"client_name":   map[string]any{"type": "string", "description": "Client / plaintiff name."},
						"matter_type":   map[string]any{"type": "string", "enum": allowedMatterTypes(), "description": "Case category."},
						"source":        map[string]any{"type": "string", "description": "How the matter came in (e.g. 'Phone call', 'Email', 'Walk-in')."},
						"initial_msg":   map[string]any{"type": "string", "description": "Optional first description / opening summary."},
					},
					"required": []string{"client_name", "matter_type"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				ClientName string `json:"client_name"`
				MatterType string `json:"matter_type"`
				Source     string `json:"source"`
				InitialMsg string `json:"initial_msg"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if p.Source == "" {
				p.Source = "AI assistant"
			}
			c := eng.CreateCase(p.ClientName, p.MatterType, p.Source, p.InitialMsg)
			return summariseCase(c), nil
		},
	})

	// ───────────────── delete_case ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "delete_case",
				Description: "Permanently delete a matter. Always confirm with the user before calling this — it is destructive.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id": map[string]any{"type": "string"},
					},
					"required": []string{"case_id"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID string `json:"case_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if err := eng.DeleteCase(p.CaseID); err != nil {
				return nil, err
			}
			return map[string]any{"deleted": p.CaseID}, nil
		},
	})

	// ───────────────── advance_case ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "advance_case",
				Description: "Advance a matter to the next workflow stage (e.g. CASE_EVALUATION → FEE_COLLECTION).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id": map[string]any{"type": "string"},
					},
					"required": []string{"case_id"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID string `json:"case_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if err := eng.AdvanceState(p.CaseID); err != nil {
				return nil, err
			}
			c, _ := eng.GetCaseSnapshot(p.CaseID)
			return summariseCase(c), nil
		},
	})

	// ───────────────── add_note ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "add_note",
				Description: "Append a note to a matter's activity log. Use this to record observations, decisions, or context that should persist on the case.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id": map[string]any{"type": "string"},
						"text":    map[string]any{"type": "string"},
					},
					"required": []string{"case_id", "text"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID string `json:"case_id"`
				Text   string `json:"text"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			eng.AddNote(p.CaseID, p.Text)
			return map[string]any{"ok": true, "case_id": p.CaseID}, nil
		},
	})

	// ───────────────── update_case ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "update_case",
				Description: "Edit a matter's client name and/or matter type. Both fields are optional — pass only what changes.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id":     map[string]any{"type": "string"},
						"client_name": map[string]any{"type": "string"},
						"matter_type": map[string]any{"type": "string", "enum": allowedMatterTypes()},
					},
					"required": []string{"case_id"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID     string `json:"case_id"`
				ClientName string `json:"client_name"`
				MatterType string `json:"matter_type"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			if err := eng.UpdateCase(p.CaseID, p.ClientName, p.MatterType); err != nil {
				return nil, err
			}
			c, _ := eng.GetCaseSnapshot(p.CaseID)
			return summariseCase(c), nil
		},
	})

	// ───────────────── approve_hitl ─────────────────
	r.Register(Tool{
		Def: llm.ToolDef{
			Type: "function",
			Function: llm.ToolDefFn{
				Name:        "approve_hitl",
				Description: "Approve or reject a human-in-the-loop checkpoint on a matter. Always confirm with the user before approving — this advances the workflow.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"case_id":  map[string]any{"type": "string"},
						"approved": map[string]any{"type": "boolean"},
						"reason":   map[string]any{"type": "string", "description": "Required when approved=false."},
					},
					"required": []string{"case_id", "approved"},
				},
			},
		},
		Handler: func(args json.RawMessage) (any, error) {
			var p struct {
				CaseID   string `json:"case_id"`
				Approved bool   `json:"approved"`
				Reason   string `json:"reason"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return nil, err
			}
			c, ok := eng.GetCaseSnapshot(p.CaseID)
			if !ok {
				return nil, fmt.Errorf("case %q not found", p.CaseID)
			}
			if err := eng.ApproveHITL(p.CaseID, c.State, p.Approved, p.Reason); err != nil {
				return nil, err
			}
			c2, _ := eng.GetCaseSnapshot(p.CaseID)
			return summariseCase(c2), nil
		},
	})

	// ───────────────── generate_document ─────────────────
	// Only registered if the caller provided a generator + at least one
	// allowed doc type. The enum exposed to the LLM is exactly the registry's
	// list of doctype IDs, so the agent can't invent unknown types.
	if gen != nil && len(allowedDocTypes) > 0 {
		r.Register(Tool{
			Def: llm.ToolDef{
				Type: "function",
				Function: llm.ToolDefFn{
					Name: "generate_document",
					Description: "Generate a Chinese legal filing document (e.g. 起诉状 / Civil Complaint) for a case, grounded in its uploaded evidence. The result lands as a 'draft' that the lawyer must approve before .docx export. Use this when the user asks to draft, prepare, or produce a specific filing type.",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"case_id": map[string]any{
								"type":        "string",
								"description": "Target case ID. Default to the current case context if the user didn't name one.",
							},
							"doc_type": map[string]any{
								"type":        "string",
								"enum":        allowedDocTypes,
								"description": "Which document type to generate. Pick the closest match — 起诉状/complaint for a complaint, etc.",
							},
							"user_context": map[string]any{
								"type":        "string",
								"description": "Optional free-text guidance the user provided (e.g. 'emphasize the unpaid wages', 'use a more formal tone').",
							},
						},
						"required": []string{"case_id", "doc_type"},
					},
				},
			},
			Handler: func(args json.RawMessage) (any, error) {
				var p struct {
					CaseID      string `json:"case_id"`
					DocType     string `json:"doc_type"`
					UserContext string `json:"user_context"`
				}
				if err := json.Unmarshal(args, &p); err != nil {
					return nil, err
				}
				doc, modelUsed, err := gen(p.CaseID, p.DocType, p.UserContext)
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"doc_id":     doc.ID,
					"doc_type":   doc.DocType,
					"version":    doc.Version,
					"title":      doc.Title,
					"status":     doc.Status,
					"section_count": len(doc.Sections),
					"model_used": modelUsed,
				}, nil
			},
		})
	}

	return r
}

// summariseCase produces a compact JSON-friendly view of a case for the LLM.
// We deliberately keep this small — the LLM doesn't need every field, and a
// big tool result eats context.
func summariseCase(c model.Case) map[string]any {
	return map[string]any{
		"case_id":       c.CaseID,
		"client_name":   c.ClientName,
		"matter_type":   c.MatterType,
		"state":         c.State,
		"is_paid":       c.IsPaid,
		"doc_count":     len(c.UploadedDocuments),
		"note_count":    len(c.Notes),
		"opened_at":     c.CreatedAt,
		"updated_at":    c.UpdatedAt,
	}
}

func filterMatch(filter string, c model.Case) bool {
	switch filter {
	case "", "all":
		return true
	case "needs_attention":
		// states that conventionally need lawyer sign-off
		switch c.State {
		case "CASE_EVALUATION", "FEE_COLLECTION", "CLIENT_REVIEW", "DRAFT_PREPARATION":
			return true
		}
		return false
	case "active":
		return c.State != "CASE_CLOSED"
	case "closed":
		return c.State == "CASE_CLOSED"
	}
	return true
}

// allowedMatterTypes mirrors llmutil.AllowedMatterTypes — kept as a slice so
// it can be embedded directly in the tool's JSON Schema enum.
func allowedMatterTypes() []string {
	return []string{
		"Civil Litigation", "Contract Dispute", "Sales Contract Dispute",
		"Debt Dispute", "Loan Dispute", "Lease Dispute",
		"Labor Dispute", "Commercial Lease Dispute",
	}
}
