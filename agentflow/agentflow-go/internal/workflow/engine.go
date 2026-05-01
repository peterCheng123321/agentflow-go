package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"agentflow-go/internal/model"
	"agentflow-go/internal/rag"

	_ "modernc.org/sqlite"
)

const (
	StateClientCapture      = "CLIENT_CAPTURE"
	StateInitialContact     = "INITIAL_CONTACT"
	StateCaseEvaluation     = "CASE_EVALUATION"
	StateFeeCollection      = "FEE_COLLECTION"
	StateGroupCreation      = "GROUP_CREATION"
	StateMaterialIngestion  = "MATERIAL_INGESTION"
	StateDocumentGeneration = "DOCUMENT_GENERATION"
	StateClientApproval     = "CLIENT_APPROVAL"
	StateFinalPDFSend       = "FINAL_PDF_SEND"
	StateArchiveClose       = "ARCHIVE_CLOSE"
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
	mu         sync.RWMutex
	cases      map[string]*model.Case
	maxCases   int
	persistDir string
	onChange   func()
	db         *sql.DB
}

func NewEngine(maxCases int, persistDir string, onChange func()) *Engine {
	e := &Engine{
		cases:      make(map[string]*model.Case),
		maxCases:   maxCases,
		persistDir: persistDir,
		onChange:   onChange,
	}
	e.openDB()
	e.loadStore()
	return e
}

func (e *Engine) openDB() {
	if e.persistDir == "" {
		return
	}
	os.MkdirAll(e.persistDir, 0755)
	dbPath := filepath.Join(e.persistDir, "cases.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Printf("workflow: failed to open SQLite DB: %v", err)
		return
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cases (id TEXT PRIMARY KEY, data TEXT NOT NULL)`); err != nil {
		log.Printf("workflow: failed to create cases table: %v", err)
		_ = db.Close()
		return
	}
	e.db = db
}

func (e *Engine) Close() {
	if e.db != nil {
		_ = e.db.Close()
		e.db = nil
	}
}

func (e *Engine) loadStore() {
	if e.db != nil {
		e.loadFromSQLite()
		return
	}
	e.loadFromJSON()
}

func (e *Engine) loadFromSQLite() {
	rows, err := e.db.Query(`SELECT id, data FROM cases`)
	if err != nil {
		log.Printf("workflow: loadFromSQLite: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			continue
		}
		var c model.Case
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			log.Printf("workflow: corrupt case %s: %v", id, err)
			continue
		}
		e.cases[id] = &c
	}
}

func (e *Engine) loadFromJSON() {
	if e.persistDir == "" {
		return
	}
	storePath := filepath.Join(e.persistDir, "cases.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		return
	}
	var ps struct {
		Cases map[string]*model.Case `json:"cases"`
	}
	if err := json.Unmarshal(data, &ps); err != nil {
		return
	}
	if ps.Cases != nil {
		e.cases = ps.Cases
	}
}

// persistCaseLocked writes a single case to SQLite. Must be called while holding e.mu write lock.
func (e *Engine) persistCaseLocked(c *model.Case) {
	if e.db == nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	if _, err := e.db.Exec(`INSERT OR REPLACE INTO cases (id, data) VALUES (?, ?)`, c.CaseID, string(data)); err != nil {
		log.Printf("workflow: persistCase %s: %v", c.CaseID, err)
	}
}

// removeCaseLocked deletes a case from SQLite. Must be called while holding e.mu write lock.
func (e *Engine) removeCaseLocked(id string) {
	if e.db == nil {
		return
	}
	if _, err := e.db.Exec(`DELETE FROM cases WHERE id = ?`, id); err != nil {
		log.Printf("workflow: removeCase %s: %v", id, err)
	}
}

func deepCopyCase(c *model.Case) model.Case {
	out := *c
	out.Notes = append([]model.Note(nil), c.Notes...)
	out.UploadedDocuments = append([]string(nil), c.UploadedDocuments...)
	out.GeneratedDocs = cloneGeneratedDocs(c.GeneratedDocs)
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

func cloneGeneratedDocs(in []model.GeneratedDoc) []model.GeneratedDoc {
	if in == nil {
		return nil
	}
	out := make([]model.GeneratedDoc, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Sections = append([]model.DocSection(nil), in[i].Sections...)
		for j := range out[i].Sections {
			out[i].Sections[j].Highlights = append([]model.DocHighlight(nil), in[i].Sections[j].Highlights...)
		}
		out[i].Highlights = append([]model.DocHighlight(nil), in[i].Highlights...)
	}
	return out
}

func (e *Engine) CreateCase(clientName, matterType, sourceChannel, initialMsg string) model.Case {
	e.mu.Lock()

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

	e.persistCaseLocked(c)
	snapshot := deepCopyCase(c)
	e.mu.Unlock()

	if e.onChange != nil {
		go e.onChange()
	}

	return snapshot
}

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

	e.persistCaseLocked(c)

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

	e.persistCaseLocked(c)

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

	e.persistCaseLocked(c)

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
	exists := false
	for _, f := range c.UploadedDocuments {
		if rag.NormalizeLogicalName(f) == fn {
			exists = true
			break
		}
	}

	if !exists {
		c.UploadedDocuments = append(c.UploadedDocuments, fn)
		c.Notes = append(c.Notes, model.Note{
			Text:      fmt.Sprintf("Document uploaded: %s", fn),
			Timestamp: time.Now(),
		})
	}
	c.UpdatedAt = time.Now()

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

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}
}

func (e *Engine) DetachDocument(caseID, filename string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}

	fn := rag.NormalizeLogicalName(filename)

	var newDocs []string
	for _, f := range c.UploadedDocuments {
		if rag.NormalizeLogicalName(f) != fn {
			newDocs = append(newDocs, f)
		}
	}
	c.UploadedDocuments = newDocs

	var newSums []map[string]interface{}
	for _, s := range c.AIFileSummaries {
		if sfn, ok := s["filename"].(string); ok && rag.NormalizeLogicalName(sfn) == fn {
			continue
		}
		newSums = append(newSums, s)
	}
	c.AIFileSummaries = newSums

	c.UpdatedAt = time.Now()
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Document removed: %s", fn),
		Timestamp: time.Now(),
	})

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}

	return nil
}

func (e *Engine) SetAICaseSummary(caseID, summary string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.AICaseSummary = summary
	c.UpdatedAt = time.Now()

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}

	return nil
}

func (e *Engine) AppendGeneratedDoc(caseID string, doc model.GeneratedDoc) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return 0, fmt.Errorf("case not found")
	}
	version := 1
	for _, existing := range c.GeneratedDocs {
		if existing.DocType == doc.DocType && existing.Version >= version {
			version = existing.Version + 1
		}
	}
	now := time.Now()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}
	doc.UpdatedAt = now
	if doc.Status == "" {
		doc.Status = "draft"
	}
	doc.Version = version
	c.GeneratedDocs = append(c.GeneratedDocs, doc)
	c.UpdatedAt = now
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Generated document draft: %s v%d", doc.DocType, version),
		Timestamp: now,
	})
	e.persistCaseLocked(c)
	if e.onChange != nil {
		go e.onChange()
	}
	return version, nil
}

func (e *Engine) UpdateGeneratedDocStatus(caseID, docID, status string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	now := time.Now()
	for i := range c.GeneratedDocs {
		if c.GeneratedDocs[i].ID != docID {
			continue
		}
		c.GeneratedDocs[i].Status = status
		c.GeneratedDocs[i].UpdatedAt = now
		if status == "approved" {
			c.GeneratedDocs[i].ApprovedAt = &now
		}
		c.UpdatedAt = now
		e.persistCaseLocked(c)
		if e.onChange != nil {
			go e.onChange()
		}
		return nil
	}
	return fmt.Errorf("generated doc not found")
}

func (e *Engine) MarkGeneratedDocExported(caseID, docID, filename string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	now := time.Now()
	for i := range c.GeneratedDocs {
		if c.GeneratedDocs[i].ID != docID {
			continue
		}
		c.GeneratedDocs[i].Status = "exported"
		c.GeneratedDocs[i].ExportedFilename = filename
		c.GeneratedDocs[i].ExportedAt = &now
		c.GeneratedDocs[i].UpdatedAt = now
		c.UpdatedAt = now
		e.persistCaseLocked(c)
		if e.onChange != nil {
			go e.onChange()
		}
		return nil
	}
	return fmt.Errorf("generated doc not found")
}

func (e *Engine) UpdateGeneratedDocSection(caseID, docID, sectionID, content string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	now := time.Now()
	for i := range c.GeneratedDocs {
		if c.GeneratedDocs[i].ID != docID {
			continue
		}
		if c.GeneratedDocs[i].Status != "draft" {
			return fmt.Errorf("only draft status is editable")
		}
		for j := range c.GeneratedDocs[i].Sections {
			if c.GeneratedDocs[i].Sections[j].ID != sectionID {
				continue
			}
			c.GeneratedDocs[i].Sections[j].Content = content
			c.GeneratedDocs[i].UpdatedAt = now
			c.UpdatedAt = now
			e.persistCaseLocked(c)
			if e.onChange != nil {
				go e.onChange()
			}
			return nil
		}
		return fmt.Errorf("section not found")
	}
	return fmt.Errorf("generated doc not found")
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

	e.persistCaseLocked(c)

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
	e.removeCaseLocked(caseID)

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
		e.removeCaseLocked(oldestID)
	}
}

func (e *Engine) SetDraftPreview(caseID, content string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.DraftPreview = content
	c.UpdatedAt = time.Now()

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}

	return nil
}

func (e *Engine) SetDocumentDraft(caseID string, draft map[string]interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}
	c.DocumentDraft = draft
	c.UpdatedAt = time.Now()

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}

	return nil
}

func (e *Engine) AddDocumentToCase(caseID, filename string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cases[caseID]
	if !ok {
		return fmt.Errorf("case not found")
	}

	for _, existing := range c.UploadedDocuments {
		if existing == filename {
			return nil
		}
	}

	c.UploadedDocuments = append(c.UploadedDocuments, filename)
	c.UpdatedAt = time.Now()
	c.Notes = append(c.Notes, model.Note{
		Text:      fmt.Sprintf("Document added: %s", filename),
		Timestamp: time.Now(),
	})

	e.persistCaseLocked(c)

	if e.onChange != nil {
		go e.onChange()
	}

	return nil
}
