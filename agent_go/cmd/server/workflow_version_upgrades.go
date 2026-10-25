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

// Current and previous agent prompt contracts are execution-compatible during
// the reviewed description-first rollout. Historical 1.0.41-1.0.43
// workflows owe the nested-agent-artifact migration, 1.0.44 workflows the
// managed database script migration, and 1.0.45 workflows the step description
// layout migration.
func workflowContractVersionIsExecutionCompatible(version string) bool {
	switch strings.TrimSpace(version) {
	case workflowContractUnifiedAgentVersion, workflowContractDescriptionFirstVersion, workflowContractResourceIntentVersion, workflowContractGoalAgentVersion, workflowContractSkillsVersion:
		return true
	}
	return false
}

var goalsOnlyWorkflowUpgrades = map[string]bool{
	"upgrade-workflow-skills": true,
	"upgrade-goal-agent":      true,
}

var relayStoreUpgrades = map[string]bool{
	"upgrade-managed-db-scripts": true,
}

func manifestIsRelay(manifest *WorkflowManifest) bool {
	return manifest != nil && manifest.Kind == "relay"
}

// manifestContractIsExecutionCompatible reports whether a manifest's contract
// version lets it run. A Goals workflow must be on a compatible version. A Relay
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
	if !manifestIsRelay(manifest) || workflowContractNeedsRecreation(manifest) {
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
		workflowContractScriptedPlanTypeVersion,
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
		workflowContractDescriptionFirstVersion,
		workflowContractResourceIntentVersion,
		workflowContractGoalAgentVersion,
		workflowContractSkillsVersion,
	}
	for rank, candidate := range known {
		if version == candidate {
			return rank, true
		}
	}
	return 0, false
}

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

Before any plan checker, call migrate_agent_steps to prepare saved records as canonical agent steps. This raw-JSON prerequisite works on earlier contract versions and does not stamp 1.0.47; keep stamps in order. If preparation fails, stop and report it.

Do only the description migration after preparation. Every agent step's description (type agent, nested sub-agent steps included) now uses the section layout from builder-reference references/step-description.md ("Use this section layout"), in this order:
## Goal       — what the step achieves, in one to three sentences
## Inputs     — what it reads: prior step outputs, DB tables, KB notes by name
## Output     — what it produces and where; the shape stays in validation_schema
## Rules      — binding business constraints, approval limits, actions outside its authority
## Done when  — the success boundary, including what counts as a valid no-op
## Guides     — the skill/learning references to use, named only, never copied
Goal, Inputs, Output and Done when are required; Rules and Guides may be omitted only when there is nothing to say. Leave every other step type as it is: scripted (scripted), routing, branch and crew steps are driven by code, routes or a Crew, and do not use the layout; an empty description on them is fine. Read that reference once before you start.

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

const oldWorkflowContractMessage = "This workflow is on an old version; ask its Builder to recreate or re-stamp it."

func workflowContractNeedsRecreation(manifest *WorkflowManifest) bool {
	if isPythonRelay(manifest) {
		return false
	}
	rank, known := workflowContractVersionRank(workflowContractVersionForUpgrade(manifest))
	return known && rank < 43
}

// workflowVersionUpgradePlan lists supported upgrades for this product.
func workflowVersionUpgradePlan(manifest *WorkflowManifest) []workflowVersionUpgrade {
	if isPythonRelay(manifest) || workflowContractNeedsRecreation(manifest) {
		return nil
	}
	return eligibleWorkflowVersionUpgrades(manifest, fullWorkflowVersionUpgradePlan(manifest))
}

func eligibleWorkflowVersionUpgrades(manifest *WorkflowManifest, plan []workflowVersionUpgrade) []workflowVersionUpgrade {
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
	if !known || version == WorkflowContractCurrentVersion {
		return nil
	}

	steps := make([]workflowVersionUpgrade, 0, 8)
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
	if rank < 47 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractDescriptionFirstVersion, label: "upgrade-description-first", query: upgradeDescriptionFirst})
	}
	if rank < 48 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractResourceIntentVersion, label: "upgrade-resource-intent", query: upgradeResourceIntent})
	}
	if rank < 49 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractGoalAgentVersion, label: "upgrade-goal-agent", query: upgradeGoalAgent})
	}
	if rank < 50 {
		steps = append(steps, workflowVersionUpgrade{from: version, to: workflowContractSkillsVersion, label: "upgrade-workflow-skills", query: upgradeWorkflowSkills})
	}

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

const upgradeDescriptionFirst = `WORKFLOW CONTRACT UPGRADE: DESCRIPTION FIRST, OPTIONAL FOLLOW-UPS (1.0.48).

Do only this migration; never run the workflow or its side-effecting tools. Read builder-reference/references/step-description.md and agent.md. Review every ordinary agent including nested routes and orphans. Custom authored_prompt agents keep their explicit prompts and items.

The description executes as the first user turn ALONE, before any sequence item. The first item is a later turn, never combined with the description. Zero items is valid. Builder chooses useful next phases or semantic checks when automated validation cannot prove the business evidence; do not require generic execute/verify/repair turns. Automatic final validation and its repair loop remain.

For each ordinary agent, use update_step(step_id=..., changes={...}) to supply prompt_contract="description-first-v1", a runnable description whose ## Rules section holds the binding policies, approval, idempotency and scope constraints, and reviewed items. Do not author system_rules: task rules live only in the description, and any saved or supplied system_rules is merged into its Rules. Drop old items that repeat task execution; fold any unique requirements from them into the description (its Rules). Keep genuinely subsequent phases and their IDs/order, store narrowing, scripts, intermediate gates and output contracts. On the first retained item, set retained_reason to explain the distinct subsequent phase, why the automatic gate cannot cover it, and how it avoids repeating initial work. This is a saved review note, not a user turn. For side-effecting tasks, make verifiers evidence-only: no new send, claim, purchase, browser action, post or publish to repair ambiguous confirmation; report that gap. Replace legacy references to executing a system charter with this task. If a first execute item had narrowed writes, preserve them with initial_write_access. Do not infer edits by regex or blindly drop default-looking instructions. Review script-first ordering: prerequisites needed before task execution belong upstream, not after the new opener. Preserve live input semantics, route re-entry continuity, and reference/read permissions. Do not change scripts, validation schemas, access or external behavior just to stamp.

Check preview_step_runtime for the description turn (default) and retained items. Read back the plan and compare the complete diff, including send-once/claims/spend/provider-call boundaries. Resolve any real business ambiguity with the operator. Once every ordinary agent is reviewed, call set_workflow_contract_version(version="1.0.48"). The stamp refuses unmigrated definitions. Existing old-contract plans remain executable with their saved semantics until migrated; active runs keep their loaded plan. Stop after stamping.`

const upgradeResourceIntent = `WORKFLOW CONTRACT UPGRADE: RESOURCE INTENT IN STEP DESCRIPTIONS (1.0.49).

Do only this migration; never run the workflow or its side-effecting tools. Read builder-reference/references/step-description.md. This is a definitions-only review: the only thing you change is description text, through update_step.

GRANT WARNING: naming a workflow path (learnings/, knowledgebase/, code/shared/, soul/, db/README.md) or a brain: note in a step's Inputs, Resources or Guides GIVES that step read access. Describe a resource by purpose ("the company policy notes") and never add such a reference to a step that does not already name it. This review must not add or widen any access.

A description is the task, its resource intent and its Rules. Review EVERY ordinary agent: each top-level step, each predefined route's sub-agent at any depth, and each orphan agent. Custom authored_prompt agents keep their explicit prompts. For each one, read its description, items, scripts, validation schema and effective access (preview_step_runtime with include_prompt=false), then make its ## Resources section state, one line each:
- Knowledge base: what it reads or writes and why, or none
- Database: what it reads or writes and why, or none
- Brain: what it reads or writes and why, or none
- Learnings: what it reads or contributes and why, or none
Say read or write exactly as the step's actual grants allow; never claim a write the grant does not permit. A resource the step merely could reach but does not use is none. A step may read workflow files, including named guides, even when knowledgebase_access or learnings_access is none; those settings govern automatic context and writes, not workflow-folder reads. Write each line in plain words for the workflow's owner, who is not technical: say what the step looks at or records and why, not internal terms such as writer ownership, append-only logs or hashes; the exact table names already appear under Inputs and Output. Before writing a Database line, check every table against the step's real outputs and the writers listed in db/README.md: a summary or report file is not a database write, and a step that changes the status of findings or releases should say so. If the step also keeps something outside the four resources, such as evidence files under db/assets, say that in one short phrase. Describe per-workflow learnings only: global learnings are deprecated. Keep Goal, Inputs, Output, Rules, Done when and Guides as they are; procedural how-to stays in skills, not here. Scripted, routing and Crew steps keep short descriptions with no required section; if a scripted step's code uses the workflow database or a knowledge base, say so in one short line.

LIMITS. (1) No new access. Describe only resources the step already has. Naming a workflow path (learnings/, knowledgebase/, code/shared/, soul/, db/README.md) or a brain: note in Inputs, Resources or Guides grants read access, so do not write such a reference in a step that does not already name it; describe the resource by purpose instead. Never edit access, grants, store modes or secret selections. If a description needs a resource the step cannot reach, do not fix it: report it to the operator. (2) Definitions only: no data moves between stores, no database schema change, no learnings rewrite. (3) Do not change scripts, validation, wiring, routes, items, ordering or optional sequence semantics. (4) Existing wording and policies in each description stay unless they contradict a grant.

Read back the plan and show the operator the full description diff before stamping, and name any step whose description_reviewed mark was cleared because its description changed (the plan tools do that on purpose). Once every ordinary agent is reviewed, call set_workflow_contract_version(version="1.0.49"). The stamp refuses an agent whose ## Resources does not state all four resources, or says none for a resource its Inputs, Resources or Guides name; it does not judge your wording. Workflows on 1.0.48 keep running until migrated. Stop after stamping.`

const upgradeGoalAgent = `WORKFLOW CONTRACT UPGRADE: EXPLICIT GOAL AGENT TASKS (1.0.50).

Read planning/plan.json and planning/step_config.json first. Call migrate_goal_agents. It retires only LLM specialist routes and automatic reflection objectives, preserving their original names, instructions and destinations verbatim in each step's goal_agent_migration audit note and the plan changelog. Scripted routes with typed I/O remain callable helpers. IDs, explicit access settings, authored message phases, outputs, data and run history stay. Configured access modes are unchanged. Former objective-inferred write access is recorded in the audit note, never converted into a standing grant; let the owner decide if an explicit task needs a new write grant.

Read the resulting notes. For every former learning_objective or knowledgebase_contribution, tell the owner what it used to contribute and its destination. If that contribution is required by the goal, move it into the relevant task description (respect the existing store grants); otherwise explain that Builder or Crew can do it explicitly. No automatic post-validation reflection turn remains, including scripted post-processing and restart recovery.

Keep explicitly authored follow-up phases. Do not collapse an authored sequence by guessing; remove only reviewed redundant platform scaffolding, preserving any unique requirement in the task and describing each edit in the changelog. Do not convert to a new agent type or rewrite the Goal runner. For retired LLM specialist routes, record an AgentWorks issue suggesting an explicit Crew step; do not create a Crew automatically. Keep the retired route instructions in the audit note, outside executable descriptions.

Read back the full plan/config diff. Preserve validation with bounded repair in the same conversation, resource grants, tools/secrets, real outputs, logs, costs and metric history. Do not run a business workflow as part of migration. When review is complete call set_workflow_contract_version(version="1.0.50") and stop. Older compatible workflows may still run, but LLM specialist spawning and automatic reflection are retired at runtime even before this explicit migration.`

const upgradeWorkflowSkills = `WORKFLOW CONTRACT UPGRADE: KNOWLEDGE BASE AND SKILLS.

Do only this explicit upgrade. Finish the existing code-layout migration first if code_layout_version is not 1; never move or delete a legacy script by treating all of learnings/ as prose. Call migrate_workflow_skills. It preserves task guides and supporting assets in this workflow's own skills/workflow-how-to/ bundle (a collision-safe suffix may be used), records copy hashes and a changelog, and keeps the old learning files intact. The short skill index identifies the originating step and opens only the reference needed; never inline the full prose bundle into instructions.

Review the copied index and references, existing step descriptions, declared guides and scripts. Repoint live how-to references to the workflow-local skill while preserving the original prose copies and run history. Existing step learning access is audited and retired; knowledge-base grants do not change. Only prior guide-reading steps select the migrated skill; respect existing none selections. Reusable how-to belongs in skills, facts in the knowledge base and results in the database. Steps read skills; Builder or Crew maintain them. Do not add reflection, another learning store, a Crew or new permissions automatically.

Read back the migration and unchanged KB grants. Verify the private copied files and skill selection, then stamp 1.0.51. Older workflows continue reading their old guides until this explicit migration is completed. Do not execute business side effects or delete the old learning folder during this upgrade.`
