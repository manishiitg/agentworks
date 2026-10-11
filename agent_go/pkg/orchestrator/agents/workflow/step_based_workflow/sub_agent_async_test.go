package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	baseevents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestAsyncGenericAgentPreservesAgentContext(t *testing.T) {
	want := []virtualtools.GenericAgentMessage{{ID: "lens", Message: "inspect"}}
	toolCtx := context.WithValue(context.Background(), virtualtools.GenericAgentAgentKey, want)
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	childCtx, call := execCtx.registerAsyncCall(toolCtx, "child-sequence", "review", "", "generic")
	defer execCtx.completeAsyncCall(call, "done", nil)
	got := virtualtools.GenericAgentAgentFromContext(childCtx)
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("async child sequence = %#v, want %#v", got, want)
	}
}

func TestAsyncScriptedAgentPreservesInvocationAndParameterContext(t *testing.T) {
	parameters := map[string]interface{}{"market": "dubai", "limit": float64(5)}
	toolCtx := context.WithValue(context.Background(), virtualtools.SubAgentParametersKey, parameters)
	toolCtx = context.WithValue(toolCtx, virtualtools.ScriptedSubAgentInvocationKey, true)
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	childCtx, call := execCtx.registerAsyncCall(toolCtx, "child-script", "dubai", "collector", "predefined")
	defer execCtx.completeAsyncCall(call, "done", nil)

	if !virtualtools.IsScriptedSubAgentInvocation(childCtx) {
		t.Fatal("async child lost the dedicated scripted invocation marker")
	}
	got := virtualtools.SubAgentParametersFromContext(childCtx)
	if got["market"] != "dubai" || got["limit"] != float64(5) {
		t.Fatalf("async child parameters = %#v", got)
	}
}

type subAgentAsyncTestNoopListener struct{}

func (subAgentAsyncTestNoopListener) HandleEvent(context.Context, *baseevents.AgentEvent) error {
	return nil
}
func (subAgentAsyncTestNoopListener) Name() string { return "noop" }

// TestAsyncSubAgentChildLLMCallsReportIntoParentStepTiming is the PLAT-032
// regression: registerAsyncCall builds a child's execution context from the
// step's long-lived ParentContext, not from the in-flight tool-call context
// that carries the parent step's active timing-capture ID. Without
// propagating that ID, a child's LLM/tool calls silently land in a
// different (or the default) collector and never appear in the parent
// step's drained trace — exactly the 40-traced-vs-490-ledger gap reported.
func TestAsyncSubAgentChildLLMCallsReportIntoParentStepTiming(t *testing.T) {
	bridge := orchestrator.NewContextAwareEventBridge(subAgentAsyncTestNoopListener{}, loggerv2.NewNoop())

	// The parent step starts its own capture and makes the tool-call context
	// (what the call_sub_agent tool executor actually receives) carry it.
	toolCtx := bridge.StartTimingCaptureFor(context.Background())

	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	childCtx, call := execCtx.registerAsyncCall(toolCtx, "child-1", "todo-1", "route-1", "predefined")
	defer execCtx.completeAsyncCall(call, "done", nil)

	// Simulate the child agent's own LLM call reporting through the SAME
	// bridge (as real child agents do) but with its detached childCtx.
	if err := bridge.HandleEvent(childCtx, &baseevents.AgentEvent{
		Type:      baseevents.LLMGenerationStart,
		Timestamp: time.Now(),
		Component: "test",
		Data:      &baseevents.LLMGenerationStartEvent{Turn: 1, ModelID: "claude-opus-5"},
	}); err != nil {
		t.Fatalf("bridge.HandleEvent: %v", err)
	}

	snapshot := bridge.DrainTimingCaptureFor(toolCtx)
	if len(snapshot.LLMCalls) != 1 {
		t.Fatalf("parent step trace has %d LLM calls, want 1 (child call was not attributed to the parent's capture)", len(snapshot.LLMCalls))
	}
	if snapshot.LLMCalls[0].ModelID != "claude-opus-5" {
		t.Fatalf("captured call model = %q, want claude-opus-5", snapshot.LLMCalls[0].ModelID)
	}
}

// TestAsyncSubAgentWithNoActiveParentCaptureLeavesChildContextUnchanged
// guards the no-op path: when the parent step never started a timing
// capture, registerAsyncCall must not fabricate one for the child.
func TestAsyncSubAgentWithNoActiveParentCaptureLeavesChildContextUnchanged(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	childCtx, call := execCtx.registerAsyncCall(context.Background(), "child-1", "todo-1", "route-1", "predefined")
	defer execCtx.completeAsyncCall(call, "done", nil)

	bridge := orchestrator.NewContextAwareEventBridge(subAgentAsyncTestNoopListener{}, loggerv2.NewNoop())
	if err := bridge.HandleEvent(childCtx, &baseevents.AgentEvent{
		Type:      baseevents.LLMGenerationStart,
		Timestamp: time.Now(),
		Component: "test",
		Data:      &baseevents.LLMGenerationStartEvent{Turn: 1, ModelID: "claude-opus-5"},
	}); err != nil {
		t.Fatalf("bridge.HandleEvent: %v", err)
	}
	// No capture was ever started, so there is nothing to drain — this call
	// just proves HandleEvent doesn't panic/error without an active capture.
}

func TestWaitForUnreconciledWaitsForEveryOwnedChild(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	_, first := execCtx.registerAsyncCall(context.Background(), "child-1", "one", "review", "predefined")
	_, second := execCtx.registerAsyncCall(context.Background(), "child-2", "two", "", "generic")

	result := make(chan []asyncSubAgentCompletion, 1)
	go func() {
		completions, _ := execCtx.waitForUnreconciled(context.Background())
		result <- completions
	}()

	execCtx.completeAsyncCall(first, "first result", nil)
	select {
	case <-result:
		t.Fatal("completion barrier released before every owned child was terminal")
	case <-time.After(30 * time.Millisecond):
	}
	execCtx.completeAsyncCall(second, "", errors.New("second failed"))

	select {
	case completions := <-result:
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
		if completions[0].Status != "completed" || completions[1].Status != "failed" {
			t.Fatalf("unexpected statuses: %#v", completions)
		}
	case <-time.After(time.Second):
		t.Fatal("completion barrier did not release")
	}
}

func TestWaitForUnreconciledHonorsParentCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	execCtx := &SubAgentExecutionContext{ParentContext: parent, AsyncEnabled: true}
	childCtx, _ := execCtx.registerAsyncCall(context.Background(), "child-1", "one", "review", "predefined")
	cancel()

	select {
	case <-childCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not reach child")
	}
	if _, err := execCtx.waitForUnreconciled(parent); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error=%v, want context canceled", err)
	}
}

func TestAsyncSubAgentResultErrorDoesNotInferFailureFromProse(t *testing.T) {
	if err := asyncSubAgentResultError("Fixed the error: missing import; validation now passes"); err != nil {
		t.Fatalf("successful result classified from prose as failure: %v", err)
	}
	if err := asyncSubAgentResultError(""); err == nil {
		t.Fatal("empty child result was treated as success")
	}
}

func TestRunAsyncCallTurnsPanicIntoTerminalFailure(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	_, call := execCtx.registerAsyncCall(context.Background(), "child-panic", "panic", "", "generic")
	execCtx.runAsyncCall(call, func() (string, error) {
		panic("provider crashed")
	})
	completions, err := execCtx.waitForUnreconciled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(completions) != 1 || completions[0].Status != "failed" || completions[0].Error != "sub-agent panicked: provider crashed" {
		t.Fatalf("unexpected panic completion: %#v", completions)
	}
}

func TestRunAsyncCallPreservesChildExecutionFailure(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	_, call := execCtx.registerAsyncCall(context.Background(), "child-failed", "failed", "route", "predefined")
	execCtx.runAsyncCall(call, func() (string, error) {
		return "partial child evidence", errors.New("failed to create execution-only agent")
	})
	completions, err := execCtx.waitForUnreconciled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(completions) != 1 {
		t.Fatalf("got %d completions, want 1", len(completions))
	}
	got := completions[0]
	if got.Status != "failed" || got.Error != "failed to create execution-only agent" || got.Result != "partial child evidence" {
		t.Fatalf("failed child completion = %#v", got)
	}
}

func TestQueryAndStopAsyncSubAgent(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	childCtx, call := execCtx.registerAsyncCall(context.Background(), "child-stop", "stop", "review", "predefined")

	running, err := execCtx.queryAsyncCall("child-stop")
	if err != nil || !strings.Contains(running, `"status": "running"`) {
		t.Fatalf("running query=(%q, %v), want running", running, err)
	}
	if _, err := execCtx.queryAsyncCall("foreign-child"); err == nil {
		t.Fatal("query accepted an execution not owned by this orchestrator")
	}

	stopResult, err := execCtx.stopAsyncCall("child-stop")
	if err != nil || !strings.Contains(stopResult, `"status": "canceling"`) {
		t.Fatalf("stop result=(%q, %v), want canceling", stopResult, err)
	}
	select {
	case <-childCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel the child context")
	}
	execCtx.completeAsyncCall(call, "", childCtx.Err())
	completed, err := execCtx.waitForUnreconciled(context.Background())
	if err != nil || len(completed) != 1 || completed[0].Status != "canceled" {
		t.Fatalf("completion=(%#v, %v), want one canceled child", completed, err)
	}
}

func TestCancelOutstandingAndWaitStopsEveryOwnedChild(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	firstCtx, first := execCtx.registerAsyncCall(context.Background(), "child-1", "one", "review", "predefined")
	secondCtx, second := execCtx.registerAsyncCall(context.Background(), "child-2", "two", "", "generic")

	for _, pair := range []struct {
		ctx  context.Context
		call *asyncSubAgentCall
	}{{firstCtx, first}, {secondCtx, second}} {
		pair := pair
		go func() {
			<-pair.ctx.Done()
			execCtx.completeAsyncCall(pair.call, "", pair.ctx.Err())
		}()
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := execCtx.cancelOutstandingAndWait(cleanupCtx); err != nil {
		t.Fatalf("cancelOutstandingAndWait: %v", err)
	}
	for _, call := range []*asyncSubAgentCall{first, second} {
		select {
		case <-call.done:
		default:
			t.Fatalf("child %s still running after cleanup", call.ExecutionID)
		}
		if status := asyncSubAgentCallStatus(call); status != "canceled" {
			t.Fatalf("child %s status=%s, want canceled", call.ExecutionID, status)
		}
	}
}

func TestAgentAsyncCallCompletesOnlyAfterWholeSequenceReturns(t *testing.T) {
	execCtx := &SubAgentExecutionContext{ParentContext: context.Background(), AsyncEnabled: true}
	_, call := execCtx.registerAsyncCall(context.Background(), "sequence-child", "sequence", "sequence-route", "predefined")
	firstMessageDone := make(chan struct{})
	finishSequence := make(chan struct{})
	execCtx.runAsyncCall(call, func() (string, error) {
		close(firstMessageDone)
		<-finishSequence
		return "all sequence messages completed", nil
	})

	<-firstMessageDone
	statusJSON, err := execCtx.queryAsyncCall("sequence-child")
	if err != nil {
		t.Fatal(err)
	}
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatal(err)
	}
	if status["status"] != "running" {
		t.Fatalf("status after first sequence item=%v, want running", status["status"])
	}
	select {
	case <-call.done:
		t.Fatal("sequence child completed before the full sequence returned")
	default:
	}

	close(finishSequence)
	select {
	case <-call.done:
	case <-time.After(time.Second):
		t.Fatal("sequence child did not complete after the full sequence returned")
	}
}

func TestCompletionBatchIsAutomaticNotification(t *testing.T) {
	message := formatAsyncSubAgentCompletions([]asyncSubAgentCompletion{{ExecutionID: "child-1", Status: "completed"}})
	if !strings.HasPrefix(message, "[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH") {
		t.Fatalf("completion message is not labeled as an automatic notification: %q", message)
	}
}

func TestSubAgentParentExecutionIDFallsBackToExecutionTreeContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), events.ParentExecutionIDKey, "parent-step-1")
	if got := subAgentParentExecutionID(ctx); got != "parent-step-1" {
		t.Fatalf("parent execution id=%q, want parent-step-1", got)
	}
}
