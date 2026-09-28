package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrewSuggestionFromAnotherUserReachesOnlyTheOwner(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["work"]},{"id":"user","username":"user","role":"editor","products":["work"]},{"id":"other","username":"other","role":"editor","products":["work"]}]}`)
	server, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const crewRoot = "_users/owner/Chats/Work/projects/demo"
	ctxFor := func(id string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: id})
	}

	api := &StreamingAPI{}
	reg := &recordingRegistrar{}
	if err := api.registerCrewSuggestionTool(reg, "user", "user-chat", crewRoot); err != nil {
		t.Fatal(err)
	}
	tool := reg.tools[crewSuggestionToolName]
	if _, err := tool.exec(ctxFor("owner"), map[string]interface{}{"suggestion": "x"}); err == nil {
		t.Fatal("the owner should change the Crew directly, not suggest")
	}
	if _, err := tool.exec(ctxFor("user"), map[string]interface{}{"suggestion": " "}); err == nil {
		t.Fatal("empty suggestion accepted")
	}
	if _, err := tool.exec(ctxFor("user"), map[string]interface{}{"suggestion": "Also post the summary to #qa", "reason": "the team reads it there", "about": "daily_report"}); err != nil {
		t.Fatal(err)
	}

	// Stored once at the Crew's root, whichever way the owner addresses it.
	inputs, err := listReportHumanInputs(ctxFor("owner"), "Chats/Work/projects/demo", "pending", "user_suggestion")
	if err != nil || len(inputs) != 1 {
		t.Fatalf("owner's view: %v %v", inputs, err)
	}
	input := inputs[0]
	if input.CreatedBy != "user" || input.ApplyContract.Mode != "no_change" || input.Evidence != "daily_report" {
		t.Fatalf("unsafe suggestion: %+v", input)
	}

	// Only the owner reads and answers a Crew's decisions.
	if read, _ := reportHumanInputAccess(ctxFor("other"), crewRoot); read {
		t.Fatal("another user can read the Crew's suggestions")
	}
	if read, _ := reportHumanInputAccess(ctxFor("user"), crewRoot); read {
		t.Fatal("the suggesting user can read every suggestion of the Crew")
	}
	if read, write := reportHumanInputAccess(ctxFor("owner"), crewRoot); !read || !write {
		t.Fatal("the owner must read and answer")
	}
	answer := ReportHumanInputAnswerRequest{SelectedOptionID: "approve", AnsweredBy: "user", AnsweredByKind: "human_ui"}
	if _, err := answerReportHumanInput(ctxFor("user"), crewRoot, input.ID, answer); err == nil {
		t.Fatal("the suggesting user approved their own suggestion")
	}
	answer.AnsweredBy = "owner"
	if approved, err := answerReportHumanInput(ctxFor("owner"), crewRoot, input.ID, answer); err != nil || approved.Status != "answered" {
		t.Fatalf("owner review: %+v %v", approved, err)
	}
}

// The decisions routes check the workspace they name: a workflow reader
// lists but cannot answer; a user without access gets nothing.
func TestReportHumanInputRoutesCheckWorkspaceAccess(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor"},{"id":"reader","username":"reader","role":"editor"},{"id":"stranger","username":"stranger","role":"editor"}]}`)
	server, docs := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	docs.files["Workflow/test/workflow.json"] = `{"id":"wf_test","created_by":"owner","access":{"owners":["owner"],"readers":["reader"]}}`
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	call := func(user string, write bool, method, target string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(`{"workspace_path":"Workflow/test"}`))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		requireReportHumanInputAccess(write, ok)(w, req)
		return w.Code
	}
	list := "/api/report-human-inputs?workspace_path=Workflow/test"
	if got := call("reader", false, http.MethodGet, list); got != http.StatusOK {
		t.Fatalf("reader list = %d", got)
	}
	if got := call("reader", true, http.MethodPost, "/api/report-human-inputs/x/answer"); got != http.StatusForbidden {
		t.Fatalf("reader answer = %d", got)
	}
	if got := call("stranger", false, http.MethodGet, list); got != http.StatusForbidden {
		t.Fatalf("stranger list = %d", got)
	}
	if got := call("owner", true, http.MethodPost, "/api/report-human-inputs/x/answer"); got != http.StatusOK {
		t.Fatalf("owner answer = %d", got)
	}
}
