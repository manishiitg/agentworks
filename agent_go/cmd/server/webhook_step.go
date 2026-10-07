package server

import (
	"context"
	"fmt"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"strings"
)

type webhookStepOption struct {
	StepID string `json:"step_id"`
	Title  string `json:"title"`
}

func workflowWebhookSteps(ctx context.Context, path string) ([]webhookStepOption, error) {
	manifest, _, err := ReadWorkflowManifest(ctx, path)
	if err != nil {
		return nil, err
	}
	if isPythonRelay(manifest) {
		return []webhookStepOption{}, nil
	}
	plan, err := readPlanFromWorkspace(ctx, path)
	if err != nil {
		return nil, err
	}
	options := []webhookStepOption{}
	for _, step := range plan.Steps {
		if step == nil {
			continue
		}
		if _, err := stepworkflow.WebhookStepIndex(plan.Steps, step.GetID()); err == nil {
			options = append(options, webhookStepOption{StepID: step.GetID(), Title: step.GetTitle()})
		}
	}
	return options, nil
}

func validateWebhookTarget(ctx context.Context, path, stepID string, routes map[string]string) error {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" {
		return validateWebhookRoutes(ctx, path, routes)
	}
	if len(routes) != 0 {
		return fmt.Errorf("choose a step or route selections, not both")
	}
	manifest, _, err := ReadWorkflowManifest(ctx, path)
	if err != nil {
		return err
	}
	if isPythonRelay(manifest) {
		return fmt.Errorf("Python Relays run relay.py; workflow step targets are not supported")
	}
	plan, err := readPlanFromWorkspace(ctx, path)
	if err != nil {
		return err
	}
	_, err = stepworkflow.WebhookStepIndex(plan.Steps, stepID)
	return err
}
