"""Sanity checks. If these don't pass, nothing else will."""

import pytest
import requests


@pytest.mark.smoke
@pytest.mark.embed
def test_sidecar_health(embed_sidecar):
    r = requests.get(f"{embed_sidecar}/health", timeout=5)
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert "model" in body


@pytest.mark.smoke
@pytest.mark.embed
def test_sidecar_embed_shape(embed_sidecar):
    r = requests.post(
        f"{embed_sidecar}/api/embed",
        json={"input": ["hello", "你好"]},
        timeout=10,
    )
    r.raise_for_status()
    body = r.json()
    assert len(body["embeddings"]) == 2
    dim = len(body["embeddings"][0])
    assert dim > 0
    assert len(body["embeddings"][1]) == dim
    # multilingual-e5-small produces 384-dim vectors.
    assert dim == 384, f"unexpected embedding dim {dim} (expected 384)"


@pytest.mark.smoke
@pytest.mark.server
def test_agentflow_health(agentflow):
    r = requests.get(f"{agentflow}/health", timeout=5)
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    er = body.get("embed_router")
    assert er is not None and er.get("enabled"), "embed router should be enabled"
    assert er.get("ready"), "embed sidecar should be ready by the time tests run"
