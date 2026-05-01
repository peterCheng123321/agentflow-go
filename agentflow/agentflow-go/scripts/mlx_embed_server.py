#!/usr/bin/env python3
"""Tiny HTTP wrapper around mlx_embeddings — load a multilingual embedding
model once and expose POST /embed {"input": [...]} -> {"embeddings": [[...]]}.

Used by the Go embedrouter package. Speaks an Ollama-compatible response
shape so the same Go client can talk to either runtime.

Usage:
    python3 scripts/mlx_embed_server.py \
        --model mlx-community/multilingual-e5-small-mlx \
        --host 127.0.0.1 --port 8095
"""

import argparse
import json
import logging
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import mlx.core as mx
from mlx_embeddings import load, generate

log = logging.getLogger("mlx_embed_server")

# Loaded by main(); referenced by the handler.
MODEL = None
PROCESSOR = None
MODEL_NAME = None
LOCK = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        log.info("%s - %s", self.address_string(), fmt % args)

    def do_GET(self):
        if self.path in ("/health", "/v1/health"):
            self._json(200, {"status": "ok", "model": MODEL_NAME})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path not in ("/embed", "/api/embed"):
            self._json(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            req = json.loads(body)
            texts = req.get("input")
            if texts is None:
                self._json(400, {"error": "missing 'input'"})
                return
            if isinstance(texts, str):
                texts = [texts]
            if not isinstance(texts, list) or not all(isinstance(t, str) for t in texts):
                self._json(400, {"error": "'input' must be a string or list of strings"})
                return

            t0 = time.time()
            with LOCK:
                arr = generate(MODEL, PROCESSOR, texts)
                # mlx_embeddings.generate may return a 2D mx.array
                # (batch, dim) directly, or a model-output dataclass with a
                # `.text_embeds` attribute (e.g. CLIP-style models). Handle both.
                if hasattr(arr, "text_embeds"):
                    arr = arr.text_embeds
                # Force eval, then to numpy/list. mx.array.tolist() works.
                mx.eval(arr)
                vecs = arr.tolist()
            dt_ms = int((time.time() - t0) * 1000)

            self._json(200, {"model": MODEL_NAME, "embeddings": vecs, "took_ms": dt_ms})
        except Exception as e:
            log.exception("embed failed")
            self._json(500, {"error": str(e)})

    def _json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="mlx-community/multilingual-e5-small-mlx",
                    help="HF repo id (must be an MLX-format embedding model)")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8095)
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO,
                        format="%(asctime)s [%(levelname)s] %(message)s",
                        stream=sys.stderr)

    log.info("loading %s ...", args.model)
    t0 = time.time()
    global MODEL, PROCESSOR, MODEL_NAME
    MODEL, PROCESSOR = load(args.model)
    MODEL_NAME = args.model
    log.info("model loaded in %.1fs", time.time() - t0)

    # Warm up so first request isn't slow.
    _ = generate(MODEL, PROCESSOR, ["warmup"])
    log.info("ready at http://%s:%d (POST /embed)", args.host, args.port)

    # allow_reuse_address is True by default on HTTPServer but we set it
    # explicitly so test runners can rebind the port across rapid restarts
    # without waiting for TIME_WAIT to clear.
    ThreadingHTTPServer.allow_reuse_address = True
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        log.info("shutting down")
        srv.shutdown()


if __name__ == "__main__":
    main()
