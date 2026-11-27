package server

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// A chat's replies (owner, 26 Nov, like Claude Desktop's Output style and Summary and next steps): picked per chat
// from the mode menu in the message box, for every coding app (owner, 26 Nov: style at prompt level, card at message
// level). Owner, 27 Nov: every rule lives in the chat's prompt (chatReplyStylePrompt), so the person's messages stay
// what they typed; a change mid-chat reaches the running app as the one-time "instructions changed" note. A short
// reminder goes on the person's message only when the rules may be fading (chat_rules_reminder.go): after a
// compaction, when the card stopped being followed, after a long break, or every 20 messages. The screen hides it
// (chatMessageContent.ts, the "⚙️ Chat settings" marker).
//   - ReplyStyle: "" (Default) or one of chatReplyStyles.
//   - NextSteps: off unless "on" (owner, 27 Nov: opt-in; was on by default). On a turn the person sends from the app, the agent may end its reply with a short
//     summary, one question and two or three answers as a ```next block; the screen shows it as a card with one-click
//     answers instead of as text.
//   - UnrelatedTask (owner, 26 Nov, like Claude Desktop's "When you ask for an unrelated task"): what the agent does
//     when the person asks in this chat for a different piece of work. "" (here, the default since owner 27 Nov: opt-in)
//     adds nothing (the coding app's usual way); ask asks them (new tab or here), a note on the person's app turns;
//     new_tab is a standing rule in the prompt.

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

const chatUnrelatedNewTabPrompt = "\n\nWhen the person asks in this chat for a piece of work unrelated to this chat's task, don't do it here: open a new tab for it (" + chatNewTabHow + ") and tell them in one line where it went (the person's choice for this chat)."

func normalizeChatUnrelatedTask(value string) (string, error) {
	choice := strings.ToLower(strings.TrimSpace(value))
	switch choice {
	case "", "here":
		return "", nil
	case "ask", "new_tab":
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
		unrelated = "here"
	}
	return &chatRepliesView{Style: style, Styles: chatReplyStyles, NextSteps: chatNextStepsOn(identity), UnrelatedTask: unrelated, UnrelatedTasks: chatUnrelatedTaskChoices}
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
		value := "off"
		if *nextSteps {
			value = "on"
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
	unrelated, _ := normalizeChatUnrelatedTask(identity.UnrelatedTask)
	switch unrelated {
	case "new_tab":
		out += chatUnrelatedNewTabPrompt
	case "ask":
		out += chatUnrelatedAskPrompt
	}
	if chatNextStepsOn(identity) {
		out += chatNextStepsPrompt
	}
	return out
}

// chatRulesOnlyPersonTurns limits the card and the unrelated-work question to the person's own messages.
const chatRulesOnlyPersonTurns = " Only when answering a message the person typed in the app, never an agent message, a schedule, a Slack message or an automated event."

const chatUnrelatedAskPrompt = "\n\nUnrelated work (the person's choice for this chat): if the person asks for a piece of work unrelated to this chat's task, don't start it yet: ask whether to open a new tab for it or do it here, then follow the answer (" + chatNewTabHow + ")." + chatRulesOnlyPersonTurns

const chatNextStepsPrompt = "\n\nSummary and next steps card (the person's choice for this chat): " + chatNextStepsNote + chatRulesOnlyPersonTurns

// chatRulesTurnNote is the note for this turn's message, or "": on the person's own app turn only a due reminder
// (reminderDue); on any other turn, while a rule is on, a line saying the rules don't apply to it.
func chatRulesTurnNote(identity ProductAgentIdentity, attended, reminderDue bool) string {
	ask := normalizedUnrelatedAsk(identity)
	card := chatNextStepsOn(identity)
	if !ask && !card {
		return ""
	}
	if !attended {
		return chatRepliesMarker + " This message is not from the person in the app: no next card and no unrelated-work question."
	}
	if !reminderDue {
		return ""
	}
	parts := []string{}
	if card {
		parts = append(parts, "end a reply that waits on the person with the ```next card")
	}
	if ask {
		parts = append(parts, "ask before starting work unrelated to this chat")
	}
	return chatRepliesMarker + " Reminder: " + strings.Join(parts, "; ") + " (details in your instructions)."
}

func normalizedUnrelatedAsk(identity ProductAgentIdentity) bool {
	unrelated, _ := normalizeChatUnrelatedTask(identity.UnrelatedTask)
	return unrelated == "ask"
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

// chatNextStepsOn: the card is opt-in per chat (owner, 27 Nov). Chats that never chose ("") and chats turned off
// ("off") get no card note.
func chatNextStepsOn(identity ProductAgentIdentity) bool {
	return identity.NextSteps == "on"
}
