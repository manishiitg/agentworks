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
		State: "state", Scopes: []string{"files:read", "code:review"},
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
