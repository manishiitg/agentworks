package server

import (
	"context"
	"strings"
	"testing"
)

func TestChatRepliesNoteFollowsStyleAndCard(t *testing.T) {
	if note := chatRepliesNote(ProductAgentIdentity{NextSteps: "off", UnrelatedTask: "here"}, true); note != "" {
		t.Fatalf("with the card off and unrelated work done here the message gets nothing, got %q", note)
	}
	if note := chatRepliesNote(ProductAgentIdentity{}, false); note != "" {
		t.Fatalf("the card is only asked for on the person's own app turns, got %q", note)
	}
	note := chatRepliesNote(ProductAgentIdentity{ReplyStyle: "concise"}, true)
	if !strings.HasPrefix(note, chatRepliesMarker) || !strings.Contains(note, "```next") || strings.Contains(note, "Reply concisely") {
		t.Fatalf("the message carries only the card note; the style is in the prompt, got %q", note)
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
	if view.Style != "default" || !view.NextSteps {
		t.Fatalf("a new chat shows Default with the card on, got %+v", view)
	}
}

func TestChatUnrelatedTaskAsksByDefault(t *testing.T) {
	note := chatRepliesNote(ProductAgentIdentity{NextSteps: "off"}, true)
	if !strings.HasPrefix(note, chatRepliesMarker) || !strings.Contains(note, "new tab") || strings.Contains(note, "```next") {
		t.Fatalf("a new chat asks about unrelated work, even with the card off, got %q", note)
	}
	if note := chatRepliesNote(ProductAgentIdentity{UnrelatedTask: "new_tab"}, true); strings.Contains(note, "unrelated") {
		t.Fatalf("new_tab is a prompt rule, not a message note, got %q", note)
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
	if view.UnrelatedTask != "ask" || len(view.UnrelatedTasks) != 3 {
		t.Fatalf("a new chat shows Ask me, got %+v", view)
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
