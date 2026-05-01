package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"agentflow-go/internal/config"
)

// defaultE2EDocsDir is the user's local case folder; CI/other machines skip if absent.
const defaultE2EDocsDir = "/Users/peter/Downloads/谢茂福 民间借贷"

func e2eDocsDir(t *testing.T) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv("AGENTFLOW_E2E_DOCS_DIR"))
	if dir == "" {
		dir = defaultE2EDocsDir
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		t.Skipf("E2E docs directory not available (set AGENTFLOW_E2E_DOCS_DIR): %v", err)
	}
	return dir
}

func e2eCollectDocPaths(t *testing.T, root string, max int) []string {
	t.Helper()
	// Raster images only: large PDFs can yield multi‑MB OCR text and make BM25 ingest + JSON persist pathological in tests.
	type sized struct {
		path string
		size int64
	}
	var found []sized
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".jpg", ".jpeg", ".png":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 6<<20 {
			return nil
		}
		found = append(found, sized{path, info.Size()})
		return nil
	})
	if len(found) == 0 {
		t.Fatal("no jpg/png under docs dir (PDFs excluded for E2E speed)")
	}
	sort.Slice(found, func(i, j int) bool { return found[i].size < found[j].size })
	if len(found) > max {
		found = found[:max]
	}
	out := make([]string, len(found))
	for i := range found {
		out[i] = found[i].path
	}
	return out
}

func e2eWriteChatCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	esc, _ := json.Marshal(content)
	_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + string(esc) + `}}]}`))
}

// e2eMockDashScope returns plausible assistant text/JSON for each pipeline stage.
func e2eMockDashScope() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		s := string(raw)

		const ocrSnippet = "民事起诉状。原告谢茂福，被告王某。诉讼请求：判令被告偿还借款本金人民币50000元及利息。" +
			"事实与理由：2023年1月双方成立民间借贷关系，原告以转账方式交付50000元，被告出具借条，后逾期未还。" +
			"此致某市某区人民法院。"

		switch {
		case strings.Contains(s, `"image_url"`):
			e2eWriteChatCompletion(w, ocrSnippet)
		case strings.Contains(s, "智能法律文书助手"):
			e2eWriteChatCompletion(w, `{"can_generate":true,"confidence":"high","reasoning":"材料包含当事人与借贷事实","missing_info":[],"suggested_searches":[],"questions_for_user":[],"proceed_anyway":true}`)
		case strings.Contains(s, "专业的法律案件分析助手"):
			// extractStructuredSummary (draft prep) — must be JSON, not OCR prose
			e2eWriteChatCompletion(w, `{"plaintiffs":["谢茂福"],"defendants":["王某"],"matter_type":"Loan Dispute","case_description":"民间借贷纠纷，主张偿还借款本金5万元。","key_facts":["双方成立借贷关系","被告逾期未还"],"key_amounts":["50000元"],"key_dates":["2023年"],"confidence":"high","reasoning":"材料信息一致","gaps_identified":[]}`)
		case strings.Contains(s, "专业的中国法律材料分析专家"):
			e2eWriteChatCompletion(w, `{"client_name":"谢茂福","matter_type":"Loan Dispute","plaintiffs":["谢茂福"],"defendants":["王某"],"files":[]}`)
		case strings.Contains(s, "法院立案与证据材料分类专家"):
			e2eWriteChatCompletion(w, `{"document_type":"wechat_chat_screenshot","display_name_zh":"证据截图","confidence":"high","summary_zh":"谢茂福主张借款5万元","entities":{}}`)
		case strings.Contains(s, "结构化案件摘要"):
			e2eWriteChatCompletion(w, "## 一、案件概述\n民间借贷。\n## 二、当事人与请求\n原告谢茂福请求还款。\n## 三、关键事实与证据\n借条与转账材料未完全展示。\n## 四、风险与后续建议\n核实原件。")
		case strings.Contains(s, "法律文书草稿"):
			e2eWriteChatCompletion(w, `{"title":"民事起诉状（草稿）","sections":[{"id":"s1","title":"事实与理由","content":"原告谢茂福诉称向被告出借人民币50000元，被告逾期未还。","highlights":[{"text":"50000元","reason":"金额需与借条核对","category":"amount","source_file":"材料","source_ref":"伍万元"}]}]}`)
		case strings.Contains(s, "法律材料信息抽取助手"):
			e2eWriteChatCompletion(w, `{"client_name":"谢茂福","matter_type":"Loan Dispute","confidence":"high"}`)
		default:
			e2eWriteChatCompletion(w, ocrSnippet)
		}
	}))
}

// TestE2EDocsUploadSummaryDraft runs upload → job wait → case lookup → summarize → draft generate
// against real files under AGENTFLOW_E2E_DOCS_DIR or defaultE2EDocsDir, with a mock OpenAI-compat server.
func TestE2EDocsUploadSummaryDraft(t *testing.T) {
	dir := e2eDocsDir(t)
	paths := e2eCollectDocPaths(t, dir, 6)

	t.Setenv("AGENTFLOW_LLM_BACKEND", "dashscope")
	t.Setenv("AGENTFLOW_DASHSCOPE_API_KEY", "e2e-test-key")
	t.Setenv("AGENTFLOW_LLM_CACHE", "0")
	t.Setenv("AGENTFLOW_MAX_CONCURRENT", "4")

	mock := e2eMockDashScope()
	t.Cleanup(mock.Close)

	cfg := config.Load()
	cfg.DataDir = t.TempDir()
	cfg.DashScopeBaseURL = mock.URL
	cfg.MaxConcurrent = 4

	srv := New(cfg)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		part, err := mw.CreateFormFile("files", filepath.Base(p))
		if err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if _, err := io.Copy(part, f); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		_ = f.Close()
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/upload/batch", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("batch upload: %d %s", rr.Code, rr.Body.String())
	}
	var up struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&up); err != nil || up.JobID == "" {
		t.Fatalf("job_id: %v body=%s", err, rr.Body.String())
	}

	// Poll s.mux (not Router): /v1/* on Router is rate-limited and rapid polling returns 429.
	deadline := time.Now().Add(25 * time.Minute)
	var jobBody []byte
	for time.Now().Before(deadline) {
		reqJ := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+up.JobID, nil)
		rrJ := httptest.NewRecorder()
		srv.mux.ServeHTTP(rrJ, reqJ)
		jobBody = rrJ.Body.Bytes()
		if rrJ.Code == http.StatusNotFound {
			// Jobs are removed shortly after completion; fall through to case-list discovery.
			break
		}
		if rrJ.Code != http.StatusOK {
			t.Fatalf("job poll: %d %s", rrJ.Code, string(jobBody))
		}
		var job struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(jobBody, &job)
		if job.Status == "completed" || job.Status == "failed" {
			if job.Status == "failed" {
				t.Fatalf("job failed: %s", string(jobBody))
			}
			break
		}
		time.Sleep(800 * time.Millisecond)
	}

	var jobFull struct {
		Result struct {
			CaseID string   `json:"case_id"`
			Count  int      `json:"count"`
			Failed []string `json:"failed"`
		} `json:"result"`
	}
	caseID := ""
	if err := json.Unmarshal(jobBody, &jobFull); err == nil {
		caseID = strings.TrimSpace(jobFull.Result.CaseID)
	}
	if caseID == "" {
		// Completed job may have been purged from s.jobs; list cases and pick the upload-routed case.
		reqL := httptest.NewRequest(http.MethodGet, "/v1/cases", nil)
		rrL := httptest.NewRecorder()
		srv.mux.ServeHTTP(rrL, reqL)
		if rrL.Code != http.StatusOK {
			t.Fatalf("list cases fallback: %d %s", rrL.Code, rrL.Body.String())
		}
		var list struct {
			Cases []struct {
				CaseID        string `json:"case_id"`
				ClientName    string `json:"client_name"`
				SourceChannel string `json:"source_channel"`
			} `json:"cases"`
		}
		if err := json.NewDecoder(rrL.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		for _, c := range list.Cases {
			if strings.Contains(c.ClientName, "谢茂福") || c.SourceChannel == "Upload" {
				caseID = c.CaseID
				break
			}
		}
		if caseID == "" && len(list.Cases) > 0 {
			caseID = list.Cases[len(list.Cases)-1].CaseID
		}
	}
	if caseID == "" {
		t.Fatalf("missing case_id (job body: %s)", string(jobBody))
	}
	t.Logf("batch job case_id=%s (uploaded=%d failed=%d)", caseID, jobFull.Result.Count, len(jobFull.Result.Failed))

	reqG := httptest.NewRequest(http.MethodGet, "/v1/cases/"+caseID, nil)
	rrG := httptest.NewRecorder()
	srv.Router().ServeHTTP(rrG, reqG)
	if rrG.Code != http.StatusOK {
		t.Fatalf("get case: %d %s", rrG.Code, rrG.Body.String())
	}
	var csnap struct {
		CaseID            string   `json:"case_id"`
		ClientName        string   `json:"client_name"`
		UploadedDocuments []string `json:"uploaded_documents"`
	}
	if err := json.NewDecoder(rrG.Body).Decode(&csnap); err != nil {
		t.Fatal(err)
	}
	if csnap.CaseID != caseID {
		t.Fatalf("case id mismatch: %q vs %q", csnap.CaseID, caseID)
	}
	if len(csnap.UploadedDocuments) == 0 {
		t.Fatal("expected at least one uploaded document on case")
	}
	t.Logf("case client=%q docs=%d", csnap.ClientName, len(csnap.UploadedDocuments))

	reqS := httptest.NewRequest(http.MethodPost, "/v1/cases/"+caseID+"/summarize", nil)
	rrS := httptest.NewRecorder()
	srv.Router().ServeHTTP(rrS, reqS)
	if rrS.Code != http.StatusOK {
		t.Fatalf("summarize: %d %s", rrS.Code, rrS.Body.String())
	}
	var sum struct {
		Summary string `json:"summary"`
	}
	if err := json.NewDecoder(rrS.Body).Decode(&sum); err != nil || sum.Summary == "" {
		t.Fatalf("summary response: %v body=%s", err, rrS.Body.String())
	}
	if !strings.Contains(sum.Summary, "案件") && !strings.Contains(sum.Summary, "借贷") {
		t.Logf("summary (snippet): %.200q", sum.Summary)
	}

	reqD := httptest.NewRequest(http.MethodPost, "/v1/cases/"+caseID+"/draft/generate", bytes.NewReader([]byte(`{"force":true}`)))
	reqD.Header.Set("Content-Type", "application/json")
	rrD := httptest.NewRecorder()
	srv.Router().ServeHTTP(rrD, reqD)
	if rrD.Code != http.StatusOK {
		t.Fatalf("draft generate: %d %s", rrD.Code, rrD.Body.String())
	}
	var draft map[string]interface{}
	if err := json.NewDecoder(rrD.Body).Decode(&draft); err != nil {
		t.Fatal(err)
	}
	title, _ := draft["title"].(string)
	if title == "" {
		t.Fatalf("draft missing title: %v", draft)
	}
	t.Logf("draft title=%q", title)

	reqDG := httptest.NewRequest(http.MethodGet, "/v1/cases/"+caseID+"/draft", nil)
	rrDG := httptest.NewRecorder()
	srv.Router().ServeHTTP(rrDG, reqDG)
	if rrDG.Code != http.StatusOK {
		t.Fatalf("draft get: %d %s", rrDG.Code, rrDG.Body.String())
	}
	var draftGet struct {
		DocumentDraft map[string]interface{} `json:"document_draft"`
	}
	if err := json.NewDecoder(rrDG.Body).Decode(&draftGet); err != nil || draftGet.DocumentDraft == nil {
		t.Fatalf("draft persisted: %v body=%s", err, rrDG.Body.String())
	}
}
