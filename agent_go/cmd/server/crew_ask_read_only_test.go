package server

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// 856-59: a guest's free-text ask only reads. The reader deny list keeps the generic write tools off (the managed DB
// session is read-only for every reader turn: ConfigureManagedWorkflowDBSession(..., !crewReadOnlyTurn)).
func TestGuestAskIsReadOnly(t *testing.T) {
	denied := crewReaderDeniedTools()
	for _, tool := range []string{"diff_patch_workspace_file", "image_gen", "image_edit", "create_project_schedule", "update_project_skill_selection", "set_workflow_secret"} {
		if !slices.Contains(denied, tool) {
			t.Errorf("%s must be refused to a guest's ask", tool)
		}
	}
	notice := crewSessionModeNotice("Crew/c-1", "", true)
	if strings.Contains(notice, "only place you can write") || !strings.Contains(notice, "Change nothing") {
		t.Fatalf("a read-only ask must be told to change nothing:\n%s", notice)
	}
}

// The tool description every MCP client reads states the final model.
func TestAskCrewDescriptionStatesTheModel(t *testing.T) {
	if !strings.Contains(crewAskModelSentence, "read-only") || !strings.Contains(crewAskModelSentence, "call one of its functions") {
		t.Fatal(crewAskModelSentence)
	}
	src, err := os.ReadFile("external_tools.go")
	if err != nil || !strings.Contains(string(src), `add("ask_crew", "Send a conversational message to a Crew. "+crewAskModelSentence`) {
		t.Fatal("ask_crew description does not carry the sentence")
	}
}

// A guest's ask gets no outward tools and no MCP bridge (the probe found Slack, Google and Vault tools reachable).
func TestGuestAskRefusesOutwardAndMCPTools(t *testing.T) {
	const sid = "guest-ask-session"
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })
	common.SetSessionCrewReader(sid, true)
	if msg := readerDeniedToolRefusal(ctx, "send_slack_message"); msg != "" {
		t.Fatalf("a function guest turn keeps its outward tools: %q", msg)
	}
	common.SetSessionCrewAskReadOnly(sid, true)
	for _, tool := range crewAskDeniedTools() {
		if msg := readerDeniedToolRefusal(ctx, tool); msg == "" {
			t.Errorf("%s must be refused in a read-only ask", tool)
		}
	}
	if cfg := common.GetSessionShellConfig(sid); cfg == nil || !cfg.CrewAskReadOnly {
		t.Fatal("ask flag not recorded for the MCP bridge")
	}
}
