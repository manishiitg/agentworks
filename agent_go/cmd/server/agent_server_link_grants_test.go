package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Real PKCE, browser-role consent, durable grants and HTTP agent endpoints.
// A messaging link must never turn into a general account sign-in on refresh.
func TestAgentServerLinkGrantKeepsItsNamespaceAndMessagingScope(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AUTH_SECRET", "agent-server-link-fixture-secret")
	mux := http.NewServeMux()
	mux.HandleFunc(mcpOAuthRegisterPath, env.api.handleMCPOAuthRegister)
	mux.HandleFunc(mcpOAuthAuthorizePath, env.api.handleMCPOAuthAuthorize)
	mux.HandleFunc(mcpOAuthConsentPath, env.api.handleMCPOAuthConsent)
	mux.HandleFunc(mcpOAuthTokenPath, env.api.handleMCPOAuthToken)
	mux.HandleFunc("/api/external/v1/call", env.api.handleExternalCall)
	mux.HandleFunc("/api/external/v1/tools", env.api.handleExternalTools)
	mux.HandleFunc("/api/external/v1/agents/attachment", env.api.handleAgentAttachment)
	srv := httptest.NewServer(AuthMiddleware(mux))
	defer srv.Close()
	t.Setenv("PUBLIC_URL", srv.URL)
	httpClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path, body, bearer, contentType string) (int, []byte, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var raw json.RawMessage
		if response.StatusCode != http.StatusSeeOther {
			if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
				t.Fatal("invalid response JSON", response.StatusCode)
			}
		}
		return response.StatusCode, raw, response.Header
	}
	status, raw, _ := request("POST", mcpOAuthRegisterPath, `{"client_name":"Home server","redirect_uris":["https://home.example.com/agentbridge/servers/callback"],"agent_server_link":true}`, "", "application/json")
	var registered mcpOAuthClient
	if status != 201 || json.Unmarshal(raw, &registered) != nil || !isAgentServerLinkClient(registered.ID) {
		t.Fatal("link client registration", status)
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	resource := srv.URL + externalMCPPath
	q := url.Values{"response_type": {"code"}, "client_id": {registered.ID}, "redirect_uri": {registered.RedirectURIs[0]}, "resource": {resource}, "state": {"fixture-state"}, "scope": {"workflows:read files:read vault:manage"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	status, _, headers := request("GET", mcpOAuthAuthorizePath+"?"+q.Encode(), "", "", "")
	if status != 303 {
		t.Fatal("start consent", status)
	}
	consentURL := strings.Replace(headers.Get("Location"), "/oauth/consent", mcpOAuthConsentPath, 1)
	jwt, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	status, raw, _ = request("GET", consentURL, "", jwt, "")
	var consent struct {
		Scopes        []string `json:"scopes"`
		MessagingOnly bool     `json:"messaging_only"`
		LinkOrigin    string   `json:"link_origin"`
	}
	if status != 200 || json.Unmarshal(raw, &consent) != nil || !consent.MessagingOnly || consent.LinkOrigin != "https://home.example.com" || !slices.Equal(consent.Scopes, agentServerLinkScopes) {
		t.Fatal("consent widened beyond messaging", status, consent.Scopes)
	}
	status, raw, _ = request("POST", consentURL, `{"decision":"approve"}`, jwt, "application/json")
	var redirect struct {
		URL string `json:"redirect_url"`
	}
	if status != 200 || json.Unmarshal(raw, &redirect) != nil {
		t.Fatal("consent approval", status)
	}
	destination, _ := url.Parse(redirect.URL)
	exchange := url.Values{"grant_type": {"authorization_code"}, "client_id": {registered.ID}, "redirect_uri": {registered.RedirectURIs[0]}, "resource": {resource}, "code_verifier": {verifier}, "code": {destination.Query().Get("code")}}
	status, raw, _ = request("POST", mcpOAuthTokenPath, exchange.Encode(), "", "application/x-www-form-urlencoded")
	var pair struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	if status != 200 || json.Unmarshal(raw, &pair) != nil || pair.Access == "" || pair.Refresh == "" || pair.Scope != strings.Join(agentServerLinkScopes, " ") {
		t.Fatal("messaging token exchange", status)
	}
	parent, err := agentworksclient.New(srv.URL, pair.Access)
	if err != nil {
		t.Fatal(err)
	}
	call := func(c *agentworksclient.Client, args map[string]any, allowed bool) {
		t.Helper()
		_, err := c.Call(context.Background(), "messages", args)
		if (err == nil) != allowed {
			t.Fatalf("action %s allowed=%t error=%v", args["action"], allowed, err)
		}
	}

	mail, err := readAgentMessageStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mail.Agents = append(mail.Agents, namedMessageAgent{ID: "native-reviewer", UserID: "owner", Name: "reviewer", Connections: []string{"native-only"}, CreatedAt: time.Now().UTC()})
	mail.Conversations = append(mail.Conversations, agentConversation{ID: "native-inbox", Endpoints: [2]agentMessageEndpoint{
		{UserID: "owner", Kind: triggerCallerUser, ID: "owner", NamedAgentID: "native-reviewer", External: true, Label: "reviewer"},
		{UserID: "owner", Kind: triggerCallerUser, ID: "owner", External: true, Label: "person"},
	}, Messages: []agentMessage{{ID: "native-message", AgentSequence: 1, Sequence: 1, From: 1, Text: "private native mail", SentAt: time.Now().UTC(), Delivery: "stored"}}, Next: 2})
	if err = saveAgentMessageStore(context.Background(), mail); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "send", "ack", "count"} {
		for _, name := range []string{"reviewer", ""} {
			call(parent, map[string]any{"action": action, "agent_name": name, "inbox_id": "native-inbox", "message_id": "native-message", "message": "impersonation attempt", "submission_id": "root-native-" + action + name, "delivery": "delivered"}, false)
		}
	}
	call(parent, map[string]any{"action": "register", "agent_name": "reviewer"}, false)
	call(parent, map[string]any{"action": "register", "agent_name": "reviewer@other.example.com"}, false)
	call(parent, map[string]any{"action": "register", "agent_name": "reviewer@home.example.com"}, true)
	for _, name := range []string{"workspace", "users", "files"} {
		if _, err := parent.Call(context.Background(), name, map[string]any{"action": "list"}); err == nil {
			t.Fatal("linked server reached another product", name)
		}
	}
	call(parent, map[string]any{"action": "clear", "agent_name": "reviewer@home.example.com"}, false)
	childConfig, err := parent.CreateAgentConnection(context.Background(), "reviewer@home.example.com")
	if err != nil {
		t.Fatal("derive child", err)
	}
	child, err := agentworksclient.New(srv.URL, childConfig.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.CreateAgentConnection(context.Background(), "reviewer@home.example.com"); err == nil {
		t.Fatal("child derived another child")
	}
	call(child, map[string]any{"action": "register", "agent_name": "other@home.example.com"}, false)
	if _, err := child.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: "reviewer@home.example.com", Provider: "codex", SessionID: "home-agent"}); err == nil {
		t.Fatal("relay impersonated a native chat")
	}
	if _, err := child.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: "reviewer@home.example.com", Provider: "relay", SessionID: "home-agent"}); err != nil {
		t.Fatal("connect relay reader", err)
	}

	sent, err := child.Call(context.Background(), "messages", map[string]any{"action": "send", "agent_name": "reviewer@home.example.com", "target": "agent:reviewer", "message": "reader restart continuity", "submission_id": "relay-reader-restart"})
	var sentReceipt struct {
		ID string `json:"message_id"`
	}
	if err != nil || json.Unmarshal(sent, &sentReceipt) != nil || sentReceipt.ID == "" {
		t.Fatal("send before reader replacement", err)
	}
	replacement, err := parent.CreateAgentConnection(context.Background(), "reviewer@home.example.com")
	if err != nil {
		t.Fatal(err)
	}
	replacementClient, _ := agentworksclient.New(srv.URL, replacement.Token)
	if _, err = replacementClient.AttachAgent(context.Background(), agentworksclient.AgentAttachmentOptions{AgentName: "reviewer@home.example.com", Provider: "relay", SessionID: "home-agent"}); err != nil {
		t.Fatal(err)
	}
	mail, err = readAgentMessageStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var queued agentMessage
	var conversation agentConversation
	for _, c := range mail.Conversations {
		for _, m := range c.Messages {
			if m.ID == sentReceipt.ID {
				queued, conversation = m, c
			}
		}
	}
	if queued.ID == "" {
		t.Fatal("missing queued relay mail")
	}
	if err = env.api.recheckAgentMessageAdmission(context.Background(), conversation, queued); err != nil {
		t.Fatal("reader replacement stranded approved mail", err)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {registered.ID}, "resource": {resource}, "refresh_token": {pair.Refresh}}
	status, raw, _ = request("POST", mcpOAuthTokenPath, refresh.Encode(), "", "application/x-www-form-urlencoded")
	if status != 200 || json.Unmarshal(raw, &pair) != nil || pair.Scope != strings.Join(agentServerLinkScopes, " ") {
		t.Fatal("refresh widened link scope", status)
	}
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	grant, err := store.Authenticate(context.Background(), pair.Access)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeFamily(context.Background(), grant.FamilyID, "owner"); err != nil {
		t.Fatal(err)
	}
	call(child, map[string]any{"action": "count", "agent_name": "reviewer@home.example.com"}, false)
	if err = env.api.recheckAgentMessageAdmission(context.Background(), conversation, queued); err == nil {
		t.Fatal("revoked server approval released queued mail")
	}
}
