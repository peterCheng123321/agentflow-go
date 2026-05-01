# FLOW_AUDIT.md — AgentFlow V1 Complete Audit & V2 Roadmap

---

## 1. SYSTEM MAP

### Files & Roles

| File | Role |
|------|------|
| `agent_flow.py` | Core state machine. 10 states, DAG executor, HITL gates, all `_process_*` handlers |
| `server.py` | FastAPI backend. 30+ endpoints. WebSocket broadcaster |
| `llm_provider.py` | Qwen2.5 MLX local LLM. Legal text generation + tool calling |
| `rag_manager.py` | BM25 search engine. Document ingestion, chunking, structure-aware splitting |
| `wechat_connector.py` | WeChat adapter. Mock mode + OpenClaw bridge mode |
| `usage_tracker.py` | SQLite event store. All user actions, transitions, approvals |
| `tool_registry_v1.py` | Tool execution framework. Manual/auto modes per tool |
| `sys_scanner.py` | Hardware detection. RAM, chip, model recommendation |
| `auto_device.py` | Device config generator. Per-platform LLM backend selection |
| `setup_manager.py` | First-run setup. Directory creation, model download |
| `v2/adaptive_engine.py` | Stub. Will auto-tune system from V1 usage data |
| `v2/data_analyzer.py` | Stub. Will generate optimization rules from tracker |
| `frontend/index.html` | Single-page app. Tailwind CSS, vanilla JS |
| `grok-svg-*.svg` | Workflow diagram. 10 states, 3 HITL gates |

### Data Flow

```
User -> Frontend -> FastAPI -> LegalAgentMachine
                              |
                              +-> _process_*() -> LLM (Qwen2.5 MLX)
                              |                    +-> generate_legal_text(prompt, rag_context)
                              |                          +-> output -> case["document_draft"]
                              |
                              +-> rag.search(query) -> BM25 -> chunks
                              |
                              +-> wechat.send_message() -> Mock/OpenClaw
                              |
                              +-> tracker.record_*() -> SQLite (usage.db)
```

### Workflow States (from SVG Diagram)

```
1. CLIENT_CAPTURE -> 2. INITIAL_CONTACT -> 3. CASE_EVALUATION [HITL] ->
4. FEE_COLLECTION -> 5. GROUP_CREATION -> 6. MATERIAL_INGESTION ->
7. DOCUMENT_GENERATION [HITL] -> 8. CLIENT_APPROVAL ->
9. FINAL_PDF_SEND [HITL] -> 10. ARCHIVE_CLOSE
```

**HITL Gates (Human must approve before continuing):**
- Gate 1: CASE_EVALUATION — AI legal viability analysis
- Gate 2: DOCUMENT_GENERATION — AI-drafted legal document
- Gate 3: FINAL_PDF_SEND — Final delivery confirmation

**Parallel States:**
- FEE_COLLECTION and GROUP_CREATION run in parallel after CASE_EVALUATION
- MATERIAL_INGESTION waits for both FEE_COLLECTION and GROUP_CREATION

---

## 2. BROKEN THINGS (P0 — App is non-functional without these)

### 2.1 No "Execute State" — AI pipeline unreachable from UI

**Where:** `server.py` + `frontend/index.html`

**Problem:** The 10 `_process_*` handlers exist but are only called by `process_all_states()` which is only invoked from `agent_flow.py:run()` (the `__main__` entry). No API endpoint triggers them. The "Advance" button only changes the state enum — it doesn't run any AI processing.

**Impact:** Every state panel shows empty data. No evaluation text, no fee schedule, no draft, no contact log. The entire AI is unreachable from the UI.

**Fix:** Add `POST /cases/{case_id}/execute` that calls the current state's `_process_*` handler and returns populated data.

### 2.2 Frontend reads status data but panels need detail data

**Where:** `frontend/index.html:selectCase()` + `server.py:list_cases()`

**Problem:** `selectCase()` reads from the `/status` payload which calls `list_cases()` -> `get_case_status()`. This returns a flat snapshot that does NOT include `evaluation_detail`, `document_draft`, `fee_record`, `contact_log`, `group_info`, `approval_record`, `delivery_record`, `client_record`, or `ingestion_summary`. The state panels read all of these — they're always `undefined`.

Only `GET /cases/{case_id}` -> `get_case_detail()` includes these fields. The frontend never calls it.

**Fix:** `selectCase()` must call `GET /cases/{id}` to get full detail for the selected case.

### 2.3 `set_approval()` doesn't record the rejection reason for analytics

**Where:** `agent_flow.py:277-294`

**Problem:** The `reason` parameter is accepted but only appended to a case note. It's never passed to `tracker.record_approval()`. The tracker only records: state, approved (bool), response_time_s. Rejection reasons are lost from analytics.

**Fix:** Add `reason` to the approval tracking payload.

### 2.4 `get_case_status` serialization edge cases

**Where:** `agent_flow.py:310-329`

**Problem:** The snapshot filters out `hitl_events` but includes `state_outputs` which can contain arbitrary objects. The `completed_states` is converted from set to list correctly, but nested objects within `state_outputs` may not be JSON-serializable if they contain non-primitive types.

**Fix:** Ensure deep copy serialization in `get_case_status`. Convert all nested objects to JSON-safe primitives.

---

## 3. MISSING FEATURES (P1 — User can't do basic tasks)

### 3.1 No "Run Full Pipeline" button

Backend has `process_all_states()` — a DAG executor that runs all states sequentially, pausing at HITL gates. Not exposed via API. User needs to manually advance through every state.

**Fix:** Add `POST /cases/{case_id}/run-pipeline` as a background task. WebSocket pushes updates as each state completes.

### 3.2 No loading/progress indicator for AI operations

Local LLM generation takes 5-30 seconds. Frontend shows nothing during this time. User doesn't know if it crashed or is working.

**Fix:** Show spinner + "AI is generating evaluation..." text during execute. WebSocket can push progress.

### 3.3 Rewind uses browser `prompt()`

User must type exact state key like `CASE_EVALUATION`. Non-technical lawyers can't do this.

**Fix:** Dropdown or clickable progress bar to select target state.

### 3.4 No search/filter in case list

With 20+ cases the sidebar becomes unusable.

**Fix:** Add search input at top of sidebar. Filter by client name or state.

### 3.5 No RAG search UI

User can upload documents but can't search them. This is critical during evaluation and drafting states.

**Fix:** Add search input in Material Ingestion panel. Wire to a new `GET /rag/search?q=...` endpoint.

### 3.6 No timestamps on activity notes

Notes are stored as plain strings in `case["notes"]` — a `list[str]`. No timestamp, no author, no type tag. Impossible to reconstruct timeline.

**Fix:** Change notes to `list[dict]` with `{text, timestamp, author, type}`.

### 3.7 New case modal hardcodes source channel

`source_channel: "CRM"` is hardcoded. The SVG shows 4 channels: CRM, WeChat, Phone, Email.

**Fix:** Add channel dropdown to new case modal.

### 3.8 No indication of which states have been processed

The progress bar shows completed/active/pending but doesn't indicate whether a state's handler has actually run. A state can be "active" but have no output.

**Fix:** Check `case["state_outputs"][state_name]` to show processed vs unprocessed states.

### 3.9 No "Process & Advance" combined action

User expects clicking "Next" = do the work and move on. Currently they must click "Execute" then "Advance" — two separate actions.

**Fix:** Single "Process & Advance" button that executes current state handler, then advances to next state. If HITL gate, pause and show approval UI.

---

## 4. DESIGN PROBLEMS (P2 — Confusing for non-technical user)

### 4.1 HITL approval is small and hidden

The approve/reject button is the same size as "Edit Draft" and "Add Note". For the 3 critical gates (evaluation, draft, final delivery), this should be the PRIMARY action — big, centered, impossible to miss.

**Better:** When a state requires HITL approval, the entire state panel transforms into an approval review:

```
+---------------------------------------------+
|  AI EVALUATION - Review Required            |
|                                             |
|  [Full evaluation text here]                |
|                                             |
|  [APPROVE]   [REJECT WITH REASON]           |
|  [reason input]                             |
|                                             |
|  Or edit the evaluation first:              |
|  [Edit button]                              |
+---------------------------------------------+
```

### 4.2 No guidance text per state

Each panel just shows data. A non-technical user doesn't know what to do.

**Better:** Each state panel should have a contextual instruction:
- CLIENT_CAPTURE: "New client registered. Click Next to send intake message."
- INITIAL_CONTACT: "Intake message sent. Waiting for client response."
- CASE_EVALUATION: "AI has evaluated this case. Review and approve or reject."
- FEE_COLLECTION: "Fee quote sent. Mark as paid when payment received."

### 4.3 WeChat is a separate modal, not integrated

WeChat actions should appear contextually within the relevant state panels:
- Initial Contact -> "Resend via WeChat" button
- Group Creation -> group status inline, "Create Group" button
- Client Approval -> "Send to client via WeChat" button

### 4.4 No system health visibility

No indication of LLM status, RAG health, model name, or WeChat connection on the main screen.

**Better:** Small status bar in header showing: `Model: Qwen2.5-7B | RAG: 23 chunks | WeChat: Connected | Device: M3 Pro`

### 4.5 No keyboard shortcuts

Power users (lawyers processing 20+ cases/day) need speed.

**Better:** `N` = new note, `->` = advance, `A` = approve, `R` = reject, `E` = edit, `Esc` = close modal, `/` = search

### 4.6 Workflow progress bar is tiny

It's a 5-column grid of 2-letter labels. User doesn't know what each state means, which ones need action, or which are automated.

**Better:** Vertical stepper showing full descriptions, with icons for HITL gates, and clear "waiting for you" indicators.

---

## 5. ULTIMATE USER FLOW (What a non-technical lawyer actually wants)

```
A client messages the firm on WeChat:
  -> System auto-creates case, auto-sends intake template
  -> AI evaluates the case and shows the lawyer:

     +=========================================+
     |  NEW CASE REVIEW                       |
     |                                         |
     |  Client: Zhang Wei                     |
     |  Type: Commercial Lease Dispute        |
     |  Win Probability: 70%                  |
     |  Risk: Incomplete documentation        |
     |  Recommended: Accept                   |
     |                                         |
     |  [Full AI evaluation text...]          |
     |                                         |
     |  [ACCEPT CASE]    [REJECT CASE]        |
     +=========================================+

  -> Lawyer clicks ACCEPT
  -> System sends fee quote, creates WeChat group, waits for documents
  -> Client uploads lease in group
  -> System ingests docs, AI drafts legal document
  -> Shows lawyer the draft:

     +=========================================+
     |  DRAFT REVIEW - Edit before sending    |
     |                                         |
     |  [Full editable draft text...]         |
     |                                         |
     |  [APPROVE & SEND]  [REJECT & REGENERATE]|
     +=========================================+

  -> Lawyer edits paragraph 3, clicks APPROVE & SEND
  -> System sends to client for signature
  -> Client signs, system delivers final PDF

     +=========================================+
     |  FINAL DELIVERY - Confirm completion   |
     |                                         |
     |  [CONFIRM DELIVERY]    [HOLD]          |
     +=========================================+

  -> Lawyer clicks CONFIRM
  -> CASE COMPLETE

The lawyer NEVER:
  - Clicks "Advance Workflow"
  - Types state names like "CASE_EVALUATION"
  - Opens a separate WeChat modal
  - Wonders "what do I do now?"
  - Sees empty panels with no data
  - Has to understand what "execute" means
```

---

## 6. HITL DEEP-DIVE: Human-in-the-Loop Flow Audit

### 6.1 Current HITL Architecture

3 HITL gates exist in the workflow:

| Gate | State | What AI Produces | What Human Must Do |
|------|-------|-----------------|-------------------|
| Gate 1 | CASE_EVALUATION | Legal viability analysis with win probability | Review + Approve/Reject |
| Gate 2 | DOCUMENT_GENERATION | Full legal document draft | Review + Edit + Approve/Reject |
| Gate 3 | FINAL_PDF_SEND | Final delivery confirmation | Confirm delivery or hold |

### 6.2 Current HITL Flow (What Happens Now)

```
DAG executor runs _execute_state_node():
  1. Runs _process_* handler (generates AI output)
  2. Stores output in case["state_outputs"][state_name]
  3. Checks if node.is_hitl_gate
  4. If HITL gate AND not approved:
     - Logs "Pausing for human approval"
     - Records _state_start_times[state_name] = now
     - Awaits asyncio.Event (blocks until set)
  5. Human clicks Approve in frontend:
     - POST /approve {case_id, state, approved: true}
     - server.py -> agent_machine.set_approval()
     - Sets hitl_approvals[state] = true
     - Sets asyncio.Event -> unblocks DAG executor
     - Records tracker.record_approval(state, true, response_time)
  6. DAG executor continues to next state
```

### 6.3 What's Broken in This Flow

**Problem 1: No execute button means HITL gates are never reached**

The DAG executor only runs via `process_all_states()`. No API endpoint triggers it. The `POST /approve` endpoint sets approval flags, but since the DAG never runs, nothing is waiting for them. Approvals are set on states that were never executed.

**Problem 2: Approval is binary (approve/reject) — no edit-before-approve flow**

Current flow:
- Option A: Approve -> continue
- Option B: Reject -> ...nothing happens. No regeneration, no edit loop.

What lawyers actually need:
- Option A: Approve as-is -> continue
- Option B: Edit inline -> approve edited version -> continue
- Option C: Reject with reason -> AI regenerates -> new review cycle
- Option D: Partial approve -> accept some sections, request re-draft of others

**Problem 3: Edit tracking is a black hole**

```python
def edit_draft(self, case_id, new_draft_text):
    # Records: "edit_draft" action + case_id
    # Does NOT record:
    #   - What the old text was
    #   - What changed (diff)
    #   - How long the edit took
    #   - Which sections were modified
    #   - How many edit iterations occurred
    self.tracker.record_user_action("edit_draft", case_id)
```

**Problem 4: Rejection has no follow-through**

When a lawyer rejects at Gate 1 (evaluation) or Gate 2 (draft):
- The rejection reason is stored in a note (free text)
- No structured data about WHY it was rejected
- No automatic regeneration
- No way to re-run the handler with different parameters
- The state stays "rejected" — there's no path forward

**Problem 5: No multi-round HITL tracking**

A real workflow involves multiple edit cycles:
```
AI generates draft -> Lawyer rejects -> AI regenerates -> Lawyer edits -> Lawyer approves
```
Currently we only track: "approval=true" at the end. We lose the entire correction history.

### 6.4 Complete HITL Edit Flow (Target Design)

```
State: DOCUMENT_GENERATION

+---------------------------------------------+
|  DRAFT REVIEW - Round 1                     |
|                                             |
|  AI generated 1,247 characters.             |
|  Review before sending.                     |
|                                             |
|  + Section: Facts Statement ------------+   |
|  | [editable text]                      |   |
|  +--------------------------------------+   |
|  + Section: Legal Basis ---------------+   |
|  | [editable text]                      |   |
|  +--------------------------------------+   |
|  + Section: Claims --------------------+   |
|  | [editable text]                      |   |
|  +--------------------------------------+   |
|                                             |
|  Edit summary: 0 changes made               |
|                                             |
|  [APPROVE]  [REJECT]  [REGENERATE]          |
|                                             |
|  If rejecting, select reason:               |
|  [dropdown: wrong_tone / missing_clauses]   |
|  Additional notes: [text input]             |
+---------------------------------------------+

Lawyer edits Section 2, adds a clause:

|  Edit summary: 1 section changed (+47 chars)|
|  Changed: Legal Basis                       |

Lawyer clicks APPROVE:

-> System records:
  - hitl_review: {decision: "edited_then_approved",
                   edit_count: 1, edit_char_count: 47,
                   edit_ratio: 0.038, is_minor_edit: true,
                   sections_edited: ["Legal Basis"]}
  - Draft is saved with lawyer's edits as final version
  - Continues to CLIENT_APPROVAL state
```

### 6.5 Rejection Taxonomy (Structured Reasons)

Instead of free-text rejection, provide structured options:

**Evaluation rejection reasons:**
- `wrong_legal_basis` — AI cited wrong law/article
- `missing_facts` — Evaluation doesn't account for key facts
- `wrong_conclusion` — Disagree with win probability
- `incomplete_analysis` — Missing risk factors
- `wrong_case_type` — This isn't the right matter type

**Draft rejection reasons:**
- `wrong_tone` — Too formal / too casual
- `missing_clauses` — Specific clauses need to be added
- `factual_errors` — Client details are wrong
- `wrong_structure` — Needs different document structure
- `missing_evidence` — Doesn't reference available evidence

**Delivery rejection reasons:**
- `client_unreachable` — Can't reach client
- `payment_pending` — Client hasn't paid yet
- `needs_revision` — Client requested changes

---

## 7. FINE-TUNING DATA PIPELINE

### 7.1 What We Collect Now

| Event Type | Fields Recorded | Fields STRIPPED |
|------------|----------------|-----------------|
| case_created | matter_type, source_channel | client_name (hashed) |
| state_transition | from_state, to_state, duration_s | case_id (hashed) |
| approval | state, approved, response_time_s | rejection reason (lost) |
| tool_call | tool_name, success, latency_ms | — |
| document_upload | file_ext, chunk_count, ingest_success | filename (lost) |
| user_action | action, detail string | all content (stripped) |

**Current events in DB: 13 rows**

### 7.2 What We STRIP That We Need for Fine-Tuning

The PII policy hashes client names and deletes all text content. This is correct for analytics. But for fine-tuning we need the actual text — stored in a SEPARATE, encrypted, opt-in table.

**What we currently delete and shouldn't:**
- `text`, `message`, `initial_msg` — stripped entirely by `usage_tracker.py:87-89`
- `client_name` — hashed, fine for analytics but we need the original for training data context
- `contact_name` — hashed

**What we never capture at all:**
- LLM prompt sent to model
- LLM raw output (before any post-processing)
- Lawyer's edits to drafts (diff between AI output and final version)
- Which sections were edited
- How many edit iterations
- Whether lawyer agreed with AI's evaluation
- RAG context chunks that were retrieved
- Generation parameters (temperature, max_tokens)

### 7.3 New Table: `llm_interactions`

```sql
CREATE TABLE llm_interactions (
    id INTEGER PRIMARY KEY,
    ts REAL NOT NULL,
    case_id_hash TEXT NOT NULL,
    state_name TEXT NOT NULL,

    -- Inputs
    rag_query TEXT,                          -- what we searched
    rag_context TEXT,                        -- retrieved chunks (anonymized)
    llm_prompt TEXT NOT NULL,                -- full prompt sent to model
    system_prompt TEXT,                      -- system prompt used
    model_name TEXT NOT NULL,                -- which model
    generation_params TEXT,                  -- JSON: {temp, max_tokens, etc}

    -- Outputs
    llm_raw_output TEXT NOT NULL,            -- raw model output (before edits)
    llm_final_output TEXT,                   -- after lawyer edits (NULL if not edited)
    edit_diff TEXT,                          -- unified diff: raw -> final
    edit_summary TEXT,                       -- JSON: {char_count, section_edits, ratio}

    -- Quality signal
    was_edited BOOLEAN DEFAULT FALSE,
    edit_round INTEGER DEFAULT 1,
    final_approval_decision TEXT,            -- approved / edited_then_approved / rejected
    rejection_reason TEXT,
    review_duration_s REAL,

    -- For JSONL export
    exported BOOLEAN DEFAULT FALSE
);
```

### 7.4 New Table: `hitl_reviews`

```sql
CREATE TABLE hitl_reviews (
    id INTEGER PRIMARY KEY,
    ts REAL NOT NULL,
    case_id_hash TEXT NOT NULL,
    state_name TEXT NOT NULL,
    review_round INTEGER DEFAULT 1,        -- which iteration (1st, 2nd, 3rd review)
    decision TEXT NOT NULL,                  -- 'approved', 'rejected', 'edited_then_approved'
    review_duration_s REAL,                 -- time from output shown to decision
    rejection_reason TEXT,                  -- structured reason category
    rejection_detail TEXT,                  -- free text reason

    -- Edit metrics
    edit_count INTEGER DEFAULT 0,           -- number of manual edits made
    edit_char_count INTEGER DEFAULT 0,      -- characters changed
    edit_ratio REAL DEFAULT 0,              -- changed_chars / total_chars
    is_minor_edit BOOLEAN,                  -- < 10% changed = minor
    sections_edited TEXT,                   -- JSON list of which sections were modified

    -- AI output reference
    llm_output_id INTEGER,                  -- FK to llm_interactions table

    -- Session
    session_id TEXT
);
```

### 7.5 Fine-Tuning Export Format (Qwen Chat Template)

Each approved interaction becomes one training example:

```jsonl
{"messages": [
  {"role": "system", "content": "You are a professional legal assistant focused on Chinese commercial lease disputes. Answer directly in Chinese based on the provided context."},
  {"role": "user", "content": "Context: [RAG chunks]\nRequest: Please evaluate the legal viability of this case: Zhang Wei vs Landlord Co, commercial lease dispute."},
  {"role": "assistant", "content": "[LAWYER-APPROVED final text, with any lawyer edits incorporated]"}
]}
```

**Key principle:** The `assistant` content is ALWAYS the lawyer's final approved version — never the raw AI output. The model learns to produce what the lawyer accepted.

### 7.6 Edit Quality Scoring

For each interaction, compute quality metrics:

| Metric | Formula | Purpose |
|--------|---------|---------|
| edit_ratio | `changed_chars / total_chars` | Low ratio = AI was already good |
| edit_count | number of discrete edits | High count = many small fixes |
| is_minor_edit | `edit_ratio < 0.1` | Filter: only use high-quality edits for training |
| review_speed | `review_duration_s / output_length` | Fast approval = high confidence |
| rejection_count | rounds before approval | High count = AI needs improvement here |

**Training data filtering:**
- Include: `approved` or `edited_then_approved` with `is_minor_edit=true`
- Include: `edited_then_approved` with `edit_ratio < 0.3`
- Exclude: `rejected` (no good target output)
- Exclude: `edited_then_approved` with `edit_ratio > 0.7` (AI was too wrong)
- Exclude: Round 1 rejections that were followed by full regeneration

### 7.7 User Habit Data We Should Track

**Lawyer behavior patterns:**

| Behavior | How to Track | Why It Matters |
|----------|-------------|----------------|
| Which states take longest to review | `review_duration_s` per state | Identify bottleneck states |
| Which states get rejected most | `decision=rejected` rate per state | Identify worst AI output states |
| How much lawyers edit | `edit_ratio` distribution | Measure AI quality baseline |
| Edit patterns by matter type | `edit_ratio` grouped by matter_type | Some case types may need better prompts |
| Time between state execution and review | timestamp diff | Are lawyers reviewing immediately or later? |
| Rejection reason frequency | `rejection_reason` counts | Which AI weaknesses are most common |
| RAG retrieval quality | `rag_context` length vs `edit_ratio` | Are we retrieving the right chunks? |
| Model performance over time | `edit_ratio` over time | Is the base model improving or degrading? |
| Lawyer-specific patterns | per-session metrics | Different lawyers may have different standards |

### 7.8 V2 Fine-Tuning Pipeline

```
V1 Data Collection (now):
  +-- llm_interactions table captures every AI output + lawyer correction
  +-- hitl_reviews table captures approval/rejection patterns
  +-- At least 100 approved cases before training

Data Export:
  +-- Filter: approved or edited_then_approved, edit_ratio < 0.3
  +-- Transform: row -> Qwen chat format JSONL
  +-- Split: 80% train, 10% validation, 10% test
  +-- Anonymize: strip case_id_hash from training data

Fine-Tuning:
  +-- Base: Qwen2.5-3B-Instruct (same architecture, fits on device)
  +-- Method: LoRA (rank 16, alpha 32) -- trains in 30 min on M3
  +-- Training: 3 epochs, learning rate 2e-4
  +-- Evaluate: compare edit_ratio before/after on held-out cases

Deployment:
  +-- If edit_ratio improves > 10%: promote to production
  +-- If rejection rate drops: promote
  +-- A/B test: 50% base model, 50% fine-tuned
  +-- Track metrics for 2 weeks before full rollout

V2 Adaptive Engine activates:
  +-- Auto-promote tools from manual -> auto when success_rate > 95%
  +-- Auto-approve states when rejection_rate < 2% over 50 cases
  +-- Adjust RAG chunk sizes based on retrieval quality metrics
  +-- Re-enable OpenClaw for real WeChat integration
```

---

## 8. IMPLEMENTATION PLAN

### Phase A: Fix the broken core (P0) — 1 day

1. **Add `POST /cases/{case_id}/execute` endpoint** in `server.py`
   - Calls `_execute_state_node(state_name, case)` for current state
   - Returns populated case data
   - Records to `llm_interactions` table

2. **Add `POST /cases/{case_id}/run-pipeline` endpoint** in `server.py`
   - Runs `process_all_states()` as background task
   - Returns task_id for progress tracking

3. **Fix `selectCase()` to fetch detail** in `frontend/index.html`
   - Call `GET /cases/{id}` instead of reading from status payload

4. **Add `llm_interactions` table** in `usage_tracker.py`
   - Record every LLM call: prompt, context, raw output

5. **Add `hitl_reviews` table** in `usage_tracker.py`
   - Record every HITL decision: approval, rejection, edit metrics

6. **Add `record_llm_interaction()` method** in `usage_tracker.py`
   - Called by `agent_flow.py` after every LLM generation

7. **Fix `set_approval()` to include reason in tracking** in `agent_flow.py`

### Phase B: Make it usable (P1) — 2 days

1. **"Process & Advance" button** — execute + advance in one click
2. **"Run Full Pipeline" button** — runs entire DAG with live progress
3. **Loading spinners** for AI operations
4. **Rewind dropdown** instead of prompt()
5. **Case search filter** in sidebar
6. **RAG search** in material panel + `GET /rag/search` endpoint
7. **Timestamps on notes** — change `list[str]` to `list[dict]`
8. **Source channel dropdown** in new case modal
9. **Process state indicators** — show which states have been executed vs just advanced to

### Phase C: Make it delightful (P2) — 3 days

1. **Inline HITL approval** — big, centered, with rejection taxonomy dropdown
2. **Section-based draft editing** — edit per section, track which sections changed
3. **Edit diff tracking** — compute and store unified diff between AI output and final
4. **Contextual guidance text** per state
5. **WeChat actions integrated** into state panels
6. **System health bar** in header
7. **Keyboard shortcuts** — N, ->, A, R, E, Esc, /
8. **Streaming LLM output** — SSE endpoint for real-time token display

### Phase D: Fine-tuning pipeline (V2) — 1 week

1. **JSONL export endpoint** — `GET /v1/export/training-data`
2. **Edit quality scoring** — compute metrics on every review
3. **LoRA fine-tuning script** — train Qwen2.5-3B on collected data
4. **A/B test framework** — compare base vs fine-tuned
5. **Adaptive engine activation** — auto-promote tools and approvals
6. **OpenClaw re-enablement** — real WeChat integration

---

## 9. SPEED PRIORITIES (For the non-technical user)

The #1 complaint will be: "It's too slow." Here's what to optimize:

| Bottleneck | Current | Target | How |
|------------|---------|--------|-----|
| LLM generation | 5-30s | < 5s | Stream tokens to frontend, show partial results |
| First page load | 3-5s | < 1s | Cache static assets, pre-warm LLM |
| State advance | 2 API calls | 1 API call | Merge execute + advance |
| RAG search | ~100ms | < 50ms | Pre-index common queries, cache results |
| Case list refresh | Full poll | Push only | WebSocket already exists, use it for diffs |
| Draft editing | Full save | Auto-save | Debounced auto-save every 3s |

**Streaming LLM output to frontend:**

Currently `generate_legal_text()` blocks until complete. For the frontend, add a streaming endpoint:

```
GET /cases/{id}/execute?stream=true
-> Server-Sent Events:
  data: {"type": "token", "content": ""}
  data: {"type": "token", "content": ""}
  ...
  data: {"type": "done", "output_id": 42}
```

This lets the user see the AI thinking in real-time instead of staring at a spinner.

---

## 10. DATA ARCHITECTURE SUMMARY

### Current SQLite Schema (usage.db)

```
events table:
  id, ts, event_type, category, payload (JSON), session_id

summaries table:
  id, period_type, period_key, summary_json, created_at
```

### New Tables Needed

```
llm_interactions table:
  id, ts, case_id_hash, state_name,
  rag_query, rag_context, llm_prompt, system_prompt, model_name, generation_params,
  llm_raw_output, llm_final_output, edit_diff, edit_summary,
  was_edited, edit_round, final_approval_decision, rejection_reason, review_duration_s,
  exported

hitl_reviews table:
  id, ts, case_id_hash, state_name, review_round,
  decision, review_duration_s, rejection_reason, rejection_detail,
  edit_count, edit_char_count, edit_ratio, is_minor_edit, sections_edited,
  llm_output_id, session_id
```

### JSONL Export (for fine-tuning)

```jsonl
{"messages": [{"role": "system", "..."}, {"role": "user", "..."}, {"role": "assistant", "..."}]}
{"messages": [{"role": "system", "..."}, {"role": "user", "..."}, {"role": "assistant", "..."}]}
```

### Event Categories Currently Tracked

| Category | Events |
|----------|--------|
| cases | case_created |
| workflow | state_transition |
| hitl | approval |
| tools | tool_call |
| documents | document_upload |
| ui | user_action |

### Event Categories to Add

| Category | Events |
|----------|--------|
| llm | llm_generate, llm_stream_start, llm_stream_end |
| hitl | hitl_edit, hitl_reject, hitl_approval_with_edit |
| notes | note_added, note_edited, note_deleted |
| documents | document_edited, document_section_modified |

---

## 11. VISION: WHAT THIS BECOMES

### V1 (Now): Manual Workflow Engine
- Lawyer manually triggers each step
- AI generates content, human approves
- Simple tracking for analytics

### V2 (Next): Semi-Automated Pipeline
- One-click "Run Pipeline" — AI handles everything, human handles HITL gates
- Fine-tuned model from V1 data
- Real WeChat integration via OpenClaw
- Streaming LLM output

### V3 (Future): Autonomous Legal Assistant
- Fully automated case processing
- AI handles low-risk approvals automatically (based on rejection rate data)
- Multi-case parallel processing
- Client-facing chatbot for initial intake
- Predictive case outcome modeling from accumulated data
- Auto-generated case reports for partners

---

*Document generated: 2026-03-30*
*Project: AgentFlow V1 — LegalCaseAgent-HITL*
*Audit scope: Complete system, frontend, backend, data pipeline, fine-tuning strategy*
