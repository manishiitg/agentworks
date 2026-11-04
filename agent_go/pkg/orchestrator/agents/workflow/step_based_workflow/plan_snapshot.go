package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

type executionPlanContextKey struct{}

// withExecutionPlan scopes a loaded plan to one execution context. Queries and
// subsequent dispatches never replace this snapshot through controller state.
// Runtime fields are populated by the owner before execution starts.
func withExecutionPlan(ctx context.Context, plan *PlanningResponse) context.Context {
	return context.WithValue(ctx, executionPlanContextKey{}, plan)
}

var positionalStepReference = regexp.MustCompile(`(?i)^(?:step[- ]?)?[0-9]+$`)
var planRevisionReference = regexp.MustCompile(`^plan-[a-f0-9]{64}$`)

func isPositionalStepReference(id string) bool { return positionalStepReference.MatchString(id) }

// readRunPlanSnapshot never substitutes today's plan for historical evidence.
func (hcpo *StepBasedWorkflowOrchestrator) readRunPlanSnapshot(ctx context.Context, runFolder string) (*PlanningResponse, error) {
	if runFolder == "" {
		return nil, fmt.Errorf("no run folder selected")
	}
	content, err := hcpo.ReadWorkspaceFile(ctx, workflowRunMetadataPath(runFolder))
	if err != nil {
		return nil, err
	}
	var metadata struct {
		PlanRevision string `json:"plan_revision"`
	}
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return nil, err
	}
	if !planRevisionReference.MatchString(metadata.PlanRevision) {
		return nil, fmt.Errorf("run has no valid retained plan revision")
	}
	content, err = hcpo.ReadWorkspaceFile(ctx, planRevisionDirectory+"/"+metadata.PlanRevision+".json")
	if err != nil {
		return nil, err
	}
	var revision executablePlanRevision
	if err := json.Unmarshal([]byte(content), &revision); err != nil {
		return nil, err
	}
	id, _, err := planRevisionForFiles(revision.Files)
	if err != nil || id != metadata.PlanRevision || revision.RevisionID != id {
		return nil, fmt.Errorf("run plan revision content does not match its identity")
	}
	path := "planning/plan.json"
	if revision.Files[path] == nil {
		return nil, fmt.Errorf("run revision has no %s", path)
	}
	encoded, err := json.Marshal(revision.Files[path])
	if err != nil {
		return nil, err
	}
	var plan PlanningResponse
	configPath := "planning/step_config.json"
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return nil, err
	}
	if err := resolvePlanOrphanStepRefs(&plan); err != nil {
		return nil, err
	}
	// Recovery must not combine historical descriptions with today's config.
	if rawConfig := revision.Files[configPath]; rawConfig != nil {
		encodedConfig, err := json.Marshal(rawConfig)
		if err != nil {
			return nil, err
		}
		configs, err := ParseStepConfigContent(string(encodedConfig))
		if err != nil {
			return nil, err
		}
		for _, info := range collectAllSteps(plan.Steps) {
			if err := populateRuntimeFields(info.Step, configs); err != nil {
				return nil, err
			}
		}
	}
	return &plan, nil
}

func executionPlanFromContext(ctx context.Context) *PlanningResponse {
	plan, _ := ctx.Value(executionPlanContextKey{}).(*PlanningResponse)
	return plan
}

// ReadCurrentPlan always reads the workspace service; it has no session cache
// and does not mutate execution state.
func (hcpo *StepBasedWorkflowOrchestrator) ReadCurrentPlan(ctx context.Context) (*PlanningResponse, error) {
	if err := hcpo.loadCodeLayout(ctx); err != nil {
		return nil, err
	}
	if hcpo.directCrewScript != nil {
		plan := CrewScriptPlan(hcpo.directCrewScript, hcpo.GetWorkspacePath())
		if hcpo.directCrewScriptConfig != nil {
			plan.Steps[0].(*ScriptedPlanStep).crewScript = hcpo.directCrewScriptConfig
		}
		return plan, nil
	}
	plan, err := readPlanFromFile(ctx, hcpo.GetWorkspacePath(), hcpo.ReadWorkspaceFile)
	if err != nil {
		return nil, err
	}
	if err := hcpo.hydrateCrewScripts(ctx, plan); err != nil {
		return nil, err
	}
	return plan, nil
}
