# Incident archive

Each file is one investigated defect: symptom, evidence, root cause, and what was
changed. They are written to be re-read by someone who was not there.

Cross-workflow Pulse findings that require shared runtime work are indexed in
[pulse_platform_issue_register.md](pulse_platform_issue_register.md). Canonical
per-ticket status, evidence, ownership, and acceptance live in the fragmented
files under [`pulse_platform/`](pulse_platform/), organized into per-category
subdirectories (see [pulse_platform_categories.md](pulse_platform_categories.md)
for the category list and filing rule). This lets Codex and Claude
Code work separate tickets concurrently without editing one large register.
The incident reports below remain deep investigations where their raw evidence
is useful; they are not the active ticket-status source.

## The agent-facing contract (2026-08-01 → 02)

Nine of these were written in two days and describe one subsystem: **what an
agent is told it can do, versus what the runtime actually permits.** They are
listed together because each was found by pulling on the previous one, and
reading any of them alone understates the pattern.

| Document | What it establishes |
|---|---|
| [custom_tool_category_as_agent_addressing.md](custom_tool_category_as_agent_addressing.md) | `CustomTool.Category` served as both an authorization key and an agent-visible address. `get_api_spec` demanded a category the prompt said was not an address — 46 failed calls in one day, the largest single source of failed tool calls in the system. Also covers the tool-inventory delivery gap and the `read_skill` migration. |
| [pulse_fixer_sqlite_readonly_wal_and_schema_guessing.md](pulse_fixer_sqlite_readonly_wal_and_schema_guessing.md) | A WAL database cannot be opened read-only without an existing `-shm`. The failure looked environment-dependent and was not; the original sandbox diagnosis is recorded and disproved. Includes the audit of every read-only open site. |
| [codex_structured_turn_completion_hang.md](codex_structured_turn_completion_hang.md) | Process teardown was reachable only from a stdout terminal event. A stage agent wrote its complete result, then held its caller for 65 minutes. Fixed with an independent completion signal and a bounded reap. |
| [stage_agents_cannot_read_skills_or_query_db.md](stage_agents_cannot_read_skills_or_query_db.md) | The missing builder allow-list entry was a false diagnosis: mcpagent injects intrinsic `read_skill` per turn. The stage still had a real bug—no reference skills were attached—now fixed by attaching the complete reference surface. Also records the remaining plan-review DB gap. |
| [scripted_steps_invisible_to_pulse_review.md](scripted_steps_invisible_to_pulse_review.md) | A scripted step that exits 0 with schema-valid output is reported as success forever, because `bug_review`'s trace contract named only agentic artifacts. The run log existed all along at `logs/<step>/execution/scripted_fast_path.json`. Split: `bug_review` owns correctness defects, `strategy_auditor` owns drift. **Prompt changes unverified — mcpagent tree does not compile.** |
| [steps_never_learn_from_their_own_validation_failures.md](steps_never_learn_from_their_own_validation_failures.md) | A step is given its validation schema but never told its last output failed it, so a contract mismatch recurs forever — `deliver-briefing` at `seen_count 3` reading an identical prompt each run. Fixed for agentic steps. **Two gaps stay open**: `bug_review` saw these and worked on other things, and a *succeeding* scripted step produces no trace for review to read. |
| [tool_failures_invisible_in_backend_logs.md](tool_failures_invisible_in_backend_logs.md) | A rollout scan found 78 bridge failure-envelope occurrences rendered as green checks and unfindable in logs because the outer shell transport returned `exit_code: 0`. Fixed with a `[TOOL_ERROR]` marker and two narrow UI detectors; one post-rebuild runtime marker check remains. |
| [workflow_step_shell_working_directory.md](workflow_step_shell_working_directory.md) | A workflow step has two unrelated working directories—the CLI's isolated temp dir and the server-side shell's. Dedicated child sessions lost the intended shell cwd and fell back to workspace root. Fixed by assigning the run execution cwd directly and rejecting missing workflow-step cwd. |
| [diff_patch_unbounded_subprocess_hang.md](diff_patch_unbounded_subprocess_hang.md) | Found *using* the `[TOOL_ERROR]` markers above. `DiffPatchDocument` shells to `patch(1)` with `exec.Command` — no context, no timeout, no stdin — so a stuck subprocess is uninterruptible: 4 of 10 requests never completed, against a 7–12 ms norm. The agent saw `context canceled` (step teardown, not the cause), retried three times, then routed around the tool with `printf >`. **Root-cause location established, trigger unconfirmed. Not fixed.** |
| [tool_result_spill_outside_workspace.md](tool_result_spill_outside_workspace.md) | The recurrence of #5 above, through a different tool. `read_skill` returned 67,971 characters of batched reference docs; the CLI truncated against its own token cap, spilled the full copy under its project directory, and ordered the agent to read a path the folder guard forbids. The agent had no legal move and spent the session guessing; the upgrade preflight it was running never finished, so the scheduled workflow never started. Verified the limit against the Claude Code binary (`MAX_MCP_OUTPUT_TOKENS`, default 25,000). **OPEN.** Routes closed: one read per call, no bundle doc over the cap, cap enforced on the serialized payload. One route remains — `agent_browser` returns page content uncapped. Records why `~/.claude` must not be added to the folder guard, and why the CLI's own recovery advice cannot work under this platform's tool restrictions. |
| [workflow_variables_lost_on_env_ref_replacement.md](workflow_variables_lost_on_env_ref_replacement.md) | `SetWorkspaceEnvRef` replaces the env map wholesale and backfilled only `SECRET_*`, so every `VAR_*` a workflow had resolved was dropped by the next store. `VAR_SITE_URL` was synced three times and appears zero times in any env ref in a full day's log. An agent in confida-qa-testing correctly reported "Variables aren't injected in this shell" and re-derived the value with `jq`. The doc comment above the function already described this exact bug — for secrets. **Fixed; needs a server restart.** |
| [ask_in_chat_never_reaches_a_running_turn.md](ask_in_chat_never_reaches_a_running_turn.md) | "Ask in chat" on a pending decision only appended to `queuedMessages`, and the drain waits for idle — so on a running chat the question parked as a chip forever, with no steer button on a coding CLI to force it. There are two live-delivery mechanisms, `/api/query` with `preferLiveInput` for tmux CLIs and workflow chats, and `sendLiveInput` for an in-flight API turn; the first fix handled only the former and duplicated ChatInput's routing into ChatArea, and was reverted. **Fixed** — routing extracted to one tested helper, used where it already lived. |
| [claude_first_prompt_unsubmitted_after_resume.md](claude_first_prompt_unsubmitted_after_resume.md) | After a restart, the first message into a resumed Claude chat was pasted but never submitted; the user pressed Enter by hand. The submit check read on-screen activity from the loading conversation as "accepted". **Fixed** — activity counts only once the prompt left the box. |
| [bot_followups_queued_not_steered.md](bot_followups_queued_not_steered.md) | A second Slack DM sent while the first was running was queued as its own turn: the steer-or-queue rule treated every bot turn like a schedule. Also `/live-input` reported a definite refusal as "delivery uncertain". **Fixed** — bot conversation follow-ups steer. |
| [decision_option_click_only_filled_the_chat_box.md](decision_option_click_only_filled_the_chat_box.md) | Clicking a decision option only filled the chat composer, so it looked like nothing happened. **Fixed** — it sends (two-click confirm, like Ask AI). |
| [../refactor/mcpagent_public_api_simplification.md](../refactor/mcpagent_public_api_simplification.md) | The refactor these motivated: 70 exported `Agent` methods let callers sequence internal lifecycle themselves, so the same fact lived in several independently stale copies. |

### The shared shape

Every one is the same defect in a different place: **one fact, two sources, and
nothing checking they agree.**

- the prompt says categories are not addresses; `get_api_spec` demanded one
- the prompt says `db/db.sqlite` is writable; the folder guard denies it
- the prompt lists `get_prompt`/`get_resource`; they are not registered
- the prompt names the run folder; the guard grants only its `execution/` child
- the tool index is materialized into prompt text that a later overwrite discards

Two second-order lessons worth carrying:

**Recovery text is an instruction, not documentation.** An agent that gets a bad
error message does not stop — it acts on the hint. Three separate bugs turned one
failed call into several because the correction named something that did not
exist. Error text should be held to the same standard of truth as a tool result.

**Absence is the case that needs handling.** Several of these were written as
"update if present, else silently skip", which is indistinguishable from
"correctly had nothing to do". When a component's job is to supply something,
skipping is the bug.

### Why these were invisible

A tool that fails behind the HTTP bridge returns its error as stdout with
`exit_code: 0`, and shell denials exit 0 as well. Every one of these rendered in
the UI as a green check until `frontend/src/utils/toolCallFormatting.ts` learned
to read the harness envelope and stderr. Before changing anything in this area,
confirm the failure is actually visible — otherwise a fix cannot be verified.

## The agent-facing contract, part two (2026-08-04)

| Document | What it establishes |
|---|---|
| [what_the_runtime_tells_an_agent_about_itself.md](what_the_runtime_tells_an_agent_about_itself.md) | Seven defects from one `grep '[TOOL_ERROR]'` over a 5h37m window (137 markers). Four sharpen the house pattern into something worse: **the runtime named a cause the code had evidence against** — a denial blaming "workshop mode" the registry has never heard of, a shell tool description asserting a working directory that was never true, a guard advising a workspace path for a file that cannot exist there, and a Pulse pre-check answered with an identity error the validator two lines above had just disproved. Also: a 100KB cap that turned out to live on a test fixture while the live path was uncapped, allow-lists withholding tools the prompts instruct agents to call, and a deliberate re-registration that became fatal when a map became a slice — which killed the Chief of Staff daily pass two days running. |

## The agent-facing contract, part three (2026-08-10)

| Document | What it establishes |
|---|---|
| [hybrid_profile_told_it_has_no_shell.md](hybrid_profile_told_it_has_no_shell.md) | The house pattern at two layers at once, exposed by the first profile to combine `agent_tools: hybrid`, `api_transport: native_shell`, and a `tool_policy` allow-list. **The tool gate guarded execution but not advertisement**: `defaultBridgeToolDef` synthesizes a definition for an unregistered core tool, so the bridge kept offering `execute_shell_command` — described as *"use this to call HTTP endpoints with curl"* — after the profile removed it. Claude Code absorbed the failed call and fell back to `Bash`, which is why it read as "the allow-list working" for six weeks. **And a second prompt source contradicted the first**: `BuildCLIToolEnvironmentPrompt` asserts "your native tools are **disabled**" to any CLI provider, never asking the profile, though `resolvedProfile` is in scope 118 lines above. Both fixed and verified. A third defect — Codex reporting no native shell while every flag says it has one — is recorded **unresolved with the contradiction intact**, plus the `functions.exec` vs `exec_command` naming trap that makes two comments in this repo appear to disagree. |

## SparkQuill production deployment (2026-09-16)

| Document | What it establishes |
|---|---|
| [chrome_process_singleton_lock_survives_auto_recovery.md](chrome_process_singleton_lock_survives_auto_recovery.md) | A family's real SparkQuill session kept failing forever after one Chrome crash, even with only one daemon confirmed running. Chrome's own `SingletonLock`/`SingletonSocket`/`SingletonCookie` files survive an unclean exit; the existing dead-session auto-recovery path cleared agent-browser's own bookkeeping but never Chrome's lock files, and didn't even trigger for the `ProcessSingleton` error text in the first place. Fixed in `executor.go` + `browserconfig/launch.go`. **Code fixed, build clean, not yet deployed or verified against a real crash.** |

## WhatsApp self-chat echo loop (2026-09-19)

| Document | What it establishes |
|---|---|
| [whatsapp-self-chat-echo-loop.md](whatsapp-self-chat-echo-loop.md) | Right after pairing a WhatsApp "Message yourself" chat, the workflow chooser re-sent in a loop. Both user-typed and AgentWorks-sent self-chat messages arrive with `IsFromMe`, so the flag cannot be dropped wholesale — and the echo can arrive before the outbound ID is recorded, or mirrored under another ID, so it was dispatched as a new unrouted user message that generated another chooser. Fixed with short-lived SHA-256 expected-echo markers (30s expiry, counts for identical concurrent sends, failed sends cancel) plus message-ID dedup as a second guard. Also scopes WhatsApp routing to the paired account: saved slugs are destination pointers, not grants, and revoked/stale routes are hidden or cleared against the live manifest. |

## Earlier incidents

- [voice_dictation_mic_captures_silence.md](voice_dictation_mic_captures_silence.md) — **OPEN**, environment-level, not yet root-caused. A dev machine's mic reads real silence through every app (not just this one) despite a correctly-granted, unmuted device; the STT pipeline itself was independently proven correct via a raw WAV file and a synthetic-tone browser test.
- [session_tool_registry_lifecycle_leak.md](session_tool_registry_lifecycle_leak.md)
- [auto_unlock_loop_orchestration.md](auto_unlock_loop_orchestration.md)
- [dependency_update_failure.md](dependency_update_failure.md)
- [parallel_tool_lock_contention.md](parallel_tool_lock_contention.md)
- [mcp_startup_retry_and_double_construction.md](mcp_startup_retry_and_double_construction.md)
- [uvx_cache_bloat_latest_versions.md](uvx_cache_bloat_latest_versions.md)
- [workspace_docs_path_inside_repo.md](workspace_docs_path_inside_repo.md)
