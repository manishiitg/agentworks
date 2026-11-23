package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBridgeRequestErrorIdentifiesTimeoutLayer(t *testing.T) {
	got := bridgeRequestError("custom", "execute_shell_command", "session-1", 90*time.Minute, context.DeadlineExceeded)
	for _, want := range []string{"TIMEOUT", "layer=mcpbridge_http", "tool=execute_shell_command", "session=session-1", "timeout=1h30m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestBridgeRequestErrorIdentifiesCancellation(t *testing.T) {
	got := bridgeRequestError("virtual", "call_generic_agent", "session-2", time.Minute, context.Canceled)
	for _, want := range []string{"CANCELED", "layer=mcpbridge_http", "tool=call_generic_agent", "session=session-2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q missing %q", got, want)
		}
	}
}

func TestBridgeMaxCallParsesOnlyPositiveSeconds(t *testing.T) {
	for raw, want := range map[string]time.Duration{"270": 270 * time.Second, " 30 ": 30 * time.Second, "": 0, "0": 0, "-5": 0, "abc": 0} {
		if got := bridgeMaxCall(raw); got != want {
			t.Fatalf("bridgeMaxCall(%q) = %v, want %v", raw, got, want)
		}
	}
}

// PLAT-861-344: dynamically named script tools used to lose their 30m deadline
// and get cut off at the ordinary 5m bridge limit.
func TestBridgeUsesDeclaredToolTimeoutWithinExistingCaps(t *testing.T) {
	millis := func(d time.Duration) *int64 { v := d.Milliseconds(); return &v }
	for _, tc := range []struct {
		name      string
		def       ToolDef
		cap, want time.Duration
	}{
		{"dynamic script", ToolDef{Name: "collect_records", Type: "custom", TimeoutMillis: millis(30 * time.Minute)}, 90 * time.Minute, 30 * time.Minute},
		{"no tool deadline", ToolDef{Type: "custom", TimeoutMillis: millis(0)}, 90 * time.Minute, 90 * time.Minute},
		{"provider cap", ToolDef{Type: "custom", TimeoutMillis: millis(30 * time.Minute)}, 270 * time.Second, 270 * time.Second},
		{"ordinary discovery", ToolDef{Type: "virtual"}, 90 * time.Minute, 5 * time.Minute},
		{"external MCP cannot borrow platform deadline", ToolDef{Type: "mcp", TimeoutMillis: millis(30 * time.Minute)}, 90 * time.Minute, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridgeToolHTTPTimeout(tc.def, 5*time.Minute, tc.cap); got != tc.want {
				t.Fatalf("timeout=%v want %v", got, tc.want)
			}
		})
	}
}
