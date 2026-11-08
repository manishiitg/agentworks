package step_based_workflow

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

func TestWorkshopCostIdentityFallsBackOnlyToKnownContext(t *testing.T) {
	known := context.WithValue(context.Background(), common.UserIDKey, "alice")
	known = costobserver.ContextWithSourcePlatform(known, "whatsapp")
	session, cancel := newWorkshopSessionContext(known, &WorkshopConfig{})
	defer cancel()
	if common.SessionUserIDFromContext(session) != "alice" || costobserver.SourcePlatformFromContext(session) != "whatsapp" {
		t.Fatal("non-server callers must retain their existing typed context identity")
	}
	unknown, cancelUnknown := newWorkshopSessionContext(context.Background(), &WorkshopConfig{})
	defer cancelUnknown()
	if common.SessionUserIDFromContext(unknown) != "" {
		t.Fatal("missing identity must not be guessed from the workflow owner")
	}
}
