#!/usr/bin/env python3
"""
AgentFlow — simple setup and launcher.

Usage:
    python setup_app.py          # setup + start
    python setup_app.py --setup  # setup only
    python setup_app.py --start  # start only (assumes setup done)
"""
import os
import subprocess
import sys
import time
import venv
from pathlib import Path

ROOT = Path(__file__).resolve().parent
VENV = ROOT / ".venv"
REQUIREMENTS = ROOT / "requirements-local.txt"
REQUIRED_DIRS = [
    "data/docs",
    "data/vector_store",
    "data/tracking",
    "data/mock_cases",
    "data/reports",
]

BOLD = "\033[1m"
GREEN = "\033[32m"
YELLOW = "\033[33m"
RESET = "\033[0m"


def log(section: str, msg: str):
    print(f"{BOLD}[{section}]{RESET} {msg}")


def create_dirs():
    for d in REQUIRED_DIRS:
        (ROOT / d).mkdir(parents=True, exist_ok=True)
    log("dirs", "Ready")


def create_venv():
    if VENV.exists() and (VENV / "bin" / "python").exists():
        log("venv", "Already exists")
        return
    log("venv", f"Creating at {VENV}")
    venv.create(VENV, with_pip=True, clear=False)
    log("venv", "Created")


def install_deps():
    pip = VENV / "bin" / "pip"
    log("pip", "Upgrading pip...")
    subprocess.run([str(pip), "install", "--upgrade", "pip"], check=True, capture_output=True)

    is_arm = os.uname().machine == "arm64"
    cmd = [str(pip), "install", "-r", str(REQUIREMENTS)]
    if is_arm:
        cmd.append("--prefer-binary")
        log("pip", "Installing deps (ARM64 optimized)...")
    else:
        log("pip", "Installing deps...")

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(result.stderr)
        log("pip", "⚠ Some packages may have failed. Check output above.")
    else:
        log("pip", "Done")


def setup():
    log("setup", "AgentFlow")
    create_dirs()
    create_venv()
    install_deps()
    log("setup", f"{GREEN}Ready{RESET}")


def start():
    if not (VENV / "bin" / "python").exists():
        log("error", "venv not found. Run setup first: python setup_app.py --setup")
        sys.exit(1)

    venv_python = str(VENV / "bin" / "python")
    port = os.environ.get("AGENTFLOW_PORT", "8000")

    log("start", f"Launching on http://127.0.0.1:{port}")
    log("start", "First request will be slow (model loading)...")
    print()

    os.chdir(ROOT)
    env = os.environ.copy()
    env["PATH"] = str(VENV / "bin") + os.pathsep + env["PATH"]
    env["PYTHONUNBUFFERED"] = "1"

    try:
        subprocess.run(
            [venv_python, "-m", "uvicorn", "server:app", "--host", "0.0.0.0", "--port", port, "--timeout-keep-alive", "120"],
            env=env,
            check=False,
        )
    except KeyboardInterrupt:
        log("stop", "Shutting down")


if __name__ == "__main__":
    args = sys.argv[1:]
    if "--setup" in args:
        setup()
    elif "--start" in args:
        start()
    else:
        setup()
        print()
        start()
