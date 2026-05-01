# LangGraph V2 Plan

## Summary

This document defines a LangGraph-first V2 architecture for AgentFlow.
V2 keeps the current APIs usable, preserves the existing engine as fallback, and hard-disables the big-model path for now.
Phase 1 focuses on running the full workflow on the local small model through LangGraph.
Phase 2 prepares the structure for future multi-model expansion without turning the big-model path back on.

## Phase 1: Small-Model-Only LangGraph Core

### Goal

Replace custom AI and workflow orchestration with LangGraph while staying fully on the local small model.

### Deliverables

- LangGraph runtime
- graph state schema
- SQLite checkpointing
- top-level graph for all 10 SVG workflow states
- per-state subgraph decomposition
- tool wrappers for RAG, WeChat, PDF, ingestion, and tracking
- interrupt/resume flow for HITL gates
- API compatibility layer
- node-level progress and graph status fields

### Top-Level Workflow

1. `CLIENT_CAPTURE`
2. `INITIAL_CONTACT`
3. `CASE_EVALUATION`
4. `FEE_COLLECTION`
5. `GROUP_CREATION`
6. `MATERIAL_INGESTION`
7. `DOCUMENT_GENERATION`
8. `CLIENT_APPROVAL`
9. `FINAL_PDF_SEND`
10. `ARCHIVE_CLOSE`

### Phase 1 Rules

- LangGraph owns workflow execution, pause/resume, orchestration, and node history.
- The local small model is the only active model for AI nodes.
- Big-model API usage is hard-disabled by config and code-path guards.
- Current REST endpoints stay available and adapt into the V2 graph where appropriate.
- Plain CRUD stays outside the graph unless it advances workflow or triggers AI/tool work.

### Exit Criteria

- Full case flow can run through V2.
- All three HITL gates pause and resume correctly.
- No active execution path can call a big-model API.

## Phase 2: Structure for Future Multi-Model Expansion

### Goal

Harden and extend the small-model LangGraph system while preserving a dormant seam for later big-model reactivation.

### Deliverables

- stronger review/rewrite loops on the small model
- adaptive optimizer graphs
- richer telemetry and traceability
- node capability registry for future model routing
- prompt/template separation by node type
- evaluation hooks for future small-vs-big comparison

### Phase 2 Rules

- Future big-model nodes remain structurally defined but disabled.
- Candidate future big-model responsibilities:
  - legal synthesis
  - long-form drafting
  - final QA and rewrite
  - polished client-facing outputs
- Re-enabling the big model must require explicit config and tests.

### Exit Criteria

- V2 is the default engine.
- V1 remains fallback only.
- Big-model path is still disabled by default and covered by guard tests.

## Public Interface Expectations

- Preserve current API surface first.
- Extend case/status payloads with:
  - `engine`
  - `graph_run_id`
  - `current_node`
  - `pending_interrupt`
  - `node_history`
  - `checkpoint_status`
- Add helper endpoints as needed:
  - `POST /cases/{case_id}/execute`
  - `POST /cases/{case_id}/resume`
  - `GET /cases/{case_id}/graph`

## Test Plan

- Unit tests for subgraph happy paths and branch behavior
- checkpoint tests across all three HITL gates
- compatibility tests for current REST endpoints and WebSocket payloads
- tests proving active graph nodes use only the local small model
- guard tests proving big-model paths are disabled
- failure tests for missing MLX, RAG, WeChat/OpenClaw, tool failures, malformed retrieval output, and interrupted resume
- end-to-end tests for the SVG workflow from intake to archive

## Assumptions and Defaults

- LangGraph is the primary V2 orchestration framework.
- SQLite is the default checkpoint store.
- The local small model is the only active model in V2 for now.
- The big-model API path is preserved only as a disabled seam.
- The current engine remains available as fallback during migration.
