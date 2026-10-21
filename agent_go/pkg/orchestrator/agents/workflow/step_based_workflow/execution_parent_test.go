package step_based_workflow

import (
	"context"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	orchestrator_events "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func TestWithWorkshopExecutionParentPreservesDetachedLifecycleIdentity(t *testing.T) {
	launch := virtualtools.WithBackgroundAgentID(context.Background(), "query-root")
	launch = costobserver.ContextWithAttribution(launch, costledger.Attribution{ProductID: "workflow-a", Activity: "scheduled_run"})
	source := costobserver.Source{ID: "schedule-a", Label: "Daily checks", RunID: "occurrence-a"}
	launch = costobserver.ContextWithSource(launch, source)
	stale := costobserver.ContextWithSource(context.Background(), costobserver.Source{ID: "old-schedule"})
	detached := withWorkshopExecutionParent(stale, launch)

	if got := currentWorkshopParentExecutionID(detached); got != "query-root" {
		t.Fatalf("parent execution id = %q, want query-root", got)
	}
	if got := costobserver.SourceFromContext(detached); got != source {
		t.Fatalf("child source = %+v, want %+v", got, source)
	}
	if got := costobserver.AttributionFromContext(detached); got.ProductID != "workflow-a" || got.Activity != "scheduled_run" {
		t.Fatalf("child lost admitted product: %+v", got)
	}
	person := withWorkshopExecutionParent(detached, context.Background())
	if costobserver.SourceFromContext(person).ID != "" || costobserver.AttributionFromContext(person).Activity != "" {
		t.Fatal("a later person turn must not inherit the previous schedule's cost identity")
	}
}

func TestCurrentWorkshopParentExecutionIDUsesSemanticParentNotCorrelation(t *testing.T) {
	parentCtx := context.WithValue(context.Background(), orchestrator_events.ParentExecutionIDKey, "parent-node")
	if got := currentWorkshopParentExecutionID(parentCtx); got != "parent-node" {
		t.Fatalf("ParentExecutionIDKey = %q, want parent-node", got)
	}

	correlationCtx := context.WithValue(context.Background(), orchestrator_events.ForceCorrelationIDKey, "workshop-step-display-group")
	if got := currentWorkshopParentExecutionID(correlationCtx); got != "" {
		t.Fatalf("correlation id must not become semantic parent, got %q", got)
	}
}
