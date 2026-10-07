package server

import (
	"context"
	"strings"
	"testing"
)

func TestFastPulseRequestCoalescesAndConsumes(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	first, err := requestFastPulse(ctx, "Workflow/example", "run-one", "schema changed", []string{"workflow.json"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if first.Status != "pending" {
		t.Fatalf("first status = %q, want pending", first.Status)
	}
	if _, err := requestFastPulse(ctx, "Workflow/example", "run-two", "evaluation contract changed", []string{"planning/plan.json"}); err != nil {
		t.Fatalf("coalesced request: %v", err)
	}
	pending, err := pendingFastPulseRequest(ctx, "Workflow/example")
	if err != nil {
		t.Fatalf("pending request: %v", err)
	}
	if pending == nil || pending.RequestedRunID != "run-two" || !strings.Contains(pending.Reason, "evaluation") {
		t.Fatalf("pending = %+v, want latest coalesced request", pending)
	}
	if err := markFastPulseRequestDelivered(ctx, "Workflow/example", "pulse-run"); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	pending, err = pendingFastPulseRequest(ctx, "Workflow/example")
	if err != nil {
		t.Fatalf("pending after delivery: %v", err)
	}
	if pending != nil {
		t.Fatalf("pending after delivery = %+v, want nil", pending)
	}
}
