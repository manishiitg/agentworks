package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/oauth"
	"golang.org/x/oauth2"
)

// TestSaveConfigConcurrentSavesLeaveAValidFile: concurrent saves once shared
// one ".tmp" path, so one save could rename another's half-written file into
// place and leave a broken config.
func TestSaveConfigConcurrentSavesLeaveAValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp_servers.json")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := &MCPConfig{MCPServers: map[string]MCPServerConfig{}}
			for j := 0; j <= i*20; j++ {
				cfg.MCPServers[fmt.Sprintf("server-%d", j)] = MCPServerConfig{URL: "https://example.com/mcp"}
			}
			if err := SaveConfig(path, cfg); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got MCPConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config left unreadable after concurrent saves: %v", err)
	}
	if leftovers, _ := filepath.Glob(path + ".*.tmp"); len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestBuildStdioEnvScrubsInternalSecretsAndPreservesNormalEnv(t *testing.T) {
	hostEnv := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/example",
		"AWS_PROFILE=dev",
		"SECRET_GITHUB_TOKEN=ghp_should_not_leak",
		"MCP_API_TOKEN=bridge_secret",
		"AGENTWORKS_AUTH_SECRET=internal_secret",
		"JWT_SECRET=jwt_secret",
		"CAPLAYER_SERVICE_TOKEN=vault_secret",
	}
	overrides := map[string]string{
		"CUSTOM_KEY": "explicit-value",
		"SECRET_APP": "explicit-override-allowed",
	}
	got := buildStdioEnv(hostEnv, overrides)
	for _, stripped := range []string{"SECRET_GITHUB_TOKEN", "MCP_API_TOKEN", "AGENTWORKS_AUTH_SECRET", "JWT_SECRET", "CAPLAYER_SERVICE_TOKEN"} {
		if _, exists := got[stripped]; exists {
			t.Fatalf("expected internal env var %s to be scrubbed", stripped)
		}
	}
	if got["PATH"] != "/usr/bin:/bin" || got["HOME"] != "/home/example" || got["AWS_PROFILE"] != "dev" {
		t.Fatalf("normal host env vars were lost: %+v", got)
	}
	if got["CUSTOM_KEY"] != "explicit-value" || got["SECRET_APP"] != "explicit-override-allowed" {
		t.Fatalf("explicit overrides were not applied: %+v", got)
	}
}

func TestWithOAuthBearerDynamicallyRefreshesTokens(t *testing.T) {
	var refreshCount atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := refreshCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","token_type":"Bearer","expires_in":3600,"refresh_token":"rt-%d"}`, n, n)
	}))
	defer tokenSrv.Close()

	var gotAuth []string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer apiSrv.Close()

	tokenFile := filepath.Join(t.TempDir(), "token.json")
	store := oauth.NewTokenStore(tokenFile)
	if err := store.Save(&oauth2.Token{
		AccessToken:  "initial-valid",
		RefreshToken: "rt-0",
		Expiry:       time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	mgr := oauth.NewManager(&oauth.OAuthConfig{
		ClientID:  "cid",
		AuthURL:   tokenSrv.URL + "/auth",
		TokenURL:  tokenSrv.URL + "/token",
		TokenFile: tokenFile,
	}, nil)

	httpClient := withOAuthBearer(nil, mgr)

	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, apiSrv.URL, nil)
	resp1, err := httpClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp1.Body.Close()

	// Expire the stored token and confirm the next request pulls the refreshed Bearer header.
	if err := store.Save(&oauth2.Token{
		AccessToken:  "now-expired",
		RefreshToken: "rt-0",
		Expiry:       time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, apiSrv.URL, nil)
	resp2, err := httpClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()

	if len(gotAuth) != 2 || gotAuth[0] != "Bearer initial-valid" || gotAuth[1] != "Bearer tok-1" {
		t.Fatalf("Authorization headers = %v, want [Bearer initial-valid, Bearer tok-1]", gotAuth)
	}
}
