[← browser / automation](index.md)

## Internal reconciliation — 2026-09-05

PUL-DD9EDE3C: bounded snapshot output, recovery guidance, granted full-result spill and folder read access verified with focused tests; PLAT-078's historical report is covered.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-200 — oversized `agent_browser` snapshots were silently discarded, not just poorly retried; `--depth` was also wrongly listed as an equal-weight retry option on wide/flat pages

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | browser |
| Area | automation |
| Summary | P2 — real evidence loss on every QA/scan step that hits a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — recovery command corrected after post-fix review |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — real evidence loss on every QA/scan step that hits a
  wide page, forcing either a blind narrower retry or a full second-cost
  re-run just to recover data the platform already had in memory once.
- **Owner:** `agent_go/pkg/browser/executor.go` (`handleOversizedSnapshot`),
  `agent_go/cmd/server/virtual-tools/workspace_browser_tools.go`
  (`spillOversizedBrowserOutput`).
- **Related:** `harness:oversized-tool-result-recovery` (confida-login,
  medium). The finding's own text: workflow-plan-level prompt guidance for
  this got merged into the same block as an unrelated fix and was wiped out
  by a revert of that other fix — but its own "underlying platform defect...
  is UNCHANGED and still real" claim (an oversized `agent_browser` result is
  unrecoverable, and `--depth <n>` is a confirmed no-op on a wide/flat page)
  is what this ticket verifies and fixes directly in platform code, so it
  survives independently of any workflow-plan block getting reverted again.
  Builds on PLAT-078 (`tool_output_folder` read grant) and PLAT-175
  (`message_sequence` step builder parity for that same grant).

## Two separate, both real, claims — verified independently

**1. `--depth <n>` is a no-op on a wide/flat page.** Live-reproduced, not
assumed: a synthetic 500-sibling flat page (`agent-browser open file://...`,
real Chrome via CDP) returned a byte-identical snapshot at `--depth 2` vs.
unlimited depth (24,256 bytes both), while `--depth 1` collapsed it to 6,364
bytes. `--depth` genuinely limits nesting depth exactly as its own `--help`
documents — the gap is that a wide/flat page's bloat comes from **sibling
count**, not nesting depth, so any `--depth` value at or above the page's
actual (shallow) max depth does nothing. The harness finding's own evidence
(byte-identical 30,365-rune retries on two different dates) matches this
exactly.

**2. An oversized snapshot's excess data was never persisted anywhere.**
Traced `handleOversizedSnapshot`'s default (non-`--full-snapshot`) path:
`head := string([]rune(*output)[:maxInlineSnapshotOutputRunes])` overwrites
the output var with only the head — the rest is discarded from memory, not
written to disk. The only recovery path was `--full-snapshot`, which
re-runs the *entire* (possibly expensive) snapshot command a second time to
get data the platform already had once. Confirmed `Executor` had no
workspace-path field at all — this package has never had a way to persist
anything.

## Fix

**Guidance (depth caveat).** `handleOversizedSnapshot`'s retry-option banner
now explains *why* to pick an option instead of listing them in try-order:
`--depth` is now explicitly flagged as only helping when the truncated head
shown to the agent looks deeply nested, not wide; `--compact` and
`--interactive` were added as real alternatives for wide/flat and
action-only cases respectively.

**Persistence (the bigger fix).** `Executor` gained an optional
`SpillOversized OversizedOutputSpiller` hook (`WithOversizedOutputSpiller`).
When set, the default truncation path calls it with the full untruncated
tree; on success the banner points the agent at the registered
`execute_shell_command(command="sed -n '1,240p' <saved-path>")` recovery
command instead of only offering a `--full-snapshot` re-run. The command can
read the already-granted `tool_output_folder` in bounded chunks, so the agent
can request later line ranges without re-running the snapshot. This replaced
the previous, incorrect reference to the test-only `read_workspace_file`
helper.
A nil hook, or a hook that errors, falls back to exactly today's behavior —
no regression, no crash.

The production wiring (`spillOversizedBrowserOutput`,
`workspace_browser_tools.go`) **never invents a write location.** It reads
the calling session's own already-granted `ReadPaths`
(`common.GetSessionShellConfig(sessionID)`) and only writes if an entry
ending in `tool_output_folder` is actually present — reusing PLAT-078's
proven grant instead of re-deriving a workspace path independently. No
session on context, no folder guard configured, or no `tool_output_folder`
grant → declines and returns an error, which the executor package treats as
"no spill available," not a hard failure.

The write itself uses `workspace.Client.UpdateWorkspaceFile` (the same HTTP
document-write path every other workspace file write already uses — never a
raw local `os.WriteFile`, since `agent_go`'s server process does not own
`workspace-docs/` directly; that's the separate `workspace/` Go module's
job) scoped with `workspace.WithSystemManagedWritePaths(ctx,
toolOutputDir)` — the documented, existing mechanism for "trusted Go-side
tools write access to system-owned paths that remain blocked from shell and
general file tools," since `tool_output_folder` is deliberately read-only
for agent-facing writes by design (PLAT-073 cluster F: only the bridge
machinery writes there).

## Explicitly not done

- Did not touch the workflow-plan-level prompt block the harness finding
  describes getting reverted — this fix lives in platform code specifically
  so it can't be lost to a plan-block revert again, which supersedes the
  finding's own suggested remediation (reapply the plan-level guidance as a
  separable block).
- Did not raise or remove `maxInlineSnapshotOutputRunes` itself — it exists
  specifically to stay under a *third*, smaller cap (Claude Code's own MCP
  tool-result cap) that spills to a location outside every folder guard
  entirely if hit; this fix persists the excess before that layer ever sees
  it, rather than trying to raise the threshold past it.
- Did not thread a `workspace_path` argument onto the `agent_browser` tool
  schema — the session's own folder-guard state was already sufficient to
  verify the grant, so no new agent-facing argument was needed.

## Verification

- `go build ./...` clean (only the pre-existing, unrelated onnxruntime
  linker warning).
- `go test ./pkg/browser/... ./pkg/workspace/... ./cmd/server/virtual-tools/...`
  — all pass, including 4 new tests for `spillOversizedBrowserOutput`
  (declines with no session, declines with no folder guard, declines with
  no `tool_output_folder` grant, and a real `httptest`-backed write proving
  the exact HTTP PUT path and body) and 4 for the executor-level spill hook
  (spiller succeeds and the banner surfaces the real path; no spiller
  configured falls back cleanly; spiller error falls back cleanly and is
  never propagated as a call failure).
- Full-suite `go test ./...`: 19 pre-existing failures, none in any package
  this ticket touched (`pkg/browser`, `pkg/workspace`,
  `cmd/server/virtual-tools` all pass clean) — no regression.
- Live-reproduced the `--depth` no-op claim directly against real Chrome via
  `agent-browser` (not simulated) before writing the guidance fix.
- Not yet reverified live against a real confida-login oversized-snapshot
  run in production.
