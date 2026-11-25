package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

func TestVaultSlackIdentityFetchCacheAndCleanup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")

	connID := "c-1234abcd"
	tokenFile := getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(connID))
	if err := oauth.NewTokenStore(tokenFile).Save(&oauth2.Token{
		AccessToken: "test-slack-access-value",
		Expiry:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	var authTestCalls atomic.Int32
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authTestCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-slack-access-value" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"team":    "Acme Workspace",
			"team_id": "T012345",
			"url":     "https://acme-team.slack.com/",
			"user":    "alex",
			"user_id": "U012345",
		})
	}))
	defer slackSrv.Close()

	origURL := slackAuthTestURL
	origClient := slackIdentityHTTPClient
	slackAuthTestURL = slackSrv.URL
	slackIdentityHTTPClient = func() *http.Client { return slackSrv.Client() }
	t.Cleanup(func() {
		slackAuthTestURL = origURL
		slackIdentityHTTPClient = origClient
		vaultSlackIdentityMiss.Delete(connID)
	})

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vaults" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"vaults": []any{
			map[string]any{"group": map[string]any{"ID": "v-own", "Name": personalVaultName}, "role": "owner", "connections": []any{
				map[string]any{"id": connID, "label": "My Slack account", "provider": "slack", "status": "active"},
			}},
		}})
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)

	api := &StreamingAPI{mcpConfigPath: filepath.Join("..", "..", "configs", "mcp_servers_clean.json"), logger: loggerv2.NewNoop()}
	r := httptest.NewRequest(http.MethodGet, "/api/integrations/slack/accounts", nil).WithContext(personContext("alice"))

	// First call fetches auth.test once and caches it on disk.
	out, err := api.personalSlackAccounts(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Accounts) != 1 {
		t.Fatalf("accounts = %+v", out.Accounts)
	}
	got := out.Accounts[0]
	if got.TeamName != "Acme Workspace" || got.TeamDomain != "acme-team.slack.com" || got.UserName != "alex" {
		t.Fatalf("identity = %+v", got)
	}
	if authTestCalls.Load() != 1 {
		t.Fatalf("auth.test calls = %d, want 1", authTestCalls.Load())
	}

	// Second call reads from disk cache without hitting Slack again.
	out2, err := api.personalSlackAccounts(r, "")
	if err != nil {
		t.Fatal(err)
	}
	if out2.Accounts[0].TeamName != "Acme Workspace" || authTestCalls.Load() != 1 {
		t.Fatalf("expected cached identity with 1 call, got calls=%d account=%+v", authTestCalls.Load(), out2.Accounts[0])
	}

	// Local-only helper used by Connected here prompt sees cached identity with no network calls.
	if formatted := formatSlackIdentity(readVaultSlackIdentity(connID)); formatted != "Acme Workspace (@alex)" {
		t.Fatalf("formatSlackIdentity = %q, want %q", formatted, "Acme Workspace (@alex)")
	}

	// Cleanup removes cached identity on disconnect.
	removeVaultCredentialFiles(connID)
	if !readVaultSlackIdentity(connID).empty() {
		t.Fatalf("expected identity cache to be cleared after removeVaultCredentialFiles")
	}

	// allowFetch=false never contacts Slack.
	if !api.vaultSlackIdentityCached(context.Background(), connID, false).empty() || authTestCalls.Load() != 1 {
		t.Fatalf("allowFetch=false should not fetch from Slack")
	}
}
