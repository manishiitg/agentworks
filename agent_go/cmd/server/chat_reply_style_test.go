package server

import (
	"context"
	"strings"
	"testing"
)

// Owner, 27 Nov: the rules live in the chat's prompt; the person's message carries nothing unless a reminder is due.
func TestChatRulesLiveInThePromptNotTheMessage(t *testing.T) {
	on := ProductAgentIdentity{ReplyStyle: "concise", NextSteps: "on", UnrelatedTask: "ask"}
	prompt := chatReplyStylePrompt(on)
	if !strings.Contains(prompt, "```next") || !strings.Contains(prompt, "Reply concisely") || !strings.Contains(prompt, "new tab") || !strings.Contains(prompt, "never an agent message") {
		t.Fatalf("card, style and unrelated question are prompt rules for the person's own turns, got %q", prompt)
	}
	if note := chatRulesTurnNote(on, true, false); note != "" {
		t.Fatalf("the person's message stays what they typed, got %q", note)
	}
	note := chatRulesTurnNote(on, true, true)
	if !strings.HasPrefix(note, chatRepliesMarker) || !strings.Contains(note, "Reminder") || len(note) > 220 {
		t.Fatalf("a due reminder is one short marked line, got %q", note)
	}
	if note := chatRulesTurnNote(on, false, false); !strings.Contains(note, "not from the person") {
		t.Fatalf("an agent/schedule/Slack turn is told the rules don't apply, got %q", note)
	}
	if note := chatRulesTurnNote(ProductAgentIdentity{ReplyStyle: "concise"}, false, true); note != "" {
		t.Fatalf("with no card or question on, nothing is ever added, got %q", note)
	}
	if line := chatReplyStylePrompt(ProductAgentIdentity{ReplyStyle: "Learning"}); !strings.Contains(line, "Teach as you go") {
		t.Fatalf("the style is a prompt line, got %q", line)
	}
	if line := chatReplyStylePrompt(ProductAgentIdentity{}); line != "" {
		t.Fatalf("Default adds nothing to the prompt, got %q", line)
	}
}

func TestChatReplyStyleAcceptsOnlyOfferedStyles(t *testing.T) {
	for _, style := range chatReplyStyles {
		if _, err := normalizeChatReplyStyle(style); err != nil {
			t.Fatalf("offered style %q refused: %v", style, err)
		}
	}
	if _, err := normalizeChatReplyStyle("pirate"); err == nil {
		t.Fatal("an unknown style must be refused")
	}
	view := chatRepliesViewFor(ProductAgentIdentity{})
	if view.Style != "default" || view.NextSteps {
		t.Fatalf("a new chat shows Default with the card off, got %+v", view)
	}
	if !chatRepliesViewFor(ProductAgentIdentity{NextSteps: "on"}).NextSteps {
		t.Fatal("a chat that turned the card on shows it on")
	}
}

func TestChatUnrelatedTaskAskIsOptIn(t *testing.T) {
	if line := chatReplyStylePrompt(ProductAgentIdentity{}); strings.Contains(line, "unrelated") {
		t.Fatalf("a new chat has no unrelated-work rule: asking is opt-in, got %q", line)
	}
	if line := chatReplyStylePrompt(ProductAgentIdentity{UnrelatedTask: "ask"}); !strings.Contains(line, "ask whether to open a new tab") {
		t.Fatalf("Ask me is a prompt rule, got %q", line)
	}
	if line := chatReplyStylePrompt(ProductAgentIdentity{UnrelatedTask: "new_tab"}); !strings.Contains(line, "open a new tab") || !strings.Contains(line, "`sessions` action=open") {
		t.Fatalf("new_tab is a prompt line naming how to open the tab, got %q", line)
	}
	if line := chatReplyStylePrompt(ProductAgentIdentity{UnrelatedTask: "here"}); line != "" {
		t.Fatalf("here adds nothing to the prompt, got %q", line)
	}
	if _, err := normalizeChatUnrelatedTask("sometimes"); err == nil {
		t.Fatal("an unknown choice must be refused")
	}
	view := chatRepliesViewFor(ProductAgentIdentity{})
	if view.UnrelatedTask != "here" || len(view.UnrelatedTasks) != 3 {
		t.Fatalf("a new chat shows Do it here, got %+v", view)
	}
}

// #4339: the chat-settings note is only on turns the person typed in the app; another agent's message and a
// Watcher event (delivered as an agent message) never get it, and a batch only when the person typed in it.
func TestChatPersonAppTurnOnlyForThePersonsOwnMessages(t *testing.T) {
	ctx := context.Background()
	if !chatPersonAppTurn(ctx, &QueryRequest{Query: "hi"}, "chat-1") {
		t.Fatal("the person's own typed turn lost the note")
	}
	if chatPersonAppTurn(ctx, &QueryRequest{Query: "event", AgentMessageInbox: "inbox-1"}, "chat-1") {
		t.Fatal("an agent message or Watcher event got the note")
	}
	delivered := context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "agent_message"})
	if chatPersonAppTurn(delivered, &QueryRequest{Query: "event"}, "chat-1") {
		t.Fatal("a delivered agent message without an inbox got the note")
	}
	if chatPersonAppTurn(context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "owner_app"}), &QueryRequest{Query: "x"}, "chat-1") {
		t.Fatal("the owner's other AI app got the note")
	}
	if chatPersonAppTurn(context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "queued_batch"}), &QueryRequest{Query: "x"}, "chat-1") {
		t.Fatal("a batch of agent messages got the note")
	}
	if !chatPersonAppTurn(context.WithValue(ctx, deliveredAgentMessageKey{}, map[string]interface{}{"source": "queued_batch", "combined_person_input": true}), &QueryRequest{Query: "x"}, "chat-1") {
		t.Fatal("a batch the person typed in lost the note")
	}
	if chatPersonAppTurn(ctx, &QueryRequest{Query: "x", TriggeredBy: "schedule"}, "chat-1") || chatPersonAppTurn(ctx, &QueryRequest{Query: "x", IsAutoNotification: true}, "chat-1") {
		t.Fatal("a schedule or automatic notification got the note")
	}
}
