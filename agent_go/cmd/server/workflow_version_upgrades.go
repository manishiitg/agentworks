package server

import (
	"fmt"
	"strconv"
	"strings"
)

type workflowVersionUpgrade struct {
	from  string
	to    string
	label string
	query string
}

func workflowContractVersionForUpgrade(manifest *WorkflowManifest) string {
	if manifest == nil {
		return workflowContractInitialVersion
	}
	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		return workflowContractInitialVersion
	}
	return version
}

// Only the current contract is execution-compatible. Historical 1.0.41-1.0.43
// workflows owe the nested-agent-artifact migration, 1.0.44 workflows the
// managed database script migration, and 1.0.45 workflows the step description
// layout migration.
func workflowContractVersionIsExecutionCompatible(version string) bool {
	return strings.TrimSpace(version) == workflowContractUnifiedAgentVersion
}

// goalsOnlyWorkflowUpgrades are migrations about the goal-driven product:
// schedules, Pulse, reports, notifications and run summaries. A Relay shares the
// workflow runtime (steps, scripts, code layout) and therefore every other
// migration, except its absent stores below (PLAT-431, PLAT-441). New ladder
// entries apply to both products by default; these are explicit exceptions.
var goalsOnlyWorkflowUpgrades = map[string]bool{
	"upgrade-notification-config":            true,
	"upgrade-current-artifact-contract":      true,
	"upgrade-direct-html-reports":            true,
	"upgrade-schedule-execution-model":       true,
	"upgrade-dedicated-pulse-schedule":       true,
	"upgrade-schedule-prompt-contract":       true,
	"upgrade-schedule-finalizer-ownership":   true,
	"upgrade-report-activity-section":        true,
	"upgrade-report-activity-tab":            true,
	"upgrade-pulse-lifecycle-reconciliation": true,
	"upgrade-pulse-backlog-triage":           true,
	"upgrade-pulse-actionable-backlog":       true,
	"upgrade-activity-tab-from-run-summary":  true,
	"upgrade-route-summaries":                true,
	"upgrade-explicit-schedule-pulse":        true,
	"upgrade-eval-verdict-schema":            true,
}

// relayStoreUpgrades migrate the workflow database, knowledge base or learnings.
// A Relay has none of those stores, so it never owes them.
var relayStoreUpgrades = map[string]bool{
	"upgrade-kb-write-method":    true,
	"upgrade-managed-db-scripts": true,
}

func manifestIsRelay(manifest *WorkflowManifest) bool {
	return manifest != nil && manifest.Kind == "relay"
}

// manifestContractIsExecutionCompatible reports whether a manifest's contract
// version lets it run. A Goals workflow must be on the current version. A Relay
// must be on a version this server knows with no shared migration pending: a
// Goals-only migration it skipped never blocks it. Callers still require
// code_layout_version 1.
func manifestContractIsExecutionCompatible(manifest *WorkflowManifest) bool {
	if isPythonRelay(manifest) {
		return true
	}
	version := workflowContractVersionForUpgrade(manifest)
	if workflowContractVersionIsExecutionCompatible(version) {
		return true
	}
	if !manifestIsRelay(manifest) {
		return false
	}
	if _, known := workflowContractVersionRank(version); !known {
		return false
	}
	return len(workflowVersionUpgradePlan(manifest)) == 0
}

// workflowContractVersionRank is intentionally a closed set. A workflow made
// by a newer server must never be silently "upgraded" backwards by an older
// server: manualWorkflowUpgradeTurns treats an unknown version as having no path.
func workflowContractVersionRank(version string) (int, bool) {
	known := []string{
		"1.0.0", "1.0.1", "1.0.2", "1.0.3", "1.0.4", "1.0.5", "1.0.6", "1.0.7", "1.0.8", "1.0.9",
		workflowContractAgentCodeVersion,
		workflowContractPulseHistoryVersion,
		workflowContractNotificationConfigVersion,
		workflowContractHumanInputOwnershipVersion,
		workflowContractHumanReadablePulseStateVersion,
		workflowContractKBWriteMethodRetiredVersion,
		workflowContractEvalVerdictSchemaVersion,
		workflowContractCompactPulseReportVersion,
		workflowContractLightweightPulseReportVersion,
		workflowContractExecutivePulseJournalVersion,
		workflowContractArtifactPurityVersion,
		workflowContractLegacy122Version,
		workflowContractDirectHTMLReportsVersion,
		workflowContractScheduledRouteVersion,
		workflowContractScheduleExecutionModelVersion,
		workflowContractPeriodicPulseReviewVersion,
		workflowContractDedicatedPulseScheduleVersion,
		workflowContractSchedulePromptContractVersion,
		workflowContractFinalizerOwnedScheduleVersion,
		workflowContractReportActivitySectionVersion,
		workflowContractReportActivityTabVersion,
		workflowContractPulseLifecycleReconciliationVersion,
		workflowContractPulseBacklogTriageVersion,
		workflowContractPulseActionableBacklogVersion,
		workflowContractOrchestratorStepTypeVersion,
		workflowContractActivityTabFromRunSummaryVersion,
		workflowContractScriptedTypeStaysRegularVersion,
		workflowContractDeclaredExecutionModeRetiredVersion,
		workflowContractDeclaredExecutionModeStrippedVersion,
		workflowContractRouteSummariesVersion,
		workflowContractExplicitSchedulePulseVersion,
		workflowContractRunScopedRoutesVersion,
		workflowContractEvalRetirementVersion,
		workflowContractNestedAgentArtifactsVersion,
		workflowContractManagedDBScriptsVersion,
		workflowContractStepDescriptionLayoutVersion,
		workflowContractUnifiedAgentVersion,
	}
	for rank, candidate := range known {
		if version == candidate {
			return rank, true
		}
	}
	return 0, false
}

const upgradeAgentCode = `WORKFLOW CONTRACT UPGRADE: MESSAGE-SEQUENCE CODE.

This workflow predates the current agent contract. Do only this migration.
Call migrate_agent_code_items. It is the trusted tool that either converts unambiguous legacy code items into standalone scripted steps or reports a precise blocker. Then inspect the resulting plan and confirm no legacy agent code item remains. Do not run the workflow. If the migration is blocked or validation fails, do not stamp a version. Otherwise call set_workflow_contract_version(version="1.0.10") and stop.`

const upgradeNestedAgentArtifacts = `WORKFLOW CONTRACT UPGRADE: NESTED AGENT RUNTIME ARTIFACTS.

Do only this migration. Runtime artifacts now live exclusively beneath their owning Agent step:

runs/<run>/execution/<parent-step>/agents/<route-id>/session.json
runs/<run>/execution/<parent-step>/agents/<route-id>/calls/<call-id>/
runs/<run>/execution/<parent-step>/scripts/items/<item-id>/calls/<call-id>/
runs/<run>/execution/<parent-step>/scripts/routes/<route-id>/calls/<call-id>/

There is no runtime fallback to flattened step-*-sub-*, step-*-generic-*, execution/agents/, or scripts/<item-id>/<call-id>/ paths. Historical run folders are evidence: do not move, rewrite, or delete them.

Read workflow.json, planning/plan.json, planning/step_config.json, and every workflow-authored script or referenced instruction that names runtime paths. Replace hardcoded legacy execution paths with STEP_OUTPUT_DIR for the current invocation, STEP_EXECUTION_DIR or declared context dependencies for reads, and the nested layout only where an explicit relative artifact reference is unavoidable. Do not alter business behavior.

Every scripted step must use the canonical source code/<script-step-id>/main.py with code/<script-step-id>/ as its working directory. If workflow.json.code_layout_version is not 1, perform the deliberate code-layout migration: copy and verify every complete scripted bundle (main.py, helpers, and script_metadata.json) under code/<step-id>/, then switch with set_code_layout_version(code_layout_version=1). Do not leave a scripted step available only under learnings/.

Validate the changed plan/config and run focused script checks without executing the workflow's external side effects. Re-read every changed artifact. If a hardcoded legacy path is ambiguous or a scripted bundle cannot be migrated safely, report the exact blocker and do not stamp. Otherwise call set_workflow_contract_version(version="1.0.44") and stop.`

const upgradeNotificationConfig = `WORKFLOW CONTRACT UPGRADE: NOTIFICATION CONFIGURATION.

This workflow predates workflow-scoped notification configuration. Do only this migration. Read workflow.json and soul/soul.md. If soul contains an explicit user-approved Notifications preference, move only the mappable workflow-specific preference into workflow.json capabilities.notifications using update_workflow_config, then remove that duplicate preference from soul. Preserve account-wide delivery settings, unknown text, recipients, and constraints rather than guessing. If there is no legacy Notifications section, this is a no-op. Do not run the workflow. After re-reading the changed artifacts and confirming no notification meaning was lost, call set_workflow_contract_version(version="1.0.12") and stop.`

const upgradeKBWriteMethod = `WORKFLOW CONTRACT UPGRADE: RETIRED KNOWLEDGEBASE WRITE METHOD.

This workflow predates inline knowledgebase writes. Do only this migration. Inspect planning/step_config.json and remove the retired knowledgebase_write_method and learnings_write_method keys from every step configuration while preserving every other field. A KB-writing step still needs knowledgebase_access write/read-write plus a non-empty knowledgebase_contribution; do not invent contributions or change its access. Re-read and validate the complete config after the rewrite. Do not run the workflow. If any ambiguous legacy configuration cannot be safely preserved, report it and do not stamp. Otherwise call set_workflow_contract_version(version="1.0.15") and stop.`

const upgradeEvalVerdictSchema = `WORKFLOW CONTRACT UPGRADE: EVALUATION VERDICTS.

The evaluation subsystem this migration once normalized is retired, and no replacement evaluation migration is required. Do only this historical migration: verify planning/plan.json and workflow.json parse, change nothing, and call set_workflow_contract_version(version="1.0.16") and stop. Do not create, edit, or score any evaluation plan.`

const upgradeCurrentArtifactContract = `WORKFLOW CONTRACT UPGRADE: CURRENT ARTIFACT CONTRACT.

Pulse state is SQLite-backed and shown only in the Pulse popup. Do not create, read, update, publish, validate, or archive a separate Pulse HTML journal. builder/improve.html and builder/improve-archive/ are retired presentation surfaces, not a store of record. Typed Pulse state, soul, plan/config, reports, knowledgebase, learnings, runs, and database remain authoritative.

NOTHING IS DELETED IN THIS MIGRATION. Move builder/improve.html and builder/improve-archive/ into migration-backups/artifact-purity-<UTC timestamp>/, keeping their relative paths. Both files stay on disk, and in git history, at the new location. Read them back at the new path and confirm builder/ no longer holds them before you stamp; if the move cannot preserve a file, that IS a blocker — report it and do not stamp.

A previous turn on this workflow may have declined to advance this version. Read its recorded reason before deciding whether it still applies. The objection on record is to stamping the version while old finding history would be DESTROYED. A relocation does not destroy it: history that exists only in these files and not in the pulse_* tables survives this migration unchanged, at the new path. So do not migrate that history into SQLite, and do not treat its absence from SQLite as a reason to stop — verify the files are intact where you moved them, which is the thing that objection was actually protecting.

Then inspect workflow.json, planning/plan.json, planning/step_config.json, learnings/_global/SKILL.md and referenced learning Markdown, plus knowledgebase notes. Remove only shared AgentWorks transport, MCP bridge, Folder Guard, managed-tool, tool-discovery, or native-session mechanics from workflow-authored prose. Preserve domain-specific inputs, outputs, side effects, safety boundaries, acceptance criteria, selectors, external API behavior, and recovery knowledge. Use the normal typed plan/config tools for plan changes. Re-read every changed artifact and validate the plan/config. If a rewrite is ambiguous, do not stamp. Otherwise call set_workflow_contract_version(version="1.0.21") and stop.`

const upgradeDirectHTMLReports = `WORKFLOW CONTRACT UPGRADE: DIRECT HTML REPORT PAGES.

This workflow predates the direct HTML report-page contract. Migrate its report
documents agentically; do not replace them with generic placeholders, wrappers,
iframes, or an empty navigation shell.

First inspect reports/report_plan.json when it exists. In its existing section and
entry order, inventory every enabled HTML file widget and read every complete source
document, including old sources outside the report folder such as db/report.html.
Also inventory immediate db/reports/*.html files so useful report content
that was not registered in the old plan is preserved.

Compose one complete standalone report at db/reports/index.html. The platform does
not create report tabs or navigation: this HTML owns the entire user experience and
may choose tabs, a sidebar, anchored sections, expandable panels, or one scrolling
briefing according to the workflow's reporting needs. Preserve the old primary
dashboard, SQL, window.report calls, scripts, styles, media references, and all
useful user-visible content. Consolidate secondary documents as coherent sections
or views inside index.html rather than exposing their filenames as platform pages.
Historical setup/choice artifacts should be linked or placed in a clearly secondary
view, not presented as peers of the operational dashboard. Ensure index.html has a
meaningful <title>. Fix relative asset references only when consolidation changes
their resolution. Do not change what the report measures during this migration.

Validate db/reports/index.html with validate_report_html. Re-read it and confirm
every enabled old-plan document is represented, including the primary dashboard,
and that its internal navigation exposes every intended reporting section. Only
after that verification, delete the retired reports/report_plan.json and superseded
standalone report HTML files; retain their media assets. Do not delete any old source
until index.html has passed validation. Do not run the workflow. If a source is
missing or the consolidation is ambiguous, report the blocker and do not stamp.
Otherwise call set_workflow_contract_version(version="1.0.23") and stop.`

const upgradeDedicatedPulseSchedule = `WORKFLOW CONTRACT UPGRADE: POST-RUN PULSE ENABLEMENT.

Recurring Pulse is represented by workflow.json pulse.enabled. It has no
independent cron: after each normal scheduled workflow run, Pulse Gate decides
whether Review+Fix work is due for that run's evidence. Legacy post_run_monitor,
post_run_monitor_mode, and pulse_review_only schedules are obsolete.

Read the raw workflow.json before changing anything. Preserve prior enablement:
if post_run_monitor was true or an enabled pulse_review_only schedule exists,
set pulse.enabled=true. Otherwise leave Pulse disabled. Remove every
pulse_review_only schedule; do not change any normal workflow schedule.
Calling set_workflow_contract_version rewrites the manifest through the current
schema and removes the retired fields.

Do not run the workflow. Call set_workflow_contract_version(version="1.0.27") and stop.`

const upgradeSchedulePromptContract = `WORKFLOW CONTRACT UPGRADE: CURRENT SCHEDULE INSTRUCTIONS.

Do only this one-time schedule migration. It cleans recurring schedule messages;
it does not run, redesign, pause, delete, or create any schedule.

Read workflow.json and inspect every schedule before changing anything. Preserve
each schedule's ID, name, cron/calendar timing, timezone, enabled state, groups,
route selections, execution mode, and every domain safety boundary. Do not change
a route, step, public-action policy, or backup destination in this migration.

Remove historical implementation debris from normal workflow schedule messages
and direct_messages_reason: dated incidents, previous failure counts, old operator
decisions, ticket references, watchdog/idle-wait stories, implementation folder
or database probes, and old workaround narratives. A recurring prompt must state
the current contract, not narrate why an older platform version needed it.

Measurement ownership must remain correct:
1. If a schedule currently owns routine measurement (an evaluation pass or a
   measurement route), keep that behavior as one concise instruction
   immediately after the selected work completes. Do not turn it into a
   conditional probe/retry of an undocumented automatic pass.
2. If a higher-frequency schedule deliberately skips measurement because one
   designated daily/closing schedule owns it, preserve that division plainly.
3. Do not add measurement to a schedule that did not already own it, and do
   not remove routine measurement merely to shorten the message.

Keep a schedule message only when it carries genuinely schedule-specific work
that cannot be represented by its existing groups and route selections. Make it
one concise current-state instruction. A route-backed schedule whose message only
restates running the selected route should have no message. Do not turn ordinary
workflow behavior into a schedule-local procedure.

Do not weaken concrete backup behavior in this migration. Preserve the existing
configured backup contract; only remove historical dates or rationale that do
not change what must be backed up or how success is reported. Do not replace a
working concrete backup procedure with a promise that some other component will
handle it.

Use the schedule-management tools to make any required schedule updates. Re-read
workflow.json afterwards. Confirm every retained message is concise and current,
no retained schedule prose contains a historical incident date or workaround,
and normal workflow schedules do not perform Pulse Gate/Review/Fix inline. Do
not run the workflow. If any change would make measurement, backup, safety, or
a public action ambiguous, preserve that schedule unchanged, report the
blocker, and do not stamp. Otherwise call
set_workflow_contract_version(version="1.0.28") and stop.`

const upgradeScheduleFinalizerOwnership = `WORKFLOW CONTRACT UPGRADE: SCHEDULE ROUTE AND FINALIZER OWNERSHIP.

Do only this one-time schedule migration. Do not run, pause, delete, or create
any schedule.

The scheduler always executes a schedule's saved route selection before any
retained schedule message. For a route-backed schedule, route_selections own
what workflow work runs; messages are optional follow-ups only.

The platform owns normal run finalization: backup, execution-report publish,
run notification, and—when pulse.enabled is true—the post-run Pulse Gate,
Review, and Fixer. Remove a normal schedule message when it merely tells the
agent to do any of the following after selected work completes: routine
measurement, generic completion reporting, backup/status.json updates, Git
commit/push, report publishing, notification, or Pulse review/fixing. These
are platform lifecycle duties and must not be copied into schedule prose.

Preserve genuine schedule-specific work and its safety boundary. In particular,
do not delete a direct schedule message just because it mentions measurement or
backup if its primary purpose is a distinct time-bound procedure that cannot be
expressed by the selected route (for example a market-close-only operation).
Remove only the copied generic lifecycle tail, leaving the special procedure
concise and truthful. A route-backed schedule whose only message is that copied
lifecycle tail must end with messages empty and no direct_messages_reason.

Read every schedule, update only what this ownership rule requires, re-read
workflow.json, and confirm all saved route selections, groups, timing, enabled
states, public-action boundaries, and backup configuration are unchanged. Then
call set_workflow_contract_version(version="1.0.29") and stop.`

const upgradeScheduledRoutes = `WORKFLOW CONTRACT UPGRADE: SCHEDULE EXECUTION MODEL (PLAT-086).

Workflow schedules support two valid execution models. A route-based schedule
selects canonical plan work and therefore receives normal step learnings,
validation/retry, repair, and Pulse attribution. A direct agent is a
workshop conversation owned by the schedule; it is more flexible, but those
step-level lifecycle guarantees are not automatic. Audit every workflow.json
schedule and classify it from its actual behavior, not merely its text length.

Choose exactly one classification per schedule:

1. EQUIVALENT ROUTE EXISTS. Verify the same inputs, outputs, external side
   effects, approval boundary, failure behavior, and group semantics. Move any
   deterministic routing choice into route_selections and clear messages.
2. DURABLE WORKFLOW BEHAVIOR, NO ROUTE EXISTS. Create the missing canonical
   route/steps and their validation before changing the schedule. Never map a
   draft-only schedule to a route that can publish. Then set route_selections
   and clear messages.
3. GENUINELY SCHEDULE-SPECIFIC CONVERSATION. Preserve its messages and set a
   concise direct_messages_reason explaining why a planned route is the wrong
   abstraction and acknowledging the weaker step-level lifecycle. Do not force
   a route merely because the queue is long.

Compact messages that only restate run_full_workflow/execute_step should normally
be converted to data-backed group_names/route_selections. Move reusable domain
procedure into owning steps or skills, but preserve conversation turns when their
schedule-specific context is the reason the direct model was chosen.

Validate every plan/config mutation, re-read workflow.json, and confirm each
enabled schedule is either safely route-backed or carries an explicit direct
message rationale. Do not run the workflow. If route equivalence is ambiguous,
keep the direct sequence with an honest rationale rather than guessing. Then call
set_workflow_contract_version(version="1.0.25") and stop.`

const upgradeReportActivitySection = `WORKFLOW CONTRACT UPGRADE: REPORT ACTIVITY SECTION.

This workflow predates the reporting contract's required activity section. Do only this one-time report migration.

If db/reports/index.html does not exist, this is a no-op — do not create a report from scratch in this migration. Otherwise read it in full.

Every report must include one section — its own tab, panel, or anchored region; the overall layout stays the report's own choice — that answers "what did this workflow actually do," in plain, non-technical language: recent runs and the actions taken in each, in the order a non-technical reader would want them, with no raw JSON, internal IDs, or state codes. Name it for the workflow's real run cadence: Daily Action (or Today's Actions) for a workflow that genuinely runs daily, Recent Activity or Latest Run for one that runs hourly, weekly, or on demand.

If the report already has an equivalent section under any name, do not duplicate it; at most rename or lightly adjust it to fit the cadence guidance above. If it is missing, add it as a new section reading live data from db/db.sqlite through window.report.query — do not fabricate content or invent values.

Call validate_report_html after editing; repair every error. Do not run the workflow. If the required source data is ambiguous or does not exist yet, report the blocker and do not stamp. Otherwise call set_workflow_contract_version(version="1.0.30") and stop.`

const upgradeReportActivityTab = `WORKFLOW CONTRACT UPGRADE: REPORT ACTIVITY SECTION MUST BE A TOP-LEVEL TAB.

This workflow predates the requirement that its "what did this workflow actually do" activity section (added or confirmed by the 1.0.30 migration -- Daily Action, Today's Actions, Recent Activity, or Latest Run, named for the workflow's real run cadence) be a top-level tab specifically, not merely a section, panel, or anchored region within another tab or a single scrolling page.

If db/reports/index.html does not exist, this is a no-op -- do not create a report from scratch in this migration. Otherwise read it in full.

If the report already uses tab-based navigation and the activity section is already one of those top-level tabs, this is already satisfied -- do not restructure anything else. If the activity section exists but is not a top-level tab (e.g. a subsection scrolled past within another tab, as in a report with only "Dashboard" and one or more content tabs), promote it into its own top-level tab, moving its existing markup and live-data wiring (window.report.query calls, ids) intact -- do not rewrite its content or invent new data. If the report currently has no tab structure at all (a single scrolling page, a sidebar, or anchored sections only), add a minimal tab bar and make this the first tab, migrating the rest of the existing content into a second tab (or more, if the report already has other genuinely distinct views) without changing what that other content says.

Call validate_report_html after editing; repair every error. Do not run the workflow. If the required source data is ambiguous or does not exist yet, report the blocker and do not stamp. Otherwise call set_workflow_contract_version(version="1.0.31") and stop.`

const upgradePulseLifecycleReconciliation = `WORKFLOW CONTRACT UPGRADE: RETIRED PULSE LIFECYCLE CHECKPOINT.

The legacy Pulse data reconciliation for this version has been retired. Do not inspect or modify Pulse data and do not run the workflow. Call set_workflow_contract_version(version="1.0.32") and stop.`

const upgradePulseBacklogTriage = `WORKFLOW CONTRACT UPGRADE: RETIRED PULSE BACKLOG CHECKPOINT.

The legacy Pulse backlog migration for this version has been retired. Do not inspect or modify Pulse data and do not run the workflow. Call set_workflow_contract_version(version="1.0.33") and stop.`

const upgradePulseActionableBacklog = `WORKFLOW CONTRACT UPGRADE: RETIRED PULSE ACTIONABLE-BACKLOG CHECKPOINT.

The legacy Pulse actionable-backlog migration for this version has been retired. Do not inspect or modify Pulse data and do not run the workflow. Call set_workflow_contract_version(version="1.0.34") and stop.`

const upgradeOrchestratorStepType = `WORKFLOW CONTRACT UPGRADE: RETIRED STEP-TYPE CHECKPOINT.

This historical rename is superseded by the agent migration at v1.0.47. Do not rewrite plan types here or run the workflow. Call set_workflow_contract_version(version="1.0.35") and stop.`

const upgradeActivityTabFromRunSummary = `WORKFLOW CONTRACT UPGRADE: THE ACTIVITY TAB CAN READ THE RUN SUMMARIES YOU ALREADY SEND.

Do only this one-time review. Nothing here is mandatory — the point is to offer the parent a simpler option and let them choose.

Every notify_user(notification_kind="run_summary") call already writes a structured row — title, status, message, fields, timestamp — into the org_dashboard_notifications table in this workflow's own db/db.sqlite, the same file the report queries. The required activity tab (Daily Action / Recent Activity) can therefore read ` + "`SELECT ... FROM org_dashboard_notifications WHERE notification_kind = 'run_summary' ORDER BY created_at DESC`" + ` with no step, table, or column of its own. The message is markdown: render it with window.report.renderMarkdown.

If db/reports/index.html does not exist, this is a no-op — do not create a report in this migration.

Otherwise read the report and find what feeds its activity tab:

1. It already reads org_dashboard_notifications, or it reads the workflow's own domain tables that exist for real business reasons — nothing to do. Do not rewrite a working tab.
2. A step, table, or column exists ONLY to feed this tab and has no other consumer — this is the case worth raising. Tell the parent plainly, in one or two sentences, what that extra machinery is and that the run summaries already carry the same facts, then ask whether to switch the tab over and retire it, or keep the custom version. Their answer decides; a richer bespoke view is a legitimate choice.
3. You cannot tell which — leave it alone and say so.

Do not delete a step or table without the parent agreeing in this conversation. Do not run the workflow. Call validate_report_html after any edit and repair every error. Then call set_workflow_contract_version(version="1.0.36") and stop — stamp it whichever way the parent chose, including "keep what we have".`

const upgradeScriptedTypeStaysRegular = `WORKFLOW CONTRACT UPGRADE: A DECLARED-SCRIPTED STEP'S PLAN TYPE MUST BE REGULAR, NEVER MESSAGE_SEQUENCE.

Do only this migration. Read planning/step_config.json and planning/plan.json. Find every step whose step_config declares declared_execution_mode="scripted" but whose plan.json type is "agent" instead of "regular" -- that combination is invalid (PLAT-280): the real scripted executor only runs true regular-type steps and reliably injects $DB_PATH/STEP_OUTPUT_DIR there, which the agent runtime does not guarantee even when its config claims to be scripted. This caused a live production step to silently lose database access.

For each matching step, call change_step_type(step_id=<its id>, target_type="scripted", reason="PLAT-280 migration: agent type with declared scripted mode is not a valid combination"). It atomically converts the step's plan type to regular in place -- same id, step_config.json history preserved -- and drops its agent items, since a scripted step's real work is the checked-in learnings/{step-id}/main.py, not plan-authored items. Do not hand-edit plan.json or step_config.json. Do not run the workflow.

If no step matches, this is a no-op. If change_step_type reports an error for any matching step, do not stamp -- leave the mismatch as-is and report what blocked it. Otherwise call set_workflow_contract_version(version="1.0.37") and stop.`

const upgradeDeclaredExecutionModeRetired = `WORKFLOW CONTRACT UPGRADE: EVERY STEP'S PLAN TYPE STATES ITS EXECUTION MODEL EXPLICITLY (PLAT-287, HALF 1).

Do only this migration. A "regular" plan step is a scripted step (its work is the checked-in learnings/<step-id>/main.py) and a "agent" step is conversational. Until now a "regular" step WITHOUT a declared scripted mode was a legacy agentic step that the runtime silently ran as a agent. Call migrate_declared_execution_mode once. It rewrites planning/plan.json so that shape is explicit -- every legacy agentic regular step becomes the agent it already ran as (same id, description, dependencies, validation, position), and any agent still declared scripted becomes regular -- validates the plan, and records the change. It touches only plan.json: planning/step_config.json is left exactly as it is, declared_execution_mode included, because the current runtime still reads that field; a later contract version retires it. Behavior does not change. A workflow already in this shape is a no-op.

Do not hand-edit plan.json or step_config.json and do not run the workflow. The tool refuses, without changing anything, when a step is declared scripted but has no learnings/<step-id>/main.py: that step is already broken, and this migration will not guess whether it should become a sequence or get a script. If it refuses, report exactly which step and why and do not stamp. Otherwise call set_workflow_contract_version(version="1.0.38") and stop.`

const upgradeDeclaredExecutionModeStripped = `WORKFLOW CONTRACT UPGRADE: THE RETIRED declared_execution_mode KEY IS REMOVED (PLAT-287, HALF 2).

Do only this migration. Since contract v1.0.38 every step's plan type states its execution model, and the runtime now reads only that: a "regular" step is scripted (it runs the checked-in learnings/<step-id>/main.py) and a "agent" step is conversational. The declared_execution_mode and declared_execution_mode_reason keys in planning/step_config.json are therefore dead and are removed. Call strip_declared_execution_mode() once. It removes both keys from planning/step_config.json only -- evaluation files are retired and left untouched -- and records every removed reason in planning/changelog. It is idempotent and a no-op when nothing is left to strip.

Do not hand-edit plan.json or step_config.json, and do not run the workflow. The tool refuses, without changing anything, while a regular step still carries declared_execution_mode="agentic": that means the v1.0.38 migration (migrate_declared_execution_mode) did not complete -- run it, then retry; if it still refuses, do not stamp and report what blocked it. Otherwise call set_workflow_contract_version(version="1.0.39") and stop.`

const upgradeManagedDBScripts = `WORKFLOW CONTRACT UPGRADE: SCRIPTS USE THE MANAGED DATABASE HELPER (PLAT-428).

Do only this migration. Scripted steps used to open db/db.sqlite themselves (import sqlite3, $DB_PATH). They now reach the database through the built-in Python module agentworks_db, which calls the same managed query_workflow_db / mutate_workflow_db tools agents use: SQL is validated, every write is one transaction, and bulk writes and paged reads are built in. $DB_PATH stays set for old scripts until every workflow is on this contract; do not use it in anything you write.

1. Call scan_workflow_script_db_usage. It lists every script under code/ that still opens the database directly (raw_db) and every schema statement found in a script (ddl). If it reports nothing, go to step 5.
2. Convert each listed script. The helper is imported with: from agentworks_db import query, query_one, scalar, iter_query, execute, insert, execute_many, transaction, DBError. Mapping:
   - conn = sqlite3.connect(os.environ["DB_PATH"]) / conn.close(): delete both; there is no connection.
   - cur.execute("SELECT ...", p).fetchall(): query with the SQL and a params list. Rows are dicts (row["col"], not row[0]); give the SELECT an ORDER BY when order matters. fetchone(): query_one with the same arguments; a single value: scalar; a very large result: iter_query.
   - cur.execute("INSERT/UPDATE/DELETE ...", p) followed by conn.commit(): execute (it commits itself and returns the rows affected); cur.lastrowid: insert.
   - cur.executemany(sql, rows): execute_many with the statement and the list of rows (2000 rows per call, each call atomic; atomic=True makes it one transaction of at most 5000 rows).
   - several writes that must succeed together (with conn: ...): transaction with a list of (sql, params) pairs (up to 200 statements).
   - conn.row_factory = sqlite3.Row: not needed. Dates and datetimes are sent as ISO text.
   - PRAGMA foreign_keys = ON: delete it. Managed writes always enforce the foreign keys the schema declares, so the protection stays.
   - Any other PRAGMA, ATTACH, VACUUM, executescript and creating triggers at run time are not available through the helper. Do not invent a workaround: leave that script unconverted and report it (see below).
3. Schema statements (ddl): CREATE, ALTER and DROP are refused by the managed tools, so they cannot stay in a script. Read db/README.md first and keep each table's documented contract. Move every statement into a migration file db/migrations/<date>-<name>.sql (idempotent, for example CREATE TABLE IF NOT EXISTS), write it with ordinary file tools, call apply_workflow_db_migration(migration_file=...) once, then remove the statement from the script.
4. Verify each converted script without running the whole workflow: check it parses (python3 -c "import ast; ast.parse(open('code/<step-id>/main.py').read())") and run the step's own test_*.py when it has one. Test files may keep using sqlite3 to check results; they are not scanned. Then call scan_workflow_script_db_usage again until it reports no raw_db and no ddl.
5. Do not hand-edit workflow.json. When the scan is clean call set_workflow_contract_version(version="1.0.45"). The stamp tool runs the same scan and refuses, naming what is left, while any script still opens the database or carries a schema statement.

If a script cannot be converted without a product decision (it needs a PRAGMA, ATTACH, a trigger created at run time, or its behaviour is unclear), do not guess and do not stamp: leave it as it is and report exactly what blocked it so the operator can decide.`

const upgradeStepDescriptionLayout = `WORKFLOW CONTRACT UPGRADE: STEP DESCRIPTIONS USE THE STANDARD LAYOUT.

Do only this migration. Every agent step's description (type agent or orchestrator, nested sub-agent steps included) now uses the section layout from builder-reference references/step-description.md ("Use this section layout"), in this order:
## Goal       — what the step achieves, in one to three sentences
## Inputs     — what it reads: prior step outputs, DB tables, KB notes by name
## Output     — what it produces and where; the shape stays in validation_schema
## Rules      — binding business constraints, approval limits, actions outside its authority
## Done when  — the success boundary, including what counts as a valid no-op
## Guides     — the skill/learning references to use, named only, never copied
Goal, Inputs, Output and Done when are required; Rules and Guides may be omitted only when there is nothing to say. Leave every other step type as it is: scripted (regular), routing, branch, human_input and crew steps are driven by code, routes, a question to a person or a Crew, and do not use the layout; an empty description on them is fine. Read that reference once before you start.

THIS IS A TEXT REORGANIZATION. NEVER CHANGE BEHAVIOUR: do not change outputs, context_dependencies, context_output, validation_schema, items, routes, step type, or any other step field; only the description moves.

1. Read planning/plan.json and list every step with its description length. A step whose description already has the ## Goal, ## Inputs, ## Output and ## Done when headings is done: skip it, so a re-run of this migration resumes where the last one stopped.
2. Work one step at a time, largest description first. For each step:
   - Keep only WHAT in the description: objective, inputs, output location, binding constraints, success boundary.
   - Move binding business rules that are long or shared by several steps to a knowledgebase note, and reusable HOW-TO (tool usage, selectors, API sequences, troubleshooting, verified techniques) to a learnings skill reference; name each moved file under ## Inputs or ## Guides (a named workflow path is delivered to the step). Write moved files first.
   - Express any Brain/knowledge-base use the step already has the way the reference says: Brain reads under ## Inputs or ## Guides as brain:<folder>/<note> (delivered read-only), Brain writes under ## Output, limits under ## Rules; look up real folder paths with brain_browse. Do not change the step's knowledgebase_access value.
   - Remove history: dated observations, incident narratives and "this exists because" explanations. Keep any rule they carry; put the history itself in the change reason.
   - Run check_plan_no_loss(step_id=..., proposed_description=...) (pass dropped_history only for tokens that appear solely in dated/incident sentences). Fix everything it reports missing and run it again until it passes, then save the new description with the normal plan edit tools.
3. If a step cannot be converted without a product decision (its rules conflict, or what a passage means is unclear), do not guess: leave it unchanged, finish the other steps, and report exactly what blocked it.
4. Do not hand-edit workflow.json and do not run the workflow. Stamp only when every step is done: call set_workflow_contract_version(version="1.0.46"). The stamp tool refuses, naming each step still missing a required heading, until every plan step is in the layout.`

const workflowUpgradeWorkspacePathPlaceholder = "{{WORKSPACE_PATH}}"

func bindWorkflowUpgradeWorkspacePath(query, workspacePath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return query
	}
	return strings.ReplaceAll(query, workflowUpgradeWorkspacePathPlaceholder, strconv.Quote(workspacePath))
}

// workflowVersionUpgradePlan keeps the retired HTML presentation migrations
// retired, but preserves the independent behavioral/data migrations older
// workflows still need. They are deliberately grouped into bounded,
// blocking preflight turns rather than replaying the old 21-turn HTML chain.
// workflowVersionUpgradePlan is the ordered list of migrations a workflow owes.
// A Relay owes only the shared ones (see goalsOnlyWorkflowUpgrades).
func workflowVersionUpgradePlan(manifest *WorkflowManifest) []workflowVersionUpgrade {
	if isPythonRelay(manifest) {
		return nil
	}
	plan := fullWorkflowVersionUpgradePlan(manifest)
	if !manifestIsRelay(manifest) {
		return plan
	}
	shared := plan[:0:0]
	for _, upgrade := range plan {
		if !goalsOnlyWorkflowUpgrades[upgrade.label] && !relayStoreUpgrades[upgrade.label] {
			shared = append(shared, upgrade)
		}
	}
	return shared
}

func fullWorkflowVersionUpgradePlan(manifest *WorkflowManifest) []workflowVersionUpgrade {
	version := workflowContractVersionForUpgrade(manifest)
	rank, known := workflowContractVersionRank(version)
	if !known || workflowContractVersionIsExecutionCompatible(version) {
		return nil
	}

	steps := make([]workflowVersionUpgrade, 0, 8)
	if rank < 10 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractAgentCodeVersion, label: "upgrade-agent-code", query: upgradeAgentCode})
	}
	if rank < 12 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractNotificationConfigVersion, label: "upgrade-notification-config", query: upgradeNotificationConfig})
	}
	if rank < 15 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractKBWriteMethodRetiredVersion, label: "upgrade-kb-write-method", query: upgradeKBWriteMethod})
	}
	if rank < 16 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractEvalVerdictSchemaVersion, label: "upgrade-eval-verdict-schema", query: upgradeEvalVerdictSchema})
	}
	// workflowContractArtifactPurityVersion ("1.0.21") sits at rank 20 in the
	// known-version list above — a workflow whose rank is already >= 20 passed
	// this step in an earlier preflight and must not repeat it.
	if rank < 20 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractArtifactPurityVersion, label: "upgrade-current-artifact-contract", query: upgradeCurrentArtifactContract})
	}
	if rank < 22 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractDirectHTMLReportsVersion, label: "upgrade-direct-html-reports", query: upgradeDirectHTMLReports})
	}
	// workflowContractScheduleExecutionModelVersion ("1.0.25") sits at rank 24
	// — a workflow already at or past it must not repeat this rung.
	if rank < 24 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractScheduleExecutionModelVersion, label: "upgrade-schedule-execution-model", query: upgradeScheduledRoutes})
	}
	if rank < 26 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractDedicatedPulseScheduleVersion, label: "upgrade-dedicated-pulse-schedule", query: upgradeDedicatedPulseSchedule})
	}
	if rank < 27 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractSchedulePromptContractVersion, label: "upgrade-schedule-prompt-contract", query: upgradeSchedulePromptContract})
	}
	if rank < 28 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractFinalizerOwnedScheduleVersion, label: "upgrade-schedule-finalizer-ownership", query: upgradeScheduleFinalizerOwnership})
	}
	if rank < 29 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractReportActivitySectionVersion, label: "upgrade-report-activity-section", query: upgradeReportActivitySection})
	}
	if rank < 30 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractReportActivityTabVersion, label: "upgrade-report-activity-tab", query: upgradeReportActivityTab})
	}
	if rank < 31 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractPulseLifecycleReconciliationVersion, label: "upgrade-pulse-lifecycle-reconciliation", query: upgradePulseLifecycleReconciliation})
	}
	if rank < 32 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractPulseBacklogTriageVersion, label: "upgrade-pulse-backlog-triage", query: upgradePulseBacklogTriage})
	}
	if rank < 33 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractPulseActionableBacklogVersion, label: "upgrade-pulse-actionable-backlog", query: upgradePulseActionableBacklog})
	}
	// workflowContractOrchestratorStepTypeVersion ("1.0.35") sits at rank 34.
	if rank < 34 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractOrchestratorStepTypeVersion, label: "upgrade-orchestrator-step-type", query: upgradeOrchestratorStepType})
	}
	// workflowContractActivityTabFromRunSummaryVersion ("1.0.36") sits at rank 35.
	if rank < 35 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractActivityTabFromRunSummaryVersion, label: "upgrade-activity-tab-from-run-summary", query: upgradeActivityTabFromRunSummary})
	}
	// workflowContractScriptedTypeStaysRegularVersion ("1.0.37") sits at rank 36.
	if rank < 36 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractScriptedTypeStaysRegularVersion, label: "upgrade-scripted-type-stays-regular", query: upgradeScriptedTypeStaysRegular})
	}
	// workflowContractDeclaredExecutionModeRetiredVersion ("1.0.38") sits at rank 37.
	if rank < 37 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractDeclaredExecutionModeRetiredVersion, label: "upgrade-declared-execution-mode-retired", query: upgradeDeclaredExecutionModeRetired})
	}
	// workflowContractDeclaredExecutionModeStrippedVersion ("1.0.39") sits at rank 38.
	if rank < 38 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractDeclaredExecutionModeStrippedVersion, label: "upgrade-declared-execution-mode-stripped", query: upgradeDeclaredExecutionModeStripped})
	}
	if rank < 39 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractRouteSummariesVersion, label: "upgrade-route-summaries", query: upgradeRouteSummaries})
	}
	if rank < 40 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractExplicitSchedulePulseVersion, label: "upgrade-explicit-schedule-pulse", query: upgradeExplicitSchedulePulse})
	}
	// Contracts v1.0.42 and v1.0.43 remain known historical markers, but their
	// retired migrations are skipped. Every older workflow advances directly to
	// the first active contract after them.
	if rank < 43 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractNestedAgentArtifactsVersion, label: "upgrade-nested-agent-artifacts", query: upgradeNestedAgentArtifacts})
	}
	// workflowContractManagedDBScriptsVersion ("1.0.45") sits at rank 44.
	if rank < 44 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractManagedDBScriptsVersion, label: "upgrade-managed-db-scripts", query: upgradeManagedDBScripts})
	}
	// workflowContractStepDescriptionLayoutVersion ("1.0.46") sits at rank 45.
	if rank < 45 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractStepDescriptionLayoutVersion, label: "upgrade-step-description-layout", query: upgradeStepDescriptionLayout})
	}
	if rank < 46 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractUnifiedAgentVersion, label: "upgrade-agent-step", query: upgradeAgentStep})
	}
	// Attached here rather than at the call site so the turn text is identical
	// wherever it is built. The version pair used to be added only on the Pulse
	// delivery path, which meant the blocking preflight — the one that actually
	// gates on the stamp — never told the agent which version it was moving
	// from or to. That path is gone; the context it carried is not.
	for i := range steps {
		steps[i].query += fmt.Sprintf(
			"\n\nCurrent workflow.json version: %q. Target workflow contract version: %q.",
			version, steps[i].to,
		) + manualUpgradeTurnNote
	}
	return steps
}

// manualUpgradeTurnNote is appended to every upgrade instruction returned to
// Workshop. Contract migrations are operator-started work: schedules never own
// them. The operator can therefore answer a genuine product or safety question
// in the same conversation; the agent must not guess merely to obtain a stamp.
const manualUpgradeTurnNote = `

EXECUTION CONTEXT. This is a manual platform migration started by the workflow operator in Workshop chat. Complete and verify this migration before stamping it. If a genuine product, business, or safety choice is required, do not guess and do not stamp: explain the exact choice in this conversation and ask the operator.`

const upgradeAgentStep = `WORKFLOW CONTRACT UPGRADE: ONE AGENT STEP TYPE.

Do only this migration. Conversational steps now use type agent with items and optional predefined_routes. Call migrate_agent_steps once. It rewrites message_sequence, orchestrator and todo_task records, including nested and orphan agents, to agent; legacy messages become items. Routes, IDs, wiring, validation and unknown fields are preserved. Do not edit plan.json by hand or run the workflow. If the migration reports an error, do not stamp. Otherwise call set_workflow_contract_version(version="1.0.47") and stop.`
