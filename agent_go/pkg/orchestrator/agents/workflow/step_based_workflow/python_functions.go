package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The trusted run folder survives continuation. Neither Python nor a model can
// choose another run's function-call scope or its owning Crew.
func (hcpo *StepBasedWorkflowOrchestrator) pythonFunctionTools(ctx context.Context, stepID string) ([]llmtypes.Tool, map[string]interface{}, error) {
	opts := hcpo.GetExecutionOptions()
	if opts == nil || opts.AgentFunctionTools == nil || hcpo.selectedRunFolder == "" {
		return nil, nil, fmt.Errorf("Crew function calls are unavailable to this automation; open its Crew chat to check its connections.")
	}
	identity, _ := json.Marshal([]string{hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, stepID})
	hash := sha256.Sum256(identity)
	return opts.AgentFunctionTools(ctx, AgentFunctionScope{WorkspacePath: hcpo.GetWorkspacePath(), ExecutionID: hcpo.selectedRunFolder, StepID: stepID, Group: hcpo.currentGroupName, SessionID: "python-functions-" + hex.EncodeToString(hash[:16])})
}

func (hcpo *StepBasedWorkflowOrchestrator) runPythonFunction(ctx context.Context, parent PlanStepInterface, action string, arguments map[string]interface{}, submissionID string) pythonHelperReply {
	_, executors, err := hcpo.pythonFunctionTools(ctx, parent.GetID())
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	tool := "project_functions_get_call"
	args := map[string]interface{}{"call_id": arguments["call_id"]}
	if action == "call_function" {
		tool = "project_functions_call"
		args = map[string]interface{}{"project": arguments["crew"], "function": arguments["name"], "input": arguments["inputs"], "submission_id": submissionID, "notify": false}
		for _, name := range []string{"wait_seconds", "timeout_minutes"} {
			if value := arguments[name]; value != nil {
				args[name] = value
			}
		}
	}
	execute, ok := executors[tool].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		return pythonHelperReply{Error: "This automation's Crew function service is unavailable."}
	}
	raw, err := execute(ctx, args)
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return pythonHelperReply{Error: "The function returned an unreadable receipt; check its call before retrying."}
	}
	status, _ := result["status"].(string)
	failure, _ := result["error"].(string)
	if status == "" {
		return pythonHelperReply{Error: "The function returned no call status; check its call before retrying."}
	}
	return pythonHelperReply{OK: failure == "" && status != "failed" && status != "stopped" && status != "canceled", Error: failure, Value: result}
}
