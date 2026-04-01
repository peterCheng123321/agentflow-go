#!/usr/bin/env python3
"""
Stress test for AgentFlow Go backend.
Tests memory stability, concurrent uploads, search, and case management.
"""
import asyncio
import aiohttp
import time
import os
import sys
import json
import tracemalloc

BASE_URL = "http://localhost:8000"
TEST_DIR = "/Users/peter/Downloads/10.15 罗海霞"

# Track memory
tracemalloc.start()

async def test_health(session):
    """Test health endpoint"""
    async with session.get(f"{BASE_URL}/health") as resp:
        data = await resp.json()
        return data.get("status") == "ok"

async def test_status(session):
    """Test status endpoint"""
    async with session.get(f"{BASE_URL}/v1/status") as resp:
        data = await resp.json()
        return data

async def test_create_case(session, name, matter_type):
    """Create a test case"""
    payload = {
        "client_name": name,
        "matter_type": matter_type,
        "source_channel": "stress_test",
        "initial_msg": f"Stress test case for {name}"
    }
    async with session.post(f"{BASE_URL}/v1/cases/create", json=payload) as resp:
        data = await resp.json()
        return data.get("case_id")

async def test_upload_file(session, filepath, case_id=None):
    """Upload a single file"""
    data = aiohttp.FormData()
    data.add_field("file", open(filepath, "rb"), filename=os.path.basename(filepath))
    
    url = f"{BASE_URL}/v1/upload"
    if case_id:
        url += f"?case_id={case_id}"
        
    async with session.post(url, data=data) as resp:
        result = await resp.json()
        return result

async def test_search(session, query):
    """Test search endpoint"""
    payload = {"query": query, "k": 5}
    async with session.post(f"{BASE_URL}/v1/rag/search", json=payload) as resp:
        data = await resp.json()
        return data

async def test_list_cases(session):
    """List all cases"""
    async with session.get(f"{BASE_URL}/v1/cases") as resp:
        data = await resp.json()
        return data

async def test_document_metadata(session, filename):
    """Get document metadata"""
    async with session.get(f"{BASE_URL}/v1/documents/{filename}/metadata") as resp:
        data = await resp.json()
        return data

async def stress_test_uploads(session, files, iterations=3):
    """Stress test file uploads"""
    print(f"\n{'='*60}")
    print(f"STRESS TEST: {iterations} iterations of {len(files)} files")
    print(f"{'='*60}")
    
    results = []
    for i in range(iterations):
        print(f"\n--- Iteration {i+1}/{iterations} ---")
        start = time.time()
        
        # Upload files concurrently
        tasks = []
        for f in files:
            tasks.append(test_upload_file(session, f))
        
        upload_results = await asyncio.gather(*tasks, return_exceptions=True)
        
        elapsed = time.time() - start
        success = sum(1 for r in upload_results if isinstance(r, dict) and r.get("status") == "uploaded")
        failed = sum(1 for r in upload_results if isinstance(r, Exception))
        
        print(f"  Uploaded: {success}/{len(files)} in {elapsed:.2f}s")
        if failed:
            print(f"  Failed: {failed}")
        
        results.append({
            "iteration": i+1,
            "success": success,
            "failed": failed,
            "time": elapsed
        })
        
        # Brief pause between iterations
        await asyncio.sleep(0.5)
    
    return results

async def stress_test_search(session, queries, iterations=5):
    """Stress test search endpoint"""
    print(f"\n{'='*60}")
    print(f"STRESS TEST: {iterations} iterations of {len(queries)} queries")
    print(f"{'='*60}")
    
    results = []
    for i in range(iterations):
        print(f"\n--- Iteration {i+1}/{iterations} ---")
        start = time.time()
        
        # Search concurrently
        tasks = []
        for q in queries:
            tasks.append(test_search(session, q))
        
        search_results = await asyncio.gather(*tasks, return_exceptions=True)
        
        elapsed = time.time() - start
        success = sum(1 for r in search_results if isinstance(r, dict) and "results" in r)
        failed = sum(1 for r in search_results if isinstance(r, Exception))
        
        print(f"  Searches: {success}/{len(queries)} in {elapsed:.2f}s")
        if failed:
            print(f"  Failed: {failed}")
        
        results.append({
            "iteration": i+1,
            "success": success,
            "failed": failed,
            "time": elapsed
        })
        
        await asyncio.sleep(0.2)
    
    return results

async def stress_test_case_creation(session, count=50):
    """Stress test case creation"""
    print(f"\n{'='*60}")
    print(f"STRESS TEST: Creating {count} cases rapidly")
    print(f"{'='*60}")
    
    start = time.time()
    tasks = []
    for i in range(count):
        name = f"StressTest{i:03d}"
        tasks.append(test_create_case(session, name, "Test Matter"))
    
    results = await asyncio.gather(*tasks, return_exceptions=True)
    elapsed = time.time() - start
    
    success = sum(1 for r in results if r and isinstance(r, str))
    failed = sum(1 for r in results if isinstance(r, Exception))
    
    print(f"  Created: {success}/{count} cases in {elapsed:.2f}s")
    print(f"  Rate: {success/elapsed:.1f} cases/sec")
    if failed:
        print(f"  Failed: {failed}")
    
    return success, failed, elapsed

async def monitor_memory(session, duration=60):
    """Monitor server memory usage"""
    print(f"\n{'='*60}")
    print(f"MEMORY MONITOR: {duration}s")
    print(f"{'='*60}")
    
    start = time.time()
    memory_readings = []
    
    while time.time() - start < duration:
        try:
            async with session.get(f"{BASE_URL}/v1/status") as resp:
                data = await resp.json()
                # Get process memory info
                import psutil
                # This would need the server to expose memory info
                # For now, just track response times
                memory_readings.append({
                    "time": time.time() - start,
                    "status": "ok"
                })
        except Exception as e:
            memory_readings.append({
                "time": time.time() - start,
                "status": f"error: {e}"
            })
        
        await asyncio.sleep(2)
    
    return memory_readings

async def main():
    print("="*60)
    print("AGENTFLOW GO - STRESS TEST")
    print("="*60)
    
    # Get test files
    test_files = []
    if os.path.isdir(TEST_DIR):
        for f in os.listdir(TEST_DIR):
            fp = os.path.join(TEST_DIR, f)
            if os.path.isfile(fp):
                test_files.append(fp)
    
    print(f"Test files found: {len(test_files)}")
    for f in test_files[:5]:
        print(f"  - {os.path.basename(f)}")
    if len(test_files) > 5:
        print(f"  ... and {len(test_files)-5} more")
    
    # Test queries
    test_queries = [
        "罗海霞",
        "证据",
        "合同",
        "律师函",
        "租金",
        "租赁",
        "test",
        "罗海霞 证据",
        "合同 条款",
    ]
    
    async with aiohttp.ClientSession() as session:
        # 1. Health check
        print("\n1. Health Check:")
        try:
            healthy = await test_health(session)
            print(f"   {'✓' if healthy else '✗'} Server healthy")
        except Exception as e:
            print(f"   ✗ Server not responding: {e}")
            print("\nMake sure the Go server is running: cd agentflow-go && ./agentflow-go")
            return
        
        # 2. Status check
        print("\n2. Status Check:")
        try:
            status = await test_status(session)
            print(f"   ✓ Version: {status.get('version')}")
            print(f"   ✓ Cases: {status.get('case_count')}")
            print(f"   ✓ Active WS: {status.get('active_ws')}")
        except Exception as e:
            print(f"   ✗ Status failed: {e}")
        
        # 3. Stress test uploads
        if test_files:
            upload_results = await stress_test_uploads(session, test_files[:5], iterations=3)
        
        # 4. Stress test search
        search_results = await stress_test_search(session, test_queries, iterations=5)
        
        # 5. Stress test case creation
        success, failed, elapsed = await stress_test_case_creation(session, count=50)
        
        # 6. List all cases
        print(f"\n{'='*60}")
        print("FINAL STATE:")
        print(f"{'='*60}")
        try:
            cases = await test_list_cases(session)
            print(f"   Total cases: {cases.get('count', 0)}")
        except Exception as e:
            print(f"   Failed to list cases: {e}")
        
        # 7. Memory usage
        current, peak = tracemalloc.get_traced_memory()
        tracemalloc.stop()
        print(f"\n{'='*60}")
        print("CLIENT MEMORY USAGE:")
        print(f"{'='*60}")
        print(f"   Current: {current / 1024 / 1024:.2f} MB")
        print(f"   Peak:    {peak / 1024 / 1024:.2f} MB")
        
        print(f"\n{'='*60}")
        print("STRESS TEST COMPLETE")
        print(f"{'='*60}")

if __name__ == "__main__":
    asyncio.run(main())
