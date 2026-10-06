[← browser / automation](index.md)

# PLAT-215 — guarded `agent_browser download` already uses platform staging; correct the stale external-only diagnosis

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | browser |
| Area | automation |
| Summary | P3 — the original data-loss report was real for a direct |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — current guarded execution prevents the unsafe third-party destination write |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — the original data-loss report was real for a direct
  third-party destination write, but it is not the current guarded platform
  behavior.
- **Owner:** `agent_go/pkg/browser/artifact_broker.go`,
  `workspace/security/browser_artifact.go`.
- **Related:** `harness:agent_browser.download` (`Workflow/ICICI-BANK-PARSING`,
  medium; canonical after `merge_pulse_issues` folded `PUL-8F2C747E` into it).

## Corrected diagnosis

The earlier ticket record said `download <selector> <destination>` passed
straight to the third-party `agent-browser` CLI. That is stale. When the
session has an enabled folder guard, `Executor.HandleAgentBrowser` calls
`prepareBrowserArtifact("download", ...)`, replaces the requested destination
with a fresh path under `/tmp/agentworks-browser-artifacts/`, and sends the
original destination only as an `ArtifactTransfer` instruction to the trusted
workspace server.

The third-party CLI therefore writes only to the managed staging location. On
successful completion, `FinalizeBrowserArtifact` validates that the original
destination is inside the workspace, covered by the current write allowlist,
and not blocked; only then does it atomically publish the staged file. An
unauthorized destination fails at this platform-owned finalization boundary,
after the third-party download has safely completed to staging. The platform
does not ask `agent-browser` to write the unauthorized destination, so its
cleanup-on-denial behavior is not exercised.

## Evidence

- `TestPrepareBrowserArtifactRewritesDownloadDestination` proves the CLI
  destination is replaced with managed staging.
- `TestHandleAgentBrowserBrokersExplicitDownload` proves the outgoing browser
  command contains only the staging path while the transfer retains the
  requested destination.
- `TestExecuteShellBrowserArtifactDestinationIsWorkspaceRelative` proves the
  trusted workspace server publishes only through the artifact-transfer path.
- `FinalizeBrowserArtifact` enforces workspace containment, write-path
  coverage, blocked-path checks, regular non-empty staged files, and atomic
  publish.

## Scope

This protection applies to guarded workflow/session calls. Unguarded local
browser use intentionally keeps direct-path behavior because there is no
session authorization boundary to enforce. If a new data-loss report occurs,
first verify whether that call was unguarded or bypassed the `agent_browser`
executor before treating it as a third-party regression.

No new code is required by this correction; the safe broker was already
shipped. The previous "no platform fix available" disposition was inaccurate.
