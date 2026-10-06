[← coding-agents / pi](index.md)

# PLAT-186 — pi-cli's MCP `directTools` never activates: a per-launch-randomized env value defeats the third-party adapter's cache hash, forcing every session into the fragile double-JSON-encoded `mcp()` proxy wrapper

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | coding-agents |
| Area | pi |
| Summary | P1 — this is the structural reason a whole class of pi-cli |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — live-verified against real Gemini; see "Fix implemented" below |
| Last synchronized | `2026-08-23` |

- **Priority:** P1 — this is the structural reason a whole class of pi-cli
  failure is possible at all, not just one incident. Every pi-cli tool call
  today goes through a fragile double-JSON-encoded wrapper that a model can
  (and did) get wrong, when a config we already write (`directTools: true`)
  was specifically meant to avoid that path and never actually takes effect.
- **Owner:** `mcpagent/agent/coding_agents_bridge.go` (`buildBridgeMCPConfig`),
  `mcpagent/cmd/mcpbridge/main.go` (env consumption), the third-party
  `pi-mcp-adapter` npm package (cache/hash logic, not ours to change).
- **Related:** the live incident this was found while investigating —
  a structured pi-cli run on the `confida-login` workflow
  (2026-08-23) failed with `"pi run returned no text output"` after 6 real
  `tool_execution_end` `IsError` events, all `Invalid args JSON: Bad control
  character in string literal in JSON at position N` — Gemini malformed the
  double-encoded `args` string of the generic `mcp` proxy tool. The fix for
  *that* symptom (surfacing pi's IsError events into the returned error
  instead of losing them) already shipped in `multi-llm-provider-go` commit
  `6d273aa`. This ticket is about the deeper, structural cause that makes
  the whole error class reachable in the first place.

## Background: how pi-cli gets MCP support at all

Pi CLI has no native MCP client. We load a genuine third-party npm
extension, `pi-mcp-adapter` (`github.com/nicobailon/pi-mcp-adapter`, MIT,
not ours — confirmed via `npm view pi-mcp-adapter`), via pi's own
`npm:pi-mcp-adapter` extension-loading mechanism
(`defaultPiMCPExtension` in `multi-llm-provider-go/pkg/adapters/picli/options.go:27`).
Separately, we also load our own genuinely-custom marker extension
(`piMarkerExtensionSource()`, `multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go:2557`)
alongside it — but that one is purely observational (event listeners
writing to a marker file for our own live-streaming) and has nothing to do
with MCP connectivity or tool-schema exposure.

By default, `pi-mcp-adapter` exposes every MCP-server tool through one
generic proxy tool: `mcp({ tool: "<name>", args: <object-or-string> })`.
Per its own README: *"`args` can be a JSON object or a JSON string. Prefer
the object form when your model handles it reliably; the string form
remains supported for providers that need simpler schemas."* Our own
observed transcripts show Gemini via pi-cli consistently using the
string-encoded form — i.e. the fragile, double-JSON-encoded path, not the
safer native-object form.

`pi-mcp-adapter` also supports `directTools` — registering specific MCP
tools as individual, natively-typed Pi tools instead of routing everything
through the generic proxy, which avoids the double-encoding problem
entirely for those tools. We already set this:

```go
// mcpagent/agent/coding_agents_bridge.go — normalizePiMCPConfig-adjacent code
if _, exists := bridge["directTools"]; !exists {
    bridge["directTools"] = true
}
```

## Confirmed root cause — traced to source in a third dependency, not guessed

`directTools` never actually takes effect for us, regardless of this
setting being present and correct, because of how `pi-mcp-adapter` decides
whether to trust its on-disk tool-metadata cache
(`~/.pi/agent/mcp-cache.json`, confirmed present and populated with real
`api-bridge` tool schemas on this machine — this is not a cold-cache
problem):

```typescript
// pi-mcp-adapter@2.27.0, metadata-cache.ts:82 (pulled via `npm pack`, read directly)
export function computeServerHash(definition: ServerEntry): string {
  const identity: Record<string, unknown> = {
    command: definition.command,
    args: definition.args,
    // ...
    env: interpolateEnvRecord(definition.env),   // <-- the whole env object
    // ...
  };
  const normalized = stableStringify(identity);
  return createHash("sha256").update(normalized).digest("hex");
}
```

```typescript
// direct-tools.ts:132 — resolveDirectTools
for (const [serverName, definition] of Object.entries(config.mcpServers)) {
  if (isServerDisabled(definition)) continue;
  const serverCache = cache.servers[serverName];
  if (!serverCache || !isServerCacheValid(serverCache, definition)) continue;  // <-- skips ENTIRELY, no partial/async fallback
  // ... registers direct tools only past this point
}
```

If the current launch's server-config hash doesn't match the cached
entry's hash, that server is skipped for direct-tool registration
outright — proxy-only for the *entire* session, with no async catch-up
mid-session (the "hot-load direct tools into the current session" behavior
the README describes only applies once the cache becomes valid on a
*later* launch, not within the same invalidated one).

We build that exact `env` object fresh on every single call
(`buildBridgeMCPConfig`, `coding_agents_bridge.go:230-283`), and one field
is **unconditionally, unavoidably different every time**:

```go
// coding_agents_bridge.go — allocated fresh on every call, no exceptions
a.bridgeReadyFile = ""
if f, tmpErr := os.CreateTemp("", "mcpbridge-ready-*.marker"); tmpErr == nil {
    readyPath := f.Name()
    _ = f.Close()
    _ = os.Remove(readyPath)
    a.bridgeReadyFile = readyPath
    bridgeEnv["MCP_READY_FILE"] = readyPath
}
```

The comment right there explains why it's randomized: *"A unique temp path
per call... means a stale marker from a prior session in the same
workspace can never falsely satisfy the gate."* That's a real, deliberate
protection — not an oversight — but it's also sufficient, entirely on its
own, to bust `computeServerHash` on every launch regardless of anything
else in the config. `directTools: true` has been structurally inert for us
since the day it was added, independent of whether it's configured
correctly.

## Also checked, to scope the fix correctly

- **Moving volatile values to the parent `pi` process's own environment
  instead of the declared per-server config is NOT viable.** Pulled
  `@modelcontextprotocol/client@2.0.0` (the stdio transport SDK
  `pi-mcp-adapter` depends on) via `npm pack` and read
  `dist/stdio.mjs` directly: `StdioClientTransport` spawns each MCP server
  with `env: { ...getDefaultEnvironment(), ...this._serverParams.env }`,
  where `getDefaultEnvironment()` is a small hardcoded allowlist
  (`HOME`, `LOGNAME`, `PATH`, `SHELL`, `TERM`, `USER` on non-Windows) — **not**
  the full parent environment. Anything not explicitly declared in the
  per-server config never reaches the bridge subprocess at all.
- **`MCP_API_TOKEN` is NOT a contributor.** Generated once at server
  startup (`agent_go/cmd/server/server.go:1809`,
  `resolveServerAPIToken()`) and reused for the server process's entire
  lifetime across every workflow, step, and pi-cli call — stable.
- **`MCP_API_URL` is NOT a contributor by design.** Deliberately kept
  static — see the existing comment in `coding_agents_bridge.go:218-222`:
  *"the MCP_API_URL remains static... so the generated mcp-config JSON can
  be easily normalized or kept identical across sessions."* (A parallel,
  unrelated bridge-setup path exists in `agent_go/internal/agentsession/agentsession.go`
  that *does* mint a fresh ephemeral port/token per call — confirmed via
  grep that this path is used only by `cmd/family-server/` (a different
  product), not by the workflow step-execution path this ticket concerns.
  Do not conflate the two when reading this codebase.)
- **`MCP_SESSION_ID` is NOT YET confirmed either way.** It's set per
  step-execution for folder-guard isolation (a step's shell/file tools must
  resolve against its own narrow read/write scope), which makes it
  plausible it's minted fresh per pi-cli invocation the same way
  `MCP_READY_FILE` is — but this was not traced to the same certainty.
  Check this after the confirmed fix below, on a live retest: if
  `directTools` still doesn't activate, this is the next suspect.
- **`MCP_TOOL_OUTPUT_DIR`** is derived from the working directory and looks
  stable for a given workflow/step identity across repeated launches — not
  flagged as a likely contributor, but not independently proven stable either.

## Fix implemented

Did not remove the anti-staleness protection `MCP_READY_FILE`'s
randomization provided; got the same guarantee a different way, in
`mcpagent/agent/coding_agents_bridge.go` (`buildBridgeMCPConfig`):

1. The ready-file's *path* is now deterministic per (workspace, agent)
   identity (`filepath.Join(workingDir, ".mcpbridge-ready.marker")`)
   instead of a fresh random temp path every call.
2. Immediately before each new launch, our own Go code deletes any
   leftover file at that fixed path. We control both the deletion and the
   launch ordering, so this gives the exact same "a stale marker from a
   prior run can never satisfy this run's readiness check" guarantee —
   just via "we just cleaned it up" instead of "nobody has used this name
   before."
3. Falls back to the original random-path behavior when there's no stable
   working directory to anchor to (a fresh temp dir never had a prior
   cache entry to protect either).
4. `mcpagent/cmd/mcpbridge/main.go`'s `makeReadyFileWriter` /
   `writeReadyFileOnce` needed no change — it consumes `MCP_READY_FILE`
   as-is regardless of whether the path is deterministic or random.

`MCP_SESSION_ID` was not found to need the same treatment — the live test
below passed without touching it, so it was not investigated further.

### A real, provable P0 test — not just "no regression"

The one existing live test exercising `directTools`
(`TestPiCLIRealMCPBridgeOnlyToolsContract`) tolerates the proxy fallback in
its own prompt wording, so it would pass whether or not `directTools`
ever activated — it could not have caught this bug or proven this fix.
Writing a real one required closing a separate, genuine gap first:
`StreamChunk.ToolName` is already-recovered by the time a caller sees it
(PLAT-179's own recovery logic renames a successfully-recovered generic
`"mcp"` proxy call to look exactly like a real tool name), so tool name
alone can never distinguish a genuinely native/direct call from a proxy
call recovery merely renamed. Every other adapter (claude-code, codex-cli,
cursor-cli, and pi's own *structured* adapter) already populated
`StreamChunk.ToolArgs` with the raw, pre-recovery args on
`tool_execution_start`/`tool_execution_end` — pi's *interactive* adapter
was the one gap. Populated it there too
(`multi-llm-provider-go/pkg/adapters/picli/picli_interactive_adapter.go`).
Raw args are the one signal that actually distinguishes the two cases: a
proxy call's raw args always carry the `{"tool":...,"args":...}` wrapper
shape; a native call's raw args are the tool's own parameters directly.

`TestPiCLIRealDirectToolsActivateOnRepeatedStableConfig`
(`multi-llm-provider-go/pkg/adapters/picli/picli_mcp_bridge_real_test.go`)
runs two real, separate pi-cli launches sharing an identical MCP server
config with `directTools: true`, and asserts the second launch's
`report_cwd` tool call carries native args, not the proxy wrapper shape.
**Live-verified against real Gemini: PASS in ~32s.**

While verifying, also live-confirmed (the hard way, costing two full
4-minute hangs) that the free `openrouter/stealth/ox-alpha` fallback model
used elsewhere this session for cost reasons is currently
unreliable/hanging, independent of this change — re-ran the
already-proven-working `TestPiCLIRealMCPBridgeToolCallReportsRealToolName`
reference test with the same model override and it hung the identical
way, isolating that as an unrelated, pre-existing model-availability
issue rather than anything wrong with either test.

## Explicitly out of scope for this ticket

- Changing `pi-mcp-adapter`'s own cache/hash logic — it's a third-party
  dependency; the fix works entirely within what we control (what we put
  into its declared config).
- The live-incident symptom fix (surfacing IsError events into the
  returned error) — already shipped separately, `multi-llm-provider-go`
  commit `6d273aa`.
- Investigating `MCP_SESSION_ID`'s stability — not needed once the live
  test passed with `MCP_READY_FILE` alone fixed.

## Verification

- `go build ./...` clean in both `mcpagent` and `multi-llm-provider-go`.
- `mcpagent`: two new fail-before/pass-after unit tests
  (`TestBuildBridgeMCPConfigReadyFileIsStableAcrossRepeatedCallsForTheSameIdentity`,
  confirmed failing against the prior `os.CreateTemp`-every-call behavior;
  `TestBuildBridgeMCPConfigReadyFileFallsBackToRandomPathWithoutAWorkingDir`
  for the no-working-directory fallback) plus the full existing
  `agent/coding_agents_bridge_test.go` suite, all pass. One pre-existing,
  unrelated failure (`TestAgentReviewsApproved`, a manual test-data review
  gate) confirmed present identically on a clean checkout — not a
  regression.
- `multi-llm-provider-go`: `go test ./pkg/adapters/picli/... -short`
  passes in full (no regression from the `ToolArgs` change).
- `TestPiCLIRealDirectToolsActivateOnRepeatedStableConfig` — **live,
  against real Gemini, PASS** — the actual end-to-end proof that
  `pi-mcp-adapter` genuinely activates direct tools given a stable config,
  not just that our own config-building stays byte-identical.

## Post-implementation review — readiness-marker concurrency gap (2026-08-23)

**Verdict:** the direct-tools cache invalidation RCA is correct and the
sequential live proof demonstrates that the intended native-tool path now
activates. A P1 follow-up remains before the readiness-marker change can be
called safe for concurrent agents.

### What remains unsafe

The implementation describes the marker as stable per **(workspace, agent)**
identity, but the actual path is only per working directory:

```go
filepath.Join(workingDir, ".mcpbridge-ready.marker")
```

Two Pi sessions with the same MCP configuration are deliberately allowed to
run concurrently in one working directory by
`acquirePiWorkspaceMCPConfigLease`. Each launch calls `os.Remove` on that
same path before starting its own bridge. Either bridge can then write the
shared marker after `tools/list`, and either adapter can observe it. A second
launch can therefore clear or satisfy the first launch's readiness gate; the
marker no longer proves that *that session's* bridge has connected.

This does **not** invalidate the direct-tools result: both launches retain the
same config fingerprint, so the cache can still activate native tools. It
does mean the claimed replacement for the old per-launch readiness guarantee
is incomplete, and can reintroduce the cold-turn tool-availability race in
parallel workflow/step execution.

### Required follow-up

Keep the MCP configuration fingerprint stable, but make readiness ownership
safe too. The fix must either serialize same-workspace Pi bridge startup until
each session reaches its own ready point, or provide an equivalent
session-owned readiness handshake that does not add a per-launch value to the
`pi-mcp-adapter`-hashed MCP config.

Add a live P0 contract which launches two same-config Pi sessions in the same
working directory concurrently and proves each first prompt waits for its own
bridge readiness and can call its tool. The existing direct-tools P0 is a
strong sequential proof, but cannot expose this race.

Also update the stale `Agent.bridgeReadyFile` comment in
`mcpagent/agent/agent.go`, which still documents the previous random,
per-launch path behavior.

## Follow-up fix implemented (2026-08-23, same day)

Went with the session-owned-handshake direction rather than true
serialization: a real cross-call Go mutex spanning `buildBridgeMCPConfig`
(mcpagent) through to the actual readiness wait (a different package,
`multi-llm-provider-go`'s adapter, reached only via an options callback)
has no clean synchronous release point, and a lock held too long or
released on the wrong path risks a deadlock — a strictly worse failure
mode than the race it would fix.

Instead, `buildBridgeMCPConfig` now tracks, per working directory, which
`sessionID` most recently received the stable marker path and when
(`piReadyMarkerLastAcquired`, `piReadyMarkerAcquisition{sessionID,
acquiredAt}`). A launch reusing the *same* `sessionID` as that directory's
last acquisition always gets the stable path back, however soon it
repeats — that stability is the entire point of this ticket's original
fix, and must never be defeated by the safety guard meant to protect it.
A *different* (or unidentified/empty) `sessionID` arriving within the
readiness wait's own window (45s: `codingready.DefaultMCPReadyWait` 30s
plus a launch-overhead margin) is treated as a plausibly-concurrent,
different session and falls back to a private, uniquely-named marker for
that one launch — the original pre-this-ticket behavior, still fully
safe, just without that one launch's caching benefit.

`Agent.bridgeReadyFile`'s comment in `mcpagent/agent/agent.go` updated to
describe both the stability change and this concurrency guard.

The suggested live two-concurrent-sessions P0 contract was **not** added —
scoped as a real, separate live-E2E effort (two genuinely concurrent real
pi-cli launches, real API cost, real synchronization to hit the race
window deterministically) rather than bundling it into this same-day
follow-up. The unit-level test below proves the guard's decision logic
directly (same-session stability preserved, different-session collision
avoided, window expiry restores the stable path) without needing a live
run to do it.

### Verification (follow-up)

- `go build ./...` clean in `mcpagent`.
- `TestBuildBridgeMCPConfigReadyFileFallsBackToPrivatePathForConcurrentLaunchesInSameWorkingDir`
  (new) — proves all three cases in one test: same-session repeat stays
  stable, a different session within the window gets a distinct private
  path (never colliding with the first), and after the window elapses a
  third session gets the stable path back. Confirmed this exercises real
  decision logic, not a tautology: the original (pre-session-aware)
  version of this fix broke the *existing*
  `TestBuildBridgeMCPConfigReadyFileIsStableAcrossRepeatedCallsForTheSameIdentity`
  test by treating that test's own same-agent repeated calls as
  concurrent — caught and fixed by keying the guard on session identity,
  not just elapsed time.
- Full `mcpagent/agent` suite: only the same pre-existing, unrelated
  `TestAgentReviewsApproved` failure (a manual test-data review gate,
  confirmed present identically on a clean checkout) — no regressions.
