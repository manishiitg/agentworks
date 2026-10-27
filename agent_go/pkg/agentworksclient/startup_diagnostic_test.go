package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-856-222: local stderr/paths never go over the wire, and another joined
// server or saved login cannot consume the originating login's pending record.
func TestStartupDiagnosticStaysWithOriginatingLogin(t *testing.T) {
	root := t.TempDir()
	posted := 0
	var requestBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted++
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	login := filepath.Join(root, "login.json")
	if err := SaveConfig(login, Config{Server: srv.URL, Token: "private-test-token"}); err != nil {
		t.Fatal(err)
	}
	// A missing release -X value must not silently erase the failure.
	RecordStartupFailure(root, login, "", "", "timeout")
	path := startupDiagnosticPath(root, login)
	var saved savedStartupDiagnostic
	if err := readLiveJSON(path, &saved); err != nil {
		t.Fatal(err)
	}
	client, _ := New(srv.URL, "private-test-token")
	other, _ := New("https://example.com", "other-private-token")
	a := AgentAttachment{ID: "test-attachment", AgentName: "test-agent"}
	other.reportStartupFailure(context.Background(), root, login, a)
	client.reportStartupFailure(context.Background(), root, filepath.Join(root, "different-login.json"), a)
	if posted != 0 {
		t.Fatal("another login reported the diagnostic")
	}
	client.reportStartupFailure(context.Background(), root, login, a)
	client.reportStartupFailure(context.Background(), root, login, a)
	if posted != 1 {
		t.Fatalf("reported %d times", posted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("accepted record remained: %v", err)
	}
	b, _ := json.Marshal(requestBody)
	for _, private := range []string{root, "private-test-token", "config_path", "stderr", "server"} {
		if strings.Contains(string(b), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
	var got StartupDiagnostic
	b, _ = json.Marshal(requestBody["diagnostic"])
	if err := json.Unmarshal(b, &got); err != nil || !got.Valid() || got.ID != saved.Diagnostic.ID || got.Build != "unknown" || got.Version != "unknown" {
		t.Fatalf("invalid diagnostic: %+v %v", got, err)
	}
}
