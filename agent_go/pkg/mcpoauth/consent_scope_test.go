package mcpoauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConsentIssuesOnlyScopesAllowedForTheHuman(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.sqlite")
	cfg := Config{
		CurrentUser: func(*http.Request) (*User, bool) {
			return &User{ID: "member", Email: "member@example.com"}, true
		},
		FilterScopes: func(_ *http.Request, scopes []string) []string {
			return []string{"files:read"}
		},
	}
	cfg.OpenStore = func() (*Store, error) { return OpenStore(path, cfg) }
	st, err := cfg.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	redirect := "https://client.example/callback"
	client, err := st.RegisterClient(context.Background(), "client", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	requestID, err := st.SaveRequest(context.Background(), AuthRequest{
		ClientID: client.ID, RedirectURI: redirect, Resource: "https://gateway.example/mcp",
		State: "state", Scopes: []string{"files:read", "crews:review"},
		Challenge: base64.RawURLEncoding.EncodeToString(hash[:]), ExpiresUnix: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	srv := NewServer(cfg)
	pathWithRequest := "/consent?request=" + url.QueryEscape(requestID)
	get := httptest.NewRecorder()
	srv.HandleConsent(get, httptest.NewRequest(http.MethodGet, pathWithRequest, nil))
	var shown struct {
		Scopes []string `json:"scopes"`
	}
	if get.Code != http.StatusOK || json.Unmarshal(get.Body.Bytes(), &shown) != nil || len(shown.Scopes) != 1 || shown.Scopes[0] != "files:read" {
		t.Fatalf("consent displayed unauthorized scope: %d %s", get.Code, get.Body.String())
	}

	post := httptest.NewRecorder()
	srv.HandleConsent(post, httptest.NewRequest(http.MethodPost, pathWithRequest, strings.NewReader(`{"decision":"approve"}`)))
	var approval struct {
		RedirectURL string `json:"redirect_url"`
	}
	if post.Code != http.StatusOK || json.Unmarshal(post.Body.Bytes(), &approval) != nil {
		t.Fatalf("consent failed: %d %s", post.Code, post.Body.String())
	}
	callback, err := url.Parse(approval.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	st, err = cfg.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	grant, _, _, err := st.ExchangeCode(context.Background(), callback.Query().Get("code"), client.ID, redirect, "https://gateway.example/mcp", verifier)
	if err != nil || len(grant.Scopes) != 1 || grant.Scopes[0] != "files:read" {
		t.Fatalf("issued grant contains unauthorized scope: %+v, %v", grant.Scopes, err)
	}
}

func TestChallengeUsesResourceMetadataParameter(t *testing.T) {
	srv := NewServer(Config{
		PublicURL:             "https://platform.example",
		ResourcePath:          "/api/vault/mcp",
		ProtectedResourcePath: "/.well-known/oauth-protected-resource/api/vault/mcp",
		Scopes:                []string{"vault:mcp"},
	})
	rec := httptest.NewRecorder()
	srv.Challenge(rec)
	got := rec.Header().Get("WWW-Authenticate")
	want := `Bearer resource_metadata="https://platform.example/.well-known/oauth-protected-resource/api/vault/mcp", scope="vault:mcp"`
	if got != want {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
	}
}

func TestRefreshKeepsRefreshTokenStableAcrossRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.sqlite")
	active := true
	cfg := Config{
		PublicURL:     "https://gateway.example",
		ResourcePath:  "/mcp",
		Scopes:        []string{"vault:mcp"},
		AccessPrefix:  "aw_vault_",
		RefreshPrefix: "aw_vault_refresh_",
		GrantActive:   func(context.Context, Grant) bool { return active },
	}
	cfg.OpenStore = func() (*Store, error) { return OpenStore(path, cfg) }

	st, err := cfg.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	redirect := "https://client.example/callback"
	client, err := st.RegisterClient(context.Background(), "client", []string{redirect})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("b", 43)
	hash := sha256.Sum256([]byte(verifier))
	reqID, err := st.SaveRequest(context.Background(), AuthRequest{
		ClientID: client.ID, RedirectURI: redirect, Resource: "https://gateway.example/mcp",
		State: "s", Scopes: []string{"vault:mcp"},
		Challenge: base64.RawURLEncoding.EncodeToString(hash[:]), ExpiresUnix: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, code, err := st.Decide(context.Background(), reqID, User{ID: "u1", Email: "u1@example.com"}, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, _, refresh1, err := st.ExchangeCode(context.Background(), code, client.ID, redirect, "https://gateway.example/mcp", verifier)
	if err != nil {
		t.Fatal(err)
	}

	// Repeated refreshes with the same refresh token must stay valid and keep the refresh token stable.
	_, access2, refresh2, err := st.Refresh(context.Background(), refresh1, client.ID, "https://gateway.example/mcp")
	if err != nil || refresh2 != refresh1 || access2 == "" {
		t.Fatalf("first refresh failed: access=%q refreshMatch=%v err=%v", access2, refresh2 == refresh1, err)
	}
	_, access3, refresh3, err := st.Refresh(context.Background(), refresh1, client.ID, "https://gateway.example/mcp")
	if err != nil || refresh3 != refresh1 || access3 == "" {
		t.Fatalf("second refresh failed: access=%q refreshMatch=%v err=%v", access3, refresh3 == refresh1, err)
	}
	if _, err := st.Authenticate(context.Background(), access3); err != nil {
		t.Fatalf("refreshed access token failed authenticate: %v", err)
	}

	// Inactive grant must be refused on refresh.
	active = false
	if _, _, _, err := st.Refresh(context.Background(), refresh1, client.ID, "https://gateway.example/mcp"); err == nil {
		t.Fatal("expected refresh refusal when GrantActive returns false")
	}

	// Revoking the family still blocks refresh even if GrantActive is true again.
	active = true
	if err := st.RevokeFamily(context.Background(), grant.FamilyID, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Refresh(context.Background(), refresh1, client.ID, "https://gateway.example/mcp"); err == nil {
		t.Fatal("expected refresh refusal after family revocation")
	}
}

func TestAuthenticateCLIEnforcesGrantActive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli-oauth.sqlite")
	active := true
	cfg := Config{
		PublicURL:        "https://platform.example",
		ResourcePath:     "/mcp",
		Scopes:           []string{"vault:mcp"},
		AccessPrefix:     "aw_vault_",
		RefreshPrefix:    "aw_vault_refresh_",
		CLIClientID:      "agentworks-cli",
		CLIClientName:    "AgentWorks CLI",
		CLIAccessPrefix:  "aw_cli_",
		CLIRefreshPrefix: "aw_cli_refresh_",
		CLIResourcePath:  "/cli",
		CLIDefaultScopes: []string{"vault:mcp"},
		GrantActive:      func(context.Context, Grant) bool { return active },
	}
	cfg.OpenStore = func() (*Store, error) { return OpenStore(path, cfg) }

	st, err := cfg.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	deviceCode, verifyCode, err := st.CreateCLIDevice(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DecideCLIDevice(context.Background(), verifyCode, User{ID: "u1", Email: "u1@example.com"}, true); err != nil {
		t.Fatal(err)
	}
	_, access, _, err := st.PollCLIDevice(context.Background(), deviceCode, "https://platform.example/cli")
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(cfg)
	req := httptest.NewRequest(http.MethodGet, "https://platform.example/cli", nil)
	if _, err := srv.AuthenticateCLI(context.Background(), req, access); err != nil {
		t.Fatalf("expected active CLI grant to authenticate, got %v", err)
	}
	active = false
	if _, err := srv.AuthenticateCLI(context.Background(), req, access); err == nil {
		t.Fatal("expected inactive CLI grant to be rejected")
	}
}
