import os
import platform
import shutil
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Any


def _probe_mlx_runtime() -> bool:
    try:
        result = subprocess.run(
            [sys.executable, "-c", "import mlx_lm"],
            capture_output=True,
            text=True,
            timeout=20,
        )
        return result.returncode == 0
    except Exception:
        return False


def detect_device() -> dict[str, Any]:
    info: dict[str, Any] = {
        "os": platform.system(),
        "os_version": platform.version(),
        "arch": platform.machine(),
        "python_version": f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}",
        "cpu_count": os.cpu_count() or 1,
        "total_ram_gb": 0,
        "gpu_info": None,
        "is_apple_silicon": False,
        "chip_generation": None,
        "platform_id": "unknown",
    }

    def _detect_os_specific():
        if info["os"] == "Darwin":
            _detect_macos(info)
        elif info["os"] == "Linux":
            _detect_linux(info)
        elif info["os"] == "Windows":
            _detect_windows(info)

    def _detect_ram():
        try:
            import psutil
            return round(psutil.virtual_memory().total / (1024**3), 2)
        except ImportError:
            return None

    with ThreadPoolExecutor(max_workers=4) as executor:
        os_future = executor.submit(_detect_os_specific)
        ram_future = executor.submit(_detect_ram)
        mlx_future = executor.submit(_probe_mlx_runtime)

        os_future.result()
        ram_result = ram_future.result()
        if ram_result:
            info["total_ram_gb"] = ram_result
        info["_mlx_available"] = mlx_future.result()

    info["platform_id"] = _compute_platform_id(info)
    return info


def _detect_macos(info: dict):
    try:
        chip = subprocess.check_output(
            ["sysctl", "-n", "machdep.cpu.brand_string"],
            stderr=subprocess.DEVNULL,
        ).decode().strip()
        info["gpu_info"] = chip
        if "Apple" in chip:
            info["is_apple_silicon"] = True
            for gen in ["M4", "M3", "M2", "M1"]:
                if gen in chip:
                    info["chip_generation"] = gen
                    break
    except Exception:
        pass


def _detect_linux(info: dict):
    try:
        gpu_info = subprocess.check_output(
            ["lspci"],
            stderr=subprocess.DEVNULL,
        ).decode()
        for line in gpu_info.splitlines():
            if "VGA" in line or "3D" in line or "GPU" in line:
                info["gpu_info"] = line.split(":", 1)[-1].strip()
                break
    except Exception:
        pass

    try:
        if os.path.exists("/proc/meminfo"):
            with open("/proc/meminfo") as f:
                for line in f:
                    if line.startswith("MemTotal:"):
                        kb = int(line.split()[1])
                        if not info["total_ram_gb"]:
                            info["total_ram_gb"] = round(kb / (1024**2), 2)
                        break
    except Exception:
        pass

    if os.path.exists("/.dockerenv"):
        info["in_docker"] = True


def _detect_windows(info: dict):
    try:
        result = subprocess.check_output(
            ["wmic", "path", "win32_VideoController", "get", "name"],
            stderr=subprocess.DEVNULL,
        ).decode()
        lines = [l.strip() for l in result.splitlines() if l.strip() and l.strip() != "Name"]
        if lines:
            info["gpu_info"] = lines[0]
    except Exception:
        pass


def _compute_platform_id(info: dict) -> str:
    parts = [
        info["os"].lower(),
        info["arch"],
        str(info["chip_generation"] or "cpu"),
        f"{int(info['total_ram_gb'])}g" if info["total_ram_gb"] else "unknown-ram",
    ]
    return "-".join(parts)


def recommend_config(device_info: dict | None = None) -> dict[str, Any]:
    if device_info is None:
        device_info = detect_device()

    os_name = device_info["os"]
    arch = device_info["arch"]
    ram = device_info["total_ram_gb"]
    is_silicon = device_info.get("is_apple_silicon", False)
    chip_gen = device_info.get("chip_generation")

    config: dict[str, Any] = {
        "model": None,
        "llm_backend": "api",
        "rag_backend": "lightweight_bm25",
        "wechat_mode": "mock",
        "max_file_size_mb": 50,
        "chunk_size": 512,
        "chunk_overlap": 80,
        "setup_commands": [],
        "auto_start": False,
    }

    if os_name == "Darwin" and is_silicon:
        config["llm_backend"] = "mlx" if device_info.get("_mlx_available", _probe_mlx_runtime()) else "transformers_local"
        if chip_gen in ["M3", "M4"]:
            if ram >= 32:
                config["model"] = "mlx-community/Qwen2.5-14B-Instruct-4bit"
            elif ram >= 16:
                config["model"] = "mlx-community/Qwen2.5-7B-Instruct-4bit"
            else:
                config["model"] = "mlx-community/Qwen2.5-3B-Instruct-4bit"
        elif chip_gen == "M2":
            if ram >= 16:
                config["model"] = "mlx-community/Qwen2.5-7B-Instruct-4bit"
            else:
                config["model"] = "mlx-community/Qwen2.5-3B-Instruct-4bit"
        else:
            if ram >= 16:
                config["model"] = "mlx-community/Qwen2.5-7B-Instruct-4bit"
            elif ram >= 8:
                config["model"] = "mlx-community/Qwen2.5-3B-Instruct-4bit"
            else:
                config["model"] = "mlx-community/Qwen2.5-1.5B-Instruct-4bit"
        if config["llm_backend"] == "transformers_local":
            config["model"] = os.getenv("AGENTFLOW_SMALL_LOCAL_MODEL", "Qwen/Qwen2.5-0.5B-Instruct")
        config["auto_start"] = True

    elif os_name == "Linux":
        gpu = device_info.get("gpu_info") or ""
        if "nvidia" in gpu.lower() or "geforce" in gpu.lower() or "rtx" in gpu.lower():
            config["llm_backend"] = "cuda"
            if ram >= 32:
                config["model"] = "Qwen/Qwen2.5-14B-Instruct-GPTQ-Int4"
            elif ram >= 16:
                config["model"] = "Qwen/Qwen2.5-7B-Instruct-GPTQ-Int4"
            else:
                config["model"] = "Qwen/Qwen2.5-3B-Instruct"
        else:
            config["llm_backend"] = "api"
            config["model"] = "api-fallback"
        config["auto_start"] = True

    elif os_name == "Windows":
        gpu = device_info.get("gpu_info") or ""
        if "nvidia" in gpu.lower() or "rtx" in gpu.lower():
            config["llm_backend"] = "cuda"
            config["model"] = "Qwen/Qwen2.5-7B-Instruct-GPTQ-Int4" if ram >= 16 else "Qwen/Qwen2.5-3B-Instruct"
        else:
            config["llm_backend"] = "api"
            config["model"] = "api-fallback"
        config["auto_start"] = True

    if ram and ram < 4:
        config["chunk_size"] = 256
        config["chunk_overlap"] = 40
    elif ram and ram >= 32:
        config["chunk_size"] = 768
        config["chunk_overlap"] = 120

    config["setup_commands"] = _generate_setup_commands(config, device_info)
    return config


def _generate_setup_commands(config: dict, device_info: dict) -> list[str]:
    cmds = []
    os_name = device_info["os"]

    if os_name == "Darwin":
        cmds.append("xcode-select --install 2>/dev/null || true")
        if not shutil.which("brew"):
            cmds.append('/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"')
        cmds.append("brew install python@3.12 2>/dev/null || true")
    elif os_name == "Linux":
        cmds.append("sudo apt-get update -qq && sudo apt-get install -y python3 python3-pip python3-venv 2>/dev/null || true")
    elif os_name == "Windows":
        cmds.append("winget install Python.Python.3.12 2>nul || echo Python install skipped")

    cmds.append(f"{sys.executable} -m venv .venv")

    pip_cmd = ".venv/bin/pip install -r requirements-local.txt" if os_name != "Windows" else ".venv\\Scripts\\pip install -r requirements-local.txt"
    cmds.append(pip_cmd)

    if config["llm_backend"] == "api":
        pass

    return cmds


def auto_setup() -> dict[str, Any]:
    device = detect_device()
    config = recommend_config(device)

    results: dict[str, Any] = {
        "device": device,
        "config": config,
        "steps_completed": [],
        "steps_failed": [],
        "ready": False,
    }

    for d in ["data/docs", "data/vector_store", "data/tracking", "data/mock_cases", "frontend"]:
        os.makedirs(d, exist_ok=True)
    results["steps_completed"].append("directories_created")

    venv_path = os.path.join(os.path.dirname(__file__), ".venv")
    if not os.path.exists(venv_path):
        try:
            subprocess.run(
                [sys.executable, "-m", "venv", venv_path],
                check=True,
                capture_output=True,
            )
            results["steps_completed"].append("venv_created")
        except Exception as e:
            results["steps_failed"].append(f"venv_creation: {e}")

    results["ready"] = len(results["steps_failed"]) == 0
    return results


def get_device_report() -> dict[str, Any]:
    device = detect_device()
    config = recommend_config(device)
    return {
        "device": device,
        "recommended_config": config,
        "python_ok": sys.version_info >= (3, 10),
        "openclaw_available": False,
        "openclaw_note": "Paused in V1. Will be re-enabled in V2 with adaptive optimization.",
    }
