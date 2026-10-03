package step_based_workflow

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
)

func TestWorkshopChildPreservesCostIdentityAndSessionCancellation(t *testing.T) {
	request, cancelRequest := context.WithCancel(context.Background())
	request = context.WithValue(request, common.UserIDKey, "different-context-user")
	session, cancelSession := newWorkshopSessionContext(request, &WorkshopConfig{UserID: "authorized-owner", SourcePlatform: "slack"})
	defer cancelSession()
	manager := &InteractiveWorkshopManager{sessionCtx: session}
	child, cancelChild, err := manager.newExecContext(request)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelChild()
	if common.SessionUserIDFromContext(child) != "authorized-owner" || costobserver.SourcePlatformFromContext(child) != "slack" {
		t.Fatal("workshop child lost the server-resolved cost actor/channel")
	}
	cancelRequest()
	if child.Err() != nil {
		t.Fatal("returning/cancelling the HTTP request must not cancel workshop children")
	}
	cancelSession()
	if child.Err() != context.Canceled {
		t.Fatal("closing the workshop must still cancel children")
	}
	if _, _, err := manager.newExecContext(context.Background()); err == nil {
		t.Fatal("a stopped workshop must reject another child")
	}
}

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
