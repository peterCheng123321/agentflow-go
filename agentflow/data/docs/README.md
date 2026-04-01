# LegalCaseAgent CRM

Local-first legal intake CRM with dynamic case handling, lightweight document retrieval, and a pluggable WeChat connector.

## Main Docs

- Final installation guide: `INSTALLATION.md`
- Sandbox notes: `openclaw_sandbox/README.md`

## Main Commands

Bootstrap:

```bash
bash scripts/bootstrap_safe_stack.sh
```

Configure WeChat Official Account:

```bash
bash scripts/configure_wechat_official_account.sh
```

Start backend:

```bash
bash scripts/start_backend.sh
```

Start full local stack:

```bash
bash scripts/start_local_stack.sh
```

Cleanup:

```bash
bash scripts/uninstall_safe_stack.sh
```

## What Works

- dynamic cases
- CRM frontend
- lightweight local RAG
- OpenClaw runtime detection by CLI
- WeChat read/send/group/RAG-signal app contract

## Important

The remaining production step is connecting your real WeChat Official Account credentials and public webhook to the installed OpenClaw runtime.
