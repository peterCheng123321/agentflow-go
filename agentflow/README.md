# AgentFlow

A legal-operations Mac app for Chinese-language legal workflows. Native SwiftUI frontend, Go backend, cloud LLMs (DashScope `qwen-plus`) for synthesis, with a local MLX embedding model spine for routing and retrieval.

## Architecture

The SwiftUI app spawns a bundled `agentflow-serve` Go process. The Go process supervises an MLX embedding sidecar (Python). The sidecar runs `mlx-community/multilingual-e5-small-mlx` and serves at port 8095. Intent classification, dense RAG, and matter-type inference all use the embedding model; cloud LLM calls go to DashScope for actual generation.

Default ports: 8000 (Go API), 8090 (legacy LLM router, opt-in), 8095 (embedding sidecar).

## Repo layout

| Path | Purpose |
| --- | --- |
| `agentflow-go/` | Go HTTP server + supervisors + tests |
| `agentflow-go/scripts/mlx_embed_server.py` | Python embedding sidecar |
| `agentflow-go/tests/` | pytest suite + `MANUAL_TESTS.md` checklist |
| `AgentFlowUI/` | SwiftUI Mac app |
| `data/` | runtime data (cases, vector store, OCR cache); ignored by git |
| `docs/legacy/` | pre-Go documentation (historical only) |

## Setup / run

Requires Apple Silicon, Python 3.12+, Go 1.22+, and a DashScope API key at `~/Library/Application Support/AgentFlow/secrets/dashscope_api_key.txt`.

```
pip3 install mlx-embeddings mlx-lm
cd agentflow-go && go build -o agentflow-serve ./cmd
# The Mac app at /Applications/AgentFlow.app spawns this binary automatically.
# For dev: ./agentflow-serve & open /Applications/AgentFlow.app
```

## Tests

```
cd agentflow-go && pytest tests/
```

~23 tests, ~45s, covering local routing and DashScope-backed E2E flows. Manual checklist at `agentflow-go/tests/MANUAL_TESTS.md`.
