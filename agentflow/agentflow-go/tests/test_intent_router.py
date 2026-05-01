"""
Comprehensive accuracy test for the chat-intent embedding router.

Strategy:
  1. Build the router once (Init embeds the corpus).
  2. Random-shuffle the eval set under a fixed seed (reproducibility +
     ordering should not affect predictions).
  3. Classify each prompt; record (label, margin, latency).
  4. Assert overall accuracy bar AND per-class recall bars AND latency
     bounds (separately for warm and worst-case requests).

The accuracy bar is intentionally below 100% — this catches drift but
allows occasional borderline misses on adversarial prompts.
"""

from __future__ import annotations

import random
import statistics
from collections import Counter, defaultdict

import pytest

from tests._corpus import INTENT_CORPUS, INTENT_MARGIN
from tests._router import Router
from tests.data.intent_eval import INTENT_EVAL


# --- Tunable bars (move these as we grow / improve the corpus) ---
MIN_OVERALL_ACCURACY = 0.92
MIN_PER_CLASS_RECALL = {
    "NEEDS_TOOLS":    0.92,
    "NEEDS_RAG":      0.85,
    "CONVERSATIONAL": 0.85,
}
MAX_P50_LATENCY_MS = 50.0   # warm, single-query
MAX_P95_LATENCY_MS = 150.0  # any single query

SHUFFLE_SEED = 1337


@pytest.fixture(scope="module")
def intent_router(embed_sidecar):
    """Build the router once per module — corpus init is the slow part."""
    return Router(embed_sidecar, INTENT_CORPUS)


@pytest.mark.embed
def test_intent_corpus_loads(intent_router):
    assert len(intent_router.refs) == len(INTENT_CORPUS)
    # Every label appears at least twice in the corpus.
    counts = Counter(r.label for r in intent_router.refs)
    for label, n in counts.items():
        assert n >= 2, f"label {label} only has {n} examples in the corpus"


@pytest.mark.embed
def test_intent_accuracy_and_latency(intent_router, request):
    rng = random.Random(SHUFFLE_SEED)
    samples = INTENT_EVAL.copy()
    rng.shuffle(samples)

    correct = 0
    per_class: dict[str, dict[str, int]] = defaultdict(lambda: {"tp": 0, "fn": 0})
    latencies: list[float] = []
    margins: list[float] = []
    misses: list[tuple[dict, str, float]] = []

    # First call also bears the per-process JIT cost on mlx — exclude it
    # from the latency stats by classifying once with a throwaway prompt.
    intent_router.classify("warmup")

    for s in samples:
        res = intent_router.classify(s["prompt"])
        latencies.append(res.latency_ms)
        margins.append(res.margin)
        gold = s["label"]
        per_class[gold]
        if res.label == gold:
            correct += 1
            per_class[gold]["tp"] += 1
        else:
            per_class[gold]["fn"] += 1
            misses.append((s, res.label, res.margin))

    n = len(samples)
    accuracy = correct / n

    # Stash report to durations file for visibility.
    report_lines = [
        f"\n=== INTENT ROUTER (seed={SHUFFLE_SEED}, n={n}) ===",
        f"accuracy: {correct}/{n} = {accuracy:.1%}",
        f"latency:  p50={statistics.median(latencies):.1f}ms  p95={_p(latencies, 0.95):.1f}ms  max={max(latencies):.1f}ms",
        f"margin:   median={statistics.median(margins):.3f}  min={min(margins):.3f}",
        "per-class recall:",
    ]
    for label, counts in per_class.items():
        n_class = counts["tp"] + counts["fn"]
        recall = counts["tp"] / n_class if n_class else 0
        report_lines.append(f"  {label:15s}  recall={recall:.2f}  ({counts['tp']}/{n_class})")
    if misses:
        report_lines.append(f"misses ({len(misses)}):")
        for s, pred, margin in misses:
            report_lines.append(
                f"  gold={s['label']:14s} pred={pred:14s} margin={margin:.3f}  "
                f"({s.get('lang','?')}) {s['prompt'][:60]!r}"
            )
    report = "\n".join(report_lines)
    print(report)
    request.config.stash.setdefault("intent_report", report)

    # --- Hard assertions ---
    assert accuracy >= MIN_OVERALL_ACCURACY, (
        f"accuracy regressed: {accuracy:.1%} < {MIN_OVERALL_ACCURACY:.0%}"
    )
    for label, bar in MIN_PER_CLASS_RECALL.items():
        counts = per_class.get(label, {"tp": 0, "fn": 0})
        n_class = counts["tp"] + counts["fn"]
        if n_class == 0:
            pytest.fail(f"no eval examples for class {label}")
        recall = counts["tp"] / n_class
        assert recall >= bar, f"{label} recall {recall:.1%} < {bar:.0%}"
    p50 = statistics.median(latencies)
    p95 = _p(latencies, 0.95)
    assert p50 < MAX_P50_LATENCY_MS, f"p50 latency {p50:.1f}ms exceeds {MAX_P50_LATENCY_MS}ms"
    assert p95 < MAX_P95_LATENCY_MS, f"p95 latency {p95:.1f}ms exceeds {MAX_P95_LATENCY_MS}ms"


@pytest.mark.embed
def test_intent_margin_separates_correct_from_wrong(intent_router):
    """
    The whole point of the confidence gate: correct predictions should
    have higher margins than wrong ones, on average. Verify the gap is
    real on this dataset.
    """
    intent_router.classify("warmup")

    correct_margins: list[float] = []
    wrong_margins: list[float] = []
    for s in INTENT_EVAL:
        res = intent_router.classify(s["prompt"])
        (correct_margins if res.label == s["label"] else wrong_margins).append(res.margin)

    if not wrong_margins:
        pytest.skip("no wrong predictions on this dataset — gap test is vacuous")

    median_correct = statistics.median(correct_margins)
    median_wrong = statistics.median(wrong_margins)
    assert median_correct > median_wrong, (
        f"correct-prediction margins ({median_correct:.3f}) should exceed "
        f"wrong-prediction margins ({median_wrong:.3f})"
    )


@pytest.mark.embed
def test_intent_determinism(intent_router):
    """Same query twice should produce the same label and very similar
    margin. Ensures no nondeterminism in the model + transport."""
    intent_router.classify("warmup")
    queries = ["What's the deadline for case 12345?", "你好", "Summarize this contract"]
    for q in queries:
        a = intent_router.classify(q)
        b = intent_router.classify(q)
        assert a.label == b.label, f"label flipped for {q!r}: {a.label} → {b.label}"
        # Same input → identical embedding → identical scores. Allow tiny
        # float jitter through the network/json roundtrip.
        assert abs(a.score - b.score) < 1e-5
        assert abs(a.margin - b.margin) < 1e-5


def _p(xs: list[float], q: float) -> float:
    if not xs:
        return 0.0
    sorted_xs = sorted(xs)
    idx = min(len(sorted_xs) - 1, int(q * len(sorted_xs)))
    return sorted_xs[idx]
