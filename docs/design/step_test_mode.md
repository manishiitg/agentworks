# Step test mode

Ticket: [PLAT-562](../bugs/pulse_platform/goals/steps/general/plat-562.md). Asked for by
[PLAT-559](../bugs/pulse_platform/goals/pulse/general/plat-559.md) and [A simpler Pulse](pulse_simplified.md)
("A test mode for steps with external effects", "Goal for QA").

## Goal

Run a changed step once to verify a fix without real-world side effects, so the Builder and Pulse can test before a
change counts as done, including steps such as Upwork `bid-pick-job` (claims a proposals row for a real job),
`bid-submit` (spends Connects, submits a proposal) and outreach steps (send messages).

Reads stay real. Anything with an external effect is stubbed and recorded ("would have run"), or redirected into a copy
that belongs to the test run. **Test mode fails closed:** an action whose effect cannot be classified is stubbed, never
run.

## How a step runs in test mode

- `execute_step(step_id, test_mode=true)` in the workshop (Builder) and in Pulse, which verifies with the same tool.
  The external API's proxied `execute_step` passes `test_mode` through.
- The run gets its own folder `runs/test-<id>/<group>/`. The real run folder's `execution/` tree is copied in first
  (capped at 512 MB) so the step reads what earlier steps produced.
- The workflow DB is copied with `VACUUM INTO` (consistent, committed WAL rows included) to `runs/test-<id>/db/db.sqlite`.
- Every tool session the step controller sets up while the test run is active is registered as a test session
  (`pkg/testmode`). Children inherit it (`CopySessionFolderGuard`, chat delegation). The workshop group session the
  saved-script fast path calls the bridge with is registered too.
- A test run starts only when nothing else runs in that workshop, and nothing else starts while it runs: the run
  shares the workshop's controller and its group tool session.
- The step learns it is in test mode from a `## TEST MODE` section in its prompt, and from `AGENTWORKS_TEST_MODE=1`
  in its shell and script environment. A stubbed call returns `TEST MODE: <tool> was NOT run (<reason>)`, telling the
  agent to continue as if it succeeded and never to look for another way to perform it.
- The result starts with a summary of every stubbed action. `runs/test-<id>/test_mode_actions.jsonl` records each
  guarded call as it happens; `runs/test-<id>/test_mode.json` is the run's record.

## One check for every tool call

mcpagent has one pre-call check (`toolguard`) at every dispatch point: the agent loop (sequential and parallel), the
executor HTTP handlers that CLI coding agents and scripts call over the bridge, and the code-exec registry. AgentWorks
installs its test-mode policy there. Calls from sessions outside a test run are untouched.

## Effects and what test mode does

| Effect | How it can happen | Test mode |
|---|---|---|
| MCP tool calls | Any connected MCP server (Upwork, Gmail, Slack, place MCPs, Vault-granted tools) | **Allow** only when the server lists the tool with `readOnlyHint: true` and not `destructiveHint: true`, read live from the connection at call time. Otherwise **stub + record**. A missing annotation, an unlisted tool or a lookup error stubs. |
| agent_browser | One platform tool; the effect depends on the command | **Allow** `status`, `skills`, `tab`, `open`/`goto`/`navigate`, `back`, `forward`, `reload`, `snapshot`, `get`, `is`, `screenshot`, `pdf`, `console`, `errors`, `wait`, `scroll`. **Stub + record** everything else: `click`, `fill`, `type`, `press`, `select`, `hover`, `upload`, `download`, `network` routes, `record`/`capture`/`trace`, and `eval` (JavaScript can submit a form as easily as read a title, so it cannot be classified). |
| Workflow DB | `query_workflow_db`, `mutate_workflow_db`, scripts with `$DB_PATH`, the `agentworks_db` helper over the bridge | **Redirect to the copy**: the DB tools resolve the test run's copy; `DB_PATH` names the copy; the real `db/` is write-blocked. Migrations and backup snapshots are **stubbed**. No DB to copy means no DB access. |
| Workspace files | File tools, native CLI file edits, shell and scripts | **Redirect**: write grants are narrowed to the test run folder; every other entry of the workflow folder (db, learnings, knowledgebase, code, planning, other runs) and every attached or Crew folder is write-blocked, applied each time the session's config is read so no later grant widens it. |
| Shell commands and scripts | `execute_shell_command`, saved `main.py` | **Run** with the DB copy, the test folder and `AGENTWORKS_TEST_MODE=1`; their bridge calls go through the guard. **Not contained: outbound network.** A script that posts with `curl` or an HTTP library reaches the real site. The sandbox's network deny exists only for strict profiles on macOS, and blocking it would also break reads. |
| Messaging | Slack/Gmail/WhatsApp MCP tools, `notify_user`, `send_slack_message`, `human_feedback`, `create_human_input_request`, `google_workspace_cli` | **Stub + record** (MCP send tools are not read-only; the platform tools are not on the test-mode list). |
| Workflow functions, triggers, delegation | `call_generic_agent`, chat delegation tools, webhooks, schedule tools | **Stub + record** (not on the list). Orchestrator sub-steps (`call_sub_agent`, `call_scripted_sub_agent`) **run inside the test run**: their sessions are registered by the same controller. |
| Crew calls | `crew` step type | **Refused**: the step fails in test mode, because a Crew call acts in another project. |
| Schedules | Schedule tools | **Stub + record** (not on the list). |
| Learnings and knowledge | Reflection turn, direct learning writes, `update_knowledgebase`, script save into learnings, scripted run stats, freshness confirmations | **Off**: no reflection turn, learnings access reduced to read, script save and stats skipped, confirmations skipped, KB writes stubbed and the folders write-blocked. |
| Goal metrics and Pulse | `record_goal_observations`, run folders Pulse reads | `record_goal_observations` **stubbed**. Pulse's step-concern and step-output scans and Pulse intake skip `runs/test-*`. The execution carries `test_run_id` in its metadata. No run metadata, retry-recovery record or scheduled-run binding is written. |
| Model calls | `generate_text_llm`, the step's own model | **Allow** (cost only, no external effect). |
| Anything else | A tool not listed above, a new tool | **Stub + record.** Adding a tool to the allow list is a reviewed code change in `pkg/testmode`. |

## Trust and limits

- `readOnlyHint` is the server's own claim. A server that marks a write tool read-only defeats test mode for that
  tool. A per-connection override (owner marks a tool read-only or not) is future work.
- Browser navigation is real: a GET that has side effects (an unsubscribe link, a magic-login link) runs. In CDP mode the
  browser is the owner's logged-in Chrome.
- Shell network is not contained (above). The prompt tells the agent not to work around a stub.
- A session the controller did not set up and that does not inherit from a test session is not in test mode. Every
  step session path known today is covered; a new path must register itself.
- Full-workflow runs (`run_full_workflow`) have no test mode yet.

## Not built yet

- `run_full_workflow(test_mode=true)` and a Pulse rule that requires a test-mode verification for steps with external
  effects before a fix counts as verified.
- Per-connection read-only overrides for MCP tools without annotations.
- A network allow list for test-mode shells (reads to named hosts only).
- A UI marker for test runs in the run history.
