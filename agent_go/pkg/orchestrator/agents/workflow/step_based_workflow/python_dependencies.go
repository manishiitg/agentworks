package step_based_workflow

import (
	"crypto/sha256"
	"fmt"
)

// The resolver captured these exact requirement bytes with the source bundle.
// Each saved script keeps its own entries inside the existing cache grant.
func (hcpo *StepBasedWorkflowOrchestrator) scriptDependencies(step PlanStepInterface) string {
	saved, ok := step.(*ScriptedPlanStep)
	if !ok || saved.crewScript == nil {
		return ""
	}
	requirements, found := saved.crewScript.Files["requirements.txt"]
	if !found {
		return ""
	}
	return workflowAbsPath(hcpo.GetWorkspacePath(), ".sandbox-cache", "python-packages", saved.crewScript.Definition.ID, fmt.Sprintf("%x", sha256.Sum256(requirements)))
}

type pythonScriptRuntime struct {
	SourceDir       string
	DependenciesDir string
}

func (runtime pythonScriptRuntime) apply(env map[string]string) {
	if runtime.DependenciesDir != "" {
		env["WORKFLOW_CODE_DEPS"] = runtime.DependenciesDir
	}
	useExecutedScriptSource(env, runtime.SourceDir)
}
