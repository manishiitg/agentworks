package services

import (
	"context"
	"testing"
)

// A person who writes in a thread before picking from the "who should answer" buttons is asked again, with their message
// waiting for the pick; someone else in the thread is not, and a thread with no open question still starts nothing.
func TestPlainReplyInThreadWithOpenPickerAsksAgain(t *testing.T) {
	t.Setenv("WORKSPACE_API_URL", "http://127.0.0.1:1") // no stored thread binding
	targets := []SlackTarget{
		{Ref: SlackTargetRef{ProfileID: "work", WorkspacePath: "Crew/a-1"}, Slug: "alpha", Label: "Alpha"},
		{Ref: SlackTargetRef{WorkspacePath: "Workflow/beta"}, Slug: "beta", Label: "Beta"},
	}
	SetSlackRoutingHooks(&SlackRoutingHooks{
		Channel: func(context.Context, string, string) SlackTargetSet {
			return SlackTargetSet{Targets: targets, Default: -1}
		},
		DM:       func(context.Context, string) SlackTargetSet { return SlackTargetSet{} },
		Route:    func(context.Context, string, string, SlackTarget) (*ChannelRoute, error) { return &ChannelRoute{}, nil },
		CanReach: func(context.Context, string, ChannelRoute) bool { return true },
	})
	t.Cleanup(func() { SetSlackRoutingHooks(nil) })
	s := &SlackService{connectionID: "conn"}
	thread := ThreadID{Platform: "slack", ChannelID: "C0CHAN", ThreadTS: "1790000000.000100", ConnectionID: "conn"}

	if pick := s.selectChannelTarget(context.Background(), "C0CHAN", thread.ThreadTS, "run smoke test", false); len(pick.choices) != 0 || pick.route != nil {
		t.Fatalf("a thread with no open question must start nothing, got %+v", pick)
	}

	rememberSlackPendingPick(thread, BotIncomingMessage{UserID: "U-asker"}, nil, targets)
	t.Cleanup(func() { takeSlackPendingPick(thread) })
	pick := s.selectChannelTarget(context.Background(), "C0CHAN", thread.ThreadTS, "run smoke test", false)
	if len(pick.choices) != 2 || pick.nudgeFor != "U-asker" || pick.text != "run smoke test" {
		t.Fatalf("a plain reply must re-ask the open question for its asker, got %+v", pick)
	}
}
