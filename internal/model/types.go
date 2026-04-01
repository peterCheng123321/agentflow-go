package model

import "time"

type Case struct {
	CaseID             string                 `json:"case_id"`
	ClientName         string                 `json:"client_name"`
	MatterType         string                 `json:"matter_type"`
	SourceChannel      string                 `json:"source_channel"`
	InitialMsg         string                 `json:"initial_msg"`
	State              string                 `json:"state"`
	Notes              []Note                 `json:"notes"`
	UploadedDocuments  []string               `json:"uploaded_documents"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
	Evaluation         string                 `json:"evaluation,omitempty"`
	EvaluationDetail   map[string]interface{} `json:"evaluation_detail,omitempty"`
	DocumentDraft      map[string]interface{} `json:"document_draft,omitempty"`
	DraftPreview       string                 `json:"draft_preview,omitempty"`
	Highlights         []Highlight            `json:"highlights,omitempty"`
	HITLApprovals      map[string]bool        `json:"hitl_approvals,omitempty"`
	AICaseSummary      string                 `json:"ai_case_summary,omitempty"`
	AIFileSummaries    []map[string]interface{} `json:"ai_file_summaries,omitempty"`
	IsPaid             bool                   `json:"is_paid"`
	NodeHistory        []string               `json:"node_history"`
}

type Note struct {
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

type Highlight struct {
	Text       string `json:"text"`
	Category   string `json:"category"`
	Importance string `json:"importance"`
	Reason     string `json:"reason"`
	SourceFile string `json:"source_file,omitempty"`
	SourcePage *int   `json:"source_page,omitempty"`
}

type SearchResult struct {
	Filename  string  `json:"filename"`
	Chunk     string  `json:"chunk"`
	Score     float64 `json:"score"`
	MatchMode string  `json:"match_mode"`
}

type DocumentRecord struct {
	Filename         string                 `json:"filename"`
	Path             string                 `json:"path"`
	FileType         string                 `json:"file_type"`
	FileSizeBytes    int64                  `json:"file_size_bytes"`
	Chunks           []string               `json:"chunks"`
	PageTexts        []string               `json:"page_texts,omitempty"`
	TaggedChunks     []TaggedChunk          `json:"tagged_chunks,omitempty"`
	PDFMetadata      map[string]interface{} `json:"pdf_metadata,omitempty"`
	UserPreferences  map[string]string      `json:"user_preferences,omitempty"`
	AIMetadata       map[string]interface{} `json:"ai_metadata,omitempty"`
	IngestedAt       time.Time              `json:"ingested_at"`
}

type TaggedChunk struct {
	Text string `json:"text"`
	Page int    `json:"page"`
}

type ToolResult struct {
	Success  bool                   `json:"success"`
	Error    string                 `json:"error,omitempty"`
	Output   map[string]interface{} `json:"output,omitempty"`
	LatencyMs float64               `json:"latency_ms"`
}

type OrchestrationResult struct {
	Objective                string                   `json:"objective"`
	Plan                     map[string]interface{}   `json:"plan"`
	ToolRuns                 []map[string]interface{} `json:"tool_runs"`
	AggregatedContextPreview string                   `json:"aggregated_context_preview"`
	Synthesis                string                   `json:"synthesis"`
	RanAt                    string                   `json:"ran_at"`
}
