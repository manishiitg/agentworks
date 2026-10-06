[← platform / coding-agent-bridge](index.md)

# PLAT-275 — Every agent start spent ~19 s per pass (38 s per Builder turn) retrying an MCP connector that can never connect without a login

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P1 — a 52 s `hi` on RTS, 38 s of it before the model launched; every turn and every scheduled run of a workflow with an unauthenticated connector paid it. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — deployed to RTS 2026-09-03 and verified live; see Verification |
| Last synchronized | `2026-09-03` |

- **Priority:** P1 — a 52 s `hi` on RTS, 38 s of it before the model launched; every turn and every scheduled run of a workflow with an unauthenticated connector paid it.
- **Owner:** `mcpagent/mcpclient/client.go` (`Connect`, `ConnectWithRetry`, `isNonRetryableConnectError`), `mcpagent/agent/connection_session.go` (lazy path only after a first successful connect).
- **Origin:** live, RTS `rtslatency` Builder chat 2026-09-03 (`[LATENCY_DEBUG] T+38375ms | Starting StreamWithEvents` for `hi`); reported by the operator.

## What happened

`rtslatency` selects the Notion connector. The admin user has no Notion OAuth login on RTS, so its tool list was never cached and the lazy path (cache hit → defer) never applied; every agent build attempted the connection. The chat agent and the Workshop session it creates each ran their own pass: 2 × 19 s.

## Root causes

- `oauth.ErrNoValidToken` ("no valid token available - run authentication flow": the token file `<tokens root>/<user>/Notion.json` does not exist; no network is involved) was retried like a transient failure.
- The retries nested by accident: `ConnectWithRetry` (4 attempts, 1/2/4 s backoff) called `Connect`, which has its own 3-attempt loop (1 s + 2 s). 4 × 3 + 7 = 19 s of pure sleep around twelve stats of a missing file.

## Fix

- Missing OAuth token is non-retryable in both loops (mcpagent `13dabbb`, `TestConnectFailsFastWithoutOAuthToken`).
- `ConnectWithRetry` makes single attempts (`connectOnce`), so transient failures get four tries, not twelve (`b39c978`, `TestConnectWithRetryDoesNotNestConnectRetries`).

## Verification

Live on RTS after release `9d48c370c-20260903135124`: the same `hi` reached `Starting StreamWithEvents` at `T+399ms`; both connector passes completed in under 0.5 ms. Remaining time-to-first-token (~14 s) is cursor-agent's own startup and is not platform time.

## Follow-ups (not done)

- Skip OAuth connectors that have no token at agent build and say "Notion needs sign-in" in the chat instead of attempting and logging an error.
- Reuse the chat agent's connection results for the Workshop session instead of a second pass.
- Classify retries by cause (connection refused, timeout, 5xx) instead of by count; a 4xx or a refused registration should fail once.
- The hosted "Cloudflare Docs" catalogue entry returns HTTP 410 and costs 19 s at server boot; update or drop it.
- Cross-reference: [PLAT-102](plat-102.md) measured perceived send latency but never saw this pre-launch cost because its traces had authenticated connectors.

