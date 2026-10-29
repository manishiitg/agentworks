// Package guidance owns the canonical guided-flow text for every workflow
// slash command in the workshop UI. The text lives as embedded markdown
// templates so it can be rendered with focus/iteration/run_folder params and
// returned to the agent — the agent then follows the rendered prose verbatim.
// Focus is the conversation-derived instruction or context that caused the
// command, not just a narrow keyword. Slash-command wrappers should pass the
// user's surrounding/preceding request into focus when available.
//
// The same prose is reachable from three contexts:
//
//  1. A user typed a slash command. The slash command's frontend onSubmit
//     collapses to one line: "Call get_workflow_command_guidance(kind=<name>,
//     ...) and follow the returned instructions."
//  2. A user described the same intent in chat ("help me improve this
//     workflow"). The agent recognizes the intent and calls the tool.
//  3. An ordinary scheduled chat calls
//     the tool to get the same canonical flow.
//
// One source of truth, three callers.
package guidance

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/externalguidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

//go:embed templates/builder/*.md templates/review/*.md templates/improve/*.md templates/report/*.md templates/system/*.md
var templatesFS embed.FS

// kindMeta captures everything we know about a guided flow at registration
// time: which file holds its template, which workshop modes are allowed to
// invoke it, and a one-line description used in the tool-list enum.
type kindMeta struct {
	Group          string // builder | review | improve | report | kb | learning | db
	Description    string // shown to the agent in the kind enum
	Modes          []string
	RawTemplate    bool // expose runtime template source without evaluating its conditions/placeholders
	PanelSkill     string
	PanelReference string

	// Tools names the runtime tools this doc explains. It is the selection key
	// for surfaces that choose references by capability instead of by mode —
	// today that is step execution (see MaterializeStepExecutionReferenceSkill).
	//
	// PLAT-125: a step agent holding eight tools was handed the workshop chat's
	// 41-doc builder bundle, including llm-provider-config, and duly called
	// list_published_llms — which its session never registered. Left empty, a
	// doc is simply never selected by capability, which is the safe default for
	// the Builder reference docs that no runtime tool corresponds to.
	Tools []string
}

// allKinds is the canonical registry of guided flows. Adding a new kind:
//
//  1. Drop a markdown file in templates/<group>/<kind>.md.
//  2. Add an entry here with description + allowed modes.
//  3. Update the slash command's onSubmit to call this tool with the new kind.
//
// Modes match interactive_workshop_manager.go's switch. "workshop" is the
// canonical merged mode (was builder + optimizer in older releases);
// "builder" and "optimizer" resolve to the merged mode. "run" is constrained
// runtime; "reporting" is the report-only surface. The tool refuses kinds
// not allowed in the caller's mode and tells the agent to suggest a mode
// switch.
var allKinds = map[string]kindMeta{
	// Comprehensive plan review: critical artifact audit plus better-shape design guidance.
	"design-plan": {Group: "builder", Description: "Comprehensive workflow plan and dependent-artifact review with better design recommendations", Modes: []string{"workshop"}},

	// Reviews record concrete evidence and unresolved owner decisions.
	"run-plan-drift": {Group: "review", Description: "Manual Plan Drift: check affected compatibility and prompt quality, apply bounded safe repairs through the scheduled candidate/receipt contract, and persist remaining dependency findings", Modes: []string{"workshop"}},

	// Improvements — evidence-driven reliability and strategy flows
	"setup-goals":      {Group: "improve", Description: "Set up primary and secondary outcome goals, primary and supporting metrics, collection and targets for new or existing workflows", Modes: []string{"workshop"}},
	"design-dashboard": {Group: "report", Description: "Create or update user-designed db/reports/*.html views using the platform runtime and shared toolbar discovery: live goal/measurement/cost helpers, optional prebuilt metric widgets and daisyUI, report-owned approval buttons, live outside data via window.report.run scripts in code/reports/, and direct requests via window.report.sendChatMessage.", Modes: []string{"workshop"}},
}

// referenceKinds is the registry of system reference docs — content that
// used to live inline in the workshop system prompt and is now loaded on
// demand by the agent via mcpagent's intrinsic read_skill tool. These are not procedural
// flows (those live in allKinds); they are reference material the agent reads
// before performing certain actions (e.g. read "code-authoring" before
// patching main.py). Which docs to read is the agent's judgment call: every
// kind is also materialized into the projected reference skill, so the agent
// can reach the same content by reading references/<kind>.md from the attached
// builder-reference bundle.
//
// Adding a new reference doc:
//
//  1. Drop a markdown file in templates/system/<kind>.md.
//  2. Add an entry here with description + allowed modes.
//
// Modes use the same workshop mode strings as allKinds. "workshop" is the
// unified editable mode; "run" is constrained runtime; "reporting"
// is the report-only surface.
// References own procedures and examples. Prompts retain mode/authorization
// boundaries and live context; tool discovery uses the current runtime registry.
var referenceKinds = map[string]kindMeta{
	"chat-visuals":        {Group: "system", Description: "Native app chat diagrams, swimlanes, sequences, cards, charts and timelines; presentation only, with bounded formats and examples.", Modes: []string{"workshop", "run"}, RawTemplate: true, PanelSkill: "chat-visuals", PanelReference: "references/formats.md"},
	"dashboard-authoring": {Group: "system", Description: "Use the shared dashboard APIs to discover, author, validate, preview, publish, restore and share live dashboards; follow user design preferences.", Modes: []string{"workshop", "run", "reporting"}, RawTemplate: true, PanelSkill: "panel-dashboard", PanelReference: "references/dashboard/references/authoring.md"},
	"workflow-chat":       {Group: "system", Description: "Workflow chat procedures: runtime grounding, route/input selection, execution and notifications, Builder design/review flow, schedules and channel handling. Read before any workflow platform action.", Modes: []string{"workshop", "run"}},
	"step-system-prompts": {Group: "system", Description: "Canonical runtime system prompt source for agent steps, including managed DB guidance. Read alongside step-description before authoring: these platform rules are supplied by the runtime, so descriptions should add only task-specific requirements. Conditions/placeholders are resolved per run; get_step_prompts shows a saved run.", Modes: []string{"workshop"}, RawTemplate: true},
	// Workflow-scoped reference docs (workshop / run modes).
	"code-authoring":   {Group: "system", Description: "Detailed main.py authoring rules and patterns (env access, sys.argv contract, data authenticity, patching discipline)", Modes: []string{"workshop"}},
	"stores":           {Group: "system", Description: "Persistent store design contract: skill vs knowledgebase vs db, when to write to which", Modes: []string{"multi-agent", "workshop"}, Tools: []string{"query_workflow_db", "mutate_workflow_db", "apply_workflow_db_migration"}},
	"assumption-audit": {Group: "system", Description: "Bounded cross-artifact check that separates explicit user constraints and verified external facts from revisable design choices and agent-inferred assumptions; prevents plan/measurement/report/KB/learnings/DB/code from freezing an outdated approach", Modes: []string{"workshop"}},
	// Shared authoring and review references.
	"fix-verification":      {Group: "system", Description: "Proportional immediate checks for bounded repairs. Record what actually passed; changed_unverified still closes an applied fix when stronger runtime proof is unavailable. Reopen only on reproduction, with no future-run verification queue. Load before applying fixes.", Modes: []string{"workshop"}},
	"agent":                 {Group: "system", Description: "Agent steps: a description-only first user turn plus optional subsequent items in one shared conversation, with optional predefined_routes chosen by the agent, SQL foreach, validation/repair turns, and saved-script batches. Load before authoring items, routes, scripted_steps/parameters, or reusable specialist sequences.", Modes: []string{"workshop"}},
	"routing":               {Group: "system", Description: "Routing step design: deterministic major sub-workflow selection versus branch, plain agent, or an agent with adaptive specialist routes; route_selection.json, route_selections, convergence, and anti-patterns.", Modes: []string{"workshop"}},
	"branch":                {Group: "system", Description: "Branch step design: deterministic small in-flow next-step selection versus routing, human input, plain agent, or an agent with adaptive specialists; branch_question, convergence, and anti-patterns.", Modes: []string{"workshop"}},
	"scripted":              {Group: "system", Description: "Scripted step design: deterministic API/CLI/data work, script_parameters contracts and defaults, STEP_PARAMS_JSON, validation, permissions, and reuse from agent script batches or agent routes. Canonical plan type is scripted; conversational work uses agent. Load before adding or parameterizing a saved script.", Modes: []string{"workshop"}},
	"playwright-scripted":   {Group: "system", Description: "Repeatable browser tests: use @agentworks/playwright for JS/TS or agentworks-playwright Python sync/async/pytest fixtures for Chromium live viewing and Builder interaction", Modes: []string{"multi-agent", "workshop", "run"}, Tools: []string{"agent_browser", "execute_shell_command"}},
	"playbook-setup":        {Group: "system", Description: "Playbooks: recommending one with search_playbooks, reporting an update from its changelog, and setting up an installed multi-Crew Playbook (slots, handoffs, validators, first real case)", Modes: []string{"workshop", "run"}},
	"optimize-playbook":     {Group: "system", Description: "Optimizer deep-dive: harden vs replan decision tree, measurement, and the AgentWorks/Strategic Review framework", Modes: []string{"workshop"}},
	"step-config":           {Group: "system", Description: "Per-step config reference (planning/step_config.json via update_step_config): store-access modes (including db_access mapped to managed query/mutation tools, with $DB_PATH only for saved scripted compatibility), locks, execution mode, model selection, validation_schema, skills/tools, and clearing fields. Load before tuning a step's access, locks, mode, or model.", Modes: []string{"workshop"}},
	"file-layout":           {Group: "system", Description: "Workspace file layout reference and path discipline", Modes: []string{"multi-agent", "workshop", "run"}},
	"workflow-guide":        {Group: "system", Description: "Plain-language help for first-time users: explain the current view and core workflow concepts, answer before acting, and offer relevant next actions without unrequested changes.", Modes: []string{"workshop", "run"}},
	"workspace-views":       {Group: "system", Description: "What each workspace view on the workflow page shows and when to open or refresh it for the user (perform_ui_action)", Modes: []string{"workshop", "run"}, Tools: []string{"list_ui_capabilities", "get_ui_state", "perform_ui_action"}},
	"plan-editing-tools":    {Group: "system", Description: "Consolidated typed plan mutation arguments, native handler reuse and plan/config validation-store boundaries", Modes: []string{"workshop"}},
	"plan-design":           {Group: "system", Description: "Plan-design playbook: step boundaries, step-type selection, context flow, validation/failure design, anti-patterns, step-types reference. Load when designing a new plan or restructuring an existing one in DESIGN phase.", Modes: []string{"workshop"}},
	"step-description":      {Group: "system", Description: "Write agent prompts with the correct authority boundary: description and its task rules execute as the first user turn alone, and optional items are later user turns, skills/learnings carry reusable HOW, and validation_schema defines the load-bearing output shape. Load before writing or editing a step description, sequence items, or validation schema.", Modes: []string{"workshop"}},
	"plan-change-impact":    {Group: "system", Description: "Plan-change impact analysis: when a step changes (add/remove/reorder, output contract, db writes, or behavior) trace and reconcile the blast radius across downstream steps, measurement, the report dashboard, db, learnings, and KB — by searching the workspace for references to the change's surface, then fixing the clear ones and flagging the judgment calls. The planning/changelog is the work-list; record an impact summary and let run-plan-drift be the audit backstop. After related edits, do one combined compatibility check in the current agent before the targeted test; reserve a full drift audit for AgentWorks or an explicit user request. Load before treating a plan change as done.", Modes: []string{"workshop"}},
	"measurement-plan":      {Group: "system", Description: "Shared Builder measurement contract: ordinary steps record DB measurements and period history independently of folder identities; record_goal_observations for AgentWorks goal-progress history; legacy evaluation artifacts are read-only history. Load before editing measurement.", Modes: []string{"workshop"}},
	"llm-provider-config":   {Group: "system", Description: "Model Library and provider-auth management for multi-agent chat and workflow workshop: discover provider models, validate candidates, optionally save reusable provider/model/options configurations, preserve reasoning_effort, and never read or edit config/ files directly. Load when the user asks which LLMs exist, wants a reusable saved configuration, or needs provider auth.", Modes: []string{"multi-agent", "workshop"}},
	"llm-selection":         {Group: "system", Description: "Choosing the LLM that runs workflow work: provider-profile vs explicit Builder/high/medium/low roles via set_workflow_llm_config, per-step overrides (execution_tier, execution_llm, validation_llm), precedence rules, cost review tools, and provider auth. Load when picking, pinning, or changing which model executes a workflow step (not media generation — see workspace-media-tools for that).", Modes: []string{"workshop"}},
	"skill-management":      {Group: "system", Description: "Install skills and wire them into workflows: find (list_skills/search_skills), install (install_skill/import_skill), select for workflow/builder context (update_workflow_config add_skills), enable at runtime per step (update_step_config enabled_skills), the no-cascade attachment model, learnings/_global/SKILL.md as shared-know-how home, remove/uninstall, and troubleshooting. Load before installing a skill or wiring skills onto a workflow or step.", Modes: []string{"multi-agent", "workshop"}},
	"integration-discovery": {Group: "system", Description: "Connect a third-party service using CLI, skills or MCP. Honor explicit MCP requests. Search our catalog, GitHub and the official MCP Registry; fall back to internet search when no suitable server is found. Verify provider documentation, prefer direct endpoints, explain intermediaries before authorization, and confirm discovery after installation. Load whenever a user asks to integrate or connect a service/tool.", Modes: []string{"multi-agent", "workshop"}, Tools: []string{"search_mcp_catalog", "search_skills", "install_skill", "install_mcp_server", "add_mcp_server"}},

	// Multi-agent and Workshop reference docs for secret management. Run can
	// inspect available secret names but cannot create, replace, or delete them.
	"slack-bot-routing": {Group: "system", Tools: []string{"manage_slack_account"}, Description: "Manage personal Slack accounts and scoped Slack bot grants, product routes, deterministic listeners, and scripted cross-step thread replies without exposing credentials", Modes: []string{"workshop", "run"}},
	"webhook-triggers":  {Group: "system", Description: "Create, authenticate and test inbound route webhooks with manage_workflow_webhook; configure raw-payload mappings for groups, branches and standalone steps; inspect accepted runs and distinguish internal testing from public delivery; how a step reads its trigger input when one workflow is started by a provider webhook, an MCP/bearer call and a Crew or Code function call (delivery shapes, read order, reporting why a trigger could not be read)", Modes: []string{"workshop"}},
	"user-management":   {Group: "system", Description: "Builder user administration and workflow ownership/sharing: inspect permissions, resolve shared KB audience mismatches, owner/admin enforcement", Modes: []string{"workshop"}},
	"secret-management": {Group: "system", Description: "Manage workflow / global secrets via list_secrets, set_workflow_secret, delete_workflow_secret — buckets, naming rules, attach-after-store discipline", Modes: []string{"multi-agent", "workshop"}},

	// Cross-mode operational reference docs (browser and code-execution bridge).
	// Currently duplicated in the always-on system-prompt sections;
	// adding them as skills lets the agent load deep details on-demand and
	// sets up the eventual prompt-trim.
	"html-output":             {Group: "system", Description: "HTML output usage: document and asset paths, browser dependencies, live report APIs, theme compatibility and validation. Follow user design preferences. Load before writing any .html output file.", Modes: []string{"multi-agent", "workshop"}},
	"browser-usage":           {Group: "system", Description: "Interactive managed agent-browser guide; for repeatable tests read playwright-scripted (@agentworks/playwright). Covers deployment CDP capability/status, agent_browser HTTP API, CDP vs headless modes, macOS CDP installation when supported, snapshot/click/fill workflow, tab management, file uploads, session limits, common mistakes. Load before driving a browser, scraping pages, automating logins, or uploading files via a web form.", Modes: []string{"multi-agent", "workshop", "run"}, Tools: []string{"agent_browser"}},
	"agent-inbox":             {Group: "system", Description: "Crew inboxes: receiving on by default on servers, select the owner’s Gmail in your project's Vault panel or in chat, read Crew-addressed mail in the right panel, or add a Python Watcher to call existing functions. Server only; other personal mail remains private.", Modes: []string{"workshop"}, Tools: []string{"get_agent_inbox", "configure_agent_inbox", "manage_google_account", "manage_condition_triggers"}},
	"gmail-connection-scopes": {Group: "system", Description: "Managing a Gmail/Google Workspace connection's permissions: checking current state with list_gmail_connections (stored request vs what Google actually granted, and the exact diagnosis when they've drifted — including the Google Cloud Console consent-screen-scope-registration gotcha), how to widen or narrow scope from chat with update_gmail_connection_grants, why a reconnect link is required afterward, and how a workflow's own service allowlist relates to the connection's. Load before changing, explaining, or troubleshooting a Gmail connection's Drive/Sheets/Docs/Slides/Calendar, mailbox-read, or agent draft/send/reply access.", Modes: []string{"multi-agent", "workshop"}, Tools: []string{"manage_google_account", "list_gmail_connections", "update_gmail_connection_grants"}},
	"mcp-bridge":              {Group: "system", Description: "MCP HTTP bridge mechanics: $MCP_API_URL / $MCP_API_TOKEN env vars, curl pattern for calling MCP tools, response envelope, $VAR_* / $SECRET_* variable rules, single-call discipline. Load before writing scripts that call MCP tools via the bridge, or when debugging bridge errors.", Modes: []string{"multi-agent", "workshop", "run"}},
	"secure-share-links":      {Group: "system", Description: "Create authenticated AgentWorks links for a workflow report with dashboard action=link or for existing files and folders with get_file_link, including scripted-step bridge usage, path rules, validation, and the difference between access-controlled sharing and public publishing. Load when a user asks to share, link, or expose an existing workflow artifact without publishing it.", Modes: []string{"multi-agent", "workshop", "run"}, Tools: []string{"get_file_link", "get_report_link"}},
	"workflow-tools":          {Group: "system", Description: "Full reference for workshop / workflow tools: step execution & inspection (execute_step, query_step, debug_step, run_full_workflow), step config and read-only review tools, plan modification (add/update/delete step tools, agent routes, versioning), Goal Advisor proposal workflow, variables & MCP server config, shell, skills, and secrets. Schedule management now lives in its own \"schedules\" reference doc. Load when you need a tool's exact signature, parameters, or when-to-use rules and the inline cheat sheet doesn't suffice.", Modes: []string{"workshop", "run"}},
	"schedules":               {Group: "system", Description: "Full schedule management reference: list/create/update/delete/trigger_schedule and get_schedule_runs; cron vs calendar schedules; collision queues; after_schedule_ids chains and multi-schedule fan-in with status, delay, and deadline controls; mandatory per-schedule AgentWorks policy; backup coverage; and unattended route/message design. Load before creating, editing, or reasoning about any schedule.", Modes: []string{"workshop"}},
	"workspace-media-tools":   {Group: "system", Description: "Active workspace LLM tools: generate_text_llm, plus provider-auth discipline. Provider media tools are deprecated and hidden from agents while text testing is the focus.", Modes: []string{"multi-agent", "workshop", "run"}, Tools: []string{"generate_text_llm"}},
	"execution-policy":        {Group: "system", Description: "Per-group sequential execution policy for run_full_workflow on multi-group workflows: why per-group by default (cleaner failure signal, fixes propagate forward, avoids resource contention, earlier abort, correct iteration rotation), the recipe pattern, exceptions where parallel is appropriate, and how to handle ambiguous 'run the workflow' requests. Load before kicking off a multi-group run or when the user asks about parallel/sequential execution.", Modes: []string{"workshop", "run"}},
	"deployed-channel":        {Group: "system", Description: "Deployed channel runtime: handling Slack/WhatsApp/bot-channel-routed workflow requests — group identification from message, runtime context grounding (soul.md/learnings/KB/db), direct answer vs run_full_workflow vs execute_step decision, channel-context plumbing through human_inputs, in-channel result summarization, and Run-vs-Workshop boundary rules. Load when a chat or message arrives via a deployed channel route.", Modes: []string{"workshop", "run"}},
	"runtime-context":         {Group: "system", Description: "Ground direct runtime work in workflow learnings, business context, DB contracts, current results and costs without crossing Run-mode edit boundaries.", Modes: []string{"workshop", "run"}},
	"human-in-the-loop":       {Group: "system", Description: "Choose how a workflow gets human input: chat, planned checkpoints, execution-only urgent response cards, durable AgentWorks decisions, report-owned approvals, report-to-chat requests, and live execution corrections. Load when designing or handling human review, approval, feedback, or a human-in-the-loop handoff; distinguish saved answers, queued requests, and applied work.", Modes: []string{"workshop", "run"}, Tools: []string{"human_feedback", "create_human_input_request", "answer_human_input_request", "get_human_input_request", "mark_human_input_consumed", "send_step_message"}},
	"motion-guide":            {Group: "system", Description: "On-demand dashboard motion usage: designer-controlled interactive playback with window.report.motion, real historical data, refresh, and optional snapshot MP4 through the core dashboard export operation.", Modes: []string{"workshop"}, Tools: []string{"export_dashboard_motion"}, PanelSkill: "panel-dashboard", PanelReference: "references/dashboard/references/motion-guide.md"},
	"reporting-policy":        {Group: "system", Description: "Dashboard contract: hosted React TSX with shared shadcn/Recharts components, live project data and actions, validation/preview/publication, shared Run chat widgets, and HTML pages for quick sharing. Read dashboard-authoring for the current component/API procedure. Load for reports, dashboards, actions, themes, or layouts.", Modes: []string{"workshop", "run"}},
	"running-steps":           {Group: "system", Description: "Step execution mechanics: iterations & groups (always iteration-0 in workshop builder, read variables.json for group names), the 6-step execution procedure (determine group → execute_step → handle human_input → wait/notification → success/failure handling → always follow up), auto-notification system (no polling, system-generated [AUTO-NOTIFICATION] prefix, may be delayed), and stopping tasks (stop_all_executions / stop_step are required, text alone does NOT stop). Load before calling execute_step / run_full_workflow or when a user asks how to stop/cancel.", Modes: []string{"workshop", "run"}},
	"planning-steps":          {Group: "system", Description: "Workshop plan composition: take-action-by-default discipline, one large agent per shared-context span with proof/double-check/repair turns, intelligent separation when contexts should not be shared, scripted deterministic boundaries, required validation_schema, forward-only context flow, step types, fixed-branch routing, and deeper references. Load before adding/editing plan steps.", Modes: []string{"workshop"}},
	"workshop-mode-flow":      {Group: "system", Description: "Build or improve the workflow through its normal typed tools. Keep useful work small and verify actual results.", Modes: []string{"workshop"}},
	"debugging-flow":          {Group: "system", Description: "Debugging failed/stuck workflow steps: read-only investigation, AgentWorks Bug Review/Fixer, bounded manual fixes, run-mode inspection, root-cause mapping, and retry versus design-change decisions. Load when a step fails or behaves unexpectedly.", Modes: []string{"multi-agent", "workshop", "run"}},
	"publish-strategy":        {Group: "system", Description: "Publish workflow report dashboards to a public URL on any static host — the share-twin of backup. Provider-agnostic + agentic (no per-provider code): three universal deploy paths (provider CLI like netlify/vercel/wrangler/gh-pages/surge/firebase; git-push-to-deploy; object-store/rclone/rsync sync), auth from a named secret. Includes export_dashboard_static (same TSX/HTML page, shared runtimes and captured authorized data, no SQL extraction or raw DB), existing publishing visibility, the configure->verify->auto flow, workflow publish/status.json. Load when the user asks to publish/share/host a workflow report, or to set up a publish destination.", Modes: []string{"multi-agent", "workshop"}},
	"backup-strategy":         {Group: "system", Description: "Workflow and org backup playbook: when to commit to git vs push to a large-file backend, what never to back up (secrets, transient state), workflow status in backup/status.json, org config/status in pulse/backup.json + pulse/backup/status.json, git commit/pull/push discipline (atomic commits, --force-with-lease, JSON merge handling, hook bypass policy), and a comparison of large-file backends — HuggingFace Hub, Cloudflare R2, Backblaze B2, AWS S3, Google Cloud Storage, Azure Blob, and rclone. Includes CLI commands, auth env-var convention, and a decision matrix by content type. Load when the user asks about backup/versioning, how to push a workflow folder, organization backup, or setting up a storage destination.", Modes: []string{"multi-agent", "workshop"}},
}

// tmplData is the typed context passed to every guidance template. Focus is
// the conversation-derived instruction/context for this command. New fields
// require updating the markdown templates that consume them.
type tmplData struct {
	Focus        string
	Iteration    string
	RunFolder    string
	WorkshopMode string
}

const pathDisciplineGuidance = `PATH DISCIPLINE
Use absolute workspace paths for shell commands when the prompt or env exposes them (` + "`AbsWorkspacePath`" + `, ` + "`VAR_WORKSPACE_PATH`" + `, ` + "`STEP_OUTPUT_DIR`" + `, ` + "`STEP_EXECUTION_DIR`" + `). For file tools that expect workspace paths, use workflow-root-qualified paths. If the current workflow path is ` + "`Workflow/<name>`" + `, read ` + "`Workflow/<name>/runs/...`" + `, ` + "`Workflow/<name>/planning/...`" + `, and ` + "`Workflow/<name>/db/...`" + ` rather than bare ` + "`runs/...`" + `, ` + "`planning/...`" + `, or ` + "`db/...`" + `. Bare examples in this guidance are shorthand for paths under the current workflow root. Do not use host paths outside workspace-docs.

`

// renderKind loads templates/<group>/<kind>.md, renders it with the supplied
// params, and returns the rendered text. Returns an error if the kind isn't
// known or its template is malformed.
func renderKind(kind string, data tmplData) (string, error) {
	return renderFromRegistry(kind, data, allKinds)
}

// renderReferenceKind is the same as renderKind but resolves against the
// reference-doc registry (templates/system/*.md). Both registries share
// rendering internals so behavior stays consistent (path discipline header,
// template params, trailing newline handling).
func renderReferenceKind(kind string, data tmplData) (string, error) {
	return renderFromRegistry(kind, data, referenceKinds)
}

// renderFromRegistry is the shared rendering core. It looks up `kind` in the
// supplied registry, reads templates/<group>/<kind>.md from the embedded FS,
// executes it as a Go template with the supplied data, and prepends the
// shared path-discipline preamble.
func renderFromRegistry(kind string, data tmplData, registry map[string]kindMeta) (string, error) {
	meta, ok := registry[kind]
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}
	rel := path.Join("templates", meta.Group, kind+".md")
	body, err := readKindTemplate(meta, rel)
	if err != nil {
		return "", fmt.Errorf("read template %s: %w", rel, err)
	}
	if meta.RawTemplate {
		return pathDisciplineGuidance + string(body), nil
	}
	tmpl, err := template.New(kind).Parse(string(body))
	if err != nil {
		return "", fmt.Errorf("parse template %s: %w", rel, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %s: %w", rel, err)
	}
	return pathDisciplineGuidance + strings.TrimRight(buf.String(), "\n") + "\n", nil
}

// modeAllowed reports whether a kind can be invoked from a given workshop
// mode. The caller passes their current mode (builder / optimizer / workshop
// / run / reporting); the kind's allow-list is checked.
func modeAllowed(kind, mode string) bool {
	return modeAllowedIn(kind, mode, allKinds)
}

// modeAllowedIn is the registry-parameterized form of modeAllowed. Used by
// both the procedural-guidance tool (allKinds) and the reference-doc tool
// (referenceKinds).
func modeAllowedIn(kind, mode string, registry map[string]kindMeta) bool {
	meta, ok := registry[kind]
	if !ok {
		return false
	}
	for _, m := range meta.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// kindEnum returns sorted kind names — used to populate the tool schema's
// enum and for diagnostic error messages.
func kindEnum() []string {
	return kindEnumFrom(allKinds)
}

// kindEnumFrom returns sorted kind names for any registry.
func kindEnumFrom(registry map[string]kindMeta) []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// kindEnumWithDescriptions formats the kind list for the tool description so
// the agent can see, in one place, every guided flow available to it.
func kindEnumWithDescriptions() string {
	return kindEnumWithDescriptionsFrom(allKinds)
}

// kindEnumWithDescriptionsFrom formats the kind list for any registry.
func kindEnumWithDescriptionsFrom(registry map[string]kindMeta) string {
	type row struct {
		k     string
		d     string
		modes []string
	}
	rows := make([]row, 0, len(registry))
	for k, v := range registry {
		rows = append(rows, row{k: k, d: v.Description, modes: v.Modes})
	}
	sort.Slice(rows, func(i, j int) bool {
		gi, gj := registry[rows[i].k].Group, registry[rows[j].k].Group
		if gi != gj {
			return gi < gj
		}
		return rows[i].k < rows[j].k
	})
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s — %s [modes: %s]\n", r.k, r.d, strings.Join(r.modes, ", "))
	}
	return b.String()
}

// RegisterGuidanceTool exposes get_workflow_command_guidance to the agent.
// The tool returns the rendered prompt for any kind in allKinds. Mode is
// validated against the kind's allow-list — calling a kind from the wrong
// mode returns an error message instructing the agent to suggest a mode
// switch.
type DefinitionToolRegistrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}

func RegisterGuidanceTool(agent DefinitionToolRegistrar, currentMode string, logger loggerv2.Logger) {
	desc := "Get the canonical guided-flow text for any workflow command. " +
		"Call this tool — and follow the returned instructions verbatim — when (1) the user invokes a slash command " +
		"like /design-plan or /setup-goals. A command dispatch can supply the exact " +
		"" +
		"kind and focus; when the dispatch message explicitly states kind=... and focus=... " +
		"use those exact values. Pass the " +
		"surrounding conversation/request into focus when available, (2) the user describes " +
		"the same intent in plain chat (\"help me improve this workflow\", \"review whether the goal is being met\", " +
		"\"improve the measurement step\") — recognize the intent and pick the matching kind, or (3) you're running on a " +
		"schedule and the message names a kind. The returned text is your instructions for this turn — do not paraphrase " +
		"or skip steps. Available kinds:\n" + kindEnumWithDescriptions() +
		"Mode validation: each kind is gated to specific workshop modes. If the user's request matches a kind not allowed " +
		"in the current mode, tell the user the mode they need to switch to instead of calling the tool."

	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"kind": map[string]interface{}{
				"type":        "string",
				"enum":        kindEnum(),
				"description": "The guided-flow to render. See the tool description for the full list of kinds and their per-mode availability.",
			},
			"focus": map[string]interface{}{
				"type":        "string",
				"description": "Optional but strongly recommended. The conversation-derived instruction/context for this command: include the user's recent request, constraints, examples, or focus area that led to the slash command. This is how a slash command carries 'based on the conversation that just happened' into the canonical guidance.",
			},
			"iteration": map[string]interface{}{
				"type":        "string",
				"description": "Optional. Run iteration to use as evidence (e.g. \"iteration-3\"). When set, templates that take an iteration use it as the starting evidence set.",
			},
			"run_folder": map[string]interface{}{
				"type":        "string",
				"description": "Optional. Full run folder path (e.g. \"iteration-3/group-a\"). Used by ops-review-style flows that anchor on a specific run.",
			},
		},
		"required": []string{"kind"},
	}

	handler := func(ctx context.Context, args map[string]interface{}) (string, error) {
		kind, _ := args["kind"].(string)
		focus, _ := args["focus"].(string)
		iteration, _ := args["iteration"].(string)
		runFolder, _ := args["run_folder"].(string)

		if _, ok := allKinds[kind]; !ok {
			return fmt.Sprintf("error: unknown kind %q. Valid kinds: %s", kind, strings.Join(kindEnum(), ", ")), nil
		}
		if currentMode != "" && !modeAllowed(kind, currentMode) {
			meta := allKinds[kind]
			return fmt.Sprintf(
				"error: kind %q is not available in mode %q. It runs in: %s. Tell the user they need to switch workshop mode before this command can run.",
				kind, currentMode, strings.Join(meta.Modes, ", "),
			), nil
		}

		text, err := renderKind(kind, tmplData{
			Focus:        strings.TrimSpace(focus),
			Iteration:    strings.TrimSpace(iteration),
			RunFolder:    strings.TrimSpace(runFolder),
			WorkshopMode: strings.TrimSpace(currentMode),
		})
		if err != nil {
			return fmt.Sprintf("error rendering guidance for %q: %v", kind, err), nil
		}
		// Wrap the rendered guidance in a JSON envelope so the agent sees a
		// stable shape; the actual prose is the `guidance` field.
		envelope, _ := json.MarshalIndent(map[string]interface{}{
			"kind":     kind,
			"guidance": text,
		}, "", "  ")
		return string(envelope), nil
	}

	if err := agent.RegisterCustomTool("get_workflow_command_guidance", desc, params, handler, "auto_improvement"); err != nil {
		if logger != nil {
			logger.Warn(fmt.Sprintf("Failed to register get_workflow_command_guidance: %v", err))
		}
	}
}

// ReferenceKindNames returns the sorted list of reference-doc kinds known
// to this package. Exported for cross-package tests that need to enumerate
// every doc without depending on the private registry.
func ReferenceKindNames() []string {
	return kindEnumFrom(referenceKinds)
}

// BuildSystemToolsSkill returns a single small "meta" skill whose body
// teaches the agent the system-tool surface available in this session:
// the MCP bridge, get_api_spec for tool discovery, read_skill for deeper
// system docs, and get_workflow_command_guidance for
// procedural flows. The skill enumerates the reference-doc kinds that
// are allowed in the given mode so the agent knows which bundled files it can
// read.
//
// Why a meta-skill rather than one skill per reference doc: copying
// every reference-doc body into a skill folder per session duplicates
// content and risks drift. Instead this small skill points at the
// attached bundle so the agent loads detail on demand through mcpagent.
//
// An empty mode returns nil (no skill to attach).
func BuildSystemToolsSkill(mode string) *llmtypes.Skill {
	return buildSystemToolsSkillWithMCP(mode, true)
}

func buildSystemToolsSkillWithMCP(mode string, mcpManagement bool) *llmtypes.Skill {
	if strings.TrimSpace(mode) == "" {
		return nil
	}

	// The per-kind catalog (name + description) is deliberately NOT inlined
	// here: the builder-reference mega-skill's TOC
	// already lists every mode-allowed kind, and duplicating the registry in
	// two attached skills costs prompt tokens in every session. This skill
	// just points at that catalog.
	hasKinds := false
	for _, kind := range kindEnumFrom(referenceKinds) {
		if modeAllowedIn(kind, mode, referenceKinds) {
			hasKinds = true
			break
		}
	}
	kindList := "(no reference docs are available in this mode)\n"
	if hasKinds {
		refSkillName := referenceSkillSpecForMode(mode).Name
		kindList = "The full catalog is the `references/` list in the `" + refSkillName + "` skill. Read a topic with `read_skill(skills=[{\"name\":\"" + refSkillName + "\",\"path\":\"references/<kind>.md\"}])`.\n"
	}

	configAccess := buildConfigurationAccessGuidance(mode)
	referenceExamples := "`code-authoring` before authoring `main.py`, `llm-selection` before changing workflow models, `integration-discovery` before connecting a new third-party service/MCP server, `browser-usage` before driving a browser or troubleshooting CDP, or `gmail-connection-scopes` before touching a Gmail/Workspace connection's permissions"
	if !mcpManagement {
		referenceExamples = strings.ReplaceAll(referenceExamples, "`integration-discovery` before connecting a new third-party service/MCP server, ", "")
	}
	proceduralGuidance := "- `get_workflow_command_guidance(kind, focus?)` — canonical procedural flows (design-plan, run-plan-drift, setup-goals, etc.). The returned text is your instructions for that turn; follow it verbatim.\n"
	if strings.EqualFold(strings.TrimSpace(mode), "run") {
		referenceExamples = "`runtime-context` before answering from workflow state, `running-steps` before execution, or `human-in-the-loop` when work needs a user decision"
		proceduralGuidance = ""
	}

	body := `This skill is a quick guide to the system tools available in this session. Use it as your map for discovery and deep documentation.

## Tool / API discovery

- ` + "`search_tools(query)` and `get_api_spec(tool_name)`" + ` — when you do not know an MCP tool's parameters or response shape, call this first.
- ` + "`read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/<kind>.md\"}])`" + ` — load system reference docs before deep actions (for example ` + referenceExamples + `). This intrinsic mcpagent tool works on API and coding-CLI transports; native CLI skill files are the same bundle.
` + proceduralGuidance + `
## Configuration access

` + configAccess + `

### Reference doc kinds available in this mode

` + kindList + `
## MCP bridge — only in code-execution mode

When you are running scripts via ` + "`execute_shell_command`" + ` (code-execution mode), call MCP tools through HTTP:

` + "```bash" + `
payload='{"arg":"value"}'
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_MCP/{server_name}/{tool_name}"
` + "```" + `

` + "`$MCP_AUTH`" + ` is already the complete ` + "`Authorization: Bearer ...`" + ` header. Never prepend another header or Bearer prefix. ` + "`--json`" + ` already selects POST and Content-Type, so do not add ` + "`-X POST`" + `, another Content-Type header, or ` + "`--data`" + `. Keep the call unpiped so curl's nonzero HTTP-failure status reaches ` + "`execute_shell_command`" + `.

Use ` + "`$MCP_MCP`" + ` only for real MCP servers from the workflow's selected server list. Built-in/custom categories such as ` + "`human_tools`" + `, ` + "`workflow`" + `, ` + "`workspace_advanced`" + `, ` + "`auto_improvement`" + `, and ` + "`knowledgebase_tools`" + ` are not MCP servers; call them as ` + "`$MCP_CUSTOM/{tool_name}`" + ` with no category segment. Example: ` + "`$MCP_CUSTOM/send_email`" + `, never ` + "`$MCP_MCP/human_tools/send_email`" + `.

Pre-set environment for scripts:
- ` + "`$MCP_MCP`" + `, ` + "`$MCP_CUSTOM`" + `, ` + "`$MCP_VIRTUAL`" + ` — short bridge endpoint bases
- ` + "`$MCP_AUTH`" + ` — Authorization header value for ` + "`curl -H`" + `
- ` + "`$MCP_API_URL`" + ` + ` + "`$MCP_API_TOKEN`" + ` — full endpoint + token fallback
- ` + "`$STEP_OUTPUT_DIR`" + `, ` + "`$STEP_EXECUTION_DIR`" + ` — write outputs here
- ` + "`$VAR_<NAME>`" + ` — workflow config (e.g. ` + "`$VAR_USER_ID`" + `); reference, never hardcode
- ` + "`$SECRET_<NAME>`" + ` — credentials; never echo to stdout, never write to files

In non-code-execution mode you call tools directly via the LLM tool-call API; the bridge curl pattern is not needed.

## When in doubt

Call the right discovery tool above before guessing. Hallucinated tool names or parameter shapes will fail at the bridge; reading the spec or the reference doc is cheap.
`

	return &llmtypes.Skill{
		Name:        "system-tools",
		Description: "How to use the MCP bridge, tool discovery (get_api_spec), attached references (read_skill), and workflow command guidance in this session.",
		Content:     body,
		Source:      llmtypes.SkillSource{Origin: "builtin"},
	}
}

func buildConfigurationAccessGuidance(mode string) string {
	var parts []string

	// The prohibition is a guardrail and holds in every mode. The tool
	// recommendation must not: `llm-provider-config`'s registry entry decides
	// which modes manage provider configuration, and only those modes register
	// list_published_llms/list_provider_models/test_llm/save_published_llm/
	// set_provider_auth. Naming them unconditionally told `run`-mode sessions —
	// every scheduled workflow run — to call tools that
	// mode never registers, producing `tools_unavailable` and sending agents off
	// to guess provider names instead.
	parts = append(parts, "LLM/provider configuration is tool-managed. Do not read or edit `config/` files with shell or file tools.")
	if modeAllowedIn("llm-provider-config", mode, referenceKinds) {
		parts = append(parts, "For published chat models and provider auth, use `list_published_llms`, `list_provider_models`, `test_llm`, `save_published_llm`, and `set_provider_auth` as appropriate; load `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-provider-config.md\"}])` before publishing or changing provider auth.")
	}

	if modeAllowedIn("llm-selection", mode, referenceKinds) {
		parts = append(parts, "For workflow execution tiers and per-step model choices, use `get_llm_config` and `set_workflow_llm_config`; load `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/llm-selection.md\"}])` before changing workflow execution models.")
	}
	if modeAllowedIn("workspace-media-tools", mode, referenceKinds) {
		parts = append(parts, "For media/search provider tools, load `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/workspace-media-tools.md\"}])`.")
	}
	if modeAllowedIn("secure-share-links", mode, referenceKinds) {
		parts = append(parts, "For an authenticated link to the workflow dashboard use `dashboard action=link`; for existing workflow files or folders use `get_file_link`. Load `read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/secure-share-links.md\"}])`; these are access-controlled links, not public publishing.")
	}

	return strings.Join(parts, " ")
}

// RenderSystemDoc renders the named reference doc with no caller context,
// stripping the path-discipline preamble. Intended for production code that
// needs system-doc content inline (for example deterministic server-side
// assembly that is not itself an agent turn). Agent-facing access uses the
// attached builder-reference skill through mcpagent's read_skill tool.
//
// Panics on error because the embedded FS is compile-time — if a kind is
// declared in referenceKinds but its .md file is missing or malformed, that
// is a build-time bug, not a runtime condition.
func RenderSystemDoc(kind string) string {
	meta, ok := referenceKinds[kind]
	if !ok {
		panic(fmt.Sprintf("guidance: RenderSystemDoc called with unknown kind %q", kind))
	}
	rel := path.Join("templates", meta.Group, kind+".md")
	body, err := readKindTemplate(meta, rel)
	if err != nil {
		panic(fmt.Sprintf("guidance: read %s: %v", rel, err))
	}
	if meta.RawTemplate {
		return string(body)
	}
	tmpl, err := template.New(kind).Parse(string(body))
	if err != nil {
		panic(fmt.Sprintf("guidance: parse %s: %v", rel, err))
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, tmplData{}); err != nil {
		panic(fmt.Sprintf("guidance: execute %s: %v", rel, err))
	}
	return strings.TrimRight(buf.String(), "\n") + "\n"
}

// RenderReferenceKindForTest renders the named reference doc with empty
// caller context. Exported so step_based_workflow's prompt size/coverage
// tests can verify every kind is renderable and reasonably sized without
// depending on internals.
func RenderReferenceKindForTest(kind, mode string) (string, error) {
	return renderReferenceKind(kind, tmplData{WorkshopMode: mode})
}

// ListReferenceKindsForTest is an alias for ReferenceKindNames kept for
// test ergonomics ("ForTest" suffix signals "use from tests only").
func ListReferenceKindsForTest() []string {
	return ReferenceKindNames()
}

// ExternalTopics selects only source files explicitly marked for MCP exposure.
func ExternalTopics() ([]externalguidance.Topic, error) {
	topics, err := externalguidance.Load(templatesFS, "templates/system/*.md")
	if err != nil {
		return nil, err
	}
	for _, meta := range referenceKinds {
		if meta.PanelSkill == "" {
			continue
		}
		raw, err := skills.WorkspacePanelReference(meta.PanelSkill, meta.PanelReference)
		if err != nil {
			return nil, err
		}
		topic, content, err := externalguidance.Parse(raw)
		if err != nil {
			return nil, err
		}
		if topic.External {
			topic.Content = content
			topics = append(topics, topic)
		}
	}
	return topics, nil
}

func readTemplate(path string) ([]byte, error) {
	raw, err := templatesFS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_, body, err := externalguidance.Parse(raw)
	return []byte(body), err
}

func readKindTemplate(meta kindMeta, relative string) ([]byte, error) {
	if meta.PanelSkill == "" {
		return readTemplate(relative)
	}
	raw, err := skills.WorkspacePanelReference(meta.PanelSkill, meta.PanelReference)
	if err != nil {
		return nil, err
	}
	_, body, err := externalguidance.Parse(raw)
	return []byte(body), err
}
