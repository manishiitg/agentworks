package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchestratoragents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type asyncSubAgentExecutionIDContextKey struct{}

type asyncSubAgentCall struct {
	ExecutionID   string
	TodoID        string
	RouteID       string
	AgentType     string
	StartedAt     time.Time
	CompletedAt   time.Time
	Result        string
	Err           error
	done          chan struct{}
	cancel        context.CancelFunc
	stopRequested bool
	reconciled    bool
}

type asyncSubAgentCompletion struct {
	ExecutionID string    `json:"execution_id"`
	TodoID      string    `json:"task_id"`
	RouteID     string    `json:"route_id,omitempty"`
	AgentType   string    `json:"agent_type"`
	Status      string    `json:"status"`
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

func asyncSubAgentExecutionID(ctx context.Context) string {
	value, _ := ctx.Value(asyncSubAgentExecutionIDContextKey{}).(string)
	return strings.TrimSpace(value)
}

func subAgentParentExecutionID(ctx context.Context) string {
	if specID := strings.TrimSpace(virtualtools.SubAgentSpecFromContext(ctx).BackgroundAgentID); specID != "" {
		return specID
	}
	if parentID, _ := ctx.Value(events.ParentExecutionIDKey).(string); strings.TrimSpace(parentID) != "" {
		return strings.TrimSpace(parentID)
	}
	if correlationID, _ := ctx.Value(events.ForceCorrelationIDKey).(string); strings.TrimSpace(correlationID) != "" {
		return strings.TrimSpace(correlationID)
	}
	return ""
}

func copyAsyncSubAgentContextValues(base, source context.Context) context.Context {
	for _, key := range []interface{}{
		virtualtools.PreferredTierContextKey,
		virtualtools.SubAgentAgentRestartKey,
		virtualtools.SubAgentParametersKey,
		virtualtools.ScriptedSubAgentInvocationKey,
		virtualtools.GenericAgentAgentKey,
	} {
		if value := source.Value(key); value != nil {
			base = context.WithValue(base, key, value)
		}
	}
	base = virtualtools.WithSubAgentSpec(base, virtualtools.SubAgentSpecFromContext(source))
	// Without this, an async child's LLM/tool calls report their timing into
	// a different collector than the one the dispatching step drains for its
	// own trace, because base is built from the step's long-lived
	// ParentContext rather than the in-flight tool-call context that carries
	// the parent's active capture ID (PLAT-032).
	base = orchestrator.CopyTimingCaptureContext(base, source)
	return base
}

func (execCtx *SubAgentExecutionContext) registerAsyncCall(
	toolCtx context.Context,
	executionID, todoID, routeID, agentType string,
) (context.Context, *asyncSubAgentCall) {
	baseCtx := execCtx.ParentContext
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	baseCtx = copyAsyncSubAgentContextValues(baseCtx, toolCtx)
	if execCtx.WorkshopCorrelationID != "" {
		baseCtx = context.WithValue(baseCtx, events.ForceCorrelationIDKey, execCtx.WorkshopCorrelationID)
		baseCtx = context.WithValue(baseCtx, events.IsSubAgentContextKey, true)
	}
	childCtx, cancel := context.WithCancel(baseCtx)
	childCtx = context.WithValue(childCtx, asyncSubAgentExecutionIDContextKey{}, executionID)

	call := &asyncSubAgentCall{
		ExecutionID: executionID,
		TodoID:      todoID,
		RouteID:     routeID,
		AgentType:   agentType,
		StartedAt:   time.Now().UTC(),
		done:        make(chan struct{}),
		cancel:      cancel,
	}
	execCtx.asyncMu.Lock()
	if execCtx.asyncCalls == nil {
		execCtx.asyncCalls = make(map[string]*asyncSubAgentCall)
	}
	execCtx.asyncCalls[executionID] = call
	execCtx.asyncOrder = append(execCtx.asyncOrder, executionID)
	execCtx.asyncMu.Unlock()
	return childCtx, call
}

func (execCtx *SubAgentExecutionContext) completeAsyncCall(call *asyncSubAgentCall, result string, err error) {
	execCtx.asyncMu.Lock()
	call.Result = result
	call.Err = err
	call.CompletedAt = time.Now().UTC()
	close(call.done)
	execCtx.asyncMu.Unlock()
	call.cancel()
}

func asyncSubAgentCallStatus(call *asyncSubAgentCall) string {
	select {
	case <-call.done:
		if call.Err == nil {
			return "completed"
		}
		if errors.Is(call.Err, context.Canceled) || call.stopRequested {
			return "canceled"
		}
		return "failed"
	default:
		if call.stopRequested {
			return "canceling"
		}
		return "running"
	}
}

func (execCtx *SubAgentExecutionContext) queryAsyncCall(executionID string) (string, error) {
	executionID = strings.TrimSpace(executionID)
	execCtx.asyncMu.Lock()
	call := execCtx.asyncCalls[executionID]
	if call == nil {
		execCtx.asyncMu.Unlock()
		return "", fmt.Errorf("sub-agent execution %q is not owned by this orchestrator", executionID)
	}
	payload := map[string]interface{}{
		"execution_id": call.ExecutionID,
		"task_id":      call.TodoID,
		"agent_type":   call.AgentType,
		"status":       asyncSubAgentCallStatus(call),
		"started_at":   call.StartedAt,
	}
	if call.RouteID != "" {
		payload["route_id"] = call.RouteID
	}
	if !call.CompletedAt.IsZero() {
		payload["completed_at"] = call.CompletedAt
		payload["result"] = call.Result
		if call.Err != nil {
			payload["error"] = call.Err.Error()
		}
	}
	execCtx.asyncMu.Unlock()
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	return string(encoded), nil
}

func (execCtx *SubAgentExecutionContext) stopAsyncCall(executionID string) (string, error) {
	executionID = strings.TrimSpace(executionID)
	execCtx.asyncMu.Lock()
	call := execCtx.asyncCalls[executionID]
	if call == nil {
		execCtx.asyncMu.Unlock()
		return "", fmt.Errorf("sub-agent execution %q is not owned by this orchestrator", executionID)
	}
	select {
	case <-call.done:
		status := asyncSubAgentCallStatus(call)
		execCtx.asyncMu.Unlock()
		return "", fmt.Errorf("sub-agent execution %q is already %s", executionID, status)
	default:
	}
	call.stopRequested = true
	cancel := call.cancel
	execCtx.asyncMu.Unlock()
	cancel()
	encoded, _ := json.MarshalIndent(map[string]interface{}{
		"execution_id": executionID,
		"status":       "canceling",
		"message":      "Cancellation requested. The runtime will wait for the child to stop before advancing.",
	}, "", "  ")
	return string(encoded), nil
}

func (execCtx *SubAgentExecutionContext) runAsyncCall(call *asyncSubAgentCall, execute func() (string, error)) {
	go func() {
		var result string
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("sub-agent panicked: %v", recovered)
			}
			if err == nil {
				err = asyncSubAgentResultError(result)
			}
			execCtx.completeAsyncCall(call, result, err)
		}()
		result, err = execute()
	}()
}

func (execCtx *SubAgentExecutionContext) waitForUnreconciled(ctx context.Context) ([]asyncSubAgentCompletion, error) {
	execCtx.asyncMu.Lock()
	var calls []*asyncSubAgentCall
	for _, executionID := range execCtx.asyncOrder {
		call := execCtx.asyncCalls[executionID]
		if call != nil && !call.reconciled {
			calls = append(calls, call)
		}
	}
	execCtx.asyncMu.Unlock()
	if len(calls) == 0 {
		return nil, nil
	}

	for _, call := range calls {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
		}
	}

	completions := make([]asyncSubAgentCompletion, 0, len(calls))
	execCtx.asyncMu.Lock()
	for _, call := range calls {
		completion := asyncSubAgentCompletion{
			ExecutionID: call.ExecutionID,
			TodoID:      call.TodoID,
			RouteID:     call.RouteID,
			AgentType:   call.AgentType,
			Status:      "completed",
			Result:      call.Result,
			StartedAt:   call.StartedAt,
			CompletedAt: call.CompletedAt,
		}
		if call.Err != nil {
			completion.Status = asyncSubAgentCallStatus(call)
			completion.Error = call.Err.Error()
		}
		completions = append(completions, completion)
	}
	execCtx.asyncMu.Unlock()
	sort.SliceStable(completions, func(i, j int) bool {
		return completions[i].StartedAt.Before(completions[j].StartedAt)
	})
	return completions, nil
}

func (execCtx *SubAgentExecutionContext) markReconciled(completions []asyncSubAgentCompletion) {
	execCtx.asyncMu.Lock()
	defer execCtx.asyncMu.Unlock()
	for _, completion := range completions {
		if call := execCtx.asyncCalls[completion.ExecutionID]; call != nil {
			call.reconciled = true
		}
	}
}

// cancelOutstandingAndWait is the failure-path backstop. A todo_task must not
// return while a child it owns is still running, even when the parent turn,
// scripted sequence, validation, or reconciliation itself failed.
func (execCtx *SubAgentExecutionContext) cancelOutstandingAndWait(ctx context.Context) error {
	if execCtx == nil {
		return nil
	}
	execCtx.asyncMu.Lock()
	var calls []*asyncSubAgentCall
	for _, executionID := range execCtx.asyncOrder {
		call := execCtx.asyncCalls[executionID]
		if call == nil {
			continue
		}
		select {
		case <-call.done:
			continue
		default:
			call.stopRequested = true
			calls = append(calls, call)
		}
	}
	execCtx.asyncMu.Unlock()

	for _, call := range calls {
		call.cancel()
	}
	for _, call := range calls {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for canceled sub-agent %s: %w", call.ExecutionID, ctx.Err())
		case <-call.done:
		}
	}
	return nil
}

func asyncSubAgentStartResult(executionID, todoID, routeID, agentType string) string {
	result := map[string]interface{}{
		"async":        true,
		"execution_id": executionID,
		"task_id":      todoID,
		"agent_type":   agentType,
		"status":       "running",
		"message":      "Sub-agent started. Return from this turn; the runtime will deliver its result to this orchestrator automatically.",
	}
	if routeID != "" {
		result["route_id"] = routeID
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	return string(encoded)
}

func (hcpo *StepBasedWorkflowOrchestrator) createExecutePredefinedSubAgentFunc(
	execCtx *SubAgentExecutionContext,
) virtualtools.ExecutePredefinedSubAgentFunc {
	syncExecute := hcpo.createExecutePredefinedSubAgentSyncFunc(execCtx)
	if execCtx == nil || !execCtx.AsyncEnabled {
		return syncExecute
	}
	return func(toolCtx context.Context, routeID, todoID, instructions string) (string, error) {
		if execCtx == nil || execCtx.OrchestratorStep == nil {
			return "", fmt.Errorf("call_sub_agent is only available inside a todo_task step")
		}
		routeExists := false
		for _, route := range execCtx.OrchestratorStep.PredefinedRoutes {
			if route.RouteID == routeID {
				routeExists = true
				break
			}
		}
		if !routeExists {
			return "", fmt.Errorf("route_id %q not found in todo task step %q", routeID, execCtx.OrchestratorStep.GetID())
		}

		executionID := fmt.Sprintf("todo-sub-%s-%s-%d",
			workflowSafeIDPart(routeID, "route"),
			workflowSafeIDPart(todoID, "todo"),
			time.Now().UnixNano(),
		)
		childCtx, call := execCtx.registerAsyncCall(toolCtx, executionID, todoID, routeID, "predefined")
		execCtx.runAsyncCall(call, func() (string, error) {
			return syncExecute(childCtx, routeID, todoID, instructions)
		})
		return asyncSubAgentStartResult(executionID, todoID, routeID, "predefined"), nil
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) createExecuteGenericAgentFunc(
	execCtx *SubAgentExecutionContext,
) virtualtools.ExecuteGenericAgentFunc {
	syncExecute := hcpo.createExecuteGenericAgentSyncFunc(execCtx)
	if execCtx == nil || !execCtx.AsyncEnabled {
		return syncExecute
	}
	return func(toolCtx context.Context, todoID, instructions string) (string, error) {
		if execCtx == nil || execCtx.OrchestratorStep == nil {
			return "", fmt.Errorf("call_generic_agent is only available inside a todo_task step")
		}
		executionID := fmt.Sprintf("todo-generic-%s-%d", workflowSafeIDPart(todoID, "todo"), time.Now().UnixNano())
		childCtx, call := execCtx.registerAsyncCall(toolCtx, executionID, todoID, "", "generic")
		execCtx.runAsyncCall(call, func() (string, error) {
			return syncExecute(childCtx, todoID, instructions)
		})
		return asyncSubAgentStartResult(executionID, todoID, "", "generic"), nil
	}
}

func asyncSubAgentResultError(result string) error {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return fmt.Errorf("sub-agent returned an empty result")
	}
	// Runtime errors are carried by the executor's error return. Do not infer
	// failure from prose: successful summaries often contain phrases such as
	// "fixed the error" or quote a prior "execution failed" message.
	return nil
}

func formatAsyncSubAgentCompletions(completions []asyncSubAgentCompletion) string {
	encoded, _ := json.MarshalIndent(completions, "", "  ")
	return fmt.Sprintf(`[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH

The runtime waited for every child launched in your previous turn. These are authoritative terminal results:

%s

Continue the same task now. Use successful results as evidence, handle failures explicitly, and launch another sub-agent batch only when needed. Do not claim completion while any newly launched child is still running.`, string(encoded))
}

func (hcpo *StepBasedWorkflowOrchestrator) reconcileAsyncSubAgentCalls(
	ctx context.Context,
	stepID string,
	agent orchestratoragents.OrchestratorAgent,
	execCtx *SubAgentExecutionContext,
	history *[]llmtypes.MessageContent,
) error {
	if execCtx == nil || agent == nil || history == nil {
		return nil
	}
	const maxReconciliationRounds = 64
	for round := 1; round <= maxReconciliationRounds; round++ {
		completions, err := execCtx.waitForUnreconciled(ctx)
		if err != nil {
			return fmt.Errorf("wait for owned sub-agents: %w", err)
		}
		if len(completions) == 0 {
			return nil
		}
		message := formatAsyncSubAgentCompletions(completions)
		baseAgent := agent.GetBaseAgent()
		if baseAgent == nil {
			return fmt.Errorf("todo task orchestrator has no base agent for sub-agent completion reconciliation")
		}
		_, updatedHistory, err := hcpo.withWorkshopMessageTarget(ctx, stepID, "todo-sub-agent-completion", agent, func() (string, []llmtypes.MessageContent, error) {
			return baseAgent.Execute(ctx, message, *history, "", false)
		})
		if err != nil {
			return fmt.Errorf("deliver sub-agent completion batch: %w", err)
		}
		*history = updatedHistory
		execCtx.markReconciled(completions)
	}
	return fmt.Errorf("sub-agent completion reconciliation exceeded %d rounds", maxReconciliationRounds)
}
