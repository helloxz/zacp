# zacp frontend

Web UI for browsing ACP agents, sessions, and streaming agent updates.

This directory is a placeholder. Scaffold your preferred framework here, for example:

```bash
# example (React + Vite)
npm create vite@latest . -- --template react-ts
```

Suggested responsibilities:

- Agent list / enable-disable
- Chat / session view with streaming tokens & tool calls
- Permission prompts from ACP `requestPermission`
- Workspace / cwd selection

Talk to the backend REST + WebSocket APIs under `backend/internal/api` and `backend/internal/ws`.
