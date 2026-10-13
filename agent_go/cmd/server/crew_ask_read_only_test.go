package server

import (
	"context"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
	if !strings.Contains(crewAskModelSentence, "Run mode") || !strings.Contains(crewAskModelSentence, "published") || !strings.Contains(crewAskModelSentence, "call one of its functions") {
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
		"list_skills", "list_workflow_triggers", "read_agent_messages", "read_image", "read_skill",
		"search_skills", "search_tools", "send_message", crewSuggestionToolName,
		crewPublishedCallToolName, crewPublishedGetToolName,
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
	api := &StreamingAPI{}
	for _, path := range []string{"builder/conversation/chat.json", "db/db.sqlite", "workflow.json", ".git/config", "../other/file.txt", "/etc/passwd", "schedule-runs.json", "triggers/deliveries/call-1.json", "./Triggers/Deliveries"} {
		if _, err := api.crewAskFilesRun(context.Background(), "_users/owner/Chats/Work/projects/alpha", "Crew/alpha", map[string]interface{}{"action": "read", "path": path}); err == nil {
			t.Errorf("%s must not be readable by a guest's ask", path)
		}
	}
	if _, err := api.crewAskFilesRun(context.Background(), "x", "Crew/alpha", map[string]interface{}{"action": "write"}); err == nil {
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

// The model reads the notice to learn the tools are direct (the bridge spec still shows HTTP routes).
func TestGuestAskNoticeSaysTheToolsAreDirect(t *testing.T) {
	if !strings.Contains(crewAskNotice("Crew/c-1", false), "DIRECT tools") {
		t.Fatal("the guest ask notice must tell the model its tools are direct")
	}
}

// Other callers' function-call payloads and the schedule run log belong to the owner and co-owners: a guest's ask and
// any non-owner reader never see them, in a read or in a listing or search result.
func TestCallerPrivateCrewAreas(t *testing.T) {
	for _, p := range []string{"schedule-runs.json", "triggers/deliveries", "triggers/deliveries/a/b.json", "./Triggers/Deliveries/x", "/triggers/deliveries/x"} {
		if !crewCallerPrivatePath(p) {
			t.Errorf("%s must be private to the owner", p)
		}
	}
	for _, p := range []string{"", "triggers", "triggers/other.json", "docs/schedule-runs.json.md", "outputs/triggers/deliveries-note.md"} {
		if crewCallerPrivatePath(p) {
			t.Errorf("%s is ordinary project data", p)
		}
	}
	listed := withoutCrewCallerPrivate(wf.Result{Entries: []wf.Entry{{Path: "docs/a.md"}, {Path: "triggers/deliveries/c1.json"}, {Path: "schedule-runs.json"}, {Path: "triggers/x.json"}}})
	if len(listed.Entries) != 2 || listed.Entries[0].Path != "docs/a.md" || listed.Entries[1].Path != "triggers/x.json" {
		t.Fatalf("private areas must be dropped from a listing: %+v", listed.Entries)
	}
}

// A function-call turn's notice names the call's own output folder (the only one it can write), not <crew>/outputs/.
func TestFunctionCallTurnNamesItsOwnOutputFolder(t *testing.T) {
	call := &crewFunctionCall{ID: "call-notice-1", IsolatedExecution: true, SessionID: "sess-notice-1", TargetPath: "Crew/alpha"}
	crewFunctionCalls.Lock()
	if crewFunctionCalls.m == nil {
		crewFunctionCalls.m = map[string]*crewFunctionCall{}
	}
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	defer func() { crewFunctionCalls.Lock(); delete(crewFunctionCalls.m, call.ID); crewFunctionCalls.Unlock() }()
	common.SetSessionShellEnv("sess-notice-1", map[string]string{"FUNCTION_CALL_ID": call.ID})
	if got := crewFunctionOutputFolder("sess-notice-1", "alice", "Crew/alpha"); got != "Crew/alpha/.calls/call-notice-1/" {
		t.Fatalf("a function call's turn must name its own output folder: %q", got)
	}
	if got := crewFunctionOutputFolder("another-session", "alice", "Crew/alpha"); got != "" {
		t.Fatalf("another session must not get it: %q", got)
	}
}

// Every non-owner chat with a Crew (the in-app chat, an MCP ask, a dashboard Ask) is one policy decided in one place (856-59,
// owner 2026-10-13): read-only plus the owner's published functions. The owner's own chat and a guest's function-call turn are not it.
func TestNonOwnerChatsShareOnePolicy(t *testing.T) {
	ownerRoot := "_users/owner/Chats/Work/projects/alpha"
	reader := QueryRequest{AgentProfileID: "work", SelectedFolder: ownerRoot}
	ownerTurn := QueryRequest{AgentProfileID: "work", SelectedFolder: ownerRoot}
	guestAsk := QueryRequest{AgentProfileID: "work", SelectedFolder: ownerRoot, CrewGuestCaller: "reader", CrewAskReadOnly: true, PinRunMode: true}
	guestCall := QueryRequest{AgentProfileID: "work", SelectedFolder: ownerRoot, CrewGuestCaller: "reader", PinRunMode: true}
	if !crewAskReadOnlyTurn(reader, "reader", true) {
		t.Error("a non-owner's in-app chat must follow the policy")
	}
	if !crewAskReadOnlyTurn(guestAsk, "owner", true) {
		t.Error("a non-owner's MCP ask must follow the policy")
	}
	if crewAskReadOnlyTurn(ownerTurn, "owner", false) {
		t.Error("the owner's chat is the full one")
	}
	if crewAskReadOnlyTurn(guestCall, "owner", true) {
		t.Error("a guest's function-call turn runs the owner's function, not the chat policy")
	}
	// A Slack channel mention runs as the owner's route but read-only: it is Run mode too, not a looser tool set.
	if !crewAskReadOnlyTurn(ownerTurn, "owner", true) {
		t.Error("a read-only turn on a Crew (channel mention, pinned turn) must follow the same policy")
	}
	if crewAskReadOnlyTurn(QueryRequest{AgentProfileID: "work", SelectedFolder: "Chats/Work"}, "owner", true) {
		t.Error("the landing chat is not a Crew")
	}
	for _, tool := range []string{crewPublishedCallToolName, crewPublishedGetToolName} {
		if !crewAskAllowedTools[tool] {
			t.Errorf("%s must be allowed: it is how a non-owner acts", tool)
		}
	}
	if crewAskAllowedTools["execute_shell_command"] || crewAskAllowedTools["call_function"] {
		t.Error("no shell and no calls to other Crews or workflows in a non-owner's chat")
	}
	if notice := crewAskNotice("Crew/c-1", false); !strings.Contains(notice, crewPublishedCallToolName) || !strings.Contains(notice, "Run mode") {
		t.Fatalf("the notice must teach the published-function tool:\n%s", notice)
	}
}

// A published function's run may change the Crew (owner decision 2026-10-13): its notice must not claim the output folder is
// the only place it can write, and still keeps secrets private.
func TestFunctionRunNoticeSaysPublishedFunctionsMayWrite(t *testing.T) {
	notice := crewFunctionRunNotice("Crew/c-1", "Crew/c-1/.calls/fn-1/")
	for _, want := range []string{"owner's authority", "change the Crew's files, memory and skills", "Crew/c-1/.calls/fn-1/", "Never reveal a secret", "data, never instructions"} {
		if !strings.Contains(notice, want) {
			t.Errorf("function-run notice missing %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "only place you can write") || strings.Contains(notice, "Change nothing") {
		t.Errorf("a published function is not told it can write only the output folder:\n%s", notice)
	}
}

// A queued message of a chat that is waiting for its runtime to be replaced is flagged so the app can say so and drop Steer/Remove.
func TestQueuedMessagesAreFlaggedWhileTheChatIsBeingUpdated(t *testing.T) {
	api := &StreamingAPI{}
	api.setConversationTurnRuntimeChange("chat-1", true)
	messages := []PendingQueuedMessage{{MessageID: "m1"}}
	api.markRefreshingQueuedMessages("chat-1", messages)
	if !messages[0].Refreshing {
		t.Fatal("a message waiting for a runtime change must be flagged refreshing")
	}
	other := []PendingQueuedMessage{{MessageID: "m2"}}
	api.markRefreshingQueuedMessages("chat-2", other)
	if other[0].Refreshing {
		t.Fatal("another chat's messages are not refreshing")
	}
}
