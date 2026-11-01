package services

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/whatsappbot"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestWhatsAppDefaultProfileAcceptsMessageWithoutWorkflowSlug(t *testing.T) {
	var got *BotIncomingMessage
	svc := &WhatsAppService{
		messageHandler: func(msg BotIncomingMessage) { got = &msg },
		owner: &WhatsAppOwner{
			UserID:              "user-1",
			DefaultProfileID:    "example-app",
			DefaultUploadFolder: "Chats/ExampleApp/inbox",
		},
	}
	chat := types.JID{User: "15551234567", Server: types.DefaultUserServer}
	event := &events.Message{Info: types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, Sender: chat},
		ID:            "phone-message", Timestamp: time.Now(),
	}}
	svc.HandleMessage(context.Background(), &whatsappbot.Message{
		Event: event, Chat: chat, Sender: chat, ID: event.Info.ID, Text: "How is school going?",
	})
	if got == nil {
		t.Fatal("message with a configured default profile was held as unrouted")
	}
	if got.Text != "How is school going?" || got.PresetWorkflow != nil || got.PresetProfile != nil {
		t.Fatalf("forwarded message = %+v, want an ordinary default-profile turn", got)
	}
}

// PLAT (SetDeviceLabel network bootstrap): the label must persist to disk
// through the meta-store-only path, durably (read back with a fresh
// instance, not just held in the writer's memory), and without ever
// starting the whatsmeow connector.
func TestSetDeviceLabelOfflinePersistsWithoutStartingConnector(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "device.db")
	svc := NewWhatsAppService(dbPath)

	if err := svc.SetDeviceLabelOffline(context.Background(), "Mom's Phone"); err != nil {
		t.Fatalf("SetDeviceLabelOffline failed: %v", err)
	}
	if svc.IsEnabled() {
		t.Fatal("SetDeviceLabelOffline must not start the whatsmeow connector")
	}

	reader := NewWhatsAppService(dbPath)
	if err := reader.openMetaStore(context.Background()); err != nil {
		t.Fatalf("openMetaStore failed: %v", err)
	}
	defer reader.closeMetaStore()
	reader.loadDeviceLabel(context.Background())
	if got := reader.DeviceLabel(); got != "Mom's Phone" {
		t.Fatalf("DeviceLabel() after fresh reload = %q, want %q", got, "Mom's Phone")
	}
}

func TestSetDeviceLabelOfflineRejectsOverlongLabel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "device.db")
	svc := NewWhatsAppService(dbPath)
	overlong := make([]rune, 61)
	for i := range overlong {
		overlong[i] = 'a'
	}
	if err := svc.SetDeviceLabelOffline(context.Background(), string(overlong)); err == nil {
		t.Fatal("expected an error for a label over 60 characters")
	}
}

func TestWhatsAppSessionBindingPersistsAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session.db")
	writer := NewWhatsAppService(dbPath)
	if err := writer.openMetaStore(context.Background()); err != nil {
		t.Fatalf("open writer meta store: %v", err)
	}
	wantTime := time.Now().UTC().Truncate(time.Millisecond)
	if err := writer.saveBotSessionBinding(context.Background(), "15551234567@s.whatsapp.net", BotSessionBinding{
		SessionID: "bot-whatsapp--session-1",
		RouteKey:  "workflow|report",
		UpdatedAt: wantTime,
	}); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	writer.closeMetaStore()

	reader := NewWhatsAppService(dbPath)
	if err := reader.openMetaStore(context.Background()); err != nil {
		t.Fatalf("open reader meta store: %v", err)
	}
	defer reader.closeMetaStore()
	reader.loadSessionBindings(context.Background())

	got, ok := reader.loadBotSessionBinding("15551234567@s.whatsapp.net", "workflow|report")
	if !ok {
		t.Fatal("expected binding to survive reopening the SQLite store")
	}
	if got.SessionID != "bot-whatsapp--session-1" || got.RouteKey != "workflow|report" || !got.UpdatedAt.Equal(wantTime) {
		t.Fatalf("binding after reopen = %+v", got)
	}
	if _, ok := reader.loadBotSessionBinding("15551234567@s.whatsapp.net", "workflow|other"); ok {
		t.Fatal("a binding must not cross workflow routes")
	}
}

func TestWhatsAppSessionBindingClearPersists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session.db")
	svc := NewWhatsAppService(dbPath)
	if err := svc.openMetaStore(context.Background()); err != nil {
		t.Fatalf("open meta store: %v", err)
	}
	if err := svc.saveBotSessionBinding(context.Background(), "chat@s.whatsapp.net", BotSessionBinding{SessionID: "session-1"}); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	if err := svc.clearBotSessionBinding(context.Background(), "chat@s.whatsapp.net"); err != nil {
		t.Fatalf("clear binding: %v", err)
	}
	svc.closeMetaStore()

	reader := NewWhatsAppService(dbPath)
	if err := reader.openMetaStore(context.Background()); err != nil {
		t.Fatalf("reopen meta store: %v", err)
	}
	defer reader.closeMetaStore()
	reader.loadSessionBindings(context.Background())
	if _, ok := reader.loadBotSessionBinding("chat@s.whatsapp.net", ""); ok {
		t.Fatal("cleared binding reappeared after reopening the SQLite store")
	}
}

func TestWhatsAppUserNotificationSkipsWhenUnpaired(t *testing.T) {
	svc := &WhatsAppService{}

	msgID, err := svc.SendUserNotification(context.Background(), "hello", "", &NotificationDestination{UserID: "user-1"})
	if err != nil {
		t.Fatalf("SendUserNotification returned error for unpaired WhatsApp service: %v", err)
	}
	if msgID != "" {
		t.Fatalf("SendUserNotification msgID = %q, want empty", msgID)
	}
}

func TestParseWhatsAppWorkflowCommandRecognizesBotSessionControls(t *testing.T) {
	tests := []struct {
		text    string
		wantCmd string
		wantArg string
	}{
		{text: "@resume", wantCmd: "resume"},
		{text: "@resume 2", wantCmd: "resume", wantArg: "2"},
		{text: "@continue abc123", wantCmd: "continue", wantArg: "abc123"},
		{text: "@sessions", wantCmd: "sessions"},
		{text: "@runs", wantCmd: "runs"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			cmd, arg, ok := parseWhatsAppWorkflowCommand(tt.text)
			if !ok {
				t.Fatalf("parseWhatsAppWorkflowCommand(%q) ok=false", tt.text)
			}
			if cmd != tt.wantCmd || arg != tt.wantArg {
				t.Fatalf("parseWhatsAppWorkflowCommand(%q) = (%q, %q), want (%q, %q)", tt.text, cmd, arg, tt.wantCmd, tt.wantArg)
			}
		})
	}
}

func TestWhatsAppBotSessionControlsForwardToBotManager(t *testing.T) {
	var got []BotIncomingMessage
	svc := &WhatsAppService{
		messageHandler: func(msg BotIncomingMessage) {
			got = append(got, msg)
		},
	}
	owner := &WhatsAppOwner{UserID: "user-1", Email: "user@example.com"}
	info := types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:   types.JID{User: "15551234567", Server: types.DefaultUserServer},
			Sender: types.JID{User: "15551234567", Server: types.DefaultUserServer},
		},
		ID:        "msg-1",
		Timestamp: time.Now(),
		PushName:  "User",
	}

	if !svc.handleWorkflowCommand(context.Background(), "@resume 2", "15551234567@s.whatsapp.net", owner, info) {
		t.Fatal("expected @resume to be handled")
	}
	if !svc.handleWorkflowCommand(context.Background(), "@sessions", "15551234567@s.whatsapp.net", owner, info) {
		t.Fatal("expected @sessions to be handled")
	}
	if !svc.handleWorkflowCommand(context.Background(), "@status", "15551234567@s.whatsapp.net", owner, info) {
		t.Fatal("expected @status to be handled")
	}

	if len(got) != 3 {
		t.Fatalf("forwarded messages = %d, want 3", len(got))
	}
	if got[0].Text != "@resume 2" {
		t.Fatalf("first forwarded text = %q, want @resume 2", got[0].Text)
	}
	if got[1].Text != "@status" {
		t.Fatalf("second forwarded text = %q, want @status", got[1].Text)
	}
	if got[2].Text != "@status" {
		t.Fatalf("third forwarded text = %q, want @status", got[2].Text)
	}
	if got[0].WorkspaceUserID != "user-1" || got[0].UserEmail != "user@example.com" {
		t.Fatalf("forwarded owner fields = userID %q email %q", got[0].WorkspaceUserID, got[0].UserEmail)
	}
}

func TestWhatsAppRoutesAreFilteredByPairedUserAccess(t *testing.T) {
	svc := &WhatsAppService{
		owner: &WhatsAppOwner{UserID: "paired-user"},
		routing: WhatsAppRouting{
			"allowed": {WorkflowID: "wf-allowed", WorkspacePath: "Workflow/allowed"},
			"denied":  {WorkflowID: "wf-denied", WorkspacePath: "Workflow/denied"},
		},
	}
	var checked []string
	svc.SetWorkflowAccessFunc(func(_ context.Context, userID string, route ChannelRoute) (bool, error) {
		if userID != "paired-user" {
			t.Fatalf("access checked as %q", userID)
		}
		checked = append(checked, route.WorkflowID)
		return route.WorkflowID == "wf-allowed", nil
	})

	got := svc.accessibleRouting(context.Background())
	if len(got) != 1 || got["allowed"].WorkflowID != "wf-allowed" {
		t.Fatalf("accessible routing = %#v", got)
	}
	if svc.Resolve(context.Background(), "chat", "denied") != nil {
		t.Fatal("inaccessible saved slug resolved")
	}
	if svc.Resolve(context.Background(), "chat", "allowed") == nil {
		t.Fatal("accessible saved slug did not resolve")
	}
	if len(checked) != 4 {
		t.Fatalf("access checks = %v", checked)
	}
}
