package services

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// A reload used to leave the old listener running: its event loop kept reading
// the old connection forever and acked through the service's current client,
// which is nil after a stop (a panic that stopped the server). A stopped
// listener's loop must end.
func TestSlackSocketListenerEventLoopEndsWhenStopped(t *testing.T) {
	s := &SlackService{}
	client := socketmode.New(slack.New("xoxb-test", slack.OptionAppLevelToken("xapp-test")))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleSocketModeEvents(ctx, client)
	}()
	client.Events <- socketmode.Event{Type: socketmode.EventTypeConnectionError, Data: "test"}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("event loop kept running after its listener was stopped")
	}
}

// slack-go wire logging writes every raw Slack message, full text included,
// to the server log: it must stay off unless asked for.
func TestSlackSocketWireLoggingOffByDefault(t *testing.T) {
	t.Setenv("SLACK_SOCKET_DEBUG", "")
	if slackSocketDebug() {
		t.Fatal("Slack wire logging is on by default")
	}
	t.Setenv("SLACK_SOCKET_DEBUG", "true")
	if !slackSocketDebug() {
		t.Fatal("SLACK_SOCKET_DEBUG=true did not turn wire logging on")
	}
}

// A settings save swaps the Slack config and client while sends, listeners and
// settings reads use them; unguarded, a read could see a half-swapped service
// (a nil client mid-send stops the server). Run with -race.
func TestSlackReloadSwapAndReadsDoNotRace(t *testing.T) {
	s := &SlackService{}
	conn := SlackConnection{ID: "slack_001", Enabled: true, BotToken: "xoxb-test", AppToken: "xapp-test"}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			s.setConfig(&SlackConfig{DefaultConnectionID: conn.ID, Connections: []SlackConnection{conn}})
			if i%2 == 0 {
				s.setClient(slack.New(conn.BotToken), true)
			} else {
				s.setClient(nil, false)
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		_ = s.GetConfig()
		_ = s.IsEnabled()
		_ = s.ListConnections()
		_, _ = s.effectiveConnection()
		_ = s.AddReaction(context.Background(), "", "", "")
	}
	close(stop)
	<-done
}
