package step_based_workflow

import (
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"path/filepath"
)

// NestedAgentArtifactsContractVersion is shared with the server's workflow
// contract ladder because the stamp executor must enforce this migration's
// source-layout prerequisite, not merely describe it in a prompt.
const NestedAgentArtifactsContractVersion = "1.0.44"

// ManagedDBScriptsContractVersion is the contract where scripted steps reach the
// workflow database through the built-in agentworks_db helper (the managed
// query/mutate tools) instead of opening db.sqlite themselves (PLAT-428).
const ManagedDBScriptsContractVersion = "1.0.45"

// StepDescriptionLayoutContractVersion is the contract where every plan step's
// description uses the standard section layout (## Goal, ## Inputs, ## Output,
// ## Rules, ## Done when, ## Guides; see the step-description guidance).
// validateStepDescriptionLayoutStamp gates the stamp (step_description_layout.go).
const StepDescriptionLayoutContractVersion = "1.0.46"

// Stamp validation must read definitions, code and retained guides from their
// existing storage roots. A Crew workflow's runtime root contains neither its
// manifest/plan nor its canonical code.
func workflowStampLayout(workflowPath string) (crewroot.ExecutableLayout, error) {
	if !filepath.IsAbs(workflowPath) {
		layout, bound, err := crewroot.LookupExecutable(GetPromptDocsRoot(), workflowPath)
		if err != nil {
			return layout, err
		}
		if bound {
			return layout, nil
		}
		workflowPath = workflowAbsPath(workflowPath)
	}
	return crewroot.ExecutableLayout{Definitions: workflowPath, Runtime: workflowPath, Code: filepath.Join(workflowPath, "code")}, nil
}
