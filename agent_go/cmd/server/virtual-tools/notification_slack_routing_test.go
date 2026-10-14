package virtualtools

import (
	"context"
	"errors"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"strings"
	"testing"
)

func TestSlackWorkflowPrecedenceAndExclusions(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		webhook, exclude, durable, fail bool
		wantGlobal, wantWebhook         int
	}{
		{"global fallback", false, false, false, false, 1, 0},
		{"workflow replaces global", true, false, false, false, 0, 1},
		{"one-off excludes both", true, true, false, false, 0, 0},
		{"durable excludes both", true, false, true, false, 0, 0},
		{"failure never falls back", true, false, false, true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := services.GetNotificationManager()
			ch := make(chan *services.NotificationDestination, 4)
			manager.RegisterConnector(&testUserNotificationConnector{name: "slack", ch: ch})
			defer manager.UnregisterConnector("slack")
			old := sendRichSlackIncomingWebhook
			defer func() { sendRichSlackIncomingWebhook = old }()
			calls := 0
			sendRichSlackIncomingWebhook = func(context.Context, string, string, services.SlackWebhookContent) (string, error) {
				calls++
				if tc.fail {
					return "", errors.New("webhook rejected request")
				}
				return "sent", nil
			}
			dest := &services.NotificationDestination{}
			if tc.webhook {
				dest.SlackWebhook = &services.SlackWebhookDest{URL: "https://example.invalid/webhook"}
			}
			if tc.durable {
				dest.ExcludeChannels = []string{"slack"}
			}
			args := map[string]interface{}{"message_for_user": "routing test"}
			if tc.exclude {
				args["exclude_channels"] = []string{" SLACK "}
			}
			result, err := DeliverWorkflowNotification(context.WithValue(context.Background(), BotNotificationDestinationKey, dest), args)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantWebhook || len(ch) != tc.wantGlobal {
				t.Fatalf("webhook=%d global=%d receipt=%s", calls, len(ch), result)
			}
			if tc.fail && !strings.Contains(result, "webhook rejected request") {
				t.Fatalf("error lost: %s", result)
			}
		})
	}
}
