package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agentflow-go/internal/config"
)

func TestCanonicalDocumentType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"resident_id_card", "resident_id_card", "resident_id_card"},
		{"wechat_chat_screenshot", "wechat_chat_screenshot", "wechat_chat_screenshot"},
		{"civil_complaint", "civil_complaint", "civil_complaint"},
		{"iou_debt_note", "iou_debt_note", "iou_debt_note"},
		{"court_form_other", "court_form_other", "court_form_other"},
		{"other", "other", "other"},
		{"empty string", "", ""},
		{"whitespace", "  other  ", "other"},
		{"invalid type", "invalid_type", ""},
		{"chinese text", "身份证", ""},
		{"partial match", "resident_id", ""},
		{"uppercase", "RESIDENT_ID_CARD", ""},
		{"spreadsheet_shipment_ledger", "spreadsheet_shipment_ledger", "spreadsheet_shipment_ledger"},
		{"power_of_attorney", "power_of_attorney", "power_of_attorney"},
		{"online_case_filing_confirmation", "online_case_filing_confirmation", "online_case_filing_confirmation"},
		{"printed_chat_evidence", "printed_chat_evidence", "printed_chat_evidence"},
		{"wechat_pay_receipt", "wechat_pay_receipt", "wechat_pay_receipt"},
		{"civil_petition_fragment", "civil_petition_fragment", "civil_petition_fragment"},
		{"household_registration_query_result", "household_registration_query_result", "household_registration_query_result"},
		{"litigation_service_address_confirmation", "litigation_service_address_confirmation", "litigation_service_address_confirmation"},
		{"litigation_fee_refund_account_form", "litigation_fee_refund_account_form", "litigation_fee_refund_account_form"},
		{"legal_statute_excerpt", "legal_statute_excerpt", "legal_statute_excerpt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalDocumentType(tt.input)
			if got != tt.want {
				t.Errorf("canonicalDocumentType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseClassificationResponse(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		wantOK bool
	}{
		{
			name:   "valid classification",
			raw:    `{"document_type": "civil_complaint", "display_name_zh": "民事起诉状", "confidence": "high", "summary_zh": "张三诉李四借款纠纷", "entities": {"plaintiffs": ["张三"]}}`,
			wantOK: true,
		},
		{
			name:   "with markdown fence",
			raw:    "```json\n{\"document_type\": \"other\", \"display_name_zh\": \"其他\", \"confidence\": \"low\", \"summary_zh\": \"材料信息不足\", \"entities\": {}}\n```",
			wantOK: true,
		},
		{
			name:   "empty entities",
			raw:    `{"document_type": "iou_debt_note", "display_name_zh": "借条", "confidence": "high", "summary_zh": "借款5000元", "entities": {}}`,
			wantOK: true,
		},
		{
			name:   "missing entities field",
			raw:    `{"document_type": "wechat_chat_screenshot", "display_name_zh": "微信截图", "confidence": "low", "summary_zh": "聊天记录"}`,
			wantOK: true,
		},
		{
			name:   "invalid json",
			raw:    "not json",
			wantOK: false,
		},
		{
			name:   "empty string",
			raw:    "",
			wantOK: false,
		},
		{
			name:   "partial json",
			raw:    `{"document_type": "civil_complaint"`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseClassificationResponse(tt.raw)
			if ok != tt.wantOK {
				t.Errorf("parseClassificationResponse() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if got.Entities == nil {
					t.Error("entities should not be nil after parsing")
				}
			}
		})
	}
}

func TestBuildClassificationPrompt(t *testing.T) {
	logicalName := "evidence_001.pdf"
	prompt := buildClassificationPrompt(logicalName)

	if !strings.Contains(prompt, logicalName) {
		t.Errorf("prompt should contain logical name %q", logicalName)
	}
	if !strings.Contains(prompt, "document_type") {
		t.Error("prompt should contain document_type field")
	}
	if !strings.Contains(prompt, "resident_id_card") {
		t.Error("prompt should list allowed document types")
	}
	if !strings.Contains(prompt, "civil_complaint") {
		t.Error("prompt should list civil_complaint type")
	}
}

func TestClassificationResponseNormalization(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "classify-norm-test-*")
	defer os.RemoveAll(tempDir)

	os.Setenv("AGENTFLOW_LLM_BACKEND", "dashscope")
	os.Setenv("AGENTFLOW_DASHSCOPE_API_KEY", "test-key")
	defer os.Unsetenv("AGENTFLOW_LLM_BACKEND")
	defer os.Unsetenv("AGENTFLOW_DASHSCOPE_API_KEY")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "{\"document_type\": \"CIVIL_COMPLAINT\", \"display_name_zh\": \"民事起诉状\", \"confidence\": \"HIGH\", \"summary_zh\": \"测试案件\"}"}}]}`)
	}))
	defer ts.Close()

	cfg := config.Load()
	cfg.DataDir = tempDir
	cfg.DashScopeBaseURL = ts.URL

	s := New(cfg)
	result := s.classifyLegalDocumentFromOCR("这是一份民事起诉状，原告张三诉被告李四合同纠纷一案，涉案金额五万元。", "test_file.pdf")

	if result == nil {
		t.Fatal("classification result should not be nil")
	}
	dt := result["document_type"].(string)
	if dt != "other" {
		t.Errorf("expected 'other' for invalid document_type 'CIVIL_COMPLAINT', got %q", dt)
	}
	conf := result["confidence"].(string)
	if conf != "high" {
		t.Errorf("expected 'high' confidence (normalized from 'HIGH'), got %q", conf)
	}
}

func TestClassificationEmptyText(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "classify-empty-*")
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		DataDir:        tempDir,
		IsAppleSilicon: false,
		OllamaURL:      "http://localhost:11434",
	}
	s := New(cfg)

	result := s.classifyLegalDocumentFromOCR("", "empty.txt")
	if result != nil {
		t.Error("classification of empty text should return nil")
	}

	result = s.classifyLegalDocumentFromOCR("   ", "whitespace.txt")
	if result != nil {
		t.Error("classification of whitespace-only text should return nil")
	}

	result = s.classifyLegalDocumentFromOCR("[OCR Error] nothing readable", "error.txt")
	if result != nil {
		t.Error("classification of OCR error text should return nil")
	}
}
