"""
Microbenchmarks for the embedding sidecar. Doesn't replace the
end-to-end latency assertions in test_intent_router / test_agent_chat —
those measure the full classify path. This file isolates the embedder
itself so a regression there can't hide behind the rest of the system.
"""

from __future__ import annotations

import random
import statistics
import time

import numpy as np
import pytest
import requests


SHORT_PROMPTS_EN = [
    "What's the deadline for case 12345?",
    "Hi, how are you?",
    "Summarize the contract",
    "What does Section 3 say about indemnification?",
    "Schedule a meeting next week",
]
SHORT_PROMPTS_ZH = [
    "你好",
    "案件123的状态",
    "总结这份合同",
    "查找张三的案件",
    "什么时候开庭",
]


@pytest.mark.embed
def test_embed_single_query_latency(embed_sidecar):
    """One short query at a time. Warm the connection first."""
    # warm
    requests.post(f"{embed_sidecar}/api/embed", json={"input": ["warm"]}, timeout=10)

    seed = random.randint(0, 1_000_000)
    rng = random.Random(seed)
    prompts = (SHORT_PROMPTS_EN + SHORT_PROMPTS_ZH) * 5  # 50 calls
    rng.shuffle(prompts)

    latencies: list[float] = []
    for p in prompts:
        t0 = time.perf_counter()
        r = requests.post(f"{embed_sidecar}/api/embed", json={"input": [p]}, timeout=5)
        latencies.append((time.perf_counter() - t0) * 1000)
        r.raise_for_status()

    p50 = statistics.median(latencies)
    p95 = sorted(latencies)[int(0.95 * len(latencies))]
    p99 = sorted(latencies)[int(0.99 * len(latencies))]
    print(
        f"\nembed single-query latency over {len(latencies)} calls (seed={seed}): "
        f"p50={p50:.1f}ms  p95={p95:.1f}ms  p99={p99:.1f}ms  max={max(latencies):.1f}ms"
    )

    # Bars: warm e5-small on M-series ~ 5-15ms typical. Allow generous
    # headroom for the HTTP framing + JSON marshal.
    assert p50 < 30.0, f"p50 {p50:.1f}ms > 30ms — embed sidecar slowed down"
    assert p95 < 80.0, f"p95 {p95:.1f}ms > 80ms — tail latency regressed"


@pytest.mark.embed
def test_embed_batch_amortizes(embed_sidecar):
    """
    Embedding 25 prompts in one request should be faster than 25
    sequential single-prompt requests. Validates batched ingest is a real
    optimization for the RAG embed-on-ingest path.
    """
    prompts = (SHORT_PROMPTS_EN + SHORT_PROMPTS_ZH) * 3   # 30 prompts
    prompts = prompts[:25]

    # Sequential
    t0 = time.perf_counter()
    for p in prompts:
        requests.post(f"{embed_sidecar}/api/embed", json={"input": [p]}, timeout=10).raise_for_status()
    seq_ms = (time.perf_counter() - t0) * 1000

    # Batched
    t0 = time.perf_counter()
    r = requests.post(f"{embed_sidecar}/api/embed", json={"input": prompts}, timeout=15)
    r.raise_for_status()
    batch_ms = (time.perf_counter() - t0) * 1000
    assert len(r.json()["embeddings"]) == len(prompts)

    print(f"\nembed batch vs seq for {len(prompts)} prompts: batch={batch_ms:.0f}ms  seq={seq_ms:.0f}ms  speedup={seq_ms/batch_ms:.1f}×")
    assert batch_ms * 2 < seq_ms, f"batch ({batch_ms:.0f}ms) didn't beat sequential ({seq_ms:.0f}ms) by ≥2×"


@pytest.mark.embed
def test_embed_is_deterministic(embed_sidecar):
    """Same input → byte-identical embeddings (within float32 tolerance)."""
    prompts = ["Hi, how are you?", "你好", "Summarize the contract"]
    a = np.asarray(
        requests.post(f"{embed_sidecar}/api/embed", json={"input": prompts}, timeout=10).json()["embeddings"]
    )
    b = np.asarray(
        requests.post(f"{embed_sidecar}/api/embed", json={"input": prompts}, timeout=10).json()["embeddings"]
    )
    assert a.shape == b.shape
    diff = np.max(np.abs(a - b))
    assert diff < 1e-5, f"non-determinism in embeddings (max abs diff = {diff:.2e})"
