package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

const routeSelectionFileName = "route_selection.json"

type deterministicRoutingSelection struct {
	SelectedRouteID string
	Reasoning       string
	SourcePath      string
	SourceKind      string
	RawValue        string
}

func (s *deterministicRoutingSelection) routingResponse() *RoutingResponse {
	if s == nil {
		return nil
	}
	return &RoutingResponse{
		SelectedRouteID: s.SelectedRouteID,
		Reasoning:       s.Reasoning,
	}
}

func parseRouteSelectionPayload(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("route selection file is empty")
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return "", fmt.Errorf("route selection file must be valid JSON: %w", err)
	}

	for _, key := range []string{"select_route", "route_id", "selected_route_id"} {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", key)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("%s must not be empty", key)
		}
		return value, nil
	}

	return "", fmt.Errorf("route selection file must contain select_route, route_id, or selected_route_id")
}

func resolveRouteSelectionValue(routes []RoutingRoute, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("route selection value is empty")
	}

	for _, route := range routes {
		if value == strings.TrimSpace(route.RouteID) {
			return route.RouteID, nil
		}
	}

	var nextStepMatches []RoutingRoute
	for _, route := range routes {
		if value == strings.TrimSpace(route.NextStepID) {
			nextStepMatches = append(nextStepMatches, route)
		}
	}
	if len(nextStepMatches) == 1 {
		return nextStepMatches[0].RouteID, nil
	}
	if len(nextStepMatches) > 1 {
		routeIDs := make([]string, 0, len(nextStepMatches))
		for _, route := range nextStepMatches {
			routeIDs = append(routeIDs, route.RouteID)
		}
		return "", fmt.Errorf("route selection value %q matches multiple next_step_id routes: %s", value, strings.Join(routeIDs, ", "))
	}

	available := make([]string, 0, len(routes))
	for _, route := range routes {
		available = append(available, fmt.Sprintf("%s -> %s", route.RouteID, route.NextStepID))
	}
	return "", fmt.Errorf("route selection value %q does not match any route_id or unique next_step_id (available: %s)", value, strings.Join(available, ", "))
}

func isWorkspaceNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "not found") ||
		strings.Contains(errText, "no such file") ||
		strings.Contains(errText, "does not exist")
}

func (hcpo *StepBasedWorkflowOrchestrator) routingStepExecutionPath(step routeSwitchStep, stepIndex int) string {
	stepPath := fmt.Sprintf("step-%d", stepIndex+1)
	return hcpo.routingStepExecutionPathForStepPath(step, stepIndex, stepPath)
}

func (hcpo *StepBasedWorkflowOrchestrator) routingStepExecutionPathForStepPath(step routeSwitchStep, stepIndex int, stepPath string) string {
	runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	return getExecutionFolderPath(executionWorkspacePath, step.GetID(), stepPath)
}

func (hcpo *StepBasedWorkflowOrchestrator) seedRouteSelectionsForRun(ctx context.Context, steps []PlanStepInterface) error {
	if hcpo.executionOptions == nil {
		// PLAT-066: proven at least once that a caller-supplied route_selections
		// payload can fail to reach this point even though it was parsed
		// correctly at the run_full_workflow tool boundary. This log line is the
		// only signal that distinguishes "caller genuinely passed nothing" from
		// "something upstream dropped it" — the two look identical from here
		// otherwise, and the router step then silently falls back to its
		// default_route_id with no error anywhere in the chain.
		hcpo.GetLogger().Info("🔀 seedRouteSelectionsForRun: executionOptions is nil at seed time (no route selection possible even if the caller supplied one)")
		return nil
	}
	if len(hcpo.executionOptions.RouteSelections) == 0 {
		hcpo.GetLogger().Info("🔀 seedRouteSelectionsForRun: executionOptions present but RouteSelections is empty at seed time")
		return nil
	}
	if hcpo.selectedRunFolder == "" {
		return fmt.Errorf("run folder not resolved - cannot seed route selections")
	}

	routingSteps := make(map[string]struct {
		step  routeSwitchStep
		index int
	})
	for i, step := range steps {
		routingStep, ok := step.(routeSwitchStep)
		if !ok {
			continue
		}
		stepID := strings.TrimSpace(routingStep.GetID())
		if stepID == "" {
			continue
		}
		routingSteps[stepID] = struct {
			step  routeSwitchStep
			index int
		}{step: routingStep, index: i}
	}

	for stepID, rawValue := range hcpo.executionOptions.RouteSelections {
		stepID = strings.TrimSpace(stepID)
		rawValue = strings.TrimSpace(rawValue)
		routingStepInfo, ok := routingSteps[stepID]
		if !ok {
			return fmt.Errorf("route_selections references unknown routing step %q", stepID)
		}
		selectedRouteID, err := resolveRouteSelectionValue(routingStepInfo.step.GetRoutes(), rawValue)
		if err != nil {
			return fmt.Errorf("invalid route_selections[%q]: %w", stepID, err)
		}

		stepExecutionPath := hcpo.routingStepExecutionPath(routingStepInfo.step, routingStepInfo.index)
		if err := hcpo.ensureStepExecutionFolderExists(ctx, stepExecutionPath); err != nil {
			return fmt.Errorf("failed to create routing step folder for %q: %w", stepID, err)
		}

		payload := map[string]string{"select_route": selectedRouteID}
		content, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal route selection for %q: %w", stepID, err)
		}
		routeFilePath := filepath.Join(stepExecutionPath, routeSelectionFileName)
		if err := hcpo.WriteWorkspaceFile(ctx, routeFilePath, string(content)); err != nil {
			return fmt.Errorf("failed to seed route selection for %q: %w", stepID, err)
		}
		hcpo.GetLogger().Info(fmt.Sprintf("🔀 Seeded deterministic route selection for %s: %s -> %s", stepID, rawValue, selectedRouteID))
	}

	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveDeterministicRoutingSelection(
	ctx context.Context,
	routingStep routeSwitchStep,
	stepIndex int,
	routingStepPath string,
	allSteps []PlanStepInterface,
	execCtx *ExecutionContext,
) (*deterministicRoutingSelection, error) {
	if routingStep == nil {
		return nil, fmt.Errorf("routing step is nil")
	}

	// Relay decisions use the same branch executor, but select a route from
	// structured trigger input or a committed prior step result. A configured
	// value path is authoritative: a missing value must not silently take a
	// default or pick up a stale route_selection.json from another attempt.
	if branch, ok := routingStep.(*BranchPlanStep); ok && strings.TrimSpace(branch.ValuePath) != "" {
		valuePath := strings.TrimSpace(branch.ValuePath)
		if !authoredPromptVariable.MatchString(valuePath) || authoredPromptVariable.FindString(valuePath) != valuePath {
			return nil, fmt.Errorf("branch %q value_path must be one {{input.field}} or {{steps.id.output.field}} reference", branch.ID)
		}
		value, err := hcpo.renderAuthoredPrompt(ctx, valuePath)
		if err != nil {
			return nil, fmt.Errorf("branch %q value_path: %w", branch.ID, err)
		}
		routeID, matched := branch.ValueCases[value]
		if !matched {
			routeID = branch.DefaultRouteID
		}
		if routeID == "" {
			return nil, fmt.Errorf("branch %q has no route for value %q and no default_route_id", branch.ID, value)
		}
		selectedRouteID, err := resolveRouteSelectionValue(branch.Routes, routeID)
		if err != nil {
			return nil, fmt.Errorf("branch %q value %q: %w", branch.ID, value, err)
		}
		return &deterministicRoutingSelection{
			SelectedRouteID: selectedRouteID,
			Reasoning:       fmt.Sprintf("Value %q selected route %q.", value, selectedRouteID),
			SourceKind:      "value_path",
			RawValue:        value,
		}, nil
	}

	for _, ownRouteFilePath := range hcpo.routingStepOwnRouteFileCandidates(routingStep, stepIndex, routingStepPath) {
		if selection, found, err := hcpo.readDeterministicRoutingSource(ctx, routingStep, ownRouteFilePath, "routing step preseed"); err != nil {
			return nil, err
		} else if found {
			return selection, nil
		}
	}

	if source := strings.TrimSpace(routingStep.GetRouteSourceFile()); source != "" {
		for _, candidate := range hcpo.resolveRouteSourceCandidates(ctx, source, stepIndex, routingStepPath, allSteps) {
			if selection, found, err := hcpo.readDeterministicRoutingSource(ctx, routingStep, candidate, "route_source_file"); err != nil {
				return nil, err
			} else if found {
				return selection, nil
			}
		}
		return nil, fmt.Errorf("routing step %q route_source_file %q was not found", routingStep.GetID(), source)
	}

	for _, dep := range routingStep.GetContextDependencies() {
		dep = strings.TrimSpace(dep)
		if !isRouteSelectionDependency(dep) {
			continue
		}
		for _, candidate := range hcpo.resolveRouteSourceCandidates(ctx, dep, stepIndex, routingStepPath, allSteps) {
			if selection, found, err := hcpo.readDeterministicRoutingSource(ctx, routingStep, candidate, "context_dependencies"); err != nil {
				return nil, err
			} else if found {
				return selection, nil
			}
		}
	}

	// A human-decided branch: nothing was preseeded, so ask (or, unattended,
	// fall back to default_route_id). Sits ahead of the plain default so an
	// interactive run really asks instead of silently taking the default.
	if isHumanRoutedBranch(routingStep) {
		return hcpo.resolveHumanBranchSelection(ctx, routingStep, stepIndex, execCtx)
	}

	if defaultRouteID := strings.TrimSpace(routingStep.GetDefaultRouteID()); defaultRouteID != "" {
		selectedRouteID, err := resolveRouteSelectionValue(routingStep.GetRoutes(), defaultRouteID)
		if err != nil {
			return nil, fmt.Errorf("invalid default_route_id for routing step %q: %w", routingStep.GetID(), err)
		}
		return &deterministicRoutingSelection{
			SelectedRouteID: selectedRouteID,
			Reasoning:       fmt.Sprintf("No %s found; using default_route_id %q.", routeSelectionFileName, selectedRouteID),
			SourceKind:      "default_route_id",
			RawValue:        defaultRouteID,
		}, nil
	}

	return nil, fmt.Errorf("routing step %q requires %s or default_route_id", routingStep.GetID(), routeSelectionFileName)
}

func (hcpo *StepBasedWorkflowOrchestrator) routingStepOwnRouteFileCandidates(routingStep routeSwitchStep, stepIndex int, routingStepPath string) []string {
	if routingStepPath == "" {
		routingStepPath = fmt.Sprintf("step-%d", stepIndex+1)
	}

	return []string{
		filepath.Join(hcpo.routingStepExecutionPathForStepPath(routingStep, stepIndex, routingStepPath), routeSelectionFileName),
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveRouteSourceCandidates(
	ctx context.Context,
	source string,
	stepIndex int,
	routingStepPath string,
	allSteps []PlanStepInterface,
) []string {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	if filepath.IsAbs(source) || strings.Contains(source, "/") {
		return []string{source}
	}

	runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
	docsRoot := GetPromptDocsRoot()
	resolved := hcpo.resolveDependencyPathsWithWorkspace(ctx, []string{source}, stepIndex, routingStepPath, allSteps, executionWorkspacePath, docsRoot, hcpo.variableValues)
	if len(resolved) == 0 {
		return []string{source}
	}
	return resolved
}

func (hcpo *StepBasedWorkflowOrchestrator) readDeterministicRoutingSource(
	ctx context.Context,
	routingStep routeSwitchStep,
	sourcePath string,
	sourceKind string,
) (*deterministicRoutingSelection, bool, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil, false, nil
	}

	content, err := hcpo.ReadWorkspaceFile(ctx, sourcePath)
	if err != nil {
		if isWorkspaceNotFoundErr(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read route selection source %q: %w", sourcePath, err)
	}

	rawValue, err := parseRouteSelectionPayload(content)
	if err != nil {
		return nil, true, fmt.Errorf("invalid route selection source %q: %w", sourcePath, err)
	}
	selectedRouteID, err := resolveRouteSelectionValue(routingStep.GetRoutes(), rawValue)
	if err != nil {
		return nil, true, fmt.Errorf("invalid route selection source %q: %w", sourcePath, err)
	}

	return &deterministicRoutingSelection{
		SelectedRouteID: selectedRouteID,
		Reasoning:       fmt.Sprintf("Selected deterministically from %s (%s): %s.", sourceKind, sourcePath, rawValue),
		SourcePath:      sourcePath,
		SourceKind:      sourceKind,
		RawValue:        rawValue,
	}, true, nil
}

func isRouteSelectionDependency(dep string) bool {
	dep = strings.TrimSpace(dep)
	if dep == "" {
		return false
	}
	return filepath.Base(dep) == routeSelectionFileName
}
