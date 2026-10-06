[← platform / integrations](index.md)

# PLAT-475 — Agent-started OAuth sign-ins redirect to a port nothing listens on (local runs)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | integrations |
| Area | integrations |
| Summary | fixed on `main`: `run_agentworks` now exports `PUBLIC_URL` as the frontend it serves, beating a stale `agent_go/.env` value. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; takes effect the next time the app is launched with `run_agentworks` |
| Date | 2026-10-04 |
| Owner | integrations |
| Related | PLAT-474 (consent screen name) |

## Source

The owner connected the Upwork MCP server from an agent chat. After approving, the
browser went to `http://localhost:5173/api/oauth/callback?code=...` and said
"localhost refused to connect". The sign-in itself had worked once the owner pasted the
URL back to the chat (the server exchanged the code and saved the token).

## Cause

A sign-in an agent starts has no request to take its address from, so the callback comes
from `PUBLIC_URL`. `agent_go/.env` (local, git-ignored) had
`PUBLIC_URL=http://localhost:5173`, Vite's default port; the app runs on 51733.
Sign-ins started from the UI derive the address from the page and were not affected.

## Done

- `agent_go/run_server_with_logging.sh` (what `run_agentworks` runs): when it starts the
  frontend and `PUBLIC_URL` is not already exported, it exports
  `PUBLIC_URL=http://localhost:<frontend port>`. The server's `.env` loader never overrides
  an exported variable, so this wins over the stale file value. A `PUBLIC_URL` exported in
  the shell (a deployment) is untouched. It prints the callback address at start-up.

## Left

- Providers that check the callback exactly (Upwork, Google) need
  `http://localhost:<port>/api/oauth/callback` registered in their app settings; the
  Upwork app was registered with `localhost:5173`. Until that is added, Upwork refuses the
  new address. The `.env` line can be deleted.
- Not tried with a fresh launch.

## Register notes

[PLAT-475](plat-475.md), fixed on `main`: `run_agentworks` now
exports `PUBLIC_URL` as the frontend it serves, beating a stale `agent_go/.env` value.
Provider apps that check the callback exactly need that address registered.
