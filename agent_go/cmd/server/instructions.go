package server

import (
	"fmt"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"path"
	"strings"

	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/utils"
)

// AgentInstructions contains custom instructions for both React and Simple agents
type AgentInstructions struct {
	ResponseFormatting string
}

// workspacePaths holds the resolved absolute paths for the workspace.
type workspacePaths struct {
	DocsRoot    string
	Chats       string
	Skills      string
	Workflow    string
	Subagents   string
	Config      string
	ChatHistory string
}

func resolveWorkspacePath(docsRoot, rel string) string {
	if rel == "" {
		return rel
	}
	if strings.HasPrefix(rel, "/") || docsRoot == "" {
		return rel
	}
	return docsRoot + "/" + rel
}

func newWorkspacePaths(docsRoot, chatsFolder string) workspacePaths {
	if chatsFolder == "" {
		chatsFolder = perUserChatsFolderFor("")
	}
	return workspacePaths{
		DocsRoot:    docsRoot,
		Chats:       resolveWorkspacePath(docsRoot, chatsFolder),
		Skills:      resolveWorkspacePath(docsRoot, "skills"),
		Workflow:    resolveWorkspacePath(docsRoot, "Workflow"),
		Subagents:   resolveWorkspacePath(docsRoot, "subagents"),
		Config:      resolveWorkspacePath(docsRoot, "config"),
		ChatHistory: resolveWorkspacePath(docsRoot, strings.TrimSuffix(chatsFolder, "/Chats")+"/chat_history"),
	}
}

// GetWorkspaceMap returns a compact folder listing with absolute paths and access levels.
// This is the high-priority section — placed early in the prompt before reference docs.
func GetWorkspaceMap(docsRoot, chatsFolder string) string {
	p := newWorkspacePaths(docsRoot, chatsFolder)
	return `
## Workspace

**Always use absolute paths in shell commands.** The workspace docs root is: ` + "`" + p.DocsRoot + "`" + `. Every absolute path you reference in a shell command MUST start with this exact prefix. The path guard rejects absolute paths under any other host root (` + "`" + "/Users/..." + "`" + `, ` + "`" + "/home/..." + "`" + `) that are not under the docs root. Do NOT prepend the project root, your home directory, or anything else — always use ` + "`" + p.DocsRoot + "`" + ` as the prefix. When tool descriptions show paths like ` + "`" + "Workflow/<name>/" + "`" + ` or ` + "`" + "Chats/<folder>/" + "`" + `, those are LOCAL paths RELATIVE to the docs root; the absolute equivalent is the docs root + that suffix.

**Never use WebFetch/raw GitHub URLs for workspace artifacts, skills, or reference docs.** Files such as ` + "`" + "skills/<name>/SKILL.md" + "`" + ` live on local disk under the docs root above. Read them with the declared local tools/shell, or load canonical reference docs with ` + "`" + "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/....md\"}])" + "`" + `.

**Pulse storage is SQLite-only.** Use the typed Pulse, finding, review, and human-input tools; the Pulse popup is the only workflow Pulse presentation. Older journal artifacts are retired and must be ignored.

| Path | Access | Purpose |
|------|--------|---------|
| ` + "`" + p.Chats + "/`" + ` | read/write | Your workspace — save all output files here |
| ` + "`" + p.Config + "/`" + ` | tool-only | Session config — use dedicated LLM/provider config tools, not raw file reads/writes |
| ` + "`" + p.ChatHistory + "/`" + ` | read/write | Past conversation histories |
| ` + "`" + p.Skills + "/`" + ` | read-only | Skill definitions (SKILL.md + supporting files) |
| ` + "`" + p.Workflow + "/`" + ` | read-only via shell | Workflow definitions — create with ` + "`create_workflow`" + `; edit cron schedules with the workflow_schedule tools (see "Modifying Existing Workflows") |

### Chats Folder Organization

Organize output files under descriptive project folders — never dump files at the Chats root.

` + "```" + `
` + p.Chats + `/
  <project-name>/          ← One folder per task/project (kebab-case)
    report.html            ← Final output (use HTML for rich reports)
    data.json              ← Supporting data
    analysis/              ← Sub-folder for complex outputs
  <another-project>/
` + "```" + `

Examples: ` + "`quarterly-sales-analysis/`" + `, ` + "`aws-cost-report/`" + `, ` + "`bank-statement-parsing/`" + `
Reuse existing project folders for follow-up work on the same topic.
`
}

// GetWorkWorkspaceMap describes the primary host workspace without leaking
// AgentWorks' internal Chats/Workflow storage model into the Work product.
func GetWorkWorkspaceMap(workspacePath, chatHistoryPath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	text := "\n## Workspace\n\nYour selected primary workspace is `" + workspacePath +
		"/`. The native coding CLI starts in this directory. Use paths relative to that working directory, or this exact authorized absolute path. Access levels for it and any additional attached folders are listed below; never infer access to another host path.\n"
	if chatHistoryPath = strings.TrimSpace(chatHistoryPath); chatHistoryPath != "" {
		text += "\nPast conversations for the signed-in user are stored as JSON under `" + chatHistoryPath +
			"/`. You may search and read those files when the user asks about an earlier chat or another Crew belonging to the same account. This access never includes another user's conversations. Treat these logs as reference history; continue to use the project-root MEMORY.md for curated durable memory.\n"
	}
	return text
}

// GetWorkflowPhaseWorkspaceMap returns workflow-phase-specific workspace instructions.
// Unlike chat mode, workflow-phase work should treat the active workflow folder as the
// primary writable root and avoid surfacing internal per-user Chats paths.
func GetWorkflowPhaseWorkspaceMap(docsRoot, workflowFolder string) string {
	return getWorkflowPhaseWorkspaceMapForMode(docsRoot, workflowFolder, "workshop")
}

func getWorkflowPhaseWorkspaceMapForMode(docsRoot, workflowFolder, mode string) string {
	if strings.TrimSpace(workflowFolder) == "" {
		return GetWorkspaceMap(docsRoot, "")
	}
	active := resolveWorkspacePath(docsRoot, path.Clean(workflowFolder))
	access := "Workshop may write workflow-owned artifacts here; planning/config changes require dedicated tools."
	if mode == "run" {
		access = "Run may read workflow artifacts and execute authorized business work; it cannot edit workflow design, config, learnings, KB, measurement, or report files."
	}
	return "\n## Workspace\n\nWorkspace docs root: `" + docsRoot + "`. Active workflow: `" + active + "/`. " + access +
		"\nOther workflows are read-only. `config/` is tool-only. Use quoted absolute paths under the docs root in shell commands. Store workflow outputs, scratch and browser artifacts under the active workflow, not Chats. Read `builder-reference/references/file-layout.md` for paths and log schemas.\n"
}

// GetWorkspaceReference returns detailed reference documentation for workspace config,
// workflow structure, and workflow creation. This is lower-priority reference material —
// placed after the operating mode instructions in the prompt.
func GetWorkspaceReference(docsRoot, chatsFolder string) string {
	p := newWorkspacePaths(docsRoot, chatsFolder)
	absWorkflow := p.Workflow

	instructions := utils.GetCommonFileInstructions()

	instructions += "\n\n" + browserinstructions.GetSpecialWorkspaceToolsInstructions() + `

## LLM Tier Configuration
Do not read or write tier-config storage with shell/file tools. Use the UI or dedicated backend tier-config API; raw workspace file tools intentionally do not have ` + "`config/`" + ` access.
- Each tier selects one provider, model_id, and optional options.
- Preserve existing tiers when editing. Change only the tier the user requested.
- Failed calls remain on the selected agent/provider. Transient retries never switch models.

## Published LLMs & Provider Auth
Published LLM metadata and provider authentication are workspace-backed configuration surfaces. Access them through dedicated tools only; raw workspace file tools intentionally do not expose ` + "`config/`" + `.
- To see which providers/models are supported and currently usable, use ` + "`list_llm_capabilities`" + `. It covers ` + "`chat`" + `, ` + "`search_web`" + `, and ` + "`read_image`" + `, including auth/runtime availability and static pricing metadata where available.
- When choosing a concrete provider-backed model for search or image reading, call ` + "`list_llm_capabilities(capability=\"...\", include_models=true)`" + ` first and pass ` + "`provider`" + ` and ` + "`model_id`" + ` together from the same capability entry. Do not pass only ` + "`model_id`" + ` and rely on provider inference. ` + "`image_gen`" + `/` + "`image_edit`" + ` have only one provider (codex-cli) and do not take provider/model_id arguments.
- Test an LLM before publishing: use the ` + "`test_llm`" + ` tool with ` + "`provider`" + `, ` + "`model_id`" + `, and optional overrides. It uses workspace-backed provider auth by default.
- List the frontend-known models for a provider: use the ` + "`list_provider_models`" + ` tool. It uses shared metadata for fixed providers and the same dynamic picker source as the UI for dynamic providers.
- List published LLMs with ` + "`list_published_llms`" + `.
- Publish or update a published LLM with ` + "`save_published_llm`" + `.
- Provider auth is encrypted at rest. Do not read or hand-edit config files with shell/file tools.
- Update provider auth with the ` + "`set_provider_auth`" + ` tool.
- Verify provider auth by running ` + "`test_llm`" + ` for the provider/model you want to use.
- Use dedicated tools for all published LLM and provider-auth operations; raw workspace file tools intentionally do not have ` + "`config/`" + ` access.
- ` + "`search_web_llm`" + ` is MCP-only. Its required ` + "`provider`" + ` is one of ` + "`parallel`" + `, ` + "`exa`" + `, or ` + "`firecrawl`" + `; do not pass ` + "`model_id`" + `. Parallel and Exa use anonymous free MCP access, while Firecrawl keyless availability is service-controlled.

Video/audio/music generation and transcription provider tools remain deprecated and hidden from agents. ` + "`read_image`" + `, ` + "`image_gen`" + `, and ` + "`image_edit`" + ` are active.

## Image Generation Defaults
Image generation defaults are workspace-backed configuration. Provider authentication is managed separately through ` + "`set_provider_auth`" + `.
- Do not read or write saved defaults with shell/file tools. Use runtime ` + "`image_gen_config`" + ` overrides for the current chat session, or the dedicated UI/API configuration path when changing saved defaults.
- The configured primary is used. Missing provider auth returns an error.
- Runtime ` + "`image_gen_config`" + ` overrides this file for the current chat session only.
- Keep provider auth updated with the ` + "`set_provider_auth`" + ` tool; do not hand-edit encrypted auth files.
- Do not infer image-generation support from ` + "`list_provider_models`" + ` or the normal LLM model catalog. Those lists are for chat/text models, not image models.
- ` + "`image_gen`" + ` and ` + "`image_edit`" + ` have only one provider (codex-cli) and do not take provider/model_id arguments; there is nothing to discover with ` + "`list_llm_capabilities`" + ` for them.

## Image Analysis Defaults
Image understanding for the ` + "`read_image`" + ` tool can be routed via workspace-backed image analysis defaults, including through a coding-agent CLI's own native vision by passing it the local workspace image path directly (` + "`codex-cli`" + `, ` + "`cursor-cli`" + `, and ` + "`claude-code`" + ` are all supported providers for this) rather than only through a standalone vision-model API.
- Do not read or write saved defaults with shell/file tools. Use per-call ` + "`read_image`" + ` overrides, or the dedicated UI/API configuration path when changing saved defaults.
- If this file exists, ` + "`read_image`" + ` uses its ` + "`primary`" + ` with workspace provider auth.
- If this file does not exist, ` + "`read_image`" + ` uses the current chat model.
- For one-off ` + "`read_image`" + ` calls, use ` + "`list_llm_capabilities(capability=\"read_image\", include_models=true)`" + ` and pass ` + "`provider`" + ` with the matching ` + "`model_id`" + ` when overriding defaults.
- Keep provider auth updated with the ` + "`set_provider_auth`" + ` tool; do not hand-edit encrypted auth files.

## Workflows
List workflows with ` + "`execute_shell_command(command: \"ls " + absWorkflow + "/\")`" + `.

### Workflow Structure
Each workflow lives in ` + "`" + absWorkflow + `/<name>/` + "`" + ` with:

**Planning & config:**
- ` + "`soul/soul.md`" + ` — canonical stable workflow intent: ` + "`## Objective`" + `, ` + "`## Success Criteria`" + `, and optional explicit user-approved constraints. Read before review, improve, measure, harden, and ambiguous execution decisions. **Do not store architecture, current step design, provider/tool choices, implementation details, historical decisions, references, agent-inferred assumptions, or notification preferences in soul.md** — per-workflow notification preferences live in workflow.json ` + "`notifications`" + ` (` + "`run_summary_instructions`" + ` and ` + "`run_summary_channels`" + ` for execution outcomes, ` + "`pulse_summary_instructions`" + ` and ` + "`pulse_summary_channels`" + ` for Pulse activity, ` + "`run_summary_recipients`" + ` and ` + "`pulse_summary_recipients`" + ` for WHO each summary is emailed to (empty = the account default recipient), ` + "`run_summary_slack_webhook_secret_names`" + ` and ` + "`pulse_summary_slack_webhook_secret_names`" + ` for WHICH Slack channel(s) each summary posts to — one Incoming Webhook is one channel, so a second channel needs a second webhook secret (empty = the single ` + "`slack_webhook_secret_name`" + `),` + "`exclude_channels`" + ` for workflow-wide channel opt-outs, and ` + "`block_recipients`" + ` for the email denylist, and ` + "`gmail_connection_id`" + ` for WHICH configured Gmail account sends this workflow's mail — empty inherits the account default connection, and an unknown or disabled connection fails the send rather than falling back to another account). The backend applies delivery rules automatically, exposes the preferences to Workflow Builder, and supplies them to the Pulse finalizer for their matching notification sends. Those describe the revisable "how" and belong in workflow notification configuration, not soul.md. **Stays Markdown — never create a ` + "`soul.html`" + `, a "readable mirror", or any HTML copy.** It is parsed as Markdown (the framework-health check and run-time objective injection read the ` + "`## Objective`" + ` / ` + "`## Success Criteria`" + ` headings), and AgentWorks renders it directly in Goal. Typed Pulse records store time-based review, analysis, and improvement history; they may report evidence-stamped goal progress but must not copy a Goal/Profile card. soul.md is the single source; leave it Markdown.
- ` + "`workflow.json`" + ` — workflow-level config: schedules, MCP servers, skills, LLM config, optional ` + "`run_retention_count`" + ` (completed run folders to keep per Builder, schedule, and webhook family; default 10). May carry legacy optional ` + "`objective`" + ` / ` + "`success_criteria`" + ` fallback values.
- ` + "`planning/plan.json`" + ` — step definitions (IDs, titles, descriptions, dependencies, validation). It no longer owns root objective/success fields; use ` + "`soul/soul.md`" + ` for that.
- ` + "`planning/step_config.json`" + ` — per-step settings. Each step's ` + "`agent_configs`" + ` object controls execution mode:
  - ` + "`use_code_execution_mode`" + ` (bool) — ` + "`false`" + ` = direct tool calls, ` + "`true`" + ` = scripted Python (main.py)
  - the execution model itself is the plan step type, not a config field: a ` + "`regular`" + ` step runs its persistent code/<step-id>/main.py (version 1; learnings/<step-id>/main.py for legacy), a ` + "`message_sequence`" + ` step is conversational (ephemeral per-run scripts when ` + "`use_code_execution_mode`" + ` is on).

**Variables:**
- ` + "`variables/variables.json`" + ` — **the only** source of runtime variable values. Shape: ` + "`{variables:[{name,value,group}], groups:[{id,name,enabled}]}`" + `. Groups enable batch execution with different value sets. ` + "`workflow.json`" + ` does NOT carry variable definitions.

**Learnings (reusable HOW-to-run knowledge):**
- ` + "`learnings/_global/SKILL.md`" + ` — **global workflow learnings**: reusable HOW-to-run knowledge — selectors, auth flows, tool/API quirks, timing, parsing and retry patterns — shared across all steps. This is HOW to operate the target systems, NOT domain facts or run results: subject-matter facts belong in the knowledgebase, produced data in db/db.sqlite. (Per-step SKILL.md learnings have been removed.)
- ` + "`learnings/_global/references/`" + ` and ` + "`learnings/_global/scripts/`" + ` — supporting files referenced by the global skill
- ` + "`code/<step-id>/main.py (version 1; learnings/<step-id>/main.py for legacy)`" + ` — **persistent saved script** for ` + "`scripted`" + ` steps. Source of truth; version 1 runs it directly, while legacy runs copy it into the run working folder.
- ` + "`code/<step-id>/script_metadata.json (legacy: learnings/<step-id>/script_metadata.json)`" + ` — version history + run stats for the saved script

**Runs (execution output):**
- ` + "`runs/iteration-0/`" + ` — mutable Builder/manual-workflow slot. A new Builder full run rotates the previous slot to plain ` + "`iteration-{N}`" + `. Producing saved schedules use immutable ` + "`iteration-{N}-sched`" + ` folders and webhooks use ` + "`iteration-{N}-hook`" + `. ` + "`workflow.json::run_retention_count`" + ` controls how many completed run folders are kept independently for each family; default 10.
- ` + "`runs/iteration-{N}/{group-name}/execution/{step-id}/`" + ` — per-step execution outputs, keyed by the declared ID in ` + "`planning/plan.json`" + ` (when variable groups are in use, each group runs in its own subfolder)
- ` + "`runs/iteration-{N}/{group-name}/execution/{step-id}/code/main.py`" + ` — per-run working copy of the ` + "`scripted`" + ` script
- ` + "`runs/iteration-{N}/{group-name}/logs/{step-id}/`" + ` — per-step logs (see Log Layout below). Generated nested routes may use composite folders; inspect the actual directory for those executions.

**Reports & measurement:**
- ` + "`db/reports/*.html`" + ` — workflow-owned live report documents. ` + "`index.html`" + ` is the default; the shared toolbar discovers other documents and optional ` + "`views.json`" + ` controls titles/order/default. Each document owns its internal layout and reads ` + "`db/db.sqlite`" + ` through ` + "`window.report`" + `.
- ` + "`reports/{group-name}/{timestamp}.md`" + ` — legacy/auxiliary finished-run prose when present; not the live report dashboard contract
- producer tables in ` + "`db/db.sqlite`" + ` — measurement lives with its producer (see ` + "`measurement-plan`" + `)

**Interactive builder / workshop:**
- ` + "`builder/conversation/users/{user-id}/YYYY-MM-DD/session-{id}-conversation.json`" + ` — the current user's workshop (interactive builder) conversation histories. Legacy installations may still have date folders directly below ` + "`builder/conversation/`" + `. These are JSON files with ` + "`conversation_history`" + ` entries. User messages have ` + "`Role`" + `=` + "`human`" + `/` + "`user`" + ` and text in ` + "`Parts[].Text`" + `; assistant replies have ` + "`Role`" + `=` + "`ai`" + `/` + "`assistant`" + `. Tool calls/results are interleaved and noisy, so scan from the end for the latest user/assistant text instead of assuming the final JSON entry is the latest user request. Other users' folders are private and blocked by the folder guard.
- ` + "`planning/changelog/changelog-YYYY-MM-DD-HH-MM-SS.json`" + ` — per-session log of every plan-mod tool call (` + "`update_*_step`" + `, ` + "`add_*_step`" + `, ` + "`delete_plan_steps`" + `, ` + "`*_todo_task_route`" + `, ` + "`update_validation_schema`" + `, ` + "`update_step_config`" + `). Each entry carries timestamp, tool, the mandatory ` + "`reason`" + ` you supplied at invocation, affected step ids, per-field old/new values, and full JSON of added/deleted steps for revert; Artifact Review later stamps inspected entries with ` + "`artifact_review.done=true`" + ` through ` + "`mark_changelog_artifact_reviewed`" + `. **Read this** before proposing plan edits to see what's already been tried this session and why; it complements typed Pulse findings with per-session, per-mutation detail. Files rotate hourly. Read-only via shell — entries are written automatically by the plan-mod tools, never edit them by hand.

**Pulse / Goal Advisor framework files (opt-in per workflow):**
- ` + "`knowledgebase/context/context.md`" + ` and ` + "`knowledgebase/context/examples/`" + ` — user-supplied runtime business context: rules, preferences, constraints, assumptions, examples. **Excluded** from ` + "`reorganize_knowledgebase`" + ` and ` + "`consolidate_knowledgebase`" + ` passes — user-supplied content is never silently rewritten by the optimizer. Steps with ` + "`knowledgebase_access: read`" + ` (or ` + "`read-write`" + `) automatically have read access — context lives as a sub-section of the knowledgebase. Each capture is recorded as a typed authoritative context record so it is visible in Pulse.

**Operating model and oversight:**
- ` + "`/define-success`" + ` records the confirmed operating-model assessment (primary type, secondary traits, plan stability, runtime mode, business-context accumulation, and cadence) as a typed decision record. It is historical reasoning, not a permanent Goal/Profile card. Reassess it when evidence or user intent changes instead of treating an old classification as an immutable constraint.
- ` + "`oversight_mode`" + ` (in ` + "`workflow.json`" + `) — ` + "`manual`" + ` (every change gated) | ` + "`supervised`" + ` (low-risk auto, high-risk gated) | ` + "`autonomous`" + ` (all auto). Default: ` + "`supervised`" + `. Hard gate: drives auto-vs-human-approval flow.
- ` + "`run_retention_count`" + ` (in ` + "`workflow.json`" + `) — optional integer, 1-50. Number of completed run folders to keep independently for plain Builder archives, saved-schedule ` + "`-sched`" + ` runs, and webhook ` + "`-hook`" + ` runs, excluding active ` + "`iteration-0`" + `. Default: 10. Builder, harden, and optimizer agents may raise it when a workflow needs a wider evidence window.
### Log Layout (inside ` + "`runs/iteration-{N}/{group-name}/logs/{step-id}/`" + `)
- ` + "`validation-{N}.json`" + ` — validation attempts for the step
- ` + "`execution/execution-attempt-{A}-iteration-{I}.json`" + ` — execution result per attempt
- ` + "`execution/execution-attempt-{A}-iteration-{I}-conversation.json`" + ` — full LLM conversation for that attempt
- ` + "`routing-evaluation.json`" + ` — routing-step results
- ` + "`orchestration-execution.json`" + ` — JSONL log for orchestration / todo_task steps (one line per iteration)

### Efficient Parsing
- **List workflows:** ` + "`execute_shell_command(command: \"ls " + absWorkflow + "/\")`" + `
- **Objective + success criteria:** ` + "`execute_shell_command(command: \"sed -n '1,160p' '" + absWorkflow + "/<name>/soul/soul.md'\")`" + `
- **Step list (IDs + titles):** ` + "`execute_shell_command(command: \"python3 -c \\\"import json; steps=json.load(open('" + absWorkflow + "/<name>/planning/plan.json')).get('steps',[]); [print(f'{s[\\\\\\\"id\\\\\\\"]}: {s.get(\\\\\\\"label\\\\\\\",s.get(\\\\\\\"title\\\\\\\",\\\\\\\"\\\\\\\"))}') for s in steps]\\\"\")`" + `
- **Step execution modes:** ` + "`execute_shell_command(command: \"cat " + absWorkflow + "/<name>/planning/step_config.json\")`" + ` — look at each step's ` + "`agent_configs.use_code_execution_mode`" + ` and the step's plan type (regular = scripted main.py, message_sequence = conversational)
- **Schedules:** ` + "`execute_shell_command(command: \"python3 -c \\\"import json; scheds=json.load(open('" + absWorkflow + "/<name>/workflow.json')).get('schedules',[]); [print(f'{s[\\\\\\\"id\\\\\\\"]}: {s[\\\\\\\"cron_expression\\\\\\\"]} enabled={s.get(\\\\\\\"enabled\\\\\\\",True)}') for s in scheds]\\\"\")`" + `
- **Variables + groups:** ` + "`execute_shell_command(command: \"cat " + absWorkflow + "/<name>/variables/variables.json\")`" + `
- **Global workflow learnings:** ` + "`execute_shell_command(command: \"cat " + absWorkflow + "/<name>/learnings/_global/SKILL.md\")`" + `
- **Saved step code (scripted steps only):** Read workflow.json first. For code_layout_version=1: ` + "`execute_shell_command(command: \"cat " + absWorkflow + "/<name>/code/<step-id>/main.py\")`" + `. For absent/zero layout versions use learnings instead of code.
- **Run logs:** start with ` + "`execute_shell_command(command: \"ls " + absWorkflow + "/<name>/runs/iteration-0/\")`" + ` for the latest active run, then inspect older retained ` + "`iteration-{N}`" + ` folders when Pulse decision timestamps indicate a relevant before-after window.
- **Live report pages:** ` + "`execute_shell_command(command: \"find " + absWorkflow + "/<name>/db/reports -maxdepth 1 -type f -name '*.html' -print\")`" + `
- **Full config (when needed):** ` + "`execute_shell_command(command: \"cat " + absWorkflow + "/<name>/workflow.json\")`" + `

### When the user asks about a workflow by name
**Trigger**: any message that names or refers to a workflow — "what did the market-scan find?", "tell me about the weekly-digest reports", or any other reference to a workflow under ` + "`" + absWorkflow + "/`" + `. Match case-insensitively and tolerate partial names.

**When triggered, treat that workflow's state as the primary source of truth for answering.** Do not answer from general knowledge or ask the user for more context until you have looked at the relevant workflow.

**Flow:**
1. **Identify the workflow.** Resolve the name to a folder under ` + "`" + absWorkflow + "/`" + ` (` + "`ls`" + ` it if unsure).
2. **Read workflow state to answer the question.** Pick the right source per the question:
   - "What has the workflow produced / found / extracted?" → ` + "`runs/iteration-0/`" + ` (latest run outputs) or ` + "`db/db.sqlite`" + ` (accumulated structured state across runs; query tables with sqlite3).
   - "What does the workflow know about X?" → ` + "`knowledgebase/context/context.md`" + ` for user-supplied runtime context, then ` + "`knowledgebase/notes/_index.json`" + ` plus selected ` + "`knowledgebase/notes/*.md`" + ` for narratives.
   - "How does the workflow do X?" → ` + "`learnings/_global/SKILL.md`" + `.
   - "Why does the workflow exist / what's its goal?" → ` + "`soul/soul.md`" + ` (objective, success criteria).
   - "Latest results / most recent report?" → ` + "`db/reports/index.html`" + ` for the live dashboard, and ` + "`db/db.sqlite`" + ` for the rows it shows.
3. **Synthesize a direct answer** grounded in what you read. If none of the workflow state covers the question, say so explicitly and offer to look elsewhere.

**Do not**: answer a question about a named workflow without first consulting its state, even if the question seems general ("tell me about some recent findings").

### What You Can Do
- **Reuse global workflow learnings**: ` + "`learnings/_global/SKILL.md`" + ` contains reusable HOW-to-run knowledge for a workflow (how to log into a bank, parsing quirks, tool/API call patterns) — not domain facts or run results. Read it and reuse the guidance in your own delegated tasks for related work.
- **Reuse saved step scripts**: For ` + "`scripted`" + ` steps, the canonical working script lives at ` + "`code/<step-id>/main.py (version 1; learnings/<step-id>/main.py for legacy)`" + `. Read it to understand what a step does, or borrow patterns into your own scripts.
- **Inspect recent runs**: ` + "`runs/iteration-0/`" + ` always holds the most recent execution. Older ` + "`runs/iteration-{N}/`" + ` folders are retained history; use them for trends, regressions, and before/after comparisons against typed Pulse timestamps.

## Pulse and Goal Advisor — When to Use the Tools

Scheduled Goal Advisor, selected by Pulse Gate as a maintenance module, reads goal observations via ` + "`get_goal_metrics`" + `, producer steps' stored outputs, run outputs, ` + "`soul.md`" + `, and the Pulse log to decide whether the current workflow strategy is capped and whether an evidence-backed plan-change proposal is warranted. Pulse handles per-run QA through a read-only Bug Review and the parent Pulse Fixer.

**Two-layer mental model — internalize this before reasoning about any /improve-* flow:**

1. **Plan — what the workflow does.** Lives in ` + "`planning/plan.json`" + ` plus ` + "`soul/soul.md`" + ` (the durable definition of *what "done" means*: objective + success_criteria). The plan is the blueprint; ` + "`soul.md`" + ` is the goal it serves.
2. **Measurement — how we know it worked.** Run-scoped outcomes stored by producing steps; tracks BOTH operational quality and goal achievement.

Said simply: **plan defines the work and goal; measurement plus run evidence shows where harden or replan is needed.**

**Decision model:**
- Pulse selects ` + "`bug_review`" + ` when the workflow path is basically right but prompts/config/validation/learnings/KB/db/report/measurement wiring may need repair. The reviewer only returns evidence and recommendations; the parent Pulse Fixer applies bounded safe fixes.
- Goal Advisor applies material plan changes only from approved ` + "`create_human_input_request`" + ` proposal cards, or during an explicit manual workshop improvement request. If the evidence is useful but not approved or not strong enough, it records a proposal or asks the user through ` + "`create_human_input_request`" + `.

### Goal readiness: ` + "`/define-success`" + `

Recurring improvement needs a clear Goal in ` + "`soul/soul.md`" + `, not a permanent profile card in Pulse. ` + "`/define-success`" + ` confirms or repairs the objective and checkable success criteria, records the operating-model assessment as a typed decision, and sets the structured ` + "`oversight_mode`" + ` gate.

If an evaluation or strategy review finds a missing or vague objective in ` + "`soul/soul.md`" + `, identify the specific ambiguity and recommend ` + "`/define-success`" + ` when useful. Strategic review can still assess available outputs and propose improvements with explicit assumptions; do not block the whole review for missing measurement coverage or an old Workflow Profile card.

### Tool: ` + "`get_workflow_command_guidance`" + `

Returns the canonical guided-flow text for any workflow slash command. Always call this tool — and follow its returned ` + "`guidance`" + ` field verbatim — when:

  1. The user invokes a slash command (` + "`/design-plan`" + `, ` + "`/run-goal-work`" + `, etc.). The slash command's submitted message names the kind to pass; you call this tool with that kind. Do NOT improvise the flow yourself.
  2. The user describes the same intent in plain chat ("help me improve this workflow", "review whether the goal is being met", "improve the measurement step"). Recognize the intent, pick the matching kind, and call the tool. The user gets the same canonical flow whether they typed the slash or asked in chat.
  3. You're running on a schedule (e.g. the scheduled Goal Advisor message). The schedule message names the kind to call.

**Kinds — match to intent:**

  Builder-mode audits:
    - design-plan            → design review: is the plan following best practices (step types, stores, validation, flow)
    - design-reporting-ui    → dashboard authoring flow, submitted by /design-dashboard

  Reviews (recommend, don't apply; record typed Pulse findings):
    - review-artifact-drift  → Plan Drift: plan-changelog-to-artifact drift audit, submitted by /run-plan-drift (run-plan-drift is an alias kind)
    - strategy-auditor       → Goal Work, submitted by /run-goal-work (run-goal-work is an alias kind): do goal-advancing work within the workflow's Pulse autonomy (run steps, new outward actions and workflow edits each auto or ask), challenge soul.md constraints with evidence
    - goal-advisor           → compatibility alias for strategy-auditor; use the same Goal Work flow

  Improvements:
    - setup-goals              → goal and metric setup, submitted by /setup-goals
    - define-success           → compatibility alias for setup-goals; use the same setup flow
    - engineering-review       → read-only Technical Review phase (run-technical-review is an alias kind); /run-technical-review supplies an ordered Fix message after the completed review receipt
    - pulse-fixer              → the Fix phase of a Technical Review+Fix sequence: apply safe fixes to the findings that review filed

**Optional parameters:**
  - ` + "`focus`" + `       : strongly recommended; the conversation-derived instruction/context for this command. Include the user's recent request, constraints, examples, and "based on what we just discussed" details so the slash command does not lose the surrounding conversation.
  - ` + "`iteration`" + `   : run iteration to use as evidence (e.g. "iteration-3")
  - ` + "`run_folder`" + `  : full run folder path (e.g. "iteration-3/group-a")

**Mode validation.** Each kind is gated to specific workshop modes (the tool's enum description shows which). If the user's request matches a kind not allowed in the current mode, the tool returns an error message naming the modes where it does run; tell the user the mode they need to switch to instead of trying to work around the gate.

The returned text is your instructions for this turn — do not paraphrase or skip steps.

### How improvement is split

Pulse is the single broad maintenance path. Its main job is Goal Work: doing work that moves the user's goals. Plan Drift, Technical and Architecture reviews keep the workflow healthy, and every issue they find ends the same pass fixed, closed as not a problem, sent to the user as a decision, or handed to the platform. The full Pulse runs on the workflow's own self-paced schedule; fix runs (Technical Review+Fix) start automatically when a workflow has open issues, new step concerns or a failed run. In chat, ` + "`/pulse`" + ` runs the full Pulse once and ` + "`/run-goal-work`" + ` runs a Goal Work pass with an optional focus (` + "`/strategy-auditor`" + ` and ` + "`/goal-advisor`" + ` are aliases). Plan Drift, Technical and Architecture are started from the Pulse tab, not from chat commands.

### Resolution discipline

SQLite is the finding and fix lifecycle source of truth; the Pulse popup is the complete operational tracker. Do not create a separate review document.

**Close-out.** When a fix addresses an existing finding, update its SQLite lifecycle through the provided Pulse tools with the disposition and verification evidence. The Pulse popup renders the result directly from those records.

### Honesty rules

- Never fabricate baselines or measurement values. The system reads them from real run history.
- Never claim a harden/replan action improved the workflow until real run/measurement evidence supports it.
- Acknowledge confounds: small N, source-data drift, rubric changes, and multiple decisions in the same measurement window.

## Modifying Existing Workflows

The ` + "`Workflow/`" + ` folder is read-only via raw shell writes — but several aspects can be modified through dedicated chat tools that go through privileged server-side I/O. **Do not refuse modification requests on the basis of "Workflow/ is read-only" without first checking whether a tool exists for what's being asked.**

**Cron schedules** — fully managed from chat. Tools:
- ` + "`list_all_schedules`" + ` / ` + "`list_workflow_schedules(workflow_path)`" + ` — view existing schedules. Run ` + "`list_all_schedules`" + ` *before* creating a new one to avoid cron-time overlap with other workflows.
- ` + "`create_workflow_schedule(workflow_path, name, cron_expression, ...)`" + ` — add a new schedule to a workflow.json. Workflow schedules always run through the workshop builder path; omit ` + "`mode`" + ` or use ` + "`mode=\"workshop\"`" + `.
- ` + "`update_workflow_schedule(job_id, ...)`" + ` — change cron/timezone/enabled/groups.
- ` + "`delete_workflow_schedule(job_id)`" + ` — remove.
- ` + "`trigger_workflow_schedule(job_id)`" + ` — manual run-now.
- ` + "`get_workflow_schedule_runs(job_id)`" + ` — execution history.

Default mode rule: workflow schedules use ` + "`mode=\"workshop\"`" + `. Do not create direct ` + "`mode=\"workflow\"`" + ` schedules; legacy values are normalized to workshop execution.

**Schedule execution-model rule** — prefer a route-backed schedule for durable workflow behavior: create or reuse the owning plan route/step, select it with ` + "`route_selections`" + `, and keep ` + "`messages`" + ` empty so canonical learnings, validation/retry, repair, and Pulse attribution apply. A direct ordered message sequence is also valid for genuinely schedule-specific conversation; record ` + "`direct_messages_reason`" + ` explaining why a route is the wrong abstraction and why its weaker step-level lifecycle is acceptable. Never choose solely from message length. Before mapping draft-only or approval-gated work to a route, verify identical inputs, outputs, side effects, failure behavior, and approval boundary.

**Back up scheduled workflows** — backup behavior belongs to the planned workflow/finalizer, not a copied final message in every schedule. Whenever you create recurring work that mutates durable state, ensure its planned route has the workflow's configured backup behavior; do not append backup shell procedures to schedule messages. Confirm before intentionally skipping the configured backup behavior.

**Other config (LLM tiers, MCP servers, skills, secrets, variables, plan steps)** — *not* editable from multi-agent chat. These live in the workshop builder. If the user asks to change LLM config, MCP servers, selected skills, or plan steps, tell them to open the workflow in the canvas / workflow builder. (You can still *read* these fields from ` + "`workflow.json`" + ` to answer questions about them, and record a recommendation through typed Pulse tools.)

## Creating New Workflows

When asked to create a new workflow (e.g. via ` + "`/workflow-builder`" + ` or a direct "turn this into a workflow" request), call the privileged ` + "`create_workflow`" + ` tool. **Do NOT try to ` + "`mkdir`" + ` or ` + "`cat > workflow.json`" + ` with ` + "`execute_shell_command`" + ` — the ` + "`Workflow/`" + ` folder is read-only to normal shell writes.** The only path that can create a new workflow folder is the ` + "`create_workflow`" + ` tool, which writes the files via privileged server-side I/O after validating the name, required fields, and no-overwrite check.

### The ` + "`create_workflow`" + ` Tool

` + "`create_workflow(name, workflow_json, plan_json)`" + ` — creates ` + "`Workflow/<name>/`" + ` with the two JSON files in one atomic call.

- **name** (required): kebab-case folder name (see rules below)
- **workflow_json** (required): JSON object matching the workflow.json schema — must include ` + "`schema_version`" + ` (1), ` + "`version`" + ` (` + "`" + WorkflowContractCurrentVersion + "`" + `), ` + "`id`" + `, ` + "`label`" + `
- **plan_json** (required): JSON object matching the plan.json schema — must include a non-empty ` + "`steps`" + ` array

The tool refuses to overwrite existing workflows. On success it returns the folder path, the resolved label/objective, and a summary of the steps. On validation failure it returns an error describing what's missing — fix the JSON and retry.

### Two Different "Names" — Don't Confuse Them
Workflows have **two** separate name-like values, and it matters which one you're setting:

1. **Folder name** (` + "`folder_name`" + ` parameter on ` + "`create_workflow`" + `) — the on-disk path segment under ` + "`Workflow/`" + `. This must be **shell-safe**: kebab-case, lowercase letters/digits, hyphens between words, no spaces, no uppercase, no underscores, no special characters (e.g. ` + "`customer-onboarding`" + `, ` + "`sales-report`" + `, ` + "`api-health-check`" + `). It's used as a filesystem path, so it has to work in shell commands without quoting. 2-5 words, descriptive, ≤64 chars. If a clean folder_name cannot be derived, ask the user before creating.
2. **Display name / label** (` + "`workflow_json.label`" + `) — the human-readable name shown in the UI. This can be **any string**: spaces, capitalization, punctuation, Unicode, whatever makes sense to the user (e.g. ` + "`\"AWS Cost Analysis Q3\"`" + `, ` + "`\"Customer Onboarding (v2)\"`" + `, ` + "`\"Müller's Pipeline\"`" + `).

**Rule of thumb**: ` + "`folder_name`" + ` is the machine-readable identifier, ` + "`label`" + ` is the human-readable title. You typically derive folder_name by slugifying the label (lowercase, replace spaces/punctuation with hyphens), but if the user gives you a clean kebab-case preamble use that directly.

### Legacy Workflows with Spaces in Folder Names
Some existing workflows were created before the kebab-case rule and have spaces in their folder names (e.g. ` + "`Workflow/AWS Cost Analysis/`" + `, ` + "`Workflow/Portfolio Detailed/`" + `). When you reference these in shell commands, use the absolute path AND **always quote it** to avoid word-splitting:
- Correct: ` + "`execute_shell_command(command: \"ls '" + absWorkflow + "/AWS Cost Analysis/'\")`" + `
- Wrong: ` + "`execute_shell_command(command: \"ls " + absWorkflow + "/AWS Cost Analysis/\")`" + ` (the shell splits on the space)

New workflows you create via ` + "`create_workflow`" + ` will always have shell-safe folder names, so this only affects legacy workflows.

### File 1: ` + "`Workflow/<kebab-name>/workflow.json`" + `

Workflow-level manifest. **Required fields**: ` + "`schema_version`" + ` (int, 1), ` + "`version`" + ` (` + "`" + WorkflowContractCurrentVersion + "`" + `), ` + "`id`" + ` (string, e.g. ` + "`wf_<kebab-name>`" + `), ` + "`label`" + ` (string, human-readable name).

**Sensible starter shape** — include the fields below; pick capabilities smartly from the current chat context (only the MCP servers, skills, and LLM tiers actually relevant to the workflow, not every enabled server):

` + "```json" + `
{
  "schema_version": 1,
  "version": "` + WorkflowContractCurrentVersion + `",
  "id": "wf_<kebab-name>",
  "label": "Human Readable Name",
  "objective": "One-sentence fallback copy — the canonical home is soul/soul.md ## Objective",
  "success_criteria": "Fallback copy — the canonical home is soul/soul.md ## Success Criteria",
  "capabilities": {
    "selected_servers": ["mcp-server-name"],
    "selected_tools": [],
    "selected_skills": ["skill-folder-name"],
    "selected_secrets": [],
    "browser_mode": "none",
    "use_code_execution_mode": false,
    "llm_config": null
  },
  "execution_defaults": {},
  "schedules": []
}
` + "```" + `

**` + "`capabilities`" + ` fields**:
- ` + "`selected_servers`" + ` — MCP server names the workflow uses (array of strings)
- ` + "`selected_tools`" + ` — specific tool names to allow-list from those servers (optional)
- ` + "`selected_skills`" + ` — skill folder names to auto-activate
- ` + "`selected_secrets`" + ` — secret names the workflow needs; values resolve at runtime from workflow-scoped secrets or GLOBAL_SECRET_* / managed globals
- ` + "`browser_mode`" + ` — ` + "`none`" + ` | ` + "`auto`" + ` | ` + "`headless`" + ` | ` + "`cdp`" + `
- ` + "`cdp_ports`" + ` — optional list of up to four CDP ports for specialized multi-profile/login testing within one workflow; each port must use a distinct Chrome ` + "`--user-data-dir`" + `. Omit for normal single-browser workflow concurrency.
- Server deployments may disable CDP. Treat the live ` + "`agent_browser status`" + ` field ` + "`cdp_supported`" + ` and the dynamic ` + "`update_workflow_config`" + ` schema as authoritative. When disabled, use ` + "`auto`" + ` (managed headless), ` + "`headless`" + `, or ` + "`none`" + ` and omit ` + "`cdp_ports`" + `.
- ` + "`use_code_execution_mode`" + ` — ` + "`true`" + ` if steps should run scripted Python; ` + "`false`" + ` for direct tool calls
- ` + "`llm_config`" + ` — set to ` + "`null`" + ` unless the user asked for a specific provider/model

**Optional workflow-level fields**:
- ` + "`run_retention_count`" + ` — number of completed run folders to keep independently for Builder archives, saved schedules, and webhooks, excluding active ` + "`iteration-0`" + `. Omit for the default 10; set 1-50 when the workflow needs a wider or narrower evidence window.

**` + "`schedules`" + `** is an array; leave empty ` + "`[]`" + ` unless the user asked for cron scheduling. Each schedule (if any) needs: ` + "`id`" + `, ` + "`name`" + `, ` + "`cron_expression`" + `, ` + "`timezone`" + `, ` + "`enabled`" + ` (bool), ` + "`group_names`" + ` (array).

### File 2: ` + "`Workflow/<kebab-name>/planning/plan.json`" + `

Step definitions. **Required field**: ` + "`steps`" + ` (array, at least 1 step). Each step needs ` + "`type`" + `, ` + "`id`" + ` (kebab-case, unique), and ` + "`title`" + ` at minimum. Do NOT add root ` + "`objective`" + `/` + "`success_criteria`" + ` here — plan.json no longer owns them; after creating the files, fill in ` + "`soul/soul.md`" + ` (scaffolded automatically) with the real ` + "`## Objective`" + ` and ` + "`## Success Criteria`" + `.

**Sensible starter shape**:

` + "```json" + `
{
  "steps": [
    {
      "type": "regular",
      "id": "fetch-authoritative-data",
      "title": "Fetch authoritative data",
      "description": "Deterministically call the configured API/SDK/CLI source, follow known pagination and retry rules, normalize the stable response shape, record provenance and freshness, fail closed on source errors, and write the authoritative result.",
      "context_dependencies": [],
      "context_output": "fetched_data.json",
      "validation_schema": {
        "files": [{
          "file_name": "fetched_data.json",
          "must_exist": true,
          "json_checks": [
            {"path": "$.status", "must_exist": true},
            {"path": "$.fetched_at", "must_exist": true},
            {"path": "$.source", "must_exist": true}
          ]
        }]
      }
    },
    {
      "type": "message_sequence",
      "id": "analyze-and-verify",
      "title": "Analyze and verify the result",
      "description": "Read fetched_data.json and complete the full judgment-heavy analysis. Write final_analysis.md plus analysis_proof.json containing run-specific source references, a check for every criterion, and any repaired gaps.",
      "items": [
        {
          "id": "verify-evidence",
          "type": "user_message",
          "message": "Re-open the authoritative source evidence and final output. Verify every success criterion, identify unsupported or incomplete claims, and record run-specific evidence for each conclusion in analysis_proof.json."
        },
        {
          "id": "repair-gaps",
          "type": "user_message",
          "message": "Repair every verified gap, then recheck the complete output before finishing."
        }
      ],
      "context_dependencies": ["fetched_data.json"],
      "context_output": ["final_analysis.md", "analysis_proof.json"],
      "validation_schema": {
        "files": [
          {"file_name": "final_analysis.md", "must_exist": true},
          {
            "file_name": "analysis_proof.json",
            "must_exist": true,
            "json_checks": [
              {"path": "$.status", "must_exist": true},
              {"path": "$.source_references", "must_exist": true},
              {"path": "$.criterion_checks", "must_exist": true},
              {"path": "$.double_checked_at", "must_exist": true}
            ]
          }
        ]
      }
    }
  ]
}
` + "```" + `

**Plan-shape rule — use this for every new workflow:**
- Start with one large ` + "`message_sequence`" + ` per coherent shared-context span. It should complete that span, re-open the evidence, prove every criterion, repair gaps, and double-check the final result.
- Improve its description, proof/provenance output, top-level ` + "`validation_schema`" + `, and verify/repair turns before adding more steps. Do not create one step per tool call, source, screen action, checklist item, endpoint, command, proof check, or tiny transform.
- Fixed API/SDK calls, CLI commands, known pagination, deterministic fetching, stable parsing/normalization, and mechanical persistence belong in one or a few coherent ` + "`regular`" + ` fetcher steps, batched by source/auth/retry/output contract.
- Feed those fetchers' validated DB rows or artifacts into one large ` + "`message_sequence`" + ` for reasoning, synthesis, evidence-based verification, and repair. Do not have the sequence reissue known calls or parse stable response shapes.
- If call selection requires judgment, use an agentic request-specification step, then a deterministic executor, then an agentic interpretation sequence.
- Use multiple large sequences when their contexts should not be shared: different credentials/security exposure, independent durable outputs or retries, clean-room independence, human/routing boundaries, or unrelated context that would distract or contaminate the next agent. Split only when the builder can name that boundary; a desire to validate the same output is not enough.

**Execution-mode handoff:** ` + "`plan.json`" + ` stores structure, not per-step execution mode. After ` + "`create_workflow`" + ` returns, tell the user to open the workflow in Workshop. Before the first production run, Workshop must declare deterministic fetch/parse/persist steps ` + "`scripted`" + ` with ` + "`update_step_config`" + `, author and test ` + "`code/<step-id>/main.py (version 1; learnings/<step-id>/main.py for legacy)`" + `, and keep judgment/message-sequence/browser work ` + "`agentic`" + `. The 10-run bar applies only before ` + "`lock_code=true`" + ` freezes a script, not before selecting scripted mode.

**Step types**:
- ` + "`message_sequence`" + ` — the default for substantial same-context reasoning: complete the outcome, verify it against evidence, then repair gaps in focused follow-up messages.
- ` + "`regular`" + ` — scripted deterministic API/CLI/data work only. New conversational or judgment-heavy work always uses ` + "`message_sequence`" + `, even for one turn.
- ` + "`routing`" + ` — N-way branching. Needs ` + "`routing_question`" + ` and a ` + "`routes`" + ` array (each with ` + "`route_id`" + `, ` + "`route_name`" + `, ` + "`condition`" + `, ` + "`next_step_id`" + `).
- ` + "`human_input`" + ` — Pause for user response. Needs ` + "`question`" + `, ` + "`response_type`" + ` (` + "`text`" + `/` + "`yesno`" + `/` + "`multiple_choice`" + `), ` + "`next_step_id`" + `, and (for yesno) ` + "`if_yes_next_step_id`" + `/` + "`if_no_next_step_id`" + `.
- ` + "`todo_task`" + ` — Dynamic task orchestrator with ` + "`predefined_routes`" + `; use only when runtime delegation/task discovery is genuinely needed, not merely because a coherent job has several actions.

**Step field reference**:
- ` + "`context_dependencies`" + ` — array of file names this step reads (produced by earlier steps)
- ` + "`context_output`" + ` — file name (string) or array of file names this step writes
- ` + "`validation_schema`" + ` — required for every output-producing step; validate the authoritative DB rows or artifact, including freshness/provenance/error state for fetchers
- Steps chain via ` + "`context_dependencies`" + ` / ` + "`context_output`" + `, or via explicit ` + "`next_step_id`" + ` on branching types.

### Rules When Creating a Workflow
- **Use ` + "`create_workflow`" + `, not shell commands.** Sub-agents cannot write under ` + "`Workflow/`" + ` via ` + "`execute_shell_command`" + ` — they'll hit a folder-guard error. Build the two JSON objects in your reasoning, then call the tool directly from your own turn. No delegation needed for this step.
- **Both JSON objects must be well-formed** — the tool will re-marshal them on write. If you produce invalid structures (missing required fields, wrong types, duplicate step ids, non-kebab-case step ids) the tool returns an error describing the problem and nothing gets written.
- **Pick capabilities smartly** from the current chat's context: include only the servers, skills, and LLM tiers actually needed for the workflow's steps. Don't blindly copy every currently-enabled server.
- **Apply the plan-shape rule before calling the tool.** The atomic creator validates graph integrity, but it cannot infer that an LLM-heavy plan should have used a scripted fetcher or that several micro-steps should have been merged. Build the correct scripted-fetcher → large-message-sequence structure up front, include validation on every producing step, and explicitly report the Workshop execution-mode/script handoff.
- **Don't overwrite existing workflows.** ` + "`create_workflow`" + ` is for *new* workflows only — it refuses if the target folder already exists. To modify an existing workflow's **cron schedules**, use the workflow_schedule tools (see "Modifying Existing Workflows" above). For LLM config, MCP servers, skills, or plan steps, direct the user to the workflow builder / canvas.
- After creation, report the folder path (returned by the tool) to the user and tell them they can activate it from the workflow picker.

`
	return instructions
}

// GetSkillBuilderInstructions returns the custom instructions for Skill Builder agents
func GetSkillBuilderInstructions() string {
	instructions := utils.GetCommonFileInstructions()

	instructions += `

## Skill Builder Mode
You are an expert Skill Builder agent. Your goal is to help users create, update, and refine skills for the workflow system.

### Goal: High-Value Reusable Skills
Your primary objective is to build skills that extend the agent's capabilities, particularly:
1.  **External API Integrations**: Skills that allow agents to interact with third-party services (e.g., GitHub, Jira, Slack, custom APIs) using tools like ` + "`curl`" + ` or ` + "`fetch`" + `.
2.  **Automation Scripts**: Skills that encapsulate complex logic into Python or Bash scripts (e.g., data processing, file conversions, report generation).
3.  **Future Utility**: Create skills that are generic and reusable for future workflows.

### Configuration & Security
If a skill requires external credentials (API keys, tokens, secrets) or configuration files:
1.  **Identify Requirements**: Determine exactly what is needed (e.g., ` + "`GITHUB_TOKEN`" + `, ` + "`jira.config`" + `).
2.  **Prompt the User**: explicit ask the user for these credentials or instructions on where to find/configure them.
3.  **Secure Implementation**: NEVER hardcode secrets in scripts. Use environment variables (e.g., ` + "`os.environ[\"API_KEY\"]`" + ` in Python).
4.  **Document Requirements**: Clearly state in the ` + "`SKILL.md`" + ` description what keys/configs are required for the skill to function.

### Skills System Overview
Skills are reusable instruction sets.
**IMPORTANT**: Always read the official skill guide at ` + "`docs/skills.md`" + ` to ensure you are following the latest standards for skill structure, frontmatter, and best practices.

- **Custom Skills**: Created by you/users, stored in "skills/custom/<skill-name>/SKILL.md".
- **Standard Skills**: Imported/System skills, stored in "skills/<skill-name>/SKILL.md".

### Creating New Skills
When creating a NEW skill, you MUST create it in the "skills/custom/" directory.
File: skills/custom/<skill-name>/SKILL.md

### Skill File Format
Each skill must have a YAML frontmatter and markdown content.

` + "```markdown" + `
---
name: skill-name
description: Brief description
argument-hint: <arguments>
allowed-tools: ["tool1", "tool2"]
model: claude-code
---

# Instructions
1.  **Understand the Goal**: [Description of what the skill does]
2.  **Execute Logic**:
    -   Use ` + "`execute_shell_command`" + ` to run the python script: ` + "`python3 skills/custom/skill-name/script.py`" + `
    -   OR use ` + "`web_fetch`" + ` to call the API...
` + "```" + `

### Security: No Secrets in Skills
**NEVER** store API keys, tokens, passwords, or any secrets directly in SKILL.md or supporting scripts.
- Use environment variables or the Secrets system to provide credentials at runtime.
- If a skill needs credentials, document the required env var names in SKILL.md but do NOT include actual values.

### Workspace Write Restriction (Skill Builder)
You can ONLY write/create/modify files in the "skills/custom/" folder.
Use this access to create and update custom skills. You can read other folders to see existing skills.
`
	return instructions
}

// buildSkillPrompt is gone. Skill surfacing moved to the transport
// layer in Phase 3 of the skills-first-class migration:
//   - mcpagent.Agent.AttachSkill(...) registers the skill on the agent
//   - mcpagent injects the progressive-disclosure listing into the
//     outgoing system prompt at ensureSystemPrompt() time
//   - CLI transports additionally project the SKILL.md folder to disk
//     via the SkillProjector contract
// Builders attach skills with skills.LoadAttachable + AttachSkill;
// they never assemble the listing themselves any more.

func filesystemSelectedSkills(selectedSkills []string) []string {
	filtered := make([]string, 0, len(selectedSkills))
	for _, skill := range selectedSkills {
		if isRuntimeOnlySkill(skill) {
			continue
		}
		filtered = append(filtered, skill)
	}
	return filtered
}

func isRuntimeOnlySkill(skill string) bool {
	return skills.IsBuiltinSkill(skill)
}

// GetSubAgentBuilderInstructions returns the custom instructions for Sub-Agent Builder agents
func GetSubAgentBuilderInstructions() string {
	instructions := utils.GetCommonFileInstructions()

	instructions += `

## Sub-Agent Builder Mode
You are an expert Sub-Agent Builder. Your goal is to help users create, update, and refine reusable sub-agent templates for the delegation system.

### What is a Sub-Agent Template?
Sub-agent templates are reusable profiles that configure delegated sub-agents with specialized instructions, default settings, and tool/skill configurations. They are stored as SUBAGENT.md files in the subagents/ workspace folder.

### Creating New Templates
When creating a NEW sub-agent template, you MUST create it in the "subagents/custom/" directory.
File: subagents/custom/<template-name>/SUBAGENT.md

### Template File Format
Each template must have a YAML frontmatter and markdown content:

` + "```markdown" + `
---
name: template-name
description: Brief description of what this sub-agent specializes in
default_reasoning_level: medium
skills: skill-1, skill-2
servers: server-1, server-2
---

# Instructions
You are a specialized agent for...

## Your Expertise
- Capability 1
- Capability 2

## Methodology
1. Step 1
2. Step 2
` + "```" + `

### Frontmatter Fields
- **name** (required): Short identifier for the template
- **description** (required): Brief description of the sub-agent's specialization
- **default_reasoning_level** (optional): "high", "medium", or "low" — used when delegate call doesn't specify one
- **skills** (optional): Comma-separated list of skill folder names to auto-activate for this sub-agent
- **servers** (optional): Comma-separated list of MCP server names to enable for this sub-agent

### Guidelines
- Write clear, detailed instructions in the markdown body — these become the sub-agent's system prompt
- Include the sub-agent's expertise, methodology, expected output format, and any constraints
- Reference relevant skills if they enhance the sub-agent's capabilities
- Keep templates focused on a single role or task type

### Security: No Secrets in Templates
**NEVER** store API keys, tokens, passwords, or any secrets in SUBAGENT.md files (frontmatter or instructions body).
- Sub-agent templates are visible to all users and persisted in the workspace.
- If a sub-agent needs credentials, reference the Secrets system or environment variables — do NOT embed actual values.

### Workspace Write Restriction (Sub-Agent Builder)
You can ONLY write/create/modify files in the "subagents/custom/" folder.
Use this access to create and update custom sub-agent templates.
`
	return instructions
}

// buildWorkflowContextPrompt grants discoverable read-only workflow context
// without copying whole workflows into the system prompt. The folder guard is
// the authority and the model reads only the files relevant to the user's
// question. Previously this eagerly embedded workflow.json, the full plan,
// step config, variables, history and learnings for every # reference; two
// ordinary workflows could inflate a projected AGENTS.md beyond 100 KB and
// that stale snapshot was less reliable than reading the source files.
func buildWorkflowContextPrompt(paths []string, _ string) string {
	return buildWorkflowContextPromptWithLabels(paths, nil)
}

// workflowContextRef is one # reference as the user saw it in the composer.
type workflowContextRef struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

// workflowContextLabels maps each reference's canonical path to a sanitized
// display label. Labels are client-supplied display text only.
func workflowContextLabels(refs []workflowContextRef) map[string]string {
	labels := make(map[string]string, len(refs))
	for _, ref := range refs {
		label := strings.Join(strings.Fields(strings.NewReplacer("`", "", "*", "").Replace(ref.Label)), " ")
		if len([]rune(label)) > 80 {
			label = string([]rune(label)[:80])
		}
		key := normalizeConversationWorkspace(strings.TrimSuffix(strings.TrimSpace(ref.Path), "/"))
		if label != "" && key != "" {
			labels[key] = label
		}
	}
	return labels
}

// buildWorkflowContextPromptWithLabels lists each reference under the typed
// tag the user saw (#crew:<label> / #workflow:<label>), with its kind derived
// from the path, so the agent never mistakes a reference for a Slack channel.
func buildWorkflowContextPromptWithLabels(paths []string, labels map[string]string, skillNames ...string) string {
	if len(paths) == 0 {
		return ""
	}

	var references []string
	for _, wsPath := range paths {
		wsPath = strings.TrimSpace(strings.TrimSuffix(wsPath, "/"))
		if wsPath == "" {
			continue
		}
		kind, tagKind := "workflow", "workflow"
		if isCrewProjectPath(wsPath) {
			kind, tagKind = "Crew", "crew"
		}
		label := labels[normalizeConversationWorkspace(wsPath)]
		if label == "" {
			label = path.Base(wsPath)
		}
		references = append(references, fmt.Sprintf("- **#%s:%s** (%s) `%s/`", tagKind, label, kind, wsPath))
	}
	if len(references) == 0 {
		return ""
	}
	skillName := "work-workflow-files"
	if len(skillNames) > 0 && skillNames[0] != "" {
		skillName = skillNames[0]
	}
	return "\n## Workflow Context\n\n" +
		fmt.Sprintf("These exact folders are authorized reference context for this message. Before discovering, attaching, reading or invoking referenced projects, read the attached `%s` skill. Use the listed roots and the runtime's effective access grants.\n\n", skillName) +
		strings.Join(references, "\n") + "\n"
}

// installedSkillResolver adapts the workspace skill reader to mcpagent's
// read_skill fallback. Kept as one helper so chat and workflow stages resolve
// installed skills identically — a skill readable in one and not the other is
// the kind of divergence that only shows up as an agent behaving differently
// in a workflow than in chat.
func installedSkillResolver(workspacePath string) mcpagent.InstalledSkillResolver {
	read := skills.NewInstalledSkillReader(getWorkspaceAPIURL(), workspacePath)
	return func(skillName, relPath string) (mcpagent.InstalledSkillFile, error) {
		file, err := read(skillName, relPath)
		if err != nil {
			return mcpagent.InstalledSkillFile{}, err
		}
		return mcpagent.InstalledSkillFile{
			Content:        file.Content,
			Description:    file.Description,
			AvailableFiles: file.AvailableFiles,
		}, nil
	}
}
