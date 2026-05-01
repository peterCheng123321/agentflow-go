package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"agentflow-go/internal/chatagent"
	"agentflow-go/internal/doctype"
	"agentflow-go/internal/llm"
	"agentflow-go/internal/model"
)

// agentChatRequest — POST /v1/agent/chat body.
type agentChatRequest struct {
	Messages []chatMessage `json:"messages"`
	CaseID   string        `json:"case_id,omitempty"`
}

// agentChatResponse — what the UI gets back. `steps` traces every tool call
// the agent made, `reply` is the final assistant text.
type agentChatResponse struct {
	Reply         string         `json:"reply"`
	Steps         []chatagent.Step   `json:"steps"`
	Stopped       string         `json:"stopped"`
	Error         string         `json:"error,omitempty"`
	ModelUsed     string         `json:"model_used"`
	ElapsedMillis int64          `json:"elapsed_ms"`
}

// agentSystemPrompt is the system message the agent receives. It tells the
// model what it can do and sets the tone — terse, status-first, verbs lead,
// matching AgentFlow's voice.
const agentSystemPrompt = `You are AgentFlow, an AI legal-operations assistant for a working lawyer. You can read and modify the firm's case state via the supplied tools.

Voice: terse, status-first, verbs lead. No marketing copy, no exclamation marks.

Rules:
- BEFORE you mutate state (create_case, delete_case, advance_case, approve_hitl, update_case), confirm with the user IN PLAIN TEXT first if intent is ambiguous. If the user has already given a clear instruction in this conversation, proceed without re-confirming.
- delete_case is destructive. Prefer asking the user to confirm even if they were terse.
- list_cases / get_case are read-only — call them freely whenever you need context.
- After tool calls succeed, summarise what changed in one or two sentences.
- If a tool returns an error, surface it to the user; don't retry without their okay.
- If the user gives you a case context (case_id), assume operations target that case unless they name a different one.`

func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req agentChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	// Router fast-path: classify the latest user message. CONVERSATIONAL
	// turns (greetings, capability questions, smalltalk) skip the tool
	// registry and ReAct loop entirely — one direct chat call instead of
	// the full agent setup. Saves ~500-2000ms per turn on chitchat.
	//
	// We only fast-path the *last* message and only when the case context
	// is empty: if the user is in a case the agent should always have its
	// tools wired up, even for greetings ("hi" mid-conversation may still
	// be answered against case state).
	if req.CaseID == "" {
		if lastUser := lastUserContent(req.Messages); lastUser != "" {
			if intent := s.classifyIntent(r.Context(), lastUser); intent == IntentConversational {
				if s.tryConversationalReply(w, r, lastUser, started) {
					return
				}
				// classify said CONVERSATIONAL but the fast-path errored —
				// log and continue with the full agent path below.
			}
		}
	}

	// Build the case-tool registry, freshly bound to this server's workflow
	// engine. The generator closure lets the `generate_document` tool reuse
	// the same generation pipeline as the HTTP endpoint.
	registry := chatagent.BuildCaseTools(
		s.workflow,
		func(caseID, docType, userContext string) (model.GeneratedDoc, string, error) {
			gd, used, _, err := s.generateDocumentCore(caseID, docType, userContext)
			return gd, used, err
		},
		doctype.IDs(),
	)

	// Translate the wire-format messages into LLM ChatTurn shape.
	turns := make([]llm.ChatTurn, 0, len(req.Messages)+1)
	if req.CaseID != "" {
		turns = append(turns, llm.ChatTurn{
			Role:    "system",
			Content: fmt.Sprintf("Current case context: case_id=%q. If the user uses a pronoun ('this case', 'it'), assume they mean this matter unless they name another.", req.CaseID),
		})
	}
	for _, m := range req.Messages {
		turns = append(turns, llm.ChatTurn{
			Role:    strings.ToLower(strings.TrimSpace(m.Role)),
			Content: m.Content,
		})
	}

	// The synthesis model is also our agent model — accuracy matters more
	// than speed here because each tool call costs another round trip.
	model := s.modelForTask("synth")

	result := chatagent.Run(s.llm, registry, turns, chatagent.Config{
		MaxIterations: 6,
		Model:         model,
		System:        agentSystemPrompt,
		Temp:          0.1,
		MaxTokens:     1024,
	})

	s.writeJSON(w, http.StatusOK, agentChatResponse{
		Reply:         result.FinalText,
		Steps:         result.Steps,
		Stopped:       result.Stopped,
		Error:         result.Error,
		ModelUsed:     model,
		ElapsedMillis: time.Since(started).Milliseconds(),
	})
}

// conversationalSystemPrompt is the system message for the router-fast-path
// reply. Same voice as the full agent prompt but explicitly admits that
// no tools or case state are reachable on this turn — the router decided
// the user didn't need them.
const conversationalSystemPrompt = `You are AgentFlow, an AI legal-operations assistant. The user just sent a greeting, smalltalk, or a capability question. Reply briefly in their language.

Voice: terse, status-first, verbs lead. No marketing copy, no exclamation marks. One or two sentences.

If asked what you can do, answer at a high level (read and modify case state, draft documents, schedule, summarize) — do not enumerate every tool.`

// tryConversationalReply is the fast path for messages the router
// classified as CONVERSATIONAL. One direct chat call, no tools, no
// ReAct loop. Returns true if it wrote a response, false if the caller
// should fall through to the full agent path.
func (s *Server) tryConversationalReply(w http.ResponseWriter, r *http.Request, lastUser string, started time.Time) bool {
	model := s.modelForTask("synth")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	reply, err := s.llm.Classify(ctx, conversationalSystemPrompt, lastUser, 256)
	if err != nil {
		log.Printf("[router] conversational fast-path failed, falling through: %v", err)
		return false
	}
	s.writeJSON(w, http.StatusOK, agentChatResponse{
		Reply:         reply,
		Steps:         nil,
		Stopped:       "fast_path_conversational",
		ModelUsed:     model,
		ElapsedMillis: time.Since(started).Milliseconds(),
	})
	return true
}

// lastUserContent returns the content of the most recent user message,
// or "" if there isn't one.
func lastUserContent(msgs []chatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(msgs[i].Role), "user") {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}
