package workflow

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"agentflow-go/internal/model"
	"agentflow-go/internal/rag"
)

const (
	StateClientCapture     = "CLIENT_CAPTURE"
	StateInitialContact    = "INITIAL_CONTACT"
	StateCaseEvaluation    = "CASE_EVALUATION"
	StateFeeCollection     = "FEE_COLLECTION"
	StateGroupCreation     = "GROUP_CREATION"
	StateMaterialIngestion = "MATERIAL_INGESTION"
	StateDocumentGeneration = "DOCUMENT_GENERATION"
	StateClientApproval    = "CLIENT_APPROVAL"
	StateFinalPDFSend      = "FINAL_PDF_SEND"
	StateArchiveClose      = "ARCHIVE_CLOSE"
)

var stateOrder = []string{
	StateClientCapture,
	StateInitialContact,
	StateCaseEvaluation,
	StateFeeCollection,
	StateGroupCreation,
	StateMaterialIngestion,
	StateDocumentGeneration,
	StateClientApproval,
	StateFinalPDFSend,
	StateArchiveClose,
}

var hitlGates = map[string]bool{
	StateCaseEvaluation:     true,
	StateDocumentGeneration: true,
	StateFinalPDFSend:       true,
}

var hitlGateLabels = map[string]string{
	StateCaseEvaluation:     "Case evaluation",
	StateDocumentGeneration: "Document drafting",
	StateFinalPDFSend:       "Final PDF delivery",
}

type Engine struct {
	mu     sync.RWMutex
	cases  map[string]*model.Case
	maxCases int
	onChange func()
}

func NewEngine(maxCases int, onChange func()) *Engine {
	return &Engine{
		cases:    make(map[string]*model.Case),
		maxCases: maxCases,
		onChange: onChange,
	}
}

func deepCopyCase(c *model.Case) model.Case {
	out := *c
	out.Notes = append([]model.Note(nil), c.Notes...)
	out.UploadedDocuments = append([]string(nil), c.UploadedDocuments...)
	out.NodeHistory = append([]string(nil), c.NodeHistory...)
	out.Highlights = append([]model.Highlight(nil), c.Highlights...)
	if c.HITLApprovals != nil {
		out.HITLApprovals = make(map[string]bool, len(c.HITLApprovals))
		for k, v := range c.HITLApprovals {
			out.HITLApprovals[k] = v
		}
	}
	if c.AIFileSummaries != nil {
		out.AIFileSummaries = append([]map[string]interface{}(nil), c.AIFileSummaries...)
	}
	if c.EvaluationDetail != nil {
		out.EvaluationDetail = make(map[string]interface{}, len(c.EvaluationDetail))
		for k, v := range c.EvaluationDetail {
			out.EvaluationDetail[k] = v
		}
	}
	if c.DocumentDraft != nil {
		out.DocumentDraft = make(map[string]interface{}, len(c.DocumentDraft))
		for k, v := range c.DocumentDraft {
			out.DocumentDraft[k] = v
		}
	}
	return out
}

func (e *Engine) CreateCase(clientName, matterType, sourceChannel, initialMsg string) model.Case {
	e.mu.Lock()

	// Evict old cases if at capacity
	if len(e.cases) >= e.maxCases {
		e.evictOldest()
	}

	caseID := fmt.Sprintf("LAW-%s-%X", time.Now().Format("2006"), time.Now().UnixNano()%0xFFFFFF)

	c := &model.Case{
		CaseID:            caseID,
		ClientName:        clientName,
		MatterType:        matterType,
		SourceChannel:     sourceChannel,
		InitialMsg:        initialMsg,
		State:             StateClientCapture,
		Notes:             []model.Note{},
		UploadedDocuments: []string{},
		HITLApprovals:     make(map[string]bool),
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		NodeHistory:       []string{StateClientCapture},
	}

	e.cases[caseID] = c

	if initialMsg != "" {
		c.Notes = append(c.Notes, model.Note{
			Text:      fmt.Sprintf("Initial intake captured: %s", initialMsg),
			Timestamp: time.Now(),
		})
	}

	snapshot := deepCopyCase(c)
	e.mu.Unlock()

	if e.onChange != nil {
		go e.onChange()
	}

	return snapshot
}

// GetCaseSnapshot returns an independent copy safe to use after the lock is released
// (e.g. for JSON encoding without racing mutating goroutines).
func (e *Engine) GetCaseSnapshot(caseID string) (model.Case, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.cases[caseID]
	if !ok {
		return model.Case{}, false
	}
	return deepCopyCase(c), true
}

func (e *Engine) ListCases() []model.Case {
	e.mu.RLock()
	defer e.mu.RUnlock()

	cases := make([]model.Case, 0, len(e.cases))
	for _, c := range e.cases {
		cases = append(cases, deepCopyCase(c))
	}

	sort.Slice(cases, func(i, j int) bool {
		return cases[i].UpdatedAt.After(cases[j].UpdatedAt)
	})

	return cases
}

func (e *Engine) AdvanceState(caseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found: %s", caseID)
	}
	
	currentIdx := -1
	for i, state := range stateOrder {
		if state == c.State {
			currentIdx = i
			break
		}
	}
	
	if currentIdx < 0 || currentIdx >= len(stateOrder)-1 {
		return fmt.Errorf("cannot advance from state: %s", c.State)
	}
	
	nextState := stateOrder[currentIdx+1]
	
	// Check if next state is a HITL gate
	if hitlGates[nextState] && !c.HITLApprovals[nextState] {
		return fmt.Errorf("state %s requires HITL approval", nextState)
	}
	
	c.State = nextState
	c.UpdatedAt = time.Now()
	c.NodeHistory = append(c.NodeHistory, nextState)
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Advanced to state: %s", nextState),
		Timestamp: time.Now(),
	})
	
	if e.onChange != nil {
		go e.onChange()
	}
	
	return nil
}

func (e *Engine) ApproveHITL(caseID, state string, approved bool, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found: %s", caseID)
	}
	
	if !hitlGates[state] {
		return fmt.Errorf("state %s is not a HITL gate", state)
	}
	
	c.HITLApprovals[state] = approved
	c.UpdatedAt = time.Now()
	
	action := "Approved"
	if !approved {
		action = "Rejected"
	}
	label := hitlGateLabels[state]
	if label == "" {
		label = state
	}
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Human review — %s: %s — %s", label, action, reason),
		Timestamp: time.Now(),
	})
	
	if e.onChange != nil {
		go e.onChange()
	}
	
	return nil
}

func (e *Engine) AddNote(caseID, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	c, ok := e.cases[caseID]
	if !ok {
		return
	}
	
	c.Notes = append(c.Notes, model.Note{
		Text:      text,
		Timestamp: time.Now(),
	})
	c.UpdatedAt = time.Now()
	
	if e.onChange != nil {
		go e.onChange()
	}
}

// AttachDocument records an uploaded file on the case. Optional maps (first entry only) are merged into
// the corresponding ai_file_summaries row (e.g. classification from OCR+LLM).
func (e *Engine) AttachDocument(caseID, filename string, extras ...map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var merge map[string]interface{}
	if len(extras) > 0 {
		merge = extras[0]
	}

	c, ok := e.cases[caseID]
	if !ok {
		return
	}

	fn := rag.NormalizeLogicalName(filename)
	for _, f := range c.UploadedDocuments {
		if rag.NormalizeLogicalName(f) == fn {
			return
		}
	}

	c.UploadedDocuments = append(c.UploadedDocuments, fn)
	c.UpdatedAt = time.Now()
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Document uploaded: %s", fn),
		Timestamp: time.Now(),
	})

	// One row per uploaded file for clients/APIs that consume ai_file_summaries
	foundIdx := -1
	for i := range c.AIFileSummaries {
		if c.AIFileSummaries[i] == nil {
			continue
		}
		if sfn, _ := c.AIFileSummaries[i]["filename"].(string); rag.NormalizeLogicalName(sfn) == fn {
			foundIdx = i
			break
		}
	}
	now := time.Now().Format(time.RFC3339)
	if foundIdx >= 0 {
		c.AIFileSummaries[foundIdx]["added_at"] = now
		for k, v := range merge {
			c.AIFileSummaries[foundIdx][k] = v
		}
	} else {
		row := map[string]interface{}{
			"filename": fn,
			"source":   "upload",
			"added_at": now,
		}
		for k, v := range merge {
			row[k] = v
		}
		c.AIFileSummaries = append(c.AIFileSummaries, row)
	}

	if e.onChange != nil {
		go e.onChange()
	}
}

// DetachDocument removes a document from the case record.
func (e *Engine) DetachDocument(caseID, filename string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}

	fn := rag.NormalizeLogicalName(filename)
	
	// Remove from UploadedDocuments
	var newDocs []string
	for _, f := range c.UploadedDocuments {
		if rag.NormalizeLogicalName(f) != fn {
			newDocs = append(newDocs, f)
		}
	}
	c.UploadedDocuments = newDocs

	// Remove from AIFileSummaries
	var newSums []map[string]interface{}
	for _, s := range c.AIFileSummaries {
		if sfn, ok := s["filename"].(string); ok && rag.NormalizeLogicalName(sfn) == fn {
			continue // skip
		}
		newSums = append(newSums, s)
	}
	c.AIFileSummaries = newSums

	c.UpdatedAt = time.Now()
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Document removed: %s", fn),
		Timestamp: time.Now(),
	})

	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}

// SetAICaseSummary persists the latest generated case summary on the case record.
func (e *Engine) SetAICaseSummary(caseID, summary string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.AICaseSummary = summary
	c.UpdatedAt = time.Now()
	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}

func (e *Engine) UpdateCase(caseID, clientName, matterType string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}

	if clientName != "" {
		c.ClientName = clientName
	}
	if matterType != "" {
		c.MatterType = matterType
	}
	c.UpdatedAt = time.Now()
	
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Case info updated: %s (%s)", clientName, matterType),
		Timestamp: time.Now(),
	})

	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}

func (e *Engine) DeleteCase(caseID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cases[caseID]; !ok {
		return fmt.Errorf("case not found")
	}
	delete(e.cases, caseID)

	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}

func (e *Engine) evictOldest() {
	var oldestID string
	var oldestTime time.Time
	
	for id, c := range e.cases {
		if oldestTime.IsZero() || c.UpdatedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = c.UpdatedAt
		}
	}
	
	if oldestID != "" {
		delete(e.cases, oldestID)
	}
}

// SetDraftPreview stores the rendered draft text (markdown/plain) for a case.
func (e *Engine) SetDraftPreview(caseID, content string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.DraftPreview = content
	c.UpdatedAt = time.Now()
	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}

// SetDocumentDraft stores the structured draft (sections + highlights with evidence links).
func (e *Engine) SetDocumentDraft(caseID string, draft map[string]interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.DocumentDraft = draft
	c.UpdatedAt = time.Now()
	if e.onChange != nil {
		go e.onChange()
	}
	return nil
}
