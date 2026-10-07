package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackTriggerMatchesTrustedSourcesOnly(t *testing.T) {
	trigger := &SlackTrigger{Type: "trusted_app", AppID: "ASENTRY", BotID: "BSENTRY", Contains: "incident"}
	base := slackevents.MessageEvent{User: "USENTRY", BotID: "BSENTRY", Channel: "C123", Text: "new incident", TimeStamp: "1.2", SubType: "bot_message", Message: &slack.Msg{BotProfile: &slack.BotProfile{AppID: "ASENTRY"}}}
	for _, tc := range []struct {
		name string
		edit func(*slackevents.MessageEvent)
		want bool
	}{
		{name: "sentry", want: true},
		{name: "unknown bot", edit: func(e *slackevents.MessageEvent) { e.BotID = "BOTHER" }},
		{name: "missing app", edit: func(e *slackevents.MessageEvent) { e.Message = nil }},
		{name: "own message", edit: func(e *slackevents.MessageEvent) { e.User = "UOWN" }},
		{name: "thread reply", edit: func(e *slackevents.MessageEvent) { e.ThreadTimeStamp = "0.1" }},
		{name: "edit", edit: func(e *slackevents.MessageEvent) { e.SubType = "message_changed" }},
		{name: "no match", edit: func(e *slackevents.MessageEvent) { e.Text = "hello" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := base
			if tc.edit != nil {
				tc.edit(&event)
			}
			if got := SlackTriggerMatches(trigger, &event, "UOWN"); got != tc.want {
				t.Fatalf("matched=%v", got)
			}
		})
	}
	human := &SlackTrigger{Type: "human_message"}
	base.BotID = ""
	base.SubType = ""
	if !SlackTriggerMatches(human, &base, "UOWN") {
		t.Fatal("human listener did not match")
	}
	base.BotID = "BEXTERNAL"
	if SlackTriggerMatches(human, &base, "UOWN") {
		t.Fatal("human listener accepted bot")
	}
}
func TestSlackRouteDecoderRestrictsLegacyOwnerToRun(t *testing.T) {
	route := ResolveChannelRoute(`{"C123":{"profile_id":"work","conversation_key":"acme","workspace_path":"Chats/Work/projects/acme","workspace_user_id":"alice","bot_grant":"owner"}}`, "C123")
	if route == nil || route.WorkflowID != "" || route.ProfileID != "work" || route.BotGrant != "run" || route.WorkspaceUserID != "alice" {
		t.Fatalf("route=%+v", route)
	}
	if ResolveChannelRoute(`{"C123":{"workflow_id":"w","profile_id":"work","conversation_key":"acme","workspace_path":"Workflow/w"}}`, "C123") != nil {
		t.Fatal("accepted mixed destination")
	}
}

func TestSlackHumanTriggerDeduplicatesMessageAndMentionInEitherOrder(t *testing.T) {
	routes := `{"C123":{"workflow_id":"demo","workspace_path":"Workflow/demo","bot_grant":"run","trigger":{"type":"human_message"}}}`
	raw, _ := json.Marshal(map[string]interface{}{"slack": map[string]interface{}{"allowed_channels": routes}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/config/bot-connectors.json" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": map[string]interface{}{"content": string(raw)}})
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	for _, mentionFirst := range []bool{true, false} {
		count := 0
		service := &SlackService{botUserID: "UOWN"}
		service.SetTriggerHandler(func(_ context.Context, channel string, event *slackevents.MessageEvent) error {
			count++
			if channel != "C123" || event.TimeStamp != "1.2" {
				t.Fatal("wrong trigger")
			}
			return nil
		})
		mention := func() {
			service.handleAppMentionEvent(&slackevents.AppMentionEvent{Channel: "C123", User: "UHUMAN", Text: "<@UOWN> incident", TimeStamp: "1.2"}, nil)
		}
		message := func() {
			service.handleSocketModeMessage(&slackevents.MessageEvent{Channel: "C123", User: "UHUMAN", Text: "<@UOWN> incident", TimeStamp: "1.2"})
		}
		if mentionFirst {
			mention()
			message()
		} else {
			message()
			mention()
		}
		if count != 1 {
			t.Fatalf("mentionFirst=%v triggered %d times", mentionFirst, count)
		}
	}
}

func TestSlackCredentialCodecAndShortTokenMasking(t *testing.T) {
	encrypt, decrypt := slackCredentialEncrypt, slackCredentialDecrypt
	defer ConfigureSlackCredentialCodec(encrypt, decrypt)
	ConfigureSlackCredentialCodec(func(s string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(s)), nil }, func(s string) (string, error) { b, e := base64.StdEncoding.DecodeString(s); return string(b), e })
	encoded, err := encodeSlackCredential("secret-token")
	if err != nil || !strings.HasPrefix(encoded, "encrypted:v1:") || strings.Contains(encoded, "secret-token") {
		t.Fatalf("storage: %q %v", encoded, err)
	}
	decoded, err := decodeSlackCredential(encoded)
	if err != nil || decoded != "secret-token" {
		t.Fatalf("decode: %q %v", decoded, err)
	}
	service := &SlackService{config: &SlackConfig{BotToken: "abc", AppToken: "xyz"}}
	masked := service.GetConfig()
	if strings.Contains(masked.BotToken, "abc") || strings.Contains(masked.AppToken, "xyz") {
		t.Fatal("short credential leaked")
	}
	ConfigureSlackCredentialCodec(nil, nil)
	if _, err := encodeSlackCredential("secret-token"); err == nil {
		t.Fatal("persisted plaintext without codec")
	}
}

func TestSlackOwnBotMessageIgnoredWithoutUserField(t *testing.T) {
	service := &SlackService{botID: "BOWN"}
	if !service.handleConfiguredSlackTrigger(&slackevents.MessageEvent{BotID: "BOWN", Channel: "C123", TimeStamp: "1.2"}) {
		t.Fatal("own bot message entered routing")
	}
}

func TestSlackEmailExclusions(t *testing.T) {
	route := ChannelRoute{BlockedEmails: []string{"person@example.com"}}
	for _, tc := range []struct {
		email   string
		allowed bool
	}{{"PERSON@example.com", false}, {"", false}, {"other@example.com", true}} {
		if SlackRouteAllowsEmail(route, tc.email) != tc.allowed {
			t.Fatalf("email %q", tc.email)
		}
	}
	if !SlackRouteAllowsEmail(ChannelRoute{}, "") {
		t.Fatal("open route requires email")
	}
}
