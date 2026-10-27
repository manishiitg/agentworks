package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

type replyBoundaryConnector struct {
	testBotConnector
	updates []string
	removed []string
}

func (c *replyBoundaryConnector) SendThreadMessage(_ context.Context, _ ThreadID, text string) (string, error) {
	c.sent = append(c.sent, text)
	return fmt.Sprintf("reply-%d", len(c.sent)), nil
}
func (c *replyBoundaryConnector) UpdateMessage(_ context.Context, _ ThreadID, id, text string) error {
	c.updates = append(c.updates, id+":"+text)
	return nil
}
func (c *replyBoundaryConnector) RemoveReaction(_ context.Context, channel, ts, emoji string) error {
	c.removed = append(c.removed, channel+":"+ts+":"+emoji)
	return nil
}
func replyChunk(text string, delta bool) BotEventData {
	return BotEventData{Type: "streaming_chunk", Data: &events.AgentEvent{Data: &events.StreamingChunkEvent{Content: text, IsDelta: delta}}}
}
func TestAcceptedFollowUpGetsSeparateEditableReply(t *testing.T) {
	for _, platform := range []string{"slack", "discord", "telegram"} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			c := &replyBoundaryConnector{}
			f := NewBotEventFilter(c, ThreadID{Platform: platform}, "session", "", "user")
			f.processEvent(ctx, replyChunk("First answer.", false))
			f.pendingDelegations = 1
			f.processEvent(ctx, BotEventData{Type: "user_message"})
			if f.pendingDelegations != 1 || f.sessionDone || f.completionReceived {
				t.Fatal("reply boundary changed agent turn tracking")
			}
			f.processEvent(ctx, replyChunk("First answer. Second answer", false))
			f.lastStreamingUpdate = time.Time{}
			f.processEvent(ctx, replyChunk("First answer. Second answer continues", false))
			f.flushStreamingMessage(ctx, "Second answer final")
			if len(c.sent) != 2 || c.sent[0] != "First answer." || c.sent[1] != " Second answer" {
				t.Fatalf("separate replies = %#v", c.sent)
			}
			if len(c.updates) != 2 || c.updates[0] != "reply-2: Second answer continues" || c.updates[1] != "reply-2:Second answer final" {
				t.Fatalf("follow-up stream repeated old text: %#v", c.updates)
			}
			for _, update := range c.updates {
				if len(update) < 8 || update[:8] != "reply-2:" {
					t.Fatalf("edited older reply: %q", update)
				}
			}
		})
	}
}
func TestFollowUpReplySupportsDeltaChunksAndRepeatedMessages(t *testing.T) {
	c := &replyBoundaryConnector{}
	f := NewBotEventFilter(c, ThreadID{Platform: "slack"}, "session", "", "user")
	ctx := context.Background()
	f.processEvent(ctx, replyChunk("One", true))
	f.processEvent(ctx, BotEventData{Type: "user_message"})
	f.processEvent(ctx, replyChunk(" two", true))
	f.processEvent(ctx, BotEventData{Type: "user_message"})
	f.processEvent(ctx, replyChunk("One two three", false))
	if len(c.sent) != 3 || c.sent[2] != " three" {
		t.Fatalf("replies = %#v", c.sent)
	}
	f.ResetForNewTurn()
	if f.streamingReplyPrefix != "" {
		t.Fatal("prior turn prefix retained")
	}
}
func TestCompletionClearsEveryAcceptedMessageReaction(t *testing.T) {
	c := &replyBoundaryConnector{testBotConnector: testBotConnector{name: "slack"}}
	m := &BotConversationManager{connectors: map[string]BotConnector{"slack": c}}
	m.clearBotMessageReactions(context.Background(), []ThreadID{
		{Platform: "slack", ChannelID: "C1", ThreadTS: "original"},
		{Platform: "slack", ChannelID: "C1", ThreadTS: "follow-up"},
	})
	if len(c.removed) != 4 {
		t.Fatalf("reaction cleanup = %#v", c.removed)
	}
	if c.removed[2] != "C1:follow-up:eyes" {
		t.Fatalf("follow-up acknowledgement left behind: %#v", c.removed)
	}
}

// The final message is the answer the web chat shows, not the answer with the
// turn's running narration in front of it.
func TestFinalReplyReplacesStreamedNarration(t *testing.T) {
	ctx := context.Background()
	c := &replyBoundaryConnector{}
	f := NewBotEventFilter(c, ThreadID{Platform: "slack"}, "session", "", "user")
	f.processEvent(ctx, replyChunk("I'll check the AWS logs.\n\nSwitching to us-east-2.\n\nLogs show a LiveKit disconnect at 21:56.", false))
	f.flushStreamingMessage(ctx, "Logs show a LiveKit disconnect at 21:56.")
	if len(c.updates) == 0 || c.updates[len(c.updates)-1] != "reply-1:Logs show a LiveKit disconnect at 21:56." {
		t.Fatalf("final message kept the narration: sent=%#v updates=%#v", c.sent, c.updates)
	}
	updates := len(c.updates)
	f.flushStreamingMessage(ctx, "Logs show a LiveKit disconnect at 21:56.")
	if len(c.updates) != updates {
		t.Fatal("an identical final message must not be re-sent")
	}
}

// Server B 2026-10-27: a reply streamed "I", the rest arrived inside the update interval, then a tool
// step ran for minutes with no text, and Slack showed only "I". Throttled text must still go out.
func TestThrottledStreamTextReachesSlackWithoutAnotherChunk(t *testing.T) {
	old := botStreamUpdateInterval
	botStreamUpdateInterval = 40 * time.Millisecond
	defer func() { botStreamUpdateInterval = old }()
	ctx := context.Background()
	c := &replyBoundaryConnector{}
	f := NewBotEventFilter(c, ThreadID{Platform: "slack"}, "session", "", "user")
	f.processEvent(ctx, replyChunk("I", true))
	f.processEvent(ctx, replyChunk("'m searching the dev logs around the alert.", true))
	time.Sleep(200 * time.Millisecond)
	f.streamingSendMu.Lock()
	defer f.streamingSendMu.Unlock()
	if len(c.sent) != 1 || len(c.updates) == 0 || c.updates[len(c.updates)-1] != "reply-1:I'm searching the dev logs around the alert." {
		t.Fatalf("Slack did not get the full streamed text: sent=%#v updates=%#v", c.sent, c.updates)
	}
}
