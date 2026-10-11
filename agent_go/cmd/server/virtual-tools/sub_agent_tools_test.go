package virtualtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestHandleCallSubAgentReturnsTypedFailedEnvelope(t *testing.T) {
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(context.Context, string, string, string) (string, error) {
			return "partial child evidence", errors.New("failed to create execution-only agent")
		},
	))

	result, err := handleCallSubAgent(ctx, map[string]interface{}{
		"route_id":       "review",
		"task_id":        "todo-1",
		"instructions":   "review the output",
		"preferred_tier": float64(1),
	})
	if err != nil {
		t.Fatalf("tool boundary should return a readable failure envelope, got error: %v", err)
	}
	var payload SubAgentResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	if payload.Success || payload.Error != "failed to create execution-only agent" || payload.Result != "partial child evidence" {
		t.Fatalf("failure envelope = %#v", payload)
	}
}

func TestHandleCallScriptedSubAgentPropagatesParametersAndInvocationKind(t *testing.T) {
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(ctx context.Context, routeID, todoID, instructions string) (string, error) {
			params := SubAgentParametersFromContext(ctx)
			if routeID != "script" || todoID != "dubai" || instructions != "" {
				t.Fatalf("unexpected args: route=%q todo=%q instructions=%q", routeID, todoID, instructions)
			}
			if !reflect.DeepEqual(params, map[string]interface{}{"market": "dubai", "limit": float64(5)}) {
				t.Fatalf("parameters = %#v", params)
			}
			if !IsScriptedSubAgentInvocation(ctx) {
				t.Fatal("dedicated scripted invocation marker missing")
			}
			return "ok", nil
		},
	))
	_, err := handleCallScriptedSubAgent(ctx, map[string]interface{}{
		"route_id": "script", "task_id": "dubai", "preferred_tier": float64(2),
		"parameters": map[string]interface{}{"market": "dubai", "limit": float64(5)},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleCallGenericAgentReturnsTypedFailedEnvelope(t *testing.T) {
	ctx := context.WithValue(context.Background(), ExecuteGenericAgentKey, ExecuteGenericAgentFunc(
		func(context.Context, string, string) (string, error) {
			return "partial generic evidence", errors.New("generic child failed")
		},
	))

	result, err := handleCallGenericAgent(ctx, map[string]interface{}{
		"task_id":        "todo-1",
		"instructions":   "inspect",
		"preferred_tier": float64(1),
	})
	if err != nil {
		t.Fatalf("tool boundary should return a readable failure envelope, got error: %v", err)
	}
	var payload SubAgentResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	if payload.Success || payload.Error != "generic child failed" || payload.Result != "partial generic evidence" {
		t.Fatalf("failure envelope = %#v", payload)
	}
}

func TestHandleCallGenericAgentPropagatesAgent(t *testing.T) {
	var captured []GenericAgentMessage
	ctx := context.WithValue(context.Background(), ExecuteGenericAgentKey, ExecuteGenericAgentFunc(
		func(ctx context.Context, todoID, instructions string) (string, error) {
			captured = GenericAgentAgentFromContext(ctx)
			if todoID != "review" || instructions != "collect shared evidence" {
				t.Fatalf("unexpected opening args: todo=%q instructions=%q", todoID, instructions)
			}
			return "complete", nil
		},
	))

	_, err := handleCallGenericAgent(ctx, map[string]interface{}{
		"task_id":        "review",
		"instructions":   "collect shared evidence",
		"preferred_tier": float64(1),
		"agent": []interface{}{
			map[string]interface{}{"id": "lens", "title": "Lens", "message": "inspect"},
			map[string]interface{}{"id": "final", "message": "consolidate"},
		},
	})
	if err != nil {
		t.Fatalf("handleCallGenericAgent: %v", err)
	}
	want := []GenericAgentMessage{
		{ID: "lens", Title: "Lens", Message: "inspect"},
		{ID: "final", Message: "consolidate"},
	}
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("captured sequence = %#v, want %#v", captured, want)
	}
}

func TestCallGenericAgentSchemaPublishesAgent(t *testing.T) {
	tools := CreateSubAgentTools()
	for _, tool := range tools {
		if tool.Function == nil || tool.Function.Name != "call_generic_agent" {
			continue
		}
		encoded, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		for _, want := range []string{`"agent"`, `"maxItems":12`, `"id"`, `"message"`} {
			if !strings.Contains(text, want) {
				t.Fatalf("call_generic_agent schema missing %s: %s", want, text)
			}
		}
		return
	}
	t.Fatal("call_generic_agent tool not found")
}

func TestDedicatedSubAgentSchemasSeparateInstructionsFromParameters(t *testing.T) {
	foundAgent := false
	foundScripted := false
	for _, tool := range CreateSubAgentTools() {
		if tool.Function == nil || (tool.Function.Name != "call_sub_agent" && tool.Function.Name != "call_scripted_sub_agent") {
			continue
		}
		encoded, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]interface{} `json:"properties"`
			Required   []string               `json:"required"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		required := make(map[string]bool, len(schema.Required))
		for _, name := range schema.Required {
			required[name] = true
		}
		switch tool.Function.Name {
		case "call_sub_agent":
			foundAgent = true
			if !required["instructions"] {
				t.Fatalf("call_sub_agent must require instructions: %s", encoded)
			}
			if _, ok := schema.Properties["parameters"]; ok {
				t.Fatalf("call_sub_agent must not publish parameters: %s", encoded)
			}
		case "call_scripted_sub_agent":
			foundScripted = true
			if !required["parameters"] {
				t.Fatalf("call_scripted_sub_agent must require parameters: %s", encoded)
			}
			if _, ok := schema.Properties["instructions"]; ok {
				t.Fatalf("call_scripted_sub_agent must not publish instructions: %s", encoded)
			}
		}
	}
	if !foundAgent || !foundScripted {
		t.Fatalf("dedicated tools missing: call_sub_agent=%v call_scripted_sub_agent=%v", foundAgent, foundScripted)
	}
}

func TestDedicatedSubAgentHandlersRejectCrossedArgumentShapes(t *testing.T) {
	if _, err := handleCallSubAgent(context.Background(), map[string]interface{}{
		"route_id": "agent", "task_id": "todo", "preferred_tier": float64(2),
	}); err == nil || !strings.Contains(err.Error(), "instructions are required") {
		t.Fatalf("call_sub_agent missing-instructions error = %v", err)
	}
	if _, err := handleCallScriptedSubAgent(context.Background(), map[string]interface{}{
		"route_id": "script", "task_id": "todo", "preferred_tier": float64(2),
	}); err == nil || !strings.Contains(err.Error(), "parameters is required") {
		t.Fatalf("call_scripted_sub_agent missing-parameters error = %v", err)
	}
}

func TestHandleCallSubAgentPropagatesAgentRestart(t *testing.T) {
	called := false
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(ctx context.Context, routeID, todoID, instructions string) (string, error) {
			called = true
			if restart, _ := ctx.Value(SubAgentAgentRestartKey).(bool); !restart {
				t.Fatalf("expected agent restart flag to be propagated")
			}
			if routeID != "seq-route" || todoID != "todo-1" || instructions != "run again" {
				t.Fatalf("unexpected args: route=%q todo=%q instructions=%q", routeID, todoID, instructions)
			}
			return "ok", nil
		},
	))

	result, err := handleCallSubAgent(ctx, map[string]interface{}{
		"route_id":       "seq-route",
		"task_id":        "todo-1",
		"instructions":   "run again",
		"preferred_tier": float64(1),
		"agent_restart":  true,
	})
	if err != nil {
		t.Fatalf("handleCallSubAgent returned error: %v", err)
	}
	if !called {
		t.Fatalf("expected execute function to be called")
	}
	if !strings.Contains(result, `"success": true`) {
		t.Fatalf("expected successful result JSON, got %s", result)
	}
}

func TestHandleCallSubAgentPassesThroughAsyncStart(t *testing.T) {
	const asyncResult = `{"async":true,"execution_id":"child-123","status":"running"}`
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(context.Context, string, string, string) (string, error) {
			return asyncResult, nil
		},
	))

	result, err := handleCallSubAgent(ctx, map[string]interface{}{
		"route_id":       "review",
		"task_id":        "todo-1",
		"instructions":   "review the output",
		"preferred_tier": float64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != asyncResult {
		t.Fatalf("result=%s, want the authoritative async start unchanged", result)
	}
	if strings.Contains(result, `"success"`) || strings.Contains(result, `"completed_at"`) {
		t.Fatalf("async start was falsely wrapped as terminal: %s", result)
	}
}

func TestQueryAndStopSubAgentHandlersDispatchByExecutionID(t *testing.T) {
	ctx := context.WithValue(context.Background(), QuerySubAgentKey, QuerySubAgentFunc(
		func(_ context.Context, executionID string) (string, error) {
			return "query:" + executionID, nil
		},
	))
	ctx = context.WithValue(ctx, StopSubAgentKey, StopSubAgentFunc(
		func(_ context.Context, executionID string) (string, error) {
			return "stop:" + executionID, nil
		},
	))

	queryResult, err := handleQuerySubAgent(ctx, map[string]interface{}{"execution_id": "child-1"})
	if err != nil || queryResult != "query:child-1" {
		t.Fatalf("query result=(%q, %v)", queryResult, err)
	}
	stopResult, err := handleStopSubAgent(ctx, map[string]interface{}{"execution_id": "child-1"})
	if err != nil || stopResult != "stop:child-1" {
		t.Fatalf("stop result=(%q, %v)", stopResult, err)
	}
	if _, err := handleQuerySubAgent(context.Background(), map[string]interface{}{"execution_id": "child-1"}); err == nil {
		t.Fatal("query handler succeeded without an orchestrator-owned function")
	}
}

func TestGetSubAgentConversationDispatchesByExecutionID(t *testing.T) {
	ctx := context.WithValue(context.Background(), GetSubAgentConversationKey, GetSubAgentConversationFunc(
		func(_ context.Context, executionID string, fromLastX, offsetLastX int) (string, error) {
			return fmt.Sprintf("%s:%d:%d", executionID, fromLastX, offsetLastX), nil
		},
	))

	result, err := handleGetSubAgentConversation(ctx, map[string]interface{}{
		"execution_id":  "child-123",
		"from_last_x":   float64(20),
		"offset_last_x": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "child-123:20:5" {
		t.Fatalf("result=%q", result)
	}
	if _, err := handleGetSubAgentConversation(ctx, map[string]interface{}{
		"task_id":     "todo-1",
		"from_last_x": float64(20),
	}); err == nil || !strings.Contains(err.Error(), "execution_id is required") {
		t.Fatalf("legacy ambiguous task_id unexpectedly accepted: %v", err)
	}
}

func TestGetSubAgentConversationSchemaRequiresExecutionID(t *testing.T) {
	for _, tool := range CreateSubAgentTools() {
		if tool.Function == nil || tool.Function.Name != "get_sub_agent_conversation" {
			continue
		}
		encoded, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		if !strings.Contains(text, `"execution_id"`) || strings.Contains(text, `"task_id"`) || strings.Contains(text, `"todo_id"`) {
			t.Fatalf("conversation lookup schema must use exact execution identity: %s", text)
		}
		return
	}
	t.Fatal("get_sub_agent_conversation tool not found")
}
