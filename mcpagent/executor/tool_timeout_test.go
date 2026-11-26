package executor

import (
	"context"
	"testing"
	"time"
)

func TestResolveCustomToolTimeoutDefaultsToNoTimeout(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "")

	if got := resolveCustomToolTimeout("call_sub_agent"); got != 0 {
		t.Fatalf("resolveCustomToolTimeout default = %v, want 0", got)
	}
}

func TestResolveCustomToolTimeoutAllowsExplicitZero(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "0")

	if got := resolveCustomToolTimeout("call_sub_agent"); got != 0 {
		t.Fatalf("resolveCustomToolTimeout zero = %v, want 0", got)
	}
}

func TestResolveCustomToolTimeoutHonorsPositiveOverride(t *testing.T) {
	t.Setenv("TOOL_EXECUTION_TIMEOUT", "2s")

	if got := resolveCustomToolTimeout("call_sub_agent"); got != 2*time.Second {
		t.Fatalf("resolveCustomToolTimeout override = %v, want 2s", got)
	}
}

func TestContextWithOptionalTimeoutSkipsDeadlineForZero(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), 0)
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("contextWithOptionalTimeout(0) unexpectedly set a deadline")
	}
}

func TestContextWithOptionalTimeoutSetsDeadlineForPositiveTimeout(t *testing.T) {
	ctx, cancel := contextWithOptionalTimeout(context.Background(), time.Second)
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("contextWithOptionalTimeout(1s) did not set a deadline")
	}
}
