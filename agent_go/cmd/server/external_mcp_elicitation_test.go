package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func postModernExternalTool(t *testing.T, url, target string, args map[string]any, capable bool, requestState string, responses map[string]any) map[string]any {
	t.Helper()
	capabilities := map[string]any{}
	if capable {
		capabilities["elicitation"] = map[string]any{}
	}
	params := map[string]any{
		"name":      externalMCPToolCall,
		"arguments": map[string]any{"name": target, "arguments": args},
		"_meta": map[string]any{
			mcp.MetaKeyProtocolVersion:    mcp.ProtocolVersion20260728,
			mcp.MetaKeyClientCapabilities: capabilities,
		},
	}
	if requestState != "" {
		params["requestState"] = requestState
		params["inputResponses"] = responses
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(mcp.HeaderProtocolVersion, mcp.ProtocolVersion20260728)
	req.Header.Set(mcp.HeaderMethod, "tools/call")
	req.Header.Set(mcp.HeaderName, externalMCPToolCall)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var message map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP HTTP %d: %v", resp.StatusCode, message)
	}
	if failure, ok := message["error"]; ok {
		t.Fatalf("MCP protocol error: %v", failure)
	}
	result, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing MCP result: %v", message)
	}
	return result
}

func TestExternalMCPFunctionQuestionUsesCapabilityGatedElicitation(t *testing.T) {
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	call := &crewFunctionCall{ID: "fn-mcp-elicit-crew", UserID: "owner", CallerKind: triggerCallerUser,
		TargetKind: triggerCallerCrew, TargetID: "beta", Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	store := virtualtools.GetHumanFeedbackStore()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, call.ID)
		crewFunctionCalls.Unlock()
		store.WithdrawOperation(call.ID)
	})
	if err := store.CreatePendingRequest("mcp-elicit-request", "Which branch?", "", "crew-chat", []string{"main", "release"}, false, time.Minute, call.ID); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}}
	srv := serveExternalMCP(t, env.api, claims)
	url := srv.URL + externalMCPPath
	args := map[string]any{"call_id": call.ID}

	// A modern client without the elicitation capability receives the same
	// pollable pending_inputs contract as before.
	plain := postModernExternalTool(t, url, "get_crew_function_call", args, false, "", nil)
	if plain["resultType"] == "input_required" {
		t.Fatalf("capability-less client was elicited: %v", plain)
	}
	if got := plain["structuredContent"].(map[string]any)["pending_inputs"].([]any); len(got) != 1 {
		t.Fatalf("polling fallback lost pending question: %v", plain)
	}

	first := postModernExternalTool(t, url, "get_crew_function_call", args, true, "", nil)
	if first["resultType"] != "input_required" {
		t.Fatalf("expected input_required, got %v", first)
	}
	state, ok := first["requestState"].(string)
	if !ok || state == "" {
		t.Fatalf("missing request state: %v", first)
	}
	requests := first["inputRequests"].(map[string]any)
	question := requests["mcp-elicit-request"].(map[string]any)
	if question["method"] != "elicitation/create" {
		t.Fatalf("wrong input request: %v", question)
	}
	params := question["params"].(map[string]any)
	answer := params["requestedSchema"].(map[string]any)["properties"].(map[string]any)["response"].(map[string]any)
	if got := answer["enum"].([]any); len(got) != 2 || got[0] != "main" || got[1] != "release" {
		t.Fatalf("choice schema = %v", answer)
	}

	// A different user cannot use the returned state as authority to answer.
	other := serveExternalMCP(t, env.api, &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:run"}, AllCrews: true}})
	responses := map[string]any{"mcp-elicit-request": map[string]any{"action": "accept", "content": map[string]any{"response": "main"}}}
	foreign := postModernExternalTool(t, other.URL+externalMCPPath, "get_crew_function_call", args, true, state, responses)
	if foreign["isError"] != true {
		t.Fatalf("foreign user answered call: %v", foreign)
	}
	changed := postModernExternalTool(t, url, "get_crew_function_call", map[string]any{"call_id": "another-call"}, true, state, responses)
	if changed["isError"] != true {
		t.Fatalf("changed call_id accepted an elicitation answer: %v", changed)
	}
	invalid := postModernExternalTool(t, url, "get_crew_function_call", args, true, state,
		map[string]any{"mcp-elicit-request": map[string]any{"action": "accept", "content": map[string]any{"response": "unknown"}}})
	if invalid["isError"] != true {
		t.Fatalf("invalid choice was accepted: %v", invalid)
	}
	declined := postModernExternalTool(t, url, "get_crew_function_call", args, true, state,
		map[string]any{"mcp-elicit-request": map[string]any{"action": "decline"}})
	if declined["isError"] == true || declined["resultType"] == "input_required" {
		t.Fatalf("declining should return a pollable status: %v", declined)
	}

	final := postModernExternalTool(t, url, "get_crew_function_call", args, true, state, responses)
	if final["isError"] == true || final["resultType"] == "input_required" {
		t.Fatalf("answer did not finish elicitation: %v", final)
	}
	if answer, err := store.WaitForResponseCtx(context.Background(), "mcp-elicit-request", time.Second); err != nil || answer != "main" {
		t.Fatalf("waiting Crew got %q, %v", answer, err)
	}
	replay := postModernExternalTool(t, url, "get_crew_function_call", args, true, state, responses)
	if replay["isError"] != true {
		t.Fatalf("duplicate elicitation answer was accepted: %v", replay)
	}
}

func TestExternalMCPWorkflowQuestionArrivesAfterFirstPoll(t *testing.T) {
	f := newExternalToolsFixture(t)
	call := &crewFunctionCall{ID: "fn-mcp-elicit-workflow", UserID: "owner", CallerKind: triggerCallerUser,
		TargetKind: triggerCallerWorkflow, TargetID: "invoices", Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now(), done: make(chan struct{})}
	crewFunctionCalls.Lock()
	crewFunctionCalls.m[call.ID] = call
	crewFunctionCalls.Unlock()
	store := virtualtools.GetHumanFeedbackStore()
	t.Cleanup(func() {
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, call.ID)
		crewFunctionCalls.Unlock()
		store.WithdrawOperation(call.ID)
	})
	srv := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", Username: "owner"})
	url := srv.URL + externalMCPPath
	args := map[string]any{"workflow_id": "invoices", "call_id": call.ID}
	first := postModernExternalTool(t, url, "get_workflow_function_call", args, true, "", nil)
	if first["resultType"] == "input_required" {
		t.Fatalf("question appeared before it existed: %v", first)
	}
	if err := store.CreatePendingRequest("mcp-late-workflow-request", "What should the report say?", "", "workflow-chat", nil, true, time.Minute, call.ID); err != nil {
		t.Fatal(err)
	}
	second := postModernExternalTool(t, url, "get_workflow_function_call", args, true, "", nil)
	if second["resultType"] != "input_required" {
		t.Fatalf("late question was not elicited: %v", second)
	}
	state := second["requestState"].(string)
	responses := map[string]any{"mcp-late-workflow-request": map[string]any{"action": "accept", "content": map[string]any{"response": "Ship the summary"}}}
	answered := postModernExternalTool(t, url, "get_workflow_function_call", args, true, state, responses)
	if answered["isError"] == true {
		t.Fatalf("workflow answer failed: %v", answered)
	}
	if answer, err := store.WaitForResponseCtx(context.Background(), "mcp-late-workflow-request", time.Second); err != nil || answer != "Ship the summary" {
		t.Fatalf("waiting workflow got %q, %v", answer, err)
	}
}

func TestExternalMCPWorkflowReaderDoesNotReceiveElicitation(t *testing.T) {
	ctx := server.WithRequestProtocolInfo(context.Background(), &server.RequestProtocolInfo{
		Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728,
		ClientCapabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{}},
	})
	raw := []byte(`{"call_id":"fn-reader","can_reply":false,"pending_inputs":[{"request_id":"question","message":"Choose a branch","options":["main"],"allow_feedback":false}]}`)
	allowed := map[string]externalTool{"get_workflow_function_call": {}, "reply_workflow_function_call": {}}
	host := (&StreamingAPI{}).externalMCPElicitation(nil, allowed)
	if result := host.MaybeElicit(ctx, "get_workflow_function_call", map[string]any{"workflow_id": "invoices"}, raw); result != nil {
		t.Fatalf("reader received an unanswerable elicitation: %+v", result)
	}
}
