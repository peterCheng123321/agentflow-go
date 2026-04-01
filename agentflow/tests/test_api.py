"""
API endpoint tests using FastAPI TestClient.

Tests all server.py endpoints including:
- Case CRUD operations
- State transitions
- HITL approval/rejection
- Draft editing
- Note management
- Document upload
- WeChat endpoints
- Status and system endpoints
"""

import asyncio
import io
import json
import os
import sys
import tempfile
import unittest
from unittest.mock import AsyncMock, patch

import pytest
from fastapi.testclient import TestClient


class FakeAPILLM:
    def generate_structured_json(self, prompt, context="", profile="small", fallback=None):
        if "evidence_category" in prompt:
            return {
                "parties": ["Test Co"],
                "document_date": "2024-01-01",
                "case_type": "Commercial Lease",
                "key_clauses": ["rent"],
                "urgent_notes": "",
                "evidence_category": "Contract",
            }
        objective = "draft" if "Objective: draft" in prompt else "evaluation"
        return {
            "objective": objective,
            "triage_summary": "Plan targeted retrieval first.",
            "rag_queries": ["rent abatement landlord work", "lease remedies"],
            "tool_calls": [{"tool_name": "search_rag", "args": {"query": "rent abatement landlord work"}, "reason": "Retrieve support."}],
            "final_instruction": "Use the retrieved snippets.",
        }

    def generate(self, prompt, context=""):
        return f"API SYNTHESIS {prompt[:40]}"


def get_client():
    """Create a test client with fresh machine state."""
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    if root not in sys.path:
        sys.path.insert(0, root)
    from server import app

    return TestClient(app)


class TestAPIHealth(unittest.TestCase):
    def setUp(self):
        self.client = get_client()

    def test_root_serves_html(self):
        response = self.client.get("/")
        self.assertEqual(response.status_code, 200)
        self.assertIn("text/html", response.headers["content-type"])

    def test_status_endpoint(self):
        response = self.client.get("/status")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("version", data)
        self.assertIn("active_cases", data)
        self.assertIn("rag_ready", data)

    def test_docs_endpoint(self):
        response = self.client.get("/docs")
        self.assertEqual(response.status_code, 200)

    def test_openapi_endpoint(self):
        response = self.client.get("/openapi.json")
        self.assertEqual(response.status_code, 200)
        schema = response.json()
        self.assertIn("paths", schema)


class TestAICaseOrchestration(unittest.TestCase):
    def setUp(self):
        self.client = get_client()
        from server import agent_machine
        self.machine = agent_machine
        self.original_llm = agent_machine.llm
        self.machine.llm = FakeAPILLM()
        self.machine.rag.ingest_file("./data/mock_cases/mock_commercial_lease_case_2018_sec_source.txt")
        resp = self.client.post("/cases", json={
            "client_name": "AI API Test",
            "matter_type": "Commercial Lease Dispute",
            "initial_msg": "Need help with rent abatement and landlord work."
        })
        self.case_id = resp.json()["case"]["case_id"]

    def tearDown(self):
        self.machine.llm = self.original_llm

    def test_ai_orchestrate_endpoint(self):
        response = self.client.post(f"/cases/{self.case_id}/ai-orchestrate", json={"objective": "evaluation"})
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "completed")
        self.assertEqual(data["orchestration"]["objective"], "evaluation")
        self.assertGreaterEqual(len(data["orchestration"]["tool_runs"]), 1)

    def test_ai_orchestrate_rejects_invalid_objective(self):
        response = self.client.post(f"/cases/{self.case_id}/ai-orchestrate", json={"objective": "invalid"})
        self.assertEqual(response.status_code, 400)

    def test_case_graph_execute_and_status_endpoints(self):
        execute = self.client.post(f"/cases/{self.case_id}/execute")
        self.assertEqual(execute.status_code, 200)
        execute_data = execute.json()
        self.assertEqual(execute_data["graph"]["engine"], "v2_langgraph")
        self.assertEqual(execute_data["graph"]["current_node"], "INITIAL_CONTACT")
        self.assertGreater(len(execute_data["graph"]["node_history"]), 0)

        graph = self.client.get(f"/cases/{self.case_id}/graph")
        self.assertEqual(graph.status_code, 200)
        graph_data = graph.json()
        self.assertTrue(graph_data["graph"]["checkpoint_status"]["available"])

    def test_approval_response_includes_graph_state(self):
        self.client.post(f"/cases/{self.case_id}/execute")
        self.client.post(f"/cases/{self.case_id}/execute")
        self.client.post(f"/cases/{self.case_id}/execute")

        response = self.client.post("/approve", json={
            "case_id": self.case_id,
            "state": "CASE_EVALUATION",
            "approved": True,
            "reason": None,
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("graph", data)

    def test_document_helper_endpoints(self):
        self.machine.rag.ingest_file("./data/docs/chinese_mock_rental_case.txt")

        summary = self.client.get("/documents/chinese_mock_rental_case.txt/summary")
        self.assertEqual(summary.status_code, 200)
        self.assertTrue(summary.json()["summary_points"])

        inspect_response = self.client.get("/documents/chinese_mock_rental_case.txt/inspect?start_line=1&window=5")
        self.assertEqual(inspect_response.status_code, 200)
        self.assertGreaterEqual(len(inspect_response.json()["lines"]), 1)

        grep = self.client.post("/documents/chinese_mock_rental_case.txt/grep", json={"pattern": "租金", "max_results": 5, "case_sensitive": False})
        self.assertEqual(grep.status_code, 200)
        self.assertGreaterEqual(grep.json()["count"], 1)


class TestAPICases(unittest.TestCase):
    def setUp(self):
        self.client = get_client()

    def test_list_cases(self):
        response = self.client.get("/cases")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("cases", data)
        self.assertIn("active_case_id", data)

    def test_create_case(self):
        response = self.client.post("/cases", json={
            "client_name": "API Test Client",
            "matter_type": "Civil Litigation",
            "source_channel": "CRM",
            "initial_msg": "Test message"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "created")
        self.assertIn("case", data)
        self.assertEqual(data["case"]["client_name"], "API Test Client")

    def test_create_case_default_values(self):
        response = self.client.post("/cases", json={
            "client_name": "Default Test"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["case"]["matter_type"], "Commercial Lease Dispute")
        self.assertEqual(data["case"]["source_channel"], "CRM")

    def test_get_case(self):
        # Create a case first
        create_resp = self.client.post("/cases", json={"client_name": "GetTest"})
        case_id = create_resp.json()["case"]["case_id"]

        response = self.client.get(f"/cases/{case_id}")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["case"]["client_name"], "GetTest")

    def test_get_nonexistent_case_returns_404(self):
        response = self.client.get("/cases/NONEXISTENT")
        self.assertEqual(response.status_code, 404)

    def test_update_case(self):
        create_resp = self.client.post("/cases", json={"client_name": "UpdateTest"})
        case_id = create_resp.json()["case"]["case_id"]

        response = self.client.put(f"/cases/{case_id}", json={
            "client_name": "Updated Name",
            "priority": "Low"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "updated")

    def test_update_nonexistent_case_returns_404(self):
        response = self.client.put("/cases/NONEXISTENT", json={"client_name": "X"})
        self.assertEqual(response.status_code, 404)

    def test_update_empty_body_returns_400(self):
        create_resp = self.client.post("/cases", json={"client_name": "EmptyUpdateTest"})
        case_id = create_resp.json()["case"]["case_id"]
        response = self.client.put(f"/cases/{case_id}", json={})
        self.assertEqual(response.status_code, 400)

    def test_delete_case(self):
        create_resp = self.client.post("/cases", json={"client_name": "DeleteTest"})
        case_id = create_resp.json()["case"]["case_id"]

        response = self.client.delete(f"/cases/{case_id}")
        self.assertEqual(response.status_code, 200)

        # Verify it's gone
        get_resp = self.client.get(f"/cases/{case_id}")
        self.assertEqual(get_resp.status_code, 404)

    def test_delete_nonexistent_case_returns_404(self):
        response = self.client.delete("/cases/NONEXISTENT")
        self.assertEqual(response.status_code, 404)

    def test_select_case(self):
        create_resp = self.client.post("/cases", json={"client_name": "SelectTest"})
        case_id = create_resp.json()["case"]["case_id"]

        response = self.client.post("/cases/select", json={"case_id": case_id})
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "selected")

    def test_select_nonexistent_case_returns_404(self):
        response = self.client.post("/cases/select", json={"case_id": "NONEXISTENT"})
        self.assertEqual(response.status_code, 404)


class TestAPIStateTransitions(unittest.TestCase):
    def setUp(self):
        self.client = get_client()
        # Create a fresh case for each test
        resp = self.client.post("/cases", json={"client_name": "StateTest"})
        self.case_id = resp.json()["case"]["case_id"]

    def test_advance_state(self):
        # Advance with no body (empty content) - should advance to next state
        response = self.client.post(f"/cases/{self.case_id}/advance", content=b"")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "advanced")
        self.assertEqual(data["case"]["state"], "INITIAL_CONTACT")

    def test_advance_multiple_states(self):
        self.client.post(f"/cases/{self.case_id}/advance", content=b"")
        self.client.post(f"/cases/{self.case_id}/advance", content=b"")
        resp = self.client.get(f"/cases/{self.case_id}")
        self.assertEqual(resp.json()["case"]["state"], "CASE_EVALUATION")

    def test_rewind_state(self):
        # Advance twice then rewind
        self.client.post(f"/cases/{self.case_id}/advance", content=b"")
        self.client.post(f"/cases/{self.case_id}/advance", content=b"")

        response = self.client.post(f"/cases/{self.case_id}/rewind", json={
            "target_state": "CLIENT_CAPTURE"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "rewound")

    def test_rewind_invalid_state_returns_400(self):
        response = self.client.post(f"/cases/{self.case_id}/rewind", json={
            "target_state": "INVALID"
        })
        self.assertEqual(response.status_code, 400)

    def test_rewind_nonexistent_case_returns_404(self):
        response = self.client.post("/cases/NONEXISTENT/rewind", json={
            "target_state": "CLIENT_CAPTURE"
        })
        self.assertEqual(response.status_code, 404)

    def test_advance_nonexistent_case_returns_404(self):
        response = self.client.post("/cases/NONEXISTENT/advance", content=b"")
        self.assertEqual(response.status_code, 404)


class TestAPIHITL(unittest.TestCase):
    def setUp(self):
        self.client = get_client()
        resp = self.client.post("/cases", json={"client_name": "HITLTest"})
        self.case_id = resp.json()["case"]["case_id"]

    def test_approve_case_evaluation(self):
        response = self.client.post("/approve", json={
            "case_id": self.case_id,
            "state": "CASE_EVALUATION",
            "approved": True,
            "reason": None
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "Updated")
        self.assertTrue(data["approved"])

    def test_reject_case_evaluation(self):
        response = self.client.post("/approve", json={
            "case_id": self.case_id,
            "state": "CASE_EVALUATION",
            "approved": False,
            "reason": "Missing key evidence"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertFalse(data["approved"])

    def test_approve_nonexistent_case_returns_404(self):
        response = self.client.post("/approve", json={
            "case_id": "NONEXISTENT",
            "state": "CASE_EVALUATION",
            "approved": True
        })
        self.assertEqual(response.status_code, 404)

    def test_all_hitl_states(self):
        """Test that all 3 HITL gates can be approved."""
        hitl_states = ["CASE_EVALUATION", "DOCUMENT_GENERATION", "FINAL_PDF_SEND"]
        for state in hitl_states:
            response = self.client.post("/approve", json={
                "case_id": self.case_id,
                "state": state,
                "approved": True
            })
            self.assertEqual(response.status_code, 200, f"Failed to approve {state}")


class TestAPIDraft(unittest.TestCase):
    def setUp(self):
        self.client = get_client()
        resp = self.client.post("/cases", json={"client_name": "DraftTest"})
        self.case_id = resp.json()["case"]["case_id"]

    def test_edit_draft(self):
        response = self.client.put(f"/cases/{self.case_id}/draft", json={
            "draft_text": "This is a legal draft document."
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "updated")

    def test_edit_draft_long_content(self):
        long_text = "Legal content. " * 1000
        response = self.client.put(f"/cases/{self.case_id}/draft", json={
            "draft_text": long_text
        })
        self.assertEqual(response.status_code, 200)

    def test_edit_draft_nonexistent_case_returns_404(self):
        response = self.client.put("/cases/NONEXISTENT/draft", json={
            "draft_text": "Test"
        })
        self.assertEqual(response.status_code, 404)


class TestAPINotes(unittest.TestCase):
    def setUp(self):
        self.client = get_client()
        resp = self.client.post("/cases", json={"client_name": "NoteTest"})
        self.case_id = resp.json()["case"]["case_id"]

    def test_add_note(self):
        response = self.client.post(f"/cases/{self.case_id}/notes", json={
            "text": "This is a test note"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "added")

    def test_add_multiple_notes(self):
        for i in range(5):
            self.client.post(f"/cases/{self.case_id}/notes", json={
                "text": f"Note {i}"
            })

        resp = self.client.get(f"/cases/{self.case_id}")
        notes = resp.json()["case"]["notes"]
        self.assertEqual(len(notes), 5)

    def test_edit_note(self):
        self.client.post(f"/cases/{self.case_id}/notes", json={"text": "Original"})
        response = self.client.put(f"/cases/{self.case_id}/notes/0", json={
            "text": "Edited"
        })
        self.assertEqual(response.status_code, 200)

    def test_edit_note_invalid_index_returns_400(self):
        response = self.client.put(f"/cases/{self.case_id}/notes/999", json={
            "text": "Fail"
        })
        self.assertEqual(response.status_code, 400)

    def test_delete_note(self):
        self.client.post(f"/cases/{self.case_id}/notes", json={"text": "To delete"})
        response = self.client.delete(f"/cases/{self.case_id}/notes/0")
        self.assertEqual(response.status_code, 200)

    def test_delete_note_invalid_index_returns_400(self):
        response = self.client.delete(f"/cases/{self.case_id}/notes/999")
        self.assertEqual(response.status_code, 400)

    def test_add_note_nonexistent_case_returns_404(self):
        response = self.client.post("/cases/NONEXISTENT/notes", json={"text": "Fail"})
        self.assertEqual(response.status_code, 404)


class TestAPIDocumentUpload(unittest.TestCase):
    _ocr_sample = (
        "This is a legal document about rent abatement between Landlord LLC and Tenant Inc. "
        "第一条 租金条款 — effective 2024-01-15."
    )

    def setUp(self):
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        if root not in sys.path:
            sys.path.insert(0, root)
        import ocr_engine as ocr_mod

        patcher = patch.object(
            ocr_mod.ocr_engine,
            "scan_file",
            new=AsyncMock(return_value=self._ocr_sample),
        )
        patcher.start()
        self.addCleanup(patcher.stop)
        self.client = get_client()
        resp = self.client.post("/cases", json={"client_name": "UploadTest"})
        self.case_id = resp.json()["case"]["case_id"]

    def test_upload_txt_file(self):
        content = "This is a legal document about rent abatement. 第一条 租金条款".encode("utf-8")
        response = self.client.post(
            f"/upload?case_id={self.case_id}",
            files={"file": ("test.txt", io.BytesIO(content), "text/plain")},
            data={"category": "__auto__"},
        )
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertTrue(data["ingested"])

    def test_upload_multiple_files(self):
        """Test uploading multiple files to a case."""
        for i in range(3):
            content = f"Document {i} about lease terms 第{i}条".encode("utf-8")
            response = self.client.post(
                f"/upload?case_id={self.case_id}",
                files={"file": (f"doc{i}.txt", io.BytesIO(content), "text/plain")},
                data={"category": "Contract"},
            )
            self.assertEqual(response.status_code, 200)

        # Verify case has uploaded documents
        resp = self.client.get(f"/cases/{self.case_id}")
        docs = resp.json()["case"]["uploaded_documents"]
        self.assertEqual(len(docs), 3)

    def test_upload_batch_endpoint(self):
        boundaries = []
        for i in range(2):
            content = f"Batch doc {i} lease covenant text 第{i}条".encode("utf-8")
            boundaries.append(("files", (f"batch{i}.txt", io.BytesIO(content), "text/plain")))
        response = self.client.post(
            f"/upload/batch?case_id={self.case_id}",
            files=boundaries,
            data={"category": "__auto__"},
        )
        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["count"], 2)
        self.assertEqual(body["ingested_ok"], 2)
        self.assertEqual(len(body["results"]), 2)

    def test_upload_nonexistent_case_returns_404(self):
        content = b"test"
        response = self.client.post(
            "/upload?case_id=NONEXISTENT",
            files={"file": ("test.txt", io.BytesIO(content), "text/plain")}
        )
        self.assertEqual(response.status_code, 404)


class TestAPIDemo(unittest.TestCase):
    def setUp(self):
        self.client = get_client()

    def test_load_mock_case(self):
        response = self.client.post("/demo/load-mock-case")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "loaded")
        self.assertIn("case", data)
        self.assertIn("PROS, INC", data["case"]["client_name"])


class TestAPIWeChat(unittest.TestCase):
    def setUp(self):
        self.client = get_client()

    def test_wechat_status(self):
        response = self.client.get("/wechat/status")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("status", data)
        self.assertIn("mode", data)

    def test_wechat_connect(self):
        response = self.client.post("/wechat/connect")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("connected", data)

    def test_wechat_contacts(self):
        response = self.client.get("/wechat/contacts")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("contacts", data)

    def test_wechat_send(self):
        response = self.client.post("/wechat/send", json={
            "contact_name": "Test User",
            "text": "Hello from test"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("sent", data)

    def test_wechat_group(self):
        response = self.client.post("/wechat/group", json={
            "group_name": "Test Group",
            "members": ["Alice", "Bob"]
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("created", data)

    def test_wechat_inbound_message(self):
        response = self.client.post("/wechat/inbound", json={
            "contact_name": "Incoming User",
            "text": "I need legal help",
            "metadata": {"wechat_id": "test-001"}
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "received")


class TestAPISystem(unittest.TestCase):
    def setUp(self):
        self.client = get_client()

    def test_v1_status(self):
        response = self.client.get("/v1/status")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("version", data)
        self.assertIn("device", data)

    def test_v1_device(self):
        response = self.client.get("/v1/device")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("device", data)

    def test_v1_tracking_summary(self):
        response = self.client.get("/v1/tracking/summary?hours=24")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("total_events", data)

    def test_v1_tracking_events(self):
        response = self.client.get("/v1/tracking/events?limit=10")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("events", data)

    def test_v1_auto_setup(self):
        response = self.client.post("/v1/auto-setup")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("device", data)

    def test_v1_tools_mode_all(self):
        response = self.client.post("/v1/tools/mode-all?mode=auto")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["mode"], "auto")

    def test_v1_tools_mode_all_invalid_mode(self):
        response = self.client.post("/v1/tools/mode-all?mode=invalid")
        self.assertEqual(response.status_code, 400)

    def test_v1_set_tool_mode(self):
        response = self.client.post("/v1/tools/mode", json={
            "tool_name": "send_wechat",
            "mode": "auto"
        })
        self.assertEqual(response.status_code, 200)

    def test_v1_set_tool_mode_invalid(self):
        response = self.client.post("/v1/tools/mode", json={
            "tool_name": "send_wechat",
            "mode": "invalid_mode"
        })
        self.assertEqual(response.status_code, 400)

    def test_rag_signal(self):
        # Create a case first
        create_resp = self.client.post("/cases", json={"client_name": "RAGTest"})
        case_id = create_resp.json()["case"]["case_id"]

        response = self.client.post("/rag/signal", json={
            "case_id": case_id,
            "note": "Document received via WeChat"
        })
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertEqual(data["status"], "recorded")

    def test_rag_signal_nonexistent_case(self):
        response = self.client.post("/rag/signal", json={
            "case_id": "NONEXISTENT",
            "note": "test"
        })
        self.assertEqual(response.status_code, 404)

    def test_setup_endpoint(self):
        response = self.client.post("/setup")
        self.assertEqual(response.status_code, 200)
        data = response.json()
        self.assertIn("setup_ran", data)


if __name__ == "__main__":
    unittest.main()
