package server

import (
	"log"
	"time"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Reuse the existing cleanup tick and trusted script registry. The Resume
// claim prevents expiry between admission and recording the new running state.
func (api *StreamingAPI) cleanupExpiredPythonAgentSessions(now time.Time) {
	bindings, err := readCrewExecutables()
	if err != nil {
		return
	}
	for _, binding := range bindings {
		if binding.Kind != "script" {
			continue
		}
		view := workflowfiles.WorkflowFS{Docs: workflow.GetPromptDocsRoot(), Root: binding.Root}
		folders, err := view.ReadDir("runs")
		if err != nil {
			continue
		}
		for _, folder := range folders {
			if !folder.IsDir() {
				continue
			}
			claim := workflow.GetPromptDocsRoot() + "\x00" + binding.Root + "\x00" + folder.Name()
			if _, busy := pythonResumeClaims.LoadOrStore(claim, true); busy {
				continue
			}
			_, err := workflow.ExpireFailedPythonAgentSessions(workflow.GetPromptDocsRoot(), binding.Root, folder.Name(), now)
			pythonResumeClaims.Delete(claim)
			if err != nil {
				log.Printf("[PYTHON_AGENT_RETENTION] workflow=%s run=%s cleanup_error=%q", binding.ID, folder.Name(), err)
			}
		}
	}
}
