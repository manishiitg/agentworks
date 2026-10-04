package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVaultDirectoryToolUsesLiveActivePlatformEmails(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"admin","username":"operator","email":"admin@example.com","role":"admin","password_hash":"never-return","sso":{"external_id":"private-id"}},{"id":"alice","username":"handle","email":"alice@example.com","role":"viewer"},{"id":"disabled","email":"disabled@example.com","disabled":true}]}`)
	t.Setenv("CAPLAYER_SERVICE_URL", "") // Directory lookup needs no gateway.
	read := func() string {
		t.Helper()
		result, err := capLayerAgentAccess(context.Background(), "admin", "list_users", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, "disabled") || strings.Contains(result, "never-return") || strings.Contains(result, "private-id") || strings.Contains(result, "role") {
			t.Fatal("directory exposed inactive accounts or private metadata", result)
		}
		return result
	}
	result := read()
	if !strings.Contains(result, `"email":"alice@example.com"`) || !strings.Contains(result, `"id":"alice"`) || !strings.Contains(result, `"username":"handle"`) {
		t.Fatal("email was not bound to its stable ID", result)
	}
	*directory = `{"users":[{"id":"admin","email":"admin@example.com","role":"admin"},{"id":"alice","email":"new@example.com"}]}`
	invalidateUserDirectoryCache()
	if result = read(); !strings.Contains(result, "new@example.com") || strings.Contains(result, "alice@example.com") {
		t.Fatal("directory returned a stale email", result)
	}
	for _, args := range []string{`{"email":"someone@example.com"}`, `null`, `[]`, `{} {}`} {
		if _, err := capLayerAgentAccess(context.Background(), "admin", "list_users", json.RawMessage(args)); err == nil {
			t.Fatal("accepted unsupported directory arguments", args)
		}
	}
	for _, id := range []string{"", "alice", "disabled", "unknown"} {
		if _, err := capLayerAgentAccess(context.Background(), id, "list_users", json.RawMessage(`{}`)); err == nil {
			t.Fatal("non-administrator read the directory", id)
		}
	}
	*directory = `{"users":[{"id":"admin","role":"admin","disabled":true}]}`
	invalidateUserDirectoryCache()
	if _, err := capLayerAgentAccess(context.Background(), "admin", "list_users", json.RawMessage(`{}`)); err == nil {
		t.Fatal("disabled administrator retained directory access")
	}
}

func TestVaultEnvironmentUsesCentralDirectoryInsteadOfGatewayEmails(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"admin","role":"admin","email":"admin@example.com"},{"id":"alice","email":"alice@example.com"}]}`)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/admin/setup/tool" || r.Header.Get("X-CapLayer-Actor") != "admin" {
			t.Error("incorrect setup request")
		}
		w.Write([]byte(`{"tools":[],"groups":[{"ID":"g"}],"users":[{"id":"alice","email":"stale@example.com"}]}`))
	}))
	defer gateway.Close()
	t.Setenv("CAPLAYER_SERVICE_URL", gateway.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	result, err := capLayerAgentAccess(context.Background(), "admin", "inspect_environment", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(result, "alice@example.com") || strings.Contains(result, "stale@example.com") || !strings.Contains(result, `"ID":"g"`) {
		t.Fatalf("wrong environment: %s %v", result, err)
	}
}

func TestVaultDirectoryDoesNotInventLocalEmailAndReportsReadFailure(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	withMemoryUserDirectory(t, "")
	users, err := vaultDirectoryUsers(&UserClaims{UserID: "default", Username: "Local account"})
	if err != nil || len(users) != 1 || users[0].Email != "" || users[0].Username != "Local account" {
		t.Fatal(users, err)
	}
	userDirectoryRead = func() (string, bool, error) { return "", false, errors.New("unavailable") }
	if _, err = vaultDirectoryUsers(&UserClaims{UserID: "default"}); err == nil {
		t.Fatal("directory failure became an empty/local account list")
	}
}
