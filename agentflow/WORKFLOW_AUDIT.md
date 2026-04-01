# Workflow Audit: State-by-State Problem Analysis

## State 1: CLIENT_CAPTURE
**What happens**: Captures client name, matter type, source channel, initial message into `client_record`. Sends a WeChat notification to "File Transfer".

### Problems
1. **Bootstrap case is noise** — `__init__` creates a default "ClientX" case on startup. This pollutes the case list with junk data and gets auto-selected.
2. **No input validation** — Empty `client_name` is accepted. A case with `client_name=""` will propagate through the entire pipeline.
3. **WeChat dependency even in mock** — `_process_client_capture` calls `self.wechat.send_message()`. If WeChat is paused/disconnected, this silently fails but the state still reports "captured" as success.
4. **No deduplication** — Creating two cases for the same client with the same matter type creates duplicate cases. No check exists.

### Fixes
- Skip bootstrap case creation or make it opt-in.
- Validate `client_name` is non-empty before creating case.
- Handle WeChat send failure explicitly (log warning, don't treat as success).
- Add a note about WeChat being paused when mode is "mock".

---

## State 2: INITIAL_CONTACT
**What happens**: Sends a templated Chinese greeting to the client via WeChat asking for more details.

### Problems
1. **Static template, no LLM personalization** — The message is hardcoded. For a "legal AI agent" the initial contact should be personalized based on the matter type and initial message.
2. **Channel mismatch** — `case["source_channel"]` could be "CRM" or "Demo Intake", but the code sends via WeChat regardless. If the source was "CRM" (user typed in the web UI), sending a WeChat message makes no sense.
3. **Send failure is not terminal** — If `sent=False`, the workflow continues normally. The contact_log records failure but there's no retry mechanism and no human notification.
4. **Message sent to client before evaluation** — We're asking for more details but haven't evaluated anything yet. The client receives a generic message before the system has done any analysis.

### Fixes
- Check `source_channel` and skip WeChat send if channel isn't WeChat.
- Log send failure prominently in agent console and add a note prompting manual follow-up.
- Consider skipping this state entirely if source is CRM.

---

## State 3: CASE_EVALUATION (HITL Gate)
**What happens**: Calls `orchestrate_case()` which plans RAG queries, executes them, then generates legal text via LLM. Pauses for human approval.

### Problems
1. **Empty RAG = hallucination risk** — If no documents have been uploaded yet (we're only at state 3 of 10), RAG returns nothing. The LLM generates legal analysis with zero source material. This is the most dangerous state — the model will confidently fabricate legal analysis.
2. **`orchestrate_case` calls LLM twice** — Once for planning (structured JSON), once for synthesis. But `process_all_states` (DAG runner) doesn't call orchestrate — the v1 runner calls `_process_case_evaluation` which calls it again. This means the evaluation step is architecturally different depending on which runner is used.
3. **HITL gate blocks but doesn't timeout** — The `_HitlGate.wait_async()` can block forever. No timeout mechanism. If the operator walks away, the pipeline hangs indefinitely.
4. **Approval response time tracking is wrong** — `_state_start_times` is set in `_execute_state_node`, but `set_approval` pops it. If `advance_state` is called manually (not through the agent), `_state_start_times` may never have been set, so it defaults to `time.time()` which makes response time ~0 seconds.
5. **`_HitlGate._spin_wait` uses `time.sleep(0.05)` in a thread** — This is a polling loop that burns CPU. For long-running waits (hours), this creates unnecessary background thread activity.
6. **Reject doesn't stop the pipeline** — If the operator rejects the evaluation, the HITL gate unblocks and the pipeline continues anyway. There's no mechanism to halt or retry.

### Fixes
- **Critical**: Check if RAG has relevant documents before evaluation. If not, log a warning and use a disclaimer in the output: "No source documents available — analysis based on model knowledge only."
- Add timeout to HITL gates (configurable, e.g., 24 hours).
- On rejection, don't just unblock — mark the case as "needs_revision" and don't advance.
- Replace `_spin_wait` with an `asyncio.Event` that works across the same event loop.

---

## State 4: FEE_COLLECTION
**What happens**: Looks up a hardcoded fee table, constructs a fee message, sends via WeChat (using the old `ToolRegistry.send_wechat_msg_async`, not the V1 registry).

### Problems
1. **Hardcoded fee table** — Fees are defined in the handler as a dict literal. Should be configurable.
2. **Uses old `ToolRegistry` (not V1)** — `_process_fee_collection` calls `ToolRegistry.send_wechat_msg_async()` (from `llm_provider.py`) instead of `self.tool_registry.execute("send_wechat", ...)`. This bypasses the V1 tool registry entirely — no confirmation flow, no stats tracking, no auto/manual mode.
3. **Sets `is_paid = False` unconditionally** — Even if the case was already marked as paid (operator manually set it), this state overrides it to False.
4. **No payment tracking** — Sends the fee message but has no mechanism to track if the client actually pays.

### Fixes
- Use `self.tool_registry.execute("send_wechat", ...)` for consistency.
- Don't override `is_paid` if already set.
- Move fee table to configuration or the case data.

---

## State 5: GROUP_CREATION
**What happens**: Creates a WeChat group with client name + lawyers. Also publishes to Douyin (social media).

### Problems
1. **Douyin publish is inappropriate** — `ToolRegistry.publish_to_douyin_async()` announces a new case to social media. For a legal CRM, this is a privacy violation. Case details are being broadcast.
2. **Parallel execution with FEE_COLLECTION** — The DAG says FEE_COLLECTION and GROUP_CREATION depend on CASE_EVALUATION, so they run in parallel. But group creation includes the client, and fee collection sends the fee message. If fee collection fails, the group is still created with the client — they're in a group for a case that might not proceed.
3. **Static member list** — "承办律师" and "案件助理" are hardcoded Chinese strings, not actual user accounts.
4. **Uses old ToolRegistry** — Same as FEE_COLLECTION, bypasses V1 registry.

### Fixes
- **Remove Douyin publish** entirely — legal cases should never be posted to social media automatically.
- Make group creation sequential after fee collection (fix the DAG dependency).
- Use configurable member templates.

---

## State 6: MATERIAL_INGESTION
**What happens**: Summarizes what documents have been uploaded and what's in the RAG index. No actual processing — it's a reporting step.

### Problems
1. **This state does nothing active** — It just reads the current state of `uploaded_documents` and `rag.get_summary()`. If the operator hasn't uploaded anything, it reports "0 documents, 0 chunks" and moves on. There's no waiting for documents, no prompting the operator to upload.
2. **No PDF processing happens here** — Despite the name "Material Ingestion", the actual ingestion happens at upload time (POST /upload). This state just reports stats. The naming is misleading.
3. **RAG stats are global, not case-specific** — `rag.get_summary()` returns ALL documents in the index, not just the ones attached to this case. If multiple cases exist, the chunk counts are inflated.

### Fixes
- Add a check: if no documents are uploaded, log a warning and add a note prompting the operator.
- Filter RAG stats to only show documents attached to this case.
- Consider renaming to "Material Summary" or adding actual ingestion logic.

---

## State 7: DOCUMENT_GENERATION (HITL Gate)
**What happens**: Calls `orchestrate_case(objective="draft")`, generates a legal document via LLM, extracts highlights, generates PDF report.

### Problems
1. **Orchestration runs RAG queries again** — `_process_document_generation` calls `orchestrate_case` which does a full planning + RAG retrieval + synthesis cycle. If the RAG didn't have good results during evaluation, it still won't have good results here (same index, same documents).
2. **Highlight extraction uses truncated context** — `source_text = context[:3000]` — only the first 3000 chars of aggregated RAG context. For multi-page PDFs, this misses most of the document.
3. **Highlight page mapping uses first uploaded doc** — `uploaded_docs[0]` is used for `find_text_in_pages`. But the highlight text came from RAG context (aggregated from multiple documents), not necessarily from the first PDF.
4. **PDF generation failure is not terminal** — If reportlab fails, the state continues with "draft_generated" status. The document_draft won't have `pdf_report` set, but the workflow advances.
5. **Same HITL issues as CASE_EVALUATION** — No timeout, rejection doesn't stop pipeline.

### Fixes
- Use full RAG context for highlight extraction (or extract from actual uploaded PDF text).
- Search across all uploaded documents for page mapping, not just the first.
- Add error handling for PDF generation failure.

---

## State 8: CLIENT_APPROVAL
**What happens**: Creates an approval record and logs that the draft was submitted for client review.

### Problems
1. **No actual client notification** — The code creates an `approval_record` dict but doesn't actually send anything to the client. It just logs. The name "CLIENT_APPROVAL" implies the client is being asked, but no message is sent.
2. **`approval_status` is set to "pending_client_review"` but there's no mechanism for the client to respond** — The approval only happens through the operator UI (HITL buttons in the frontend). The actual client never sees or approves anything.
3. **`delivery_method` and `delivery_group` reference WeChat** — But if the source channel was CRM, these fields are misleading.

### Fixes
- Actually send the draft to the client via WeChat or show it in the group.
- Or rename this state to "INTERNAL_REVIEW" to reflect what actually happens.

---

## State 9: FINAL_PDF_SEND (HITL Gate)
**What happens**: Constructs a delivery record, sends a WeChat message to the client saying their document is ready.

### Problems
1. **Doesn't send the actual PDF** — The WeChat message just says "您的案件文书已准备完毕" but doesn't attach or link to the PDF. The PDF exists on disk but is never delivered to the client.
2. **No attachment mechanism** — WeChat mock only supports text messages. There's no file transfer capability in the connector.
3. **HITL gate before delivery is correct** — But the operator approval here is about reviewing the final delivery, which is good. However, the operator has no ability to modify the delivery at this point.
4. **Fee status in delivery record** — If payment hasn't been confirmed, the delivery proceeds anyway. No gate on payment.

### Fixes
- If a PDF report was generated, include a download link or reference in the WeChat message.
- Add a payment gate before final delivery.

---

## State 10: ARCHIVE_CLOSE
**What happens**: Adds a note "Case completed and archived." Returns success.

### Problems
1. **No actual archiving** — Nothing is archived. The case stays in `self.cases` dict in memory. No data is moved to cold storage, no PDF cleanup, no export.
2. **No PDF cleanup** — Per the user's request ("if no download automatically deleted"), generated PDFs should be cleaned up after archival if they weren't downloaded. Currently they persist forever.
3. **No case summary/final report** — The archive step could generate a final summary of everything that happened.

### Fixes
- Implement actual archiving: export case data to JSON, optionally clean up generated PDFs.
- Add a configurable retention policy for generated reports.

---

## Cross-Cutting Issues

### 1. Two execution paths diverge
- **V1 DAG runner** (`process_all_states`): Uses `_execute_state_node` which calls `_process_*` methods directly. HITL gates use `_HitlGate.wait_async()`.
- **V2 LangGraph runner** (`run_graph_until_pause`): Uses `LangGraphWorkflowEngine._run_state` which has special handling for CASE_EVALUATION and DOCUMENT_GENERATION (calls `_run_evaluation_subgraph` / `_run_drafting_subgraph`). HITL gates don't block — the graph returns with `pending_interrupt`.
- **Problem**: These two paths produce different results. V2 adds subgraph recording, different orchestration flow, and checkpoint persistence. V1 doesn't checkpoint at all.

### 2. `_HitlGate` vs `asyncio.Event` inconsistency
- `default_case_data` now creates `_HitlGate()` objects.
- `rewind_state` line 329 still creates `asyncio.Event()` instead of `_HitlGate()`.
- `_execute_state_node` calls `evt.wait()` (line 654) which is the sync `wait()` that `_HitlGate` overrides to raise RuntimeError.
- **This is a crash bug**: V1 DAG runner will crash at every HITL gate because `_HitlGate.wait()` raises `RuntimeError("Synchronous wait not supported")`.

### 3. Race conditions on case dict
- Multiple async tasks can modify `case` dict concurrently (e.g., parallel FEE_COLLECTION + GROUP_CREATION in the DAG runner).
- No locking on case data. `_invalidate_cache` and `_cache_valid` are not thread-safe.
- WebSocket broadcast can read partial state.

### 4. No persistence
- All case data lives in memory. Server restart loses everything.
- RAG store is persisted to JSON but case data is not.
- LangGraph checkpoints are persisted to SQLite but case data isn't.

### 5. `get_case_status` serialization of `AgentState` enum
- Line 378: `snapshot["state"] = case["state"].name` — this works.
- But line 378 also copies all keys from case dict except protected ones. If any value is non-serializable (like `_HitlGate` or `set`), the `orjson.dumps` in WebSocket broadcast will fail.
- `completed_states` is a `set` which is excluded, but `state_outputs` dict values could contain anything.

### 6. Status cache staleness
- `_cache_valid` is set to True after building a snapshot. But if case data changes between the cache check and the cache read, stale data is returned.
- The `deepcopy` in `create_case` returns a snapshot, but `get_case_status` returns a reference to `_status_cache` which is then mutated by callers.

### 7. `process_all_states` DAG runner infinite loop risk
- Line 598-606: If all remaining states have unmet dependencies, `ready` is empty, and the loop `await asyncio.sleep(0.5)` forever. This shouldn't happen with the current DAG but there's no safeguard.
