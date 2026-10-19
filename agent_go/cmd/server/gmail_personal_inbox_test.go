package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

func TestGmailPersonalInboxFansOutOnlyToApprovedBindings(t *testing.T) {
	api, ctx, mock := gmailTriggerFixture(t)
	manifest := NewWorkflowManifest("Second target")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	raw, _ := json.Marshal(manifest)
	mock.mu.Lock()
	mock.files["Workflow/second/workflow.json"] = string(raw)
	mock.files["Workflow/second/planning/plan.json"] = mock.files["Workflow/mail/planning/plan.json"]
	mock.files["Workflow/second/variables/variables.json"] = mock.files["Workflow/mail/variables/variables.json"]
	mock.mu.Unlock()
	for _, path := range []string{"Workflow/mail", "Workflow/second"} {
		if _, err := api.gmailTriggerToolRequest(ctx, "human", path, map[string]interface{}{"action": "configure", "group_names": []string{"prod"}, "route_selections": map[string]string{"triage": "support"}, "reply": false}); err != nil {
			t.Fatal(err)
		}
	}
	routes, err := api.gmailInbound.Store.Routes(ctx)
	if err != nil || len(routes) != 2 || routes[0].Address != routes[1].Address || routes[0].ID == routes[1].ID {
		t.Fatalf("not one inbox with independent bindings: %+v %v", routes, err)
	}
	for _, route := range routes {
		stored, _, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
		data, _ := json.Marshal(stored)
		if err != nil || strings.Contains(strings.ToLower(string(data)), route.Address) {
			t.Fatal("personal address leaked into shared configuration")
		}
	}
	message := gmailinbound.Message{ID: "message", ThreadID: "thread", From: "notifications@vendor.example", Automatic: true, Authenticated: true, Recipients: []string{routes[0].Address}, ReceivedAt: time.Now().Add(time.Minute).UnixMilli()}
	// A default binding needs approval even without any sender/content rule.
	if err := api.authorizeInboundEmail(ctx, routes[0], message); err == nil {
		t.Fatal("unapproved default inbox binding executed")
	}
	for _, route := range routes {
		approveGmailSendersForTest(t, api, route)
	}
	// This real authorizer reads consent from the same one-connection SQLite
	// store. Keeping it inside admission's transaction used to deadlock.
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := api.gmailInbound.Store.EnqueueAuthorized(bounded, routes[0], message, func(context.Context, gmailinbound.Route, gmailinbound.Message) error {
		return gmailinbound.ErrAdmissionRetry
	}); err == nil {
		t.Fatal("temporary admission failure was swallowed")
	}
	for repeat := 0; repeat < 2; repeat++ {
		for _, route := range routes {
			if err := api.gmailInbound.Store.EnqueueAuthorized(bounded, route, message, api.authorizeInboundEmail); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, route := range routes {
		history, err := api.gmailInbound.Store.History(ctx, route.ID)
		if err != nil || len(history) != 1 {
			t.Fatalf("binding dedup/fan-out: %+v %v", history, err)
		}
	}
	if _, err := api.gmailTriggerToolRequest(ctx, "human", routes[0].WorkspacePath, map[string]interface{}{"action": "disable"}); err != nil {
		t.Fatal(err)
	}
	current, _ := api.gmailInbound.Store.Routes(ctx)
	for _, route := range current {
		if route.ID == routes[0].ID && api.authorizeInboundEmail(ctx, route, message) == nil {
			t.Fatal("disabled binding executed")
		}
	}
	inbox, _ := api.gmailInbound.Store.Inbox(ctx, "owner")
	oldAddress := inbox.Address
	inbox.Address = "owner+agent-fedcba98765443219876543210abcdef@example.com"
	if err := api.gmailInbound.Store.SaveInbox(ctx, *inbox); err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		if api.authorizeInboundEmail(ctx, route, message) == nil {
			t.Fatal("old address remained usable after rotation")
		}
		if api.gmailInbound.Store.ConfirmSenderConsent(ctx, "owner", route.ID, gmailinbound.SenderPolicyHash(route), true) == nil {
			t.Fatal("old address could be approved again")
		}
	}
	if oldAddress == inbox.Address {
		t.Fatal("rotation kept the old address")
	}
}

func TestGmailPersonalInboxUsesOwnSavedAccountWithoutGrantingProducts(t *testing.T) {
	api, ctx, mock := gmailTriggerFixture(t)
	svc := services.GetGmailService()
	cfg := svc.GetConfig()
	cfg.Connections[0].Personal = true
	cfg.Connections[0].Workspaces = []string{"Workflow/mail"}
	raw, _ := json.Marshal(cfg)
	mock.mu.Lock()
	mock.files["config/gmail-config.json"] = string(raw)
	mock.mu.Unlock()
	if err := svc.ReloadConfig(ctx); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	for _, action := range []string{"select_inbox", "rotate_inbox"} {
		if _, err := api.personalGmailInboxOperation(req, "Workflow/mail", map[string]interface{}{"action": action, "connection_id": "mail"}); err != nil {
			t.Fatal(err)
		}
	}
	inbox, _ := api.gmailInbound.Store.Inbox(ctx, "owner")
	if inbox == nil || inbox.ConnectionID != "mail" || !strings.Contains(inbox.Address, "+agent-") || strings.Contains(inbox.Address, inbox.ID) {
		t.Fatalf("bad identity: %+v", inbox)
	}
	account, _ := svc.GetConnection("mail")
	if len(account.Workspaces) != 1 || account.Workspaces[0] != "Workflow/mail" || len(svc.ListConnections()) != 1 {
		t.Fatal("inbox setup created a connection or changed item grants")
	}
	client, err := inboundClientFor("mail", gmailInboundConfig{Topics: map[string]string{"app": "projects/test-project/topics/mail"}})
	if err != nil || !account.UsableFrom(client.Scope) {
		t.Fatalf("personal watch did not retain selected scope: %+v %v", client.Scope, err)
	}
	reader := sharedSecretsRequest("POST", "/", "reader", nil)
	if _, err := api.personalGmailInboxOperation(reader, "Workflow/mail", map[string]interface{}{"action": "select_inbox", "connection_id": "mail"}); err == nil {
		t.Fatal("another person selected the owner's inbox")
	}
}

func TestGmailExecutionDataDoesNotPublishPersonalInboxCapability(t *testing.T) {
	route := gmailinbound.Route{Address: "owner+agent-random@example.com"}
	original := gmailinbound.Message{Recipients: []string{route.Address}, From: "sender@example.com", Subject: "Fwd to " + strings.ToUpper(route.Address), Body: "To: " + route.Address + "\nCc: " + strings.ToUpper(route.Address) + "\nDelivered-To: " + route.Address + "\nQuoted reply: write to " + route.Address, Attachments: []gmailinbound.Attachment{{Name: route.Address + ".txt"}}}
	before, _ := json.Marshal(original)
	view := gmailAgentMessage(route, original)
	data, _ := json.Marshal(view)
	if strings.Contains(strings.ToLower(string(data)), route.Address) || len(view.Recipients) != 0 {
		t.Fatal("personal address leaked in workflow input")
	}
	query, err := gmailChatQuery(route, original)
	if err != nil || strings.Contains(strings.ToLower(query), route.Address) || !strings.Contains(query, "untrusted content") {
		t.Fatal("personal address leaked into project chat")
	}
	after, _ := json.Marshal(original)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("formatting changed original authorization or reply data")
	}
}
