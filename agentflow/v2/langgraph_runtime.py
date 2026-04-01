import asyncio
import json
import os
import sqlite3
from datetime import UTC, datetime
from typing import Any
from uuid import uuid4

try:
    from langgraph.graph import END, StateGraph
    LANGGRAPH_AVAILABLE = True
except Exception:  # pragma: no cover - fallback for minimal environments
    END = "__END__"
    LANGGRAPH_AVAILABLE = False

    class _CompiledGraph:
        def __init__(self, entry_point, nodes, edges):
            self.entry_point = entry_point
            self.nodes = nodes
            self.edges = edges

        async def ainvoke(self, state, config=None):
            current = self.entry_point
            while current and current != END:
                update = await self.nodes[current](state)
                if update:
                    state.update(update)
                current = state.pop("_goto", None) or self.edges.get(current, END)
            return state

    class StateGraph:
        def __init__(self, _state_type):
            self._nodes = {}
            self._edges = {}
            self._entry_point = None

        def add_node(self, name, handler):
            self._nodes[name] = handler

        def add_edge(self, source, target):
            self._edges[source] = target

        def set_entry_point(self, name):
            self._entry_point = name

        def compile(self):
            return _CompiledGraph(self._entry_point, self._nodes, self._edges)


WORKFLOW_ORDER = [
    "CLIENT_CAPTURE",
    "INITIAL_CONTACT",
    "CASE_EVALUATION",
    "FEE_COLLECTION",
    "GROUP_CREATION",
    "MATERIAL_INGESTION",
    "DOCUMENT_GENERATION",
    "CLIENT_APPROVAL",
    "FINAL_PDF_SEND",
    "ARCHIVE_CLOSE",
]

PARALLEL_BRANCHES = {
    "CASE_EVALUATION": ["FEE_COLLECTION", "GROUP_CREATION"],
}

HITL_STATES = {"CASE_EVALUATION", "DOCUMENT_GENERATION", "FINAL_PDF_SEND"}


class SQLiteGraphCheckpointStore:
    def __init__(self, path="./data/tracking/langgraph_checkpoints.db"):
        self.path = path
        os.makedirs(os.path.dirname(self.path), exist_ok=True)
        self._init_db()

    def _connect(self):
        return sqlite3.connect(self.path)

    def _init_db(self):
        with self._connect() as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS graph_checkpoints (
                    case_id TEXT PRIMARY KEY,
                    checkpoint_id TEXT NOT NULL,
                    graph_run_id TEXT NOT NULL,
                    current_node TEXT,
                    pending_interrupt TEXT,
                    payload TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )
                """
            )
            conn.commit()

    def save(self, case_id: str, payload: dict[str, Any]):
        checkpoint_id = uuid4().hex[:12]
        with self._connect() as conn:
            conn.execute(
                """
                INSERT INTO graph_checkpoints(case_id, checkpoint_id, graph_run_id, current_node, pending_interrupt, payload, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(case_id) DO UPDATE SET
                    checkpoint_id=excluded.checkpoint_id,
                    graph_run_id=excluded.graph_run_id,
                    current_node=excluded.current_node,
                    pending_interrupt=excluded.pending_interrupt,
                    payload=excluded.payload,
                    updated_at=excluded.updated_at
                """,
                (
                    case_id,
                    checkpoint_id,
                    payload.get("graph_run_id", ""),
                    payload.get("current_node"),
                    payload.get("pending_interrupt"),
                    json.dumps(payload, ensure_ascii=False),
                    datetime.now(UTC).isoformat(),
                ),
            )
            conn.commit()
        return checkpoint_id

    def load(self, case_id: str) -> dict[str, Any] | None:
        with self._connect() as conn:
            row = conn.execute(
                "SELECT payload FROM graph_checkpoints WHERE case_id = ?",
                (case_id,),
            ).fetchone()
        if row is None:
            return None
        return json.loads(row[0])

    def status(self, case_id: str) -> dict[str, Any]:
        with self._connect() as conn:
            row = conn.execute(
                "SELECT checkpoint_id, graph_run_id, current_node, pending_interrupt, updated_at FROM graph_checkpoints WHERE case_id = ?",
                (case_id,),
            ).fetchone()
        if row is None:
            return {"available": False}
        return {
            "available": True,
            "checkpoint_id": row[0],
            "graph_run_id": row[1],
            "current_node": row[2],
            "pending_interrupt": row[3],
            "updated_at": row[4],
        }


class LangGraphWorkflowEngine:
    def __init__(self, agent_machine, checkpoint_store=None):
        self.agent = agent_machine
        self.checkpoints = checkpoint_store or SQLiteGraphCheckpointStore()
        self.compiled_graph = self._build_graph()

    @property
    def metadata(self):
        return {
            "engine": "v2_langgraph",
            "langgraph_available": LANGGRAPH_AVAILABLE,
            "checkpoint_backend": "sqlite",
            "big_model_enabled": False,
            "small_model_only": True,
        }

    def ensure_case_graph_state(self, case: dict):
        case.setdefault("engine", "v2_langgraph")
        case.setdefault("graph_run_id", None)
        case.setdefault("graph_checkpoint_id", None)
        case.setdefault("current_node", case["state"].name if hasattr(case["state"], "name") else str(case["state"]))
        case.setdefault("pending_interrupt", None)
        case.setdefault("node_history", [])
        case.setdefault("checkpoint_status", {"available": False})
        case.setdefault("model_policy", {"active": "small", "big_model_enabled": False})
        return case

    def _build_graph(self):
        graph = StateGraph(dict)
        graph.add_node("router", self._router_node)
        for state_name in WORKFLOW_ORDER:
            graph.add_node(state_name, self._build_state_node(state_name))
        graph.set_entry_point("router")
        for state_name in WORKFLOW_ORDER:
            graph.add_edge("router", state_name)
        return graph.compile()

    async def _router_node(self, state: dict[str, Any]):
        state["_goto"] = state["current_node"]
        return state

    def _build_state_node(self, state_name: str):
        async def runner(state: dict[str, Any]):
            case = self.agent.cases[state["case_id"]]
            self.ensure_case_graph_state(case)
            await self._record_node(case, f"{state_name}:start", "running")
            output = await self._run_state(case, state_name)
            await self._record_node(case, f"{state_name}:complete", "completed")
            state["last_output"] = output
            if state_name in HITL_STATES and not case["hitl_approvals"].get(state_name):
                case["pending_interrupt"] = state_name
                case["current_node"] = state_name
                state["pending_interrupt"] = state_name
                state["_goto"] = END
                visible_state = state_name
            else:
                case["pending_interrupt"] = None
                next_node = self._next_node(state_name)
                case["current_node"] = next_node or state_name
                if next_node:
                    state["current_node"] = next_node
                    state["_goto"] = END
                else:
                    state["current_node"] = state_name
                    state["_goto"] = END
                visible_state = next_node or state_name
            self._sync_case_state(case, visible_state)
            self._checkpoint_case(case)
            return state

        return runner

    async def _record_node(self, case: dict, node_name: str, status: str):
        case["node_history"].append(
            {
                "node": node_name,
                "status": status,
                "timestamp": datetime.now(UTC).isoformat(),
            }
        )
        case["node_history"] = case["node_history"][-50:]

    def _next_node(self, state_name: str) -> str | None:
        if state_name == "FEE_COLLECTION":
            return "GROUP_CREATION"
        if state_name == "GROUP_CREATION":
            return "MATERIAL_INGESTION"
        idx = WORKFLOW_ORDER.index(state_name)
        if idx + 1 >= len(WORKFLOW_ORDER):
            return None
        return WORKFLOW_ORDER[idx + 1]

    def _sync_case_state(self, case: dict, state_name: str):
        state_enum = self.agent.AgentState[state_name] if hasattr(self.agent, "AgentState") else None
        if state_enum is not None:
            case["state"] = state_enum
        case["status_label"] = state_name
        completed = set(case.get("completed_states", set()))
        for item in WORKFLOW_ORDER:
            if item == state_name:
                break
            completed.add(item)
        case["completed_states"] = completed
        case["updated_at"] = datetime.now(UTC).isoformat()
        case["engine"] = "v2_langgraph"
        case["model_policy"] = {"active": "small", "big_model_enabled": False}

    async def _run_state(self, case: dict, state_name: str):
        if state_name == "CASE_EVALUATION":
            return await self._run_evaluation_subgraph(case)
        if state_name == "DOCUMENT_GENERATION":
            return await self._run_drafting_subgraph(case)
        handler = getattr(self.agent, f"_process_{state_name.lower()}", None)
        if handler is None:
            return {"status": "missing_handler"}
        output = await handler(case)
        case["state_outputs"][state_name] = output
        return output

    async def _run_evaluation_subgraph(self, case: dict):
        await self._record_node(case, "CASE_EVALUATION:plan", "completed")
        orchestration = await self.agent.orchestrate_case(case["case_id"], objective="evaluation")
        await self._record_node(case, "CASE_EVALUATION:retrieve", "completed")
        output = await self.agent._process_case_evaluation(case, orchestration=orchestration)
        await self._record_node(case, "CASE_EVALUATION:synthesize", "completed")
        case["state_outputs"]["CASE_EVALUATION"] = output
        case["ai_last_orchestration"] = orchestration
        return output

    async def _run_drafting_subgraph(self, case: dict):
        await self._record_node(case, "DOCUMENT_GENERATION:plan", "completed")
        orchestration = await self.agent.orchestrate_case(case["case_id"], objective="draft")
        await self._record_node(case, "DOCUMENT_GENERATION:retrieve", "completed")
        output = await self.agent._process_document_generation(case, orchestration=orchestration)
        await self._record_node(case, "DOCUMENT_GENERATION:review", "completed")
        case["state_outputs"]["DOCUMENT_GENERATION"] = output
        case["ai_last_orchestration"] = orchestration
        return output

    def _checkpoint_case(self, case: dict):
        payload = {
            "graph_run_id": case["graph_run_id"],
            "current_node": case["current_node"],
            "pending_interrupt": case["pending_interrupt"],
            "node_history": case["node_history"],
            "model_policy": case["model_policy"],
        }
        checkpoint_id = self.checkpoints.save(case["case_id"], payload)
        case["graph_checkpoint_id"] = checkpoint_id
        case["checkpoint_status"] = self.checkpoints.status(case["case_id"])

    async def execute_current_state(self, case_id: str):
        case = self.agent.cases[case_id]
        self.ensure_case_graph_state(case)
        if case["graph_run_id"] is None:
            case["graph_run_id"] = uuid4().hex[:12]

        pending_parallel = case.get("_parallel_pending", [])
        if pending_parallel:
            next_node = pending_parallel[0]
            case["_parallel_pending"] = pending_parallel[1:]
            case["current_node"] = next_node
        else:
            next_node = case.get("current_node")

        if next_node in PARALLEL_BRANCHES:
            parallel_nodes = PARALLEL_BRANCHES[next_node]
            case["_parallel_pending"] = parallel_nodes[1:]
            next_node = parallel_nodes[0]
            case["current_node"] = next_node

        state = {
            "case_id": case_id,
            "current_node": next_node,
            "pending_interrupt": case.get("pending_interrupt"),
            "graph_run_id": case["graph_run_id"],
        }
        node_fn = self._build_state_node(next_node)
        result = await node_fn(state)
        self.agent._invalidate_cache(case_id)
        self.agent.notify_change()
        return {"status": "executed", "state": result.get("current_node"), "pending_interrupt": case["pending_interrupt"]}

    async def run_until_pause(self, case_id: str):
        case = self.agent.cases[case_id]
        self.ensure_case_graph_state(case)
        if case["graph_run_id"] is None:
            case["graph_run_id"] = uuid4().hex[:12]
        while True:
            await self.execute_current_state(case_id)
            if case.get("pending_interrupt"):
                break
            next_node = case.get("current_node")
            if next_node == WORKFLOW_ORDER[-1] and case.get("state_outputs", {}).get("ARCHIVE_CLOSE"):
                break
            if next_node == case["state"].name and case["state"].name == WORKFLOW_ORDER[-1]:
                break
            if next_node is None:
                break
        return self.get_graph_status(case_id)

    async def resume_case(self, case_id: str):
        case = self.agent.cases[case_id]
        self.ensure_case_graph_state(case)
        pending = case.get("pending_interrupt")
        if pending and case["hitl_approvals"].get(pending):
            case["pending_interrupt"] = None
            next_node = self._next_node(pending)
            if next_node:
                case["current_node"] = next_node
        return await self.run_until_pause(case_id)

    def sync_approval(self, case_id: str, state_name: str, approved: bool):
        case = self.agent.cases[case_id]
        self.ensure_case_graph_state(case)
        # Approvals can be submitted before any graph execution occurs.
        # Ensure graph_run_id is always set before checkpoint writes (SQLite schema requires NOT NULL).
        if case.get("graph_run_id") is None:
            case["graph_run_id"] = uuid4().hex[:12]
        if approved and case.get("pending_interrupt") == state_name:
            case["pending_interrupt"] = None
            next_node = self._next_node(state_name)
            if next_node:
                case["current_node"] = next_node
        self._checkpoint_case(case)
        self.agent._invalidate_cache(case_id)

    def get_graph_status(self, case_id: str):
        case = self.agent.cases[case_id]
        self.ensure_case_graph_state(case)
        case["checkpoint_status"] = self.checkpoints.status(case_id)
        return {
            "engine": "v2_langgraph",
            "langgraph_available": LANGGRAPH_AVAILABLE,
            "graph_run_id": case.get("graph_run_id"),
            "graph_checkpoint_id": case.get("graph_checkpoint_id"),
            "current_node": case.get("current_node"),
            "pending_interrupt": case.get("pending_interrupt"),
            "node_history": case.get("node_history", []),
            "checkpoint_status": case.get("checkpoint_status", {"available": False}),
            "model_policy": case.get("model_policy", {"active": "small", "big_model_enabled": False}),
        }
