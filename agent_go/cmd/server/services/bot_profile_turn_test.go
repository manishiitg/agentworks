package services

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/events"
)

// An unrouted WhatsApp message on an account with a default product profile
// runs in that profile's own conversation: the session id and request come
// from the profile-turn builder, not the generic bot request.
func TestUnroutedMessageRunsInTheDefaultProfileConversation(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&testBotConnector{})

	manager.SetProfileTurnFunc(func(_ context.Context, userID string, msg BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		if userID != "user-1" {
			t.Fatalf("profile turn userID = %q, want user-1", userID)
		}
		return map[string]interface{}{"agent_profile_id": "sparkquill", "query": msg.Text}, "conv-sparkquill", true, nil
	})

	started := make(chan struct {
		sessionID string
		req       map[string]interface{}
	}, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, sessionID, _ string, _ func(*events.AgentEvent)) error {
		started <- struct {
			sessionID string
			req       map[string]interface{}
		}{sessionID, req}
		return nil
	})

	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform:        "whatsapp",
		UserID:          "phone",
		WorkspaceUserID: "user-1",
		ChannelID:       "dm",
		Text:            "how is she doing?",
		IsMention:       true,
	})

	select {
	case got := <-started:
		if got.sessionID != "conv-sparkquill" {
			t.Fatalf("session = %q, want the profile conversation conv-sparkquill", got.sessionID)
		}
		if got.req["agent_profile_id"] != "sparkquill" || got.req["query"] != "how is she doing?" {
			t.Fatalf("request = %#v, want the profile turn", got.req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}

	manager.mu.RLock()
	active := manager.sessions[ThreadID{Platform: "whatsapp", ChannelID: "dm", ThreadTS: "dm"}.Key()]
	manager.mu.RUnlock()
	if active == nil || !active.profileTurn || active.SessionID != "conv-sparkquill" {
		t.Fatalf("active session = %+v, want a profile-turn session on conv-sparkquill", active)
	}
	// Later turns in that session are built the same way, never the generic
	// bot request with its workflow-runtime preamble.
	req := manager.turnRequestForActive(active, "and maths?", "user-1", "whatsapp", active.ThreadID)
	if req["agent_profile_id"] != "sparkquill" || req["query"] != "and maths?" {
		t.Fatalf("follow-up request = %#v, want the profile turn", req)
	}
	if text := manager.withBotRuntimeState(active, "and maths?"); text != "and maths?" {
		t.Fatalf("profile turn text = %q, want it untouched", text)
	}
}

// A message that names a workflow route never consults the default profile:
// @<slug> routing keeps working exactly as before.
func TestRoutedMessageBypassesTheDefaultProfile(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&testBotConnector{})
	manager.SetProfileTurnFunc(func(context.Context, string, BotIncomingMessage, ThreadID) (map[string]interface{}, string, bool, error) {
		t.Fatal("profile turn consulted for a routed message")
		return nil, "", false, nil
	})
	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, sessionID, _ string, _ func(*events.AgentEvent)) error {
		if req["preset_query_id"] != "wf-report" {
			t.Errorf("request preset = %v, want wf-report", req["preset_query_id"])
		}
		started <- sessionID
		return nil
	})

	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform:        "whatsapp",
		UserID:          "phone",
		WorkspaceUserID: "user-1",
		ChannelID:       "dm",
		Text:            "run it",
		IsMention:       true,
		PresetWorkflow:  &ChannelRoute{WorkflowID: "wf-report", WorkspacePath: "Workflow/report"},
	})
	select {
	case sessionID := <-started:
		if sessionID == "" {
			t.Fatal("no session id")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}
}

func TestSlackProfileRouteUsesConfiguredWorkspaceOwner(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertBotConnectorConfig(context.Background(), &chathistory.CreateBotConnectorConfigRequest{
		ID:              "slack",
		Enabled:         true,
		BotMode:         true,
		AllowedChannels: `{"C1234567890":{"profile_id":"work","conversation_key":"acme","workspace_path":"_users/owner-1/Chats/Work/projects/acme","bot_grant":"owner"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	manager := NewBotConversationManager(store, "", "")
	manager.RegisterConnector(&testBotConnector{name: "slack", supportsThreads: true})
	manager.SetProfileTurnFunc(func(_ context.Context, userID string, msg BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		if userID != "owner-1" {
			t.Fatalf("profile turn userID = %q, want route workspace owner owner-1", userID)
		}
		if msg.WorkspaceUserID != "owner-1" {
			t.Fatalf("message workspace user = %q, want owner-1", msg.WorkspaceUserID)
		}
		if msg.PresetProfile == nil || msg.PresetProfile.WorkspaceUserID != "owner-1" {
			t.Fatalf("preset profile = %+v, want workspace owner owner-1", msg.PresetProfile)
		}
		return map[string]interface{}{
			"agent_profile_id":               msg.PresetProfile.ProfileID,
			"agent_profile_conversation_key": msg.PresetProfile.ConversationKey,
			"selected_folder":                msg.PresetProfile.UploadFolder,
			"query":                          msg.Text,
		}, "conv-work-acme", true, nil
	})

	started := make(chan struct {
		sessionID string
		userID    string
		req       map[string]interface{}
	}, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, sessionID, userID string, _ func(*events.AgentEvent)) error {
		started <- struct {
			sessionID string
			userID    string
			req       map[string]interface{}
		}{sessionID: sessionID, userID: userID, req: req}
		return nil
	})

	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform:  "slack",
		UserID:    "U999",
		UserName:  "Slack Sender",
		ChannelID: "C1234567890",
		ThreadTS:  "1720000000.000100",
		Text:      "status?",
		IsMention: true,
	})

	select {
	case got := <-started:
		if got.sessionID != "conv-work-acme" || got.userID != "owner-1" {
			t.Fatalf("started session = %q userID=%q, want conv-work-acme owner-1", got.sessionID, got.userID)
		}
		if got.req["bot_platform"] != "slack" || got.req["bot_route_grant"] != "run" {
			t.Fatalf("request bot metadata = %#v", got.req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}
}

// Without a default profile the builder says "not handled" and the message
// runs as the generic chat — AgentWorks' own behaviour, unchanged.
func TestNoDefaultProfileFallsBackToTheGenericChat(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&testBotConnector{})
	manager.SetProfileTurnFunc(func(context.Context, string, BotIncomingMessage, ThreadID) (map[string]interface{}, string, bool, error) {
		return nil, "", false, nil
	})
	started := make(chan map[string]interface{}, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, _ string, _ string, _ func(*events.AgentEvent)) error {
		started <- req
		return nil
	})
	manager.HandleIncomingMessage(BotIncomingMessage{Platform: "whatsapp", UserID: "phone", WorkspaceUserID: "user-1", ChannelID: "dm", Text: "hi", IsMention: true})
	select {
	case req := <-started:
		if _, isProfile := req["agent_profile_id"]; isProfile {
			t.Fatalf("generic request = %#v, want no agent profile", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}
}

// The default profile lives on the owner row: it survives the pairing UI's
// repeated re-claims and a service restart, and only the owner can set it.
func TestDefaultProfilePersistsWithTheOwner(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "whatsapp.db")
	svc := NewWhatsAppService(dbPath)
	if err := svc.openMetaStore(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefaultProfile("user-1", "sparkquill", "Chats/SparkQuill/inbox"); err == nil {
		t.Fatal("SetDefaultProfile before pairing succeeded, want an error")
	}
	if err := svc.ClaimOwnership("user-1", "user@example.com", "user"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefaultProfile("user-2", "sparkquill", ""); err == nil {
		t.Fatal("another user set the default profile, want an error")
	}
	if err := svc.SetDefaultProfile("user-1", "sparkquill", "/Chats/SparkQuill/inbox/"); err != nil {
		t.Fatal(err)
	}
	// The pairing screen polls the QR, which re-claims ownership every time.
	if err := svc.ClaimOwnership("user-1", "user@example.com", "user"); err != nil {
		t.Fatal(err)
	}
	if profileID, folder := svc.DefaultProfile(); profileID != "sparkquill" || folder != "Chats/SparkQuill/inbox" {
		t.Fatalf("default profile after re-claim = (%q, %q), want (sparkquill, Chats/SparkQuill/inbox)", profileID, folder)
	}

	reopened := NewWhatsAppService(dbPath)
	if err := reopened.openMetaStore(ctx); err != nil {
		t.Fatal(err)
	}
	reopened.loadOwner(ctx)
	if profileID, folder := reopened.DefaultProfile(); profileID != "sparkquill" || folder != "Chats/SparkQuill/inbox" {
		t.Fatalf("default profile after restart = (%q, %q), want it persisted", profileID, folder)
	}
	if err := reopened.SetDefaultProfile("user-1", "", "ignored"); err != nil {
		t.Fatal(err)
	}
	if profileID, folder := reopened.DefaultProfile(); profileID != "" || folder != "" {
		t.Fatalf("cleared default profile = (%q, %q), want empty", profileID, folder)
	}
}

// A crew's Slack thread is its own chat, new to the thread: its first turn
// carries the thread's earlier messages, as a workflow's first turn does
// (server A 2026-09-26: the crew answered without the thread it was asked about).
func TestSlackCrewFirstTurnGetsTheThread(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&historyTestConnector{
		testBotConnector: &testBotConnector{name: "slack", supportsThreads: true},
		history: []ThreadMessage{
			{UserID: "U-alice", UserName: "alice", Text: "the invoice for ACME is attached, amount looks wrong"},
			{UserID: "U-bob", UserName: "bob", Text: "@crew check this"},
		},
	})
	texts := make(chan string, 1)
	titles := make(chan interface{}, 1)
	manager.SetProfileTurnFunc(func(_ context.Context, _ string, msg BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		texts <- msg.Text
		return map[string]interface{}{"agent_profile_id": "work", "query": msg.Text}, "crew-thread-chat", true, nil
	})
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, _ string, _ string, _ func(*events.AgentEvent)) error {
		titles <- req["session_title"]
		return nil
	})

	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform:        "slack",
		UserID:          "U-bob",
		UserName:        "Bob",
		UserEmail:       "bob@example.com",
		WorkspaceUserID: "owner",
		ChannelID:       "C0CREW",
		ThreadTS:        "1700000000.000100",
		MessageTS:       "1700000000.000200",
		Text:            "check this",
		IsMention:       true,
		IsThreadReply:   true,
		PresetWorkflow:  &ChannelRoute{ProfileID: "work", ConversationKey: "crew-aaa", WorkspacePath: "_users/owner/Chats/Work/projects/alpha", WorkspaceUserID: "owner", BotGrant: "run"},
	})
	select {
	case text := <-texts:
		if !strings.Contains(text, "amount looks wrong") || !strings.Contains(text, "check this") {
			t.Fatalf("first crew turn lacks the thread: %q", text)
		}
		// Many people talk in one channel thread: the turn says who asks.
		if !strings.Contains(text, "From: Bob <bob@example.com> (Slack)") || strings.Count(text, "From: Bob") != 1 {
			t.Fatalf("first crew turn does not name the sender once: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("profile turn never built")
	}
	// The thread's tab says whose question it is, not just "Slack".
	select {
	case title := <-titles:
		if got, _ := title.(string); !strings.HasPrefix(got, "Bob: check this") {
			t.Fatalf("session_title = %v", title)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session never started")
	}
}
