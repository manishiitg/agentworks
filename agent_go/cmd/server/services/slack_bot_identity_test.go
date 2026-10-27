package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSlackBotIdentityUsesSlackNamesAndCredentialBoundCache(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	calls := 0
	http.DefaultClient = &http.Client{Transport: slackDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "slack.com" || r.URL.Path != "/api/auth.test" {
			t.Fatal("identity lookup called a message or unrelated API")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"user":"workspace-helper","team":"Example workspace","user_id":"U123","team_id":"T123"}`))}, nil
	})}
	service := &SlackService{}
	conn := SlackConnection{ID: "one", DisplayName: "Default", BotToken: "xoxb-synthetic-private", Enabled: true}
	for i := 0; i < 2; i++ {
		result := service.BotIdentity(context.Background(), conn)
		if !result.Verified || result.Name != "workspace-helper" || result.Workspace != "Example workspace" {
			t.Fatalf("wrong safe identity: %+v", result)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "xoxb") || strings.Contains(string(raw), "token") {
			t.Fatal("identity exposed credentials")
		}
	}
	if calls != 1 {
		t.Fatal("list refetched the same saved identity")
	}
	conn.BotToken = "xoxb-rotated-synthetic"
	service.BotIdentity(context.Background(), conn)
	if calls != 2 {
		t.Fatal("token rotation reused a different credential's identity")
	}
	conn.Enabled = false
	if result := service.BotIdentity(context.Background(), conn); result.Verified || calls != 2 {
		t.Fatal("disabled connection reused cached verification")
	}
	conn.BotToken = "xoxb-not-running"
	if result := service.BotIdentity(context.Background(), conn); result.Verified || calls != 2 {
		t.Fatal("paused unknown connection was verified")
	}
}
