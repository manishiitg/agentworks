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

func TestHandleCallScriptedSubAgentReturnsTypedFailedEnvelope(t *testing.T) {
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(context.Context, string, string, string) (string, error) {
			return `{"value":"partial child evidence","ok":false,"log_file":"runs/current/script.log","log_complete":true}`, errors.New("failed to create execution-only agent")
		},
	))

	result, err := handleCallScriptedSubAgent(ctx, map[string]interface{}{
		"route_id":       "review",
		"task_id":        "todo-1",
		"parameters":     map[string]interface{}{},
		"preferred_tier": float64(1),
	})
	if err != nil {
		t.Fatalf("tool boundary should return a readable failure envelope, got error: %v", err)
	}
	var payload SubAgentResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode result: %v\n%s", err, result)
	}
	if payload.Success || payload.Error != "failed to create execution-only agent" || !strings.Contains(payload.Result, "partial child evidence") || payload.LogFile != "runs/current/script.log" || !payload.LogComplete {
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

func TestScriptedSubAgentSchemaRequiresTypedParameters(t *testing.T) {
	for _, tool := range CreateSubAgentTools() {
		if tool.Function == nil || tool.Function.Name != "call_scripted_sub_agent" {
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
		required := false
		for _, name := range schema.Required {
			required = required || name == "parameters"
		}
		if !required || schema.Properties["instructions"] != nil {
			t.Fatalf("script helper must require typed parameters, not instructions: %s", encoded)
		}
		return
	}
	t.Fatal("scripted helper tool missing")
}

func TestDedicatedSubAgentHandlersRejectCrossedArgumentShapes(t *testing.T) {
	if _, err := handleCallScriptedSubAgent(context.Background(), map[string]interface{}{
		"route_id": "script", "task_id": "todo", "parameters": map[string]interface{}{}, "instructions": "change task",
	}); err == nil || !strings.Contains(err.Error(), "instructions are not accepted") {
		t.Fatalf("script helper accepted conversational instructions: %v", err)
	}
	if _, err := handleCallScriptedSubAgent(context.Background(), map[string]interface{}{
		"route_id": "script", "task_id": "todo", "preferred_tier": float64(2),
	}); err == nil || !strings.Contains(err.Error(), "parameters is required") {
		t.Fatalf("call_scripted_sub_agent missing-parameters error = %v", err)
	}
}

func TestHandleCallScriptedSubAgentPassesThroughAsyncStart(t *testing.T) {
	const asyncResult = `{"async":true,"execution_id":"child-123","status":"running"}`
	ctx := context.WithValue(context.Background(), ExecutePredefinedSubAgentKey, ExecutePredefinedSubAgentFunc(
		func(context.Context, string, string, string) (string, error) {
			return asyncResult, nil
		},
	))

	result, err := handleCallScriptedSubAgent(ctx, map[string]interface{}{
		"route_id":       "review",
		"task_id":        "todo-1",
		"parameters":     map[string]interface{}{},
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
