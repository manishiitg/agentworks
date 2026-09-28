package step_based_workflow

import (
	"context"
	"testing"
)

func TestExternalBuilderPlanOriginSurvivesNormalBuilderDecorator(t *testing.T) {
	ctx := WithExternalBuilderPlanOrigin(context.Background(), "op-1", "grant-1", "owner", "Owner", "main-chat")
	ctx = withPlanChangeOrigin(ctx, "workflow-builder")
	origin := planChangeOriginFromContext(ctx, "")
	if origin.Type != "external_builder" || origin.OperationID != "op-1" || origin.ViaToken != "token:grant-1" || origin.UserID != "owner" || origin.SessionID != "main-chat" {
		t.Fatalf("external plan provenance was overwritten: %+v", origin)
	}
}
