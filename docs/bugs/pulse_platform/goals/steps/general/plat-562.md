[← goals / steps / general](index.md)

# PLAT-562: Step test mode: verify a step without real-world side effects

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/general |
| Summary | Run a changed step once in test mode: reads real, external effects stubbed and recorded, DB a copy, files in a test run folder, no learnings. |

## What happened

Verifying a fix needs a real run, but steps like Upwork `bid-pick-job` (claims a proposals row for a real job),
`bid-submit` (spends Connects, submits a proposal) and outreach steps (send messages) act on the outside world. Without a
test mode the "test before save" and verification-run parts of [PLAT-559](../../pulse/general/plat-559.md) only work for
harmless steps.

## Fix

Design: [Step test mode](../../../../../design/step_test_mode.md) (every effect type and what test mode does with it).

- mcpagent `toolguard`: one pre-call check at every tool dispatch point (agent loop sequential and parallel, executor
  HTTP handlers for the CLI bridge and scripts, code-exec registry); MCP calls carry a live lookup of the tool's
  annotations. mcpagent b266ace (1260330 + a comment fix; 1260330's message says PLAT-560 by mistake).
- `pkg/testmode`: test runs keyed by tool session; the guard policy (MCP allowed only with `readOnlyHint` and not
  destructive; browser read commands only, `eval` stubbed; a short list of read-only or redirected platform tools;
  everything else stubbed and recorded). Installed at package init, so every binary that links `pkg/common` has it.
- `execute_step(test_mode=true)`: run folder `runs/test-<id>/<group>`, upstream `execution/` copied in, DB copied with
  `VACUUM INTO`, all step sessions registered, writes confined to the test folder (applied whenever a session's config
  is read), `DB_PATH` and the DB tools point at the copy, `AGENTWORKS_TEST_MODE=1`, a `## TEST MODE` prompt section,
  no learnings/KB writes, no script repair, Crew steps refused, run metadata and retry records skipped. One test run at
  a time per workshop, never alongside another execution there. Result starts with the stubbed-action summary; record in
  `runs/test-<id>/test_mode.json` and `test_mode_actions.jsonl`.
- Pulse step-concern/step-output scans and Pulse intake skip `runs/test-*`.

Evidence: `TestStepTestModeContainsExternalEffects` (step_based_workflow) drives the real bridge path: mcpagent executor
HTTP handlers, a real MCP server over streamable HTTP, the real DB tools against the real workspace query/mutate
handlers and SQLite. In a test session the read-only MCP tool and browser snapshot run; `submit_bid`, the browser
click and `notify_user` never reach their targets; the DB write lands in the copy and the real DB keeps one row; the
record lists each action. The same calls from a normal session reach the server and the real DB. With the guard
removed the test fails (`submit_bid` reached the server). Not yet run live through a Builder chat on a server.

## Judgment calls

- Browser `eval` is stubbed even for reads: JavaScript cannot be classified as read-only.
- MCP: only an explicit `readOnlyHint: true` allows a call (mcp-go defaults new tools to `readOnlyHint: false`). The
  server's claim is trusted; no per-connection override yet.
- Shell runs, with the DB copy and confined writes, but **outbound network is not contained**: blocking it would break
  reads, and the sandbox's network deny exists only for strict macOS profiles. Documented, and the prompt forbids
  working around a stub.
- Orchestrator sub-steps run inside the test run; chat-style delegation and generic agents are stubbed.
- Crew steps fail in test mode rather than run half-contained.
- Upstream outputs are copied (not linked) so a test run can never write into the real run folder; capped at 512 MB.
- A test run blocks other executions in its workshop because it shares the controller and the group tool session.

## Left

- Not deployed.
- Live check through a Builder chat on an isolated server (a scripted step and an agent step on a temp workflow).
- `run_full_workflow(test_mode=true)`; Pulse rule: a fix to a step with external effects counts as verified only after a
  test-mode run (PLAT-559 migration step 5).
- Per-connection read-only overrides for MCP tools without annotations; a network allow list for test-mode shells.
- Run-history UI marker for test runs.
