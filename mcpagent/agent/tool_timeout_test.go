package mcpagent

import (
	"testing"
	"time"
)

func TestGetToolExecutionTimeoutDefaultsToNoTimeout(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")

	if got := getToolExecutionTimeout(&Agent{}); got != 0 {
		t.Fatalf("getToolExecutionTimeout default = %v, want 0", got)
	}
}

func TestGetToolExecutionTimeoutAllowsExplicitZero(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "0")

	if got := getToolExecutionTimeout(&Agent{}); got != 0 {
		t.Fatalf("getToolExecutionTimeout env zero = %v, want 0", got)
	}
}

func TestGetToolExecutionTimeoutHonorsPositiveOverride(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "2s")

	if got := getToolExecutionTimeout(&Agent{}); got != 2*time.Second {
		t.Fatalf("getToolExecutionTimeout env = %v, want 2s", got)
	}
}

func TestGetToolExecutionTimeoutHonorsAgentOverride(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "0")

	if got := getToolExecutionTimeout(&Agent{toolTimeout: 3 * time.Second}); got != 3*time.Second {
		t.Fatalf("getToolExecutionTimeout agent override = %v, want 3s", got)
	}
}
