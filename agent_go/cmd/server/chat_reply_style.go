package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// A chat's replies (owner, 26 Nov, like Claude Desktop's Output style and Summary and next steps): picked per chat
// from the mode menu in the message box, for every coding app (owner, 26 Nov: style at prompt level, card at message
// level). The style is a line of the chat's prompt (chatReplyStylePrompt). The card's request is a short note at the
// end of the person's message (chatRepliesNote), only on turns sent from the app; the screen hides it from the
// person's bubble (chatMessageContent.ts, the "⚙️ Chat settings:" marker).
//   - ReplyStyle: "" (Default) or one of chatReplyStyles.
//   - NextSteps: on unless "off". On a turn the person sends from the app, the agent may end its reply with a short
//     summary, one question and two or three answers as a ```next block; the screen shows it as a card with one-click
//     answers instead of as text.
//   - UnrelatedTask (owner, 26 Nov, like Claude Desktop's "When you ask for an unrelated task"): what the agent does
//     when the person asks in this chat for a different piece of work. "" asks them (new tab or here), a note on the
//     person's app turns; new_tab is a standing rule in the prompt; here adds nothing (the coding app's usual way).

var chatReplyStyles = []string{"default", "concise", "explanatory", "learning", "proactive"}

var chatReplyStyleNotes = map[string]string{
	"concise":     "Reply concisely: lead with the result, a few short lines, no recap of the question or your steps.",
	"explanatory": "Explain as you go: say why you chose an approach and point out the patterns and trade-offs in the code you touch.",
	"learning":    "Teach as you go: explain the key ideas, and where it helps the person learn, leave a small, clearly marked part for them to write themselves.",
	"proactive":   "Be proactive: after the asked work, name the next useful steps and do the safe, clearly related ones without waiting to be asked.",
}

// chatNextStepsNote asks for the card's block. Its format is read by the screen (nextSteps.ts); keep them in step.
const chatNextStepsNote = "When you end a reply and the person's next choice is clear, finish the reply with a block: a line ```next, then one line of JSON {\"summary\": \"one or two plain sentences on where the work stands\", \"question\": \"one short question\", \"options\": [\"answer\", \"answer\"]}, then a line ```. Give two or three short answers written as the person would send them, the most likely first. Leave the block out when nothing is waiting on them, and never mention it."

var chatUnrelatedTaskChoices = []string{"ask", "new_tab", "here"}

// chatNewTabHow is how the agent opens the new tab; the sessions tool is in every project chat's tools.
const chatNewTabHow = "a new tab is `sessions` action=open with place=beside and the request, in full, as its task"

const chatUnrelatedAskNote = "If this message asks for a piece of work unrelated to this chat's task, don't start it yet: ask whether to open a new tab for it or do it here, then follow the answer (" + chatNewTabHow + ")."

const chatUnrelatedNewTabPrompt = "\n\nWhen the person asks in this chat for a piece of work unrelated to this chat's task, don't do it here: open a new tab for it (" + chatNewTabHow + ") and tell them in one line where it went (the person's choice for this chat)."

func normalizeChatUnrelatedTask(value string) (string, error) {
	choice := strings.ToLower(strings.TrimSpace(value))
	switch choice {
	case "", "ask":
		return "", nil
	case "new_tab", "here":
		return choice, nil
	}
	return "", fmt.Errorf("unrelated_task must be one of %s", strings.Join(chatUnrelatedTaskChoices, ", "))
}

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
	// UnrelatedTask is ask, new_tab or here.
	UnrelatedTask  string   `json:"unrelated_task"`
	UnrelatedTasks []string `json:"unrelated_tasks"`
}

func chatRepliesViewFor(identity ProductAgentIdentity) *chatRepliesView {
	style, _ := normalizeChatReplyStyle(identity.ReplyStyle)
	if style == "" {
		style = "default"
	}
	unrelated, _ := normalizeChatUnrelatedTask(identity.UnrelatedTask)
	if unrelated == "" {
		unrelated = "ask"
	}
	return &chatRepliesView{Style: style, Styles: chatReplyStyles, NextSteps: identity.NextSteps != "off", UnrelatedTask: unrelated, UnrelatedTasks: chatUnrelatedTaskChoices}
}

func setChatReplies(ctx context.Context, owner string, tab chatContextTab, style *string, nextSteps *bool, unrelated *string) (chatContextTab, error) {
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
	if unrelated != nil {
		normalized, err := normalizeChatUnrelatedTask(*unrelated)
		if err != nil {
			return tab, err
		}
		change.UnrelatedTask = &normalized
	}
	identity, err := defaultProductConversationRegistryStore().setChatContextOptions(ctx, owner, tab.ProfileID, tab.Record.ConversationKey, change, owner)
	if err != nil {
		return tab, err
	}
	log.Printf("[CHAT_REPLIES] owner=%s session=%s style=%q next_steps=%q unrelated_task=%q", owner, tab.Record.SessionID, identity.ReplyStyle, identity.NextSteps, identity.UnrelatedTask)
	tab.Identity = identity
	return tab, nil
}

// chatReplyStylePrompt is the chat's standing reply rules as lines of its prompt, or "" when it has none: the reply
// style (not Default) and an unrelated task always going to a new tab. They live in the prompt with the chat's role,
// not on every message.
func chatReplyStylePrompt(identity ProductAgentIdentity) string {
	out := ""
	if style, _ := normalizeChatReplyStyle(identity.ReplyStyle); style != "" {
		out += "\n\nReply style for this chat (the person's choice): " + chatReplyStyleNotes[style]
	}
	if unrelated, _ := normalizeChatUnrelatedTask(identity.UnrelatedTask); unrelated == "new_tab" {
		out += chatUnrelatedNewTabPrompt
	}
	return out
}

// chatRepliesNote is the note added to this turn's message, or "" when nothing applies: the card's request and, when
// the chat asks about unrelated work, that question. Both need the person to answer, so only the server, which knows
// where a turn came from, adds them, and only on turns the person sent from the app (attended).
func chatRepliesNote(identity ProductAgentIdentity, attended bool) string {
	if !attended {
		return ""
	}
	parts := []string{}
	if unrelated, _ := normalizeChatUnrelatedTask(identity.UnrelatedTask); unrelated == "" {
		parts = append(parts, chatUnrelatedAskNote)
	}
	if identity.NextSteps != "off" {
		parts = append(parts, chatNextStepsNote)
	}
	if len(parts) == 0 {
		return ""
	}
	return chatRepliesMarker + " " + strings.Join(parts, " ")
}

// chatPersonAppTurn is a turn the person typed in the app (#4339): attended, typed by a person (not a schedule,
// bot or someone else's ask), and not delivered as a message. Another agent's message, a Watcher event (also
// delivered as an agent message) and the owner's other AI apps are attended for question cards but never get the
// chat-settings note (#4245, #4211). A batch of queued turns gets it only when the person typed one of them.
func chatPersonAppTurn(ctx context.Context, req *QueryRequest, sessionID string) bool {
	if req == nil || strings.TrimSpace(req.AgentMessageInbox) != "" || crewAskInputFromContext(ctx) != nil {
		return false
	}
	if delivery, ok := ctx.Value(deliveredAgentMessageKey{}).(map[string]interface{}); ok && len(delivery) > 0 {
		if delivery["source"] != "queued_batch" || delivery["combined_person_input"] != true {
			return false
		}
	}
	return codingAgentTurnAttended(GetUserFromContext(ctx), req, sessionID) && personTypedChatTurn(ctx, *req, sessionID, false)
}
