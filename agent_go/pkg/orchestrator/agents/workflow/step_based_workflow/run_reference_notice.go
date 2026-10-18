package step_based_workflow

import (
	"fmt"
	"strings"
)

// observeRunReferences reports useful repair evidence at the shared execution
// boundary. It makes no model call and cannot refuse or delay a run for review.
// Pulse reads the same reference map/flags through its existing tools; no second
// review queue or copy of the workflow's findings is created here.
func (hcpo *StepBasedWorkflowOrchestrator) observeRunReferences(stepID string) {
	if hcpo.isRelayExecution() {
		return
	}
	root := hcpo.GetWorkspacePath()
	report, err := CollectReferenceMap(root)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[RUN REFERENCES] workflow=%s step=%s check=unavailable; execution continues: %v", root, stepID, err))
		return
	}
	hcpo.GetLogger().Info(fmt.Sprintf("[RUN REFERENCES] workflow=%s step=%s breaks=%d warnings=%d advisory=true", root, stepID, report.Breaks, report.Warnings))
	shown := 0
	for _, issue := range report.Issues {
		if issue.Severity == refSeverityInfo || stepID != "" && strings.HasPrefix(issue.Source, "step:") && issue.Source != "step:"+stepID {
			continue
		}
		hcpo.GetLogger().Warn(fmt.Sprintf("[RUN REFERENCES] workflow=%s source=%s kind=%s reference=%s", root, issue.Source, issue.Kind, issue.Ref))
		shown++
		if shown == 5 {
			break
		}
	}
}
