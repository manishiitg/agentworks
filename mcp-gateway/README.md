# CapLayer MCP Gateway (local alpha)

This module is an opt-in, single-user local alpha. It serves a governed MCP endpoint and a CapLayer admin console. Startup rejects public URLs and non-loopback binds until individual sign-in and durable governance storage are implemented.
Do not put a reverse proxy or tunnel in front of its loopback listener; the process cannot detect a proxy configured outside it.

## Start locally

Run the AgentWorks launcher with the gateway enabled, or start `go run ./cmd/server` from this directory. The gateway generates a fresh admin token on each start in `<GATEWAY_STATE_DIR>/admin-token` (default `./var/admin-token`, mode `0600`). The launcher prints the file path. Open CapLayer and enter that token when prompted. An explicitly supplied `GATEWAY_HUMAN_TOKEN` must be unique and at least 32 characters; the old `GATEWAY_LOCAL_ADMIN=1` bypass is rejected.

Private-network upstream MCP servers are disabled by default. Set `GATEWAY_ALLOW_PRIVATE_UPSTREAMS=1` only when that access is intentional. Catalog servers requiring upstream OAuth are labeled unavailable; this version does not hold upstream OAuth credentials.
Upstream URLs with query parameters are rejected so credentials cannot be stored in connector URLs or returned by the admin API.

## Current limits

- MCP client OAuth consent still maps to one local human. The API supports groups and users, but this is not individual team sign-in.
- Users, grants, connector approvals, API keys, PII rules, and audit history are held in memory and disappear on restart. The OAuth token database is separate and persists. Public/team deployment requires durable governance storage and per-user identity.
- The in-memory audit retains the latest 50,000 events in a ring. The PII review queue holds at most 10,000 entries and limits each caller to 100 active reviews. Full history needs durable storage.
- The bundled MCP SDK's `ListTools` method follows upstream `NextCursor` pages automatically.

## Review fixes in this branch

Connector deletion now removes its tool grants, group-server grants, PII rules, review requests, and version history so a new connector cannot inherit them. The admin console requires a secret on loopback, and the standalone admin cookie contains a short-lived session ID instead of the master token. Connector add, resync, and removal are serialized to prevent stale sessions from returning after deletion.
