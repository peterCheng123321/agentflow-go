import argparse
import asyncio
import os
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(__file__))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

from agent_flow import LegalAgentMachine
from rag_manager import RAGManager


PDF_TEXT = """
COMMERCIAL LEASE DISPUTE TEST PDF

Section 1 Property Delivery
The landlord shall deliver the premises in usable condition and complete all agreed HVAC work.

Section 2 Rent Abatement
If the premises cannot be used for normal office operations because of incomplete landlord work or air conditioning failure,
the tenant may request rent abatement for the affected period.

Section 3 Evidence
Relevant evidence includes handover checklists, maintenance tickets, tenant complaints, and photographs.

Section 4 Remedies
The tenant may demand repairs, rent reduction, reimbursement of temporary office costs, and written confirmation of completion.
"""


class FakeSmallLLM:
    def generate_structured_json(self, prompt, context="", profile="small", fallback=None):
        objective = "draft" if "Objective: draft" in prompt else "evaluation"
        return {
            "objective": objective,
            "triage_summary": "Retrieve PDF-derived lease facts before generating a response.",
            "rag_queries": ["rent abatement hvac landlord work", "repair costs and evidence"],
            "tool_calls": [{"tool_name": "search_rag", "args": {"query": "rent abatement hvac landlord work"}, "reason": "Collect PDF-backed facts."}],
            "final_instruction": "Ground the output in the uploaded PDF.",
        }

    def generate(self, prompt, context=""):
        return f"SMOKE OUTPUT\n{prompt}\n{context[:300]}"


def create_smoke_pdf(output_path: str):
    import pymupdf

    doc = pymupdf.open()
    page = doc.new_page(width=595, height=842)
    page.insert_textbox((40, 40, 555, 802), PDF_TEXT, fontsize=12)
    doc.save(output_path)
    doc.close()


async def run_smoke(use_real_model: bool):
    with tempfile.TemporaryDirectory() as tmpdir:
        pdf_path = os.path.join(tmpdir, "lease_smoke.pdf")
        create_smoke_pdf(pdf_path)

        rag = RAGManager(persist_directory=os.path.join(tmpdir, "vector_store"))
        ok, message = rag.ingest_file(pdf_path)
        print({"ingested": ok, "message": message})
        print(rag.summarize_document("lease_smoke.pdf"))
        print(rag.grep_document("lease_smoke.pdf", "abatement"))
        print(rag.inspect_document("lease_smoke.pdf", start_line=1, window=10))

        llm = None if use_real_model else FakeSmallLLM()
        machine = LegalAgentMachine(rag=rag, llm=llm)
        case_id = machine.active_case_id
        machine.attach_document_to_case("lease_smoke.pdf", case_id)
        machine.update_case(case_id, client_name="PDF Smoke Client", matter_type="Commercial Lease Dispute", initial_msg="HVAC failed and rent abatement is requested.")
        await machine.execute_case_graph(case_id)
        await machine.execute_case_graph(case_id)
        await machine.execute_case_graph(case_id)
        print(machine.get_graph_status(case_id))
        print(machine.get_case_detail(case_id).get("evaluation_detail"))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run PDF + LangGraph smoke test.")
    parser.add_argument("--real-model", action="store_true", help="Use the configured real local model instead of the fake smoke LLM.")
    args = parser.parse_args()
    asyncio.run(run_smoke(use_real_model=args.real_model))
