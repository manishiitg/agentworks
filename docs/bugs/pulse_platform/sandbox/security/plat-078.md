[← platform / security-sandbox](index.md)

## Internal reconciliation — 2026-09-05

PUL-DD9EDE3C: current browser oversized-result, production spill and every-folder-guard tool_output_folder tests pass; also covered by PLAT-200.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-078 — spilled bridge tool output (large agent_browser snapshots) landed outside every granted read path

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | sandbox |
| Area | security |
| Summary | P2 — a step gets an unrecoverable, unactionable failure when |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — both contributing mechanisms are fixed; runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P2 — a step gets an unrecoverable, unactionable failure when
  a browser snapshot is large enough to overflow the coding CLI's own
  MCP result cap
- **Owner:** step execution folder guard (`setupExecutionFolderGuard`,
  `controller_agent_factory.go`)
- **Found on:** PLAT-073 cluster F, `dd9ede3c` (upwork): *"agent_browser
  snapshot results continue to overflow the tool-result size limit
  post-v1.0.21-purification (toptal-scan-jobs 68,223 chars,
  search-find-and-shortlist 130,705 chars on 2026-08-07), and the
  spilled-result fallback read also fails as outside every workspace root."*

## Root cause

Two independent, contributing mechanisms — only the first is fixed here:

1. **`agent_browser`'s `snapshot` command has no output-size cap.** Claude
   Code's own MCP tool-result cap (independent of, and smaller than,
   mcpagent's own 128 KiB bridge-result limit) truncates a large snapshot and
   spills the untruncated copy to a location under its own CLI project
   directory — the exact same failure shape already named and fixed for
   `read_skill` (`mcpagent/agent/skill.go:21-33`, `maxReadSkillBatchSize=1`,
   *"the coding CLI truncated it against its 25,000-token result cap, wrote
   the full copy under its own project directory, and told the agent to read
   that path — which the workspace folder guard forbids. The agent had no
   legal way to comply."*). `agent_browser` has no equivalent bound.
2. **`MCP_TOOL_OUTPUT_DIR` (mcpagent's own 128 KiB inline-truncation spill
   target) resolves outside the folder guard's grants.** It's set to
   `<CodingAgentWorkingDir>/tool_output_folder`
   (`mcpagent/agent/coding_agents_bridge.go:198-200`), and
   `CodingAgentWorkingDir` resolves to the bare workflow root
   (`resolveCodingAgentWorkingDir`, `pkg/orchestrator/base_orchestrator_agent_factory.go:174-183`,
   fed `bo.GetWorkspacePath()`) — a sibling of `runs/` that
   `setupExecutionFolderGuard` never granted a normal execution step read
   access to. Confirmed against the live filesystem:
   `workspace-docs/Workflow/<name>/tool_output_folder/` exists as a sibling
   of `runs/`, `db/`, `execution/`, holding spilled files up to 16 MB that a
   normal step could not read back.

## Fixed (2026-08-10)

`setupExecutionFolderGuard` (`controller_agent_factory.go:427`) now adds
`<workspace-root>/tool_output_folder` to `readPaths` for every step. This
closes mechanism 2 directly — a step that hits mcpagent's own 128 KiB spill
can now read the result back — without touching
`resolveCodingAgentWorkingDir`, which is shared by every coding-agent step
and would have been a far riskier, broader-blast-radius change to make just
for this one symptom.

Test: `TestExecutionFolderGuardGrantsToolOutputFolderRead`
(`controller_agent_factory_test.go`) — asserts the read grant exists and,
mirroring the sibling run-folder-widening test, that granting it does not
widen access to the bare workflow root.

## Mechanism 1 fixed (2026-08-10)

The initial `6b688f51c` implementation narrowed default arguments and
truncated the returned snapshot. That prevented an overflow, but it silently
changed the agent's requested evidence surface; it was immediately superseded.

The replacement runs the exact `snapshot` command the agent supplied. If the
raw result exceeds 24,000 runes, it returns a typed
`SNAPSHOT_RESULT_TOO_LARGE` error containing the raw rune/byte count and clear
retry options (`--selector <css>` or `--depth <n>`). It returns no partial
snapshot, does not inject `--compact`, and does not alter any explicit option.
The agent therefore makes the next evidence-scope decision itself.

This is not a regression of the folder-guard fix: the earlier change made an
already-spilled mcpagent result readable; this change prevents an independent,
smaller coding-CLI result limit from receiving an unbounded page tree at all.
Tests cover exact argument preservation and the typed oversized-result error.

## Verification

- `go build ./...` clean.
- New tests pass; full baseline
  (`go test ./cmd/server/... ./pkg/orchestrator/agents/workflow/step_based_workflow/...`)
  still shows exactly 22 pre-existing failures — no new failures.
- **Not yet reverified live** — requires a restart and an upwork run that
  produces a large `agent_browser` snapshot before `dd9ede3c` can be closed.

## Acceptance

- A step that triggers mcpagent's 128 KiB bridge-result spill can read the
  spilled file back from `tool_output_folder/` without a folder-guard denial.
- A large snapshot returns `SNAPSHOT_RESULT_TOO_LARGE` with no transformed or
  partial evidence.
- The retry is agent-chosen and explicit: a targeted selector or smaller depth.
