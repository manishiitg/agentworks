[← platform / integrations](index.md)

# PLAT-132 — a permanently-broken MCP server is re-attempted on every server restart

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | integrations |
| Area | integrations |
| Summary | P3 — nothing is broken for users and no run fails because of it. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — diagnosed, deliberately not fixed yet |
| Last synchronized | `2026-08-18` |

- **Priority:** P3 — nothing is broken for users and no run fails because of it. The
  cost is ~30s of startup work and 4 error-level log lines per restart, for a
  server that cannot succeed, plus the diagnostic confusion of those errors
  looking like a real workflow failure.
- **Owner:** `agent_go/cmd/server/tools.go`
  (`initializeToolCache`, `runBackgroundDiscovery`, `discoveryFailedServers`)

## Symptom

Every server startup logs a full connection failure cycle for the `MiniMax`
MCP server:

```
Connecting to MCP server  server="uvx [minimax-coding-plan-mcp]"
Connection attempt failed  attempt=1 ... transport error: transport closed
Connection attempt failed  attempt=2 ...
Connection attempt failed  attempt=3 ...
Connection attempt failed  attempt=4 ...
Connection failed  duration=29.589920917s
```

This surfaced while investigating an unrelated workflow question, because
error-level lines from a startup cache-warm are indistinguishable in the log
from errors belonging to the workflow that happened to be running at the time.

## Root cause of the underlying server failure

The `MiniMax` entry in the MCP config declares no dependency pin:

```json
"MiniMax": { "command": "uvx", "args": ["minimax-coding-plan-mcp"] }
```

`minimax-coding-plan-mcp` imports `mcp.server.fastmcp`, which only exists in
`mcp >= 1.8`. Unpinned, `uvx` resolves an `mcp` that lacks it, and the process
dies on import before speaking the protocol — which the platform sees as
`transport closed`. Reproduced directly:

```
$ uvx minimax-coding-plan-mcp --help
ModuleNotFoundError: No module named 'mcp.server.fastmcp'

$ uvx --with 'mcp>=1.8,<2' minimax-coding-plan-mcp --help
ValueError: MINIMAX_API_KEY environment variable is required   # past the import; expected
```

`google-sheets`, two entries above it in the same file, already carries exactly
that pin (`--with 'mcp>=1.8,<2'`) and works. So the immediate trigger is a
missing pin on one entry.

## Why that is not the interesting part

The config entry is incidental. Two platform behaviours make a single bad entry
cost something on every restart:

**1. Discovery is demand-independent, deliberately.** `initializeToolCache`
iterates every server in the merged config and hands any cache miss to
`startBackgroundDiscovery`, with no check for whether any workflow references
it. Verified against real `selected_servers` config (not step prose) across all
19 workflows: **zero** select `MiniMax`. `trading`'s step descriptions do tell
the agent to "use the MiniMax server", but that workflow's `selected_servers`
is `[]`, so the instruction is unreachable text — worth its own look, unrelated
to this ticket.

This is not a bug on its own. `api.toolStatus` feeds `GET /api/tools`
(`handleGetTools`), the UI's MCP server/tool catalog. A picker has to list what
is *available*, not what is already used, and a server can only be listed after
connecting once to learn its tools. Scoping discovery to
workflow-referenced servers would make a newly added server undiscoverable
until something already used it. So catalogue-everything is the right shape.

**2. The failure memory only recognises auth errors.** There *is* a skip list,
and it is exactly the right idea — but its classifier is too narrow
(`tools.go:948`):

```go
if strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "401") ||
   strings.Contains(errMsg, "OAuth") || strings.Contains(errMsg, "forbidden") ||
   strings.Contains(errMsg, "403") {
    api.discoveryFailedServers[serverName] = errMsg   // never retried
}
```

Its comment justifies the skip as *"Auth/unauthorized errors won't resolve
without config changes."* That reasoning applies just as well to a server whose
process dies on import: `ModuleNotFoundError` will not resolve without a config
change either. It simply is not an auth error, so it is never remembered, never
skipped, and is retried in full on every restart forever.

A failed discovery also never writes a cache entry, so the server is
permanently a cache miss — there is no other mechanism that would damp it.

## Suggested repair (not implemented)

Remember deterministic non-auth failures the same way auth failures are already
remembered, so any permanently-broken server is attempted once and then skipped
until its configuration changes. The cache key is already config-aware
(`GenerateUnifiedCacheKey(serverName, serverConfig)`), so editing the entry
naturally re-qualifies it for a retry — no manual reset needed.

Two things to decide when picking this up, both of which is why it is filed
rather than fixed:

- **What counts as deterministic.** `transport closed` on startup is a strong
  signal (the process never spoke the protocol), but a genuinely transient
  cause — a machine briefly out of disk, a slow first `uvx` download — can
  produce it too. Marking too aggressively risks a server staying skipped after
  a one-off blip. A retry budget (skip after N consecutive identical failures)
  is likely safer than classifying on the message alone.
- **Whether the skip should expire.** Auth failures skip until config changes,
  which is right for a credential problem. A dependency problem can be fixed
  *outside* the config — a fresh package release, a cleared `uv` cache — with
  the config byte-identical, so a config-keyed skip would never re-attempt it.

Removing the unused `MiniMax` entry would silence this instance today, but
leaves the general behaviour in place for the next broken server.

## Acceptance

- A server that fails discovery for a deterministic, non-auth reason is not
  re-attempted on every subsequent restart.
- Editing that server's config re-qualifies it for discovery.
- A transient failure does not permanently suppress a server that would
  otherwise recover.
- `GET /api/tools` still lists every configured server, including ones no
  workflow references.
