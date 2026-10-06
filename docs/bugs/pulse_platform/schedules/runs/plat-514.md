# PLAT-514: webhook step cannot write the workflow database: "mutate_workflow_db caller does not own this tool session"

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | runs |
| Summary | open, diagnosed to the tool lookup path, not fixed. |

**State:** fixed and verified live on RTS for the Crew/Code entry path (run 756, 2026-10-05 14:14 UTC). GitHub-webhook and MCP entry paths not yet tested. P1. P1 (blocks the `rtsprreviweer` PR review on RTS).

**Found:** 2026-10-05 11:27 UTC, RTS, the 4th webhook run of `rtsprreviweer` (PLAT-502 had just made the run folder writable, so the step now gets as far as the database). `pr-eligibility-gate` wrote `route_selection.json`, then `mutate_workflow_db` (the INSERT into `pr_gate_decisions`) failed with `mutate_workflow_db caller does not own this tool session` (`agentworks_db.DBError`, session `session-group-default-1791199672093001445`, HTTP session `schedule-webhook--d04df8c1_…`).

**What the logs and code show:** the check is `bindToolExecutionContextForSession` (`agent_go/cmd/server/tool_execution_context.go`): the caller's tool session must equal the executor's own, or be a registered child of it. A step's `session-group-*` session has no registry of its own; `CallCustomToolWithSession` (mcpagent `agent/codeexec/registry.go`) sends it to its parent run's registry only if that parent registered the tool, else to the shared global table where the last registration wins. At the same second the SDE Private Crew (`code:project:3b40f2c4…`, which started this review) was running a tool, so the global executor was very likely bound to that Crew's session. This is the PLAT-355 class (09-23) recurring for webhook runs; the "falling back to global" line is debug level, so its absence from the log proves nothing. NOT yet confirmed: that a webhook run registers its tools in its own session registry (`controller_agent_factory.go:2030` registers for step sessions; the webhook path was not traced).

**Next:** trace what a webhook run registers under `schedule-webhook--…`; make its tools session-scoped (no global fallback) so a concurrent Crew cannot take them; re-run the PR review from SDE Private while the Crew is active (that is the failing shape).

## 2026-10-05: findings from the live runs, and the Builder skill

- Run 747: the gate wrote `route_selection.json` (PLAT-502 fixed) but recorded `skip` / `webhook_delivery_unresolved` with an empty `run_id`: it never found the delivery. The delivery files on RTS are group-readable and the platform grants the file to the step (`controller_agent_factory.go` read and read-only paths), so the cause (variable not set, or the file not readable inside the step's sandbox) is NOT proven. The gate swallows the read error, which is why. The gate's fallback looks for `.webhook-run-id` one folder below where the server writes it, so it cannot find it.
- A Crew function call delivery is `variables` = the three inputs plus `payload` = `{function, args, from, call_id}`; the gate reads only the top level of `payload`. GitHub, MCP/bearer and function calls all reach a step through the same delivery file.
- The Builder skill `webhook-triggers` now has "How a step reads its trigger input (all entry paths)": the shape per path, the read order (`variables`, then `payload.args`/flat keys, then the provider event, then `VAR_*`), never turning an unreadable delivery into `skip` without recording the reason, testing each path, and treating `caller does not own this tool session` as a platform fault. The owner will point the Builder at it to fix the gate.
- Still open here: the `[TOOL_OWNERSHIP]` diagnostic needs one more run; whether to copy the delivery into the run's own folder.

## 2026-10-05 12:27 UTC: cause proven by the diagnostic, fixed

Run 748 logged `[TOOL_OWNERSHIP] rejected mutate_workflow_db: caller="session-group-default-…" tool_session="code:project:3b40f2c4-…" authority="code:project:3b40f2c4-…" caller_parent="schedule-webhook--d04df8c1_…"`. The step's bridge session is correctly registered under the webhook run, but the executor it was handed was bound to the SDE Private Crew's session: the first suspected cause (global table, last writer wins) was right. A webhook run's tools are registered by its workflow session (a sibling of the step's bridge session), not by the HTTP run id, so `registryScopeForSession` found no tool on the parent and fell to the global table, which the Crew chat (running at the same second because it started the review) had just written.

Fix (mcpagent d07fe58): `mcpclient.MCPSessionsForHTTPSession` lists a run's live sessions; when the parent has no registry for the tool, `siblingScopeForTool` uses the run's other session if exactly one registered it (none or several keeps the legacy lookup). One regression test in `agent/codeexec/registry_test.go`. Not changed: the global table itself.

**Left:** re-trigger the PR review from SDE Private while the Crew is active (the failing shape); expect no `[TOOL_OWNERSHIP]` line and a decision in `pr_gate_decisions`. The gate's own handling of the delivery (empty owner/repo) is separate and is the Builder's change (see the `webhook-triggers` skill).

## 2026-10-05 13:20 UTC: the first fix did not work; the real gap

Run 752 (after deploying d07fe58) was rejected the same way. The log of that minute shows session-scoped tool registrations only for the SDE Private Crew (`code:project:3b40f2c4…`); the webhook run registered no session-scoped tools at all, under any of its sessions, so the sibling lookup had nothing to find. A server-owned run's tools (`createCustomTools(true, user, session)`, which includes `mutate_workflow_db`) went only into the global table.

Fix (builder, server.go workflow branch): for a run session (`schedule-…`: scheduled, webhook and Slack runs) register the run's own executors with `codeexec.InitRegistryForSession(sessionID, …)`, so the step's bridge session resolves to its parent run (the PLAT-355 path), and `codeexec.CleanupSession` when the run ends. Not for chat sessions: they keep their own registry and cleanup would clear it. Checked that all 39 run tools, including `mutate_workflow_db`, keep their executor type. mcpagent d07fe58 stays (harmless when the parent has the tool).

**Left:** re-trigger from SDE Private; expect no `[TOOL_OWNERSHIP]` line.

## 2026-10-05 13:30 UTC: why the gate never found its delivery

The Builder rewrote the gate (13:16) to record why a delivery could not be read; run 752 still recorded `webhook_delivery_unresolved` with no error, which means `WORKFLOW_TRIGGER_INPUT_FILE` was not in the step's environment at all. The workspace service's `isAllowedShellExtraEnvKey` (`workspace/handlers/shell.go`) drops every extra variable not on its allowlist before running a step's command; `VAR_*`, `STEP_*` and `SECRET_*` were allowed, the trigger file, `WORKFLOW_TRIGGER_CONTEXT_FILE`, `WORKFLOW_KB_*`, `WORKFLOW_DB_ACCESS` and `RUN_FOLDER` were not. This broke every entry path (GitHub, MCP, Crew), not only Crew calls, and knowledge-base paths in scripted steps too. Fix: allow `WORKFLOW_TRIGGER_*`, `WORKFLOW_KB_*`, `WORKFLOW_DB_ACCESS`, `RUN_FOLDER` (paths and flags, no secrets); the allowlist test now pins the trigger variable.

## 2026-10-05 13:44 UTC: run 753 passed the gate; the review step could not start a shell

After the run-tool registration and the trigger-variable fixes, run 753 resolved the Crew's request (owner/repo/PR 180, head sha, reason `eligible`), the branch step completed and no `[TOOL_OWNERSHIP]` rejection occurred. The next blocker: the review step's shell refused to start: `SANDBOX_UNAVAILABLE: this Folder Guard policy cannot be enforced: policy path is unavailable: stat …/Workflow/rtsprreviweer/learnings/_global: no such file or directory`. The platform always lists `learnings/_global` (and `learnings/<step>`) as readable for a step; this workflow never created it, and the sandbox treated one missing READ folder as fatal (`canonicalPolicyPaths`, `workspace/security/isolator_linux.go`). Fix: read-only folders use the existing optional mode (a missing one grants nothing and cannot be granted later); writable folders and folders that exist but cannot be read still fail closed. One Linux test pins both.

**Left:** deploy, re-trigger, check the review is posted (a GitHub review on PR #180, and its decision in `pr_gate_decisions`).

## 2026-10-05 13:56 UTC: run 754, the same sandbox error again: the folder is a WRITE grant

The read-only fix (b1d91d9) did not change run 754: `learnings/_global` was still refused. For a message_sequence item whose learnings access is read-write, `setupMessageSequenceFolderGuard` (`controller_message_sequence.go`) grants `learnings/_global` as WRITABLE, and the sandbox correctly keeps refusing a missing writable folder. The read-only change stays (the platform also lists optional folders as read-only, and a missing one grants nothing), but it was not the failing path. Fix: create the global learnings folder (idempotent, through the workspace API, group-writable under the slot umask) when the write grant is built. Run 754 had passed the gate (owner/repo/PR 180, `eligible`) and the branch step, with no `[TOOL_OWNERSHIP]` rejection.

## 2026-10-05 14:14 UTC: verified live (Crew path)

Run 756, started from SDE Private on release 32eb951 (workspace service restarted 14:05:23): gate resolved the request (Real-Training-Systems/course_designer PR 181, `eligible`), branch and review steps completed, the review step posted an APPROVE as `qa-test-rts` on the PR's head SHA (CI had no failing, pending or missing required checks) and wrote `PR ok`. No `[TOOL_OWNERSHIP]` rejection since 13:20 and no sandbox refusal since 13:44, both before the fixes. The causes, in the order they surfaced: owner-only run folders (PLAT-502), the Crew's tool copy handed to a webhook step (run-tool registration, mcpagent d07fe58 and server.go), `WORKFLOW_TRIGGER_*`/`WORKFLOW_KB_*`/`WORKFLOW_DB_ACCESS`/`RUN_FOLDER` dropped by the workspace shell env allowlist, the gate (the Builder's rewrite), and the sandbox refusing a missing `learnings/_global` (created when granted; a missing read-only folder is skipped). A suspected stale `/usr/local/libexec/agentworks/slotctl` (2026-10-01) was NOT the cause: the policy is built in the workspace service.

**Left:** test the GitHub-webhook path and the MCP path (the gate was rewritten for all three; only the Crew path has run); the old `slotctl` copy on RTS is still from 10-01 and is worth refreshing at the next root step.

## Register notes

[PLAT-514](plat-514.md), P1, open, diagnosed to the tool lookup path, not fixed. Blocks the RTS PR review once the run folder is writable (PLAT-502).
