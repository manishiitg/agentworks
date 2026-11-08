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
	ExecutionLLM              *AgentLLMConfig  `json:"execution_llm,omitempty"`
	ExecutionLLMReason        string           `json:"execution_llm_reason,omitempty"`        // PLAT-060. Why this step is pinned to a specific model. Required whenever execution_llm is set — update_step_config rejects the pin without it. A pin outranks execution_tier entirely, so it silently overrides every tier decision above it. Cite the owning architecture_review finding id, and the human_input_id when the pin was user-approved.
	ExecutionTier             string           `json:"execution_tier,omitempty"`              // Persistent execution tier override in tiered mode: "high" | "medium" | "low"
	ExecutionTierReason       string           `json:"execution_tier_reason,omitempty"`       // PLAT-060. Why this step's tier is pinned. Required whenever execution_tier is set — update_step_config rejects the override without it. The tier stays configured until explicitly changed; run counters do not select tiers. Cite the owning architecture_review finding id, and the human_input_id when approved.
	LearningObjective         string           `json:"learning_objective,omitempty"`          // Archived input for the one-off skills converter; put new instructions in the task.
	LearningsAccess           string           `json:"learnings_access,omitempty"`            // Archived input for the one-off skills converter; no execution effect.
	LegacyLockLearnings       *bool            `json:"lock_learnings,omitempty"`              // Archived input only; never consumed by runtime.
	LegacyLockLearningsReason string           `json:"lock_learnings_reason,omitempty"`       // PLAT-263 migration-only audit residue; removed with LegacyLockLearnings.
	SelectedServers           []string         `json:"selected_servers,omitempty"`            // step-level MCP server selection (subset of preset servers)
	SelectedTools             []string         `json:"selected_tools,omitempty"`              // step-level tool selection (format: "server:tool" or "server:*" for all tools)
	EnabledCustomTools        []string         `json:"enabled_custom_tools,omitempty"`        // optional additions, e.g. ["human_tools:get_notification_history"]; baseline is the workspace tools; name send_email, send_slack_message or create_human_input_request when a step needs them
	UseCodeExecutionMode      *bool            `json:"use_code_execution_mode,omitempty"`     // Step-level code execution mode override (nil = use preset default, true/false = override)
	EnabledSkills             []string         `json:"enabled_skills,omitempty"`              // Step-level skill selection (skill folder names, overrides preset if specified)
	AdditionalReadPaths       []string         `json:"additional_read_paths,omitempty"`       // Extra workflow-relative folders/files this step may read (for example "variables" or "reports/reference.json"). Paths are read-only, must stay inside this workflow, and never widen write access.
	KnowledgebaseAccess       string           `json:"knowledgebase_access,omitempty"`        // "read" | "write" | "read-write" | "none". Empty defaults to "read" so every step can consume shared context; "none" explicitly opts out. A non-empty knowledgebase_contribution promotes an unset mode to "read-write", but writes still require that contribution contract. Read access includes knowledgebase/context/context.md. knowledgebase/context/ is excluded from KB health/fixer passes — user-supplied content is never silently rewritten.
	KnowledgebaseContribution string           `json:"knowledgebase_contribution,omitempty"`  // Legacy automatic KB contribution instruction, archived with its destination by contract 1.0.50.
	CodingAgentTmuxLifecycle  string           `json:"coding_agent_tmux_lifecycle,omitempty"` // Tmux lifecycle for CLI coding providers: "close_on_completion" (default for steps) or "keep_alive" (only when a step intentionally needs the native coding session after completion).
	SuccessfulRuns            *int             `json:"successful_runs,omitempty"`             // System-managed counter. Written by syncSuccessfulRunsToStepConfig after each successful validation; mirrors the authoritative count in learning metadata. Read by the readiness checklist to gauge optimization progress (3+ = ready). Agents must NOT set this directly.
	DescriptionReviewed       *bool            `json:"description_reviewed,omitempty"`        // True when the step description has been reviewed — clarity AND secrets/hardcoded values.
	ReviewNotes               string           `json:"review_notes,omitempty"`                // Free-form rationale covering why config, learning/KB choices, code locks, or description review state are justified.
	DriftReview               *StepDriftReview `json:"drift_review,omitempty"`                // Plan-drift review record: evidence from the last completed plan_drift_review pass over this step, plus a NeedsReview flag every persisted field change or unresolved failed drift check keeps true. Nil (no record yet) or NeedsReview==true both mean "due." Set by that module or its manual slash-command equivalent; never by a step-editing agent directly.
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
	StepTypeScripted StepType = "scripted"
	StepTypeRouting  StepType = "routing"
	StepTypeAgent    StepType = "agent"
	// StepTypeBranch is a small in-flow next-step decision — same executor
	// as StepTypeRouting, distinct type so guidance/reporting/eval tooling
	// can tell it apart from routing, which is now the "route" (major fork)
	// concept. See PLAT-259.
	StepTypeBranch StepType = "branch"
)

// CommonStepFields contains fields shared by all step types
type CommonStepFields struct {
	GoalAgentMigration json.RawMessage `json:"goal_agent_migration,omitempty"` // Historical instructions, never executed or injected.

	RequiredVariables   []string              `json:"required_variables,omitempty"` // Launch values required before this step can run.
	ID                  string                `json:"id"`                           // Stable step ID (generated from title) - required
	Title               string                `json:"title"`
	Description         string                `json:"description"`
	ContextDependencies []string              `json:"context_dependencies"`
	ContextOutput       FlexibleContextOutput `json:"context_output"`              // Use flexible type to handle string or array
	ValidationSchema    *ValidationSchema     `json:"validation_schema,omitempty"` // Optional structured validation schema for step outputs
	// ScriptParameters is the public runtime-input contract for a scripted
	// (scripted) step. Todo-task orchestrators discover this contract from the
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

// StepSharing defines which agents in the same plan may reuse an orphan step.
type StepSharing struct {
	AgentIDs []string `json:"agent_ids,omitempty"`
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

// ScriptedPlanStep represents a scripted step
type ScriptedPlanStep struct {
	Type StepType `json:"type"` // Always "scripted" - required for JSON marshaling/unmarshaling
	CommonStepFields
	Script       string                        `json:"script,omitempty"`
	ScriptInputs map[string]ScriptInputBinding `json:"script_inputs,omitempty"`
	crewScript   *CrewScriptConfiguration
	NextStepID   string        `json:"next_step_id,omitempty"` // Optional explicit successor; empty preserves sequential execution
	AgentConfigs *AgentConfigs `json:"-"`                      // runtime: per-agent configuration (LLM, max turns, toggles) - not stored in plan.json
}

// Implement PlanStepInterface for ScriptedPlanStep
func (r *ScriptedPlanStep) GetID() string                           { return r.ID }
func (r *ScriptedPlanStep) GetTitle() string                        { return r.Title }
func (r *ScriptedPlanStep) GetDescription() string                  { return r.Description }
func (r *ScriptedPlanStep) GetContextDependencies() []string        { return r.ContextDependencies }
func (r *ScriptedPlanStep) GetContextOutput() FlexibleContextOutput { return r.ContextOutput }
func (r *ScriptedPlanStep) GetValidationSchema() *ValidationSchema  { return r.ValidationSchema }
func (r *ScriptedPlanStep) StepType() StepType                      { return StepTypeScripted }
func (r *ScriptedPlanStep) GetCommonFields() CommonStepFields       { return r.CommonStepFields }

// MarshalJSON ensures the type field is always set when marshaling
func (r *ScriptedPlanStep) MarshalJSON() ([]byte, error) {
	// Ensure type is set
	r.Type = StepTypeScripted
	// Use type alias to avoid infinite recursion
	type Alias ScriptedPlanStep
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
// it in a prior agent step that writes route_selection.json, then point this
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
	return r.CommonStepFields
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
	return b.CommonStepFields
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

// These labels are retained only for archived configs consumed by the one-off
// skills converter. Workflow execution does not grant learning access.
const (
	LearningsAccessRead      = "read"
	LearningsAccessReadWrite = "read-write"
	LearningsAccessNone      = "none"
)

// AgentWriteAccess optionally narrows store writes for one sequence item.
// When it is empty, the item inherits the step-level DB and KB access.
// Read access remains governed by the step-level configuration.
type AgentWriteAccess struct {
	Knowledgebase bool `json:"knowledgebase,omitempty"`
	DB            bool `json:"db,omitempty"`
	Learnings     bool `json:"learnings,omitempty"` // Archived field; never grants access.
}

// UnmarshalJSON rejects per-file scoping attempts on write_access. The access is
// folder-level booleans (knowledgebase/db); authors frequently try
// {"db": true, "paths": ["db/foo.json"]} expecting a single file to be writable.
// Standard decoding silently drops the unknown "paths" key, granting the whole
// db/ folder while the author thinks it was scoped — a real footgun. Catch the
// path-like keys with an explicit, actionable error instead.
func (w *AgentWriteAccess) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, key := range []string{"paths", "path", "files", "file"} {
		if _, ok := raw[key]; ok {
			return fmt.Errorf("write_access does not support per-file scoping (%q): it is folder-level booleans only — use {\"db\": true}, {\"knowledgebase\": true}, only", key)
		}
	}
	type alias AgentWriteAccess
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*w = AgentWriteAccess(a)
	return nil
}

type AgentItem struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"` // "user_message" | "prevalidation" | "foreach" | "scripted"
	Kind             string            `json:"kind,omitempty"`
	Title            string            `json:"title,omitempty"`
	Message          string            `json:"message,omitempty"`
	RetainedReason   string            `json:"retained_reason,omitempty"` // reviewed reason this phase follows the description instead of repeating it
	WriteAccess      AgentWriteAccess  `json:"write_access,omitempty"`
	ValidationSchema *ValidationSchema `json:"validation_schema,omitempty"`
	Prevalidation    *ValidationSchema `json:"prevalidation,omitempty"`
	// foreach items: iterate db/db.sqlite table rows, one templated user_message
	// per row. source_sql is a read-only query; each result row binds to '.'.
	SourceSQL     string `json:"source_sql,omitempty"`     // read-only SQL against db/db.sqlite
	MaxIterations int    `json:"max_iterations,omitempty"` // optional cap on rows (0 = all)
	// scripted items run a declared batch of saved scripts, without agent sessions.
	ScriptedSteps []AgentScriptCall `json:"scripted_steps,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	// prevalidation entries: corrective turns allowed on gate failure. Used by the
	// orchestrator's scripted sequence (todo_task); a standalone agent
	// uses its own fixed repair cap. Default 1 when unset.
	MaxCorrections int `json:"max_corrections,omitempty"`
	// Synthetic marks an item the runtime appended itself rather than one an
	// author wrote in plan.json — today only the final validation gate from
	// appendAgentFinalValidation. Never serialized: these items exist
	// only for the duration of a run, and writing them back would turn a
	// generated gate into authored plan content.
	Synthetic bool `json:"-"`
}

type AgentPlanStep struct {
	Type StepType `json:"type"`
	CommonStepFields
	// CrewAgent selects configuration from this workflow's owning Crew. It
	// never selects a conversation to continue or imports that chat's messages.
	Inputs            map[string]ScriptInputBinding `json:"inputs,omitempty"`
	CrewAgent         string                        `json:"crew_agent,omitempty"`
	crewAgentResolved bool                          // runtime snapshot only; never serialized into the plan
	Items             []AgentItem                   `json:"items,omitempty"`
	// Empty contract retains the saved system-charter semantics until reviewed migration.
	PromptContract     string            `json:"prompt_contract,omitempty"`
	SystemRules        string            `json:"system_rules,omitempty"`
	InitialWriteAccess *AgentWriteAccess `json:"initial_write_access,omitempty"`
	// AuthoredPrompt runs the exact system prompt and user messages supplied by
	// the author. It is used by Flows on the shared agent executor.
	AuthoredPrompt          bool                     `json:"authored_prompt,omitempty"`
	SystemPrompt            string                   `json:"system_prompt,omitempty"`
	PredefinedRoutes        []PlanOrchestrationRoute `json:"predefined_routes,omitempty"`
	NextStepID              string                   `json:"next_step_id,omitempty"`
	AgentConfigs            *AgentConfigs            `json:"-"`
	AgentDelegationDecision *AgentDelegationDecision `json:"-"`
}

func (m *AgentPlanStep) GetID() string                           { return m.ID }
func (m *AgentPlanStep) GetTitle() string                        { return m.Title }
func (m *AgentPlanStep) GetDescription() string                  { return m.Description }
func (m *AgentPlanStep) GetContextDependencies() []string        { return m.ContextDependencies }
func (m *AgentPlanStep) GetContextOutput() FlexibleContextOutput { return m.ContextOutput }
func (m *AgentPlanStep) GetValidationSchema() *ValidationSchema  { return m.ValidationSchema }
func (m *AgentPlanStep) StepType() StepType                      { return StepTypeAgent }
func (m *AgentPlanStep) GetCommonFields() CommonStepFields       { return m.CommonStepFields }

func (m *AgentPlanStep) MarshalJSON() ([]byte, error) {
	m.Type = StepTypeAgent
	type Alias AgentPlanStep
	return json.Marshal((*Alias)(m))
}

// UnmarshalJSON handles the interface-valued sub_agent_step fields in optional
// delegation routes. An agent with predefined_routes is still a
// agent plan step; the routes only add bounded, agent-decided
// delegation capabilities to its conversation.
func (m *AgentPlanStep) UnmarshalJSON(data []byte) error {
	type agentSequenceAlias struct {
		Type StepType `json:"type"`
		CommonStepFields
		Inputs              map[string]ScriptInputBinding `json:"inputs,omitempty"`
		CrewAgent           string                        `json:"crew_agent,omitempty"`
		Items               []AgentItem                   `json:"items,omitempty"`
		PromptContract      string                        `json:"prompt_contract,omitempty"`
		SystemRules         string                        `json:"system_rules,omitempty"`
		InitialWriteAccess  *AgentWriteAccess             `json:"initial_write_access,omitempty"`
		AuthoredPrompt      bool                          `json:"authored_prompt,omitempty"`
		SystemPrompt        string                        `json:"system_prompt,omitempty"`
		PredefinedRoutesRaw []json.RawMessage             `json:"predefined_routes,omitempty"`
		NextStepID          string                        `json:"next_step_id,omitempty"`
	}
	var raw agentSequenceAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal agent step: %w", err)
	}

	m.Type = StepTypeAgent
	m.CommonStepFields = raw.CommonStepFields
	m.CrewAgent = raw.CrewAgent
	m.Inputs = raw.Inputs
	m.Items = raw.Items
	m.PromptContract = raw.PromptContract
	m.SystemRules = raw.SystemRules
	m.InitialWriteAccess = raw.InitialWriteAccess
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
			return fmt.Errorf("failed to unmarshal agent predefined_route[%d]: %w", i, err)
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
				return fmt.Errorf("failed to unmarshal sub_agent_step in agent predefined_route[%d]: %w", i, err)
			}
			parsed.SubAgentStep = step
		}
		m.PredefinedRoutes = append(m.PredefinedRoutes, parsed)
	}
	return nil
}

// AgentDelegationDecision represents the structured output from the Orchestrator orchestrator agent
type AgentDelegationDecision struct {
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
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(stepData, &stepWithType); err != nil {
		return nil, fmt.Errorf("failed to parse %s %d type: %w", label, index, err)
	}

	if stepWithType.Type == "" {
		return nil, fmt.Errorf("%s %d is missing required 'type' field (must be: scripted, agent, routing, or branch)", label, index)
	}

	if stepWithType.Type == "human_input" {
		return nil, fmt.Errorf("%s %d (id %q): human_input steps are retired; ask Builder to replace this step with a supported human interaction before running again. See docs/workflow/human-input-step-retirement.md", label, index, stepWithType.ID)
	}

	switch stepWithType.Type {
	case "scripted", "regular":
		var step ScriptedPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse scripted %s %d: %w", label, index, err)
		}
		step.Type = StepTypeScripted
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
	case "agent":
		var step AgentPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse agent %s %d: %w", label, index, err)
		}
		return &step, nil
	default:
		return nil, fmt.Errorf("unknown step type %q in %s %d (must be: scripted, agent, routing, or branch)", stepWithType.Type, label, index)
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
	RequiredVariables   []string              `json:"required_variables,omitempty"`
	ExistingStepID      string                `json:"existing_step_id"`               // Required: ID of existing step to update
	Title               string                `json:"title,omitempty"`                // Optional: New title (if renaming)
	Description         string                `json:"description,omitempty"`          // Optional: Updated description
	ClearDescription    bool                  `json:"clear_description,omitempty"`    // Optional: Clear legacy descriptions that are no longer valid for routing steps
	ContextDependencies []string              `json:"context_dependencies,omitempty"` // Optional: Updated context dependencies
	ContextOutput       FlexibleContextOutput `json:"context_output,omitempty"`       // Optional: Updated context output
	// Todo task step fields
	PredefinedRoutes []PlanOrchestrationRoute `json:"predefined_routes,omitempty"` // Optional: Updated predefined routes for agent steps
	// Routing fields
	NextStepID string `json:"next_step_id,omitempty"` // Optional: Updated next_step_id (for routing steps)
	// Routing step fields
	RoutingQuestion string                        `json:"routing_question,omitempty"`  // Optional: Updated routing question
	Routes          []RoutingRoute                `json:"routes,omitempty"`            // Optional: Updated routes
	DefaultRouteID  string                        `json:"default_route_id,omitempty"`  // Optional: Updated default route ID
	RouteSourceFile string                        `json:"route_source_file,omitempty"` // Optional: Updated deterministic route source file
	ValuePath       string                        `json:"value_path,omitempty"`
	ValueCases      map[string]string             `json:"value_cases,omitempty"`
	ScriptRef       *string                       `json:"script,omitempty"`
	ScriptInputs    map[string]ScriptInputBinding `json:"script_inputs,omitempty"`
	// Branch step fields (PLAT-259: same deterministic-switch shape as
	// routing, just its own field name for the human-readable prompt)
	BranchQuestion         string                               `json:"branch_question,omitempty"`          // Optional: Updated branch question
	RouteSource            string                               `json:"route_source,omitempty"`             // Optional: "human" makes the branch ask a person when nothing was preseeded (branch only)
	ValidationSchema       *ValidationSchema                    `json:"validation_schema,omitempty"`        // Optional: Updated validation schema
	ScriptParameters       map[string]ScriptParameterDefinition `json:"script_parameters,omitempty"`        // Optional: replace the scripted runtime parameter contract; pass {} to clear
	ScriptParametersSchema map[string]interface{}               `json:"script_parameters_schema,omitempty"` // Optional: replace the full JSON Schema contract; pass {} to clear
	// Agent fields
	Inputs             map[string]ScriptInputBinding `json:"inputs,omitempty"`
	CrewAgentRef       *string                       `json:"crew_agent,omitempty"`
	Items              []AgentItem                   `json:"items,omitempty"`
	PromptContract     *string                       `json:"prompt_contract,omitempty"`
	SystemRules        *string                       `json:"system_rules,omitempty"`
	InitialWriteAccess *AgentWriteAccess             `json:"initial_write_access,omitempty"`
	AuthoredPrompt     *bool                         `json:"authored_prompt,omitempty"`
	SystemPrompt       *string                       `json:"system_prompt,omitempty"`
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
// planChangelogWritingKey marks a context that is inside writePlanChangelogEntry.
type planChangelogWritingKey struct{}

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
	// Writing the changelog file can itself rewrite workflow.json (a manifest read that migrates it), which logs a change
	// of its own. That nested entry is part of this one: it must not wait on the lock this call already holds.
	if ctx.Value(planChangelogWritingKey{}) != nil {
		return nil
	}
	ctx = context.WithValue(ctx, planChangelogWritingKey{}, true)
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
			// delete_agent_route) — hash that instead of collapsing to
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
		case strings.HasPrefix(entry.Target, "knowledgebase/"):
			entry.DependencyClass = "knowledge_base"
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

// getUpdateValidationSchemaSchema returns the JSON schema for update_validation_schema tool
// updateToolForStepType names the tool that edits a given step type.
//
// A refusal that says only "use its type-specific update tool" tells an agent it
// was wrong without telling it what is right, and the reasonable next conclusion
// is that the step cannot be edited at all. latency-report shows the cost: a fixer
// tried update_scripted_step on two agent collectors, was correctly
// refused, and recorded "legacy agentic scripted step, not editable" as a blocked
// finding. It re-reported that for days while update_agent_step sat in
// its own tool surface the whole time.
func updateToolForStepType(stepType StepType) string {
	switch stepType {
	case StepTypeAgent:
		return "update_agent_step"
	case StepTypeRouting:
		return "update_routing_step"
	case StepTypeBranch:
		return "update_branch_step"
	case StepTypeScripted:
		return "update_scripted_step"
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
		return nil, fmt.Errorf("plan.json uses an invalid or legacy format: %w", err)
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

	// Default to "scripted" type if not specified (common for sub_agent_steps in routes)
	stepType := stepWithType.Type
	if stepType == "" {
		stepType = "scripted"
	}

	// Unmarshal based on type
	var typedStep PlanStepInterface
	switch stepType {
	case "scripted", "regular":
		var step ScriptedPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse scripted step: %w", err)
		}
		// Ensure Type field is set (may be empty if not in JSON)
		step.Type = StepTypeScripted
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
	case "agent":
		var step AgentPlanStep
		if err := json.Unmarshal(stepData, &step); err != nil {
			return nil, fmt.Errorf("failed to parse agent step: %w", err)
		}
		step.Type = StepTypeAgent
		typedStep = &step
	default:
		return nil, fmt.Errorf("unknown step type %q (must be: scripted, agent, routing, or branch)", stepType)
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
	case *ScriptedPlanStep:
		s.ValidationSchema = schema
	case *RoutingPlanStep:
		s.ValidationSchema = schema
	case *BranchPlanStep:
		s.ValidationSchema = schema
	case *AgentPlanStep:
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
	case *ScriptedPlanStep:
		// No type-specific fields to diff (loop fields removed)

	case *AgentPlanStep:
		if newS, ok := newStep.(*AgentPlanStep); ok {
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
	case *ScriptedPlanStep:
		// Create updated copy
		updated := *step
		if partialUpdate.RequiredVariables != nil {
			updated.RequiredVariables = append([]string{}, partialUpdate.RequiredVariables...)
		}
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
		if partialUpdate.ScriptRef != nil {
			updated.Script = strings.TrimPrefix(strings.TrimSpace(*partialUpdate.ScriptRef), "script:")
		}
		if partialUpdate.ScriptInputs != nil {
			updated.ScriptInputs = partialUpdate.ScriptInputs
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

	case *AgentPlanStep:
		updated := *step
		if partialUpdate.Inputs != nil {
			updated.Inputs = partialUpdate.Inputs
		}
		if partialUpdate.PromptContract != nil {
			updated.PromptContract = *partialUpdate.PromptContract
		}
		if partialUpdate.SystemRules != nil {
			updated.SystemRules = *partialUpdate.SystemRules
		}
		if partialUpdate.InitialWriteAccess != nil {
			access := *partialUpdate.InitialWriteAccess
			updated.InitialWriteAccess = &access
		}

		if partialUpdate.RequiredVariables != nil {
			updated.RequiredVariables = append([]string{}, partialUpdate.RequiredVariables...)
		}
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
		if partialUpdate.CrewAgentRef != nil {
			updated.CrewAgent = strings.TrimSpace(*partialUpdate.CrewAgentRef)
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

	case *RoutingPlanStep:
		updated := *step
		if partialUpdate.RequiredVariables != nil {
			updated.RequiredVariables = append([]string{}, partialUpdate.RequiredVariables...)
		}
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
		if partialUpdate.RequiredVariables != nil {
			updated.RequiredVariables = append([]string{}, partialUpdate.RequiredVariables...)
		}
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
		case *AgentPlanStep:
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
		case *AgentPlanStep:
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
	if partialUpdate.RequiredVariables != nil {
		changedFields = append(changedFields, "required_variables")
		*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "required_variables", OldValue: existingStep.GetCommonFields().RequiredVariables, NewValue: partialUpdate.RequiredVariables})
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

	if partialUpdate.Items != nil {
		changedFields = append(changedFields, "items")
		oldItemsJSON := "[]"
		if sequenceStep, ok := existingStep.(*AgentPlanStep); ok {
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
	if sequenceStep, ok := existingStep.(*AgentPlanStep); ok {
		if partialUpdate.Inputs != nil {
			changedFields = append(changedFields, "inputs")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "inputs", OldValue: sequenceStep.Inputs, NewValue: partialUpdate.Inputs})
		}
		if partialUpdate.CrewAgentRef != nil {
			changedFields = append(changedFields, "crew_agent")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "crew_agent", OldValue: sequenceStep.CrewAgent, NewValue: *partialUpdate.CrewAgentRef})
		}
		if partialUpdate.PromptContract != nil {
			changedFields = append(changedFields, "prompt_contract")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "prompt_contract", OldValue: sequenceStep.PromptContract, NewValue: *partialUpdate.PromptContract})
		}
		if partialUpdate.SystemRules != nil {
			changedFields = append(changedFields, "system_rules")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "system_rules", OldValue: sequenceStep.SystemRules, NewValue: *partialUpdate.SystemRules})
		}
		if partialUpdate.InitialWriteAccess != nil {
			changedFields = append(changedFields, "initial_write_access")
			*fieldChanges = append(*fieldChanges, PlanFieldChange{StepID: partialUpdate.ExistingStepID, Field: "initial_write_access", OldValue: sequenceStep.InitialWriteAccess, NewValue: partialUpdate.InitialWriteAccess})
		}
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
	if partialUpdate.PredefinedRoutes != nil {
		changedFields = append(changedFields, "predefined_routes")
		// Get old routes
		var oldRoutes []PlanOrchestrationRoute
		if orchestratorStep, ok := existingStep.(*AgentPlanStep); ok {
			oldRoutes = orchestratorStep.PredefinedRoutes
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
	if partialUpdate.NextStepID != "" {
		changedFields = append(changedFields, "next_step_id")
		oldNextStepID := ""
		switch step := existingStep.(type) {
		case *ScriptedPlanStep:
			oldNextStepID = step.NextStepID
		case *AgentPlanStep:
			oldNextStepID = step.NextStepID
		}
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "next_step_id",
			OldValue: oldNextStepID,
			NewValue: partialUpdate.NextStepID,
		})
	}
	if partialUpdate.Items != nil {
		changedFields = append(changedFields, "items")
		oldMessagesJSON := "[]"
		if orchestratorStep, ok := existingStep.(*AgentPlanStep); ok {
			oldBytes, _ := json.Marshal(orchestratorStep.Items)
			oldMessagesJSON = string(oldBytes)
		}
		newBytes, _ := json.Marshal(partialUpdate.Items)
		*fieldChanges = append(*fieldChanges, PlanFieldChange{
			StepID:   partialUpdate.ExistingStepID,
			Field:    "items",
			OldValue: oldMessagesJSON,
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

var planDriftMaterialFieldNames = map[string]bool{
	"type": true, "required_variables": true, "context_dependencies": true, "context_output": true,
	"items": true, "next_step_id": true,
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

const planEditImpactGuidance = "After related edits are complete, do one combined compatibility check in the current agent before the targeted test. For small internal edits with unchanged outputs, DB writes, routing, and behavior, confirm compatibility briefly and test. For output, DB, routing, or behavior changes, inspect and reconcile only affected dependencies before testing. Stop once affected consumers are compatible; unrelated backlog and missing audit receipts must not delay the test. Read builder-reference/references/plan-change-impact.md for the procedure. Do not launch a separate drift reviewer for each edit or test retry; reserve the full run-plan-drift audit for scheduled AgentWorks or an explicit user request."

// planEditImpactGuidancePointer replaces planEditImpactGuidance on repeated plan edits of the same workflow.
// The full paragraph (~700 characters) used to come back on every edit, most of each update response.
const planEditImpactGuidancePointer = "Same compatibility procedure as earlier in this session: one combined check after related edits, then the targeted test (builder-reference/references/plan-change-impact.md)."

const planEditImpactGuidanceRepeatWindow = 30 * time.Minute

// planEditImpactGuidanceShownAt records, per workflow, when the full paragraph was last sent.
var planEditImpactGuidanceShownAt sync.Map

// compactRepeatedPlanEditImpactGuidance sends the full compatibility paragraph on the first plan edit of a workflow
// and a one-line pointer to the same procedure on edits within the following 30 minutes of activity. Without a
// workflow path it always keeps the full text.
func compactRepeatedPlanEditImpactGuidance(workspacePath, notice string, now time.Time) string {
	key := strings.TrimSpace(workspacePath)
	if key == "" || !strings.Contains(notice, planEditImpactGuidance) {
		return notice
	}
	last, seen := planEditImpactGuidanceShownAt.Load(key)
	planEditImpactGuidanceShownAt.Store(key, now)
	if seen && now.Sub(last.(time.Time)) < planEditImpactGuidanceRepeatWindow {
		return strings.Replace(notice, planEditImpactGuidance, planEditImpactGuidancePointer, 1)
	}
	return notice
}

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
		b.WriteString("- Flagged step_config.agent_configs.drift_review.needs_review=true — the plan_drift_review AgentWorks module will now treat this step as due until it (or the manual run-plan-drift slash command) records fresh evidence via record_plan_drift_review.\n")
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
	return compactRepeatedPlanEditImpactGuidance(workspacePath, buildPlanStepDependentArtifactReviewNotice(stepID, fieldChanges, descriptionReviewCleared, driftReviewCleared, driftReviewFlagFailed), time.Now())
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

// requireStepUpdateType prevents a typed updater from changing execution models.
func requireStepUpdateType(plan *PlanningResponse, stepID string, expected StepType) error {
	if plan == nil {
		return fmt.Errorf("cannot update step %q: plan is unavailable", stepID)
	}
	step, _, _ := findStepByID(plan.Steps, stepID)
	if step == nil {
		step, _, _ = findStepByID(plan.OrphanSteps, stepID)
	}
	if step != nil && step.StepType() != expected {
		return wrongStepTypeToolError(stepID, step.StepType(), "update_"+string(expected)+"_step")
	}
	return nil // The update handler reports a missing step.
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
	descriptionSizeSevereMultiplier   = 3.0
	descriptionSizeSevereFloor        = 12000
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
// add/update_agent_step's response when the description is well
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
	layout := "Description should stay WHAT to achieve, in the sections Goal, Inputs, Output, Rules, Done when and Guides. " +
		"Reusable HOW (technique, procedure, phrasing rules, proven methods) belongs in a skill reference; domain facts and decisions belong in a knowledgebase note; " +
		"dated observations and incident stories belong in neither the description nor a guide: delete them once the fix is in place. " +
		"Before adding to a skill, check learnings/_global/SKILL.md's index and attached skills for an existing topic; correct that topic file in place rather than appending a second version of the same rule. " +
		"For genuinely new technique add learnings/_global/references/<topic>.md (named for the subject, not this step) with a one-line index entry."
	severeThreshold := int(float64(planMedian) * descriptionSizeSevereMultiplier)
	if severeThreshold < descriptionSizeSevereFloor {
		severeThreshold = descriptionSizeSevereFloor
	}
	if after >= severeThreshold {
		// Advisory text alone did not stop slow growth (Upwork 2026-10-05: profile-suggest-report kept growing past
		// 21k characters with the nudge showing every time). Above this size, ask for the move now.
		return fmt.Sprintf("\n\nOVER BUDGET: this description is now %d characters (+%d this edit; this plan's other steps have a median of %d), more than %.0fx the plan's normal size. "+
			"The edit is saved. Before your next test or edit of this step, restructure it in this session: keep only Goal, Inputs, Output, Rules, Done when and Guides in the description, "+
			"move procedures to a skill reference and facts or decisions to a knowledgebase note, delete dated history, and name the moved guides under Guides. "+
			"Verify nothing binding was lost (every rule, threshold, identifier and file name still appears in the description or a referenced note). %s", after, grew, planMedian, descriptionSizeSevereMultiplier, layout)
	}
	return fmt.Sprintf("\n\nDescription is now %d characters (+%d this edit; this plan's other steps have a median of %d). %s", after, grew, planMedian, layout)
}

func createUpdateAgentStepExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
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
		if err := requireStepUpdateType(plan, partialUpdate.ExistingStepID, StepTypeAgent); err != nil {
			return "", err
		}
		descriptionBeforeLen := 0
		if _, descriptionProvided := args["description"]; descriptionProvided {
			if existingStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID); existingStep != nil {
				descriptionBeforeLen = len(existingStep.GetDescription())
			}
		}

		if partialUpdate.PromptContract != nil && *partialUpdate.PromptContract == AgentDescriptionFirstPromptContract {
			target, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID)
			if target == nil {
				target, _, _ = findStepByID(plan.OrphanSteps, partialUpdate.ExistingStepID)
			}
			if agent, ok := target.(*AgentPlanStep); ok && !agentDescriptionFirst(agent) {
				for _, field := range []string{"description", "items"} {
					if _, provided := args[field]; !provided {
						return "", fmt.Errorf("description-first migration requires a reviewed description and items (including []); missing %s", field)
					}
				}
			}
		}
		fieldChanges := make([]PlanFieldChange, 0)
		_, _, err = updateSingleStep(plan, partialUpdate, &fieldChanges)
		if err != nil {
			return "", err
		}
		updatedStep, _, _ := findStepByID(plan.Steps, partialUpdate.ExistingStepID)
		if updatedStep == nil {
			updatedStep, _, _ = findStepByID(plan.OrphanSteps, partialUpdate.ExistingStepID)
		}
		if sequenceStep, ok := updatedStep.(*AgentPlanStep); ok {
			if !sequenceStep.AuthoredPrompt && sequenceStep.SystemRules != "" {
				beforeDescription, beforeRules := sequenceStep.Description, sequenceStep.SystemRules
				normalizeAgentTaskRules(sequenceStep)
				for _, change := range []PlanFieldChange{{StepID: sequenceStep.ID, Field: "description", OldValue: beforeDescription, NewValue: sequenceStep.Description}, {StepID: sequenceStep.ID, Field: "system_rules", OldValue: beforeRules, NewValue: ""}} {
					found := false
					for i := range fieldChanges {
						if fieldChanges[i].StepID == change.StepID && fieldChanges[i].Field == change.Field {
							fieldChanges[i].NewValue = change.NewValue
							found = true
							break
						}
					}
					if !found && change.OldValue != change.NewValue {
						fieldChanges = append(fieldChanges, change)
					}
				}
			}
			if partialUpdate.PredefinedRoutes != nil {
				if err := validateGoalScriptedRouteAuthoring(sequenceStep.PredefinedRoutes, plan); err != nil {
					return "", err
				}
			}
			if err := validateAgentStepFieldsTyped(sequenceStep); err != nil {
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
			Tool:    "update_agent_step",
			Reason:  reason,
			StepIDs: []string{partialUpdate.ExistingStepID},
			Changes: fieldChanges,
		}, readFile, writeFile, logger)
		dependentReviewNotice := handlePlanStepDependentArtifactReview(ctx, workspacePath, partialUpdate.ExistingStepID, fieldChanges, readFile, writeFile, logger)
		sizeNudge := ""
		if _, descriptionProvided := args["description"]; descriptionProvided && updatedStep != nil {
			planMedian := planMedianOtherStepDescriptionLen(plan.Steps, partialUpdate.ExistingStepID)
			sizeNudge = stepDescriptionSizeNudge(descriptionBeforeLen, len(updatedStep.GetDescription()), planMedian)
			sizeNudge += descriptionReferencesEditNotice(workspacePath, updatedStep.GetDescription())
		}
		logger.Info(fmt.Sprintf("✅ Updated agent step '%s' in plan", partialUpdate.ExistingStepID))
		return fmt.Sprintf("Successfully updated agent step '%s' in the plan%s%s", partialUpdate.ExistingStepID, dependentReviewNotice, sizeNudge), nil
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

		cleanupNotice := compactRepeatedPlanEditImpactGuidance(workspacePath, buildDeletedStepArtifactCleanupNotice(deletedIDs, prunedConfigIDs, driftReviewFlagFailed), time.Now())

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

// Returns an error message suitable for returning as a tool response if validation fails

func validateAgentDelegationRoutes(title, stepID string, routes []PlanOrchestrationRoute) error {
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
		case *AgentPlanStep:
			if err := validateAgentStepFieldsTyped(subStep); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) predefined_route[%d] (route_id: %s): %w", title, stepID, i, route.RouteID, err)
			}
		case *ScriptedPlanStep:
			if err := validateScriptParameterContract(&subStep.CommonStepFields); err != nil {
				return fmt.Errorf("step (title: %q, ID: %s) predefined_route[%d] (route_id: %s) has invalid script_parameters: %w", title, stepID, i, route.RouteID, err)
			}
		}
	}
	return nil
}

func validateAgentStepFieldsTyped(step *AgentPlanStep) error {
	if step == nil {
		return fmt.Errorf("agent step is nil")
	}
	if strings.TrimSpace(step.ID) == "" {
		return fmt.Errorf("agent step (title: %q) is missing required id field", step.Title)
	}
	if strings.TrimSpace(step.Title) == "" {
		return fmt.Errorf("agent step (ID: %s) is missing required title field", step.ID)
	}
	if strings.TrimSpace(step.Description) == "" {
		return fmt.Errorf("agent step (title: %q, ID: %s) is missing required description field", step.Title, step.ID)
	}
	if err := validateStepInputBindings(step.Inputs, step.ContextDependencies); err != nil {
		return fmt.Errorf("step %q: %w", step.ID, err)
	}
	if step.PromptContract != "" && step.PromptContract != AgentDescriptionFirstPromptContract {
		return fmt.Errorf("agent step %q has unknown prompt_contract %q", step.ID, step.PromptContract)
	}
	if step.AuthoredPrompt && step.PromptContract != "" {
		return fmt.Errorf("agent step %q: authored_prompt cannot use a generated prompt_contract", step.ID)
	}
	if len(step.Items) == 0 && !agentDescriptionFirst(step) {
		return fmt.Errorf("agent step (title: %q, ID: %s) must include at least one item under the legacy or authored prompt contract", step.Title, step.ID)
	}
	if agentDescriptionFirst(step) {
		for _, item := range step.Items {
			if item.ID == AgentDescriptionTurnID {
				return fmt.Errorf("agent step %q: item id %q is reserved for the description turn", step.ID, item.ID)
			}
		}
	}
	if step.AuthoredPrompt {
		if strings.TrimSpace(step.SystemPrompt) == "" {
			return fmt.Errorf("agent step %q: authored_prompt requires system_prompt", step.ID)
		}
		if lastType := strings.TrimSpace(step.Items[len(step.Items)-1].Type); lastType != "" && lastType != "user_message" {
			return fmt.Errorf("agent step %q: authored_prompt must end with a user_message that returns JSON", step.ID)
		}
	}
	seen := make(map[string]bool, len(step.Items))
	for i, item := range step.Items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("agent step %q item[%d] is missing required id", step.ID, i)
		}
		if seen[item.ID] {
			return fmt.Errorf("agent step %q has duplicate item id %q", step.ID, item.ID)
		}
		seen[item.ID] = true
		itemType := strings.TrimSpace(item.Type)
		if itemType == "" {
			itemType = "user_message"
		}
		if itemType != "scripted" && (len(item.ScriptedSteps) > 0 || item.MaxParallel != 0) {
			return fmt.Errorf("agent item %q: scripted_steps/max_parallel require type scripted", item.ID)
		}
		switch itemType {
		case "scripted":
			if err := validateAgentScriptItem(item); err != nil {
				return err
			}
		case "user_message":
			if strings.TrimSpace(item.Message) == "" {
				return fmt.Errorf("agent step %q item %q is a user_message but message is empty", step.ID, item.ID)
			}
		case "code":
			return fmt.Errorf("agent step %q item %q uses removed type \"code\"; ask Builder to replace it with a standalone scripted step", step.ID, item.ID)
		case "prevalidation":
			if item.ValidationSchema == nil && item.Prevalidation == nil && step.ValidationSchema == nil {
				return fmt.Errorf("agent step %q item %q is prevalidation but no validation_schema/prevalidation exists", step.ID, item.ID)
			}
		case "foreach":
			if strings.TrimSpace(item.SourceSQL) == "" {
				return fmt.Errorf("agent step %q item %q is foreach but source_sql is empty", step.ID, item.ID)
			}
			if strings.TrimSpace(item.Message) == "" {
				return fmt.Errorf("agent step %q item %q is foreach but message (the per-row template) is empty", step.ID, item.ID)
			}
		default:
			return fmt.Errorf("agent step %q item %q has unsupported type %q", step.ID, item.ID, item.Type)
		}
		if step.AuthoredPrompt && itemType != "user_message" && itemType != "scripted" {
			return fmt.Errorf("agent step %q item %q: authored_prompt supports only user_message and scripted items", step.ID, item.ID)
		}

		// NOTE: per-item write access (db/kb/learnings) is intentionally NOT
		// validated from the message prose here. Detecting "this item writes the
		// db" from natural language is unreliable (false negatives like "upsert
		// into trade_ideas", false positives on descriptive text), so we don't
		// gate plans on a guess. Enforcement lives at the runtime folder guard
		// (the source of truth — it blocks the write and emits an actionable
		// "grant write_access.db" failure), and structured signals (kind /
		// item kind can auto-grant via resolveAgentItemWriteAccess. The
		// authoring contract is carried by guidance, not a prose regex.
	}
	if err := validateAgentDelegationRoutes(step.Title, step.ID, step.PredefinedRoutes); err != nil {
		return err
	}
	if err := validateAgentNestingDepth(step, 0); err != nil {
		return err
	}
	return nil
}

func setStepIdentity(step PlanStepInterface, id, title string) error {
	switch s := step.(type) {
	case *ScriptedPlanStep:
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
	case *AgentPlanStep:
		s.ID = id
		if strings.TrimSpace(s.Title) == "" {
			s.Title = title
		}
	default:
		return fmt.Errorf("unsupported sub_agent_step type %T for identity normalization", step)
	}
	return nil
}

func validateAgentNestingDepth(step PlanStepInterface, todoRouteDepth int) error {
	var routes []PlanOrchestrationRoute
	switch s := step.(type) {
	case *AgentPlanStep:
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
			if err := validateAgentNestingDepth(route.SubAgentStep, todoRouteDepth+1); err != nil {
				return fmt.Errorf("predefined_route[%d] (route_id: %s): %w", i, route.RouteID, err)
			}
		}
	}
	return nil
}

type planChangeDependencyMismatch struct {
	StepID   string   `json:"step_id"`
	Expected []string `json:"expected"`
	Actual   []string `json:"actual,omitempty"`
	Reason   string   `json:"reason"`
}

func withPlanMutationWriteAccess(workspacePath string, writeFile func(context.Context, string, string) error) func(context.Context, string, string) error {
	planningPath := normalizePathForWorkspaceAPI("planning", workspacePath)
	return func(ctx context.Context, path, content string) error {
		return writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, planningPath), path, content)
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
