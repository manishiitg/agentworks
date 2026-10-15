package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

// Website, native MCP/Builder operation and real persisted runtime resolution
// share one account selection. No Slack message or OAuth consent is performed.
func TestPersonalSlackConnectionsAcrossSurfaces(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "personal-slack-test-secret-with-enough-entropy")
	withMCPConnectionsRoot(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"builder","username":"builder","can_create":true},{"id":"editor","username":"editor","can_create":true},{"id":"runner","username":"runner"},{"id":"boss","username":"boss","admin":true}]}`)
	wf := WorkflowManifest{ID: "orders", Label: "Orders", Access: &WorkflowAccess{Owners: []string{"builder"}, Editors: []string{"editor"}, Readers: []string{"runner"}}}
	raw, _ := json.Marshal(wf)
	flow, _ := json.Marshal(WorkflowManifest{ID: "flow", Kind: "relay", Label: "Flow", Access: &WorkflowAccess{Owners: []string{"builder"}}})
	workspace := &mockWorkspaceAPI{files: map[string]string{"Workflow/orders/workflow.json": string(raw), "Workflow/flow/workflow.json": string(flow)}}
	ws := httptest.NewServer(workspace)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	api := &StreamingAPI{mcpConfigPath: filepath.Join("..", "..", "configs", "mcp_servers_clean.json"), logger: loggerv2.NewNoop()}
	call := func(user, path, action, owner, id string, want int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"workspace_path": path, "action": action, "connection_id": id, "connection_owner": owner})
		rec := httptest.NewRecorder()
		api.handlePersonalSlackAccounts(rec, googleAppRequest(http.MethodPost, "/api/integrations/slack/accounts", string(body), user))
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s", user, action, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	created := call("editor", "Workflow/orders", "connect", "", "", 200)
	id, _ := created["connection_id"].(string)
	if id == "" || created["status"] != "needs_client_id" {
		t.Fatalf("missing app setup next step: %+v", created)
	}
	internal, cfg, err := placeMCPServerConfig("editor", id)
	if err != nil || cfg.OAuth == nil {
		t.Fatal("saved connection unavailable", err)
	}
	if err := oauth.NewTokenStore(cfg.OAuth.TokenFile).Save(&oauth2.Token{AccessToken: "fixture-slack-access", TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(cfg.OAuth.TokenFile)
	if err != nil || strings.Contains(string(sealed), "fixture-slack-access") {
		t.Fatal("login was not sealed")
	}
	// Neither another Builder nor an administrator attaches somebody else's account.
	call("builder", "Workflow/orders", "attach", "editor", id, 403)
	call("boss", "Workflow/orders", "attach", "editor", id, 403)
	call("boss", "Workflow/orders", "reconnect", "editor", id, 403)
	call("runner", "Workflow/orders", "attach", "editor", id, 403)
	call("editor", "Workflow/flow", "attach", "editor", id, 403)
	call("editor", "Chats/Code/projects/private", "attach", "editor", id, 200)
	call("editor", "Chats/Work/projects/crew", "attach", "editor", id, 200)
	root := personalConnectionItemRoot("editor", "Chats/Work/projects/crew")
	reg := crewCapabilityRegistrar{}
	if err := api.registerPersonalSlackAccountTool(reg, "Workflow/orders"); err != nil {
		t.Fatal(err)
	}
	ctx := googleAppRequest("POST", "/", "", "editor").Context()
	listed, err := reg["manage_slack_account"].execute(ctx, map[string]any{"action": "list_saved"})
	if err != nil || !strings.Contains(listed, `"selected":true`) || strings.Contains(listed, "fixture-slack-access") {
		t.Fatalf("native saved account list: %s %v", listed, err)
	}
	for name, claims := range map[string]*UserClaims{
		"bot":              {UserID: "editor", Username: "editor", Provider: "bot_route"},
		"unselected token": {UserID: "editor", Username: "editor", AccessToken: &accesstokens.Token{Scopes: []string{"builder:chat"}, WorkflowIDs: []string{"another"}}},
	} {
		ctx := context.WithValue(context.Background(), UserContextKey, claims)
		if _, err := reg["manage_slack_account"].execute(ctx, map[string]any{"action": "attach", "connection_id": id}); err == nil {
			t.Fatalf("%s attached account", name)
		}
	}
	common.SetSessionWorkflowPath("slack-selected-run", "Workflow/orders")
	common.SetSessionWorkflowPath("slack-unselected-run", "Workflow/flow")
	common.SetSessionWorkflowPath("slack-selected-crew", root)
	t.Cleanup(func() {
		for _, id := range []string{"slack-selected-run", "slack-unselected-run", "slack-selected-crew"} {
			common.ClearSessionShellConfig(id)
		}
	})
	runCtx := googleAppRequest("POST", "/", "", "runner").Context()
	resolved, handled, err := api.resolvePlaceAttachedMCP(runCtx, "slack-selected-run", internal)
	if !handled || err != nil || resolved == nil || resolved.Config.OAuth.TokenFile != cfg.OAuth.TokenFile {
		t.Fatalf("shared run did not use selected account: handled=%t err=%v", handled, err)
	}
	if _, handled, _ := api.resolvePlaceAttachedMCP(runCtx, "slack-unselected-run", internal); handled {
		t.Fatal("another item's run used the saved account")
	}
	// The same sealed login remains selected after the connector loses Builder.
	withMemoryUserDirectory(t, `{"users":[{"id":"builder","username":"builder","can_create":true},{"id":"editor","username":"editor"},{"id":"runner","username":"runner"},{"id":"boss","username":"boss","admin":true}]}`)
	if resolved, handled, err := api.resolvePlaceAttachedMCP(context.Background(), "slack-selected-crew", internal); !handled || err != nil || resolved.Config.OAuth.TokenFile != cfg.OAuth.TokenFile {
		t.Fatalf("lost Builder auto-detached account: handled=%t err=%v", handled, err)
	}
	call("editor", "Workflow/orders", "detach", "editor", id, 200)
	if _, handled, _ := api.resolvePlaceAttachedMCP(runCtx, "slack-selected-run", internal); handled {
		t.Fatal("detached account still resolved")
	}
	if !slackSelected("editor", id, root) {
		t.Fatal("detaching one item removed another")
	}
	call("builder", root, "disconnect", "editor", id, 403)
	call("boss", root, "disconnect", "editor", id, 200)
	if _, _, err := placeMCPServerConfig("editor", id); err == nil {
		t.Fatal("disconnected saved account still resolved")
	}
	if _, err := os.Stat(cfg.OAuth.TokenFile); !os.IsNotExist(err) {
		t.Fatal("disconnected token retained")
	}
	if slackSelected("editor", id, root) {
		t.Fatal("disconnect left selection")
	}
}

// Drive the actual callback and exchange: a rejected reconnect must neither
// replace the live sealed token nor claim success in the browser.
func TestPersonalSlackOAuthPreservesSelectedIdentity(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "slack-identity-test-secret-with-enough-entropy")
	server := placeMCPServer{Name: "slack_work", Label: "Work Slack", Catalog: "Slack", URL: "https://mcp.slack.com/mcp", Transport: "http", OAuth: &oauth.OAuthConfig{AuthURL: "https://slack.com/oauth/v2_user/authorize", TokenURL: "https://slack.com/api/oauth.v2.user.access"}}
	if _, err := addPlaceMCPServer("builder", server); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := placeMCPServerConfig("builder", server.Name)
	if err != nil {
		t.Fatal(err)
	}
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := "U111"
		if r.Form.Get("code") == "different" {
			user = "U222"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-" + r.Form.Get("code"), "token_type": "Bearer", "authed_user": map[string]any{"id": user}, "team": map[string]any{"id": "T111"}})
	}))
	defer exchange.Close()
	api := &StreamingAPI{logger: loggerv2.NewNoop()}
	login := func(code string, want int) {
		t.Helper()
		config := cfg
		copied := *cfg.OAuth
		config.OAuth = &copied
		copied.ClientID, copied.ClientSecret = "fixture-client", "fixture-secret"
		copied.TokenURL, copied.PublicOnly = exchange.URL, false
		done := make(chan struct{}, 1)
		started, discovery, err := api.runOAuthFlow("", "https://app.example.com/api/oauth/callback", oauthFlowTarget{Name: "slack-test", Config: config, ValidateToken: func(ctx context.Context, token *oauth2.Token) error {
			return validatePersonalSlackToken(ctx, "builder", server.Name, token)
		}, Notify: func(bool, string) { done <- struct{}{} }})
		if err != nil || discovery != nil || started == nil {
			t.Fatalf("start: %v", err)
		}
		rec := httptest.NewRecorder()
		api.handleOAuthCallback(rec, httptest.NewRequest(http.MethodGet, "/api/oauth/callback?state="+started.State+"&code="+code, nil))
		if rec.Code != want {
			t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
		}
		if want == 400 && !strings.Contains(rec.Body.String(), "different Slack account") {
			t.Fatal("identity mismatch not explained")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("flow did not finish")
		}
	}
	login("first", 200)
	bound, err := personalSlackServer("builder", server.Name)
	if err != nil || bound.SlackUserID != "U111" || bound.SlackTeamID != "T111" {
		t.Fatal("identity not recorded", err)
	}
	login("different", 400)
	stored, err := oauth.NewTokenStore(cfg.OAuth.TokenFile).Load()
	if err != nil || stored.AccessToken != "fixture-first" {
		t.Fatal("rejected reconnect replaced live token", err)
	}
	login("same", 200)
	stored, err = oauth.NewTokenStore(cfg.OAuth.TokenFile).Load()
	if err != nil || stored.AccessToken != "fixture-same" {
		t.Fatal("same account reconnect failed", err)
	}
	files, _ := filepath.Glob(cfg.OAuth.TokenFile + ".pending-*")
	if len(files) != 0 {
		t.Fatal("pending token retained")
	}
}
