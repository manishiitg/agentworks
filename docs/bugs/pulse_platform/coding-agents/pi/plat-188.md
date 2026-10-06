[← coding-agents / pi](index.md)

# PLAT-188 — `MCP_SERVER_API_TOKEN` regenerates on every server restart, busting pi-mcp-adapter's cache for every workspace's first post-restart launch

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | coding-agents |
| Area | pi |
| Summary | P3 — bounded, self-healing degradation (one throwaway launch per |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — deferred by explicit operator decision; not a bug to fix reflexively, a tradeoff to revisit later |
| Last synchronized | `2026-08-24` |

- **Priority:** P3 — bounded, self-healing degradation (one throwaway launch per
  workspace per restart, then normal for the rest of that server's uptime),
  not a correctness bug. Deferred at the operator's explicit request; do not
  "fix" this without asking first — see "Decision" below.
- **Owner:** `agent_go/cmd/server/server.go` (`resolveServerAPIToken`).
- **Related:** found while live-verifying [PLAT-187](plat-187.md) after a
  server restart. Directly touches the same `computeServerHash` mechanism
  [PLAT-186](plat-186.md) root-caused; PLAT-186's own text says `MCP_API_TOKEN`
  "is generated once at server startup... and reused for the server
  process's entire lifetime" — true within one process, but says nothing
  about *across* restarts. This ticket is that missing other half.

## What happens

```go
// agent_go/cmd/server/server.go:190
func resolveServerAPIToken() string {
	if token := strings.TrimSpace(os.Getenv(envMCPServerAPIToken)); token != "" {
		return token
	}
	return executor.GenerateAPIToken()
}
```

Unless `MCP_SERVER_API_TOKEN` is explicitly set, the token is freshly
randomized on every server startup. It's part of the `api-bridge` MCP
server's declared `env` block, which `pi-mcp-adapter` hashes
(`computeServerHash`, per PLAT-186) to decide whether its on-disk
tool-metadata cache is valid for the current launch. A changed token means
a changed hash means a guaranteed cache miss for every workspace's first
pi-cli launch after any server restart — `directTools` can't activate yet,
so PLAT-187's `disableProxyTool` default can't hide the `mcp()` proxy tool
either, for that one launch. Live-confirmed on the `trading` workflow
immediately after a restart: `mcp({search: "shell"})` succeeded (the proxy
was visible and answered), while the very next call to
`api_bridge_execute_shell_command` in the *same* session worked as a
native direct call — proving the cache warmed mid-session, and the earlier
proxy call was exactly this one-time post-restart gap, not a regression in
PLAT-187's fix.

## Trade-off (laid out in full, per the operator's ask)

**Option A — leave as-is (current default).** Every server restart implicitly
rotates this credential. A token leaked from a *previous* server lifetime
(an old log line, a stale `.pi/mcp.json` captured in a bug report, a pasted
config screenshot) becomes worthless the moment the server next restarts —
automatic, no manual rotation needed. Cost: every workspace's first pi-cli
launch after each restart pays a one-time proxy-fallback tax (native
`directTools` and the hidden-proxy benefit only apply once that
workspace's cache repopulates, typically the very next launch).

**Option B — fix `MCP_SERVER_API_TOKEN` to a static value.** Eliminates the
post-restart cache-miss window entirely; the first pi-cli launch after any
restart gets native tools + hidden proxy immediately. Cost: removes the
implicit rotation-on-restart property. A leak from months ago stays
exploitable forever until someone manually rotates the value. Note this
doesn't create a *new* exposure during a live session — the server binds
to `127.0.0.1` only, so exploiting the token at all already requires local
code execution on this exact machine, at which point the current token is
readable straight out of the live `.pi/mcp.json`/process env regardless of
this setting. What Option A actually protects against is narrower: a token
captured and stored *outside* the live session (logs, screenshots,
backups) being replayed after the server has since restarted.

**Option C — persist the token to a local file on first generation**
(e.g. `~/.config/agent_go/mcp-server-token`), reuse it on every subsequent
startup, rotation becomes an explicit action (delete the file) instead of
an automatic side effect of every restart. Gets Option B's reliability win
for *normal* restarts while keeping rotation possible and intentional
rather than removed outright.

## Decision (2026-08-24)

Operator: keep current behavior (Option A) for now — "for now security is
fine." Recorded as a pending item with the trade-off spelled out for a
later revisit, not implemented. Do not change `resolveServerAPIToken`
without re-confirming the choice; this is a deliberate stance, not an
oversight.

## Verification

N/A — no code change made. This ticket exists to record the trade-off
analysis and the explicit decision to defer, not to track fix progress.
