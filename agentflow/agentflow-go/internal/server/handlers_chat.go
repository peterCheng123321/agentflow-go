package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-go/internal/llm"
)

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
	CaseID   string        `json:"case_id,omitempty"`
	UseRAG   bool          `json:"use_rag,omitempty"`
}

type chatResponse struct {
	Reply    string   `json:"reply"`
	Sources  []string `json:"sources,omitempty"`
	Model    string   `json:"model,omitempty"`
	TokensIn int      `json:"tokens_in,omitempty"`
}

// buildChatPrompt constructs the prompt + RAG context + source filenames for a chat turn.
func (s *Server) buildChatPrompt(req chatRequest) (prompt, ragCtx string, sources []string) {
	var sb strings.Builder
	for _, m := range req.Messages[:len(req.Messages)-1] {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	history := sb.String()
	user := req.Messages[len(req.Messages)-1].Content

	if req.UseRAG && s.rag != nil {
		for _, h := range s.rag.Search(user, 4) {
			ragCtx += h.Chunk + "\n---\n"
			if h.Filename != "" {
				sources = append(sources, h.Filename)
			}
		}
		ragCtx = clipContext(ragCtx, 8000)
	}

	caseCtx := ""
	if req.CaseID != "" && s.workflow != nil {
		if c, ok := s.workflow.GetCaseSnapshot(req.CaseID); ok {
			caseCtx = fmt.Sprintf("Current case: %s (client=%s, matter=%s, state=%s)\n",
				c.CaseID, c.ClientName, c.MatterType, c.State)
		}
	}

	system := "You are AgentFlow, a helpful AI assistant embedded in a legal case management app. " +
		"Answer clearly and concisely. If retrieval context is provided, ground your answer in it. " +
		"If no relevant context is available, answer from general knowledge and say so."

	prompt = system + "\n\n"
	if caseCtx != "" {
		prompt += caseCtx + "\n"
	}
	if history != "" {
		prompt += "Conversation so far:\n" + history + "\n"
	}
	prompt += "USER: " + user + "\nASSISTANT:"
	return
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	prompt, ragCtx, sources := s.buildChatPrompt(req)

	reply, err := s.llm.Generate(prompt, ragCtx, llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.4,
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("LLM error: %v", err))
		return
	}

	s.writeJSON(w, http.StatusOK, chatResponse{
		Reply:   strings.TrimSpace(reply),
		Sources: sources,
	})
}

// handleChatStream streams the assistant reply as SSE events. Event shapes:
//
//	data: {"delta":"next token"}\n\n
//	data: {"done":true,"sources":["a.pdf","b.pdf"]}\n\n
//
// On error, a single {"error":"..."} event is sent followed by {"done":true}.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.Messages) == 0 {
		s.writeError(w, http.StatusBadRequest, "messages required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(obj map[string]interface{}) {
		b, _ := json.Marshal(obj)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	prompt, ragCtx, sources := s.buildChatPrompt(req)

	err := s.llm.GenerateStream(prompt, ragCtx, llm.GenerationConfig{
		MaxTokens: 2048,
		Temp:      0.4,
	}, func(delta string) error {
		send(map[string]interface{}{"delta": delta})
		return nil
	})
	if err != nil {
		send(map[string]interface{}{"error": err.Error()})
	}
	send(map[string]interface{}{"done": true, "sources": sources})
}
