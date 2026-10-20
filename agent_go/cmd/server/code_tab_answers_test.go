package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// A tab's final answer goes back to the tab that asked, once, and only when the tab sent nothing back itself.
func TestTabFinalAnswerIsRelayedOnceAndOnlyWhenNoReplyWasSent(t *testing.T) {
	asker := agentMessageEndpoint{Profile: workproduct.ProfileID, Session: "s-asker", Label: "main"}
	tab := agentMessageEndpoint{Profile: workproduct.ProfileID, Session: "s-tab", Label: "tests"}
	asked := agentMessage{ID: "msg-1", Sequence: 1, From: 0}
	conversation := agentConversation{Messages: []agentMessage{asked}}
	if text := tabFinalAnswerText(conversation, asked, asker, tab, "TESTS-DONE"); !strings.Contains(text, "TESTS-DONE") || !strings.Contains(text, "msg-1") {
		t.Fatalf("the final answer was not relayed: %q", text)
	}
	replied := agentConversation{Messages: []agentMessage{asked, {ID: "msg-2", Sequence: 2, From: 1}}}
	if tabFinalAnswerText(replied, asked, asker, tab, "TESTS-DONE") != "" {
		t.Fatal("a tab that replied itself had its answer relayed too")
	}
	relayed := agentMessage{ID: "msg-3", Sequence: 3, From: 1, Submission: tabFinalAnswerPrefix + "msg-1"}
	if tabFinalAnswerText(agentConversation{Messages: []agentMessage{relayed}}, relayed, tab, asker, "thanks") != "" {
		t.Fatal("a relayed answer bounced back to the tab")
	}
	if tabFinalAnswerText(conversation, asked, agentMessageEndpoint{Profile: "work", Session: "s-x"}, tab, "x") != "" {
		t.Fatal("an answer was relayed to something that is not a Code tab")
	}
	if tabFinalAnswerText(conversation, asked, asker, tab, "  ") != "" {
		t.Fatal("an empty answer was relayed")
	}
}
