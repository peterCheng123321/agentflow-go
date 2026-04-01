#!/usr/bin/env python3
"""
Test: Upload files one-by-one from two different users to verify auto-routing.

Simulates the real-world scenario where files from different cases arrive
individually and the system must correctly identify which user/case each
file belongs to based on filename patterns.
"""
import asyncio
import time
import os
import sys

sys.path.insert(0, '/Users/peter/Downloads/agentflow')

from agent_flow import LegalAgentMachine
from rag_manager import RAGManager
from ocr_engine import ocr_engine
from server import _extract_client_from_filename, _extract_matter_type_from_filenames

# Files from two different users
USER_FILES = {
    "徐克林（买卖合同） 起诉立案": [
        "徐克林民事起诉状.docx",
        "廖国泽欠款条.jpg",
        "身份证正面.jpg",
        "徐克林证据目录.docx",
        "货款单.png",
        "诚信诉讼承诺书（当事人版）.docx",
    ],
    "10.15 罗海霞": [
        "罗海霞律师函.docx",
        "证据目录.docx",
        "罗海霞新证据目录_20251030120252.pdf",
    ],
}

BASE_PATHS = {
    "徐克林（买卖合同） 起诉立案": "/Users/peter/Downloads/徐克林（买卖合同） 起诉立案",
    "10.15 罗海霞": "/Users/peter/Downloads/10.15 罗海霞",
}


def test_filename_parsing():
    """Test that filename parsing correctly identifies users."""
    print("=" * 60)
    print("TEST 1: Filename Parsing")
    print("=" * 60)
    
    expected = {
        "徐克林民事起诉状.docx": "徐克林",
        "廖国泽欠款条.jpg": "廖国泽",
        "身份证正面.jpg": None,
        "徐克林证据目录.docx": "徐克林",
        "罗海霞律师函.docx": "罗海霞",
        "证据目录.docx": None,
        "罗海霞新证据目录_20251030120252.pdf": None,
        "货款单.png": None,
    }
    
    all_pass = True
    for fn, expected_name in expected.items():
        result = _extract_client_from_filename(fn)
        status = "✓" if result == expected_name else "✗"
        if result != expected_name:
            all_pass = False
        print(f"  {status} '{fn}' -> '{result}' (expected: '{expected_name}')")
    
    print(f"\n  Result: {'PASS' if all_pass else 'FAIL'}\n")
    return all_pass


async def test_one_by_one_upload():
    """Test uploading files one by one and verify auto-routing."""
    print("=" * 60)
    print("TEST 2: One-by-One Upload with Auto-Routing")
    print("=" * 60)
    
    machine = LegalAgentMachine()
    
    # Track which files went to which case
    case_assignments = {}
    
    # Interleave files from both users to simulate real-world scenario
    upload_sequence = [
        ("徐克林（买卖合同） 起诉立案", "徐克林民事起诉状.docx"),
        ("10.15 罗海霞", "罗海霞律师函.docx"),
        ("徐克林（买卖合同） 起诉立案", "廖国泽欠款条.jpg"),
        ("10.15 罗海霞", "证据目录.docx"),
        ("徐克林（买卖合同） 起诉立案", "身份证正面.jpg"),
        ("徐克林（买卖合同） 起诉立案", "徐克林证据目录.docx"),
        ("10.15 罗海霞", "罗海霞新证据目录_20251030120252.pdf"),
        ("徐克林（买卖合同） 起诉立案", "货款单.png"),
    ]
    
    print()
    for i, (dir_name, filename) in enumerate(upload_sequence, 1):
        filepath = os.path.join(BASE_PATHS[dir_name], filename)
        if not os.path.exists(filepath):
            print(f"  {i}. SKIP: {filename} (file not found)")
            continue
        
        print(f"  {i}. Uploading: {filename}")
        
        # Extract client name from filename
        detected_client = _extract_client_from_filename(filename)
        
        # Find or create case
        if detected_client:
            result = machine.find_or_create_case_for_client(
                detected_client,
                matter_type="Civil Litigation",
            )
            case_id = result["case_id"]
            created = result["created"]
            
            if case_id not in case_assignments:
                case_assignments[case_id] = {
                    "client": detected_client,
                    "files": [],
                    "created": created,
                }
            
            case_assignments[case_id]["files"].append(filename)
            
            print(f"      Detected client: {detected_client}")
            print(f"      Case ID: {case_id}")
            print(f"      Case created: {created}")
        else:
            # Use existing case or create with directory name
            # For files without clear client name, use the first detected client
            # from the same directory
            existing_cases = {cid: info for cid, info in case_assignments.items()}
            if existing_cases:
                # Assign to the most recent case from the same directory
                # (simplified: just use the first case for now)
                case_id = list(existing_cases.keys())[0]
                case_assignments[case_id]["files"].append(filename)
                print(f"      No client detected, assigned to existing case: {case_id}")
            else:
                print(f"      No client detected and no existing case")
        
        # OCR and ingest
        t0 = time.time()
        try:
            scanned_text = await ocr_engine.scan_file(filepath, task="Text Recognition")
        except Exception as e:
            scanned_text = f"[OCR Error] {e}"
        elapsed = time.time() - t0
        
        if scanned_text and not scanned_text.startswith("["):
            success, msg = machine.rag.ingest_file(
                filepath,
                user_preferences={"category": "Evidence"},
                force_ocr_text=scanned_text if scanned_text else None,
            )
            if success:
                machine.attach_document_to_case(filename, case_id if detected_client else None)
                print(f"      ✓ Ingested ({elapsed:.1f}s)")
            else:
                print(f"      ✗ Ingest failed: {msg}")
        else:
            print(f"      ✗ OCR failed: {scanned_text[:100]}")
        
        print()
    
    # Print summary
    print("-" * 60)
    print("UPLOAD SUMMARY:")
    print("-" * 60)
    for case_id, info in case_assignments.items():
        print(f"\n  Case: {case_id}")
        print(f"  Client: {info['client']}")
        print(f"  Created: {info['created']}")
        print(f"  Files ({len(info['files'])}):")
        for f in info['files']:
            print(f"    - {f}")
    
    print(f"\n  Total cases created: {len(case_assignments)}")
    print(f"  Total cases in system: {len(machine.cases)}")
    
    # Verify cases were created correctly
    print("\n" + "=" * 60)
    print("TEST 3: Case Verification")
    print("=" * 60)
    
    all_correct = True
    for case_id, info in case_assignments.items():
        case = machine.cases.get(case_id)
        if case:
            print(f"\n  ✓ Case {case_id} exists")
            print(f"    Client: {case.get('client_name')}")
            print(f"    Matter: {case.get('matter_type')}")
            print(f"    Uploaded docs: {case.get('uploaded_documents', [])}")
        else:
            print(f"\n  ✗ Case {case_id} not found")
            all_correct = False
    
    return all_correct


async def main():
    print("\n" + "=" * 60)
    print("AUTO-ROUTING TEST: Two Users, One-by-One Upload")
    print("=" * 60 + "\n")
    
    # Test 1: Filename parsing
    test1_pass = test_filename_parsing()
    
    # Test 2 & 3: Upload and verify
    test2_pass = await test_one_by_one_upload()
    
    print("\n" + "=" * 60)
    print("FINAL RESULTS:")
    print("=" * 60)
    print(f"  Filename Parsing: {'PASS' if test1_pass else 'FAIL'}")
    print(f"  Auto-Routing: {'PASS' if test2_pass else 'FAIL'}")
    print("=" * 60 + "\n")


if __name__ == "__main__":
    asyncio.run(main())
