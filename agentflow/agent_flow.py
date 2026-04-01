import asyncio
import hashlib
import json
import time
from collections import deque
from copy import deepcopy
from dataclasses import dataclass, field
from datetime import datetime, UTC
from enum import Enum, auto
from typing import Dict, List, Optional, Any
from uuid import uuid4


class _HitlGate:
    """Async event replacement that is safe across event loops and serializable."""
    __slots__ = ("_flag",)

    def __init__(self):
        self._flag = False

    def set(self):
        self._flag = True

    def is_set(self) -> bool:
        return self._flag

    def wait(self):
        raise RuntimeError("Synchronous wait not supported - use await wait_async()")

    async def wait_async(self, timeout: float | None = None) -> bool:
        loop = asyncio.get_running_loop()
        try:
            if timeout is not None:
                await asyncio.wait_for(loop.run_in_executor(None, self._spin_wait), timeout=timeout)
            else:
                await loop.run_in_executor(None, self._spin_wait)
            return True
        except asyncio.TimeoutError:
            return False

    def _spin_wait(self):
        while not self._flag:
            time.sleep(0.05)

    def clear(self):
        self._flag = False

    def __getstate__(self):
        return {"_flag": self._flag}

    def __setstate__(self, state):
        self._flag = state["_flag"]

from auto_device import detect_device, recommend_config
from llm_provider import OptimizedLLMProvider
from rag_manager import RAGManager
from tool_registry_v1 import ToolRegistryV1, ToolMode
from usage_tracker import UsageTracker
from v2.langgraph_runtime import LangGraphWorkflowEngine
from wechat_connector import WeChatConnector


class AgentState(Enum):
    CLIENT_CAPTURE = auto()
    INITIAL_CONTACT = auto()
    CASE_EVALUATION = auto()
    FEE_COLLECTION = auto()
    GROUP_CREATION = auto()
    MATERIAL_INGESTION = auto()
    DOCUMENT_GENERATION = auto()
    CLIENT_APPROVAL = auto()
    FINAL_PDF_SEND = auto()
    ARCHIVE_CLOSE = auto()


@dataclass
class StateNode:
    name: str
    dependencies: List[str] = field(default_factory=list)
    is_hitl_gate: bool = False


WORKFLOW_GRAPH = {
    "CLIENT_CAPTURE": StateNode("CLIENT_CAPTURE", []),
    "INITIAL_CONTACT": StateNode("INITIAL_CONTACT", ["CLIENT_CAPTURE"]),
    "CASE_EVALUATION": StateNode("CASE_EVALUATION", ["INITIAL_CONTACT"], is_hitl_gate=True),
    "FEE_COLLECTION": StateNode("FEE_COLLECTION", ["CASE_EVALUATION"]),
    "GROUP_CREATION": StateNode("GROUP_CREATION", ["CASE_EVALUATION"]),
    "MATERIAL_INGESTION": StateNode("MATERIAL_INGESTION", ["FEE_COLLECTION", "GROUP_CREATION"]),
    "DOCUMENT_GENERATION": StateNode("DOCUMENT_GENERATION", ["MATERIAL_INGESTION"], is_hitl_gate=True),
    "CLIENT_APPROVAL": StateNode("CLIENT_APPROVAL", ["DOCUMENT_GENERATION"]),
    "FINAL_PDF_SEND": StateNode("FINAL_PDF_SEND", ["CLIENT_APPROVAL"], is_hitl_gate=True),
    "ARCHIVE_CLOSE": StateNode("ARCHIVE_CLOSE", ["FINAL_PDF_SEND"]),
}

HITL_STATES = {"CASE_EVALUATION", "DOCUMENT_GENERATION", "FINAL_PDF_SEND"}

WORKFLOW_ORDER = [
    "CLIENT_CAPTURE", "INITIAL_CONTACT", "CASE_EVALUATION",
    "FEE_COLLECTION", "GROUP_CREATION", "MATERIAL_INGESTION",
    "DOCUMENT_GENERATION", "CLIENT_APPROVAL", "FINAL_PDF_SEND", "ARCHIVE_CLOSE",
]


def default_case_data(client_name="ClientX", matter_type="Commercial Lease Dispute", source_channel="WeChat", initial_msg=""):
    case_id = f"LAW-2026-{uuid4().hex[:6].upper()}"
    return {
        "client_name": client_name,
        "case_id": case_id,
        "matter_type": matter_type,
        "source_channel": source_channel,
        "initial_msg": initial_msg,
        "is_paid": False,
        "notes": [],
        "uploaded_documents": [],
        "draft_preview": "",
        "evaluation": "",
        "state": AgentState.CLIENT_CAPTURE,
        "status_label": "Intake",
        "priority": "High",
        "created_at": datetime.now(UTC).isoformat(),
        "updated_at": datetime.now(UTC).isoformat(),
        "hitl_approvals": {name: False for name in HITL_STATES},
        "hitl_events": {name: _HitlGate() for name in HITL_STATES},
        "client_record": None,
        "contact_log": None,
        "evaluation_detail": None,
        "fee_record": None,
        "group_info": None,
        "ingestion_summary": None,
        "document_draft": None,
        "approval_record": None,
        "delivery_record": None,
        "ai_action_plan": None,
        "ai_last_orchestration": None,
        "engine": "v2_langgraph",
        "graph_run_id": None,
        "graph_checkpoint_id": None,
        "current_node": "CLIENT_CAPTURE",
        "pending_interrupt": None,
        "node_history": [],
        "checkpoint_status": {"available": False},
        "model_policy": {"active": "small", "big_model_enabled": False},
        "highlights": [],
        "state_outputs": {},
        "completed_states": set(),
        "_status_cache": None,
        "_cache_valid": False,
        "billing_entries": [],
        "billing_active": False,
        "billing_start_time": None,
        "billing_description": "",
        "deadlines": [],
    }


class LegalAgentMachine:
    def __init__(self, provider_type="local", api_key=None, wechat=None, rag=None, llm=None):
        self.AgentState = AgentState
        self.device_info = detect_device()
        self.device_config = recommend_config(self.device_info)
        self.model = self.device_config["model"]
        # WeChat is paused by default (see AGENTFLOW_ENABLE_WECHAT).
        self.wechat = wechat or WeChatConnector(mode="auto")
        self.rag = rag or RAGManager()
        self.llm = llm or OptimizedLLMProvider(model_name=self.model)
        self.tracker = UsageTracker()
        self.tool_registry = ToolRegistryV1(usage_tracker=self.tracker, default_mode=ToolMode.MANUAL)
        self.cases: Dict[str, dict] = {}
        self.max_cases = 200  # Memory cap: LRU eviction when exceeded
        self.active_case_id = None
        self._change_event = asyncio.Event()
        self._agent_log: deque = deque(maxlen=200)
        self._state_start_times: Dict[str, float] = {}
        self.v2_engine = LangGraphWorkflowEngine(self)
        bootstrap_case = self.create_case()
        self.select_case(bootstrap_case["case_id"])
        self._register_wechat_handler()

    @property
    def change_event(self):
        return self._change_event

    def notify_change(self):
        self._change_event.set()

    def _log(self, message: str):
        timestamp = datetime.now(UTC).strftime("%H:%M:%S")
        entry = f"[{timestamp}] {message}"
        self._agent_log.append(entry)
        print(entry)

    def get_agent_log(self) -> list[str]:
        return list(self._agent_log)

    def _register_wechat_handler(self):
        @self.wechat.on_message
        async def handle_new_case(msg):
            await self.handle_incoming_message(
                contact_name=msg["from"],
                text=msg["text"],
                source_channel="WeChat",
                metadata=msg.get("metadata"),
            )

    def find_case_by_client_name(self, client_name: str) -> str | None:
        if not client_name or not str(client_name).strip():
            return None
        key = " ".join(str(client_name).strip().split()).casefold()
        for cid, case in self.cases.items():
            cn = " ".join(str(case.get("client_name", "") or "").strip().split()).casefold()
            if cn == key:
                return cid
        return None

    def find_or_create_case_for_client(
        self,
        client_name: str,
        matter_type: str = "Commercial Lease Dispute",
        source_channel: str = "Document Upload",
        initial_msg: str = "",
    ):
        cid = self.find_case_by_client_name(client_name)
        if cid:
            self.select_case(cid)
            return {"case_id": cid, "created": False, "case": self.get_case_status(cid)}
        display_name = " ".join(client_name.strip().split())
        case = self.create_case(
            client_name=display_name,
            matter_type=matter_type,
            source_channel=source_channel,
            initial_msg=initial_msg or f"Case opened from document upload for {display_name}.",
        )
        self.select_case(case["case_id"])
        return {"case_id": case["case_id"], "created": True, "case": self.get_case_status(case["case_id"])}

    def sync_uploaded_docs_with_rag(self):
        valid = {d.get("filename") for d in self.rag.documents if d.get("filename")}
        for case in self.cases.values():
            ud = list(case.get("uploaded_documents") or [])
            case["uploaded_documents"] = [u for u in ud if u in valid]
        self.notify_change()

    def create_case(self, client_name="ClientX", matter_type="Commercial Lease Dispute", source_channel="WeChat", initial_msg=""):
        # Memory cap: evict oldest archived cases when limit exceeded
        if len(self.cases) >= self.max_cases:
            self._evict_oldest_cases()
        
        case = default_case_data(
            client_name=client_name,
            matter_type=matter_type,
            source_channel=source_channel,
            initial_msg=initial_msg,
        )
        if initial_msg:
            case["notes"].append(f"Initial intake captured: {initial_msg}")
        self.cases[case["case_id"]] = case
        self.v2_engine.ensure_case_graph_state(case)
        if not self.active_case_id:
            self.active_case_id = case["case_id"]
        self.tracker.record_case_created(matter_type=matter_type, source_channel=source_channel)
        self.notify_change()
        return deepcopy(case)

    def _evict_oldest_cases(self):
        """Evict oldest non-active cases to stay under memory cap."""
        # Sort by updated_at, oldest first
        sorted_cases = sorted(
            [(cid, c) for cid, c in self.cases.items() if cid != self.active_case_id],
            key=lambda x: x[1].get("updated_at", ""),
        )
        # Evict oldest 20% of cases
        evict_count = max(1, len(sorted_cases) // 5)
        for cid, _ in sorted_cases[:evict_count]:
            # Archive case to disk before eviction
            self._archive_case(cid)
            del self.cases[cid]
        print(f"[Memory] Evicted {evict_count} old cases to stay under {self.max_cases} case limit")

    def _archive_case(self, case_id: str):
        """Archive a case to disk before eviction."""
        import json
        case = self.cases.get(case_id)
        if not case:
            return
        archive_dir = os.path.join(os.path.dirname(__file__), "data", "archived_cases")
        os.makedirs(archive_dir, exist_ok=True)
        archive_path = os.path.join(archive_dir, f"{case_id}.json")
        with open(archive_path, "w", encoding="utf-8") as f:
            json.dump(case, f, ensure_ascii=False, indent=2)

    def select_case(self, case_id):
        if case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {case_id}")
        self.active_case_id = case_id
        self.notify_change()
        return self.get_case_status(case_id)

    def current_case(self):
        return self.cases[self.active_case_id]

    def _invalidate_cache(self, case_id):
        case = self.cases.get(case_id)
        if case:
            case["_cache_valid"] = False
            case["_status_cache"] = None

    def update_case(self, case_id, **fields):
        case = self.cases[case_id]
        protected = {"case_id", "hitl_events", "hitl_approvals", "completed_states", "_status_cache", "_cache_valid"}
        for k, v in fields.items():
            if k not in protected:
                case[k] = v
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._invalidate_cache(case_id)
        self.tracker.record_user_action("update_case", f"updated fields: {list(fields.keys())}")
        self.v2_engine.ensure_case_graph_state(case)
        self.notify_change()
        return deepcopy(case)

    def delete_case(self, case_id):
        if case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {case_id}")
        del self.cases[case_id]
        if self.active_case_id == case_id:
            remaining = list(self.cases.keys())
            self.active_case_id = remaining[0] if remaining else None
        self.tracker.record_user_action("delete_case", case_id)
        self.notify_change()
        return {"deleted": case_id}

    def add_note(self, case_id, note):
        case = self.cases[case_id]
        case["notes"].append(note)
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._invalidate_cache(case_id)
        self.v2_engine.ensure_case_graph_state(case)
        self.tracker.record_user_action("add_note", f"case {case_id}")
        self.notify_change()

    def edit_note(self, case_id, note_index, new_text):
        case = self.cases[case_id]
        if note_index < 0 or note_index >= len(case["notes"]):
            raise IndexError(f"Note index {note_index} out of range")
        case["notes"][note_index] = new_text
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._invalidate_cache(case_id)
        self.notify_change()
        return {"edited": note_index}

    def delete_note(self, case_id, note_index):
        case = self.cases[case_id]
        if note_index < 0 or note_index >= len(case["notes"]):
            raise IndexError(f"Note index {note_index} out of range")
        case["notes"].pop(note_index)
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._invalidate_cache(case_id)
        self.notify_change()
        return {"deleted": note_index}

    def advance_state(self, case_id, target_state=None):
        case = self.cases[case_id]
        current_idx = WORKFLOW_ORDER.index(case["state"].name) if case["state"].name in WORKFLOW_ORDER else -1
        if target_state:
            if target_state not in AgentState.__members__:
                raise ValueError(f"Invalid state: {target_state}")
            new_state = target_state
        else:
            next_idx = current_idx + 1
            if next_idx >= len(WORKFLOW_ORDER):
                raise ValueError("Already at final state")
            new_state = WORKFLOW_ORDER[next_idx]
        prev = case["state"].name
        case["state"] = AgentState[new_state]
        case["status_label"] = new_state
        case["updated_at"] = datetime.now(UTC).isoformat()
        completed = case.get("completed_states", set())
        completed.add(prev)
        case["completed_states"] = completed
        self._invalidate_cache(case_id)
        self.tracker.record_state_transition(prev, new_state, case_id, 0)
        self.tracker.record_user_action("advance_state", f"{prev}->{new_state}")
        self._log(f"[Manual] State advanced: {prev} -> {new_state}")
        self.notify_change()
        return self.get_case_status(case_id)

    def rewind_state(self, case_id, target_state):
        if target_state not in AgentState.__members__:
            raise ValueError(f"Invalid state: {target_state}")
        case = self.cases[case_id]
        prev = case["state"].name
        case["state"] = AgentState[target_state]
        case["status_label"] = target_state
        case["updated_at"] = datetime.now(UTC).isoformat()
        completed = case.get("completed_states", set())
        completed.discard(target_state)
        for state in WORKFLOW_ORDER:
            if state == target_state:
                break
            completed.add(state)
        case["completed_states"] = completed
        case["hitl_approvals"][target_state] = False
        if target_state in case.get("hitl_events", {}):
            case["hitl_events"][target_state] = _HitlGate()
        self._invalidate_cache(case_id)
        self.tracker.record_state_transition(prev, target_state, case_id, 0)
        self.tracker.record_user_action("rewind_state", f"{prev}->{target_state}")
        self.notify_change()
        return self.get_case_status(case_id)

    def set_approval(self, state_name, approved, case_id=None, reason=""):
        case = self.cases[case_id or self.active_case_id]
        prev_approved = case["hitl_approvals"].get(state_name, False)
        case["hitl_approvals"][state_name] = approved
        if state_name in case.get("hitl_events", {}):
            case["hitl_events"][state_name].set()
        note_text = f"Human approval for {state_name}: {'Approved' if approved else 'REJECTED'}"
        if reason:
            note_text += f" — Reason: {reason}"
        self.add_note(case["case_id"], note_text + ".")
        if approved != prev_approved:
            start = self._state_start_times.pop(state_name, time.time())
            self.tracker.record_approval(
                state=state_name,
                approved=approved,
                response_time_s=time.time() - start,
            )
        self.v2_engine.sync_approval(case["case_id"], state_name, approved)
        self.notify_change()

    def edit_draft(self, case_id, new_draft_text):
        case = self.cases[case_id]
        case["draft_preview"] = new_draft_text[:500]
        if case.get("document_draft"):
            case["document_draft"]["draft_text"] = new_draft_text
            case["document_draft"]["draft_preview"] = new_draft_text[:500]
            case["document_draft"]["edited_at"] = datetime.now(UTC).isoformat()
            case["document_draft"]["edited_by"] = "operator"
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._invalidate_cache(case_id)
        self.tracker.record_user_action("edit_draft", case_id)
        self.add_note(case_id, f"Draft edited by operator ({len(new_draft_text)} chars).")
        return self.get_case_status(case_id)

    def get_case_status(self, case_id=None):
        case_id = case_id or self.active_case_id
        if case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {case_id}")
        case = self.cases[case_id]
        if case.get("_cache_valid") and case.get("_status_cache"):
            return case["_status_cache"]

        snapshot = {k: v for k, v in case.items() if k not in ("hitl_events", "_status_cache", "_cache_valid", "completed_states")}
        snapshot["state"] = case["state"].name
        snapshot["model"] = self.model
        snapshot["engine"] = case.get("engine", "v1")
        snapshot["graph_run_id"] = case.get("graph_run_id")
        snapshot["graph_checkpoint_id"] = case.get("graph_checkpoint_id")
        snapshot["current_node"] = case.get("current_node")
        snapshot["pending_interrupt"] = case.get("pending_interrupt")
        snapshot["node_history"] = case.get("node_history", [])
        snapshot["checkpoint_status"] = case.get("checkpoint_status", {"available": False})
        snapshot["model_policy"] = case.get("model_policy", {"active": "small", "big_model_enabled": False})
        snapshot["wechat_status"] = self.wechat.status.name
        snapshot["wechat_mode"] = self.wechat.adapter_mode
        snapshot["rag_ready"] = True
        snapshot["rag_backend_mode"] = self.rag.backend_mode
        snapshot["rag_error"] = self.rag.init_error
        snapshot["completed_states"] = list(case.get("completed_states", set()))
        case["_status_cache"] = snapshot
        case["_cache_valid"] = True
        return snapshot

    def get_case_detail(self, case_id):
        if case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {case_id}")
        status = self.get_case_status(case_id)
        case = self.cases[case_id]
        status["evaluation_detail"] = case.get("evaluation_detail")
        status["document_draft"] = case.get("document_draft")
        status["fee_record"] = case.get("fee_record")
        status["contact_log"] = case.get("contact_log")
        status["group_info"] = case.get("group_info")
        status["approval_record"] = case.get("approval_record")
        status["delivery_record"] = case.get("delivery_record")
        status["client_record"] = case.get("client_record")
        status["ingestion_summary"] = case.get("ingestion_summary")
        status["ai_action_plan"] = case.get("ai_action_plan")
        status["ai_last_orchestration"] = case.get("ai_last_orchestration")
        status["highlights"] = case.get("highlights", [])
        status["graph_status"] = self.v2_engine.get_graph_status(case_id)
        return status

    def _tool_catalog_for_prompt(self):
        tools = []
        for tool in self.tool_registry.list_tools():
            tools.append(
                {
                    "name": tool["name"],
                    "mode": tool["mode"],
                    "requires_confirmation": tool["requires_confirmation"],
                    "description": tool["description"],
                }
            )
        return tools

    def _case_context_for_ai(self, case: dict) -> str:
        compact = {
            "case_id": case["case_id"],
            "client_name": case["client_name"],
            "matter_type": case["matter_type"],
            "source_channel": case["source_channel"],
            "initial_msg": case.get("initial_msg", ""),
            "notes": case.get("notes", [])[-6:],
            "uploaded_documents": case.get("uploaded_documents", []),
            "evaluation": case.get("evaluation", ""),
            "draft_preview": case.get("draft_preview", ""),
            "tool_catalog": self._tool_catalog_for_prompt(),
        }
        return json.dumps(compact, ensure_ascii=False, indent=2)

    def _fallback_action_plan(self, objective: str) -> dict:
        if objective == "draft":
            rag_queries = ["legal document template", "facts timeline", "claims and remedies"]
        else:
            rag_queries = ["rights remedies obligations", "evidence requirements", "risk analysis"]
        return {
            "objective": objective,
            "triage_summary": "Use RAG to gather targeted support, then synthesize a structured response.",
            "rag_queries": rag_queries,
            "tool_calls": [
                {
                    "tool_name": "search_rag",
                    "args": {"query": query},
                    "reason": "Collect supporting legal context.",
                }
                for query in rag_queries
            ],
            "final_instruction": "Produce a concise, well-structured result grounded in the retrieved material.",
        }

    async def orchestrate_case(self, case_id=None, objective="evaluation"):
        case = self.cases[case_id or self.active_case_id]
        case_context = self._case_context_for_ai(case)
        planner_prompt = (
            "Create a JSON plan for this legal workflow run.\n"
            f"Objective: {objective}\n"
            "Required keys: objective, triage_summary, rag_queries, tool_calls, final_instruction.\n"
            "tool_calls must be an array of objects with keys: tool_name, args, reason.\n"
            "Prefer repeated search_rag calls for targeted queries. Only include manual tools as recommendations.\n"
            "Keep rag_queries to 2-4 items."
        )
        plan = await asyncio.to_thread(
            self.llm.generate_structured_json,
            planner_prompt,
            case_context,
            "small",
            self._fallback_action_plan(objective),
        )

        rag_queries = [
            q for q in plan.get("rag_queries", [])
            if isinstance(q, str) and q.strip()
        ][:4]
        if not rag_queries:
            rag_queries = self._fallback_action_plan(objective)["rag_queries"]

        tool_runs = []
        for query in rag_queries:
            structured_hits = self.rag.search_structured(query, k=3)
            tool_runs.append(
                {
                    "tool_name": "search_rag",
                    "query": query,
                    "result": {"success": True, "latency_ms": 0, "output": {}},
                    "hits": structured_hits,
                }
            )

        aggregated_context_parts = []
        for run in tool_runs:
            snippets = [item.get("chunk", "") for item in run["hits"][:2] if item.get("chunk")]
            if snippets:
                aggregated_context_parts.append(f"Query: {run['query']}\n" + "\n".join(snippets))
        aggregated_context = "\n\n".join(aggregated_context_parts)

        if objective == "draft":
            synthesis_prompt = (
                f"请为当事人{case['client_name']}起草一份{case['matter_type']}法律文书。"
                "请包含事实陈述、法律依据、诉讼请求和下一步建议。"
            )
        else:
            synthesis_prompt = (
                f"请评估{case['client_name']}的{case['matter_type']}案件。"
                "请输出案情摘要、法律依据、关键证据、主要风险、建议动作。"
            )
        final_instruction = plan.get("final_instruction", "")
        synthesis_context = (
            f"Planner summary:\n{plan.get('triage_summary', '')}\n\n"
            f"Planner instruction:\n{final_instruction}\n\n"
            f"Retrieved evidence:\n{aggregated_context}"
        )
        synthesis = await asyncio.to_thread(self.llm.generate, synthesis_prompt, synthesis_context)

        orchestration = {
            "objective": objective,
            "plan": plan,
            "tool_runs": tool_runs,
            "aggregated_context_preview": aggregated_context[:1200],
            "synthesis": synthesis,
            "ran_at": datetime.now(UTC).isoformat(),
        }
        case["ai_action_plan"] = plan
        case["ai_last_orchestration"] = orchestration
        self.add_note(
            case["case_id"],
            f"AI orchestration completed for {objective} using {len(tool_runs)} RAG tool calls.",
        )
        return orchestration

    def list_cases(self):
        ordered = sorted(self.cases.values(), key=lambda item: item["updated_at"], reverse=True)
        return [self.get_case_status(case["case_id"]) for case in ordered]

    def attach_document_to_case(self, filename, case_id=None):
        case = self.cases[case_id or self.active_case_id]
        if filename not in case["uploaded_documents"]:
            case["uploaded_documents"].append(filename)
            self.add_note(case["case_id"], f"Document uploaded: {filename}.")

    def signal_rag_ingestion(self, note, case_id=None):
        case = self.cases[case_id or self.active_case_id]
        self.add_note(case["case_id"], f"RAG signal: {note}")

    async def handle_incoming_message(self, contact_name, text, source_channel="WeChat", metadata=None):
        case = self.create_case(
            client_name=contact_name,
            source_channel=source_channel,
            initial_msg=text,
        )
        self.v2_engine.ensure_case_graph_state(self.cases[case["case_id"]])
        self.select_case(case["case_id"])
        self.add_note(case["case_id"], f"New case intake received from {contact_name}.")
        if metadata:
            self.add_note(case["case_id"], f"Connector metadata: {metadata}")
        return self.get_case_status(case["case_id"])

    async def run(self):
        self._log(f"[*] Starting LegalAgentMachine [V1 MODE] on {self.system_info['chip_name']}")
        self._log(f"[*] Device: {self.device_info['platform_id']}")
        self._log(f"[*] Recommended Model: {self.model}")
        self._log(f"[*] LLM Backend: {self.device_config['llm_backend']}")
        self._log(f"[*] OpenClaw: PAUSED (V1 mode, mock WeChat)")
        self._log(f"[*] Tracking: active (budget {self.tracker.DATA_BUDGET_MB}MB)")
        await self.wechat.login()
        self._log("[*] Waiting for incoming messages (mock mode)...")
        await asyncio.sleep(2)
        await self.wechat.simulate_incoming_message("ClientX", "I have a dispute over a commercial lease.")
        while True:
            await asyncio.sleep(10)

    async def process_all_states(self, case_id=None):
        case = self.cases[case_id or self.active_case_id]
        self.v2_engine.ensure_case_graph_state(case)
        self._log("[*] Starting DAG-based state execution...")
        completed = set(case.get("completed_states", set()))

        while len(completed) < len(WORKFLOW_GRAPH):
            ready = [
                name for name, node in WORKFLOW_GRAPH.items()
                if name not in completed
                and all(dep in completed for dep in node.dependencies)
            ]
            if not ready:
                await asyncio.sleep(0.5)
                continue

            if len(ready) > 1:
                self._log(f"[DAG] Running in parallel: {ready}")
                tasks = [self._execute_state_node(name, case) for name in ready]
                await asyncio.gather(*tasks)
            else:
                await self._execute_state_node(ready[0], case)

            for name in ready:
                completed.add(name)
            case["completed_states"] = completed
            self._invalidate_cache(case["case_id"])
            self.notify_change()

        self._log("[*] All states completed. Case archived.")

    async def _execute_state_node(self, state_name, case):
        prev_state = case["state"].name if isinstance(case["state"], AgentState) else str(case["state"])
        self._log(f"[Flow] Executing: {state_name} | Case: {case['case_id']}")
        case["state"] = AgentState[state_name]
        case["status_label"] = state_name
        case["updated_at"] = datetime.now(UTC).isoformat()
        self._state_start_times[state_name] = time.time()

        handler = getattr(self, f"_process_{state_name.lower()}", None)
        if handler:
            output = await handler(case)
            case["state_outputs"][state_name] = output
            self._log(f"[Flow] {state_name} output: {output.get('status', 'done')}")
        else:
            case["state_outputs"][state_name] = {"status": "no_handler"}

        elapsed = time.time() - self._state_start_times.get(state_name, time.time())
        self.tracker.record_state_transition(
            from_state=prev_state,
            to_state=state_name,
            case_id=case["case_id"],
            duration_s=elapsed,
        )

        node = WORKFLOW_GRAPH[state_name]
        if node.is_hitl_gate:
            if not case["hitl_approvals"].get(state_name):
                self._log(f"[HITL] Pausing at {state_name} for human approval...")
                self._state_start_times[state_name] = time.time()
                evt = case["hitl_events"].get(state_name)
                if evt:
                    await evt.wait_async()
                else:
                    while not case["hitl_approvals"].get(state_name):
                        await asyncio.sleep(1)
                if not case["hitl_approvals"].get(state_name):
                    self._log(f"[HITL] {state_name} was rejected. Pipeline halted for this state.")
                    case["state_outputs"][state_name] = {"status": "rejected", "halted": True}
                    return
                self._log(f"[HITL] Approval received for {state_name}. Continuing...")

    async def _process_client_capture(self, case):
        self._log(f"[Capture] Capturing client record: {case['client_name']} ({case['matter_type']})")
        client_record = {
            "client_name": case["client_name"],
            "case_id": case["case_id"],
            "matter_type": case["matter_type"],
            "source_channel": case["source_channel"],
            "initial_msg": case.get("initial_msg", ""),
            "priority": case["priority"],
            "captured_at": datetime.now(UTC).isoformat(),
        }
        case["client_record"] = client_record
        await self.wechat.send_message(
            "File Transfer",
            f"[Bot] Client Captured: {case['client_name']} | Case: {case['case_id']} | Type: {case['matter_type']}",
        )
        self.add_note(case["case_id"], f"Client record captured from {case['source_channel']}: {case['client_name']} ({case['matter_type']}).")
        return {"status": "captured", "client_record": client_record}

    async def _process_initial_contact(self, case):
        self._log(f"[Contact] Sending initial contact to {case['client_name']} via {case['source_channel']}")
        client_name = case["client_name"]
        matter_type = case["matter_type"]
        message = (
            f"您好 {client_name}，我们已收到您的{matter_type}咨询。"
            f"请提供更多案件详情，包括合同签署时间、争议金额及对方当事人信息。"
        )
        sent, send_detail = await self.wechat.send_message(client_name, message)
        contact_log = {
            "contact_name": client_name,
            "channel": case["source_channel"],
            "message_sent": message,
            "sent_successfully": sent,
            "send_detail": send_detail,
            "contacted_at": datetime.now(UTC).isoformat(),
        }
        case["contact_log"] = contact_log
        status = "sent" if sent else "failed"
        self._log(f"[Contact] WeChat message {status} to {client_name}")
        self.add_note(case["case_id"], f"Initial contact {status} to {client_name} via {case['source_channel']}.")
        return {"status": status, "contact_log": contact_log}

    async def _process_case_evaluation(self, case, orchestration=None):
        self._log(f"[Evaluation] Querying RAG for {case['matter_type']} dispute analysis")
        orchestration = orchestration or await self.orchestrate_case(case["case_id"], objective="evaluation")
        matter_type = case["matter_type"]
        query = ", ".join(orchestration["plan"].get("rag_queries", []))
        context = orchestration["aggregated_context_preview"]
        evaluation_text = orchestration["synthesis"]
        self._log(f"[Evaluation] LLM response received ({len(evaluation_text)} chars)")
        evaluation_detail = {
            "matter_type": matter_type,
            "client_name": case["client_name"],
            "rag_query": query,
            "rag_context_chunks": context[:300] if context else "",
            "evaluation_text": evaluation_text,
            "viability": "pending_review",
            "evaluated_at": datetime.now(UTC).isoformat(),
        }
        case["evaluation"] = evaluation_text
        case["evaluation_detail"] = evaluation_detail
        self.add_note(case["case_id"], "Case evaluation generated with RAG context for operator review.")
        return {"status": "evaluated", "evaluation_detail": evaluation_detail}

    async def _process_fee_collection(self, case):
        self._log(f"[Fee] Generating fee schedule for {case['client_name']}")
        fee_schedule = {
            "Commercial Lease Dispute": {"base_fee": 10000, "currency": "CNY", "contingency_pct": 10},
            "Civil Litigation": {"base_fee": 8000, "currency": "CNY", "contingency_pct": 12},
            "Contract Review": {"base_fee": 5000, "currency": "CNY", "contingency_pct": 0},
        }
        selected_fee = fee_schedule.get(case["matter_type"], fee_schedule["Civil Litigation"])
        fee_message = (
            f"{case['client_name']}您好，关于您的{case['matter_type']}案件，"
            f"律师费标准如下：基础费用{selected_fee['base_fee']}{selected_fee['currency']}"
            + (f"，风险代理比例{selected_fee['contingency_pct']}%。" if selected_fee["contingency_pct"] else "。")
        )
        tool_result = await self.tool_registry.execute("send_wechat", contact_name=case["client_name"], text=fee_message)
        fee_record = {
            "client_name": case["client_name"],
            "fee_schedule": selected_fee,
            "fee_message": fee_message,
            "tool_dispatch": tool_result,
            "confirmed": False,
            "sent_at": datetime.now(UTC).isoformat(),
        }
        case["fee_record"] = fee_record
        if not case.get("is_paid"):
            case["is_paid"] = False
        self.add_note(case["case_id"], f"Fee standard ({selected_fee['base_fee']} {selected_fee['currency']}) sent to {case['client_name']}.")
        return {"status": "fee_sent", "fee_record": fee_record}

    async def _process_group_creation(self, case):
        self._log(f"[Group] Creating collaboration group for case {case['case_id']}")
        group_name = f"案件协作-{case['case_id']}"
        members = [case["client_name"], "承办律师", "案件助理"]
        created, detail, group_data = await self.wechat.create_group_chat(group_name, members)
        douyin_result = await self.tool_registry.execute("publish_douyin", media_path="case_start.mp4", caption=f"New case {case['case_id']} started.")
        group_info = {
            "group_name": group_name,
            "requested_members": members,
            "created": created,
            "creation_detail": detail,
            "group_data": group_data,
            "douyin_announced": douyin_result.get("ok", False),
            "created_at": datetime.now(UTC).isoformat(),
        }
        case["group_info"] = group_info
        self.add_note(case["case_id"], f"Collaboration group '{group_name}' {'created' if created else 'failed'}.")
        return {"status": "group_created" if created else "group_failed", "group_info": group_info}

    async def _process_material_ingestion(self, case):
        self._log(f"[Ingestion] Processing materials for case {case['case_id']}")
        uploaded = case.get("uploaded_documents", [])
        rag_summary = self.rag.get_summary()
        ingestion_summary = {
            "documents_uploaded": list(uploaded),
            "document_count": len(uploaded),
            "rag_document_count": rag_summary.get("document_count", 0),
            "rag_documents": rag_summary.get("documents", []),
            "rag_backend": rag_summary.get("backend_mode", "unknown"),
            "total_chunks": rag_summary.get("total_chunks", 0),
            "initial_msg_captured": bool(case.get("initial_msg")),
            "notes_count": len(case.get("notes", [])),
            "ingested_at": datetime.now(UTC).isoformat(),
        }
        case["ingestion_summary"] = ingestion_summary
        self.add_note(
            case["case_id"],
            f"Material ingestion complete: {len(uploaded)} uploaded, {rag_summary.get('total_chunks', 0)} chunks in index.",
        )
        return {"status": "ingested", "ingestion_summary": ingestion_summary}

    async def _process_document_generation(self, case, orchestration=None):
        self._log(f"[Draft] Generating legal document for {case['client_name']} ({case['matter_type']})")
        matter_type = case["matter_type"]
        uploaded_docs = case.get("uploaded_documents", [])
        orchestration = orchestration or await self.orchestrate_case(case["case_id"], objective="draft")
        query = ", ".join(orchestration["plan"].get("rag_queries", []))
        context = orchestration["aggregated_context_preview"]
        draft_text = orchestration["synthesis"]
        self._log(f"[Draft] Document generated: {len(draft_text)} chars")
        document_draft = {
            "draft_text": draft_text,
            "draft_preview": draft_text[:500],
            "matter_type": matter_type,
            "client_name": case["client_name"],
            "source_documents": list(uploaded_docs),
            "rag_query": query,
            "rag_context_chunks": context[:300] if context else "",
            "generated_at": datetime.now(UTC).isoformat(),
        }
        case["draft_preview"] = draft_text[:500]
        case["document_draft"] = document_draft

        # Extract highlights from source documents
        highlights = []
        source_text = context[:3000] if context else ""
        if source_text:
            self._log(f"[Highlights] Extracting key passages from source material...")
            highlights = await asyncio.to_thread(
                self.llm.extract_highlights, source_text, draft_text[:500]
            )
            if highlights:
                for hl in highlights:
                    hl["source_file"] = uploaded_docs[0] if uploaded_docs else "RAG context"
                    # Map highlight text to source PDF page
                    if uploaded_docs:
                        quoted = hl.get("text", "")[:80]
                        if quoted:
                            pages = self.rag.find_text_in_pages(uploaded_docs[0], quoted)
                            hl["source_page"] = pages[0] if pages else None
                        else:
                            hl["source_page"] = None
                    else:
                        hl["source_page"] = None
                case["highlights"] = highlights
                self._log(f"[Highlights] Found {len(highlights)} important passages")
            else:
                self._log(f"[Highlights] No highlights extracted (model returned empty)")

        # Auto-generate PDF report
        sections = []
        eval_detail = case.get("evaluation_detail")
        if eval_detail and eval_detail.get("evaluation_text"):
            sections.append({"heading": "Case Evaluation", "body": eval_detail["evaluation_text"]})
        sections.append({"heading": "Legal Document Draft", "body": draft_text})

        pdf_result = await self.tool_registry.execute(
            "generate_pdf",
            title=f"{matter_type} Report",
            case_id=case["case_id"],
            client_name=case["client_name"],
            sections=sections,
            highlights=highlights,
        )
        if pdf_result.success:
            document_draft["pdf_report"] = pdf_result.output
            self._log(f"[Draft] PDF report generated: {pdf_result.output.get('filename')} ({pdf_result.output.get('pages')} pages)")
        else:
            self._log(f"[Draft] PDF generation failed: {pdf_result.error}")

        self.add_note(case["case_id"], f"Draft document generated ({len(draft_text)} chars) with {len(highlights)} highlights, awaiting review.")
        return {"status": "draft_generated", "document_draft": document_draft, "highlights": highlights}

    async def _process_client_approval(self, case):
        self._log(f"[Approval] Submitting draft for client approval via {case['source_channel']}")
        draft = case.get("document_draft") or {}
        draft_text = draft.get("draft_text", case.get("draft_preview", ""))
        approval_record = {
            "client_name": case["client_name"],
            "draft_preview": draft_text[:300],
            "approval_status": "pending_client_review",
            "approval_requested_at": datetime.now(UTC).isoformat(),
            "delivery_method": case["source_channel"],
        }
        if case.get("group_info", {}).get("created"):
            approval_record["delivery_group"] = case["group_info"]["group_name"]
        case["approval_record"] = approval_record
        self.add_note(case["case_id"], f"Draft ({len(draft_text)} chars) prepared for client approval via {case['source_channel']}.")
        return {"status": "approval_requested", "approval_record": approval_record}

    async def _process_final_pdf_send(self, case):
        self._log(f"[Delivery] Sending final document to {case['client_name']}")
        draft = case.get("document_draft") or {}
        approval = case.get("approval_record") or {}
        client_name = case["client_name"]
        delivery_record = {
            "case_id": case["case_id"],
            "client_name": client_name,
            "document_title": f"{case['matter_type']}法律文书",
            "delivery_channel": case["source_channel"],
            "draft_preview": draft.get("draft_preview", case.get("draft_preview", "")),
            "approval_status": approval.get("approval_status", "unknown"),
            "fee_status": "paid" if case.get("is_paid") else "unpaid",
            "fee_detail": case.get("fee_record", {}),
            "sent_at": datetime.now(UTC).isoformat(),
        }
        sent, detail = await self.wechat.send_message(
            client_name,
            f"[Final] 您的案件文书已准备完毕，请查收。案件编号：{case['case_id']}",
        )
        delivery_record["send_success"] = sent
        delivery_record["send_detail"] = detail
        self._log(f"[Delivery] Final document {'sent' if sent else 'failed'} to {client_name}")
        case["delivery_record"] = delivery_record
        self.add_note(case["case_id"], f"Final delivery {'sent' if sent else 'failed'} to {client_name}.")
        return {"status": "delivered" if sent else "delivery_failed", "delivery_record": delivery_record}

    async def _process_archive_close(self, case):
        self._log(f"[Archive] Case {case['case_id']} completed and archived")
        self.add_note(case["case_id"], "Case completed and archived.")
        return {"status": "archived", "case_id": case["case_id"]}

    async def execute_case_graph(self, case_id=None):
        target_case_id = case_id or self.active_case_id
        if target_case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {target_case_id}")
        return await self.v2_engine.execute_current_state(target_case_id)

    async def resume_case_graph(self, case_id=None):
        target_case_id = case_id or self.active_case_id
        if target_case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {target_case_id}")
        return await self.v2_engine.resume_case(target_case_id)

    async def run_graph_until_pause(self, case_id=None):
        target_case_id = case_id or self.active_case_id
        if target_case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {target_case_id}")
        return await self.v2_engine.run_until_pause(target_case_id)

    def get_dashboard_summary(self) -> dict:
        total = len(self.cases)
        by_state: dict[str, int] = {}
        stuck_cases: list[dict] = []
        hitl_pending: list[dict] = []

        for case_id, case in self.cases.items():
            state = case.get("state", AgentState.CLIENT_CAPTURE)
            state_name = state.name if isinstance(state, AgentState) else str(state)
            by_state[state_name] = by_state.get(state_name, 0) + 1

            if state_name in HITL_STATES and not case.get("hitl_approvals", {}).get(state_name, False):
                hitl_pending.append({
                    "case_id": case_id,
                    "client_name": case.get("client_name", "Unknown"),
                    "state": state_name,
                    "waiting_since": case.get("updated_at", ""),
                })
                stuck_cases.append({
                    "case_id": case_id,
                    "client_name": case.get("client_name", "Unknown"),
                    "state": state_name,
                    "matter_type": case.get("matter_type", ""),
                    "updated_at": case.get("updated_at", ""),
                })

        return {
            "total_cases": total,
            "by_state": by_state,
            "hitl_pending_count": len(hitl_pending),
            "hitl_pending": hitl_pending,
            "stuck_cases": stuck_cases,
            "active_case_id": self.active_case_id,
        }

    def billing_start(self, case_id: str | None, description: str = "") -> dict:
        case = self.cases[case_id or self.active_case_id]
        if case.get("billing_active"):
            return {"error": "Billing already active", "active_entry": case.get("billing_start_time")}
        case["billing_active"] = True
        case["billing_start_time"] = time.time()
        case["billing_description"] = description
        self.tracker.record_user_action("billing_start", f"{case['case_id']}: {description}")
        return {"status": "started", "case_id": case["case_id"], "description": description}

    def billing_stop(self, case_id: str | None) -> dict:
        case = self.cases[case_id or self.active_case_id]
        if not case.get("billing_active"):
            return {"error": "No active billing session"}
        start = case.get("billing_start_time", time.time())
        elapsed = time.time() - start
        entry = {
            "description": case.get("billing_description", ""),
            "duration_seconds": round(elapsed, 1),
            "case_id": case["case_id"],
            "stopped_at": datetime.now(UTC).isoformat(),
        }
        case.setdefault("billing_entries", []).append(entry)
        case["billing_active"] = False
        case["billing_start_time"] = None
        case["billing_description"] = ""
        self.tracker.record_user_action("billing_stop", f"{case['case_id']}: {elapsed:.1f}s")
        return {"status": "stopped", "entry": entry, "total_entries": len(case.get("billing_entries", []))}

    def billing_get(self, case_id: str | None) -> dict:
        case = self.cases[case_id or self.active_case_id]
        active = case.get("billing_active", False)
        current_duration = 0.0
        if active and case.get("billing_start_time"):
            current_duration = time.time() - case["billing_start_time"]
        return {
            "active": active,
            "current_duration_seconds": round(current_duration, 1),
            "description": case.get("billing_description", ""),
            "entries": case.get("billing_entries", []),
            "total_billed_seconds": sum(e["duration_seconds"] for e in case.get("billing_entries", [])),
        }

    def add_deadline(self, case_id: str | None, deadline_date: str, description: str, priority: str = "medium") -> dict:
        case = self.cases[case_id or self.active_case_id]
        deadline_id = hashlib.md5(f"{case['case_id']}:{deadline_date}:{description}".encode()).hexdigest()[:8]
        entry = {
            "id": deadline_id,
            "date": deadline_date,
            "description": description,
            "priority": priority,
            "added_at": datetime.now(UTC).isoformat(),
            "completed": False,
        }
        case.setdefault("deadlines", []).append(entry)
        self.add_note(case["case_id"], f"Deadline added: {description} on {deadline_date}.")
        self.tracker.record_user_action("deadline_add", f"{case['case_id']}: {description}")
        return entry

    def complete_deadline(self, case_id: str | None, deadline_id: str) -> dict:
        case = self.cases[case_id or self.active_case_id]
        for dl in case.get("deadlines", []):
            if dl.get("id") == deadline_id:
                dl["completed"] = True
                self.add_note(case["case_id"], f"Deadline completed: {dl['description']}.")
                return {"status": "completed", "deadline": dl}
        return {"error": "Deadline not found"}

    def get_deadlines(self, case_id: str | None) -> dict:
        case = self.cases[case_id or self.active_case_id]
        deadlines = case.get("deadlines", [])
        now = datetime.now(UTC)
        upcoming = []
        overdue = []
        completed = []
        for dl in deadlines:
            try:
                dl_date = datetime.fromisoformat(dl["date"].replace("Z", "+00:00"))
                if dl.get("completed"):
                    completed.append(dl)
                elif dl_date < now:
                    overdue.append(dl)
                else:
                    upcoming.append(dl)
            except (ValueError, TypeError):
                upcoming.append(dl)
        return {"upcoming": upcoming, "overdue": overdue, "completed": completed}

    def get_graph_status(self, case_id=None):
        target_case_id = case_id or self.active_case_id
        if target_case_id not in self.cases:
            raise KeyError(f"Unknown case_id: {target_case_id}")
        return self.v2_engine.get_graph_status(target_case_id)


if __name__ == "__main__":
    machine = LegalAgentMachine()
    asyncio.run(machine.run())
