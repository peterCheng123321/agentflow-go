// Package doctype defines the catalogue of legal document types AgentFlow
// can generate — civil complaint, lawyer letter, evidence index, POA, etc.
//
// Each type owns its prompt and its canonical section schema. The generation
// endpoint dispatches on `DocType.ID`, builds a prompt via `Type.Prompt(ctx)`,
// and parses the LLM response into a `model.GeneratedDoc` with the type's
// section ordering.
//
// Why a registry + Go-file-per-type:
//   - Each Chinese filing type has its own conventions (起诉状 mandates 当事人 /
//     诉讼请求 / 事实与理由 / 证据; 律师函 is more free-form). Hardcoding all
//     of that in a single switch in handlers would be unreadable.
//   - The prompt is the document. Treating each type as one source file with
//     prompt + sections + required-fields keeps the calibration owned by
//     domain experts, not handler code.
package doctype

// DocType describes one generatable document type — its identity, the
// human labels we show, the case fields it requires before generation can
// proceed, and the prompt + section schema that drive the LLM call.
type DocType struct {
	// ID is the stable string used on the wire and in storage.
	// Examples: "complaint", "lawyer_letter", "evidence_index", "poa", "service_notice".
	ID string

	// LabelZH is the Chinese name shown in the UI ("起诉状").
	LabelZH string
	// LabelEN is the English name shown in the UI ("Civil Complaint").
	LabelEN string

	// Icon is the SF Symbol name used by the SwiftUI catalogue cards.
	Icon string

	// Description — one-line user-facing explanation of when to use this type.
	Description string

	// RequiredFields names the case attributes that must be set before this
	// type can be generated. The UI greys out the "Generate" button when any
	// required field is missing. Values are case-sensitive Go field names on
	// model.Case OR the synthetic keys "plaintiffs" / "defendants" which we
	// derive from the case's batch-analysis JSON when present.
	RequiredFields []string

	// Sections is the canonical ordering for this type. The LLM is instructed
	// to produce exactly these sections in this order; missing sections fall
	// to "材料未载明" rather than being omitted.
	Sections []SectionSpec

	// Prompt builds the type-specific Chinese prompt from the case + evidence.
	// Implementations should:
	//   - tell the LLM what role it's playing (中国执业律师助理)
	//   - provide the case meta + evidence excerpts (caller passes them in ctx)
	//   - require strict JSON output with the section IDs from Sections
	//   - require source citations on every claim of fact (highlight reasons)
	Prompt func(ctx PromptContext) string
}

// SectionSpec describes one canonical section of a document type.
type SectionSpec struct {
	// ID is the JSON key the LLM emits and the UI references.
	ID string
	// TitleZH is the Chinese section heading rendered into the .docx output.
	TitleZH string
	// Required: true → LLM must emit this section, even if content is "材料未载明".
	Required bool
	// Description is what the section should contain (used in the prompt to
	// disambiguate, not shown to the user).
	Description string
}

// PromptContext is the data the prompt builder receives. The caller (the
// generation handler) populates this from the live workflow.Case + the RAG
// search over the case's evidence corpus.
type PromptContext struct {
	// CaseID — for the generated doc's metadata.
	CaseID string
	// ClientName — the lawyer's client (typically the plaintiff).
	ClientName string
	// MatterType — canonical matter taxonomy (e.g. "Labor Dispute").
	MatterType string
	// State — current workflow stage; helps the prompt know what's expected.
	State string

	// Plaintiffs / Defendants — extracted parties from intake batch analysis,
	// or filled by the lawyer manually. Either may be empty; prompts should
	// instruct the LLM to use the case's evidence to fill in.
	Plaintiffs []string
	Defendants []string

	// EvidenceContext is a pre-truncated text blob — typically a concatenation
	// of the most relevant RAG chunks with their source filenames, ready to
	// drop into the prompt as `--- File: ... ---\n<text>\n` blocks. The
	// caller is responsible for ensuring this fits within the model's window.
	EvidenceContext string

	// EvidenceFiles is the list of filenames currently attached to the case,
	// independent of which ones surfaced via RAG. Some prompts (e.g. the
	// evidence-index type) iterate the full file list rather than just the
	// retrieved chunks.
	EvidenceFiles []string

	// InitialMsg is the case's opening intake message (free text from the
	// lawyer or the folder-intake summarised description). Often carries
	// facts not captured anywhere else.
	InitialMsg string
}
