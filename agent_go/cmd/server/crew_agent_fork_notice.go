package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// crewAgentForkRefusal is a plain-words reason the person can act on (for example "accept this copy on your
// computer"). Every other error is shown as a generic line, so no path or raw error ever reaches the screen.
type crewAgentForkRefusal string

func (r crewAgentForkRefusal) Error() string { return string(r) }

func crewAgentForkRefuse(message string) error { return crewAgentForkRefusal(message) }

// crewAgentForkFailureReason names why a fork step failed for the log: our own refusal text, or a class with no path.
func crewAgentForkFailureReason(err error) string {
	var refusal crewAgentForkRefusal
	if errors.As(err, &refusal) {
		return string(refusal)
	}
	return crewSnapshotFailureReason(err)
}

// forkCopyFirstTurnNote is what the copy of a teammate's agent reads on its first turn: the conversation above is the
// maker's, and this agent now works for whoever accepted it.
func forkCopyFirstTurnNote(agentName, maker string) string {
	return fmt.Sprintf("[This chat is a copy of the agent %q, made by %s and shared with you. The conversation above is theirs: those replies were written by their agent, not by you. You now work for the person who accepted this copy. No files, memory, skills or logins came with it. Do not carry on its unfinished work unless the person asks you to.]", agentName, maker)
}

// helperFirstTurnNote is what a fresh helper agent reads on its first turn. It has no copy of the opener's
// conversation: the task it is sent is all it knows.
// ownAgentFirstTurnNote is helperFirstTurnNote for a new agent opened as its own (place=beside or a group, owner
// 25 Nov): it works with the person directly and does not report back to its opener.
func ownAgentFirstTurnNote(agentName, openerName string) string {
	return fmt.Sprintf("[This chat is %q, its own agent, opened by %q. You start without that agent's conversation: the task you are sent has everything you need. You are not %q. Work with the person here; do not report to %q or send it updates unless the person asks.]", agentName, openerName, openerName, openerName)
}

func helperFirstTurnNote(helperName, openerName string) string {
	return fmt.Sprintf("[This chat is %q, a helper agent opened by %q to do part of its work. You start without that agent's conversation: the task you are sent has everything you need. You are not %q. When you finish, report back in the same AgentBridge thread with one short message: what you did and where the results are.]", helperName, openerName, openerName)
}

// noteForkCopyAnswered puts a divider line in the maker's chat when a teammate accepts or declines a copy of it.
// The maker's chat may not be on this server (a Mac chat); then there is nothing to write and the log says so.
func (api *StreamingAPI) noteForkCopyAnswered(ctx context.Context, offer crewAgentFork, answer string) {
	if api.eventStore == nil {
		return
	}
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, offer.Sender, crewProfileID, offer.Project)
	if err != nil {
		log.Printf("[AGENT_FORK] project=%s fork=%s event=maker_notice_skipped reason=chats_unavailable", offer.Project, offer.ID)
		return
	}
	for _, record := range records {
		if record.ConversationKey != offer.Agent.ChatKey || record.SessionID == "" {
			continue
		}
		fields := map[string]interface{}{"person": crewOwnerDisplayName(offer.Recipient), "answer": answer, "agent_name": offer.Agent.Name}
		event := agentevents.NewAgentEvent(storeevents.NewGenericEventData("chat_fork_created", fields))
		event.SessionID = record.SessionID
		api.eventStore.SetSessionOwner(record.SessionID, offer.Sender)
		if err := api.eventStore.SetSessionPersistenceClass(record.SessionID, storeevents.SessionPersistenceInteractiveChat); err != nil {
			log.Printf("[AGENT_FORK] project=%s fork=%s event=maker_notice_skipped reason=journal_unavailable", offer.Project, offer.ID)
			return
		}
		if err := api.eventStore.AddEventChecked(record.SessionID, storeevents.Event{ID: "fork-answer:" + uuid.NewString(), Type: "chat_fork_created", Timestamp: time.Now(), SessionID: record.SessionID, Data: event}); err != nil {
			log.Printf("[AGENT_FORK] project=%s fork=%s event=maker_notice_failed", offer.Project, offer.ID)
		}
		return
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s event=maker_notice_skipped reason=chat_not_on_this_server", offer.Project, offer.ID)
}
