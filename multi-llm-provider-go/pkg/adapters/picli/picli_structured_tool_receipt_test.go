package picli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPiStructuredToolEventsPreserveReceiptDetails(t *testing.T) {
	// Captured shape from Pi 0.84's AgentSessionEvent contract. The end event
	// deliberately does not repeat args, so the adapter must retain them from
	// the matching start event.
	var startEvent piJSONEvent
	if err := json.Unmarshal([]byte(`{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{"command":"printf hello"}}`), &startEvent); err != nil {
		t.Fatal(err)
	}
	start, call := piStructuredToolStartChunk(startEvent)
	if start.Type != llmtypes.StreamChunkTypeToolCallStart {
		t.Fatalf("start type = %q", start.Type)
	}
	if start.ToolName != "bash" || start.ToolCallID != "call-1" {
		t.Fatalf("start identity was not retained: %+v", start)
	}
	if start.ToolArgs != `{"command":"printf hello"}` {
		t.Fatalf("start args = %q", start.ToolArgs)
	}

	var endEvent piJSONEvent
	if err := json.Unmarshal([]byte(`{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":{"stdout":"hello","exit_code":0},"isError":false}`), &endEvent); err != nil {
		t.Fatal(err)
	}
	end := piStructuredToolEndChunk(endEvent, call, 33*time.Millisecond)
	if end.Type != llmtypes.StreamChunkTypeToolCallEnd {
		t.Fatalf("end type = %q", end.Type)
	}
	if end.ToolName != "bash" || end.ToolCallID != "call-1" {
		t.Fatalf("end identity was not retained: %+v", end)
	}
	if end.ToolArgs != start.ToolArgs {
		t.Fatalf("end args = %q, want start args %q", end.ToolArgs, start.ToolArgs)
	}
	if end.ToolResult != `{"stdout":"hello","exit_code":0}` {
		t.Fatalf("end result = %q", end.ToolResult)
	}
	if end.ToolDuration != 33*time.Millisecond {
		t.Fatalf("end duration = %s", end.ToolDuration)
	}
}

func TestPiStructuredToolEndUsesStartNameWhenEndOmitsIt(t *testing.T) {
	end := piStructuredToolEndChunk(
		piJSONEvent{ToolCallID: "call-2", Result: json.RawMessage(`"ok"`)},
		piStructuredToolCall{Name: "mcp", Args: `{"search":"workflow"}`},
		0,
	)
	if end.ToolName != "mcp" || end.ToolArgs != `{"search":"workflow"}` || end.ToolResult != "ok" {
		t.Fatalf("fallback receipt = %+v", end)
	}
}
