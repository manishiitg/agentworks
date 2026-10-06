[← platform / security-sandbox](index.md)

# PLAT-296 — Isolate workflow chat CLI prompts and skills across concurrent Owner and Run sessions

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | records the delivered two-mode execution model. |

> **Update 2026-09-30 ([PLAT-371](plat-371.md)):** the private per-session runtime folder stays for workflow Builder/Run chats. Crew and Code run in the project folder and no longer overwrite or delete a project's instruction files: adapters add a marked, session-counted block and `mcpagent`'s cleanup removes only what a session projected. The collisions and "restore or remove" behavior described below for a *shared* folder are superseded there. Moving workflows to the shared folder too is planned: [plan](../../../../design/workflow_shared_folder_plan.md), [design](../../../../design/project_instruction_files.md).

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented on main — concurrent Builder and Run chats use separate prompts, tools, skills and private CLI runtimes while sharing the guarded workflow workspace; scheduled runs use the Run surface. Cross-provider cleanup and real two-user authorization remain follow-up acceptance work` |
| Last synchronized | `2026-09-07` |

- **Related:** [PLAT-262](plat-262.md) (server-enforced Run permissions),
  [PLAT-297](../coding-agent-bridge/plat-297.md)) (Codex retained-session, resume and structured-event
  reliability).

## Problem

Owner/Workshop and Run sessions can work on the same workflow, with different
prompts, tools and skills. The server selects these per session, but main
workflow chats give their CLI processes the same workflow working directory.
The CLI adapters project session-specific instructions and skills into fixed
paths in that directory. Separate conversations therefore do not guarantee
separate instruction files.

For example, an Owner session projects its instructions, then a Run session
projects different instructions into the same path. A subsequent file read,
startup or continuation can see the other session's content. Closing one
session can also remove or restore a file another session still needs.
Same-mode sessions can collide too; Owner versus Run makes the difference
in intended guidance particularly visible.

The adapter-level collision is now reproduced (see the test record below).
It is **not yet a reproduced live two-user incident or a demonstrated permission bypass**. Server-side tool grants are
separate from projected prompt text. The complete filesystem and user
isolation boundary still needs verification.

## Code evidence

- `agent_go/cmd/server/server.go`: workflow-phase chats set
  `chatWorkingFolder = workflowPhaseFolder`, derive `chatWorkingDir`, and pass
  it as `CodingAgentWorkingDir`. Read-only users are forced into Run mode.
- `agent_go/cmd/server/coding_agent_modes.go`:
  `codingAgentWorkspaceWorkingDir` resolves the workflow folder beneath the
  workspace documents root.
- `agent_go/cmd/server/workflow_phase_tools.go`: Workshop mode gates the
  available mutation tools and guidance. Guidance materialization in
  `guidance/materialize.go` attaches mode-specific content under stable skill
  identities, including `builder-reference`.
- The locally replaced `mcpagent` dependency, `agent/coding_agent_options.go`,
  uses the configured working directory unless isolated-session workspace
  mode is enabled. It requests project-instruction-only delivery, so a
  successful projection can be the CLI's sole system-instruction carrier.
- The locally replaced `multi-llm-provider-go` dependency's Codex adapter,
  `pkg/adapters/codexcli/codexcli_interactive_adapter.go`, writes `AGENTS.md`
  beneath that working directory. Its cleanup restores or removes the path
  without checking whether another session now owns its contents.
  `codexcli_skills.go` projects skills into `.agents/skills` beneath the same
  directory.
- The Claude adapter also projects project instructions and attached skills
  into the working directory. Audit each supported provider's actual paths,
  discovery rules and cleanup behavior during implementation.
- `mcpagent/agent/coding_agent_options.go` already provides an isolated
  temporary directory keyed by session ID for opted-in agents. This is a
  useful precedent, but it can fall back to the shared working directory if
  directory creation fails. It is not a complete main-chat migration.
- `mcpagent/agent/session_handle.go` prioritizes the persisted working
  directory when resolving continuations. Existing CLI sessions therefore
  need an explicit resume/migration strategy.

Dependency paths above refer to sibling repositories selected by
`agent_go/go.mod`; changes may span all three repositories.

## Implementation requirements

1. Give each chat a private runtime directory scoped to the authenticated
   user/tenant, workflow and session. Keep its identity stable across turns
   and supported server restarts. Prompt, skill and provider configuration
   writes must not affect another live session, including during mode changes.
2. Keep the workflow's plan, reports, database, outputs and learnings in the
   authoritative shared workflow workspace. Do not copy the entire workflow
   into independent per-user folders. Separate the CLI runtime directory
   from the workspace tools' workflow root.
3. Audit relative paths, shell execution, provider instruction discovery and
   skill references. Use explicit workflow-root resolution or a controlled
   mapping so moving the CLI runtime does not break workflow file access or
   accidentally discover another mode's generated instructions.
4. Make cleanup ownership-aware. Cancellation, failures and shutdown must
   remove only the terminating session's generated files. Preserve
   user-maintained project instructions.
5. Persist the runtime location needed by native CLI resume. Define migration
   for existing sessions: resume safely or start a new native conversation
   with application history preserved and an explicit handoff. Do not
   silently resume against a different workflow or mode.
6. Retain server-side authorization independently of prompt isolation. Run
   users must not acquire Owner tools or unauthorized filesystem access by
   changing a client mode value, path or prompt. Folder separation alone is
   not a permission boundary.
7. Fail with an actionable error if required isolation cannot be established;
   do not silently fall back to shared instruction files.
8. Check Codex, Claude and other supported CLI provider paths, plus main chats,
   scheduled execution and step agents. Reuse existing isolation where it
   meets this contract and avoid breaking provider session coordination.

Concurrent workflow edits remain a separate concern: verify compatibility
with [PLAT-290](../../goals/plans-contracts/plat-290.md))'s execution snapshots and retained run revisions.
Private instruction folders must not change which plan revision an active
run executes or widen workflow mutation permissions.

## Acceptance and regression checks

- Run two same-provider chats on one workflow as Owner and Run, with distinct
  prompt/skill markers. Test both startup orders, continued turns and skill
  reads. Each session consistently receives its own guidance.
- Repeat with two same-mode sessions and relevant cross-provider combinations.
- Close, cancel or fail one session while the other continues. Its instruction
  and skill files remain intact and usable.
- Switch one chat's mode while another is active; verify both content and
  actual server tool grants. Forged Run-to-Owner requests remain denied.
- Verify normal workflow file reads, permitted writes, report generation,
  database access and outputs resolve to the correct shared workspace after
  changing the runtime directory.
- Restart and resume chats, including a pre-migration session. Check native
  conversation identity, history, mode and working-directory association.
- Change a plan as Owner while a Run session executes; confirm the existing
  run-revision behavior remains correct.
- Force private-directory creation failure and assert an actionable failure
  with no shared projection fallback.
- Use a fixture workflow with no public side effects for concurrency tests.

## Delivery status

Workflow-chat isolation is implemented as the server default, with
`AGENTWORKS_ISOLATE_WORKFLOW_CLI=false` retained as an explicit rollback. The
separate-instance launcher also defaults to isolation. Both initial agent
construction and workflow-phase setup select private directories; the workspace
bridge retains the shared workflow root. Resume checks reject mismatched cwd and
Codex project-directory overrides. Read-only mode identity is pinned to Run.

The implementation is on main. Full cross-provider cleanup and two-user
authorization acceptance remain open. Codex restart/resume behavior and its
structured transcript boundary were subsequently hardened and tested under
[PLAT-297](../coding-agent-bridge/plat-297.md)).
The current policy replays application history into a fresh native conversation
when resuming into a different app chat; only the same private chat identity
retains native continuation. Runtime directory garbage collection is deferred.

## 2026-09-06 — Repeatable testing-workflow reproduction

Standard process: [Isolated workflow testing](../../../../getting-started/isolated-workflow-testing.md).
Runner: `scripts/test-workflow-isolation.py`; projection fixture:
`scripts/testdata/plat296_projection_test.go.tmpl`.

Used an allowlisted, sanitized copy of the existing `Workflow/testing`. Tested
real Codex and Claude prompt/skill projection functions through Go overlays,
without modifying the provider checkout or invoking any native CLI/model.
Both providers reproduced prompt, skill body, supporting-reference and cleanup
collisions with overlapping simulated Owner/Run lifetimes. Both startup orders
and both cleanup modes were exercised. Private-directory controls passed while
retaining a shared workflow plan. Source workflow input hashes were unchanged.

16 cases passed in reproduction mode (8 per provider, including controls).
The `--expect isolated --providers codexcli` negative check failed as expected
on the shared-directory cases. The existing isolated-instance launcher test
also passed using its fake runner.

Local retained evidence:
`.local/workflow-tests/plat-296-5wrfgb8_/receipt.json` and the provider logs.
The receipt records the source hashes and scope. No running AgentWorks server,
browser, workflow or native session was started/stopped/reconfigured. Zero model
calls. This does not yet verify the server's directory choice, real two-user
permissions, native continuation, restart or mode switching.

## 2026-09-06 — Opt-in implementation and offline verification

- Production selector: `agent_go/cmd/server/workflow_cli_isolation.go`, backed
  by `agent_go/pkg/cliruntime/workspace.go`. Private storage is required outside
  workspace documents and rejects symlinked managed directories and obstructions.
- Server restore tests cover matching private continuation, old/shared or
  other-chat rejection, provider/mode isolation, and both representations of
  Codex's cwd override. Claude, Pi and Cursor handle checks are exercised;
  their full native CLIs are not invoked.
- `python3 scripts/test-workflow-isolation.py --server-isolation` tests the
  production selector, then feeds its paths to the real Codex/Claude projection
  and cleanup functions. Both startup orders and cleanup modes pass, with shared
  workflow instructions/data retained. Shared-directory cases still reproduce
  the original collision, so the probe retains its negative control.
- `bash scripts/test-local-instance.sh` verifies flag forwarding with a fake
  runner. No existing server, browser, tmux pane or source workflow was changed.

Evidence: `.local/workflow-tests/plat-296-azko23d8/receipt.json`,
`server-isolation.log`, `server-runtime-directories.json`, `codexcli.log`, and
`claudecode.log`. These are local retained artifacts, not committed fixtures.

## 2026-09-06 — Live chat and shared file-read smoke check

The separate test instance at `http://127.0.0.1:52733/` was restarted with the
user's existing `/Users/mipl/.codex` login, as requested. The earlier fresh Codex
authentication directory had caused the CLI to wait at sign-in; it was a test
setup issue. No credentials were copied and the production backend remained
running as the same process.

The real Codex chat now completes a response, reads `builder-reference` and its
file-layout reference, and lists the shared testing workflow's `planning/plan.json`,
`planning/step_config.json`, `soul.md`, `workflow.json`, and conversation files.
The CLI launches in the private runtime selected before the restart, while
workspace shell commands still run in the shared testing workflow folder.

Listing the private CLI directory through the workspace shell is denied by its
workspace-root boundary. This is recorded separately, not counted as a successful
private-directory read. This smoke check does not complete concurrent Owner/Run,
cleanup, mode-change, two-user authorization, or native-resume acceptance.

Local evidence: `.local/agentworks-instances/plat296-live/live-file-access-retry.json`
and that instance's server log; chat `87999abb-c50e-4e8b-a26b-b58f3dc9ed3a`.

## 2026-09-06 — Actual builder-chat acceptance

Tested the existing Codex chat as a builder, using its normal workflow APIs and
file tools. The outer test driver only submitted chat requests and inspected
results; it did not implement the plan or Python changes for the builder.

Verified on disk:

- Read the existing plan and builder-reference skills.
- Renamed the existing step through `update_scripted_step`.
- Added `builder-write-probe` through `add_scripted_step`, including JSON output
  validation, and authored `learnings/builder-write-probe/main.py` through the
  workspace patch tool.
- Passed `validate_plan_change` with no issues.
- On a subsequent chat turn, updated both code and plan description and repaired
  the test fixture's file layout through the same normal tools.
- Executed only the new scripted step using `execute_step(fast_path_only=true)`.
  Execution `exec-builder-write-probe-1788679689541286000` finished successfully.
  Its real output at
  `runs/iteration-0/execution/builder-write-probe/builder-write-probe.json` contains
  `marker: PLAT296_BUILDER_OK`, `soul_readable: true`, and
  `shared_workflow_marker: PLAT296_SHARED_WORKFLOW`.

The first execution failed because the test fixture placed `soul.md` at the
workflow root. Execution steps are granted read access to the canonical `soul/`
folder. The builder copied the fixture content into `soul/soul.md` and corrected
its script and description; the retry passed without widening permissions.
The native CLI cwd remained the private per-chat runtime throughout these turns.
Production testing-workflow input hashes were unchanged.

**The entire automatic builder flow does not pass yet.** On both the failed and
successful step completions, the notification dispatcher cleared a supposedly
stale busy flag and attempted a synthetic turn while the retained Codex turn was
still active. `AskWithHistory` returned
`mcpagent: a turn is already in flight on this agent`. The dispatcher then logged
the synthetic turn as completed, and chat stayed at “waiting for automatic
completion.” The successful JSON was independently verified; automatic return
to the builder and readback were not credited as passing. Investigate the busy
check and notification retry handling in `background_agents.go` before claiming
end-to-end readiness. This evidence does not establish that directory isolation
caused the notification race.

Evidence: `.local/agentworks-instances/plat296-live/builder-acceptance/receipt.json`,
its `before/` and `after/` artifacts, and server log entries at 12:52:48 and
12:58:09 local time. Concurrent Owner/Run, mode-change, two-user permissions and
the full native-resume/restart matrix remain pending.

## 2026-09-06 — Fresh chat with ordinary user messages

Opened the blank Builder tab in the isolated test UI and verified a new backend
chat (`07fdef59-20d2-4e9d-8ead-fd76d88d00eb`) and private CLI runtime. The test
used only two ordinary messages, with no tool names, paths or acceptance markers:

1. “What does this workflow do right now?”
2. “Can you add a step at the end that writes a short, readable summary of what
   this workflow checks? Run that new step once and show me the summary.”

The builder independently read skills and canonical files, explained the existing
workflow, added `summarize-workflow-checks` as a message-sequence step, validated
the plan and launched it. The first execution failed because the new step lacked
read permission for `planning/plan.json`. Its failure notification reached the
builder, which added scoped `additional_read_paths`, validated and retried without
another user message. Execution `exec-summarize-workflow-checks-1788680544501472000`
succeeded. The success notification also reached the builder, which read and
displayed the actual `workflow-checks-summary.md`; the file was independently
verified on disk. Production testing-workflow input hashes were unchanged.

This fresh-chat builder flow passes after autonomous recovery. It does not erase
the earlier retained-chat notification race or establish concurrent Owner/Run,
two-user permissions, or restart/resume acceptance. Local receipt and artifact
copies: `.local/agentworks-instances/plat296-live/natural-chat-acceptance/`.

## 2026-09-06 — Concurrent Builder and Run chats

Added a `Run chat` action in the workflow composer controls. It creates an
independent workflow chat whose tab metadata pins `workshop_mode=run`; normal
Builder chats continue to use `workshop`. Submission and command discovery now
read the mode from the owning tab, so changing tabs cannot make two concurrent
chats inherit one workflow-global mode.

Live-tested both chats at the same time in the isolated testing instance:

- Run chat `5d413cc2-f374-4599-b93a-234d21606f81` received the `run` override,
  had 67 registered workflow tools, and used private runtime
  `cli-runtimes/v1/7c0acb...`.
- A Builder chat received the `workshop` override with 128 registered workflow
  tools while the Run chat was active. A second Builder conversation created
  directly from the permanent Builder launcher repeated that result as fresh
  session `926dba2c-f472-4b24-9549-c46947108154`, using private runtime
  `cli-runtimes/v1/d3e469db...`.
- Both read the same shared testing workflow while running concurrently.
- After switching away and back, the Run chat refused a requested plan type
  conversion and directed the user to Workshop. No workflow files changed.

This completes same-user concurrent Builder/Run prompt and tool separation.
Two-user authorization and restart/native-resume acceptance remain pending.

## 2026-09-06 — Scheduled Run isolation and smaller runtime surface

Normal cron and calendar schedule messages now execute with
`execution_options.workshop_mode=run`. This gives unattended work the same
constrained prompt and tool policy as an interactive Run chat and selects a
private CLI runtime directory by default, derived from the schedule session,
provider, user, workflow and Run mode.
Explicitly selected workflow business skills remain attached so the scheduled
job can perform its purpose.

The scheduler chooses authority per turn. Contract-upgrade and answered-decision
preflights use Workshop mode because those bounded turns may update workflow
artifacts. Pulse Gate, review/fix and finalization also use Workshop mode. The
request helper clones nested execution options, preventing one maintenance
turn's elevation from leaking into the normal scheduled run that follows.

Run's projected reference bundle no longer contains Workshop slash-command procedures, Pulse
repair/drift material, secret management, or backup/publish authoring guides.
Focused scheduler, tool-policy, guidance materialization and prompt tests pass.

Conversation and cost continuity were audited after introducing these per-turn
mode changes. The canonical conversation JSON continues to merge the full
pre-switch transcript with each new exchange while excluding the synthetic
conversation-file pointer used to ground the relaunched CLI. A disk fallback now
loads that prior transcript when the process-local history cache is empty, so the
overwrite guard does not preserve the old file at the expense of dropping the
new turn.

Cost attribution now uses `pulse_lifecycle_turn` as the authoritative Pulse
signal; Pulse can use the Builder model without the retired
`llm_config_source=scheduled_pulse` marker. Phase-token cumulative bookkeeping is
also keyed by native CLI session epoch. A Run→Workshop relaunch therefore records
the new process's complete first-turn usage instead of subtracting the prior
process's counters. Non-native phase turns use their query/turn id as the epoch.
Tests cover cumulative history across mode changes, disk-history recovery,
native-runtime counter resets, explicit Pulse scope, and the existing immutable
cost-ledger behavior.

## 2026-09-06 — Cross-provider path and resume audit

The private-directory design remains necessary: the offline negative control
reproduces prompt, skill, reference and cleanup collisions when two modes share
one directory. The review found additional provider lifecycle defects, now
covered by failing-before/fixed-after regressions:

- Cursor compared Git-root strings, misidentifying case aliases on macOS. Its
  fallback removed existing `.git` metadata. Root checks now use filesystem
  identity, marker creation refuses existing files/directories, setup fails
  explicitly instead of loading the parent project's configuration, and cleanup
  checks that it still owns the marker directory.
- Cursor used one textual cwd hash for transcripts. Physical spelling now drives
  discovery; exact native-session lookup also accepts legacy path hashes.
  Missing known transcripts no longer fall through to a different chat's newest
  database. Resumed streaming follows the same native-ID rule and primes old
  messages before submitting the next prompt.
- Cursor cleanup deleted the entire `.cursor` tree, overriding even the explicit
  restore option. It now cleans generated files individually, preserves unrelated
  content, and honors restore mode for configuration and projected instructions.
  The existing default remains non-restoring for the specific generated files.
- Pi's workspace MCP lease and retained-launch comparison used literal paths;
  aliases could bypass the conflicting-configuration check. They now use physical
  identity/canonical keys. Claude trust, MCP approvals and transcript candidates
  now include physical spellings alongside legacy keys.
- AgentWorks containment, resume and Pi cleanup matching now account for physical
  filesystem identity. The existing v1 runtime digest input is deliberately
  unchanged, so this repair does not migrate saved chats to new directories.
  User, session, provider and mode boundaries remain enforced.

A shared `pathidentity` package in the provider library implements these rules.
It does not blindly lowercase paths or change the process working directory.
Case-sensitive filesystems must continue distinguishing genuinely different
folders. The test suite exercises symlinks everywhere and real case aliases on
case-insensitive filesystems.

### Repeatable offline acceptance

From the AgentWorks repository:

```sh
python3 scripts/test-workflow-isolation.py --server-isolation --expect collision
```

`collision` is the expectation for the deliberately shared-folder negative
control. Every private-directory case must remain isolated. The harness now
covers Codex, Claude, Cursor and Pi using production prompt/skill writers and
server-selected runtime directories (32 projection cases). Pi always restores
prior projected-file contents; the two restore parameter cases exercise the
same Pi behavior. It copies only testing-workflow design inputs, disables
schedules/capabilities, verifies source hashes afterward, and launches no server,
CLI session or model. All four providers and server selection passed in
`.local/workflow-tests/plat-296-p3q25_68/`.

The full four provider adapter unit packages and shared path tests pass, as do
focused AgentWorks resume/mode/schedule, prompt, conversation/cost-continuity and
mcpagent continuation tests. Provider lint reports zero issues.

Remaining acceptance is explicit: live cross-provider restart/resume and real
two-user authorization were not rerun during this audit. Cursor's first-ever
turn still uses time-based transcript discovery until a native ID is known.
Changing the workflow path spelling itself can still select a different v1
runtime digest; a future identity migration must preserve existing sessions.
The case-alias resume fix covers different spellings of the same runtime, not
an automatic migration between different runtime directories.

### Standalone dependency validation

The audit also found that `mcpagent` compiled inside the local `go.work`, but
failed with `GOWORK=off`: its published provider pin did not define
`llmtypes.WithCodingAgentReleaseSession`, already used by its code. The provider
pin is now updated, with the required transitive module versions selected by Go.
Focused continuation, session-handle and isolated-workspace tests pass against
the published module with the workspace disabled. Future acceptance must include
this check; local replacement modules alone cannot prove a deployable build.

AgentWorks also passes focused isolation, resume, prompt and conversation/cost
checks with `GOWORK=off` and both provider/mcpagent local replacements removed
from a temporary module file. That verifies the published dependencies rather
than sibling checkouts. The tested pins are provider `62c1b6e` and mcpagent
`6073b0a`. The check uses published dependency modules and deletes its temporary
module file afterward. Focused path/Pi-lease race tests also pass.

The AgentWorks repository-wide commit hook still reports 18 existing non-blocking
lint findings (one `reflect.Ptr` vet suggestion and 17 spelling findings), all in
files outside this patch. This audit does not claim that repository-wide lint is
clean; provider and mcpagent lint are clean.

## 2026-09-07 — Overlapping message-sequence cleanup regression (local fix)

The social-media run reproduced a same-mode session ownership collision: a
cancelled execution completed cleanup after its replacement had started, and
both used the same deterministic message-sequence session ID. The old cleanup
removed the new execution's shell cwd/guard, producing `WORKFLOW STEP CONFIG
ERROR: ... refusing workspace-root fallback`.

New runtimes now receive a unique owner suffix; only turns in the same live
sequence reuse it. Saved history cannot reclaim an old runtime identity. The
regression test failed before the fix and passes after it. This addition is
**local source, not yet deployed**; no live retry or server restart was done.
See [incident and verification record](../../../../audits/message-sequence-session-cleanup-2026-09-07.md).

The follow-up lifecycle review also fixes failed-creation rollback, final MCP
connection cleanup for the owned runtime, and inconsistent DB tool selection
between default and explicit lists. Shared browser sessions remain separately
owned. Permanent tests cover those cases, restricted parent tool pools, and
partially initialized agent cleanup. These changes remain **local, not
deployed**. See the [session/tool lifecycle review](../../../../audits/session-tool-lifecycle-review-2026-09-07.md).

## Register notes

[PLAT-296](plat-296.md) records the delivered two-mode execution
model. Builder/Workshop and Run chats now pin distinct prompts, tools and skills
per tab and execute from private session runtimes while guarded tools continue
to use the same authoritative workflow workspace. Concurrent same-user Builder
and Run chats were live-tested: Builder received 128 tools, Run received 67,
both read the shared testing workflow, and Run refused a plan mutation. Normal
schedules use the smaller Run surface and private per-schedule runtimes; bounded
maintenance and Pulse turns elevate only their own requests. Mode-stable resume,
conversation continuity and cost attribution are covered by focused tests, with
Codex resume reliability completed in PLAT-297. **Implemented on `main`; full
live cross-provider restart and real two-user authorization remain follow-up
acceptance.** The follow-up audit fixes Cursor metadata cleanup and wrong-chat
transcript fallback, Pi alias lease conflicts, and shared path identity. Offline
projection acceptance now covers all four coding providers; saved v1 runtime
identifiers remain compatible. The audit also fixes an outdated `mcpagent`
provider pin that failed standalone compilation while local `go.work` masked it.
