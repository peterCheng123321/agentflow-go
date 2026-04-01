import asyncio
import mimetypes
import os
import re
import time
import uuid
from typing import List

import orjson
from fastapi import FastAPI, File, Form, HTTPException, Query, UploadFile, WebSocket, WebSocketDisconnect
from fastapi.middleware.cors import CORSMiddleware
from fastapi.middleware.gzip import GZipMiddleware
from fastapi.responses import FileResponse, ORJSONResponse, StreamingResponse

from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from agent_flow import LegalAgentMachine
from auto_device import auto_setup, detect_device, get_device_report, recommend_config
from setup_manager import get_setup_status, run_local_setup
from template_generator import TemplateGenerator, DOCUMENT_TYPES
from tool_registry_v1 import ToolMode

try:
    import python_multipart  # noqa: F401
    MULTIPART_AVAILABLE = True
except Exception:
    try:
        import multipart  # noqa: F401
        MULTIPART_AVAILABLE = True
    except Exception:
        MULTIPART_AVAILABLE = False

app = FastAPI(default_response_class=ORJSONResponse)
app.add_middleware(GZipMiddleware, minimum_size=1000)
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

app.mount("/static", StaticFiles(directory="frontend"), name="static")


@app.get("/")
async def serve_frontend():
    return FileResponse("frontend/index.html")


@app.middleware("http")
async def add_process_time_header(request, call_next):
    start = time.perf_counter()
    response = await call_next(request)
    response.headers["X-Process-Time"] = f"{time.perf_counter() - start:.4f}"
    return response


agent_machine = LegalAgentMachine()
template_generator = TemplateGenerator(llm=agent_machine.llm, rag=agent_machine.rag)

ALLOWED_EVIDENCE_CATEGORIES = (
    "General Evidence",
    "Evidence",
    "Contract",
    "Correspondence",
    "Internal Note",
    "Court Document",
    "Financial Record",
    "Identity Document",
    "Medical Record",
    "Labor Document",
    "Other",
)
_CATEGORY_BY_LOWER = {c.lower(): c for c in ALLOWED_EVIDENCE_CATEGORIES}

_CHINESE_CATEGORY_HINTS = [
    ("证据目录", "Evidence"),
    ("证据清单", "Evidence"),
    ("劳务合同", "Labor Document"),
    ("劳动合同", "Labor Document"),
    ("律师函", "Correspondence"),
    ("起诉状", "Court Document"),
    ("判决书", "Court Document"),
    ("裁定书", "Court Document"),
    ("身份证", "Identity Document"),
    ("护照", "Identity Document"),
    ("病历", "Medical Record"),
    ("诊断", "Medical Record"),
    ("发票", "Financial Record"),
    ("收据", "Financial Record"),
    ("合同", "Contract"),
    ("协议", "Contract"),
    ("函", "Correspondence"),
    ("证据", "Evidence"),
    ("账单", "Financial Record"),
    ("备注", "Internal Note"),
    ("笔记", "Internal Note"),
]


def _parse_float_form(value: str | None, default: float) -> float:
    try:
        return float(str(value).strip()) if value is not None else default
    except (TypeError, ValueError):
        return default


def _form_bool(value: str | None, default: bool = False) -> bool:
    if value is None or str(value).strip() == "":
        return default
    return str(value).strip().lower() in ("1", "true", "yes", "on")


def _resolve_upload_target_case(
    case_id: str | None,
    client_name: str,
    matter_type: str,
    filename: str = "",
) -> tuple[str, dict]:
    cn = (client_name or "").strip()
    
    # Auto-detect client name from filename if not provided
    detected_from_filename = False
    if not cn and filename:
        cn = _extract_client_from_filename(filename) or ""
        detected_from_filename = bool(cn)
    
    # Auto-detect matter type from filename if not provided
    mt = (matter_type or "").strip()
    if not mt and filename:
        mt = _extract_matter_type_from_filenames([filename]) or "Civil Litigation"
    
    if cn:
        # Check if this name appears as a defendant or party in any existing case
        existing_case_id = _find_case_by_entity_name(cn)
        if existing_case_id:
            return existing_case_id, {
                "case_created": False,
                "case_matched_existing": True,
                "auto_detected_client": detected_from_filename,
                "auto_detected_matter": not matter_type.strip(),
                "matched_as_entity": True,
            }
        
        r = agent_machine.find_or_create_case_for_client(
            cn,
            matter_type=mt or "Commercial Lease Dispute",
        )
        return r["case_id"], {
            "case_created": r["created"],
            "case_matched_existing": not r["created"],
            "auto_detected_client": detected_from_filename,
            "auto_detected_matter": not matter_type.strip(),
        }
    
    # No client detected — only then fall back to selected/active case if available.
    # This preserves the intended feature: uploading a case file should auto-create/match
    # a case from filename when possible, rather than silently attaching to an unrelated active case.
    explicit = (case_id or "").strip()
    if explicit:
        if explicit not in agent_machine.cases:
            raise HTTPException(status_code=404, detail=f"Unknown case_id: {explicit}")
        return explicit, {"case_created": False, "case_matched_existing": True, "explicit_case_id": True}

    if agent_machine.active_case_id and agent_machine.active_case_id in agent_machine.cases:
        return agent_machine.active_case_id, {
            "case_created": False,
            "case_matched_existing": True,
            "auto_detected_client": False,
            "auto_detected_matter": False,
            "fallback_to_active": True,
        }
    
    raise HTTPException(
        status_code=400,
        detail="Provide client_name (or a filename that includes a recognizable client name) or select a valid case_id.",
    )


def _find_case_by_entity_name(entity_name: str) -> str | None:
    """Check if a name appears as a defendant or party in any existing case."""
    if not hasattr(agent_machine, 'cases') or not agent_machine.cases:
        return None
    
    for case_id, case in agent_machine.cases.items():
        if case_id not in agent_machine.cases:
            continue
        if case.get("client_name") == entity_name:
            return case_id
        
        for note in case.get("notes", []) or []:
            note_text = note.get("text", "") if isinstance(note, dict) else str(note)
            if entity_name in note_text:
                return case_id
        
        for doc in case.get("uploaded_documents", []) or []:
            if entity_name in doc:
                return case_id
        
        eval_detail = case.get("evaluation_detail") or {}
        if entity_name in str(eval_detail.get("evaluation_text", "")):
            return case_id
        
        draft = case.get("document_draft") or {}
        if entity_name in str(draft.get("draft_text", "")):
            return case_id
    
    return None


def _find_case_by_ocr_content(ocr_text: str) -> str | None:
    """Scan OCR text for known case entities and route to matching case.
    
    This is the key fix: when uploading 廖国泽欠款条.jpg or 被告信息.jpg,
    the OCR text contains names that can be matched to existing cases.
    """
    if not ocr_text or len(ocr_text) < 10:
        return None
    
    # Priority 1: Check if any existing case's client name appears in OCR text
    for case_id, case in agent_machine.cases.items():
        client_name = case.get("client_name", "")
        if client_name and client_name in ocr_text:
            return case_id
    
    # Priority 2: Check if any uploaded document filename appears in OCR text
    for case_id, case in agent_machine.cases.items():
        for doc in case.get("uploaded_documents", []) or []:
            if doc in ocr_text:
                return case_id
    
    # Priority 3: Extract entities from OCR text and check if they match known cases
    entity_patterns = [
        r'(?:原告|被告|当事人|欠款人|债务人|债权人|甲方|乙方)[:：\s]*([\u4e00-\u9fff]{2,6})',
        r'([\u4e00-\u9fff]{2,6})(?:系原告|系被告|系当事人)',
    ]
    
    for pattern in entity_patterns:
        matches = re.findall(pattern, ocr_text)
        for match in matches:
            case_id = _find_case_by_entity_name(match)
            if case_id:
                return case_id
    
    return None


_NAME_PATTERNS = [
    re.compile(r'([^\s（(]{2,6})（'),
    re.compile(r'([^\s（(]{2,6})\('),
    re.compile(r'^([^\s（(]{2,6})[的之与和及]'),
    re.compile(r'^[（(]?([^\s（()]{2,6})[）)]'),
    re.compile(r'([^\s（()]{2,6})[的之与和及]'),
]

_NAME_PREFIX_DOCS = [
    '律师函', '民事起诉状', '起诉状', '起诉书', '证据目录', '证据清单',
    '欠款条', '欠条', '送达地址', '授权委托书', '退费账户', '承诺书',
    '材料清单', '文书', '判决书', '裁定书', '合同', '协议', '报告',
    '清单', '目录', '诉状', '被告信息', '身份信息', '身份证',
]

_NAME_BLACKLIST = {'证据', '被告', '原告', '当事人', '申请人', '被申请人', '证人', '鉴定', '附件', '补充', '新增', '其他', '未知', '新建', '文本文档'}

_MATTER_HINTS = {
    '买卖': 'Sales Contract Dispute',
    '合同': 'Contract Dispute',
    '欠款': 'Debt Dispute',
    '借贷': 'Loan Dispute',
    '租赁': 'Lease Dispute',
    '劳务': 'Labor Dispute',
    '劳动': 'Labor Dispute',
    '侵权': 'Tort Dispute',
    '起诉': 'Civil Litigation',
    '诉讼': 'Civil Litigation',
}


def _extract_client_from_filename(filename: str) -> str | None:
    name = filename.rsplit('.', 1)[0]
    for pat in _NAME_PATTERNS:
        m = pat.search(name)
        if m and len(m.group(1).strip()) >= 2:
            return m.group(1).strip()
    for doc_type in _NAME_PREFIX_DOCS:
        idx = name.find(doc_type)
        if idx > 0:
            candidate = name[:idx].strip()
            # Remove trailing 新/旧/等 modifiers
            candidate = re.sub(r'[新旧补附]', '', candidate).strip()
            if candidate in _NAME_BLACKLIST:
                continue
            if 2 <= len(candidate) <= 6 and all('\u4e00' <= c <= '\u9fff' for c in candidate):
                return candidate
    return None


def _extract_matter_type_from_filenames(filenames: list[str]) -> str:
    for fn in filenames:
        for keyword, matter in _MATTER_HINTS.items():
            if keyword in fn:
                return matter
    return "Civil Litigation"


def _normalize_evidence_category(value: str | None) -> str:
    if not value or not str(value).strip():
        return "General Evidence"
    v = str(value).strip()
    if v in ALLOWED_EVIDENCE_CATEGORIES:
        return v
    return _CATEGORY_BY_LOWER.get(v.lower(), "General Evidence")


def _auto_categorize_by_filename(filename: str) -> str | None:
    for hint, category in _CHINESE_CATEGORY_HINTS:
        if hint in filename:
            return category
    return None


class HITLApproval(BaseModel):
    case_id: str
    state: str
    approved: bool
    reason: str | None = None


class CaseCreateRequest(BaseModel):
    client_name: str
    matter_type: str = "Commercial Lease Dispute"
    source_channel: str = "CRM"
    initial_msg: str = ""


class CaseUpdateRequest(BaseModel):
    client_name: str | None = None
    matter_type: str | None = None
    source_channel: str | None = None
    initial_msg: str | None = None
    priority: str | None = None
    is_paid: bool | None = None


class CaseSelectRequest(BaseModel):
    case_id: str


class NoteCreateRequest(BaseModel):
    text: str


class NoteEditRequest(BaseModel):
    text: str


class StateChangeRequest(BaseModel):
    target_state: str


class DraftEditRequest(BaseModel):
    draft_text: str


class HITLRejectRequest(BaseModel):
    case_id: str
    state: str
    reason: str


class WeChatInboundRequest(BaseModel):
    contact_name: str
    text: str
    metadata: dict | None = None


class WeChatSendRequest(BaseModel):
    contact_name: str
    text: str


class WeChatGroupRequest(BaseModel):
    group_name: str
    members: list[str]
    case_id: str | None = None


class RagSignalRequest(BaseModel):
    case_id: str | None = None
    note: str


class RagSearchRequest(BaseModel):
    query: str
    k: int = 5
    ocr_fallback: bool = False


class DocumentGrepRequest(BaseModel):
    pattern: str
    max_results: int = 20
    case_sensitive: bool = False


class AIOrchestrationRequest(BaseModel):
    objective: str = "evaluation"


class ConnectionManager:
    def __init__(self):
        self.active_connections: List[WebSocket] = []
        self._last_payload_hash = None
        self._last_log_length = 0

    async def connect(self, websocket: WebSocket):
        await websocket.accept()
        self.active_connections.append(websocket)
        payload = status_payload()
        await websocket.send_bytes(orjson.dumps(payload))

    def disconnect(self, websocket: WebSocket):
        if websocket in self.active_connections:
            self.active_connections.remove(websocket)

    async def broadcast_on_change(self):
        evt = agent_machine.change_event
        while True:
            await evt.wait()
            evt.clear()
            payload = status_payload()
            raw = orjson.dumps(payload)
            payload_hash = hash(raw)
            log_length = len(payload.get("agent_log", []))
            if payload_hash == self._last_payload_hash and log_length == self._last_log_length:
                continue
            self._last_payload_hash = payload_hash
            self._last_log_length = log_length
            stale = []
            for ws in self.active_connections:
                try:
                    await ws.send_bytes(raw)
                except Exception:
                    stale.append(ws)
            for ws in stale:
                self.active_connections.remove(ws)


manager = ConnectionManager()


@app.on_event("startup")
async def startup_event():
    asyncio.create_task(manager.broadcast_on_change())


def status_payload():
    case_status = agent_machine.get_case_status()
    rag_summary = agent_machine.rag.get_summary()
    return {
        "version": "v2",
        "engine": "v2_langgraph",
        "agent_state": case_status["state"],
        "recommended_model": agent_machine.model,
        "llm_backend": agent_machine.device_config.get("llm_backend", "unknown"),
        "wechat": {
            "status": agent_machine.wechat.status.name,
            "mode": agent_machine.wechat.adapter_mode,
            "capabilities": agent_machine.wechat.capabilities,
        },
        "rag_ready": rag_summary["ready"],
        "rag_error": rag_summary["error"],
        "rag_backend_mode": rag_summary["backend_mode"],
        "rag_total_chunks": rag_summary.get("total_chunks", 0),
        "case": case_status,
        "documents": rag_summary["documents"],
        "active_cases": agent_machine.list_cases(),
        "setup": get_setup_status(),
        "tracking": agent_machine.tracker.get_status(),
        "tools": agent_machine.tool_registry.list_tools(),
        "device": agent_machine.device_info.get("platform_id", "unknown"),
        "graph_runtime": agent_machine.v2_engine.metadata,
        "agent_log": agent_machine.get_agent_log(),
    }


@app.get("/status")
async def get_status():
    return status_payload()


@app.get("/cases")
async def list_cases():
    return {"active_case_id": agent_machine.active_case_id, "cases": agent_machine.list_cases()}


@app.post("/cases")
async def create_case(payload: CaseCreateRequest):
    case = agent_machine.create_case(
        client_name=payload.client_name,
        matter_type=payload.matter_type,
        source_channel=payload.source_channel,
        initial_msg=payload.initial_msg,
    )
    agent_machine.select_case(case["case_id"])
    return {"status": "created", "case": agent_machine.get_case_status(case["case_id"])}


@app.get("/cases/{case_id}")
async def get_case(case_id: str):
    try:
        return {"case": agent_machine.get_case_detail(case_id)}
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.put("/cases/{case_id}")
async def update_case(case_id: str, payload: CaseUpdateRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    fields = {k: v for k, v in payload.model_dump().items() if v is not None}
    if not fields:
        raise HTTPException(status_code=400, detail="No fields to update")
    case = agent_machine.update_case(case_id, **fields)
    return {"status": "updated", "case": case}


@app.delete("/cases/{case_id}")
async def delete_case(case_id: str):
    try:
        result = agent_machine.delete_case(case_id)
        return result
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.post("/cases/select")
async def select_case(payload: CaseSelectRequest):
    try:
        case = agent_machine.select_case(payload.case_id)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    return {"status": "selected", "case": case}


@app.post("/cases/{case_id}/notes")
async def add_note(case_id: str, payload: NoteCreateRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    agent_machine.add_note(case_id, payload.text)
    return {"status": "added", "note": payload.text}


@app.put("/cases/{case_id}/notes/{note_index}")
async def edit_note(case_id: str, note_index: int, payload: NoteEditRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    try:
        result = agent_machine.edit_note(case_id, note_index, payload.text)
        return result
    except IndexError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.delete("/cases/{case_id}/notes/{note_index}")
async def delete_note(case_id: str, note_index: int):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    try:
        result = agent_machine.delete_note(case_id, note_index)
        return result
    except IndexError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.post("/cases/{case_id}/advance")
async def advance_state(case_id: str, payload: StateChangeRequest | None = None):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    try:
        target = payload.target_state if payload else None
        case = agent_machine.advance_state(case_id, target)
        return {"status": "advanced", "case": case}
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.post("/cases/{case_id}/rewind")
async def rewind_state(case_id: str, payload: StateChangeRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    try:
        case = agent_machine.rewind_state(case_id, payload.target_state)
        return {"status": "rewound", "case": case}
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.post("/approve")
async def approve_step(approval: HITLApproval):
    try:
        agent_machine.set_approval(approval.state, approval.approved, approval.case_id, approval.reason)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    return {
        "status": "Updated",
        "state": approval.state,
        "approved": approval.approved,
        "graph": agent_machine.get_graph_status(approval.case_id),
    }


@app.post("/cases/{case_id}/reject")
async def reject_with_reason(case_id: str, payload: HITLRejectRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    agent_machine.set_approval(payload.state, False, case_id, payload.reason)
    return {"status": "rejected", "state": payload.state, "reason": payload.reason}


@app.put("/cases/{case_id}/draft")
async def edit_draft(case_id: str, payload: DraftEditRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.edit_draft(case_id, payload.draft_text)
    return {"status": "updated", "case": case}


@app.get("/cases/{case_id}/state-output/{state_name}")
async def get_state_output(case_id: str, state_name: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    output = case.get("state_outputs", {}).get(state_name)
    if output is None:
        raise HTTPException(status_code=404, detail=f"No output for state {state_name}")
    return {"state": state_name, "output": output}


@app.post("/cases/{case_id}/execute")
async def execute_case_node(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    result = await agent_machine.execute_case_graph(case_id)
    return {
        "status": result["status"],
        "case_id": case_id,
        "graph": agent_machine.get_graph_status(case_id),
        "case": agent_machine.get_case_status(case_id),
    }


@app.post("/cases/{case_id}/resume")
async def resume_case_graph(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    graph = await agent_machine.resume_case_graph(case_id)
    return {"status": "resumed", "case_id": case_id, "graph": graph, "case": agent_machine.get_case_status(case_id)}


@app.get("/cases/{case_id}/graph")
async def case_graph_status(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    return {"case_id": case_id, "graph": agent_machine.get_graph_status(case_id)}


@app.post("/setup")
async def setup_local_environment():
    return run_local_setup()


@app.get("/wechat/status")
async def wechat_status():
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        return {
            "status": "paused",
            "mode": "paused",
            "capabilities": {"paused": True, "supports_outbound_send": False, "supports_contact_read": False, "supports_group_create": False},
        }
    return {
        "status": agent_machine.wechat.status.name,
        "mode": agent_machine.wechat.adapter_mode,
        "capabilities": agent_machine.wechat.capabilities,
    }


@app.post("/wechat/connect")
async def wechat_connect():
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        raise HTTPException(status_code=503, detail="WeChat is paused (development focus: file processing)")
    success = await agent_machine.wechat.login()
    return {
        "connected": success,
        "status": agent_machine.wechat.status.name,
        "mode": agent_machine.wechat.adapter_mode,
    }


@app.post("/wechat/inbound")
async def wechat_inbound(payload: WeChatInboundRequest):
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        raise HTTPException(status_code=503, detail="WeChat is paused (development focus: file processing)")
    processed = await agent_machine.wechat.receive_message(
        payload.contact_name,
        payload.text,
        metadata=payload.metadata,
    )
    if not processed:
        case = await agent_machine.handle_incoming_message(
            contact_name=payload.contact_name,
            text=payload.text,
            source_channel="WeChat",
            metadata=payload.metadata,
        )
    else:
        case = agent_machine.get_case_status()
    return {"status": "received", "processed": processed, "case": case}


@app.get("/wechat/contacts")
async def wechat_contacts():
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        raise HTTPException(status_code=503, detail="WeChat is paused (development focus: file processing)")
    contacts = await agent_machine.wechat.list_contacts()
    return {"contacts": contacts, "mode": agent_machine.wechat.adapter_mode}


@app.post("/wechat/send")
async def wechat_send(payload: WeChatSendRequest):
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        raise HTTPException(status_code=503, detail="WeChat is paused (development focus: file processing)")
    success, message = await agent_machine.wechat.send_message(payload.contact_name, payload.text)
    return {"sent": success, "message": message, "mode": agent_machine.wechat.adapter_mode}


@app.post("/wechat/group")
async def wechat_group(payload: WeChatGroupRequest):
    if os.getenv("AGENTFLOW_ENABLE_WECHAT", "0") != "1":
        raise HTTPException(status_code=503, detail="WeChat is paused (development focus: file processing)")
    success, message, group = await agent_machine.wechat.create_group_chat(payload.group_name, payload.members)
    if success:
        target_case_id = payload.case_id or agent_machine.active_case_id
        if target_case_id in agent_machine.cases:
            agent_machine.add_note(target_case_id, f"WeChat group created: {group.get('group_name')} ({group.get('group_id')}).")
    return {"created": success, "message": message, "group": group}


@app.post("/rag/signal")
async def rag_signal(payload: RagSignalRequest):
    target_case_id = payload.case_id or agent_machine.active_case_id
    if target_case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {target_case_id}")
    agent_machine.signal_rag_ingestion(payload.note, target_case_id)
    return {"status": "recorded", "case_id": target_case_id, "note": payload.note}


async def _rag_search_ocr_live(rag, query: str, k: int) -> list[dict]:
    """When indexed text misses, run GLM-OCR on image/PDF files and match query in fresh text."""
    from ocr_engine import ocr_engine
    from rag_manager import _is_unindexable_placeholder_text, _segment

    q = (query or "").strip()
    if len(q) < 2:
        return []
    tokens = [t for t in _segment(q) if len(t.strip()) >= 2]
    out: list[dict] = []
    exts = (".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tif", ".tiff", ".gif", ".heic", ".pdf")
    for doc in rag.documents[:35]:
        path = doc.get("path")
        fn = doc.get("filename")
        if not path or not os.path.isfile(path):
            continue
        if not str(path).lower().endswith(exts):
            continue
        try:
            text = await ocr_engine.scan_file(path, task="Text Recognition")
        except Exception:
            continue
        if not text or not str(text).strip() or _is_unindexable_placeholder_text(str(text)):
            continue
        text = str(text)
        hit = q in text
        if not hit and tokens:
            hit = all(t in text for t in tokens[:10])
        if hit:
            out.append(
                {
                    "filename": fn,
                    "chunk": text[:2000],
                    "score": 0.55,
                    "match_mode": "ocr_live",
                }
            )
        if len(out) >= k:
            break
    return out[:k]


@app.post("/rag/search")
async def rag_search(payload: RagSearchRequest):
    results = agent_machine.rag.search_structured(payload.query, k=payload.k)
    ocr_used = False
    # OCR live fallback removed for memory safety - indexed text is sufficient
    # with improved BM25 scoring and exact phrase boosting
    return {
        "query": payload.query,
        "results": results,
        "count": len(results),
        "ocr_fallback_used": ocr_used,
    }


@app.post("/rag/purge-placeholder-docs")
async def rag_purge_placeholder_docs():
    removed_info = agent_machine.rag.purge_placeholder_documents()
    agent_machine.sync_uploaded_docs_with_rag()
    agent_machine.notify_change()
    return removed_info


@app.get("/documents")
async def list_documents():
    return {"documents": agent_machine.rag.get_summary()["documents"]}


@app.get("/documents/{filename}/summary")
async def document_summary(filename: str):
    try:
        return agent_machine.rag.summarize_document(filename)
    except (KeyError, ValueError) as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.get("/documents/{filename}/inspect")
async def inspect_document(filename: str, start_line: int = 1, window: int = 40, max_chars: int = 4000):
    try:
        return agent_machine.rag.inspect_document(filename, start_line=start_line, window=window, max_chars=max_chars)
    except (KeyError, ValueError) as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.post("/documents/{filename}/grep")
async def grep_document(filename: str, payload: DocumentGrepRequest):
    try:
        return agent_machine.rag.grep_document(
            filename,
            pattern=payload.pattern,
            max_results=payload.max_results,
            case_sensitive=payload.case_sensitive,
        )
    except (KeyError, ValueError, re.error) as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.get("/documents/{filename}/view")
async def view_document(filename: str):
    doc = agent_machine.rag.get_document_record(filename)
    if doc is None:
        raise HTTPException(status_code=404, detail=f"Unknown document: {filename}")
    path = doc.get("path")
    if isinstance(path, str) and path and not os.path.isabs(path):
        path = os.path.normpath(os.path.join(os.path.dirname(__file__), path.lstrip("./")))
    if not path or not os.path.exists(path):
        raise HTTPException(status_code=404, detail=f"Document file not found: {filename}")
    if filename.lower().endswith(".pdf"):
        return FileResponse(
            path,
            media_type="application/pdf",
            filename=filename,
            headers={"Content-Disposition": f'inline; filename="{filename}"'},
        )
    media_type, _ = mimetypes.guess_type(path)
    return FileResponse(path, media_type=media_type or "application/octet-stream", filename=filename)


@app.get("/documents/{filename}/thumbnail")
async def document_thumbnail(filename: str, page: int = 1):
    from rag_manager import _render_pdf_page
    doc_rec = agent_machine.rag.get_document_record(filename)
    if doc_rec is None:
        raise HTTPException(status_code=404, detail=f"Unknown document: {filename}")
    path = doc_rec.get("path")
    if isinstance(path, str) and path and not os.path.isabs(path):
        path = os.path.normpath(os.path.join(os.path.dirname(__file__), path.lstrip("./")))
    if not path or not os.path.exists(path):
        raise HTTPException(status_code=404, detail="File not found")
    lower = path.lower()
    if lower.endswith((".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".tif", ".tiff", ".heic")):
        media_type, _ = mimetypes.guess_type(path)
        return FileResponse(path, media_type=media_type or "image/jpeg", filename=filename)
    png_bytes = _render_pdf_page(path, page_number=page, dpi=150)
    if png_bytes is None:
        raise HTTPException(status_code=404, detail=f"Could not render page {page}")
    from fastapi.responses import Response
    return Response(content=png_bytes, media_type="image/png")


@app.get("/documents/{filename}/page-image")
async def document_page_image(filename: str, page: int = 1):
    from rag_manager import _render_pdf_page
    doc_rec = agent_machine.rag.get_document_record(filename)
    if doc_rec is None:
        raise HTTPException(status_code=404, detail=f"Unknown document: {filename}")
    path = doc_rec.get("path")
    if isinstance(path, str) and path and not os.path.isabs(path):
        path = os.path.normpath(os.path.join(os.path.dirname(__file__), path.lstrip("./")))
    if not path or not os.path.exists(path):
        raise HTTPException(status_code=404, detail="File not found")
    png_bytes = _render_pdf_page(path, page_number=page, dpi=200)
    if png_bytes is None:
        raise HTTPException(status_code=404, detail=f"Could not render page {page}")
    from fastapi.responses import Response
    return Response(content=png_bytes, media_type="image/png")


@app.post("/documents/{filename}/rescan")
async def rescan_document(filename: str, task: str = "Text Recognition"):
    doc = agent_machine.rag.get_document_record(filename)
    if not doc:
        raise HTTPException(status_code=404, detail=f"Unknown document: {filename}")
    
    from ocr_engine import ocr_engine
    print(f"[OCR] Re-scanning {filename} with GLM-OCR ({task})...")
    scanned_text = await ocr_engine.scan_file(doc["path"], task=task)
    
    # Re-ingest with updated text
    success, message = agent_machine.rag.ingest_file(
        doc["path"],
        user_preferences=doc.get("user_preferences", {}),
        ai_metadata=doc.get("ai_metadata", {}),
        force_ocr_text=scanned_text # Pass scanned text to RAG
    )
    return {"filename": filename, "rescan_success": success, "message": message}


@app.get("/documents/{filename}/search")
async def search_in_pdf(filename: str, query: str):
    if not query:
        return {"results": []}
    try:
        matches = agent_machine.rag.find_text_in_pages(filename, query)
        return {"filename": filename, "query": query, "pages": matches}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/documents/{filename}/metadata")
async def document_metadata(filename: str):
    doc_rec = agent_machine.rag.get_document_record(filename)
    if doc_rec is None:
        raise HTTPException(status_code=404, detail=f"Unknown document: {filename}")
    pdf_meta = dict(doc_rec.get("pdf_metadata") or {})
    ft = doc_rec.get("file_type") or "unknown"
    page_count = pdf_meta.get("page_count")
    if page_count is None:
        page_count = 1
    pdf_meta["page_count"] = page_count
    meta_out = pdf_meta
    return {
        "filename": filename,
        "metadata": meta_out,
        "file_type": ft,
        "file_size_bytes": doc_rec.get("file_size_bytes", 0),
    }


@app.get("/documents/{filename}/pdf-inspect")
async def pdf_inspect_document(filename: str):
    try:
        return agent_machine.rag.inspect_pdf(filename)
    except (KeyError, ValueError) as exc:
        raise HTTPException(status_code=404, detail=str(exc))


@app.post("/cases/{case_id}/ai-orchestrate")
async def ai_orchestrate_case(case_id: str, payload: AIOrchestrationRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    if payload.objective not in {"evaluation", "draft"}:
        raise HTTPException(status_code=400, detail="objective must be 'evaluation' or 'draft'")
    orchestration = await agent_machine.orchestrate_case(case_id, objective=payload.objective)
    return {"status": "completed", "case_id": case_id, "orchestration": orchestration}


@app.post("/cases/{case_id}/ai-orchestrate/stream")
async def ai_orchestrate_case_stream(case_id: str, payload: AIOrchestrationRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")

    async def event_generator():
        yield b"data: " + orjson.dumps(
            {"status": "planning", "message": f"Planning {payload.objective}..."}
        ) + b"\n\n"
        orchestration = await agent_machine.orchestrate_case(case_id, objective=payload.objective)
        plan = orchestration["plan"]
        yield b"data: " + orjson.dumps({"status": "retrieving", "plan": plan}) + b"\n\n"
        yield b"data: " + orjson.dumps({"status": "synthesizing"}) + b"\n\n"
        full_text = orchestration["synthesis"]
        
        # Save to case state for UI display
        case = agent_machine.cases[case_id]
        if payload.objective == "draft":
            case["draft_preview"] = full_text[:500]
            case["document_draft"] = case.get("document_draft") or {}
            case["document_draft"]["draft_text"] = full_text
            case["document_draft"]["sections"] = [{"heading": "AI Generated Draft", "body": full_text}]
        elif payload.objective == "evaluation":
            case["evaluation"] = full_text
            case["evaluation_detail"] = case.get("evaluation_detail") or {}
            case["evaluation_detail"]["evaluation_text"] = full_text
        
        agent_machine.notify_change()
        
        # Stream the text chunk by chunk
        chunk_size = 20
        for i in range(0, len(full_text), chunk_size):
            chunk = full_text[i : i + chunk_size]
            yield b"data: " + orjson.dumps({"status": "typing", "chunk": chunk}) + b"\n\n"
            await asyncio.sleep(0.02)
        yield b"data: " + orjson.dumps({"status": "completed", "full_text": full_text}) + b"\n\n"

    return StreamingResponse(event_generator(), media_type="text/event-stream")


@app.post("/cases/{case_id}/run-agent")
async def run_agent(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    if case.get("_agent_running"):
        raise HTTPException(status_code=409, detail="Agent already running for this case")
    case["_agent_running"] = True
    asyncio.create_task(_run_agent_background(case_id))
    return {"status": "started", "case_id": case_id}


async def _run_agent_background(case_id: str):
    try:
        await agent_machine.run_graph_until_pause(case_id)
    finally:
        case = agent_machine.cases.get(case_id)
        if case:
            case["_agent_running"] = False
        agent_machine.notify_change()


@app.post("/cases/{case_id}/run-pipeline")
async def run_full_pipeline(case_id: str):
    """Run all states in the workflow until completion or HITL gate."""
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    if case.get("_pipeline_running"):
        raise HTTPException(status_code=409, detail="Pipeline already running for this case")
    case["_pipeline_running"] = True
    asyncio.create_task(_run_pipeline_background(case_id))
    return {"status": "started", "case_id": case_id, "message": "Full pipeline execution started"}


async def _run_pipeline_background(case_id: str):
    try:
        await agent_machine.process_all_states(case_id)
    finally:
        case = agent_machine.cases.get(case_id)
        if case:
            case["_pipeline_running"] = False
        agent_machine.notify_change()


@app.post("/demo/load-mock-case")
async def load_mock_case():
    case = agent_machine.create_case(
        client_name="PROS, INC. (Mock)",
        matter_type="Commercial Lease Dispute",
        source_channel="Demo Intake",
        initial_msg="Tenant requests rent abatement because landlord work and building services were incomplete.",
    )
    agent_machine.select_case(case["case_id"])

    mock_path = "./data/mock_cases/mock_commercial_lease_case_2018_sec_source.txt"
    if os.path.exists(mock_path):
        success, message = agent_machine.rag.ingest_file(mock_path)
        if success:
            agent_machine.attach_document_to_case("mock_commercial_lease_case_2018_sec_source.txt", case["case_id"])
        else:
            message = f"Mock case loaded into case file, but ingestion failed: {message}"
    else:
        message = "Mock case file is missing"

    agent_machine.add_note(case["case_id"], "Loaded mock commercial lease dispute from public SEC-derived sample.")
    return {"status": "loaded", "message": message, "case": agent_machine.get_case_status(case["case_id"])}


if MULTIPART_AVAILABLE:
    async def _process_one_upload(
        *,
        target_case_id: str,
        upload: UploadFile,
        category: str,
        tags: str,
        priority: str,
        auto_category: bool,
        id_card_meta: dict | None = None,
        ocr_stagger_sec: float = 0.0,
        allow_auto_case_from_ai: bool = False,
    ) -> dict:
        from ocr_engine import ocr_engine

        safe_name = os.path.basename(upload.filename or "")
        if not safe_name:
            return {"filename": "", "ingested": False, "message": "Missing filename", "case_id": target_case_id}

        os.makedirs("./data/docs", exist_ok=True)
        save_path = f"./data/docs/{safe_name}"
        content = await upload.read()
        with open(save_path, "wb") as handle:
            handle.write(content)

        if ocr_stagger_sec and ocr_stagger_sec > 0:
            await asyncio.sleep(ocr_stagger_sec)

        print(f"[Upload] Running initial scan for {safe_name}...")
        scanned_text = await ocr_engine.scan_file(save_path, task="Text Recognition")

        # Quick entity extraction to find case matches before LLM call
        entity_match = _find_case_by_ocr_content(scanned_text[:2000])
        if entity_match:
            target_case_id = entity_match
            print(f"[Upload] Entity match found, routing to case {entity_match}")

        text_preview = scanned_text[:6000]
        cat_list = ", ".join(f'"{c}"' for c in ALLOWED_EVIDENCE_CATEGORIES)
        ai_scan_prompt = (
            "Analyze this OCR-scanned legal document and return a JSON object with: "
            "parties (list of people/companies), document_date (YYYY-MM-DD), case_type (str), "
            "key_clauses (list of critical topics), urgent_notes (str), "
            f"and evidence_category (exactly one of: {cat_list}). "
            "Pick the single best evidence_category bucket for filing this document. "
            "Consider Chinese legal document naming conventions."
        )
        ai_metadata = await asyncio.to_thread(
            agent_machine.llm.generate_structured_json,
            ai_scan_prompt,
            text_preview,
            "small",
            {
                "parties": [],
                "document_date": "unknown",
                "case_type": "unknown",
                "key_clauses": [],
                "urgent_notes": "",
                "evidence_category": "General Evidence",
            },
        )

        # If the user didn't select a case and we couldn't infer one from filename/OCR entity match,
        # use AI-extracted parties as a last-resort to auto-create/match a case.
        if allow_auto_case_from_ai and not entity_match:
            parties = ai_metadata.get("parties") if isinstance(ai_metadata, dict) else None
            if isinstance(parties, list) and parties:
                candidate = ""
                for p in parties:
                    if isinstance(p, str) and 2 <= len(p.strip()) <= 30:
                        candidate = p.strip()
                        break
                if candidate:
                    r = agent_machine.find_or_create_case_for_client(
                        candidate,
                        matter_type=(ai_metadata.get("case_type") or "") or "Civil Litigation",
                        source_channel="Document Upload",
                        initial_msg=f"Auto-created from AI party extraction: {safe_name}",
                    )
                    target_case_id = r["case_id"]
                    print(f"[Upload] AI party match, routing to case {target_case_id}")

        if auto_category:
            filename_hint = _auto_categorize_by_filename(safe_name)
            if filename_hint:
                final_category = filename_hint
            else:
                final_category = _normalize_evidence_category(ai_metadata.get("evidence_category"))
        else:
            final_category = _normalize_evidence_category(category)

        user_prefs = {
            "category": final_category,
            "tags": [t.strip() for t in tags.split(",") if t.strip()],
            "priority": priority,
            "auto_category": auto_category,
        }
        if id_card_meta:
            for k, v in id_card_meta.items():
                if v is not None and v != "":
                    user_prefs[k] = v

        success, message = agent_machine.rag.ingest_file(
            save_path,
            user_preferences=user_prefs,
            ai_metadata=ai_metadata,
            force_ocr_text=scanned_text,
        )

        if success:
            agent_machine.attach_document_to_case(safe_name, target_case_id)
            agent_machine.add_note(
                target_case_id,
                f"Doc '{safe_name}' scanned: {ai_metadata.get('case_type', 'Legal material')} detected. Category: {final_category}.",
            )

        return {
            "filename": safe_name,
            "ingested": success,
            "message": message,
            "ai_metadata": ai_metadata,
            "user_preferences": user_prefs,
            "category_applied": final_category,
            "case_id": target_case_id,
        }

    @app.post("/upload")
    async def upload_document(
        file: UploadFile = File(...),
        case_id: str | None = Query(None),
        client_name: str = Form(""),
        matter_type: str = Form(""),
        category: str = Form("__auto__"),
        tags: str = Form(""),
        priority: str = Form("Medium"),
        id_card_group_id: str = Form(""),
        id_card_side: str = Form(""),
    ):
        safe_name = os.path.basename(file.filename or "") if file.filename else ""
        target_case_id, resolve_meta = _resolve_upload_target_case(case_id, client_name, matter_type, safe_name)
        allow_auto_case_from_ai = (not (client_name or "").strip()) and not (case_id or "").strip()
        auto_cat = category.strip().lower() in {"__auto__", "auto"}
        manual_category = "" if auto_cat else category
        side_norm = (id_card_side or "").strip().lower()
        if side_norm not in ("", "front", "back"):
            side_norm = ""
        id_meta = None
        if side_norm in ("front", "back"):
            gid = (id_card_group_id or "").strip() or str(uuid.uuid4())
            id_meta = {"id_card_group_id": gid, "id_card_side": side_norm}
        result = await _process_one_upload(
            target_case_id=target_case_id,
            upload=file,
            category=manual_category,
            tags=tags,
            priority=priority,
            auto_category=auto_cat,
            id_card_meta=id_meta,
            allow_auto_case_from_ai=allow_auto_case_from_ai,
        )
        result.update(resolve_meta)
        return result

    @app.post("/upload/batch")
    async def upload_documents_batch(
        files: list[UploadFile] = File(...),
        case_id: str | None = Query(None),
        client_name: str = Form(""),
        matter_type: str = Form(""),
        category: str = Form("__auto__"),
        tags: str = Form(""),
        priority: str = Form("Medium"),
        id_pair_batch: str = Form("false"),
        id_card_group_id: str = Form(""),
        id_card_side: str = Form(""),
        id_ocr_stagger_sec: str = Form("0.45"),
    ):
        if not files:
            raise HTTPException(status_code=400, detail="No files uploaded")
        pair = _form_bool(id_pair_batch)
        if pair and len(files) != 2:
            raise HTTPException(
                status_code=400,
                detail="ID front/back pair mode requires exactly 2 image files in order: front, then back.",
            )
        
        # Auto-detect client/matter from first filename if not provided
        first_fn = os.path.basename(files[0].filename or "") if files[0].filename else ""
        target_case_id, resolve_meta = _resolve_upload_target_case(case_id, client_name, matter_type, first_fn)
        allow_auto_case_from_ai = (not (client_name or "").strip()) and not (case_id or "").strip()
        auto_cat = category.strip().lower() in {"__auto__", "auto"}
        manual_category = "" if auto_cat else category
        stagger = _parse_float_form(id_ocr_stagger_sec, 0.45)
        side_norm = (id_card_side or "").strip().lower()
        if side_norm not in ("", "front", "back"):
            side_norm = ""
        if pair and side_norm:
            raise HTTPException(
                status_code=400,
                detail="Use either ID pair (2 files) or a single id_card_side per batch, not both.",
            )
        if len(files) > 1 and side_norm in ("front", "back") and not pair:
            raise HTTPException(
                status_code=400,
                detail="For multiple files, use ID pair mode (2 files: front then back) or omit id_card_side.",
            )
        group_id = (id_card_group_id or "").strip()
        if pair:
            group_id = group_id or str(uuid.uuid4())
        elif side_norm in ("front", "back"):
            group_id = group_id or str(uuid.uuid4())
        results = []
        for i, f in enumerate(files):
            id_meta = None
            if pair:
                id_meta = {
                    "id_card_group_id": group_id,
                    "id_card_side": "front" if i == 0 else "back",
                    "id_card_pair_batch": True,
                }
            elif side_norm in ("front", "back"):
                id_meta = {"id_card_group_id": group_id, "id_card_side": side_norm}
            stagger_sec = stagger if (pair and i > 0) else 0.0
            results.append(
                await _process_one_upload(
                    target_case_id=target_case_id,
                    upload=f,
                    category=manual_category,
                    tags=tags,
                    priority=priority,
                    auto_category=auto_cat,
                    id_card_meta=id_meta,
                    ocr_stagger_sec=stagger_sec,
                    allow_auto_case_from_ai=allow_auto_case_from_ai,
                )
            )
        ok = sum(1 for r in results if r.get("ingested"))
        return {
            "case_id": target_case_id,
            "count": len(results),
            "ingested_ok": ok,
            "results": results,
            **resolve_meta,
        }

    @app.post("/upload/directory")
    async def upload_directory(
        directory_path: str = Form(...),
        client_name: str = Form(""),
        matter_type: str = Form(""),
    ):
        """Upload all files from a directory, auto-detect client name and matter type."""
        if not os.path.isdir(directory_path):
            raise HTTPException(status_code=400, detail=f"Directory not found: {directory_path}")

        all_files = []
        for root, dirs, files in os.walk(directory_path):
            for fn in files:
                fp = os.path.join(root, fn)
                if os.path.isfile(fp):
                    all_files.append(fp)

        if not all_files:
            raise HTTPException(status_code=400, detail="No files found in directory")

        auto_client = client_name.strip() or _extract_client_from_filename(os.path.basename(directory_path))
        for fn in all_files:
            if not auto_client:
                auto_client = _extract_client_from_filename(os.path.basename(fn))
                if auto_client:
                    break

        auto_matter = matter_type.strip() or _extract_matter_type_from_filenames(
            [os.path.basename(f) for f in all_files]
        )

        if not auto_client:
            auto_client = os.path.basename(directory_path).strip() or "Unknown Client"

        r = agent_machine.find_or_create_case_for_client(
            auto_client,
            matter_type=auto_matter,
        )
        target_case_id = r["case_id"]

        ingested = []
        failed = []
        for fp in all_files:
            safe_name = os.path.basename(fp)
            filename_hint = _auto_categorize_by_filename(safe_name)

            try:
                scanned_text = await ocr_engine.scan_file(fp, task="Text Recognition")
            except Exception:
                scanned_text = ""

            text_preview = scanned_text[:6000]
            cat_list = ", ".join(f'"{c}"' for c in ALLOWED_EVIDENCE_CATEGORIES)
            ai_scan_prompt = (
                "Analyze this legal document and return a JSON object with: "
                "parties, document_date, case_type, key_clauses, urgent_notes, "
                f"and evidence_category (exactly one of: {cat_list})."
            )
            ai_metadata = await asyncio.to_thread(
                agent_machine.llm.generate_structured_json,
                ai_scan_prompt,
                text_preview,
                "small",
                {
                    "parties": [],
                    "document_date": "unknown",
                    "case_type": "unknown",
                    "key_clauses": [],
                    "urgent_notes": "",
                    "evidence_category": "General Evidence",
                },
            )

            final_category = filename_hint or _normalize_evidence_category(
                ai_metadata.get("evidence_category", "General Evidence")
            )

            dest = f"./data/docs/{safe_name}"
            os.makedirs("./data/docs", exist_ok=True)
            if not os.path.exists(dest):
                import shutil
                shutil.copy2(fp, dest)

            success, message = agent_machine.rag.ingest_file(
                dest,
                user_preferences={"category": final_category},
                ai_metadata=ai_metadata,
                force_ocr_text=scanned_text if scanned_text else None,
            )
            if success:
                agent_machine.attach_document_to_case(safe_name, target_case_id)
                ingested.append(safe_name)
            else:
                failed.append({"filename": safe_name, "error": message})

        case = agent_machine.cases.get(target_case_id)
        if case:
            agent_machine.add_note(
                target_case_id,
                f"Directory upload: {len(ingested)} files ingested from {os.path.basename(directory_path)}",
            )
            agent_machine.notify_change()

        return {
            "status": "completed",
            "case_id": target_case_id,
            "client_name": auto_client,
            "matter_type": auto_matter,
            "case_created": r["created"],
            "ingested_count": len(ingested),
            "failed_count": len(failed),
            "ingested_files": ingested,
            "failed_files": failed,
        }

    @app.post("/cases/{case_id}/ai-summarize")
    async def ai_summarize_case(case_id: str):
        """LLM scans all ingested documents and generates a case summary in Chinese."""
        if case_id not in agent_machine.cases:
            raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")

        case = agent_machine.cases[case_id]
        all_text_parts = []
        file_summaries = []

        for doc in agent_machine.rag.documents:
            fn = doc.get("filename", "")
            attached = fn in (case.get("uploaded_documents") or [])
            if not attached:
                continue

            chunks = doc.get("chunks", [])
            doc_text = "\n".join(chunks)
            all_text_parts.append(f"=== {fn} ===\n{doc_text[:3000]}")

            ai_meta = doc.get("ai_metadata", {})
            file_summaries.append({
                "filename": fn,
                "category": doc.get("user_preferences", {}).get("category", "unknown"),
                "case_type": ai_meta.get("case_type", "unknown"),
                "parties": ai_meta.get("parties", []),
                "key_clauses": ai_meta.get("key_clauses", []),
            })

        combined_text = "\n\n".join(all_text_parts)
        if not combined_text.strip():
            return {"summary": "暂无可用文档材料进行总结。", "file_summaries": file_summaries}

        summary_prompt = (
            "你是一名专业的中国法律助理。请仔细阅读以下案件材料，然后用中文撰写一份简明扼要的案件总结报告。\n\n"
            "报告必须包含以下四个部分，每部分用标题分隔：\n\n"
            "## 一、案件概述\n"
            "简要说明发生了什么纠纷，涉及什么类型的案件，核心争议点是什么。\n\n"
            "## 二、涉及的当事人\n"
            "列出所有相关当事人及其角色（原告/被告/第三方等）。\n\n"
            "## 三、关键证据和事实\n"
            "列出最重要的证据和事实，包括金额、日期、合同条款等关键信息。\n\n"
            "## 四、法律风险和建议\n"
            "分析案件的法律风险，并给出具体的下一步行动建议。\n\n"
            "要求：\n"
            "- 必须使用中文回答\n"
            "- 基于提供的材料，不要编造信息\n"
            "- 简明扼要，总字数不超过600字\n"
            "- 重点突出关键数字（金额、日期等）"
        )

        summary = await asyncio.to_thread(
            agent_machine.llm.generate,
            summary_prompt,
            context=combined_text[:8000],
        )

        case["ai_case_summary"] = summary
        case["ai_file_summaries"] = file_summaries
        agent_machine.add_note(case_id, f"AI案件总结已生成（分析了 {len(file_summaries)} 份文件）")
        agent_machine.notify_change()

        return {
            "case_id": case_id,
            "summary": summary,
            "files_analyzed": len(file_summaries),
            "file_summaries": file_summaries,
        }
else:
    @app.post("/upload")
    async def upload_document_unavailable():
        raise HTTPException(status_code=503, detail="python-multipart is required for uploads in this environment")

    @app.post("/upload/batch")
    async def upload_batch_unavailable():
        raise HTTPException(status_code=503, detail="python-multipart is required for uploads in this environment")


@app.websocket("/ws")
async def websocket_endpoint(websocket: WebSocket):
    await manager.connect(websocket)
    try:
        while True:
            await asyncio.sleep(30)
    except WebSocketDisconnect:
        manager.disconnect(websocket)


@app.get("/v1/status")
async def v1_status():
    tracker = agent_machine.tracker
    return {
        "version": "v1",
        "device": agent_machine.device_info,
        "config": agent_machine.device_config,
        "tracking": tracker.get_status(),
        "tools": agent_machine.tool_registry.list_tools(),
        "openclaw_status": "paused",
        "openclaw_note": "V1 mode. OpenClaw re-enabled in V2 with adaptive optimization.",
    }


@app.get("/v1/tracking/summary")
async def v1_tracking_summary(hours: int = 24):
    return agent_machine.tracker.generate_summary(period_hours=hours)


@app.get("/v1/tracking/events")
async def v1_tracking_events(limit: int = 50, event_type: str | None = None):
    return {"events": agent_machine.tracker.get_recent_events(limit=limit, event_type=event_type)}


class ToolModeRequest(BaseModel):
    tool_name: str
    mode: str


@app.post("/v1/tools/mode")
async def v1_set_tool_mode(payload: ToolModeRequest):
    try:
        mode = ToolMode(payload.mode)
    except ValueError:
        raise HTTPException(status_code=400, detail=f"Invalid mode: {payload.mode}. Use 'manual' or 'auto'.")
    agent_machine.tool_registry.set_tool_mode(payload.tool_name, mode)
    agent_machine.tracker.record_user_action("set_tool_mode", f"{payload.tool_name}:{payload.mode}")
    return {"status": "updated", "tool_name": payload.tool_name, "mode": payload.mode}


@app.post("/v1/tools/mode-all")
async def v1_set_all_tool_modes(mode: str):
    try:
        tool_mode = ToolMode(mode)
    except ValueError:
        raise HTTPException(status_code=400, detail=f"Invalid mode: {mode}")
    agent_machine.tool_registry.set_all_mode(tool_mode)
    return {"status": "updated", "mode": mode}


@app.get("/cases/{case_id}/highlights")
async def get_highlights(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    return {"case_id": case_id, "highlights": case.get("highlights", [])}


@app.get("/cases/{case_id}/report")
async def get_case_report(case_id: str, download: bool = False, fmt: str = "pdf"):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    draft = case.get("document_draft") or {}
    delivery = case.get("delivery_record") or {}

    if fmt == "docx":
        report_info = delivery.get("docx_report") or draft.get("docx_report")
    else:
        report_info = delivery.get("pdf_report") or draft.get("pdf_report")

    if not report_info or not report_info.get("path"):
        raise HTTPException(status_code=404, detail=f"No {fmt.upper()} report generated yet. Run the agent first.")
    file_path = report_info["path"]
    if not os.path.exists(file_path):
        raise HTTPException(status_code=404, detail=f"{fmt.upper()} file not found on disk.")
    disposition = "attachment" if download else "inline"
    filename = report_info.get("filename", os.path.basename(file_path))
    media_type = "application/vnd.openxmlformats-officedocument.wordprocessingml.document" if fmt == "docx" else "application/pdf"
    return FileResponse(
        file_path,
        media_type=media_type,
        filename=filename,
        headers={"Content-Disposition": f'{disposition}; filename="{filename}"'},
    )


class GenerateReportRequest(BaseModel):
    title: str | None = None
    include_highlights: bool = True
    format: str = "both"


@app.post("/cases/{case_id}/generate-report")
async def generate_case_report(case_id: str, payload: GenerateReportRequest | None = None):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    evaluation = case.get("evaluation_detail") or {}
    draft = case.get("document_draft") or {}
    highlights = case.get("highlights", []) if (payload and payload.include_highlights) else []
    title = (payload and payload.title) or f"{case.get('matter_type', 'Case')} Report"
    fmt = (payload and payload.format) or "both"

    sections = []
    if evaluation.get("evaluation_text"):
        sections.append({"heading": "Case Evaluation", "body": evaluation["evaluation_text"]})
    if draft.get("draft_text"):
        sections.append({"heading": "Legal Document Draft", "body": draft["draft_text"]})
    if draft.get("sections"):
        sections = draft["sections"]
    if not sections:
        sections.append({"heading": "Case Summary", "body": case.get("draft_preview", "No content generated yet.")})

    results = {}
    if fmt in ("pdf", "both"):
        pdf_result = await agent_machine.tool_registry.execute(
            "generate_pdf",
            title=title,
            case_id=case_id,
            client_name=case.get("client_name", ""),
            sections=sections,
            highlights=highlights,
        )
        if pdf_result.success:
            results["pdf"] = pdf_result.output
            case["document_draft"] = case.get("document_draft") or {}
            case["document_draft"]["pdf_report"] = pdf_result.output
            agent_machine.add_note(case_id, f"PDF report generated: {pdf_result.output.get('filename')}")

    if fmt in ("docx", "both"):
        docx_result = await agent_machine.tool_registry.execute(
            "generate_docx",
            title=title,
            case_id=case_id,
            client_name=case.get("client_name", ""),
            sections=sections,
            highlights=highlights,
        )
        if docx_result.success:
            results["docx"] = docx_result.output
            case["document_draft"] = case.get("document_draft") or {}
            case["document_draft"]["docx_report"] = docx_result.output
            agent_machine.add_note(case_id, f"DOCX report generated: {docx_result.output.get('filename')}")

    if results:
        agent_machine.notify_change()
        return {"status": "generated", "reports": results}
    raise HTTPException(status_code=500, detail="Report generation failed for all formats")


class SectionsUpdateRequest(BaseModel):
    sections: list[dict]


@app.put("/cases/{case_id}/sections")
async def update_case_sections(case_id: str, payload: SectionsUpdateRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    case["document_draft"] = case.get("document_draft") or {}
    case["document_draft"]["sections"] = payload.sections
    agent_machine.add_note(case_id, f"Document sections updated ({len(payload.sections)} sections)")
    agent_machine.notify_change()
    return {"status": "updated", "section_count": len(payload.sections)}


@app.get("/cases/{case_id}/sections")
async def get_case_sections(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
    case = agent_machine.cases[case_id]
    draft = case.get("document_draft") or {}
    sections = draft.get("sections", [])
    if not sections and draft.get("draft_text"):
        sections = [{"heading": "Document", "body": draft["draft_text"]}]
    return {"case_id": case_id, "sections": sections}


if MULTIPART_AVAILABLE:
    @app.post("/cases/{case_id}/ingest-docx")
    async def ingest_edited_docx(case_id: str, file: UploadFile = File(...)):
        if case_id not in agent_machine.cases:
            raise HTTPException(status_code=404, detail=f"Unknown case_id: {case_id}")
        if not file.filename or not file.filename.lower().endswith(".docx"):
            raise HTTPException(status_code=400, detail="Must upload a .docx file")

        case = agent_machine.cases[case_id]
        os.makedirs("./data/docs", exist_ok=True)
        save_path = f"./data/docs/{file.filename}"
        content = await file.read()
        with open(save_path, "wb") as handle:
            handle.write(content)

        try:
            from docx import Document
            doc = Document(save_path)
            extracted_text = "\n\n".join(p.text for p in doc.paragraphs if p.text.strip())
        except Exception as exc:
            raise HTTPException(status_code=500, detail=f"Failed to read DOCX: {exc}")

        success, message = agent_machine.rag.ingest_file(
            save_path,
            user_preferences={"category": "Legal Document", "source": "edited_docx"},
            ai_metadata={"case_id": case_id, "client_name": case.get("client_name", "")},
            force_ocr_text=extracted_text,
        )
        if success:
            agent_machine.attach_document_to_case(file.filename, case_id)
            agent_machine.add_note(case_id, f"Edited DOCX re-ingested: {file.filename}")
            agent_machine.notify_change()
        return {"filename": file.filename, "ingested": success, "message": message, "case_id": case_id}


@app.get("/v1/device")
async def v1_device_info():
    return get_device_report()


@app.post("/v1/auto-setup")
async def v1_auto_setup():
    result = auto_setup()
    agent_machine.tracker.record_user_action("auto_setup")
    return result


@app.get("/dashboard")
async def get_dashboard():
    return agent_machine.get_dashboard_summary()


class TemplateGenerateRequest(BaseModel):
    template_id: str
    case_id: str | None = None
    field_values: dict[str, str]
    do_research: bool | None = None


class TemplateDraftUpdate(BaseModel):
    edited_text: str
    final_adopted: bool = False


@app.get("/templates")
async def list_templates(matter_type: str | None = None):
    return {"templates": template_generator.list_templates(matter_type=matter_type)}


@app.get("/templates/{template_id}")
async def get_template(template_id: str):
    tmpl = template_generator.get_template(template_id)
    if not tmpl:
        raise HTTPException(status_code=404, detail="Template not found")
    return {"template": tmpl}


@app.post("/templates/generate")
async def generate_template(payload: TemplateGenerateRequest):
    try:
        draft = await template_generator.generate(
            template_id=payload.template_id,
            field_values=payload.field_values,
            case_id=payload.case_id,
            do_research=payload.do_research,
        )
        draft_id = template_generator.save_draft(draft)
        return {
            "draft_id": draft_id,
            "template_label": draft.template_label,
            "generated_text": draft.generated_text,
            "research_count": len(draft.research_results),
            "research_results": draft.research_results[:6],
            "filled_fields": draft.filled_fields,
        }
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc))


@app.get("/templates/drafts")
async def list_template_drafts(template_id: str | None = None, limit: int = 20):
    return {"drafts": template_generator.list_drafts(template_id=template_id, limit=limit)}


@app.get("/templates/drafts/{draft_id}")
async def get_template_draft(draft_id: str):
    draft = template_generator.get_draft(draft_id)
    if not draft:
        raise HTTPException(status_code=404, detail="Draft not found")
    return {"draft": draft}


@app.put("/templates/drafts/{draft_id}")
async def update_template_draft(draft_id: str, payload: TemplateDraftUpdate):
    success = template_generator.update_draft(draft_id, payload.edited_text, payload.final_adopted)
    if not success:
        raise HTTPException(status_code=404, detail="Draft not found")
    return {"status": "updated", "draft_id": draft_id, "final_adopted": payload.final_adopted}


@app.get("/templates/preferences")
async def get_template_preferences():
    return template_generator.get_preferences()


@app.put("/templates/preferences")
async def set_template_preference(key: str, value: str):
    try:
        int_val = int(value)
        template_generator.set_preference(key, int_val)
    except ValueError:
        if value.lower() in ("true", "false"):
            template_generator.set_preference(key, value.lower() == "true")
        else:
            template_generator.set_preference(key, value)
    return template_generator.get_preferences()


@app.post("/cases/{case_id}/billing/start")
async def billing_start(case_id: str, description: str = ""):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.billing_start(case_id, description)


@app.post("/cases/{case_id}/billing/stop")
async def billing_stop(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.billing_stop(case_id)


@app.get("/cases/{case_id}/billing")
async def billing_get(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.billing_get(case_id)


class DeadlineAddRequest(BaseModel):
    deadline_date: str
    description: str
    priority: str = "medium"


@app.post("/cases/{case_id}/deadlines")
async def add_deadline(case_id: str, payload: DeadlineAddRequest):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.add_deadline(case_id, payload.deadline_date, payload.description, payload.priority)


@app.get("/cases/{case_id}/deadlines")
async def get_deadlines(case_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.get_deadlines(case_id)


@app.post("/cases/{case_id}/deadlines/{deadline_id}/complete")
async def complete_deadline(case_id: str, deadline_id: str):
    if case_id not in agent_machine.cases:
        raise HTTPException(status_code=404, detail="Case not found")
    return agent_machine.complete_deadline(case_id, deadline_id)


@app.get("/documents/{filename}/entities")
async def extract_document_entities(filename: str):
    try:
        return agent_machine.rag.extract_entities(filename)
    except (KeyError, ValueError) as exc:
        raise HTTPException(status_code=404, detail=str(exc))


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
