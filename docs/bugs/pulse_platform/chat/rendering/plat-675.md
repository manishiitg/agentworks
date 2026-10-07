[← chat / rendering](index.md)

# PLAT-675: Report links to a dead localhost port

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | rendering |
| Summary | Report links pointed at a dead localhost:5173 (stale PUBLIC_URL in a local .env); the chat now opens local share-page links on the app in use |

## What happened

Local, 2026-10-07: an agent's dashboard link was `http://localhost:5173/report?path=V29ya2Zsb3cvbGlua2VkaW4%3D`
(`Workflow/linkedin`). Nothing listens on 5173. `get_report_link` builds links from `effectiveShareBaseURL()`, i.e.
`PUBLIC_URL`, and the owner's local `agent_go/.env` sets `PUBLIC_URL=http://localhost:5173`, a leftover from an old
dev setup. `run_server_with_logging.sh` keeps an explicit `PUBLIC_URL` and otherwise uses the agent server URL. The
same value also feeds chat-driven OAuth callbacks (MCP, Vault, CLI, Gmail), which therefore pointed at the dead port too.

## Fix

- Owner action: remove the `PUBLIC_URL` line from the local `agent_go/.env`; the launcher then sets it to the running
  agent server (`http://localhost:18743`). Re-register any OAuth app whose redirect was `localhost:5173`.
- Chat safety net: `repairLocalAppLink` (utils/sharedLinks.ts), used by the markdown renderer's link check, opens a
  `localhost`/`127.0.0.1` link to one of the app's own share pages (`/report`, `/file`, `/folder` with `path=`) on
  another port at the current local origin. Hosted origins and every other link are left unchanged.

## Verification

GitHub verify (`sharedLinks.test.ts`, markdown renderer tests, type check). After the restart: a report link in chat
opens the dashboard.
