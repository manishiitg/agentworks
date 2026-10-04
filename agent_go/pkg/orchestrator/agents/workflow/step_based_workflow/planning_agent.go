package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/PaesslerAG/jsonpath"
)

// FlexibleContextOutput handles both string and array types for context_output field
// This prevents JSON parsing errors when LLM returns arrays instead of strings
type FlexibleContextOutput string

// PlanOrchestrationRoute represents a possible route/sub-agent for planning
type PlanOrchestrationRoute struct {
	RouteID       string            `json:"route_id"`                  // Unique ID for this route
	RouteName     string            `json:"route_name"`                // Human-readable name
	Condition     string            `json:"condition"`                 // Condition description (e.g., "If error is authentication-related")
	SubAgentStep  PlanStepInterface `json:"sub_agent_step"`            // The sub-agent step to execute (private, not in main workflow) - must have "type" field
	OrphanStepRef string            `json:"orphan_step_ref,omitempty"` // Optional: reference to a reusable orphan step definition in the same plan
}

// MarshalJSON implements custom marshaling for PlanOrchestrationRoute
// This is needed to properly handle the SubAgentStep field which is a PlanStepInterface
func (r PlanOrchestrationRoute) MarshalJSON() ([]byte, error) {
	type routeJSON struct {
		RouteID       string          `json:"route_id"`
		RouteName     string          `json:"route_name"`
		Condition     string          `json:"condition"`
		SubAgentStep  json.RawMessage `json:"sub_agent_step,omitempty"`
		OrphanStepRef string          `json:"orphan_step_ref,omitempty"`
	}

	result := routeJSON{
		RouteID:       r.RouteID,
		RouteName:     r.RouteName,
		Condition:     r.Condition,
		OrphanStepRef: r.OrphanStepRef,
	}

	// For reusable orphan step references, persist only the reference in plan.json.
	if r.OrphanStepRef != "" {
		result.SubAgentStep = nil
	} else if r.SubAgentStep != nil {
		subAgentJSON, err := json.Marshal(r.SubAgentStep)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal sub_agent_step: %w", err)
		}
		result.SubAgentStep = subAgentJSON
	}

	return json.Marshal(result)
}

// UnmarshalJSON implements custom unmarshaling for PlanOrchestrationRoute
// This is needed to properly handle the SubAgentStep field which is a PlanStepInterface
func (r *PlanOrchestrationRoute) UnmarshalJSON(data []byte) error {
	// First, unmarshal into a temporary struct to extract SubAgentStep as raw JSON
	var temp struct {
		RouteID       string          `json:"route_id"`
		RouteName     string          `json:"route_name"`
		Condition     string          `json:"condition"`
		SubAgentStep  json.RawMessage `json:"sub_agent_step"`
		OrphanStepRef string          `json:"orphan_step_ref,omitempty"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal orchestration route: %w", err)
	}

	// Copy basic fields
	r.RouteID = temp.RouteID
	r.RouteName = temp.RouteName
	r.Condition = temp.Condition
	r.OrphanStepRef = temp.OrphanStepRef

	// Unmarshal nested SubAgentStep
	if len(temp.SubAgentStep) > 0 {
		// Check if it's null
		subAgentStepStr := string(temp.SubAgentStep)
		if subAgentStepStr != "null" {
			step, err := unmarshalStepFromJSON(temp.SubAgentStep)
			if err != nil {
				return fmt.Errorf("failed to unmarshal sub_agent_step: %w", err)
			}
			r.SubAgentStep = step
		} else {
			r.SubAgentStep = nil
		}
	} else {
		r.SubAgentStep = nil
	}

	return nil
}

// UnmarshalJSON implements custom unmarshaling for FlexibleContextOutput
// Handles both string and array formats to prevent parsing errors
func (f *FlexibleContextOutput) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexibleContextOutput(s)
		return nil
	}

	// Try to unmarshal as array
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		// Join array elements with comma and space
		*f = FlexibleContextOutput(strings.Join(arr, ", "))
		return nil
	}

	// If both fail, return the error from string unmarshal
	return fmt.Errorf(fmt.Sprintf("failed to unmarshal context_output as string or array"), nil)
}

// String returns the string value
func (f FlexibleContextOutput) String() string {
	return string(f)
}

// AgentLLMConfig represents LLM configuration for an agent
type AgentLLMConfig struct {
	ConnectionID   string                 `json:"connection_id,omitempty"`
	PublishedLLMID string                 `json:"published_llm_id,omitempty"` // Optional published LLM registry reference
	Provider       string                 `json:"provider,omitempty"`         // coding-agent CLI, e.g. "claude-code", "codex-cli", "pi-cli"
	ModelID        string                 `json:"model_id,omitempty"`         // e.g., "gpt-4o", "claude-3-5-sonnet-20241022"
	Options        map[string]interface{} `json:"options,omitempty"`          // Provider-specific runtime options
}

// ValidationSchema represents structured validation rules for step outputs
type ValidationSchema struct {
	Files []FileValidationRule `json:"files,omitempty"`
	// DB gates the step on read-only SQL queries against db/db.sqlite — the source
	// of truth — instead of (or alongside) a hand-written output file. Lets a step
	// be validated on what it actually produced in the db, so context_output files
	// don't have to exist just to be validatable.
	DB []DBValidationRule `json:"db,omitempty"`
}

// DBValidationRule asserts on a read-only SQL query against db/db.sqlite.
type DBValidationRule struct {
	Name    string                `json:"name,omitempty"`     // label shown in failure feedback
	SQL     string                `json:"sql"`                // read-only SELECT against db/db.sqlite
	MinRows *int                  `json:"min_rows,omitempty"` // result must have >= N rows
	MaxRows *int                  `json:"max_rows,omitempty"` // result must have <= N rows
	Checks  []JSONValidationCheck `json:"checks,omitempty"`   // JSONPath asserts on the FIRST result row (row = object keyed by column)
}

// UnmarshalJSON implements custom unmarshaling to fix double-escaped regex patterns
// This fixes patterns at the source (during JSON unmarshaling) rather than during validation
func (vs *ValidationSchema) UnmarshalJSON(data []byte) error {
	// Use type alias to avoid infinite recursion
	type Alias ValidationSchema
	alias := (*Alias)(vs)

	// Unmarshal normally first
	if err := json.Unmarshal(data, alias); err != nil {
		return err
	}

	// Fix double-escaped patterns in all checks, file-based and DB-based alike
	// -- both run the same JSONValidationCheck through the same pattern
	// matcher at runtime (pre_validation.go / pre_validation_db.go).
	forEachSchemaCheck(vs, func(_ string, check *JSONValidationCheck) {
		if check.Pattern != "" {
			check.Pattern = fixDoubleEscapedPattern(check.Pattern)
		}
	})

	return nil
}

// forEachSchemaCheck calls fn once for every JSONValidationCheck in the
// schema, across both file-based rules (schema.Files[].JSONChecks) and
// DB-based rules (schema.DB[].Checks) -- the same JSONValidationCheck type,
// evaluated by the same validateJSONCheck/validatePattern logic at runtime
// (pre_validation.go, pre_validation_db.go). label identifies which rule the
// check belongs to, for error messages. Every write-time schema validator
// must walk both rule kinds through this helper: an unsatisfiable or
// malformed check is exactly as real in a DB rule as in a file rule.
func forEachSchemaCheck(schema *ValidationSchema, fn func(label string, check *JSONValidationCheck)) {
	if schema == nil {
		return
	}
	for i := range schema.Files {
		fileRule := &schema.Files[i]
		for j := range fileRule.JSONChecks {
			fn(fmt.Sprintf("File '%s'", fileRule.FileName), &fileRule.JSONChecks[j])
		}
	}
	for i := range schema.DB {
		dbRule := &schema.DB[i]
		label := strings.TrimSpace(dbRule.Name)
		if label == "" {
			label = fmt.Sprintf("DB check #%d", i+1)
		}
		for j := range dbRule.Checks {
			fn(fmt.Sprintf("DB rule '%s'", label), &dbRule.Checks[j])
		}
	}
}

// FileValidationRule represents validation rules for a specific file
type FileValidationRule struct {
	FileName   string                `json:"file_name"`             // e.g., "results.json"
	MustExist  bool                  `json:"must_exist"`            // File must exist
	JSONChecks []JSONValidationCheck `json:"json_checks,omitempty"` // JSON structure checks
}

// JSONValidationCheck represents a validation check on JSON content
type JSONValidationCheck struct {
	Path             string           `json:"path"`                        // JSONPath, e.g., "$.status", "$.databases[0].name"
	MustExist        bool             `json:"must_exist"`                  // Key/path must exist
	ValueType        string           `json:"value_type,omitempty"`        // "string", "number", "boolean", "array", "object"
	MinLength        *int             `json:"min_length,omitempty"`        // For arrays/strings
	MaxLength        *int             `json:"max_length,omitempty"`        // For arrays/strings
	Pattern          string           `json:"pattern,omitempty"`           // Regex for format validation
	MinValue         *float64         `json:"min_value,omitempty"`         // For numbers
	MaxValue         *float64         `json:"max_value,omitempty"`         // For numbers
	ConsistencyCheck *ConsistencyRule `json:"consistency_check,omitempty"` // Compare with other fields
}

// ConsistencyRule represents a consistency check between fields
type ConsistencyRule struct {
	Type            string `json:"type"`              // "equals", "greater_than", "less_than", "array_length", "in_array"
	CompareWithPath string `json:"compare_with_path"` // JSONPath to compare with
}

// AgentConfigs represents per-agent configuration for a step
type AgentConfigs struct {
	ExecutionLLM                 *AgentLLMConfig `json:"execution_llm,omitempty"`
	ExecutionLLMReason           string          `json:"execution_llm_reason,omitempty"`            // PLAT-060. Why this step is pinned to a specific model. Required whenever execution_llm is set — update_step_config rejects the pin without it. A pin outranks execution_tier entirely, so it silently overrides every tier decision above it. Cite the owning architecture_review finding id, and the human_input_id when the pin was user-approved.
	ExecutionTier                string          `json:"execution_tier,omitempty"`                  // Persistent execution tier override in tiered mode: "high" | "medium" | "low"
	ExecutionTierReason          string          `json:"execution_tier_reason,omitempty"`           // PLAT-060. Why this step's tier is pinned. Required whenever execution_tier is set — update_step_config rejects the override without it. The tier stays configured until explicitly changed; run counters do not select tiers. Cite the owning architecture_review finding id, and the human_input_id when approved.
	LearningObjective            string          `json:"learning_objective,omitempty"`              // What SKILL.md should capture from successful runs of this step — selectors, timings, auth flows, tool-call patterns, API quirks. This is the instruction the step agent uses during its post-completion turn to know what HOW-to-run knowledge to extract. Required when learnings_access includes write; must be specific (not "learn from this run").
	LearningsAccess              string          `json:"learnings_access,omitempty"`                // "read" | "read-write" | "none". Mirrors knowledgebase_access. "read" (default): step sees global SKILL.md in its prompt but doesn't write. "read-write": reads and writes — requires learning_objective to be non-empty. "none": no read, no write. Empty = legacy auto-migration (see resolveLearningsAccess). Writes happen via the step agent's own post-completion turn (shell + diff_patch_workspace_file); no separate analyzer runs.
	LegacyLockLearnings          *bool           `json:"lock_learnings,omitempty"`                  // PLAT-263 migration-only. true is normalized to learnings_access="read"; false is removed. Never consumed by runtime.
	LegacyLockLearningsReason    string          `json:"lock_learnings_reason,omitempty"`           // PLAT-263 migration-only audit residue; removed with LegacyLockLearnings.
	LockCode                     *bool           `json:"lock_code,omitempty"`                       // lock code (main.py) - prevents LLM-rewritten main.py from being saved back to learnings, skips fix loop (nil = not set/unlocked, true = locked, false = explicitly unlocked)
	SelectedServers              []string        `json:"selected_servers,omitempty"`                // step-level MCP server selection (subset of preset servers)
	SelectedTools                []string        `json:"selected_tools,omitempty"`                  // step-level tool selection (format: "server:tool" or "server:*" for all tools)
	EnabledCustomTools           []string        `json:"enabled_custom_tools,omitempty"`            // optional additions, e.g. ["human_tools:get_notification_history"]; baseline workspace + three human tools are automatic
	UseCodeExecutionMode         *bool           `json:"use_code_execution_mode,omitempty"`         // Step-level code execution mode override (nil = use preset default, true/false = override)
	EnabledSkills                []string        `json:"enabled_skills,omitempty"`                  // Step-level skill selection (skill folder names, overrides preset if specified)
	AdditionalReadPaths          []string        `json:"additional_read_paths,omitempty"`           // Extra workflow-relative folders/files this step may read (for example "variables" or "reports/reference.json"). Paths are read-only, must stay inside this workflow, and never widen write access.
	KnowledgebaseAccess          string          `json:"knowledgebase_access,omitempty"`            // "read" | "write" | "read-write" | "none". Empty defaults to "read" so every step can consume shared context; "none" explicitly opts out. A non-empty knowledgebase_contribution promotes an unset mode to "read-write", but writes still require that contribution contract. Read access includes knowledgebase/context/context.md. knowledgebase/context/ is excluded from KB health/fixer passes — user-supplied content is never silently rewritten.
	KnowledgebaseContribution    string          `json:"knowledgebase_contribution,omitempty"`      // User-authored instruction for KB writes — what this step should contribute to notes/. In "agent" write-method, it's the extraction instruction handed to the post-step KB update agent. In "direct" write-method, it's injected into the step agent's prompt as its contribution contract. Required to trigger KB writes; if empty, no KB write happens regardless of access.
	DisableParallelToolExecution *bool           `json:"disable_parallel_tool_execution,omitempty"` // Disable parallel tool execution for this step (nil = enabled by default, true = disabled, false = explicitly enabled)
	CodingAgentTmuxLifecycle     string          `json:"coding_agent_tmux_lifecycle,omitempty"`     // Tmux lifecycle for CLI coding providers: "close_on_completion" (default for steps) or "keep_alive" (only when a step intentionally needs the native coding session after completion).
	SuccessfulRuns               *int            `json:"successful_runs,omitempty"`                 // System-managed counter. Written by syncSuccessfulRunsToStepConfig after each successful validation; mirrors the authoritative count in learning metadata. Read by the readiness checklist to gauge optimization progress (3+ = ready). Agents must NOT set this directly.
	// LegacyDeclaredExecutionMode / LegacyDeclaredExecutionModeReason are the
	// RETIRED declared_execution_mode keys (PLAT-287). A step's execution model
	// is decided by its plan type -- regular is scripted, message_sequence is
	// conversational.
	// Nothing sets these any more. They still round-trip through step_config.json
	// for exactly two reasons: the runtime shim that keeps a not-yet-migrated
	// legacy agentic regular step running as a message_sequence, and so that a
	// step_config write made before the v1.0.39 migration does not silently drop
	// the key and flip such a step to scripted. The v1.0.39 migration
	// (strip_declared_execution_mode) clears both; delete the fields and the
	// shim once no live workflow predates it.
	LegacyDeclaredExecutionMode       string           `json:"declared_execution_mode,omitempty"`
	LegacyDeclaredExecutionModeReason string           `json:"declared_execution_mode_reason,omitempty"`
	DescriptionReviewed               *bool            `json:"description_reviewed,omitempty"` // True when the step description has been reviewed — clarity AND secrets/hardcoded values.
	ReviewNotes                       string           `json:"review_notes,omitempty"`         // Free-form rationale covering why config, learning/KB choices, code locks, or description review state are justified.
	DriftReview                       *StepDriftReview `json:"drift_review,omitempty"`         // Plan-drift review record: evidence from the last completed plan_drift_review pass over this step, plus a NeedsReview flag every persisted field change or unresolved failed drift check keeps true. Nil (no record yet) or NeedsReview==true both mean "due." Set by that module or its manual slash-command equivalent; never by a step-editing agent directly.
}

// StepDriftReview is the "stale flag" model (superseding the earlier
// null-on-edit design): evidence is preserved across edits rather than
// destroyed, and a step's due-ness is its own NeedsReview flag, not the
// presence/absence of the whole record. Every persisted plan-step field
// change sets NeedsReview=true and leaves everything else untouched; only a
// completed plan_drift_review turn replaces Checks/ReviewedAt/ReviewedBy/
// ReviewedThroughChangeID and clears the flag only when no check remains failed.
// This means a step's last real
// review is always available as evidence, even while a newer edit has made it
// stale — the reviewer reads it plus only the changelog entries after
// ReviewedThroughChangeID, instead of starting from nothing.
type StepDriftReview struct {
	NeedsReview             bool             `json:"needs_review"`
	ReviewedAt              string           `json:"reviewed_at"`                          // RFC3339, of the last completed review
	ReviewedBy              string           `json:"reviewed_by"`                          // e.g. "pulse:plan_drift_review" or a user-invoked slash-command session id
	ReviewedThroughChangeID string           `json:"reviewed_through_change_id,omitempty"` // the latest planning/changelog change_id this review consumed
	Checks                  []StepDriftCheck `json:"checks"`
	// ContractVersion is the planDriftReviewContractVersion (plan_drift_candidates.go)
	// in effect when this review was recorded. A step's own field edits are
	// what NeedsReview tracks; a *global* change to which checks the reviewer
	// turn is required to run (e.g. PLAT-259 phase B adding
	// route_structural_isolation/route_eval_pairing) is not a per-step plan
	// edit and would never set NeedsReview -- so an old review recorded under
	// a lower contract version is treated as due again even though nothing
	// about the step itself changed. Zero (the JSON-omitted default) means
	// "recorded before this field existed," which is always < the current
	// version and therefore always due -- the correct behavior, since no
	// review recorded before this field existed could have run the checks a
	// later contract version added.
	ContractVersion int `json:"contract_version,omitempty"`
}

// StepDriftCheck is the evidence-required record for one drift check against
// one step. Status "fixed" means the check found real drift AND a repair was
// applied in this same review pass — not merely flagged.
type StepDriftCheck struct {
	CheckID  string `json:"check_id"` // stable id, e.g. "report_query_compatibility", "kb_access_mode"
	Status   string `json:"status"`   // "pass" | "fail" | "fixed"
	Evidence string `json:"evidence"` // what was compared and what was found — required, not optional
	// FindingID is the durable Pulse issue_id (e.g. "PUL-xxxxxxxx") this check's
	// failure was filed as, via record_pulse_finding. Required when Status is
	// "fail" — record_plan_drift_review rejects a fail-status check with no
	// finding_id, so a failed check can never be persisted as "reviewed"
	// without a corresponding repair item already existing. This closes the
	// window where the review call succeeds, the finding call never happens
	// (crash, turn ends, tool error), and the failure silently disappears from
	// both the drift record (marked reviewed) and the Pulse backlog (no
	// finding filed).
	FindingID string `json:"finding_id,omitempty"`
}

// WorkflowDriftReviewStepID is the reserved step_config.json ID that carries
// the workflow-level (not per-step) drift review record — deletion coverage.
// Deleting a step cascade-removes ITS OWN drift_review record along with its
// step_config.json entry (correct: the step is gone, its per-step evidence is
// moot), but that also means CollectPlanDriftCandidates — which derives its
// candidate set from plan.json's current step list — structurally cannot see
// anything requiring review for a step that no longer exists. A deletion can
// still leave dangling references in dependent artifacts (reports, evals,
// other steps' next_step_id/routes, learnings/KB mentions, DB contracts) that
// need auditing. This sentinel ID reuses the exact same StepDriftReview record
// shape and record_plan_drift_review write path a real step uses — it is
// never a valid plan.json step id, so it can never collide with one, and
// cleanup_orphan_step_configs must explicitly exempt it (it is not "live" in
// plan.json by definition, but is also never orphaned garbage).
const WorkflowDriftReviewStepID = "__workflow_drift_review__"

// flagWorkflowDriftReviewOnDeletion sets NeedsReview=true on the workflow-
// level drift review record (creating it if this workflow has never had a
// deletion before), preserving any prior evidence — the same stale-flag
// pattern clearDriftReviewAfterPlanUpdate uses for a real step's own record,
// so the last completed workflow-level audit stays available even while
// flagged stale. Unlike a real step, an absent record here does NOT mean
// "due": a workflow that has never deleted a step has nothing for this
// candidate to cover, so CollectPlanDriftCandidates only treats the sentinel
// as pending when a record exists AND is flagged, never merely because it is
// missing (see its own doc comment).
func flagWorkflowDriftReviewOnDeletion(configs []StepConfig, deletedStepIDs []string) []StepConfig {
	if len(deletedStepIDs) == 0 {
		return configs
	}
	for i := range configs {
		if configs[i].ID != WorkflowDriftReviewStepID {
			continue
		}
		if configs[i].AgentConfigs == nil {
			configs[i].AgentConfigs = &AgentConfigs{}
		}
		if configs[i].AgentConfigs.DriftReview == nil {
			configs[i].AgentConfigs.DriftReview = &StepDriftReview{}
		}
		configs[i].AgentConfigs.DriftReview.NeedsReview = true
		return configs
	}
	return append(configs, StepConfig{
		ID:           WorkflowDriftReviewStepID,
		AgentConfigs: &AgentConfigs{DriftReview: &StepDriftReview{NeedsReview: true}},
	})
}

// cascadeDeleteStepConfigsOnce reads step_config.json, prunes the deleted
// steps' own entries, flags the workflow-level drift review record, and
// writes the result back in one pass.
func cascadeDeleteStepConfigsOnce(ctx context.Context, workspacePath string, deletedSet map[string]bool, deletedIDs []string, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) ([]string, error) {
	existingConfigs, cfgErr := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
	if cfgErr != nil {
		return nil, cfgErr
	}
	newConfigs, removed := pruneStepConfigsByID(existingConfigs, deletedSet)
	newConfigs = flagWorkflowDriftReviewOnDeletion(newConfigs, deletedIDs)
	if writeErr := writeStepConfigViaFileCallback(ctx, workspacePath, newConfigs, writeFile); writeErr != nil {
		return nil, writeErr
	}
	return removed, nil
}

// cascadeDeleteStepConfigsRetried retries cascadeDeleteStepConfigsOnce once on
// failure — the same pattern clearDriftReviewAfterPlanUpdateRetried uses for a
// real step's own flag write. A failure here is more consequential than that
// case: the deleted step's own drift_review record is already gone by
// definition, so if this workflow-level replacement also fails to persist,
// plan_drift_review has NO remaining way to learn the deletion happened at
// all. driftReviewFlagFailed is true only when both attempts failed, so the
// caller can surface it loudly in its own response rather than only logging it.
func cascadeDeleteStepConfigsRetried(ctx context.Context, workspacePath string, deletedSet map[string]bool, deletedIDs []string, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, logger loggerv2.Logger) (prunedConfigIDs []string, driftReviewFlagFailed bool) {
	removed, err := cascadeDeleteStepConfigsOnce(ctx, workspacePath, deletedSet, deletedIDs, readFile, writeFile)
	if err == nil {
		if len(removed) > 0 {
			logger.Info(fmt.Sprintf("🧹 Cascade-removed %d step_config entries: %v", len(removed), removed))
		}
		logger.Info(fmt.Sprintf("🚩 Flagged workflow-level drift review needs_review after deleting %d step(s): %v", len(deletedIDs), deletedIDs))
		return removed, false
	}
	logger.Warn(fmt.Sprintf("⚠️ Failed to cascade-delete step_config entries / flag workflow-level drift review, retrying once: %v", err))
	removed, err = cascadeDeleteStepConfigsOnce(ctx, workspacePath, deletedSet, deletedIDs, readFile, writeFile)
	if err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Cascade-delete step_config entries / flag workflow-level drift review failed again after retry: %v", err))
		return nil, true
	}
	if len(removed) > 0 {
		logger.Info(fmt.Sprintf("🧹 Cascade-removed %d step_config entries: %v", len(removed), removed))
	}
	logger.Info(fmt.Sprintf("🚩 Flagged workflow-level drift review needs_review after deleting %d step(s): %v", len(deletedIDs), deletedIDs))
	return removed, false
}

// ============================================================================
// TYPE-SAFE STEP SYSTEM (New Implementation)
// ============================================================================

// StepType represents the type of a plan step
type StepType string

const (
	StepTypeRegular      StepType = "regular"
	StepTypeHumanInput   StepType = "human_input"
	StepTypeOrchestrator StepType = "orchestrator"
	// StepTypeTodoTaskLegacy is the pre-v1.0.35 name of the orchestrator step. It is
	// accepted on read everywhere a plan type is parsed and never written back;
	// migrate_orchestrator_step_type rewrites it in plan.json.
	StepTypeTodoTaskLegacy StepType = "todo_task"
	StepTypeRouting        StepType = "routing"
	StepTypeMessageSeq     StepType = "message_sequence"
	// StepTypeBranch is a small in-flow next-step decision — same executor
	// as StepTypeRouting, distinct type so guidance/reporting/eval tooling
	// can tell it apart from routing, which is now the "route" (major fork)
	// concept. See PLAT-259.
	StepTypeBranch StepType = "branch"
	// StepTypeCrew invokes a Crew project's trigger and waits for its final
	// response. Top-level only; nested crew steps parse but never execute.
	StepTypeCrew StepType = "crew"
)

// CommonStepFields contains fields shared by all step types
type CommonStepFields struct {
	ID                  string                `json:"id"` // Stable step ID (generated from title) - required
	Title               string                `json:"title"`
	Description         string                `json:"description"`
	ContextDependencies []string              `json:"context_dependencies"`
	ContextOutput       FlexibleContextOutput `json:"context_output"`              // Use flexible type to handle string or array
	ValidationSchema    *ValidationSchema     `json:"validation_schema,omitempty"` // Optional structured validation schema for step outputs
	// ScriptParameters is the public runtime-input contract for a scripted
	// (regular) step. Todo-task orchestrators discover this contract from the
	// route and pass matching values to main.py through STEP_PARAMS_JSON.
	ScriptParameters map[string]ScriptParameterDefinition `json:"script_parameters,omitempty"`
	// ScriptParametersSchema is the alternative contract: one full JSON Schema
	// (type object) for the whole parameters object, for nested or constrained
	// inputs. A step declares one or the other, never both (PLAT-432).
	ScriptParametersSchema map[string]interface{} `json:"script_parameters_schema,omitempty"`
	SharedWith             *StepSharing           `json:"shared_with,omitempty"` // Optional: visibility rules for reusable orphan steps
}

// ScriptParameterDefinition describes one input accepted by a scripted step.
// It deliberately stays smaller than full JSON Schema: these values cross a
// workflow boundary and should remain easy for builders, orchestrators, and
// repair agents to understand identically.
type ScriptParameterDefinition struct {
	Type        string        `json:"type"`
	Description string        `json:"description"`
	Required    bool          `json:"required,omitempty"`
	Default     interface{}   `json:"default,omitempty"`
	Enum        []interface{} `json:"enum,omitempty"`
}

// StepSharing defines which orchestrators in the same plan may reuse an orphan step.
type StepSharing struct {
	OrchestratorIDs []string `json:"orchestrator_ids,omitempty"`
}

// PlanStepInterface is the interface that all step types must implement
// PlanStep is a type alias for PlanStepInterface for convenience
type PlanStepInterface interface {
	GetID() string
	GetTitle() string
	GetDescription() string
	GetContextDependencies() []string
	GetContextOutput() FlexibleContextOutput
	GetValidationSchema() *ValidationSchema
	StepType() StepType
	// GetCommonFields returns a copy of common fields for convenience
	GetCommonFields() CommonStepFields
}

// RegularPlanStep represents a regular step
type RegularPlanStep struct {
	Type StepType `json:"type"` // Always "regular" - required for JSON marshaling/unmarshaling
	CommonStepFields
	// ScriptOnly executes the saved main.py without agent generation or repair.
	// Relay script nodes use this on the existing scripted step executor.
	ScriptOnly      bool          `json:"script_only,omitempty"`
	NextStepID      string        `json:"next_step_id,omitempty"`     // Optional explicit successor; empty preserves sequential execution
	HasLoop         bool          `json:"has_loop"`                   // DEPRECATED: loop feature removed, kept for JSON backward compatibility
	LoopCondition   string        `json:"loop_condition,omitempty"`   // DEPRECATED: loop feature removed
	MaxIterations   int           `json:"max_iterations,omitempty"`   // DEPRECATED: loop feature removed
	LoopDescription string        `json:"loop_description,omitempty"` // DEPRECATED: loop feature removed
	AgentConfigs    *AgentConfigs `json:"-"`                          // runtime: per-agent configuration (LLM, max turns, toggles) - not stored in plan.json
}

// Implement PlanStepInterface for RegularPlanStep
func (r *RegularPlanStep) GetID() string                           { return r.ID }
func (r *RegularPlanStep) GetTitle() string                        { return r.Title }
func (r *RegularPlanStep) GetDescription() string                  { return r.Description }
func (r *RegularPlanStep) GetContextDependencies() []string        { return r.ContextDependencies }
func (r *RegularPlanStep) GetContextOutput() FlexibleContextOutput { return r.ContextOutput }
func (r *RegularPlanStep) GetValidationSchema() *ValidationSchema  { return r.ValidationSchema }
func (r *RegularPlanStep) StepType() StepType                      { return StepTypeRegular }
func (r *RegularPlanStep) GetCommonFields() CommonStepFields       { return r.CommonStepFields }

// MarshalJSON ensures the type field is always set when marshaling
func (r *RegularPlanStep) MarshalJSON() ([]byte, error) {
	// Ensure type is set
	r.Type = StepTypeRegular
	// Use type alias to avoid infinite recursion
	type Alias RegularPlanStep
	return json.Marshal((*Alias)(r))
}

// RoutingRoute represents a single route option in a routing step
type RoutingRoute struct {
	RouteID    string `json:"route_id"`
	RouteName  string `json:"route_name"`
	Condition  string `json:"condition"`
	NextStepID string `json:"next_step_id"`
}

// RoutingResponse represents the structured response from routing evaluation
type RoutingResponse struct {
	SelectedRouteID string `json:"selected_route_id"` // The selected route ID
	Reasoning       string `json:"reasoning"`         // Reasoning for the selection
}

// routeSwitchStep is implemented by both RoutingPlanStep and BranchPlanStep --
// identical deterministic-switch shape and evaluation logic, just a
// different step-type tag (routing is now the "route"/major-fork concept
// going forward; branch is the small in-flow decision). Lets the shared
// executor (executeRoutingStep in controller_routing.go,
// resolveDeterministicRoutingSelection and its helpers in
// controller_routing_deterministic.go) operate on either without
// duplicating that logic. See PLAT-259.
type routeSwitchStep interface {
	PlanStepInterface
	GetRoutes() []RoutingRoute
	GetDefaultRouteID() string
	GetRouteSourceFile() string
	GetRouteSource() string // "" for a purely preseeded switch; "human" when the branch asks a person if nothing was preseeded (branch only)
	GetRoutingQuestionText() string
	SetSelectedRouteID(string)
	SetRoutingResponse(*RoutingResponse)
}

// RoutingPlanStep represents a deterministic N-way switch.
// Routing steps never execute agents. If an agent/probe/judgment is needed, put
// it in a prior message_sequence step that writes route_selection.json, then point this
// routing step at that file via route_source_file or context_dependencies.
type RoutingPlanStep struct {
	Type StepType `json:"type"` // Always "routing" - required for JSON marshaling/unmarshaling
	CommonStepFields
	RoutingQuestion string           `json:"routing_question"`            // Human-readable route decision prompt; retained for compatibility/readability
	Routes          []RoutingRoute   `json:"routes"`                      // Available routes (min 2, required)
	DefaultRouteID  string           `json:"default_route_id,omitempty"`  // Optional fallback route_id when no route file is available
	RouteSourceFile string           `json:"route_source_file,omitempty"` // Optional route_selection.json source produced by a prior step
	SelectedRouteID string           `json:"-"`                           // runtime: stores selected route ID
	RoutingResponse *RoutingResponse `json:"-"`                           // runtime: stores structured routing response
	AgentConfigs    *AgentConfigs    `json:"-"`                           // runtime: per-agent configuration
}

// Implement PlanStepInterface for RoutingPlanStep
func (r *RoutingPlanStep) GetID() string                           { return r.ID }
func (r *RoutingPlanStep) GetTitle() string                        { return r.Title }
func (r *RoutingPlanStep) GetDescription() string                  { return r.Description }
func (r *RoutingPlanStep) GetContextDependencies() []string        { return r.ContextDependencies }
func (r *RoutingPlanStep) GetContextOutput() FlexibleContextOutput { return r.ContextOutput }
func (r *RoutingPlanStep) GetValidationSchema() *ValidationSchema  { return r.ValidationSchema }
func (r *RoutingPlanStep) StepType() StepType                      { return StepTypeRouting }
func (r *RoutingPlanStep) GetCommonFields() CommonStepFields {
	return CommonStepFields{
		ID:                  r.ID,
		Title:               r.Title,
		Description:         r.Description,
		ContextDependencies: r.ContextDependencies,
		ContextOutput:       r.ContextOutput,
		ValidationSchema:    r.ValidationSchema,
		SharedWith:          r.SharedWith,
	}
}

// MarshalJSON ensures the type field is always set when marshaling
func (r *RoutingPlanStep) MarshalJSON() ([]byte, error) {
	r.Type = StepTypeRouting
	type Alias RoutingPlanStep
	return json.Marshal((*Alias)(r))
}

// UnmarshalJSON implements custom unmarshaling for RoutingPlanStep
func (r *RoutingPlanStep) UnmarshalJSON(data []byte) error {
	type Alias RoutingPlanStep
	var temp Alias
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal routing step: %w", err)
	}
	*r = RoutingPlanStep(temp)
	return nil
}

// Implement routeSwitchStep for RoutingPlanStep
func (r *RoutingPlanStep) GetRoutes() []RoutingRoute                { return r.Routes }
func (r *RoutingPlanStep) GetDefaultRouteID() string                { return r.DefaultRouteID }
func (r *RoutingPlanStep) GetRouteSourceFile() string               { return r.RouteSourceFile }
func (r *RoutingPlanStep) GetRoutingQuestionText() string           { return r.RoutingQuestion }
func (r *RoutingPlanStep) SetSelectedRouteID(id string)             { r.SelectedRouteID = id }
func (r *RoutingPlanStep) SetRoutingResponse(resp *RoutingResponse) { r.RoutingResponse = resp }

// BranchPlanStep represents a small in-flow deterministic N-way switch.
// Identical mechanics to RoutingPlanStep (never executes an agent, resolves
// its route from a preseeded route_selection.json / route_source_file /
// context_dependencies entry / default_route_id) -- the only difference is
// intent: branch is for a lightweight next-step decision, while routing is
// now the "route" (major sub-workflow fork) concept. See PLAT-259.
type BranchPlanStep struct {
	Type StepType `json:"type"` // Always "branch" - required for JSON marshaling/unmarshaling
	CommonStepFields
	BranchQuestion  string         `json:"branch_question"`             // Human-readable decision prompt; retained for compatibility/readability
	Routes          []RoutingRoute `json:"routes"`                      // Available routes (min 2, required)
	DefaultRouteID  string         `json:"default_route_id,omitempty"`  // Optional fallback route_id when no route file is available
	RouteSourceFile string         `json:"route_source_file,omitempty"` // Optional route_selection.json source produced by a prior step
	RouteSource     string         `json:"route_source,omitempty"`      // "human": when no route was preseeded, ask the person (routes are the options) instead of falling to default_route_id. Successor to yesno/multiple_choice human_input. See controller_branch_human.go.
	// ValuePath is one authored input or prior JSON output reference, for
	// example {{input.kind}}. ValueCases maps exact values to route IDs.
	ValuePath       string            `json:"value_path,omitempty"`
	ValueCases      map[string]string `json:"value_cases,omitempty"`
	SelectedRouteID string            `json:"-"` // runtime: stores selected route ID
	RoutingResponse *RoutingResponse  `json:"-"` // runtime: stores structured routing response
	AgentConfigs    *AgentConfigs     `json:"-"` // runtime: per-agent configuration
}

// Implement PlanStepInterface for BranchPlanStep
func (b *BranchPlanStep) GetID() string                           { return b.ID }
func (b *BranchPlanStep) GetTitle() string                        { return b.Title }
func (b *BranchPlanStep) GetDescription() string                  { return b.Description }
func (b *BranchPlanStep) GetContextDependencies() []string        { return b.ContextDependencies }
func (b *BranchPlanStep) GetContextOutput() FlexibleContextOutput { return b.ContextOutput }
func (b *BranchPlanStep) GetValidationSchema() *ValidationSchema  { return b.ValidationSchema }
func (b *BranchPlanStep) StepType() StepType                      { return StepTypeBranch }
func (b *BranchPlanStep) GetCommonFields() CommonStepFields {
	return CommonStepFields{
		ID:                  b.ID,
		Title:               b.Title,
		Description:         b.Description,
		ContextDependencies: b.ContextDependencies,
		ContextOutput:       b.ContextOutput,
		ValidationSchema:    b.ValidationSchema,
		SharedWith:          b.SharedWith,
	}
}

// Implement routeSwitchStep for BranchPlanStep
func (b *BranchPlanStep) GetRoutes() []RoutingRoute                { return b.Routes }
func (b *BranchPlanStep) GetDefaultRouteID() string                { return b.DefaultRouteID }
func (b *BranchPlanStep) GetRouteSourceFile() string               { return b.RouteSourceFile }
func (b *BranchPlanStep) GetRoutingQuestionText() string           { return b.BranchQuestion }
func (b *BranchPlanStep) SetSelectedRouteID(id string)             { b.SelectedRouteID = id }
func (b *BranchPlanStep) SetRoutingResponse(resp *RoutingResponse) { b.RoutingResponse = resp }

// MarshalJSON ensures the type field is always set when marshaling
func (b *BranchPlanStep) MarshalJSON() ([]byte, error) {
	b.Type = StepTypeBranch
	type Alias BranchPlanStep
	return json.Marshal((*Alias)(b))
}

// UnmarshalJSON implements custom unmarshaling for BranchPlanStep
func (b *BranchPlanStep) UnmarshalJSON(data []byte) error {
	type Alias BranchPlanStep
	var temp Alias
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal branch step: %w", err)
	}
	*b = BranchPlanStep(temp)
	return nil
}

// HumanInputPlanStep represents a step that asks a question to a human and blocks for input
// This step type has no LLM, no execution, no validation, and no learning - just human input
type HumanInputPlanStep struct {
	Type StepType `json:"type"` // Always "human_input" - required for JSON marshaling/unmarshaling
	CommonStepFields
	Question           string            `json:"question"`                      // Required: question to ask human
	VariableName       string            `json:"variable_name,omitempty"`       // Optional: store response in variable
	ResponseType       string            `json:"response_type,omitempty"`       // "text" (default), "yesno", "multiple_choice"
	Options            []string          `json:"options,omitempty"`             // For multiple_choice type
	NextStepID         string            `json:"next_step_id"`                  // Default: where to go after response (or "end") when no response-specific route matches
	IfYesNextStepID    string            `json:"if_yes_next_step_id,omitempty"` // Optional: for yesno type when response is "yes"
	IfNoNextStepID     string            `json:"if_no_next_step_id,omitempty"`  // Optional: for yesno type when response is "no"
	OptionRoutes       map[string]string `json:"option_routes,omitempty"`       // Optional: for multiple_choice type - maps option index (as string "0", "1", etc.) or option value to next_step_id
	SelectedNextStepID string            `json:"-"`                             // runtime: stores computed next step ID based on response - not stored in plan.json
	AgentConfigs       *AgentConfigs     `json:"-"`                             // runtime: per-agent configuration (LLM, max turns, toggles) - not stored in plan.json
}

// Implement PlanStepInterface for HumanInputPlanStep
func (h *HumanInputPlanStep) GetID() string                           { return h.ID }
func (h *HumanInputPlanStep) GetTitle() string                        { return h.Title }
func (h *HumanInputPlanStep) GetDescription() string                  { return h.Description }
func (h *HumanInputPlanStep) GetContextDependencies() []string        { return h.ContextDependencies }
func (h *HumanInputPlanStep) GetContextOutput() FlexibleContextOutput { return h.ContextOutput }
func (h *HumanInputPlanStep) GetValidationSchema() *ValidationSchema  { return h.ValidationSchema }
func (h *HumanInputPlanStep) StepType() StepType                      { return StepTypeHumanInput }
func (h *HumanInputPlanStep) GetCommonFields() CommonStepFields       { return h.CommonStepFields }

// MarshalJSON ensures the type field is always set when marshaling
func (h *HumanInputPlanStep) MarshalJSON() ([]byte, error) {
	// Ensure type is set
	h.Type = StepTypeHumanInput
	// Use type alias to avoid infinite recursion
	type Alias HumanInputPlanStep
	return json.Marshal((*Alias)(h))
}

// MessageSequenceWriteAccess optionally narrows store writes for one sequence item.
// When it is empty, the item inherits the step-level DB, KB, and learnings access.
// Read access remains governed by the step-level configuration.
type MessageSequenceWriteAccess struct {
	Knowledgebase bool `json:"knowledgebase,omitempty"`
	DB            bool `json:"db,omitempty"`
	Learnings     bool `json:"learnings,omitempty"`
}

// UnmarshalJSON rejects per-file scoping attempts on write_access. The access is
// folder-level booleans (knowledgebase/db/learnings); authors frequently try
// {"db": true, "paths": ["db/foo.json"]} expecting a single file to be writable.
// Standard decoding silently drops the unknown "paths" key, granting the whole
// db/ folder while the author thinks it was scoped — a real footgun. Catch the
// path-like keys with an explicit, actionable error instead.
func (w *MessageSequenceWriteAccess) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"paths", "path", "files", "file"} {
		if _, ok := raw[key]; ok {
			return fmt.Errorf("write_access does not support per-file scoping (%q): it is folder-level booleans only — use {\"db\": true}, {\"knowledgebase\": true}, and/or {\"learnings\": true}", key)
		}
	}
	type alias MessageSequenceWriteAccess
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*w = MessageSequenceWriteAccess(a)
	return nil
}

type MessageSequenceItem struct {
	ID               string                     `json:"id"`
	Type             string                     `json:"type"` // "user_message" | "prevalidation" | "foreach" | "scripted"
	Kind             string                     `json:"kind,omitempty"`
	Title            string                     `json:"title,omitempty"`
	Message          string                     `json:"message,omitempty"`
	WriteAccess      MessageSequenceWriteAccess `json:"write_access,omitempty"`
	ValidationSchema *ValidationSchema          `json:"validation_schema,omitempty"`
	Prevalidation    *ValidationSchema          `json:"prevalidation,omitempty"`
	// foreach items: iterate db/db.sqlite table rows, one templated user_message
	// per row. source_sql is a read-only query; each result row binds to '.'.
	SourceSQL     string `json:"source_sql,omitempty"`     // read-only SQL against db/db.sqlite
	MaxIterations int    `json:"max_iterations,omitempty"` // optional cap on rows (0 = all)
	// scripted items run a declared batch of saved scripts, without agent sessions.
	ScriptedSteps []MessageSequenceScriptCall `json:"scripted_steps,omitempty"`
	MaxParallel   int                         `json:"max_parallel,omitempty"`
	// prevalidation entries: corrective turns allowed on gate failure. Used by the
	// orchestrator's scripted sequence (todo_task); a standalone message_sequence
	// uses its own fixed repair cap. Default 1 when unset.
	MaxCorrections int `json:"max_corrections,omitempty"`
	// Synthetic marks an item the runtime appended itself rather than one an
	// author wrote in plan.json — today only the final validation gate from
	// appendMessageSequenceFinalValidation. Never serialized: these items exist
	// only for the duration of a run, and writing them back would turn a
	// generated gate into authored plan content.
	Synthetic bool `json:"-"`
}

type MessageSequencePlanStep struct {
	Type StepType `json:"type"`
	CommonStepFields
	Items []MessageSequenceItem `json:"items,omitempty"`
	// AuthoredPrompt runs the exact system prompt and user messages supplied by
	// the author. It is used by Relays on the shared message-sequence executor.
	AuthoredPrompt   bool                     `json:"authored_prompt,omitempty"`
	SystemPrompt     string                   `json:"system_prompt,omitempty"`
	PredefinedRoutes []PlanOrchestrationRoute `json:"predefined_routes,omitempty"`
	NextStepID       string                   `json:"next_step_id,omitempty"`
	AgentConfigs     *AgentConfigs            `json:"-"`
}

func (m *MessageSequencePlanStep) GetID() string                           { return m.ID }
func (m *MessageSequencePlanStep) GetTitle() string                        { return m.Title }
func (m *MessageSequencePlanStep) GetDescription() string                  { return m.Description }
func (m *MessageSequencePlanStep) GetContextDependencies() []string        { return m.ContextDependencies }
func (m *MessageSequencePlanStep) GetContextOutput() FlexibleContextOutput { return m.ContextOutput }
func (m *MessageSequencePlanStep) GetValidationSchema() *ValidationSchema  { return m.ValidationSchema }
func (m *MessageSequencePlanStep) StepType() StepType                      { return StepTypeMessageSeq }
func (m *MessageSequencePlanStep) GetCommonFields() CommonStepFields       { return m.CommonStepFields }

func (m *MessageSequencePlanStep) MarshalJSON() ([]byte, error) {
	m.Type = StepTypeMessageSeq
	type Alias MessageSequencePlanStep
	return json.Marshal((*Alias)(m))
}

// UnmarshalJSON handles the interface-valued sub_agent_step fields in optional
// delegation routes. A message sequence with predefined_routes is still a
// message_sequence plan step; the routes only add bounded, agent-decided
// delegation capabilities to its conversation.
func (m *MessageSequencePlanStep) UnmarshalJSON(data []byte) error {
	type messageSequenceAlias struct {
		Type StepType `json:"type"`
		CommonStepFields
		Items               []MessageSequenceItem `json:"items,omitempty"`
		AuthoredPrompt      bool                  `json:"authored_prompt,omitempty"`
		SystemPrompt        string                `json:"system_prompt,omitempty"`
		PredefinedRoutesRaw []json.RawMessage     `json:"predefined_routes,omitempty"`
		NextStepID          string                `json:"next_step_id,omitempty"`
	}
	var raw messageSequenceAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal message_sequence step: %w", err)
	}

	m.Type = StepTypeMessageSeq
	m.CommonStepFields = raw.CommonStepFields
	m.Items = raw.Items
	m.AuthoredPrompt = raw.AuthoredPrompt
	m.SystemPrompt = raw.SystemPrompt
	m.NextStepID = raw.NextStepID
	m.PredefinedRoutes = nil
	for i, routeJSON := range raw.PredefinedRoutesRaw {
		var route struct {
			RouteID       string          `json:"route_id"`
			RouteName     string          `json:"route_name"`
			Condition     string          `json:"condition"`
			SubAgentStep  json.RawMessage `json:"sub_agent_step"`
			OrphanStepRef string          `json:"orphan_step_ref,omitempty"`
		}
		if err := json.Unmarshal(routeJSON, &route); err != nil {
			return fmt.Errorf("failed to unmarshal message_sequence predefined_route[%d]: %w", i, err)
		}
		parsed := PlanOrchestrationRoute{
			RouteID:       route.RouteID,
			RouteName:     route.RouteName,
			Condition:     route.Condition,
			OrphanStepRef: route.OrphanStepRef,
		}
		if len(route.SubAgentStep) > 0 && string(route.SubAgentStep) != "null" {
			step, err := unmarshalStepFromJSON(route.SubAgentStep)
			if err != nil {
				return fmt.Errorf("failed to unmarshal sub_agent_step in message_sequence predefined_route[%d]: %w", i, err)
			}
			parsed.SubAgentStep = step
		}
		m.PredefinedRoutes = append(m.PredefinedRoutes, parsed)
	}
	return nil
}

// OrchestratorPlanStep represents a todo task orchestrator step that manages a dynamic todo list
// It combines predefined sub-agents (with learning/prevalidation) and an optional generic execution agent
// The main orchestrator creates/assigns tasks, then delegates to appropriate agents
// NOTE: Todo task steps are orchestration-like wrappers that manage todo lists instead of success criteria.
// Loops are NOT supported on todo task wrappers - the step completes when all todos are done.
type OrchestratorPlanStep struct {
	Type                 StepType                 `json:"type"` // Always "todo_task" - required for JSON marshaling/unmarshaling
	CommonStepFields                              // Embeds ID, Title, Description, SuccessCriteria, ContextDependencies, ContextOutput, ValidationSchema
	PredefinedRoutes     []PlanOrchestrationRoute `json:"predefined_routes,omitempty"` // Predefined sub-agents (with learning/prevalidation)
	NextStepID           string                   `json:"next_step_id,omitempty"`      // ID of step after todo task completes (or "end")
	Messages             []MessageSequenceItem    `json:"messages,omitempty"`          // Optional scripted message sequence fed into the orchestrator's own conversation after its first turn
	OrchestratorDecision *OrchestratorDecision    `json:"-"`                           // runtime: stores orchestrator decisions - not stored in plan.json
	AgentConfigs         *AgentConfigs            `json:"-"`                           // runtime: per-agent configuration - not stored in plan.json
	// AuthoredPrompt and SystemPrompt carry an authored agent (a Relay agent)
	// through the delegation runtime when it owns routes, so its exact system
	// prompt and JSON result contract survive (PLAT-441). Runtime only.
	AuthoredPrompt bool   `json:"-"`
	SystemPrompt   string `json:"-"`
}

// A todo_task step's optional scripted message sequence reuses MessageSequenceItem
// (the unified sequence-item type). The orchestrator only runs the conversational
// kinds — user_message/message, prevalidation, foreach — and delegates real work to
// sub-agents; code/file/write_access items are rejected at plan validation. After the
// orchestrator's first turn each item is fed into the SAME orchestrator conversation
// in order; a prevalidation item is a hard gate (up to MaxCorrections corrective
// turns). The whole sequence runs within one execution — no persistence or re-entry.

// OrchestratorDecision represents the structured output from the Orchestrator orchestrator agent
type OrchestratorDecision struct {
	// Task management (via tools, not structured output - these are for reference)
	TasksCreated   []string `json:"tasks_created,omitempty"`   // IDs of tasks created this turn
	TasksUpdated   []string `json:"tasks_updated,omitempty"`   // IDs of tasks updated this turn
	TasksCompleted []string `json:"tasks_completed,omitempty"` // IDs of tasks completed this turn

	// Agent assignment decision
	NextAction      string `json:"next_action"`                 // "delegate", "complete", "continue"
	SelectedRouteID string `json:"selected_route_id,omitempty"` // Route ID for predefined agent
	UseGenericAgent bool   `json:"use_generic_agent,omitempty"` // Use generic agent instead

	// Delegation instructions
	TodoIDToExecute            string                 `json:"todo_id_to_execute,omitempty"`             // Which todo to work on
	InstructionsToSubAgent     string                 `json:"instructions_to_sub_agent,omitempty"`      // Detailed instructions
	ScriptParameters           map[string]interface{} `json:"script_parameters,omitempty"`              // Typed inputs for a scripted predefined route
	SuccessCriteriaForSubAgent string                 `json:"success_criteria_for_sub_agent,omitempty"` // Measurable criteria

	// Overall status
	AllTasksComplete bool   `json:"all_tasks_complete"`          // True when all todos are completed
	ProgressSummary  string `json:"progress_summary"`            // Human-readable progress
	CompletionReason string `json:"completion_reason,omitempty"` // Why the step is complete
}

// Implement PlanStepInterface for OrchestratorPlanStep
func (t *OrchestratorPlanStep) GetID() string                           { return t.ID }
func (t *OrchestratorPlanStep) GetTitle() string                        { return t.Title }
func (t *OrchestratorPlanStep) GetDescription() string                  { return t.Description }
func (t *OrchestratorPlanStep) GetContextDependencies() []string        { return t.ContextDependencies }
func (t *OrchestratorPlanStep) GetContextOutput() FlexibleContextOutput { return t.ContextOutput }
func (t *OrchestratorPlanStep) GetValidationSchema() *ValidationSchema  { return t.ValidationSchema }
func (t *OrchestratorPlanStep) StepType() StepType                      { return StepTypeOrchestrator }
func (t *OrchestratorPlanStep) GetCommonFields() CommonStepFields       { return t.CommonStepFields }

// MarshalJSON ensures the type field is always set when marshaling OrchestratorPlanStep
// Writes the flat format (no nested todo_task_step)
func (t *OrchestratorPlanStep) MarshalJSON() ([]byte, error) {
	// Ensure type is set
	t.Type = StepTypeOrchestrator

	// Use type alias to avoid infinite recursion
	type Alias OrchestratorPlanStep
	return json.Marshal((*Alias)(t))
}

// UnmarshalJSON implements custom unmarshaling for OrchestratorPlanStep
// Supports both the new flat format and the legacy nested todo_task_step format for backwards compatibility.
func (t *OrchestratorPlanStep) UnmarshalJSON(data []byte) error {
	// First, unmarshal into a temporary struct to extract nested steps as raw JSON
	var temp struct {
		Type  StepType `json:"type"`
		ID    string   `json:"id"`
		Title string   `json:"title"`
		// Flat format fields (new)
		Description         string                `json:"description"`
		ContextDependencies []string              `json:"context_dependencies"`
		ContextOutput       FlexibleContextOutput `json:"context_output"`
		ValidationSchema    *ValidationSchema     `json:"validation_schema,omitempty"`
		// Legacy nested field (backwards compatibility)
		OrchestratorStep json.RawMessage `json:"todo_task_step,omitempty"`
		PredefinedRoutes []struct {
			RouteID       string          `json:"route_id"`
			RouteName     string          `json:"route_name"`
			Condition     string          `json:"condition"`
			SubAgentStep  json.RawMessage `json:"sub_agent_step"`
			OrphanStepRef string          `json:"orphan_step_ref,omitempty"`
		} `json:"predefined_routes,omitempty"`
		NextStepID string                `json:"next_step_id,omitempty"`
		Messages   []MessageSequenceItem `json:"messages,omitempty"`
	}

	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal todo_task step: %w", err)
	}

	// Copy basic fields
	t.Type = temp.Type
	t.ID = temp.ID
	t.Title = temp.Title
	t.NextStepID = temp.NextStepID
	t.Messages = temp.Messages

	// Copy flat format fields
	t.Description = temp.Description
	t.ContextDependencies = temp.ContextDependencies
	t.ContextOutput = temp.ContextOutput
	t.ValidationSchema = temp.ValidationSchema

	// BACKWARDS COMPATIBILITY: if legacy todo_task_step is present, migrate fields from it
	// Top-level fields take precedence if both are present
	if len(temp.OrchestratorStep) > 0 && string(temp.OrchestratorStep) != "null" {
		innerStep, err := unmarshalStepFromJSON(temp.OrchestratorStep)
		if err != nil {
			return fmt.Errorf("failed to unmarshal legacy todo_task_step: %w", err)
		}
		if t.Description == "" {
			t.Description = innerStep.GetDescription()
		}
		if t.ContextDependencies == nil {
			t.ContextDependencies = innerStep.GetContextDependencies()
		}
		if t.ContextOutput == "" {
			t.ContextOutput = innerStep.GetContextOutput()
		}
		if t.ValidationSchema == nil {
			t.ValidationSchema = innerStep.GetValidationSchema()
		}
	}

	// Unmarshal predefined_routes with nested sub_agent_step
	if len(temp.PredefinedRoutes) > 0 {
		t.PredefinedRoutes = make([]PlanOrchestrationRoute, len(temp.PredefinedRoutes))
		for i, route := range temp.PredefinedRoutes {
			t.PredefinedRoutes[i].RouteID = route.RouteID
			t.PredefinedRoutes[i].RouteName = route.RouteName
			t.PredefinedRoutes[i].Condition = route.Condition
			t.PredefinedRoutes[i].OrphanStepRef = route.OrphanStepRef

			// Unmarshal nested sub_agent_step
			if len(route.SubAgentStep) > 0 && string(route.SubAgentStep) != "null" {
				step, err := unmarshalStepFromJSON(route.SubAgentStep)
				if err != nil {
					return fmt.Errorf("failed to unmarshal sub_agent_step in predefined route %d: %w", i, err)
				}
				t.PredefinedRoutes[i].SubAgentStep = step
			} else {
				t.PredefinedRoutes[i].SubAgentStep = nil
			}
		}
	} else {
		t.PredefinedRoutes = nil
	}

	return nil
}

// PlanStep is now an alias for PlanStepInterface for convenience
// All code should use PlanStepInterface directly
type PlanStep = PlanStepInterface

// PlanningResponse represents the structured response from planning
// Uses type-safe PlanStepInterface - all plans must be in new format with "type" field
type PlanningResponse struct {
	Objective       string              `json:"objective,omitempty"`
	SuccessCriteria string              `json:"success_criteria,omitempty"`
	Steps           []PlanStepInterface `json:"-"`
	OrphanSteps     []PlanStepInterface `json:"-"`
}

// parseStepFromJSON parses a single step from raw JSON using the type field
func parseStepFromJSON(stepData json.RawMessage, index int, label string) (PlanStepInterface, error) {
	var stepWithType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(stepData, &stepWithType); err != nil {
		return nil, fmt.Errorf("failed to parse %s %d type: %w", label, index, err)
	}

	if stepWithType.Type == "" {
		return nil, fmt.Errorf("%s %d is missing required 'type' field (must be: regular, human_input, todo_task, routing, branch, message_sequence, or crew)", label, index)
	}

	switch stepWithType.Type {
	case "regular":
		var step RegularPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse regular %s %d: %w", label, index, err)
		}
		return &step, nil
	case "crew":
		var step CrewPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse crew %s %d: %w", label, index, err)
		}
		return &step, nil
	case "human_input":
		var step HumanInputPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse human_input %s %d: %w", label, index, err)
		}
		return &step, nil
	case "orchestrator", "todo_task":
		var step OrchestratorPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse orchestrator %s %d: %w", label, index, err)
		}
		step.Type = StepTypeOrchestrator
		return &step, nil
	case "routing":
		var step RoutingPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse routing %s %d: %w", label, index, err)
		}
		return &step, nil
	case "branch":
		var step BranchPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse branch %s %d: %w", label, index, err)
		}
		return &step, nil
	case "message_sequence":
		var step MessageSequencePlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse message_sequence %s %d: %w", label, index, err)
		}
		return &step, nil
	default:
		return nil, fmt.Errorf("unknown step type %q in %s %d (must be: regular, human_input, todo_task, routing, branch, message_sequence, or crew)", stepWithType.Type, label, index)
	}
}

// UnmarshalJSON implements custom unmarshaling for typed steps
func (pr *PlanningResponse) UnmarshalJSON(data []byte) error {
	var temp struct {
		Objective       string            `json:"objective"`
		SuccessCriteria string            `json:"success_criteria"`
		Steps           []json.RawMessage `json:"steps"`
		OrphanSteps     []json.RawMessage `json:"orphan_steps"`
	}
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal plan: %w", err)
	}

	pr.Objective = temp.Objective
	pr.SuccessCriteria = temp.SuccessCriteria
	pr.Steps = make([]PlanStepInterface, len(temp.Steps))
	for i, stepData := range temp.Steps {
		typedStep, err := parseStepFromJSON(stepData, i, "step")
		if err != nil {
			return err
		}
		pr.Steps[i] = typedStep
	}

	if len(temp.OrphanSteps) > 0 {
		pr.OrphanSteps = make([]PlanStepInterface, len(temp.OrphanSteps))
		for i, stepData := range temp.OrphanSteps {
			typedStep, err := parseStepFromJSON(stepData, i, "orphan step")
			if err != nil {
				return err
			}
			pr.OrphanSteps[i] = typedStep
		}
	}

	return nil
}

// MarshalJSON implements custom marshaling for typed steps
func (pr PlanningResponse) MarshalJSON() ([]byte, error) {
	wrappedSteps := make([]json.RawMessage, len(pr.Steps))
	for i, step := range pr.Steps {
		stepJSON, err := json.Marshal(step)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal step %d: %w", i, err)
		}
		wrappedSteps[i] = stepJSON
	}

	result := map[string]interface{}{
		"steps": wrappedSteps,
	}

	if pr.Objective != "" {
		result["objective"] = pr.Objective
	}
	if pr.SuccessCriteria != "" {
		result["success_criteria"] = pr.SuccessCriteria
	}
	if len(pr.OrphanSteps) > 0 {
		wrappedOrphans := make([]json.RawMessage, len(pr.OrphanSteps))
		for i, step := range pr.OrphanSteps {
			stepJSON, err := json.Marshal(step)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal orphan step %d: %w", i, err)
			}
			wrappedOrphans[i] = stepJSON
		}
		result["orphan_steps"] = wrappedOrphans
	}

	return json.Marshal(result)
}

// PartialPlanStep represents a partial update to a plan step (used only in tool schemas)
// NOTE: This struct works with typed steps (PlanStepInterface).
// Nested steps (OrchestrationStep, IfTrueSteps, IfFalseSteps) are PlanStepInterface.
// Once plan.json is migrated to use new type-safe types, this struct can be updated accordingly.
type PartialPlanStep struct {
	ExistingStepID      string                `json:"existing_step_id"`               // Required: ID of existing step to update
	Title               string                `json:"title,omitempty"`                // Optional: New title (if renaming)
	Description         string                `json:"description,omitempty"`          // Optional: Updated description
	ClearDescription    bool                  `json:"clear_description,omitempty"`    // Optional: Clear legacy descriptions that are no longer valid for routing steps
	ContextDependencies []string              `json:"context_dependencies,omitempty"` // Optional: Updated context dependencies
	ContextOutput       FlexibleContextOutput `json:"context_output,omitempty"`       // Optional: Updated context output
	HasLoop             *bool                 `json:"has_loop,omitempty"`             // DEPRECATED: loop feature removed, kept for JSON backward compatibility
	LoopCondition       string                `json:"loop_condition,omitempty"`       // DEPRECATED: loop feature removed
	MaxIterations       *int                  `json:"max_iterations,omitempty"`       // DEPRECATED: loop feature removed
	LoopDescription     string                `json:"loop_description,omitempty"`     // DEPRECATED: loop feature removed
	// Todo task step fields
	OrchestratorStep map[string]interface{}   `json:"todo_task_step,omitempty"`    // Optional: Updated todo task step - will be converted to PlanStepInterface
	PredefinedRoutes []PlanOrchestrationRoute `json:"predefined_routes,omitempty"` // Optional: Updated predefined routes for todo task steps
	Messages         []MessageSequenceItem    `json:"messages,omitempty"`          // Optional: Updated scripted message sequence for todo task steps
	// Routing fields
	NextStepID string `json:"next_step_id,omitempty"` // Optional: Updated next_step_id (for routing steps)
	// Routing step fields
	RoutingQuestion string            `json:"routing_question,omitempty"`  // Optional: Updated routing question
	Routes          []RoutingRoute    `json:"routes,omitempty"`            // Optional: Updated routes
	DefaultRouteID  string            `json:"default_route_id,omitempty"`  // Optional: Updated default route ID
	RouteSourceFile string            `json:"route_source_file,omitempty"` // Optional: Updated deterministic route source file
	ValuePath       string            `json:"value_path,omitempty"`
	ValueCases      map[string]string `json:"value_cases,omitempty"`
	ScriptOnly      *bool             `json:"script_only,omitempty"`
	// Branch step fields (PLAT-259: same deterministic-switch shape as
	// routing, just its own field name for the human-readable prompt)
	BranchQuestion string `json:"branch_question,omitempty"` // Optional: Updated branch question
	RouteSource    string `json:"route_source,omitempty"`    // Optional: "human" makes the branch ask a person when nothing was preseeded (branch only)
	// Human input step fields
	Question               string                               `json:"question,omitempty"`                 // Optional: Updated question
	VariableName           string                               `json:"variable_name,omitempty"`            // Optional: Updated variable name
	ResponseType           string                               `json:"response_type,omitempty"`            // Optional: Updated response type
	Options                []string                             `json:"options,omitempty"`                  // Optional: Updated options (for multiple_choice)
	IfYesNextStepID        string                               `json:"if_yes_next_step_id,omitempty"`      // Optional: Updated if_yes_next_step_id (for yesno)
	IfNoNextStepID         string                               `json:"if_no_next_step_id,omitempty"`       // Optional: Updated if_no_next_step_id (for yesno)
	OptionRoutes           map[string]string                    `json:"option_routes,omitempty"`            // Optional: Updated option routes (for multiple_choice)
	ValidationSchema       *ValidationSchema                    `json:"validation_schema,omitempty"`        // Optional: Updated validation schema
	ScriptParameters       map[string]ScriptParameterDefinition `json:"script_parameters,omitempty"`        // Optional: replace the scripted runtime parameter contract; pass {} to clear
	ScriptParametersSchema map[string]interface{}               `json:"script_parameters_schema,omitempty"` // Optional: replace the full JSON Schema contract; pass {} to clear
	// Message sequence fields
	Items          []MessageSequenceItem `json:"items,omitempty"`
	AuthoredPrompt *bool                 `json:"authored_prompt,omitempty"`
	SystemPrompt   *string               `json:"system_prompt,omitempty"`
	// Crew step fields
	CrewProfileID      string `json:"crew_profile_id,omitempty"` // Optional: Updated crew profile ID
	CrewProjectID      string `json:"crew_project_id,omitempty"` // Optional: Updated crew project ID
	CrewTriggerID      string `json:"trigger_id,omitempty"`      // Optional: Updated crew trigger ID
	CrewInstruction    string `json:"instruction,omitempty"`     // Optional: Updated crew instruction
	CrewTimeoutSeconds int    `json:"timeout_seconds,omitempty"` // Optional: Updated crew timeout; 0 leaves the existing value
}

// planFileMutex ensures thread-safe access to plan.json
var planFileMutex sync.Mutex

// PlanFieldChange represents a single field change with old and new values
type PlanFieldChange struct {
	StepID   string      `json:"step_id"`   // Step ID that was changed
	Field    string      `json:"field"`     // Field name (title, description, success_criteria, etc.)
	OldValue interface{} `json:"old_value"` // Old value (can be nil if field didn't exist)
	NewValue interface{} `json:"new_value"` // New value
}

// Every plan-mod tool requires a `reason` field — a one-line rationale captured
// at tool-invocation time so the plan changelog can store the why alongside the
// diff. The schema fragment is hard-coded in each schema; keep them in sync.

// PlanChangelogEntry is one entry in the per-session plan changelog. Each
// successful plan-mod tool call appends one entry to the active session file
// under planning/changelog/.
type PlanChangelogEntry struct {
	ChangeID        string                       `json:"change_id"`
	Timestamp       string                       `json:"timestamp"`                 // ISO 8601 UTC
	Tool            string                       `json:"tool"`                      // tool name (e.g. "update_scripted_step")
	Reason          string                       `json:"reason"`                    // mandatory rationale supplied by the agent
	StepIDs         []string                     `json:"step_ids,omitempty"`        // affected step IDs
	Changes         []PlanFieldChange            `json:"changes,omitempty"`         // per-field old/new values when known
	AddedSteps      []json.RawMessage            `json:"added_steps,omitempty"`     // full JSON of steps added (for revert)
	DeletedSteps    []json.RawMessage            `json:"deleted_steps,omitempty"`   // full JSON of steps deleted (for revert)
	ArtifactReview  *PlanChangelogArtifactReview `json:"artifact_review,omitempty"` // Pulse Artifact Review completion metadata
	Target          string                       `json:"target"`                    // canonical artifact path or surface
	BeforeRef       string                       `json:"before_ref"`                // immutable digest of the pre-mutation state
	AfterRef        string                       `json:"after_ref"`                 // immutable digest of the post-mutation state
	NoOp            bool                         `json:"no_op,omitempty"`           // true when BeforeSnapshot/AfterSnapshot were both real and identical
	Actor           string                       `json:"actor"`                     // managed mutation authority
	DependencyClass string                       `json:"dependency_class"`          // review domain affected by the mutation
	Origin          PlanChangeOrigin             `json:"origin"`

	// BeforeSnapshot / AfterSnapshot are the actual target-artifact content
	// before and after the mutation, captured by the caller. When set, these
	// — not Changes — are what BeforeRef/AfterRef hash, so the recorded refs
	// always reflect the real artifact instead of a caller-reported diff
	// that can be incomplete or entirely absent (PLAT-033: before_ref ==
	// after_ref == sha256("[]") whenever a caller left Changes empty, even
	// though the artifact demonstrably changed). Never persisted — only the
	// resulting hashes are, not the raw content.
	BeforeSnapshot interface{} `json:"-"`
	AfterSnapshot  interface{} `json:"-"`
}

type PlanChangelogArtifactReview struct {
	Done          bool                                   `json:"done"`
	ReviewedAt    string                                 `json:"reviewed_at,omitempty"`
	ReviewedBy    string                                 `json:"reviewed_by,omitempty"`
	Result        string                                 `json:"result,omitempty"`
	ReportEntryID string                                 `json:"report_entry_id,omitempty"`
	Surfaces      map[string]PlanDependencySurfaceReview `json:"surfaces,omitempty"`
}

type PlanDependencySurfaceReview struct {
	Disposition string   `json:"disposition"`
	Evidence    []string `json:"evidence,omitempty"`
	IssueIDs    []string `json:"issue_ids,omitempty"`
}

// PlanChangelog is the per-session changelog file under planning/changelog/.
type PlanChangelog struct {
	Entries []PlanChangelogEntry `json:"entries"`
}

// Session-file tracking — one file per workshop run, rotated after an hour
// of inactivity.
var (
	planChangelogSessionMutex sync.Mutex
	planChangelogSessionFile  string
	planChangelogSessionStart time.Time
)

// asString safely extracts a string from a map[string]interface{} value,
// returning "" for nil / missing / non-string values.
func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// requireReason extracts and validates the mandatory `reason` argument that
// every plan-mod tool requires. Returns the trimmed reason or an error if
// missing/empty.
func requireReason(args map[string]interface{}) (string, error) {
	reason := strings.TrimSpace(asString(args["reason"]))
	if reason == "" {
		return "", fmt.Errorf("reason is required: provide a one-sentence rationale for this plan change (it will be appended to the plan changelog)")
	}
	return reason, nil
}

// logPlanChange is the non-fatal wrapper every plan-mod executor calls after a
// successful writePlanToFile. A changelog write failure is logged at warn
// level but never blocks the actual plan mutation.
func logPlanChange(
	ctx context.Context,
	workspacePath string,
	entry PlanChangelogEntry,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
	logger loggerv2.Logger,
) {
	if err := writePlanChangelogEntry(ctx, workspacePath, entry, readFile, writeFile, logger); err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Plan changelog write failed (non-fatal): %v", err))
	}
}

// writePlanChangelogEntry appends entry to the active session changelog file
// under planning/changelog/. The file is created if missing; subsequent calls
// within the same hour append to the same file. Errors are logged at warn
// level by the caller — a changelog write must never block the actual plan
// mutation.
func writePlanChangelogEntry(
	ctx context.Context,
	workspacePath string,
	entry PlanChangelogEntry,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
	logger loggerv2.Logger,
) error {
	planChangelogSessionMutex.Lock()
	defer planChangelogSessionMutex.Unlock()

	now := time.Now().UTC()
	if planChangelogSessionFile == "" || now.Sub(planChangelogSessionStart) > time.Hour {
		planChangelogSessionStart = now
		planChangelogSessionFile = fmt.Sprintf("changelog-%s.json", now.Format("2006-01-02-15-04-05"))
		logger.Info(fmt.Sprintf("📝 Plan changelog: starting new session file %s", planChangelogSessionFile))
	}

	if entry.Timestamp == "" {
		entry.Timestamp = now.Format(time.RFC3339)
	}
	entry.Origin = planChangeOriginFromContext(ctx, workspacePath)
	if entry.Origin.Type == "pulse_fixer" {
		entry.Origin.IssueIDs, entry.Origin.FixAttemptID, entry.Origin.HumanInputID = pulseChangeReferences(ctx, workspacePath, entry.Origin.PulseRunID)
		if entry.Origin.HumanInputID != "" {
			entry.Origin.Type = "human_decision"
		}
	}
	completePlanChangelogEntry(&entry)
	if entry.ChangeID == "" {
		entry.ChangeID = strings.TrimPrefix(artifactContentRef(map[string]interface{}{
			"timestamp": entry.Timestamp, "tool": entry.Tool, "target": entry.Target,
			"before_ref": entry.BeforeRef, "after_ref": entry.AfterRef,
			"session_id": entry.Origin.SessionID,
		}), "sha256:")[:20]
		entry.ChangeID = "change-" + entry.ChangeID
	}

	relPath := filepath.Join("planning", "changelog", planChangelogSessionFile)
	changelogPath := normalizePathForWorkspaceAPI(relPath, workspacePath)

	var clog PlanChangelog
	if existing, err := readFile(ctx, changelogPath); err == nil && strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &clog); err != nil {
			logger.Warn(fmt.Sprintf("⚠️ Plan changelog: failed to parse existing file, starting fresh: %v", err))
			clog = PlanChangelog{}
		}
	}
	clog.Entries = append(clog.Entries, entry)

	data, err := json.MarshalIndent(clog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan changelog: %w", err)
	}
	writeCtx := withPlanningFileMutationWriteAccess(ctx, workspacePath, filepath.Join("changelog", planChangelogSessionFile))
	if err := writeFile(writeCtx, changelogPath, string(data)); err != nil {
		return fmt.Errorf("write plan changelog: %w", err)
	}
	logger.Info(fmt.Sprintf("📝 Plan changelog: %s — %s", entry.Tool, entry.Reason))
	return nil
}

func artifactContentRef(value interface{}) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func completePlanChangelogEntry(entry *PlanChangelogEntry) {
	if entry == nil {
		return
	}
	tool := strings.ToLower(strings.TrimSpace(entry.Tool))
	if entry.Target == "" {
		switch {
		case strings.Contains(tool, "learning"):
			entry.Target = "learnings/_global"
		case strings.Contains(tool, "workflow_config"):
			entry.Target = "workflow.json"
		case strings.Contains(tool, "step_config"):
			entry.Target = "planning/step_config.json"
		default:
			entry.Target = "planning/plan.json"
		}
	}
	if entry.BeforeRef == "" {
		switch {
		case entry.BeforeSnapshot != nil:
			entry.BeforeRef = artifactContentRef(entry.BeforeSnapshot)
		case len(entry.DeletedSteps) > 0:
			// No explicit snapshot, but the caller captured the real
			// pre-mutation content as DeletedSteps (e.g. delete_plan_steps,
			// delete_todo_task_route) — hash that instead of collapsing to
			// the empty-Changes placeholder (PLAT-074).
			entry.BeforeRef = artifactContentRef(entry.DeletedSteps)
		default:
			before := make([]interface{}, 0, len(entry.Changes))
			for _, change := range entry.Changes {
				before = append(before, change.OldValue)
			}
			entry.BeforeRef = artifactContentRef(before)
		}
	}
	if entry.AfterRef == "" {
		switch {
		case entry.AfterSnapshot != nil:
			entry.AfterRef = artifactContentRef(entry.AfterSnapshot)
		case len(entry.AddedSteps) > 0:
			// Same fallback for the add side (e.g. add_scripted_step and its
			// siblings) — AddedSteps already carries the real added content.
			entry.AfterRef = artifactContentRef(entry.AddedSteps)
		default:
			after := make([]interface{}, 0, len(entry.Changes))
			for _, change := range entry.Changes {
				after = append(after, change.NewValue)
			}
			entry.AfterRef = artifactContentRef(after)
		}
	}
	// Only claim a no-op when both sides came from a real snapshot — an empty
	// Changes list with no snapshot is "unknown," not "nothing changed."
	if entry.BeforeSnapshot != nil && entry.AfterSnapshot != nil && entry.BeforeRef == entry.AfterRef {
		entry.NoOp = true
	}
	if entry.Actor == "" {
		entry.Actor = "managed_tool:" + strings.TrimSpace(entry.Tool)
	}
	if entry.DependencyClass == "" {
		switch {
		case strings.Contains(tool, "evaluation"):
			entry.DependencyClass = "evaluation_contract"
		case strings.Contains(tool, "learning"):
			entry.DependencyClass = "runtime_guidance"
		case strings.Contains(tool, "workflow_config") || strings.Contains(tool, "step_config"):
			entry.DependencyClass = "runtime_configuration"
		default:
			entry.DependencyClass = "workflow_plan"
		}
	}
}

// getUpdateRegularStepSchema returns the JSON schema for update_scripted_step.
func getUpdateRegularStepSchema() string {
	return `{
					"type": "object",
					"properties": {
						"existing_step_id": {
							"type": "string",
							"description": "REQUIRED: The ID of the step in the existing plan that you want to update. Use the step's id field from the plan."
						},
						"script_only": {"type": "boolean", "description": "For a Relay script node, execute saved main.py strictly, without agent repair."},
						"title": {
							"type": "string",
							"description": "OPTIONAL: New title for the step. Only include if you want to rename the step. If omitted, the existing title is preserved."
						},
			"description": {
				"type": "string",
				"description": "OPTIONAL: Replaces the deterministic execution contract implemented by code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py). Specify inputs, target-domain operations, persistence behavior, outputs, idempotency, error handling, and provenance/freshness requirements. Do not copy shared AgentWorks bridge/auth, Folder Guard, managed-tool, tool-discovery, or coding-session mechanics into this field. This is not an LLM prompt; conversational or judgment-heavy work belongs in update_message_sequence_step. Omit to preserve the existing description."
						},
						"script_parameters": {
							"type": "object",
							"description": "OPTIONAL: Replace the named runtime-input contract for main.py. Each value is {type, description, required?, default?, enum?}; pass {} to clear.",
							"additionalProperties": {"type": "object", "properties": {"type": {"type": "string", "enum": ["string", "number", "integer", "boolean", "array", "object"]}, "description": {"type": "string"}, "required": {"type": "boolean"}, "default": {}, "enum": {"type": "array"}}, "required": ["type", "description"]}
						},
						"context_dependencies": {
							"type": "array",
							"items": { "type": "string" },
							"description": "OPTIONAL: Updated context dependencies. Only include if you want to change them. If omitted, the existing context dependencies are preserved."
						},
						"context_output": {
							"type": "string",
							"description": "OPTIONAL: Updated context output. Only include if you want to change it. If omitted, the existing context output is preserved."
						},
						"next_step_id": {
							"type": "string",
							"description": "OPTIONAL: Explicit next scripted or shared step ID, or 'end'. Omit to preserve sequential execution."
						},
						"reason": {
							"type": "string",
							"description": "REQUIRED: One-sentence rationale for why this change is being made. Captured into the plan changelog. Be specific — 'tighten validation' is weak; 'add validation_schema for context_output to catch upstream JSON-shape regressions surfaced in iteration-3' is good."
						}
					},
					"required": ["existing_step_id", "reason"]
	}`
}

// getDeletePlanStepsSchema returns the JSON schema for delete_plan_steps tool
func getDeletePlanStepsSchema() string {
	return `{
		"type": "object",
		"properties": {
			"deleted_step_ids": {
				"type": "array",
				"items": { "type": "string" },
				"description": "IDs of steps to delete from the plan. Use the step's id field from the plan. The deletion is atomic and will be rejected if any remaining route, next_step_id, human-input response route, or message sequence still targets a deleted ID; reroute those references first, then retry."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why these steps are being removed. Captured into the plan changelog. Be specific — 'cleanup' is weak; 'remove step-3 (intent-classifier) — replaced by step-2 doing the classification inline' is good."
			}
		},
		"required": ["deleted_step_ids", "reason"]
	}`
}

// getCreatePlanSchema returns the JSON schema for the create_plan tool.
// The tool takes no arguments — it initializes an empty planning/plan.json
// so that subsequent add_*_step tools have a file to modify.
func getCreatePlanSchema() string {
	return `{
		"type": "object",
		"properties": {}
	}`
}

// getAddRegularStepSchema returns the JSON schema for add_scripted_step.
func getAddRegularStepSchema() string {
	return `{
		"type": "object",
		"properties": {
            "is_orphan": {"type": "boolean", "description": "Set true to create a reusable saved script in orphan_steps instead of the main flow, for scripted message-sequence batches. It does not auto-run. Use empty insert_after_step_id."},
			"script_only": {"type": "boolean", "description": "For a Relay script node, require an existing main.py and stop on any error without agent generation or repair."},
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this new step. Generate a unique, URL-friendly ID based on the step title (e.g., 'deploy-application' from 'Deploy Application')."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the new step"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED: Complete semantic execution contract for the checked-in code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py) script. Specify inputs, target-domain operations, persistence behavior, outputs, idempotency, error handling, and provenance/freshness requirements. Do not copy shared AgentWorks bridge/auth, Folder Guard, managed-tool, tool-discovery, or coding-session mechanics into this field. This is not an LLM prompt; conversational or judgment-heavy work belongs in add_message_sequence_step."
			},
			"script_parameters": {
				"type": "object",
				"description": "OPTIONAL: Named runtime inputs accepted by main.py when this scripted step is used as a todo-task route. Each key defines {type, description, required?, default?, enum?}. Supported types: string, number, integer, boolean, array, object. main.py reads the validated values from STEP_PARAMS_JSON. Use {} when the script takes no route parameters.",
				"additionalProperties": {"type": "object", "properties": {"type": {"type": "string", "enum": ["string", "number", "integer", "boolean", "array", "object"]}, "description": {"type": "string"}, "required": {"type": "boolean"}, "default": {}, "enum": {"type": "array"}}, "required": ["type", "description"]}
			},
			"context_dependencies": {
				"type": "array",
				"items": { "type": "string" },
				"description": "REQUIRED: List of context files from previous steps that this step depends on. Use empty array [] if no dependencies."
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: The context file this step creates for subsequent steps - e.g., 'step_1_results.json'. OMIT IT when the step's real output is the db (db/db.sqlite) — then validate the step with validation_schema.db instead of a file, and downstream steps read the db; this avoids a hand-written receipt file that drifts from the db. Provide it when a downstream step needs the result injected via context_dependencies, or you want a small file artifact. Execution agents work ONLY in the execution folder, so the file is written there. Keep JSON files SMALL (< 100KB); put large text (logs, content > 1KB) in a separate markdown file and reference it from JSON. JSON should hold structured data: counts, IDs, status, file references, brief summaries."
			},
			"next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Explicit next scripted or shared step ID, or 'end'. Use this to chain multiple scripted steps within one route and converge without falling through into sibling routes. Omit for sequential execution."
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string \"\" to insert at the beginning of the plan (before the first step)."
			},
			"validation_schema": {
				"type": "object",
				"description": "REQUIRED: Structured validation schema for fast code-based pre-validation. You MUST generate this by parsing the step description and extracting file names, field requirements, and validation rules. This enables pre-validation before LLM validation (improves speed by 50-70%). Structure: {files: [{file_name: string, must_exist: boolean, json_checks: [{path: string (JSONPath like $.field_name), must_exist: boolean, value_type?: string (string/number/boolean/array/object), min_length?: number, max_length?: number, pattern?: string (VALID Go regex - must compile with regexp.Compile, ensure balanced parentheses, escape special chars), min_value?: number, max_value?: number, consistency_check?: {type: string (array_length/equals/greater_than/less_than), compare_with_path: string}}]}]}. Example: If the step description requires 'File results.json contains status field and count field equals items array length', generate schema with file_name: 'results.json', json_checks: [{path: '$.status', must_exist: true}, {path: '$.count', must_exist: true, consistency_check: {type: 'array_length', compare_with_path: '$.items'}}]. CRITICAL FOR array_length CONSISTENCY CHECKS - AVOID COMMON MISTAKE: For array_length checks, path MUST point to COUNT/NUMBER field (e.g., '$.count', '$.total_expected_count', '$.length'), and compare_with_path MUST point to ARRAY field (e.g., '$.items', '$.downloaded_files', '$.files'). CORRECT examples: {path: '$.count', consistency_check: {type: 'array_length', compare_with_path: '$.items'}}, {path: '$.total_expected_count', consistency_check: {type: 'array_length', compare_with_path: '$.downloaded_files'}}. INCORRECT (SWAPPED) - DO NOT DO THIS: {path: '$.items', consistency_check: {type: 'array_length', compare_with_path: '$.count'}} ❌ WRONG - paths are swapped! Field name hints: Number fields often contain 'count', 'total', 'length', 'size', 'num'. Array fields often contain 'files', 'items', 'list', 'array', 'entries', 'results'. IMPORTANT: For pattern field, only use if you can generate a VALID Go regex pattern. Invalid patterns will be skipped. Examples of valid patterns: '^success$', '^\\d+$', '^[A-Za-z0-9_]+$'. Do NOT use incomplete patterns. DB CHECKS — to gate on the db source of truth instead of a file, add a 'db' array alongside 'files': {db: [{name, sql (read-only SELECT against db/db.sqlite), min_rows?, max_rows?, checks? (same json_check shape, asserted on the FIRST result row, which is an object keyed by column)}]}. min_rows:1 requires the query returns results; max_rows:0 requires none (e.g. a query for FAIL rows must return 0); checks with path '$.column' + pattern assert a column value on the first row. PREFER db checks when the step writes its results to db/db.sqlite — they validate what was actually produced, so the step needs no hand-written output file that can drift from the db.",
				"properties": {
					"files": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"file_name": {"type": "string", "description": "Path rules: a bare filename (e.g. 'results.json') is resolved under the step's execution folder. A path starting with 'db/' is resolved against the workflow-root 'db/' store; pre-validation accepts the file either there OR inside the step execution folder, since 'db/' and the step folder are the only two places a step may legally write. Paths starting with other workflow-root prefixes ('knowledgebase/', 'learnings/', 'planning/', etc.) are workflow-root-relative and must be present exactly at that location — those folders are written by dedicated agents, so a step-local copy is a bug and will fail validation."},
								"must_exist": {"type": "boolean"},
								"json_checks": {
									"type": "array",
									"items": {
										"type": "object",
										"properties": {
											"path": {"type": "string"},
											"must_exist": {"type": "boolean"},
											"value_type": {"type": "string"},
											"min_length": {"type": "number"},
											"max_length": {"type": "number"},
											"pattern": {"type": "string", "description": "OPTIONAL: Valid Go regex pattern for string format validation. MUST be valid Go regex syntax (use regexp.Compile). Common patterns: '^[A-Z]+$' (uppercase only), '^\\d{4}-\\d{2}-\\d{2}$' (date format), '^[a-z0-9-]+$' (lowercase alphanumeric with hyphens). CRITICAL: Ensure all parentheses are balanced, escape special characters (use \\\\ for backslash, \\\\( for literal parenthesis), and test your pattern. Invalid patterns will be skipped with a warning. Examples: '^success$', '^\\d+$', '^[A-Za-z0-9_]+$'. Avoid: incomplete patterns like 'VALUE\\\\(SUBSTITUTE\\\\(.*' (missing closing parentheses)."},
											"min_value": {"type": "number"},
											"max_value": {"type": "number"},
											"consistency_check": {
												"type": "object",
												"properties": {
													"type": {"type": "string", "description": "Type of consistency check: 'array_length' (CRITICAL - AVOID COMMON MISTAKE: path must point to COUNT/number field like '$.count' or '$.total_expected_count', compare_with_path must point to ARRAY field like '$.items' or '$.downloaded_files'. DO NOT SWAP THEM! Correct: path='$.count', compare_with_path='$.items'. Wrong: path='$.items', compare_with_path='$.count' ❌), 'equals', 'greater_than', 'less_than', or 'in_array'"},
													"compare_with_path": {"type": "string", "description": "REQUIRED: JSONPath to compare with. For 'array_length' type: MUST point to the ARRAY field (e.g., '$.items', '$.downloaded_files', '$.files', '$.databases', '$.entries'). DO NOT use number fields here! For other types: JSONPath to compare with (e.g., '$.count', '$.status'). Must be a valid, non-empty JSONPath string."}
												},
												"required": ["type", "compare_with_path"]
											}
										}
									}
								}
							}
						}
					}
				}
			}
			,
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this step is being added. Captured into the plan changelog. Be specific — 'add new step' is weak; 'add step-4 (verify-payment) — eval iteration-3 showed 30% of records skip payment verification when step-3 fails silently' is good."
			}
		},
		"required": ["id", "title", "description", "context_dependencies","insert_after_step_id", "validation_schema", "reason"]
	}`
}

func getAddMessageSequenceStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "REQUIRED: Stable URL-friendly step ID."},
			"title": {"type": "string", "description": "REQUIRED: Short title for the message sequence step."},
			"description": {"type": "string", "description": "REQUIRED: The durable system-level charter for the whole agent step: objective, boundaries, inputs, durable result, failure behavior, and definition of done. It is visible on every turn but is not itself a user turn. Do not copy shared AgentWorks bridge/auth, Folder Guard, managed-tool, tool-discovery, or coding-session mechanics here. Put ordered execution and verification instructions in items[]."},
			"authored_prompt": {"type": "boolean", "description": "Relay agent mode: run the exact system_prompt and user_message items without generated workflow turns. The final user_message must return valid JSON."},
			"system_prompt": {"type": "string", "description": "Required when authored_prompt is true. The author's system prompt; supports {{input}} for all caller JSON, {{input.field}} for a required field, and {{steps.id.output.field}} for earlier output."},
			"context_dependencies": {"type": "array", "items": {"type": "string"}, "description": "REQUIRED: Prior context files this sequence depends on. Use [] if none."},
			"context_output": {"type": "string", "description": "OPTIONAL: Summary/result file for later steps. Omit when the step writes its result to the db (validate via validation_schema.db)."},
			"items": {
				"type": "array",
				"description": "REQUIRED: Ordered user-message queue describing how to execute and verify the system-level step charter. Add a user_message for a coherent execution phase, evidence-based verification, critique, repair, new external input, or a real phase change; do not create one item per checklist line or tool call. Use explicit prevalidation items only for intermediate gates; the top-level validation_schema runs automatically after the final work turn. Use a scripted item to execute saved orphan scripts as a finite batch, without agentic children. Inline code is not supported. Each referenced script needs its own typed parameters and validation contract. Plain turns inherit step-level DB/KB/learnings writes. Use kind or a non-empty write_access only to narrow a turn to selected stores. Before authoring more than a single verify/repair turn, load references/message-sequence.md: read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/message-sequence.md\"}]).",
				"items": {
					"type": "object",
					"properties": {
						"id": {"type": "string"},
						"type": {"type": "string", "enum": ["user_message", "prevalidation", "foreach", "scripted"], "description": "user_message, prevalidation, foreach, or a deterministic scripted batch"},
						"kind": {"type": "string", "description": "Optional shorthand that narrows this turn's inherited writes to one store: learning, knowledgebase, or db. Explicit non-empty write_access overrides kind."},
						"title": {"type": "string"},
						"message": {"type": "string", "description": "For user_message items: a follow-up semantic workflow instruction run in the same persistent conversation. In a Relay authored step, use {{input}} for the complete caller JSON, {{input.field}} for a required field, or {{steps.id.output.field}} for earlier output. Do not use {{input.INPUT}} or {{variables.INPUT}}. Do not copy shared AgentWorks bridge/auth, Folder Guard, managed-tool, tool-discovery, or coding-session mechanics here. Prefer evidence-seeking validation/repair turns (e.g. \"Re-open the output. Did you actually satisfy every criterion? Quote the evidence, then fix every unsupported or incomplete item.\") over routine subtask instructions. For foreach items: a Go text/template rendered once per row of source, with the row bound to '.' (e.g. 'Process {{.id}}: {{.task}}')."},
						"write_access": {
							"type": "object",
							"description": "Optional item-scoped narrowing. Omit it to inherit the step-level DB/KB/learnings write permissions. A non-empty object limits this turn to the selected stores and can never exceed step-level access. Folder-level booleans only — NO per-file path scoping (a \"paths\" list is rejected).",
							"properties": {
								"knowledgebase": {"type": "boolean"},
								"db": {"type": "boolean"},
								"learnings": {"type": "boolean"}
							}
						},
						"validation_schema": {"type": "object", "description": "For prevalidation items: backend validation schema (a hard gate). Interleave prevalidation items with DIFFERENT schemas between turns to gate distinct claims one at a time."},
						"prevalidation": {"type": "object", "description": "Alias for validation_schema on a prevalidation item."},
						"source_sql": {"type": "string", "description": "For foreach items: a read-only SQL query against db/db.sqlite (e.g. \"SELECT id, name FROM tasks WHERE status='pending'\"). The runtime runs it and sends one user_message turn per result row, with the row (an object keyed by column) bound to '.' in the message template. Use this to reliably process every row a prior step wrote to the db."},
						"max_iterations": {"type": "number", "description": "For foreach items: optional cap on rows processed (0 = all). Excess rows are skipped and logged, never silently dropped."},
                        "scripted_steps": {"type": "array", "minItems": 1, "description": "For scripted items: saved regular scripts in orphan_steps. No agentic workers, arbitrary code, or dynamic routes. All calls are checked before execution. Failures stop the sequence after the batch has settled; no child LLM repair or automatic retry.", "items": {"type": "object", "additionalProperties": false, "properties": {"id": {"type": "string", "description": "Unique invocation ID; letters, digits, hyphens, underscores; max 128."}, "step_id": {"type": "string", "description": "ID of an existing scripted regular definition in orphan_steps."}, "parameters": {"type": "object", "description": "Values matching the script_parameters contract; supplied via STEP_PARAMS_JSON."}}, "required": ["id", "step_id"]}},
                        "max_parallel": {"type": "integer", "minimum": 0, "maximum": 8, "description": "For scripted items: concurrency limit, default 0/1 means sequential. Use >1 only for independent scripts with safe shared-store writes. Repeated calls to one script serialize. The next item waits for every call."}
					},
					"required": ["id", "type"]
				}
			},
			"predefined_routes": {
				"type": "array",
				"description": "OPTIONAL: Bounded specialist agents available to this message-sequence agent. The agent decides at runtime whether, when, and how often to call them. This does not prescribe execution order. Each route must provide route_id, route_name, condition, and either sub_agent_step or orphan_step_ref.",
				"items": {"type": "object"}
			},
			"next_step_id": {"type": "string", "description": "Optional next step ID or end."},
			"insert_after_step_id": {"type": "string", "description": "REQUIRED: Existing step ID to insert after, or empty string to insert first."},
			"validation_schema": {"type": "object", "description": "OPTIONAL: Step-level final validation contract. When present, the runtime automatically runs it after the configured work turns and before learning/knowledge closing turns. Validation failures are sent back to the same conversation for repair and retried."},
			"reason": {"type": "string", "description": "REQUIRED: One-sentence rationale for why this sequence step is being added."}
		},
		"required": ["id", "title", "description", "context_dependencies","items", "insert_after_step_id", "reason"]
	}`
}

func getUpdateMessageSequenceStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {"type": "string", "minLength": 1, "description": "REQUIRED: ID of the message_sequence step to update. Legacy non-scripted regular steps are accepted and atomically upgraded to message_sequence because that is already their effective runtime type."},
			"title": {"type": "string", "description": "OPTIONAL: New title. Omit to preserve the existing title."},
			"description": {"type": "string", "description": "OPTIONAL: Replaces the durable system-level charter for the whole agent step: objective, boundaries, inputs, durable result, failure behavior, and definition of done. It is visible on every turn but is not itself a user turn. Put ordered execution and verification instructions in items[]. Omit to preserve the existing description — do not resend it unchanged just to also change another field."},
			"authored_prompt": {"type": "boolean", "description": "Switch exact authored prompt mode on or off. When true, provide system_prompt and end items with a JSON-returning user_message."},
			"system_prompt": {"type": "string", "description": "Replace the authored system prompt. Supports {{input}} for all caller JSON, {{input.field}} for a required field, and {{steps.id.output.field}} for earlier output."},
			"context_dependencies": {"type": "array", "items": {"type": "string"}, "description": "OPTIONAL: Replaces the full list of prior context files this sequence depends on. Omit to preserve the existing list."},
			"context_output": {"type": "string", "description": "OPTIONAL: Replaces the summary/result file for later steps. Omit to preserve the existing value, or to leave the step writing its result to the db (validate via validation_schema.db) instead of a file."},
			"items": {
				"type": "array",
				"description": "OPTIONAL: Replaces the entire ordered user-message queue — this is a full replacement, not a merge, so include every item you want kept. Items describe how to execute and verify the system-level description charter. Add a user_message for a coherent execution phase, evidence-based verification, critique, repair, new external input, or a real phase change; do not create one item per checklist line or tool call. Use explicit prevalidation items only for intermediate gates; the top-level validation_schema runs automatically after the final work turn. Use a scripted item to execute saved orphan scripts as a finite batch, without agentic children. Inline code is not supported. Each referenced script needs its own typed parameters and validation contract. Plain turns inherit step-level DB/KB/learnings writes; use kind or a non-empty write_access only to narrow a turn to selected stores. Before restructuring items[] into anything beyond a single verify/repair turn, load references/message-sequence.md: read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/message-sequence.md\"}]).",
				"items": {"type": "object"}
			},
			"predefined_routes": {
				"type": "array",
				"description": "OPTIONAL: Replaces the bounded specialist routes available to this agent. The agent—not the plan queue—decides which routes to call. Include every route to keep; [] removes delegation.",
				"items": {"type": "object"}
			},
			"next_step_id": {"type": "string", "description": "OPTIONAL: Replaces the explicit next step ID, or 'end'. Omit to preserve sequential execution."},
			"validation_schema": {"type": "object", "description": "OPTIONAL: Replaces the step-level final validation contract. When present, the runtime automatically runs it after the configured work turns and before learning/knowledge closing turns; failures are sent back to the same conversation for repair and retried. Omit to preserve the existing schema. Keep it to the load-bearing checks a downstream step, evaluator, or user actually depends on — not every field the output happens to contain."},
			"reason": {"type": "string", "description": "REQUIRED: One-sentence rationale for this update."}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// getAddRoutingStepSchema returns the JSON schema for add_routing_step tool
func getAddRoutingStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this routing step. Generate a unique, URL-friendly ID based on the step title."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the routing step"
			},
						"description": {
							"type": "string",
				"description": "DO NOT SET for routing steps. Routing is deterministic-only and never executes an agent. If a probe/judgment is needed, add a prior message_sequence step that writes route_selection.json, then route from that file."
			},
			"context_dependencies": {
				"type": "array",
				"items": {"type": "string"},
				"description": "REQUIRED: List of context files from previous steps that this step depends on. For prior-step routing decisions, include route_selection.json and ensure a prior step declares route_selection.json in context_output. Use empty array [] if no dependencies."
			},
			"context_output": {
				"type": "string",
				"description": "Do not set for routing steps. Routing steps do not create outputs; a prior message_sequence step should declare route_selection.json in its context_output when it produces the route decision."
			},
			"routing_question": {
				"type": "string",
				"description": "REQUIRED for compatibility/readability: Human-readable route decision prompt. It is not evaluated by an LLM at runtime; the selected route must come from caller route_selections, route_source_file, route_selection.json dependency, or default_route_id."
			},
			"routes": {
				"type": "array",
				"minItems": 2,
				"items": {
					"type": "object",
					"properties": {
						"route_id": {
							"type": "string",
							"description": "REQUIRED: Unique identifier for this route (e.g., 'route_positive', 'route_negative')"
						},
						"route_name": {
							"type": "string",
							"description": "REQUIRED: Human-readable name for this route (e.g., 'Positive Sentiment')"
						},
						"condition": {
							"type": "string",
							"description": "REQUIRED: Description of when this route should be selected (e.g., 'Select when sentiment analysis indicates positive sentiment')"
						},
						"next_step_id": {
							"type": "string",
							"description": "REQUIRED: ID of step to route to when this route is selected. Use step ID from the plan, or 'end' to terminate workflow."
						}
					},
					"required": ["route_id", "route_name", "condition", "next_step_id"]
				},
				"description": "REQUIRED: Array of possible routes (minimum 2). Each route has a route_id, route_name, condition description, and next_step_id."
			},
			"default_route_id": {
				"type": "string",
				"description": "OPTIONAL: Fallback route_id to use when no route_selection.json is available. Must match one of the route_ids in routes."
			},
			"route_source_file": {
				"type": "string",
				"description": "OPTIONAL: Explicit route_selection.json source file, usually a context output from a prior step. The file must contain {\"select_route\":\"<route_id>\"}."
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string to insert at the beginning."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this routing step is being added. Captured into the plan changelog."
			}
		},
		"required": ["id", "title", "routing_question", "routes", "context_dependencies", "insert_after_step_id", "reason"]
	}`
}

// getUpdateRoutingStepSchema returns the JSON schema for update_routing_step tool
func getUpdateRoutingStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the routing step to update. Use the step's id field from the plan."
			},
			"title": {
				"type": "string",
				"description": "OPTIONAL: New title for the step."
			},
			"description": {
				"type": "string",
				"description": "DO NOT SET for routing steps. Routing is deterministic-only and never executes an agent. Use clear_description=true to remove a legacy routing description; put any probe/judgment in a prior message_sequence step that writes route_selection.json."
			},
			"clear_description": {
				"type": "boolean",
				"description": "OPTIONAL: Set true to clear a legacy routing description during migration. New routing steps must keep description empty."
			},
			"context_dependencies": {
				"type": "array",
				"items": { "type": "string" },
				"description": "OPTIONAL: Updated context dependencies. Use route_selection.json when consuming a prior step's deterministic route decision."
			},
			"context_output": {
				"type": "string",
				"description": "Do not set for routing steps. Routing steps do not create outputs; route_selection.json should be produced by a prior message_sequence step or preseeded by caller route_selections."
			},
			"routing_question": {
				"type": "string",
				"description": "OPTIONAL: Updated human-readable routing prompt. Runtime does not LLM-evaluate this field."
			},
			"routes": {
				"type": "array",
				"minItems": 2,
				"items": {
					"type": "object",
					"properties": {
						"route_id": {"type": "string"},
						"route_name": {"type": "string"},
						"condition": {"type": "string"},
						"next_step_id": {"type": "string"}
					},
					"required": ["route_id", "route_name", "condition", "next_step_id"]
				},
				"description": "OPTIONAL: Updated routes array (minimum 2)."
			},
			"default_route_id": {
				"type": "string",
				"description": "OPTIONAL: Updated default route ID."
			},
			"route_source_file": {
				"type": "string",
				"description": "OPTIONAL: Updated explicit route_selection.json source file produced by a prior step."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this routing step is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// getAddBranchStepSchema returns the JSON schema for add_branch_step tool. Same
// shape as add_routing_step -- branch_question instead of routing_question --
// since branch is the small in-flow decision, routing is now the "route"
// (major sub-workflow fork) concept. See PLAT-259.
func getAddBranchStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this branch step. Generate a unique, URL-friendly ID based on the step title."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the branch step"
			},
			"description": {
				"type": "string",
				"description": "DO NOT SET for branch steps. Branch is deterministic-only and never executes an agent. If a probe/judgment is needed, add a prior message_sequence step that writes route_selection.json, then route from that file."
			},
			"context_dependencies": {
				"type": "array",
				"items": {"type": "string"},
				"description": "REQUIRED: List of context files from previous steps that this step depends on. For prior-step decisions, include route_selection.json and ensure a prior step declares route_selection.json in context_output. Use empty array [] if no dependencies."
			},
			"context_output": {
				"type": "string",
				"description": "Do not set for branch steps. Branch steps do not create outputs; a prior message_sequence step should declare route_selection.json in its context_output when it produces the decision."
			},
			"branch_question": {
				"type": "string",
				"description": "REQUIRED for compatibility/readability: Human-readable next-step decision prompt. It is not evaluated by an LLM at runtime; the selected route must come from caller route_selections, route_source_file, route_selection.json dependency, or default_route_id."
			},
			"value_path": {"type": "string", "description": "Relay decision: one {{input.field}} or {{steps.id.output.field}} reference."},
			"value_cases": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Relay decision: map exact rendered values to route_id; unmatched values use default_route_id or fail."},
			"routes": {
				"type": "array",
				"minItems": 2,
				"items": {
					"type": "object",
					"properties": {
						"route_id": {
							"type": "string",
							"description": "REQUIRED: Unique identifier for this route (e.g., 'route_positive', 'route_negative')"
						},
						"route_name": {
							"type": "string",
							"description": "REQUIRED: Human-readable name for this route (e.g., 'Positive Sentiment')"
						},
						"condition": {
							"type": "string",
							"description": "REQUIRED: Description of when this route should be selected (e.g., 'Select when sentiment analysis indicates positive sentiment')"
						},
						"next_step_id": {
							"type": "string",
							"description": "REQUIRED: ID of step to route to when this route is selected. Use step ID from the plan, or 'end' to terminate workflow."
						}
					},
					"required": ["route_id", "route_name", "condition", "next_step_id"]
				},
				"description": "REQUIRED: Array of possible routes (minimum 2). Each route has a route_id, route_name, condition description, and next_step_id."
			},
			"default_route_id": {
				"type": "string",
				"description": "OPTIONAL: Fallback route_id to use when no route_selection.json is available. Must match one of the route_ids in routes."
			},
			"route_source_file": {
				"type": "string",
				"description": "OPTIONAL: Explicit route_selection.json source file, usually a context output from a prior step. The file must contain {\"select_route\":\"<route_id>\"}."
			},
			"route_source": {
				"type": "string",
				"enum": ["human"],
				"description": "OPTIONAL: Set to \"human\" when a person decides this branch mid-run (approve/hold, yes/no, pick one). The routes are the options shown (route_name is the label) and branch_question is the prompt. Preseeded sources still win: caller route_selections, route_selection.json, route_source_file or a context_dependencies entry answer it without asking -- that is how schedules answer up front. Unattended runs use default_route_id (set it to the safe do-nothing route) or fail. Successor to yesno/multiple_choice human_input, which add_human_input_step no longer accepts."
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string to insert at the beginning."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this branch step is being added. Captured into the plan changelog."
			}
		},
		"required": ["id", "title", "branch_question", "routes", "context_dependencies", "insert_after_step_id", "reason"]
	}`
}

// getUpdateBranchStepSchema returns the JSON schema for update_branch_step tool
func getUpdateBranchStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the branch step to update. Use the step's id field from the plan."
			},
			"title": {
				"type": "string",
				"description": "OPTIONAL: New title for the step."
			},
			"description": {
				"type": "string",
				"description": "DO NOT SET for branch steps. Branch is deterministic-only and never executes an agent. Use clear_description=true to remove a legacy description; put any probe/judgment in a prior message_sequence step that writes route_selection.json."
			},
			"clear_description": {
				"type": "boolean",
				"description": "OPTIONAL: Set true to clear a legacy description during migration. New branch steps must keep description empty."
			},
			"context_dependencies": {
				"type": "array",
				"items": { "type": "string" },
				"description": "OPTIONAL: Updated context dependencies. Use route_selection.json when consuming a prior step's deterministic decision."
			},
			"context_output": {
				"type": "string",
				"description": "Do not set for branch steps. Branch steps do not create outputs; route_selection.json should be produced by a prior message_sequence step or preseeded by caller route_selections."
			},
			"branch_question": {
				"type": "string",
				"description": "OPTIONAL: Updated human-readable branch prompt. Runtime does not LLM-evaluate this field."
			},
			"value_path": {"type": "string", "description": "Relay decision: one {{input.field}} or {{steps.id.output.field}} reference."},
			"value_cases": {"type": "object", "additionalProperties": {"type": "string"}, "description": "Relay decision: replace exact value to route_id mapping."},
			"routes": {
				"type": "array",
				"minItems": 2,
				"items": {
					"type": "object",
					"properties": {
						"route_id": {"type": "string"},
						"route_name": {"type": "string"},
						"condition": {"type": "string"},
						"next_step_id": {"type": "string"}
					},
					"required": ["route_id", "route_name", "condition", "next_step_id"]
				},
				"description": "OPTIONAL: Updated routes array (minimum 2)."
			},
			"default_route_id": {
				"type": "string",
				"description": "OPTIONAL: Updated default route ID."
			},
			"route_source_file": {
				"type": "string",
				"description": "OPTIONAL: Updated explicit route_selection.json source file produced by a prior step."
			},
			"route_source": {
				"type": "string",
				"enum": ["human"],
				"description": "OPTIONAL: Set to \"human\" so this branch asks the person when no route was preseeded (routes are the options, branch_question is the prompt); unattended runs use default_route_id."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this branch step is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// getAddHumanInputStepSchema returns the JSON schema for add_human_input_step tool
func getAddHumanInputStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this human input step. Generate a unique, URL-friendly ID based on the step title (e.g., 'ask-month' from 'Ask Month')."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the human input step"
			},
			"question": {
				"type": "string",
				"description": "REQUIRED: The question to ask the human. This will be displayed to the user and execution will block until they respond."
			},
			"response_type": {
				"type": "string",
				"enum": ["text"],
				"description": "OPTIONAL: New human input steps accept text only (default), for a free-form value. For a fixed choice or approval, use add_branch_step with route_source=human. Existing legacy choice steps can still be maintained with update_human_input_step."
			},
			"options": {
				"type": "array",
				"items": { "type": "string" },
				"description": "Legacy field: omit for new text steps. Use add_branch_step with route_source=human for a fixed choice; maintain existing choice steps with update_human_input_step."
			},
			"variable_name": {
				"type": "string",
				"description": "OPTIONAL: Variable name to store the human's response. If provided, the response will be stored in this variable and can be referenced in subsequent steps using {{variable_name}}."
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: Context file name to save the response (e.g., 'step-1.json'). Defaults to 'step-{index}.json' if not specified. The response will be saved as JSON with question, response, response_type, timestamp, and step details."
			},
			"next_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the next step to execute after receiving the response, or 'end' to terminate the workflow. This is the default for text responses and the fallback when no response-specific route matches."
			},
			"if_yes_next_step_id": {
				"type": "string",
				"description": "Legacy field: omit for new text steps. Use add_branch_step with route_source=human for a fixed choice; maintain existing choice steps with update_human_input_step."
			},
			"if_no_next_step_id": {
				"type": "string",
				"description": "Legacy field: omit for new text steps. Use add_branch_step with route_source=human for a fixed choice; maintain existing choice steps with update_human_input_step."
			},
			"option_routes": {
				"type": "object",
				"additionalProperties": { "type": "string" },
				"description": "Legacy field: omit for new text steps. Use add_branch_step with route_source=human for a fixed choice; maintain existing choice steps with update_human_input_step."
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string \"\" to insert at the beginning of the plan (before the first step)."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this human-input step is being added. Captured into the plan changelog."
			}
		},
		"required": ["id", "title", "question", "next_step_id", "insert_after_step_id", "reason"]
	}`
}

// getAddOrchestratorStepSchema returns the JSON schema for add_todo_task_step tool
func getAddOrchestratorStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "REQUIRED: Stable step ID for this todo task step. Generate a unique, URL-friendly ID based on the step title (e.g., 'process-data-tasks' from 'Process Data Tasks')."
			},
			"title": {
				"type": "string",
				"description": "REQUIRED: Short, clear title for the todo task step"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED: The durable system-level charter for this legacy orchestrator shape: overall objective, boundaries, and definition of done. Ordered execution instructions belong in messages[]."
			},
			"context_dependencies": {
				"type": "array",
				"items": {"type": "string"},
				"description": "REQUIRED: List of context files from previous steps. Use empty array [] if no dependencies."
			},
			"context_output": {
				"type": "string",
				"description": "REQUIRED: Context file this step will create with final summary."
			},
			"validation_schema": {
				"type": "object",
				"description": "OPTIONAL: Validation schema for the step output",
				"properties": {
					"files": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"file_name": {"type": "string", "description": "Path rules: a bare filename (e.g. 'results.json') is resolved under the step's execution folder. A path starting with 'db/' is resolved against the workflow-root 'db/' store; pre-validation accepts the file either there OR inside the step execution folder, since 'db/' and the step folder are the only two places a step may legally write. Paths starting with other workflow-root prefixes ('knowledgebase/', 'learnings/', 'planning/', etc.) are workflow-root-relative and must be present exactly at that location — those folders are written by dedicated agents, so a step-local copy is a bug and will fail validation."},
								"must_exist": {"type": "boolean"},
								"json_checks": {"type": "array", "items": {"type": "object"}}
							}
						}
					}
				}
			},
			"predefined_routes": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"route_id": {
							"type": "string",
							"description": "REQUIRED: Unique ID for this predefined route (e.g., 'api-fetcher', 'data-transformer')"
						},
						"route_name": {
							"type": "string",
							"description": "REQUIRED: Human-readable name for this route (e.g., 'API Data Fetcher', 'Data Transformer')"
						},
						"condition": {
							"type": "string",
							"description": "REQUIRED: Description of when to use this predefined agent (e.g., 'For tasks requiring API calls', 'For data transformation tasks')"
						},
						"sub_agent_step": {
							"type": "object",
							"description": "REQUIRED: The sub-agent step definition. Use type='message_sequence' for every new conversational or judgment-heavy specialist, even a one-turn specialist. Use type='regular' only for a deterministic scripted boundary, or type='todo_task' for one nested orchestrator layer.",
							"properties": {
								"type": {"type": "string", "enum": ["message_sequence", "regular", "orchestrator", "todo_task"], "description": "REQUIRED: Use message_sequence for conversational work. Regular is scripted-only. Nested todo_task routes may not contain another todo_task route."},
								"id": {"type": "string", "description": "REQUIRED: Stable step ID for the sub-agent step"},
								"title": {"type": "string", "description": "REQUIRED: Title of the sub-agent step"},
								"description": {"type": "string", "description": "REQUIRED: What this specialized agent does AND its standing brief. For scripted regular routes this is the stable code contract; runtime variation belongs in script_parameters. For agent routes, per-call instructions are added on top."},
								"script_parameters": {"type": "object", "description": "For regular scripted routes: named values main.py accepts through STEP_PARAMS_JSON. Each value is {type, description, required?, default?, enum?}.", "additionalProperties": {"type": "object", "properties": {"type": {"type": "string", "enum": ["string", "number", "integer", "boolean", "array", "object"]}, "description": {"type": "string"}, "required": {"type": "boolean"}, "default": {}, "enum": {"type": "array"}}, "required": ["type", "description"]}},
								"script_parameters_schema": {"type": "object", "description": "For regular scripted routes, instead of script_parameters: one full JSON Schema (type object) for the whole parameters object, for nested or constrained inputs (nested objects, patterns, min/max, additionalProperties false). Declare one or the other, never both. The route's named tool uses it as its input schema."},
								"items": {"type": "array", "description": "REQUIRED when type='message_sequence'. Ordered user_message, prevalidation, or foreach turns.", "items": {"type": "object"}},
								"context_dependencies": {"type": "array", "items": {"type": "string"}},
								"context_output": {"type": "string", "description": "OPTIONAL: Context file this step creates. Omit when the step writes to the db (validate via validation_schema.db)."},
								"predefined_routes": {"type": "array", "description": "When type='todo_task', nested predefined routes for the child todo task."},
								"next_step_id": {"type": "string", "description": "When type='todo_task', child next step ID. Ignored when used as a sub-agent."},
								"validation_schema": {
									"type": "object",
									"description": "REQUIRED: Validation schema for the sub-agent output",
									"properties": {
										"files": {
											"type": "array",
											"items": {
												"type": "object",
												"properties": {
													"file_name": {"type": "string", "description": "Path rules: a bare filename (e.g. 'results.json') is resolved under the step's execution folder. A path starting with 'db/' is resolved against the workflow-root 'db/' store; pre-validation accepts the file either there OR inside the step execution folder, since 'db/' and the step folder are the only two places a step may legally write. Paths starting with other workflow-root prefixes ('knowledgebase/', 'learnings/', 'planning/', etc.) are workflow-root-relative and must be present exactly at that location — those folders are written by dedicated agents, so a step-local copy is a bug and will fail validation."},
													"must_exist": {"type": "boolean"},
													"json_checks": {"type": "array", "items": {"type": "object"}}
												}
											}
										}
									}
								}
							},
							"required": ["type", "id", "title"]
						}
					},
					"required": ["route_id", "route_name", "condition", "sub_agent_step"]
				},
				"description": "OPTIONAL: Array of predefined routes for specialized sub-agents. These agents have learning and prevalidation. Use for tasks that benefit from specialized handling and accumulated learnings."
			},
			"next_step_id": {
				"type": "string",
				"description": "REQUIRED: ID of step to connect to after all todos are complete, or 'end' to end the workflow."
			},
			"messages": {
				"type": "array",
				"description": "OPTIONAL: Scripted message sequence for long, multi-phase work on one orchestrator step. After the orchestrator's first turn, each entry is fed into the SAME orchestrator conversation in order, so it keeps going with full memory of prior turns and sub-agent results. Runs within one execution; NO persistence/re-entry. A foreach entry iterates a db array (e.g. one a prior step wrote) and feeds one orchestrator turn per row — reliable enumeration of every row. For a specialist that resumes across the orchestrator's own repeated calls, use a message_sequence route instead.",
				"items": {
					"type": "object",
					"properties": {
						"id": {"type": "string"},
						"type": {"type": "string", "description": "user_message (alias: message; default), prevalidation, or foreach. Same message_sequence item shape, but the orchestrator runs only these conversational kinds — code/file items must go to a sub-agent route."},
						"message": {"type": "string", "description": "message entries: the instruction for one orchestrator turn (e.g. a follow-up phase, or 'now verify X and fix any gaps'). foreach entries: a Go text/template rendered once per row of source, row bound to '.' (e.g. 'Handle task {{.id}}: {{.desc}}')."},
						"validation_schema": {"type": "object", "description": "prevalidation entries: a hard gate checked between turns. On failure the orchestrator receives the failures as a corrective turn and retries up to max_corrections."},
						"max_corrections": {"type": "number", "description": "prevalidation entries only: corrective orchestrator turns allowed on gate failure (default 1)."},
						"source_sql": {"type": "string", "description": "foreach entries: a read-only SQL query against db/db.sqlite (e.g. \"SELECT id, name FROM tasks WHERE status='pending'\"). The orchestrator gets one turn per result row, with the row bound to '.' — reliable processing of every row a prior step wrote to the db."},
						"max_iterations": {"type": "number", "description": "foreach entries: optional cap on rows processed (0 = all)."}
					},
					"required": ["type"]
				}
			},
			"insert_after_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step to insert after. Use the step's id field from the plan. Use empty string to insert at the beginning."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this todo-task step is being added. Captured into the plan changelog."
			}
		},
		"required": ["id", "title", "description", "context_dependencies","next_step_id", "insert_after_step_id", "reason"]
	}`
}

// getUpdateOrchestratorStepSchema returns the JSON schema for update_todo_task_step tool
func getUpdateOrchestratorStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the todo task step to update. Use the step's id field from the plan."
			},
			"title": {
				"type": "string",
				"description": "OPTIONAL: New title for the todo task step"
			},
			"description": {
				"type": "string",
				"description": "OPTIONAL: Updated description of the overall objective"
			},
			"context_dependencies": {
				"type": "array",
				"items": {"type": "string"},
				"description": "OPTIONAL: Updated list of context files from previous steps"
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: Updated context file this step will create"
			},
			"validation_schema": {
				"type": "object",
				"description": "OPTIONAL: Updated validation schema"
			},
			"predefined_routes": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"route_id": {
							"type": "string",
							"description": "Unique ID for this predefined route"
						},
						"route_name": {
							"type": "string",
							"description": "Human-readable name for this route"
						},
						"condition": {
							"type": "string",
							"description": "Description of when to use this predefined agent"
						},
						"sub_agent_step": {
							"type": "object",
							"description": "The sub-agent step definition. Use message_sequence for conversational or judgment-heavy work, regular only for deterministic scripted work, or todo_task for one nested orchestrator layer.",
							"properties": {
								"type": {"type": "string", "enum": ["message_sequence", "regular", "orchestrator", "todo_task"]},
								"id": {"type": "string"},
								"title": {"type": "string"},
								"description": {"type": "string"},
								"items": {"type": "array", "description": "Required when type='message_sequence'.", "items": {"type": "object"}},
								"context_dependencies": {"type": "array", "items": {"type": "string"}},
								"context_output": {"type": "string"},
								"predefined_routes": {"type": "array", "description": "When type='todo_task', nested predefined routes for the child todo task."},
								"next_step_id": {"type": "string", "description": "When type='todo_task', child next step ID. Ignored when used as a sub-agent."},
								"validation_schema": {"type": "object"}
							}
						}
					}
				},
				"description": "OPTIONAL: Updated array of predefined routes. This REPLACES the existing routes."
			},
			"next_step_id": {
				"type": "string",
				"description": "OPTIONAL: ID of step to connect to after all todos are complete, or 'end'"
			},
			"messages": {
				"type": "array",
				"description": "OPTIONAL: Replaces the scripted message sequence. After the orchestrator's first turn, each entry is fed into the same orchestrator conversation in order. Entries use the same message_sequence item shape {id, type, message, validation_schema, max_corrections, source_sql, max_iterations}, but the orchestrator runs only the CONVERSATIONAL kinds — type: user_message (or message), prevalidation, foreach. It cannot run code/file items itself; delegate that to a sub-agent route.",
				"items": {"type": "object"}
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this todo-task step is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// getAddOrchestratorRouteSchema returns the JSON schema for add_todo_task_route tool
func getAddOrchestratorRouteSchema() string {
	return `{
		"type": "object",
		"properties": {
			"parent_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the todo task step (parent step). Use the step's id field from the plan."
			},
			"new_route": {
				"type": "object",
				"description": "REQUIRED: The new predefined route to add. Must include all required fields.",
				"properties": {
					"route_id": {
						"type": "string",
						"description": "REQUIRED: Unique ID for this route (e.g., 'api-fetcher', 'data-transformer')"
					},
					"route_name": {
						"type": "string",
						"description": "REQUIRED: Human-readable name for this route (e.g., 'API Data Fetcher', 'Data Transformer')"
					},
					"condition": {
						"type": "string",
						"description": "REQUIRED: Description of when to use this predefined agent (e.g., 'For tasks requiring API calls')"
					},
					"orphan_step_ref": {
						"type": "string",
						"description": "OPTIONAL: ID of a reusable orphan step from orphan_steps in this same plan. Use this instead of sub_agent_step when you want the route to reuse a shared orphan step definition that is shared_with this parent orchestrator."
					},
					"sub_agent_step": {
						"type": "object",
						"description": "OPTIONAL: The inline sub-agent step definition. Use type='message_sequence' for every new conversational or judgment-heavy specialist, even one turn. Use type='regular' only for a deterministic scripted boundary, or type='todo_task' for one nested orchestrator layer. Omit this when using orphan_step_ref.",
						"properties": {
							"type": {"type": "string", "enum": ["message_sequence", "regular", "orchestrator", "todo_task"], "description": "REQUIRED: message_sequence for conversational work; regular only for deterministic scripted work; todo_task for one nested orchestrator layer."},
							"id": {"type": "string", "description": "REQUIRED: Stable step ID for the sub-agent step"},
							"title": {"type": "string", "description": "REQUIRED: Title of the sub-agent step"},
							"description": {"type": "string", "description": "REQUIRED: What this specialized agent does AND its standing brief. For scripted regular routes this is the stable code contract; runtime variation belongs in script_parameters. For agent routes, per-call instructions are added on top."},
							"script_parameters": {"type": "object", "description": "For regular scripted routes: named values main.py accepts through STEP_PARAMS_JSON. Each value is {type, description, required?, default?, enum?}.", "additionalProperties": {"type": "object", "properties": {"type": {"type": "string", "enum": ["string", "number", "integer", "boolean", "array", "object"]}, "description": {"type": "string"}, "required": {"type": "boolean"}, "default": {}, "enum": {"type": "array"}}, "required": ["type", "description"]}},
							"script_parameters_schema": {"type": "object", "description": "For regular scripted routes, instead of script_parameters: one full JSON Schema (type object) for the whole parameters object, for nested or constrained inputs (nested objects, patterns, min/max, additionalProperties false). Declare one or the other, never both. The route's named tool uses it as its input schema."},
							"items": {"type": "array", "description": "REQUIRED when type='message_sequence'. Ordered user_message, prevalidation, or foreach turns.", "items": {"type": "object"}},
							"context_dependencies": {"type": "array", "items": {"type": "string"}, "description": "Exact durable file outputs this child consumes. The runtime resolves and injects these files. Use [] when the child reads durable state from managed DB/KB tools instead."},
							"context_output": {"type": "string", "description": "OPTIONAL: Context file this step creates. Omit when the step writes to the db (validate via validation_schema.db)."},
							"todo_task_step": {"type": "object", "description": "When type='todo_task': the nested orchestrator's inner regular step metadata."},
							"predefined_routes": {"type": "array", "description": "When type='todo_task': predefined routes for the nested orchestrator. Conversational children use message_sequence; deterministic scripted children may use regular. Another todo_task layer is not allowed."},
							"validation_schema": {
								"type": "object",
								"description": "OPTIONAL: Validation schema for the sub-agent output"
							}
						},
						"required": ["type", "id", "title"]
					}
				},
				"required": ["route_id", "route_name", "condition"]
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this todo-task route is being added. Captured into the plan changelog."
			}
		},
		"required": ["parent_step_id", "new_route", "reason"]
	}`
}

// getUpdateOrchestratorRouteSchema returns the JSON schema for update_todo_task_route tool
func getUpdateOrchestratorRouteSchema() string {
	return `{
		"type": "object",
		"properties": {
			"parent_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the todo task step (parent step). Use the step's id field from the plan."
			},
			"existing_route_id": {
				"type": "string",
				"description": "REQUIRED: The route_id of the route to update. Use the route's route_id field from the plan."
			},
			"route_name": {
				"type": "string",
				"description": "OPTIONAL: Updated route name. Only include if you want to change it."
			},
			"condition": {
				"type": "string",
				"description": "OPTIONAL: Updated condition description. Only include if you want to change it."
			},
			"orphan_step_ref": {
				"type": "string",
				"description": "OPTIONAL: Reference a reusable orphan step from orphan_steps in this same plan. When set, the route will resolve that orphan step for this orchestrator instead of using an inline sub_agent_step."
			},
			"sub_agent_step": {
				"type": "object",
				"description": "OPTIONAL: Updated inline sub-agent step. Use message_sequence for conversational or judgment-heavy work, regular only for deterministic scripted work, or todo_task for one nested orchestrator layer. Omit this when using orphan_step_ref.",
				"properties": {
					"type": {"type": "string", "enum": ["message_sequence", "regular", "orchestrator", "todo_task"]},
					"id": {"type": "string"},
					"title": {"type": "string"},
					"description": {"type": "string", "description": "OPTIONAL: Replaces the stable route contract. For scripted regular routes, runtime variation belongs in script_parameters. Omit to preserve the existing description."},
					"script_parameters": {"type": "object", "description": "For regular scripted routes: replace the named STEP_PARAMS_JSON contract. Each value is {type, description, required?, default?, enum?}.", "additionalProperties": {"type": "object", "properties": {"type": {"type": "string", "enum": ["string", "number", "integer", "boolean", "array", "object"]}, "description": {"type": "string"}, "required": {"type": "boolean"}, "default": {}, "enum": {"type": "array"}}, "required": ["type", "description"]}},
					"script_parameters_schema": {"type": "object", "description": "For regular scripted routes, instead of script_parameters: one full JSON Schema (type object) for the whole parameters object, for nested or constrained inputs (nested objects, patterns, min/max, additionalProperties false). Declare one or the other, never both. The route's named tool uses it as its input schema."},
					"items": {"type": "array", "description": "Required when type='message_sequence'. Replaces the entire ordered user-message queue — a full replacement, not a merge. The description is the system charter; items explain how to execute and verify it. Add a user_message for a coherent phase, evidence-based verification, critique, repair, new input, or a real phase change. Before restructuring this into anything beyond a single verify/repair turn, load references/message-sequence.md: read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/message-sequence.md\"}]).", "items": {"type": "object"}},
					"context_dependencies": {"type": "array", "items": {"type": "string"}, "description": "Exact durable file outputs this child consumes. The runtime resolves and injects these files. Use [] when the child reads durable state from managed DB/KB tools instead."},
					"context_output": {"type": "string"},
					"todo_task_step": {"type": "object", "description": "When type='todo_task': nested orchestrator inner step metadata."},
					"predefined_routes": {"type": "array", "description": "When type='todo_task': nested routes may use message_sequence or scripted regular, but not another todo_task layer."},
						"validation_schema": {"type": "object"}
				},
				"required": ["type", "id", "title"]
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this todo-task route is being updated. Captured into the plan changelog."
			}
		},
		"required": ["parent_step_id", "existing_route_id", "reason"]
	}`
}

// getDeleteOrchestratorRouteSchema returns the JSON schema for delete_todo_task_route tool
func getDeleteOrchestratorRouteSchema() string {
	return `{
		"type": "object",
		"properties": {
			"parent_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the todo task step (parent step). Use the step's id field from the plan."
			},
			"deleted_route_id": {
				"type": "string",
				"description": "REQUIRED: The route_id of the route to delete. Use the route's route_id field from the plan."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this todo-task route is being deleted. Captured into the plan changelog."
			}
		},
		"required": ["parent_step_id", "deleted_route_id", "reason"]
	}`
}

// getUpdateHumanInputStepSchema returns the JSON schema for update_human_input_step tool
func getUpdateHumanInputStepSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the human input step to update. Use the step's id field from the plan."
			},
			"title": {
				"type": "string",
				"description": "OPTIONAL: New title for the step. Only include if you want to rename the step. If omitted, the existing title is preserved."
			},
			"question": {
				"type": "string",
				"description": "OPTIONAL: Updated question to ask the human. Only include if you want to change it. If omitted, the existing question is preserved."
			},
			"response_type": {
				"type": "string",
				"enum": ["text", "yesno", "multiple_choice"],
				"description": "OPTIONAL: Updated response type. Only include if you want to change it. 'text' (default) for free-form text, 'yesno' for yes/no questions, 'multiple_choice' for selecting from options. If omitted, the existing response type is preserved."
			},
			"options": {
				"type": "array",
				"items": { "type": "string" },
				"description": "OPTIONAL: Updated options for multiple_choice response type. Only include if you want to change them. Required if response_type is 'multiple_choice'. If omitted, the existing options are preserved."
			},
			"variable_name": {
				"type": "string",
				"description": "OPTIONAL: Updated variable name to store the response. Only include if you want to change it. If omitted, the existing variable name is preserved."
			},
			"context_output": {
				"type": "string",
				"description": "OPTIONAL: Updated context file name to save the response. Only include if you want to change it. Defaults to 'step-{index}.json' if not specified. If omitted, the existing context output is preserved. CRITICAL: Keep JSON files SMALL (< 100KB). For large text content, store it in a separate markdown file and reference it from JSON."
			},
			"next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Updated next step ID. Only include if you want to change it. The ID of the next step to execute after receiving the response, or 'end' to terminate the workflow. If omitted, the existing next_step_id is preserved."
			},
			"if_yes_next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Updated if_yes_next_step_id. Only include if you want to change it. For 'yesno' response type, the step ID to route to when user responds 'yes', or 'end' to terminate. If omitted, the existing value is preserved."
			},
			"if_no_next_step_id": {
				"type": "string",
				"description": "OPTIONAL: Updated if_no_next_step_id. Only include if you want to change it. For 'yesno' response type, the step ID to route to when user responds 'no', or 'end' to terminate. If omitted, the existing value is preserved."
			},
			"option_routes": {
				"type": "object",
				"additionalProperties": { "type": "string" },
				"description": "OPTIONAL: Updated option routes. Only include if you want to change them. For 'multiple_choice' response type, maps option index (as string '0', '1', etc.) or option value to next_step_id. If omitted, the existing option routes are preserved."
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this human-input step is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "reason"]
	}`
}

// getUpdateValidationSchemaSchema returns the JSON schema for update_validation_schema tool
// updateToolForStepType names the tool that edits a given step type.
//
// A refusal that says only "use its type-specific update tool" tells an agent it
// was wrong without telling it what is right, and the reasonable next conclusion
// is that the step cannot be edited at all. rtslatency shows the cost: a fixer
// tried update_scripted_step on two message_sequence collectors, was correctly
// refused, and recorded "legacy agentic regular step, not editable" as a blocked
// finding. It re-reported that for days while update_message_sequence_step sat in
// its own tool surface the whole time.
func updateToolForStepType(stepType StepType) string {
	switch stepType {
	case StepTypeMessageSeq:
		return "update_message_sequence_step"
	case StepTypeOrchestrator:
		return "update_orchestrator_step"
	case StepTypeRouting:
		return "update_routing_step"
	case StepTypeBranch:
		return "update_branch_step"
	case StepTypeHumanInput:
		return "update_human_input_step"
	case StepTypeRegular:
		return "update_scripted_step"
	case StepTypeCrew:
		return "update_crew_step"
	default:
		return ""
	}
}

// wrongStepTypeToolError explains what the step is and which tool edits it, so a
// refusal cannot be mistaken for an unsupported step.
func wrongStepTypeToolError(stepID string, actual StepType, attempted string) error {
	if tool := updateToolForStepType(actual); tool != "" {
		return fmt.Errorf(
			"step %q is a %s step, so %s does not apply — use %s instead. The step is editable; only the tool was wrong",
			stepID, actual, attempted, tool,
		)
	}
	return fmt.Errorf("step %q is a %s step, which %s does not handle and no update tool covers", stepID, actual, attempted)
}

func getUpdateValidationSchemaSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "REQUIRED: The ID of the step in the existing plan that you want to update. Use the step's id field from the plan."
			},
			"validation_schema": {
				"type": "object",
				"description": "REQUIRED: Structured validation schema for fast code-based pre-validation. You MUST generate this by parsing the step description and extracting file names, field requirements, and validation rules. This enables pre-validation before LLM validation (improves speed by 50-70%). Structure: {files: [{file_name: string, must_exist: boolean, json_checks: [{path: string (JSONPath like $.field_name), must_exist: boolean, value_type?: string (string/number/boolean/array/object), min_length?: number, max_length?: number, pattern?: string (VALID Go regex - must compile with regexp.Compile, ensure balanced parentheses, escape special chars), min_value?: number, max_value?: number, consistency_check?: {type: string (array_length/equals/greater_than/less_than), compare_with_path: string}}]}]}. Example: If the step description requires 'File results.json contains status field and count field equals items array length', generate schema with file_name: 'results.json', json_checks: [{path: '$.status', must_exist: true}, {path: '$.count', must_exist: true, consistency_check: {type: 'array_length', compare_with_path: '$.items'}}]. CRITICAL FOR array_length CONSISTENCY CHECKS - AVOID COMMON MISTAKE: For array_length checks, path MUST point to COUNT/NUMBER field (e.g., '$.count', '$.total_expected_count', '$.length'), and compare_with_path MUST point to ARRAY field (e.g., '$.items', '$.downloaded_files', '$.files'). CORRECT examples: {path: '$.count', consistency_check: {type: 'array_length', compare_with_path: '$.items'}}, {path: '$.total_expected_count', consistency_check: {type: 'array_length', compare_with_path: '$.downloaded_files'}}. INCORRECT (SWAPPED) - DO NOT DO THIS: {path: '$.items', consistency_check: {type: 'array_length', compare_with_path: '$.count'}} ❌ WRONG - paths are swapped! Field name hints: Number fields often contain 'count', 'total', 'length', 'size', 'num'. Array fields often contain 'files', 'items', 'list', 'array', 'entries', 'results'. IMPORTANT: For pattern field, only use if you can generate a VALID Go regex pattern. Invalid patterns will be skipped. Examples of valid patterns: '^success$', '^\\d+$', '^[A-Za-z0-9_]+$'. Do NOT use incomplete patterns. DB CHECKS — to gate on the db source of truth instead of a file, add a 'db' array alongside 'files': {db: [{name, sql (read-only SELECT against db/db.sqlite), min_rows?, max_rows?, checks? (same json_check shape, asserted on the FIRST result row, which is an object keyed by column)}]}. min_rows:1 requires the query returns results; max_rows:0 requires none (e.g. a query for FAIL rows must return 0); checks with path '$.column' + pattern assert a column value on the first row. PREFER db checks when the step writes its results to db/db.sqlite — they validate what was actually produced, so the step needs no hand-written output file that can drift from the db.",
				"properties": {
					"files": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"file_name": {"type": "string", "description": "Path rules: a bare filename (e.g. 'results.json') is resolved under the step's execution folder. A path starting with 'db/' is resolved against the workflow-root 'db/' store; pre-validation accepts the file either there OR inside the step execution folder, since 'db/' and the step folder are the only two places a step may legally write. Paths starting with other workflow-root prefixes ('knowledgebase/', 'learnings/', 'planning/', etc.) are workflow-root-relative and must be present exactly at that location — those folders are written by dedicated agents, so a step-local copy is a bug and will fail validation."},
								"must_exist": {"type": "boolean"},
								"json_checks": {
									"type": "array",
									"items": {
										"type": "object",
										"properties": {
											"path": {"type": "string"},
											"must_exist": {"type": "boolean"},
											"value_type": {"type": "string"},
											"min_length": {"type": "number"},
											"max_length": {"type": "number"},
											"pattern": {"type": "string", "description": "OPTIONAL: Valid Go regex pattern for string format validation. MUST be valid Go regex syntax (use regexp.Compile). Common patterns: '^[A-Z]+$' (uppercase only), '^\\d{4}-\\d{2}-\\d{2}$' (date format), '^[a-z0-9-]+$' (lowercase alphanumeric with hyphens). CRITICAL: Ensure all parentheses are balanced, escape special characters (use \\\\ for backslash, \\\\( for literal parenthesis), and test your pattern. Invalid patterns will be skipped with a warning. Examples: '^success$', '^\\d+$', '^[A-Za-z0-9_]+$'. Avoid: incomplete patterns like 'VALUE\\\\(SUBSTITUTE\\\\(.*' (missing closing parentheses)."},
											"min_value": {"type": "number"},
											"max_value": {"type": "number"},
											"consistency_check": {
												"type": "object",
												"properties": {
													"type": {"type": "string", "description": "Type of consistency check: 'array_length' (CRITICAL - AVOID COMMON MISTAKE: path must point to COUNT/number field like '$.count' or '$.total_expected_count', compare_with_path must point to ARRAY field like '$.items' or '$.downloaded_files'. DO NOT SWAP THEM! Correct: path='$.count', compare_with_path='$.items'. Wrong: path='$.items', compare_with_path='$.count' ❌), 'equals', 'greater_than', 'less_than', or 'in_array'"},
													"compare_with_path": {"type": "string", "description": "REQUIRED: JSONPath to compare with. For 'array_length' type: MUST point to the ARRAY field (e.g., '$.items', '$.downloaded_files', '$.files', '$.databases', '$.entries'). DO NOT use number fields here! For other types: JSONPath to compare with (e.g., '$.count', '$.status'). Must be a valid, non-empty JSONPath string."}
												},
												"required": ["type", "compare_with_path"]
											}
										}
									}
								}
							}
						}
					}
				}
			},
			"reason": {
				"type": "string",
				"description": "REQUIRED: One-sentence rationale for why this validation schema is being updated. Captured into the plan changelog."
			}
		},
		"required": ["existing_step_id", "validation_schema", "reason"]
	}`
}

// readPlanFromFile reads plan.json from the workspace.
// Uses normalizePathForWorkspaceAPI to build the full path, so the readFile function
// does not need to auto-prepend the workspace path (works with both orchestrator and chat-mode readers).
func readPlanFromFile(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) (*PlanningResponse, error) {
	return readPlanFromFileWithGraphValidation(ctx, workspacePath, readFile, true)
}

// readPlanForMutation permits a structurally valid plan with dangling graph
// references to be loaded by the update tools that can repair those fields.
// The invalid graph can never be persisted again because writePlanToFile runs
// the complete ValidatePlanStructure check.
func readPlanForMutation(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error)) (*PlanningResponse, error) {
	return readPlanFromFileWithGraphValidation(ctx, workspacePath, readFile, false)
}

func readPlanFromFileWithGraphValidation(ctx context.Context, workspacePath string, readFile func(context.Context, string) (string, error), validateGraph bool) (*PlanningResponse, error) {
	planPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "plan.json"), workspacePath)

	planFileMutex.Lock()
	defer planFileMutex.Unlock()

	content, err := readFile(ctx, planPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan.json: %w", err)
	}

	var plan PlanningResponse
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan.json: %w", err)
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		return nil, fmt.Errorf("failed to resolve orphan step references in plan.json: %w", err)
	}
	validate := validateLoadedPlanStructureCore
	if validateGraph {
		validate = validateLoadedPlanStructure
	}
	if err := validate(&plan); err != nil {
		// A plan whose only defect is a pre-v1.0.10 message_sequence "code"
		// item can still be LOADED here for editing — e.g. so
		// delete_plan_steps or update_message_sequence_step can remove or
		// repair the offending step — even though it fails strict
		// validation. It can never execute or be persisted in this state:
		// execution's own preflight and every
		// writePlanToFile call independently enforce the strict validator.
		// The shared current-plan reader uses this fallback so mutation tools
		// aren't the one path left
		// unable to even open a legacy plan to fix it.
		validateLegacy := validateLoadedPlanStructureCoreAllowLegacyMessageSequenceCode
		if validateGraph {
			validateLegacy = validateLoadedPlanStructureAllowLegacyMessageSequenceCode
		}
		if legacyErr := validateLegacy(&plan); legacyErr != nil {
			return nil, fmt.Errorf("plan.json uses an invalid or legacy format: %w", err)
		}
	}

	return &plan, nil
}

// writePlanToFile writes PlanningResponse to plan.json in the workspace.
// Validates that all steps have IDs before saving (planning agent should always generate them).
// Uses normalizePathForWorkspaceAPI to build the full path.
func writePlanToFile(ctx context.Context, workspacePath string, plan *PlanningResponse, _ func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, logger loggerv2.Logger) error {
	planPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "plan.json"), workspacePath)

	planFileMutex.Lock()
	defer planFileMutex.Unlock()

	if err := ValidatePlanStructure(plan); err != nil {
		return err
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal plan: %w", err)
	}

	if err := writeFile(ctx, planPath, string(data)); err != nil {
		return fmt.Errorf("failed to write plan.json: %w", err)
	}

	return nil
}

// convertMapToStep converts a map[string]interface{} to PlanStepInterface
// Uses the same logic as PlanningResponse.UnmarshalJSON
func convertMapToStep(stepMap map[string]interface{}) (PlanStepInterface, error) {
	// Check for type field
	stepType, ok := stepMap["type"].(string)
	if !ok || stepType == "" {
		return nil, fmt.Errorf("step is missing required 'type' field")
	}

	// Marshal to JSON and unmarshal into appropriate type
	stepJSON, err := json.Marshal(stepMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal step: %w", err)
	}

	var typedStep PlanStepInterface
	switch stepType {
	case "regular":
		var step RegularPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse regular step: %w", err)
		}
		typedStep = &step
	case "crew":
		var step CrewPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse crew step: %w", err)
		}
		step.Type = StepTypeCrew
		typedStep = &step
	case "human_input":
		var step HumanInputPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse human_input step: %w", err)
		}
		typedStep = &step
	case "orchestrator", "todo_task":
		var step OrchestratorPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse orchestrator step: %w", err)
		}
		step.Type = StepTypeOrchestrator
		typedStep = &step
	case "routing":
		var step RoutingPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse routing step: %w", err)
		}
		typedStep = &step
	case "branch":
		var step BranchPlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse branch step: %w", err)
		}
		typedStep = &step
	case "message_sequence":
		var step MessageSequencePlanStep
		if err := json.Unmarshal(stepJSON, &step); err != nil {
			return nil, fmt.Errorf("failed to parse message_sequence step: %w", err)
		}
		typedStep = &step
	default:
		return nil, fmt.Errorf("unknown step type %q", stepType)
	}

	return typedStep, nil
}

// unmarshalStepFromJSON unmarshals a single step from JSON by checking its type field
// This is a helper function used by custom UnmarshalJSON methods for step types with nested steps
func unmarshalStepFromJSON(stepData json.RawMessage) (PlanStepInterface, error) {
	// Check for type field
	var stepWithType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(stepData, &stepWithType); err != nil {
		return nil, fmt.Errorf("failed to parse step type: %w", err)
	}

	// Default to "regular" type if not specified (common for sub_agent_steps in routes)
	stepType := stepWithType.Type
	if stepType == "" {
		stepType = "regular"
	}

	// Unmarshal based on type
	var typedStep PlanStepInterface
	switch stepType {
	case "regular":
		var step RegularPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse regular step: %w", err)
		}
		// Ensure Type field is set (may be empty if not in JSON)
		step.Type = StepTypeRegular
		typedStep = &step
	case "crew":
		var step CrewPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse crew step: %w", err)
		}
		step.Type = StepTypeCrew
		typedStep = &step
	case "human_input":
		var step HumanInputPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse human_input step: %w", err)
		}
		step.Type = StepTypeHumanInput
		typedStep = &step
	case "orchestrator", "todo_task":
		var step OrchestratorPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse orchestrator step: %w", err)
		}
		step.Type = StepTypeOrchestrator
		typedStep = &step
	case "routing":
		var step RoutingPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse routing step: %w", err)
		}
		step.Type = StepTypeRouting
		typedStep = &step
	case "branch":
		var step BranchPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse branch step: %w", err)
		}
		step.Type = StepTypeBranch
		typedStep = &step
	case "message_sequence":
		var step MessageSequencePlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse message_sequence step: %w", err)
		}
		step.Type = StepTypeMessageSeq
		typedStep = &step
	default:
		return nil, fmt.Errorf("unknown step type %q (must be: regular, human_input, todo_task, routing, or message_sequence)", stepType)
	}

	return typedStep, nil
}

// validateRegexPatternsInSchema validates all regex patterns in a ValidationSchema
// Returns an error with details if any pattern is invalid
// Note: Patterns are already fixed during JSON unmarshaling (see ValidationSchema.UnmarshalJSON),
// but we keep a safety net here in case patterns come from other sources
func validateRegexPatternsInSchema(schema *ValidationSchema) error {
	if schema == nil {
		return nil
	}

	var errors []string
	forEachSchemaCheck(schema, func(label string, check *JSONValidationCheck) {
		if check.Pattern == "" {
			return
		}
		// Safety net: Fix double-escaped patterns if they weren't fixed during unmarshaling
		// (e.g., if schema was created programmatically rather than from JSON)
		fixedPattern := fixDoubleEscapedPattern(check.Pattern)
		if fixedPattern != check.Pattern {
			// Update the pattern in the schema to the fixed version
			check.Pattern = fixedPattern
		}

		// Validate the pattern
		if _, err := regexp.Compile(check.Pattern); err != nil {
			errors = append(errors, fmt.Sprintf("%s at path '%s': invalid regex pattern: %v (pattern: '%s')", label, check.Path, err, check.Pattern))
		}
	})

	if len(errors) > 0 {
		return fmt.Errorf("validation schema contains invalid regex patterns:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// validateValueTypePatternCompatibility rejects a check that pairs a non-string
// value_type with a pattern. validatePattern (pre_validation.go) only ever
// matches string values -- for any other value_type, the actual pattern check
// always fails regardless of the real data, since a genuinely well-typed
// boolean/number/array/object value can never also be a string. A schema
// combining these two constraints on one path is unsatisfiable by
// construction: no JSON value can ever pass it. PLAT-236 traced two
// (already independently fixed) Twitter/social-media findings to exactly
// this shape reaching a live step's validation_schema undetected.
func validateValueTypePatternCompatibility(schema *ValidationSchema) error {
	if schema == nil {
		return nil
	}

	var errors []string
	forEachSchemaCheck(schema, func(label string, check *JSONValidationCheck) {
		if check.Pattern == "" {
			return
		}
		valueType := strings.TrimSpace(check.ValueType)
		if valueType != "" && valueType != "string" {
			errors = append(errors, fmt.Sprintf(
				"%s: path '%s' sets both value_type=%q and a pattern, but pattern only ever matches string values -- this combination can never pass for any real data. Remove the pattern or drop value_type to \"string\"",
				label, check.Path, valueType,
			))
		}
	})

	if len(errors) > 0 {
		return fmt.Errorf("validation schema has an unsatisfiable value_type/pattern combination:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// validateJSONPathSyntax validates that all JSONPaths in the schema are syntactically valid
func validateJSONPathSyntax(schema *ValidationSchema) error {
	if schema == nil {
		return nil
	}

	var errors []string
	dummyData := map[string]interface{}{}

	forEachSchemaCheck(schema, func(label string, check *JSONValidationCheck) {
		// Validate check.Path
		path := strings.TrimSpace(check.Path)
		if path == "" {
			errors = append(errors, fmt.Sprintf("%s: Empty path is not allowed", label))
		} else {
			if !strings.HasPrefix(path, "$.") {
				errors = append(errors, fmt.Sprintf("%s: Path '%s' must start with '$.'", label, path))
			} else {
				// Check syntax by attempting to evaluate against dummy data
				_, err := jsonpath.Get(path, dummyData)
				if err != nil && strings.Contains(err.Error(), "parsing error") {
					errors = append(errors, fmt.Sprintf("%s: Invalid JSONPath syntax in '%s': %v", label, path, err))
				}
			}
		}

		// Validate consistency_check if it exists and is actually specified
		// Skip validation if both Type and CompareWithPath are empty (treat as not specified)
		if check.ConsistencyCheck != nil {
			comparePath := strings.TrimSpace(check.ConsistencyCheck.CompareWithPath)
			checkType := strings.TrimSpace(check.ConsistencyCheck.Type)

			// Only validate if at least one field is specified
			if comparePath == "" && checkType == "" {
				// Both empty - treat as if consistency_check wasn't specified, skip validation
			} else if comparePath == "" || checkType == "" {
				// One is specified but not the other - error
				if comparePath == "" {
					errors = append(errors, fmt.Sprintf("%s: compare_with_path is required when consistency check type is specified", label))
				}
				if checkType == "" {
					errors = append(errors, fmt.Sprintf("%s: type is required when consistency check compare_with_path is specified", label))
				}
			} else {
				// Both specified - validate the path syntax
				if !strings.HasPrefix(comparePath, "$.") {
					errors = append(errors, fmt.Sprintf("%s: compare_with_path '%s' must start with '$.'", label, comparePath))
				} else {
					// Check syntax
					_, err := jsonpath.Get(comparePath, dummyData)
					if err != nil && strings.Contains(err.Error(), "parsing error") {
						errors = append(errors, fmt.Sprintf("%s: Invalid JSONPath syntax in compare_with_path '%s': %v", label, comparePath, err))
					}
				}
			}
		}
	})

	if len(errors) > 0 {
		return fmt.Errorf("validation schema contains invalid JSONPaths:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

// detectFieldTypeFromPath analyzes a JSONPath to determine if it likely points to a number/count field or an array field
// Returns: (isNumberField, isArrayField)
func detectFieldTypeFromPath(path string) (bool, bool) {
	pathLower := strings.ToLower(path)

	// Number/count field indicators
	numberIndicators := []string{"count", "total", "length", "size", "num", "number", "amount", "quantity", "sum"}
	for _, indicator := range numberIndicators {
		if strings.Contains(pathLower, indicator) {
			return true, false
		}
	}

	// Array field indicators
	arrayIndicators := []string{"files", "items", "list", "array", "entries", "results", "data", "records", "elements", "objects", "values"}
	for _, indicator := range arrayIndicators {
		if strings.Contains(pathLower, indicator) {
			return false, true
		}
	}

	return false, false
}

// validateArrayLengthConsistencyChecks validates array_length consistency checks in a ValidationSchema
// This performs basic structural validation and detects ambiguous paths based on field name patterns
// Runtime validation (pre_validation.go) supports bidirectional checks (array_length(count, array) OR array_length(array, count))
func validateArrayLengthConsistencyChecks(schema *ValidationSchema) error {
	if schema == nil {
		return nil
	}

	var errors []string
	forEachSchemaCheck(schema, func(label string, check *JSONValidationCheck) {
		if check.ConsistencyCheck == nil || check.ConsistencyCheck.Type != "array_length" {
			return
		}

		path := strings.TrimSpace(check.Path)
		comparePath := strings.TrimSpace(check.ConsistencyCheck.CompareWithPath)

		// Basic validation: paths must be non-empty and different
		if path == "" {
			errors = append(errors, fmt.Sprintf(
				"%s: array_length consistency check has empty path. Path must be a valid JSONPath.",
				label))
			return
		}

		if comparePath == "" {
			errors = append(errors, fmt.Sprintf(
				"%s: array_length consistency check has empty compare_with_path. compare_with_path must be a valid JSONPath.",
				label))
			return
		}

		if path == comparePath {
			errors = append(errors, fmt.Sprintf(
				"%s: array_length consistency check has path '%s' equal to compare_with_path '%s'. They must be different - one should point to a COUNT/number field, the other to an ARRAY field.",
				label, path, comparePath))
			return
		}

		// Detect field types to check for ambiguity
		pathIsNumber, pathIsArray := detectFieldTypeFromPath(path)
		compareIsNumber, compareIsArray := detectFieldTypeFromPath(comparePath)

		// Check for ambiguous configurations
		if pathIsArray && compareIsArray {
			errors = append(errors, fmt.Sprintf(
				"%s: AMBIGUOUS CHECK! Both path '%s' and compare_with_path '%s' contain array field indicators. One must be a NUMBER/COUNT field. Please check your paths.",
				label, path, comparePath))
		} else if pathIsNumber && compareIsNumber {
			errors = append(errors, fmt.Sprintf(
				"%s: AMBIGUOUS CHECK! Both path '%s' and compare_with_path '%s' contain number field indicators. One must be an ARRAY field. Please check your paths.",
				label, path, comparePath))
		}

		// Note: We no longer enforce strict order (Number, Array) vs (Array, Number)
		// because the runtime validator now handles both cases.
		// We only flag if both look like the SAME type.
	})

	if len(errors) > 0 {
		return fmt.Errorf("validation schema contains misconfigured array_length consistency checks:\n%s", strings.Join(errors, "\n"))
	}

	return nil
}

var _ = updateValidationSchemaOnStep

// updateValidationSchemaOnStep updates validation schema on any step type
func updateValidationSchemaOnStep(step PlanStepInterface, schema *ValidationSchema) {
	switch s := step.(type) {
	case *RegularPlanStep:
		s.ValidationSchema = schema
	case *HumanInputPlanStep:
		s.ValidationSchema = schema
	case *OrchestratorPlanStep:
		s.ValidationSchema = schema
	case *RoutingPlanStep:
		s.ValidationSchema = schema
	case *BranchPlanStep:
		s.ValidationSchema = schema
	case *MessageSequencePlanStep:
		s.ValidationSchema = schema
	case *CrewPlanStep:
		s.ValidationSchema = schema
	}
}

// compareNestedStepFields compares two PlanStepInterface objects and tracks all field changes
// prefix is used to create hierarchical field names (e.g., "orchestration_step.description")
func compareNestedStepFields(oldStep PlanStepInterface, newStep PlanStepInterface, stepID string, prefix string, fieldChanges *[]PlanFieldChange) {
	if oldStep == nil && newStep == nil {
		return
	}

	// If one is nil, track the entire step as changed
	if oldStep == nil {
		newStepJSON, _ := json.Marshal(newStep)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix,
			OldValue: nil,
			NewValue: string(newStepJSON),
		})
		return
	}
	if newStep == nil {
		oldStepJSON, _ := json.Marshal(oldStep)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix,
			OldValue: string(oldStepJSON),
			NewValue: nil,
		})
		return
	}

	// Compare common fields
	if oldStep.GetTitle() != newStep.GetTitle() {
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix + ".title",
			OldValue: oldStep.GetTitle(),
			NewValue: newStep.GetTitle(),
		})
	}
	if oldStep.GetDescription() != newStep.GetDescription() {
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix + ".description",
			OldValue: oldStep.GetDescription(),
			NewValue: newStep.GetDescription(),
		})
	}
	// Compare context dependencies
	oldDeps := oldStep.GetContextDependencies()
	newDeps := newStep.GetContextDependencies()
	if !equalStringSlices(oldDeps, newDeps) {
		// Store as JSON for proper revert
		oldDepsJSON, _ := json.Marshal(oldDeps)
		newDepsJSON, _ := json.Marshal(newDeps)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix + ".context_dependencies",
			OldValue: string(oldDepsJSON),
			NewValue: string(newDepsJSON),
		})
	}

	// Compare context output
	oldOutput := oldStep.GetContextOutput().String()
	newOutput := newStep.GetContextOutput().String()
	if oldOutput != newOutput {
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix + ".context_output",
			OldValue: oldOutput,
			NewValue: newOutput,
		})
	}

	// Compare validation schema
	oldSchema := oldStep.GetValidationSchema()
	newSchema := newStep.GetValidationSchema()
	if !equalValidationSchemas(oldSchema, newSchema) {
		oldSchemaJSON := "nil"
		if oldSchema != nil {
			oldBytes, _ := json.Marshal(oldSchema)
			oldSchemaJSON = string(oldBytes)
		}
		newSchemaJSON := "nil"
		if newSchema != nil {
			newBytes, _ := json.Marshal(newSchema)
			newSchemaJSON = string(newBytes)
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   stepID,
			Field:    prefix + ".validation_schema",
			OldValue: oldSchemaJSON,
			NewValue: newSchemaJSON,
		})
	}

	// Compare type-specific fields
	switch oldS := oldStep.(type) {
	case *RegularPlanStep:
		// No type-specific fields to diff (loop fields removed)

	case *MessageSequencePlanStep:
		if newS, ok := newStep.(*MessageSequencePlanStep); ok {
			oldItemsJSON, _ := json.Marshal(oldS.Items)
			newItemsJSON, _ := json.Marshal(newS.Items)
			if string(oldItemsJSON) != string(newItemsJSON) {
				*fieldChanges = append(*fieldChanges, PlanFieldChange{
					StepID:   stepID,
					Field:    prefix + ".items",
					OldValue: string(oldItemsJSON),
					NewValue: string(newItemsJSON),
				})
			}
			oldRoutesJSON, _ := json.Marshal(oldS.PredefinedRoutes)
			newRoutesJSON, _ := json.Marshal(newS.PredefinedRoutes)
			if string(oldRoutesJSON) != string(newRoutesJSON) {
				*fieldChanges = append(*fieldChanges, PlanFieldChange{
					StepID:   stepID,
					Field:    prefix + ".predefined_routes",
					OldValue: string(oldRoutesJSON),
					NewValue: string(newRoutesJSON),
				})
			}
		}

	}
}

// Helper functions for comparison
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalValidationSchemas(a, b *ValidationSchema) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

func equalOrchestrationRoutes(a, b []PlanOrchestrationRoute) bool {
	if len(a) != len(b) {
		return false
	}
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

// mergePartialStepUpdate merges a PartialPlanStep update into an existing PlanStepInterface
// Uses type switches to handle each step type appropriately
func mergePartialStepUpdate(existingStep PlanStepInterface, partialUpdate PartialPlanStep) PlanStepInterface {
	// Use type switch to handle each step type
	switch step := existingStep.(type) {
	case *RegularPlanStep:
		// Create updated copy
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.NextStepID != "" {
			updated.NextStepID = partialUpdate.NextStepID
		}
		if partialUpdate.ScriptOnly != nil {
			updated.ScriptOnly = *partialUpdate.ScriptOnly
		}
		// Loop fields ignored (feature removed)
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		if partialUpdate.ScriptParameters != nil {
			updated.ScriptParameters = partialUpdate.ScriptParameters
		}
		if partialUpdate.ScriptParametersSchema != nil {
			updated.ScriptParametersSchema = partialUpdate.ScriptParametersSchema
			if len(partialUpdate.ScriptParametersSchema) == 0 {
				updated.ScriptParametersSchema = nil
			}
		}
		// Validation schema is LLM-generated only - no code-based auto-generation
		return &updated

	case *HumanInputPlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.Question != "" {
			updated.Question = partialUpdate.Question
		}
		if partialUpdate.VariableName != "" {
			updated.VariableName = partialUpdate.VariableName
		}
		if partialUpdate.ResponseType != "" {
			updated.ResponseType = partialUpdate.ResponseType
		}
		if partialUpdate.Options != nil {
			updated.Options = partialUpdate.Options
		}
		if partialUpdate.NextStepID != "" {
			updated.NextStepID = partialUpdate.NextStepID
		}
		if partialUpdate.IfYesNextStepID != "" {
			updated.IfYesNextStepID = partialUpdate.IfYesNextStepID
		}
		if partialUpdate.IfNoNextStepID != "" {
			updated.IfNoNextStepID = partialUpdate.IfNoNextStepID
		}
		if partialUpdate.OptionRoutes != nil {
			updated.OptionRoutes = partialUpdate.OptionRoutes
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		return &updated

	case *MessageSequencePlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.Items != nil {
			updated.Items = partialUpdate.Items
		}
		if partialUpdate.AuthoredPrompt != nil {
			updated.AuthoredPrompt = *partialUpdate.AuthoredPrompt
		}
		if partialUpdate.SystemPrompt != nil {
			updated.SystemPrompt = *partialUpdate.SystemPrompt
		}
		if partialUpdate.PredefinedRoutes != nil {
			updated.PredefinedRoutes = append([]PlanOrchestrationRoute(nil), partialUpdate.PredefinedRoutes...)
		}
		if partialUpdate.NextStepID != "" {
			updated.NextStepID = partialUpdate.NextStepID
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		return &updated

	case *OrchestratorPlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = partialUpdate.ContextOutput
		}
		// BACKWARDS COMPATIBILITY: if legacy todo_task_step map is present, extract fields from it
		if partialUpdate.OrchestratorStep != nil {
			if desc, ok := partialUpdate.OrchestratorStep["description"].(string); ok && desc != "" {
				updated.Description = desc
			}
			if title, ok := partialUpdate.OrchestratorStep["title"].(string); ok && title != "" {
				updated.Title = title
			}
			if contextDeps, ok := partialUpdate.OrchestratorStep["context_dependencies"].([]interface{}); ok {
				updated.ContextDependencies = make([]string, 0, len(contextDeps))
				for _, dep := range contextDeps {
					if depStr, ok := dep.(string); ok {
						updated.ContextDependencies = append(updated.ContextDependencies, depStr)
					}
				}
			}
			if contextOutput, ok := partialUpdate.OrchestratorStep["context_output"]; ok {
				if contextOutputMap, ok := contextOutput.(map[string]interface{}); ok {
					contextOutputJSON, _ := json.Marshal(contextOutputMap)
					json.Unmarshal(contextOutputJSON, &updated.ContextOutput)
				} else if contextOutputStr, ok := contextOutput.(string); ok {
					updated.ContextOutput = FlexibleContextOutput(contextOutputStr)
				}
			}
			if validationSchema, ok := partialUpdate.OrchestratorStep["validation_schema"]; ok {
				if validationSchemaMap, ok := validationSchema.(map[string]interface{}); ok {
					validationSchemaJSON, _ := json.Marshal(validationSchemaMap)
					var vs ValidationSchema
					if json.Unmarshal(validationSchemaJSON, &vs) == nil {
						updated.ValidationSchema = &vs
					}
				}
			}
		}
		if partialUpdate.PredefinedRoutes != nil {
			updated.PredefinedRoutes = make([]PlanOrchestrationRoute, len(partialUpdate.PredefinedRoutes))
			for i, route := range partialUpdate.PredefinedRoutes {
				updated.PredefinedRoutes[i] = route
			}
		}
		if partialUpdate.Messages != nil {
			updated.Messages = partialUpdate.Messages
		}
		if partialUpdate.NextStepID != "" {
			updated.NextStepID = partialUpdate.NextStepID
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		return &updated

	case *RoutingPlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.ClearDescription {
			updated.Description = ""
		} else if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		if partialUpdate.RoutingQuestion != "" {
			updated.RoutingQuestion = partialUpdate.RoutingQuestion
		}
		if len(partialUpdate.Routes) > 0 {
			updated.Routes = partialUpdate.Routes
		}
		if partialUpdate.DefaultRouteID != "" {
			updated.DefaultRouteID = partialUpdate.DefaultRouteID
		}
		if partialUpdate.RouteSourceFile != "" {
			updated.RouteSourceFile = partialUpdate.RouteSourceFile
		}
		return &updated

	case *BranchPlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.ClearDescription {
			updated.Description = ""
		} else if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		if partialUpdate.BranchQuestion != "" {
			updated.BranchQuestion = partialUpdate.BranchQuestion
		}
		if len(partialUpdate.Routes) > 0 {
			updated.Routes = partialUpdate.Routes
		}
		if partialUpdate.DefaultRouteID != "" {
			updated.DefaultRouteID = partialUpdate.DefaultRouteID
		}
		if partialUpdate.RouteSourceFile != "" {
			updated.RouteSourceFile = partialUpdate.RouteSourceFile
		}
		if partialUpdate.RouteSource != "" {
			updated.RouteSource = partialUpdate.RouteSource
		}
		if partialUpdate.ValuePath != "" {
			updated.ValuePath = partialUpdate.ValuePath
		}
		if partialUpdate.ValueCases != nil {
			updated.ValueCases = partialUpdate.ValueCases
		}
		return &updated

	case *CrewPlanStep:
		updated := *step
		if partialUpdate.Title != "" {
			updated.Title = partialUpdate.Title
		}
		if partialUpdate.Description != "" {
			updated.Description = partialUpdate.Description
		}
		if partialUpdate.ContextDependencies != nil {
			updated.ContextDependencies = partialUpdate.ContextDependencies
		}
		if partialUpdate.ContextOutput != "" {
			updated.ContextOutput = FlexibleContextOutput(partialUpdate.ContextOutput)
		}
		if partialUpdate.CrewProfileID != "" {
			updated.CrewProfileID = partialUpdate.CrewProfileID
		}
		if partialUpdate.CrewProjectID != "" {
			updated.CrewProjectID = partialUpdate.CrewProjectID
		}
		if partialUpdate.CrewTriggerID != "" {
			updated.TriggerID = partialUpdate.CrewTriggerID
		}
		if partialUpdate.CrewInstruction != "" {
			updated.Instruction = partialUpdate.CrewInstruction
		}
		if partialUpdate.CrewTimeoutSeconds != 0 {
			updated.TimeoutSeconds = partialUpdate.CrewTimeoutSeconds
		}
		if partialUpdate.NextStepID != "" {
			updated.NextStepID = partialUpdate.NextStepID
		}
		if partialUpdate.ValidationSchema != nil {
			updated.ValidationSchema = partialUpdate.ValidationSchema
		}
		return &updated

	default:
		// Unknown type - return original
		return existingStep
	}
}

// updateSingleStep is a helper function that updates a single step in the plan
// Returns the step index and field changes for changelog
// findStepByID recursively searches for a step with the given ID in the plan steps.
// It returns the step interface, its index at the current level, and the slice it belongs to.
func findStepByID(steps []PlanStepInterface, id string) (PlanStepInterface, int, []PlanStepInterface) {
	for i, step := range steps {
		if step.GetID() == id {
			return step, i, steps
		}

		// Search in nested structures
		switch s := step.(type) {
		case *OrchestratorPlanStep:
			for _, route := range s.PredefinedRoutes {
				if route.SubAgentStep != nil {
					if foundStep, idx, slice := findStepByID([]PlanStepInterface{route.SubAgentStep}, id); foundStep != nil {
						return foundStep, idx, slice
					}
				}
			}
		case *MessageSequencePlanStep:
			for _, route := range s.PredefinedRoutes {
				if route.SubAgentStep != nil {
					if foundStep, idx, slice := findStepByID([]PlanStepInterface{route.SubAgentStep}, id); foundStep != nil {
						return foundStep, idx, slice
					}
				}
			}
		}
	}
	return nil, -1, nil
}

// updateStepRecursively updates a step within the plan structure, even if nested.
func updateStepRecursively(steps []PlanStepInterface, partialUpdate PartialPlanStep, fieldChanges *[]PlanFieldChange) (bool, int) {
	for i, step := range steps {
		if step.GetID() == partialUpdate.ExistingStepID {
			// Found the step at this level
			steps[i] = mergePartialStepUpdate(step, partialUpdate)
			return true, i
		}

		// Recursively search and update in nested structures
		switch s := step.(type) {
		case *OrchestratorPlanStep:
			for j := range s.PredefinedRoutes {
				if s.PredefinedRoutes[j].SubAgentStep != nil {
					tmpSlice := []PlanStepInterface{s.PredefinedRoutes[j].SubAgentStep}
					if updated, _ := updateStepRecursively(tmpSlice, partialUpdate, fieldChanges); updated {
						s.PredefinedRoutes[j].SubAgentStep = tmpSlice[0]
						return true, i
					}
				}
			}
		case *MessageSequencePlanStep:
			for j := range s.PredefinedRoutes {
				if s.PredefinedRoutes[j].SubAgentStep != nil {
					tmpSlice := []PlanStepInterface{s.PredefinedRoutes[j].SubAgentStep}
					if updated, _ := updateStepRecursively(tmpSlice, partialUpdate, fieldChanges); updated {
						s.PredefinedRoutes[j].SubAgentStep = tmpSlice[0]
						return true, i
					}
				}
			}
		}
	}
	return false, -1
}

// replaceStepRecursively replaces a step while preserving its position in the
// plan, including inline todo-task routes. This is used by compatibility
// upgrades where a persisted legacy type must become its effective runtime type
// before the normal partial-update machinery can run.
func replaceStepRecursively(steps []PlanStepInterface, stepID string, replacement PlanStepInterface) (bool, int) {
	for i, step := range steps {
		if step.GetID() == stepID {
			steps[i] = replacement
			return true, i
		}

		var routes *[]PlanOrchestrationRoute
		switch agentStep := step.(type) {
		case *OrchestratorPlanStep:
			routes = &agentStep.PredefinedRoutes
		case *MessageSequencePlanStep:
			routes = &agentStep.PredefinedRoutes
		}
		if routes != nil {
			for routeIndex := range *routes {
				routeStep := (*routes)[routeIndex].SubAgentStep
				if routeStep == nil {
					continue
				}
				tmpSlice := []PlanStepInterface{routeStep}
				if replaced, _ := replaceStepRecursively(tmpSlice, stepID, replacement); replaced {
					(*routes)[routeIndex].SubAgentStep = tmpSlice[0]
					return true, i
				}
			}
		}
	}
	return false, -1
}

func updateSingleStep(plan *PlanningResponse, partialUpdate PartialPlanStep, fieldChanges *[]PlanFieldChange) (int, []string, error) {
	// Find the step to update (for field tracking)
	existingStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID)

	if existingStep == nil {
		// Try orphan steps
		existingStep, _, _ = findStepByID(plan.OrphanSteps, partialUpdate.ExistingStepID)
	}

	if existingStep == nil {
		availableIDs := make([]string, 0, len(plan.Steps)+len(plan.OrphanSteps))
		for _, step := range plan.Steps {
			availableIDs = append(availableIDs, step.GetID())
		}
		for _, step := range plan.OrphanSteps {
			availableIDs = append(availableIDs, step.GetID())
		}
		return -1, nil, fmt.Errorf("step ID '%s' not found in existing plan. Available top-level step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
	}

	changedFields := []string{}

	// Track each field change with old and new values using interface methods
	if partialUpdate.Title != "" {
		changedFields = append(changedFields, "title")
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "title",
			OldValue: existingStep.GetTitle(),
			NewValue: partialUpdate.Title,
		})
	}
	if partialUpdate.Description != "" {
		changedFields = append(changedFields, "description")
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "description",
			OldValue: existingStep.GetDescription(),
			NewValue: partialUpdate.Description,
		})
	}
	if partialUpdate.ClearDescription {
		changedFields = append(changedFields, "description")
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "description",
			OldValue: existingStep.GetDescription(),
			NewValue: "",
		})
	}
	if partialUpdate.ContextDependencies != nil {
		changedFields = append(changedFields, "context_dependencies")
		oldDeps := existingStep.GetContextDependencies()
		// Store as JSON for proper revert
		oldDepsJSON, _ := json.Marshal(oldDeps)
		newDepsJSON, _ := json.Marshal(partialUpdate.ContextDependencies)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "context_dependencies",
			OldValue: string(oldDepsJSON),
			NewValue: string(newDepsJSON),
		})
	}
	if partialUpdate.ContextOutput != "" {
		changedFields = append(changedFields, "context_output")
		oldOutput := existingStep.GetContextOutput()
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "context_output",
			OldValue: oldOutput.String(),
			NewValue: partialUpdate.ContextOutput,
		})
	}
	if partialUpdate.ScriptParameters != nil {
		changedFields = append(changedFields, "script_parameters")
		oldParameters := existingStep.GetCommonFields().ScriptParameters
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "script_parameters",
			OldValue: oldParameters,
			NewValue: partialUpdate.ScriptParameters,
		})
	}
	if partialUpdate.ScriptParametersSchema != nil {
		changedFields = append(changedFields, "script_parameters_schema")
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "script_parameters_schema",
			OldValue: existingStep.GetCommonFields().ScriptParametersSchema,
			NewValue: partialUpdate.ScriptParametersSchema,
		})
	}
	if partialUpdate.ScriptOnly != nil {
		changedFields = append(changedFields, "script_only")
		old := false
		if regular, ok := existingStep.(*RegularPlanStep); ok {
			old = regular.ScriptOnly
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "script_only", OldValue: old, NewValue: *partialUpdate.ScriptOnly})
	}
	if partialUpdate.Items != nil {
		changedFields = append(changedFields, "items")
		oldItemsJSON := "[]"
		if sequenceStep, ok := existingStep.(*MessageSequencePlanStep); ok {
			oldBytes, _ := json.Marshal(sequenceStep.Items)
			oldItemsJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.Items)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "items",
			OldValue: oldItemsJSON,
			NewValue: string(newBytes),
		})
	}
	if sequenceStep, ok := existingStep.(*MessageSequencePlanStep); ok {
		if partialUpdate.AuthoredPrompt != nil {
			changedFields = append(changedFields, "authored_prompt")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "authored_prompt", OldValue: sequenceStep.AuthoredPrompt, NewValue: *partialUpdate.AuthoredPrompt})
		}
		if partialUpdate.SystemPrompt != nil {
			changedFields = append(changedFields, "system_prompt")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "system_prompt", OldValue: sequenceStep.SystemPrompt, NewValue: *partialUpdate.SystemPrompt})
		}
	}
	// Loop fields ignored (feature removed)
	// Legacy todo_task_step field — extract fields and track them as top-level changes
	if partialUpdate.OrchestratorStep != nil {
		if desc, ok := partialUpdate.OrchestratorStep["description"].(string); ok && desc != "" {
			changedFields = append(changedFields, "description (via todo_task_step)")
			if orchestratorStep, ok := existingStep.(*OrchestratorPlanStep); ok {
				*fieldChanges = append(*fieldChanges, PlanFieldChange{
					StepID:   partialUpdate.ExistingStepID,
					Field:    "description",
					OldValue: orchestratorStep.Description,
					NewValue: desc,
				})
			}
		}
	}
	if partialUpdate.PredefinedRoutes != nil {
		changedFields = append(changedFields, "predefined_routes")
		// Get old routes
		var oldRoutes []PlanOrchestrationRoute
		if orchestratorStep, ok := existingStep.(*OrchestratorPlanStep); ok {
			oldRoutes = orchestratorStep.PredefinedRoutes
		} else if sequenceStep, ok := existingStep.(*MessageSequencePlanStep); ok {
			oldRoutes = sequenceStep.PredefinedRoutes
		}
		// Compare routes in detail
		if !equalOrchestrationRoutes(oldRoutes, partialUpdate.PredefinedRoutes) {
			// Track detailed changes for each route
			maxLen := len(oldRoutes)
			if len(partialUpdate.PredefinedRoutes) > maxLen {
				maxLen = len(partialUpdate.PredefinedRoutes)
			}
			for i := 0; i < maxLen; i++ {
				routePrefix := fmt.Sprintf("predefined_routes[%d]", i)
				if i >= len(oldRoutes) {
					// New route added
					newRouteJSON, _ := json.Marshal(partialUpdate.PredefinedRoutes[i])
					*fieldChanges = append(*fieldChanges, PlanFieldChange{
						StepID:   partialUpdate.ExistingStepID,
						Field:    routePrefix,
						OldValue: nil,
						NewValue: string(newRouteJSON),
					})
				} else if i >= len(partialUpdate.PredefinedRoutes) {
					// Route removed
					oldRouteJSON, _ := json.Marshal(oldRoutes[i])
					*fieldChanges = append(*fieldChanges, PlanFieldChange{
						StepID:   partialUpdate.ExistingStepID,
						Field:    routePrefix,
						OldValue: string(oldRouteJSON),
						NewValue: nil,
					})
				} else {
					// Route modified - compare fields
					oldRoute := oldRoutes[i]
					newRoute := partialUpdate.PredefinedRoutes[i]
					if oldRoute.RouteID != newRoute.RouteID {
						*fieldChanges = append(*fieldChanges, PlanFieldChange{
							StepID:   partialUpdate.ExistingStepID,
							Field:    routePrefix + ".route_id",
							OldValue: oldRoute.RouteID,
							NewValue: newRoute.RouteID,
						})
					}
					if oldRoute.RouteName != newRoute.RouteName {
						*fieldChanges = append(*fieldChanges, PlanFieldChange{
							StepID:   partialUpdate.ExistingStepID,
							Field:    routePrefix + ".route_name",
							OldValue: oldRoute.RouteName,
							NewValue: newRoute.RouteName,
						})
					}
					if oldRoute.Condition != newRoute.Condition {
						*fieldChanges = append(*fieldChanges, PlanFieldChange{
							StepID:   partialUpdate.ExistingStepID,
							Field:    routePrefix + ".condition",
							OldValue: oldRoute.Condition,
							NewValue: newRoute.Condition,
						})
					}
					// Compare nested sub-agent step
					if oldRoute.SubAgentStep != nil && newRoute.SubAgentStep != nil {
						compareNestedStepFields(oldRoute.SubAgentStep, newRoute.SubAgentStep, partialUpdate.ExistingStepID, routePrefix+".sub_agent_step", fieldChanges)
					} else if oldRoute.SubAgentStep != nil || newRoute.SubAgentStep != nil {
						oldID := "nil"
						if oldRoute.SubAgentStep != nil {
							oldID = oldRoute.SubAgentStep.GetID()
						}
						newID := "nil"
						if newRoute.SubAgentStep != nil {
							newID = newRoute.SubAgentStep.GetID()
						}
						*fieldChanges = append(*fieldChanges, PlanFieldChange{
							StepID:   partialUpdate.ExistingStepID,
							Field:    routePrefix + ".sub_agent_step",
							OldValue: oldID,
							NewValue: newID,
						})
					}
				}
			}
		}
	}
	if partialUpdate.CrewProfileID != "" {
		changedFields = append(changedFields, "crew_profile_id")
		oldValue := ""
		if crewStep, ok := existingStep.(*CrewPlanStep); ok {
			oldValue = crewStep.CrewProfileID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "crew_profile_id",
			OldValue: oldValue,
			NewValue: partialUpdate.CrewProfileID,
		})
	}
	if partialUpdate.CrewProjectID != "" {
		changedFields = append(changedFields, "crew_project_id")
		oldValue := ""
		if crewStep, ok := existingStep.(*CrewPlanStep); ok {
			oldValue = crewStep.CrewProjectID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "crew_project_id",
			OldValue: oldValue,
			NewValue: partialUpdate.CrewProjectID,
		})
	}
	if partialUpdate.CrewTriggerID != "" {
		changedFields = append(changedFields, "trigger_id")
		oldValue := ""
		if crewStep, ok := existingStep.(*CrewPlanStep); ok {
			oldValue = crewStep.TriggerID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "trigger_id",
			OldValue: oldValue,
			NewValue: partialUpdate.CrewTriggerID,
		})
	}
	if partialUpdate.CrewInstruction != "" {
		changedFields = append(changedFields, "instruction")
		oldValue := ""
		if crewStep, ok := existingStep.(*CrewPlanStep); ok {
			oldValue = crewStep.Instruction
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "instruction",
			OldValue: oldValue,
			NewValue: partialUpdate.CrewInstruction,
		})
	}
	if partialUpdate.CrewTimeoutSeconds != 0 {
		changedFields = append(changedFields, "timeout_seconds")
		oldValue := 0
		if crewStep, ok := existingStep.(*CrewPlanStep); ok {
			oldValue = crewStep.TimeoutSeconds
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "timeout_seconds",
			OldValue: oldValue,
			NewValue: partialUpdate.CrewTimeoutSeconds,
		})
	}
	if partialUpdate.NextStepID != "" {
		changedFields = append(changedFields, "next_step_id")
		oldNextStepID := ""
		switch step := existingStep.(type) {
		case *RegularPlanStep:
			oldNextStepID = step.NextStepID
		case *OrchestratorPlanStep:
			oldNextStepID = step.NextStepID
		case *HumanInputPlanStep:
			oldNextStepID = step.NextStepID
		case *MessageSequencePlanStep:
			oldNextStepID = step.NextStepID
		case *CrewPlanStep:
			oldNextStepID = step.NextStepID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "next_step_id",
			OldValue: oldNextStepID,
			NewValue: partialUpdate.NextStepID,
		})
	}
	if partialUpdate.Messages != nil {
		changedFields = append(changedFields, "messages")
		oldMessagesJSON := "[]"
		if orchestratorStep, ok := existingStep.(*OrchestratorPlanStep); ok {
			oldBytes, _ := json.Marshal(orchestratorStep.Messages)
			oldMessagesJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.Messages)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "messages",
			OldValue: oldMessagesJSON,
			NewValue: string(newBytes),
		})
	}
	if partialUpdate.Question != "" {
		changedFields = append(changedFields, "question")
		oldQuestion := ""
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldQuestion = humanInputStep.Question
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "question",
			OldValue: oldQuestion,
			NewValue: partialUpdate.Question,
		})
	}
	if partialUpdate.VariableName != "" {
		changedFields = append(changedFields, "variable_name")
		oldVariableName := ""
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldVariableName = humanInputStep.VariableName
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "variable_name",
			OldValue: oldVariableName,
			NewValue: partialUpdate.VariableName,
		})
	}
	if partialUpdate.ResponseType != "" {
		changedFields = append(changedFields, "response_type")
		oldResponseType := ""
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldResponseType = humanInputStep.ResponseType
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "response_type",
			OldValue: oldResponseType,
			NewValue: partialUpdate.ResponseType,
		})
	}
	if partialUpdate.Options != nil {
		changedFields = append(changedFields, "options")
		oldOptionsJSON := "[]"
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldBytes, _ := json.Marshal(humanInputStep.Options)
			oldOptionsJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.Options)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "options",
			OldValue: oldOptionsJSON,
			NewValue: string(newBytes),
		})
	}
	if partialUpdate.IfYesNextStepID != "" {
		changedFields = append(changedFields, "if_yes_next_step_id")
		oldNextStepID := ""
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldNextStepID = humanInputStep.IfYesNextStepID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "if_yes_next_step_id",
			OldValue: oldNextStepID,
			NewValue: partialUpdate.IfYesNextStepID,
		})
	}
	if partialUpdate.IfNoNextStepID != "" {
		changedFields = append(changedFields, "if_no_next_step_id")
		oldNextStepID := ""
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldNextStepID = humanInputStep.IfNoNextStepID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "if_no_next_step_id",
			OldValue: oldNextStepID,
			NewValue: partialUpdate.IfNoNextStepID,
		})
	}
	if partialUpdate.OptionRoutes != nil {
		changedFields = append(changedFields, "option_routes")
		oldRoutesJSON := "{}"
		if humanInputStep, ok := existingStep.(*HumanInputPlanStep); ok {
			oldBytes, _ := json.Marshal(humanInputStep.OptionRoutes)
			oldRoutesJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.OptionRoutes)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "option_routes",
			OldValue: oldRoutesJSON,
			NewValue: string(newBytes),
		})
	}
	if partialUpdate.RoutingQuestion != "" {
		changedFields = append(changedFields, "routing_question")
		oldQuestion := ""
		if routingStep, ok := existingStep.(*RoutingPlanStep); ok {
			oldQuestion = routingStep.RoutingQuestion
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "routing_question",
			OldValue: oldQuestion,
			NewValue: partialUpdate.RoutingQuestion,
		})
	}
	if partialUpdate.BranchQuestion != "" {
		changedFields = append(changedFields, "branch_question")
		oldQuestion := ""
		if branchStep, ok := existingStep.(*BranchPlanStep); ok {
			oldQuestion = branchStep.BranchQuestion
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "branch_question",
			OldValue: oldQuestion,
			NewValue: partialUpdate.BranchQuestion,
		})
	}
	if partialUpdate.ValuePath != "" {
		changedFields = append(changedFields, "value_path")
		old := ""
		if branch, ok := existingStep.(*BranchPlanStep); ok {
			old = branch.ValuePath
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "value_path", OldValue: old, NewValue: partialUpdate.ValuePath})
	}
	if partialUpdate.ValueCases != nil {
		changedFields = append(changedFields, "value_cases")
		var old map[string]string
		if branch, ok := existingStep.(*BranchPlanStep); ok {
			old = branch.ValueCases
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "value_cases", OldValue: old, NewValue: partialUpdate.ValueCases})
	}
	if partialUpdate.Routes != nil {
		changedFields = append(changedFields, "routes")
		oldRoutesJSON := "[]"
		if routingStep, ok := existingStep.(routeSwitchStep); ok {
			oldBytes, _ := json.Marshal(routingStep.GetRoutes())
			oldRoutesJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.Routes)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "routes",
			OldValue: oldRoutesJSON,
			NewValue: string(newBytes),
		})
	}
	if partialUpdate.DefaultRouteID != "" {
		changedFields = append(changedFields, "default_route_id")
		oldDefaultRouteID := ""
		if routingStep, ok := existingStep.(routeSwitchStep); ok {
			oldDefaultRouteID = routingStep.GetDefaultRouteID()
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "default_route_id",
			OldValue: oldDefaultRouteID,
			NewValue: partialUpdate.DefaultRouteID,
		})
	}
	if partialUpdate.RouteSourceFile != "" {
		changedFields = append(changedFields, "route_source_file")
		oldRouteSourceFile := ""
		if routingStep, ok := existingStep.(routeSwitchStep); ok {
			oldRouteSourceFile = routingStep.GetRouteSourceFile()
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "route_source_file",
			OldValue: oldRouteSourceFile,
			NewValue: partialUpdate.RouteSourceFile,
		})
	}
	if partialUpdate.RouteSource != "" {
		changedFields = append(changedFields, "route_source")
		oldRouteSource := ""
		if routingStep, ok := existingStep.(routeSwitchStep); ok {
			oldRouteSource = routingStep.GetRouteSource()
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "route_source",
			OldValue: oldRouteSource,
			NewValue: partialUpdate.RouteSource,
		})
	}
	if partialUpdate.ValidationSchema != nil {
		changedFields = append(changedFields, "validation_schema")
		oldSchema := existingStep.GetValidationSchema()
		oldSchemaJSON := "nil"
		if oldSchema != nil {
			oldSchemaBytes, _ := json.Marshal(oldSchema)
			oldSchemaJSON = string(oldSchemaBytes)
		}
		newSchemaJSON := "nil"
		if partialUpdate.ValidationSchema != nil {
			newSchemaBytes, _ := json.Marshal(partialUpdate.ValidationSchema)
			newSchemaJSON = string(newSchemaBytes)
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "validation_schema",
			OldValue: oldSchemaJSON,
			NewValue: newSchemaJSON,
		})
	}

	// Validate regex patterns in validation schema before merging
	if partialUpdate.ValidationSchema != nil {
		if err := validateRegexPatternsInSchema(partialUpdate.ValidationSchema); err != nil {
			return -1, nil, fmt.Errorf("invalid regex patterns in validation schema: %w", err)
		}

		// Validate JSONPath syntax
		if err := validateJSONPathSyntax(partialUpdate.ValidationSchema); err != nil {
			return -1, nil, fmt.Errorf("invalid JSONPath syntax in validation schema: %w", err)
		}

		// Validate array_length consistency checks
		if err := validateArrayLengthConsistencyChecks(partialUpdate.ValidationSchema); err != nil {
			return -1, nil, fmt.Errorf("invalid array_length consistency checks in validation schema: %w", err)
		}

		// Validate value_type/pattern compatibility
		if err := validateValueTypePatternCompatibility(partialUpdate.ValidationSchema); err != nil {
			return -1, nil, fmt.Errorf("invalid validation schema: %w", err)
		}
	}

	// Apply updates recursively
	updated, topLevelIndex := updateStepRecursively(plan.Steps, partialUpdate, fieldChanges)
	if !updated {
		// Orphan definitions are editable too. Routes that reference them are
		// materialized only for runtime and continue to serialize by reference.
		updated, topLevelIndex = updateStepRecursively(plan.OrphanSteps, partialUpdate, fieldChanges)
	}
	if !updated {
		// This shouldn't happen because we already checked for existence using findStepByID.
		return -1, nil, fmt.Errorf("failed to apply update to step '%s'", partialUpdate.ExistingStepID)
	}

	return topLevelIndex, changedFields, nil
}

func planStepUpdateRequiresDependentArtifactReview(fieldChanges []PlanFieldChange) bool {
	return len(fieldChanges) > 0
}

// planDriftMaterialFieldNames are step fields whose change can alter what a
// step produces or how the plan routes, so a dependent step, an eval, a
// report query, a DB contract, or downstream learnings/KB may now be stale.
// It is the same set planStepUpdateInvalidatesDescriptionReview uses, minus
// "description" and "title": wording edits are the single largest share of
// plan-mod calls (2026-09-26 review: description-only edits on regular and
// message-sequence steps were the biggest bucket of the unreviewed changelog
// backlog across live workflows), and treating every one as drift-worthy made
// plan_drift_review due on nearly every plan edit. A wording rewrite
// occasionally hides a real behavior change, but that then shows up as an
// output or data change on the step's next run, which Pulse already catches
// through its own step-output and step-concern review, not through Plan
// Drift. Schedule timing, review_notes, updated_at, and model/tier settings
// (Architecture's, not Plan Drift's, per pulse-gate.md) were never in this
// set either.
var planDriftMaterialFieldNames = map[string]bool{
	"type": true, "context_dependencies": true, "context_output": true,
	"items": true, "messages": true, "next_step_id": true,
	"validation_schema": true, "success_criteria": true, "script_parameters": true,
	"routing_question": true, "branch_question": true, "routes": true,
	"default_route_id": true, "route_source_file": true,
	"question": true, "variable_name": true, "response_type": true,
	"options": true, "if_yes_next_step_id": true, "if_no_next_step_id": true,
	"option_routes": true,
}

// planDriftFieldNameIsMaterial checks one field name, following the last
// path segment for a dotted name (a manifest-level changelog entry such as
// "workflow.json.schedules" or "workflow.json.capabilities.llm_config.
// builder_llm" records its field this way; a step-level PlanFieldChange
// never does).
func planDriftFieldNameIsMaterial(field string) bool {
	field = strings.TrimSpace(field)
	if field == "" {
		return false
	}
	name := field
	if idx := strings.LastIndex(field, "."); idx >= 0 {
		name = field[idx+1:]
	}
	if strings.HasPrefix(name, "predefined_routes") || strings.Contains(field, "sub_agent_step") {
		return true
	}
	return planDriftMaterialFieldNames[name]
}

// planStepFieldChangesRequireDriftReview decides whether an edit makes
// plan_drift_review due for the step: true only when at least one changed
// field can affect a dependent. An empty list (nothing to check) is not
// flagged; the caller already gates on len(fieldChanges) > 0 before this is
// reached.
func planStepFieldChangesRequireDriftReview(fieldChanges []PlanFieldChange) bool {
	for _, change := range fieldChanges {
		if planDriftFieldNameIsMaterial(change.Field) {
			return true
		}
	}
	return false
}

// planChangeFieldsAreDriftMaterial is the changelog-backlog counterpart: an
// entry with NO recorded field names (an untyped update_step_config call, or
// a step add/delete, which changes the plan's shape without a field-level
// diff) stays conservative -- there is nothing proving it is safe to skip.
// Only an entry whose every named field is known and none of them material
// is dropped from the backlog.
func planChangeFieldsAreDriftMaterial(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if planDriftFieldNameIsMaterial(f) {
			return true
		}
	}
	return false
}

func planStepUpdateInvalidatesDescriptionReview(fieldChanges []PlanFieldChange) bool {
	for _, change := range fieldChanges {
		field := change.Field
		if field == "type" ||
			field == "description" ||
			field == "context_dependencies" ||
			field == "context_output" ||
			field == "items" ||
			field == "messages" ||
			field == "next_step_id" ||
			field == "validation_schema" ||
			field == "script_parameters" ||
			field == "routing_question" ||
			field == "branch_question" ||
			field == "routes" ||
			field == "default_route_id" ||
			field == "route_source_file" ||
			field == "question" ||
			field == "variable_name" ||
			field == "response_type" ||
			field == "options" ||
			field == "if_yes_next_step_id" ||
			field == "if_no_next_step_id" ||
			field == "option_routes" ||
			strings.HasPrefix(field, "predefined_routes") ||
			strings.Contains(field, ".sub_agent_step") {
			return true
		}
	}
	return false
}

func clearDescriptionReviewedAfterPlanUpdate(ctx context.Context, workspacePath, stepID string, fieldChanges []PlanFieldChange, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (bool, error) {
	if !planStepUpdateInvalidatesDescriptionReview(fieldChanges) {
		return false, nil
	}

	configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
	if err != nil {
		return false, err
	}
	for i := range configs {
		if configs[i].ID != stepID {
			continue
		}
		if configs[i].AgentConfigs == nil || configs[i].AgentConfigs.DescriptionReviewed == nil {
			return false, nil
		}
		clearStepConfigField(&configs[i], "description_reviewed")
		if err := writeStepConfigViaFileCallback(ctx, workspacePath, configs, writeFile); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// clearDriftReviewAfterPlanUpdate flags an existing drift review stale rather
// than destroying it — the "stale flag" model. Whether it fires is now
// planStepFieldChangesRequireDriftReview's call (planDriftMaterialFieldNames):
// until 2026-09-26 this fired on ANY changed field, title included, which
// made plan_drift_review due on nearly every plan edit — description-only
// edits were the single largest share of live workflows' unreviewed
// changelog backlog. A step's last real review can be trusted through a
// wording-only edit; a change to what it outputs, depends on, validates, or
// routes to cannot.
//
// A step with NeedsReview==true (or no record at all) is what makes the
// plan_drift_review Pulse module due — this is the mechanism that turns "we
// edited a step" into "Pulse will now notice," without discarding the
// step's last real review: Checks/ReviewedAt/ReviewedBy/
// ReviewedThroughChangeID stay exactly as they were until a completed
// plan_drift_review turn replaces them. The plan-mod tool's own call already
// appends the changelog entry for this field change elsewhere in the same
// turn — no separate changelog entry is needed here.
func clearDriftReviewAfterPlanUpdate(ctx context.Context, workspacePath, stepID string, fieldChanges []PlanFieldChange, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (bool, error) {
	if !planStepFieldChangesRequireDriftReview(fieldChanges) {
		return false, nil
	}

	configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
	if err != nil {
		return false, err
	}
	for i := range configs {
		if configs[i].ID != stepID {
			continue
		}
		if configs[i].AgentConfigs == nil || configs[i].AgentConfigs.DriftReview == nil {
			// No prior review to flag — a step with no drift_review record at
			// all is already a plan_drift_review candidate on its own.
			return false, nil
		}
		if configs[i].AgentConfigs.DriftReview.NeedsReview {
			// Already flagged by an earlier edit in this same unreviewed window;
			// nothing changed about due-ness, so this is not a new "cleared" event.
			return false, nil
		}
		configs[i].AgentConfigs.DriftReview.NeedsReview = true
		if err := writeStepConfigViaFileCallback(ctx, workspacePath, configs, writeFile); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// clearDriftReviewAfterPlanUpdateRetried retries the flag write once before
// giving up. The plan-mod tool's own field change has already landed by the
// time this runs (it is a separate, earlier write against plan.json), so
// true atomicity — rolling that back if this second write fails — is not
// achievable without a transactional multi-file write mechanism this
// codebase has nowhere else. A transient I/O error retried once, plus a
// persistent failure surfaced loudly to the calling agent (not just logged)
// instead of silently swallowed, is the pragmatic mitigation: it meaningfully
// shrinks the window where a step changes but plan_drift_review never
// notices, without pretending to solve the general problem.
func clearDriftReviewAfterPlanUpdateRetried(ctx context.Context, workspacePath, stepID string, fieldChanges []PlanFieldChange, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, logger loggerv2.Logger) (cleared bool, persistentErr error) {
	cleared, err := clearDriftReviewAfterPlanUpdate(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile)
	if err == nil {
		return cleared, nil
	}
	logger.Warn(fmt.Sprintf("⚠️ Failed to flag drift_review.needs_review after updating step %s, retrying once: %v", stepID, err))
	cleared, err = clearDriftReviewAfterPlanUpdate(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile)
	return cleared, err
}

// Editing records stale review state for Pulse; it does not launch an audit.
// Keep interactive checks scoped to the combined change, including when several
// typed tools are called to implement one repair.
const planEditImpactGuidance = "After related edits are complete, do one combined compatibility check in the current agent before the targeted test. For small internal edits with unchanged outputs, DB writes, routing, and behavior, confirm compatibility briefly and test. For output, DB, routing, or behavior changes, inspect and reconcile only affected dependencies before testing. Stop once affected consumers are compatible; unrelated backlog and missing audit receipts must not delay the test. Read builder-reference/references/plan-change-impact.md for the procedure. Do not launch a separate drift reviewer for each edit or test retry; reserve the full review-artifact-drift audit for scheduled Pulse or an explicit user request."

func buildPlanStepDependentArtifactReviewNotice(stepID string, fieldChanges []PlanFieldChange, descriptionReviewCleared, driftReviewCleared, driftReviewFlagFailed bool) string {
	if !planStepUpdateRequiresDependentArtifactReview(fieldChanges) {
		return ""
	}

	changed := make([]string, 0, len(fieldChanges))
	seen := make(map[string]bool, len(fieldChanges))
	for _, change := range fieldChanges {
		if change.Field == "" || seen[change.Field] {
			continue
		}
		seen[change.Field] = true
		changed = append(changed, change.Field)
	}

	var b strings.Builder
	b.WriteString("\n\nPlan change recorded")
	if len(changed) > 0 {
		b.WriteString(" for changed fields: ")
		b.WriteString(strings.Join(changed, ", "))
	}
	b.WriteString(".\n")
	if descriptionReviewCleared {
		b.WriteString("- Cleared step_config.agent_configs.description_reviewed because the reviewed step contract may now be stale.\n")
	}
	if driftReviewCleared {
		b.WriteString("- Flagged step_config.agent_configs.drift_review.needs_review=true — the plan_drift_review Pulse module will now treat this step as due until it (or the manual review-artifact-drift slash command) records fresh evidence via record_plan_drift_review.\n")
	}
	if driftReviewFlagFailed {
		b.WriteString("- ⚠️ FAILED to flag drift_review.needs_review after two attempts — plan_drift_review may NOT notice this step changed until it happens to be re-edited later. Report this failure explicitly in your final response rather than treating the edit as fully clean; do not attempt to write drift_review yourself (it is not an update_step_config field — only record_plan_drift_review may set it).\n")
	}
	b.WriteString("- " + planEditImpactGuidance)
	return b.String()
}

func handlePlanStepDependentArtifactReview(ctx context.Context, workspacePath, stepID string, fieldChanges []PlanFieldChange, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, logger loggerv2.Logger) string {
	if !planStepUpdateRequiresDependentArtifactReview(fieldChanges) {
		return ""
	}

	descriptionReviewCleared, err := clearDescriptionReviewedAfterPlanUpdate(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile)
	if err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to clear description_reviewed after updating step %s: %v", stepID, err))
	}
	driftReviewCleared, err := clearDriftReviewAfterPlanUpdateRetried(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile, logger)
	driftReviewFlagFailed := false
	if err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to flag drift_review.needs_review after updating step %s (both attempts failed): %v", stepID, err))
		driftReviewFlagFailed = true
	}
	return buildPlanStepDependentArtifactReviewNotice(stepID, fieldChanges, descriptionReviewCleared, driftReviewCleared, driftReviewFlagFailed)
}

func buildAddedStepArtifactSetupNotice(stepID, stepType string) string {
	var b strings.Builder
	b.WriteString("\n\nNew step artifact setup required.\n")
	b.WriteString("- Step config: decide execution_llm/tier, servers/tools, browser mode, skills, secrets, and description_reviewed in planning/step_config.json.\n")
	b.WriteString("- Pre-validation: add or confirm validation_schema for required inputs/outputs and generated files.\n")
	b.WriteString("- Learnings: decide learnings_access, learning_objective, and whether this step should write reusable execution know-how.\n")
	b.WriteString("- DB: decide whether the step reads/writes db/ files; update db/README.md and schemas/merge rules if it does.\n")
	b.WriteString("- KB: decide knowledgebase_access and knowledgebase_contribution for business context and notes.\n")
	b.WriteString("- Scripted code: if " + stepType + " step " + stepID + " should run code, create or review learnings/" + stepID + "/main.py and set code execution config; otherwise make sure no stale script is implied.\n")
	b.WriteString("- Downstream wiring: connect routes/next_step_id/context_dependencies, and update db/reports/index.html or the measurement step if this step affects outputs.\n")
	b.WriteString("- Description & schema quality: before finalizing this step's description and validation_schema, call read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/step-description.md\"}]) and apply it.\n")
	b.WriteString("- " + planEditImpactGuidance)
	return b.String()
}

func buildDeletedStepArtifactCleanupNotice(deletedIDs []string, prunedConfigIDs []string, driftReviewFlagFailed bool) string {
	var b strings.Builder
	b.WriteString("\n\nDeleted step artifact cleanup required")
	if len(deletedIDs) > 0 {
		b.WriteString(" for: ")
		b.WriteString(strings.Join(deletedIDs, ", "))
	}
	b.WriteString(".\n")
	if len(prunedConfigIDs) > 0 {
		b.WriteString("- Removed matching planning/step_config.json entries: " + strings.Join(prunedConfigIDs, ", ") + ".\n")
	} else {
		b.WriteString("- Step config: confirm no stale planning/step_config.json entries remain for the deleted IDs.\n")
	}
	if driftReviewFlagFailed {
		b.WriteString("- ⚠️ FAILED to flag the workflow-level drift review record (" + WorkflowDriftReviewStepID + ") after two attempts — plan_drift_review has no other way to learn this deletion happened, since the deleted step's own drift_review record is already gone. Report this failure explicitly in your final response; do not treat the deletion's dependent-artifact fallout as tracked. Record a durable finding for this tracking failure and inspect the deleted steps' affected dependencies in the current agent before testing.\n")
	}
	b.WriteString("- Plan wiring: remove or reroute next_step_id, routes, predefined_routes, and downstream context_dependencies that referenced deleted steps.\n")
	b.WriteString("- Pre-validation: remove validation schemas that validate deleted-step outputs and update schemas that depended on them.\n")
	b.WriteString("- Learnings/code: remove or archive stale learnings/<step-id>/ content and main.py scripts, unless intentionally kept as reusable docs.\n")
	b.WriteString("- DB/KB: remove stale db writers/readers, db/README.md mentions, knowledgebase references, and contribution rules tied to deleted steps.\n")
	b.WriteString("- Reports/metrics/docs: update db/reports/index.html, measurement queries, and docs that referenced deleted outputs.\n")
	b.WriteString("- " + planEditImpactGuidance)
	return b.String()
}

func buildOrchestratorRouteArtifactReviewNotice(parentStepID, routeID, action string, descriptionReviewCleared, driftReviewCleared, driftReviewFlagFailed bool) string {
	var b strings.Builder
	b.WriteString("\n\nTodo route artifact review required for ")
	b.WriteString(action)
	b.WriteString(" route ")
	b.WriteString(routeID)
	b.WriteString(" under ")
	b.WriteString(parentStepID)
	b.WriteString(".\n")
	if descriptionReviewCleared {
		b.WriteString("- Cleared the parent step description_reviewed flag because its orchestration contract changed.\n")
	}
	if driftReviewCleared {
		b.WriteString("- Flagged the parent step's drift_review.needs_review=true — plan_drift_review will treat it as due again.\n")
	}
	if driftReviewFlagFailed {
		b.WriteString("- ⚠️ FAILED to flag the parent step's drift_review.needs_review after two attempts — plan_drift_review may NOT notice this change. Report this failure explicitly rather than treating the route edit as fully clean.\n")
	}
	switch action {
	case "added":
		b.WriteString("- Route setup: configure step_config, tools/servers/skills, prevalidation, learnings, KB, DB, and optional learnings/" + routeID + "/main.py for the new nested agent.\n")
	case "deleted":
		b.WriteString("- Route cleanup: remove stale step_config, learnings/" + routeID + ", scripted main.py, DB/KB/report/eval references, and downstream context dependencies for the removed nested agent.\n")
	default:
		b.WriteString("- Route update: review route condition, sub-agent description, declared context_dependencies, prevalidation, learnings, DB, KB, scripts, reports/evals, and downstream dependencies.\n")
	}
	b.WriteString("- " + planEditImpactGuidance)
	return b.String()
}

func handleOrchestratorRouteArtifactReview(ctx context.Context, workspacePath, parentStepID, routeID, action string, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, logger loggerv2.Logger) string {
	fieldChanges := []PlanFieldChange{{
		StepID: parentStepID,
		Field:  "predefined_routes",
	}}
	descriptionReviewCleared, err := clearDescriptionReviewedAfterPlanUpdate(ctx, workspacePath, parentStepID, fieldChanges, readFile, writeFile)
	if err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to clear description_reviewed after %s route %s on step %s: %v", action, routeID, parentStepID, err))
	}
	driftReviewCleared, err := clearDriftReviewAfterPlanUpdateRetried(ctx, workspacePath, parentStepID, fieldChanges, readFile, writeFile, logger)
	driftReviewFlagFailed := false
	if err != nil {
		logger.Warn(fmt.Sprintf("⚠️ Failed to flag drift_review.needs_review after %s route %s on step %s (both attempts failed): %v", action, routeID, parentStepID, err))
		driftReviewFlagFailed = true
	}
	return buildOrchestratorRouteArtifactReviewNotice(parentStepID, routeID, action, descriptionReviewCleared, driftReviewCleared, driftReviewFlagFailed)
}

// createUpdateRegularStepExecutor edits the internal regular plan type exposed as update_scripted_step.
func createUpdateRegularStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		// Convert args to JSON and unmarshal to PartialPlanStep
		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		// Read current plan
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}
		stepConfigs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read step config: %w", err)
		}
		downgradedLegacySequence, err := prepareScriptedStepUpdateTarget(plan, stepConfigs, partialUpdate.ExistingStepID)
		if err != nil {
			return "", err
		}

		// Track per-field changes — passed to the plan changelog after a
		// successful write.
		fieldChanges := make([]PlanFieldChange, 0)
		if downgradedLegacySequence {
			fieldChanges = append(fieldChanges, PlanFieldChange{
				StepID:   partialUpdate.ExistingStepID,
				Field:    "type",
				OldValue: string(StepTypeMessageSeq),
				NewValue: string(StepTypeRegular),
			})
		}

		_, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}
		updatedStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID)
		if updatedStep == nil {
			updatedStep, _, _ = findStepByID(plan.OrphanSteps, partialUpdate.ExistingStepID)
		}
		if scriptedStep, ok := updatedStep.(*RegularPlanStep); ok {
			if err := validateScriptParameterContract(&scriptedStep.CommonStepFields); err != nil {
				return "", fmt.Errorf("validation failed: invalid script_parameters: %w", err)
			}
		}

		// Validate all steps after update
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_scripted_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)

		downgradeNotice := ""
		if downgradedLegacySequence {
			downgradeNotice = " and downgraded its saved legacy message_sequence type to regular, so the real scripted executor (with its $DB_PATH/STEP_OUTPUT_DIR injection) now runs it"
		}
		logger.Info(fmt.Sprintf("✅ Updated scripted step '%s' in plan%s", partialUpdate.ExistingStepID, downgradeNotice))
		return fmt.Sprintf("Successfully updated scripted step '%s' in the plan%s%s", partialUpdate.ExistingStepID, downgradeNotice, dependentReviewNotice), nil
	}
}

// prepareScriptedStepUpdateTarget is prepareMessageSequenceUpdateTarget's
// mirror (PLAT-280). A message_sequence step whose step_config already
// declares scripted mode has no in-place way to become a true `regular` plan
// step, so update_message_sequence_step was its only editable path even
// though its checked-in learnings/{step-id}/main.py needs the real scripted
// executor. This atomically downgrades it to `regular` (dropping items, which
// carry no information a scripted step uses) while applying the edit.
func prepareScriptedStepUpdateTarget(plan *PlanningResponse, stepConfigs []StepConfig, stepID string) (bool, error) {
	if plan == nil {
		return false, fmt.Errorf("cannot update scripted step %q: plan is unavailable", stepID)
	}
	existingStep, _, _ := findStepByID(plan.Steps, stepID)
	if existingStep == nil {
		existingStep, _, _ = findStepByID(plan.OrphanSteps, stepID)
	}
	if existingStep == nil {
		return false, nil // updateSingleStep returns the existing detailed not-found error.
	}

	switch step := existingStep.(type) {
	case *RegularPlanStep:
		if isLegacyAgenticRegularStep(step, MatchStepConfigByID(stepID, stepConfigs)) {
			return false, fmt.Errorf("step %q is a legacy agentic regular step (retired declared_execution_mode=\"agentic\") that still runs as a message_sequence; use update_message_sequence_step, which upgrades its saved type atomically with the edit (or run the v1.0.38 migration migrate_declared_execution_mode), or change_step_type(step_id=%q, target_type=\"scripted\") if it should become a real scripted step", stepID, stepID)
		}
		return false, nil
	case *MessageSequencePlanStep:
		return false, fmt.Errorf("step %q is a message_sequence step, not a scripted step; use update_message_sequence_step, or change_step_type(step_id=%q, target_type=\"scripted\") first if it should become a scripted step", stepID, stepID)
	default:
		return false, wrongStepTypeToolError(stepID, existingStep.StepType(), "update_scripted_step")
	}
}

// prepareMessageSequenceUpdateTarget makes the mutation API agree with the
// runtime compatibility adapter. Persisted non-scripted regular steps already
// execute as message_sequence, so the message-sequence updater is their single
// valid editing path and upgrades the stored type atomically with the edit.
func prepareMessageSequenceUpdateTarget(plan *PlanningResponse, stepConfigs []StepConfig, stepID string) (bool, error) {
	if plan == nil {
		return false, fmt.Errorf("cannot update message_sequence step %q: plan is unavailable", stepID)
	}
	existingStep, _, _ := findStepByID(plan.Steps, stepID)
	if existingStep == nil {
		existingStep, _, _ = findStepByID(plan.OrphanSteps, stepID)
	}
	if existingStep == nil {
		return false, nil // updateSingleStep returns the detailed not-found error.
	}

	switch step := existingStep.(type) {
	case *MessageSequencePlanStep:
		return false, nil
	case *RegularPlanStep:
		if !isLegacyAgenticRegularStep(step, MatchStepConfigByID(stepID, stepConfigs)) {
			return false, fmt.Errorf("step %q is a scripted (regular) step, not message_sequence; use update_scripted_step, or change_step_type(step_id=%q, target_type=\"message_sequence\") first if it should become conversational", stepID, stepID)
		}
		replacement := normalizeRegularStepToMessageSequence(step)
		replaced, _ := replaceStepRecursively(plan.Steps, stepID, replacement)
		if !replaced {
			replaced, _ = replaceStepRecursively(plan.OrphanSteps, stepID, replacement)
		}
		if !replaced {
			return false, fmt.Errorf("failed to upgrade legacy agentic regular step %q to message_sequence", stepID)
		}
		return true, nil
	default:
		return false, wrongStepTypeToolError(stepID, existingStep.StepType(), "update_message_sequence_step")
	}
}

// descriptionSizeRelativeMultiplier and descriptionSizeAbsoluteFloor gate
// stepDescriptionSizeNudge's plan-relative check; descriptionSizeGrowthThreshold
// gates its single-edit-growth check. A nudge that fired unconditionally
// would itself be the same prompt-bloat problem one level up.
//
// A fixed global size (originally 8000) doesn't hold across workflows: real
// data checked 2026-09-17 across four plans found linkedin's own MEDIAN step
// description already at 7,151 chars (18 steps) while testing's was 731 (11
// steps) -- a global floor would nag constantly on one and stay silent on a
// real outlier in the other. Comparing against this plan's own median
// instead is self-calibrating. The floor keeps a small workflow (like
// testing) from tripping on relative size alone when every step is still
// objectively tiny.
const (
	descriptionSizeRelativeMultiplier = 2.5
	descriptionSizeAbsoluteFloor      = 5000
	descriptionSizeGrowthThreshold    = 1000
)

// planMedianOtherStepDescriptionLen returns the median description length
// across this plan's other steps (excluding excludeStepID), 0 if none have a
// description. Used as stepDescriptionSizeNudge's per-plan baseline.
func planMedianOtherStepDescriptionLen(steps []PlanStepInterface, excludeStepID string) int {
	var lens []int
	for _, step := range steps {
		if step == nil || step.GetID() == excludeStepID {
			continue
		}
		if l := len(step.GetDescription()); l > 0 {
			lens = append(lens, l)
		}
	}
	if len(lens) == 0 {
		return 0
	}
	sort.Ints(lens)
	mid := len(lens) / 2
	if len(lens)%2 == 0 {
		return (lens[mid-1] + lens[mid]) / 2
	}
	return lens[mid]
}

// stepDescriptionSizeNudge returns a pointed reminder to route durable
// technique into a skill instead of the step description, appended to
// add/update_message_sequence_step's response when the description is well
// above this plan's own normal (descriptionSizeRelativeMultiplier times
// planMedian, floored at descriptionSizeAbsoluteFloor so a small workflow's
// relative size alone doesn't trip it) or grew significantly in this one
// edit (descriptionSizeGrowthThreshold) -- growth alone catches a sudden
// large edit even mid-plan; the relative check catches slow accumulation
// across many small edits that each look harmless individually.
//
// Traced live (2026-09-17, sales-outreach's step-research-and-draft-outreach,
// plan median 4,335 chars): four separate owner-approved fixes for "emails
// read as AI-generated" each patched the description directly (2,951 ->
// 22,784 chars, 5.3x this plan's own median, over 5 weeks) instead of being
// promoted to a skill once proven -- nothing signaled the cost of that
// choice at edit time, and a Plan Drift Review that ran specifically against
// this step 20 minutes after the last edit still did not close the loop
// back to a fix. This is the signal that was missing.
func stepDescriptionSizeNudge(before, after, planMedian int) string {
	grew := after - before
	relativeThreshold := int(float64(planMedian) * descriptionSizeRelativeMultiplier)
	if relativeThreshold < descriptionSizeAbsoluteFloor {
		relativeThreshold = descriptionSizeAbsoluteFloor
	}
	if after < relativeThreshold && grew < descriptionSizeGrowthThreshold {
		return ""
	}
	return fmt.Sprintf("\n\nDescription is now %d characters (+%d this edit; this plan's other steps have a median of %d). Description should stay WHAT to achieve -- the outcome, business rules, and binding constraints. HOW to do it (technique, phrasing rules, proven methods) belongs in a skill. Before finalizing: check learnings/_global/SKILL.md's index and attached skills for an existing topic covering this. If a topic already covers it, correct that topic file in place and fold this into it -- do not also leave the old wording standing if this changes it, and do not just append a new section under it; a topic file with two unreconciled versions of the same rule is worse than either alone, since nothing tells a step which one to follow. If this is genuinely new technique with no existing topic, add learnings/_global/references/<topic>.md (named for the subject, not this step) and a one-line index entry, rather than growing this description.", after, grew, planMedian)
}

func createUpdateMessageSequenceStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}
		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}
		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}
		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}
		stepConfigs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read step config: %w", err)
		}
		upgradedLegacyRegular, err := prepareMessageSequenceUpdateTarget(plan, stepConfigs, partialUpdate.ExistingStepID)
		if err != nil {
			return "", err
		}
		descriptionBeforeLen := 0
		if _, descriptionProvided := args["description"]; descriptionProvided {
			if existingStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID); existingStep != nil {
				descriptionBeforeLen = len(existingStep.GetDescription())
			}
		}

		fieldChanges := make([]PlanFieldChange, 0)
		if upgradedLegacyRegular {
			fieldChanges = append(fieldChanges, PlanFieldChange{
				StepID:   partialUpdate.ExistingStepID,
				Field:    "type",
				OldValue: string(StepTypeRegular),
				NewValue: string(StepTypeMessageSeq),
			})
		}
		_, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}
		updatedStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID)
		if updatedStep == nil {
			updatedStep, _, _ = findStepByID(plan.OrphanSteps, partialUpdate.ExistingStepID)
		}
		if sequenceStep, ok := updatedStep.(*MessageSequencePlanStep); ok {
			if err := validateMessageSequenceStepFieldsTyped(sequenceStep); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}
		}
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_message_sequence_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)
		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)
		upgradeNotice := ""
		if upgradedLegacyRegular {
			upgradeNotice = " and upgraded its saved legacy regular type to message_sequence"
		}
		sizeNudge := ""
		if _, descriptionProvided := args["description"]; descriptionProvided && updatedStep != nil {
			planMedian := planMedianOtherStepDescriptionLen(plan.Steps, partialUpdate.ExistingStepID)
			sizeNudge = stepDescriptionSizeNudge(descriptionBeforeLen, len(updatedStep.GetDescription()), planMedian)
		}
		logger.Info(fmt.Sprintf("✅ Updated message_sequence step '%s' in plan%s", partialUpdate.ExistingStepID, upgradeNotice))
		return fmt.Sprintf("Successfully updated message_sequence step '%s' in the plan%s%s%s", partialUpdate.ExistingStepID, upgradeNotice, dependentReviewNotice, sizeNudge), nil
	}
}

// extractStringArray extracts a []string from args[key], handling cases where the LLM
// sends either a proper JSON array or a stringified array (JSON or Python-style).
func extractStringArray(args map[string]interface{}, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("missing required argument: %s", key)
	}

	// Case 1: already a []interface{} (normal JSON array)
	if arr, ok := raw.([]interface{}); ok {
		result := make([]string, 0, len(arr))
		for _, v := range arr {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("invalid %s: array elements must be strings", key)
			}
			result = append(result, s)
		}
		return result, nil
	}

	// Case 2: string — try to parse as JSON array, then fall back to Python-style
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		// Try JSON parse first: ["a", "b"]
		var parsed []string
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed, nil
		}
		// Python-style: ['a', 'b'] — replace single quotes with double quotes and retry
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			fixed := strings.ReplaceAll(s, "'", "\"")
			if err := json.Unmarshal([]byte(fixed), &parsed); err == nil {
				return parsed, nil
			}
		}
		return nil, fmt.Errorf("invalid %s: could not parse string as array: %s", key, s)
	}

	return nil, fmt.Errorf("invalid %s: expected array or string, got %T", key, raw)
}

// createDeletePlanStepsExecutor creates an executor function for delete_plan_steps tool
// Note: For deleted steps, we unlock based on the old plan's step indices before deletion
func createDeletePlanStepsExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}

		// Extract deleted_step_ids from args
		deletedIDs, err := extractStringArray(args, "deleted_step_ids")
		if err != nil {
			return "", err
		}

		// Read current plan
		oldPlan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Create set of deleted step IDs
		deletedSet := make(map[string]bool)
		for _, id := range deletedIDs {
			deletedSet[id] = true
		}

		// Validate that all deleted steps exist
		existingStepsMap := make(map[string]bool)
		for _, step := range oldPlan.Steps {
			existingStepsMap[step.GetID()] = true
		}
		for _, step := range oldPlan.OrphanSteps {
			existingStepsMap[step.GetID()] = true
		}
		for _, id := range deletedIDs {
			if !existingStepsMap[id] {
				// Build list of available step IDs for better error message
				availableIDs := make([]string, 0, len(oldPlan.Steps)+len(oldPlan.OrphanSteps))
				for _, step := range oldPlan.Steps {
					availableIDs = append(availableIDs, step.GetID())
				}
				for _, step := range oldPlan.OrphanSteps {
					availableIDs = append(availableIDs, step.GetID())
				}
				return "", fmt.Errorf("step ID '%s' not found in existing plan (cannot delete). Available step IDs: %v", id, availableIDs)
			}
		}

		// Capture deleted steps BEFORE filtering (for changelog revert support)
		// Convert to JSON for changelog storage
		deletedSteps := make([]json.RawMessage, 0, len(deletedIDs))
		for _, step := range oldPlan.Steps {
			stepID := step.GetID()
			if deletedSet[stepID] {
				// Marshal step to JSON for changelog
				stepJSON, err := json.Marshal(step)
				if err != nil {
					logger.Warn(fmt.Sprintf("⚠️ Failed to marshal deleted step %s for changelog: %v", stepID, err))
					continue
				}
				deletedSteps = append(deletedSteps, stepJSON)
			}
		}

		// Filter out deleted steps
		filteredSteps := make([]PlanStepInterface, 0, len(oldPlan.Steps))
		for _, step := range oldPlan.Steps {
			if !deletedSet[step.GetID()] {
				filteredSteps = append(filteredSteps, step)
			}
		}

		// Also capture and filter orphan steps
		for _, step := range oldPlan.OrphanSteps {
			stepID := step.GetID()
			if deletedSet[stepID] {
				stepJSON, err := json.Marshal(step)
				if err != nil {
					logger.Warn(fmt.Sprintf("⚠️ Failed to marshal deleted orphan step %s for changelog: %v", stepID, err))
					continue
				}
				deletedSteps = append(deletedSteps, stepJSON)
			}
		}

		filteredOrphanSteps := make([]PlanStepInterface, 0, len(oldPlan.OrphanSteps))
		for _, step := range oldPlan.OrphanSteps {
			if !deletedSet[step.GetID()] {
				filteredOrphanSteps = append(filteredOrphanSteps, step)
			}
		}

		newPlan := &PlanningResponse{Steps: filteredSteps, OrphanSteps: filteredOrphanSteps}

		// Reject atomically before any plan/config/changelog side effect. This
		// gives the Builder every inbound reference it must reroute first.
		if err := ValidatePlanStructure(newPlan); err != nil {
			return "", fmt.Errorf("step deletion rejected; the existing plan was left unchanged: %w", err)
		}

		// Write updated plan (creates backup automatically)
		if err := writePlanToFile(ctx, workspacePath, newPlan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:         "delete_plan_steps",
			Reason:       reason,
			StepIDs:      deletedIDs,
			DeletedSteps: deletedSteps,
		}, readFile, writeFile, logger)

		// Cascade-delete the matching entries from planning/step_config.json so
		// the step's per-step config doesn't linger as an orphan after its plan
		// entry is gone, and flag the workflow-level drift review record
		// (WorkflowDriftReviewStepID) needs_review — a deleted step's own
		// drift_review record is gone along with it, so CollectPlanDriftCandidates'
		// per-step scan structurally cannot see anything requiring review for it;
		// this is the durable signal that dependent-artifact fallout from THIS
		// deletion still needs auditing. Both writes are combined into the same
		// read-modify-write to avoid a lost update between two separate passes.
		// Retried once on failure (same pattern as clearDriftReviewAfterPlanUpdateRetried):
		// a transient failure here would otherwise leave neither the deleted
		// step's own trigger (already gone) nor this workflow-level replacement,
		// so plan_drift_review would have no way at all to learn the deletion
		// happened. A persistent failure after the retry still doesn't fail the
		// plan-deletion call (the plan was already written, and is the point of
		// no return — this codebase has no transactional multi-file write
		// mechanism to roll it back), but is surfaced loudly in the tool's own
		// response rather than only logged, so it cannot be silently missed.
		prunedConfigIDs, driftReviewFlagFailed := cascadeDeleteStepConfigsRetried(ctx, workspacePath, deletedSet, deletedIDs, readFile, writeFile, logger)

		cleanupNotice := buildDeletedStepArtifactCleanupNotice(deletedIDs, prunedConfigIDs, driftReviewFlagFailed)

		logger.Info(fmt.Sprintf("✅ Deleted %d steps from plan", len(deletedIDs)))
		return fmt.Sprintf("Successfully deleted %d step(s) from the plan%s", len(deletedIDs), cleanupNotice), nil
	}
}

// createCleanupOrphanStepConfigsExecutor creates the executor for the
// cleanup_orphan_step_configs tool. Reads planning/step_config.json, computes
// the set of step IDs that no longer exist in plan.json (top-level Steps +
// OrphanSteps, recursing into nested sub_agent_step definitions),
// and rewrites step_config.json without those orphan entries.
func createCleanupOrphanStepConfigsExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Build the set of step IDs that legitimately exist anywhere in the plan
		// (including nested via collectAllSteps). Anything in step_config.json
		// outside this set is orphan.
		liveIDs := make(map[string]bool)
		for _, info := range collectAllSteps(plan.Steps) {
			liveIDs[info.Step.GetID()] = true
		}
		for _, info := range collectAllSteps(plan.OrphanSteps) {
			liveIDs[info.Step.GetID()] = true
		}

		configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read step_config.json: %w", err)
		}
		if len(configs) == 0 {
			return "No step_config.json entries found — nothing to clean up.", nil
		}

		kept := make([]StepConfig, 0, len(configs))
		var removed []string
		for _, cfg := range configs {
			// WorkflowDriftReviewStepID is never a real plan.json step, so it is
			// never "live" by this check's own definition — but it also is not
			// orphan garbage. It is the durable workflow-level drift review
			// record deletion coverage depends on; removing it here would erase
			// exactly the signal flagWorkflowDriftReviewOnDeletion just set.
			if liveIDs[cfg.ID] || cfg.ID == WorkflowDriftReviewStepID {
				kept = append(kept, cfg)
				continue
			}
			removed = append(removed, cfg.ID)
		}

		if len(removed) == 0 {
			return fmt.Sprintf("All %d step_config.json entries match a step in plan.json — nothing to remove.", len(configs)), nil
		}

		if err := writeStepConfigViaFileCallback(ctx, workspacePath, kept, writeFile); err != nil {
			return "", fmt.Errorf("failed to write step_config.json: %w", err)
		}

		logger.Info(fmt.Sprintf("🧹 cleanup_orphan_step_configs removed %d orphan entries: %v", len(removed), removed))
		return fmt.Sprintf("Removed %d orphan step_config.json entries: %v. Kept %d.", len(removed), removed, len(kept)), nil
	}
}

// createUpdateHumanInputStepExecutor creates an executor function for update_human_input_step tool
func createUpdateHumanInputStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		// Read current plan
		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Find the human input step
		var existingStep PlanStepInterface
		for _, step := range plan.Steps {
			if step.GetID() == partialUpdate.ExistingStepID {
				existingStep = step
				break
			}
		}
		if existingStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("step ID '%s' not found in existing plan. Available step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
		}

		// Validate it's a human input step before updating
		_, ok := existingStep.(*HumanInputPlanStep)
		if !ok {
			return "", fmt.Errorf("step with ID '%s' is not a human input step", partialUpdate.ExistingStepID)
		}

		fieldChanges := make([]PlanFieldChange, 0)

		_, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Validate all steps after update
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_human_input_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated human input step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated human input step '%s' in the plan%s", partialUpdate.ExistingStepID, dependentReviewNotice), nil
	}
}

// createUpdateOrchestratorStepExecutor creates an executor function for update_todo_task_step tool
func createUpdateOrchestratorStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		// Read current plan
		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Find the todo task step
		var existingStep PlanStepInterface
		for _, step := range plan.Steps {
			if step.GetID() == partialUpdate.ExistingStepID {
				existingStep = step
				break
			}
		}
		if existingStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("step ID '%s' not found in existing plan. Available step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
		}

		// Validate it's a todo task step before updating
		orchestratorStep, ok := existingStep.(*OrchestratorPlanStep)
		if !ok {
			return "", fmt.Errorf("step with ID '%s' is not a todo task step", partialUpdate.ExistingStepID)
		}
		existingRegularRouteIDs := make(map[string]bool)
		for _, regularStep := range collectRegularPlanSteps(orchestratorStep) {
			existingRegularRouteIDs[regularStep.ID] = true
		}
		descriptionBeforeLen := 0
		_, descriptionProvided := args["description"]
		if descriptionProvided {
			descriptionBeforeLen = len(existingStep.GetDescription())
		}

		// Track changes for changelog
		fieldChanges := make([]PlanFieldChange, 0)

		// Update the step
		var stepIndex int
		stepIndex, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Get the updated step from the plan
		updatedStep := plan.Steps[stepIndex]
		updatedOrchestratorStep, ok := updatedStep.(*OrchestratorPlanStep)
		if !ok {
			return "", fmt.Errorf("updated step is not a todo task step")
		}

		// Validate the updated step has all required fields
		if err := validateOrchestratorStepFieldsTyped(updatedOrchestratorStep); err != nil {
			return "", fmt.Errorf("validation failed after update: %w", err)
		}

		// Validate all steps after update
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}
		var newRegularRoutes []*RegularPlanStep
		for _, regularStep := range collectRegularPlanSteps(updatedOrchestratorStep) {
			if !existingRegularRouteIDs[regularStep.ID] {
				newRegularRoutes = append(newRegularRoutes, regularStep)
			}
		}
		if _, err := configureRegularStepsAsScripted(ctx, workspacePath, newRegularRoutes, readFile, writeFile); err != nil {
			return "", fmt.Errorf("todo task was updated but required scripted configuration for a newly added regular route could not be saved: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_orchestrator_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		_ = orchestratorStep // Suppress unused variable warning
		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)
		sizeNudge := ""
		if descriptionProvided {
			planMedian := planMedianOtherStepDescriptionLen(plan.Steps, partialUpdate.ExistingStepID)
			sizeNudge = stepDescriptionSizeNudge(descriptionBeforeLen, len(updatedOrchestratorStep.GetDescription()), planMedian)
		}
		logger.Info(fmt.Sprintf("✅ Updated todo task step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated todo task step '%s' in the plan%s%s", partialUpdate.ExistingStepID, dependentReviewNotice, sizeNudge), nil
	}
}

// createAddRoutingStepExecutor creates an executor function for add_routing_step tool
func createAddRoutingStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "routing")
}

// createAddBranchStepExecutor creates an executor function for add_branch_step tool.
func createAddBranchStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "branch")
}

// validateRoutingStepFieldsTyped validates that a RoutingPlanStep has all required fields
func validateRoutingStepFieldsTyped(step *RoutingPlanStep) error {
	if step.ID == "" {
		return fmt.Errorf("routing step (title: %q) is missing required ID field", step.Title)
	}
	if strings.TrimSpace(step.Description) != "" {
		return fmt.Errorf("routing step (title: %q, ID: %s) must not set description; routing is deterministic-only. Put any probe or judgment in a prior message_sequence step that writes %s", step.Title, step.ID, routeSelectionFileName)
	}
	if step.RoutingQuestion == "" {
		return fmt.Errorf("routing step (title: %q, ID: %s) is missing required routing_question field", step.Title, step.ID)
	}
	if len(step.Routes) < 2 {
		return fmt.Errorf("routing step (title: %q, ID: %s) must have at least 2 routes, got %d", step.Title, step.ID, len(step.Routes))
	}
	// Check for duplicate route IDs
	routeIDs := make(map[string]bool)
	for _, route := range step.Routes {
		if route.RouteID == "" {
			return fmt.Errorf("routing step (title: %q, ID: %s) has a route with empty route_id", step.Title, step.ID)
		}
		if route.NextStepID == "" {
			return fmt.Errorf("routing step (title: %q, ID: %s) route %q is missing required next_step_id", step.Title, step.ID, route.RouteID)
		}
		if routeIDs[route.RouteID] {
			return fmt.Errorf("routing step (title: %q, ID: %s) has duplicate route_id %q", step.Title, step.ID, route.RouteID)
		}
		routeIDs[route.RouteID] = true
	}
	// Validate default_route_id if set
	if step.DefaultRouteID != "" {
		if !routeIDs[step.DefaultRouteID] {
			return fmt.Errorf("routing step (title: %q, ID: %s) has default_route_id %q that doesn't match any route_id", step.Title, step.ID, step.DefaultRouteID)
		}
	}
	return nil
}

// validateBranchStepFieldsTyped validates that a BranchPlanStep has all required
// fields. Mirrors validateRoutingStepFieldsTyped exactly -- same deterministic-
// switch rules, branch_question instead of routing_question.
func validateBranchStepFieldsTyped(step *BranchPlanStep) error {
	if step.ID == "" {
		return fmt.Errorf("branch step (title: %q) is missing required ID field", step.Title)
	}
	if strings.TrimSpace(step.Description) != "" {
		return fmt.Errorf("branch step (title: %q, ID: %s) must not set description; branch is deterministic-only. Put any probe or judgment in a prior message_sequence step that writes %s", step.Title, step.ID, routeSelectionFileName)
	}
	if step.BranchQuestion == "" {
		return fmt.Errorf("branch step (title: %q, ID: %s) is missing required branch_question field", step.Title, step.ID)
	}
	if rs := strings.TrimSpace(step.RouteSource); rs != "" && !strings.EqualFold(rs, branchRouteSourceHuman) {
		return fmt.Errorf("branch step (title: %q, ID: %s) has unsupported route_source %q; leave it unset for a preseeded switch or set \"human\" to ask the person when no route was preseeded", step.Title, step.ID, step.RouteSource)
	}
	if len(step.Routes) < 2 {
		return fmt.Errorf("branch step (title: %q, ID: %s) must have at least 2 routes, got %d", step.Title, step.ID, len(step.Routes))
	}
	// Check for duplicate route IDs
	routeIDs := make(map[string]bool)
	for _, route := range step.Routes {
		if route.RouteID == "" {
			return fmt.Errorf("branch step (title: %q, ID: %s) has a route with empty route_id", step.Title, step.ID)
		}
		if route.NextStepID == "" {
			return fmt.Errorf("branch step (title: %q, ID: %s) route %q is missing required next_step_id", step.Title, step.ID, route.RouteID)
		}
		if routeIDs[route.RouteID] {
			return fmt.Errorf("branch step (title: %q, ID: %s) has duplicate route_id %q", step.Title, step.ID, route.RouteID)
		}
		routeIDs[route.RouteID] = true
	}
	// Validate default_route_id if set
	if step.DefaultRouteID != "" {
		if !routeIDs[step.DefaultRouteID] {
			return fmt.Errorf("branch step (title: %q, ID: %s) has default_route_id %q that doesn't match any route_id", step.Title, step.ID, step.DefaultRouteID)
		}
	}
	return nil
}

// createUpdateRoutingStepExecutor creates an executor function for update_routing_step tool
func createUpdateRoutingStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if clearDescription, _ := args["clear_description"].(bool); clearDescription {
			if description, ok := args["description"].(string); ok && strings.TrimSpace(description) != "" {
				return "", fmt.Errorf("description must be empty when clear_description=true")
			}
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		var existingStep PlanStepInterface
		for _, step := range plan.Steps {
			if step.GetID() == partialUpdate.ExistingStepID {
				existingStep = step
				break
			}
		}
		if existingStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("step ID '%s' not found in existing plan. Available step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
		}

		if _, ok := existingStep.(*RoutingPlanStep); !ok {
			return "", fmt.Errorf("step with ID '%s' is not a routing step", partialUpdate.ExistingStepID)
		}

		fieldChanges := make([]PlanFieldChange, 0)
		stepIndex, _, err := updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Validate the updated step
		updatedStep := plan.Steps[stepIndex]
		updatedRoutingStep, ok := updatedStep.(*RoutingPlanStep)
		if !ok {
			return "", fmt.Errorf("updated step is not a routing step")
		}

		if err := validateRoutingStepFieldsTyped(updatedRoutingStep); err != nil {
			return "", fmt.Errorf("validation failed after update: %w", err)
		}

		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_routing_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated routing step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated routing step '%s' in the plan%s", partialUpdate.ExistingStepID, dependentReviewNotice), nil
	}
}

// createUpdateBranchStepExecutor creates an executor function for update_branch_step
// tool. Mirrors createUpdateRoutingStepExecutor exactly -- same shape, *BranchPlanStep
// instead of *RoutingPlanStep.
func createUpdateBranchStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if clearDescription, _ := args["clear_description"].(bool); clearDescription {
			if description, ok := args["description"].(string); ok && strings.TrimSpace(description) != "" {
				return "", fmt.Errorf("description must be empty when clear_description=true")
			}
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var partialUpdate PartialPlanStep
		if err := json.Unmarshal(stepJSON, &partialUpdate); err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		var existingStep PlanStepInterface
		for _, step := range plan.Steps {
			if step.GetID() == partialUpdate.ExistingStepID {
				existingStep = step
				break
			}
		}
		if existingStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("step ID '%s' not found in existing plan. Available step IDs: %v", partialUpdate.ExistingStepID, availableIDs)
		}

		if _, ok := existingStep.(*BranchPlanStep); !ok {
			return "", fmt.Errorf("step with ID '%s' is not a branch step", partialUpdate.ExistingStepID)
		}

		fieldChanges := make([]PlanFieldChange, 0)
		stepIndex, _, err := updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Validate the updated step
		updatedStep := plan.Steps[stepIndex]
		updatedBranchStep, ok := updatedStep.(*BranchPlanStep)
		if !ok {
			return "", fmt.Errorf("updated step is not a branch step")
		}

		if err := validateBranchStepFieldsTyped(updatedBranchStep); err != nil {
			return "", fmt.Errorf("validation failed after update: %w", err)
		}

		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_branch_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated branch step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated branch step '%s' in the plan%s", partialUpdate.ExistingStepID, dependentReviewNotice), nil
	}
}

// getConvertRoutingBranchStepTypeSchema returns the JSON schema for
// convert_routing_branch_step_type.
func getConvertRoutingBranchStepTypeSchema() string {
	return `{
		"type": "object",
		"properties": {
			"existing_step_id": {
				"type": "string",
				"description": "The step's id field from plan.json. Must currently be a routing or branch step."
			},
			"target_type": {
				"type": "string",
				"enum": ["routing", "branch"],
				"description": "The type to convert the step to. Must differ from its current type -- routing to convert a branch step into a routing step, branch to convert a routing step into a branch step. A plan has at most one routing step (the mode selector), so converting to routing is rejected while another routing step exists; convert that one to branch first."
			},
			"reason": {
				"type": "string",
				"description": "One-sentence rationale for reclassifying the step. Recorded in the plan changelog."
			}
		},
		"required": ["existing_step_id", "target_type", "reason"]
	}`
}

// createConvertRoutingBranchStepTypeExecutor creates the executor for
// convert_routing_branch_step_type: an atomic, in-place reclassification
// between routing and branch that keeps the step's id unchanged. routing and
// branch share the exact same routeSwitchStep shape (routes/default_route_id/
// route_source_file, only the human-readable question field's name differs),
// so this is a pure relabeling, not a data migration.
//
// Deliberately does NOT delete and recreate the step. An earlier guided-flow
// doc (migrate-routing-to-branch.md) told the agent to reroute references,
// delete the old step, then recreate it under the original id to "preserve
// history" -- but delete_plan_steps prunes the deleted id's step_config.json
// row (drift_review, execution_tier, etc.) before the id is ever reused, so
// that claimed continuity was false. Keeping the same id throughout, in one
// mutation, means step_config.json's row for it is never orphaned at all --
// there is nothing to lose and nothing to restore.
func createConvertRoutingBranchStepTypeExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		stepID := strings.TrimSpace(asString(args["existing_step_id"]))
		if stepID == "" {
			return "", fmt.Errorf("existing_step_id is required")
		}
		targetType := strings.TrimSpace(asString(args["target_type"]))
		if targetType != "routing" && targetType != "branch" {
			return "", fmt.Errorf("target_type must be \"routing\" or \"branch\", got %q", targetType)
		}

		plan, err := readPlanForMutation(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		stepIndex := -1
		for i, step := range plan.Steps {
			if step.GetID() == stepID {
				stepIndex = i
				break
			}
		}
		if stepIndex == -1 {
			return "", fmt.Errorf("step ID '%s' not found in existing plan", stepID)
		}

		// PLAT-294: converting to routing must not give the plan a second
		// routing step. Converting to branch is always allowed.
		if targetType == "routing" {
			if err := validateSingleRoutingStepForMutation(plan, stepID); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}
		}

		var updated PlanStepInterface
		var oldType string
		switch s := plan.Steps[stepIndex].(type) {
		case *RoutingPlanStep:
			if targetType != "branch" {
				return "", fmt.Errorf("step '%s' is already a routing step", stepID)
			}
			updated = &BranchPlanStep{
				Type:             StepTypeBranch,
				CommonStepFields: s.CommonStepFields,
				BranchQuestion:   s.RoutingQuestion,
				Routes:           s.Routes,
				DefaultRouteID:   s.DefaultRouteID,
				RouteSourceFile:  s.RouteSourceFile,
			}
			oldType = "routing"
		case *BranchPlanStep:
			if targetType != "routing" {
				return "", fmt.Errorf("step '%s' is already a branch step", stepID)
			}
			// PLAT-294: a routing step's routes each start a sub-workflow;
			// a route to end marks a simple if-condition that should stay a branch.
			if err := validateRoutingRoutesStartSubWorkflows(stepID, s.Routes); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}
			// Only a branch can ask a person; routing is always preseeded.
			if isHumanRoutedBranch(s) {
				return "", fmt.Errorf("validation failed: branch step %q has route_source \"human\" and cannot become a routing step; routing is the mode selector and is always answered up front by route_selections. Keep it a branch, or clear route_source first if a caller will always preseed it", stepID)
			}
			updated = &RoutingPlanStep{
				Type:             StepTypeRouting,
				CommonStepFields: s.CommonStepFields,
				RoutingQuestion:  s.BranchQuestion,
				Routes:           s.Routes,
				DefaultRouteID:   s.DefaultRouteID,
				RouteSourceFile:  s.RouteSourceFile,
			}
			oldType = "branch"
		default:
			return "", fmt.Errorf("step '%s' is a %T, not a routing or branch step", stepID, plan.Steps[stepIndex])
		}
		plan.Steps[stepIndex] = updated

		switch s := updated.(type) {
		case *RoutingPlanStep:
			if err := validateRoutingStepFieldsTyped(s); err != nil {
				return "", fmt.Errorf("validation failed after conversion: %w", err)
			}
		case *BranchPlanStep:
			if err := validateBranchStepFieldsTyped(s); err != nil {
				return "", fmt.Errorf("validation failed after conversion: %w", err)
			}
		}
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after conversion: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after conversion: %w", err)
		}

		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		fieldChanges := []PlanFieldChange{{StepID: stepID, Field: "type", OldValue: oldType, NewValue: targetType}}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "convert_routing_branch_step_type",
			Reason:  reason,
			StepIDs: []string{stepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, stepID, fieldChanges, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Converted step '%s' from %s to %s (same id, step_config.json/drift-review history preserved)", stepID, oldType, targetType))
		return fmt.Sprintf("Successfully converted step '%s' from %s to %s. The step id is unchanged, so its step_config.json and drift_review history remain continuous -- no separate cleanup needed.%s", stepID, oldType, targetType, dependentReviewNotice), nil
	}
}

// createAddRegularStepExecutor creates the internal regular plan type exposed as add_scripted_step.
func createAddRegularStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "regular")
}

// upsertNewScriptedRegularStepConfig gives a new regular (scripted) step the
// one config flag its type implies: code execution on. The plan type itself
// is what makes the step scripted (PLAT-287).
func upsertNewScriptedRegularStepConfig(configs []StepConfig, stepID, title string) []StepConfig {
	stepID = strings.TrimSpace(stepID)
	useCode := true
	for i := range configs {
		if configs[i].ID != stepID {
			continue
		}
		if configs[i].AgentConfigs == nil {
			configs[i].AgentConfigs = &AgentConfigs{}
		}
		configs[i].Title = title
		configs[i].AgentConfigs.UseCodeExecutionMode = &useCode
		configs[i].AgentConfigs.LegacyDeclaredExecutionMode = ""
		configs[i].AgentConfigs.LegacyDeclaredExecutionModeReason = ""
		return configs
	}
	return append(configs, StepConfig{
		ID:           stepID,
		Title:        title,
		AgentConfigs: &AgentConfigs{UseCodeExecutionMode: &useCode},
	})
}

func collectRegularPlanSteps(step PlanStepInterface) []*RegularPlanStep {
	if step == nil {
		return nil
	}
	switch typed := step.(type) {
	case *RegularPlanStep:
		return []*RegularPlanStep{typed}
	case *OrchestratorPlanStep:
		var regularSteps []*RegularPlanStep
		for _, route := range typed.PredefinedRoutes {
			regularSteps = append(regularSteps, collectRegularPlanSteps(route.SubAgentStep)...)
		}
		return regularSteps
	default:
		return nil
	}
}

func configureNewRegularStepsAsScripted(ctx context.Context, workspacePath string, step PlanStepInterface, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (int, error) {
	regularSteps := collectRegularPlanSteps(step)
	return configureRegularStepsAsScripted(ctx, workspacePath, regularSteps, readFile, writeFile)
}

func configureRegularStepsAsScripted(ctx context.Context, workspacePath string, regularSteps []*RegularPlanStep, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (int, error) {
	if len(regularSteps) == 0 {
		return 0, nil
	}
	configs, err := readStepConfigViaFileCallback(ctx, workspacePath, readFile)
	if err != nil {
		return 0, err
	}
	for _, regularStep := range regularSteps {
		configs = upsertNewScriptedRegularStepConfig(configs, regularStep.ID, regularStep.Title)
	}
	if err := writeStepConfigViaFileCallback(ctx, workspacePath, configs, writeFile); err != nil {
		return 0, err
	}
	return len(regularSteps), nil
}

func createAddMessageSequenceStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "message_sequence")
}

// createAddHumanInputStepExecutor creates an executor function for add_human_input_step tool
func createAddHumanInputStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, "human_input")
}

// createAddOrchestratorStepExecutor creates an executor function for add_todo_task_step tool
func createAddOrchestratorStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return createSingleStepAdder(workspacePath, logger, readFile, writeFile, moveFile, string(StepTypeOrchestrator))
}

// validateOrchestratorStepFieldsTyped validates that a OrchestratorPlanStep has all required fields
// Returns an error message suitable for returning as a tool response if validation fails
func validateOrchestratorStepFieldsTyped(step *OrchestratorPlanStep) error {
	if step.ID == "" {
		return fmt.Errorf("step (title: %q) has todo_task step type but is missing required id field", step.Title)
	}
	if step.Description == "" {
		return fmt.Errorf("step (title: %q, ID: %s) has todo_task step type but is missing required description field. Please provide a description", step.Title, step.ID)
	}
	if step.NextStepID == "" {
		return fmt.Errorf("step (title: %q, ID: %s) has todo_task step type but is missing required next_step_id field. Please provide the ID of the step to connect to after all todos are complete, or 'end' to terminate the workflow", step.Title, step.ID)
	}
	if err := validateAgentDelegationRoutes(step.Title, step.ID, step.PredefinedRoutes, false); err != nil {
		return err
	}
	if err := validateOrchestratorNestingDepth(step, 0); err != nil {
		return err
	}
	for i, m := range step.Messages {
		mType := strings.TrimSpace(m.Type)
		switch mType {
		case "", "message", "user_message":
			if strings.TrimSpace(m.Message) == "" {
				return fmt.Errorf("step (title: %q, ID: %s) messages[%d] is a message entry but has an empty message", step.Title, step.ID, i)
			}
		case "prevalidation":
			if m.ValidationSchema == nil {
				return fmt.Errorf("step (title: %q, ID: %s) messages[%d] is a prevalidation entry but has no validation_schema", step.Title, step.ID, i)
			}
		case "foreach":
			if strings.TrimSpace(m.SourceSQL) == "" {
				return fmt.Errorf("step (title: %q, ID: %s) messages[%d] is a foreach entry but has no source_sql", step.Title, step.ID, i)
			}
			if strings.TrimSpace(m.Message) == "" {
				return fmt.Errorf("step (title: %q, ID: %s) messages[%d] is a foreach entry but has no message (the per-row template)", step.Title, step.ID, i)
			}
		case "scripted":
			if err := validateMessageSequenceScriptItem(m); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) messages[%d]: %w", step.Title, step.ID, i, err)
			}
		default:
			return fmt.Errorf("step (title: %q, ID: %s) messages[%d] has unsupported type %q (use message, prevalidation, foreach, or scripted)", step.Title, step.ID, i, mType)
		}
	}
	return nil
}

func validateAgentDelegationRoutes(title, stepID string, routes []PlanOrchestrationRoute, allowLegacyCode bool) error {
	for i, route := range routes {
		if route.RouteID == "" {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] with missing required route_id field", title, stepID, i)
		}
		if route.RouteName == "" {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] (route_id: %s) with missing required route_name field", title, stepID, i, route.RouteID)
		}
		if route.SubAgentStep == nil {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] (route_id: %s) with missing required sub_agent_step field", title, stepID, i, route.RouteID)
		}
		if route.SubAgentStep.GetID() == "" {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] (route_id: %s) with sub_agent_step missing required ID field", title, stepID, i, route.RouteID)
		}
		if route.SubAgentStep.GetID() != route.RouteID {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] with mismatched IDs: route_id=%q but sub_agent_step.id=%q. For agent routes, use a single canonical ID only", title, stepID, i, route.RouteID, route.SubAgentStep.GetID())
		}
		if route.SubAgentStep.GetTitle() == "" {
			return fmt.Errorf("step (title: %q, ID: %s) has predefined_route[%d] (route_id: %s) with sub_agent_step missing required title field", title, stepID, i, route.RouteID)
		}
		switch subStep := route.SubAgentStep.(type) {
		case *MessageSequencePlanStep:
			if err := validateMessageSequenceStepFieldsTypedWithOptions(subStep, allowLegacyCode); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) predefined_route[%d] (route_id: %s): %w", title, stepID, i, route.RouteID, err)
			}
		case *OrchestratorPlanStep:
			if err := validateOrchestratorStepFieldsTyped(subStep); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) predefined_route[%d] (route_id: %s): %w", title, stepID, i, route.RouteID, err)
			}
		case *RegularPlanStep:
			if err := validateScriptParameterContract(&subStep.CommonStepFields); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) predefined_route[%d] (route_id: %s) has invalid script_parameters: %w", title, stepID, i, route.RouteID, err)
			}
		}
	}
	return nil
}

func validateMessageSequenceStepFieldsTyped(step *MessageSequencePlanStep) error {
	return validateMessageSequenceStepFieldsTypedWithOptions(step, false)
}

func validateMessageSequenceStepFieldsTypedWithOptions(step *MessageSequencePlanStep, allowLegacyCode bool) error {
	if step == nil {
		return fmt.Errorf("message_sequence step is nil")
	}
	if strings.TrimSpace(step.ID) == "" {
		return fmt.Errorf("message_sequence step (title: %q) is missing required id field", step.Title)
	}
	if strings.TrimSpace(step.Title) == "" {
		return fmt.Errorf("message_sequence step (ID: %s) is missing required title field", step.ID)
	}
	if strings.TrimSpace(step.Description) == "" {
		return fmt.Errorf("message_sequence step (title: %q, ID: %s) is missing required description field", step.Title, step.ID)
	}
	if len(step.Items) == 0 {
		return fmt.Errorf("message_sequence step (title: %q, ID: %s) must include at least one item", step.Title, step.ID)
	}
	if step.AuthoredPrompt {
		if strings.TrimSpace(step.SystemPrompt) == "" {
			return fmt.Errorf("message_sequence step %q: authored_prompt requires system_prompt", step.ID)
		}
		if lastType := strings.TrimSpace(step.Items[len(step.Items)-1].Type); lastType != "" && lastType != "user_message" {
			return fmt.Errorf("message_sequence step %q: authored_prompt must end with a user_message that returns JSON", step.ID)
		}
	}
	seen := make(map[string]bool, len(step.Items))
	for i, item := range step.Items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("message_sequence step %q item[%d] is missing required id", step.ID, i)
		}
		if seen[item.ID] {
			return fmt.Errorf("message_sequence step %q has duplicate item id %q", step.ID, item.ID)
		}
		seen[item.ID] = true
		itemType := strings.TrimSpace(item.Type)
		if itemType == "" {
			itemType = "user_message"
		}
		if itemType != "scripted" && (len(item.ScriptedSteps) > 0 || item.MaxParallel != 0) {
			return fmt.Errorf("message_sequence item %q: scripted_steps/max_parallel require type scripted", item.ID)
		}
		switch itemType {
		case "scripted":
			if err := validateMessageSequenceScriptItem(item); err != nil {
				return err
			}
		case "user_message":
			if strings.TrimSpace(item.Message) == "" {
				return fmt.Errorf("message_sequence step %q item %q is a user_message but message is empty", step.ID, item.ID)
			}
		case "code":
			if allowLegacyCode {
				continue
			}
			return fmt.Errorf("message_sequence step %q item %q uses removed type \"code\"; upgrade this workflow to contract v1.0.10 so migrate_message_sequence_code_items can convert it to a standalone scripted regular step", step.ID, item.ID)
		case "prevalidation":
			if item.ValidationSchema == nil && item.Prevalidation == nil && step.ValidationSchema == nil {
				return fmt.Errorf("message_sequence step %q item %q is prevalidation but no validation_schema/prevalidation exists", step.ID, item.ID)
			}
		case "foreach":
			if strings.TrimSpace(item.SourceSQL) == "" {
				return fmt.Errorf("message_sequence step %q item %q is foreach but source_sql is empty", step.ID, item.ID)
			}
			if strings.TrimSpace(item.Message) == "" {
				return fmt.Errorf("message_sequence step %q item %q is foreach but message (the per-row template) is empty", step.ID, item.ID)
			}
		default:
			return fmt.Errorf("message_sequence step %q item %q has unsupported type %q", step.ID, item.ID, item.Type)
		}
		if step.AuthoredPrompt && itemType != "user_message" && itemType != "scripted" {
			return fmt.Errorf("message_sequence step %q item %q: authored_prompt supports only user_message and scripted items", step.ID, item.ID)
		}

		// NOTE: per-item write access (db/kb/learnings) is intentionally NOT
		// validated from the message prose here. Detecting "this item writes the
		// db" from natural language is unreliable (false negatives like "upsert
		// into trade_ideas", false positives on descriptive text), so we don't
		// gate plans on a guess. Enforcement lives at the runtime folder guard
		// (the source of truth — it blocks the write and emits an actionable
		// "grant write_access.db" failure), and structured signals (kind /
		// item kind can auto-grant via resolveMessageSequenceItemWriteAccess. The
		// authoring contract is carried by guidance, not a prose regex.
	}
	if err := validateAgentDelegationRoutes(step.Title, step.ID, step.PredefinedRoutes, allowLegacyCode); err != nil {
		return err
	}
	if err := validateOrchestratorNestingDepth(step, 0); err != nil {
		return err
	}
	return nil
}

func setStepIdentity(step PlanStepInterface, id, title string) error {
	switch s := step.(type) {
	case *RegularPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *OrchestratorPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *HumanInputPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *RoutingPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *BranchPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *MessageSequencePlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	case *CrewPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	default:
		return fmt.Errorf("unsupported sub_agent_step type %T for identity normalization", step)
	}
	return nil
}

func validateOrchestratorNestingDepth(step PlanStepInterface, todoRouteDepth int) error {
	var routes []PlanOrchestrationRoute
	switch s := step.(type) {
	case *OrchestratorPlanStep:
		routes = s.PredefinedRoutes
	case *MessageSequencePlanStep:
		routes = s.PredefinedRoutes
	}
	if len(routes) > 0 {
		if todoRouteDepth > 1 {
			return fmt.Errorf(
				"delegating agent step %q (ID: %s) exceeds the supported nesting depth. Only one nested delegation layer is allowed under an agent route",
				step.GetTitle(),
				step.GetID(),
			)
		}
		for i, route := range routes {
			if route.SubAgentStep == nil {
				continue
			}
			if err := validateOrchestratorNestingDepth(route.SubAgentStep, todoRouteDepth+1); err != nil {
				return fmt.Errorf("predefined_route[%d] (route_id: %s): %w", i, route.RouteID, err)
			}
		}
	}
	return nil
}

// createSingleStepAdder is a shared executor that handles adding a single step to the plan
// stepType is used for logging and validation purposes
func createSingleStepAdder(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, moveFile func(context.Context, string, string) error, stepType string) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		// Ensure step has type field based on stepType parameter
		args["type"] = stepType

		// Convert map to typed step
		typedStep, err := convertMapToStep(args)
		if err != nil {
			return "", fmt.Errorf("failed to parse step: %w", err)
		}

		// Validate step has ID
		if typedStep.GetID() == "" {
			return "", fmt.Errorf("step is missing required ID field. Step title: %q", typedStep.GetTitle())
		}

		// Validation schema is LLM-generated only - no code-based auto-generation

		// Validate regex patterns in validation schema before proceeding
		if validationSchema := typedStep.GetValidationSchema(); validationSchema != nil {
			if err := validateRegexPatternsInSchema(validationSchema); err != nil {
				return "", fmt.Errorf("invalid regex patterns in validation schema: %w", err)
			}

			// Validate JSONPath syntax
			if err := validateJSONPathSyntax(validationSchema); err != nil {
				return "", fmt.Errorf("invalid JSONPath syntax in validation schema: %w", err)
			}

			// Validate array_length consistency checks
			if err := validateArrayLengthConsistencyChecks(validationSchema); err != nil {
				return "", fmt.Errorf("invalid array_length consistency checks in validation schema: %w", err)
			}

			// Validate value_type/pattern compatibility
			if err := validateValueTypePatternCompatibility(validationSchema); err != nil {
				return "", fmt.Errorf("invalid validation schema: %w", err)
			}
		}

		// Validate step type-specific required fields BEFORE writing to plan
		// This allows the agent to correct errors immediately via tool response
		switch stepType {
		case "regular":
			if scriptedStep, ok := typedStep.(*RegularPlanStep); ok {
				if err := validateScriptParameterContract(&scriptedStep.CommonStepFields); err != nil {
					return "", fmt.Errorf("validation failed: invalid script_parameters: %w", err)
				}
			}
		case "orchestrator", "todo_task":
			if orchestratorStep, ok := typedStep.(*OrchestratorPlanStep); ok {
				if err := validateOrchestratorStepFieldsTyped(orchestratorStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		case "routing":
			if routingStep, ok := typedStep.(*RoutingPlanStep); ok {
				if err := validateRoutingStepFieldsTyped(routingStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		case "branch":
			if branchStep, ok := typedStep.(*BranchPlanStep); ok {
				if err := validateBranchStepFieldsTyped(branchStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		case "crew":
			if crewStep, ok := typedStep.(*CrewPlanStep); ok {
				if err := validateCrewStepFieldsTyped(crewStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		case "human_input":
			// New decision-type human_input steps are branch steps with
			// route_source "human" now; human_input keeps only free-form
			// value capture. Existing steps are untouched (add-time only).
			if humanStep, ok := typedStep.(*HumanInputPlanStep); ok {
				if err := validateNewHumanInputStepIsTextOnly(humanStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		case "message_sequence":
			if sequenceStep, ok := typedStep.(*MessageSequencePlanStep); ok {
				if err := validateMessageSequenceStepFieldsTyped(sequenceStep); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		}

		// Read current plan
		oldPlan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// PLAT-294: at most one routing step per plan. Checked only here, on
		// the mutation that would introduce a second one -- existing plans
		// with extra routing steps are left alone (see routing_step_cardinality.go).
		if stepType == "routing" {
			if err := validateSingleRoutingStepForMutation(oldPlan, typedStep.GetID()); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}
			if routingStep, ok := typedStep.(*RoutingPlanStep); ok {
				if err := validateRoutingRoutesStartSubWorkflows(routingStep.GetID(), routingStep.Routes); err != nil {
					return "", fmt.Errorf("validation failed: %w", err)
				}
			}
		}

		// Check if this is an orphan step
		isOrphan := false
		if orphanVal, ok := args["is_orphan"].(bool); ok {
			isOrphan = orphanVal
		}

		var newPlan *PlanningResponse
		if isOrphan {
			// Orphan step: append to OrphanSteps array (no insertion point needed)
			newOrphanSteps := make([]PlanStepInterface, len(oldPlan.OrphanSteps)+1)
			copy(newOrphanSteps, oldPlan.OrphanSteps)
			newOrphanSteps[len(oldPlan.OrphanSteps)] = typedStep
			newPlan = &PlanningResponse{Steps: oldPlan.Steps, OrphanSteps: newOrphanSteps}
		} else {
			// Normal step: find insertion point and insert into Steps array
			var insertAfterStepID string
			if insertID, ok := args["insert_after_step_id"].(string); ok {
				insertAfterStepID = insertID
			} else {
				return "", fmt.Errorf(fmt.Sprintf("missing required insert_after_step_id field"), nil)
			}

			// Create map of step IDs to indices
			idToIndex := make(map[string]int)
			for i, s := range oldPlan.Steps {
				idToIndex[s.GetID()] = i
			}

			var afterIndex int
			var found bool

			if insertAfterStepID == "" {
				// Insert at beginning
				afterIndex = -1
			} else {
				// Find the step to insert after
				afterIndex, found = idToIndex[insertAfterStepID]
				if !found {
					availableIDs := make([]string, 0, len(oldPlan.Steps))
					for _, s := range oldPlan.Steps {
						availableIDs = append(availableIDs, s.GetID())
					}
					return "", fmt.Errorf("step ID '%s' not found in existing plan (cannot insert after it). Available step IDs: %v", insertAfterStepID, availableIDs)
				}
			}

			// Build new plan with insertion
			newPlanSteps := make([]PlanStepInterface, 0, len(oldPlan.Steps)+1)

			// Insert at beginning if needed
			if afterIndex == -1 {
				newPlanSteps = append(newPlanSteps, typedStep)
			}

			// Add existing steps and insert new step at the right position
			for i, originalStep := range oldPlan.Steps {
				newPlanSteps = append(newPlanSteps, originalStep)
				if i == afterIndex {
					newPlanSteps = append(newPlanSteps, typedStep)
				}
			}

			newPlan = &PlanningResponse{Steps: newPlanSteps, OrphanSteps: oldPlan.OrphanSteps}
		}

		// Validate all steps including the new one
		if err := validatePlanStepIDs(newPlan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed before writing: %w", err)
		}
		if err := validateStepIDUniqueness(newPlan); err != nil {
			return "", fmt.Errorf("plan validation failed before writing: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, newPlan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		scriptedRegularCount, err := configureNewRegularStepsAsScripted(ctx, workspacePath, typedStep, readFile, writeFile)
		if err != nil {
			return "", fmt.Errorf("step was added but required scripted configuration for its regular execution boundary could not be saved: %w", err)
		}

		var addedStepJSON []json.RawMessage
		if rawAdded, marshalErr := json.Marshal(typedStep); marshalErr == nil {
			addedStepJSON = []json.RawMessage{rawAdded}
		}
		changelogTool := fmt.Sprintf("add_%s_step", stepType)
		displayStepType := stepType
		if stepType == string(StepTypeRegular) {
			changelogTool = "add_scripted_step"
			displayStepType = "scripted"
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:       changelogTool,
			Reason:     reason,
			StepIDs:    []string{typedStep.GetID()},
			AddedSteps: addedStepJSON,
		}, readFile, writeFile, logger)

		setupNotice := buildAddedStepArtifactSetupNotice(typedStep.GetID(), stepType)
		if scriptedRegularCount > 0 {
			setupNotice += fmt.Sprintf("\n\nConfigured %d new scripted execution boundary/boundaries (regular plan type, code execution on). Author and test each code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py) before production use.", scriptedRegularCount)
		}
		if stepType == "message_sequence" || stepType == "orchestrator" || stepType == "todo_task" {
			planMedian := planMedianOtherStepDescriptionLen(oldPlan.Steps, typedStep.GetID())
			setupNotice += stepDescriptionSizeNudge(0, len(typedStep.GetDescription()), planMedian)
		}

		logger.Info(fmt.Sprintf("✅ Added %s step '%s' (ID: %s) to plan", displayStepType, typedStep.GetTitle(), typedStep.GetID()))
		return fmt.Sprintf("Successfully added %s step '%s' (ID: %s) to the plan%s", displayStepType, typedStep.GetTitle(), typedStep.GetID(), setupNotice), nil
	}
}

// createCreatePlanExecutor returns an executor for the create_plan tool, which
// initializes an empty planning/plan.json for workflows that don't have one yet.
// Fails if plan.json already exists so we never silently clobber a live plan.
func createCreatePlanExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		planPath := normalizePathForWorkspaceAPI(filepath.Join("planning", "plan.json"), workspacePath)

		planFileMutex.Lock()
		defer planFileMutex.Unlock()

		if existing, err := readFile(ctx, planPath); err == nil && strings.TrimSpace(existing) != "" {
			return "", fmt.Errorf("plan.json already exists at %s — use add_scripted_step / update_* / delete_plan_steps to modify it instead of recreating it", planPath)
		}

		emptyPlan := &PlanningResponse{}
		data, err := json.MarshalIndent(emptyPlan, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal empty plan: %w", err)
		}

		if err := writeFile(ctx, planPath, string(data)); err != nil {
			return "", fmt.Errorf("failed to write plan.json: %w", err)
		}

		logger.Info(fmt.Sprintf("🆕 Created new empty plan.json at %s", planPath))
		return fmt.Sprintf(`Created empty plan.json at %s. For the first step, pass insert_after_step_id="" to insert at the beginning.

Before adding it, decide the step type by who decides the work, not by habit:
- add_message_sequence_step is the DEFAULT for conversational/judgment work, including one-turn work. Default to one large message_sequence per shared-context span; split only for a concrete boundary (distinct durable output, independent retry/failure domain, different credentials/tools, a downstream consumer needing the intermediate artifact, or a human checkpoint) — not because there are several known sub-tasks or many tool calls.
- add_scripted_step only for deterministic, no-judgment work (fixed API/CLI/data fetch, parsing, mechanical writes). A known list of actions sharing one context is still ONE message_sequence, not one scripted/agentic step per action.
- add optional predefined_routes to the message_sequence only when the agent must interpret runtime evidence and adaptively choose specialist work — a fixed child set/order does not qualify. Do not create a new orchestrator/todo_task compatibility step.
- add_routing_step / add_branch_step only for a real fixed branch choice (routing: one major sub-workflow fork per plan; branch: a small in-flow decision).
- add_human_input_step only for a free-form value the workflow cannot otherwise obtain.

For the full decision playbook and anti-patterns, call read_skill(skills=[{"name":"builder-reference","path":"references/plan-design.md"}]) before adding the first step.`, planPath), nil
	}
}

// registerPlanModificationTools registers all plan modification tools (plan update tools only)
// Note: human_feedback is NOT registered here because it's already included in WorkspaceTools
// This shared function is used by planning agent, code exec debugging agent, etc.
func registerNativePlanModificationTools(
	mcpAgent DefinitionToolRegistrar,
	workspacePath string,
	logger loggerv2.Logger,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
	moveFile func(context.Context, string, string) error,
	agentName string, // e.g., "planning agent" or "plan improvement agent"
) error {
	mcpAgent = planChangeOriginRegistrar{DefinitionToolRegistrar: mcpAgent, agentName: agentName}
	rawWriteFile := writeFile
	writeFile = withPlanMutationWriteAccess(workspacePath, writeFile)

	// Note: human_feedback is already registered via WorkspaceTools (created by createCustomTools in server.go)
	// No need to register it again here to avoid duplicate registration errors

	// Register create_plan first — it's the only way to initialize planning/plan.json for a new
	// workflow so that the add_* tools (which require an existing plan) can run.
	createPlanSchema := getCreatePlanSchema()
	createPlanParams, err := parseSchemaForToolParameters(createPlanSchema)
	if err != nil {
		return fmt.Errorf("failed to parse create plan schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"create_plan",
		"Initialize an empty planning/plan.json for a new workflow. Call this FIRST when the workflow has no plan.json yet, before using add_scripted_step / add_message_sequence_step / add_human_input_step / add_todo_task_step / add_routing_step to populate it. Refuses to overwrite an existing plan.json. Takes no arguments. Note: workflow objective lives in soul/soul.md — edit that file separately; plan.json no longer stores it.",
		createPlanParams,
		createCreatePlanExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register create_plan tool: %w", err)
	}

	if err := mcpAgent.RegisterCustomTool(
		"validate_plan_change",
		"Validate the concrete invariants of a completed plan change without running the workflow. The agent remains responsible for deciding the intended design; this tool proves the declared result against the persisted plan and its known dependent artifacts. Supply every removed step id or obsolete path as forbidden_references, and the exact context_dependencies expected for each changed consumer. A structural decision must not be consumed unless this returns passed=true.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"forbidden_references": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Exact removed step ids, obsolete artifact path prefixes, or retired field names that must no longer occur in current workflow artifacts.",
				},
				"expected_context_dependencies": map[string]interface{}{
					"type":                 "object",
					"additionalProperties": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"description":          "Map of changed step id to its exact expected context_dependencies array. Extra or missing dependencies fail validation.",
				},
			},
			"anyOf": []interface{}{
				map[string]interface{}{"required": []interface{}{"forbidden_references"}},
				map[string]interface{}{"required": []interface{}{"expected_context_dependencies"}},
			},
		},
		createValidatePlanChangeExecutor(workspacePath, readFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register validate_plan_change tool: %w", err)
	}

	migrateSequenceCodeParams, err := parseSchemaForToolParameters(`{"type":"object","properties":{}}`)
	if err != nil {
		return fmt.Errorf("failed to parse migrate_message_sequence_code_items schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"migrate_message_sequence_code_items",
		"Product-managed workflow-version migration for issue #170. Converts only unambiguous top-level message_sequence steps containing code + prevalidation items into visible standalone scripted regular steps. It copies scripts to code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py), preserves durable dependencies/outputs/validation, and updates plan/config with rollback on a config-write failure. Mixed conversational/code or nested sequences are rejected without changing plan/config. Call only during the v1.0.10 workflow preflight.",
		migrateSequenceCodeParams,
		createMigrateMessageSequenceCodeItemsExecutor(workspacePath, logger, readFile, rawWriteFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register migrate_message_sequence_code_items tool: %w", err)
	}

	migrateOrchestratorTypeParams, err := parseSchemaForToolParameters(`{"type":"object","properties":{}}`)
	if err != nil {
		return fmt.Errorf("failed to parse migrate_orchestrator_step_type schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"migrate_orchestrator_step_type",
		"Product-managed workflow-version migration for contract v1.0.35. Rewrites every legacy `\"type\": \"todo_task\"` step discriminator in planning/plan.json to `orchestrator`, validates the plan, and records the change. Behavior is unchanged: the runtime reads both names. Idempotent. Call only during the v1.0.35 workflow preflight.",
		migrateOrchestratorTypeParams,
		createMigrateOrchestratorStepTypeExecutor(workspacePath, logger, readFile, rawWriteFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register migrate_orchestrator_step_type tool: %w", err)
	}

	migrateDeclaredModeParams, err := parseSchemaForToolParameters(`{"type":"object","properties":{}}`)
	if err != nil {
		return fmt.Errorf("failed to parse migrate_declared_execution_mode schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"migrate_declared_execution_mode",
		"Product-managed workflow-version migration for contract v1.0.38 (PLAT-287, half 1). Rewrites planning/plan.json so every step's type states its execution model explicitly: each legacy agentic regular step (no declared scripted mode) becomes the message_sequence the runtime already ran it as, and any message_sequence still declared scripted becomes regular -- same id, description, dependencies, validation, next_step_id, position. Touches only plan.json: step_config.json is read, never written, and declared_execution_mode stays in place because the current runtime still reads it. Behavior is unchanged. Idempotent. Refuses without changing anything when a step is declared scripted but has no code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py). Call only during the v1.0.38 workflow preflight.",
		migrateDeclaredModeParams,
		createMigrateDeclaredExecutionModeExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register migrate_declared_execution_mode tool: %w", err)
	}

	stripDeclaredModeParams, err := parseSchemaForToolParameters(`{"type":"object","properties":{}}`)
	if err != nil {
		return fmt.Errorf("failed to parse strip_declared_execution_mode schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"strip_declared_execution_mode",
		"Product-managed workflow-version migration for contract v1.0.39 (PLAT-287, half 2). The plan type alone now decides how a step runs -- regular is scripted, message_sequence is conversational -- so the retired declared_execution_mode / declared_execution_mode_reason keys are removed from planning/step_config.json. Evaluation files are retired and left untouched. Every removed reason is preserved in the planning/changelog entry. Refuses, without changing anything, while any regular step still carries declared_execution_mode=\"agentic\" (run migrate_declared_execution_mode, the v1.0.38 step, first). Idempotent.",
		stripDeclaredModeParams,
		createStripDeclaredExecutionModeExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register strip_declared_execution_mode tool: %w", err)
	}

	// Register workflow-specific plan update tools with "workflow" category
	// Individual update tools for each step type
	regularUpdateSchema := getUpdateRegularStepSchema()
	regularUpdateParams, err := parseSchemaForToolParameters(regularUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update scripted step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_scripted_step",
		"Update an existing deterministic scripted step. The internal plan type remains regular, but this tool only edits a checked-in script boundary implemented by code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py). Provide existing_step_id and only the contract fields to change. Use next_step_id to chain scripted steps inside a selected route and make the final script converge on a shared downstream step. Do not use it for conversational or judgment-heavy work; those steps must be message_sequence. A message_sequence step is rejected: convert it first with change_step_type(target_type=\"scripted\"), or edit it as a sequence with update_message_sequence_step. A regular step is scripted by its plan type alone (PLAT-287); a regular step still carrying the retired declared_execution_mode=\"agentic\" runs as a sequence until the v1.0.38 migration converts it, and is likewise rejected here. The plan is updated immediately. After related edits, update and test main.py and check affected validation, learnings, and downstream consumers once in the current agent; follow builder-reference/references/plan-change-impact.md. A full drift audit is reserved for Pulse or an explicit user request.",
		regularUpdateParams,
		createUpdateRegularStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_scripted_step tool: %w", err)
	}

	changeStepTypeParams, err := parseSchemaForToolParameters(getChangeStepTypeSchema())
	if err != nil {
		return fmt.Errorf("failed to parse change_step_type schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"change_step_type",
		"Convert a step between the two execution models in place, keeping its id, description, dependencies, validation_schema, next_step_id and position (nested and orphan steps included). target_type=\"scripted\" turns a message_sequence into a deterministic scripted step (internal plan type regular) and declares scripted mode in step_config in the same atomic change -- its conversational items are dropped, since a scripted step's work lives in code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py), which you then write with update_scripted_step(code=...). target_type=\"message_sequence\" turns a scripted step into a sequence with one execute-and-verify item and clears the declared mode; refine the turns with update_message_sequence_step. Use it instead of add_scripted_step + delete_plan_steps + rewiring. Records a revertable before/after entry in planning/changelog. Only scripted <-> message_sequence; other step types have their own tools.",
		changeStepTypeParams,
		createChangeStepTypeExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register change_step_type tool: %w", err)
	}

	// NOTE: update_conditional_step tool removed (deprecated in favor of decision/routing).

	// NOTE: update_orchestration_step tool removed (deprecated in favor of todo_task).

	humanInputUpdateSchema := getUpdateHumanInputStepSchema()
	humanInputUpdateParams, err := parseSchemaForToolParameters(humanInputUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update human input step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_human_input_step",
		"Update a human input step in the plan. Provide existing_step_id (required) to identify which human input step to update, and only include the fields you want to change (question, response_type, options, variable_name, context_output, next_step_id, if_yes_next_step_id/if_no_next_step_id, option_routes). The plan.json file is updated immediately when this tool is called. After related edits, follow builder-reference/references/plan-change-impact.md: do one combined compatibility check of affected dependencies in the current agent before the targeted test. A full drift audit is reserved for Pulse or an explicit user request.",
		humanInputUpdateParams,
		createUpdateHumanInputStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_human_input_step tool: %w", err)
	}

	deleteSchema := getDeletePlanStepsSchema()
	deleteParams, err := parseSchemaForToolParameters(deleteSchema)
	if err != nil {
		return fmt.Errorf("failed to parse delete schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"delete_plan_steps",
		"Delete steps from the plan by providing their IDs. Use the step's id field from the plan. The mutation is atomic: before anything is saved, the complete plan graph is validated. If another route or next_step_id targets a deleted step, the tool returns PLAN_GRAPH_INVALID with every blocking reference; update those references to an existing step or end, then retry. Any matching planning/step_config.json entries are removed only after the plan deletion succeeds.",
		deleteParams,
		createDeletePlanStepsExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register delete_plan_steps tool: %w", err)
	}

	cleanupOrphanSchema := `{
		"type": "object",
		"properties": {}
	}`
	cleanupOrphanParams, err := parseSchemaForToolParameters(cleanupOrphanSchema)
	if err != nil {
		return fmt.Errorf("failed to parse cleanup_orphan_step_configs schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"cleanup_orphan_step_configs",
		"Sweep planning/step_config.json and remove entries whose step_id no longer exists in plan.json (or plan.OrphanSteps). Use this once when you notice the agent describing orphan step_config entries it can't reach via update_step_config. Idempotent: returns the list of IDs removed (empty if nothing was orphaned).",
		cleanupOrphanParams,
		createCleanupOrphanStepConfigsExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register cleanup_orphan_step_configs tool: %w", err)
	}

	driftReviewSchema := getRecordPlanDriftReviewSchema()
	driftReviewParams, err := parseSchemaForToolParameters(driftReviewSchema)
	if err != nil {
		return fmt.Errorf("failed to parse record_plan_drift_review schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"record_plan_drift_review",
		"Record the results of a plan-drift compatibility pass for one step in planning/step_config.json. Call once per step with every check that actually ran, including checks that passed. Select checks from the specific approved-plan change: prompt and step-type best-practice checks apply only to a first baseline or when those surfaces changed; a title-only or unrelated edit may use a compact compatibility check. General design optimization belongs to Architecture. Each check needs check_id, status (pass/fail/fixed — fixed means real drift was found and repaired in this pass), and evidence describing what was compared and found. Writing this record clears the step from plan_drift_review's due list only when no failed check remains.",
		driftReviewParams,
		createRecordPlanDriftReviewExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register record_plan_drift_review tool: %w", err)
	}

	// Register type-specific step addition tools
	regularSchema := getAddRegularStepSchema()
	regularParams, err := parseSchemaForToolParameters(regularSchema)
	if err != nil {
		return fmt.Errorf("failed to parse scripted step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_scripted_step",
		"Add a deterministic scripted execution step. Use only for fixed API/SDK calls, CLI commands, known pagination, stable parsing/normalization/transforms, or mechanical persistence that share one source/auth/retry/output contract. The internal plan type is regular, and a regular step is by definition scripted (PLAT-287): it runs its checked-in code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py). Use next_step_id to chain multiple scripts within one selected route and point the final script at the shared convergence step; omit it for legacy sequential execution. This tool does not create an LLM step and does not convert prose into code: author and test code/<step-id>/main.py (code_layout_version=1; legacy: learnings/<step-id>/main.py) before production. Use add_message_sequence_step for every conversational or judgment-heavy task, including one-turn work. Give the script an authoritative DB or explicit file output, freshness/provenance, fail-closed errors, idempotency where relevant, and deterministic validation. The plan and step config are updated immediately.",
		regularParams,
		createAddRegularStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_scripted_step tool: %w", err)
	}

	messageSequenceSchema := getAddMessageSequenceStepSchema()
	messageSequenceParams, err := parseSchemaForToolParameters(messageSequenceSchema)
	if err != nil {
		return fmt.Errorf("failed to parse message sequence step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_message_sequence_step",
		"Add an agent step (plan type message_sequence): one persistent agent conversation with ordered user turns, optional deterministic scripted batches, and optional predefined_routes for bounded specialist delegation. When routes are present, this agent decides at runtime whether, when, and how often to call them; the routes are capabilities, not a prescribed checklist. Put the durable objective, boundaries, and definition of done in the system-level description; put execution and verification instructions in items[]. Fixed API/SDK/CLI work belongs in scripted steps or scripted items. The top-level validation_schema is enforced with same-conversation repair retries. Plain turns inherit step-level DB/KB/learnings permissions; kind or non-empty write_access can narrow one turn.",
		messageSequenceParams,
		createAddMessageSequenceStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_message_sequence_step tool: %w", err)
	}

	// NOTE: add_orchestration_step tool removed (deprecated in favor of todo_task).
	// Schema, executor, and execution code kept for backward compatibility with existing workflows.

	humanInputSchema := getAddHumanInputStepSchema()
	humanInputParams, err := parseSchemaForToolParameters(humanInputSchema)
	if err != nil {
		return fmt.Errorf("failed to parse human input step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_human_input_step",
		"Add a human input step to the plan to capture a FREE-FORM VALUE from a person (an ID, a month, a note to format) into variable_name, blocking until they respond. New steps accept response_type=text only: a person's DECISION between fixed options (approve/hold, yes/no, pick one) is a branch step with route_source=\"human\" (add_branch_step) -- the routes are the options and schedules answer it via route_selections -- so yesno and multiple_choice are rejected here; existing human_input steps of every type keep working. This step has no LLM, no execution, no validation, and no learning. The response is saved to a JSON file and passed to the next step. Provide: id, title, question (required), response_type (text), options (legacy, multiple_choice only), variable_name (optional), context_output (optional, defaults to step-{index}.json), next_step_id (required), if_yes_next_step_id/if_no_next_step_id (for yesno), option_routes (for multiple_choice), insert_after_step_id. The plan.json file is updated immediately when this tool is called.",
		humanInputParams,
		createAddHumanInputStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_human_input_step tool: %w", err)
	}

	orchestratorSchema := getAddOrchestratorStepSchema()
	orchestratorParams, err := parseSchemaForToolParameters(orchestratorSchema)
	if err != nil {
		return fmt.Errorf("failed to parse todo task step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_orchestrator_step",
		"Legacy compatibility tool for persisted orchestrator plans. Do not use it for new work: create a message_sequence with optional predefined_routes instead. The unified agent uses description as its system charter and items as user turns. Use this tool only for a compatibility operation whose live schema specifically requires the legacy orchestrator shape.",
		orchestratorParams,
		createAddOrchestratorStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_orchestrator_step tool: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_todo_task_step",
		"Deprecated alias of add_orchestrator_step kept for one contract version; call add_orchestrator_step instead. Same parameters and behavior.",
		orchestratorParams,
		createAddOrchestratorStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_todo_task_step alias: %w", err)
	}

	routingSchema := getAddRoutingStepSchema()
	routingParams, err := parseSchemaForToolParameters(routingSchema)
	if err != nil {
		return fmt.Errorf("failed to parse routing step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_routing_step",
		"Add a deterministic routing step to the plan. Routing is the workflow's mode selector -- the single major fork whose route a schedule or caller picks via route_selections -- so a plan has AT MOST ONE routing step; this tool rejects a second one. Each route is a sub-workflow of the main work (many steps), so every route must start at a real step -- a route straight to end is rejected, because a mode that does nothing is a simple if-condition. Every other fixed choice inside the flow (skip/continue, probe ok/failed, approve/hold) is a branch step (add_branch_step). Use this when the workflow must choose exactly one of multiple existing downstream sub-workflows. Routing has one mode only: read caller route_selections, the routing step's preseeded route_selection.json, route_source_file, a context_dependencies entry named route_selection.json, or default_route_id, then switch to the selected route. Do not set description or context_output on routing steps; if an agent/probe/judgment is needed, add a prior message_sequence step that writes route_selection.json and declare that file in the routing step's route_source_file or context_dependencies. Provide: id, title, routing_question (readability/compatibility only), routes (min 2 with route_id/route_name/condition/next_step_id), context_dependencies, optional default_route_id, optional route_source_file, insert_after_step_id. The plan.json file is updated immediately when this tool is called.",
		routingParams,
		createAddRoutingStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_routing_step tool: %w", err)
	}

	routingUpdateSchema := getUpdateRoutingStepSchema()
	routingUpdateParams, err := parseSchemaForToolParameters(routingUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update routing step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_routing_step",
		"Update a deterministic routing step in the plan. Provide existing_step_id (required) and only include fields you want to change: title, routing_question, routes, default_route_id, route_source_file, context_dependencies, or clear_description=true for legacy migration. Do not set description or context_output; routing never executes an agent and never LLM-evaluates routing_question. The selected route must come from caller route_selections, route_selection.json, route_source_file, context_dependencies, or default_route_id. The plan.json file is updated immediately when this tool is called. After related edits, follow builder-reference/references/plan-change-impact.md: do one combined compatibility check of affected dependencies in the current agent before the targeted test. A full drift audit is reserved for Pulse or an explicit user request.",
		routingUpdateParams,
		createUpdateRoutingStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_routing_step tool: %w", err)
	}

	branchSchema := getAddBranchStepSchema()
	branchParams, err := parseSchemaForToolParameters(branchSchema)
	if err != nil {
		return fmt.Errorf("failed to parse branch step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_branch_step",
		"Add a deterministic branch step to the plan. Use this for a small in-flow next-step decision -- the workflow must choose exactly one of a few existing downstream steps. (Routing steps now represent a larger, major sub-workflow fork; use branch for a lightweight decision instead.) When a PERSON makes the decision mid-run (approve/hold, yes/no, pick one), set route_source=\"human\": the routes are the options, branch_question is the prompt, schedules answer it up front via route_selections, unattended runs use default_route_id, interactive runs ask -- this replaces yesno/multiple_choice human_input steps. Otherwise branch has one mode only: read caller route_selections, the branch step's preseeded route_selection.json, route_source_file, a context_dependencies entry named route_selection.json, or default_route_id, then switch to the selected route. Do not set description or context_output on branch steps; if an agent/probe/judgment is needed, add a prior message_sequence step that writes route_selection.json and declare that file in the branch step's route_source_file or context_dependencies. Provide: id, title, branch_question (readability/compatibility only), routes (min 2 with route_id/route_name/condition/next_step_id), context_dependencies, optional default_route_id, optional route_source_file, insert_after_step_id. The plan.json file is updated immediately when this tool is called.",
		branchParams,
		createAddBranchStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_branch_step tool: %w", err)
	}

	branchUpdateSchema := getUpdateBranchStepSchema()
	branchUpdateParams, err := parseSchemaForToolParameters(branchUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update branch step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_branch_step",
		"Update a deterministic branch step in the plan. Provide existing_step_id (required) and only include fields you want to change: title, branch_question, routes, default_route_id, route_source_file, context_dependencies, or clear_description=true for legacy migration. Do not set description or context_output; branch never executes an agent and never LLM-evaluates branch_question. The selected route must come from caller route_selections, route_selection.json, route_source_file, context_dependencies, or default_route_id. The plan.json file is updated immediately when this tool is called. After related edits, follow builder-reference/references/plan-change-impact.md: do one combined compatibility check of affected dependencies in the current agent before the targeted test. A full drift audit is reserved for Pulse or an explicit user request.",
		branchUpdateParams,
		createUpdateBranchStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_branch_step tool: %w", err)
	}

	crewSchema := getAddCrewStepSchema()
	crewParams, err := parseSchemaForToolParameters(crewSchema)
	if err != nil {
		return fmt.Errorf("failed to parse crew step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_crew_step",
		"Add a crew step to the plan. Use this when the work belongs to a persistent Crew project -- a maintained reviewer, operator, or specialist with its own memory, skills, and files -- instead of a one-off workflow agent. The step invokes one trigger of that Crew and waits for its final response, which is saved as the step's context output for downstream steps. The trigger owns its saved base instruction and conversation destination; the step supplies the workflow-specific instruction and runtime input. Provide: id, title, crew_profile_id (initially \"work\"), crew_project_id (stable project ID, never a name or path), trigger_id, instruction (rendered with variables at run time), context_dependencies (workflow outputs sent as trigger input), optional context_output (defaults to response.md), optional timeout_seconds (defaults to 1800), optional next_step_id, insert_after_step_id. The plan.json file is updated immediately when this tool is called.",
		crewParams,
		createAddCrewStepExecutor(workspacePath, logger, readFile, writeFile, moveFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_crew_step tool: %w", err)
	}

	crewUpdateSchema := getUpdateCrewStepSchema()
	crewUpdateParams, err := parseSchemaForToolParameters(crewUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update crew step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_crew_step",
		"Update a crew step in the plan. Provide existing_step_id (required) and only include fields you want to change: title, description, crew_profile_id, crew_project_id, trigger_id, instruction, context_dependencies, context_output, timeout_seconds, next_step_id. The plan.json file is updated immediately when this tool is called. After related edits, follow builder-reference/references/plan-change-impact.md: do one combined compatibility check of affected dependencies in the current agent before the targeted test. A full drift audit is reserved for Pulse or an explicit user request.",
		crewUpdateParams,
		createUpdateCrewStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_crew_step tool: %w", err)
	}

	convertRoutingBranchSchema := getConvertRoutingBranchStepTypeSchema()
	convertRoutingBranchParams, err := parseSchemaForToolParameters(convertRoutingBranchSchema)
	if err != nil {
		return fmt.Errorf("failed to parse convert_routing_branch_step_type schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"convert_routing_branch_step_type",
		"Reclassify an existing routing step as branch, or an existing branch step as routing, in place. Both types share the exact same deterministic-switch shape (routes/default_route_id/route_source_file; only the human-readable question field's name differs) — this only relabels the step, it never deletes and recreates it, so the step's id is unchanged and its planning/step_config.json row (drift_review, execution_tier, etc.) stays continuous. Use this instead of manually deleting and re-adding a step to change its type — that approach loses step_config.json history for the deleted id. Provide existing_step_id, target_type (\"routing\" or \"branch\", must differ from the step's current type), and a one-sentence reason recorded in the plan changelog. The plan.json file is updated immediately when this tool is called.",
		convertRoutingBranchParams,
		createConvertRoutingBranchStepTypeExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register convert_routing_branch_step_type tool: %w", err)
	}

	messageSequenceUpdateSchema := getUpdateMessageSequenceStepSchema()
	messageSequenceUpdateParams, err := parseSchemaForToolParameters(messageSequenceUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update_message_sequence_step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_message_sequence_step",
		"Update a message_sequence step in the plan. This also accepts a persisted legacy non-scripted regular step and atomically upgrades it to message_sequence, matching the compatibility runtime agents already see; declared scripted regular steps still require update_scripted_step. Provide existing_step_id and only the fields to change. Replacing items changes the configured queue; an existing runtime session will still resume unless explicitly restarted by execution controls. After related edits, follow builder-reference/references/plan-change-impact.md: do one combined compatibility check of affected dependencies in the current agent before the targeted test. A full drift audit is reserved for Pulse or an explicit user request.",
		messageSequenceUpdateParams,
		createUpdateMessageSequenceStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_message_sequence_step tool: %w", err)
	}

	// NOTE: add/update/delete_orchestration_route tools removed (deprecated in favor of todo_task).

	// Register todo task step update tool
	orchestratorUpdateSchema := getUpdateOrchestratorStepSchema()
	orchestratorUpdateParams, err := parseSchemaForToolParameters(orchestratorUpdateSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update_todo_task_step schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_orchestrator_step",
		"Legacy compatibility tool for updating an existing orchestrator/todo_task record. Do not create new work in this shape; the canonical agent is message_sequence with optional predefined_routes. Preserve the saved description as the system charter and legacy messages as user items. After edits, follow builder-reference/references/plan-change-impact.md and validate affected dependencies.",
		orchestratorUpdateParams,
		createUpdateOrchestratorStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_orchestrator_step tool: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_todo_task_step",
		"Deprecated alias of update_orchestrator_step kept for one contract version; call update_orchestrator_step instead. Same parameters and behavior.",
		orchestratorUpdateParams,
		createUpdateOrchestratorStepExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_todo_task_step alias: %w", err)
	}

	// Register todo task route management tools
	addOrchestratorRouteSchema := getAddOrchestratorRouteSchema()
	addOrchestratorRouteParams, err := parseSchemaForToolParameters(addOrchestratorRouteSchema)
	if err != nil {
		return fmt.Errorf("failed to parse add_todo_task_route schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_orchestrator_route",
		"Add a new predefined route (sub-agent) to an Orchestrator step (orchestrator type; todo_task is the legacy alias). New conversational routes must use sub_agent_step.type=message_sequence, even for one turn. Use regular only for a deterministic scripted boundary; it is automatically configured as scripted. Provide parent_step_id and new_route with route_id, route_name, and condition, plus either sub_agent_step or orphan_step_ref. The plan.json file is updated immediately.",
		addOrchestratorRouteParams,
		createAddOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_orchestrator_route tool: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"add_todo_task_route",
		"Deprecated alias of add_orchestrator_route kept for one contract version; call add_orchestrator_route instead. Same parameters and behavior.",
		addOrchestratorRouteParams,
		createAddOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register add_todo_task_route alias: %w", err)
	}

	updateOrchestratorRouteSchema := getUpdateOrchestratorRouteSchema()
	updateOrchestratorRouteParams, err := parseSchemaForToolParameters(updateOrchestratorRouteSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update_todo_task_route schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_orchestrator_route",
		"Update an existing predefined route (sub-agent) within an Orchestrator step (orchestrator type; todo_task is the legacy alias). Conversational route definitions use message_sequence; regular is reserved for deterministic scripted work. Provide parent_step_id, existing_route_id, and only the fields to change. Use orphan_step_ref for a reusable orphan step. The plan.json file is updated immediately.",
		updateOrchestratorRouteParams,
		createUpdateOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_orchestrator_route tool: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_todo_task_route",
		"Deprecated alias of update_orchestrator_route kept for one contract version; call update_orchestrator_route instead. Same parameters and behavior.",
		updateOrchestratorRouteParams,
		createUpdateOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_todo_task_route alias: %w", err)
	}

	deleteOrchestratorRouteSchema := getDeleteOrchestratorRouteSchema()
	deleteOrchestratorRouteParams, err := parseSchemaForToolParameters(deleteOrchestratorRouteSchema)
	if err != nil {
		return fmt.Errorf("failed to parse delete_todo_task_route schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"delete_orchestrator_route",
		"Delete a predefined route (sub-agent) from an Orchestrator step (orchestrator type; todo_task is the legacy alias). Provide parent_step_id and deleted_route_id. Unlike routing steps, Orchestrator steps may have 0 predefined routes (generic-agent-only). The plan.json file is updated immediately when this tool is called.",
		deleteOrchestratorRouteParams,
		createDeleteOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register delete_orchestrator_route tool: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"delete_todo_task_route",
		"Deprecated alias of delete_orchestrator_route kept for one contract version; call delete_orchestrator_route instead. Same parameters and behavior.",
		deleteOrchestratorRouteParams,
		createDeleteOrchestratorRouteExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register delete_todo_task_route alias: %w", err)
	}

	// Register validation schema update tool
	updateValidationSchemaSchema := getUpdateValidationSchemaSchema()
	updateValidationSchemaParams, err := parseSchemaForToolParameters(updateValidationSchemaSchema)
	if err != nil {
		return fmt.Errorf("failed to parse update_validation_schema schema: %w", err)
	}
	if err := mcpAgent.RegisterCustomTool(
		"update_validation_schema",
		"Update the validation schema for an existing step in the plan. Provide existing_step_id (required) and validation_schema (required). The validation schema enables fast code-based pre-validation before LLM validation. The plan.json file is updated immediately when this tool is called.",
		updateValidationSchemaParams,
		createUpdateValidationSchemaExecutor(workspacePath, logger, readFile, writeFile),
		"workflow",
	); err != nil {
		return fmt.Errorf("failed to register update_validation_schema tool: %w", err)
	}

	if logger != nil {
		logger.Info(fmt.Sprintf("✅ Registered all plan modification tools for %s", agentName))
	}

	return nil
}

type planChangeDependencyMismatch struct {
	StepID   string   `json:"step_id"`
	Expected []string `json:"expected"`
	Actual   []string `json:"actual,omitempty"`
	Reason   string   `json:"reason"`
}

func createValidatePlanChangeExecutor(
	workspacePath string,
	readFile func(context.Context, string) (string, error),
) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		forbidden, err := stringSliceToolArg(args["forbidden_references"], "forbidden_references")
		if err != nil {
			return "", err
		}
		expectedDependencies, err := stringSliceMapToolArg(args["expected_context_dependencies"], "expected_context_dependencies")
		if err != nil {
			return "", err
		}
		if len(forbidden) == 0 && len(expectedDependencies) == 0 {
			return "", fmt.Errorf("provide forbidden_references and/or expected_context_dependencies")
		}

		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", err
		}
		if err := ValidatePlanStructure(plan); err != nil {
			return "", err
		}

		steps := make(map[string]PlanStepInterface)
		var collect func([]PlanStepInterface)
		collect = func(items []PlanStepInterface) {
			for _, step := range items {
				if step == nil {
					continue
				}
				steps[step.GetID()] = step
				if todo, ok := step.(*OrchestratorPlanStep); ok {
					for _, route := range todo.PredefinedRoutes {
						if route.SubAgentStep != nil {
							collect([]PlanStepInterface{route.SubAgentStep})
						}
					}
				}
			}
		}
		collect(plan.Steps)
		collect(plan.OrphanSteps)

		mismatches := make([]planChangeDependencyMismatch, 0)
		stepIDs := make([]string, 0, len(expectedDependencies))
		for stepID := range expectedDependencies {
			stepIDs = append(stepIDs, stepID)
		}
		sort.Strings(stepIDs)
		for _, stepID := range stepIDs {
			expected := normalizedStringSet(expectedDependencies[stepID])
			step, ok := steps[stepID]
			if !ok {
				mismatches = append(mismatches, planChangeDependencyMismatch{StepID: stepID, Expected: expected, Reason: "step not found"})
				continue
			}
			actual := normalizedStringSet(step.GetContextDependencies())
			if !planChangeEqualStringSlices(expected, actual) {
				mismatches = append(mismatches, planChangeDependencyMismatch{StepID: stepID, Expected: expected, Actual: actual, Reason: "context_dependencies differ"})
			}
		}

		artifactPaths := []string{
			"planning/plan.json",
			"planning/step_config.json",
			"planning/variables.json",
			"db/reports/index.html",
			"db/README.md",
			"workflow.json",
			"soul/soul.md",
			"learnings/_global/SKILL.md",
			"knowledgebase/_index.json",
		}
		hits := make(map[string][]string)
		scanned := make([]string, 0, len(artifactPaths))
		for _, relativePath := range artifactPaths {
			path := normalizePathForWorkspaceAPI(relativePath, workspacePath)
			content, readErr := readFile(ctx, path)
			if readErr != nil {
				continue
			}
			scanned = append(scanned, relativePath)
			for _, reference := range forbidden {
				if strings.Contains(content, reference) {
					hits[reference] = append(hits[reference], relativePath)
				}
			}
		}

		issues := make([]string, 0, len(hits)+len(mismatches))
		for _, reference := range forbidden {
			if paths := hits[reference]; len(paths) > 0 {
				issues = append(issues, fmt.Sprintf("forbidden reference %q remains in %s", reference, strings.Join(paths, ", ")))
			}
		}
		for _, mismatch := range mismatches {
			issues = append(issues, fmt.Sprintf("step %q: %s", mismatch.StepID, mismatch.Reason))
		}
		result := map[string]interface{}{
			"passed":                        len(issues) == 0,
			"plan_structure":                "passed",
			"scanned_artifacts":             scanned,
			"forbidden_reference_hits":      hits,
			"context_dependency_mismatches": mismatches,
			"issues":                        issues,
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func stringSliceToolArg(raw interface{}, name string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		if typed, typedOK := raw.([]string); typedOK {
			return normalizedStringSet(typed), nil
		}
		return nil, fmt.Errorf("%s must be an array of strings", name)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s must contain only non-empty strings", name)
		}
		values = append(values, value)
	}
	return normalizedStringSet(values), nil
}

func stringSliceMapToolArg(raw interface{}, name string) (map[string][]string, error) {
	if raw == nil {
		return nil, nil
	}
	object, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s must be an object keyed by step id", name)
	}
	values := make(map[string][]string, len(object))
	for rawStepID, rawDependencies := range object {
		stepID := strings.TrimSpace(rawStepID)
		if stepID == "" {
			return nil, fmt.Errorf("%s contains an empty step id", name)
		}
		dependencies, err := stringSliceToolArg(rawDependencies, name+"["+stepID+"]")
		if err != nil {
			return nil, err
		}
		values[stepID] = dependencies
	}
	return values, nil
}

func normalizedStringSet(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func planChangeEqualStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func withPlanMutationWriteAccess(workspacePath string, writeFile func(context.Context, string, string) error) func(context.Context, string, string) error {
	planningPath := normalizePathForWorkspaceAPI("planning", workspacePath)
	return func(ctx context.Context, path, content string) error {
		return writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, planningPath), path, content)
	}
}

func mutableAgentRoutes(step PlanStepInterface) (*[]PlanOrchestrationRoute, string, func() error, bool) {
	switch s := step.(type) {
	case *MessageSequencePlanStep:
		return &s.PredefinedRoutes, s.Title, func() error { return validateMessageSequenceStepFieldsTyped(s) }, true
	case *OrchestratorPlanStep:
		return &s.PredefinedRoutes, s.Title, func() error { return validateOrchestratorStepFieldsTyped(s) }, true
	default:
		return nil, "", nil, false
	}
}

// createAddOrchestratorRouteExecutor creates an executor function for add_todo_task_route tool
func createAddOrchestratorRouteExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		// Accept parent_step_id or legacy alias step_id
		parentStepID, ok := args["parent_step_id"].(string)
		if !ok || parentStepID == "" {
			parentStepID, ok = args["step_id"].(string)
		}
		if !ok || parentStepID == "" {
			return "", fmt.Errorf("invalid or missing parent_step_id")
		}

		// Accept new_route or legacy alias predefined_route; handle JSON-string form
		var newRouteRaw map[string]interface{}
		if v, ok2 := args["new_route"]; ok2 {
			switch val := v.(type) {
			case map[string]interface{}:
				newRouteRaw = val
			case string:
				if err := json.Unmarshal([]byte(val), &newRouteRaw); err != nil {
					return "", fmt.Errorf("failed to parse new_route JSON string: %w", err)
				}
			}
		}
		if newRouteRaw == nil {
			if v, ok2 := args["predefined_route"]; ok2 {
				switch val := v.(type) {
				case map[string]interface{}:
					newRouteRaw = val
				case string:
					if err := json.Unmarshal([]byte(val), &newRouteRaw); err != nil {
						return "", fmt.Errorf("failed to parse predefined_route JSON string: %w", err)
					}
				}
			}
		}
		if newRouteRaw != nil {
			if err := validateWorkflowArtifactMutationValue("step.new_route", newRouteRaw); err != nil {
				return "", err
			}
		}
		if newRouteRaw == nil {
			return "", fmt.Errorf("invalid new_route argument")
		}

		// Convert to JSON and unmarshal to PlanOrchestrationRoute
		newRouteJSON, err := json.Marshal(newRouteRaw)
		if err != nil {
			return "", fmt.Errorf("failed to marshal new_route: %w", err)
		}
		var newRoute PlanOrchestrationRoute
		if err := json.Unmarshal(newRouteJSON, &newRoute); err != nil {
			return "", fmt.Errorf("failed to parse new_route: %w", err)
		}

		// Read current plan
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Find the parent todo task step by ID. Recurses into nested steps
		// (e.g. a todo_task sitting in another todo_task's predefined_routes.sub_agent_step).
		parentStep, _, _ := findStepByID(plan.Steps, parentStepID)
		if parentStep == nil {
			parentStep, _, _ = findStepByID(plan.OrphanSteps, parentStepID)
		}
		if parentStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("parent step ID '%s' not found in existing plan. Available top-level step IDs: %v", parentStepID, availableIDs)
		}

		routes, parentTitle, validateParent, ok := mutableAgentRoutes(parentStep)
		if !ok {
			return "", fmt.Errorf("step with ID '%s' is not an agent step (message_sequence or legacy orchestrator)", parentStepID)
		}

		// Validate that the new route has a route_id
		if newRoute.RouteID == "" {
			return "", fmt.Errorf("new route is missing required route_id field")
		}

		// Check if route_id already exists
		for _, existingRoute := range *routes {
			if existingRoute.RouteID == newRoute.RouteID {
				return "", fmt.Errorf("route with route_id '%s' already exists in todo task step '%s'", newRoute.RouteID, parentStepID)
			}
		}

		// Validate inline sub_agent_step identity when provided.
		if newRoute.OrphanStepRef != "" && newRoute.SubAgentStep != nil {
			return "", fmt.Errorf("new route %q cannot define both orphan_step_ref and sub_agent_step", newRoute.RouteID)
		}
		if newRoute.SubAgentStep != nil {
			if newRoute.SubAgentStep.GetID() != "" && newRoute.SubAgentStep.GetID() != newRoute.RouteID {
				return "", fmt.Errorf("sub_agent_step.id %q must exactly match route_id %q for predefined todo routes", newRoute.SubAgentStep.GetID(), newRoute.RouteID)
			}
			if err := setStepIdentity(newRoute.SubAgentStep, newRoute.RouteID, newRoute.RouteName); err != nil {
				return "", err
			}
		}

		// Add new route
		*routes = append(*routes, newRoute)
		if err := resolvePlanOrphanStepRefs(plan); err != nil {
			return "", fmt.Errorf("failed to resolve orphan step references after adding route: %w", err)
		}
		if err := validateParent(); err != nil {
			return "", fmt.Errorf("validation failed after adding route: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		if _, err := configureNewRegularStepsAsScripted(ctx, workspacePath, newRoute.SubAgentStep, readFile, writeFile); err != nil {
			return "", fmt.Errorf("route was added but required scripted configuration for its regular execution boundary could not be saved: %w", err)
		}

		// Marshal the route as it actually landed in the plan (post orphan-ref
		// resolution), not the pre-resolution local var, so the changelog's
		// after_ref reflects what was really written (PLAT-074).
		var addedRouteJSON []json.RawMessage
		if addedRoute, err := json.Marshal((*routes)[len(*routes)-1]); err == nil {
			addedRouteJSON = []json.RawMessage{addedRoute}
		} else {
			logger.Warn(fmt.Sprintf("⚠️ Failed to marshal added route %s for changelog: %v", newRoute.RouteID, err))
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:       "add_orchestrator_route",
			Reason:     reason,
			StepIDs:    []string{parentStepID, newRoute.RouteID},
			AddedSteps: addedRouteJSON,
		}, readFile, writeFile, logger)

		routeReviewNotice := handleOrchestratorRouteArtifactReview(ctx, workspacePath, parentStepID, newRoute.RouteID, "added", readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Added route '%s' (ID: %s) to agent step '%s'", newRoute.RouteName, newRoute.RouteID, parentTitle))
		return fmt.Sprintf("Successfully added route '%s' (ID: %s) to agent step '%s'%s", newRoute.RouteName, newRoute.RouteID, parentTitle, routeReviewNotice), nil
	}
}

// createUpdateOrchestratorRouteExecutor creates an executor function for update_todo_task_route tool
func createUpdateOrchestratorRouteExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}
		if err := validateWorkflowArtifactMutationArgs(args); err != nil {
			return "", err
		}

		// Accept parent_step_id or legacy alias step_id
		parentStepID, ok := args["parent_step_id"].(string)
		if !ok || parentStepID == "" {
			parentStepID, ok = args["step_id"].(string)
		}
		if !ok || parentStepID == "" {
			return "", fmt.Errorf("invalid or missing parent_step_id")
		}

		existingRouteID, ok := args["existing_route_id"].(string)
		if !ok || existingRouteID == "" {
			return "", fmt.Errorf("invalid or missing existing_route_id")
		}

		// Read current plan
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Find the parent todo task step by ID.
		parentStep, _, _ := findStepByID(plan.Steps, parentStepID)
		if parentStep == nil {
			parentStep, _, _ = findStepByID(plan.OrphanSteps, parentStepID)
		}
		if parentStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("parent step ID '%s' not found in existing plan. Available top-level step IDs: %v", parentStepID, availableIDs)
		}

		routes, parentTitle, validateParent, ok := mutableAgentRoutes(parentStep)
		if !ok {
			return "", fmt.Errorf("step with ID '%s' is not an agent step (message_sequence or legacy orchestrator)", parentStepID)
		}

		// Find the route to update
		var routeToUpdate *PlanOrchestrationRoute
		for i := range *routes {
			if (*routes)[i].RouteID == existingRouteID {
				routeToUpdate = &(*routes)[i]
				break
			}
		}
		if routeToUpdate == nil {
			availableRouteIDs := make([]string, 0, len(*routes))
			for _, route := range *routes {
				availableRouteIDs = append(availableRouteIDs, route.RouteID)
			}
			return "", fmt.Errorf("route with route_id '%s' not found in todo task step '%s'. Available route IDs: %v", existingRouteID, parentStepID, availableRouteIDs)
		}

		// Capture the pre-mutation route content so the changelog can record a
		// real before/after diff instead of collapsing to a placeholder
		// (PLAT-074) — routeToUpdate is a pointer into the live plan, so this
		// must happen before any field is touched below.
		var beforeRouteSnapshot interface{}
		if beforeRouteJSON, err := json.Marshal(*routeToUpdate); err == nil {
			beforeRouteSnapshot = json.RawMessage(beforeRouteJSON)
		} else {
			logger.Warn(fmt.Sprintf("⚠️ Failed to marshal pre-update route %s for changelog: %v", existingRouteID, err))
		}

		// Update fields if provided
		if routeName, ok := args["route_name"].(string); ok && routeName != "" {
			routeToUpdate.RouteName = routeName
		}

		if condition, ok := args["condition"].(string); ok && condition != "" {
			routeToUpdate.Condition = condition
		}

		if orphanStepRef, ok := args["orphan_step_ref"].(string); ok {
			routeToUpdate.OrphanStepRef = strings.TrimSpace(orphanStepRef)
			if routeToUpdate.OrphanStepRef != "" {
				routeToUpdate.SubAgentStep = nil
			}
		}

		// Handle sub_agent_step update
		var newlyRegularSubAgentStep PlanStepInterface
		if subAgentStepRaw, ok := args["sub_agent_step"].(map[string]interface{}); ok {
			if routeToUpdate.OrphanStepRef != "" {
				return "", fmt.Errorf("route %q uses orphan_step_ref %q, so sub_agent_step cannot be updated inline", routeToUpdate.RouteID, routeToUpdate.OrphanStepRef)
			}
			if providedID, ok := subAgentStepRaw["id"].(string); ok && providedID != "" && providedID != routeToUpdate.RouteID {
				return "", fmt.Errorf("sub_agent_step.id %q must exactly match route_id %q for predefined todo routes", providedID, routeToUpdate.RouteID)
			}
			_, existingWasRegular := routeToUpdate.SubAgentStep.(*RegularPlanStep)
			mergedSubAgentStepRaw := map[string]interface{}{}
			if routeToUpdate.SubAgentStep != nil {
				existingJSON, err := json.Marshal(routeToUpdate.SubAgentStep)
				if err != nil {
					return "", fmt.Errorf("failed to serialize existing sub_agent_step: %w", err)
				}
				if err := json.Unmarshal(existingJSON, &mergedSubAgentStepRaw); err != nil {
					return "", fmt.Errorf("failed to parse existing sub_agent_step: %w", err)
				}
			}
			for k, v := range subAgentStepRaw {
				mergedSubAgentStepRaw[k] = v
			}
			// Updates preserve an existing type. A newly introduced inline route must
			// declare its type explicitly so conversational work cannot silently
			// become a legacy regular step.
			if _, hasType := mergedSubAgentStepRaw["type"]; !hasType {
				return "", fmt.Errorf("sub_agent_step.type is required when introducing a new inline route; use message_sequence for conversational work or regular only for a deterministic scripted boundary")
			}
			mergedSubAgentStepRaw["id"] = routeToUpdate.RouteID
			if title, ok := mergedSubAgentStepRaw["title"].(string); !ok || strings.TrimSpace(title) == "" {
				mergedSubAgentStepRaw["title"] = routeToUpdate.RouteName
			}
			updatedSubAgentStep, err := convertMapToStep(mergedSubAgentStepRaw)
			if err != nil {
				return "", fmt.Errorf("failed to parse sub_agent_step: %w", err)
			}
			routeToUpdate.SubAgentStep = updatedSubAgentStep
			if _, isRegular := updatedSubAgentStep.(*RegularPlanStep); isRegular && !existingWasRegular {
				newlyRegularSubAgentStep = updatedSubAgentStep
			}
		}

		if err := resolvePlanOrphanStepRefs(plan); err != nil {
			return "", fmt.Errorf("failed to resolve orphan step references after route update: %w", err)
		}
		if err := validateParent(); err != nil {
			return "", fmt.Errorf("validation failed after route update: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}
		if _, err := configureNewRegularStepsAsScripted(ctx, workspacePath, newlyRegularSubAgentStep, readFile, writeFile); err != nil {
			return "", fmt.Errorf("route was updated but required scripted configuration for its regular execution boundary could not be saved: %w", err)
		}

		var afterRouteSnapshot interface{}
		if afterRouteJSON, err := json.Marshal(*routeToUpdate); err == nil {
			afterRouteSnapshot = json.RawMessage(afterRouteJSON)
		} else {
			logger.Warn(fmt.Sprintf("⚠️ Failed to marshal post-update route %s for changelog: %v", existingRouteID, err))
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:           "update_orchestrator_route",
			Reason:         reason,
			StepIDs:        []string{parentStepID, existingRouteID},
			BeforeSnapshot: beforeRouteSnapshot,
			AfterSnapshot:  afterRouteSnapshot,
		}, readFile, writeFile, logger)

		routeReviewNotice := handleOrchestratorRouteArtifactReview(ctx, workspacePath, parentStepID, existingRouteID, "updated", readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated route '%s' (ID: %s) in agent step '%s'", routeToUpdate.RouteName, existingRouteID, parentTitle))
		return fmt.Sprintf("Successfully updated route '%s' (ID: %s) in agent step '%s'%s", routeToUpdate.RouteName, existingRouteID, parentTitle, routeReviewNotice), nil
	}
}

// createDeleteOrchestratorRouteExecutor creates an executor function for delete_todo_task_route tool
func createDeleteOrchestratorRouteExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}

		// Accept parent_step_id or legacy alias step_id
		parentStepID, ok := args["parent_step_id"].(string)
		if !ok || parentStepID == "" {
			parentStepID, ok = args["step_id"].(string)
		}
		if !ok || parentStepID == "" {
			return "", fmt.Errorf("invalid or missing parent_step_id")
		}

		deletedRouteID, ok := args["deleted_route_id"].(string)
		if !ok || deletedRouteID == "" {
			return "", fmt.Errorf("invalid or missing deleted_route_id")
		}

		// Read current plan
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Find the parent todo task step by ID. Recurses into nested steps
		// (e.g. a todo_task sitting in another todo_task's predefined_routes.sub_agent_step).
		parentStep, _, _ := findStepByID(plan.Steps, parentStepID)
		if parentStep == nil {
			parentStep, _, _ = findStepByID(plan.OrphanSteps, parentStepID)
		}
		if parentStep == nil {
			availableIDs := make([]string, 0, len(plan.Steps))
			for _, step := range plan.Steps {
				availableIDs = append(availableIDs, step.GetID())
			}
			return "", fmt.Errorf("parent step ID '%s' not found in existing plan. Available top-level step IDs: %v", parentStepID, availableIDs)
		}

		routes, parentTitle, _, ok := mutableAgentRoutes(parentStep)
		if !ok {
			return "", fmt.Errorf("step with ID '%s' is not an agent step (message_sequence or legacy orchestrator)", parentStepID)
		}

		// Find the route to delete
		var deletedRoute *PlanOrchestrationRoute
		routeIndex := -1
		for i, route := range *routes {
			if route.RouteID == deletedRouteID {
				deletedRoute = &(*routes)[i]
				routeIndex = i
				break
			}
		}
		if deletedRoute == nil {
			availableRouteIDs := make([]string, 0, len(*routes))
			for _, route := range *routes {
				availableRouteIDs = append(availableRouteIDs, route.RouteID)
			}
			return "", fmt.Errorf("route with route_id '%s' not found in todo task step '%s'. Available route IDs: %v", deletedRouteID, parentStepID, availableRouteIDs)
		}

		// Note: Unlike orchestration steps, todo task steps may have 0 predefined routes (generic-agent-only)

		// Remove the route
		*routes = append(
			(*routes)[:routeIndex],
			(*routes)[routeIndex+1:]...,
		)

		// Capture deleted route JSON before writing
		var deletedRouteJSON []json.RawMessage
		if rawDeleted, marshalErr := json.Marshal(deletedRoute); marshalErr == nil {
			deletedRouteJSON = []json.RawMessage{rawDeleted}
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:         "delete_orchestrator_route",
			Reason:       reason,
			StepIDs:      []string{parentStepID, deletedRouteID},
			DeletedSteps: deletedRouteJSON,
		}, readFile, writeFile, logger)

		deletedRouteSet := map[string]bool{deletedRouteID: true}
		if existingConfigs, cfgErr := readStepConfigViaFileCallback(ctx, workspacePath, readFile); cfgErr != nil {
			logger.Warn(fmt.Sprintf("⚠️ Failed to read step_config.json for route cascade-delete: %v", cfgErr))
		} else if newConfigs, removed := pruneStepConfigsByID(existingConfigs, deletedRouteSet); len(removed) > 0 {
			if writeErr := writeStepConfigViaFileCallback(ctx, workspacePath, newConfigs, writeFile); writeErr != nil {
				logger.Warn(fmt.Sprintf("⚠️ Failed to cascade-delete route step_config entries %v: %v", removed, writeErr))
			} else {
				logger.Info(fmt.Sprintf("🧹 Cascade-removed %d route step_config entries: %v", len(removed), removed))
			}
		}

		routeReviewNotice := handleOrchestratorRouteArtifactReview(ctx, workspacePath, parentStepID, deletedRouteID, "deleted", readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Deleted route '%s' (ID: %s) from agent step '%s'", deletedRoute.RouteName, deletedRouteID, parentTitle))
		return fmt.Sprintf("Successfully deleted route '%s' (ID: %s) from agent step '%s'%s", deletedRoute.RouteName, deletedRouteID, parentTitle, routeReviewNotice), nil
	}
}

// createUpdateValidationSchemaExecutor creates an executor function for update_validation_schema tool
func createUpdateValidationSchemaExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		reason, err := requireReason(args)
		if err != nil {
			return "", err
		}

		stepJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("failed to marshal step: %w", err)
		}

		var updateData struct {
			ExistingStepID   string            `json:"existing_step_id"`
			ValidationSchema *ValidationSchema `json:"validation_schema"`
		}
		if err := json.Unmarshal(stepJSON, &updateData); err != nil {
			return "", fmt.Errorf("failed to parse update data: %w", err)
		}

		if updateData.ExistingStepID == "" {
			return "", fmt.Errorf(fmt.Sprintf("existing_step_id is required"), nil)
		}
		if updateData.ValidationSchema == nil {
			return "", fmt.Errorf(fmt.Sprintf("validation_schema is required"), nil)
		}

		// Validate regex patterns before updating the plan
		if err := validateRegexPatternsInSchema(updateData.ValidationSchema); err != nil {
			return "", fmt.Errorf("invalid regex patterns in validation schema: %w", err)
		}

		// Validate JSONPath syntax
		if err := validateJSONPathSyntax(updateData.ValidationSchema); err != nil {
			return "", fmt.Errorf("invalid JSONPath syntax in validation schema: %w", err)
		}

		// Validate array_length consistency checks
		if err := validateArrayLengthConsistencyChecks(updateData.ValidationSchema); err != nil {
			return "", fmt.Errorf("invalid array_length consistency checks in validation schema: %w", err)
		}

		// Validate value_type/pattern compatibility
		if err := validateValueTypePatternCompatibility(updateData.ValidationSchema); err != nil {
			return "", fmt.Errorf("invalid validation schema: %w", err)
		}

		// Create PartialPlanStep with only validation schema
		partialUpdate := PartialPlanStep{
			ExistingStepID:   updateData.ExistingStepID,
			ValidationSchema: updateData.ValidationSchema,
		}

		// Read current plan
		plan, err := readPlanFromFile(ctx, workspacePath, readFile)
		if err != nil {
			return "", fmt.Errorf("failed to read plan: %w", err)
		}

		// Track changes for changelog
		fieldChanges := make([]PlanFieldChange, 0)

		// Update the step
		_, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}

		// Validate all steps after update
		if err := validatePlanStepIDs(plan.Steps); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}
		if err := validateStepIDUniqueness(plan); err != nil {
			return "", fmt.Errorf("plan validation failed after update: %w", err)
		}

		// Write updated plan
		if err := writePlanToFile(ctx, workspacePath, plan, readFile, writeFile, logger); err != nil {
			return "", fmt.Errorf("failed to write plan: %w", err)
		}

		logPlanChange(ctx, workspacePath, PlanChangelogEntry{
			Tool:    "update_validation_schema",
			Reason:  reason,
			StepIDs: []string{updateData.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)

		logger.Info(fmt.Sprintf("✅ Updated validation schema for step '%s' in plan", updateData.ExistingStepID))
		return fmt.Sprintf("Successfully updated validation schema for step '%s' in the plan", updateData.ExistingStepID), nil
	}
}

// NOTE: Learning folders are named using step IDs (e.g., learnings/{step_id}/),
// so folders don't need to be renamed when steps are reordered - the step ID stays the same.

// parseSchemaForToolParameters parses a JSON schema string and extracts properties for tool parameters
// This is a local copy of the function from mcpagent to avoid circular dependencies
func parseSchemaForToolParameters(schemaString string) (map[string]interface{}, error) {
	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaString), &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	// Extract properties - this becomes the tool parameters
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf(fmt.Sprintf("schema missing 'properties' field or it's not an object"), nil)
	}

	// Build tool parameter schema with type "object"
	toolParams := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}

	// Add required fields if present
	if required, ok := schema["required"].([]interface{}); ok {
		toolParams["required"] = required
	}

	return toolParams, nil
}
