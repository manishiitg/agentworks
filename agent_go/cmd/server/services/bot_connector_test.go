package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/events"
)

type testBotConnector struct {
	capabilities    *ChannelCapabilities
	name            string
	supportsThreads bool
	mu              sync.Mutex // guards sent; every other field here is only ever touched single-threaded
	sent            []string
	reactions       []string
	sendStarted     chan struct{}
	releaseSend     chan struct{}
}

type persistentBindingTestConnector struct {
	*testBotConnector
	bindingMu  sync.Mutex
	binding    BotSessionBinding
	hasBinding bool
}

func (c *persistentBindingTestConnector) LoadBotSessionBinding(_ context.Context, _ ThreadID, routeKey string) (BotSessionBinding, bool, error) {
	c.bindingMu.Lock()
	defer c.bindingMu.Unlock()
	if !c.hasBinding || c.binding.RouteKey != routeKey {
		return BotSessionBinding{}, false, nil
	}
	return c.binding, true, nil
}

func (c *persistentBindingTestConnector) SaveBotSessionBinding(_ context.Context, _ ThreadID, binding BotSessionBinding) error {
	c.bindingMu.Lock()
	c.binding = binding
	c.hasBinding = true
	c.bindingMu.Unlock()
	return nil
}

func (c *persistentBindingTestConnector) ClearBotSessionBinding(_ context.Context, _ ThreadID) error {
	c.bindingMu.Lock()
	c.binding = BotSessionBinding{}
	c.hasBinding = false
	c.bindingMu.Unlock()
	return nil
}

func (c *testBotConnector) Name() string {
	if c.name != "" {
		return c.name
	}
	return "whatsapp"
}
func (c *testBotConnector) IsEnabled() bool {
	return true
}
func (c *testBotConnector) SendNotification(context.Context, string, string, string, *ButtonOptions, *NotificationDestination) (string, error) {
	return "", nil
}
func (c *testBotConnector) Capabilities() ChannelCapabilities {
	if c.capabilities != nil {
		return *c.capabilities
	}
	return ChannelCapabilities{Threads: c.supportsThreads, MessageEdits: true, StreamingReplies: true, Reactions: true, MessageDeletion: true, ProgressUpdates: true, WorkflowProgress: true}
}
func (c *testBotConnector) StartListening(context.Context) error {
	return nil
}
func (c *testBotConnector) StopListening() {}
func (c *testBotConnector) SendThreadMessage(_ context.Context, _ ThreadID, message string) (string, error) {
	if c.sendStarted != nil {
		select {
		case <-c.sendStarted:
		default:
			close(c.sendStarted)
		}
	}
	if c.releaseSend != nil {
		<-c.releaseSend
	}
	c.sent = append(c.sent, message)
	return "msg", nil
}
func (c *testBotConnector) SendThreadMessageWithBlocks(_ context.Context, _ ThreadID, message string, _ []MessageBlock) (string, error) {
	return c.SendThreadMessage(context.Background(), ThreadID{}, message)
}
func (c *testBotConnector) UpdateMessage(context.Context, ThreadID, string, string) error {
	return nil
}
func (c *testBotConnector) AddReaction(_ context.Context, channelID, messageTS, emoji string) error {
	c.reactions = append(c.reactions, channelID+":"+messageTS+":"+emoji)
	return nil
}
func (c *testBotConnector) RemoveReaction(context.Context, string, string, string) error {
	return nil
}
func (c *testBotConnector) GetThreadHistory(context.Context, ThreadID) ([]ThreadMessage, error) {
	return nil, nil
}
func (c *testBotConnector) GetChannelName(context.Context, string) string { return "" }
func (c *testBotConnector) SetMessageHandler(BotMessageHandler)           {}
func (c *testBotConnector) SetInteractionHandler(BotInteractionHandler)   {}
func (c *testBotConnector) GetFormatter() MessageFormatter {
	return &WhatsAppFormatter{}
}

type historyTestConnector struct {
	*testBotConnector
	history []ThreadMessage
}

func (c *historyTestConnector) GetThreadHistory(context.Context, ThreadID) ([]ThreadMessage, error) {
	return c.history, nil
}

func TestCompletedMultiUserThreadNonMentionStaysOut(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &historyTestConnector{
		testBotConnector: &testBotConnector{name: "slack", supportsThreads: true},
		history: []ThreadMessage{
			{UserID: "U-alice", Text: "run the report"},
			{UserID: "U-bot", Text: "done", IsBot: true},
			{UserID: "U-bob", Text: "thanks Alice"},
		},
	}
	manager.RegisterConnector(connector)

	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, _ map[string]interface{}, sessionID string, _ string, _ func(event *events.AgentEvent)) error {
		started <- sessionID
		return nil
	})

	threadID := ThreadID{Platform: "slack", ChannelID: "C123", ThreadTS: "1710000000.000100"}
	active := &activeBotSession{
		SessionID:    "old-session-1",
		UserID:       "user-1",
		Status:       chathistory.BotSessionStatusCompleted,
		Platform:     "slack",
		ThreadID:     threadID,
		LastActivity: time.Now(),
		builderDone:  true,
	}

	manager.handleExistingSession(active, BotIncomingMessage{
		Platform:  "slack",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
		UserID:    "U-bob",
		Text:      "thanks Alice, I'll take it from here",
		IsMention: false,
	}, true)

	select {
	case got := <-started:
		t.Fatalf("non-mention reply in multi-user thread started session %q; want the bot to stay out", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCompletedMultiUserThreadMentionRestarts(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &historyTestConnector{
		testBotConnector: &testBotConnector{name: "slack", supportsThreads: true},
		history: []ThreadMessage{
			{UserID: "U-alice", Text: "run the report"},
			{UserID: "U-bot", Text: "done", IsBot: true},
			{UserID: "U-bob", Text: "thanks Alice"},
		},
	}
	manager.RegisterConnector(connector)

	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, _ map[string]interface{}, sessionID string, _ string, _ func(event *events.AgentEvent)) error {
		started <- sessionID
		return nil
	})

	threadID := ThreadID{Platform: "slack", ChannelID: "C123", ThreadTS: "1710000000.000100"}
	active := &activeBotSession{
		SessionID:    "old-session-1",
		UserID:       "user-1",
		Status:       chathistory.BotSessionStatusCompleted,
		Platform:     "slack",
		ThreadID:     threadID,
		LastActivity: time.Now(),
		builderDone:  true,
	}

	manager.handleExistingSession(active, BotIncomingMessage{
		Platform:  "slack",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
		UserID:    "U-bob",
		Text:      "rerun that with last quarter",
		IsMention: true,
	}, true)

	select {
	case got := <-started:
		if got != "old-session-1" {
			t.Fatalf("mentioned restart changed durable identity: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected mentioned reply to restart the conversation")
	}
}

func TestCompletedSingleUserThreadOtherUserMentionStaysOut(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	base := &testBotConnector{name: "slack", supportsThreads: true}
	connector := &historyTestConnector{
		testBotConnector: base,
		history: []ThreadMessage{
			{UserID: "U-alice", Text: "run the report"},
			{UserID: "U-bot", Text: "done", IsBot: true},
		},
	}
	manager.RegisterConnector(connector)

	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, _ map[string]interface{}, sessionID string, _ string, _ func(event *events.AgentEvent)) error {
		started <- sessionID
		return nil
	})

	threadID := ThreadID{Platform: "slack", ChannelID: "C123", ThreadTS: "1710000000.000100"}
	active := &activeBotSession{
		SessionID:    "old-session-1",
		UserID:       "user-1",
		Status:       chathistory.BotSessionStatusCompleted,
		Platform:     "slack",
		ThreadID:     threadID,
		LastActivity: time.Now(),
		builderDone:  true,
	}

	manager.handleExistingSession(active, BotIncomingMessage{
		Platform:          "slack",
		ChannelID:         "C123",
		ThreadTS:          "1710000000.000100",
		MessageTS:         "1710000000.000200",
		UserID:            "U-alice",
		Text:              "<@U-bob> can you take a look?",
		IsMention:         false,
		IsThreadReply:     true,
		MentionsOtherUser: true,
	}, true)

	select {
	case got := <-started:
		t.Fatalf("other-user mention started session %q; want the bot to stay out even in single-user threads", got)
	case <-time.After(300 * time.Millisecond):
	}
	if len(base.sent) != 0 {
		t.Fatalf("other-user mention produced replies %#v; want silence", base.sent)
	}
	if len(base.reactions) != 0 {
		t.Fatalf("other-user mention produced reactions %#v; want no visible act at all", base.reactions)
	}
}

func TestRunningSingleUserThreadOtherUserMentionNotInjected(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &historyTestConnector{
		testBotConnector: &testBotConnector{name: "slack", supportsThreads: true},
		history: []ThreadMessage{
			{UserID: "U-alice", Text: "run the report"},
		},
	}
	manager.RegisterConnector(connector)

	followUps := make(chan string, 1)
	manager.SetFollowUpFunc(func(_ context.Context, req map[string]interface{}, _ string, _ string) error {
		followUps <- req["query"].(string)
		return nil
	})

	active := &activeBotSession{
		SessionID:    "session-1",
		UserID:       "user-1",
		Status:       chathistory.BotSessionStatusRunning,
		Platform:     "slack",
		ThreadID:     ThreadID{Platform: "slack", ChannelID: "C123", ThreadTS: "1710000000.000100"},
		LastActivity: time.Now(),
	}

	manager.handleExistingSession(active, BotIncomingMessage{
		Platform:          "slack",
		ChannelID:         "C123",
		ThreadTS:          "1710000000.000100",
		UserID:            "U-alice",
		Text:              "<@U-bob> what do you think?",
		IsMention:         false,
		IsThreadReply:     true,
		MentionsOtherUser: true,
	}, true)

	select {
	case got := <-followUps:
		t.Fatalf("other-user mention injected follow-up %q into the running session; want it ignored", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestThreadlessDurableBindingDoesNotCrossCrewRoutes(t *testing.T) {
	oldRoute := &ChannelRoute{ProfileID: "work", ConversationKey: "crew-old", WorkspacePath: "Crew/old", BotGrant: "run"}
	newRoute := &ChannelRoute{ProfileID: "work", ConversationKey: "crew-new", WorkspacePath: "Crew/new", BotGrant: "run"}
	connector := &persistentBindingTestConnector{
		testBotConnector: &testBotConnector{},
		binding: BotSessionBinding{
			SessionID: "bot-whatsapp--old-workflow",
			RouteKey:  botRouteKey(oldRoute),
			UpdatedAt: time.Now(),
		},
		hasBinding: true,
	}
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(connector)
	manager.SetProfileTurnFunc(func(_ context.Context, _ string, _ BotIncomingMessage, _ ThreadID) (map[string]interface{}, string, bool, error) {
		return map[string]interface{}{"agent_profile_id": "work", "query": "new Crew"}, "new-crew-chat", true, nil
	})

	started := make(chan string, 1)
	manager.SetStartSessionFunc(func(_ context.Context, _ map[string]interface{}, sessionID string, _ string, _ func(event *events.AgentEvent)) error {
		started <- sessionID
		return nil
	})
	manager.HandleIncomingMessage(BotIncomingMessage{
		Platform:        "whatsapp",
		UserID:          "phone-user",
		WorkspaceUserID: "workspace-user",
		ChannelID:       "account|15551234567@s.whatsapp.net",
		Text:            "new Crew",
		PresetWorkflow:  newRoute,
		IsMention:       true,
	})

	select {
	case got := <-started:
		if got == "bot-whatsapp--old-workflow" {
			t.Fatal("a persisted session from another Crew was reused")
		}
	case <-time.After(time.Second):
		t.Fatal("expected a new session to start")
	}
}

func TestWhatsAppWorkflowAuthorizationUsesPairedWorkspaceUser(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	var checkedUser string
	manager.SetWorkflowAccessFunc(func(_ context.Context, userID, _ string, _ ChannelRoute) (string, bool, error) {
		checkedUser = userID
		return userID, true, nil
	})
	msg := BotIncomingMessage{
		Platform: "whatsapp", WorkspaceUserID: "paired-user", UserEmail: "paired@example.com",
		ChannelID: "chat", IsMention: true,
		PresetWorkflow: &ChannelRoute{WorkflowID: "wf", WorkspacePath: "Workflow/wf", BotGrant: "run"},
	}
	if !manager.authorizeWorkflowRouteForMessage(context.Background(), &msg, ThreadID{Platform: "whatsapp", ChannelID: "chat"}, nil) {
		t.Fatal("accessible WhatsApp route rejected")
	}
	if checkedUser != "paired-user" {
		t.Fatalf("access checked as %q, want paired workspace user", checkedUser)
	}
}

func TestSlackWorkflowAuthorizationStillUsesRoutePrincipal(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	var checkedUser string
	manager.SetWorkflowAccessFunc(func(_ context.Context, userID, _ string, _ ChannelRoute) (string, bool, error) {
		checkedUser = userID
		return userID, true, nil
	})
	route := &ChannelRoute{WorkflowID: "wf", WorkspacePath: "Workflow/wf", BotGrant: "run"}
	msg := BotIncomingMessage{Platform: "slack", WorkspaceUserID: "ignored-user", ChannelID: "C1", IsMention: true, PresetWorkflow: route}
	if !manager.authorizeWorkflowRouteForMessage(context.Background(), &msg, ThreadID{Platform: "slack", ChannelID: "C1"}, nil) {
		t.Fatal("accessible Slack route rejected")
	}
	if want := BotPrincipalIDForRoute("slack", *route); checkedUser != want {
		t.Fatalf("access checked as %q, want route principal %q", checkedUser, want)
	}
}

func TestBotSessionFailureMessageDoesNotExposeInternalHTTPError(t *testing.T) {
	got := botSessionFailureMessage(fmt.Errorf("handleQuery returned status 403: workflow access denied"))
	if strings.Contains(got, "handleQuery") || strings.Contains(got, "403") || !strings.Contains(got, "don't currently have access") {
		t.Fatalf("user-facing access error = %q", got)
	}
	got = botSessionFailureMessage(fmt.Errorf("handleQuery returned status 403: bot connector config not found: slack"))
	if strings.Contains(got, "access") || strings.Contains(got, "connector config") || !strings.Contains(got, "Slack setup") {
		t.Fatalf("a setup refusal must not read as missing access: %q", got)
	}
	got = botSessionFailureMessage(fmt.Errorf("dial tcp: private backend failed"))
	if strings.Contains(got, "dial tcp") || strings.Contains(got, "private backend") {
		t.Fatalf("user-facing internal error leaked details: %q", got)
	}
}
