# AgentFlow Onboarding

## This Project Is For

This project is a small legal-workflow demo.

It tries to do these things:

1. Receive a client message from WeChat.
2. Move the case through several legal workflow steps.
3. Use local document search (RAG) to find useful text.
4. Use a local model to generate legal writing.
5. Show progress in a simple web dashboard.

You do not need computer science knowledge to start using it.

## The Main Files

- `main.py`: starts the whole system.
- `agent_flow.py`: the step-by-step legal workflow.
- `wechat_connector.py`: the WeChat connection layer.
- `server.py`: the web server.
- `rag_manager.py`: document upload and search.
- `llm_provider.py`: talks to the local AI model.
- `frontend/index.html`: the simple user page.

## Before You Start

You need Python 3 installed on your Mac.

You also need these Python packages:

```bash
pip install psutil chromadb langchain-huggingface langchain-community langchain-text-splitters tf-keras fastapi uvicorn websockets requests pypdf pytest
```

This project also expects a local AI service at:

```text
http://localhost:11434
```

In simple words:

- If that local AI service is not running, some AI functions will fail.
- If the document embedding model cannot load, document search will fail.
- If WeChat is not really connected, the project uses mock behavior.

## How To Start

In the project folder, run:

```bash
python3 main.py
```

What should happen:

1. The web server starts on port `8000`.
2. The agent flow starts in another process.
3. A test legal text may be loaded into the document database.

## How To Open The UI

Open this file in your browser:

`frontend/index.html`

You can also check server status by visiting:

`http://localhost:8000/status`

## How The Workflow Moves

The legal flow has these stages:

1. `CLIENT_CAPTURE`
2. `INITIAL_CONTACT`
3. `CASE_EVALUATION`
4. `FEE_COLLECTION`
5. `GROUP_CREATION`
6. `MATERIAL_INGESTION`
7. `DOCUMENT_GENERATION`
8. `CLIENT_APPROVAL`
9. `FINAL_PDF_SEND`
10. `ARCHIVE_CLOSE`

Some stages are marked as human approval stages.

That means the system should stop and wait for a person to approve before continuing.

## How To Upload A Document

The server has an upload API:

`POST /upload`

When you upload a `.txt` or `.pdf` file:

1. The file is saved into `data/docs/`.
2. The system splits the file into smaller chunks.
3. The chunks are stored in the vector database.
4. Later, the agent can search those chunks.

## How To Approve A Step

The server has an approval API:

`POST /approve`

It expects JSON like this:

```json
{
  "case_id": "LAW-2026-001",
  "state": "CASE_EVALUATION",
  "approved": true
}
```

This is used for the human approval steps.

## Very Important Current Limitation

Right now, this project is not fully stable.

There is a code mismatch between `agent_flow.py` and `llm_provider.py`.

That means:

- some imports fail before the full app starts,
- some workflow steps are not ready for real use,
- the crash-focused tests in `tests/test_agent.py` are intentionally checking those weak points.

So this onboarding file explains the intended usage, but the current code still needs repair work before it is production ready.

## How To Run The Tests

Run:

```bash
python3 -m pytest -q
```

These tests do two jobs:

1. Check a few basic behaviors that already work.
2. Check failure paths that can break the system.

## Suggested Way To Read This Project

If you are new, read in this order:

1. `ONBOARDING.md`
2. `README.md`
3. `wechat_connector.py`
4. `agent_flow.py`
5. `server.py`
6. `tests/test_agent.py`

That order is easier because it starts from the big picture first, then moves into the code.
