import importlib.util
import os
from auto_device import detect_device, recommend_config


def check_mlx_installed():
    return importlib.util.find_spec("mlx_lm") is not None


def check_model_downloaded(model_name):
    if not check_mlx_installed():
        return False
    try:
        from huggingface_hub import try_to_load_from_cache
        result = try_to_load_from_cache(model_name, filename="config.json")
        return result is not None and isinstance(result, str)
    except Exception:
        return False


def ensure_model_downloaded(model_name):
    print(f"[*] Downloading model {model_name}... This may take a while.")
    try:
        from huggingface_hub import snapshot_download
        snapshot_download(repo_id=model_name)
        print(f"[+] Successfully downloaded {model_name}")
        return True
    except Exception as e:
        print(f"[-] Failed to download {model_name}: {e}")
        return False


def command_exists(command):
    try:
        import subprocess
        subprocess.run(["which", command], capture_output=True, check=True)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False


def get_setup_status():
    device_info = detect_device()
    config = recommend_config(device_info)
    model = config.get("model", "mlx-community/Qwen2.5-3B-Instruct-4bit")

    return {
        "system_info": device_info,
        "recommended_model": model,
        "brew_installed": command_exists("brew"),
        "directories_ready": all(
            os.path.isdir(path) for path in ["data", "data/docs", "data/vector_store", "frontend"]
        ),
        "mlx_installed": check_mlx_installed(),
        "model_downloaded": check_model_downloaded(model),
    }


def run_local_setup():
    os.makedirs("data/docs", exist_ok=True)
    os.makedirs("data/vector_store", exist_ok=True)
    os.makedirs("frontend", exist_ok=True)
    os.makedirs("data/mock_cases", exist_ok=True)

    status = get_setup_status()
    status["setup_ran"] = True
    status["actions_taken"] = ["Prepared local folders."]

    if status["mlx_installed"] and not status["model_downloaded"]:
        downloaded = ensure_model_downloaded(status["recommended_model"])
        status = get_setup_status()
        status["setup_ran"] = True
        status["actions_taken"] = ["Prepared local folders."]
        if downloaded:
            status["actions_taken"].append(f"Downloaded model {status['recommended_model']}.")
        else:
            status["actions_taken"].append(f"Could not download model {status['recommended_model']}.")

    if status["mlx_installed"] and status["model_downloaded"]:
        status["message"] = f"Local setup is complete. Model {status['recommended_model']} is ready."
        status["next_steps"] = ["Start the backend.", "Open the dashboard.", "Upload a legal document."]
    elif status["mlx_installed"]:
        status["message"] = f"mlx-lm is installed but model {status['recommended_model']} is not downloaded yet."
        status["next_steps"] = [
            f"Run: python -c \"from mlx_lm import load; load('{status['recommended_model']}')\""
        ]
    else:
        status["message"] = "Local folders are ready. mlx-lm is not installed."
        status["next_steps"] = [
            "Install mlx-lm: pip install mlx-lm",
            f"Then load the model: python -c \"from mlx_lm import load; load('{status['recommended_model']}')\"",
        ]

    return status


if __name__ == "__main__":
    status = get_setup_status()
    print(f"System: {status['system_info']}")
    print(f"Recommended model: {status['recommended_model']}")
    print(f"mlx_installed: {status['mlx_installed']}")
    print(f"model_downloaded: {status['model_downloaded']}")
