package codeexec

import (
	"context"
	"testing"
)

// A virtual tool call for a session never runs the global executor, which is
// a closure over whichever agent registered last (another session's outputs).
func TestVirtualToolCallForASessionDoesNotFallBackToGlobal(t *testing.T) {
	InitRegistryWithVirtualTools(nil, nil, map[string]func(context.Context, map[string]interface{}) (string, error){
		"search_large_output": func(context.Context, map[string]interface{}) (string, error) { return "someone else's output", nil },
	}, nil, nil)
	if out, err := CallVirtualToolWithSession(context.Background(), "session-without-tools", "search_large_output", nil); err == nil {
		t.Fatalf("a session call must not reach the global executor, got %q", out)
	}
	if out, err := CallVirtualToolWithSession(context.Background(), "", "search_large_output", nil); err != nil || out != "someone else's output" {
		t.Fatalf("sessionless calls keep the global executor, got %q %v", out, err)
	}
}
