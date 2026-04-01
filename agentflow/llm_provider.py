import asyncio
import hashlib
import inspect
import json
import os
import re
import subprocess
import sys
import time

LEGAL_SYSTEM_PROMPT = (
    "你是一名专业法律助理，专注于中国商业租赁纠纷。"
    "直接用中文回答，不输出思考过程。回答应基于提供的上下文材料。"
)

TOOL_SYSTEM_PROMPT = (
    "You are a professional legal assistant. Based on the user input, decide which tool to call and with what parameters. "
    "Output ONLY a JSON object. If no tool is needed, output {\"tool\": \"none\"}.\n"
    "Available Tools:\n"
    "- search_legal_docs(query, category): Search through case files and laws.\n"
    "- create_case(client_name, matter_type, priority): Initialize a new legal file.\n"
    "- send_wechat_message(contact_name, text): Message a client via WeChat.\n"
    "- generate_report(case_id, title): Create a professional PDF summary.\n"
    "Example:\n"
    "Input: 'Search for lease defaults in 2024' -> Output: {\"tool\": \"search_legal_docs\", \"query\": \"lease default\", \"category\": \"Evidence\"}\n"
    "Input: 'Send hello to Alice' -> Output: {\"tool\": \"send_wechat_message\", \"contact_name\": \"Alice\", \"text\": \"hello\"}"
)

BIG_MODEL_ENABLED = os.getenv("AGENTFLOW_ENABLE_BIG_MODEL", "0") == "1"
SMALL_LOCAL_MODEL = os.getenv("AGENTFLOW_SMALL_LOCAL_MODEL", "Qwen/Qwen2.5-0.5B-Instruct")


_MLX_CACHE = {"available": None, "detail": None}

def probe_mlx_runtime(python_executable=None):
    if _MLX_CACHE["available"] is not None:
        return _MLX_CACHE["available"], _MLX_CACHE["detail"]
    python_executable = python_executable or sys.executable
    try:
        result = subprocess.run(
            [python_executable, "-c", "import mlx_lm"],
            capture_output=True,
            text=True,
            timeout=20,
        )
        ok = result.returncode == 0
        _MLX_CACHE["available"] = ok
        _MLX_CACHE["detail"] = (result.stderr or result.stdout).strip()
        return ok, _MLX_CACHE["detail"]
    except Exception as exc:
        _MLX_CACHE["available"] = False
        _MLX_CACHE["detail"] = str(exc)
        return False, _MLX_CACHE["detail"]


def load_mlx_modules():
    from mlx_lm import load, generate as mlx_generate

    return load, mlx_generate


def mlx_generate_compat(mlx_generate, model, tokenizer, prompt, max_tokens, temperature, prompt_cache=None):
    kwargs = {
        "prompt": prompt,
        "max_tokens": max_tokens,
    }
    if prompt_cache is not None:
        kwargs["prompt_cache"] = prompt_cache
    try:
        params = inspect.signature(mlx_generate).parameters
    except Exception:
        params = {}
    if "temp" in params:
        kwargs["temp"] = temperature
    elif "temperature" in params:
        kwargs["temperature"] = temperature
    return mlx_generate(model, tokenizer, **kwargs)


class OptimizedLLMProvider:
    def __init__(self, model_name="mlx-community/Qwen2.5-3B-Instruct-4bit"):
        self.model_name = model_name
        self._model = None
        self._tokenizer = None
        self._prompt_cache = None
        self._backend = None
        cache_key = hashlib.md5(self.model_name.encode("utf-8")).hexdigest()[:10]
        self._cache_path = os.path.join(os.path.dirname(__file__), "data", f".llm_prompt_cache_{cache_key}.safetensors")
        self._warmed_up = False
        self._backend_probe = None

    def _ensure_loaded(self):
        if self._model is not None:
            return
        t0 = time.time()
        ok, detail = probe_mlx_runtime()
        self._backend_probe = {"mlx_available": ok, "detail": detail}
        if ok:
            load, _ = load_mlx_modules()
            self._model, self._tokenizer = load(self.model_name)
            self._backend = "mlx"
            self._init_prompt_cache()
        else:
            self._load_transformers_backend()
        print(f"[LLMProvider] Backend={self._backend} loaded in {time.time() - t0:.1f}s")

    def _load_transformers_backend(self):
        from transformers import AutoModelForCausalLM, AutoTokenizer

        self._tokenizer = AutoTokenizer.from_pretrained(SMALL_LOCAL_MODEL, trust_remote_code=True)
        self._model = AutoModelForCausalLM.from_pretrained(
            SMALL_LOCAL_MODEL,
            trust_remote_code=True,
            device_map="auto",
        )
        self._backend = "transformers"

    def _init_prompt_cache(self):
        try:
            load, mlx_generate = load_mlx_modules()
            from mlx_lm.models.cache import load_prompt_cache, make_prompt_cache, save_prompt_cache
            if os.path.exists(self._cache_path):
                self._prompt_cache = load_prompt_cache(self._cache_path)
                print("[LLMProvider] Loaded persisted prompt cache.")
                return
            self._prompt_cache = make_prompt_cache(self._model)
            messages = [{"role": "system", "content": LEGAL_SYSTEM_PROMPT}]
            warmup_prompt = self._tokenizer.apply_chat_template(
                messages, tokenize=False, add_generation_prompt=True
            )
            mlx_generate_compat(
                mlx_generate,
                self._model,
                self._tokenizer,
                prompt=warmup_prompt,
                max_tokens=1,
                temperature=0.0,
                prompt_cache=self._prompt_cache,
            )
            os.makedirs(os.path.dirname(self._cache_path), exist_ok=True)
            save_prompt_cache(self._cache_path, self._prompt_cache)
            print("[LLMProvider] Warm-up complete, prompt cache saved.")
        except Exception as e:
            print(f"[LLMProvider] Prompt cache init skipped: {e}")
            self._prompt_cache = None

    def warmup(self):
        if not self._warmed_up:
            self._ensure_loaded()
            self._warmed_up = True

    def generate(self, prompt, context=""):
        return self.generate_legal_text(prompt, context=context)

    def get_model_capabilities(self):
        return {
            "small_model_active": True,
            "big_model_enabled": BIG_MODEL_ENABLED,
            "default_profile": "small",
            "backend": self._backend,
            "backend_probe": self._backend_probe,
            "small_local_model": SMALL_LOCAL_MODEL,
        }

    def _resolve_profile(self, profile="small"):
        if profile == "big" and not BIG_MODEL_ENABLED:
            return "small"
        return profile

    def _profile_to_generation_config(self, profile="big"):
        profile = self._resolve_profile(profile)
        if profile == "tiny":
            return {"max_tokens": 64, "temp": 0.0}
        if profile == "small":
            return {"max_tokens": 512, "temp": 0.0}
        return {"max_tokens": 4096, "temp": 0.15}

    def _generate_with_profile(self, messages, profile="big"):
        self._ensure_loaded()
        t0 = time.time()
        if hasattr(self._tokenizer, "apply_chat_template") and self._tokenizer.chat_template:
            prompt_str = self._tokenizer.apply_chat_template(
                messages, tokenize=False, add_generation_prompt=True
            )
        else:
            prompt_str = "\n".join(f"{m['role']}: {m['content']}" for m in messages) + "\nassistant:"
        resolved_profile = self._resolve_profile(profile)
        config = self._profile_to_generation_config(resolved_profile)
        if self._backend == "mlx":
            _, mlx_generate = load_mlx_modules()
            try:
                result = mlx_generate_compat(
                    mlx_generate,
                    self._model,
                    self._tokenizer,
                    prompt=prompt_str,
                    max_tokens=config["max_tokens"],
                    temperature=config["temp"],
                    prompt_cache=self._prompt_cache,
                )
                print(f"[LLMProvider] {resolved_profile} profile: {len(result)} chars in {time.time() - t0:.2f}s")
                return result
            except Exception as exc:
                if self._prompt_cache is not None:
                    print(f"[LLMProvider] Prompt cache retry without cache: {exc}")
                    self._prompt_cache = None
                    result = mlx_generate_compat(
                        mlx_generate,
                        self._model,
                        self._tokenizer,
                        prompt=prompt_str,
                        max_tokens=config["max_tokens"],
                        temperature=config["temp"],
                        prompt_cache=None,
                    )
                    print(f"[LLMProvider] {resolved_profile} profile (no cache): {len(result)} chars in {time.time() - t0:.2f}s")
                    return result
                raise

        inputs = self._tokenizer(prompt_str, return_tensors="pt")
        output = self._model.generate(
            **inputs,
            max_new_tokens=config["max_tokens"],
            do_sample=config["temp"] > 0,
            temperature=max(config["temp"], 0.1),
        )
        new_tokens = output[0][inputs["input_ids"].shape[1]:]
        return self._tokenizer.decode(new_tokens, skip_special_tokens=True)

    def generate_legal_text(self, prompt, context=""):
        messages = [
            {"role": "system", "content": LEGAL_SYSTEM_PROMPT},
            {"role": "user", "content": f"Context: {context}\nRequest: {prompt}"},
        ]
        try:
            content = self._generate_with_profile(messages, profile="big")
            content = re.sub(r'<thought>.*?</thought>', '', content, flags=re.DOTALL).strip()
            return content
        except Exception as e:
            print(f"[LLMProvider] Error in generation: {e}")
            return "Error in generation."

    def generate_structured_json(self, prompt, context="", profile="small", fallback=None):
        messages = [
            {
                "role": "system",
                "content": (
                    "You are a structured planning assistant. "
                    "Return valid JSON only with no markdown fences or extra commentary."
                ),
            },
            {"role": "user", "content": f"Context: {context}\nRequest: {prompt}"},
        ]
        try:
            raw = self._generate_with_profile(messages, profile=profile).strip()
            raw = re.sub(r"^```json\s*|^```\s*|```$", "", raw, flags=re.MULTILINE).strip()
            match = re.search(r"\{.*\}", raw, flags=re.DOTALL)
            candidate = match.group(0) if match else raw
            return json.loads(candidate)
        except Exception as e:
            print(f"[LLMProvider] Structured generation fallback: {e}")
            return fallback if fallback is not None else {}

    def extract_highlights(self, document_text, case_context=""):
        """Ask the LLM to identify important passages in a document for human review."""
        messages = [
            {
                "role": "system",
                "content": (
                    "你是一名法律文档分析专家。你的任务是从法律文档中识别需要人工审阅的重要段落。"
                    "返回JSON数组，每个元素包含: text(原文引用), category(分类: dispute_clause/obligation/risk/key_fact/legal_basis), "
                    "importance(high/medium), reason(简短说明为什么重要)。"
                    "只返回JSON，不要其他内容。"
                ),
            },
            {
                "role": "user",
                "content": (
                    f"案件背景: {case_context}\n\n"
                    f"请从以下文档中识别5-10个需要人工关注的重点段落:\n\n{document_text}"
                ),
            },
        ]
        try:
            raw = self._generate_with_profile(messages, profile="big").strip()
            raw = re.sub(r"^```json\s*|^```\s*|```$", "", raw, flags=re.MULTILINE).strip()
            match = re.search(r"\[.*\]", raw, flags=re.DOTALL)
            if match:
                highlights = json.loads(match.group(0))
                if isinstance(highlights, list):
                    return highlights
            # Try wrapping in array
            match = re.search(r"\{.*\}", raw, flags=re.DOTALL)
            if match:
                return [json.loads(match.group(0))]
            return []
        except Exception as e:
            print(f"[LLMProvider] Highlight extraction fallback: {e}")
            return []
