package step_based_workflow

import (
	"context"
	"fmt"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// A standalone execute_step already owns its lifecycle. A full run must
// register the sequence's parent before its items start: otherwise their parent
// IDs refer to nothing, so cancellation and completion deduplication miss them.
func (hcpo *StepBasedWorkflowOrchestrator) beginAgentExecution(ctx context.Context, step PlanStepInterface) (context.Context, func(string, error)) {
	if agentSequenceExecutionID(ctx, step.GetID()) != "" {
		return ctx, func(string, error) {}
	}
	id := fmt.Sprintf("exec-%s-%d", step.GetID(), time.Now().UnixNano())
	parentID := currentWorkshopParentExecutionID(ctx)
	scoped, cancel := context.WithCancel(ctx)
	exec := &WorkshopStepExecution{ID: id, StepID: step.GetID(), Status: WorkshopStepRunning, cancel: cancel}
	if hcpo.workshopStepRegistry != nil {
		hcpo.workshopStepRegistry.Register(exec)
	}
	meta := map[string]string{"execution_type": "workflow-step", "step_id": step.GetID(), "run_folder": hcpo.selectedRunFolder, "group_name": hcpo.currentGroupName}
	if hcpo.workshopExecutionNotifier != nil {
		hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{ID: id, ParentExecutionID: parentID, Name: step.GetTitle(), Kind: "workflow_step", Metadata: meta, Cancel: cancel})
	}
	scoped = virtualtools.WithBackgroundAgentID(scoped, id)
	scoped = context.WithValue(scoped, events.ParentExecutionIDKey, id)
	return scoped, func(result string, err error) {
		defer cancel()
		if !finalizeExecStatus(exec, scoped, &result, &err) && hcpo.workshopExecutionNotifier != nil {
			hcpo.workshopExecutionNotifier.OnExecutionComplete(id, step.GetTitle(), result, meta, err)
		}
	}
}
