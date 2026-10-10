package virtualtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
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
	return "test_notify_user"
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

func TestNotifyUserDefaultsToBackendOwnedRichSlack(t *testing.T) {
	var description string
	var parameters string
	for _, tool := range CreateHumanTools() {
		if tool.Function != nil && tool.Function.Name == "notify_user" {
			description = tool.Function.Description
			raw, err := json.Marshal(tool.Function.Parameters)
			if err != nil {
				t.Fatalf("marshal notify_user parameters: %v", err)
			}
			parameters = string(raw)
			break
		}
	}
	for _, want := range []string{"backend-owned rich Block Kit", "slack_title", "slack_fields", "slack_sections", "Never access a SECRET_* webhook"} {
		if !strings.Contains(description+parameters, want) {
			t.Fatalf("notify_user contract missing %q:\ndescription=%s\nparameters=%s", want, description, parameters)
		}
	}
}

func TestNotificationHistoryToolAndDeliveryModesAreExposed(t *testing.T) {
	foundHistory := false
	foundDeliveryModes := false
	for _, tool := range CreateHumanTools() {
		if tool.Function == nil {
			continue
		}
		raw, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatalf("marshal %s parameters: %v", tool.Function.Name, err)
		}
		parameters := string(raw)
		switch tool.Function.Name {
		case "get_notification_history":
			foundHistory = strings.Contains(parameters, "exclude_pulse_run_id")
		case "notify_user":
			foundDeliveryModes = strings.Contains(parameters, "dashboard_only") && strings.Contains(parameters, "external_only")
		}
	}
	if !foundHistory {
		t.Fatal("get_notification_history tool or current-Pulse exclusion is missing")
	}
	if !foundDeliveryModes {
		t.Fatal("notify_user delivery modes are missing")
	}
	if _, ok := CreateHumanToolExecutors()["get_notification_history"]; !ok {
		t.Fatal("get_notification_history executor is not registered")
	}
}

func TestNotifyUserDeliveryModesSeparateDashboardAndExternalChannels(t *testing.T) {
	manager := services.GetNotificationManager()
	slackCh := make(chan *services.NotificationDestination, 2)
	dashboardCh := make(chan *services.NotificationDestination, 2)
	manager.RegisterConnector(&testUserNotificationConnector{name: "slack", ch: slackCh})
	manager.RegisterConnector(&testUserNotificationConnector{name: "org_dashboard", ch: dashboardCh})
	t.Cleanup(func() {
		manager.UnregisterConnector("slack")
		manager.UnregisterConnector("org_dashboard")
	})
	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{WorkspacePath: "Workflow/demo"})
	base := map[string]interface{}{
		"message_for_user":  "No material change.",
		"notification_kind": "run_summary",
		"summary_title":     "Daily run",
		"summary_status":    "completed",
	}

	base["delivery_mode"] = "dashboard_only"
	if _, err := handleNotifyUser(ctx, base); err != nil {
		t.Fatalf("dashboard-only notification: %v", err)
	}
	select {
	case <-dashboardCh:
	default:
		t.Fatal("dashboard_only did not reach Org Dashboard")
	}
	select {
	case <-slackCh:
		t.Fatal("dashboard_only reached Slack")
	default:
	}

	base["delivery_mode"] = "external_only"
	base["message_for_user"] = "A new blocker needs attention."
	if _, err := handleNotifyUser(ctx, base); err != nil {
		t.Fatalf("external-only notification: %v", err)
	}
	select {
	case <-slackCh:
	default:
		t.Fatal("external_only did not reach Slack")
	}
	select {
	case <-dashboardCh:
		t.Fatal("external_only duplicated the Org Dashboard entry")
	default:
	}
}

func TestPublishPulseUpdateIsNotAnAgentTool(t *testing.T) {
	for _, tool := range CreateHumanTools() {
		if tool.Function != nil && tool.Function.Name == "publish_pulse_update" {
			t.Fatal("publish_pulse_update must not be exposed; record_pulse_result powers Activity")
		}
	}
	if _, ok := CreateHumanToolExecutors()["publish_pulse_update"]; ok {
		t.Fatal("publish_pulse_update executor must not be registered")
	}
}

func TestNotifyUserExposesSingleHTMLBodyForGmail(t *testing.T) {
	manager := services.GetNotificationManager()
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: make(chan *services.NotificationDestination, 1)})
	t.Cleanup(func() { manager.UnregisterConnector("gmail") })

	var description string
	var parameters string
	for _, tool := range CreateHumanTools() {
		if tool.Function != nil && tool.Function.Name == "notify_user" {
			description = tool.Function.Description
			raw, err := json.Marshal(tool.Function.Parameters)
			if err != nil {
				t.Fatalf("marshal notify_user parameters: %v", err)
			}
			parameters = string(raw)
			break
		}
	}
	if !strings.Contains(parameters, `"email_html"`) {
		t.Fatalf("notify_user Gmail schema does not expose email_html: %s", parameters)
	}
	if strings.Contains(parameters, `"email_body"`) {
		t.Fatalf("notify_user Gmail schema still exposes retired email_body: %s", parameters)
	}
	for _, want := range []string{"one inline-styled email_html body", "message_for_user is the automatic fallback"} {
		if !strings.Contains(description, want) {
			t.Fatalf("notify_user Gmail description missing %q: %s", want, description)
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

func TestHandleNotifyUserUsesBotDestination(t *testing.T) {
	ch := make(chan *services.NotificationDestination, 1)
	connector := &testUserNotificationConnector{ch: ch}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(connector)
	t.Cleanup(func() {
		manager.UnregisterConnector(connector.Name())
	})

	ctx := context.WithValue(context.Background(), common.UserIDKey, "user-1")
	ctx = context.WithValue(ctx, BotNotificationDestinationKey, &services.NotificationDestination{
		Slack: &services.SlackDest{ChannelID: "C123", ThreadTS: "171.1"},
	})

	if _, err := handleNotifyUser(ctx, map[string]interface{}{"message_for_user": "FYI: done"}); err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.UserID != "user-1" {
			t.Fatalf("destination user = %#v, want user-1", dest)
		}
		if dest.Slack == nil || dest.Slack.ChannelID != "C123" || dest.Slack.ThreadTS != "171.1" {
			t.Fatalf("slack destination = %#v, want C123/171.1", dest.Slack)
		}
	case <-time.After(time.Second):
		t.Fatal("expected user notification")
	}
}

func TestHandleNotifyUserBuildsChannelNeutralOrgDashboardSummary(t *testing.T) {
	ch := make(chan *services.NotificationDestination, 1)
	connector := &testUserNotificationConnector{name: "org_dashboard_contract", ch: ch}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(connector)
	t.Cleanup(func() { manager.UnregisterConnector(connector.Name()) })

	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		WorkspacePath: "Workflow/demo",
	})
	_, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user":  "Run completed with one warning.",
		"notification_kind": "run_summary",
		"summary_title":     "Daily run",
		"summary_status":    "blocked",
		"summary_fields": []interface{}{
			map[string]interface{}{"label": "Processed", "value": "12"},
		},
	})
	if err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.WorkspacePath != "Workflow/demo" || dest.Content == nil || dest.Content.Summary == nil {
			t.Fatalf("neutral summary destination = %#v", dest)
		}
		summary := dest.Content.Summary
		if summary.Kind != "run_summary" || summary.Title != "Daily run" || summary.Status != "blocked" {
			t.Fatalf("neutral summary = %#v", summary)
		}
		if len(summary.Fields) != 1 || summary.Fields[0].Value != "12" {
			t.Fatalf("neutral summary fields = %#v", summary.Fields)
		}
		if summary.Status != "blocked" {
			t.Fatalf("summary status = %#v", summary)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Org Dashboard notification contract")
	}
}

func TestHandleNotifyUserPreservesWorkspacePathAcrossSessionRegistry(t *testing.T) {
	ch := make(chan *services.NotificationDestination, 1)
	connector := &testUserNotificationConnector{name: "org_dashboard_session_contract", ch: ch}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(connector)
	t.Cleanup(func() { manager.UnregisterConnector(connector.Name()) })

	const sessionID = "org-dashboard-session-workspace-path"
	RegisterSessionNotificationDestination(sessionID, &services.NotificationDestination{
		WorkspacePath: "Workflow/demo",
	})
	t.Cleanup(func() { DeleteSessionNotificationDestination(sessionID) })

	// Custom tools execute through a separate MCP request context. Only the
	// trusted session ID crosses that boundary, so notify_user must recover the
	// workflow path from the session destination registry.
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	_, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user":  "Run completed.",
		"notification_kind": "run_summary",
		"summary_title":     "Daily run",
		"summary_status":    "success",
	})
	if err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.WorkspacePath != "Workflow/demo" {
			t.Fatalf("session notification workspace path = %#v, want Workflow/demo", dest)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Org Dashboard notification contract")
	}
}

func TestChildSessionInheritsNotificationDestination(t *testing.T) {
	const parentSessionID = "notification-parent-session"
	const childSessionID = "notification-child-session"
	RegisterSessionNotificationDestination(parentSessionID, &services.NotificationDestination{
		UserID:             "user-1",
		WorkflowName:       "demo",
		WorkspacePath:      "Workflow/demo",
		RouteSelections:    map[string]string{"org_dashboard": "enabled", "slack": "enabled"},
		RunSummaryChannels: []string{"org_dashboard", "slack"},
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
	child.RunSummaryChannels[0] = "changed"
	parent := sessionNotificationDestination(parentSessionID)
	if parent == nil || parent.RouteSelections["org_dashboard"] != "enabled" || parent.RunSummaryChannels[0] != "org_dashboard" {
		t.Fatalf("child mutation changed parent destination: %#v", parent)
	}
}

func TestChildSessionNotifyUserUsesInheritedWorkspacePath(t *testing.T) {
	ch := make(chan *services.NotificationDestination, 1)
	connector := &testUserNotificationConnector{name: "org_dashboard_child_session_contract", ch: ch}
	manager := services.GetNotificationManager()
	manager.RegisterConnector(connector)
	t.Cleanup(func() { manager.UnregisterConnector(connector.Name()) })

	const parentSessionID = "org-dashboard-parent-session"
	const childSessionID = "org-dashboard-child-session"
	RegisterSessionNotificationDestination(parentSessionID, &services.NotificationDestination{
		WorkspacePath: "Workflow/demo",
	})
	if !InheritSessionNotificationDestination(parentSessionID, childSessionID) {
		t.Fatal("expected child notification destination inheritance")
	}
	t.Cleanup(func() {
		DeleteSessionNotificationDestination(parentSessionID)
		DeleteSessionNotificationDestination(childSessionID)
	})

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, childSessionID)
	_, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user":  "Child run completed.",
		"notification_kind": "run_summary",
		"summary_title":     "Child run",
		"summary_status":    "success",
	})
	if err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.WorkspacePath != "Workflow/demo" {
			t.Fatalf("child notification workspace path = %#v, want Workflow/demo", dest)
		}
	case <-time.After(time.Second):
		t.Fatal("expected child Org Dashboard notification contract")
	}
}

func TestNotificationSummaryUsesSemanticStatusWithoutLifecycleBookkeeping(t *testing.T) {
	summary, err := notificationSummaryFromArgs(map[string]interface{}{
		"summary_status": "waiting_for_user",
	}, "run_summary", nil, services.SlackWebhookContent{})
	if err != nil {
		t.Fatalf("semantic summary status error = %v", err)
	}
	if summary.Status != "waiting_for_user" {
		t.Fatalf("summary status = %q, want waiting_for_user", summary.Status)
	}
}

func TestHandleNotifyUserSendsWorkflowSlackWebhook(t *testing.T) {
	original := sendRichSlackIncomingWebhook
	t.Cleanup(func() { sendRichSlackIncomingWebhook = original })

	called := false
	sendRichSlackIncomingWebhook = func(_ context.Context, webhookURL, message string, content services.SlackWebhookContent) (string, error) {
		called = true
		if webhookURL != "https://hooks.slack.com/services/T123/B456/secret" {
			t.Fatalf("unexpected webhook URL")
		}
		if message != "Workflow finished" {
			t.Fatalf("message = %q", message)
		}
		if content.Title != "QA complete" || content.Color != "success" || len(content.Fields) != 1 || content.Fields[0].Label != "Passed" {
			t.Fatalf("rich Slack content = %#v", content)
		}
		return "webhook_ok", nil
	}

	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		SlackWebhook: &services.SlackWebhookDest{
			SecretName: "SLACK_NOTIFICATION_WEBHOOK_URL",
			URL:        "https://hooks.slack.com/services/T123/B456/secret",
		},
	})
	raw, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user": "Workflow finished",
		"slack_title":      "QA complete",
		"slack_color":      "success",
		"slack_fields": []interface{}{
			map[string]interface{}{"label": "Passed", "value": "12"},
		},
	})
	if err != nil {
		t.Fatalf("handleNotifyUser: %v", err)
	}
	if !called {
		t.Fatal("workflow Slack webhook was not called")
	}
	var result struct {
		Status    string   `json:"status"`
		Delivered []string `json:"delivered"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != "delivered" {
		t.Fatalf("status = %q, result=%s", result.Status, raw)
	}
	found := false
	for _, channel := range result.Delivered {
		if channel == "slack_webhook" {
			found = true
		}
	}
	if !found {
		t.Fatalf("slack_webhook missing from delivered: %v", result.Delivered)
	}
}

func TestHandleNotifyUserUsesSameSessionWebhookAcrossBridgeContext(t *testing.T) {
	const sessionID = "builder-slack-refresh-bridge-test"
	DeleteSessionNotificationDestination(sessionID)
	t.Cleanup(func() { DeleteSessionNotificationDestination(sessionID) })

	RegisterSessionNotificationDestination(sessionID, &services.NotificationDestination{UserID: "user-1"})
	UpdateSessionSlackWebhook(sessionID, &services.SlackWebhookDest{
		SecretName: "SLACK_NOTIFICATION_WEBHOOK_URL",
		URL:        "https://hooks.slack.com/services/T123/B456/session-secret",
	})

	original := sendRichSlackIncomingWebhook
	t.Cleanup(func() { sendRichSlackIncomingWebhook = original })
	called := false
	sendRichSlackIncomingWebhook = func(_ context.Context, webhookURL, message string, _ services.SlackWebhookContent) (string, error) {
		called = true
		if webhookURL != "https://hooks.slack.com/services/T123/B456/session-secret" {
			t.Fatalf("webhook URL = %q, want refreshed session webhook", webhookURL)
		}
		if message != "Same-turn notification" {
			t.Fatalf("message = %q", message)
		}
		return "webhook_ok", nil
	}

	// This is the context shape created by mcpagent's per-tool HTTP bridge: it
	// contains the trusted session identity, not the original agentCtx values.
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)
	raw, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user": "Same-turn notification",
	})
	if err != nil {
		t.Fatalf("handleNotifyUser: %v", err)
	}
	if !called {
		t.Fatalf("Slack webhook was not called; result=%s", raw)
	}
	if !strings.Contains(raw, `"slack_webhook"`) {
		t.Fatalf("result does not report Slack delivery: %s", raw)
	}
}

func TestHandleNotifyUserFansOutToRegisteredConnectors(t *testing.T) {
	manager := services.GetNotificationManager()
	whatsappCh := make(chan *services.NotificationDestination, 1)
	slackCh := make(chan *services.NotificationDestination, 1)
	whatsappConnector := &testUserNotificationConnector{name: "whatsapp", ch: whatsappCh}
	slackConnector := &testUserNotificationConnector{name: "slack", ch: slackCh}
	manager.RegisterConnector(whatsappConnector)
	manager.RegisterConnector(slackConnector)
	t.Cleanup(func() {
		manager.UnregisterConnector("whatsapp")
		manager.UnregisterConnector("slack")
	})

	ctx := context.WithValue(context.Background(), common.UserIDKey, "user-1")
	ctx = context.WithValue(ctx, BotNotificationDestinationKey, &services.NotificationDestination{
		UserID:   "user-1",
		WhatsApp: &services.WhatsAppDest{ChannelID: "919000000000@s.whatsapp.net"},
	})

	if _, err := handleNotifyUser(ctx, map[string]interface{}{"message_for_user": "FYI: done"}); err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-whatsappCh:
		if dest == nil || dest.WhatsApp == nil || dest.WhatsApp.ChannelID == "" {
			t.Fatalf("destination = %#v, want WhatsApp destination", dest)
		}
	case <-time.After(time.Second):
		t.Fatal("expected WhatsApp notification")
	}

	select {
	case <-slackCh:
	case <-time.After(time.Second):
		t.Fatal("expected Slack connector to be considered in fanout")
	}
}

func TestHandleNotifyUserEnforcesSummaryChannelRoute(t *testing.T) {
	manager := services.GetNotificationManager()
	gmailCh := make(chan *services.NotificationDestination, 1)
	slackCh := make(chan *services.NotificationDestination, 1)
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: gmailCh})
	manager.RegisterConnector(&testUserNotificationConnector{name: "slack", ch: slackCh})
	t.Cleanup(func() {
		manager.UnregisterConnector("gmail")
		manager.UnregisterConnector("slack")
	})

	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		UserID:               "user-1",
		RunSummaryChannels:   []string{"slack"},
		PulseSummaryChannels: []string{"gmail"},
	})
	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"email_subject":     "Notification routing test",
		"message_for_user":  "Run complete",
		"notification_kind": "run_summary",
	}); err != nil {
		t.Fatalf("run summary notification: %v", err)
	}
	select {
	case <-slackCh:
	case <-time.After(time.Second):
		t.Fatal("expected run summary on Slack")
	}
	select {
	case <-gmailCh:
		t.Fatal("run summary must not be sent to Gmail")
	default:
	}

	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"email_subject":     "Notification routing test",
		"message_for_user":  "Pulse complete",
		"notification_kind": "pulse_summary",
	}); err != nil {
		t.Fatalf("pulse summary notification: %v", err)
	}
	select {
	case <-gmailCh:
	case <-time.After(time.Second):
		t.Fatal("expected Pulse summary on Gmail")
	}
	select {
	case <-slackCh:
		t.Fatal("Pulse summary must not be sent to Slack")
	default:
	}
}

func TestHandleNotifyUserSuppressesWorkflowSlackWebhookForGmailOnlyPulse(t *testing.T) {
	manager := services.GetNotificationManager()
	gmailCh := make(chan *services.NotificationDestination, 1)
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: gmailCh})
	t.Cleanup(func() { manager.UnregisterConnector("gmail") })

	oldSend := sendRichSlackIncomingWebhook
	webhookCalled := false
	sendRichSlackIncomingWebhook = func(context.Context, string, string, services.SlackWebhookContent) (string, error) {
		webhookCalled = true
		return "webhook-message", nil
	}
	t.Cleanup(func() { sendRichSlackIncomingWebhook = oldSend })

	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		UserID:               "user-1",
		SlackWebhook:         &services.SlackWebhookDest{URL: "https://hooks.slack.com/services/test"},
		PulseSummaryChannels: []string{"gmail"},
	})
	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"email_subject":     "Notification routing test",
		"message_for_user":  "Pulse complete",
		"notification_kind": "pulse_summary",
	}); err != nil {
		t.Fatalf("pulse summary notification: %v", err)
	}
	if webhookCalled {
		t.Fatal("Gmail-only Pulse summary must not reach the workflow Slack webhook")
	}
}

func TestHandleNotifyUserAddsWorkflowNameToRichEmail(t *testing.T) {
	manager := services.GetNotificationManager()
	ch := make(chan *services.NotificationDestination, 1)
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: ch})
	t.Cleanup(func() { manager.UnregisterConnector("gmail") })

	ctx := context.WithValue(context.Background(), BotNotificationDestinationKey, &services.NotificationDestination{
		UserID:       "user-1",
		WorkflowName: "latency-report",
	})
	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user":  "Pulse completed",
		"notification_kind": "pulse_summary",
		"email_subject":     "Pulse summary",
		"email_html":        `<div>Three findings remain pending.</div>`,
		"summary_routes": []interface{}{map[string]interface{}{
			"routing_step_id": "review_router", "route_id": "security", "label": "Security",
			"title": "Review complete", "status": "completed", "message": "route facts already rendered by the email card",
		}},
	}); err != nil {
		t.Fatalf("Pulse notification: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.Content == nil || dest.Content.Gmail == nil {
			t.Fatalf("destination = %#v, want rich Gmail content", dest)
		}
		gmail := dest.Content.Gmail
		if gmail.Subject != "latency-report · Pulse summary" {
			t.Fatalf("subject = %q", gmail.Subject)
		}
		if !strings.Contains(gmail.HTMLBody, "Workflow: <strong") || !strings.Contains(gmail.HTMLBody, "latency-report</strong>") {
			t.Fatalf("email body missing workflow header: %s", gmail.HTMLBody)
		}
		if strings.Contains(gmail.HTMLBody, "route facts already rendered") || strings.Count(gmail.HTMLBody, "Three findings remain pending.") != 1 {
			t.Fatalf("email body duplicated route or card content: %s", gmail.HTMLBody)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Gmail notification")
	}
}

func TestHandleNotifyUserPreservesWorkflowNameThroughSessionDestination(t *testing.T) {
	manager := services.GetNotificationManager()
	ch := make(chan *services.NotificationDestination, 1)
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: ch})
	t.Cleanup(func() { manager.UnregisterConnector("gmail") })

	const sessionID = "pulse-workflow-email-test"
	RegisterSessionNotificationDestination(sessionID, &services.NotificationDestination{
		UserID:       "user-1",
		WorkflowName: "social-media",
	})
	t.Cleanup(func() { DeleteSessionNotificationDestination(sessionID) })
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user": "Pulse completed",
		"email_subject":    "Pulse summary",
		"email_html":       `<div>Complete.</div>`,
	}); err != nil {
		t.Fatalf("Pulse notification: %v", err)
	}

	select {
	case dest := <-ch:
		gmail := dest.Content.Gmail
		if gmail.Subject != "social-media · Pulse summary" || !strings.Contains(gmail.HTMLBody, "social-media</strong>") {
			t.Fatalf("workflow identity was not preserved through session destination: %#v", gmail)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Gmail notification")
	}
}

// The per-workflow recipient lists are the positive counterpart to
// block_recipients: they say where a summary is emailed. The backend addresses
// the mail from the list matching notification_kind; the agent cannot name recipients.
func TestNotifyUserRoutesToConfiguredRecipientsByKind(t *testing.T) {
	baseDest := func() *services.NotificationDestination {
		return &services.NotificationDestination{
			UserID:                 "user-1",
			Gmail:                  &services.GmailDest{Email: "account-default@example.com"},
			RunSummaryRecipients:   []string{"run@example.com", "ops@example.com"},
			PulseSummaryRecipients: []string{"pulse@example.com"},
		}
	}

	for _, tc := range []struct {
		name string
		kind string
		want string
	}{
		{name: "run summary uses the run list", kind: "run_summary", want: "run@example.com, ops@example.com"},
		{name: "pulse summary uses the pulse list", kind: "pulse_summary", want: "pulse@example.com"},
		// An unclassified send has no configured list and must fall through to
		// whatever the destination already resolved, exactly as channel routing does.
		{name: "general falls back to the account default", kind: "general", want: "account-default@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := services.GetNotificationManager()
			ch := make(chan *services.NotificationDestination, 1)
			manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: ch})
			t.Cleanup(func() { manager.UnregisterConnector("gmail") })

			ctx := context.WithValue(context.Background(), common.UserIDKey, "user-1")
			ctx = context.WithValue(ctx, BotNotificationDestinationKey, baseDest())

			if _, err := handleNotifyUser(ctx, map[string]interface{}{
				"email_subject":     "Notification routing test",
				"message_for_user":  "FYI: done",
				"notification_kind": tc.kind,
			}); err != nil {
				t.Fatalf("handleNotifyUser returned error: %v", err)
			}

			select {
			case dest := <-ch:
				if dest == nil || dest.Gmail == nil || dest.Gmail.Email != tc.want {
					t.Fatalf("gmail To = %#v, want %q", dest.Gmail, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("expected Gmail notification")
			}
		})
	}
}

// Configured recipients say where mail GOES; they never widen permission. An
// address on the workflow denylist must stay carried on the destination so the
// Gmail connector still rejects it.
func TestConfiguredRecipientsKeepWorkflowDenylist(t *testing.T) {
	manager := services.GetNotificationManager()
	ch := make(chan *services.NotificationDestination, 1)
	manager.RegisterConnector(&testUserNotificationConnector{name: "gmail", ch: ch})
	t.Cleanup(func() { manager.UnregisterConnector("gmail") })

	ctx := context.WithValue(context.Background(), common.UserIDKey, "user-1")
	ctx = context.WithValue(ctx, BotNotificationDestinationKey, &services.NotificationDestination{
		UserID:               "user-1",
		Gmail:                &services.GmailDest{BlockedRecipients: []string{"blocked@example.com"}},
		RunSummaryRecipients: []string{"blocked@example.com"},
	})

	if _, err := handleNotifyUser(ctx, map[string]interface{}{
		"email_subject":     "Notification routing test",
		"message_for_user":  "FYI: done",
		"notification_kind": "run_summary",
	}); err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}

	select {
	case dest := <-ch:
		if dest == nil || dest.Gmail == nil {
			t.Fatalf("gmail destination = %#v, want Gmail dest", dest)
		}
		if len(dest.Gmail.BlockedRecipients) != 1 || dest.Gmail.BlockedRecipients[0] != "blocked@example.com" {
			t.Fatalf("blocked recipients = %#v, want the workflow denylist preserved", dest.Gmail.BlockedRecipients)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Gmail notification")
	}
}

// A Slack Incoming Webhook is bound to one channel, so per-summary channels are
// per-summary webhooks. The kind selects the set; a kind with none configured
// falls back to the workflow's single webhook rather than posting nowhere.
func TestWebhooksForKindSelectsChannelsAndFallsBack(t *testing.T) {
	runHook := services.SlackWebhookDest{SecretName: "SLACK_RUNS", URL: "https://hooks.slack.com/services/T/B/runs"}
	pulseHook := services.SlackWebhookDest{SecretName: "SLACK_PULSE", URL: "https://hooks.slack.com/services/T/B/pulse"}
	fallback := services.SlackWebhookDest{SecretName: "SLACK_DEFAULT", URL: "https://hooks.slack.com/services/T/B/default"}

	dest := &services.NotificationDestination{
		SlackWebhook:         &fallback,
		RunSummaryWebhooks:   []services.SlackWebhookDest{runHook},
		PulseSummaryWebhooks: []services.SlackWebhookDest{pulseHook},
	}

	if got := webhooksForKind(dest, "run_summary"); len(got) != 1 || got[0].SecretName != "SLACK_RUNS" {
		t.Fatalf("run_summary webhooks = %#v, want the run channel", got)
	}
	if got := webhooksForKind(dest, "pulse_summary"); len(got) != 1 || got[0].SecretName != "SLACK_PULSE" {
		t.Fatalf("pulse_summary webhooks = %#v, want the pulse channel", got)
	}
	// "general" has no configured channel of its own, and a kind whose list is
	// empty must not go silent — both fall back to the single workflow webhook.
	if got := webhooksForKind(dest, "general"); len(got) != 1 || got[0].SecretName != "SLACK_DEFAULT" {
		t.Fatalf("general webhooks = %#v, want the fallback channel", got)
	}
	noPulse := &services.NotificationDestination{SlackWebhook: &fallback}
	if got := webhooksForKind(noPulse, "pulse_summary"); len(got) != 1 || got[0].SecretName != "SLACK_DEFAULT" {
		t.Fatalf("unconfigured pulse = %#v, want the fallback channel", got)
	}
	// No webhook at all anywhere means no Slack post, not a post to an empty URL.
	if got := webhooksForKind(&services.NotificationDestination{}, "run_summary"); len(got) != 0 {
		t.Fatalf("no webhooks configured = %#v, want none", got)
	}
}

// The delivery report is what the agent repeats back to the user. Adding
// per-channel routing must not rename the single-channel result, or every
// existing workflow's report silently changes shape.
func TestWebhookResultChannelLabel(t *testing.T) {
	hook := services.SlackWebhookDest{SecretName: "SLACK_RUNS"}
	if got := webhookResultChannel(hook, false); got != "slack_webhook" {
		t.Fatalf("single-channel label = %q, want slack_webhook", got)
	}
	if got := webhookResultChannel(hook, true); got != "slack_webhook:SLACK_RUNS" {
		t.Fatalf("multi-channel label = %q, want the qualified name", got)
	}
}

func TestNotificationRouteFromSelections(t *testing.T) {
	if got := notificationRouteFromSelections(map[string]string{"top-router": "publish-reddit"}); got != "publish-reddit" {
		t.Fatalf("single route = %q", got)
	}
	got := notificationRouteFromSelections(map[string]string{"router-b": "email", "router-a": "slack"})
	if got != "router-a=slack · router-b=email" {
		t.Fatalf("multiple routes = %q", got)
	}
}

func TestNotifyUsesOnlyOneConfiguredGmailSender(t *testing.T) {
	dest := &services.NotificationDestination{
		Gmail:                        &services.GmailDest{ConnectionIDs: []string{"gmail_default"}},
		RunSummaryGmailConnectionIDs: []string{"gmail_run"},
	}
	if got, err := notificationSenderForKind(dest, "run_summary"); err != nil || got != "gmail_run" {
		t.Fatalf("run sender = %q, %v", got, err)
	}
	if got, err := notificationSenderForKind(dest, "pulse_summary"); err != nil || got != "gmail_default" {
		t.Fatalf("pulse sender = %q, %v", got, err)
	}
	dest.RunSummaryGmailConnectionIDs = []string{"gmail_run", "gmail_other"}
	if _, err := notificationSenderForKind(dest, "run_summary"); err == nil {
		t.Fatal("legacy multi-sender Notify setting must not fan out")
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

// The in-app copy of a notification rides the same product_interaction
// event a product.yaml tool emits, and it fires because the tool ran — not
// because any delivery channel is configured (here none is), and not
// because a coding CLI narrated the call in some recognizable shape.
func TestHandleNotifyUserEmitsInAppCopyRegardlessOfChannels(t *testing.T) {
	emitter := &testProductInteractionEmitter{}
	ctx := context.WithValue(context.Background(), common.UserIDKey, "user-1")
	ctx = context.WithValue(ctx, SessionEventEmitterKey, emitter)

	raw, err := handleNotifyUser(ctx, map[string]interface{}{
		"message_for_user": "Myra finished fractions today.",
		"summary_title":    "Check-in",
	})
	if err != nil {
		t.Fatalf("handleNotifyUser returned error: %v", err)
	}
	if emitter.kind != "notify" {
		t.Fatalf("interaction kind = %q, want notify", emitter.kind)
	}
	if emitter.payload["title"] != "Check-in" || emitter.payload["message"] != "Myra finished fractions today." {
		t.Fatalf("payload = %#v", emitter.payload)
	}
	// No Gmail/Slack/WhatsApp/dashboard is configured in this test — before
	// the in-app copy counted toward delivery, the agent was told this
	// notification failed even though the person at the screen had already
	// seen it.
	var result struct {
		Status    string   `json:"status"`
		Delivered []string `json:"delivered"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status != "delivered" {
		t.Fatalf("status = %q, want delivered (in-app should count): %s", result.Status, raw)
	}
	if len(result.Delivered) != 1 || result.Delivered[0] != "in_app" {
		t.Fatalf("delivered = %#v, want [in_app]", result.Delivered)
	}
}

// notify_user cannot name recipients (PLAT-736): an old caller that still passes email_to or email_cc gets a clear
// error that points to send_email, never a silent send to the default recipient.
func TestNotifyUserRejectsRemovedRecipientArguments(t *testing.T) {
	for _, argument := range []string{"email_to", "email_cc"} {
		_, err := handleNotifyUser(context.Background(), map[string]interface{}{
			"message_for_user": "FYI: done",
			argument:           []interface{}{"someone@example.com"},
		})
		if err == nil || !strings.Contains(err.Error(), "send_email") {
			t.Fatalf("%s must be rejected with a pointer to send_email, got %v", argument, err)
		}
	}
}
