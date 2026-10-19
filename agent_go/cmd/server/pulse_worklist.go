package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const (
	// pulseModuleTechnicalReview is one durable review identity with distinct
	// agent-selected lenses for correctness, stores, runtime operations,
	// orchestration shape, model/tier fitness, cost, and execution efficiency.
	// Engineering and Operations remain useful lens names, not separate queues.
	pulseModuleTechnicalReview    = pulsemodules.TechnicalReviewID
	pulseModuleArchitectureReview = pulsemodules.ArchitectureReviewID
	// Legacy constants remain available to migration tests and readers. Runtime
	// worklists and new writes use pulseModuleTechnicalReview exclusively.
	pulseModuleWorkflowReview = pulsemodules.LegacyWorkflowReviewID
	// pulseModuleStrategicReview owns both hidden-mechanism review of the
	// current strategy and conditional discovery of materially different
	// approaches. Those are sequence turns, not independent modules.
	pulseModuleStrategicReview = pulsemodules.StrategicReviewID
)

// Derived from the canonical registry — see pkg/pulsemodules. Do not restate
// the module set here; a hand-maintained second copy is exactly what caused
// the 2026-07-29 desync.
var pulseModuleOrder = pulsemodules.IDs()

var validPulseModules = func() map[string]bool {
	m := make(map[string]bool, len(pulsemodules.IDs()))
	for _, id := range pulsemodules.IDs() {
		m[id] = true
	}
	return m
}()

// pulseModuleList renders the closed module set for rejection messages. A
// rejection that names the bad value but not the set it had to come from
// leaves the caller guessing, which is how the same invalid value gets retried.
func pulseModuleList() string {
	return strings.Join(pulseModuleOrder, ", ")
}

func ensurePulseStoreSchema(ctx context.Context, db *sql.DB) error {
	for _, ddl := range []string{backgroundAgentLogSchema, pulseFastRequestSchema} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return ensureBackgroundAgentLogColumns(ctx, db)
}

func openPulseStoreDB(ctx context.Context, workspacePath string, create bool) (string, *sql.DB, error) {
	normalized, db, err := openReportHumanInputDB(ctx, workspacePath, create)
	if err != nil || db == nil {
		return normalized, db, err
	}
	if create {
		if err := ensurePulseStoreSchema(ctx, db); err != nil {
			_ = db.Close()
			return "", nil, err
		}

	}
	return normalized, db, nil
}

func (api *StreamingAPI) handleGetPulseFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	module := normalizePulseModule(r.URL.Query().Get("module"))
	if module != "" && !validPulseModules[module] {
		http.Error(w, fmt.Sprintf("module %q is not valid", module), http.StatusBadRequest)
		return
	}
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(
		r.Context(), workspacePath, module, -1,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"findings": findings,
		"total":    len(findings),
	})
}

func (api *StreamingAPI) handleGetPulseReviews(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	module := normalizePulseModule(r.URL.Query().Get("module"))
	if module != "" && !validPulseModules[module] {
		http.Error(w, fmt.Sprintf("module %q is not valid", module), http.StatusBadRequest)
		return
	}
	receipts, err := step_based_workflow.LoadPulseReviewReceipts(r.Context(), workspacePath, module, -1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reports, err := listPulseReviewReports(workspacePath, module)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	modules := pulseModuleOrder
	if module != "" {
		modules = []string{module}
	}
	for _, owner := range modules {
		notes, err := loadPulseReviewNotes(r.Context(), workspacePath, owner, "", 50)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reports = pulseReportsWithNotes(reports, notes)
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "reviews": receipts, "total": len(receipts), "reports": reports})
}

func (api *StreamingAPI) handleGetPulseAgentMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	module := strings.TrimSpace(r.URL.Query().Get("module"))
	if module != "" {
		module = normalizePulseModule(module)
		if !validPulseModules[module] {
			http.Error(w, fmt.Sprintf("module %q is not valid", module), http.StatusBadRequest)
			return
		}
	}
	role := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("role")))
	if role != "" && role != "reviewer" && role != "fixer" {
		http.Error(w, "role must be reviewer or fixer", http.StatusBadRequest)
		return
	}
	metrics, err := step_based_workflow.LoadPulseAgentMetrics(
		r.Context(), workspacePath, r.URL.Query().Get("pulse_run_id"), module, role, -1,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"metrics": metrics,
		"total":   len(metrics),
	})
}

func (api *StreamingAPI) handleGetPulseImpact(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ledger, err := step_based_workflow.LoadPulseImpactLedger(r.Context(), workspacePath, 500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "impact": ledger})
}

func (api *StreamingAPI) handleGetPulseContext(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(r.URL.Query().Get("workspace_path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	records, err := step_based_workflow.LoadPulseContextRecords(r.Context(), workspacePath, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "records": records, "total": len(records)})
}

func createPulseTools() ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	moduleEnum := append([]string(nil), pulseModuleOrder...)
	reviewIdentityProperties := map[string]interface{}{
		"workspace_path": map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/social-media."},
		"pulse_run_id":   map[string]interface{}{"type": "string", "description": "Use \"current\" for this active Pulse conversation."},
		"module":         map[string]interface{}{"type": "string", "enum": moduleEnum},
	}
	findingProperties := map[string]interface{}{}
	for key, value := range reviewIdentityProperties {
		findingProperties[key] = value
	}
	for key, value := range map[string]interface{}{
		"issue_id":          map[string]interface{}{"type": "string", "description": "Optional visible PUL issue_id from the active or closed issue index returned by get_pulse_state(view=\"backlog\", detail=\"compact\"). Supply it when this is new evidence for an existing root cause; a closed match is reopened. Omit only for a genuinely new root issue."},
		"step_id":           map[string]interface{}{"type": "string", "description": "The plan step this finding is about, e.g. the step's id field from plan.json. Required for a plan_drift_review fail-status check's linked finding (record_plan_drift_review verifies the finding is filed against this exact step). Omit only for a genuinely module-wide finding that is not about one specific step; updating an existing issue by issue_id keeps that issue's original step identity regardless of what is passed here."},
		"concern":           map[string]interface{}{"type": "string", "description": "Current concise root-cause statement. It may be reworded when issue_id is supplied; wording never creates a new identity by itself."},
		"issue_kind":        map[string]interface{}{"type": "string", "enum": []string{"workflow_issue", "harness_issue"}},
		"classification":    map[string]interface{}{"type": "string"},
		"severity":          map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
		"summary":           map[string]interface{}{"type": "string"},
		"impact":            map[string]interface{}{"type": "string"},
		"evidence":          map[string]interface{}{"type": "array", "minItems": 1, "items": map[string]interface{}{"type": "string"}},
		"recommended_route": map[string]interface{}{"type": "string", "enum": []string{"decision_required", "fixer_handoff"}},
		"human_input_id":    map[string]interface{}{"type": "string", "description": "Required with recommended_route=decision_required. Create the pending reviewer-owned decision first and pass its exact id so the finding is atomically linked as awaiting_user."},
		"next_check":        map[string]interface{}{"type": "string"},
		"workaround":        map[string]interface{}{"type": "string"},
		"target_key":        map[string]interface{}{"type": "string"},
		"reproduction": map[string]interface{}{
			"type": "object", "additionalProperties": false,
			"properties": map[string]interface{}{
				"safe": map[string]interface{}{"type": "boolean"}, "setup": map[string]interface{}{"type": "string"},
				"action": map[string]interface{}{"type": "string"}, "expected": map[string]interface{}{"type": "string"},
				"observed": map[string]interface{}{"type": "string"}, "limitations": map[string]interface{}{"type": "string"},
			},
		},
	} {
		findingProperties[key] = value
	}
	recordFindingTool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "record_pulse_finding",
		Description: "Open or update one canonical Pulse issue. First inspect the compact active and closed issue index and reason about semantic sameness; request targeted full detail only for candidate issue_ids. For an existing root cause, including a closed one, supply its issue_id and update/reopen it; do not create a second issue because wording, evidence paths, or symptoms differ. Omit issue_id only for a genuinely distinct root cause with a different repair or owner. Put the problem, evidence, and any useful next action in this issue rather than creating separate recommendation, verification, impact, or disposition records. Backup, Publish and Notify are separate after-run actions; report only a real terminal failure after the action ran.",
		Parameters:  llmtypes.NewParameters(map[string]interface{}{"type": "object", "additionalProperties": false, "properties": findingProperties, "required": []string{"workspace_path", "pulse_run_id", "module", "concern", "issue_kind", "classification", "severity", "summary", "impact", "evidence"}}),
	}}
	mergeIssuesTool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name:        "merge_pulse_issues",
		Description: "Merge symptom-level duplicate Pulse issues into one canonical root-cause issue after a semantic backlog review. The canonical PUL issue remains visible and each duplicate is retired without losing its existing audit history. Use only when the issues share the same causal defect and one action would address them; never merge merely because they involve the same file or module.",
		Parameters: llmtypes.NewParameters(map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
			"workspace_path":      map[string]interface{}{"type": "string"},
			"canonical_issue_id":  map[string]interface{}{"type": "string", "description": "The one visible PUL issue that remains active."},
			"duplicate_issue_ids": map[string]interface{}{"type": "array", "minItems": 1, "items": map[string]interface{}{"type": "string"}, "description": "Visible PUL issue IDs to retire into the canonical issue."},
			"reason":              map[string]interface{}{"type": "string", "description": "One sentence naming the shared root cause and why one repair covers the duplicates."},
		}, "required": []string{"workspace_path", "canonical_issue_id", "duplicate_issue_ids", "reason"}}),
	}}
	stateTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "get_pulse_state",
			Description: "Read saved Pulse evidence: goal_work, goal_status, step_concerns, step_outputs, backlog, or historical review/review_notes. Evidence is not a new approval or permission. Use bounded backlog detail for specific saved issue IDs.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/social-media."},
					"view":           map[string]interface{}{"type": "string", "enum": pulseStateViewValues, "description": "Which saved Pulse evidence to read: the finding backlog, one historical review, review_notes (latest concise notes, optionally filtered by module and pulse_run_id), goal_work (Goal Work items and constraint challenges, newest first), goal_status (optional aggregate: DB measurement facts/history, recorded execution evidence, current scheduler flags, the previous goal check, memory and decisions; agents judge progress and work contribution), step_concerns (CONCERNS: lines steps wrote since the previous Pulse; leads, not findings), or step_outputs (each step's own summary of its latest runs side by side, optionally one step_id; shows steps that complete but do no new work)."},
					"pulse_run_id":   map[string]interface{}{"type": "string", "description": "Optional saved Pulse run id for historical review notes."},
					"module":         map[string]interface{}{"type": "string", "description": "Optional owning-module filter for backlog or review_notes (omit for all modules). Required for view=\"review\"."},
					"review_run_id":  map[string]interface{}{"type": "string", "description": "Required for view=\"review\": the exact run id from the saved historical review."},
					"step_id":        map[string]interface{}{"type": "string", "description": "For view=\"step_outputs\" only: limit the lineup to one plan step id."},
					"detail":         map[string]interface{}{"type": "string", "enum": []string{"compact", "full"}, "description": "For view=\"backlog\" only. compact (default) returns active issues/observations and the closed canonical issue index for semantic reuse. full requires issue_ids and returns complete lifecycle evidence only for those ids."},
					"issue_ids": map[string]interface{}{
						"type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true,
						"items":       map[string]interface{}{"type": "string", "pattern": "^PUL-[A-Za-z0-9]+$"},
						"description": "For view=\"backlog\" with detail=\"full\": 1-20 exact public PUL ids selected from the compact issues or observations index.",
					},
					"limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 50, "description": "For historical review_notes; defaults to the latest 3."},
				},
				"required": []string{"workspace_path", "view"},
			}),
		},
	}
	executors := map[string]interface{}{
		"record_pulse_finding": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			pulseRunID, _ := args["pulse_run_id"].(string)
			pulseRunID = pulseRunIDForSession(ctx, pulseRunID)
			module, _ := args["module"].(string)
			if err := validatePulseToolRunID(ctx, pulseRunID); err != nil {
				return "", err
			}
			reproduction := step_based_workflow.PulseFindingReproduction{}
			if raw, ok := args["reproduction"].(map[string]interface{}); ok {
				reproduction.Safe, _ = raw["safe"].(bool)
				reproduction.Setup, _ = raw["setup"].(string)
				reproduction.Action, _ = raw["action"].(string)
				reproduction.Expected, _ = raw["expected"].(string)
				reproduction.Observed, _ = raw["observed"].(string)
				reproduction.Limitations, _ = raw["limitations"].(string)
			}
			input := step_based_workflow.PulseReviewFindingInput{IssueID: stringToolArg(args, "issue_id"), StepID: stringToolArg(args, "step_id"), Concern: stringToolArg(args, "concern"), Module: module, HumanInputID: stringToolArg(args, "human_input_id"), PulseFindingDetails: step_based_workflow.PulseFindingDetails{
				TargetKey: stringToolArg(args, "target_key"), IssueKind: stringToolArg(args, "issue_kind"),
				RecommendedRoute: stringToolArg(args, "recommended_route"), NextCheck: stringToolArg(args, "next_check"), Classification: stringToolArg(args, "classification"),
				Severity: stringToolArg(args, "severity"), Summary: stringToolArg(args, "summary"), Impact: stringToolArg(args, "impact"), Workaround: stringToolArg(args, "workaround"),
				Evidence: stringSliceFromToolArg(args["evidence"]), Reproduction: reproduction,
			}}
			if err := checkPulsePlainText("impact, evidence and reproduction",
				plainTextField{name: "concern", text: input.Concern, maxLen: 140},
				plainTextField{name: "summary", text: input.Summary, maxLen: 500}); err != nil {
				return "", err
			}
			reviewRunID := pulseReviewRunIDForSession(ctx, pulseRunID)
			record, err := step_based_workflow.RecordPulseReviewFinding(ctx, workspacePath, pulseRunID, reviewRunID, input)
			if err != nil {
				return "", err
			}
			encoded, _ := json.Marshal(record)
			return string(encoded), nil
		},
		"merge_pulse_issues": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			merged, err := step_based_workflow.MergePulseFindingIssues(ctx, workspacePath,
				stringToolArg(args, "canonical_issue_id"), stringSliceFromToolArg(args["duplicate_issue_ids"]), stringToolArg(args, "reason"))
			if err != nil {
				return "", err
			}
			payload, _ := json.Marshal(map[string]interface{}{"status": "merged", "merged_count": merged, "canonical_issue_id": stringToolArg(args, "canonical_issue_id")})
			return string(payload), nil
		},
		"get_pulse_state": func(ctx context.Context, args map[string]interface{}) (string, error) {
			workspacePath, _ := args["workspace_path"].(string)
			view, _ := args["view"].(string)
			view = strings.ToLower(strings.TrimSpace(view))
			if strings.TrimSpace(workspacePath) == "" {
				// Every view opens the workflow's own db/db.sqlite. Without the
				// path there is no database to read, and the empty-result answer
				// reads as "nothing is tracked" rather than "you addressed nothing".
				return "", fmt.Errorf("get_pulse_state requires workspace_path: the workflow-relative path, e.g. Workflow/social-media")
			}
			switch view {
			case pulseStateViewBacklog:
				module, _ := args["module"].(string)
				return readPulseBacklogViewWithOptions(ctx, workspacePath, module, stringToolArg(args, "detail"), stringSliceFromToolArg(args["issue_ids"]))
			case pulseStateViewReview:
				module, _ := args["module"].(string)
				reviewRunID, _ := args["review_run_id"].(string)
				return readPulseReviewView(ctx, workspacePath, reviewRunID, module)
			case "review_notes":
				return readPulseReviewNotesView(ctx, workspacePath, stringToolArg(args, "module"), stringToolArg(args, "pulse_run_id"), intToolArg(args, "limit"))
			case "goal_work":
				return readPulseGoalWorkView(ctx, workspacePath, intToolArg(args, "limit"))
			case "goal_status":
				return readGoalStatusView(ctx, workspacePath)
			case "step_concerns":
				return readStepConcernsView(ctx, workspacePath)
			case "step_outputs":
				return readStepOutputsView(workspacePath, stringToolArg(args, "step_id"))
			default:
				return "", fmt.Errorf("view %q is not a valid saved Pulse view. Choose one of: %s", view, strings.Join(pulseStateViewValues, ", "))
			}
		},
	}
	categories := map[string]string{
		"record_pulse_finding": "workflow",
		"merge_pulse_issues":   "workflow",

		"get_pulse_state":     "workflow",
		"resolve_run_concern": "workflow",
	}
	resolveConcernTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "resolve_run_concern",
			Description: "Acknowledge or reject a Pulse issue returned by get_pulse_state(view=\"backlog\"). Use rejected only when evidence shows it is not a problem; rejected concerns stay closed if the same behavior recurs. Use acknowledged when it is real but deliberately deferred. For a real repair, ask Builder to verify the result and record the change and evidence in Goal Work. Absence is never evidence of a fix.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/social-media."},
					"issue_id":       map[string]interface{}{"type": "string", "description": "The visible issue_id from get_pulse_state(view=\"backlog\", detail=\"compact\")."},
					"status":         map[string]interface{}{"type": "string", "enum": []string{"acknowledged", "rejected"}},
					"note":           map[string]interface{}{"type": "string", "description": "Short justification recorded with the judgement."},
				},
				"required": []string{"workspace_path", "issue_id", "status"},
			}),
		},
	}
	executors["resolve_run_concern"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		workspacePath, _ := args["workspace_path"].(string)
		issueID := stringToolArg(args, "issue_id")
		status, _ := args["status"].(string)
		note, _ := args["note"].(string)
		if strings.EqualFold(strings.TrimSpace(status), step_based_workflow.ConcernStatusResolved) {
			return "", fmt.Errorf("Use Builder to verify a repair and record its evidence in Goal Work before closing a real finding")
		}
		finding, err := step_based_workflow.ResolvePulseFindingIssueID(ctx, workspacePath, issueID)
		if err != nil {
			return "", err
		}
		if err := step_based_workflow.ResolveRunConcern(ctx, workspacePath, finding.Fingerprint, status, "pulse", note); err != nil {
			return "", err
		}
		return fmt.Sprintf("Issue %s marked %s.", step_based_workflow.NewPulseIssue(finding).ID, status), nil
	}

	goalWorkTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_goal_work",
			Description: "Record Goal Work: work Pulse does to move the user's goals, and evidence-backed challenges to soul.md constraints. Read get_pulse_state(view=\"goal_work\") first and pass item_id to update an existing item (for example to add the effect once the metric shows it). status: idea (a gap found, not acted on yet), in_progress, needs_user (prepared, waiting on a decision; needs decision_id from create_human_input_request), done (needs action_taken: what Pulse actually did), dropped. Put prepared work under pulse/work/<date>/ and list those paths in links. Name the metric the item should move and when to check it. Later set effect to worked, no_effect or unclear with a short effect_note. For kind=constraint_challenge give constraint_text and constraint_class: boundary (safety, legal, account safety, no fabrication: ask for clarification only, never propose loosening), choice (theme, format, cadence, channel: challenge with evidence and a small reversible test), unconfirmed (no user provenance: ask the user to confirm or remove). A constraint stays binding until the user answers.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path":     map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/linkedin."},
					"pulse_run_id":       map[string]interface{}{"type": "string", "description": "The current Pulse run id."},
					"item_id":            map[string]interface{}{"type": "string", "description": "Existing GW- id to update; omit to create."},
					"kind":               map[string]interface{}{"type": "string", "enum": []string{"goal_work", "constraint_challenge"}},
					"title":              map[string]interface{}{"type": "string", "description": "The gap in plain words, or the constraint being challenged."},
					"detail":             map[string]interface{}{"type": "string", "description": "Why it matters for the goal and the evidence, briefly."},
					"status":             map[string]interface{}{"type": "string", "enum": []string{"idea", "in_progress", "needs_user", "done", "dropped"}},
					"action_taken":       map[string]interface{}{"type": "string", "description": "What Pulse actually did. Required for done."},
					"links":              map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "maxItems": 20, "description": "Workspace paths of the prepared work, usually under pulse/work/."},
					"metric":             map[string]interface{}{"type": "string", "description": "Goal metric id or name this should move."},
					"expected_direction": map[string]interface{}{"type": "string", "description": "e.g. increase, decrease."},
					"check_at":           map[string]interface{}{"type": "string", "description": "When the effect or an unresolved approach question should be revisited (RFC3339 or date). The single Pulse check respects this date within its pace; update it deliberately after an inconclusive assessment."},
					"effect":             map[string]interface{}{"type": "string", "enum": []string{"worked", "no_effect", "unclear"}},
					"effect_note":        map[string]interface{}{"type": "string"},
					"decision_id":        map[string]interface{}{"type": "string", "description": "create_human_input_request id when the user must approve or answer."},
					"constraint_text":    map[string]interface{}{"type": "string", "description": "The soul.md constraint, for constraint_challenge."},
					"constraint_class":   map[string]interface{}{"type": "string", "enum": []string{"boundary", "choice", "unconfirmed"}},
				},
				"required": []string{"workspace_path", "pulse_run_id"},
			}),
		},
	}
	executors["record_pulse_goal_work"] = recordPulseGoalWorkFromToolArgs
	categories["record_pulse_goal_work"] = "workflow"
	fastRequestTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_fast_request",
			Description: "From a normal run's finalizer only: ask for the workflow's next full Pulse as soon as its minimum-interval guard allows, because this run produced material new evidence (a serious failure or regression, a plan/schema/evaluation change, abnormal cost or runtime) where waiting for the chosen Pulse time is worse. Routine runs must not call it. It never runs a review inline or changes any schedule.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path": map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/linkedin."},
					"run_id":         map[string]interface{}{"type": "string", "description": "The finalizer's run_id."},
					"reason":         map[string]interface{}{"type": "string", "minLength": 1, "description": "The concrete material evidence."},
					"evidence":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "maxItems": 10, "description": "Bounded artifact references."},
				},
				"required": []string{"workspace_path", "run_id", "reason"},
			}),
		},
	}
	executors["record_pulse_fast_request"] = recordPulseFastRequestFromToolArgs
	categories["record_pulse_fast_request"] = "workflow"
	goalCheckTool := llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "record_pulse_goal_check",
			Description: "Record the goal check (the Pulse's first job, PLAT-697): your verdict from the evidence you chose to read or discuss with Builder; get_pulse_state(view=\"goal_status\") is optional. status: on_track (measured recently, moving or holding as expected, goal work running), at_risk, off_track, or not_measured. It is shown at the top of the Pulse tab. The code-computed alarms are stored with it. Include your conclusion and recommendation when the approach was reviewed. Paused producing schedules do not suppress future checks; avoid repeating an unchanged pause notification.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_path":      map[string]interface{}{"type": "string", "description": "Workflow-relative path, e.g. Workflow/linkedin."},
					"pulse_run_id":        map[string]interface{}{"type": "string", "description": "The current Pulse run id."},
					"status":              map[string]interface{}{"type": "string", "enum": []string{"on_track", "at_risk", "off_track", "not_measured"}},
					"key_number":          map[string]interface{}{"type": "string", "description": "The key goal number and its date in plain words, e.g. \"+2 subscribers on 7 Oct\"."},
					"summary":             map[string]interface{}{"type": "string", "minLength": 1, "description": "Two or three short plain sentences for the owner: result and judgment, obstacle or uncertainty, and next action. Keep technical inventories in existing detail records."},
					"action_taken":        map[string]interface{}{"type": "string", "description": "What you did about it, if anything."},
					"decision_id":         map[string]interface{}{"type": "string", "description": "The create_human_input_request id when you asked the owner."},
					"next_check_in_hours": map[string]interface{}{"type": "number", "minimum": 1, "maximum": 168, "description": "When to check the goal next, in hours from now; bounded to 6–168 hours (24 when omitted). Pick the earliest useful outcome or approach checkpoint as well as repairs. Earlier Goal Work check_at dates share these bounds. When Pulse is off, no next check is scheduled."},
					"next_check_reason":   map[string]interface{}{"type": "string", "description": "Why that time, in a few words, e.g. \"after tomorrow's 09:00 publish run\"."},
				},
				"required": []string{"workspace_path", "status", "summary"},
			}),
		},
	}
	executors["record_pulse_goal_check"] = recordPulseGoalCheckFromToolArgs
	categories["record_pulse_goal_check"] = "workflow"
	goalLeadTools := createGoalLeadTools()
	for _, tool := range goalLeadTools {
		categories[tool.Function.Name] = "workflow"
	}
	executors["record_pulse_recommendation"] = recordPulseRecommendationFromToolArgs
	executors["record_pulse_decision_outcome"] = recordPulseDecisionOutcomeFromToolArgs
	executors["record_pulse_goal_memory"] = recordPulseGoalMemoryFromToolArgs
	// Phase 4: focus areas and QA requests.
	for _, tool := range createGoalLeadChatKindTools() {
		goalLeadTools = append(goalLeadTools, tool)
		categories[tool.Function.Name] = "workflow"
	}
	executors["record_pulse_focus_area"] = recordPulseFocusAreaFromToolArgs

	return append([]llmtypes.Tool{recordFindingTool, mergeIssuesTool, stateTool, resolveConcernTool, fastRequestTool, goalWorkTool, goalCheckTool}, goalLeadTools...), executors, categories
}

func stringToolArg(args map[string]interface{}, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func intToolArg(args map[string]interface{}, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

// The three reads get_pulse_state merges. One tool with a named view rather
// than three tools whose names shared no rule: the agent that invented
// close_pulse_fix_attempt and resolve_human_input was guessing across a surface
// larger than the number of concepts in it.
const (
	pulseStateViewBacklog = "backlog"
	pulseStateViewReview  = "review"
)

// pulseStateViewValues is the closed view set shared by the schema enum, the
// accept check, and the rejection message.
var pulseStateViewValues = []string{pulseStateViewBacklog, pulseStateViewReview, "review_notes", "goal_work", "goal_status", "step_concerns", "step_outputs"}

const maxPulseBacklogDetailIDs = 20

func readPulseBacklogViewWithOptions(ctx context.Context, workspacePath, module, detail string, issueIDs []string) (string, error) {
	module = normalizePulseModule(module)
	if module != "" && !validPulseModules[module] {
		return "", fmt.Errorf("module %q is not a valid Pulse module. Must be one of: %s. Omit module entirely to load the complete backlog", module, pulseModuleList())
	}
	detail = strings.ToLower(strings.TrimSpace(detail))
	if detail == "" {
		detail = "compact"
	}
	if detail != "compact" && detail != "full" {
		return "", fmt.Errorf("detail %q is not valid for get_pulse_state(view=\"backlog\"); use compact or full", detail)
	}
	issueIDs = normalizePulseIssueIDs(issueIDs)
	if detail == "compact" && len(issueIDs) > 0 {
		return "", fmt.Errorf("issue_ids is only valid with detail=\"full\"; read the compact index without ids first")
	}
	if detail == "full" {
		if len(issueIDs) == 0 {
			return "", fmt.Errorf("detail=\"full\" requires 1-%d issue_ids selected from the compact backlog index; broad full-history reads are intentionally disabled", maxPulseBacklogDetailIDs)
		}
		if len(issueIDs) > maxPulseBacklogDetailIDs {
			return "", fmt.Errorf("detail=\"full\" accepts at most %d issue_ids, got %d", maxPulseBacklogDetailIDs, len(issueIDs))
		}
	}
	findings, err := step_based_workflow.LoadPulseFindingLifecycles(ctx, workspacePath, module, -1)
	if err != nil {
		return "", err
	}
	issues, observations := step_based_workflow.SplitPulseFindingLifecycles(findings)
	if detail == "full" {
		selected, missing, ambiguous := selectPulseLifecyclesByIssueID(findings, issueIDs)
		if len(missing) > 0 {
			return "", fmt.Errorf("unknown issue_ids: %s; refresh get_pulse_state(view=\"backlog\", detail=\"compact\") and use exact ids", strings.Join(missing, ", "))
		}
		if len(ambiguous) > 0 {
			return "", fmt.Errorf("ambiguous issue_ids: %s; these public prefixes match multiple lifecycle rows and require backend identity repair before mutation", strings.Join(ambiguous, ", "))
		}
		payload, marshalErr := json.Marshal(map[string]interface{}{
			"detail":  "full",
			"records": pulseBacklogAgentProjection(selected),
			"total":   len(selected),
			"note":    "Complete lifecycle evidence for only the requested public PUL ids. No unrequested issue or observation history was loaded into the agent response.",
		})
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	}

	activeIssues := activePulseFindingLifecycles(issues)
	activeObservations := activePulseFindingLifecycles(observations)
	closedIssues := make([]step_based_workflow.PulseFindingLifecycle, 0, len(issues)-len(activeIssues))
	for _, finding := range issues {
		if !pulseFindingLifecycleActive(finding) {
			closedIssues = append(closedIssues, finding)
		}
	}
	payload, marshalErr := json.Marshal(map[string]interface{}{
		"detail":            "compact",
		"issues":            pulseLifecycleAgentProjection(activeIssues, "issue_id"),
		"closed_issues":     pulseLifecycleAgentProjection(closedIssues, "issue_id"),
		"observations":      []map[string]interface{}{},
		"total":             len(activeIssues),
		"issue_total":       len(activeIssues),
		"observation_total": len(activeObservations),
		"summary":           pulseBacklogSummary(issues, observations),
		"note":              "Compact issue register: compare new evidence with active and closed issue text, reuse the matching issue_id, and request detail=\"full\" only for candidates. Historical observations are retained for audit but are not supplied as an active review queue; Technical Review reads retained run artifacts and deterministic receipts directly.",
		"navigation": map[string]string{
			"issues":        "Canonical repair roots. Start here and reuse the same PUL issue_id for the same semantic root cause.",
			"closed_issues": "Previously handled roots. Matching new evidence reopens the same root rather than creating a duplicate.",
			"observations":  "Historical audit evidence only. It is not an active reviewer queue; Technical Review reads retained run artifacts and deterministic receipts directly.",
		},
	})
	if marshalErr != nil {
		return "", marshalErr
	}
	return string(payload), nil
}

func normalizePulseIssueIDs(issueIDs []string) []string {
	seen := make(map[string]bool, len(issueIDs))
	normalized := make([]string, 0, len(issueIDs))
	for _, issueID := range issueIDs {
		issueID = strings.ToUpper(strings.TrimSpace(issueID))
		if issueID == "" || seen[issueID] {
			continue
		}
		seen[issueID] = true
		normalized = append(normalized, issueID)
	}
	return normalized
}

func selectPulseLifecyclesByIssueID(findings []step_based_workflow.PulseFindingLifecycle, issueIDs []string) ([]step_based_workflow.PulseFindingLifecycle, []string, []string) {
	byID := make(map[string][]step_based_workflow.PulseFindingLifecycle, len(findings))
	for _, finding := range findings {
		issueID := strings.ToUpper(step_based_workflow.NewPulseIssue(finding).ID)
		byID[issueID] = append(byID[issueID], finding)
	}
	selected := make([]step_based_workflow.PulseFindingLifecycle, 0, len(issueIDs))
	missing := make([]string, 0)
	ambiguous := make([]string, 0)
	for _, issueID := range issueIDs {
		matches := byID[issueID]
		if len(matches) == 0 {
			missing = append(missing, issueID)
			continue
		}
		if len(matches) > 1 {
			ambiguous = append(ambiguous, issueID)
			continue
		}
		selected = append(selected, matches[0])
	}
	return selected, missing, ambiguous
}

// pulseBacklogSummary is intentionally derived from durable lifecycle rows,
// not a model receipt. It lets a reviewer and the consolidation command report
// whether the active backlog actually moved rather than celebrating activity
// while the number of unresolved root causes rises.
func pulseBacklogSummary(issues, observations []step_based_workflow.PulseFindingLifecycle) map[string]interface{} {
	byStatus := map[string]int{}
	active, merged := 0, 0
	for _, finding := range issues {
		status := strings.TrimSpace(finding.Status)
		if status == "" {
			status = "unknown"
		}
		byStatus[status]++
		if finding.Details != nil && strings.TrimSpace(finding.Details.MergedIntoIssueID) != "" {
			merged++
		}
		switch status {
		case step_based_workflow.ConcernStatusResolved, step_based_workflow.ConcernStatusRejected, step_based_workflow.ConcernStatusExternalActionRequired:
		default:
			active++
		}
	}
	observationByStatus := map[string]int{}
	activeObservations := 0
	for _, observation := range observations {
		status := strings.TrimSpace(observation.Status)
		if status == "" {
			status = "unknown"
		}
		observationByStatus[status]++
		if pulseFindingLifecycleActive(observation) {
			activeObservations++
		}
	}
	return map[string]interface{}{
		"active_count": active, "terminal_count": len(issues) - active,
		"merged_duplicate_count": merged, "by_status": byStatus,
		"active_observation_count":   activeObservations,
		"terminal_observation_count": len(observations) - activeObservations,
		"observations_by_status":     observationByStatus,
	}
}

func pulseFindingLifecycleActive(finding step_based_workflow.PulseFindingLifecycle) bool {
	switch strings.TrimSpace(finding.Status) {
	case step_based_workflow.ConcernStatusResolved, step_based_workflow.ConcernStatusRejected, step_based_workflow.ConcernStatusExternalActionRequired:
		return false
	default:
		return true
	}
}

func activePulseFindingLifecycles(findings []step_based_workflow.PulseFindingLifecycle) []step_based_workflow.PulseFindingLifecycle {
	active := make([]step_based_workflow.PulseFindingLifecycle, 0, len(findings))
	for _, finding := range findings {
		if pulseFindingLifecycleActive(finding) {
			active = append(active, finding)
		}
	}
	return active
}

func pulseLifecycleAgentProjection(findings []step_based_workflow.PulseFindingLifecycle, idKey string) []map[string]interface{} {
	projected := make([]map[string]interface{}, 0, len(findings))
	for _, finding := range findings {
		issue := step_based_workflow.NewPulseIssue(finding)
		card := map[string]interface{}{
			idKey: issue.ID, "kind": finding.Kind, "step_id": finding.StepID,
			"phase": finding.Phase, "group_name": finding.GroupName, "summary": issue.Title,
			"first_seen_run": finding.FirstSeenRun, "first_seen_at": finding.FirstSeenAt,
			"last_seen_run": finding.LastSeenRun, "last_seen_at": finding.LastSeenAt,
			"seen_count": finding.SeenCount, "status": finding.Status, "priority": issue.Priority,
			"module": issue.Module, "repair_eligible": finding.Kind == step_based_workflow.PulseFindingKindIssue && issue.Status == "backlog",
		}
		if finding.Details != nil {
			card["target_key"] = finding.Details.TargetKey
			card["next_check"] = finding.Details.NextCheck
		}
		// A deferred issue must not wait silently: after one deferral the next
		// Pulse asks Builder for a repair or a genuine owner decision.
		if deferred := pulseFindingTimesDeferred(finding); deferred > 0 {
			card["times_deferred"] = deferred
			card["deferral_note"] = fmt.Sprintf("Already deferred %d time(s). Do not queue it again: repair it in this pass, or create a decision if only the user can choose.", deferred)
		}
		if len(finding.Verification) > 0 {
			latest := finding.Verification[0]
			card["latest_verification"] = map[string]interface{}{
				"verdict": latest.Verdict, "check": latest.Check, "verified_at": latest.At,
			}
		}
		projected = append(projected, card)
	}
	return projected
}

// pulseFindingTimesDeferred counts how often a finding was queued for a later
// pass instead of being repaired.
func pulseFindingTimesDeferred(finding step_based_workflow.PulseFindingLifecycle) int {
	count := 0
	for _, event := range finding.Events {
		if event.EventType == step_based_workflow.ConcernStatusQueuedForEngineering {
			count++
		}
	}
	return count
}

// pulseBacklogAgentProjection removes lifecycle implementation keys from the
// coding-agent tool response. The REST/UI projection can retain them as stable
// React keys while the agent receives only the one public PUL issue identity.
func pulseBacklogAgentProjection(findings []step_based_workflow.PulseFindingLifecycle) interface{} {
	raw, err := json.Marshal(findings)
	if err != nil {
		return []interface{}{}
	}
	var projected interface{}
	if err := json.Unmarshal(raw, &projected); err != nil {
		return []interface{}{}
	}
	var redact func(interface{})
	redact = func(value interface{}) {
		switch typed := value.(type) {
		case map[string]interface{}:
			delete(typed, "fingerprint")
			delete(typed, "attempt_id")
			delete(typed, "finding_id")
			for _, nested := range typed {
				redact(nested)
			}
		case []interface{}:
			for _, nested := range typed {
				redact(nested)
			}
		}
	}
	redact(projected)
	return projected
}

func readPulseReviewView(ctx context.Context, workspacePath, reviewRunID, module string) (string, error) {
	reviewRunID = strings.TrimSpace(reviewRunID)
	module = strings.TrimSpace(module)
	if reviewRunID == "" || module == "" {
		return "", fmt.Errorf("get_pulse_state(view=%q) requires both review_run_id and module (got %s); use the pair from the saved historical review",
			pulseStateViewReview,
			pulseArgArrivalReport("review_run_id", reviewRunID, "module", module))
	}
	if err := step_based_workflow.ValidatePulseReviewIdentity(reviewRunID, module); err != nil {
		return "", err
	}
	receipt, err := step_based_workflow.LoadPulseReviewReceiptForRun(ctx, workspacePath, reviewRunID, module)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no historical Pulse review is saved for review_run_id=%q module=%q; current Pulse records goal checks and useful work", reviewRunID, module)
	}
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]interface{}{
		"module":             receipt.Module,
		"review_run_id":      receipt.ReviewRunID,
		"pulse_run_id":       receipt.PulseRunID,
		"status":             receipt.Status,
		"verdict":            receipt.Verdict,
		"finding_count":      receipt.FindingCount,
		"verification_count": receipt.VerificationCount,
		"verifications":      receipt.Verifications,
		"note":               "Historical evidence only. Current Pulse work is recorded under goal_work and goal_status; saved findings are available under backlog.",
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// pulseArgArrivalReport names what actually arrived for each of a pair of
// required arguments. "requires both" alone does not say which one was missing.
func pulseArgArrivalReport(nameA, valueA, nameB, valueB string) string {
	state := func(value string) string {
		if strings.TrimSpace(value) == "" {
			return "missing"
		}
		return "set"
	}
	return fmt.Sprintf("%s=%s, %s=%s", nameA, state(valueA), nameB, state(valueB))
}

// pulseArgTypeName describes what actually arrived for a tool argument. "must
// be a string" alone does not say whether the caller sent a number, an object,
// or nothing at all.
func pulseArgTypeName(raw interface{}) string {
	switch value := raw.(type) {
	case nil:
		return "nothing"
	case string:
		if strings.TrimSpace(value) == "" {
			return "an empty string"
		}
		return "a string"
	case bool:
		return "a boolean"
	case float64, int, int64:
		return "a number"
	case []interface{}:
		return "an array"
	case map[string]interface{}:
		return "an object"
	default:
		return fmt.Sprintf("%T", raw)
	}
}

func strictStringSliceToolArg(raw interface{}) ([]string, error) {
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("must be an array of strings such as [\"first\", \"second\"] (one string goes in a one-item array), got %s", pulseArgTypeName(raw))
	}
	out := make([]string, 0, len(arr))
	for index, item := range arr {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("item %d must be a string, got %s", index, pulseArgTypeName(item))
		}
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

func stringSliceFromToolArg(raw interface{}) []string {
	if values, ok := raw.([]string); ok {
		return normalizePulseEvidence(values)
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func normalizePulseModule(module string) string {
	return pulsemodules.Normalize(module)
}

func normalizePulseEvidence(evidence []string) []string {
	out := make([]string, 0, len(evidence))
	seen := map[string]bool{}
	for _, item := range evidence {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
