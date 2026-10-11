package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
)

// executeRoutingStep executes a deterministic routing step by:
// 1. Reading route_selection.json or using default_route_id to select a route
// 2. Returning the selected route ID for routing (handled by main execution loop)
//
// Returns: (selectedRouteID string, executionResult string, error)
func (hcpo *StepBasedWorkflowOrchestrator) executeRoutingStep(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	progress *StepProgress,
	previousContextFiles []string,
	iteration int,
	execCtx *ExecutionContext,
	allSteps []PlanStepInterface,
	previousExecutionResults []string,
) (string, string, error) {
	routingStep, ok := step.(routeSwitchStep)
	if !ok {
		return "", "", fmt.Errorf("step is not a routing/branch step")
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🔀 Executing routing step %d: %s", stepIndex+1, step.GetTitle()))

	// Validate required fields
	if routingStep.GetRoutingQuestionText() == "" {
		return "", "", fmt.Errorf("routing step %d (%s) is missing required routing_question/branch_question field", stepIndex+1, step.GetTitle())
	}
	if len(routingStep.GetRoutes()) < 2 {
		return "", "", fmt.Errorf("routing step %d (%s) must have at least 2 routes, got %d", stepIndex+1, step.GetTitle(), len(routingStep.GetRoutes()))
	}
	if strings.TrimSpace(routingStep.GetDescription()) != "" {
		return "", "", fmt.Errorf("routing step %d (%s) sets description, but routing/branch is deterministic-only; move any probe or judgment into a prior agent step that writes %s, then point the step at that file via route_source_file or context_dependencies", stepIndex+1, step.GetTitle(), routeSelectionFileName)
	}

	// Emit step_started event
	routingStepPath := fmt.Sprintf("step-%d", stepIndex+1)
	hcpo.emitStepStartedEvent(ctx, step, stepIndex, routingStepPath)

	// Calculate run number
	runNumber := 1
	if progress.RoutingEvaluationCounts != nil {
		totalEvals := 0
		for _, route := range routingStep.GetRoutes() {
			key := fmt.Sprintf("%s:%s", step.GetID(), route.RouteID)
			totalEvals += progress.RoutingEvaluationCounts[key]
		}
		runNumber = totalEvals + 1
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🔀 Resolving routing step: %s (run %d) - deterministic file mode", step.GetTitle(), runNumber))

	// Ensure step execution folder exists
	runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, step.GetID(), routingStepPath)
	if err := hcpo.ensureStepExecutionFolderExists(ctx, stepExecutionPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to ensure routing step execution folder exists: %v", err))
	}

	selection, err := hcpo.resolveDeterministicRoutingSelection(ctx, routingStep, stepIndex, routingStepPath, allSteps, execCtx)
	if err != nil {
		hcpo.GetLogger().Error(fmt.Sprintf("❌ Failed to resolve routing step %d deterministically: %v", stepIndex+1, err), nil)
		hcpo.EmitOrchestratorAgentError(ctx, "workflow", "routing-step-deterministic", fmt.Sprintf("Resolve routing step: %s", step.GetTitle()), err.Error(), stepIndex, iteration)
		return "", "", fmt.Errorf("failed to resolve routing step deterministically: %w", err)
	}
	routingResponse := selection.routingResponse()
	selectedRouteID := routingResponse.SelectedRouteID

	// Routing and branch decisions are execution outputs, so persist the
	// resolved decision beside every other step artifact under the current run.
	// A shared route_source_file may be used as an input, but it must never be
	// the only record of what this particular iteration selected.
	resolvedSelection := map[string]interface{}{
		"select_route":      selectedRouteID,
		"selected_route_id": selectedRouteID,
		"source_kind":       selection.SourceKind,
		"source_path":       selection.SourcePath,
		"raw_value":         selection.RawValue,
		"resolved_at":       time.Now().UTC().Format(time.RFC3339),
	}
	resolvedSelectionJSON, err := json.MarshalIndent(resolvedSelection, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal run-scoped route selection: %w", err)
	}
	resolvedSelectionPath := fmt.Sprintf("%s/%s", stepExecutionPath, routeSelectionFileName)
	if err := hcpo.WriteWorkspaceFile(ctx, resolvedSelectionPath, string(resolvedSelectionJSON)+"\n"); err != nil {
		return "", "", fmt.Errorf("failed to persist run-scoped route selection: %w", err)
	}

	// Store result on step struct
	routingStep.SetSelectedRouteID(selectedRouteID)
	routingStep.SetRoutingResponse(routingResponse)

	hcpo.GetLogger().Info(fmt.Sprintf("✅ Routing step evaluated deterministically: selected route=%s source=%s", selectedRouteID, selection.SourceKind))

	// Emit routing_evaluated event
	hcpo.emitRoutingEvaluatedEvent(ctx, step, stepIndex, routingStepPath, routingResponse, routingStep.GetRoutes(), allSteps)

	// Save evaluation result to logs
	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}
	validationFolderPath := getValidationFolderPath(validationWorkspacePath, step.GetID(), routingStepPath)
	routingEvaluationFilePath := fmt.Sprintf("%s/routing-evaluation.json", validationFolderPath)

	routeNextStepIDs := make(map[string]string)
	for _, route := range routingStep.GetRoutes() {
		routeNextStepIDs[route.RouteID] = route.NextStepID
	}

	routingEvalResponse := map[string]interface{}{
		"step_index": stepIndex + 1,
		"step_path":  routingStepPath,
		// step_type is the step's real plan.json type ("routing" or "branch")
		// AT EXECUTION TIME, persisted into the artifact itself rather than
		// left for a reader to re-derive from the current plan -- if the step
		// is later reclassified (routing<->branch), this historical run's
		// evidence must keep reporting what it actually was when it ran, not
		// what the step is typed as today. handleGetExecutionLogs prefers
		// this field over the live plan.json lookup for exactly that reason.
		"step_type":         string(step.StepType()),
		"routing_step_id":   step.GetID(),
		"routing_question":  routingStep.GetRoutingQuestionText(),
		"selected_route_id": selectedRouteID,
		"routing_reasoning": routingResponse.Reasoning,
		"route_selection": map[string]interface{}{
			"source_kind": selection.SourceKind,
			"source_path": selection.SourcePath,
			"raw_value":   selection.RawValue,
		},
		"route_next_steps": routeNextStepIDs,
		"timestamp":        time.Now().Format(time.RFC3339),
	}

	routingJSON, err := json.MarshalIndent(routingEvalResponse, "", "  ")
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal routing evaluation response: %v", err))
	} else {
		if err := hcpo.WriteWorkspaceFile(ctx, routingEvaluationFilePath, string(routingJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write routing evaluation response: %v", err))
		}
	}

	// Emit step_finished event
	hcpo.emitStepFinishedEvent(ctx, step, stepIndex, routingStepPath)

	return selectedRouteID, "", nil
}

// emitRoutingEvaluatedEvent emits a routing_evaluated event
func (hcpo *StepBasedWorkflowOrchestrator) emitRoutingEvaluatedEvent(ctx context.Context, step PlanStepInterface, stepIndex int, stepPath string, routingResponse *RoutingResponse, routes []RoutingRoute, allSteps []PlanStepInterface) {
	bridge := hcpo.GetContextAwareBridge()
	if bridge == nil {
		return
	}

	stepTitle := step.GetTitle()
	if stepTitle == "" {
		stepTitle = fmt.Sprintf("Step %d", stepIndex+1)
	}
	stepId := step.GetID()
	if stepId == "" {
		stepId = fmt.Sprintf("step-%d", stepIndex+1)
	}

	nextStepTypes := routingNextStepTypesByID(allSteps)
	eventRoutes := make([]RoutingRouteEvent, len(routes))
	for i, route := range routes {
		eventRoutes[i] = RoutingRouteEvent{
			RouteID:      route.RouteID,
			RouteName:    route.RouteName,
			Condition:    route.Condition,
			NextStepID:   route.NextStepID,
			NextStepType: nextStepTypes[strings.TrimSpace(route.NextStepID)],
		}
	}

	evaluatedEvent := &RoutingEvaluatedEvent{
		BaseEventData: baseevents.BaseEventData{
			Timestamp: time.Now(),
			Component: "orchestrator",
		},
		StepID:    stepId,
		StepIndex: stepIndex,
		StepTitle: stepTitle,
		StepPath:  stepPath,
		RoutingQuestion: func() string {
			if routingStep, ok := step.(routeSwitchStep); ok {
				return routingStep.GetRoutingQuestionText()
			}
			return ""
		}(),
		RoutingResponse: RoutingResponseEvent{
			SelectedRouteID: routingResponse.SelectedRouteID,
			Reasoning:       routingResponse.Reasoning,
		},
		Routes:        eventRoutes,
		RunFolder:     hcpo.selectedRunFolder,
		WorkspacePath: hcpo.GetWorkspacePath(),
	}

	agentEvent := &baseevents.AgentEvent{
		Type:      events.RoutingEvaluated,
		Timestamp: time.Now(),
		Data:      evaluatedEvent,
	}

	if err := bridge.HandleEvent(ctx, agentEvent); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to emit routing evaluated event: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("📤 Emitted routing_evaluated event for step %d: %s (route=%s)", stepIndex+1, stepTitle, routingResponse.SelectedRouteID))
	}
}

func routingNextStepTypesByID(steps []PlanStepInterface) map[string]string {
	nextStepTypes := make(map[string]string, len(steps))
	for _, step := range steps {
		if step == nil {
			continue
		}
		stepID := strings.TrimSpace(step.GetID())
		if stepID == "" {
			continue
		}
		nextStepTypes[stepID] = string(step.StepType())
	}
	return nextStepTypes
}
