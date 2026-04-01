"""
Speed benchmarks and performance tests for AgentFlow V1.

Measures:
- Document ingestion speed (txt, simulated PDF)
- RAG search latency
- State transition latency
- Case CRUD latency
- Bulk operations (creating many cases)
- Memory usage during operations
- Concurrent operation performance
"""

import asyncio
import io
import json
import os
import shutil
import tempfile
import time
import tracemalloc
import unittest
from contextlib import contextmanager
from unittest.mock import patch


@contextmanager
def timer():
    """Context manager that measures elapsed time."""
    class Timer:
        elapsed = 0.0
    t = Timer()
    start = time.perf_counter()
    yield t
    t.elapsed = time.perf_counter() - start


@contextmanager
def memory_tracker():
    """Context manager that measures peak memory usage."""
    tracemalloc.start()
    class MemTracker:
        current = 0
        peak = 0
    m = MemTracker()
    yield m
    current, peak = tracemalloc.get_traced_memory()
    m.current = current
    m.peak = peak
    tracemalloc.stop()


def generate_test_text(size_kb):
    """Generate test text of approximately size_kb kilobytes."""
    base_paragraph = (
        "第{num}条 本合同由甲方（出租方）与乙方（承租方）签订。"
        "租赁物业位于中国上海市浦东新区，租赁面积为{area}平方米。"
        "租金为每月人民币{rent}元，于每月1日前支付。"
        "租期自{start}年1月1日起至{end}年12月31日止。"
        "如一方违约，应向对方支付相当于三个月租金的违约金。"
        "The tenant shall use the premises solely for general office purposes. "
        "The landlord shall maintain the building in good condition throughout the lease term. "
        "Any disputes shall be resolved through arbitration in Shanghai."
    )
    paragraphs = []
    target_bytes = size_kb * 1024
    i = 1
    while sum(len(p.encode('utf-8')) for p in paragraphs) < target_bytes:
        para = base_paragraph.format(
            num=i % 100 + 1,
            area=500 + (i * 10),
            rent=50000 + (i * 1000),
            start=2024,
            end=2026
        )
        paragraphs.append(para)
        i += 1
    return "\n\n".join(paragraphs)


def generate_test_pdf_content(size_kb):
    """Generate text content that simulates what would be in a PDF."""
    return generate_test_text(size_kb)


class TestDocumentIngestionSpeed(unittest.TestCase):
    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def _write_test_file(self, name, content):
        path = os.path.join(self.test_dir, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        return path

    def test_ingest_1kb_txt(self):
        content = generate_test_text(1)
        path = self._write_test_file("small.txt", content)
        with timer() as t:
            success, msg = self.rag.ingest_file(path)
        self.assertTrue(success)
        print(f"\n[INGEST] 1KB TXT: {t.elapsed*1000:.1f}ms | {msg}")
        self.assertLess(t.elapsed, 5.0, "1KB ingest should be under 5 seconds")

    def test_ingest_10kb_txt(self):
        content = generate_test_text(10)
        path = self._write_test_file("medium.txt", content)
        with timer() as t:
            success, msg = self.rag.ingest_file(path)
        self.assertTrue(success)
        print(f"[INGEST] 10KB TXT: {t.elapsed*1000:.1f}ms | {msg}")
        self.assertLess(t.elapsed, 10.0, "10KB ingest should be under 10 seconds")

    def test_ingest_100kb_txt(self):
        content = generate_test_text(100)
        path = self._write_test_file("large.txt", content)
        with timer() as t:
            success, msg = self.rag.ingest_file(path)
        self.assertTrue(success)
        print(f"[INGEST] 100KB TXT: {t.elapsed*1000:.1f}ms | {msg}")
        self.assertLess(t.elapsed, 30.0, "100KB ingest should be under 30 seconds")

    def test_ingest_500kb_txt(self):
        content = generate_test_text(500)
        path = self._write_test_file("xlarge.txt", content)
        with timer() as t:
            success, msg = self.rag.ingest_file(path)
        self.assertTrue(success)
        print(f"[INGEST] 500KB TXT: {t.elapsed*1000:.1f}ms | {msg}")
        self.assertLess(t.elapsed, 60.0, "500KB ingest should be under 60 seconds")

    def test_ingest_mock_case_file(self):
        mock_path = "./data/mock_cases/mock_commercial_lease_case_2018_sec_source.txt"
        if not os.path.exists(mock_path):
            self.skipTest("Mock case file not found")
        with timer() as t:
            success, msg = self.rag.ingest_file(mock_path)
        self.assertTrue(success)
        print(f"[INGEST] Mock case file: {t.elapsed*1000:.1f}ms | {msg}")

    def test_reingest_same_file(self):
        """Test that re-ingesting the same file replaces, not duplicates."""
        content = generate_test_text(5)
        path = self._write_test_file("reingest.txt", content)
        self.rag.ingest_file(path)

        with timer() as t:
            success, msg = self.rag.ingest_file(path)
        self.assertTrue(success)
        print(f"[INGEST] Re-ingest 5KB: {t.elapsed*1000:.1f}ms")
        # Should still have exactly 1 document
        self.assertEqual(self.rag.get_summary()["document_count"], 1)

    def test_ingest_multiple_files_sequential(self):
        """Ingest 5 files sequentially and measure total time."""
        total_time = 0
        for i in range(5):
            content = generate_test_text(10)
            path = self._write_test_file(f"multi_{i}.txt", content)
            start = time.perf_counter()
            self.rag.ingest_file(path)
            total_time += time.perf_counter() - start
        print(f"[INGEST] 5x 10KB sequential: {total_time*1000:.1f}ms total ({total_time*1000/5:.1f}ms avg)")
        self.assertLess(total_time, 30.0)


class TestRAGSearchSpeed(unittest.TestCase):
    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)
        # Pre-ingest a document
        content = generate_test_text(50)
        path = os.path.join(self.test_dir, "search_test.txt")
        with open(path, "w") as f:
            f.write(content)
        self.rag.ingest_file(path)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_search_latency_single_doc(self):
        queries = [
            "rent abatement landlord work",
            "违约金 违约 remedies",
            "lease term renewal options",
            "security deposit refund",
            "property maintenance obligations",
        ]
        latencies = []
        for q in queries:
            with timer() as t:
                results = self.rag.search(q)
            latencies.append(t.elapsed)
            self.assertIsInstance(results, str)

        avg_ms = sum(latencies) / len(latencies) * 1000
        max_ms = max(latencies) * 1000
        print(f"\n[SEARCH] Single doc: avg={avg_ms:.1f}ms, max={max_ms:.1f}ms, queries={len(queries)}")
        self.assertLess(avg_ms, 100, "Average search should be under 100ms")

    def test_search_latency_cached(self):
        """Search same query multiple times - LRU cache should help."""
        query = "rent abatement landlord work obligations"
        # First call (cold)
        with timer() as t:
            self.rag.search(query)
        cold_ms = t.elapsed * 1000

        # Subsequent calls (warm cache)
        warm_latencies = []
        for _ in range(10):
            with timer() as t:
                self.rag.search(query)
            warm_latencies.append(t.elapsed * 1000)

        avg_warm = sum(warm_latencies) / len(warm_latencies)
        print(f"[SEARCH] Cache: cold={cold_ms:.1f}ms, warm_avg={avg_warm:.1f}ms")

    def test_search_structured_latency(self):
        with timer() as t:
            results = self.rag.search_structured("lease dispute", k=5)
        self.assertIsInstance(results, list)
        print(f"[SEARCH] Structured: {t.elapsed*1000:.1f}ms, results={len(results)}")

    def test_search_with_many_chunks(self):
        """Ingest many files and test search scalability."""
        for i in range(20):
            content = generate_test_text(5)
            path = os.path.join(self.test_dir, f"chunk_{i}.txt")
            with open(path, "w") as f:
                f.write(content)
            self.rag.ingest_file(path)

        with timer() as t:
            results = self.rag.search("commercial lease dispute rent")
        print(f"[SEARCH] 20 docs (100KB+): {t.elapsed*1000:.1f}ms")
        self.assertIsInstance(results, str)

    def test_search_structured_returns_scores(self):
        results = self.rag.search_structured("rent", k=3)
        if results:
            self.assertIn("score", results[0])
            self.assertIsInstance(results[0]["score"], float)


class TestChunkingSpeed(unittest.TestCase):
    def test_chunking_1kb(self):
        from rag_manager import _structure_aware_chunk
        text = generate_test_text(1)
        with timer() as t:
            chunks = _structure_aware_chunk(text)
        print(f"\n[CHUNK] 1KB: {t.elapsed*1000:.1f}ms, chunks={len(chunks)}")
        self.assertGreater(len(chunks), 0)

    def test_chunking_100kb(self):
        from rag_manager import _structure_aware_chunk
        text = generate_test_text(100)
        with timer() as t:
            chunks = _structure_aware_chunk(text)
        print(f"[CHUNK] 100KB: {t.elapsed*1000:.1f}ms, chunks={len(chunks)}")
        self.assertLess(t.elapsed, 5.0)

    def test_chunking_with_legal_clauses(self):
        from rag_manager import _structure_aware_chunk
        text = "\n".join([
            f"第{i}条 租金支付条款 乙方应于每月{i}日前支付租金。"
            for i in range(1, 101)
        ])
        with timer() as t:
            chunks = _structure_aware_chunk(text)
        print(f"[CHUNK] 100 legal clauses: {t.elapsed*1000:.1f}ms, chunks={len(chunks)}")
        # Legal clause splitting should produce multiple chunks
        self.assertGreater(len(chunks), 1)


class TestStateTransitionSpeed(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()
        self.case = self.machine.create_case()
        self.case_id = self.case["case_id"]

    def test_single_advance_latency(self):
        latencies = []
        for _ in range(10):
            self.machine.cases[self.case_id]["state"] = self.machine.cases[self.case_id]["state"].__class__["CLIENT_CAPTURE"]
            self.machine.cases[self.case_id]["completed_states"] = set()
            with timer() as t:
                self.machine.advance_state(self.case_id)
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        max_ms = max(latencies) * 1000
        print(f"\n[STATE] Advance: avg={avg_ms:.2f}ms, max={max_ms:.2f}ms")
        self.assertLess(avg_ms, 50, "State advance should be under 50ms")

    def test_single_rewind_latency(self):
        # Advance to CASE_EVALUATION first
        self.machine.advance_state(self.case_id)
        self.machine.advance_state(self.case_id)

        latencies = []
        for _ in range(10):
            self.machine.advance_state(self.case_id)
            with timer() as t:
                self.machine.rewind_state(self.case_id, "CLIENT_CAPTURE")
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[STATE] Rewind: avg={avg_ms:.2f}ms")
        self.assertLess(avg_ms, 50)

    def test_approval_latency(self):
        latencies = []
        for _ in range(10):
            with timer() as t:
                self.machine.set_approval("CASE_EVALUATION", True, self.case_id)
            latencies.append(t.elapsed)
            # Reset for next iteration
            self.machine.set_approval("CASE_EVALUATION", False, self.case_id)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[STATE] Approval: avg={avg_ms:.2f}ms")
        self.assertLess(avg_ms, 20)

    def test_get_status_latency(self):
        # Warm up
        self.machine.get_case_status(self.case_id)

        latencies = []
        for _ in range(100):
            with timer() as t:
                self.machine.get_case_status(self.case_id)
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[STATE] get_status (cached): avg={avg_ms:.3f}ms")
        self.assertLess(avg_ms, 1, "Cached status should be under 1ms")


class TestCaseCRUDSpeed(unittest.TestCase):
    def setUp(self):
        from agent_flow import LegalAgentMachine
        self.machine = LegalAgentMachine()

    def test_create_case_latency(self):
        latencies = []
        for _ in range(50):
            with timer() as t:
                self.machine.create_case()
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        max_ms = max(latencies) * 1000
        print(f"\n[CASE] Create: avg={avg_ms:.2f}ms, max={max_ms:.2f}ms")
        self.assertLess(avg_ms, 10, "Case creation should be under 10ms")

    def test_create_100_cases(self):
        """Create 100 cases and measure total time."""
        with timer() as t:
            for _ in range(100):
                self.machine.create_case()
        print(f"[CASE] Create 100: {t.elapsed*1000:.1f}ms ({t.elapsed*10:.1f}ms/case)")
        self.assertEqual(len(self.machine.cases), 101)  # 100 + 1 bootstrap

    def test_list_cases_latency(self):
        # Create 50 cases
        for _ in range(50):
            self.machine.create_case()

        with timer() as t:
            cases = self.machine.list_cases()
        print(f"[CASE] List 50 cases: {t.elapsed*1000:.1f}ms")
        self.assertEqual(len(cases), 51)

    def test_delete_case_latency(self):
        case_ids = []
        for _ in range(20):
            c = self.machine.create_case()
            case_ids.append(c["case_id"])

        latencies = []
        for cid in case_ids:
            with timer() as t:
                self.machine.delete_case(cid)
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[CASE] Delete: avg={avg_ms:.2f}ms")
        self.assertLess(avg_ms, 5)

    def test_update_case_latency(self):
        case = self.machine.create_case()
        latencies = []
        for i in range(50):
            with timer() as t:
                self.machine.update_case(case["case_id"], client_name=f"Updated{i}")
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[CASE] Update: avg={avg_ms:.2f}ms")
        self.assertLess(avg_ms, 5)


class TestMemoryUsage(unittest.TestCase):
    def test_create_500_cases_memory(self):
        from agent_flow import LegalAgentMachine
        with memory_tracker() as mem:
            machine = LegalAgentMachine()
            for _ in range(500):
                machine.create_case()

        peak_mb = mem.peak / (1024 * 1024)
        print(f"\n[MEM] 500 cases: peak={peak_mb:.1f}MB")
        self.assertLess(peak_mb, 500, "500 cases should use under 500MB")

    def test_ingest_large_document_memory(self):
        from rag_manager import RAGManager
        test_dir = tempfile.mkdtemp()
        try:
            rag = RAGManager(persist_directory=test_dir)
            content = generate_test_text(1000)  # 1MB
            path = os.path.join(test_dir, "big.txt")
            with open(path, "w") as f:
                f.write(content)

            with memory_tracker() as mem:
                rag.ingest_file(path)

            peak_mb = mem.peak / (1024 * 1024)
            print(f"[MEM] 1MB document ingest: peak={peak_mb:.1f}MB")
        finally:
            shutil.rmtree(test_dir, ignore_errors=True)

    def test_search_memory(self):
        from rag_manager import RAGManager
        test_dir = tempfile.mkdtemp()
        try:
            rag = RAGManager(persist_directory=test_dir)
            content = generate_test_text(100)
            path = os.path.join(test_dir, "search_mem.txt")
            with open(path, "w") as f:
                f.write(content)
            rag.ingest_file(path)

            with memory_tracker() as mem:
                for _ in range(100):
                    rag.search("lease dispute rent abatement")

            peak_mb = mem.peak / (1024 * 1024)
            print(f"[MEM] 100 searches: peak={peak_mb:.1f}MB")
        finally:
            shutil.rmtree(test_dir, ignore_errors=True)


class TestUsageTrackerSpeed(unittest.TestCase):
    def setUp(self):
        from usage_tracker import UsageTracker
        self.test_dir = tempfile.mkdtemp()
        self.db_path = os.path.join(self.test_dir, "bench.db")
        self.tracker = UsageTracker(db_path=self.db_path)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_record_event_latency(self):
        latencies = []
        for i in range(100):
            with timer() as t:
                self.tracker.record("bench_event", "test", {"iteration": i, "data": "x" * 100})
            latencies.append(t.elapsed)

        avg_ms = sum(latencies) / len(latencies) * 1000
        p95_ms = sorted(latencies)[int(len(latencies) * 0.95)] * 1000
        print(f"\n[TRACK] Record event: avg={avg_ms:.2f}ms, p95={p95_ms:.2f}ms")
        self.assertLess(avg_ms, 10)

    def test_bulk_record_1000_events(self):
        with timer() as t:
            for i in range(1000):
                self.tracker.record("bulk_event", "test", {"i": i})
        print(f"[TRACK] 1000 records: {t.elapsed*1000:.1f}ms")
        self.assertLess(t.elapsed, 30, "1000 records should be under 30 seconds")

    def test_get_recent_events_latency(self):
        # Insert 100 events first
        for i in range(100):
            self.tracker.record("event", "test", {"i": i})

        with timer() as t:
            events = self.tracker.get_recent_events(limit=50)
        print(f"[TRACK] Get 50 recent events: {t.elapsed*1000:.1f}ms")
        self.assertEqual(len(events), 50)

    def test_generate_summary_latency(self):
        for i in range(100):
            self.tracker.record("event", "test", {"i": i})

        with timer() as t:
            summary = self.tracker.generate_summary()
        print(f"[TRACK] Generate summary: {t.elapsed*1000:.1f}ms")


class TestAPILatency(unittest.TestCase):
    def setUp(self):
        from fastapi.testclient import TestClient
        from server import app
        self.client = TestClient(app)

    def test_status_endpoint_latency(self):
        latencies = []
        for _ in range(50):
            with timer() as t:
                response = self.client.get("/status")
            latencies.append(t.elapsed)
            self.assertEqual(response.status_code, 200)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"\n[API] GET /status: avg={avg_ms:.1f}ms")
        self.assertLess(avg_ms, 100)

    def test_create_case_endpoint_latency(self):
        latencies = []
        for i in range(20):
            with timer() as t:
                response = self.client.post("/cases", json={
                    "client_name": f"Benchmark Client {i}",
                    "matter_type": "Civil Litigation"
                })
            latencies.append(t.elapsed)
            self.assertEqual(response.status_code, 200)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[API] POST /cases: avg={avg_ms:.1f}ms")
        self.assertLess(avg_ms, 100)

    def test_advance_state_endpoint_latency(self):
        latencies = []
        for i in range(20):
            # Create a fresh case for each test to avoid state exhaustion
            resp = self.client.post("/cases", json={"client_name": f"LatencyTest{i}"})
            case_id = resp.json()["case"]["case_id"]
            with timer() as t:
                response = self.client.post(f"/cases/{case_id}/advance", content=b"")
            latencies.append(t.elapsed)
            self.assertEqual(response.status_code, 200)

        avg_ms = sum(latencies) / len(latencies) * 1000
        print(f"[API] POST /cases/advance: avg={avg_ms:.1f}ms")
        self.assertLess(avg_ms, 100)

    def test_concurrent_requests_simulation(self):
        """Simulate 10 rapid sequential API calls."""
        with timer() as t:
            for i in range(10):
                self.client.post("/cases", json={"client_name": f"Concurrent {i}"})
        print(f"[API] 10 sequential creates: {t.elapsed*1000:.1f}ms")


class TestBenchmarkSummary(unittest.TestCase):
    """Print a comprehensive benchmark summary at the end."""

    @classmethod
    def tearDownClass(cls):
        print("\n" + "=" * 60)
        print("BENCHMARK SUMMARY - AgentFlow V1 Performance")
        print("=" * 60)
        print("Run pytest -v tests/test_benchmarks.py -s to see all results")
        print("=" * 60 + "\n")


if __name__ == "__main__":
    unittest.main(verbosity=2)
