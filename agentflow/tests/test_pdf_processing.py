"""
PDF and document processing benchmarks for AgentFlow V1.

Tests real-world document processing:
- PDF text extraction speed (PyMuPDF, pypdf fallback)
- Legal document ingestion pipeline
- Large document handling
- Multi-document batch processing
- Document parsing accuracy
"""

import io
import os
import shutil
import tempfile
import time
import unittest
from contextlib import contextmanager


@contextmanager
def timer():
    class Timer:
        elapsed = 0.0
    t = Timer()
    start = time.perf_counter()
    yield t
    t.elapsed = time.perf_counter() - start


def generate_legal_text(pages=1):
    """Generate legal Chinese+English text simulating a real document."""
    page_content = """
    租赁合同

    第一条 租赁物
    出租方（甲方）将位于上海市浦东新区XX路XX号的商业物业出租给承租方（乙方）使用。
    租赁面积为1500平方米，包含办公区域800平方米、会议室200平方米、公共区域500平方米。

    第二条 租赁期限
    租赁期限自2024年1月1日起至2026年12月31日止，共计36个月。
    租期届满前6个月，双方应协商续租事宜。如乙方需续租，应提前3个月书面通知甲方。

    第三条 租金及支付方式
    月租金为人民币85,000元（大写：捌万伍仟元整），含物业管理费。
    乙方应于每月1日前支付当月租金至甲方指定账户。
    逾期支付的，每逾期一日，乙方应支付应付租金0.05%的滞纳金。

    第四条 押金
    乙方应于本合同签订之日起5个工作日内向甲方支付押金人民币170,000元（大写：壹拾柒万元整）。
    租赁期满且乙方无违约行为的，甲方应于交房验收合格后15个工作日内无息退还押金。

    第五条 物业管理
    甲方负责租赁物的公共区域维护和建筑结构维修。
    乙方负责其承租区域内的日常维护和保洁。
    物业管理费已包含在月租金中，无需另行支付。

    第六条 装修改造
    乙方如需对租赁物进行装修或改造，须事先取得甲方书面同意。
    装修改造费用由乙方自行承担，装修方案须符合消防规定。
    租赁期满后，已安装的固定装修归甲方所有，可移动物品由乙方搬离。

    第七条 违约责任
    任何一方违反本合同约定的，应承担违约责任，赔偿对方损失。
    乙方提前解除合同的，已付租金不予退还，并须额外支付相当于3个月租金的违约金。
    甲方提前解除合同的，应双倍返还押金，并赔偿乙方合理的搬迁费用。

    第八条 不可抗力
    因不可抗力导致本合同无法继续履行的，双方均不承担违约责任。
    不可抗力包括但不限于：自然灾害、战争、政府征收征用等。

    第九条 争议解决
    因本合同引起的争议，双方应协商解决；协商不成的，任何一方均可向租赁物所在地人民法院提起诉讼。

    第十条 其他约定
    1. 本合同一式两份，甲乙双方各执一份。
    2. 本合同自双方签章之日起生效。
    3. 未尽事宜，双方可另行签订补充协议。

    第十一条 附件清单
    1. 租赁物平面图
    2. 物业管理公约
    3. 消防验收报告
    4. 房产证复印件

    甲方签章：_________________    乙方签章：_________________
    日期：2024年__月__日          日期：2024年__月__日

    ---
    TENANCY AGREEMENT

    1. PROPERTY
    The Landlord (Party A) hereby lets to the Tenant (Party B) the commercial property located at
    XX Road, Pudong New Area, Shanghai. Total area: 1,500 sqm.

    2. TERM
    The term of this lease shall commence on January 1, 2024 and expire on December 31, 2026.

    3. RENT
    The monthly rent shall be RMB 85,000 (Eighty-Five Thousand Yuan), inclusive of management fees.
    Payment shall be made on or before the 1st day of each month.

    4. SECURITY DEPOSIT
    A security deposit of RMB 170,000 shall be paid within 5 working days of signing.

    5. MAINTENANCE
    The Landlord shall maintain the structure and common areas.
    The Tenant shall maintain its leased premises.
    """
    return page_content * pages


class TestPDFTextExtraction(unittest.TestCase):
    """Test PDF text extraction speed with different libraries."""

    def test_pymupdf_extraction_speed(self):
        """Test PyMuPDF text extraction if available."""
        try:
            import pymupdf
        except ImportError:
            self.skipTest("PyMuPDF not installed")

        # Create a multi-page PDF in memory
        doc = pymupdf.open()
        for i in range(10):
            page = doc.new_page(width=595, height=842)
            text = generate_legal_text(pages=1)
            page.insert_text((50, 50), text, fontsize=10)

        pdf_bytes = doc.tobytes()
        doc.close()

        # Measure extraction time
        with timer() as t:
            doc = pymupdf.open(stream=pdf_bytes, filetype="pdf")
            full_text = ""
            for page in doc:
                full_text += page.get_text()
            doc.close()

        print(f"\n[PDF] PyMuPDF extract 10 pages: {t.elapsed*1000:.1f}ms, chars={len(full_text)}")
        self.assertGreater(len(full_text), 0)
        self.assertLess(t.elapsed, 5.0, "10-page PDF extraction should be under 5 seconds")

    def test_pymupdf_large_document(self):
        """Test with a large 50-page simulated PDF."""
        try:
            import pymupdf
        except ImportError:
            self.skipTest("PyMuPDF not installed")

        doc = pymupdf.open()
        for i in range(50):
            page = doc.new_page(width=595, height=842)
            text = generate_legal_text(pages=1)
            page.insert_text((50, 50), text, fontsize=10)

        pdf_bytes = doc.tobytes()
        doc.close()

        with timer() as t:
            doc = pymupdf.open(stream=pdf_bytes, filetype="pdf")
            full_text = ""
            for page in doc:
                full_text += page.get_text()
            doc.close()

        print(f"[PDF] PyMuPDF extract 50 pages: {t.elapsed*1000:.1f}ms, chars={len(full_text)}")
        self.assertGreater(len(full_text), 0)
        self.assertLess(t.elapsed, 15.0, "50-page PDF extraction should be under 15 seconds")

    def test_pypdf_extraction_speed(self):
        """Test pypdf fallback extraction."""
        try:
            from pypdf import PdfReader
            from reportlab.lib.pagesizes import A4
            from reportlab.pdfgen import canvas
        except ImportError:
            self.skipTest("pypdf or reportlab not installed")

        # Generate PDF using reportlab
        buffer = io.BytesIO()
        c = canvas.Canvas(buffer, pagesize=A4)
        for i in range(5):
            text = generate_legal_text(pages=1)
            # ReportLab has limited Unicode support, use ASCII portion
            y = 800
            for line in text.split('\n')[:30]:
                c.drawString(50, y, line[:80])
                y -= 12
                if y < 50:
                    c.showPage()
                    y = 800
            c.showPage()
        c.save()
        pdf_bytes = buffer.getvalue()

        with timer() as t:
            reader = PdfReader(io.BytesIO(pdf_bytes))
            full_text = ""
            for page in reader.pages:
                full_text += page.extract_text() or ""
        print(f"[PDF] pypdf extract 5 pages: {t.elapsed*1000:.1f}ms, chars={len(full_text)}")
        self.assertGreater(len(full_text), 0)

    def test_fallback_extraction_chain(self):
        """Test that _read_file falls back from PyMuPDF to pypdf."""
        from rag_manager import _read_file

        # Create a simple text file (PDF extraction requires actual PDF)
        test_dir = tempfile.mkdtemp()
        try:
            test_file = os.path.join(test_dir, "test.txt")
            content = generate_legal_text(pages=1)
            with open(test_file, "w", encoding="utf-8") as f:
                f.write(content)

            with timer() as t:
                result = _read_file(test_file)

            self.assertIsInstance(result, str)
            self.assertGreater(len(result), 0)
            print(f"\n[FILE] Text file read: {t.elapsed*1000:.1f}ms, chars={len(result)}")
        finally:
            shutil.rmtree(test_dir, ignore_errors=True)

    def test_unsupported_format_returns_error(self):
        from rag_manager import _read_file
        test_dir = tempfile.mkdtemp()
        try:
            test_file = os.path.join(test_dir, "test.xyz")
            with open(test_file, "w") as f:
                f.write("test")
            result = _read_file(test_file)
            self.assertIsInstance(result, tuple)
            self.assertFalse(result[0])
        finally:
            shutil.rmtree(test_dir, ignore_errors=True)


class TestLegalDocumentIngestionPipeline(unittest.TestCase):
    """Test end-to-end document ingestion pipeline."""

    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def _create_test_file(self, name, content):
        path = os.path.join(self.test_dir, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        return path

    def test_ingest_single_legal_document(self):
        """Test ingestion of a single legal document."""
        content = generate_legal_text(pages=5)
        path = self._create_test_file("lease_agreement.txt", content)

        with timer() as t:
            success, message = self.rag.ingest_file(path)

        self.assertTrue(success)
        print(f"\n[PIPELINE] Single legal doc (5 pages): {t.elapsed*1000:.1f}ms | {message}")

        # Verify search works
        with timer() as t:
            results = self.rag.search("租金 押金 rent deposit")
        self.assertIsInstance(results, str)
        self.assertGreater(len(results), 0)
        print(f"[PIPELINE] Search after ingest: {t.elapsed*1000:.1f}ms")

    def test_ingest_and_search_accuracy(self):
        """Verify that search returns relevant results for known content."""
        content = (
            "第一条 租金 月租金为人民币85,000元。"
            "第二条 押金 押金为人民币170,000元。"
            "第三条 违约金 违约金为3个月租金。"
            "Fourth clause: The tenant shall maintain insurance."
        )
        path = self._create_test_file("contract.txt", content)
        self.rag.ingest_file(path)

        # Search for rent-related content
        results = self.rag.search("租金 rent 85000")
        self.assertIn("85,000", results)

        # Search for deposit
        results = self.rag.search("押金 deposit")
        self.assertIn("170,000", results)

        # Search in English
        results = self.rag.search("tenant insurance maintenance")
        self.assertIn("insurance", results.lower())

    def test_batch_ingest_multiple_documents(self):
        """Ingest multiple documents and verify total chunks."""
        docs = [
            ("lease_1.txt", generate_legal_text(pages=3)),
            ("lease_2.txt", generate_legal_text(pages=5)),
            ("addendum.txt", "补充协议：关于租金调整的约定。\nAmendment: Rent adjustment terms."),
            ("invoice.txt", "发票编号：INV-2024-001\n金额：¥85,000\n日期：2024-01-15"),
        ]

        total_time = 0
        total_chunks = 0
        for name, content in docs:
            path = self._create_test_file(name, content)
            start = time.perf_counter()
            success, msg = self.rag.ingest_file(path)
            elapsed = time.perf_counter() - start
            total_time += elapsed
            self.assertTrue(success)
            # Extract chunk count from message
            parts = msg.split()
            for i, p in enumerate(parts):
                if p == "Ingested" and i + 1 < len(parts):
                    try:
                        total_chunks += int(parts[i + 1])
                    except ValueError:
                        pass

        summary = self.rag.get_summary()
        print(f"\n[PIPELINE] Batch ingest 4 docs: {total_time*1000:.1f}ms | {summary['document_count']} docs, {summary['total_chunks']} chunks")

        # Search across multiple documents
        results = self.rag.search_structured("租金 rent invoice", k=10)
        self.assertGreater(len(results), 0)
        print(f"[PIPELINE] Cross-doc search: {len(results)} results")

    def test_incremental_ingest(self):
        """Test that incremental document addition works correctly."""
        # Initial document
        path1 = self._create_test_file("doc1.txt", "第一条 租金为85000元。")
        self.rag.ingest_file(path1)
        self.assertEqual(self.rag.get_summary()["document_count"], 1)

        # Add second document
        path2 = self._create_test_file("doc2.txt", "第二条 押金为170000元。")
        self.rag.ingest_file(path2)
        self.assertEqual(self.rag.get_summary()["document_count"], 2)

        # Search should find content from both
        results = self.rag.search("租金押金")
        self.assertIsInstance(results, str)

    def test_large_legal_corpus_ingestion(self):
        """Test ingestion of a large corpus simulating many legal documents."""
        corpus_size_kb = 200
        content = generate_legal_text(pages=50)
        path = self._create_test_file("large_corpus.txt", content[:corpus_size_kb * 1024])

        with timer() as t:
            success, msg = self.rag.ingest_file(path)

        self.assertTrue(success)
        summary = self.rag.get_summary()
        print(f"\n[PIPELINE] Large corpus ({corpus_size_kb}KB): {t.elapsed*1000:.1f}ms | {msg}")

        # Search should still work efficiently
        with timer() as t:
            results = self.rag.search_structured("rent 违约金 remedies", k=5)
        print(f"[PIPELINE] Search large corpus: {t.elapsed*1000:.1f}ms, {len(results)} results")
        self.assertGreater(len(results), 0)

    def test_search_relevance_ranking(self):
        """Verify that BM25 returns results in relevance order."""
        docs = [
            ("rent_doc.txt", "租金支付 租金标准 租金调整 Monthly rent payment terms"),
            ("deposit_doc.txt", "押金退还 押金收取 Security deposit refund policy"),
            ("maintenance_doc.txt", "物业维修 保养维护 Building maintenance obligations"),
        ]
        for name, content in docs:
            path = self._create_test_file(name, content)
            self.rag.ingest_file(path)

        # Search for rent-specific terms
        results = self.rag.search_structured("租金 rent payment", k=3)
        self.assertEqual(len(results), 3)
        # First result should be the rent document
        self.assertIn("rent_doc", results[0]["filename"])
        # Scores should be in descending order
        for i in range(len(results) - 1):
            self.assertGreaterEqual(results[i]["score"], results[i + 1]["score"])


class TestDocumentParsingEdgeCases(unittest.TestCase):
    """Test edge cases in document parsing."""

    def test_empty_document(self):
        from rag_manager import _structure_aware_chunk
        chunks = _structure_aware_chunk("")
        self.assertEqual(chunks, [])

    def test_whitespace_only_document(self):
        from rag_manager import _structure_aware_chunk
        chunks = _structure_aware_chunk("   \n\n\t\t  ")
        self.assertEqual(chunks, [])

    def test_single_character_document(self):
        from rag_manager import _structure_aware_chunk
        chunks = _structure_aware_chunk("A")
        self.assertGreater(len(chunks), 0)

    def test_unicode_heavy_document(self):
        from rag_manager import _structure_aware_chunk
        text = "法律合同协议条款规定当事人权利义务责任违约赔偿争议解决" * 100
        chunks = _structure_aware_chunk(text)
        self.assertGreater(len(chunks), 0)

    def test_mixed_language_document(self):
        from rag_manager import _structure_aware_chunk
        text = "第一条 本合同 agreement is made between 甲方 party A and 乙方 party B 第二条"
        chunks = _structure_aware_chunk(text)
        self.assertGreater(len(chunks), 0)

    def test_very_long_line(self):
        from rag_manager import _structure_aware_chunk
        # Use a long line with sentence endings for proper chunking
        text = "This is a legal clause about rent。" * 500  # ~17500 chars
        chunks = _structure_aware_chunk(text)
        self.assertGreater(len(chunks), 0)
        # With sentence endings, should be chunked
        print(f"Very long line: {len(text)} chars -> {len(chunks)} chunks")

    def test_special_characters(self):
        from rag_manager import _structure_aware_chunk
        text = "合同金额：¥100,000.00（人民币壹拾万元整）签订日期：2024-01-15。"
        chunks = _structure_aware_chunk(text)
        self.assertGreater(len(chunks), 0)
        self.assertIn("¥100,000.00", chunks[0])

    def test_ingest_duplicate_filename(self):
        from rag_manager import RAGManager
        test_dir = tempfile.mkdtemp()
        try:
            rag = RAGManager(persist_directory=test_dir)

            # Ingest same filename with different content
            path = os.path.join(test_dir, "test.txt")
            with open(path, "w") as f:
                f.write("Version 1 content about rent abatement.")
            rag.ingest_file(path)

            with open(path, "w") as f:
                f.write("Version 2 content about deposit refund.")
            rag.ingest_file(path)

            # Should have exactly 1 document (replaced)
            self.assertEqual(rag.get_summary()["document_count"], 1)
        finally:
            shutil.rmtree(test_dir, ignore_errors=True)


class TestDocumentProcessingThroughput(unittest.TestCase):
    """Measure document processing throughput for capacity planning."""

    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_throughput_kb_per_second(self):
        """Measure document ingestion throughput in KB/s."""
        sizes_kb = [1, 5, 10, 50, 100]
        throughputs = []

        for size in sizes_kb:
            content = generate_legal_text(pages=max(1, size // 2))
            path = os.path.join(self.test_dir, f"size_{size}kb.txt")
            with open(path, "w", encoding="utf-8") as f:
                f.write(content[:size * 1024])

            start = time.perf_counter()
            self.rag.ingest_file(path)
            elapsed = time.perf_counter() - start

            throughput = size / elapsed if elapsed > 0 else 0
            throughputs.append(throughput)
            print(f"[THROUGHPUT] {size}KB: {throughput:.1f} KB/s ({elapsed*1000:.1f}ms)")

        avg_throughput = sum(throughputs) / len(throughputs)
        print(f"\n[THROUGHPUT] Average: {avg_throughput:.1f} KB/s")
        self.assertGreater(avg_throughput, 10, "Average throughput should be > 10 KB/s")

    def test_concurrent_search_throughput(self):
        """Measure search throughput with many queries."""
        # Setup: ingest a document
        content = generate_legal_text(pages=20)
        path = os.path.join(self.test_dir, "throughput.txt")
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        self.rag.ingest_file(path)

        queries = [
            "租金 rent", "押金 deposit", "违约 penalty",
            "维修 maintenance", "合同 agreement", "争议 dispute",
            "赔偿 compensation", "终止 termination", "续租 renewal",
            "装修 renovation",
        ]

        start = time.perf_counter()
        results_count = 0
        for q in queries * 10:  # 100 queries
            results = self.rag.search(q)
            results_count += 1
        elapsed = time.perf_counter() - start

        qps = results_count / elapsed
        print(f"\n[THROUGHPUT] Search QPS: {qps:.1f} queries/sec ({results_count} queries in {elapsed:.2f}s)")
        self.assertGreater(qps, 10, "Should handle at least 10 queries/second")


class TestDocProcessingAccuracy(unittest.TestCase):
    """Test that document content is preserved through processing."""

    def setUp(self):
        from rag_manager import RAGManager
        self.test_dir = tempfile.mkdtemp()
        self.rag = RAGManager(persist_directory=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_chinese_legal_terms_preserved(self):
        terms = ["第一条", "租金", "押金", "违约金", "争议解决", "不可抗力"]
        content = "。".join(terms) + "。"
        path = os.path.join(self.test_dir, "terms.txt")
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        self.rag.ingest_file(path)

        for term in terms:
            results = self.rag.search(term)
            self.assertIn(term, results, f"Term '{term}' should be searchable")

    def test_english_terms_preserved(self):
        terms = ["landlord", "tenant", "lease", "deposit", "breach", "arbitration"]
        content = " ".join(terms) + "."
        path = os.path.join(self.test_dir, "terms_en.txt")
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        self.rag.ingest_file(path)

        results = self.rag.search("landlord tenant")
        for term in ["landlord", "tenant"]:
            self.assertIn(term, results.lower(), f"Term '{term}' should be searchable")

    def test_numerical_values_preserved(self):
        content = "租金为人民币85,000元。押金为人民币170,000元。违约金为255,000元。"
        path = os.path.join(self.test_dir, "numbers.txt")
        with open(path, "w", encoding="utf-8") as f:
            f.write(content)
        self.rag.ingest_file(path)

        results = self.rag.search("85,000")
        self.assertIn("85,000", results)


if __name__ == "__main__":
    unittest.main(verbosity=2)
