package server

import (
	"strings"
	"testing"
)

func TestChatRepliesNoteFollowsStyleAndCard(t *testing.T) {
	if note := chatRepliesNote(ProductAgentIdentity{NextSteps: "off"}, true); note != "" {
		t.Fatalf("default style with the card off adds nothing, got %q", note)
	}
	if note := chatRepliesNote(ProductAgentIdentity{}, false); note != "" {
		t.Fatalf("the card is only asked for on the person's own app turns, got %q", note)
	}
	note := chatRepliesNote(ProductAgentIdentity{ReplyStyle: "Concise"}, true)
	if !strings.HasPrefix(note, chatRepliesMarker) || !strings.Contains(note, "Reply concisely") || !strings.Contains(note, "```next") {
		t.Fatalf("concise style and the card belong in one marked note, got %q", note)
	}
	if note := chatRepliesNote(ProductAgentIdentity{ReplyStyle: "learning", NextSteps: "off"}, true); strings.Contains(note, "```next") || !strings.Contains(note, "Teach as you go") {
		t.Fatalf("card off keeps only the style, got %q", note)
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
