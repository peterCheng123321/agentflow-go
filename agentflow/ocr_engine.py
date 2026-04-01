"""GLM-OCR (MLX): call sites — server.py (upload + rescan_document), rag_manager (ingest with external OCR)."""

import os
import gc
import time
import asyncio
import re
from concurrent.futures import ThreadPoolExecutor

# MLX-VLM is required for GLM-OCR on Apple Silicon
try:
    import mlx_vlm
    from mlx_vlm import load, generate
    MLX_VLM_AVAILABLE = True
except ImportError:
    MLX_VLM_AVAILABLE = False

IS_MAC = __import__("platform").system() == "Darwin"

IMAGE_EXTENSIONS = frozenset(
    {".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff", ".gif", ".heic"}
)
TEXT_EXTENSIONS = frozenset({".txt", ".md", ".markdown"})


class OCREngine:
    """
    Advanced OCR Engine using GLM-OCR (0.9B) via MLX for Apple Silicon.
    Falls back to native macOS Vision if MLX-VLM is missing.
    Memory-optimized: lazy load, explicit pixmap release, TTL unload.
    """
    # Memory optimization: lower DPI reduces pixmap memory by ~40%
    # 150 DPI still produces clear enough images for GLM-OCR
    OCR_DPI = 150
    
    # TTL unload: unload model after this many seconds of idle time
    MODEL_TTL_SECONDS = 300  # 5 minutes
    
    def __init__(self, model_id="mlx-community/GLM-OCR-6bit"):
        self.model_id = model_id
        self._model = None
        self._processor = None
        self._executor = ThreadPoolExecutor(max_workers=1)
        self._is_vision_available = False
        self._is_glm_ready = False
        self._last_used = time.time()
        # MLX-VLM inference is not safe to run concurrently; serialize scans to avoid
        # command-buffer encoder assertion failures on Apple Silicon.
        self._scan_lock = asyncio.Lock()
        
        if IS_MAC:
            try:
                import Vision
                self._is_vision_available = True
            except ImportError:
                pass

    def _ensure_glm_loaded(self):
        if self._is_glm_ready:
            return True
        # Safety switch: MLX-VLM OCR can hard-crash on some systems/workloads.
        # Keep disabled by default until explicitly enabled.
        if os.getenv("AGENTFLOW_ENABLE_GLM_OCR", "0") != "1":
            return False
        if not MLX_VLM_AVAILABLE:
            return False
        
        try:
            print(f"[OCREngine] Loading GLM-OCR model: {self.model_id}...")
            self._model, self._processor = load(self.model_id)
            self._is_glm_ready = True
            self._last_used = time.time()
            print("[OCREngine] GLM-OCR (6bit) loaded and ready.")
            return True
        except Exception as e:
            print(f"[OCREngine] Failed to load GLM-OCR: {e}")
            return False

    def _touch(self):
        """Update last-used timestamp for TTL tracking."""
        self._last_used = time.time()

    def unload_model(self):
        """Explicitly unload the OCR model to free memory."""
        if self._is_glm_ready:
            print(f"[OCREngine] Unloading GLM-OCR model (idle for {self.MODEL_TTL_SECONDS}s)...")
            self._model = None
            self._processor = None
            self._is_glm_ready = False
            gc.collect()

    def check_ttl_unload(self):
        """Check if model should be unloaded based on idle time."""
        if self._is_glm_ready and (time.time() - self._last_used) > self.MODEL_TTL_SECONDS:
            self.unload_model()

    def _read_txt_sync(self, file_path):
        with open(file_path, "r", encoding="utf-8", errors="ignore") as handle:
            return handle.read()

    def _read_docx_sync(self, file_path):
        try:
            import docx

            document = docx.Document(file_path)
            return "\n".join(p.text for p in document.paragraphs if p.text and p.text.strip())
        except Exception as exc:
            return f"[DOCX read error] {exc}"

    async def scan_file(self, file_path, task="Text Recognition"):
        """
        Initial document scanning using GLM-OCR for PDFs and images; plain text / DOCX read directly.
        Tasks: "Text Recognition", "Table Recognition", "Information Extraction"
        """
        async with self._scan_lock:
            ext = os.path.splitext(file_path)[1].lower()
            if ext in TEXT_EXTENSIONS:
                loop = asyncio.get_event_loop()
                return await loop.run_in_executor(self._executor, self._read_txt_sync, file_path)
            if ext == ".docx":
                loop = asyncio.get_event_loop()
                return await loop.run_in_executor(self._executor, self._read_docx_sync, file_path)
            if ext == ".doc":
                return (
                    "[Unsupported: legacy .doc — export to PDF or DOCX, or upload a scan image/PDF.]"
                )

            start_time = time.time()

            if not self._ensure_glm_loaded():
                # For stability: do not attempt fallback OCR for images/PDFs by default.
                # The system can still store/view the original file even if there is no text.
                if ext in IMAGE_EXTENSIONS or file_path.lower().endswith(".pdf"):
                    return ""
                print("[OCREngine] GLM-OCR unavailable, using extraction fallback...")
                return await self._fallback_scan(file_path)

            self._touch()

            if file_path.lower().endswith(".pdf"):
                return await self._scan_pdf_glm(file_path, task, start_time)
            if ext in IMAGE_EXTENSIONS:
                return await self._scan_image_glm(file_path, task, start_time)
            return await self._fallback_scan(file_path)

    async def _scan_pdf_glm(self, file_path, task, start_time):
        import pymupdf
        doc = pymupdf.open(file_path)
        full_text = []
        
        try:
            for page in doc:
                # Render page to image for GLM-OCR at reduced DPI (memory optimized)
                pix = page.get_pixmap(dpi=self.OCR_DPI)
                img_path = f"{file_path}_p{page.number}.png"
                pix.save(img_path)
                pix = None  # Explicitly free pixmap memory immediately
                
                try:
                    page_text = await self._scan_image_glm(img_path, task, start_time, cleanup=True)
                    full_text.append(f"--- Page {page.number + 1} ---\n{page_text}")
                except Exception:
                    # Ensure cleanup even on error
                    if os.path.exists(img_path):
                        os.remove(img_path)
                    full_text.append(f"--- Page {page.number + 1} ---\n[OCR failed for this page]")
                
                # Force garbage collection after each page to free memory
                gc.collect()
        finally:
            doc.close()
        
        return "\n\n".join(full_text)

    async def _scan_image_glm(self, image_path, task, start_time, cleanup=False):
        try:
            # Official GLM-OCR task strings end with ":" (see zai-org/GLM-OCR README).
            prompt = task if str(task).endswith(":") else f"{task}:"
            output = await asyncio.get_event_loop().run_in_executor(
                self._executor, self._run_glm_inference, image_path, prompt
            )
            
            if cleanup and os.path.exists(image_path):
                os.remove(image_path)
                
            return output
        except Exception as e:
            if cleanup and os.path.exists(image_path):
                os.remove(image_path)
            return f"[GLM-OCR Error] {e}"

    @staticmethod
    def _strip_glm_ocr_output(text: str) -> str:
        t = (text or "").strip()
        if not t:
            return ""
        if re.fullmatch(r"```(?:markdown)?\s*```", t, flags=re.IGNORECASE | re.DOTALL):
            return ""
        t = re.sub(r"^```(?:markdown)?\s*", "", t, flags=re.IGNORECASE)
        t = re.sub(r"\s*```\s*$", "", t)
        return t.strip()

    def _run_glm_inference(self, image_path, prompt):
        from mlx_vlm.prompt_utils import apply_chat_template

        if not prompt.endswith(":"):
            prompt = prompt + ":"

        def _one_shot(enable_thinking):
            extra = {}
            if enable_thinking is not None:
                extra["enable_thinking"] = enable_thinking
            formatted = apply_chat_template(
                self._processor,
                self._model.config,
                prompt,
                num_images=1,
                **extra,
            )
            result = generate(
                self._model,
                self._processor,
                formatted,
                image=image_path,
                max_tokens=8192,
                temperature=0.0,
            )
            return self._strip_glm_ocr_output(getattr(result, "text", None) or "")

        # Prefer /nothink template (matches Ollama "Text Recognition: ./image" behavior).
        out = _one_shot(False)
        if len(out) >= 20:
            return out
        out2 = _one_shot(None)
        if len(out2) >= 20:
            return out2
        if out or out2:
            return (out or out2).strip()
        return (
            "[GLM-OCR produced no usable text for this image with the local MLX build. "
            "Options: use the official zai-org GLM-OCR SDK (layout + OCR pipeline), "
            "or try another MLX snapshot / full-precision weights.]"
        )

    async def _fallback_scan(self, file_path):
        """PyMuPDF text for PDF/images where possible; text/docx without GLM."""
        ext = os.path.splitext(file_path)[1].lower()
        if ext in TEXT_EXTENSIONS:
            loop = asyncio.get_event_loop()
            return await loop.run_in_executor(self._executor, self._read_txt_sync, file_path)
        if ext == ".docx":
            loop = asyncio.get_event_loop()
            return await loop.run_in_executor(self._executor, self._read_docx_sync, file_path)
        try:
            import pymupdf

            doc = pymupdf.open(file_path)
            try:
                text = "\n".join(page.get_text() for page in doc)
            finally:
                doc.close()
            if len(text.strip()) >= 40:
                return text
            if self._is_vision_available and ext in IMAGE_EXTENSIONS.union({".pdf"}):
                return "[Vision OCR fallback not implemented — enable GLM-OCR or use searchable PDF]"
        except Exception as exc:
            return f"[Fallback scan error] {exc}"
        return f"[No text extracted from {os.path.basename(file_path)}]"

# Global instance
ocr_engine = OCREngine()
