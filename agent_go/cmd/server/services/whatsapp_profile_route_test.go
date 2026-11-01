package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"go.mau.fi/whatsmeow/types"
)

// An @token that is not a workflow slug is offered to the default product's
// router; a workflow slug still wins, and a token nobody knows is unknown.
func TestResolveFallsBackToTheProductRouterAfterWorkflowSlugs(t *testing.T) {
	// An owner marks the pairing as claimed; without one every workflow slug
	// is denied before the product router is even consulted.
	svc := &WhatsAppService{routing: WhatsAppRouting{"report": {WorkflowID: "wf-report", WorkspacePath: "Workflow/report"}}, owner: &WhatsAppOwner{UserID: "user-1"}}
	svc.SetProfileRouter(func(_ context.Context, token string) (*ProfileRoute, error) {
		switch token {
		case "child":
			return &ProfileRoute{ProfileID: "example-app-child", ConversationKey: "fractions", Label: "Example tutor"}, nil
		case "report":
			t.Fatal("product router consulted for a workflow slug")
		case "broken":
			return nil, errors.New("No activity yet")
		}
		return nil, nil
	})

	if route := svc.Resolve(context.Background(), "dm", "Report"); route == nil || route.Key != "report" {
		t.Fatalf("@report = %+v, want the workflow route", route)
	} else if _, isWorkflow := route.Value.(*ChannelRoute); !isWorkflow {
		t.Fatalf("@report value = %T, want *ChannelRoute", route.Value)
	}
	route := svc.Resolve(context.Background(), "dm", "Child")
	if route == nil || route.Key != "child" {
		t.Fatalf("@child = %+v, want the profile route", route)
	}
	if profile, ok := route.Value.(*ProfileRoute); !ok || profile.ProfileID != "example-app-child" || profile.ConversationKey != "fractions" {
		t.Fatalf("@child value = %+v, want the child profile keyed by the activity", route.Value)
	}
	if route := svc.Resolve(context.Background(), "dm", "nothing"); route != nil {
		t.Fatalf("@nothing = %+v, want unknown", route)
	}
	if route := svc.Resolve(context.Background(), "dm", "broken"); route != nil {
		t.Fatalf("@broken = %+v, want unknown (the product's reason is shown by RouteUnknown)", route)
	}
}

// A message routed to one of the product's profiles starts its own session
// (a different route key from the default chat), and every later turn in it
// carries the same profile route.
func TestProfileRoutedMessageKeepsItsProfileAcrossTurns(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&testBotConnector{})

	child := &ProfileRoute{ProfileID: "example-app-child", ConversationKey: "fractions"}
	manager.SetProfileTurnFunc(func(_ context.Context, _ string, msg BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		if msg.PresetProfile == nil || msg.PresetProfile.ProfileID != "example-app-child" {
			t.Errorf("profile turn built without the @child route: %+v", msg.PresetProfile)
		}
		return map[string]interface{}{"agent_profile_id": "example-app-child", "query": msg.Text}, "conv-fractions", true, nil
	})
	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, _ map[string]interface{}, sessionID, _ string, _ func(*events.AgentEvent)) error {
		started <- sessionID
		return nil
	})

	msg := BotIncomingMessage{Platform: "whatsapp", UserID: "phone", WorkspaceUserID: "user-1", ChannelID: "dm", Text: "2/5 + 1/5?", IsMention: true, PresetProfile: child}
	if key, def := botMessageRouteKey(msg), botMessageRouteKey(BotIncomingMessage{}); key == def || key == "" {
		t.Fatalf("route key for @child = %q, want distinct from the default chat's %q", key, def)
	}
	manager.HandleIncomingMessage(msg)
	select {
	case sessionID := <-started:
		if sessionID != "conv-fractions" {
			t.Fatalf("session = %q, want the child's activity conversation", sessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}

	manager.mu.RLock()
	active := manager.sessions[ThreadID{Platform: "whatsapp", ChannelID: "dm", ThreadTS: "dm"}.Key()]
	manager.mu.RUnlock()
	if active == nil || active.profileRoute == nil || active.profileRoute.ProfileID != "example-app-child" {
		t.Fatalf("active session = %+v, want the @child route remembered", active)
	}
	req := manager.turnRequestForActive(active, "is it 3/5?", "user-1", "whatsapp", active.ThreadID)
	if req["agent_profile_id"] != "example-app-child" || req["query"] != "is it 3/5?" {
		t.Fatalf("follow-up = %#v, want built as the child's turn", req)
	}
}

// A bot reply must not drop the chat out of a product route: @child is not
// a workflow slug, so the outbound bookkeeping used to treat it as stale
// and clear it — every message after the first @child turn then ran in the
// default (parent) profile. Workflow slugs keep their hint; a slug nobody
// knows is still dropped when no product router is in play.
func TestOutboundReplyKeepsTheActiveProductRoute(t *testing.T) {
	chat := "15551234567@s.whatsapp.net"

	child := &WhatsAppService{routing: WhatsAppRouting{"report": {WorkflowID: "wf-report", WorkspacePath: "Workflow/report"}}}
	child.SetProfileRouter(func(_ context.Context, token string) (*ProfileRoute, error) {
		if token == "child" {
			return &ProfileRoute{ProfileID: "example-app-child", ConversationKey: "fractions", Label: "Example tutor"}, nil
		}
		return nil, nil
	})
	child.setActiveSlug(chat, "child")
	if out := child.applyActiveRouteHint(chat, "2/5 + 1/5 = 3/5."); out != "2/5 + 1/5 = 3/5." {
		t.Fatalf("reply with @child active = %q, want the message untouched", out)
	}
	if active := child.activeSlug(chat); active != "child" {
		t.Fatalf("active route after a bot reply = %q, want @child to stick", active)
	}

	workflow := &WhatsAppService{routing: WhatsAppRouting{"report": {WorkflowID: "wf-report", WorkspacePath: "Workflow/report"}}}
	workflow.setActiveSlug(chat, "report")
	if out := workflow.applyActiveRouteHint(chat, "done"); !strings.HasSuffix(out, "[report active | @off]") {
		t.Fatalf("reply with @report active = %q, want the sticky-route hint", out)
	}
	if active := workflow.activeSlug(chat); active != "report" {
		t.Fatalf("active route after a bot reply = %q, want @report kept", active)
	}

	stale := &WhatsAppService{}
	stale.setActiveSlug(chat, "ghost")
	stale.applyActiveRouteHint(chat, "done")
	if active := stale.activeSlug(chat); active != "" {
		t.Fatalf("stale active route after a bot reply = %q, want it cleared", active)
	}
}

// @status must report a product route, not clear it as an unknown workflow.
func TestStatusKeepsTheActiveProductRoute(t *testing.T) {
	chat := "15551234567@s.whatsapp.net"
	owner := &WhatsAppOwner{UserID: "user-1", Email: "user@example.com"}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:   types.JID{User: "15551234567", Server: types.DefaultUserServer},
			Sender: types.JID{User: "15551234567", Server: types.DefaultUserServer},
		},
		ID: "msg-1", Timestamp: time.Now(), PushName: "User",
	}

	svc := &WhatsAppService{messageHandler: func(BotIncomingMessage) {}}
	svc.SetProfileRouter(func(_ context.Context, token string) (*ProfileRoute, error) {
		if token == "child" {
			return &ProfileRoute{ProfileID: "example-app-child", ConversationKey: "fractions", Label: "Example tutor"}, nil
		}
		return nil, nil
	})
	svc.setActiveSlug(chat, "child")
	if !svc.handleWorkflowCommand(context.Background(), "@status", chat, owner, info) {
		t.Fatal("expected @status to be handled")
	}
	if active := svc.activeSlug(chat); active != "child" {
		t.Fatalf("active route after @status = %q, want @child to stick", active)
	}

	stale := &WhatsAppService{messageHandler: func(BotIncomingMessage) {}}
	stale.setActiveSlug(chat, "ghost")
	if !stale.handleWorkflowCommand(context.Background(), "@status", chat, owner, info) {
		t.Fatal("expected @status to be handled")
	}
	if active := stale.activeSlug(chat); active != "" {
		t.Fatalf("stale active route after @status = %q, want it cleared", active)
	}
}
