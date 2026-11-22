package server

import (
	"fmt"
	"log"
)

// Provenance comes only from the admitted stored endpoint. Message arguments,
// model prose and client flags never choose who answered.
func agentAnswerKind(source agentMessageEndpoint) string {
	if source.PublishedAgent != "" {
		if home, err := readCrewTeam(source.ID); err == nil && home.RosterSource != "" && source.PublishedAgent == crewOwnerMainChatKey(home.ProjectID) {
			return "team_answer"
		}
	}
	return "chat"
}

func agentAnswerLine(kind, label string) string {
	label = firstNonEmptyTrimmed(cleanAgentLabel(label), "the agent")
	switch kind {
	case "team_answer":
		return "Answered by the Team agent."
	case "chat":
		return fmt.Sprintf("Answered by %s's chat.", label)
	}
	return ""
}

// Called under the mailbox mutex before saving a real reply. Status notices
// (offline, still working, ping) do not pass through this function.
func stampAgentAnswer(store agentMessageStore, c *agentConversation, m *agentMessage) {
	source := c.Endpoints[m.From]
	if source.PublishedAgent == "" && publishedConversation(*c) {
		return
	}
	question := -1
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].From != m.From && !serverAuthoredKind(c.Messages[i].Kind) {
			question = i
			break
		}
	}
	if question < 0 {
		return
	}
	kind := agentAnswerKind(source)
	if kind == "chat" && !agentAnswerOnly(source) {
		return
	}
	label := store.endpointName(source, c.Endpoints[1-m.From].UserID)
	line := agentAnswerLine(kind, label)
	if kind == "chat" && agentAnswerOnly(source) {
		line = fmt.Sprintf("Answered by %s (answers only).", firstNonEmptyTrimmed(cleanAgentLabel(label), "the agent"))
	}
	m.AnsweredBy, m.AnsweredByLine = kind, line
	m.Text = line + "\n\n" + m.Text
	c.Messages[question].AnsweredBy, c.Messages[question].AnsweredByLine = kind, line
}

func logAgentAnswer(c agentConversation, m agentMessage) {
	if m.AnsweredBy == "" {
		return
	}
	source := c.Endpoints[m.From]
	reason := "source_chat"
	if m.AnsweredBy == "team_answer" {
		reason = "team_agent"
	}
	agent := firstNonEmptyTrimmed(source.TeamAgentID, source.NamedAgentID, source.ID)
	log.Printf("[AGENT-MESSAGE] answered_by=%s agent=%s reason=%s inbox=%s message=%s", m.AnsweredBy, agent, reason, c.ID, m.ID)
}

// A separate Team ask thread is not the maker's main chat. Name the current
// saved Answers-only rule, but never infer it from absent/default metadata.
func agentAnswerOnly(source agentMessageEndpoint) bool {
	if source.TeamAgentID == "" || source.ID == "" {
		return false
	}
	team, err := readCrewTeam(source.ID)
	if err != nil {
		return false
	}
	row, ok := team.Agents[source.TeamAgentID]
	return ok && row.OwnerID == source.UserID && !row.Rule.CanMakeChanges
}
