#!/usr/bin/env python3
"""One-off: health latency + directory batch job timing. Run server separately or use --serve."""
from __future__ import annotations

import argparse
import json
import os
import shutil
import statistics
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def http_json(method: str, url: str, body: dict | None = None, timeout: float = 30.0) -> tuple[int, dict]:
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        raw = json.dumps(body).encode("utf-8")
        data = raw
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            code = resp.getcode()
            text = resp.read().decode("utf-8", errors="replace")
            return code, json.loads(text) if text.strip() else {}
    except urllib.error.HTTPError as e:
        text = e.read().decode("utf-8", errors="replace")
        try:
            return e.code, json.loads(text) if text.strip() else {}
        except json.JSONDecodeError:
            return e.code, {"_raw": text}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default="http://127.0.0.1:18080", help="Server base URL")
    ap.add_argument("--source", required=True, help="Absolute path to folder to copy under data/")
    ap.add_argument("--poll", type=float, default=0.5, help="Seconds between job polls")
    ap.add_argument("--max-wait", type=int, default=3600, help="Max seconds for job completion")
    args = ap.parse_args()

    base = args.base.rstrip("/")
    data_dir = os.path.join(ROOT, "data")
    os.makedirs(data_dir, exist_ok=True)
    run_id = f"bench_{int(time.time())}"
    dest = os.path.join(data_dir, "processing_test", run_id)
    os.makedirs(os.path.dirname(dest), exist_ok=True)

    print(f"[setup] copying corpus -> {dest}")
    t0 = time.perf_counter()
    shutil.copytree(args.source, dest, dirs_exist_ok=False)
    copy_s = time.perf_counter() - t0
    print(f"[setup] copy done in {copy_s:.2f}s")

    abs_dest = os.path.abspath(dest)

    latencies_ms: list[float] = []
    for i in range(15):
        t1 = time.perf_counter()
        code, _ = http_json("GET", f"{base}/health", timeout=5.0)
        dt = (time.perf_counter() - t1) * 1000
        latencies_ms.append(dt)
        if code != 200:
            print(f"[health] attempt {i+1} failed: HTTP {code}", file=sys.stderr)
            shutil.rmtree(dest, ignore_errors=True)
            return 1
    print(
        f"[health] 15x GET /health: min={min(latencies_ms):.1f}ms "
        f"p50={statistics.median(latencies_ms):.1f}ms max={max(latencies_ms):.1f}ms"
    )

    t1 = time.perf_counter()
    code, status = http_json("GET", f"{base}/v1/status", timeout=10.0)
    status_ms = (time.perf_counter() - t1) * 1000
    if code != 200:
        print(f"[status] HTTP {code}", file=sys.stderr)
    else:
        print(f"[status] GET /v1/status in {status_ms:.1f}ms (cases={status.get('case_count')})")

    body = {"directory_path": abs_dest}
    t_start = time.perf_counter()
    code, accepted = http_json("POST", f"{base}/v1/upload/directory", body, timeout=60.0)
    if code not in (200, 202):
        print(f"[batch] POST directory failed: HTTP {code} {accepted}", file=sys.stderr)
        shutil.rmtree(dest, ignore_errors=True)
        return 1
    job_id = accepted.get("job_id")
    if not job_id:
        print(f"[batch] no job_id: {accepted}", file=sys.stderr)
        shutil.rmtree(dest, ignore_errors=True)
        return 1
    accept_ms = (time.perf_counter() - t_start) * 1000
    print(f"[batch] accepted job_id={job_id} in {accept_ms:.1f}ms")

    deadline = time.time() + args.max_wait
    last_prog = -1
    while time.time() < deadline:
        code, job = http_json("GET", f"{base}/v1/jobs/{job_id}", timeout=30.0)
        if code != 200:
            print(f"[batch] poll failed HTTP {code}", file=sys.stderr)
            time.sleep(args.poll)
            continue
        st = job.get("status", "")
        pr = job.get("progress", 0)
        if pr != last_prog:
            print(f"[batch] status={st} progress={pr}%")
            last_prog = pr
        if st in ("completed", "failed"):
            elapsed = time.perf_counter() - t_start
            print(f"[batch] finished status={st} in {elapsed:.1f}s")
            if st == "failed":
                print(f"[batch] error: {job.get('error', '')}")
            if job.get("result") is not None:
                r = job["result"]
                if isinstance(r, dict):
                    print(
                        f"[batch] result: count={r.get('count')} uploaded={len(r.get('uploaded') or [])} "
                        f"failed={len(r.get('failed') or [])}"
                    )
            shutil.rmtree(dest, ignore_errors=True)
            print(f"[cleanup] removed {dest}")
            return 0 if st == "completed" else 2
        time.sleep(args.poll)

    print("[batch] timeout", file=sys.stderr)
    shutil.rmtree(dest, ignore_errors=True)
    print(f"[cleanup] removed {dest}")
    return 3


if __name__ == "__main__":
    raise SystemExit(main())
