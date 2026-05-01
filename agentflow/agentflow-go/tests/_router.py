"""
Python mirror of the cosine-router math used in internal/embedrouter.
Hits the embedding sidecar over HTTP and computes the same nearest-
neighbor + margin signal the Go code computes.

Tests use this directly so they validate the *model behaviour* without
depending on the agentflow-serve being booted (which would also drag
in DashScope, the case DB, etc.).
"""

from __future__ import annotations

import time
from dataclasses import dataclass
from typing import Iterable, Sequence

import numpy as np
import requests


@dataclass
class Reference:
    label: str
    utterance: str
    vec: np.ndarray  # L2-normalized


@dataclass
class Result:
    label: str
    score: float
    margin: float
    best_utterance: str
    runner_label: str
    runner_utterance: str
    latency_ms: float


def _post_embed(base_url: str, texts: Sequence[str], timeout: float = 30.0) -> np.ndarray:
    r = requests.post(
        f"{base_url}/api/embed",
        json={"input": list(texts)},
        timeout=timeout,
    )
    r.raise_for_status()
    body = r.json()
    embs = body["embeddings"]
    arr = np.asarray(embs, dtype=np.float32)
    return arr


def _normalize(v: np.ndarray) -> np.ndarray:
    n = np.linalg.norm(v, axis=-1, keepdims=True)
    n[n == 0] = 1.0
    return v / n


class Router:
    """Cosine-similarity router over a fixed labeled corpus.

    Init embeds every corpus utterance once and keeps the L2-normalized
    matrix in memory. Classify embeds the query and computes argmax cosine.
    """

    def __init__(self, base_url: str, corpus: Iterable[tuple[str, str]]):
        self.base_url = base_url
        self.refs: list[Reference] = []
        items = list(corpus)
        if not items:
            raise ValueError("empty corpus")
        utts = [u for _, u in items]
        vecs = _normalize(_post_embed(base_url, utts))
        for (label, utt), v in zip(items, vecs):
            self.refs.append(Reference(label=label, utterance=utt, vec=v))
        self._matrix = np.stack([r.vec for r in self.refs])  # (N, dim)

    def classify(self, query: str) -> Result:
        t0 = time.perf_counter()
        qv = _normalize(_post_embed(self.base_url, [query]))[0]
        scores = self._matrix @ qv  # (N,)
        order = np.argsort(-scores)
        best_idx = int(order[0])
        best = self.refs[best_idx]
        # Runner-up = best score from a *different* label.
        runner_idx = best_idx
        for i in order[1:]:
            if self.refs[int(i)].label != best.label:
                runner_idx = int(i)
                break
        runner = self.refs[runner_idx]
        margin = float(scores[best_idx] - scores[runner_idx]) if runner_idx != best_idx else 0.0
        latency_ms = (time.perf_counter() - t0) * 1000.0
        return Result(
            label=best.label,
            score=float(scores[best_idx]),
            margin=margin,
            best_utterance=best.utterance,
            runner_label=runner.label,
            runner_utterance=runner.utterance,
            latency_ms=latency_ms,
        )
