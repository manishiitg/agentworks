package agentworksclient

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestWorkspaceElicitationResumesTheExactAskWithoutRepeatingIt(t *testing.T) {
	ctx := server.WithRequestProtocolInfo(context.Background(), &server.RequestProtocolInfo{Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}}}})
	pending := json.RawMessage(`{"workspace_id":"work:project","chat_id":"my-tab","query_id":"query","next_after":4,"can_reply":true,"pending_inputs":[{"request_id":"clarification:question","questions":[{"id":"channel","question":"Choose channels","multi_select":true,"min_selections":1,"max_selections":2,"options":[{"label":"Email"},{"label":"Chat"}]},{"id":"audience","question":"Choose the audience","allow_other":true,"options":[{"label":"Team"}]}]}]}`)
	current := pending
	calls := []string{}
	var answers []map[string]any
	host := FunctionElicitationHost{
		Available: func(tool string) bool { return tool == "workspace" },
		Call: func(_ context.Context, tool string, args map[string]any) (json.RawMessage, *mcp.CallToolResult) {
			action, _ := args["action"].(string)
			calls = append(calls, action)
			if tool != "workspace" || args["workspace_id"] != "work:project" || args["chat_id"] != "my-tab" || args["query_id"] != "query" {
				t.Fatal("retry lost Ask identity", tool, args)
			}
			if action == "reply_input" {
				if args["request_id"] != "clarification:question" {
					t.Fatal("question changed")
				}
				answers = args["answers"].([]map[string]any)
				if _, exists := answers[0]["other_text"]; exists {
					t.Fatal("unanswered custom field violates native optional text schema", answers[0])
				}
				current = json.RawMessage(`{"workspace_id":"work:project","chat_id":"my-tab","query_id":"query","pending_inputs":[]}`)
			}
			return current, nil
		},
		Render: func(raw json.RawMessage) *mcp.CallToolResult { return mcp.NewToolResultStructured(raw, string(raw)) },
	}
	args := map[string]any{"action": "ask", "workspace_id": "work:project", "message": "Help me choose"}
	form := host.MaybeElicit(ctx, "workspace", args, pending)
	if form == nil || !form.NeedsInput() {
		t.Fatal("no question form", form)
	}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "workspace", Arguments: args, MultiRoundTripParams: mcp.MultiRoundTripParams{RequestState: form.RequestState, InputResponses: mcp.InputResponses{
		"clarification:question": mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"q0": []any{"Email", "Chat"}, "q1_other": "Support"}}}),
	}}}}
	changed := map[string]any{"action": "ask", "workspace_id": "work:project", "chat_id": "owner-tab"}
	if result := host.Resume(ctx, "workspace", changed, request); !result.IsError || len(calls) != 0 {
		t.Fatal("changed tab accepted", result, calls)
	}
	declined := request
	declined.Params.InputResponses = mcp.InputResponses{"clarification:question": mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}})}
	if result := host.Resume(ctx, "workspace", args, declined); result.IsError || result.NeedsInput() || len(answers) != 0 {
		t.Fatal("decline answered or reasked", result)
	}
	calls = nil
	if result := host.Resume(ctx, "workspace", args, request); result.IsError || result.NeedsInput() {
		t.Fatal("answer failed", result)
	}
	if strings.Join(calls, ",") != "read_chat,reply_input,read_chat" || len(answers) != 2 || answers[1]["other_text"] != "Support" {
		t.Fatal("Ask restarted or answers changed", calls, answers)
	}
	if result := host.Resume(ctx, "workspace", args, request); !result.IsError {
		t.Fatal("replayed question accepted")
	}
	noForm := server.WithRequestProtocolInfo(context.Background(), &server.RequestProtocolInfo{Modern: true, ClientCapabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}}}})
	if host.MaybeElicit(noForm, "workspace", args, pending) != nil {
		t.Fatal("URL-only client got a form")
	}
	sensitive := json.RawMessage(strings.ReplaceAll(string(pending), "Choose channels", "Enter your password"))
	if host.MaybeElicit(ctx, "workspace", args, sensitive) != nil {
		t.Fatal("secret collected in form")
	}
	if _, ok := MCPTaskFamilyFor("workspace"); ok {
		t.Fatal("reusable Ask became a task")
	}
}
