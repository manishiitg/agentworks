package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func googleAppRequest(method, target, body, userID string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	return req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: userID, Username: userID}))
}

// The status says whether the server has a Google app and where Google sends the person back:
// the callback the app already registers, so no new redirect URI is needed.
func TestGoogleAppStatusAndReturnURI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	withMCPConnectionsRoot(t)
	status := func() (bool, string) {
		req := googleAppRequest(http.MethodGet, "https://app.example.com/api/human-feedback/gmail/google-app", "", "alice")
		req.Host = "app.example.com"
		rec := httptest.NewRecorder()
		googleAppStatusHandler(nil)(rec, req)
		var out struct {
			Configured  bool   `json:"configured"`
			RedirectURI string `json:"redirect_uri"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Configured, out.RedirectURI
	}
	if configured, _ := status(); configured {
		t.Fatal("configured with no app stored")
	}
	if err := writeMCPApp("google", mcpApp{ClientID: "cid.apps.googleusercontent.com", ClientSecret: "shh"}); err != nil {
		t.Fatal(err)
	}
	configured, uri := status()
	if !configured || !strings.HasSuffix(uri, "/api/oauth/callback") {
		t.Fatalf("status = %v %q, want configured and the shared /api/oauth/callback", configured, uri)
	}
}

// Connecting needs the server's Google app, and only a Code's owner connects an account to it.
func TestGoogleAppConnectRefusals(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	t.Setenv("MULTI_USER_MODE", "true")
	withMCPConnectionsRoot(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","can_create":true},{"id":"bob","username":"bob","can_create":true}]}`)
	body := `{"workspace_path":"_users/alice/Chats/Code/projects/app-1"}`
	call := func(user string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		googleAppConnectHandler(nil)(rec, googleAppRequest(http.MethodPost, "/x", body, user))
		return rec
	}
	if rec := call("alice"); rec.Code != http.StatusConflict {
		t.Fatalf("no Google app configured: %d %s", rec.Code, rec.Body.String())
	}
	if err := writeMCPApp("google", mcpApp{ClientID: "cid.apps.googleusercontent.com", ClientSecret: "shh"}); err != nil {
		t.Fatal(err)
	}
	if rec := call("bob"); rec.Code != http.StatusForbidden {
		t.Fatalf("another person connected to alice's Code: %d %s", rec.Code, rec.Body.String())
	}
}

// A Google sign-in that returns through /api/oauth/callback (the one redirect URI the app
// registers) is handed to the Gmail completion, not rejected as an unknown MCP sign-in.
func TestOAuthCallbackHandsGmailSignInToGmail(t *testing.T) {
	t.Setenv("GMAIL_OAUTH_CLIENTS_DIR", t.TempDir())
	t.Setenv("GOG_HOME", t.TempDir())
	restore := services.SetGogClientStore(services.StoreGogClientForTest)
	t.Cleanup(restore)
	api, _ := newCodePrivacyFixture(t)
	api.logger = loggerv2.NewNoop()
	if _, err := services.EnsurePlatformOAuthClient(context.Background(), "cid.apps.googleusercontent.com", "shh"); err != nil {
		t.Fatal(err)
	}
	authURL, err := services.BeginGmailOAuth("conn-1", services.PlatformGoogleClientName, "https://app.example.com/api/oauth/callback", true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(authURL, "state=")
	state := authURL[i+len("state="):]
	if j := strings.Index(state, "&"); j >= 0 {
		state = state[:j]
	}
	rec := httptest.NewRecorder()
	api.handleOAuthCallback(rec, googleAppRequest(http.MethodGet, "/api/oauth/callback?state="+state+"&error=access_denied", "", "alice"))
	if !strings.Contains(rec.Body.String(), "Sign-in cancelled") {
		t.Fatalf("a Gmail sign-in was not handed to Gmail: %d %s", rec.Code, rec.Body.String())
	}
	// An unknown state is still an MCP callback problem, not Gmail's.
	rec = httptest.NewRecorder()
	api.handleOAuthCallback(rec, googleAppRequest(http.MethodGet, "/api/oauth/callback?state=unknown&code=x", "", "alice"))
	if strings.Contains(rec.Body.String(), "Sign-in cancelled") || rec.Code == http.StatusOK {
		t.Fatalf("an unknown state was treated as a Gmail sign-in: %d", rec.Code)
	}
}
