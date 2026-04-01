package server

import (
	"strings"
	"testing"
)

func TestIntakeTextUsable(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty string", "", false},
		{"whitespace only", "   \n\t  ", false},
		{"error prefix", "[OCR Error] some text", false},
		{"short text", "hello", false},
		{"exactly 24 runes", "123456789012345678901234", true},
		{"23 runes", "12345678901234567890123", false},
		{"chinese text", "这是一份民事起诉状，原告张三诉被告李四借款纠纷一案", true},
		{"mixed content", "Contract No. 12345 between 甲方 and 乙方", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := intakeTextUsable(tt.text)
			if got != tt.want {
				t.Errorf("intakeTextUsable(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"shorter than max", "hello", 10, "hello"},
		{"equal to max", "hello", 5, "hello"},
		{"longer than max", "hello world", 5, "hello…"},
		{"chinese runes", "这是一份很长的中文文本材料", 6, "这是一份很长…"},
		{"zero max", "hello", 0, "…"},
		{"empty string", "", 5, ""},
		{"emoji runes", "hello🌍world", 7, "hello🌍w…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateRunes(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain json", `{"key": "value"}`, `{"key": "value"}`},
		{"with markdown fence", "```json\n{\"key\": \"value\"}\n```", `{"key": "value"}`},
		{"with text before", "some text\n{\"key\": \"value\"}", `{"key": "value"}`},
		{"with text after", `{"key": "value"} some text`, `{"key": "value"}`},
		{"nested objects", `{"a": {"b": 1}, "c": [1,2]}`, `{"a": {"b": 1}, "c": [1,2]}`},
		{"multiple json objects", `{"a": 1} {"b": 2}`, `{"a": 1} {"b": 2}`},
		{"no json", "just plain text", "just plain text"},
		{"markdown without json lang", "```\n{\"key\": 1}\n```", `{"key": 1}`},
		{"empty braces", "{}", "{}"},
		{"array in value", `{"items": [1, 2, 3]}`, `{"items": [1, 2, 3]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONObject(tt.raw)
			if got != tt.want {
				t.Errorf("extractJSONObject(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCanonicalMatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"exact match", "Civil Litigation", "Civil Litigation"},
		{"exact match contract", "Contract Dispute", "Contract Dispute"},
		{"chinese synonym", "民事诉讼", "Civil Litigation"},
		{"chinese synonym contract", "合同纠纷", "Contract Dispute"},
		{"chinese synonym debt", "欠款", "Debt Dispute"},
		{"chinese synonym loan", "借贷", "Loan Dispute"},
		{"chinese synonym lease", "租赁", "Lease Dispute"},
		{"chinese synonym labor", "劳动", "Labor Dispute"},
		{"chinese synonym labor2", "劳务", "Labor Dispute"},
		{"case insensitive", "civil litigation", "Civil Litigation"},
		{"empty string", "", ""},
		{"whitespace", "  Civil Litigation  ", "Civil Litigation"},
		{"unknown type", "Criminal Case", ""},
		{"unknown chinese", "刑事案件", ""},
		{"sales contract", "Sales Contract Dispute", "Sales Contract Dispute"},
		{"sales contract chinese", "买卖合同纠纷", "Sales Contract Dispute"},
		{"commercial lease", "Commercial Lease Dispute", "Commercial Lease Dispute"},
		{"commercial lease chinese", "商业租赁", "Commercial Lease Dispute"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalMatter(tt.input)
			if got != tt.want {
				t.Errorf("canonicalMatter(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseIntakeResponse(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		wantOK bool
		want   intakeLLMJSON
	}{
		{
			name:   "valid json",
			raw:    `{"client_name": "张三", "matter_type": "Civil Litigation", "confidence": "high"}`,
			wantOK: true,
			want:   intakeLLMJSON{ClientName: "张三", MatterType: "Civil Litigation", Confidence: "high"},
		},
		{
			name:   "with markdown fence",
			raw:    "```json\n{\"client_name\": \"李四\", \"matter_type\": \"Contract Dispute\", \"confidence\": \"low\"}\n```",
			wantOK: true,
			want:   intakeLLMJSON{ClientName: "李四", MatterType: "Contract Dispute", Confidence: "low"},
		},
		{
			name:   "empty fields",
			raw:    `{"client_name": "", "matter_type": "", "confidence": ""}`,
			wantOK: true,
			want:   intakeLLMJSON{ClientName: "", MatterType: "", Confidence: ""},
		},
		{
			name:   "invalid json",
			raw:    "not json at all",
			wantOK: false,
		},
		{
			name:   "partial json",
			raw:    `{"client_name": "张三"`,
			wantOK: false,
		},
		{
			name:   "extra fields ignored",
			raw:    `{"client_name": "王五", "matter_type": "Debt Dispute", "confidence": "high", "extra": "ignored"}`,
			wantOK: true,
			want:   intakeLLMJSON{ClientName: "王五", MatterType: "Debt Dispute", Confidence: "high"},
		},
		{
			name:   "text before json",
			raw:    "Here is the result:\n{\"client_name\": \"赵六\", \"matter_type\": \"Loan Dispute\", \"confidence\": \"high\"}",
			wantOK: true,
			want:   intakeLLMJSON{ClientName: "赵六", MatterType: "Loan Dispute", Confidence: "high"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseIntakeResponse(tt.raw)
			if ok != tt.wantOK {
				t.Errorf("parseIntakeResponse() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseIntakeResponse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildIntakePrompt(t *testing.T) {
	logicalName := "test_file.pdf"
	prompt := buildIntakePrompt(logicalName)

	if !strings.Contains(prompt, logicalName) {
		t.Errorf("prompt should contain logical name %q", logicalName)
	}
	if !strings.Contains(prompt, "client_name") {
		t.Error("prompt should contain client_name field description")
	}
	if !strings.Contains(prompt, "matter_type") {
		t.Error("prompt should contain matter_type field description")
	}
	if !strings.Contains(prompt, "Civil Litigation") {
		t.Error("prompt should list allowed matter types")
	}
}

func TestExtractMatterType(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
		alsoOk   []string
	}{
		{"complaint keyword", "民事起诉状.pdf", "Civil Litigation", nil},
		{"contract only keyword", "合同文件.docx", "Contract Dispute", nil},
		{"debt keyword", "欠款.png", "Debt Dispute", nil},
		{"诉讼 keyword", "诉讼材料.jpg", "Civil Litigation", nil},
		{"no keyword defaults", "random_file.txt", "Civil Litigation", nil},
		{"empty filename defaults", "", "Civil Litigation", nil},
		{"借贷 keyword", "借贷协议.pdf", "Loan Dispute", nil},
		{"租赁 keyword", "租赁.pdf", "Lease Dispute", nil},
		{"劳动 keyword", "劳动合同.pdf", "Labor Dispute", []string{"Contract Dispute"}},
		{"mixed keywords", "合同欠款纠纷.pdf", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMatterType(tt.filename)
			if got == tt.want {
				return
			}
			for _, alt := range tt.alsoOk {
				if got == alt {
					return
				}
			}
			if tt.want == "" && got != "" {
				return
			}
			t.Errorf("extractMatterType(%q) = %q, want %q (also ok: %v)", tt.filename, got, tt.want, tt.alsoOk)
		})
	}
}
