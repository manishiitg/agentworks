package virtualtools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

type testUserNotificationConnector struct {
	name string
	ch   chan *services.NotificationDestination
}

type testFeedbackNotificationConnector struct {
	ch chan string
}

type capturedHumanFeedbackEvent struct {
	requestID string
	question  string
	context   string
	options   []string
}

type testHumanFeedbackEmitter struct {
	events   chan capturedHumanFeedbackEvent
	resolved chan string
}

func (e *testHumanFeedbackEmitter) EmitHumanFeedbackResolved(requestID, outcome string) {
	if e.resolved != nil {
		e.resolved <- requestID + ":" + outcome
	}
}

func (e *testHumanFeedbackEmitter) EmitBlockingHumanFeedback(requestID, question, context string, _ bool, _, _ string, options ...string) {
	e.events <- capturedHumanFeedbackEvent{
		requestID: requestID,
		question:  question,
		context:   context,
		options:   append([]string(nil), options...),
	}
}

func (e *testHumanFeedbackEmitter) EmitProductInteraction(string, map[string]interface{}) {}

func (c *testUserNotificationConnector) Name() string {
	if c.name != "" {
		return c.name
	}
	return "test_workflow_notification"
}
func (c *testUserNotificationConnector) IsEnabled() bool {
	return true
}
func (c *testUserNotificationConnector) SendNotification(context.Context, string, string, string, *services.ButtonOptions, *services.NotificationDestination) (string, error) {
	return "", nil
}
func (c *testUserNotificationConnector) SendUserNotification(ctx context.Context, message, contextMsg string, dest *services.NotificationDestination) (string, error) {
	c.ch <- dest
	return "msg-1", nil
}

func (c *testFeedbackNotificationConnector) Name() string    { return "test_human_feedback_fanout" }
func (c *testFeedbackNotificationConnector) IsEnabled() bool { return true }
func (c *testFeedbackNotificationConnector) SendNotification(_ context.Context, uniqueID, _, _ string, _ *services.ButtonOptions, _ *services.NotificationDestination) (string, error) {
	c.ch <- uniqueID
	return "feedback-msg-1", nil
}

func resetHumanToolTestState() {
	parentChatMu.Lock()
	parentChatRegistry = map[string]*ParentChatContext{}
	parentChatMu.Unlock()

	chatInjectMu.Lock()
	chatInject = nil
	chatInjectMu.Unlock()

	store := GetHumanFeedbackStore()
	store.mu.Lock()
	store.requests = make(map[string]*HumanFeedbackRequest)
	store.waiters = make(map[string]chan string)
	store.mu.Unlock()
}

func TestHumanFeedbackDescriptionRequiresForegroundBridgeWait(t *testing.T) {
	var description string
	for _, tool := range CreateHumanTools() {
		if tool.Function != nil && tool.Function.Name == "human_feedback" {
			description = tool.Function.Description
			break
		}
	}
	for _, want := range []string{
		"curl in the foreground",
		"never use nohup",
		"poll for completion",
		"shell timeout shorter than timeout_seconds",
		"at most 45 seconds",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("human_feedback description missing %q:\n%s", want, description)
		}
	}
}

func TestRetiredNotificationToolHasNoRegistration(t *testing.T) {
	const retired = "notify_user"
	for _, tool := range CreateHumanTools() {
		if tool.Function != nil && tool.Function.Name == retired {
			t.Fatal("retired notification tool is still advertised")
		}
	}
	if _, ok := CreateHumanToolExecutors()[retired]; ok {
		t.Fatal("retired notification tool is still callable")
	}
	for _, name := range HumanToolImplementationNames() {
		if name == retired {
			t.Fatal("retired notification tool is still discoverable")
		}
	}
}

func TestHumanFeedbackStoreListsPendingRequestsIndependentlyOfSessionEvents(t *testing.T) {
	resetHumanToolTestState()
	t.Cleanup(resetHumanToolTestState)

	store := GetHumanFeedbackStore()
	if err := store.CreatePendingRequest(
		"captcha-1",
		"Complete the CAPTCHA.",
		"This request expires in 45 seconds.",
		"workflow-step-1",
		[]string{"done", "submitted"},
		false,
		45*time.Second,
	); err != nil {
		t.Fatalf("create pending request: %v", err)
	}

	pending := store.ListPending(time.Now())
	if len(pending) != 1 {
		t.Fatalf("pending requests = %d, want 1", len(pending))
	}
	got := pending[0]
	if got.UniqueID != "captcha-1" || got.SessionID != "workflow-step-1" || got.MessageForUser != "Complete the CAPTCHA." {
		t.Fatalf("unexpected pending request: %#v", got)
	}
	if got.UserResponse != "" || len(got.Options) != 2 {
		t.Fatalf("unsafe or incomplete pending projection: %#v", got)
	}

	if err := store.SubmitResponse("captcha-1", "done"); err != nil {
		t.Fatalf("submit pending response: %v", err)
	}
	if got := store.ListPending(time.Now()); len(got) != 0 {
		t.Fatalf("completed request remained pending: %#v", got)
	}
}

func TestHandleHumanFeedbackWaitsForDirectHumanResponseWithoutParentRelay(t *testing.T) {
	resetHumanToolTestState()
	t.Cleanup(resetHumanToolTestState)
	feedbackNotifications := make(chan string, 1)
	feedbackConnector := &testFeedbackNotificationConnector{ch: feedbackNotifications}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(feedbackConnector)
	t.Cleanup(func() { manager.UnregisterConnector(feedbackConnector.Name()) })

	RegisterParentChat("workflow-session", &ParentChatContext{
		SessionID:    "builder-session",
		WorkflowPath: "Workflow/upwork",
		GroupName:    "daily-bid",
	})

	injected := make(chan string, 1)
	SetChatInjector(func(ctx context.Context, sessionID, userID, message string) error {
		injected <- message
		return nil
	})

	emitter := &testHumanFeedbackEmitter{events: make(chan capturedHumanFeedbackEvent, 1), resolved: make(chan string, 1)}
	ctx := context.WithValue(context.Background(), BGAgentSessionIDKey, "workflow-session")
	ctx = context.WithValue(ctx, SessionEventEmitterKey, SessionEventEmitter(emitter))
	type result struct {
		answer string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		answer, err := handleHumanFeedback(ctx, map[string]interface{}{
			"unique_id":        "req-1",
			"message_for_user": "Review the drafted cover letter.",
			"options":          []interface{}{"approve", "decline"},
			"timeout_seconds":  float64(30),
		})
		done <- result{answer: answer, err: err}
	}()

	select {
	case event := <-emitter.events:
		if event.requestID != "req-1" || event.question != "Review the drafted cover letter." {
			t.Fatalf("unexpected blocking event: %#v", event)
		}
		if !strings.Contains(event.context, "expires in 30 seconds") {
			t.Fatalf("blocking event missing expiry context: %#v", event)
		}
		if len(event.options) != 2 || event.options[0] != "approve" || event.options[1] != "decline" {
			t.Fatalf("blocking event options = %#v", event.options)
		}
	case <-time.After(time.Second):
		t.Fatal("human_feedback did not emit blocking UI event")
	}

	select {
	case uniqueID := <-feedbackNotifications:
		t.Fatalf("human_feedback unexpectedly fanned out through notification connectors: %s", uniqueID)
	case <-time.After(100 * time.Millisecond):
	}

	deadline := time.Now().Add(time.Second)
	for {
		GetHumanFeedbackStore().mu.RLock()
		_, exists := GetHumanFeedbackStore().requests["req-1"]
		GetHumanFeedbackStore().mu.RUnlock()
		if exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("human feedback request was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	if err := GetHumanFeedbackStore().SubmitResponse("req-1", "approve"); err != nil {
		t.Fatalf("submit direct response: %v", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("handleHumanFeedback returned error: %v", got.err)
		}
		if got.answer != "approve" {
			t.Fatalf("unexpected response: %q", got.answer)
		}
	case <-time.After(time.Second):
		t.Fatal("human_feedback did not return the direct human response")
	}
	select {
	case resolution := <-emitter.resolved:
		if resolution != "req-1:answered" {
			t.Fatalf("resolution marker = %q, want req-1:answered", resolution)
		}
	default:
		t.Fatal("answered request emitted no durable resolution marker")
	}

	select {
	case message := <-injected:
		t.Fatalf("human feedback was unexpectedly routed to the parent builder: %q", message)
	default:
	}

	GetHumanFeedbackStore().mu.RLock()
	_, requestRetained := GetHumanFeedbackStore().requests["req-1"]
	_, waiterRetained := GetHumanFeedbackStore().waiters["req-1"]
	GetHumanFeedbackStore().mu.RUnlock()
	if requestRetained || waiterRetained {
		t.Fatalf("consumed human response remained in memory: request=%v waiter=%v", requestRetained, waiterRetained)
	}
}

func TestHumanToolCategoryIsCanonical(t *testing.T) {
	for _, category := range []string{"human_tools", " human_tools "} {
		if !IsHumanToolCategory(category) {
			t.Fatalf("expected %q to be recognized as a human tool category", category)
		}
	}
	for _, category := range []string{"", "human", "delegation_tools", "workspace_advanced", "workflow"} {
		if IsHumanToolCategory(category) {
			t.Fatalf("did not expect %q to be recognized as a human tool category", category)
		}
	}
}

func TestHumanFeedbackTimeoutFromArgs(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
		want time.Duration
	}{
		{name: "default", want: 5 * time.Minute},
		{name: "agent value", raw: float64(120), want: 2 * time.Minute},
		{name: "minimum clamp", raw: float64(1), want: 30 * time.Second},
		{name: "maximum clamp", raw: float64(7200), want: 30 * time.Minute},
		{name: "invalid defaults", raw: "soon", want: 5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]interface{}{}
			if tt.raw != nil {
				args["timeout_seconds"] = tt.raw
			}
			if got := humanFeedbackTimeoutFromArgs(args); got != tt.want {
				t.Fatalf("timeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestChildSessionInheritsNotificationDestination(t *testing.T) {
	const parentSessionID = "notification-parent-session"
	const childSessionID = "notification-child-session"
	RegisterSessionNotificationDestination(parentSessionID, &services.NotificationDestination{
		UserID:          "user-1",
		WorkflowName:    "demo",
		WorkspacePath:   "Workflow/demo",
		RouteSelections: map[string]string{"org_dashboard": "enabled", "slack": "enabled"},
	})
	t.Cleanup(func() {
		DeleteSessionNotificationDestination(parentSessionID)
		DeleteSessionNotificationDestination(childSessionID)
	})

	if !InheritSessionNotificationDestination(parentSessionID, childSessionID) {
		t.Fatal("expected child notification destination inheritance")
	}
	child := sessionNotificationDestination(childSessionID)
	if child == nil || child.WorkspacePath != "Workflow/demo" || child.WorkflowName != "demo" {
		t.Fatalf("inherited child destination = %#v", child)
	}

	// Prove the child owns an independent clone rather than an alias of parent
	// routing state.
	child.RouteSelections["org_dashboard"] = "disabled"
	parent := sessionNotificationDestination(parentSessionID)
	if parent == nil || parent.RouteSelections["org_dashboard"] != "enabled" {
		t.Fatalf("child mutation changed parent destination: %#v", parent)
	}
}

type testProductInteractionEmitter struct {
	testHumanFeedbackEmitter
	kind    string
	payload map[string]interface{}
}

func (e *testProductInteractionEmitter) EmitProductInteraction(kind string, payload map[string]interface{}) {
	e.kind, e.payload = kind, payload
}
