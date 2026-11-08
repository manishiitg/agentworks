package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

type pythonScriptStackKey struct{}

// The stack is captured by the server's per-step bridge and saved in its call
// receipt. It is never accepted from Python or inferred from a requested path.
func pythonScriptStack(ctx context.Context, step PlanStepInterface) []string {
	if stack, ok := ctx.Value(pythonScriptStackKey{}).([]string); ok && len(stack) > 0 {
		return append([]string{}, stack...)
	}
	id := step.GetID()
	if script, ok := step.(*ScriptedPlanStep); ok && script.crewScript != nil {
		id = script.crewScript.Definition.ID
	}
	return []string{id}
}

func (hcpo *StepBasedWorkflowOrchestrator) preparePythonScriptCall(ctx context.Context, parent PlanStepInterface, args map[string]interface{}) (*CrewScriptConfiguration, []string, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return nil, nil, fmt.Errorf("Choose a saved script from this Crew by its ID.")
	}
	if _, ok := args["inputs"].(map[string]interface{}); !ok {
		return nil, nil, fmt.Errorf("Script inputs must be a named object matching its saved input definitions.")
	}
	opts := hcpo.executionOptions
	if opts == nil || opts.CrewResources == nil || opts.CrewResources.Scripts == nil {
		return nil, nil, fmt.Errorf("Saved scripts are unavailable; run this automation from its owning Crew.")
	}
	child, err := opts.CrewResources.Scripts.ResolveCrewScript(ctx, hcpo.GetWorkspacePath(), id)
	if err != nil {
		return nil, nil, err
	}
	if child.Definition.ID == "" || strings.TrimSpace(child.Files["main.py"]) == "" {
		return nil, nil, fmt.Errorf("Save this script's main.py in its Crew before running it.")
	}
	// A top may pass its complete input object. Forward only this child's
	// declared names; extra parent names are harmless, while a typo declared
	// nowhere must not silently select a child's default instead.
	parentNames := map[string]bool{}
	if script, ok := parent.(*ScriptedPlanStep); ok && script.crewScript != nil {
		for _, input := range script.crewScript.Definition.Inputs {
			parentNames[input.Name] = true
		}
	} else if hcpo.variablesManifest != nil {
		for _, input := range hcpo.variablesManifest.Variables {
			parentNames[input.Name] = true
		}
	}
	if script, ok := parent.(*ScriptedPlanStep); ok {
		for name := range script.ScriptParameters {
			parentNames[name] = true
		}
		if properties, ok := script.ScriptParametersSchema["properties"].(map[string]interface{}); ok {
			for name := range properties {
				parentNames[name] = true
			}
		}
	}
	childNames := map[string]bool{}
	for _, input := range child.Definition.Inputs {
		childNames[input.Name] = true
	}
	inputs := map[string]interface{}{}
	for name, value := range args["inputs"].(map[string]interface{}) {
		if childNames[name] {
			inputs[name] = value
		} else if !parentNames[name] {
			return nil, nil, fmt.Errorf("Input %q is not declared by this script or its caller. Fix its name or declare the input in the Crew chat.", name)
		}
	}
	// Normalize this invocation's private envelope, not the caller's map.
	// The existing runner still applies child defaults, types and required fields.
	args["inputs"] = inputs
	stack := pythonScriptStack(ctx, parent)
	if slices.Contains(stack, child.Definition.ID) {
		return nil, nil, fmt.Errorf("Saved script loop: %s. Remove the recursive call.", strings.Join(append(stack, child.Definition.ID), " → "))
	}
	if len(stack) >= 5 {
		return nil, nil, fmt.Errorf("Saved script calls reached the depth limit of 5: %s.", strings.Join(append(stack, child.Definition.ID), " → "))
	}
	return &child, append(stack, child.Definition.ID), nil
}

func (hcpo *StepBasedWorkflowOrchestrator) runPythonSavedScript(ctx context.Context, parent PlanStepInterface, saved *CrewScriptConfiguration, args map[string]interface{}, attempt string) pythonHelperReply {
	// Keep outputs outside the parent's disposable step folder. On continuation
	// the parent clears that folder, while a completed child's cached files and
	// its exact source/log must still exist in this permanent run.
	executionRoot := filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder, "execution"))
	output := filepath.ToSlash(filepath.Join(executionRoot, "helpers", attempt))
	step := CrewScriptPlan(&saved.Definition, saved.Root).Steps[0].(*ScriptedPlanStep)
	step.crewScript, step.AgentConfigs = saved, getAgentConfigs(parent)
	step.ScriptInputs = map[string]ScriptInputBinding{}
	for name, value := range args["inputs"].(map[string]interface{}) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return pythonHelperReply{Error: "Invalid script input: " + err.Error()}
		}
		step.ScriptInputs[name] = ScriptInputBinding{Value: encoded}
	}
	// A direct script's own launch values and log receipt belong to its parent,
	// not this child. The parent deadline/cancellation and identity stay intact.
	ctx = withScriptedDelegationContext(ctx, "", "", "", nil)
	ctx = context.WithValue(ctx, scriptLogKey{}, (*scriptLogReceipt)(nil))
	ctx, finish := hcpo.beginScriptInspection(ctx, step)
	var failure error
	summary := ""
	defer func() { finish(summary, failure) }()
	if err := createFolderViaAPI(ctx, output); err != nil {
		failure = err
		return pythonHelperReply{Error: "Cannot prepare this script's output folder: " + err.Error()}
	}
	result := hcpo.tryRunSavedScriptedScript(ctx, step, 0, step.GetID(), []PlanStepInterface{step}, output, executionRoot, hcpo.resolveDBAccess(step.AgentConfigs))
	failure = sequenceSavedScriptError(result)
	if result == nil {
		return pythonHelperReply{Error: failure.Error()}
	}
	paths := make([]string, 0, len(saved.Definition.Outputs))
	for _, file := range saved.Definition.Outputs {
		paths = append(paths, workflowAbsPath(output, file))
	}
	summary = fmt.Sprintf("Saved script %s finished. Output folder: %s", saved.Definition.Name, workflowAbsPath(output))
	reply := pythonHelperReply{OK: failure == nil, Text: summary, LogFile: result.LogFile, LogComplete: result.LogComplete, LogError: result.LogError,
		Value: map[string]interface{}{"output_dir": workflowAbsPath(output), "outputs": paths}}
	if failure != nil {
		reply.Error = scriptOutputPreview(failure.Error(), result.LogFile)
		summary = fmt.Sprintf("Saved script %s did not complete. Inspect its log.", saved.Definition.Name)
		if result.ExitCode < 0 && !result.HarnessTimeout {
			// No launch evidence means no output claim. An uncertain harness
			// failure may have done work, so it must not claim "did not start".
			reply.Value = nil
			if !result.HarnessFailure {
				summary = fmt.Sprintf("Saved script %s did not start. Inspect its log.", saved.Definition.Name)
			}
		}
		reply.Text = summary
	}
	return reply
}
