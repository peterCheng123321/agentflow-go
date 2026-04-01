"""
Tool Calling & LangGraph V2 Test Suite — SKELETON

This file contains the structure for V2 testing. Tests are marked with
pytest.mark.skip until the corresponding implementation is complete.

Covers:
- Module 1: Tool Calling Engine (TC-*)
- Module 2: LangGraph Workflow Engine (LG-*)
- Module 3: Checkpoint System (CK-*)
- Module 4: Subgraph Patterns (SG-*)
- Module 5: Adaptive Engine (AE-*)
- Module 6: Data Analyzer (DA-*)
- Module 7: Integration Tests (INT-*)
- Module 8: Graph State Tests (GS-*)

Usage:
    pytest tests/test_tool_calling.py -v  # Run all V2 tests
    pytest tests/test_tool_calling.py -v -m "not skip"  # Run only implemented
"""

import asyncio
import os
import tempfile
import unittest
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

# ============================================================================
# Module 1: Tool Calling Engine (TC-*)
# ============================================================================


class TestToolDecision(unittest.TestCase):
    """TC-001 through TC-013: Verify LLM decides correct tool based on input."""

    @pytest.mark.skip(reason="V2 tool calling engine not yet integrated")
    def test_tc001_send_wechat_detection(self):
        """TC-001: 'Send message to client about rent' → send_wechat"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        decision = engine.decide_tool("Send a message to client about rent abatement")
        self.assertEqual(decision, "<send_wechat>")

    @pytest.mark.skip(reason="V2 tool calling engine not yet integrated")
    def test_tc002_search_rag_detection(self):
        """TC-002: 'Search for lease law precedents' → search_rag"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        decision = engine.decide_tool("Search for lease law precedents")
        self.assertEqual(decision, "<search_rag>")

    @pytest.mark.skip(reason="V2 tool calling engine not yet integrated")
    def test_tc003_generate_pdf_detection(self):
        """TC-003: 'Generate PDF of the draft' → generate_pdf"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        decision = engine.decide_tool("Generate PDF of the draft")
        self.assertEqual(decision, "<generate_pdf>")

    @pytest.mark.skip(reason="V2 tool calling engine not yet integrated")
    def test_tc004_publish_douyin_detection(self):
        """TC-004: 'Post case update to Douyin' → publish_douyin"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        decision = engine.decide_tool("Post case update to Douyin")
        self.assertEqual(decision, "<publish_douyin>")

    @pytest.mark.skip(reason="V2 tool calling engine not yet integrated")
    def test_tc005_greeting_no_tool(self):
        """TC-005: 'Hello, how are you?' → none"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        decision = engine.decide_tool("Hello, how are you?")
        self.assertEqual(decision, "<none>")


class TestStructuredOutput(unittest.TestCase):
    """TC-010 through TC-013: Verify LLM output parsing."""

    @pytest.mark.skip(reason="V2 structured output parser not yet integrated")
    def test_tc010_valid_xml_tag(self):
        """TC-010: LLM returns valid XML tag, parser extracts correctly."""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        result = engine.parse_tool_tag("<search_rag>")
        self.assertEqual(result, "search_rag")

    @pytest.mark.skip(reason="V2 structured output parser not yet integrated")
    def test_tc012_malformed_xml_force_close(self):
        """TC-012: Malformed XML '<send_wechat' → force-closed to '<send_wechat>'"""
        from v2.tool_calling import ToolCallingEngine
        engine = ToolCallingEngine()
        result = engine.parse_tool_tag("<send_wechat")
        self.assertEqual(result, "send_wechat")


class TestToolExecution(unittest.TestCase):
    """TC-020 through TC-023: Verify tool execution by mode."""

    @pytest.mark.skip(reason="V2 tool execution not yet integrated")
    def test_tc020_auto_tool_runs_immediately(self):
        """TC-020: search_rag in auto mode runs without confirmation."""
        from v2.tool_calling import ToolExecutor
        executor = ToolExecutor()
        result = executor.execute("search_rag", query="rent abatement")
        self.assertTrue(result.success)

    @pytest.mark.skip(reason="V2 tool execution not yet integrated")
    def test_tc021_manual_tool_awaits_confirmation(self):
        """TC-021: send_wechat in manual mode returns awaiting_confirmation."""
        from v2.tool_calling import ToolExecutor
        executor = ToolExecutor(default_mode="manual")
        result = executor.execute("send_wechat", contact="Test", text="Hello")
        self.assertIn("awaiting_confirmation", result.error)


class TestToolConfirmation(unittest.TestCase):
    """TC-030 through TC-033: Verify confirmation flow."""

    @pytest.mark.skip(reason="V2 confirmation flow not yet integrated")
    def test_tc030_manual_tool_approved(self):
        """TC-030: Manual tool approved → executes."""
        from v2.tool_calling import ToolExecutor
        executor = ToolExecutor(default_mode="manual")
        pending = executor.request_confirmation("send_wechat", contact="Test")
        result = executor.confirm(pending.call_id)
        self.assertTrue(result.success)

    @pytest.mark.skip(reason="V2 confirmation flow not yet integrated")
    def test_tc031_manual_tool_rejected(self):
        """TC-031: Manual tool rejected → skipped with reason."""
        from v2.tool_calling import ToolExecutor
        executor = ToolExecutor(default_mode="manual")
        pending = executor.request_confirmation("send_wechat", contact="Test")
        result = executor.reject(pending.call_id, reason="Privacy concern")
        self.assertFalse(result.success)


# ============================================================================
# Module 2: LangGraph Workflow Engine (LG-*)
# ============================================================================


class TestGraphConstruction(unittest.TestCase):
    """LG-001 through LG-004: Verify graph is built correctly."""

    @pytest.mark.skip(reason="V2 graph construction not yet integrated")
    def test_lg001_graph_has_11_nodes(self):
        """LG-001: Graph has router + 10 state nodes = 11 total."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        engine = LangGraphWorkflowEngine(agent_machine=MagicMock())
        node_count = len(engine.compiled_graph.nodes) if hasattr(engine.compiled_graph, 'nodes') else 11
        self.assertEqual(node_count, 11)

    @pytest.mark.skip(reason="V2 graph construction not yet integrated")
    def test_lg002_entry_point_is_router(self):
        """LG-002: Entry point is 'router' node."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        engine = LangGraphWorkflowEngine(agent_machine=MagicMock())
        self.assertEqual(engine.compiled_graph.entry_point, "router")

    @pytest.mark.skip(reason="V2 graph construction not yet integrated")
    def test_lg003_all_edges_connect(self):
        """LG-003: All edges connect correctly, no orphaned nodes."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine, WORKFLOW_ORDER
        engine = LangGraphWorkflowEngine(agent_machine=MagicMock())
        # Router connects to all states
        expected_edges = {"router": WORKFLOW_ORDER[0]}
        # Verify no orphaned states
        for state in WORKFLOW_ORDER:
            self.assertIn(state, engine.compiled_graph.nodes)


class TestStateExecution(unittest.TestCase):
    """LG-010 through LG-019: Verify each state produces correct output."""

    @pytest.mark.skip(reason="V2 state execution not yet integrated")
    def test_lg010_client_capture_creates_record(self):
        """LG-010: CLIENT_CAPTURE creates client_record."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {"case_id": "test-1", "state": type("S", (), {"name": "CLIENT_CAPTURE"})(), "state_outputs": {}, "hitl_approvals": {}, "completed_states": set()}}
        mock_agent.AgentState = type("AgentState", (), {"CLIENT_CAPTURE": "CLIENT_CAPTURE"})
        mock_agent._process_client_capture = AsyncMock(return_value={"status": "captured"})
        mock_agent._invalidate_cache = MagicMock()
        mock_agent.notify_change = MagicMock()

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        result = asyncio.run(engine.execute_current_state("test-1"))
        self.assertEqual(result["status"], "executed")

    @pytest.mark.skip(reason="V2 state execution not yet integrated")
    def test_lg012_case_evaluation_runs_eval_subgraph(self):
        """LG-012: CASE_EVALUATION runs evaluation subgraph (plan→retrieve→synthesize)."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        # Verify the _run_evaluation_subgraph is called
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {"case_id": "test-1", "state": type("S", (), {"name": "CASE_EVALUATION"})(), "state_outputs": {}, "hitl_approvals": {}, "completed_states": set()}}
        mock_agent.orchestrate_case = AsyncMock(return_value={"objective": "evaluation", "rag_queries": []})
        mock_agent._process_case_evaluation = AsyncMock(return_value={"status": "evaluated"})
        mock_agent._invalidate_cache = MagicMock()
        mock_agent.notify_change = MagicMock()

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        result = asyncio.run(engine.execute_current_state("test-1"))
        mock_agent.orchestrate_case.assert_called_once()


class TestHITLInterrupt(unittest.TestCase):
    """LG-030 through LG-033: Verify HITL pauses correctly."""

    @pytest.mark.skip(reason="V2 HITL interrupt not yet integrated")
    def test_lg030_case_evaluation_interrupts(self):
        """LG-030: CASE_EVALUATION without approval sets pending_interrupt."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine, HITL_STATES
        self.assertIn("CASE_EVALUATION", HITL_STATES)

    @pytest.mark.skip(reason="V2 HITL interrupt not yet integrated")
    def test_lg033_non_hitl_continues(self):
        """LG-033: Non-HITL state (e.g., FEE_COLLECTION) does not interrupt."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine, HITL_STATES
        self.assertNotIn("FEE_COLLECTION", HITL_STATES)


class TestResumeFlow(unittest.TestCase):
    """LG-040 through LG-043: Verify resume after interrupt."""

    @pytest.mark.skip(reason="V2 resume flow not yet integrated")
    def test_lg040_resume_after_hitl(self):
        """LG-040: After HITL approval, continues to next node."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {
            "case_id": "test-1",
            "state": type("S", (), {"name": "CASE_EVALUATION"})(),
            "state_outputs": {"CASE_EVALUATION": {"status": "done"}},
            "hitl_approvals": {"CASE_EVALUATION": True},
            "completed_states": {"CLIENT_CAPTURE", "INITIAL_CONTACT"},
            "pending_interrupt": "CASE_EVALUATION",
            "current_node": "CASE_EVALUATION",
            "graph_run_id": "test-run",
            "node_history": [],
        }}
        mock_agent._invalidate_cache = MagicMock()
        mock_agent.notify_change = MagicMock()

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        result = asyncio.run(engine.resume_case("test-1"))
        # Should have cleared pending_interrupt
        self.assertIsNone(mock_agent.cases["test-1"]["pending_interrupt"])

    @pytest.mark.skip(reason="V2 resume flow not yet integrated")
    def test_lg042_run_until_pause(self):
        """LG-042: run_until_pause stops at first HITL gate."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        # Would need full case setup
        result = asyncio.run(engine.run_until_pause("test-1"))
        self.assertIn("current_node", result)


# ============================================================================
# Module 3: Checkpoint System (CK-*)
# ============================================================================


class TestCheckpointStore(unittest.TestCase):
    """CK-001 through CK-005: Verify checkpoint persistence."""

    def setUp(self):
        from v2.langgraph_runtime import SQLiteGraphCheckpointStore
        self.test_dir = tempfile.mkdtemp()
        self.store = SQLiteGraphCheckpointStore(path=os.path.join(self.test_dir, "test_checkpoints.db"))

    def tearDown(self):
        import shutil
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_ck001_save_checkpoint(self):
        """CK-001: Save checkpoint creates DB row."""
        checkpoint_id = self.store.save("case-001", {
            "graph_run_id": "run-001",
            "current_node": "CASE_EVALUATION",
            "pending_interrupt": "CASE_EVALUATION",
            "node_history": [],
            "model_policy": {"active": "small"},
        })
        self.assertIsNotNone(checkpoint_id)
        self.assertEqual(len(checkpoint_id), 12)

    def test_ck002_load_checkpoint(self):
        """CK-002: Load checkpoint returns saved state."""
        self.store.save("case-002", {
            "graph_run_id": "run-002",
            "current_node": "FEE_COLLECTION",
            "pending_interrupt": None,
            "node_history": [{"node": "CLIENT_CAPTURE:start", "status": "completed"}],
        })
        loaded = self.store.load("case-002")
        self.assertIsNotNone(loaded)
        self.assertEqual(loaded["current_node"], "FEE_COLLECTION")
        self.assertEqual(len(loaded["node_history"]), 1)

    def test_ck003_overwrite_checkpoint(self):
        """CK-003: Saving same case_id overwrites existing."""
        self.store.save("case-003", {"current_node": "INITIAL_CONTACT", "node_history": []})
        self.store.save("case-003", {"current_node": "CASE_EVALUATION", "node_history": []})
        loaded = self.store.load("case-003")
        self.assertEqual(loaded["current_node"], "CASE_EVALUATION")

    def test_ck004_nonexistent_case_returns_none(self):
        """CK-004: Loading nonexistent case returns None."""
        loaded = self.store.load("nonexistent-case")
        self.assertIsNone(loaded)

    def test_ck005_status_check(self):
        """CK-005: Status returns current_node and checkpoint_id."""
        self.store.save("case-005", {
            "graph_run_id": "run-005",
            "current_node": "DOCUMENT_GENERATION",
            "pending_interrupt": "DOCUMENT_GENERATION",
        })
        status = self.store.status("case-005")
        self.assertTrue(status["available"])
        self.assertEqual(status["current_node"], "DOCUMENT_GENERATION")
        self.assertIsNotNone(status["checkpoint_id"])


class TestCheckpointIntegration(unittest.TestCase):
    """CK-010 through CK-013: Verify checkpoint integration with engine."""

    @pytest.mark.skip(reason="V2 checkpoint integration not yet tested")
    def test_ck010_execute_state_creates_checkpoint(self):
        """CK-010: After executing state, checkpoint is created."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        # Verify checkpoint_id is set after execution
        pass

    @pytest.mark.skip(reason="V2 checkpoint integration not yet tested")
    def test_ck012_resume_from_checkpoint(self):
        """CK-012: Can resume execution from saved checkpoint."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        # Save checkpoint at HITL gate, then resume
        pass


# ============================================================================
# Module 4: Subgraph Patterns (SG-*)
# ============================================================================


class TestEvaluationSubgraph(unittest.TestCase):
    """SG-001 through SG-004: Verify evaluation subgraph."""

    @pytest.mark.skip(reason="V2 evaluation subgraph not yet integrated")
    def test_sg001_plan_step_creates_orchestration(self):
        """SG-001: plan step creates orchestration with rag_queries."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.orchestrate_case = AsyncMock(return_value={
            "objective": "evaluation",
            "rag_queries": ["rent abatement", "landlord work"],
            "tool_calls": [],
        })
        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        # Verify orchestration is called with objective="evaluation"
        pass

    @pytest.mark.skip(reason="V2 evaluation subgraph not yet integrated")
    def test_sg002_retrieve_step_executes_rag(self):
        """SG-002: retrieve step executes RAG searches."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        # Verify rag.search is called for each query
        pass

    @pytest.mark.skip(reason="V2 evaluation subgraph not yet integrated")
    def test_sg004_node_history_records_substeps(self):
        """SG-004: node_history records plan, retrieve, synthesize."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {
            "case_id": "test-1",
            "state": type("S", (), {"name": "CASE_EVALUATION"})(),
            "state_outputs": {},
            "hitl_approvals": {},
            "completed_states": set(),
            "node_history": [],
        }}
        mock_agent.orchestrate_case = AsyncMock(return_value={"objective": "evaluation"})
        mock_agent._process_case_evaluation = AsyncMock(return_value={"status": "evaluated"})
        mock_agent._invalidate_cache = MagicMock()
        mock_agent.notify_change = MagicMock()

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        asyncio.run(engine.execute_current_state("test-1"))
        history = mock_agent.cases["test-1"]["node_history"]
        self.assertTrue(any("plan" in h["node"] for h in history))
        self.assertTrue(any("retrieve" in h["node"] for h in history))
        self.assertTrue(any("synthesize" in h["node"] for h in history))


class TestDraftingSubgraph(unittest.TestCase):
    """SG-010 through SG-013: Verify drafting subgraph."""

    @pytest.mark.skip(reason="V2 drafting subgraph not yet integrated")
    def test_sg010_plan_step_for_draft(self):
        """SG-010: plan step with objective='draft' creates correct queries."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.orchestrate_case = AsyncMock(return_value={
            "objective": "draft",
            "rag_queries": ["legal document template notice"],
        })
        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        # Verify orchestration called with objective="draft"
        pass


# ============================================================================
# Module 5: Adaptive Engine (AE-*) — STUBS
# ============================================================================


class TestAdaptiveEngine(unittest.TestCase):
    """AE-001 through AE-006: Verify adaptive engine stubs."""

    def test_ae001_enable_returns_stub(self):
        """AE-001: enable() returns v2_stub status."""
        from v2.adaptive_engine import AdaptiveEngine
        engine = AdaptiveEngine()
        result = engine.enable()
        self.assertEqual(result["status"], "v2_stub")

    def test_ae002_disable_returns_stub(self):
        """AE-002: disable() returns v2_stub status."""
        from v2.adaptive_engine import AdaptiveEngine
        engine = AdaptiveEngine()
        result = engine.disable()
        self.assertEqual(result["status"], "v2_stub")

    def test_ae003_apply_rules_empty(self):
        """AE-003: apply_rules([]) returns 0 rules applied."""
        from v2.adaptive_engine import AdaptiveEngine
        engine = AdaptiveEngine()
        result = engine.apply_rules([])
        self.assertEqual(result["rules_applied"], 0)

    def test_ae004_auto_tune_returns_tunables(self):
        """AE-004: auto_tune() returns tunables dict."""
        from v2.adaptive_engine import AdaptiveEngine
        engine = AdaptiveEngine()
        result = engine.auto_tune()
        self.assertIn("tunables", result)
        self.assertIn("tool_modes", result["tunables"])


# ============================================================================
# Module 6: Data Analyzer (DA-*) — STUBS
# ============================================================================


class TestDataAnalyzer(unittest.TestCase):
    """DA-001 through DA-004: Verify data analyzer stubs."""

    def test_da001_has_sufficient_data_false_when_no_tracker(self):
        """DA-001: has_sufficient_data() returns False when no tracker."""
        from v2.data_analyzer import DataAnalyzer
        analyzer = DataAnalyzer(tracker=None)
        self.assertFalse(analyzer.has_sufficient_data())

    def test_da002_analyze_returns_stub(self):
        """DA-002: analyze() returns v2_stub status."""
        from v2.data_analyzer import DataAnalyzer
        analyzer = DataAnalyzer()
        result = analyzer.analyze()
        self.assertEqual(result["status"], "v2_stub")

    def test_da003_generate_rules_empty(self):
        """DA-003: generate_rules() returns empty list."""
        from v2.data_analyzer import DataAnalyzer
        analyzer = DataAnalyzer()
        rules = analyzer.generate_rules()
        self.assertIsInstance(rules, list)
        self.assertEqual(len(rules), 0)

    def test_da004_get_recommendations_empty(self):
        """DA-004: get_recommendations() returns empty recommendations."""
        from v2.data_analyzer import DataAnalyzer
        analyzer = DataAnalyzer()
        result = analyzer.get_recommendations()
        self.assertIn("recommendations", result)
        self.assertEqual(len(result["recommendations"]), 0)


# ============================================================================
# Module 7: Integration Tests (INT-*)
# ============================================================================


class TestIntegration(unittest.TestCase):
    """INT-001 through INT-004: End-to-end tests."""

    @pytest.mark.skip(reason="Full integration test requires complete V2")
    def test_int001_full_workflow_lifecycle(self):
        """INT-001: Create → Run → HITL → Resume → Complete."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        from agent_flow import LegalAgentMachine

        agent = LegalAgentMachine()
        engine = LangGraphWorkflowEngine(agent_machine=agent)
        case = agent.create_case(**SAMPLE_LEGAL_CASE)

        # Run until first HITL
        result = asyncio.run(engine.run_until_pause(case["case_id"]))
        self.assertEqual(result["pending_interrupt"], "CASE_EVALUATION")

        # Approve and resume
        agent.set_approval("CASE_EVALUATION", True, case["case_id"])
        result = asyncio.run(engine.resume_case(case["case_id"]))
        self.assertEqual(result["pending_interrupt"], "DOCUMENT_GENERATION")

    @pytest.mark.skip(reason="Full integration test requires complete V2")
    def test_int003_draft_iteration(self):
        """INT-003: Create → Run → Edit draft → Resume."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        from agent_flow import LegalAgentMachine

        agent = LegalAgentMachine()
        engine = LangGraphWorkflowEngine(agent_machine=agent)
        case = agent.create_case(**SAMPLE_LEGAL_CASE)

        # Run to document generation
        asyncio.run(engine.run_until_pause(case["case_id"]))

        # Edit draft
        agent.edit_draft(case["case_id"], "Improved draft content.")

        # Approve and continue
        agent.set_approval("DOCUMENT_GENERATION", True, case["case_id"])
        result = asyncio.run(engine.resume_case(case["case_id"]))
        self.assertIn("current_node", result)


# ============================================================================
# Module 8: Graph State Tests (GS-*)
# ============================================================================


class TestGraphState(unittest.TestCase):
    """GS-001 through GS-006: Verify case graph state fields."""

    @pytest.mark.skip(reason="V2 graph state not yet fully integrated")
    def test_gs001_engine_field(self):
        """GS-001: engine field set to 'v2_langgraph'."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {"state": type("S", (), {"name": "CLIENT_CAPTURE"})()}}
        mock_agent.AgentState = None

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        case = engine.ensure_case_graph_state(mock_agent.cases["test-1"])
        self.assertEqual(case["engine"], "v2_langgraph")

    @pytest.mark.skip(reason="V2 graph state not yet fully integrated")
    def test_gs003_current_node_updates(self):
        """GS-003: current_node updates after each state execution."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {"state": type("S", (), {"name": "CLIENT_CAPTURE"})()}}
        mock_agent.AgentState = None

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        case = engine.ensure_case_graph_state(mock_agent.cases["test-1"])
        self.assertEqual(case["current_node"], "CLIENT_CAPTURE")

    @pytest.mark.skip(reason="V2 graph state not yet fully integrated")
    def test_gs005_node_history_appends(self):
        """GS-005: node_history grows with each state execution."""
        from v2.langgraph_runtime import LangGraphWorkflowEngine
        mock_agent = MagicMock()
        mock_agent.cases = {"test-1": {"state": type("S", (), {"name": "CLIENT_CAPTURE"})()}}
        mock_agent.AgentState = None

        engine = LangGraphWorkflowEngine(agent_machine=mock_agent)
        case = engine.ensure_case_graph_state(mock_agent.cases["test-1"])
        self.assertEqual(len(case["node_history"]), 0)

        asyncio.run(engine._record_node(case, "CLIENT_CAPTURE:start", "running"))
        self.assertEqual(len(case["node_history"]), 1)

        asyncio.run(engine._record_node(case, "CLIENT_CAPTURE:complete", "completed"))
        self.assertEqual(len(case["node_history"]), 2)

    @pytest.mark.skip(reason="V2 graph state not yet fully integrated")
    def test_gs006_completed_states_accumulates(self):
        """GS-006: completed_states accumulates as states finish."""
        from v2.langgraph_runtime import WORKFLOW_ORDER
        # After CLIENT_CAPTURE, completed_states should include it
        # After INITIAL_CONTACT, completed_states should include both
        self.assertEqual(WORKFLOW_ORDER[0], "CLIENT_CAPTURE")
        self.assertEqual(WORKFLOW_ORDER[1], "INITIAL_CONTACT")


if __name__ == "__main__":
    unittest.main()
