package testcontracts

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAssertToolReceiptContractAcceptsCompleteReceipt(t *testing.T) {
	AssertToolReceiptContract(t, ToolReceiptCase{
		Provider:         "test-cli",
		ArgumentSentinel: "ARG_TOKEN",
		ResultSentinel:   "RESULT_TOKEN",
		FinalAnswer:      "RESULT_TOKEN",
		Chunks: []llmtypes.StreamChunk{
			{Type: llmtypes.StreamChunkTypeToolCallStart, ToolCallID: "c1", ToolName: "echo", ToolArgs: `{"token":"ARG_TOKEN"}`},
			{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: "c1", ToolName: "echo", ToolArgs: `{"token":"ARG_TOKEN"}`, ToolResult: "RESULT_TOKEN", ToolDuration: time.Millisecond},
		},
	})
}

func TestValidateToolReceiptContractRejectsConsumerVisibleLosses(t *testing.T) {
	valid := ToolReceiptCase{
		Provider:         "test-cli",
		ArgumentSentinel: "ARG_TOKEN",
		ResultSentinel:   "RESULT_TOKEN",
		FinalAnswer:      "RESULT_TOKEN",
		Chunks: []llmtypes.StreamChunk{
			{Type: llmtypes.StreamChunkTypeToolCallStart, ToolCallID: "c1", ToolName: "echo", ToolArgs: `{"token":"ARG_TOKEN"}`},
			{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: "c1", ToolName: "echo", ToolArgs: `{"token":"ARG_TOKEN"}`, ToolResult: "RESULT_TOKEN", ToolDuration: time.Millisecond},
		},
	}
	tests := []struct {
		name string
		edit func(*ToolReceiptCase)
		want string
	}{
		{name: "unknown name", edit: func(c *ToolReceiptCase) { c.Chunks[0].ToolName = "unknown" }, want: "unusable name"},
		{name: "missing args", edit: func(c *ToolReceiptCase) { c.Chunks[1].ToolArgs = "" }, want: "lost its arguments"},
		{name: "missing result", edit: func(c *ToolReceiptCase) { c.Chunks[1].ToolResult = "" }, want: "lost its output"},
		{name: "mismatched id", edit: func(c *ToolReceiptCase) { c.Chunks[1].ToolCallID = "c2" }, want: "no matching end"},
		{name: "duplicate final", edit: func(c *ToolReceiptCase) { c.FinalAnswer = "RESULT_TOKEN RESULT_TOKEN" }, want: "exactly once"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			candidate.Chunks = append([]llmtypes.StreamChunk(nil), valid.Chunks...)
			tc.edit(&candidate)
			err := ValidateToolReceiptContract(candidate)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
