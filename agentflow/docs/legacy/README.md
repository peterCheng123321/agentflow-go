# Legacy docs

These describe the pre-Go Python system (FastAPI backend, vanilla-JS SPA, in-process state machine). They are kept here for historical reference only; the current architecture lives in:

- `agentflow-go/` — Go backend (HTTP server, RAG, intake, agent loop, embedding sidecar supervisor)
- `AgentFlowUI/` — SwiftUI Mac app (Liquid Glass design, macOS 26+)
- `agentflow-go/scripts/mlx_embed_server.py` — Python embedding sidecar (multilingual MLX)

See top-level `README.md` for current setup. The files in this folder are NOT a guide to the current system.
