package server

import (
	"strings"
	"testing"
	"time"
)

// Showcase 2026-11-06 07:55:23: Muse (serve protocol) took a steered message
// 140 ms before its turn completed; the message showed as delivered and no turn
// ever answered it. A steer with no reply text after it gets one follow-up turn
// carrying the message; a steer the turn went on to answer gets none.
func TestLateSteerAfterFinalReplyStartsFollowupTurn(t *testing.T) {
	started := make(chan string, 4)
	previous := startLateSteerFollowup
	startLateSteerFollowup = func(_ *StreamingAPI, sessionID, prompt string) bool {
		started <- sessionID + "\x00" + prompt
		return true
	}
	defer func() { startLateSteerFollowup = previous }()

	api := &StreamingAPI{}
	const message = "ask it whats the latest chats"
	cases := []struct {
		name string
		// ackAfterReply is when the CLI took the steer, relative to the turn's last reply text.
		ackAfterReply time.Duration
		// ackAfterTurnEnd: the acknowledgement returns after the turn already ended.
		ackAfterTurnEnd bool
		wantFollowup    bool
	}{
		{name: "steer taken after the final reply", ackAfterReply: 3 * time.Second, wantFollowup: true},
		{name: "acknowledged after the turn ended", ackAfterReply: 3 * time.Second, ackAfterTurnEnd: true, wantFollowup: true},
		{name: "turn went on to answer the steer", ackAfterReply: -time.Second, wantFollowup: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := "late-steer-session-" + string(rune('a'+i))
			turnStarted := time.Now().Add(-20 * time.Second)
			lastReply := time.Now().Add(-5 * time.Second)
			steer := lateSteer{message: message, messageID: "steer-message-1", clientMessageID: "submission-1", provider: "muse-cli",
				sentAt: lastReply.Add(tc.ackAfterReply - 100*time.Millisecond), ackAt: lastReply.Add(tc.ackAfterReply)}
			if tc.ackAfterTurnEnd {
				api.settleLateSteers(sessionID, turnStarted, lastReply, true)
				api.noteNativeSteer(sessionID, steer)
			} else {
				api.noteNativeSteer(sessionID, steer)
				api.settleLateSteers(sessionID, turnStarted, lastReply, true)
			}
			select {
			case got := <-started:
				if !tc.wantFollowup {
					t.Fatalf("follow-up started for a steer the turn answered: %q", got)
				}
				parts := strings.SplitN(got, "\x00", 2)
				if parts[0] != sessionID || !strings.Contains(parts[1], message) {
					t.Fatalf("follow-up = session %q prompt %q, want session %q carrying the person's message", parts[0], parts[1], sessionID)
				}
			case <-time.After(300 * time.Millisecond):
				if tc.wantFollowup {
					t.Fatal("no follow-up turn for a steer the turn never answered")
				}
			}
		})
	}
}
