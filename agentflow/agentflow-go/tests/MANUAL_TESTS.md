# AgentFlow Manual Test Checklist

This is the *manual* counterpart to `pytest tests/`. The automated suite covers the embedding sidecar, intent/matter routing accuracy, and DashScope-backed agent-chat routing. **Everything below requires a human in front of the Mac app** — UI layout, drag-and-drop, sheet behavior, keyboard shortcuts, persistence across launches, network-loss recovery, etc.

Last updated: 2026-05-01 (after Phase 1/3/4/5 of the embedding-spine cutover).

---

## How to use this document

- Run through **Section 0** (preconditions) before anything else.
- For a **release smoke test**, run the items marked **🔥 critical**. ~15 min.
- For a **full pre-release audit**, run everything. ~2-3 hours end-to-end.
- Mark `[x]` for pass, `[F]` for fail, leave `[ ]` for skipped/blocked.
- For any failure, open an issue with the test ID (e.g. `MT-3.4`) and reproduce-from-scratch steps.

Each test has:
- **Setup** — what state the app should be in before the test
- **Steps** — exact actions, numbered
- **Expected** — what should happen
- **Notes** — gotchas, what *not* to confuse with a bug

---

## 0. Preconditions

- [ ] **0.1** macOS 26 (Tahoe) or compatible
- [ ] **0.2** Apple Silicon machine (M-series) — embedding sidecar requires MLX
- [ ] **0.3** DashScope API key configured (`~/Library/Application Support/AgentFlow/secrets/dashscope_api_key.txt` exists and is non-empty). Without this, anything that calls the cloud LLM (chat, draft generation, intake synthesis, OCR) will fail; the embedding router will still work but the app is largely useless.
- [ ] **0.4** `mlx_lm.server` and `mlx-embeddings` installed (`pip3 list | grep -i mlx`). The bundled `agentflow-serve` shells out to these via the supervisor.
- [ ] **0.5** No competing process on ports `8000`, `8080`, `8090`, `8095`, `8195`, `8200`. Run: `lsof -i :8000 -i :8080 -i :8090 -i :8095 -i :8195 -i :8200`. Kill anything that's lingering from a previous dev session.
- [ ] **0.6** A test folder of mixed Chinese-legal documents (PDFs/DOCX/JPGs) for intake testing. ~5-10 files, ideally including: a 起诉状, a 合同 (contract), at least one image (JPG/PNG) for OCR, at least one DOCX, at least one filename that *doesn't* match the regex hint table (e.g. `工资欠付争议.pdf`).
- [ ] **0.7** A second test folder for the matter-type stress (filenames designed to exercise the embedding router): `工资争议.pdf`, `商铺租金未付.pdf`, `购销协议.docx`, etc.
- [ ] **0.8** A test PDF with content rich enough for RAG (multiple sections discussing different topics: liability, indemnification, force majeure, late fees, confidentiality). 5-10 pages.
- [ ] **0.9** Network ON for most tests; we'll explicitly drop it for failure-mode tests.
- [ ] **0.10** No active VPN that blocks `huggingface.co` or `dashscope.aliyuncs.com` (the user's `utun7` was breaking both during dev).

---

## 1. App launch & lifecycle

### 1.1 🔥 Cold launch
- **Setup**: Quit AgentFlow if running. `pkill -f AgentFlow`.
- **Steps**: Open `/Applications/AgentFlow.app` from Finder.
- **Expected**:
  1. App window appears within ~2s
  2. Sidebar shows "Overview" with the master case list (or empty state if first run)
  3. No spinning-wheel hang > 10s
  4. Backend log at `~/Library/Application Support/AgentFlow/agentflow-serve.log` shows a recent boot
  5. `curl http://127.0.0.1:8080/health` returns 200 OK
- **Notes**: First run after install may need to grant Python permissions for the embed sidecar (`mlx_lm.server`/`mlx_embed_server.py`). If the supervisor is missing those, the LLM router and embed router quietly stay disabled — app still works but routes everything through the slow path.

### 1.2 Re-launch with warm caches
- **Setup**: 1.1 passed; quit the app cleanly (Cmd+Q).
- **Steps**: Re-open `/Applications/AgentFlow.app`.
- **Expected**:
  1. UI ready in < 1s (data cached)
  2. Backend log shows model weights load in < 5s (HF cache hit)
  3. Embed sidecar marked ready in `/health` (`embed_router.ready: true`)
- **Notes**: First inference after a cold launch always pays the per-process JIT cost on MLX. That's ~100-200ms one-shot, after which subsequent calls drop to ~10ms.

### 1.3 Force quit + relaunch (crash recovery)
- **Setup**: App running.
- **Steps**:
  1. `kill -KILL $(pgrep -f AgentFlow)` (simulates crash)
  2. Re-open the app
- **Expected**: Boots cleanly, no "previous session crashed" dialog, no orphaned `agentflow-serve` or `mlx_lm.server` / `mlx_embed_server.py` from the previous run (`pgrep -lf mlx` should return only the new ones).
- **Notes**: If you see two `mlx_embed_server.py` PIDs, that's a leak — file a bug.

### 1.4 System sleep / wake
- **Setup**: App running, on a case.
- **Steps**:
  1. Close the lid (or `pmset sleepnow`)
  2. Wait 60+ seconds
  3. Wake the machine
  4. Click around the case
- **Expected**: WebSocket reconnects automatically; UI continues to work; no "backend unavailable" banner that doesn't clear.

### 1.5 Network drop & restore
- **Setup**: App running.
- **Steps**:
  1. System Settings → Wi-Fi → Off
  2. In the AI Inspector, send a chat message ("hello")
  3. Wait for the failure response
  4. Wi-Fi back on
  5. Send another message
- **Expected**:
  1. Step 3: error reply, not a hang. Conversation does NOT silently fail.
  2. Step 5: works. The embedding router still classified during the offline period (it's local) — but the cloud chat completion failed and was surfaced.
- **Notes**: This is the right place to verify the "don't hallucinate without retrieval" guarantee — when the cloud is offline, the app should refuse rather than make things up.

---

## 2. Sidebar & navigation

### 2.1 🔥 Sidebar populates with cases
- **Setup**: Cold launch.
- **Steps**: Look at the sidebar.
- **Expected**: Cases listed, sorted (verify the order — newest first by default?). Each row shows: client name, matter type, state, most-recent-activity timestamp.

### 2.2 Search filter
- **Setup**: At least 5 cases in the sidebar.
- **Steps**: Type into the search field.
- **Expected**: Live-filters as you type. Searches client name + matter type + case ID. Esc clears.

### 2.3 Today / All filter toggle
- **Setup**: Cases of varying ages.
- **Steps**: Toggle between "All" and "Today" filters.
- **Expected**: "Today" shows only cases with activity today; "All" shows everything.

### 2.4 Refresh button
- **Setup**: App running. Modify a case via `curl POST /v1/cases/...` or the UI in another window.
- **Steps**: Click the refresh icon in the sidebar.
- **Expected**: Sidebar reflects the change. WebSocket should auto-update too — the refresh is a fallback.

### 2.5 Click case → navigate to hub
- **Setup**: Sidebar has cases.
- **Steps**: Click a case row.
- **Expected**: Right pane switches to Case Hub for that case. Sidebar row shows selection state.

### 2.6 Cmd+[ / overview navigation
- **Setup**: Inside a case hub.
- **Steps**: Press Cmd+[ (or click the back arrow in the toolbar).
- **Expected**: Returns to the master overview. Selection clears.

### 2.7 Sidebar gear → Settings
- **Steps**: Click the gear icon in the sidebar (or press Cmd+,).
- **Expected**: Settings sheet appears.

---

## 3. New matter wizard

### 3.1 🔥 Create a basic matter
- **Steps**:
  1. Cmd+N (or sidebar "New Matter" button)
  2. Enter client name "Test Client 001"
  3. Pick matter type "Civil Litigation"
  4. Leave "initial message" empty
  5. Submit
- **Expected**: Sheet closes; new case appears in sidebar; right pane shows its hub.

### 3.2 Required-field validation
- **Steps**: Open New Matter, leave client name blank, try to submit.
- **Expected**: Submit button disabled, OR error inline. No empty-string client created.

### 3.3 Matter type dropdown
- **Steps**: Open the matter type picker.
- **Expected**: All 8 types listed:
  - Civil Litigation
  - Contract Dispute
  - Sales Contract Dispute
  - Debt Dispute
  - Loan Dispute
  - Lease Dispute
  - Labor Dispute
  - Commercial Lease Dispute
- **Notes**: This is the same `AllowedMatterTypes` set in `internal/llmutil/intake.go:16`. If you see fewer options or a different one, the UI and backend are out of sync.

### 3.4 Optional initial message
- **Steps**: Create a matter with an initial message ("client called about late rent").
- **Expected**: Message appears in the activity log on the new case's hub.

### 3.5 Cancel
- **Steps**: Open New Matter, fill fields, hit Cancel or Esc.
- **Expected**: Sheet closes; no new case created; sidebar unchanged.

### 3.6 Chinese client name
- **Steps**: Create a matter with client name "张三建筑公司" and matter type "Sales Contract Dispute".
- **Expected**: Created cleanly. NFC-normalized (the backend normalizes via `rag.NormalizeLogicalName`).

---

## 4. Quick intake from folder

### 4.1 🔥 Folder intake — happy path (5 documents)
- **Setup**: Test folder from precondition 0.6.
- **Steps**:
  1. Cmd+Shift+O (or sidebar "Quick Intake")
  2. Pick the folder
  3. Wait for OCR + synthesis
  4. Review the proposed metadata (client name, matter type, file list)
  5. Click Commit
- **Expected**:
  1. Streaming progress events render in real-time (`extract` → `synthesizing` → `done`)
  2. Client name auto-suggested from doc content
  3. Matter type auto-classified — for filenames like `工资欠付争议.pdf`, this should be Labor Dispute (this is the new embedding-router behavior; previously it would default to Civil Litigation)
  4. Total time < 30s for 5 docs (OCR is parallel up to 12 workers)
  5. After commit, case appears in sidebar with all docs attached
- **Notes**: If matter type is wrong, check `tests/data/matter_eval.py` for whether a similar filename is in the test set; if not, add it and the corresponding exemplar to `internal/llmutil/matter_router.go:MatterCorpus`.

### 4.2 Folder intake — Chinese filenames not matching keyword hints
- **Setup**: Folder of files named `工资争议.pdf`, `商铺租金未付.docx`, `购销协议.pdf`.
- **Steps**: Run quick intake.
- **Expected**: Each file's classified matter type matches its filename — Labor Dispute, Commercial Lease Dispute, Sales Contract Dispute respectively. **NOT** all defaulting to Civil Litigation (that was the pre-Phase-5 behavior).

### 4.3 Folder with PDFs requiring OCR
- **Setup**: Folder with at least one scanned-image PDF or photo (.jpg).
- **Steps**: Run intake.
- **Expected**: OCR fires; per-file progress shown. OCR takes longer than text extract — that's expected.

### 4.4 Empty folder
- **Setup**: An empty folder.
- **Steps**: Pick it.
- **Expected**: Either a clear error / nothing-to-do dialog, OR a no-op. Should NOT create a phantom empty case.

### 4.5 Folder with non-supported types only
- **Setup**: Folder containing only `.exe` / `.zip` / `.mp4`.
- **Steps**: Pick it.
- **Expected**: Skipped files are clearly listed as such. The intake either prompts user with "no usable files" OR creates a case with empty file list and a warning.

### 4.6 Cancel mid-intake
- **Setup**: Folder of 10+ files, intake in progress at the OCR stage.
- **Steps**: Click Cancel.
- **Expected**: Progress stops; no half-baked case created; staging directory cleaned up. Verify with `ls ~/Library/Application Support/AgentFlow/staging/` — no leftover `intake-*` dir for the cancelled run.

### 4.7 Network failure mid-intake
- **Setup**: Intake in progress at the synthesis (LLM) stage.
- **Steps**: Disable Wi-Fi.
- **Expected**: Clear error; staged data preserved so user can retry once network returns. Should NOT silently swallow the failure.

### 4.8 Edit metadata before commit
- **Steps**: After staging, change the auto-suggested client name and matter type, THEN commit.
- **Expected**: Case is created with the user-edited values, not the auto-suggestions.

### 4.9 Re-intake the same folder
- **Steps**: Run intake on a folder, then run it again on the same folder.
- **Expected**: Second run is faster — OCR cache hits (`ocr_cache.db`). No duplicate case unless user explicitly creates one.

---

## 5. Case Hub — six tabs

### 5.1 🔥 Tab switcher
- **Setup**: Open any populated case.
- **Steps**: Click each tab in turn: Summary / Evidence / 成果 / Documents / Activity / Research.
- **Expected**:
  1. Each tab loads in < 500ms
  2. No tab shows a permanent loading spinner
  3. No tab is missing or empty when it shouldn't be

### 5.2 Summary tab
- **Steps**: Open Summary on a case with documents and activity.
- **Expected**:
  1. Next-step guidance card (e.g. "Awaiting HITL approval", "Ready to draft")
  2. Action buttons appropriate to the current state
  3. AI-generated case summary if present (set via `POST /v1/cases/.../summarize`)

### 5.3 Evidence tab
- **Steps**: Click on a doc.
- **Expected**: Document viewer opens with extracted text, OCR overlay if image.

### 5.4 成果 (Outputs) tab
- **Steps**: Open a case with at least one approved/exported draft.
- **Expected**: Generated docs listed with status (draft / approved / exported), version, last-export timestamp.

### 5.5 Documents tab
- **Steps**:
  1. Open the tab
  2. Click "Generate" (if affordance present)
  3. Pick a doc type
- **Expected**: Doc generation kicks off; loading state shown; result appears within ~30s. New entry in the case's `GeneratedDocs` list.

### 5.6 Activity tab
- **Steps**: View recent activity.
- **Expected**: Chronological list of mutations (case created, doc added, note added, state advanced). Includes both manual and AI-driven changes.

### 5.7 Research tab — see Section 7 below.

### 5.8 Hero bar buttons
- **Steps**: Try each button in the hero bar: Research / Upload files / Draft / Export case packet / Delete.
- **Expected**:
  - Research → focuses Research tab
  - Upload files → file picker
  - Draft → opens draft generation flow
  - Export → packet ZIP download
  - Delete → confirmation sheet (destructive!)

### 5.9 Delete a case
- **Steps**:
  1. Hero bar → Delete
  2. Confirm
- **Expected**: Confirmation modal explicitly mentions destruction; case removed from sidebar; data dir cleaned (case file gone, attached docs unhooked from the RAG index — verify with `curl /v1/rag/summary` showing chunk count decrease).

### 5.10 Add a note
- **Steps**: Activity tab (or hero) → Add note → text → save.
- **Expected**: Note appears with timestamp; persists across app relaunch.

---

## 6. Document viewer & editor

### 6.1 🔥 View a PDF
- **Steps**: Click a PDF in Evidence.
- **Expected**: Inline view with text + page navigation. Extracted text accessible (Cmd+F search works).

### 6.2 View a DOCX
- **Steps**: Click a DOCX in Evidence.
- **Expected**: Renders as text (formatting may be lossy, that's OK).

### 6.3 View an image (OCR)
- **Steps**: Click a JPG/PNG.
- **Expected**: Image displayed; below it, the OCR-extracted text. Cmd+F searches the OCR text.

### 6.4 Edit a generated doc section inline
- **Setup**: Generate a draft (via the Draft button), then open it.
- **Steps**:
  1. Click into a section's text
  2. Edit it
  3. Save
- **Expected**: Edit persists; section status now shows "user-edited"; activity log records the change.

### 6.5 AI-refine a section
- **Steps**:
  1. In an open generated doc, select a section
  2. Click "Refine" (or similar affordance)
  3. Type an instruction ("more concise", or "add legal citation to Section 12")
  4. Submit
- **Expected**: New version of that section replaces the old one; latency 5-15s (cloud LLM).

### 6.6 Approve a generated doc
- **Steps**: In a draft doc, click Approve.
- **Expected**: Status moves to "approved"; export buttons enabled.

### 6.7 Export to .docx
- **Steps**: After approve, click Export.
- **Expected**: System Save dialog → file written → opens cleanly in Word/Pages.

### 6.8 Delete a doc from a case
- **Steps**: Long-press / right-click a doc in Evidence → Delete.
- **Expected**: Doc removed; RAG chunks for that doc dropped (verify by searching for unique text from the deleted doc — should NOT return results).

---

## 7. Research / AI Inspector (the chat agent)

### 7.1 🔥 Open AI Inspector via Research tab
- **Steps**: Open a case → click Research tab.
- **Expected**: Chat interface with:
  - Conversation pane
  - Input box
  - Toggles: Docs (RAG on/off), Tools (agent mode on/off)
  - Model picker dropdown
  - Quick action buttons (Case brief, Missing evidence, Draft document, Risk scan, Timeline)

### 7.2 Open AI Inspector in standalone window
- **Steps**: Cmd+Option+L (or the expand icon in the Research tab).
- **Expected**: Separate window opens, scoped to the same case. Closing it returns focus to the main window.

### 7.3 🔥 Send a conversational greeting (fast path)
- **Steps**: With an EMPTY case selected (or no case): send "hi" or "你好".
- **Expected**:
  1. Reply within ~1.5s (much faster than tool-calling responses)
  2. In dev tools / backend log, `[embed-router] margin XXX > 0.05` — fast path fired
  3. Reply field shows: in English "Hi" → "Operational. Ready to assist with legal tasks." (or similar terse line); in Chinese "你好" → Chinese reply
- **Notes**: This validates the embedding-spine production cutover (Phase 3). If the response takes 3+ seconds with tool-call traces, the fast path didn't fire — investigate.

### 7.4 Send a tool-call request (slow path)
- **Steps**: Send "list my pending cases" (no case context).
- **Expected**:
  1. Reply takes 2-5s
  2. Tool-call trace shown: `list_cases(filter="all")` → result count
  3. Reply summarizes the tool result
  4. Backend log: NOT `fast_path_conversational`; instead `complete` with `steps: 1`

### 7.5 Send a RAG question (slow path with retrieval)
- **Setup**: Case with attached documents (from precondition 0.8).
- **Steps**: Send "summarize the indemnification clauses".
- **Expected**:
  1. Reply cites specific text from the documents
  2. RAG retrieval fires — backend log shows `[rag]` activity, mode=`hybrid`
  3. Without dropping the backend's RAG index, NO hallucinated content (replies references real clauses)

### 7.6 Capability question (CONVERSATIONAL — was a known edge case)
- **Steps**: Send "What can you help me with?" or "你能做什么？" (no case context).
- **Expected**: Routed to fast path. Reply is a high-level capability description, NOT an attempt to call tools or generate code.
- **Notes**: This was the persistent failure mode in the LLM-router era; embedding router gets it right.

### 7.7 Tools toggle off
- **Steps**: Disable Tools toggle. Send "list my cases".
- **Expected**: Goes through fast path (or chat without agent loop). The model may explain it can't do that or list from session context only — should NOT hang.

### 7.8 Docs (RAG) toggle off
- **Steps**: Disable Docs toggle. Send a RAG question.
- **Expected**: Reply is generic, not based on uploaded documents.

### 7.9 Pick a different model
- **Steps**: Use the model picker in the Research tab (or Cmd+Option+M). Pick a different model. Send a query.
- **Expected**: Reply comes back; backend log shows the new model ID. Latency may differ.

### 7.10 Quick action buttons
- **Steps**: Click each: Case brief / Missing evidence / Draft document / Risk scan / Timeline.
- **Expected**: Each pre-fills a templated query and runs it. Results are case-specific (use `case_id`).

### 7.11 Tool-call trace UI
- **Setup**: Send a tool-using prompt (e.g. "create a new matter for ACME Corp").
- **Expected**: Trace shows: tool name, parameters, result (or error). User can read what the agent did, not just the final reply.
- **Notes**: This is the key affordance that makes the app feel agentic. If traces are missing, the user has no way to verify what mutated.

### 7.12 Clear conversation
- **Steps**: Click Clear (or similar).
- **Expected**: Conversation pane empties; next message is fresh context.

### 7.13 Long-running tool call (doc generation)
- **Setup**: Case with documents.
- **Steps**: Send "draft a complaint based on the evidence".
- **Expected**: Visible progress (loading spinner, partial output, or step-by-step trace); completes in 30-60s. Result is a new generated doc visible in the Documents tab.

### 7.14 Conversation in case context
- **Setup**: Open a case (case_id selected).
- **Steps**: In the Research tab, send "hi" (greeting).
- **Expected**: NOT routed via fast path — case context disables the chitchat fast-path so the agent has tools available. (See `handlers_agent.go`: fast-path is gated on empty case_id.)

### 7.15 Chinese conversation in case context
- **Steps**: Same as 7.14 but with "你好" and a follow-up tool query in Chinese.
- **Expected**: Reply in Chinese; tools fire correctly on Chinese instructions ("把这个案件标记为已结案").

---

## 8. Settings

### 8.1 🔥 Open Settings
- **Steps**: Cmd+, (or sidebar gear icon).
- **Expected**: Settings sheet with API key fields, model overrides.

### 8.2 Update DashScope API key
- **Steps**:
  1. Open Settings
  2. Paste a different DashScope API key
  3. Save
- **Expected**:
  1. Backend restarts (UI may show "Reconnecting..." briefly)
  2. After restart, `/health` returns 200 with the new key in effect
  3. A new chat message succeeds

### 8.3 Empty key validation
- **Steps**: Clear the DashScope key, hit Save.
- **Expected**: Inline validation error OR Save disabled. App should not boot a backend that immediately log.Fatal's.

### 8.4 Model override
- **Steps**: Set `AGENTFLOW_MODEL` to a non-default model (e.g. `qwen-turbo`); save.
- **Expected**: Backend restarts with the new model; chat queries use it; `/health` reports it.

### 8.5 Cancel without save
- **Steps**: Open Settings, modify a field, hit Cancel.
- **Expected**: Settings sheet closes without applying changes.

---

## 9. Model picker

### 9.1 Cmd+Option+M opens picker
- **Steps**: Press Cmd+Option+M.
- **Expected**: Sheet appears; models listed grouped by provider (Anthropic, OpenAI, Google, Auto Router, DashScope·Qwen, DeepSeek, Ollama·Local, Other).

### 9.2 Search filter
- **Steps**: Type "qwen" in the search.
- **Expected**: Live-filters to Qwen-family models.

### 9.3 Select a model
- **Steps**: Click a model.
- **Expected**: Sheet closes; selection persisted (`AppStorage("af.model")`); used for next chat.

### 9.4 Model selection survives restart
- **Steps**: Pick a model. Quit the app. Re-open.
- **Expected**: Same model still selected.

---

## 10. Embedding-spine specific tests (the new infrastructure)

### 10.1 🔥 /health surfaces embed-router status
- **Steps**: `curl http://127.0.0.1:8080/health | jq`.
- **Expected**: Output contains:
  ```json
  "embed_router": {
    "enabled": true,
    "ready": true,
    "model": "mlx-community/multilingual-e5-small-mlx",
    "base_url": "http://127.0.0.1:8090",
    "corpus_loaded": true
  }
  ```
  (`corpus_loaded` flips to `true` after the first /v1/agent/chat call due to lazy init.)

### 10.2 Sidecar process is supervised
- **Setup**: App running.
- **Steps**:
  1. `pgrep -lf mlx_embed_server.py` — note the PID
  2. `kill -KILL <PID>`
  3. Wait 10s
  4. `pgrep -lf mlx_embed_server.py` — should show a NEW PID
- **Expected**: Supervisor restarted the child with exponential backoff. The brand-new process should be ready within ~10s (warm cache). `/health` `embed_router.ready` flips false → true.

### 10.3 Confidence gate escalates on borderline queries
- **Steps**: Send the agent: "What does the evidence say about that thing?" (deliberately vague).
- **Expected**: In backend log, search for `[embed-router] margin X.XXX < 0.050 — escalating to safe path`. The query should go through the slow agent path, not the fast one.
- **Notes**: This validates the production confidence-gating defense. Without it, borderline queries silently route incorrectly.

### 10.4 Embed router survives many requests without state pollution
- **Steps**: Send 30+ chat messages in a row, varying content (greetings, tool requests, RAG questions).
- **Expected**: Routing remains correct throughout. No degradation of accuracy after the 5th-10th request (this was the LLM-router bug we hit during dev — the embedding router doesn't have that failure mode but worth verifying).

### 10.5 Dense RAG vs BM25 (semantic match)
- **Setup**: Case with a document containing the phrase "indemnification" but NOT "responsible for damages".
- **Steps**:
  1. Send: "who is responsible for damages?"
- **Expected**:
  1. Reply cites the indemnification clause (semantic match, not lexical)
  2. Backend log shows `mode=hybrid` not `bm25`
  3. The response includes the relevant chunk from the document
- **Notes**: This is the BM25→hybrid quality lift from Phase 4. Pre-Phase-4, this query would miss because "responsible for damages" doesn't share keywords with "indemnify".

### 10.6 RAG hybrid mode in /v1/rag/summary
- **Steps**: `curl http://127.0.0.1:8080/v1/rag/summary | jq`.
- **Expected**:
  ```json
  {
    "document_count": N,
    "total_chunks": M,
    "chunks_with_dense": M,
    "backend_mode": "hybrid"
  }
  ```
  If `chunks_with_dense < total_chunks`, the backfill is still in progress (or never ran). Wait or check logs for `[rag] backfilling`.

### 10.7 Sidecar down → graceful BM25-only fallback
- **Setup**: Stop the embed sidecar (`pkill -KILL mlx_embed_server`). Disable supervisor restart by setting `AGENTFLOW_EMBED_ROUTER_ENABLED=0` and restarting the backend.
- **Steps**:
  1. Send a chat → should still work (slow path everywhere — no fast path available)
  2. Run a RAG query → should work, mode=`bm25`
- **Expected**: System degrades gracefully. No 500s; no hanging.
- **Notes**: After this test, RE-ENABLE the embed router or you'll be running degraded.

### 10.8 Backfill runs after first install
- **Setup**: Fresh user with existing case data but no `rag_embeddings.bin` (e.g. upgrade from pre-Phase-4 version).
- **Steps**: Boot the app. Watch the backend log.
- **Expected**: Within ~30s of boot, log line `[rag] backfilling N embeddings (of M total chunks)` appears, then `[rag] backfill complete: N new embeddings`. The first RAG query may run BM25-only; subsequent queries hit hybrid.

---

## 11. Persistence

### 11.1 🔥 Created cases survive relaunch
- **Steps**:
  1. Create case "Test Persistence 1"
  2. Quit app
  3. Re-open
- **Expected**: Case still in sidebar, with all its data.

### 11.2 Generated docs persist
- **Steps**:
  1. Generate a draft on a case
  2. Approve it
  3. Quit, re-launch
- **Expected**: Doc still listed, status still "approved", content preserved.

### 11.3 Notes persist
- **Steps**: Add a note → quit → relaunch.
- **Expected**: Note still there, with original timestamp.

### 11.4 RAG embeddings persist
- **Steps**:
  1. Ingest a folder
  2. Note `chunks_with_dense` from `/v1/rag/summary`
  3. Quit app
  4. Re-open
  5. Check `/v1/rag/summary` again
- **Expected**: `chunks_with_dense` unchanged; no re-embedding round-trip on launch (visible in log: NO `[rag] backfilling` line).

### 11.5 OCR cache persists
- **Steps**:
  1. Run intake on a folder; note time
  2. Quit, re-launch
  3. Re-run intake on the SAME folder
- **Expected**: Second run is materially faster (most/all OCR results cached at `data/ocr_cache.db`).

### 11.6 Settings survive relaunch
- **Steps**: Change model in Settings → Save → quit → relaunch.
- **Expected**: Same model still selected.

---

## 12. Edge cases

### 12.1 Empty input
- **Steps**: Send an empty chat message.
- **Expected**: Submit button disabled OR clear "no content" feedback. No backend call.

### 12.2 Very long chat input (10K+ chars)
- **Steps**: Paste a 10K-char message.
- **Expected**: Either truncated with notice, or sent successfully (cloud model handles it). No client-side hang.

### 12.3 Special characters
- **Steps**: Send "💼📄⚖️ test 🇨🇳"; send `<script>alert(1)</script>`; send strings with quotes/backslashes.
- **Expected**: Renders correctly (no XSS); backend accepts them (no JSON parse errors).

### 12.4 Rapid-fire messages
- **Steps**: Send 5 chat messages within 2 seconds.
- **Expected**: All eventually answered (in order, or marked as queued). UI doesn't freeze. No race conditions in the state.

### 12.5 Multiple cases open across windows
- **Steps**: Open the standalone chat (Cmd+Option+L) for one case; main window on a different case.
- **Expected**: Both work independently. No cross-contamination of conversation history.

### 12.6 Chinese-only conversation
- **Steps**: Run an entire 5-message conversation in Chinese.
- **Expected**: Replies always in Chinese. Tool calls work on Chinese inputs.

### 12.7 Code-switched message
- **Steps**: Send "case 12345 的 deadline 是什么".
- **Expected**: Routed to NEEDS_TOOLS; tool fires; reply mixes English/Chinese as appropriate.

### 12.8 Ambiguous prompt
- **Steps**: Send "tell me about that".
- **Expected**: Either asks for clarification, OR escalates to safe path (the embed router's confidence gate should fire). Should NOT hallucinate a specific case.

### 12.9 Restart while a job is running
- **Setup**: Folder intake in progress (synthesis stage).
- **Steps**: Force-quit the app.
- **Expected**: On relaunch, no zombie staging dir; UI reflects no-in-progress.

### 12.10 Disk-full simulation (optional, manual)
- **Setup**: Fill the disk to within ~50MB free.
- **Steps**: Run an intake; generate a draft.
- **Expected**: Clear error surfaced to user (not a silent fail). After freeing space, system recovers.

---

## 13. Performance feel

These are perceptual tests — measure with a stopwatch and document any degradation.

| Action | Bar | Notes |
|---|---|---|
| App cold launch to ready sidebar | < 5s | with warm HF cache |
| Re-launch (caches warm) | < 1.5s | |
| Click case → Case Hub renders | < 500ms | |
| Tab switch within Case Hub | < 200ms | |
| Greeting reply (fast path) | < 1.5s warm; < 3s cold | dominated by DashScope RTT |
| Tool-call reply | < 5s for one tool | up to 15s for multi-step |
| Folder intake on 5 PDFs | < 30s | OCR parallelizes up to 12 |
| Folder intake on 20 docs | < 90s | |
| Single-doc draft generation | < 60s | |
| RAG search (semantic, 100 chunks) | < 100ms | local — no DashScope |
| Embed-router classification | < 30ms warm | local |
| Backend health response | < 50ms | |

---

## 14. Keyboard shortcuts

- [ ] **14.1** Cmd+N → New Matter sheet
- [ ] **14.2** Cmd+Shift+O → Quick Intake sheet
- [ ] **14.3** Cmd+Option+L → Research (case must be selected)
- [ ] **14.4** Cmd+Option+M → Model Picker
- [ ] **14.5** Cmd+, → Settings
- [ ] **14.6** Cmd+[ → Back to Overview (from Case Hub)
- [ ] **14.7** Cmd+Shift+K → API Key Onboarding
- [ ] **14.8** Esc → close any open sheet
- [ ] **14.9** Cmd+F → search within document viewer
- [ ] **14.10** Cmd+Q → graceful quit (no zombie processes — verify with `pgrep -lf mlx_`)

---

## 15. UI quality / accessibility

- [ ] **15.1** Dark mode renders cleanly (no white-on-white text)
- [ ] **15.2** Light mode renders cleanly
- [ ] **15.3** Long client names truncate with ellipsis, full name on hover
- [ ] **15.4** Long case lists scroll smoothly (test with 100+ cases)
- [ ] **15.5** Chinese characters render without missing glyphs (test 繁体 too: 訴訟、債務)
- [ ] **15.6** Toolbar icons have tooltips
- [ ] **15.7** Disabled buttons clearly show disabled state
- [ ] **15.8** Window resize doesn't break layout (try 800x600 minimum)
- [ ] **15.9** Multi-line text inputs grow as expected
- [ ] **15.10** Liquid Glass styling preserved (translucency, blur) on macOS 26

---

## 16. Cross-platform / packaging

- [ ] **16.1** App is signed (`spctl --assess --type execute /Applications/AgentFlow.app` returns 0)
- [ ] **16.2** App is notarized (`xcrun notarytool log <id>` shows accepted, or skip if unsigned dev build)
- [ ] **16.3** First-launch Gatekeeper prompt is reasonable (not "this app cannot be opened")
- [ ] **16.4** Uninstall is clean: `rm -rf /Applications/AgentFlow.app && rm -rf ~/Library/Application\ Support/AgentFlow` removes everything
- [ ] **16.5** Sandbox: app can read user's chosen folders during intake (no permission denied)
- [ ] **16.6** Bundled `agentflow-serve` is the latest build — `/Applications/AgentFlow.app/Contents/MacOS/agentflow-serve` mtime should match the latest release date

---

## 17. Critical regressions to monitor (don't get fooled twice)

These are bugs we've already fixed — re-test on every release to make sure they don't re-emerge.

- [ ] **17.1** **`<think>` token leakage** in LLM router: With LLM router enabled (`AGENTFLOW_ROUTER_ENABLED=1`) and `AGENTFLOW_EMBED_ROUTER_ENABLED=0`, send 5 conversational queries followed by "Show me all my pending tasks". Expected: still routed to NEEDS_TOOLS. (The pre-fix bug returned CONVERSATIONAL because state polluted across requests. Embedding router is immune.)
- [ ] **17.2** **mlx_lm.server concurrency cross-contamination** (issue #965): With LLM router on, send 16 distinct queries concurrently to /v1/agent/chat (from different shell windows). Expected: each gets its own correct routing. Pre-fix bug: one client's classification leaked into another's response.
- [ ] **17.3** **Capability-question mis-routing**: Send "what can you do?" / "你能做什么？" / "How does this app work?" — all three should hit the fast path. Pre-fix bug: model conflated capability questions with NEEDS_TOOLS.
- [ ] **17.4** **Matter-type fallback to default**: Drop a folder with `工资欠付争议.pdf`. Expected matter type: Labor Dispute. Pre-fix bug: defaulted to Civil Litigation because no keyword matched.
- [ ] **17.5** **BM25-only RAG missing semantic matches**: Search "who covers losses if vendor screws up" against a doc about indemnification. Expected: hits the indemnification clause. Pre-fix bug: missed because no shared lexical tokens.
- [ ] **17.6** **Backend not auto-restarting**: Force-kill `agentflow-serve`. Expected: app's BackendManager respawns it within ~10s. Pre-fix bug: dead state, UI stuck on "connecting".

---

## Appendix A: Useful commands while testing

```bash
# Tail backend log
tail -f ~/Library/Application\ Support/AgentFlow/agentflow-serve.log

# Tail embed sidecar log (if app is running)
tail -f ~/Library/Application\ Support/AgentFlow/embed_sidecar.log  # if exists

# Check what processes are alive
pgrep -lf "agentflow-serve|mlx_lm.server|mlx_embed_server"

# Check ports
lsof -i :8080 -i :8090 -i :8095

# Health
curl -s http://127.0.0.1:8080/health | jq

# RAG summary (mode + chunk counts)
curl -s http://127.0.0.1:8080/v1/rag/summary | jq

# Force a clean state for repro
pkill -f "agentflow|mlx_lm|mlx_embed"
rm -rf ~/Library/Application\ Support/AgentFlow/staging/*
# (don't rm vector_store unless you want to re-embed everything)

# Run automated suite
cd ~/Downloads/agentflow/agentflow-go && pytest tests/

# Reproduce a specific automated-test seed
pytest tests/ --randomly-seed=<N>
```

## Appendix B: Test-pass log template

```
Tester: ___________
Date: 2026-__-__
Build: AgentFlow.app version _____ (binary mtime: _______)
macOS: 26.__
Hardware: M_ Pro/Max/Ultra

Section pass counts:
  0 Preconditions:        __/10
  1 Lifecycle:            __/5
  2 Sidebar:              __/7
  3 New matter:           __/6
  4 Folder intake:        __/9
  5 Case hub:             __/10
  6 Document viewer:      __/8
  7 AI Inspector:         __/15
  8 Settings:             __/5
  9 Model picker:         __/4
  10 Embedding spine:     __/8
  11 Persistence:         __/6
  12 Edge cases:          __/10
  13 Performance:         __/12
  14 Shortcuts:           __/10
  15 UI quality:          __/10
  16 Packaging:           __/6
  17 Regressions:         __/6

Critical (🔥) failures:
  -

Non-critical failures:
  -

Performance regressions vs prior pass:
  -
```
