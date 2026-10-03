package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/gmailinbound"
)

func gmailConsentRequest(t *testing.T, api *StreamingAPI, owner, path, hash, action string) *httptest.ResponseRecorder {
	t.Helper()
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	token, err := GenerateJWT(owner, owner, owner+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	req := sharedSecretsRequest("POST", "/api/gmail-inbound/sender-consent", owner, map[string]interface{}{"workspace_path": path, "config_hash": hash, "action": action})
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	AuthMiddleware(http.HandlerFunc(api.gmailSenderConsent)).ServeHTTP(w, req)
	return w
}

func approveGmailSendersForTest(t *testing.T, api *StreamingAPI, route gmailinbound.Route) {
	t.Helper()
	w := gmailConsentRequest(t, api, route.OwnerID, route.WorkspacePath, gmailinbound.SenderPolicyHash(route), "approve")
	if w.Code != http.StatusOK {
		t.Fatalf("owner browser approval failed: %d %s", w.Code, w.Body.String())
	}
}

func TestGmailSenderConsentBlocksInjectedProposalsAndStaleApproval(t *testing.T) {
	api, ctx, _ := gmailTriggerFixture(t)
	args := map[string]interface{}{"action": "configure", "connection_id": "mail", "group_names": []string{"prod"}, "route_selections": map[string]string{"triage": "support"}, "filters": map[string]interface{}{"sender_allowlist": []string{"owner@example.com", "evil@attacker.example"}}}
	if _, err := api.gmailTriggerToolRequest(ctx, "human", "Workflow/mail", args); err != nil {
		t.Fatal(err)
	}
	routes, _ := api.gmailInbound.Store.Routes(ctx)
	route := routes[0]
	message := gmailinbound.Message{From: "evil@attacker.example", Authenticated: true, Recipients: []string{route.Address}, ReceivedAt: time.Now().Add(time.Minute).UnixMilli()}
	if err := api.authorizeInboundEmail(ctx, route, message); err == nil {
		t.Fatal("injected agent configuration activated an external sender")
	}
	message.From = "owner@example.com"
	if err := api.authorizeInboundEmail(ctx, route, message); err != nil {
		t.Fatalf("owner lost the owner-only path: %v", err)
	}
	for _, key := range []string{"approved", "sender_consent", "confirm"} {
		if _, err := api.gmailTriggerToolRequest(ctx, "human", "Workflow/mail", map[string]interface{}{"action": "configure", key: true}); err == nil {
			t.Fatalf("agent supplied approval through %s", key)
		}
	}
	for _, action := range []string{"approve", "revoke"} {
		if _, err := api.gmailTriggerToolRequest(ctx, "human", "Workflow/mail", map[string]interface{}{"action": action}); err == nil {
			t.Fatalf("agent tool exposes consent action %s", action)
		}
	}
	// Even a trusted owner context from an internal tool request is insufficient.
	for _, auth := range []string{"", "Bearer agent-bridge-token", "Bearer awp_agent-token"} {
		req := sharedSecretsRequest("POST", "/api/gmail-inbound/sender-consent", "owner", map[string]interface{}{"workspace_path": "Workflow/mail", "config_hash": gmailinbound.SenderPolicyHash(route), "action": "approve"})
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		api.gmailSenderConsent(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("agent credential can confirm: %d %s", w.Code, w.Body.String())
		}
	}
	// A real JWT for a different person also cannot bless an internal owner
	// context. Identity canonicalization must still match the login's signer.
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	otherToken, err := GenerateJWT("reader", "reader", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	forged := sharedSecretsRequest("POST", "/api/gmail-inbound/sender-consent", "owner", nil)
	forged.Header.Set("Authorization", "Bearer "+otherToken)
	if gmailSenderConsentLogin(forged) {
		t.Fatal("a different login blessed a fabricated owner context")
	}
	w := gmailConsentRequest(t, api, "reader", "Workflow/mail", gmailinbound.SenderPolicyHash(route), "approve")
	if w.Code != http.StatusForbidden {
		t.Fatalf("workflow reader approved another owner's senders: %d %s", w.Code, w.Body.String())
	}
	approveGmailSendersForTest(t, api, route)
	message.From = "evil@attacker.example"
	if err := api.authorizeInboundEmail(ctx, route, message); err != nil {
		t.Fatalf("explicit owner receipt not honored: %v", err)
	}
	oldHash := gmailinbound.SenderPolicyHash(route)
	if _, err := api.gmailTriggerToolRequest(ctx, "human", "Workflow/mail", map[string]interface{}{"action": "configure", "reply": false}); err != nil {
		t.Fatal(err)
	}
	routes, _ = api.gmailInbound.Store.Routes(ctx)
	route = routes[0]
	if err := api.authorizeInboundEmail(ctx, route, message); err == nil {
		t.Fatal("configuration edit retained sender authority")
	}
	w = gmailConsentRequest(t, api, "owner", "Workflow/mail", oldHash, "approve")
	if w.Code != http.StatusConflict {
		t.Fatalf("stale confirmation accepted: %d %s", w.Code, w.Body.String())
	}
	approveGmailSendersForTest(t, api, route)
	w = gmailConsentRequest(t, api, "owner", "Workflow/mail", gmailinbound.SenderPolicyHash(route), "revoke")
	if w.Code != http.StatusOK || api.authorizeInboundEmail(ctx, route, message) == nil {
		t.Fatal("revocation left external sender authority")
	}
	// Queued execution and reply paths both use the same current authorization.
	if err := api.dispatchInboundEmail(context.Background(), &gmailinbound.Delivery{Route: route, Message: message}); err == nil {
		t.Fatal("queued execution bypassed revocation")
	}
	if err := api.replyInboundEmail(context.Background(), gmailinbound.Delivery{Route: route, Message: message}); err == nil {
		t.Fatal("queued email reply bypassed revocation")
	}
}

func TestGmailBuilderRejectsPublicDomainsInCommonAndRuleFilters(t *testing.T) {
	api, ctx, _ := gmailTriggerFixture(t)
	for _, rules := range []bool{false, true} {
		args := map[string]interface{}{"action": "configure", "connection_id": "mail"}
		filters := &gmailinbound.Filters{SenderAllowlist: []string{"@gmail.com"}}
		if rules {
			list := workflowEmailRules()
			list[0].Filters = filters
			args["rules"] = list
		} else {
			args["filters"], args["group_names"], args["route_selections"] = filters, []string{"prod"}, map[string]string{}
		}
		if _, err := api.gmailTriggerToolRequest(ctx, "human", "Workflow/mail", args); err == nil || !strings.Contains(err.Error(), "exact email addresses") {
			t.Fatalf("public domain persisted: %v", err)
		}
	}
}
