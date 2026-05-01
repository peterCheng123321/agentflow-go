# AgentFlow — Setup

## Quick Start

```bash
# Setup + start in one command
python setup_app.py

# Or separately
python setup_app.py --setup
python setup_app.py --start
```

That's it. This will:
1. Create a `.venv` virtual environment
2. Install Python dependencies
3. Create required data directories
4. Start the FastAPI server on `http://127.0.0.1:8000`

## Useful Endpoints

- `GET /` — Dashboard (frontend/index.html)
- `GET /v1/status` — System status
- `GET /v1/cases` — List cases
- `GET /v1/rag/summary` — RAG knowledge base status

## Cleanup

```bash
rm -rf .venv data/
```

## Notes

- **Apple Silicon (M1/M2/M3/M4)**: MLX local inference is available. First request will be slow (model download + load).
- **Intel Mac / Linux**: Falls back to Transformers (CPU-only, slower).
- **WeChat**: Runs in mock mode by default. OpenClaw bridge is optional.
