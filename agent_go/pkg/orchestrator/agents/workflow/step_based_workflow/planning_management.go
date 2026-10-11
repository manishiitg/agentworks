package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EnhancedPlanWithMetadata stores enhanced plan with caching metadata
type EnhancedPlanWithMetadata struct {
	Plan          *PlanningResponse  `json:"plan"`
	LastUpdated   time.Time          `json:"last_updated"`
	LearningFiles []LearningFileInfo `json:"learning_files"`
}

// LearningFileInfo stores information about a learning file for cache comparison
type LearningFileInfo struct {
	Filepath   string    `json:"filepath"`
	ModifiedAt time.Time `json:"modified_at"`
}

// validateRoutingStepTyped validates that a routing/branch step has all
// required fields. Operates on the shared routeSwitchStep interface (see
// planning_agent.go, PLAT-259) rather than a concrete *RoutingPlanStep, so it
// applies identically to *BranchPlanStep — no-op for every other step type.
func validateRoutingStepTyped(step PlanStepInterface, stepIndex int) error {
	if routingStep, ok := step.(routeSwitchStep); ok {
		if routingStep.GetID() == "" {
			return fmt.Errorf("routing step at index %d (title: %q) is missing required ID field", stepIndex, step.GetTitle())
		}
		if routingStep.GetRoutingQuestionText() == "" {
			return fmt.Errorf("routing step at index %d (title: %q) is missing required routing_question field", stepIndex, step.GetTitle())
		}
		if len(routingStep.GetRoutes()) < 2 {
			return fmt.Errorf("routing step at index %d (title: %q) must have at least 2 routes, got %d", stepIndex, step.GetTitle(), len(routingStep.GetRoutes()))
		}
		routeIDs := make(map[string]bool)
		for _, route := range routingStep.GetRoutes() {
			if route.RouteID == "" {
				return fmt.Errorf("routing step at index %d (title: %q) has a route with empty route_id", stepIndex, step.GetTitle())
			}
			if route.NextStepID == "" {
				return fmt.Errorf("routing step at index %d (title: %q) route %q is missing next_step_id", stepIndex, step.GetTitle(), route.RouteID)
			}
			if routeIDs[route.RouteID] {
				return fmt.Errorf("routing step at index %d (title: %q) has duplicate route_id %q", stepIndex, step.GetTitle(), route.RouteID)
			}
			routeIDs[route.RouteID] = true
		}
		if routingStep.GetDefaultRouteID() != "" && !routeIDs[routingStep.GetDefaultRouteID()] {
			return fmt.Errorf("routing step at index %d (title: %q) has default_route_id %q that doesn't match any route_id", stepIndex, step.GetTitle(), routingStep.GetDefaultRouteID())
		}
	}
	return nil
}

// validatePlanStepIDs recursively validates that all steps have IDs
// Throws error if any step is missing an ID
func validatePlanStepIDsAtPath(steps []PlanStepInterface, pathPrefix string) error {
	for i, step := range steps {
		thisLoc := fmt.Sprintf("%s[%d]", pathPrefix, i)
		if step.GetID() == "" {
			return fmt.Errorf("step at %s is missing required ID field. Step title: %q", thisLoc, step.GetTitle())
		}

		// Validate routing step fields
		if err := validateRoutingStepTyped(step, i); err != nil {
			return err
		}
		var agentRoutes []PlanOrchestrationRoute
		switch agentStep := step.(type) {
		case *AgentPlanStep:
			agentRoutes = agentStep.PredefinedRoutes
		}
		for routeIndex, route := range agentRoutes {
			if route.SubAgentStep == nil {
				continue
			}
			routePath := fmt.Sprintf("%s.predefined_routes[%d].sub_agent_step", thisLoc, routeIndex)
			if err := validatePlanStepIDsAtPath([]PlanStepInterface{route.SubAgentStep}, routePath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePlanStepIDs(steps []PlanStepInterface) error {
	return validatePlanStepIDsAtPath(steps, "steps")
}

// collectStepIDsRecursive walks steps and nested todo-task sub-agents and records
// each ID against the location where it was first seen. Returns a duplicate error
// on the first collision it encounters. Empty IDs are skipped — presence is the
// job of validatePlanStepIDs.
func collectStepIDsRecursive(steps []PlanStepInterface, pathPrefix string, seen map[string]string) error {
	for i, step := range steps {
		id := step.GetID()
		thisLoc := fmt.Sprintf("%s[%d] (title: %q)", pathPrefix, i, step.GetTitle())
		if id != "" {
			if prev, dup := seen[id]; dup {
				return fmt.Errorf("duplicate step ID %q: first at %s, again at %s", id, prev, thisLoc)
			}
			seen[id] = thisLoc
		}
		var agentRoutes []PlanOrchestrationRoute
		switch agentStep := step.(type) {
		case *AgentPlanStep:
			agentRoutes = agentStep.PredefinedRoutes
		}
		for routeIndex, route := range agentRoutes {
			if route.SubAgentStep == nil {
				continue
			}
			routePath := fmt.Sprintf("%s.predefined_routes[%d].sub_agent_step", thisLoc, routeIndex)
			if err := collectStepIDsRecursive([]PlanStepInterface{route.SubAgentStep}, routePath, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateStepIDUniqueness enforces that every step ID is unique across the plan —
// main steps, orphan steps, and nested sub-agent steps share the same namespace
// because step IDs are used as learnings/{stepID}/ folder names and collisions
// silently clobber saved scripts and metadata.
func validateStepIDUniqueness(plan *PlanningResponse) error {
	if plan == nil {
		return nil
	}
	seen := make(map[string]string)
	if err := collectStepIDsRecursive(plan.Steps, "steps", seen); err != nil {
		return err
	}
	if err := collectStepIDsRecursive(plan.OrphanSteps, "orphan_steps", seen); err != nil {
		return err
	}
	return nil
}

// checkExistingPlan checks if a plan.json file already exists in the workspace and returns the parsed plan if found
// Uses the generic ReadWorkspaceFile function from base orchestrator
func (hcpo *StepBasedWorkflowOrchestrator) checkExistingPlan(ctx context.Context, planPath string) (bool, *PlanningResponse, error) {

	// Use the generic ReadWorkspaceFile function from base orchestrator
	planContent, err := hcpo.ReadWorkspaceFile(ctx, planPath)
	if err != nil {
		// Check if it's a "file not found" error vs other errors
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			return false, nil, nil
		}
		// Other errors should be returned
		return false, nil, fmt.Errorf("failed to check existing plan: %w", err)
	}

	// Parse JSON content to PlanningResponse
	var planResponse PlanningResponse
	if err := json.Unmarshal([]byte(planContent), &planResponse); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to parse existing plan.json: %v", err))
		return false, nil, fmt.Errorf("failed to parse plan.json: %w", err)
	}
	if err := resolvePlanOrphanStepRefs(&planResponse); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to resolve orphan step references in existing plan.json: %v", err))
		return false, nil, fmt.Errorf("failed to resolve orphan step references in plan.json: %w", err)
	}
	// checkExistingPlan was previously a parse-only path: it returned
	// the plan without invoking the existing validator chain that the
	// fresh-plan write path (writePlanToFile) and the canonical load
	// path (loadPlanFromFile) both call. This let duplicate step IDs,
	// dangling routing.next_step_id and ambiguous human-response route
	// combinations through to LLM execution —
	// every per-step artifact then collides under the colliding ID
	// and writes silently clobber prior content. Re-enabling the
	// validator at this seam catches the violations at plan load.
	if err := validateLoadedPlanStructure(&planResponse); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Existing plan failed validation: %v", err))
		return false, nil, fmt.Errorf("existing plan.json failed validation: %w", err)
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Found existing plan at %s with %d steps", planPath, len(planResponse.Steps)))
	return true, &planResponse, nil
}

func validateLoadedPlanStepWithOptions(typedStep PlanStepInterface, stepIndex int, allowLegacyAgentCode bool) error {
	switch step := typedStep.(type) {
	case *RegularPlanStep:
		if err := validateScriptParameterContract(&step.CommonStepFields); err != nil {
			return fmt.Errorf("invalid script_parameters: %w", err)
		}
		return nil

	case *HumanInputPlanStep:
		return validateHumanInputStepFieldsTyped(step)

	case *AgentPlanStep:
		if err := validateAgentStepFieldsTypedWithOptions(step, allowLegacyAgentCode); err != nil {
			return err
		}
		for i, route := range step.PredefinedRoutes {
			if route.SubAgentStep != nil {
				if err := validateLoadedPlanStepWithOptions(route.SubAgentStep, i, allowLegacyAgentCode); err != nil {
					return fmt.Errorf("predefined_route[%d] (route_id: %s): %w", i, route.RouteID, err)
				}
			}
		}
		return nil

	case *CrewPlanStep:
		return validateCrewStepFieldsTyped(step)

	case *RoutingPlanStep, *BranchPlanStep:
		if err := validateRoutingStepTyped(step, stepIndex); err != nil {
			return err
		}
		return nil

	default:
		return fmt.Errorf("unsupported step type %T during loaded plan validation", typedStep)
	}
}

func validateLoadedPlanStructure(plan *PlanningResponse) error {
	return validateLoadedPlanStructureWithOptions(plan, false)
}

// validateLoadedPlanStructureAllowLegacyAgentCode exists only so a
// workflow-version preflight can open a pre-v1.0.10 plan and call the trusted
// migration tool. Execution and every persisted plan write use the strict
// validator above, so a legacy code item can never execute or be saved again.
func validateLoadedPlanStructureAllowLegacyAgentCode(plan *PlanningResponse) error {
	return validateLoadedPlanStructureWithOptions(plan, true)
}

func validateLoadedPlanStructureWithOptions(plan *PlanningResponse, allowLegacyAgentCode bool) error {
	if err := validateLoadedPlanStructureCoreWithOptions(plan, allowLegacyAgentCode); err != nil {
		return err
	}
	if err := validateNextStepIDReferences(plan); err != nil {
		return err
	}
	if err := validateAgentScriptReferences(plan); err != nil {
		return err
	}
	return nil
}

// validateLoadedPlanStructureCore validates the plan representation while
// deliberately excluding cross-step graph references. Mutation tools use this
// read mode so they can load and repair a graph left dangling by older code;
// every write still calls ValidatePlanStructure and therefore remains atomic.
func validateLoadedPlanStructureCore(plan *PlanningResponse) error {
	return validateLoadedPlanStructureCoreWithOptions(plan, false)
}

// validateLoadedPlanStructureCoreAllowLegacyAgentCode is the
// readPlanForMutation-side counterpart to
// validateLoadedPlanStructureAllowLegacyAgentCode above: it lets a
// pre-v1.0.10 plan be loaded (not persisted) by mutation tools — e.g. so
// delete_plan_steps or update_agent_step can remove or repair the
// offending step — without also re-validating cross-step graph references
// that only the full read path checks. Every write still calls
// ValidatePlanStructure, so a legacy code item can never execute or be
// saved again.
func validateLoadedPlanStructureCoreAllowLegacyAgentCode(plan *PlanningResponse) error {
	return validateLoadedPlanStructureCoreWithOptions(plan, true)
}

func validateLoadedPlanStructureCoreWithOptions(plan *PlanningResponse, allowLegacyAgentCode bool) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if err := validatePlanStepIDs(plan.Steps); err != nil {
		return err
	}
	if err := validateStepIDUniqueness(plan); err != nil {
		return err
	}
	for i, step := range plan.Steps {
		if err := validateLoadedPlanStepWithOptions(step, i, allowLegacyAgentCode); err != nil {
			return fmt.Errorf("steps[%d] (id=%s): %w", i, step.GetID(), err)
		}
	}
	for i, step := range plan.OrphanSteps {
		if err := validateLoadedPlanStepWithOptions(step, i, allowLegacyAgentCode); err != nil {
			return fmt.Errorf("orphan_steps[%d] (id=%s): %w", i, step.GetID(), err)
		}
	}
	return nil
}

// PlanValidationError identifies a plan mutation that was rejected before it
// could persist an invalid workflow. Callers can use errors.As to distinguish
// this from storage/transport failures and surface a repairable conflict.
type PlanValidationError struct {
	Cause error
}

func (e *PlanValidationError) Error() string {
	if e == nil || e.Cause == nil {
		return "workflow plan validation failed"
	}
	return "workflow plan validation failed: " + e.Cause.Error()
}

func (e *PlanValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ValidatePlanStructure validates the exact representation that would be
// persisted. The JSON round-trip prevents in-memory interface values from
// bypassing normal plan decoding, and orphan references are resolved before
// graph validation just as they are during workflow loading.
func ValidatePlanStructure(plan *PlanningResponse) error {
	validationJSON, err := json.Marshal(plan)
	if err != nil {
		return &PlanValidationError{Cause: fmt.Errorf("failed to marshal plan: %w", err)}
	}
	var validationPlan PlanningResponse
	if err := json.Unmarshal(validationJSON, &validationPlan); err != nil {
		return &PlanValidationError{Cause: fmt.Errorf("failed to decode plan: %w", err)}
	}
	if err := resolvePlanOrphanStepRefs(&validationPlan); err != nil {
		return &PlanValidationError{Cause: err}
	}
	if err := validateLoadedPlanStructure(&validationPlan); err != nil {
		return &PlanValidationError{Cause: err}
	}
	if err := validateRunScopedRouteSources(&validationPlan); err != nil {
		return &PlanValidationError{Cause: err}
	}
	return nil
}

// validateRunScopedRouteSources prevents a per-run route decision from being
// mirrored through mutable workflow state. Shared route_source_file values are
// still supported when they are intentional durable inputs. The unsafe case is
// narrower: a prior step in the same plan already declares route_selection.json
// as its output, while the router points at a shared db/assets copy instead of
// depending on that run-scoped output.
//
// This check runs on persisted mutations, not legacy plan loading, so existing
// workflows remain repairable through the Builder without being made
// unopenable by a new validator.
func validateRunScopedRouteSources(plan *PlanningResponse) error {
	if plan == nil {
		return nil
	}

	priorRunScopedProducer := ""
	for _, step := range plan.Steps {
		if step == nil {
			continue
		}

		if router, ok := step.(routeSwitchStep); ok && priorRunScopedProducer != "" {
			source := filepath.ToSlash(strings.TrimSpace(router.GetRouteSourceFile()))
			if filepath.Base(source) == routeSelectionFileName && strings.HasPrefix(source, "db/assets/") {
				return fmt.Errorf(
					"routing step %q points at shared %q even though prior step %q produces run-scoped %s; use context_dependencies [%q] so parallel runs cannot overwrite one another",
					router.GetID(), source, priorRunScopedProducer, routeSelectionFileName, routeSelectionFileName,
				)
			}
		}

		if contextOutputMatchesDependency(step.GetContextOutput().String(), routeSelectionFileName) {
			priorRunScopedProducer = step.GetID()
		}
	}

	return nil
}

// nextStepIDSentinelEnd is the literal value that any next_step_id may
// take to mean "end the workflow here" — it is NOT required to match a
// real step ID in the plan.
const nextStepIDSentinelEnd = "end"

// collectKnownStepIDs walks main steps, orphan steps, and sub-agent steps
// inside todo_task predefined routes, returning every declared step ID
// the plan declares. The set is the legal universe for any
// next_step_id reference; anything outside it (other than the "end"
// sentinel) is a dangling reference that would surface at runtime
// only after the LLM had already been billed for the routing
// decision.
func collectKnownStepIDs(plan *PlanningResponse) map[string]struct{} {
	out := make(map[string]struct{})
	if plan == nil {
		return out
	}
	var walk func(steps []PlanStepInterface)
	walk = func(steps []PlanStepInterface) {
		for _, step := range steps {
			if id := step.GetID(); id != "" {
				out[id] = struct{}{}
			}
			switch s := step.(type) {
			case *AgentPlanStep:
				for _, route := range s.PredefinedRoutes {
					if route.SubAgentStep != nil {
						walk([]PlanStepInterface{route.SubAgentStep})
					}
				}
			}
		}
	}
	walk(plan.Steps)
	walk(plan.OrphanSteps)
	return out
}

// validateNextStepIDReferences enforces that every next_step_id
// emitted by deterministic routes references a
// step that actually exists in the plan (or is the "end" sentinel).
// Without this, a typo in plan.json silently goes to LLM execution;
// the routing call gets billed; the workflow then
// dies trying to look up the missing successor with a runtime error
// that is hard to attribute back to the original mistake.
func validateNextStepIDReferences(plan *PlanningResponse) error {
	known := collectKnownStepIDs(plan)
	issues := make([]string, 0)
	ref := func(stepID, fieldDesc, nextID string) {
		nextID = strings.TrimSpace(nextID)
		if nextID == "" || nextID == nextStepIDSentinelEnd {
			return
		}
		if _, ok := known[nextID]; !ok {
			issues = append(issues, fmt.Sprintf("%s in step %q points to missing step %q", fieldDesc, stepID, nextID))
		}
	}
	var walk func(steps []PlanStepInterface)
	walk = func(steps []PlanStepInterface) {
		for _, step := range steps {
			switch s := step.(type) {
			case *RegularPlanStep:
				ref(s.GetID(), "next_step_id", s.NextStepID)
			case *RoutingPlanStep:
				for _, route := range s.Routes {
					ref(s.GetID(), fmt.Sprintf("route %q.next_step_id", route.RouteID), route.NextStepID)
				}
			case *BranchPlanStep:
				for _, route := range s.Routes {
					ref(s.GetID(), fmt.Sprintf("route %q.next_step_id", route.RouteID), route.NextStepID)
				}
			case *AgentPlanStep:
				ref(s.GetID(), "next_step_id", s.NextStepID)
				for _, route := range s.PredefinedRoutes {
					if route.SubAgentStep != nil {
						walk([]PlanStepInterface{route.SubAgentStep})
					}
				}
			case *CrewPlanStep:
				ref(s.GetID(), "next_step_id", s.NextStepID)
			case *HumanInputPlanStep:
				ref(s.GetID(), "next_step_id", s.NextStepID)
				ref(s.GetID(), "if_yes_next_step_id", s.IfYesNextStepID)
				ref(s.GetID(), "if_no_next_step_id", s.IfNoNextStepID)
				optionKeys := make([]string, 0, len(s.OptionRoutes))
				for key := range s.OptionRoutes {
					optionKeys = append(optionKeys, key)
				}
				sort.Strings(optionKeys)
				for _, key := range optionKeys {
					ref(s.GetID(), fmt.Sprintf("option_routes[%q]", key), s.OptionRoutes[key])
				}
			}
		}
	}
	walk(plan.Steps)
	walk(plan.OrphanSteps)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf(
		"PLAN_GRAPH_INVALID: %d next-step reference(s) point to missing steps:\n- %s\nNo changes were saved. Update every listed route/next-step reference to an existing step ID or \"end\", then retry the mutation",
		len(issues), strings.Join(issues, "\n- "),
	)
}

// validateHumanInputStepFieldsTyped enforces that a human_input step's
// response-routing configuration is internally consistent. The runtime
// (resolveHumanInputNextStep) never fails on a misconfigured step — an
// unmatched option falls back to the default next_step_id and then to the
// next sequential step — so a bad config doesn't error at execution, it
// routes somewhere the builder didn't intend. Catch the unambiguous
// misconfigurations at plan time instead.
func validateHumanInputStepFieldsTyped(step *HumanInputPlanStep) error {
	if strings.TrimSpace(step.Question) == "" {
		return fmt.Errorf("human_input step (title: %q, ID: %s) is missing required question field", step.Title, step.ID)
	}
	responseType := strings.TrimSpace(step.ResponseType)
	switch responseType {
	case "", "text", "yesno", "multiple_choice":
	default:
		return fmt.Errorf("human_input step %q has unsupported response_type %q (use text, yesno, or multiple_choice)", step.ID, responseType)
	}
	if (step.IfYesNextStepID != "" || step.IfNoNextStepID != "") && responseType != "yesno" {
		return fmt.Errorf("human_input step %q sets if_yes_next_step_id/if_no_next_step_id but response_type is %q — those fields only apply to response_type \"yesno\"", step.ID, responseType)
	}
	if len(step.OptionRoutes) > 0 && responseType != "multiple_choice" {
		return fmt.Errorf("human_input step %q sets option_routes but response_type is %q — option_routes only applies to response_type \"multiple_choice\"", step.ID, responseType)
	}
	if responseType == "multiple_choice" {
		if len(step.Options) == 0 {
			return fmt.Errorf("human_input step %q has response_type \"multiple_choice\" but no options", step.ID)
		}
		optionValues := make(map[string]bool, len(step.Options))
		for _, opt := range step.Options {
			optionValues[opt] = true
		}
		for key := range step.OptionRoutes {
			if idx, err := strconv.Atoi(key); err == nil {
				if idx < 0 || idx >= len(step.Options) {
					return fmt.Errorf("human_input step %q option_routes key %q is out of range — the step has %d option(s) (indices 0-%d)", step.ID, key, len(step.Options), len(step.Options)-1)
				}
				continue
			}
			if !optionValues[key] {
				return fmt.Errorf("human_input step %q option_routes key %q matches neither an option index nor an option value", step.ID, key)
			}
		}
		// A partial map is legal when the default next_step_id catches the
		// rest; without one, picking an unmapped option silently continues to
		// the next sequential step — almost always a missing mapping rather
		// than a deliberate choice.
		if len(step.OptionRoutes) > 0 && strings.TrimSpace(step.NextStepID) == "" {
			for i, opt := range step.Options {
				if _, byIdx := step.OptionRoutes[strconv.Itoa(i)]; byIdx {
					continue
				}
				if _, byVal := step.OptionRoutes[opt]; byVal {
					continue
				}
				return fmt.Errorf("human_input step %q option %d (%q) has no option_routes entry and the step has no default next_step_id — selecting it would silently continue to the next sequential step; map the option or set next_step_id as the fallback", step.ID, i, opt)
			}
		}
	}
	return nil
}

// populateRuntimeFields populates runtime fields (AgentConfigs, etc.) on plan steps in-place
// This maintains type safety by working directly with plan step types
func populateRuntimeFields(typedStep PlanStepInterface, stepConfigs []StepConfig) error {
	// Match config by step ID
	var agentConfigs *AgentConfigs
	var validationSchemaOverride *ValidationSchema
	stepID := typedStep.GetID()
	if stepID == "" {
		return fmt.Errorf("step is missing required ID field. Step title: %q", typedStep.GetTitle())
	} else if stepConfigs != nil {
		agentConfigs = MatchStepConfigByID(stepID, stepConfigs)
		// Check for validation schema override in step_config.json
		for i := range stepConfigs {
			if stepConfigs[i].ID == stepID && stepConfigs[i].ValidationSchema != nil {
				validationSchemaOverride = stepConfigs[i].ValidationSchema
				break
			}
		}
	}

	// Use type switch to handle different step types
	switch step := typedStep.(type) {
	case *RegularPlanStep:
		// Regular step (may have loops)
		// Populate runtime field directly on plan step
		step.AgentConfigs = agentConfigs
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	case *HumanInputPlanStep:
		// Human input step: no execution, validation, or learning - just asks question and blocks.
		// Force learnings_access="none" + empty objective so this step is never included
		// in the read or write path regardless of defaults. Under the learnings_access
		// split, the default is "read" (step sees _global/ SKILL.md) — which is
		// meaningless for a human-input step since it has no LLM prompt.
		if agentConfigs == nil {
			agentConfigs = &AgentConfigs{LearningsAccess: LearningsAccessNone}
		} else if strings.TrimSpace(agentConfigs.LearningObjective) != "" || agentConfigs.LearningsAccess != LearningsAccessNone {
			cleared := *agentConfigs
			cleared.LearningObjective = ""
			cleared.LearningsAccess = LearningsAccessNone
			agentConfigs = &cleared
		}

		// Populate runtime field directly on plan step
		step.AgentConfigs = agentConfigs
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	case *RoutingPlanStep:
		// Routing step: similar to decision step - evaluates a question and routes to one of N next steps
		// Populate runtime field directly on plan step
		step.AgentConfigs = agentConfigs
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	case *BranchPlanStep:
		// Branch step: same deterministic switch mechanics as routing, just the
		// small in-flow decision (PLAT-259). Populate runtime field directly.
		step.AgentConfigs = agentConfigs
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	case *AgentPlanStep:
		for i := range step.PredefinedRoutes {
			route := &step.PredefinedRoutes[i]
			if route.SubAgentStep != nil {
				if err := populateRuntimeFields(route.SubAgentStep, stepConfigs); err != nil {
					return fmt.Errorf("failed to populate sub-agent step for route '%s': %w", route.RouteID, err)
				}
			}
		}
		step.AgentConfigs = agentConfigs
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	case *CrewPlanStep:
		// Crew steps run no local agent, so there are no AgentConfigs to
		// populate; a step_config validation schema override still applies.
		if validationSchemaOverride != nil {
			step.ValidationSchema = validationSchemaOverride
		}
		return nil

	default:
		return fmt.Errorf("unknown step type: %T", typedStep)
	}
}

// IsPlanModificationTool checks if a tool name is a plan modification tool.
// Kept on 2026-09-22: the CheckAndEmitPlanUpdateEvent consumer was removed
// (never called), but four tests pin this classification as the definition
// of "plan mutation" (change_step_type, declared-execution-mode migrations,
// agent compat).
func IsPlanModificationTool(name string) bool {
	return name == "add_step" || name == "update_step" || name == "manage_step_route" || name == "manage_group" || name == "maintain_plan" || name == "update_scripted_step" || name == "update_routing_step" || name == "update_branch_step" || name == "update_human_input_step" || name == "update_todo_task_step" || name == "update_orchestrator_step" || name == "update_agent_step" || name == "update_crew_step" || name == "delete_plan_steps" || name == "add_scripted_step" || name == "add_routing_step" || name == "add_branch_step" || name == "add_human_input_step" || name == "add_todo_task_step" || name == "add_orchestrator_step" || name == "add_agent_step" || name == "add_crew_step" ||
		name == "update_validation_schema" ||
		name == "add_todo_task_route" || name == "update_todo_task_route" || name == "delete_todo_task_route" ||
		name == "add_agent_route" || name == "update_agent_route" || name == "delete_agent_route" || name == "migrate_orchestrator_step_type" ||
		name == "change_step_type" || name == "migrate_declared_execution_mode" || name == "strip_declared_execution_mode"
}

// IsStepConfigModificationTool checks if a tool name is a step_config modification tool.
// Kept alongside IsPlanModificationTool (same reason).
func IsStepConfigModificationTool(name string) bool {
	return name == "update_step_config_tools"
}
