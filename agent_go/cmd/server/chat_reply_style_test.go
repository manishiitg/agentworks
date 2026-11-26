package server

import (
	"strings"
	"testing"
)

func TestChatRepliesNoteFollowsStyleAndCard(t *testing.T) {
	if note := chatRepliesNote(ProductAgentIdentity{NextSteps: "off"}, true); note != "" {
		t.Fatalf("with the card off the message gets nothing, got %q", note)
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
