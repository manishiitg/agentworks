package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedOAuthBrokerAndPerRequestCredentials(t *testing.T) {
	token := "first"
	secret := strings.Repeat("s", 32)
	calls := 0
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args map[string]string
		json.NewDecoder(r.Body).Decode(&args)
		if r.URL.Path != "/internal/caplayer/oauth-token" || r.Header.Get("Authorization") != "Bearer "+secret || args["server_name"] != "Test" || args["url"] != "https://example.com/mcp" {
			t.Error("wrong broker binding")
		}
		calls++
		json.NewEncoder(w).Encode(map[string]string{"access_token": token})
	}))
	defer broker.Close()
	resolve, err := SharedOAuth(broker.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Get("Authorization")) }))
	defer target.Close()
	client := safeHTTPClient(DialOptions{AllowPrivate: true, AccessToken: func(ctx context.Context) (string, error) { return resolve(ctx, "Test", "https://example.com/mcp", "") }})
	for _, value := range []string{"first", "refreshed"} {
		token = value
		resp, err := client.Get(target.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if calls != 2 || strings.Join(seen, ",") != "Bearer first,Bearer refreshed" {
		t.Fatal("credential not refreshed per request", seen, calls)
	}
	denied := safeHTTPClient(DialOptions{AllowPrivate: true, AccessToken: func(context.Context) (string, error) { return "", errors.New("secret detail") }})
	if _, err := denied.Get(target.URL); err == nil || strings.Contains(err.Error(), "secret detail") || len(seen) != 2 {
		t.Fatal("authorization failure reached upstream or leaked details")
	}
}

func TestSharedOAuthNeverForwardsServiceSecretThroughRedirects(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer target.Close()
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer broker.Close()
	resolve, err := SharedOAuth(broker.URL, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(context.Background(), "Test", "https://example.com/mcp", ""); err == nil || calls != 0 {
		t.Fatal("redirect followed")
	}
	for _, url := range []string{"http://example.com", "https://user:secret@example.com", "https://example.com/path", "https://example.com?token=s"} {
		if _, err := SharedOAuth(url, strings.Repeat("s", 32)); err == nil {
			t.Fatal("invalid broker origin accepted", url)
		}
	}
}
