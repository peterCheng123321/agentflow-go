import hashlib
import json
import os
import sqlite3
import threading
import time
from collections import Counter
from datetime import datetime, UTC
from typing import Any


_SCHEMA = """
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts REAL NOT NULL,
    event_type TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'general',
    payload TEXT NOT NULL DEFAULT '{}',
    session_id TEXT NOT NULL DEFAULT 'default'
);

CREATE TABLE IF NOT EXISTS summaries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    period_type TEXT NOT NULL,
    period_key TEXT NOT NULL,
    summary_json TEXT NOT NULL,
    created_at REAL NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_category ON events(category);
"""


def _hash_pii(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:16]


def _now_ts() -> float:
    return time.time()


def _now_iso() -> str:
    return datetime.now(UTC).isoformat()


class UsageTracker:
    DATA_BUDGET_MB = 50
    EVENTS_TABLE = "events"

    def __init__(self, db_path: str | None = None):
        if db_path is None:
            db_path = os.path.join(
                os.path.dirname(__file__), "data", "tracking", "usage.db"
            )
        self.db_path = db_path
        os.makedirs(os.path.dirname(db_path), exist_ok=True)
        self._local = threading.local()
        self._session_id = _now_ts()
        self._init_db()

    def _get_conn(self) -> sqlite3.Connection:
        if not hasattr(self._local, "conn") or self._local.conn is None:
            self._local.conn = sqlite3.connect(self.db_path)
            self._local.conn.execute("PRAGMA journal_mode=WAL")
            self._local.conn.execute("PRAGMA synchronous=NORMAL")
        return self._local.conn

    def _init_db(self):
        conn = self._get_conn()
        conn.executescript(_SCHEMA)
        conn.commit()

    def record(
        self,
        event_type: str,
        category: str = "general",
        payload: dict[str, Any] | None = None,
    ):
        if payload is None:
            payload = {}
        if "client_name" in payload:
            payload["client_name_hash"] = _hash_pii(str(payload.pop("client_name")))
        if "contact_name" in payload:
            payload["contact_name_hash"] = _hash_pii(str(payload.pop("contact_name")))
        payload.pop("text", None)
        payload.pop("message", None)
        payload.pop("initial_msg", None)

        conn = self._get_conn()
        conn.execute(
            "INSERT INTO events (ts, event_type, category, payload, session_id) VALUES (?, ?, ?, ?, ?)",
            (_now_ts(), event_type, category, json.dumps(payload, default=str), str(self._session_id)),
        )
        conn.commit()
        self._enforce_budget()

    def _enforce_budget(self):
        try:
            size_mb = os.path.getsize(self.db_path) / (1024 * 1024)
            if size_mb <= self.DATA_BUDGET_MB:
                return
            conn = self._get_conn()
            cutoff = conn.execute(
                "SELECT ts FROM events ORDER BY ts DESC LIMIT 1 OFFSET 5000"
            ).fetchone()
            if cutoff:
                conn.execute("DELETE FROM events WHERE ts < ?", (cutoff[0],))
                conn.execute("DELETE FROM summaries WHERE created_at < ?", (cutoff[0],))
                conn.commit()
                conn.execute("VACUUM")
        except Exception:
            pass

    def record_tool_call(self, tool_name: str, success: bool, latency_ms: float = 0, extra: dict | None = None):
        payload = {"tool": tool_name, "success": success, "latency_ms": round(latency_ms, 1)}
        if extra:
            payload.update(extra)
        self.record("tool_call", "tools", payload)

    def record_state_transition(self, from_state: str, to_state: str, case_id: str, duration_s: float = 0):
        self.record("state_transition", "workflow", {
            "from": from_state,
            "to": to_state,
            "case_id_hash": _hash_pii(case_id),
            "duration_s": round(duration_s, 2),
        })

    def record_approval(self, state: str, approved: bool, response_time_s: float = 0):
        self.record("approval", "hitl", {
            "state": state,
            "approved": approved,
            "response_time_s": round(response_time_s, 2),
        })

    def record_document_upload(self, file_ext: str, chunk_count: int, ingest_success: bool):
        self.record("document_upload", "documents", {
            "file_ext": file_ext,
            "chunk_count": chunk_count,
            "ingest_success": ingest_success,
        })

    def record_case_created(self, matter_type: str, source_channel: str):
        self.record("case_created", "cases", {
            "matter_type": matter_type,
            "source_channel": source_channel,
        })

    def record_user_action(self, action: str, detail: str = ""):
        self.record("user_action", "ui", {"action": action, "detail": detail})

    def generate_summary(self, period_hours: int = 24) -> dict:
        conn = self._get_conn()
        cutoff = _now_ts() - (period_hours * 3600)

        total_events = conn.execute(
            "SELECT COUNT(*) FROM events WHERE ts > ?", (cutoff,)
        ).fetchone()[0]

        tool_calls = conn.execute(
            "SELECT payload FROM events WHERE event_type='tool_call' AND ts > ?",
            (cutoff,),
        ).fetchall()

        tool_stats: dict[str, dict] = {}
        for (payload_str,) in tool_calls:
            p = json.loads(payload_str)
            name = p.get("tool", "unknown")
            if name not in tool_stats:
                tool_stats[name] = {"count": 0, "success": 0, "avg_latency_ms": 0, "latencies": []}
            tool_stats[name]["count"] += 1
            if p.get("success"):
                tool_stats[name]["success"] += 1
            tool_stats[name]["latencies"].append(p.get("latency_ms", 0))

        for stats in tool_stats.values():
            lats = stats.pop("latencies")
            stats["avg_latency_ms"] = round(sum(lats) / len(lats), 1) if lats else 0
            stats["success_rate"] = round(stats["success"] / stats["count"], 2) if stats["count"] else 0

        approvals = conn.execute(
            "SELECT payload FROM events WHERE event_type='approval' AND ts > ?",
            (cutoff,),
        ).fetchall()
        approval_summary = {"total": len(approvals), "approved": 0, "avg_response_s": 0}
        response_times = []
        for (payload_str,) in approvals:
            p = json.loads(payload_str)
            if p.get("approved"):
                approval_summary["approved"] += 1
            response_times.append(p.get("response_time_s", 0))
        approval_summary["avg_response_s"] = round(sum(response_times) / len(response_times), 1) if response_times else 0

        state_transitions = conn.execute(
            "SELECT payload FROM events WHERE event_type='state_transition' AND ts > ?",
            (cutoff,),
        ).fetchall()
        transition_counts: Counter = Counter()
        state_durations: dict[str, list] = {}
        for (payload_str,) in state_transitions:
            p = json.loads(payload_str)
            key = f"{p.get('from')}->{p.get('to')}"
            transition_counts[key] += 1
            to_state = p.get("to", "")
            if to_state not in state_durations:
                state_durations[to_state] = []
            state_durations[to_state].append(p.get("duration_s", 0))

        avg_state_durations = {
            state: round(sum(durs) / len(durs), 1)
            for state, durs in state_durations.items()
            if durs
        }

        case_types = conn.execute(
            "SELECT payload FROM events WHERE event_type='case_created' AND ts > ?",
            (cutoff,),
        ).fetchall()
        matter_counts: Counter = Counter()
        channel_counts: Counter = Counter()
        for (payload_str,) in case_types:
            p = json.loads(payload_str)
            matter_counts[p.get("matter_type", "unknown")] += 1
            channel_counts[p.get("source_channel", "unknown")] += 1

        return {
            "period_hours": period_hours,
            "total_events": total_events,
            "generated_at": _now_iso(),
            "tool_stats": tool_stats,
            "approval_summary": approval_summary,
            "top_transitions": dict(transition_counts.most_common(10)),
            "avg_state_durations": avg_state_durations,
            "matter_type_counts": dict(matter_counts),
            "source_channel_counts": dict(channel_counts),
            "db_size_mb": round(os.path.getsize(self.db_path) / (1024 * 1024), 2),
        }

    def get_recent_events(self, limit: int = 50, event_type: str | None = None) -> list[dict]:
        conn = self._get_conn()
        if event_type:
            rows = conn.execute(
                "SELECT ts, event_type, category, payload FROM events WHERE event_type=? ORDER BY ts DESC LIMIT ?",
                (event_type, limit),
            ).fetchall()
        else:
            rows = conn.execute(
                "SELECT ts, event_type, category, payload FROM events ORDER BY ts DESC LIMIT ?",
                (limit,),
            ).fetchall()
        result = []
        for ts, etype, cat, payload_str in rows:
            result.append({
                "timestamp": datetime.fromtimestamp(ts, UTC).isoformat(),
                "event_type": etype,
                "category": cat,
                "payload": json.loads(payload_str),
            })
        return result

    def get_status(self) -> dict:
        return {
            "tracking_active": True,
            "session_id": str(self._session_id),
            "db_path": self.db_path,
            "db_size_mb": round(os.path.getsize(self.db_path) / (1024 * 1024), 2),
            "data_budget_mb": self.DATA_BUDGET_MB,
            "version": "v1",
        }
