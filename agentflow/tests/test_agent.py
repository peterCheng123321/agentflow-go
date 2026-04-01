import asyncio
import importlib
import unittest
from unittest.mock import Mock, patch

from sys_scanner import get_system_info, recommend_model
from wechat_connector import WeChatConnector, WeChatStatus


class FakeDualStageLLM:
    def generate_structured_json(self, prompt, context="", profile="small", fallback=None):
        return {
            "objective": "evaluation",
            "triage_summary": "Use targeted retrieval before legal synthesis.",
            "rag_queries": [
                "rent abatement landlord work",
                "commercial lease evidence requirements",
            ],
            "tool_calls": [
                {
                    "tool_name": "search_rag",
                    "args": {"query": "rent abatement landlord work"},
                    "reason": "Gather relevant retrieval context.",
                }
            ],
            "final_instruction": "Ground the answer in the retrieved context.",
        }

    def generate(self, prompt, context=""):
        return f"SYNTHESIZED OUTPUT\n{prompt}\n{context[:120]}"


class TestCurrentSystemBasics(unittest.TestCase):
    def test_system_info_has_expected_keys(self):
        info = get_system_info()
        self.assertIn("total_ram_gb", info)
        self.assertIn("os", info)

    def test_model_recommendation_switches_by_ram(self):
        self.assertEqual(recommend_model({"total_ram_gb": 4.0, "chip_generation": "M1"}), "mlx-community/Qwen2.5-0.5B-Instruct-4bit")
        self.assertEqual(recommend_model({"total_ram_gb": 32.0, "chip_generation": "M3"}), "mlx-community/Qwen2.5-14B-Instruct-4bit")

    def test_wechat_mock_login_and_send_success(self):
        connector = WeChatConnector(mode="mock")
        success = asyncio.run(connector.login())
        self.assertTrue(success)
        self.assertEqual(connector.status, WeChatStatus.CONNECTED)
        self.assertEqual(connector.adapter_mode, "mock")

        sent, message = asyncio.run(connector.send_message("Test", "Hello"))
        self.assertTrue(sent)
        self.assertEqual(message, "Sent (mock)")

    def test_wechat_send_fails_before_login(self):
        connector = WeChatConnector(mode="mock")
        sent, message = asyncio.run(connector.send_message("Test", "Hello"))
        self.assertFalse(sent)
        self.assertEqual(message, "Not connected")

    def test_wechat_message_handler_receives_mock_payload(self):
        connector = WeChatConnector(mode="mock")
        received = []

        @connector.on_message
        async def handler(message):
            received.append(message)

        asyncio.run(connector.simulate_incoming_message("Alice", "Need help"))
        self.assertEqual(received, [{"from": "Alice", "text": "Need help"}])

    def test_wechat_auto_mode_falls_back_to_mock(self):
        connector = WeChatConnector(mode="auto")
        self.assertIn(connector.adapter_mode, ["mock", "openclaw"])
        self.assertEqual(connector.capabilities["openclaw_available"], connector.adapter_mode == "openclaw")
        self.assertTrue(connector.capabilities["supports_group_create"])

    def test_wechat_openclaw_mode_uses_cli_runtime_when_available(self):
        connector = WeChatConnector(mode="openclaw")
        if connector.capabilities["openclaw_available"]:
            success = asyncio.run(connector.login())
            self.assertTrue(success)
            self.assertEqual(connector.adapter_mode, "openclaw")
            self.assertEqual(connector.status, WeChatStatus.CONNECTED)
        else:
            self.assertEqual(connector.adapter_mode, "mock")


class TestCrashCases(unittest.TestCase):
    def test_agent_flow_import_now_succeeds(self):
        module = importlib.import_module("agent_flow")
        self.assertTrue(hasattr(module, "LegalAgentMachine"))

    def test_server_import_now_succeeds(self):
        module = importlib.import_module("server")
        self.assertTrue(hasattr(module, "app"))

    def test_rag_manager_degrades_cleanly_when_backend_load_fails(self):
        rag_manager_module = importlib.import_module("rag_manager")
        rag = rag_manager_module.RAGManager()
        ready = rag.ensure_ready()
        self.assertTrue(ready)
        self.assertIn("lightweight", rag.backend_mode)

    def test_local_setup_prepares_status_payload(self):
        setup_manager = importlib.import_module("setup_manager")
        result = setup_manager.run_local_setup()

        self.assertTrue(result["setup_ran"])
        self.assertIn("recommended_model", result)
        self.assertTrue(result["directories_ready"])

    def test_tool_call_returns_none_when_local_llm_is_down(self):
        llm_provider = importlib.import_module("llm_provider")
        provider = llm_provider.OptimizedLLMProvider()

        with patch.object(provider, "_ensure_loaded", side_effect=RuntimeError("mlx unavailable")):
            decision = provider.fast_tool_call("send a message to Peter")

        self.assertEqual(decision, "<none>")

    def test_legal_generation_returns_error_when_local_llm_is_down(self):
        llm_provider = importlib.import_module("llm_provider")
        provider = llm_provider.OptimizedLLMProvider()

        def fake_generate(*args, **kwargs):
            raise RuntimeError("mlx generation failed")

        with patch.object(provider, "_ensure_loaded"):
            with patch("llm_provider.load_mlx_modules") as mock_load:
                mock_load.return_value = (None, fake_generate)
                result = provider.generate_legal_text("write a lease summary", context="contract text")

        self.assertEqual(result, "Error in generation.")

    def test_tool_engine_accepts_malformed_tag_and_force_closes_it(self):
        llm_provider = importlib.import_module("llm_provider")

        def fake_load():
            return (lambda *a, **k: "<send_wechat"), (lambda *a, **k: "<send_wechat")

        original_load = llm_provider.load_mlx_modules
        llm_provider.load_mlx_modules = fake_load
        llm_provider.mlx_generate = lambda *a, **k: "<send_wechat"
        try:
            engine = llm_provider.ToolCallingEngine()
            engine._model = "fake"
            engine._tokenizer = Mock()
            engine._tokenizer.apply_chat_template = Mock(return_value="prompt")
            engine._backend = "mlx"
            engine._prompt_cache = None
            result = engine.decide_tool("send this to wechat")
        finally:
            llm_provider.load_mlx_modules = original_load
            if hasattr(llm_provider, "mlx_generate"):
                del llm_provider.mlx_generate
        self.assertEqual(result, "<send_wechat>")

    def test_compat_generate_calls_legal_generation(self):
        llm_provider = importlib.import_module("llm_provider")
        provider = llm_provider.OptimizedLLMProvider()

        with patch.object(provider, "generate_legal_text", return_value="ok") as mock_generate:
            result = provider.generate("draft this", context="ctx")

        self.assertEqual(result, "ok")
        mock_generate.assert_called_once_with("draft this", context="ctx")

    def test_big_model_path_is_hard_disabled_by_default(self):
        llm_provider = importlib.import_module("llm_provider")
        provider = llm_provider.OptimizedLLMProvider()

        caps = provider.get_model_capabilities()

        self.assertTrue(caps["small_model_active"])
        self.assertFalse(caps["big_model_enabled"])
        self.assertEqual(provider._resolve_profile("big"), "small")

    def test_lightweight_rag_can_ingest_and_search_txt(self):
        rag_manager_module = importlib.import_module("rag_manager")
        rag = rag_manager_module.RAGManager(persist_directory="./data/test_vector_store")
        success, message = rag.ingest_file("./data/mock_cases/mock_commercial_lease_case_2018_sec_source.txt")

        self.assertTrue(success)
        self.assertIn("BM25", message)
        context = rag.search("rent abatement landlord work")
        self.assertIn("rent abatement", context.lower())

    def test_dynamic_cases_can_be_created_and_selected(self):
        agent_flow = importlib.import_module("agent_flow")
        machine = agent_flow.LegalAgentMachine()
        original_case_id = machine.active_case_id
        new_case = machine.create_case(client_name="ACME", source_channel="CRM", initial_msg="Need lease help")

        self.assertNotEqual(original_case_id, new_case["case_id"])
        selected = machine.select_case(new_case["case_id"])
        self.assertEqual(selected["client_name"], "ACME")
        self.assertEqual(machine.active_case_id, new_case["case_id"])

    def test_dynamic_case_state_advances_independently(self):
        agent_flow = importlib.import_module("agent_flow")
        machine = agent_flow.LegalAgentMachine()
        original_case_id = machine.active_case_id
        second_case = machine.create_case(client_name="Beta")

        machine.advance_state(original_case_id)
        self.assertEqual(machine.get_case_status(original_case_id)["state"], "INITIAL_CONTACT")
        self.assertEqual(machine.get_case_status(second_case["case_id"])["state"], "CLIENT_CAPTURE")

    def test_incoming_message_creates_dynamic_wechat_case(self):
        agent_flow = importlib.import_module("agent_flow")
        machine = agent_flow.LegalAgentMachine()
        before = len(machine.list_cases())

        created = asyncio.run(
            machine.handle_incoming_message(
                contact_name="Alice Zhang",
                text="We have a lease dispute and need help.",
                metadata={"wechat_id": "alice-001"},
            )
        )

        self.assertEqual(created["client_name"], "Alice Zhang")
        self.assertEqual(created["source_channel"], "WeChat")
        self.assertEqual(len(machine.list_cases()), before + 1)
        self.assertTrue(any("wechat_id" in note for note in created["notes"]))

    def test_mock_wechat_can_list_contacts_and_create_group(self):
        connector = WeChatConnector(mode="mock")
        asyncio.run(connector.login())
        contacts = asyncio.run(connector.list_contacts())
        created, message, group = asyncio.run(connector.create_group_chat("Case Team", ["Alice Zhang", "Lawyer Li"]))

        self.assertTrue(any(contact["name"] == "Alice Zhang" for contact in contacts))
        self.assertTrue(created)
        self.assertIn("mock", message.lower())
        self.assertEqual(group["group_name"], "Case Team")

    def test_rag_signal_adds_case_note(self):
        agent_flow = importlib.import_module("agent_flow")
        machine = agent_flow.LegalAgentMachine()
        case_id = machine.active_case_id
        machine.signal_rag_ingestion("wechat attachment received", case_id)

        case = machine.get_case_status(case_id)
        self.assertTrue(any("RAG signal" in note for note in case["notes"]))

    def test_orchestrate_case_uses_structured_plan_and_rag_tooling(self):
        agent_flow = importlib.import_module("agent_flow")
        rag_manager_module = importlib.import_module("rag_manager")
        rag = rag_manager_module.RAGManager(persist_directory="./data/test_vector_store")
        rag.ingest_file("./data/mock_cases/mock_commercial_lease_case_2018_sec_source.txt")
        machine = agent_flow.LegalAgentMachine(llm=FakeDualStageLLM(), rag=rag)
        case_id = machine.active_case_id
        machine.update_case(
            case_id,
            client_name="Planner Test",
            matter_type="Commercial Lease Dispute",
            initial_msg="Landlord work caused rent abatement dispute.",
        )

        orchestration = asyncio.run(machine.orchestrate_case(case_id, objective="evaluation"))

        self.assertEqual(orchestration["objective"], "evaluation")
        self.assertEqual(len(orchestration["tool_runs"]), 2)
        self.assertIn("SYNTHESIZED OUTPUT", orchestration["synthesis"])
        detail = machine.get_case_detail(case_id)
        self.assertIsNotNone(detail["ai_action_plan"])
        self.assertIsNotNone(detail["ai_last_orchestration"])

    def test_langgraph_v2_execution_updates_graph_status(self):
        agent_flow = importlib.import_module("agent_flow")
        machine = agent_flow.LegalAgentMachine(llm=FakeDualStageLLM())
        case_id = machine.active_case_id

        asyncio.run(machine.execute_case_graph(case_id))
        status = machine.get_case_status(case_id)

        self.assertEqual(status["engine"], "v2_langgraph")
        self.assertEqual(status["current_node"], "INITIAL_CONTACT")
        self.assertTrue(status["checkpoint_status"]["available"])
        self.assertGreater(len(status["node_history"]), 0)

    def test_document_helpers_support_summary_grep_and_inspect(self):
        rag_manager_module = importlib.import_module("rag_manager")
        rag = rag_manager_module.RAGManager(persist_directory="./data/test_vector_store_doc_helpers")
        rag.ingest_file("./data/docs/chinese_mock_rental_case.txt")

        summary = rag.summarize_document("chinese_mock_rental_case.txt")
        grep = rag.grep_document("chinese_mock_rental_case.txt", "租金")
        inspect_payload = rag.inspect_document("chinese_mock_rental_case.txt", start_line=1, window=5)

        self.assertTrue(summary["summary_points"])
        self.assertGreaterEqual(grep["count"], 1)
        self.assertGreaterEqual(len(inspect_payload["lines"]), 1)


if __name__ == "__main__":
    unittest.main()
