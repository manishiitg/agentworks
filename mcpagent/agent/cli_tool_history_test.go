package mcpagent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCLIToolCallChunksRoundTrip(t *testing.T) {
	chunks := []llmtypes.StreamChunk{
		{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     "bash",
			ToolCallID:   "call_1",
			ToolArgs:     `{"command":"ls"}`,
			ToolResult:   "file1.txt\nfile2.txt",
			ToolDuration: 250 * time.Millisecond,
		},
		{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     "read",
			ToolCallID:   "call_2",
			ToolArgs:     `{"path":"file1.txt"}`,
			ToolResult:   "hello world",
			ToolDuration: 100 * time.Millisecond,
		},
	}

	histJSON, err := json.Marshal(chunks)
	if err != nil {
		t.Fatalf("Marshal CLIToolCalls: %v", err)
	}

	var decoded []llmtypes.StreamChunk
	if err := json.Unmarshal(histJSON, &decoded); err != nil {
		t.Fatalf("Unmarshal cli_tool_call_chunks: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("decoded chunks = %d, want 2", len(decoded))
	}

	var messages []llmtypes.MessageContent
	for _, c := range decoded {
		if c.ToolResult == "" {
			continue
		}
		messages = append(messages, llmtypes.MessageContent{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCall{
				ID:   c.ToolCallID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      c.ToolName,
					Arguments: c.ToolArgs,
				},
			}},
		})
		messages = append(messages, llmtypes.MessageContent{
			Role: llmtypes.ChatMessageTypeTool,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{
				ToolCallID: c.ToolCallID,
				Name:       c.ToolName,
				Content:    c.ToolResult,
			}},
		})
	}

	if len(messages) != 4 {
		t.Fatalf("reconstructed messages = %d, want 4 (2 AI + 2 Tool)", len(messages))
	}

	if messages[0].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("messages[0].Role = %q, want AI", messages[0].Role)
	}
	tc, ok := messages[0].Parts[0].(llmtypes.ToolCall)
	if !ok {
		t.Fatalf("messages[0].Parts[0] type = %T, want ToolCall", messages[0].Parts[0])
	}
	if tc.FunctionCall.Name != "bash" {
		t.Fatalf("ToolCall.FunctionCall.Name = %q, want bash", tc.FunctionCall.Name)
	}
	if tc.FunctionCall.Arguments != `{"command":"ls"}` {
		t.Fatalf("ToolCall.FunctionCall.Arguments = %q", tc.FunctionCall.Arguments)
	}

	if messages[1].Role != llmtypes.ChatMessageTypeTool {
		t.Fatalf("messages[1].Role = %q, want Tool", messages[1].Role)
	}
	tr, ok := messages[1].Parts[0].(llmtypes.ToolCallResponse)
	if !ok {
		t.Fatalf("messages[1].Parts[0] type = %T, want ToolCallResponse", messages[1].Parts[0])
	}
	if tr.Name != "bash" || tr.Content != "file1.txt\nfile2.txt" {
		t.Fatalf("ToolCallResponse = {Name:%q Content:%q}", tr.Name, tr.Content)
	}

	if messages[2].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("messages[2].Role = %q, want AI", messages[2].Role)
	}
	if messages[3].Role != llmtypes.ChatMessageTypeTool {
		t.Fatalf("messages[3].Role = %q, want Tool", messages[3].Role)
	}
}

func TestCLIToolCallChunksSkipsIncomplete(t *testing.T) {
	chunks := []llmtypes.StreamChunk{
		{
			Type:       llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:   "bash",
			ToolCallID: "call_1",
			ToolArgs:   `{"command":"long_running"}`,
			ToolResult: "",
		},
		{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     "bash",
			ToolCallID:   "call_2",
			ToolArgs:     `{"command":"ls"}`,
			ToolResult:   "done",
			ToolDuration: 50 * time.Millisecond,
		},
	}

	histJSON, err := json.Marshal(chunks)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded []llmtypes.StreamChunk
	if err := json.Unmarshal(histJSON, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	var messages []llmtypes.MessageContent
	for _, c := range decoded {
		if c.ToolResult == "" {
			continue
		}
		messages = append(messages, llmtypes.MessageContent{
			Role: llmtypes.ChatMessageTypeAI,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCall{
				ID:   c.ToolCallID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      c.ToolName,
					Arguments: c.ToolArgs,
				},
			}},
		})
		messages = append(messages, llmtypes.MessageContent{
			Role: llmtypes.ChatMessageTypeTool,
			Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{
				ToolCallID: c.ToolCallID,
				Name:       c.ToolName,
				Content:    c.ToolResult,
			}},
		})
	}

	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2 (incomplete tool call should be skipped)", len(messages))
	}
	tc := messages[0].Parts[0].(llmtypes.ToolCall)
	if tc.ID != "call_2" {
		t.Fatalf("surviving tool call ID = %q, want call_2", tc.ID)
	}
}

func TestCLIToolResultIsErrorUsesCanonicalNestedStatus(t *testing.T) {
	failure := `{"content":[{"text":"{\"stdout\":\"ERROR: invalid API token\",\"stderr\":\"\",\"exit_code\":0}"}]}`
	if !cliToolResultIsError("execute_shell_command", failure) {
		t.Fatal("nested CLI tool failure was not classified as an error")
	}
	if cliToolResultIsError("execute_shell_command", `The report discusses ERROR: invalid API token from last week.`) {
		t.Fatal("error discussion was misclassified as a tool failure")
	}
	if cliToolResultIsError("query_workflow_db", `[{"status":"failed","detail":"historical finding"}]`) {
		t.Fatal("workflow DB domain row was misclassified as a tool failure")
	}
}

func TestCLIToolCallChunksAttachedToResponse(t *testing.T) {
	chunks := []llmtypes.StreamChunk{
		{
			Type:         llmtypes.StreamChunkTypeToolCallEnd,
			ToolName:     "bash",
			ToolCallID:   "call_1",
			ToolResult:   "ok",
			ToolDuration: 100 * time.Millisecond,
		},
	}

	resp := &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{Content: "test"},
		},
	}

	choice := resp.Choices[0]
	if choice.GenerationInfo == nil {
		choice.GenerationInfo = &llmtypes.GenerationInfo{}
	}
	if choice.GenerationInfo.Additional == nil {
		choice.GenerationInfo.Additional = make(map[string]interface{})
	}
	if histJSON, err := json.Marshal(chunks); err == nil {
		choice.GenerationInfo.Additional["cli_tool_call_chunks"] = string(histJSON)
	}

	chunksJSON, ok := choice.GenerationInfo.Additional["cli_tool_call_chunks"].(string)
	if !ok || chunksJSON == "" {
		t.Fatal("cli_tool_call_chunks not set in Additional")
	}

	var recovered []llmtypes.StreamChunk
	if err := json.Unmarshal([]byte(chunksJSON), &recovered); err != nil {
		t.Fatalf("Unmarshal from Additional: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("recovered chunks = %d, want 1", len(recovered))
	}
	if recovered[0].ToolName != "bash" {
		t.Fatalf("recovered ToolName = %q, want bash", recovered[0].ToolName)
	}
}
