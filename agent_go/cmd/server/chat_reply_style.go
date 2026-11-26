package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// A chat's replies (owner, 26 Nov, like Claude Desktop's Output style and Summary and next steps): picked per chat
// from the mode menu in the message box, for every coding app. Both travel as one short note at the end of the
// person's message (chatRepliesNote), so no app has to be relaunched and every app reads it the same way; the screen
// hides the note from the person's bubble (chatMessageContent.ts, the "⚙️ Chat settings:" marker).
//   - ReplyStyle: "" (Default) or one of chatReplyStyles.
//   - NextSteps: on unless "off". On a turn the person sends from the app, the agent may end its reply with a short
//     summary, one question and two or three answers as a ```next block; the screen shows it as a card with one-click
//     answers instead of as text.

var chatReplyStyles = []string{"default", "concise", "explanatory", "learning", "proactive"}

var chatReplyStyleNotes = map[string]string{
	"concise":     "Reply concisely: lead with the result, a few short lines, no recap of the question or your steps.",
	"explanatory": "Explain as you go: say why you chose an approach and point out the patterns and trade-offs in the code you touch.",
	"learning":    "Teach as you go: explain the key ideas, and where it helps the person learn, leave a small, clearly marked part for them to write themselves.",
	"proactive":   "Be proactive: after the asked work, name the next useful steps and do the safe, clearly related ones without waiting to be asked.",
}

// chatNextStepsNote asks for the card's block. Its format is read by the screen (nextSteps.ts); keep them in step.
const chatNextStepsNote = "When you end a reply and the person's next choice is clear, finish the reply with a block: a line ```next, then one line of JSON {\"summary\": \"one or two plain sentences on where the work stands\", \"question\": \"one short question\", \"options\": [\"answer\", \"answer\"]}, then a line ```. Give two or three short answers written as the person would send them, the most likely first. Leave the block out when nothing is waiting on them, and never mention it."

const chatRepliesMarker = "⚙️ Chat settings:"

func normalizeChatReplyStyle(value string) (string, error) {
	style := strings.ToLower(strings.TrimSpace(value))
	if style == "" || style == "default" {
		return "", nil
	}
	if _, ok := chatReplyStyleNotes[style]; ok {
		return style, nil
	}
	return "", fmt.Errorf("reply_style must be one of %s", strings.Join(chatReplyStyles, ", "))
}

// chatRepliesView is the chat's reply settings as the mode menu shows them.
type chatRepliesView struct {
	Style     string   `json:"style"`
	Styles    []string `json:"styles"`
	NextSteps bool     `json:"next_steps"`
}

func chatRepliesViewFor(identity ProductAgentIdentity) *chatRepliesView {
	style, _ := normalizeChatReplyStyle(identity.ReplyStyle)
	if style == "" {
		style = "default"
	}
	return &chatRepliesView{Style: style, Styles: chatReplyStyles, NextSteps: identity.NextSteps != "off"}
}

func setChatReplies(ctx context.Context, owner string, tab chatContextTab, style *string, nextSteps *bool) (chatContextTab, error) {
	change := chatContextChange{}
	if style != nil {
		normalized, err := normalizeChatReplyStyle(*style)
		if err != nil {
			return tab, err
		}
		change.ReplyStyle = &normalized
	}
	if nextSteps != nil {
		value := ""
		if !*nextSteps {
			value = "off"
		}
		change.NextSteps = &value
	}
	identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, change, owner)
	if err != nil {
		return tab, err
	}
	log.Printf("[CHAT_REPLIES] owner=%s session=%s style=%q next_steps=%q", owner, tab.Record.SessionID, identity.ReplyStyle, identity.NextSteps)
	tab.Identity = identity
	return tab, nil
}

// chatRepliesNote is the note added to this turn's message, or "" when the chat uses the defaults and the card does
// not apply. attended: the person sent this turn from the app, where the card can show.
func chatRepliesNote(identity ProductAgentIdentity, attended bool) string {
	var lines []string
	if style, _ := normalizeChatReplyStyle(identity.ReplyStyle); style != "" {
		lines = append(lines, chatReplyStyleNotes[style])
	}
	if attended && identity.NextSteps != "off" {
		lines = append(lines, chatNextStepsNote)
	}
	if len(lines) == 0 {
		return ""
	}
	return chatRepliesMarker + " " + strings.Join(lines, " ")
}
