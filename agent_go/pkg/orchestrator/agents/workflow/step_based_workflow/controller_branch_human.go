package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Human-decided branch (PLAT-294 follow-up).
//
// A branch step with route_source: "human" is the successor to a yesno /
// fixed question form: the routes ARE the options. The switch
// stays deterministic in every way a schedule or caller can see -- a
// preseeded route_selection.json, route_selections, route_source_file or a
// context_dependencies entry still wins, exactly as for any other branch --
// and only when none of those supplied the answer does the run stop and ask
// the person, through a HumanFeedbackStore prompt.
// Unattended runs never block: skip-human mode falls back to
// default_route_id or fails with an actionable error.

const branchRouteSourceHuman = "human"

// routeSwitchStep.GetRouteSource: only a branch can ask a person; routing is
// the mode selector and is always preseeded (route_selections / default).
func (r *RoutingPlanStep) GetRouteSource() string { return "" }
func (b *BranchPlanStep) GetRouteSource() string  { return b.RouteSource }

// isHumanRoutedBranch reports whether the step asks a person when no route
// selection was supplied up front.
func isHumanRoutedBranch(step routeSwitchStep) bool {
	return step != nil && strings.EqualFold(strings.TrimSpace(step.GetRouteSource()), branchRouteSourceHuman)
}

// requestHumanBranchChoice is the one blocking call, kept swappable so the
// resolution logic around it is unit-testable without the global feedback
// store. Production wires it to RequestMultipleChoiceFeedback, which returns
// "option<N>" for the N-th option (or raw text when the UI sends one).
var requestHumanBranchChoice = func(hcpo *StepBasedWorkflowOrchestrator, ctx context.Context, requestID, question string, options []string) (string, error) {
	return hcpo.RequestMultipleChoiceFeedback(ctx, requestID, question, options, "", hcpo.getSessionID(), hcpo.getWorkflowID())
}

// humanBranchOptionLabels returns the labels shown to the person: route_name,
// falling back to route_id.
func humanBranchOptionLabels(routes []RoutingRoute) []string {
	labels := make([]string, len(routes))
	for i, route := range routes {
		label := strings.TrimSpace(route.RouteName)
		if label == "" {
			label = strings.TrimSpace(route.RouteID)
		}
		labels[i] = label
	}
	return labels
}

// resolveHumanBranchAnswer maps whatever came back -- "option<N>", an option
// index, a route_name, a route_id, or a route's next_step_id -- to a route_id.
func resolveHumanBranchAnswer(routes []RoutingRoute, answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", fmt.Errorf("empty answer")
	}
	indexCandidate := answer
	if strings.HasPrefix(strings.ToLower(answer), "option") {
		indexCandidate = strings.TrimSpace(answer[len("option"):])
	}
	var index int
	if _, err := fmt.Sscanf(indexCandidate, "%d", &index); err == nil && fmt.Sprintf("%d", index) == indexCandidate {
		if index < 0 || index >= len(routes) {
			return "", fmt.Errorf("option index %d out of range (0-%d)", index, len(routes)-1)
		}
		return routes[index].RouteID, nil
	}
	for _, route := range routes {
		if strings.EqualFold(strings.TrimSpace(route.RouteName), answer) {
			return route.RouteID, nil
		}
	}
	return resolveRouteSelectionValue(routes, answer)
}

// resolveHumanBranchSelection is reached only after every preseeded source
// (route_selection.json, route_source_file, context_dependencies) came up
// empty for a route_source: "human" branch. Order: a workshop-supplied test
// answer, then the unattended fallback, then the real prompt.
func (hcpo *StepBasedWorkflowOrchestrator) resolveHumanBranchSelection(
	ctx context.Context,
	step routeSwitchStep,
	stepIndex int,
	execCtx *ExecutionContext,
) (*deterministicRoutingSelection, error) {
	routes := step.GetRoutes()

	// Workshop: execute_step(step_id, human_input=...) pre-supplies the answer.
	if execCtx != nil && strings.TrimSpace(execCtx.WorkshopHumanInput) != "" {
		routeID, err := resolveHumanBranchAnswer(routes, execCtx.WorkshopHumanInput)
		if err != nil {
			return nil, fmt.Errorf("branch step %q: workshop human_input %q does not name a route: %w", step.GetID(), execCtx.WorkshopHumanInput, err)
		}
		return &deterministicRoutingSelection{
			SelectedRouteID: routeID,
			Reasoning:       fmt.Sprintf("Workshop-supplied human answer %q selected route %q.", strings.TrimSpace(execCtx.WorkshopHumanInput), routeID),
			SourceKind:      "workshop_human_input",
			RawValue:        strings.TrimSpace(execCtx.WorkshopHumanInput),
		}, nil
	}

	// Unattended run: never block. default_route_id is the declared safe choice.
	if hcpo.IsSkipHumanInput() {
		defaultRouteID := strings.TrimSpace(step.GetDefaultRouteID())
		if defaultRouteID == "" {
			return nil, fmt.Errorf("branch step %q asks a human (route_source: human) but this run is unattended and no route was supplied: pass route_selections[%q] when starting the run, or set default_route_id on the step as the safe unattended choice", step.GetID(), step.GetID())
		}
		routeID, err := resolveRouteSelectionValue(routes, defaultRouteID)
		if err != nil {
			return nil, fmt.Errorf("invalid default_route_id for branch step %q: %w", step.GetID(), err)
		}
		return &deterministicRoutingSelection{
			SelectedRouteID: routeID,
			Reasoning:       fmt.Sprintf("Unattended run; human-decided branch used default_route_id %q.", routeID),
			SourceKind:      "default_route_id",
			RawValue:        defaultRouteID,
		}, nil
	}

	// Interactive run: ask. Same store, same UI as a multiple_choice human_input.
	question := ResolveVariables(step.GetRoutingQuestionText(), hcpo.variableValues)
	options := humanBranchOptionLabels(routes)
	requestID := fmt.Sprintf("branch_step_%d_%d", stepIndex+1, time.Now().UnixNano())
	hcpo.GetLogger().Info(fmt.Sprintf("🙋 Branch step %q asks a human: %s (%d options)", step.GetID(), question, len(options)))
	answer, err := requestHumanBranchChoice(hcpo, ctx, requestID, question, options)
	if err != nil {
		return nil, fmt.Errorf("branch step %q: failed to get human decision: %w", step.GetID(), err)
	}
	routeID, err := resolveHumanBranchAnswer(routes, answer)
	if err != nil {
		return nil, fmt.Errorf("branch step %q: human answer %q does not name a route: %w", step.GetID(), answer, err)
	}
	return &deterministicRoutingSelection{
		SelectedRouteID: routeID,
		Reasoning:       fmt.Sprintf("Human selected route %q.", routeID),
		SourceKind:      "human",
		RawValue:        strings.TrimSpace(answer),
	}, nil
}
