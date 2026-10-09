package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/mcpagent/events"
	"github.com/slack-go/slack"
)

func catchupTS(ts string) time.Time {
	t, _ := parseSlackTS(ts)
	return t
}

func catchupMsg(ts, user, text string) ThreadMessage {
	return ThreadMessage{UserID: user, UserName: user, Text: text, Timestamp: catchupTS(ts), TS: ts}
}

func catchupSelf(ts, text string) ThreadMessage {
	return ThreadMessage{UserID: "U-bot", UserName: "qa-bot", Text: text, Timestamp: catchupTS(ts), TS: ts, IsBot: true, IsSelf: true}
}

func catchupApp(ts, name, text string) ThreadMessage {
	return ThreadMessage{UserName: name, Text: text, Timestamp: catchupTS(ts), TS: ts, IsBot: true}
}

type failingHistoryConnector struct{ *testBotConnector }

func (c *failingHistoryConnector) GetThreadHistory(context.Context, ThreadID) ([]ThreadMessage, error) {
	return nil, errors.New("invalid_arguments")
}

func newCatchupManager(history []ThreadMessage) (*BotConversationManager, *historyTestConnector, ThreadID) {
	manager := NewBotConversationManager(nil, "", "")
	connector := &historyTestConnector{
		testBotConnector: &testBotConnector{name: "slack", supportsThreads: true},
		history:          history,
	}
	manager.RegisterConnector(connector)
	return manager, connector, ThreadID{Platform: "slack", ChannelID: "C1", ThreadTS: "1000.000100"}
}

// First turn: the existing prepend is unchanged and marks everything given.
// Follow-up: only messages newer than that turn and older than the current
// message are added; the bot's own posts are excluded; a repeat adds nothing.
func TestThreadCatchupOnlyNewMessagesSinceLastTurn(t *testing.T) {
	history := []ThreadMessage{
		catchupMsg("1000.000100", "U-alice", "hi"),
	}
	manager, connector, threadID := newCatchupManager(history)

	first := manager.buildQueryWithThreadHistory("hi", "slack", threadID)
	if first != "hi" {
		t.Fatalf("first turn with no prior history should stay the raw query, got %q", first)
	}

	connector.history = []ThreadMessage{
		catchupMsg("1000.000100", "U-alice", "hi"),
		catchupSelf("1000.000200", "Hello! How can I help?"),
		catchupApp("1000.000300", "Sentry", "TypeError: cannot read properties of undefined"),
		catchupMsg("1000.000400", "U-bob", "seeing this on prod too"),
		catchupMsg("1000.000500", "U-alice", "can you check why above sentry error came"),
		catchupMsg("1000.000600", "U-carol", "posted after the question"),
	}
	msg := BotIncomingMessage{Platform: "slack", ChannelID: "C1", ThreadTS: threadID.ThreadTS, MessageTS: "1000.000500", Text: "can you check why above sentry error came"}
	block := manager.threadCatchupContext(msg, threadID)

	if !strings.HasPrefix(block, "## New messages in this thread since your last reply") {
		t.Fatalf("missing catch-up header:\n%s", block)
	}
	for _, want := range []string{"**Sentry (app)**", "TypeError: cannot read properties of undefined", "**U-bob**", "seeing this on prod too"} {
		if !strings.Contains(block, want) {
			t.Fatalf("catch-up block missing %q:\n%s", want, block)
		}
	}
	for _, unwanted := range []string{"hi\n", "How can I help", "can you check why", "posted after the question"} {
		if strings.Contains(block, unwanted) {
			t.Fatalf("catch-up block should not contain %q:\n%s", unwanted, block)
		}
	}

	// The same thread state on the next turn: nothing is repeated except the
	// message that arrived after the previous current message.
	connector.history = append(connector.history, catchupSelf("1000.000700", "Looking into it"),
		catchupMsg("1000.000800", "U-alice", "any update?"))
	next := BotIncomingMessage{Platform: "slack", ChannelID: "C1", ThreadTS: threadID.ThreadTS, MessageTS: "1000.000800", Text: "any update?"}
	block2 := manager.threadCatchupContext(next, threadID)
	// Only U-carol's message is new: it was posted after the previous
	// current message, while the bot was working.
	if !strings.Contains(block2, "posted after the question") {
		t.Fatalf("follow-up missed the message posted since the last turn:\n%s", block2)
	}
	for _, unwanted := range []string{"Sentry", "U-bob", "any update?", "Looking into it"} {
		if strings.Contains(block2, unwanted) {
			t.Fatalf("repeated follow-up contains %q:\n%s", unwanted, block2)
		}
	}

	// Asking again with identical history yields nothing.
	if again := manager.threadCatchupContext(next, threadID); again != "" {
		t.Fatalf("identical follow-up should add nothing, got:\n%s", again)
	}
}

func TestThreadCatchupIncludesMessagesAfterLastBotReplyWithoutWatermark(t *testing.T) {
	// No first-turn prepend in this process (e.g. server restarted): the bot's
	// own latest post bounds the catch-up.
	manager, _, threadID := newCatchupManager([]ThreadMessage{
		catchupMsg("1000.000100", "U-alice", "old question"),
		catchupSelf("1000.000200", "old answer"),
		catchupApp("1000.000300", "Sentry", "new alert"),
		catchupMsg("1000.000400", "U-alice", "why?"),
	})
	block := manager.threadCatchupContext(BotIncomingMessage{MessageTS: "1000.000400", Text: "why?"}, threadID)
	if !strings.Contains(block, "new alert") || strings.Contains(block, "old question") || strings.Contains(block, "old answer") {
		t.Fatalf("unexpected catch-up block:\n%s", block)
	}
}

func TestThreadCatchupCapsCountAndLength(t *testing.T) {
	var history []ThreadMessage
	history = append(history, catchupSelf("1000.000001", "reply"))
	for i := 10; i < 40; i++ {
		ts := "1001.0000" + itoa2(i)
		history = append(history, catchupMsg(ts, "U-x", strings.Repeat("y", 3000)))
	}
	manager, _, threadID := newCatchupManager(history)
	block := manager.threadCatchupContext(BotIncomingMessage{MessageTS: "1002.000000", Text: "q"}, threadID)
	if len(block) > threadCatchupMaxTotalChars+500 {
		t.Fatalf("catch-up block not capped: %d chars", len(block))
	}
	if !strings.Contains(block, "earlier message(s) omitted") || !strings.Contains(block, "[truncated]") {
		t.Fatalf("expected omission and truncation markers:\n%s", block[:200])
	}
}

func itoa2(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestThreadCatchupHistoryFailureContinuesWithoutIt(t *testing.T) {
	manager := NewBotConversationManager(nil, "", "")
	manager.RegisterConnector(&failingHistoryConnector{&testBotConnector{name: "slack", supportsThreads: true}})
	threadID := ThreadID{Platform: "slack", ChannelID: "C1", ThreadTS: "1000.000100"}
	if block := manager.threadCatchupContext(BotIncomingMessage{MessageTS: "1000.000500", Text: "q"}, threadID); block != "" {
		t.Fatalf("expected empty block on history failure, got %q", block)
	}
	if got := withThreadCatchup("", "q"); got != "q" {
		t.Fatalf("empty catch-up must leave text unchanged, got %q", got)
	}
}

// The server A case end to end: a completed threaded conversation continued by a
// follow-up gets the Sentry alert posted in the thread since its last reply.
func TestCompletedThreadFollowUpIncludesAppAlert(t *testing.T) {
	manager, _, threadID := newCatchupManager([]ThreadMessage{
		catchupMsg("1000.000100", "U-alice", "hi"),
		catchupSelf("1000.000200", "Hello!"),
		catchupApp("1000.000300", "Sentry", "ZeroDivisionError in checkout"),
		catchupMsg("1000.000400", "U-alice", "can you check why above sentry error came"),
	})
	started := make(chan map[string]interface{}, 1)
	manager.SetStartSessionFunc(func(_ context.Context, req map[string]interface{}, _ string, _ string, _ func(event *events.AgentEvent)) error {
		started <- req
		return nil
	})
	active := &activeBotSession{
		SessionID: "bot-slack--old", UserID: "user-1", Status: chathistory.BotSessionStatusCompleted,
		Platform: "slack", ThreadID: threadID, LastActivity: time.Now(), builderDone: true,
	}
	manager.handleExistingSession(active, BotIncomingMessage{
		Platform: "slack", ChannelID: "C1", ThreadTS: threadID.ThreadTS, UserID: "U-alice",
		MessageTS: "1000.000400", Text: "can you check why above sentry error came", IsMention: true,
	}, true)
	select {
	case req := <-started:
		q, _ := req["query"].(string)
		if !strings.Contains(q, "## New messages in this thread since your last reply") || !strings.Contains(q, "ZeroDivisionError in checkout") {
			t.Fatalf("follow-up query missing the Sentry alert:\n%s", q)
		}
		if strings.Contains(q, "Hello!") {
			t.Fatalf("follow-up query repeated the bot's own reply:\n%s", q)
		}
		if !strings.Contains(q, "can you check why above sentry error came") {
			t.Fatalf("follow-up query lost the current message:\n%s", q)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the continued session to start")
	}
}

// The first-turn prepend keeps its exact format.
func TestFirstTurnThreadHistoryPrependUnchanged(t *testing.T) {
	manager, _, threadID := newCatchupManager([]ThreadMessage{
		{UserName: "U-alice", Text: "first", Timestamp: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)},
		{Text: "answer", IsBot: true, Timestamp: time.Date(2026, 9, 25, 10, 1, 0, 0, time.UTC)},
		{UserName: "U-alice", Text: "now", Timestamp: time.Date(2026, 9, 25, 10, 2, 0, 0, time.UTC)},
	})
	got := manager.buildQueryWithThreadHistory("now", "slack", threadID)
	want := "## Previous Conversation in This Thread\n\n**U-alice** (2026-09-25 10:00 UTC): first\n\n**Agent** (2026-09-25 10:01 UTC): answer\n\n---\n\n## Current Message\n\nnow"
	if got != want {
		t.Fatalf("first-turn prepend changed:\n got: %q\nwant: %q", got, want)
	}
}

func TestSlackMessageTextFlattensAppAttachmentsAndBlocks(t *testing.T) {
	raw := `{
		"type": "message", "bot_id": "B-sentry", "username": "Sentry", "ts": "1758790000.123456",
		"bot_profile": {"name": "Sentry"},
		"text": "",
		"attachments": [{
			"fallback": "[api] TypeError fallback",
			"title": "TypeError: Cannot read properties of undefined (reading 'id')",
			"title_link": "https://sentry.io/issues/1",
			"text": "checkout/handler.ts in processOrder",
			"fields": [{"title": "environment", "value": "production"}],
			"footer": "api-server",
			"blocks": [{"type": "section", "text": {"type": "mrkdwn", "text": "Seen 42 times"}}]
		}],
		"blocks": [
			{"type": "header", "text": {"type": "plain_text", "text": "New issue in api"}},
			{"type": "context", "elements": [{"type": "mrkdwn", "text": "project: api"}]}
		]
	}`
	var msg slack.Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	text := slackMessageText(msg)
	for _, want := range []string{"New issue in api", "project: api", "TypeError: Cannot read properties of undefined", "https://sentry.io/issues/1",
		"checkout/handler.ts in processOrder", "environment: production", "Seen 42 times", "api-server"} {
		if !strings.Contains(text, want) {
			t.Fatalf("flattened text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "fallback") {
		t.Fatalf("fallback should be used only when nothing else is readable:\n%s", text)
	}
	if name := slackMessageAuthorName(msg); name != "Sentry" {
		t.Fatalf("app author name = %q", name)
	}
	if ts, ok := parseSlackTS(msg.Timestamp); !ok || ts.Nanosecond() != 123456000 {
		t.Fatalf("slack ts parsed without microseconds: %v %v", ts, ok)
	}

	// Human messages keep text and do not duplicate their rich_text blocks.
	var human slack.Message
	_ = json.Unmarshal([]byte(`{"type":"message","user":"U1","text":"hello there","blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello there"}]}]}]}`), &human)
	if got := slackMessageText(human); got != "hello there" {
		t.Fatalf("human text = %q", got)
	}
	// Rich-text-only content is still read.
	var richOnly slack.Message
	_ = json.Unmarshal([]byte(`{"type":"message","bot_id":"B2","text":"","blocks":[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"deploy failed "},{"type":"link","url":"https://ci/1","text":"logs"}]}]}]}`), &richOnly)
	if got := slackMessageText(richOnly); got != "deploy failed logs (https://ci/1)" {
		t.Fatalf("rich text = %q", got)
	}
}
