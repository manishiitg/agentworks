**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

**Saved-code paths:** Read `workflow.json.code_layout_version` first. In this reference, `<script-dir>` means `code/<step-id>` for version 1, or `learnings/<step-id>` for absent/zero (legacy). Resolve the placeholder before using a path; never infer the version from folders or migrate an existing workflow implicitly. Version 1 executes and repairs canonical source directly, with shared helpers under `WORKFLOW_CODE_ROOT`; only legacy workflows copy code into runs and save it back.

## Workflow Tools — Mode-Scoped Reference

This reference covers both Workshop and Run. The current mode and granted
catalog are authoritative: Run may execute/inspect and capture confirmed
runtime context, but cannot mutate plan/config, schedules, skills, secrets,
or design artifacts. Mutation sections below apply only in Workshop; do not
call unavailable tools or use shell to bypass a mode boundary.

For a slash command or matching review/improvement request, call
`get_workflow_command_guidance(kind, focus)` with the user's scope and recent
conversation context. Follow the returned permitted flow; do not expand user
authorization. Missing modes or tools require the appropriate Workshop handoff.
Use `runtime-context` for grounding and durable rule capture. Use
`pulse-review-fixer` for review/fix lifecycle and real verification; maintain
existing findings through typed Pulse tools rather than a parallel review log.


The workshop chat agent has access to a broad set of workflow-management
tools. The inline system prompt now carries only a one-line-per-category
cheat sheet; this skill is the deep reference with full signatures,
parameters, when-to-use rules, and gotchas.

If you need to confirm an exact parameter shape that isn't documented here
and you are a coding CLI reaching tools through the api-bridge (code
execution / agentic mode — see the "Coding-CLI bridge routing" section
below), call `get_api_spec(tool_name="...")` — that returns the live JSON
schema for the tool. `get_api_spec` is registered only in that mode; a
native tool-calling session already receives every tool's full schema
declared directly to the model and must not call it — the runtime has
nothing to answer with, and the call fails as "not registered for session".

### Coding-CLI bridge routing

The function-style names in this catalog are logical workflow tool names;
they do not mean every tool is exposed natively by `api-bridge`. In a coding
CLI, the native bridge exposes only `execute_shell_command`,
`diff_patch_workspace_file` (not when your own tools are on: edit with them),
`agent_browser`, and `get_api_spec`. Never try
`api-bridge.list_executions`, `api-bridge.query_step`, or another catalog name
as a native bridge call. For every non-native tool, call
`get_api_spec(tool_name="<name>")` first, then use
`execute_shell_command` to invoke the endpoint it returns with the supplied
`$MCP_MCP`/`$MCP_CUSTOM` route and `$MCP_AUTH`. Do not guess or hardcode an
HTTP URL.

## Shared knowledge bases (Workshop configuration)

Use `get_workflow_config` to inspect attached KBs and discover eligible source
workflow IDs. `update_workflow_config(knowledgebase_sources=[{"workflow_id":"<id>",
"alias":"server A","access":"read"}])` replaces the entire attachment list: preserve
existing entries when adding a source; pass `[]` to detach all. Up to 20 sources
are supported on the same host. Aliases must start with a lowercase letter and
contain only lowercase letters, digits, and underscores (maximum 48 characters;
`access` is reserved). IDs and aliases must be unique; self-reference is rejected.

The source must be readable by the consuming workflow's owners and readers.
After saving, builders can immediately shell-read `$WORKFLOW_KB_server A/notes/_index.json`
and relevant files. Eligible execution and review sessions get the same read-only
source grants. Source updates are live; detach or permission changes remove access
on subsequent commands, but do not revoke an already-running subprocess's sandbox.
Run mode consumes existing attachments; configuration changes require Workshop.

## Step Execution & Inspection

- **`execute_step(step_id, group_name, instructions?, human_input?, tier?, message_sequence_restart?, script_parameters?)`** — Start a single step in the background; returns `execution_id`. In interactive Workshop mode this is the primary way to test one step after adding or editing it and uses `iteration-0`; inside a saved schedule/webhook invocation the server forces its bound `iteration-N-sched`/`iteration-N-hook` folder. For a scripted step, `script_parameters` is an object validated against the step's declared contract and passed through `STEP_PARAMS_JSON`; it is rejected for non-scripted steps. A standalone `message_sequence` always starts its configured queue from the beginning; `human_input` is inserted as an initial user message and never mutates the system-level description charter. Only a message-sequence specialist route inside an active routed-agent run has in-memory re-entry. For `human_input` steps, `human_input` is used as the response. For other executable steps, it is high-priority runtime context.
- **`execute_step(step_id, group_name, fast_path_only=true)`** *(Workshop mode only)* — Run the step's saved Python `<script-dir>/main.py` directly with the same workflow env, args, output folder, and validation behavior as a real workflow run. Never falls back to LLM. Used to quickly test `scripted` main.py patches. Without `fast_path_only`, your own `execute_step` of a scripted step may write or repair its `main.py`; a run never does.
- **`query_step(step_id, tool_call_id?)`** — One-off live status check for a running single step. Resolves the latest execution for that step and shows execution status plus structured MCP tool calls and tool-call details captured so far. For coding-CLI providers, terminal/TUI progress does not appear as MCP tool calls — but when the step runs in tmux, query_step **inlines the latest lines of the live terminal pane** and also returns the tmux session name + a `tmux capture-pane -pt <session> -S -200` command for deeper history (same session as the UI terminal). Do **not** combine `sleep`, `list_executions`, and `query_step` into a polling loop. After launching background work, use at most one immediate status check, then yield; `[AUTO-NOTIFICATION]` will resume the conversation when the work completes.
- **`send_step_message(execution_id, message)`** — Send a correction or follow-up to the exact active child-agent turn inside a running step or full-workflow execution. Use the ID returned by `execute_step` / `run_full_workflow`; never guess from the step ID. Coding CLIs receive native live input, while non-coding agents queue the message for the next safe turn boundary. This does not start or resume work. `no_active_agent` means the execution is between agent turns or running validation/script work; wait instead of retrying in a loop.
- **`debug_step(step_id, iteration, group_name)`** — Rich insights: learning status, validation result, log paths.
- **`list_executions(status_filter?)`** — List all background executions. Use it to find an execution id or debug ambiguity, not as a repeated progress poll.
- **`stop_step(execution_id)` / `stop_all_executions()`** — Cancel running steps.
- **`run_full_workflow(group_name, human_inputs?, route_selections?)`** — Execute the complete workflow (all steps) for a single variable group in background and start from the beginning. Interactive Builder runs use `iteration-0`; saved schedules and webhooks use their server-bound immutable `iteration-N-sched` / `iteration-N-hook` folder. If the selected path has `human_input` steps, provide `human_inputs` (object mapping step_id to response string). For deterministic routers, pass `route_selections` keyed by routing or branch step ID, with each value as a `route_id` or unique `next_step_id`. Returns `execution_id`. In scheduled/run-mode flows, call it once for the intended group, then stop instead of polling substeps; auto-notifications report progress/completion.

{{if ne .WorkshopMode "run"}}
## Step Config & Analysis (Workshop mode)

- **`update_step_config(step_id, ...)`** — Update servers, tools, skills, learning settings, execution mode, LLMs, locks, review notes, description review state, and read-only `additional_read_paths` grants for declared workflow-relative inputs outside the standard run/db/KB/learnings surfaces.
- **Pulse Review → Fix** — Scheduled Pulse stays in one main-agent conversation: Gate → Review+Fix → Finalize. Technical maintenance uses one retained executor that reviews, applies only a bounded safe repair when warranted, proportionally verifies it, then persists typed findings, repair outcomes, terminal result, and its `technical_review` receipt before ending. The receipt is durable evidence, not a permission switch or a separate turn. Strategic Review remains a separate read-only task and persists its own result. The parent validates typed state instead of recreating findings from notification prose. Manual `/pulse-review` and every focused `/pulse-review-*` alias use the same retained Review+Fix task; `/pulse-fixer` remains an explicit repair-only recovery command for already reviewed issues.
- **Objective + success criteria** — Edit `soul/soul.md` directly via shell (fill in the `## Objective` and `## Success Criteria` sections). `soul.md` is stable intent, not architecture: never add step design, provider/tool choices, implementation decisions, inferred assumptions, or a decision log. Optional constraints belong there only when the user explicitly approved them as durable boundaries. `plan.json` owns the current implementation attempt. No dedicated tool — use `diff_patch_workspace_file` or a shell heredoc.
- **Notification preferences** — Keep execution reporting separate from Pulse reporting. `update_workflow_config(run_notification_instructions="...")` controls the **Run outcome** section: outputs, failures, goal movement, and metrics from the workflow execution. `update_workflow_config(pulse_notification_instructions="...")` controls the **What Pulse did** section: reviews, fixes, recommendations, decisions, backup/publish state, and next actions. If the user explicitly says a preference applies to every notification, save it in both fields. `workflow.json` `capabilities.notifications.run_summary_instructions` and `pulse_summary_instructions` are authoritative and supplied to Workflow Builder and Pulse finalization. Never put notification preferences in `soul/soul.md`; that file owns goal and user-approved outcome constraints, not presentation or delivery behavior. Missing preferences use Pulse defaults.
- **Notification Gmail sender** — `get_workflow_config` shows the selected sender and available enabled Gmail connection IDs. Set the one workflow Notify sender with `update_workflow_config(notification_gmail_connection_id="<id>")`, or pass `""` to inherit the account default. This controls the FROM account for `record_summary` and `send_email`; run and Pulse recipient lists remain separate, and ordinary workflow Gmail actions can choose their own sender.
- **Goal proposals and experiments** — come from the workflow's Pulse agent: it messages you (the Builder chat) with the change it wants and the evidence, and anything that needs the owner appears under Needs you with Pulse's recommendation. Make the change within the permission levels its message runs under, then tell Pulse what you did.
- **`review_workflow_timing(iteration?, group_name?, focus?)`** — Read-only latency review: finds the slowest groups/steps/tools/LLM calls and recommends faster descriptions, fewer handoffs, safer step merges, or plan changes.
- **`review_workflow_costs(iteration?, group_name?, focus?)`** — Read-only cost review: finds the biggest cost drivers and recommends cheaper models, fewer retries/handoffs, better descriptions, or plan changes without sacrificing success criteria.
- **`get_cost_summary(run_folder?)`** — Token usage and cost breakdown.

{{end}}

## Read-Only Info

- **`get_step_prompts(step_id, attempt?, iteration?)`** — System prompt and user message for a step.
- **`get_workflow_config`** — Inspect the workflow's current MCP servers, selected skills, available secrets, notification content instructions and one-way destinations, LLM config, and schedules. Use this instead of `cat workflow.json` when you need the full workflow config. For the global installed skill catalog, use `list_skills`.
- **`get_llm_config`** — Per-step LLM overrides.
- **`get_workflow_command_guidance(kind="review-artifact-drift", focus?)`** *(Workshop only)* — Full artifact drift audit for scheduled Pulse or an explicit user request, including the plan-drift repair procedure and a read-only dependency checklist. Ordinary edits and targeted tests use `builder-reference/references/plan-change-impact.md` for one combined compatibility check in the current agent.

{{if ne .WorkshopMode "run"}}
## Plan Modification (Workshop mode)

- **Create steps**: `create_plan`, `add_step`, `add_step`, `add_step`, `add_step`, `add_step`, `add_step`, `delete_plan_steps`, `maintain_plan`.
- **Update steps**: `update_step` (for `regular` steps — the type alone makes them scripted), `update_step`, `update_step`, `update_step`, `update_step`, `update_step`. Each updater accepts only its own step type. To move a step between the scripted and conversational models use `change_step_type(step_id, target_type="scripted"|"message_sequence", reason)` — in place, same id, dependencies and routes kept, recorded in the plan changelog. Never `add_*` + `delete_plan_steps` for that, and never `update_step_config`: there is no execution-mode config field.
- **Reclassify routing/branch**: `change_step_type(existing_step_id, target_type, reason)` atomically relabels an existing `routing` step as `branch` or vice versa — same id, same routes, only the question-field name changes. `reason` is the one-sentence classification rationale written to the plan changelog. Never use `add_step`/`add_step` + `delete_plan_steps` to do this by hand: deleting the old step prunes its `step_config.json` row (`drift_review`, `execution_tier`, etc.) before the id can be reused, so it does not actually preserve history.
- **Agent specialist routes**: `manage_step_route` adds, updates, or deletes
  `predefined_routes` on a message-sequence agent (and on persisted legacy
  orchestrator records). Choose one worker pattern per route: inline
  `sub_agent_step`, or `orphan_step_ref` to reuse an allowlisted shared orphan.
  Do not set both.
- **Validation**: `update_validation_schema`.
- **Graph integrity is atomic**: every plan mutation validates all routing/branch routes, todo/message-sequence `next_step_id` fields, and human-input branches before saving. `PLAN_GRAPH_INVALID` means nothing was saved. Repair every reference listed in the error with the appropriate `update_*_step` tool (target an existing step ID or `end`), then retry the original mutation. In particular, reroute inbound references before deleting their target step.

## Variables & Config

- **`update_variable(action, name?, value?, description?)`** — Add, update, or delete a variable.
- **`manage_group` / `manage_group` / `manage_group`** — Manage variable groups.
- **MCP servers workflow**: before this — if the user is asking to connect a *new* service rather than one already configured, honor an explicit MCP request; otherwise consider CLI/skill/MCP in that order. See the `integration-discovery` reference.
  0. Not sure whether an MCP server exists for a service at all? `search_mcp_catalog(query)` checks our own catalog, GitHub's MCP Registry and the official MCP Registry. Public listings are discovery metadata; verify and prefer the provider's own documented endpoint. Do not use hosted Smithery replacements.
  1. `get_workflow_config` to inspect which servers are currently selected.
  2. `update_workflow_config(add_servers=["server-name"])` selects an **already-registered** server into the workflow. This is tier 2 of a two-tier model — registering a server (tier 1, below) only makes it available account-wide; it still does nothing for any workflow until this call. **Do NOT edit `workflow.json` manually.**
     - To **register a new server first** (tier 1, so it can be selected): for a catalog hit or any fresh URL (from `search_mcp_catalog`, or one the user gave you), use `install_mcp_server(name, url?, api_key?, client_id?)` — it live-probes the URL's real auth requirements (no sign-in / API key / OAuth, with automatic Dynamic Client Registration where supported) instead of guessing, the same scrutiny a human would give any third-party integration. For OAuth, it starts the flow and hands back a link for the user to open; the connection completes on its own once they authorize, no follow-up call needed. Only use `add_mcp_server(name, protocol="stdio"|"sse"|"http", ...)` for a wholly custom server whose config you already know in full (command/args/env for stdio, or url for sse/http) — it does no auth probing at all.
     - `edit_mcp_server` / `remove_mcp_server` manage an already user-defined server (base/catalog servers can't be touched from chat). **`remove_mcp_server` deletes the server account-wide, not just from one workflow** — it does not detach it from any workflow's `selected_servers`, so a workflow that had it attached keeps a dangling reference; the tool itself now checks and warns you which workflows are affected so you can tell the user and offer `update_workflow_config(remove_servers=[...])` for each one.
  3. Optional workflow-level allowlist: `update_workflow_config(add_tools=["server:*"])` or `add_tools=["server:tool_name"]`. Tool entries must reference selected workflow servers.
  4. `update_step_config(step_id, servers=["server-name"], tools=["server:tool_name"])` to scope specific servers/tools to a step.
  5. `list_mcp_servers` shows what's actually installed/authorized vs. merely present in the catalog but never connected — use this, not `search_mcp_catalog`, to check the status of something already configured. `get_mcp_server_logs` shows recent connection/discovery logs for one server, or lists which servers have logs — use it when a server isn't showing the tools you expect. `trigger_mcp_discovery` manually re-scans every configured server's tools in the background, for when metadata looks stale but nothing about the config itself changed.
- **Browser workflow**:
  0. Call `agent_browser(command="status", ...)` when browser capability is available. If it returns `cdp_supported=false`, CDP is disabled for this deployment: do not configure `cdp`/`cdp_ports`, probe ports, or offer CDP setup. Use `auto` (managed headless here), `headless`, or `none`.
  1. Pick the workflow mode with `update_workflow_config(browser_mode="none"|"auto"|"headless"|"cdp")`. Prefer `auto` unless the workflow must require an authenticated visible Chrome (`cdp`) or must stay isolated in the background (`headless`). For the specialized case where one workflow needs independent login identities, set `cdp_ports=[9222,9333]` (maximum four) and launch each port with a distinct Chrome `--user-data-dir`; ordinary workflow concurrency uses one shared CDP browser.
  2. For `agent_browser` steps, enable `workspace_browser:agent_browser` via `update_step_config(enabled_custom_tools=[...])` and attach the matching runtime skill with `enabled_skills=["agent-browser"]`.
  3. For browser steps, attach `enabled_skills=["agent-browser"]`; the runtime provides the managed `agent_browser` tool.
- **Slack Incoming Webhook notifications** (one-way, per workflow):
  1. Store the full URL with `set_workflow_secret(name="SLACK_NOTIFICATION_WEBHOOK_URL", value="https://hooks.slack.com/services/...")`. This encrypts and auto-attaches it; never write the URL into `workflow.json`.
  2. Configure the safe reference with `update_workflow_config(slack_webhook_secret_name="SLACK_NOTIFICATION_WEBHOOK_URL")`.
  3. `update_workflow_config` converts that secret into a backend-only notification credential and removes it from agent-visible `selected_secrets` / `SECRET_*` injection. Current and future `record_summary` calls automatically send a rich Block Kit card to the webhook in addition to enabled account-level channels. Use `slack_title`, `slack_color`, `slack_fields`, `slack_sections`, and `slack_footer` for structured summaries; even a plain call gets the safe rich default. `human_feedback` never uses it because Incoming Webhooks cannot return an answer. Pass `slack_webhook_secret_name=""` to disable it without deleting the stored secret.
  4. Never read a webhook through `$SECRET_*`, post with `curl`, put a webhook recipe into a plan step, or disable automatic Slack delivery to avoid a duplicate. The backend owns the URL, Block Kit validation, fan-out, and delivery status; the agent calls `record_summary` exactly once.
  5. This is independent of the interactive Slack bot (Socket Mode, @mentions, threads, and replies).
- **`update_workflow_config(add_servers?, remove_servers?, add_tools?, remove_tools?, add_skills?, remove_skills?, run_notification_instructions?, pulse_notification_instructions?, notification_gmail_connection_id?, slack_webhook_secret_name?, browser_mode?, cdp_ports?, run_retention_count?, advisor_specialization_approval_input_id?)`** — Update workflow MCP servers, workflow-level MCP tool allowlist, skills, secrets, scoped notification preferences and Gmail sender, one-way Slack webhook reference, browser mode/profile ports, or retention. `run_retention_count` keeps the same count independently for completed Builder archives, `-sched` runs, and `-hook` runs, with paired evaluation cleanup. It can also activate the exact owner-approved Strategy Auditor + Goal Advisor specialization proposal. Advisor specialization activation accepts only an answered `advisor-specialization-*` decision whose selected option is `activate`; it resolves both texts from that durable decision and writes them together to `workflow.json`.

  ```
  { "id": "...", "name": "...", "description": "...",
    "cron_expression": "0 9 * * 1-5", "timezone": "UTC",
    "enabled": true, "trigger_payload": {},
    "after_run": {"backup": true, "publish": true, "notify": true},
    "group_names": ["customer-prod"],
    "mode": "workshop", "workshop_mode": "workshop" }
  ```
  Fields: `id` (auto-assigned), `name` (display label), `description` (optional), `cron_expression` (standard 5-field cron), `timezone` (IANA tz e.g. `America/New_York`), `enabled` (bool), `trigger_payload` (arbitrary JSON passed to the run), `group_names` (required array of one or more explicit group names from `variables/variables.json`), `mode` (`workshop` for workflow schedules), `workshop_mode` (`workshop` for writable scheduled sessions; the server derives `run` only for read-only access).
- Schedule management is available in **Workshop mode**. If the user asks in Run mode, tell them to switch.

### Two schedule types: cron vs calendar

Every schedule in `workflow.json` has a `schedule_type` — `"cron"` (default) or `"calendar"`. They are stored side by side under the same `schedules` key; the difference is *when* they fire.

- **`cron`** — a repeating pattern that fires forever on a cadence (`create_schedule`, `cron_expression`). Use for "every weekday at 9 AM", "every 30 minutes", "first of the month". This is the default; everything in *Three ways to schedule* and *Writing messages* below applies to cron schedules.
- **`calendar`** — a fixed list of specific dated runs, each firing exactly once (`create_calendar_schedule`, `calendar_items`). Use when the user gives concrete dates/times instead of a recurring rhythm — e.g. a full-month Instagram content calendar, a launch sequence, a one-off batch on three specific days. There is no `cron_expression`; the scheduler registers **one job per future `calendar_item`** and each item fires once at its date+time, then is done.

**Choosing:** if the user describes a *rhythm* ("every…", "daily", "weekly") use cron; if they enumerate *dates* ("on the 3rd, 7th, and 12th", "post these on these days") use calendar. When in doubt, ask whether the runs repeat or are a fixed set of dates.

**`create_calendar_schedule` payload:**

```
{ "name": "March content calendar", "timezone": "Asia/Kolkata",
  "after_run": {"backup": true, "publish": true, "notify": true},
  "group_names": ["group-1"], "mode": "workshop", "workshop_mode": "workshop",
  "calendar_items": [
    { "date": "2026-03-03", "time": "09:00", "description": "Optional note" },
    { "date": "2026-03-07", "time": "18:30" }
  ] }
```

- `calendar_items` (required): each needs `date` (`YYYY-MM-DD`) and `time` (`HH:MM`), both interpreted in the schedule's `timezone`. `description` is an optional per-item note; `messages` is an optional per-item message queue.
- `timezone` (required, IANA — e.g. `Asia/Kolkata`, not `IST`) and `group_names` (required) work exactly as for cron schedules.
- **Mode is the same as cron**: workflow schedules use `mode="workshop"`. Supply per-item `messages` or a top-level default `messages` array when the default full-workflow run instruction is not specific enough.
- Past-dated items are skipped — only future items get registered. To change a calendar schedule, update its `calendar_items` (add/remove dates); editing tools (`update_schedule`, `delete_schedule`, `trigger_schedule`, `get_schedule_runs`) work on calendar schedules too.

### How workflow schedules execute

Workflow schedules always use the workshop builder execution path. Do not create direct `mode="workflow"` schedules; legacy manifests with that value are normalized to workshop execution.

- **Workshop** (`mode=workshop`, `workshop_mode=workshop`) — writable scheduled execution for approved human-decision application and normal workflow work. Scheduled sessions never authorize, apply, or stamp contract migrations; pending upgrades are started manually in the interactive Builder chat and do not block schedules. Prefer an empty queue plus `group_names`/`route_selections` for durable workflow behavior: canonical steps receive their normal learning, validation/retry, repair, and Pulse attribution lifecycle. The server pins read-only workflow users to Run.

**Default mode rule:** create workflow schedules with `mode="workshop"`. New schedules should never use `mode="workflow"`.

**After each run** (PLAT-697 phase 0): every cron and calendar schedule has `after_run` = `{backup, publish, notify}`, plain platform options with no Pulse pass. backup saves workflow state when it changed; publish refreshes the published report when it changed; notify sends the run summary (routine successes are recorded in the dashboard, failures and status changes go to the channels). Normal schedules turn all three on (the default when you omit it); turn one off only when the owner wants that, and say what is lost. `pulse_mode` is legacy: read for one release when `after_run` is absent (basic = all three, off = none) and kept in step with `after_run` automatically; do not set it. Runs started from a chat use the workflow's `after_manual_run` options; set them with `update_workflow_config(after_manual_run={backup, publish, notify})`. These options are the owner's and work the same whether Pulse is on or off. **Pulse on or off** (`workflow.json` `pulse.enabled`): on, the workflow's Pulse agent owns the goal (a daily goal check, Goal Work, a turn after a failed run) and messages you, the Builder chat, to make changes; you act on its messages within its permission levels. Off, the owner manages the workflow and nothing reviews it on its own. There are no separate reviewers and no review schedules: never create `pulse_review_only` schedules or a review cron. The Workflow Review (Plan Drift) runs before every run on its own: unchanged plans start at once, a changed plan is reviewed first.

### Back up scheduled workflows

Scheduled runs execute unattended and accumulate state (`workflow.json`, `planning/`, `knowledgebase/`, `learnings/`, `db/`, reports) that otherwise lives only on local disk. **Whenever you set up a recurring schedule, also arrange a backup** so each run persists its output off-box. Load `read_skill(skills=[{"name":"builder-reference","path":"references/backup-strategy.md"}])`, follow it once to initialise the workflow's backup destination, and persist the result in `workflow.json.backup`.

- Set `workflow.json.backup.enabled=true`, `mode="agent"`, `triggers.after_scheduled_run=true`, and a `destinations` entry for each backup target (git/github for config, R2/S3/B2/HuggingFace for large artifacts as needed).
- After each backup attempt, write `backup/status.json` with the destination results, timestamps, summary, and errors. Do not put changing backup status in `workflow.json`.
- If you rely on the default full-workflow message, the auto-notification after `run_full_workflow` will still ask the builder to honor `workflow.json.backup` and write `backup/status.json`.

Confirm with the user before skipping backup on a recurring schedule.

### Writing messages for scheduled runs

`messages` is an ordered queue of strings sent to the workshop LLM one-by-one as user turns. The LLM completes all tool calls triggered by message N before message N+1 is sent.

- Write each message as a plain instruction, like you would type in chat: `"Run the full workflow"`, `"Generate the final report"`.
- **Route-backed mode (default)**: select planned work through `group_names` plus optional `route_selections`, and keep `messages` empty. Use this for durable workflow behavior so one canonical plan owns its lifecycle.
- **Direct-sequence mode (supported exception)**: use one or more messages when the conversation itself is genuinely schedule-specific and should not become reusable plan behavior. Set `direct_messages_reason` with the concrete tradeoff. These turns run in the workshop but are not canonical steps, so step learnings, validation/retry, repair, and Pulse attribution are weaker or unavailable unless the sequence explicitly invokes planned work.
- Never choose solely from message length. Compare behavior, inputs/outputs, external side effects, approval boundaries, failure behavior, and expected reuse.
- Read `variables/variables.json` for available group names and include them explicitly in the message if needed.

**CRITICAL — schedules run unattended; messages must never require human input:**

- Explicitly tell the agent to make all decisions autonomously: `"Do not ask for confirmation, proceed automatically"`.
- Provide all required parameters upfront in the message (group names, run folders, step IDs) so the agent never needs to ask.
- Tell the agent to skip or use defaults for anything unclear rather than pausing to ask.
- Never include open-ended questions or `"let me know"` style instructions.
- **Bad**: `"Run the workflow and ask me which steps to optimize"`.
- **Good**: `"Review runs/iteration-0 for group-1, collect read-only reliability evidence, then let the parent fixer choose a bounded repair, an approved plan change, a Goal Advisor proposal, or no action."`

Pulse module cadence is not encoded in schedule JSON. Pulse Gate stores module state in `db/db.sqlite` and decides which modules are due after each normal run.

{{end}}

## Shell & Discovery

- **`execute_shell_command`** — Run shell commands. Quick lookups:
  - `jq '[.steps[] | {id, title, type}]' planning/plan.json`
  - `` jq --arg sid "step-id" '.. | objects | select(.id? == $sid) | {id, title, type, description, context_dependencies, context_output}' planning/plan.json ``
  - `cat planning/step_config.json`
  - `ls runs/`
  - `cat variables/variables.json`
For choosing between the human interaction mechanisms below, read
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`.

- **Human questions in Builder chat** — Ask them directly in the normal assistant response. The blocking `human_feedback` response-card tool is execution-only and is deliberately unavailable to the Workflow Builder.
- **`create_human_input_request`** — Non-blocking Pulse/strategic-review question stored in the workflow's `db/db.sqlite`. The user answers in the AgentWorks Pulse/report panel.
- **`get_human_input_request`** — Read one existing decision by exact `workspace_path` and `input_id`, including its context, selected answer, evidence, and lifecycle status. Use this before applying an answered decision; do not query the backing SQLite table directly.
- **`answer_human_input_request`** — Record the user's explicit final answer to an existing pending question using the exact decision and option IDs supplied by the UI chat context. Never infer an answer from discussion. This sets the question to `answered`; it does not apply the decision or mark it consumed.
- **`mark_human_input_consumed`** — Mark an answered report question consumed after using it and recording the outcome through typed Pulse tools. Pending questions are rendered directly from SQLite, not duplicated in a separate presentation artifact.

{{if ne .WorkshopMode "run"}}
## Skills

Skills are reusable instruction sets injected into step agents at runtime. They live at the **workspace root** `{{"{{"}}.AbsDocsRoot{{"}}"}}/skills/{folder}/SKILL.md` — shared across all workflows. Do NOT create or reference skills inside the workflow folder (e.g. `Workflow/trading/skills/` does not exist).

**Workflow for managing skills**:

1. **Find**: `list_skills` to see installed skills, or `search_skills(query)` to search the public registry.
2. **Install**: `install_skill(source)` (e.g. `owner/repo@skill-name`) or `import_skill(github_url)` — downloads into `{{"{{"}}.AbsDocsRoot{{"}}"}}/skills/{folder}/`. If a skill folder exists but has no `SKILL.md`, reinstall it using the same method it was originally installed with — **never write `SKILL.md` content manually**.
3. **Select for workflow/builder context**: `update_workflow_config(add_skills=["folder-name"])` — makes the skill visible as a selected workflow capability for workshop/builder discovery. **Do NOT edit `workflow.json` manually.**
4. **Enable for runtime steps**: `update_step_config(step_id, enabled_skills=["skill-a"])`. Step execution only receives the skills listed in that step's `enabled_skills`; workflow-selected skills do not cascade into runtime agents.
5. **Remove from workflow**: `update_workflow_config(remove_skills=["folder-name"])`.
6. **Uninstall**: `uninstall_skill(folder_name)` — removes files from workspace entirely.

Use `get_workflow_config` to see the workflow's selected skills. Use `list_skills` to see all installed skills.

## Secrets

Secrets are credentials (API keys, tokens, passwords) injected into step agents as `$SECRET_<NAME>` environment variables at execution time. They exist in two buckets:

- **Workflow secrets** — encrypted server-side, scoped only to this workflow. Use these by default for workflow-specific credentials.
<!-- product:mcp-gateway -->- **Vault secrets** — managed encrypted shared credentials with group permissions. They are added, rotated, shared and deleted in Vault, not from a Builder chat; a workflow selects the Vault names it uses. `GLOBAL_SECRET_*` environment entries are operator-managed.<!-- /product -->

**Storing a new secret is one step.** `set_workflow_secret(name="BUFFER_API_KEY", value="<plaintext>")` stores, attaches, and injects the value into the active builder shell and future workflow steps.

**Attaching an already-stored secret is a separate operation.** Call `list_secrets`, then `update_workflow_config(add_secrets=["BUFFER_API_KEY"])`. The builder can use it immediately as `$SECRET_BUFFER_API_KEY`; no restart or new chat is required. If the requested name does not exist, ask for the value and store it. Never claim that a stored secret is unusable merely because its plaintext cannot be returned—the builder should consume it through the injected environment without displaying it.

Do **not** give boilerplate advice like `"rotate this secret"` after a normal user-requested save. Recommend rotation only when there is a concrete exposure reason: the value was printed into logs/output, committed to a file, sent to the wrong channel, or the user explicitly asks for security remediation.

<!-- product:mcp-gateway -->**Sharing a project secret to Vault** is done in Vault by a Vault administrator (the **Share to Vault** button in the project's secrets, Vault's chat, or Vault's MCP tools). The backend copies the value without exposing it to any model and keeps the project copy. From a Builder chat, tell the user where to do it. Never read legacy generated .pi/.claude skill files in the project to infer current platform capabilities. Load the attached canonical `builder-reference/references/secret-management.md` instead.<!-- /product -->

**Other secret ops**:

- **Inspect**: `list_secrets` returns `global` and `workflow` buckets — values are never exposed.
- **Edit a value**: call `set_workflow_secret` again with the same name — it upserts.
- **Delete from store**: `delete_workflow_secret(name)`. Workflow attachments are separate — also run `update_workflow_config(remove_secrets=["NAME"])` to detach.
- **Detach only (keep value)**: `update_workflow_config(remove_secrets=["NAME"])`.

Secret VALUES are never rendered into prompts, logs, or tool outputs. Builder and step agents consume them only through `$SECRET_<NAME>` in `execute_shell_command`. Never echo, print, or hardcode a secret value in descriptions, learnings, or `main.py`.

{{end}}

For multiple accounts of one MCP provider, install with `catalog` and `label`
(e.g. `install_mcp_server(name="Notion", catalog="Notion", label="Notion · Sales")`).
Use the returned exact connection name in `selected_servers`. Each labelled
connection has independent sign-in and credentials. Reconnect/remove/discover an
existing account by its exact name without label; keep other accounts unchanged.
This also applies to Relay projects through the shared workflow MCP tools.
