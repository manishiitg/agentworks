package mcpagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-519: an MCP server that exposes a tool named like a platform tool must not stop the agent from being built.
// With every MCP tool prefixed this only guards a platform tool named like a prefixed MCP tool (safety net).
func TestRegisterCustomToolShadowsAnMCPToolOfTheSameName(t *testing.T) {
	agent := &Agent{
		toolToServer: map[string]string{"delete_function": "neon"},
		toolRegistry: directToolRegistry(),
		tools:        []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "delete_function"}}},
	}

	if err := agent.registerCustomTool("delete_function", "direct", objectSchema(), noopTool, "workflow"); err != nil {
		t.Fatalf("RegisterCustomTool() error = %v, want the platform tool to be used without failing", err)
	}
	if got := agent.toolToServer["delete_function"]; got != "custom" {
		t.Fatalf("tool owner = %q, want the platform tool (custom)", got)
	}
	if registered, ok := agent.lookupDirectTool("delete_function"); !ok || registered.Kind != toolImplementationDirect {
		t.Fatal("the platform tool is not registered")
	}
	names := 0
	for _, tool := range agent.tools {
		if tool.Function != nil && tool.Function.Name == "delete_function" {
			names++
		}
	}
	if names != 1 {
		t.Fatalf("the model sees %d tools named delete_function, want exactly 1", names)
	}
}

func TestRegisterCustomToolRejectsCategoryChangeWithoutReplacingOriginal(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(directToolFixture("query_records", "database")),
		toolToServer: map[string]string{"query_records": "custom"},
	}

	err := agent.registerCustomTool("query_records", "replacement", objectSchema(), noopTool, "workflow")
	if err == nil || !strings.Contains(err.Error(), `already registered in category "database"`) {
		t.Fatalf("RegisterCustomTool() error = %v, want category collision", err)
	}
	registered, ok := agent.lookupDirectTool("query_records")
	if !ok {
		t.Fatal("collision removed original canonical registration")
	}
	if got := registered.DisplayGroup; got != "database" {
		t.Fatalf("collision replaced original category with %q", got)
	}
}

func TestRegisterCustomToolStoresOneCompleteCanonicalRecord(t *testing.T) {
	agent := &Agent{
		toolRegistry: directToolRegistry(),
		toolToServer: make(map[string]string),
	}
	timeout := 45 * time.Second
	if err := agent.registerCustomToolWithTimeout(
		"query_records",
		"query records",
		objectSchema(),
		noopTool,
		timeout,
		"database",
	); err != nil {
		t.Fatal(err)
	}

	registered, ok := agent.lookupDirectTool("query_records")
	if !ok {
		t.Fatal("registered direct tool missing from canonical registry")
	}
	if registered.DisplayGroup != "database" || registered.Timeout != timeout || registered.Executor == nil {
		t.Fatalf("incomplete canonical record: %#v", registered)
	}
	if got := len(agent.directToolSnapshot()); got != 1 {
		t.Fatalf("canonical direct tool count = %d, want 1", got)
	}
	if got := len(agent.directToolExecutors()); got != 1 {
		t.Fatalf("derived executor count = %d, want 1", got)
	}
	if got := agent.toolToServer["query_records"]; got != "custom" {
		t.Fatalf("routing projection = %q, want custom", got)
	}
}

func TestDirectToolExecutionEventsCarryActualBridgeReceipt(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID:                 "direct-receipt-test",
		listeners:                 []AgentEventListener{listener},
		directToolExecutionEvents: true,
	}
	executor := agent.observedDirectToolExecutor("write_note", func(_ context.Context, args map[string]interface{}) (string, error) {
		if args["title"] != "today" {
			t.Fatalf("handler args = %#v", args)
		}
		return `{"saved":true}`, nil
	})
	if _, err := executor(context.Background(), map[string]interface{}{"title": "today"}); err != nil {
		t.Fatal(err)
	}
	if len(listener.events) != 2 {
		t.Fatalf("events = %d, want start + end", len(listener.events))
	}
	start, ok := listener.events[0].Data.(*events.ToolCallStartEvent)
	if !ok || start.ServerName != "direct_execution" || !strings.Contains(start.ToolParams.Arguments, `"title":"today"`) || start.ToolCallID == "" {
		t.Fatalf("start = %#v", listener.events[0].Data)
	}
	end, ok := listener.events[1].Data.(*events.ToolCallEndEvent)
	if !ok || end.Result != `{"saved":true}` || end.ToolCallID != start.ToolCallID {
		t.Fatalf("end = %#v", listener.events[1].Data)
	}

	listener.events = nil
	failing := agent.observedDirectToolExecutor("write_note", func(context.Context, map[string]interface{}) (string, error) {
		return "partial", errors.New("disk full")
	})
	if _, err := failing(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("failing handler returned nil error")
	}
	if len(listener.events) != 2 {
		t.Fatalf("failure events = %d, want start + error", len(listener.events))
	}
	if _, ok := listener.events[1].Data.(*events.ToolCallErrorEvent); !ok {
		t.Fatalf("failure event = %T, want ToolCallErrorEvent", listener.events[1].Data)
	}
}

func TestDirectToolExecutionEventsDoNotDuplicateActiveTurnTranscript(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		directToolExecutionEvents: true,
		listeners:                 []AgentEventListener{listener},
	}
	agent.setTurnInFlight(true)
	defer agent.setTurnInFlight(false)
	executor := agent.observedDirectToolExecutor("write_note", func(context.Context, map[string]interface{}) (string, error) {
		return "ok", nil
	})
	if _, err := executor(context.Background(), map[string]interface{}{"title": "today"}); err != nil {
		t.Fatal(err)
	}
	if len(listener.events) != 0 {
		t.Fatalf("bridge emitted %d duplicate events during active transcript turn", len(listener.events))
	}
}

// PLAT-180. observedDirectToolExecutor is the only source of
// tool_call_start/tool_call_end events for a retained (tmux-delivered) turn's
// tool call (see the isTurnInFlight branch above -- during an active Run this
// function does nothing at all). It runs with a bare context, the same shape
// executor/handlers.go's custom-tool HTTP handler actually hands it -- no
// canonicalTurnLifecycle attached, because that lifecycle lives on the
// context Session.Send built for an unrelated call stack. Confirmed failing
// before this fix: the emitted events carried no turn_id metadata key at all
// (canonicalTurnLifecycleFromContext found nil), not merely the wrong value.
func TestDirectToolExecutionEventsCarryTheSessionsActiveTurnID(t *testing.T) {
	listener := &recordingAgentEventListener{}
	agent := &Agent{
		sessionID:                 "retained-turn-lifecycle-test",
		listeners:                 []AgentEventListener{listener},
		directToolExecutionEvents: true,
	}
	session := &Session{agent: agent}
	registerTurnSession(agent.sessionID, session)
	defer unregisterTurnSession(session)

	lifecycle := newCanonicalTurnLifecycle("")
	session.stateMu.Lock()
	session.activeTurn = lifecycle
	session.stateMu.Unlock()

	executor := agent.observedDirectToolExecutor("execute_shell_command", func(context.Context, map[string]interface{}) (string, error) {
		return "done", nil
	})
	// Deliberately bare -- exactly what the real HTTP call site passes.
	if _, err := executor(context.Background(), map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if len(listener.events) != 2 {
		t.Fatalf("events = %d, want start + end", len(listener.events))
	}
	for _, event := range listener.events {
		base, ok := event.Data.(interface{ GetBaseEventData() *events.BaseEventData })
		if !ok {
			t.Fatalf("event %T does not expose BaseEventData", event.Data)
		}
		if got := base.GetBaseEventData().Metadata["turn_id"]; got != lifecycle.id {
			t.Fatalf("event %T turn_id = %v, want %q", event.Data, got, lifecycle.id)
		}
	}
}

func objectSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}

func noopTool(context.Context, map[string]interface{}) (string, error) {
	return "ok", nil
}

// PLAT-746-876: N setup registrations previously republished all executors N times.
func TestToolRegistrationBatchPublishesCurrentScopedClosuresOnce(t *testing.T) {
	const session = "batch-registry-regression"
	a := &Agent{sessionID: session, clients: map[string]mcpclient.ClientInterface{}, toolToServer: map[string]string{}, toolRegistry: directToolRegistry()}
	codeexec.InitRegistryForSession(session, nil, nil)
	t.Cleanup(func() { codeexec.CleanupSession(session) })
	finish := a.DeferToolRegistryPublication()
	defer finish()
	for _, value := range []string{"old", "current"} {
		value := value
		if err := a.registerCustomTool("batch_owned", "owned", objectSchema(), func(context.Context, map[string]interface{}) (string, error) { return value, nil }, "custom"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := codeexec.CallCustomToolWithSession(context.Background(), session, "batch_owned", nil); err == nil {
		t.Fatal("batch was published before setup ended")
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			end := a.DeferToolRegistryPublication()
			defer end()
			if err := a.registerCustomTool(fmt.Sprintf("batch_parallel_%d", i), "parallel setup", objectSchema(), noopTool, "custom"); err != nil {
				t.Error(err)
			}
			end()
		}(i)
	}
	workers.Wait()
	finish()
	got, err := codeexec.CallCustomToolWithSession(context.Background(), session, "batch_owned", nil)
	if err != nil || got != "current" {
		t.Fatalf("published closure = %q, %v", got, err)
	}
	finish() // deferred and explicit completion must not publish twice.
	if a.toolRegistrationDepth != 0 || a.toolRegistryDirty {
		t.Fatal("batch did not finish")
	}
}
