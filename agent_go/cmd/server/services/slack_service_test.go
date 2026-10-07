package services

import (
	"encoding/json"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"strings"
	"testing"
)

func TestSlackUserNotificationDestinationRequiresExplicitSlackDestination(t *testing.T) {
	svc := &SlackService{}

	channelID, threadTS := svc.pickUserNotificationDestination(nil)
	if channelID != "" || threadTS != "" {
		t.Fatalf("user notification destination without Slack hint = %q/%q, want empty", channelID, threadTS)
	}

	channelID, threadTS = svc.pickUserNotificationDestination(&NotificationDestination{})
	if channelID != "" || threadTS != "" {
		t.Fatalf("user notification destination without Slack hint = %q/%q, want empty", channelID, threadTS)
	}

	channelID, threadTS = svc.pickUserNotificationDestination(&NotificationDestination{
		Slack: &SlackDest{ChannelID: "C123", ThreadTS: "171.1"},
	})
	if channelID != "C123" || threadTS != "171.1" {
		t.Fatalf("user notification destination = %q/%q, want C123/171.1", channelID, threadTS)
	}
}

func TestSlackFeedbackNotificationHasNoDefaultFallback(t *testing.T) {
	svc := &SlackService{}

	channelID, threadTS := svc.pickDestination(nil)
	if channelID != "" || threadTS != "" {
		t.Fatalf("feedback notification destination = %q/%q, want empty/empty", channelID, threadTS)
	}
}

func TestSlackConnectionDoesNotRequireDefaultChannel(t *testing.T) {
	svc := &SlackService{enabled: true, client: slack.New("test")}
	if !svc.IsEnabled() {
		t.Fatal("connector requires a legacy channel")
	}
	var cfg SlackConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"channel_id":"CLEGACY"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "channel_id") {
		t.Fatal("legacy default channel survived serialization")
	}
}

func TestSlackMessageTagsAnotherUser(t *testing.T) {
	cases := []struct {
		name string
		text string
		bot  string
		want bool
	}{
		{"plain reply", "looks good, ship it", "UBOT", false},
		{"bot tag only", "<@UBOT> run the report", "UBOT", false},
		{"other user tag", "<@U123> can you take a look?", "UBOT", true},
		{"bot plus other user", "<@UBOT> ask <@U123> to rerun", "UBOT", true},
		{"labeled tag", "ping <@U123|bob> about it", "UBOT", true},
		{"channel ping is not a person", "<!channel> heads up", "UBOT", false},
		{"here ping is not a person", "<!here> standup time", "UBOT", false},
		{"unknown bot id fails open", "<@U123> hi", "", false},
	}
	for _, tc := range cases {
		if got := slackMessageTagsAnotherUser(tc.text, tc.bot); got != tc.want {
			t.Errorf("%s: slackMessageTagsAnotherUser(%q) = %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}

func TestRewriteMentionTagsResolvesNames(t *testing.T) {
	resolve := func(userID string) string {
		if userID == "U123" {
			return "bob"
		}
		return ""
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{"no tags", "run the report", "run the report"},
		{"known user", "ask <@U123> to rerun", "ask @bob to rerun"},
		{"labeled tag", "ping <@U123|robert> now", "ping @bob now"},
		{"unknown user dropped", "ask <@U999> today", "ask  today"},
		{"channel ping untouched", "<!here> standup", "<!here> standup"},
	}
	for _, tc := range cases {
		if got := rewriteMentionTags(tc.text, resolve); got != tc.want {
			t.Errorf("%s: rewriteMentionTags(%q) = %q, want %q", tc.name, tc.text, got, tc.want)
		}
	}
}

// A top-level @mention with an attachment: slackevents.AppMentionEvent drops
// the files, so they are read from the raw event (PLAT-668).
func TestSlackMentionFilesFromRawEvent(t *testing.T) {
	evt := socketmode.Event{Request: &socketmode.Request{Payload: []byte(`{"event":{"type":"app_mention","text":"<@U1> see","files":[{"id":"F1","name":"report.csv"}]}}`)}}
	raw := slackMentionFiles(evt)
	if raw == nil || len(raw.Files) != 1 || raw.Files[0].ID != "F1" {
		t.Fatalf("files = %+v", raw)
	}
	if slackMentionFiles(socketmode.Event{Request: &socketmode.Request{Payload: []byte(`{"event":{"type":"app_mention"}}`)}}) != nil {
		t.Fatal("a mention without files produced some")
	}
}
