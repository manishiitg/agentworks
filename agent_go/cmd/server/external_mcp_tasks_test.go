package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
)

func postTaskRPC(t *testing.T, endpoint, method string, params map[string]any) map[string]any {
	t.Helper()
	params["_meta"] = map[string]any{mcp.MetaKeyProtocolVersion: mcp.ProtocolVersion20260728, mcp.MetaKeyClientCapabilities: map[string]any{
		"elicitation": map[string]any{"form": map[string]any{}}, "extensions": map[string]any{agentworksclient.MCPTasksExtension: map[string]any{}},
	}}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var message map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestExternalMCPTaskLifecycleAndCurrentAccess(t *testing.T) {
	tokenTestSetup(t)
	f, _ := newExternalCrewCommandFixture(t)
	call := &crewFunctionCall{ID: "task-function", UserID: "owner", CallerKind: triggerCallerUser, TargetKind: triggerCallerCrew, TargetProfileID: "work", TargetID: "team", TargetPath: externalCommandCrewRoot, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	questions := virtualtools.GetHumanFeedbackStore()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, call.ID)
		crewFunctionCalls.Unlock()
		questions.WithdrawOperation(call.ID)
	})
	if err := questions.CreatePendingRequest("task-question", "Which branch?", "", "task-chat", []string{"main", "release"}, false, time.Minute, call.ID); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "task-connection", Scopes: []string{"crews:run"}, AllCrews: true}}
	srv := serveExternalMCP(t, f.api, claims)
	endpoint := srv.URL + externalMCPPath
	first := postTaskRPC(t, endpoint, "tools/call", map[string]any{"name": "call_tool", "arguments": map[string]any{"name": "functions", "arguments": map[string]any{"action": "status", "crew_id": "team", "call_id": call.ID}}})
	result, ok := first["result"].(map[string]any)
	if !ok || result["resultType"] != "task" {
		t.Fatalf("task creation: %v", first)
	}
	id := result["taskId"].(string)
	// Each request constructs a fresh SDK server; the handle must survive it.
	question := postTaskRPC(t, endpoint, "tasks/get", map[string]any{"taskId": id})["result"].(map[string]any)
	if question["status"] != "input_required" || question["inputRequests"] == nil {
		t.Fatalf("pending input: %v", question)
	}
	foreign := serveExternalMCP(t, f.api, &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{ID: "task-connection", Scopes: []string{"crews:run"}, AllCrews: true}})
	if postTaskRPC(t, foreign.URL+externalMCPPath, "tasks/get", map[string]any{"taskId": id})["error"] == nil {
		t.Fatal("another person read the task")
	}
	otherConnection := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{ID: "another-connection", Scopes: []string{"crews:run"}, AllCrews: true}})
	if postTaskRPC(t, otherConnection.URL+externalMCPPath, "tasks/cancel", map[string]any{"taskId": id})["error"] == nil {
		t.Fatal("another connection controlled the task")
	}
	answered := postTaskRPC(t, endpoint, "tasks/update", map[string]any{"taskId": id, "inputResponses": map[string]any{"task-question": map[string]any{"action": "accept", "content": map[string]any{"response": "main"}}}})
	if answered["error"] != nil {
		t.Fatalf("answer: %v", answered)
	}
	call.finish("completed", map[string]any{"answer": "main"}, "")
	done := postTaskRPC(t, endpoint, "tasks/get", map[string]any{"taskId": id})["result"].(map[string]any)
	if done["status"] != "completed" || done["result"] == nil {
		t.Fatalf("completion: %v", done)
	}
	claims.AccessToken.Scopes = nil
	if postTaskRPC(t, endpoint, "tasks/get", map[string]any{"taskId": id})["error"] == nil {
		t.Fatal("revoked scope retained access to a completed result")
	}
}

func TestMCPURLSignInRejectsWrongPersonAndBindsCallback(t *testing.T) {
	tokenTestSetup(t)
	store, err := openMCPURLSignIns()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := "00000000-0000-4000-8000-000000000001"
	_, err = store.db.Exec(`INSERT INTO external_mcp_url_signins(id,user_id,connection_id,target,args_hash,account_id,scope,auth_url,oauth_state,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, "owner", "connection", "workflow_google_account", "hash", "google-account", `{}`, "https://accounts.example.com/authorize?state=private-state", mcpURLHash([]byte("private-state")), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	r := httptest.NewRequest(http.MethodPost, "/api/mcp/elicitation/"+id+"/open", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: "someone-else"}))
	r = mux.SetURLVars(r, map[string]string{"request_id": id})
	w := httptest.NewRecorder()
	api.handleMCPURLSignInOpen(w, r)
	if w.Code != 404 || bytes.Contains(w.Body.Bytes(), []byte("accounts.example.com")) {
		t.Fatalf("wrong person: %d %s", w.Code, w.Body.String())
	}
	row, err := loadMCPURLSignIn(store, id)
	if err != nil || !row.Consumed || row.Opened {
		t.Fatal("failed account match did not consume the link")
	}
	completeMCPURLSignIn("another-state", "google-account")
	row, _ = loadMCPURLSignIn(store, id)
	if row.Complete {
		t.Fatal("another callback completed this flow")
	}
	completeMCPURLSignIn("private-state", "another-account")
	row, _ = loadMCPURLSignIn(store, id)
	if row.Complete {
		t.Fatal("another account completed this flow")
	}
	completeMCPURLSignIn("private-state", "google-account")
	row, _ = loadMCPURLSignIn(store, id)
	if row.Complete {
		t.Fatal("a refused browser request completed the sign-in")
	}
}
