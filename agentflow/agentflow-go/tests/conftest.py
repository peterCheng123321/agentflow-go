"""
Test fixtures for the AgentFlow embedding-spine architecture.

Fixtures (session-scoped, started once per pytest session):

  embed_sidecar  -> base URL of the standalone MLX embedding server
                    (scripts/mlx_embed_server.py at a test-only port)

  agentflow      -> base URL of an agentflow-serve instance booted with
                    the embed router enabled and the DashScope key
                    inherited from the user's prod data dir
                    ($HOME/Library/Application Support/AgentFlow/...).
                    Tests that mark this fixture transitively need a
                    real DashScope key on the machine; otherwise the
                    fixture skips with a clear message.

Random seed: pytest-randomly seeds Python's random + numpy's PRNG before
each test by default (use `--randomly-seed=N` to reproduce). Tests that
need a stable seed for, e.g., a shuffled eval set, should use
`random.Random(SEED)` locally rather than the global PRNG so they're
hermetic across reordering.
"""

from __future__ import annotations

import os
import shutil
import socket
import subprocess
import time
from pathlib import Path
from typing import Iterator

import pytest
import requests

REPO_ROOT = Path(__file__).resolve().parents[1]   # agentflow-go/
EMBED_SCRIPT = REPO_ROOT / "scripts" / "mlx_embed_server.py"
EMBED_MODEL = "mlx-community/multilingual-e5-small-mlx"
EMBED_TEST_PORT = 8195         # not 8095 — leave that for the dev sidecar
SERVER_TEST_PORT = 8200        # not 8000/8080
ROUTER_DISABLED_PORT = 0       # we don't need the legacy LLM router

# DashScope key location used by the prod app. We never read the key into
# Python — we just check it exists, and let agentflow-serve load it via
# its config loader when booted as a subprocess.
DASHSCOPE_KEY_FILE = (
    Path.home()
    / "Library"
    / "Application Support"
    / "AgentFlow"
    / "secrets"
    / "dashscope_api_key.txt"
)


def _free_port_or_die(port: int) -> None:
    """Hard-fail with a useful message if a test port is held by an
    actively-listening process. SO_REUSEADDR lets us bind through prior
    test runs' lingering TIME_WAIT entries, which would otherwise force
    the user to wait ~60s between runs.
    """
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            s.bind(("127.0.0.1", port))
        except OSError as e:
            pytest.exit(
                f"port {port} is in use ({e}); kill the holder before running tests",
                returncode=2,
            )


def _wait_for(url: str, timeout: float, what: str) -> None:
    deadline = time.time() + timeout
    last_err: Exception | None = None
    while time.time() < deadline:
        try:
            r = requests.get(url, timeout=2.0)
            if r.status_code == 200:
                return
        except Exception as e:
            last_err = e
        time.sleep(0.5)
    raise RuntimeError(f"{what} did not become ready at {url} within {timeout}s (last err: {last_err})")


def _start_embed_sidecar(port: int) -> subprocess.Popen:
    """Spawn the MLX embedding sidecar. Streams stderr to a log file."""
    if not EMBED_SCRIPT.exists():
        pytest.skip(f"embed sidecar script missing at {EMBED_SCRIPT}")
    log_path = REPO_ROOT / "tests" / "reports" / "embed_sidecar.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    log = open(log_path, "w")
    proc = subprocess.Popen(
        [
            "python3",
            str(EMBED_SCRIPT),
            "--model", EMBED_MODEL,
            "--host", "127.0.0.1",
            "--port", str(port),
        ],
        stdout=log,
        stderr=subprocess.STDOUT,
        env={**os.environ, "PYTHONUNBUFFERED": "1", "HF_HUB_DISABLE_TELEMETRY": "1"},
    )
    return proc


def _start_agentflow_serve(port: int, embed_port: int) -> subprocess.Popen:
    """Build (if needed) and spawn agentflow-serve in test mode."""
    bin_path = REPO_ROOT / "agentflow-bin-test"
    # Always rebuild so tests pick up source changes — fast on warm Go cache.
    build = subprocess.run(
        ["go", "build", "-o", str(bin_path), "./cmd"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
    )
    if build.returncode != 0:
        pytest.exit(f"go build failed:\n{build.stderr}", returncode=2)

    log_path = REPO_ROOT / "tests" / "reports" / "agentflow_serve.log"
    log_path.parent.mkdir(parents=True, exist_ok=True)
    log = open(log_path, "w")

    env = {
        **os.environ,
        "AGENTFLOW_PORT": str(port),
        "AGENTFLOW_ROUTER_ENABLED": "0",            # legacy LLM router off for tests
        "AGENTFLOW_EMBED_ROUTER_ENABLED": "1",
        "AGENTFLOW_EMBED_SERVER_PORT": str(embed_port),
        "AGENTFLOW_EMBED_SERVER_SCRIPT": str(EMBED_SCRIPT),
        # Keep prod DataDir so secrets/ and existing case state are visible —
        # tests that mutate state should clean up after themselves.
    }
    proc = subprocess.Popen(
        [str(bin_path)],
        stdout=log,
        stderr=subprocess.STDOUT,
        env=env,
    )
    return proc


@pytest.fixture(scope="session")
def embed_sidecar() -> Iterator[str]:
    """Standalone MLX embedding sidecar. Yields its base URL."""
    _free_port_or_die(EMBED_TEST_PORT)
    proc = _start_embed_sidecar(EMBED_TEST_PORT)
    try:
        _wait_for(f"http://127.0.0.1:{EMBED_TEST_PORT}/health", timeout=180, what="embed sidecar")
        yield f"http://127.0.0.1:{EMBED_TEST_PORT}"
    finally:
        proc.send_signal(2)  # SIGINT
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


@pytest.fixture(scope="session")
def agentflow(embed_sidecar) -> Iterator[str]:
    """
    Full agentflow-serve booted on test ports. Skips the test if the
    prod DashScope key file isn't present — we don't synthesize fake
    keys because every cloud-backed test would just fail.
    """
    if not DASHSCOPE_KEY_FILE.exists():
        pytest.skip(
            f"DashScope key not found at {DASHSCOPE_KEY_FILE}; "
            "set up the app once via the UI to enable cloud-backed tests"
        )
    # The embed sidecar that this server will use is a SEPARATE one
    # at a different port (the agentflow-serve supervisor spawns it).
    # Avoid colliding with the standalone sidecar fixture.
    server_embed_port = 8196
    _free_port_or_die(SERVER_TEST_PORT)
    _free_port_or_die(server_embed_port)
    proc = _start_agentflow_serve(SERVER_TEST_PORT, server_embed_port)
    base = f"http://127.0.0.1:{SERVER_TEST_PORT}"
    try:
        _wait_for(f"{base}/health", timeout=180, what="agentflow-serve")
        # Wait for embed router to also report ready (the supervisor spawns
        # its own python sidecar; the corpus loads lazily but Ready() is
        # what /health surfaces).
        deadline = time.time() + 60
        while time.time() < deadline:
            r = requests.get(f"{base}/health", timeout=2)
            er = (r.json().get("embed_router") or {})
            if er.get("ready"):
                break
            time.sleep(0.5)
        yield base
    finally:
        proc.send_signal(2)
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
        # Best-effort cleanup of the test binary.
        bin_path = REPO_ROOT / "agentflow-bin-test"
        if bin_path.exists():
            try:
                bin_path.unlink()
            except OSError:
                pass
