# Tool Calling & LangGraph V2 Test Plan

## Overview

This document outlines the comprehensive test suite for V2's LangGraph-based orchestration engine and tool calling system. The tests are designed to verify:

1. **Tool Calling** — LLM decides which tool to invoke based on context
2. **Graph Execution** — State machine runs states in correct order
3. **HITL Interrupt/Resume** — Pauses at gates, resumes on approval
4. **Checkpoint Persistence** — Save/restore across restarts
5. **Subgraph Patterns** — Plan → Retrieve → Synthesize for complex states
6. **Adaptive Engine** — Auto-tune based on V1 usage data
7. **Data Analyzer** — Generate rules from historical patterns

---

## Test File: `tests/test_tool_calling.py`

### Module 1: Tool Calling Engine

**Purpose:** Verify the LLM can decide which tool to invoke based on user input.

#### 1.1 Tool Decision Tests

| Test ID | Input | Expected Tool | Why |
|---------|-------|---------------|-----|
| TC-001 | "Send message to client about rent" | `send_wechat` | Direct send instruction |
| TC-002 | "Search for lease law precedents" | `search_rag` | Retrieval intent |
| TC-003 | "Generate PDF of the draft" | `generate_pdf` | Document generation |
| TC-004 | "Post case update to Douyin" | `publish_douyin` | Social media publish |
| TC-005 | "Hello, how are you?" | `none` | No tool needed |

#### 1.2 Structured Output Tests

| Test ID | Description | Validates |
|---------|-------------|-----------|
| TC-010 | LLM returns valid XML tag | `search_rag` → `<search_rag>` |
| TC-011 | LLM handles ambiguous input | Falls back to `<none>` |
| TC-012 | LLM returns malformed XML | Parser force-closes tag |
| TC-013 | Multiple tool candidates | Picks highest confidence |

#### 1.3 Tool Execution Tests

| Test ID | Tool | Mode | Expected |
|---------|------|------|----------|
| TC-020 | `search_rag` | auto | Runs immediately |
| TC-021 | `send_wechat` | manual | Awaits confirmation |
| TC-022 | `send_wechat` | auto | Runs immediately |
| TC-023 | Unknown tool | any | Returns error result |

#### 1.4 Tool Confirmation Flow

| Test ID | Scenario | Expected |
|---------|----------|----------|
| TC-030 | Manual tool, approve | Executes after confirm |
| TC-031 | Manual tool, reject | Skipped with reason |
| TC-032 | Pending timeout | Stays pending |
| TC-033 | Multiple pending tools | Separate call_ids |

---

### Module 2: LangGraph Workflow Engine

**Purpose:** Verify the graph-based state machine executes states correctly.

#### 2.1 Graph Construction Tests

| Test ID | Description | Validates |
|---------|-------------|-----------|
| LG-001 | Graph has 11 nodes | router + 10 states |
| LG-002 | Entry point is router | Correct start |
| LG-003 | All edges connect correctly | No orphaned nodes |
| LG-004 | FEE→GROUP parallel split | Both reachable |

#### 2.2 State Execution Tests

| Test ID | State | Validates |
|---------|-------|-----------|
| LG-010 | CLIENT_CAPTURE | Creates client_record |
| LG-011 | INITIAL_CONTACT | Sends message |
| LG-012 | CASE_EVALUATION | Runs eval subgraph |
| LG-013 | FEE_COLLECTION | Creates fee_record |
| LG-014 | GROUP_CREATION | Creates WeChat group |
| LG-015 | MATERIAL_INGESTION | Records ingestion_summary |
| LG-016 | DOCUMENT_GENERATION | Creates document_draft |
| LG-017 | CLIENT_APPROVAL | Creates approval_record |
| LG-018 | FINAL_PDF_SEND | Creates delivery_record |
| LG-019 | ARCHIVE_CLOSE | Marks archived |

#### 2.3 Router Node Tests

| Test ID | Current State | Expected Next |
|---------|---------------|---------------|
| LG-020 | CLIENT_CAPTURE | INITIAL_CONTACT |
| LG-021 | INITIAL_CONTACT | CASE_EVALUATION |
| LG-022 | CASE_EVALUATION | FEE_COLLECTION |
| LG-023 | FEE_COLLECTION | GROUP_CREATION |
| LG-024 | GROUP_CREATION | MATERIAL_INGESTION |
| LG-025 | MATERIAL_INGESTION | DOCUMENT_GENERATION |
| LG-026 | DOCUMENT_GENERATION | CLIENT_APPROVAL |
| LG-027 | CLIENT_APPROVAL | FINAL_PDF_SEND |
| LG-028 | FINAL_PDF_SEND | ARCHIVE_CLOSE |
| LG-029 | ARCHIVE_CLOSE | END |

#### 2.4 HITL Interrupt Tests

| Test ID | Interrupt State | Validates |
|---------|-----------------|-----------|
| LG-030 | CASE_EVALUATION | Pauses, pending_interrupt set |
| LG-031 | DOCUMENT_GENERATION | Pauses after draft |
| LG-032 | FINAL_PDF_SEND | Pauses before delivery |
| LG-033 | Non-HITL state | No interrupt, continues |

#### 2.5 Resume Flow Tests

| Test ID | Scenario | Validates |
|---------|----------|-----------|
| LG-040 | Resume after HITL approval | Continues to next node |
| LG-041 | Resume with no pending | No-op |
| LG-042 | Run until pause | Stops at first HITL |
| LG-043 | Run until complete | Reaches ARCHIVE_CLOSE |

---

### Module 3: Checkpoint System

**Purpose:** Verify graph state can be saved and restored.

#### 3.1 Checkpoint Store Tests

| Test ID | Description | Validates |
|---------|-------------|-----------|
| CK-001 | Save checkpoint | Creates DB row |
| CK-002 | Load checkpoint | Returns saved state |
| CK-003 | Overwrite checkpoint | Updates existing row |
| CK-004 | Nonexistent case | Returns None |
| CK-005 | Status check | Returns current_node |

#### 3.2 Checkpoint Integration Tests

| Test ID | Scenario | Validates |
|---------|----------|-----------|
| CK-010 | Execute state → checkpoint | state persisted |
| CK-011 | Interrupt → checkpoint | pending_interrupt persisted |
| CK-012 | Resume from checkpoint | Continues correctly |
| CK-013 | Multiple checkpoints | Latest wins |

---

### Module 4: Subgraph Patterns

**Purpose:** Verify the Plan → Retrieve → Synthesize pattern for complex states.

#### 4.1 Evaluation Subgraph Tests

| Test ID | Step | Validates |
|---------|------|-----------|
| SG-001 | plan | Creates orchestration with rag_queries |
| SG-002 | retrieve | Executes RAG searches |
| SG-003 | synthesize | Generates evaluation via LLM |
| SG-004 | node_history | Records plan, retrieve, synthesize |

#### 4.2 Drafting Subgraph Tests

| Test ID | Step | Validates |
|---------|------|-----------|
| SG-010 | plan | Creates orchestration with doc queries |
| SG-011 | retrieve | Gathers document context |
| SG-012 | review | Generates draft via LLM |
| SG-013 | node_history | Records all sub-steps |

#### 4.3 Orchestration Tests

| Test ID | Objective | Validates |
|---------|-----------|-----------|
| SG-020 | "evaluation" | Correct rag_queries for legal analysis |
| SG-021 | "draft" | Correct queries for document generation |
| SG-022 | Empty objective | Error handling |
| SG-023 | Tool calls in orchestration | Proper tool dispatch |

---

### Module 5: Adaptive Engine (V2 Stub)

**Purpose:** Verify adaptive engine interface and stub behavior.

| Test ID | Method | Expected |
|---------|--------|----------|
| AE-001 | `enable()` | Returns v2_stub status |
| AE-002 | `disable()` | Returns v2_stub status |
| AE-003 | `apply_rules([])` | Returns 0 rules applied |
| AE-004 | `auto_tune()` | Returns tunables dict |
| AE-005 | With real rules | Applies to tool modes |
| AE-006 | With approval rules | Updates HITL gates |

---

### Module 6: Data Analyzer (V2 Stub)

**Purpose:** Verify data analyzer interface and rule generation.

| Test ID | Method | Expected |
|---------|--------|----------|
| DA-001 | `has_sufficient_data()` | Checks tracker events |
| DA-002 | `analyze()` | Returns v2_stub status |
| DA-003 | `generate_rules()` | Returns empty list (stub) |
| DA-004 | `get_recommendations()` | Returns empty list (stub) |

---

### Module 7: Integration Tests

**Purpose:** End-to-end tests combining multiple V2 components.

#### 7.1 Full Workflow Test

| Test ID | Flow | Validates |
|---------|------|-----------|
| INT-001 | Create → Run → HITL → Resume → Complete | Full lifecycle |
| INT-002 | Create → Run until pause → Checkpoint | Interrupt + persist |
| INT-003 | Create → Run → Edit draft → Resume | Draft iteration |
| INT-004 | Multiple cases parallel | Case isolation |

#### 7.2 State Output Tests

| Test ID | State | Output Key | Validates |
|---------|-------|------------|-----------|
 be not describe. not. be users to not be not be toings like the not not to this. not not be the, to to. this, to this the ask to, to the,,, need to to the the the, to to,, the the.

 follow.

, need, to, the the
 the need need the, of and the, the the of.

.

 and this a. the. to laws. laws laws regulations.

 and.

:
| INT-010 | CLIENT_CAPTURE | client_record | Has client_name, case_id |
| INT-011 | CASE_EVALUVALUATION | evaluation_detail | Has evaluation_text |
| INT-012 | DOCUMENT_GENERATION | document_draft | Has draft_text |

---

### Module 8: Graph State Tests

**Purpose:** Verify case graph state fields are correctly maintained.

| Test ID | Field | Initial | After State |
|---------|-------|---------|------------|
| GS-001 | engine | "v2_langgraph" | Same |
| GS-002 | graph_run_id | None → hex | Persisted |
| GS-003 | current_node | CLIENT_CAPTURE | Updated |
| GS-004 | pending_interrupt | None | Set at HITL |
| GS-005 | node_history | [] | Appended |
| GS-006 | completed_states | {} | States added |

---

## Test Implementation Notes

### Mock Strategy

```python
# Mock LLM for tool calling
class MockToolCallingLLM:
    def decide_tool(self, user_input):
        if "send" in user_input.lower():
            return "<send_wechat>"
        if "search" in user_input.lower():
            return "<search_rag>"
        return "<none>"

# Mock LLM for generation
class MockLegalLLM:
    def generate(self, prompt, context=""):
        return f"Legal analysis of: {prompt[:100]}"

# Mock WeChat
class MockWeChat:
    status = WeChatStatus.CONNECTED
    async def send_message(self, contact, text):
        return True, "sent"
    async def create_group_chat(self, name, members):
        return True, "ok", {"group_id": "mock-123"}
```

### Test Data Fixtures

```python
SAMPLE_LEGAL_CASE = {
    "client_name": "PROS, INC.",
    "matter_type": "Commercial Lease Dispute",
    "source_channel": "WeChat",
    "initial_msg": "Tenant requests rent abatement.",
    "priority": "High",
}

SAMPLE_ORCHESTRATION = {
    "objective": "evaluation",
    "triage_summary": "Retrieve lease context first.",
    "rag_queries": ["rent abatement landlord work"],
    "tool_calls": [{"tool_name": "search_rag", "args": {"query": "..."}, "reason": "..."}],
    "final_instruction": "Use retrieved context for analysis.",
}
```

### Test Execution Order

```
tests/
├── test_tool_calling.py     # Module 1: Tool decision + execution
├── test_langgraph_engine.py # Module 2: Graph construction + execution
├── test_checkpoints.py      # Module 3: Checkpoint persistence
├── test_subgraphs.py        # Module 4: Subgraph patterns
├── test_adaptive.py         # Module 5: Adaptive engine
├── test_data_analyzer.py    # Module 6: Data analyzer
└── test_integration_v2.py   # Module 7: End-to-end
```

### Expected Test Counts

| Module | Tests | Priority |
|--------|-------|----------|
| Tool Calling | ~30 | P0 (core) |
| LangGraph Engine | ~25 | P0 (core) |
| Checkpoints | ~12 | P0 (core) |
| Subgraphs | ~15 | P1 |
| Adaptive Engine | ~8 | P3 (stub) |
| Data Analyzer | ~6 | P3 (stub) |
| Integration | ~10 | P1 |
| **Total** | **~95** | |

---

## Success Criteria

### Must Pass (P0)
- All tool calling decision tests
- Graph state transitions for all 10 states
- HITL interrupt and resume at 3 gates
- Checkpoint save/load/update

### Should Pass (P1)
- Subgraph plan→retrieve→synthesize
- End-to-end case lifecycle
- Parallel state handling (FEE+GROUP)

### Nice to Have (P2/P3)
- Adaptive engine stub tests
- Data analyzer stub tests
- Edge case error handling

---

## Future V2 Features (Not Yet Implemented)

These should be tested once implemented:

1. **Big model routing** — Route to larger LLM for complex evaluations
2. **Auto-promotion** — Tools auto-promote from manual to auto
3. **Auto-approval** — Safe states auto-approve based on history
4. **Parallel state execution** — FEE + GROUP truly parallel
5. **Dynamic chunk sizing** — RAG chunk size per document type
6. **OpenClaw re-enablement** — Real WeChat via OpenClaw runtime

---

*Plan generated: 2026-03-30*
*Based on: v2/langgraph_runtime.py, v2/adaptive_engine.py, v2/data_analyzer.py*
