#!/usr/bin/env python3
"""Upload multiple files via POST /v1/upload, poll /v1/jobs/{id}, print classification."""

from __future__ import annotations

import json
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path


def post_upload(api: str, path: Path) -> str:
    boundary = "----agentflowBatch"
    with path.open("rb") as f:
        raw = f.read()
    body = (
        f"--{boundary}\r\n"
        f'Content-Disposition: form-data; name="file"; filename="{path.name}"\r\n'
        f"Content-Type: application/octet-stream\r\n\r\n".encode()
        + raw
        + f"\r\n--{boundary}--\r\n".encode()
    )
    req = urllib.request.Request(
        f"{api}/upload",
        data=body,
        method="POST",
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
    )
    with urllib.request.urlopen(req, timeout=600) as resp:
        data = json.loads(resp.read().decode())
    return data["job_id"]


def poll_job(api: str, job_id: str, timeout_s: int = 600) -> dict:
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(f"{api}/jobs/{job_id}", timeout=60) as resp:
                job = json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            if e.code == 404:
                time.sleep(1)
                continue
            raise
        st = job.get("status")
        if st in ("completed", "failed"):
            return job
        time.sleep(1.5)
    return {"status": "failed", "error": "poll timeout", "id": job_id}


def main() -> None:
    argv = sys.argv[1:]
    if len(argv) < 2:
        print("Usage: batch_upload_classify_test.py BASE_URL file1.png [file2.png ...]", file=sys.stderr)
        print("Example: batch_upload_classify_test.py http://127.0.0.1:8020/v1 ./a.png ./b.png", file=sys.stderr)
        sys.exit(2)
    api = argv[0].rstrip("/")
    paths = [Path(p) for p in argv[1:]]

    rows = []
    for p in paths:
        if not p.is_file():
            print(f"skip missing: {p}", file=sys.stderr)
            continue
        print(f"upload {p.name} ...", flush=True)
        jid = post_upload(api, p)
        job = poll_job(api, jid)
        r = job.get("result") if job.get("status") == "completed" else None
        c = (r or {}).get("classification") if isinstance(r, dict) else None
        rows.append(
            {
                "file": p.name,
                "job_status": job.get("status"),
                "error": job.get("error"),
                "document_type": (c or {}).get("document_type"),
                "display_name_zh": (c or {}).get("display_name_zh"),
                "confidence": (c or {}).get("confidence"),
                "case_id": (r or {}).get("case_id"),
                "text_length": (r or {}).get("text_length"),
            }
        )
        if isinstance(c, dict):
            print(f"  -> {c.get('document_type')} / {c.get('display_name_zh')}", flush=True)
        else:
            print(f"  -> no classification", flush=True)

    print(json.dumps(rows, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
