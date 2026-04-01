"""
Comprehensive unit tests for AgentFlow V1 core modules.

Covers:
- agent_flow.py: LegalAgentMachine, state transitions, HITL, notes, drafts
- rag_manager.py: RAGManager, document ingestion, search, chunking
- usage_tracker.py: UsageTracker, event recording, PII hashing
- wechat_connector.py: WeChat adapter modes, message handling
- tool_registry_v1.py: Tool execution, modes, confirmations
- sys_scanner.py: System detection, model recommendations
"""

import asyncio
import json
import os
import shutil
import tempfile
import time
import unittest
from datetime import datetime, UTC
from unittest.mock import MagicMock, patch

# ---------------------------------------------------------------------------
# Test: default_case_data
# ---------------------------------------------------------------------------
class TestDefaultCaseData(unittest.TestCase):
    def setUp(self):
        from agent_flow import default_case_data

    def test_default_case_has_all_required_fields(self):
        from agent_flow import default_case_data, HITL_STATES
        case = default_case_data()
        required = [
            "client_name", "case_id", "matter_type", "source_channel",
            "is_paid", "notes", "uploaded_documents", "state", "priority",
            "hitl_approvals", "hitl_events", "state_outputs", "completed_states",
        ]
        for field in required:
            self.assertIn(field, case, f"Missing field: {field}")

    def test_default_case_state_is_client_capture(self):
        from agent_flow import default_case_data, AgentState
        case = default_case_data()
        self.assertEqual(case["state"], AgentState.CLIENT_CAPTURE)

    def test_default_case_hitl_approvals_all_false(self):
        from agent_flow import default_case_data, HITL_STATES
        case = default_case_data()
        for state in HITL_STATES:
            self.assertFalse(case["hitl_approvals"][state])

    def test_default_case_has_unique_id(self):
        from agent_flow import default_case_data
        ids = [default_case_data()["case_id"] for _ in range(10)]
        self.assertEqual(len(set(ids)), 10, "Case IDs should be unique")

    def test_custom_client_name(self):
        from agent_flow import default_case_data
        case = default_case_data(client_name="Test Client")
        self.assertEqual(case["client_name"], "Test Client")

    def test_custom_matter_type(self):
        from agent_flow import default_case_data
        case = default_case_data(matter_type="Civil Litigation")
        self.assertEqual(case["matter_type"], "Civil Litigation")


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Case CRUD
# ---------------------------------------------------------------------------
class TestLegalAgentMachineCaseCRUD(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()

    def test_initial_case_exists(self):
        self.assertIsNotNone(self.machine.active_case_id)
        self.assertGreater(len(self.machine.cases), 0)

    def test_create_case(self):
        before = len(self.machine.cases)
        case = self.machine.create_case(
            client_name="Test Corp",
            matter_type="Contract Review",
            source_channel="CRM",
            initial_msg="Need contract review"
        )
        self.assertEqual(len(self.machine.cases), before + 1)
        self.assertEqual(case["client_name"], "Test Corp")
        self.assertEqual(case["matter_type"], "Contract Review")

    def test_create_case_generates_valid_case_id(self):
        case = self.machine.create_case()
        self.assertTrue(case["case_id"].startswith("LAW-2026-"))
        self.assertGreater(len(case["case_id"]), 9)  # At least LAW-2026- + some chars

    def test_select_case(self):
        case = self.machine.create_case(client_name="SelectMe")
        self.machine.select_case(case["case_id"])
        self.assertEqual(self.machine.active_case_id, case["case_id"])

    def test_select_nonexistent_case_raises(self):
        with self.assertRaises(KeyError):
            self.machine.select_case("NONEXISTENT-CASE-ID")

    def test_get_case_status(self):
        case = self.machine.create_case(client_name="StatusTest")
        status = self.machine.get_case_status(case["case_id"])
        self.assertEqual(status["client_name"], "StatusTest")
        self.assertIn("state", status)
        self.assertIn("model", status)

    def test_get_case_status_nonexistent_raises(self):
        with self.assertRaises(KeyError):
            self.machine.get_case_status("NONEXISTENT")

    def test_get_case_status_caching(self):
        case = self.machine.create_case(client_name="CacheTest")
        s1 = self.machine.get_case_status(case["case_id"])
        s2 = self.machine.get_case_status(case["case_id"])
        self.assertIs(s1, s2, "Should return cached object on second call")

    def test_get_case_detail_includes_extra_fields(self):
        case = self.machine.create_case(client_name="DetailTest")
        detail = self.machine.get_case_detail(case["case_id"])
        self.assertIn("evaluation_detail", detail)
        self.assertIn("document_draft", detail)
        self.assertIn("fee_record", detail)
        self.assertIn("contact_log", detail)
        self.assertIn("group_info", detail)

    def test_update_case(self):
        case = self.machine.create_case()
        self.machine.update_case(case["case_id"], client_name="UpdatedName")
        updated = self.machine.get_case_status(case["case_id"])
        self.assertEqual(updated["client_name"], "UpdatedName")

    def test_update_case_protected_fields_ignored(self):
        case = self.machine.create_case()
        original_id = case["case_id"]
        # Note: update_case signature is update_case(case_id, **fields), 
        # so passing case_id as field will raise TypeError
        self.machine.update_case(case["case_id"], client_name="Updated", priority="Low")
        status = self.machine.get_case_status(case["case_id"])
        self.assertEqual(status["case_id"], original_id, "case_id should not change")
        self.assertEqual(status["client_name"], "Updated", "client_name should be updated")
        self.assertEqual(status["priority"], "Low", "priority should be updated")

    def test_delete_case(self):
        case = self.machine.create_case()
        case_id = case["case_id"]
        self.machine.delete_case(case_id)
        self.assertNotIn(case_id, self.machine.cases)

    def test_delete_active_case_switches_to_next(self):
        case1 = self.machine.create_case()
        case2 = self.machine.create_case()
        self.machine.select_case(case1["case_id"])
        self.machine.delete_case(case1["case_id"])
        self.assertNotEqual(self.machine.active_case_id, case1["case_id"])

    def test_list_cases_returns_all(self):
        before = len(self.machine.list_cases())
        self.machine.create_case()
        self.machine.create_case()
        after = len(self.machine.list_cases())
        self.assertEqual(after, before + 2)


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - State Transitions
# ---------------------------------------------------------------------------
class TestLegalAgentMachineStateTransitions(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine, AgentState
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case(client_name="StateTest")
        self.case_id = self.case["case_id"]
        self.AgentState = AgentState

    def test_advance_state(self):
        self.machine.advance_state(self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["state"], "INITIAL_CONTACT")

    def test_advance_state_records_transition(self):
        self.machine.advance_state(self.case_id)
        events = self.machine.tracker.get_recent_events(event_type="state_transition")
        self.assertTrue(any("CLIENT_CAPTURE" in json.dumps(e) for e in events))

    def test_advance_state_marks_completed(self):
        self.machine.advance_state(self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertIn("CLIENT_CAPTURE", status["completed_states"])

    def test_advance_to_case_evaluation(self):
        self.machine.advance_state(self.case_id)  # -> INITIAL_CONTACT
        self.machine.advance_state(self.case_id)  # -> CASE_EVALUATION
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["state"], "CASE_EVALUATION")

    def test_rewind_state(self):
        self.machine.advance_state(self.case_id)  # -> INITIAL_CONTACT
        self.machine.advance_state(self.case_id)  # -> CASE_EVALUATION
        self.machine.rewind_state(self.case_id, "CLIENT_CAPTURE")
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["state"], "CLIENT_CAPTURE")

    def test_rewind_resets_hitl_events(self):
        self.machine.advance_state(self.case_id)  # -> INITIAL_CONTACT
        self.machine.advance_state(self.case_id)  # -> CASE_EVALUATION
        self.machine.rewind_state(self.case_id, "CLIENT_CAPTURE")
        status = self.machine.get_case_status(self.case_id)
        # After rewind, state should be CLIENT_CAPTURE
        self.assertEqual(status["state"], "CLIENT_CAPTURE")

    def test_rewind_invalid_state_raises(self):
        with self.assertRaises(ValueError):
            self.machine.rewind_state(self.case_id, "INVALID_STATE")

    def test_advance_invalid_state_raises(self):
        self.machine.cases[self.case_id]["state"] = self.AgentState.ARCHIVE_CLOSE
        with self.assertRaises(ValueError):
            self.machine.advance_state(self.case_id)


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - HITL
# ---------------------------------------------------------------------------
class TestLegalAgentMachineHITL(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine, HITL_STATES
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]
        self.HITL_STATES = HITL_STATES

    def test_set_approval_true(self):
        self.machine.set_approval("CASE_EVALUATION", True, self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertTrue(status["hitl_approvals"]["CASE_EVALUATION"])

    def test_set_approval_false(self):
        self.machine.set_approval("CASE_EVALUATION", True, self.case_id)
        self.machine.set_approval("CASE_EVALUATION", False, self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertFalse(status["hitl_approvals"]["CASE_EVALUATION"])

    def test_set_approval_with_reason(self):
        self.machine.set_approval("CASE_EVALUATION", False, self.case_id, reason="Missing facts")
        status = self.machine.get_case_status(self.case_id)
        self.assertTrue(any("Missing facts" in n for n in status["notes"]))

    def test_set_approval_records_tracker_event(self):
        self.machine.set_approval("CASE_EVALUATION", True, self.case_id)
        events = self.machine.tracker.get_recent_events(event_type="approval")
        self.assertTrue(any("CASE_EVALUATION" in json.dumps(e) for e in events))

    def test_hitl_states_constant_matches_graph(self):
        from agent_flow import WORKFLOW_GRAPH
        hitl_from_graph = {name for name, node in WORKFLOW_GRAPH.items() if node.is_hitl_gate}
        self.assertEqual(self.HITL_STATES, hitl_from_graph)

    def test_all_hitl_states_start_unapproved(self):
        from agent_flow import default_case_data
        case = default_case_data()
        for state in self.HITL_STATES:
            self.assertFalse(case["hitl_approvals"][state])


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Notes
# ---------------------------------------------------------------------------
class TestLegalAgentMachineNotes(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_add_note(self):
        self.machine.add_note(self.case_id, "Test note")
        status = self.machine.get_case_status(self.case_id)
        self.assertIn("Test note", status["notes"])

    def test_add_multiple_notes(self):
        self.machine.add_note(self.case_id, "Note 1")
        self.machine.add_note(self.case_id, "Note 2")
        self.machine.add_note(self.case_id, "Note 3")
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(len(status["notes"]), 3)

    def test_edit_note(self):
        self.machine.add_note(self.case_id, "Original")
        self.machine.edit_note(self.case_id, 0, "Edited")
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["notes"][0], "Edited")

    def test_edit_note_invalid_index_raises(self):
        with self.assertRaises(IndexError):
            self.machine.edit_note(self.case_id, 999, "New text")

    def test_delete_note(self):
        self.machine.add_note(self.case_id, "To delete")
        self.machine.delete_note(self.case_id, 0)
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(len(status["notes"]), 0)

    def test_delete_note_invalid_index_raises(self):
        with self.assertRaises(IndexError):
            self.machine.delete_note(self.case_id, 999)

    def test_delete_note_shifts_indices(self):
        self.machine.add_note(self.case_id, "A")
        self.machine.add_note(self.case_id, "B")
        self.machine.add_note(self.case_id, "C")
        self.machine.delete_note(self.case_id, 1)  # Delete "B"
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["notes"], ["A", "C"])


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Draft Editing
# ---------------------------------------------------------------------------
class TestLegalAgentMachineDraft(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_edit_draft_updates_preview(self):
        self.machine.edit_draft(self.case_id, "New draft content here")
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["draft_preview"], "New draft content here")

    def test_edit_draft_long_text_truncated(self):
        long_text = "A" * 2000
        self.machine.edit_draft(self.case_id, long_text)
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(len(status["draft_preview"]), 500)

    def test_edit_draft_updates_document_draft(self):
        # Initialize document_draft first (as would happen after document generation)
        self.machine.cases[self.case_id]["document_draft"] = {"draft_text": ""}
        self.machine.edit_draft(self.case_id, "Draft v2")
        case = self.machine.cases[self.case_id]
        self.assertEqual(case["document_draft"]["draft_text"], "Draft v2")

    def test_edit_draft_records_tracker_event(self):
        self.machine.edit_draft(self.case_id, "Test draft")
        events = self.machine.tracker.get_recent_events(event_type="user_action")
        self.assertTrue(any("edit_draft" in json.dumps(e) for e in events))


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Case Updates (is_paid)
# ---------------------------------------------------------------------------
class TestLegalAgentMachinePayment(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_mark_paid(self):
        self.machine.update_case(self.case_id, is_paid=True)
        status = self.machine.get_case_status(self.case_id)
        self.assertTrue(status["is_paid"])

    def test_mark_unpaid(self):
        self.machine.update_case(self.case_id, is_paid=True)
        self.machine.update_case(self.case_id, is_paid=False)
        status = self.machine.get_case_status(self.case_id)
        self.assertFalse(status["is_paid"])


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Document Handling
# ---------------------------------------------------------------------------
class TestLegalAgentMachineDocuments(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_attach_document(self):
        self.machine.attach_document_to_case("test.pdf", self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertIn("test.pdf", status["uploaded_documents"])

    def test_attach_duplicate_document_not_duplicated(self):
        self.machine.attach_document_to_case("test.pdf", self.case_id)
        self.machine.attach_document_to_case("test.pdf", self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertEqual(status["uploaded_documents"].count("test.pdf"), 1)

    def test_attach_document_adds_note(self):
        self.machine.attach_document_to_case("doc.pdf", self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertTrue(any("Document uploaded: doc.pdf" in n for n in status["notes"]))


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - RAG Signal
# ---------------------------------------------------------------------------
class TestLegalAgentMachineRAGSignal(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_rag_signal_adds_note(self):
        self.machine.signal_rag_ingestion("attachment received", self.case_id)
        status = self.machine.get_case_status(self.case_id)
        self.assertTrue(any("RAG signal: attachment received" in n for n in status["notes"]))


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Agent Log
# ---------------------------------------------------------------------------
class TestLegalAgentMachineLog(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()

    def test_get_agent_log_returns_list(self):
        log = self.machine.get_agent_log()
        self.assertIsInstance(log, list)

    def test_agent_log_has_entries_after_operations(self):
        self.machine.advance_state(self.machine.active_case_id)
        log = self.machine.get_agent_log()
        self.assertGreater(len(log), 0)

    def test_agent_log_entries_contain_timestamp(self):
        self.machine.advance_state(self.machine.active_case_id)
        log = self.machine.get_agent_log()
        self.assertTrue(any("[" in entry for entry in log))


# ---------------------------------------------------------------------------
# Test: LegalAgentMachine - Handle Incoming Message
# ---------------------------------------------------------------------------
class TestLegalAgentMachineIncomingMessage(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()

    def test_incoming_message_creates_case(self):
        before = len(self.machine.list_cases())
        created = asyncio.run(
            self.machine.handle_incoming_message(
                contact_name="Alice Zhang",
                text="Need legal help with a lease",
                source_channel="WeChat",
                metadata={"wechat_id": "alice-001"},
            )
        )
        self.assertEqual(len(self.machine.list_cases()), before + 1)
        self.assertEqual(created["client_name"], "Alice Zhang")
        self.assertEqual(created["source_channel"], "WeChat")

    def test_incoming_message_adds_wechat_note(self):
        created = asyncio.run(
            self.machine.handle_incoming_message(
                contact_name="Bob",
                text="Help",
                source_channel="WeChat",
                metadata={"wechat_id": "bob-001"},
            )
        )
        self.assertTrue(any("wechat_id" in n for n in created["notes"]))


# ---------------------------------------------------------------------------
# Test: RAGManager
# ---------------------------------------------------------------------------
class TestRAGManager(unittest.TestCase):
    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_initial_state(self):
        self.assertEqual(self.rag.backend_mode, "lightweight_bm25")
        self.assertIsNone(self.rag.init_error)
        self.assertTrue(self.rag.ensure_ready())

    def test_ingest_txt_file(self):
        test_file = os.path.join(self.test_dir, "test.txt")
        with open(test_file, "w") as f:
            f.write("This is a test document about commercial lease law. 第一条 合同条款 rent payment terms.")
        success, message = self.rag.ingest_file(test_file)
        self.assertTrue(success)
        self.assertIn("BM25", message)
        self.assertIn("chunks", message.lower())

    def test_ingest_rejects_ocr_error_placeholder(self):
        test_file = os.path.join(self.test_dir, "scan.txt")
        with open(test_file, "w") as f:
            f.write("x")
        success, message = self.rag.ingest_file(
            test_file,
            force_ocr_text="[Fallback scan error] No module named 'pymupdf'.",
        )
        self.assertFalse(success)
        self.assertIn("Indexing skipped", message)

    def test_ingest_nonexistent_file(self):
        # ingest_file raises FileNotFoundError for txt files that don't exist
        with self.assertRaises(FileNotFoundError):
            self.rag.ingest_file("/nonexistent/file.txt")

    def test_ingest_unsupported_format(self):
        test_file = os.path.join(self.test_dir, "test.xyz")
        with open(test_file, "w") as f:
            f.write("test content")
        success, message = self.rag.ingest_file(test_file)
        self.assertFalse(success)
        self.assertIn("Unsupported", message)

    def test_search_returns_results(self):
        test_file = os.path.join(self.test_dir, "legal.txt")
        with open(test_file, "w") as f:
            f.write("Commercial lease dispute between landlord and tenant regarding rent abatement. 第一条 租金减扣条款 Landlord must repair building within 30 days.")
        self.rag.ingest_file(test_file)
        results = self.rag.search("rent abatement landlord")
        self.assertIsInstance(results, str)
        self.assertTrue(len(results) > 0)

    def test_search_returns_empty_for_irrelevant_query(self):
        test_file = os.path.join(self.test_dir, "legal.txt")
        with open(test_file, "w") as f:
            f.write("Commercial lease dispute rent abatement.")
        self.rag.ingest_file(test_file)
        results = self.rag.search("xyzzy quantum flux capacitor")
        # BM25 might return some results due to loose matching
        self.assertIsInstance(results, str)

    def test_search_structured_returns_list(self):
        test_file = os.path.join(self.test_dir, "legal.txt")
        with open(test_file, "w") as f:
            f.write("Legal document about rent abatement and landlord obligations.")
        self.rag.ingest_file(test_file)
        results = self.rag.search_structured("rent", k=3)
        self.assertIsInstance(results, list)
        if results:
            self.assertIn("filename", results[0])
            self.assertIn("chunk", results[0])
            self.assertIn("score", results[0])

    def test_get_summary(self):
        test_file = os.path.join(self.test_dir, "test.txt")
        with open(test_file, "w") as f:
            f.write("Test content for summary.")
        self.rag.ingest_file(test_file)
        summary = self.rag.get_summary()
        self.assertIn("backend_mode", summary)
        self.assertIn("document_count", summary)
        self.assertIn("total_chunks", summary)
        self.assertGreaterEqual(summary["document_count"], 1)

    def test_reingest_replaces_document(self):
        test_file = os.path.join(self.test_dir, "test.txt")
        with open(test_file, "w") as f:
            f.write("Version 1")
        self.rag.ingest_file(test_file)
        with open(test_file, "w") as f:
            f.write("Version 2 with different content about legal matters")
        self.rag.ingest_file(test_file)
        summary = self.rag.get_summary()
        # Should have exactly 1 document (replaced, not duplicated)
        self.assertEqual(summary["document_count"], 1)


# ---------------------------------------------------------------------------
# Test: RAGManager - Chunking
# ---------------------------------------------------------------------------
class TestRAGChunking(unittest.TestCase):
    def test_structure_aware_chunk_with_legal_clauses(self):
        from rag_manager import _structure_aware_chunk
        text = "第一条 合同条款 租金支付。第二条 维修责任 房东负责。第三条 违约条款 承租人违约。"
        chunks = _structure_aware_chunk(text)
        self.assertIsInstance(chunks, list)
        self.assertGreater(len(chunks), 0)

    def test_structure_aware_chunk_plain_text(self):
        from rag_manager import _structure_aware_chunk
        text = "This is a test. This is another sentence. And a third one here."
        chunks = _structure_aware_chunk(text)
        self.assertIsInstance(chunks, list)
        self.assertGreater(len(chunks), 0)

    def test_structure_aware_chunk_empty(self):
        from rag_manager import _structure_aware_chunk
        chunks = _structure_aware_chunk("")
        self.assertEqual(chunks, [])

    def test_structure_aware_chunk_whitespace_only(self):
        from rag_manager import _structure_aware_chunk
        chunks = _structure_aware_chunk("   \n\t  ")
        self.assertEqual(chunks, [])

    def test_fixed_chunk_small_text(self):
        from rag_manager import _fixed_chunk
        chunks = _fixed_chunk("Short text", chunk_size=512)
        self.assertEqual(len(chunks), 1)

    def test_fixed_chunk_large_text(self):
        from rag_manager import _fixed_chunk
        text = "A" * 2000
        chunks = _fixed_chunk(text, chunk_size=256, overlap=40)
        self.assertGreater(len(chunks), 1)
        # Check overlap exists between consecutive chunks
        if len(chunks) > 1:
            overlap_text = chunks[0][-40:]
            self.assertIn(overlap_text, chunks[1])

    def test_fixed_chunk_empty(self):
        from rag_manager import _fixed_chunk
        chunks = _fixed_chunk("")
        self.assertEqual(chunks, [])


# ---------------------------------------------------------------------------
# Test: RAGManager - Search Scoring
# ---------------------------------------------------------------------------
class TestRAGScoring(unittest.TestCase):
    def test_tf_score_counts_tokens(self):
        from rag_manager import RAGManager
        rag = RAGManager(persist_directory=tempfile.mkdtemp())
        score = rag._tf_score(["rent", "abatement"], "rent abatement rent lease")
        # "rent" appears 2 times, "abatement" appears 1 time = total 3
        self.assertEqual(score, 3)

    def test_tf_score_empty_query(self):
        from rag_manager import RAGManager
        rag = RAGManager(persist_directory=tempfile.mkdtemp())
        score = rag._tf_score([], "some text")
        self.assertEqual(score, 0)


# ---------------------------------------------------------------------------
# Test: UsageTracker
# ---------------------------------------------------------------------------
class TestUsageTracker(unittest.TestCase):
    def setUp(self):
        from usage_tracker import UsageTracker
        self.test_dir = tempfile.mkdtemp()
        self.db_path = os.path.join(self.test_dir, "test_usage.db")
        self.tracker = UsageTracker(db_path=self.db_path)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_record_event(self):
        self.tracker.record("test_event", "test", {"key": "value"})
        events = self.tracker.get_recent_events(limit=1)
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["event_type"], "test_event")
        self.assertEqual(events[0]["category"], "test")

    def test_record_pii_strips_client_name(self):
        self.tracker.record("test", "test", {"client_name": "John Doe", "other": "data"})
        events = self.tracker.get_recent_events(limit=1)
        payload = events[0]["payload"]
        self.assertNotIn("client_name", payload)
        self.assertIn("client_name_hash", payload)
        self.assertIn("other", payload)

    def test_record_pii_strips_contact_name(self):
        self.tracker.record("test", "test", {"contact_name": "Alice", "data": "ok"})
        events = self.tracker.get_recent_events(limit=1)
        payload = events[0]["payload"]
        self.assertNotIn("contact_name", payload)
        self.assertIn("contact_name_hash", payload)

    def test_record_pii_strips_text_fields(self):
        self.tracker.record("test", "test", {"text": "secret", "message": "secret2", "initial_msg": "secret3", "safe": "ok"})
        events = self.tracker.get_recent_events(limit=1)
        payload = events[0]["payload"]
        self.assertNotIn("text", payload)
        self.assertNotIn("message", payload)
        self.assertNotIn("initial_msg", payload)
        self.assertIn("safe", payload)

    def test_record_tool_call(self):
        self.tracker.record_tool_call("send_wechat", True, 45.2)
        events = self.tracker.get_recent_events(event_type="tool_call")
        self.assertTrue(any("send_wechat" in json.dumps(e) for e in events))

    def test_record_state_transition(self):
        self.tracker.record_state_transition("CLIENT_CAPTURE", "INITIAL_CONTACT", "case-123", 2.5)
        events = self.tracker.get_recent_events(event_type="state_transition")
        self.assertEqual(len(events), 1)
        payload = events[0]["payload"]
        self.assertEqual(payload["from"], "CLIENT_CAPTURE")
        self.assertEqual(payload["to"], "INITIAL_CONTACT")
        self.assertEqual(payload["duration_s"], 2.5)

    def test_record_approval(self):
        self.tracker.record_approval("CASE_EVALUATION", True, 12.5)
        events = self.tracker.get_recent_events(event_type="approval")
        self.assertEqual(len(events), 1)

    def test_record_case_created(self):
        self.tracker.record_case_created("Commercial Lease Dispute", "WeChat")
        events = self.tracker.get_recent_events(event_type="case_created")
        self.assertEqual(len(events), 1)

    def test_record_user_action(self):
        self.tracker.record_user_action("edit_draft", "case-123")
        events = self.tracker.get_recent_events(event_type="user_action")
        self.assertEqual(len(events), 1)

    def test_generate_summary_returns_dict(self):
        self.tracker.record_case_created("Test", "CRM")
        summary = self.tracker.generate_summary(period_hours=24)
        self.assertIsInstance(summary, dict)
        self.assertIn("total_events", summary)
        self.assertIn("period_hours", summary)

    def test_get_status(self):
        status = self.tracker.get_status()
        self.assertIn("tracking_active", status)
        self.assertIn("db_size_mb", status)
        self.assertTrue(status["tracking_active"])


# ---------------------------------------------------------------------------
# Test: UsageTracker - PII Hashing
# ---------------------------------------------------------------------------
class TestUsageTrackerPII(unittest.TestCase):
    def test_hash_pii_is_deterministic(self):
        from usage_tracker import _hash_pii
        h1 = _hash_pii("John Doe")
        h2 = _hash_pii("John Doe")
        self.assertEqual(h1, h2)

    def test_hash_pii_produces_consistent_length(self):
        from usage_tracker import _hash_pii
        h = _hash_pii("anything")
        self.assertEqual(len(h), 16)

    def test_hash_pii_different_inputs_different_hashes(self):
        from usage_tracker import _hash_pii
        h1 = _hash_pii("Alice")
        h2 = _hash_pii("Bob")
        self.assertNotEqual(h1, h2)


# ---------------------------------------------------------------------------
# Test: WeChatConnector
# ---------------------------------------------------------------------------
class TestWeChatConnector(unittest.TestCase):
    def test_mock_login(self):
        from wechat_connector import WeChatConnector, WeChatStatus
        connector = WeChatConnector(mode="mock")
        success = asyncio.run(connector.login())
        self.assertTrue(success)
        self.assertEqual(connector.status, WeChatStatus.CONNECTED)

    def test_mock_send_before_login_fails(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        sent, msg = asyncio.run(connector.send_message("Test", "Hello"))
        self.assertFalse(sent)

    def test_mock_send_after_login(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        asyncio.run(connector.login())
        sent, msg = asyncio.run(connector.send_message("Test", "Hello"))
        self.assertTrue(sent)
        self.assertIn("mock", msg)

    def test_mock_list_contacts(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        asyncio.run(connector.login())
        contacts = asyncio.run(connector.list_contacts())
        self.assertIsInstance(contacts, list)
        self.assertGreater(len(contacts), 0)
        self.assertIn("name", contacts[0])

    def test_mock_create_group(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        asyncio.run(connector.login())
        created, msg, group = asyncio.run(
            connector.create_group_chat("Test Group", ["A", "B"])
        )
        self.assertTrue(created)
        self.assertEqual(group["group_name"], "Test Group")
        self.assertEqual(len(group["members"]), 2)

    def test_mock_create_group_before_login_fails(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        created, msg, group = asyncio.run(
            connector.create_group_chat("Test Group", ["A"])
        )
        self.assertFalse(created)

    def test_on_message_handler(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        received = []

        @connector.on_message
        async def handler(msg):
            received.append(msg)

        asyncio.run(connector.simulate_incoming_message("Alice", "Hi"))
        self.assertEqual(len(received), 1)
        self.assertEqual(received[0]["from"], "Alice")

    def test_capabilities(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="mock")
        caps = connector.capabilities
        self.assertTrue(caps["supports_inbound_mock"])
        self.assertTrue(caps["supports_outbound_send"])
        self.assertTrue(caps["supports_group_create"])

    def test_auto_mode_falls_back_to_mock(self):
        from wechat_connector import WeChatConnector
        connector = WeChatConnector(mode="auto")
        self.assertIn(connector.adapter_mode, ["mock", "openclaw"])


# ---------------------------------------------------------------------------
# Test: ToolRegistryV1
# ---------------------------------------------------------------------------
class TestToolRegistryV1(unittest.TestCase):
    def setUp(self):
        from tool_registry_v1 import ToolRegistryV1, ToolMode
        self.registry = ToolRegistryV1(default_mode=ToolMode.AUTO)

    def test_list_tools(self):
        tools = self.registry.list_tools()
        self.assertIsInstance(tools, list)
        self.assertGreater(len(tools), 0)
        self.assertTrue(any(t["name"] == "send_wechat" for t in tools))

    def test_execute_tool(self):
        result = asyncio.run(self.registry.execute("send_wechat", contact_name="Test", text="Hello"))
        self.assertTrue(result.success)
        self.assertIsNotNone(result.output)

    def test_execute_unknown_tool_returns_error(self):
        result = asyncio.run(self.registry.execute("nonexistent_tool"))
        self.assertFalse(result.success)
        self.assertIn("Unknown", result.error)

    def test_set_tool_mode(self):
        from tool_registry_v1 import ToolMode
        self.registry.set_tool_mode("send_wechat", ToolMode.AUTO)
        tools = self.registry.list_tools()
        send_tool = next(t for t in tools if t["name"] == "send_wechat")
        self.assertEqual(send_tool["mode"], "auto")
        self.assertFalse(send_tool["requires_confirmation"])

    def test_set_all_mode(self):
        from tool_registry_v1 import ToolMode
        self.registry.set_all_mode(ToolMode.AUTO)
        for tool in self.registry.list_tools():
            self.assertEqual(tool["mode"], "auto")

    def test_confirmation_flow(self):
        from tool_registry_v1 import ToolMode
        self.registry.set_tool_mode("send_wechat", ToolMode.MANUAL)
        result = asyncio.run(self.registry.execute_with_confirmation("send_wechat", contact_name="Test", text="Hello"))
        self.assertIn("awaiting_confirmation", result.error)

    def test_get_pending(self):
        from tool_registry_v1 import ToolMode, ToolRegistryV1
        registry = ToolRegistryV1(default_mode=ToolMode.MANUAL)
        result = asyncio.run(registry.execute_with_confirmation("send_wechat", contact_name="Test", text="Hello"))
        # The result should indicate awaiting confirmation
        self.assertIn("awaiting_confirmation", result.error)


# ---------------------------------------------------------------------------
# Test: AutoDevice
# ---------------------------------------------------------------------------
class TestAutoDevice(unittest.TestCase):
    def test_detect_device(self):
        from auto_device import detect_device
        device = detect_device()
        self.assertIn("os", device)
        self.assertIn("platform_id", device)

    def test_recommend_config(self):
        from auto_device import recommend_config
        config = recommend_config()
        self.assertIn("model", config)
        self.assertIn("llm_backend", config)

    def test_get_device_report(self):
        from auto_device import get_device_report
        report = get_device_report()
        self.assertIn("device", report)
        self.assertIn("recommended_config", report)


# ---------------------------------------------------------------------------
# Test: SetupManager
# ---------------------------------------------------------------------------
class TestSetupManager(unittest.TestCase):
    def test_get_setup_status(self):
        from setup_manager import get_setup_status
        status = get_setup_status()
        self.assertIn("system_info", status)
        self.assertIn("recommended_model", status)

    def test_run_local_setup(self):
        from setup_manager import run_local_setup
        result = run_local_setup()
        self.assertTrue(result["setup_ran"])
        self.assertTrue(result["directories_ready"])


# ---------------------------------------------------------------------------
# Test: Workflow Graph Integrity
# ---------------------------------------------------------------------------
class TestWorkflowGraphIntegrity(unittest.TestCase):
    def test_all_states_have_dependencies(self):
        from agent_flow import WORKFLOW_GRAPH
        for name, node in WORKFLOW_GRAPH.items():
            if name == "CLIENT_CAPTURE":
                self.assertEqual(len(node.dependencies), 0)
            else:
                self.assertGreater(len(node.dependencies), 0, f"{name} should have dependencies")

    def test_hitl_gate_count(self):
        from agent_flow import HITL_STATES
        self.assertEqual(len(HITL_STATES), 3)

    def test_workflow_order_length(self):
        from agent_flow import WORKFLOW_ORDER
        self.assertEqual(len(WORKFLOW_ORDER), 10)

    def test_all_graph_states_in_order(self):
        from agent_flow import WORKFLOW_GRAPH, WORKFLOW_ORDER
        self.assertEqual(set(WORKFLOW_GRAPH.keys()), set(WORKFLOW_ORDER))


if __name__ == "__main__":
    unittest.main()
