package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// workflowAgentFunctionToolsForRun captures the authenticated launch identity.
// Source workspace/run/step identity comes from the controller, never model JSON.
func (api *StreamingAPI) workflowAgentFunctionToolsForRun(ctx context.Context) stepworkflow.AgentFunctionToolFactory {
	claims := GetUserFromContext(ctx)
	if api == nil || api.scheduler == nil || api.productSchedules == nil || claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return nil
	}
	actor := *claims
	if claims.AccessToken != nil {
		token := *claims.AccessToken
		actor.AccessToken = &token
	}
	return func(ctx context.Context, scope stepworkflow.AgentFunctionScope) ([]llmtypes.Tool, map[string]interface{}, error) {
		if strings.TrimSpace(scope.WorkspacePath) == "" || strings.TrimSpace(scope.StepID) == "" || strings.TrimSpace(scope.SessionID) == "" {
			return nil, nil, fmt.Errorf("agent function calls require a workflow, step and tool session")
		}
		runID := firstNonEmptyTrimmed(scope.ExecutionID, scope.SessionID)
		identity, _ := json.Marshal([]string{scope.WorkspacePath, runID, scope.StepID, scope.Group})
		executionScope := crewFunctionArgumentsFingerprint(string(identity))
		caller := workflowTriggerLinkCaller(scope.WorkspacePath)
		resolveCaller := func(ctx context.Context) (triggerLinkCaller, error) {
			if err := ctx.Err(); err != nil {
				return triggerLinkCaller{}, err
			}
			if userAccessForClaims(&actor).Disabled {
				return triggerLinkCaller{}, fmt.Errorf("the execution account is disabled")
			}
			resolved, err := caller(ctx)
			if err != nil {
				return triggerLinkCaller{}, err
			}
			if token := actor.AccessToken; token != nil {
				if !token.Allows("runs:execute") || !token.AllowsWorkflow(resolved.Stamp.ID) {
					return triggerLinkCaller{}, fmt.Errorf("this connection does not allow the source workflow run")
				}
				level, _ := workflowAccessForWorkspacePath(ctx, &actor, scope.WorkspacePath)
				if !accessTokenSessionAllowed(ctx, accessTokenSession{tokenID: token.ID, sessionID: scope.SessionID, workspace: scope.WorkspacePath, access: level}) {
					return triggerLinkCaller{}, fmt.Errorf("this connection is no longer authorized")
				}
			}
			return resolved, nil
		}
		checkTarget := func(ctx context.Context, target triggerTarget, execute bool) error {
			product := "work"
			if target.Kind == triggerCallerWorkflow {
				product = "agentworks"
				if target.Manifest != nil && target.Manifest.Kind == "relay" {
					product = "relays"
				}
			}
			if !userAllowedProduct(&actor, product) {
				return fmt.Errorf("target product is unavailable to the execution account")
			}
			if token := actor.AccessToken; token != nil {
				if target.Kind == triggerCallerCrew {
					scope := "crews:read"
					if execute {
						scope = "crews:run"
					}
					if !token.Allows(scope) || !token.AllowsCrew(target.CrewID) {
						return fmt.Errorf("this connection does not allow %s on this Crew", scope)
					}
				} else if !token.AllowsWorkflow(target.stampID()) || execute && !token.Allows("runs:execute") || !execute && !token.Allows("workflows:read") {
					return fmt.Errorf("this connection does not allow this workflow function")
				}
			}
			return nil
		}
		draft := &workflowAgentFunctionDraft{executors: map[string]interface{}{}}
		err := api.registerCrewFunctionToolSet(draft, actor.UserID, scope.SessionID, QueryRequest{}, resolveCaller, nil, crewFunctionToolOptions{Claims: &actor, ExecutionScope: executionScope, FunctionTools: agentworksproduct.ExecutionFunctionTools(), CheckTarget: checkTarget})
		return draft.tools, draft.executors, err
	}
}

type workflowAgentFunctionDraft struct {
	tools     []llmtypes.Tool
	executors map[string]interface{}
}

func (d *workflowAgentFunctionDraft) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), group string) error {
	return d.RegisterCustomToolWithTimeout(name, description, parameters, execute, 0, group)
}

func (d *workflowAgentFunctionDraft) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, _ string) error {
	d.tools = append(d.tools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name, Description: description, Parameters: llmtypes.NewParameters(parameters)}})
	d.executors[name] = execute
	return nil
}
