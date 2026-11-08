package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func TestWhatsAppCrewRouteChecksPairedOwnerProject(t *testing.T) {
	// PLAT-746: current Crew ownership and product records are private,
	// server-registered data; a public manifest cannot grant route access.
	f, _ := newExternalCrewCommandFixture(t)
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	api := f.api
	route := services.ChannelRoute{
		ProfileID: "work", ConversationKey: "team", WorkspacePath: externalCommandCrewRoot,
		WorkshopMode: "run", BotGrant: "run",
	}
	if allowed, err := api.checkWhatsAppWorkflowAccess(context.Background(), "owner", route); err != nil || !allowed {
		t.Fatalf("owner's Crew route allowed=%v err=%v", allowed, err)
	}

	for name, changed := range map[string]services.ChannelRoute{
		"wrong workspace": func() services.ChannelRoute {
			other := route
			other.WorkspacePath = "Crew/other"
			return other
		}(),
		"wrong project id":    func() services.ChannelRoute { other := route; other.ConversationKey = "other"; return other }(),
		"workflow id on crew": func() services.ChannelRoute { other := route; other.WorkflowID = "wf_other"; return other }(),
	} {
		t.Run(name, func(t *testing.T) {
			if allowed, err := api.checkWhatsAppWorkflowAccess(context.Background(), "owner", changed); err != nil || allowed {
				t.Fatalf("mismatched Crew route allowed=%v err=%v", allowed, err)
			}
		})
	}
	if allowed, err := api.checkWhatsAppWorkflowAccess(context.Background(), "other", route); err != nil || allowed {
		t.Fatalf("other user allowed=%v err=%v", allowed, err)
	}
	if err := os.Remove(filepath.Join(f.docs, externalCommandCrewState, "product.json")); err != nil {
		t.Fatal(err)
	}
	if allowed, err := api.checkWhatsAppWorkflowAccess(context.Background(), "owner", route); err != nil || allowed {
		t.Fatalf("deleted Crew route allowed=%v err=%v", allowed, err)
	}
}
