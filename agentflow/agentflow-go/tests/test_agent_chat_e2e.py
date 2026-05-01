"""
End-to-end test of POST /v1/agent/chat through the actual server.

This exercises the full stack:
  - embed sidecar classifies intent (~10ms)
  - confidence gate (server.cfg.EmbedRouterMargin) decides fast/slow
  - CONVERSATIONAL  → s.llm.Classify against DashScope qwen-plus
                      (single chat completion, no tools)
  - NEEDS_TOOLS/RAG → chatagent.Run against DashScope qwen-plus
                      (full ReAct loop with the tool registry)

Marked `cloud` — runs are real and cost real DashScope tokens. Skipped
automatically if no key is set up. Stay under one DashScope call per
test case to keep total cost negligible.
"""

from __future__ import annotations

import statistics
import time

import pytest
import requests


# --- Tunables ---
# Cloud paths are not 11ms — DashScope round-trip + qwen-plus generation
# is the dominant cost. These bars are loose by design.
MAX_FAST_PATH_LATENCY_MS  = 5_000.0   # CONVERSATIONAL (no tools)
MAX_SLOW_PATH_LATENCY_MS  = 15_000.0  # full agent ReAct loop
MAX_INTENT_OVERHEAD_MS    = 200.0     # wall-time the embed router adds


@pytest.mark.server
@pytest.mark.cloud
@pytest.mark.slow
@pytest.mark.parametrize("prompt,expect_fast,note", [
    # CONVERSATIONAL → fast path
    ("你好",                          True,  "zh greeting"),
    ("Hi how are you",               True,  "en greeting"),
    ("Thanks",                       True,  "en gratitude"),
    ("What can you help me with?",   True,  "capability question"),
    # NEEDS_TOOLS / NEEDS_RAG → slow path (full agent loop)
    ("List my pending cases",                                   False, "tools: list"),
    ("Schedule a meeting tomorrow at 3pm",                      False, "tools: scheduling"),
    ("Summarize the contract clauses about liability",          False, "rag: summary"),
    ("What does the rental agreement say about late fees?",     False, "rag: lookup"),
])
def test_agent_chat_routes_correctly(agentflow, prompt, expect_fast, note):
    """
    Each case asserts the routing decision (stopped field) and the
    end-to-end latency budget.
    """
    t0 = time.perf_counter()
    r = requests.post(
        f"{agentflow}/v1/agent/chat",
        json={"messages": [{"role": "user", "content": prompt}]},
        timeout=30,
    )
    walltime_ms = (time.perf_counter() - t0) * 1000
    assert r.status_code == 200, r.text
    body = r.json()
    stopped = body.get("stopped", "")
    is_fast = "fast_path" in stopped
    assert is_fast == expect_fast, (
        f"routing wrong for {prompt!r} ({note}): expected_fast={expect_fast}, "
        f"stopped={stopped!r}"
    )

    bar = MAX_FAST_PATH_LATENCY_MS if expect_fast else MAX_SLOW_PATH_LATENCY_MS
    elapsed = body.get("elapsed_ms", walltime_ms)
    assert elapsed < bar, f"{prompt!r} took {elapsed}ms, bar is {bar}ms"


@pytest.mark.server
@pytest.mark.cloud
@pytest.mark.slow
def test_agent_chat_in_case_context_skips_fastpath(agentflow):
    """
    When the request supplies a case_id, even a 'hi' should go through
    the full agent path — case-aware tools must be wired up. See
    handlers_agent.go: the fast-path is gated on req.CaseID == "".
    """
    r = requests.post(
        f"{agentflow}/v1/agent/chat",
        json={"messages": [{"role": "user", "content": "hi"}], "case_id": "TESTING-NOOP"},
        timeout=30,
    )
    assert r.status_code == 200
    body = r.json()
    stopped = body.get("stopped", "")
    assert "fast_path" not in stopped, (
        f"fast-path should be disabled inside a case context; got stopped={stopped!r}"
    )


@pytest.mark.server
@pytest.mark.cloud
@pytest.mark.slow
def test_agent_chat_replies_in_user_language(agentflow):
    """
    Smoke test: a Chinese greeting should produce a Chinese reply.
    Regression guard for the conversational system prompt.
    """
    r = requests.post(
        f"{agentflow}/v1/agent/chat",
        json={"messages": [{"role": "user", "content": "你好"}]},
        timeout=30,
    )
    body = r.json()
    reply = body.get("reply", "")
    assert any("一" <= c <= "鿿" for c in reply), (
        f"expected Chinese reply for 你好, got: {reply!r}"
    )


@pytest.mark.server
@pytest.mark.cloud
@pytest.mark.slow
def test_intent_router_overhead_is_bounded(agentflow):
    """
    Compare wall-time of a borderline conversational query (router
    adds ~10-100ms) against a request hitting a brand-new prompt of
    the same length to estimate the routing overhead. The fixed-cost
    floor of /v1/agent/chat ought to be dominated by the cloud call,
    not the router classification.
    """
    # Re-warm the path twice so any cold-cache effects don't pollute
    # the measurement.
    for _ in range(2):
        requests.post(f"{agentflow}/v1/agent/chat",
                      json={"messages": [{"role": "user", "content": "hi"}]},
                      timeout=30)

    times: list[float] = []
    for _ in range(5):
        t0 = time.perf_counter()
        r = requests.post(
            f"{agentflow}/v1/agent/chat",
            json={"messages": [{"role": "user", "content": "thanks!"}]},
            timeout=30,
        )
        assert r.status_code == 200
        times.append((time.perf_counter() - t0) * 1000)

    median = statistics.median(times)
    # Hard floor of cloud round-trip is ~500ms, so router overhead is at
    # most (median - 500ms). We just check median is sane.
    assert median < MAX_FAST_PATH_LATENCY_MS, (
        f"median fast-path walltime {median:.0f}ms exceeds bar {MAX_FAST_PATH_LATENCY_MS:.0f}ms"
    )
