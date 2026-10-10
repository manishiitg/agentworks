package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A call from someone other than the Crew's owner runs as their guest.
func TestCrewGuestCallerOnlyForNonOwners(t *testing.T) {
	if got := crewGuestCaller("bob", "alice"); got != "bob" {
		t.Fatalf("non-owner caller = %q", got)
	}
	if got := crewGuestCaller("alice", "alice"); got != "" {
		t.Fatalf("the owner's own call must not be a guest: %q", got)
	}
	reqMap := map[string]interface{}{}
	applyCrewGuestCaller(reqMap, "bob")
	if reqMap["pin_run_mode"] != true || reqMap["crew_guest_caller"] != "bob" {
		t.Fatalf("guest request = %v", reqMap)
	}
	owner := map[string]interface{}{}
	applyCrewGuestCaller(owner, "")
	if len(owner) != 0 {
		t.Fatalf("owner request must be untouched: %v", owner)
	}
}

// The guest field is honoured only on the owner's own Crew turn, where it can
// only narrow access; a reader's turn and non-Crew turns ignore it.
func TestCrewGuestCallerForTurnOnlyNarrowsTheOwnersTurn(t *testing.T) {
	folder := "_users/alice/Chats/Work/projects/demo"
	req := QueryRequest{AgentProfileID: "work", SelectedFolder: folder, CrewGuestCaller: "bob"}
	if got := crewGuestCallerForTurn(req, "alice"); got != "bob" {
		t.Fatalf("owner turn working for bob = %q", got)
	}
	if got := crewGuestCallerForTurn(req, "bob"); got != "" {
		t.Fatalf("a reader's own turn must ignore the field: %q", got)
	}
	self := req
	self.CrewGuestCaller = "alice"
	if got := crewGuestCallerForTurn(self, "alice"); got != "" {
		t.Fatalf("naming the owner is not a guest: %q", got)
	}
	// An owner's call in Run mode (run_mode) is their own guest: the turn must get a guest's tools to answer the call.
	pinned := self
	pinned.PinRunMode = true
	if got := crewGuestCallerForTurn(pinned, "alice"); got != "alice" {
		t.Fatalf("an owner's pinned run-mode turn is not treated as a guest: %q", got)
	}
	workflow := QueryRequest{SelectedFolder: "Workflow/x", CrewGuestCaller: "bob"}
	if got := crewGuestCallerForTurn(workflow, "alice"); got != "" {
		t.Fatalf("non-Crew turn = %q", got)
	}
	if prompt := crewSessionModeNotice("_users/alice/Chats/Work/projects/x", "", false); !strings.Contains(prompt, crewSuggestionToolName) || !strings.Contains(prompt, "Change nothing") {
		t.Fatalf("reader prompt used for a guest: %s", prompt)
	}
}

// A guest turn gets only the tools to answer its call.
func TestCrewFunctionResultOnlyRegistrar(t *testing.T) {
	inner := &recordingRegistrar{}
	reg := crewFunctionResultOnlyRegistrar{inner}
	exec := func(context.Context, map[string]interface{}) (string, error) { return "", nil }
	for _, name := range []string{"call_function", "define_function", "reply_function_call", "report_function_progress", "return_function_result", "list_functions"} {
		_ = reg.RegisterCustomTool(name, "", nil, exec, "")
		_ = reg.RegisterCustomToolWithTimeout(name, "", nil, exec, time.Second, "")
	}
	if len(inner.tools) != 2 || inner.tools["return_function_result"].exec == nil || inner.tools["report_function_progress"].exec == nil {
		t.Fatalf("registered = %v", inner.tools)
	}
}

// On a guest turn the suggestion is from the guest, even though the turn
// runs as the owner.
func TestCrewSuggestionOnAGuestTurnIsFromTheGuest(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["work"]},{"id":"guest","username":"guest","role":"editor","products":["work"]}]}`)
	server, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const crewRoot = "_users/owner/Chats/Work/projects/demo"
	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerCrewSuggestionTool(reg, "guest", "call-session", crewRoot); err != nil {
		t.Fatal(err)
	}
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if _, err := reg.tools[crewSuggestionToolName].exec(ownerCtx, map[string]interface{}{"suggestion": "Add a weekly digest"}); err != nil {
		t.Fatal(err)
	}
	inputs, err := listReportHumanInputs(ownerCtx, crewRoot, "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 || inputs[0].CreatedBy != "guest" {
		t.Fatalf("suggestion = %+v %v", inputs, err)
	}
}
