"""
Matter-type router accuracy. Same shape as test_intent_router, but with
the legal-matter corpus. Note tighter margin distribution — these
prompts are filenames, not sentences, so the model has less signal.
"""

from __future__ import annotations

import random
import statistics
from collections import defaultdict

import pytest

from tests._corpus import MATTER_CORPUS, MATTER_MARGIN
from tests._router import Router
from tests.data.matter_eval import MATTER_EVAL


# Lower bars than intent — matter classification is harder (short
# filenames, semantically-overlapping legal categories) and is gated
# behind a fallback to keyword hints. We measure raw router accuracy
# here, not the production extract path.
MIN_OVERALL_ACCURACY = 0.75
MAX_P50_LATENCY_MS = 50.0
MAX_P95_LATENCY_MS = 150.0
SHUFFLE_SEED = 4242


@pytest.fixture(scope="module")
def matter_router(embed_sidecar):
    return Router(embed_sidecar, MATTER_CORPUS)


@pytest.mark.embed
def test_matter_corpus_coverage(matter_router):
    labels = {r.label for r in matter_router.refs}
    expected = {
        "Civil Litigation",
        "Contract Dispute",
        "Sales Contract Dispute",
        "Debt Dispute",
        "Loan Dispute",
        "Lease Dispute",
        "Commercial Lease Dispute",
        "Labor Dispute",
    }
    missing = expected - labels
    assert not missing, f"matter corpus missing labels: {missing}"


@pytest.mark.embed
def test_matter_accuracy_and_latency(matter_router, request):
    rng = random.Random(SHUFFLE_SEED)
    samples = MATTER_EVAL.copy()
    rng.shuffle(samples)

    matter_router.classify("warmup")

    correct = 0
    per_class: dict[str, dict[str, int]] = defaultdict(lambda: {"tp": 0, "fn": 0})
    latencies: list[float] = []
    margins: list[float] = []
    by_margin: list[tuple[bool, float, dict, str]] = []

    for s in samples:
        res = matter_router.classify(s["filename"])
        latencies.append(res.latency_ms)
        margins.append(res.margin)
        gold = s["expected"]
        ok = res.label == gold
        by_margin.append((ok, res.margin, s, res.label))
        if ok:
            correct += 1
            per_class[gold]["tp"] += 1
        else:
            per_class[gold]["fn"] += 1

    n = len(samples)
    accuracy = correct / n

    lines = [
        f"\n=== MATTER ROUTER (seed={SHUFFLE_SEED}, n={n}) ===",
        f"accuracy: {correct}/{n} = {accuracy:.1%}",
        f"latency:  p50={statistics.median(latencies):.1f}ms  p95={_p(latencies, 0.95):.1f}ms",
        f"margin:   median={statistics.median(margins):.3f}  min={min(margins):.3f}  max={max(margins):.3f}",
        "predictions (sorted by margin asc — borderline first):",
    ]
    by_margin.sort(key=lambda t: t[1])
    for ok, margin, s, pred in by_margin:
        mark = "✓" if ok else "✗"
        lines.append(
            f"  {mark} margin={margin:.3f}  expect={s['expected']:25s}  pred={pred:25s}  {s['filename']!r}"
        )
    report = "\n".join(lines)
    print(report)
    request.config.stash.setdefault("matter_report", report)

    assert accuracy >= MIN_OVERALL_ACCURACY, f"accuracy {accuracy:.1%} < {MIN_OVERALL_ACCURACY:.0%}"
    p50 = statistics.median(latencies)
    p95 = _p(latencies, 0.95)
    assert p50 < MAX_P50_LATENCY_MS, f"p50 {p50:.1f}ms > {MAX_P50_LATENCY_MS}ms"
    assert p95 < MAX_P95_LATENCY_MS, f"p95 {p95:.1f}ms > {MAX_P95_LATENCY_MS}ms"


def _p(xs: list[float], q: float) -> float:
    if not xs:
        return 0.0
    sorted_xs = sorted(xs)
    idx = min(len(sorted_xs) - 1, int(q * len(sorted_xs)))
    return sorted_xs[idx]
