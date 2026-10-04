package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
)

func TestPrivateOAuthCallbacksReachInitiatingChat(t *testing.T) {
	for _, route := range []string{"oauth-start", "project-connect", "code-tool", "builder-tool"} {
		t.Run(route, func(t *testing.T) {
			api, _ := newCodePrivacyFixture(t)
			withMCPConnectionsRoot(t)
			t.Setenv("AUTH_SECRET", strings.Repeat("s", 32))
			api.logger = loggerv2.NewNoop()
			t.Setenv("PUBLIC_URL", "https://agentworks.example.com")
			api.eventStore = events.NewEventStore(20)
			defer api.eventStore.Stop()
			api.eventStore.SetSessionOwner("own-chat", "owner")
			api.eventStore.SetSessionOwner("foreign-chat", "other")
			_, err := addPlaceMCPServer("owner", placeMCPServer{Name: "notion", URL: "https://mcp.example.com/mcp", OAuth: &oauth.OAuthConfig{ClientID: "fixture-client", AuthURL: "https://example.com/authorize", TokenURL: "https://example.com/token"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := recordPrivateMCP("owner", "notion", codePrivacyOwnerRoot); err != nil {
				t.Fatal(err)
			}
			start := func(session string) *httptest.ResponseRecorder {
				body, _ := json.Marshal(OAuthLoginRequest{Scope: "private", ServerName: "notion", SessionID: session})
				r := httptest.NewRequest("POST", "/api/oauth/start", strings.NewReader(string(body))).WithContext(requestWithUserForSessionAccess("owner").Context())
				w := httptest.NewRecorder()
				if route == "oauth-start" {
					api.handleOAuthStart(w, r)
				} else if route == "code-tool" || route == "builder-tool" {
					ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, session)
					var output string
					var err error
					if route == "code-tool" {
						reg := &placeMCPToolRegistrar{}
						if err := api.registerPlaceMCPTool(reg, "owner", codePrivacyOwnerRoot, "https://agentworks.example.com/api/oauth/callback"); err != nil {
							t.Fatal(err)
						}
						output, err = reg.exec(ctx, map[string]interface{}{"action": "connect", "catalog": "notion"})
					} else {
						output, err = api.privateMCPTool(ctx, "owner", "install_mcp_server", map[string]interface{}{"name": "notion"})
					}
					if err != nil {
						t.Fatal(err)
					}
					for _, word := range strings.Fields(output) {
						if strings.HasPrefix(word, "https://example.com/authorize?") {
							json.NewEncoder(w).Encode(map[string]string{"auth_url": word})
							break
						}
					}
				} else {
					r.URL.Path = "/api/mcp/place/notion/connect"
					r.URL.RawQuery = "workspace_path=" + url.QueryEscape("Chats/Code/projects/app-c0de0001")
					api.handleConnectPlaceMCP(w, mux.SetURLVars(r, map[string]string{"name": "notion"}))
				}
				return w
			}
			if route == "oauth-start" || route == "project-connect" {
				if w := start("foreign-chat"); w.Code != http.StatusForbidden {
					t.Fatalf("foreign chat accepted: %d %s", w.Code, w.Body.String())
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				w := start("own-chat")
				var response struct {
					AuthURL string `json:"auth_url"`
				}
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
					t.Fatalf("start: %d %s", w.Code, w.Body.String())
				}
				authURL, err := url.Parse(response.AuthURL)
				if err != nil {
					t.Fatal(err)
				}
				oauthFlowsMu.RLock()
				flow := oauthFlows[authURL.Query().Get("state")]
				oauthFlowsMu.RUnlock()
				if flow == nil {
					t.Fatal("missing OAuth flow")
				}
				// The callback error channel avoids contacting any external provider.
				flow.ErrChan <- errors.New("fixture authorization denied")
				until := time.Now().Add(3 * time.Second)
				for len(api.eventStore.GetAllEventsRaw("own-chat")) <= attempt && time.Now().Before(until) {
					time.Sleep(5 * time.Millisecond)
				}
			}
			notices := api.eventStore.GetAllEventsRaw("own-chat")
			if len(notices) != 2 || notices[0].ID == notices[1].ID {
				t.Fatal("callback notices missing or reconnect deduplicated")
			}
			for _, notice := range notices {
				raw, _ := json.Marshal(notice)
				if notice.Type != "synthetic_turn_ready" || !strings.Contains(string(raw), `"status":"failed"`) || !strings.Contains(string(raw), `"name":"notion"`) {
					t.Fatalf("wrong callback notice: %s", raw)
				}
			}
			if len(api.eventStore.GetAllEventsRaw("foreign-chat")) != 0 {
				t.Fatal("notified another user")
			}
			// Success remains visible without a retained agent after a restart.
			api.notifyPrivateOAuthFlowOutcome("own-chat", "notion", "private-oauth:notion:success-fixture", true, "")
			notices = api.eventStore.GetAllEventsRaw("own-chat")
			raw, _ := json.Marshal(notices[len(notices)-1])
			if !strings.Contains(string(raw), `"status":"completed"`) {
				t.Fatal("success not recorded")
			}
		})
	}
}
