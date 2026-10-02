package agentworksclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type functionCaller struct {
	answer string
	calls  []string
}

type acceptingElicitation struct{}

func (acceptingElicitation) Elicit(_ context.Context, _ mcp.ElicitationRequest) (*mcp.ElicitationResult, error) {
	return &mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
		Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"response": "main"},
	}}, nil
}

func (f *functionCaller) Tools(context.Context) ([]Tool, error) {
	tools := []Tool{}
	for _, name := range []string{"get_crew_function_call", "reply_crew_function_call"} {
		tools = append(tools, Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return tools, nil
}

func (f *functionCaller) Call(_ context.Context, name string, args map[string]any) (json.RawMessage, error) {
	f.calls = append(f.calls, name)
	if name == "reply_crew_function_call" {
		f.answer, _ = args["response"].(string)
		return json.RawMessage(`{"status":"submitted"}`), nil
	}
	if f.answer == "" {
		return json.RawMessage(`{"call_id":"fn-stdio","status":"running","pending_inputs":[{"request_id":"stdio-question","message":"Which branch?","options":["main","release"],"allow_feedback":false}]}`), nil
	}
	return json.RawMessage(`{"call_id":"fn-stdio","status":"running","pending_inputs":[]}`), nil
}

func TestStdioMCPFunctionElicitationAndPollingFallback(t *testing.T) {
	caller := &functionCaller{}
	bridge, err := NewMCPServer(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	tool := bridge.GetTool("get_crew_function_call")
	args := map[string]any{"call_id": "fn-stdio"}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "get_crew_function_call", Arguments: args}}
	noCapability := server.WithRequestProtocolInfo(context.Background(), &server.RequestProtocolInfo{
		Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: &mcp.ClientCapabilities{},
	})
	plain, err := tool.Handler(noCapability, request)
	if err != nil || plain.NeedsInput() {
		t.Fatalf("polling fallback = %+v, %v", plain, err)
	}
	withCapability := server.WithRequestProtocolInfo(context.Background(), &server.RequestProtocolInfo{
		Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728,
		ClientCapabilities: &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{}},
	})
	form, err := tool.Handler(withCapability, request)
	if err != nil || !form.NeedsInput() {
		t.Fatalf("elicitation = %+v, %v", form, err)
	}
	if form.InputRequests["stdio-question"].Method != mcp.MethodElicitationCreate {
		t.Fatalf("wrong input request: %+v", form.InputRequests)
	}
	request.Params.RequestState = form.RequestState
	request.Params.InputResponses = mcp.InputResponses{
		"stdio-question": mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{
			Action: mcp.ElicitationResponseActionAccept, Content: map[string]any{"response": "main"},
		}}),
	}
	answered, err := tool.Handler(withCapability, request)
	if err != nil || answered.IsError || caller.answer != "main" {
		t.Fatalf("answer = %+v, err=%v, caller=%+v", answered, err, caller)
	}
	if len(caller.calls) != 4 || caller.calls[2] != "reply_crew_function_call" || caller.calls[3] != "get_crew_function_call" {
		t.Fatalf("retry started another function instead of replying and polling: %v", caller.calls)
	}
}

func TestStdioMCPLegacyClientElicitationRoundTrip(t *testing.T) {
	caller := &functionCaller{}
	bridge, err := NewMCPServer(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	handler := acceptingElicitation{}
	cli := client.NewClient(transport.NewInProcessTransportWithOptions(bridge, transport.WithElicitationHandler(handler)),
		client.WithElicitationHandler(handler))
	ctx := context.Background()
	if err := cli.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: "2025-11-25", ClientInfo: mcp.Implementation{Name: "legacy-elicitation-test", Version: "1"},
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := cli.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "get_crew_function_call", Arguments: map[string]any{"call_id": "fn-stdio"},
	}})
	if err != nil || result.IsError || result.NeedsInput() || caller.answer != "main" {
		t.Fatalf("legacy elicitation round trip = %+v, err=%v, caller=%+v", result, err, caller)
	}
}

func TestStdioMCPSecretQuestionsRemainPollable(t *testing.T) {
	if !bridgeSecretQuestion("Enter the password") || bridgeSecretQuestion("Choose a branch") {
		t.Fatal("secret-question elicitation guard misclassified a prompt")
	}
}
