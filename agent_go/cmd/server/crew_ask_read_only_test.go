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

// A guest's ask has exactly this tool surface: a new tool fails here until it is classified (reviewer rule, 856-59).
func TestGuestAskToolSurfaceIsAnExactAllowList(t *testing.T) {
	want := []string{
		"EndConversation", "WebSearch", crewAskFilesToolName, "get_api_spec", "get_workflow_trigger_run", "list_accessible_workflows",
		"list_attached_workflows", "list_functions", "list_mcp_servers", "list_project_schedules", "list_project_triggers",
		"list_skills", "list_work_folders", "list_workflow_triggers", "read_agent_messages", "read_image", "read_skill",
		"search_skills", "search_tools", "send_message", crewSuggestionToolName,
	}
	var got []string
	for name := range crewAskAllowedTools {
		got = append(got, name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("guest ask tool surface changed:\n got %v\nwant %v", got, want)
	}
}

func TestGuestAskRefusesEverythingOutsideTheAllowList(t *testing.T) {
	const sid = "guest-ask-session"
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sid)
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })
	common.SetSessionCrewReader(sid, true)
	if msg := readerDeniedToolRefusal(ctx, "send_slack_message"); msg != "" {
		t.Fatalf("a function guest turn keeps its outward tools: %q", msg)
	}
	common.SetSessionCrewAskReadOnly(sid, true)
	for _, tool := range []string{"execute_shell_command", "send_slack_message", "google_workspace_cli", "agent_browser", "schedule_message_wakeup", "list_secrets", "call_function", "slack"} {
		if msg := readerDeniedToolRefusal(ctx, tool); msg == "" {
			t.Errorf("%s must be refused in a read-only ask", tool)
		}
	}
	for tool := range crewAskAllowedTools {
		if msg := readerDeniedToolRefusal(ctx, tool); msg != "" {
			t.Errorf("%s is allowed in a read-only ask, got %q", tool, msg)
		}
	}
}

// The read-only file tool reaches project files but never the private areas (chats, databases, manifests, hidden folders).
func TestGuestAskFilesToolRefusesPrivateAreas(t *testing.T) {
	for _, path := range []string{"builder/conversation/chat.json", "db/db.sqlite", "workflow.json", ".git/config", "../other/file.txt", "/etc/passwd"} {
		if _, err := crewAskFilesRun(context.Background(), "_users/owner/Chats/Work/projects/alpha", "Crew/alpha", map[string]interface{}{"action": "read", "path": path}); err == nil {
			t.Errorf("%s must not be readable by a guest's ask", path)
		}
	}
	if _, err := crewAskFilesRun(context.Background(), "x", "Crew/alpha", map[string]interface{}{"action": "write"}); err == nil {
		t.Error("the tool has no write action")
	}
}

// Every non-core tool on the allow list is a native bridge tool for a guest's ask (no shell to curl them).
func TestGuestAskNativeBridgeToolsAreTheAllowListMinusCore(t *testing.T) {
	got := crewAskNativeBridgeTools()
	for _, must := range []string{crewAskFilesToolName, "send_message", "read_skill", "list_functions", crewSuggestionToolName, "read_image"} {
		if !slices.Contains(got, must) {
			t.Errorf("%s must be a native bridge tool for a guest's ask: %v", must, got)
		}
	}
	for _, never := range []string{"execute_shell_command", "search_tools", "get_api_spec", "WebSearch", "EndConversation"} {
		if slices.Contains(got, never) {
			t.Errorf("%s is core or built in, not an additional bridge tool", never)
		}
	}
}
