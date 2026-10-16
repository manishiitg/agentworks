package server

import (
	"context"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// A Code tab that was handed work by another tab (ask_project_chat, or the task of open_project_chat) answers in its own
// chat. If it finished the turn without sending a message back to that inbox, its final answer goes to the asker once,
// so the opener always gets a result (owner 2026-10-17). Other messages are never relayed: the answer is itself marked so
// the asker's own turn does not bounce it back.

const tabFinalAnswerPrefix = "final-"

// tabFinalAnswerText is the message to send back, or "" when nothing should be: only between two tabs of Code, not for an
// answer relayed already, and not when the tab replied itself after the message it was asked.
func tabFinalAnswerText(c agentConversation, m agentMessage, source, dest agentMessageEndpoint, final string) string {
	if dest.Profile != codeproduct.ProfileID || source.Profile != codeproduct.ProfileID || dest.Session == "" || source.Session == "" || dest.Session == source.Session {
		return ""
	}
	if strings.HasPrefix(m.Submission, tabFinalAnswerPrefix) || m.Kind != "" {
		return ""
	}
	final = strings.TrimSpace(final)
	if final == "" {
		return ""
	}
	for _, other := range c.Messages {
		if other.From == 1-m.From && other.Sequence > m.Sequence {
			return ""
		}
	}
	text := "This is the final answer of " + dest.Label + " to your message " + m.ID + " (it sent no message back):\n\n" + final
	if r := []rune(text); len(r) > codeChatMessageMaxRune {
		text = string(r[:codeChatMessageMaxRune-1]) + "…"
	}
	return text
}

func (api *StreamingAPI) relayTabFinalAnswer(ctx context.Context, delivered agentConversation, m agentMessage, source, dest agentMessageEndpoint, final string) {
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return
	}
	current := delivered
	for _, c := range store.Conversations {
		if c.ID == delivered.ID {
			current = c
		}
	}
	// The tab is called by its current name, not the one it had when the conversation started.
	if identities, e := defaultProductConversationRegistryStore().agentIdentities(ctx, dest.UserID, codeproduct.ProfileID, dest.ID); e == nil {
		if identity, ok := identities[dest.ChatKey]; ok && strings.TrimSpace(identity.Name) != "" {
			dest.Label = strings.TrimSpace(identity.Name)
		}
	}
	text := tabFinalAnswerText(current, m, source, dest, final)
	if text == "" {
		return
	}
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: dest.Kind, ID: dest.ID, ProfileID: dest.Profile}, Label: dest.Label, Path: dest.Path, Chat: &codeChat{Key: dest.ChatKey, SessionID: dest.Session, Name: dest.Label}}
	if _, err := api.sendAgentMessage(internalBotRequestContext(context.WithoutCancel(ctx), dest.UserID), dest.UserID, caller, triggerTarget{}, text, delivered.ID, tabFinalAnswerPrefix+m.ID); err != nil {
		log.Printf("[AGENT_MSG] final answer of %s to message %s was not relayed: %v", dest.Label, m.ID, err)
		return
	}
	log.Printf("[AGENT_MSG] relayed the final answer of %s to the asker of message %s (it sent no message back)", dest.Label, m.ID)
}
