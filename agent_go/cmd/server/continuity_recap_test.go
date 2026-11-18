package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3140: a new agent got only the chat's text, so after a switch it did not
// know which command failed or which files had changed. The recap carries
// commands with the end of their output, errors and edited files, keeps the
// first request when the budget cuts older activity, and never repeats an
// earlier recap.
func TestContinuityRecapCarriesCommandsErrorsAndChangedFiles(t *testing.T) {
	call := func(id, name, args string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: id, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: name, Arguments: args}}}}
	}
	result := func(id, content string, isError bool) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: id, Content: content, IsError: isError}}}
	}
	text := func(role llmtypes.ChatMessageType, s string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: s}}}
	}
	history := []llmtypes.MessageContent{
		text(llmtypes.ChatMessageTypeHuman, "fix the login bug"),
	}
	for i := 0; i < 60; i++ { // older activity that overflows the budget
		history = append(history, text(llmtypes.ChatMessageTypeAI, strings.Repeat("older work ", 80)))
	}
	history = append(history,
		call("1", "Edit", `{"file_path":"src/login.ts","old_string":"secret-old","new_string":"secret-new"}`),
		result("1", "ok", false),
		call("2", "apply_patch", `{"input":"*** Begin Patch\n*** Update File: src/auth.ts\n@@\n-a\n+b\n*** End Patch"}`),
		call("3", "Bash", `{"command":"npm test"}`),
		result("3", strings.Repeat("noise ", 200)+"FAIL login.test.ts expected 200 got 401", true),
		text(llmtypes.ChatMessageTypeHuman, continuityNoticeOpen+"\nold recap\n"+continuityNoticeClose+sessionModeSplit+"ok try another approach"),
	)
	lines, files, stats := continuityRecap(history)
	recap := strings.Join(lines, "\n")
	for _, want := range []string{"first request in this chat: fix the login bug", "did: edit src/login.ts", "did: edited src/auth.ts", "did: ran npm test", "error: ", "expected 200 got 401", "user: ok try another approach"} {
		if !strings.Contains(recap, want) {
			t.Fatalf("recap misses %q:\n%s", want, recap)
		}
	}
	for _, unwanted := range []string{"secret-new", "old recap"} {
		if strings.Contains(recap, unwanted) {
			t.Fatalf("recap repeats %q", unwanted)
		}
	}
	if strings.Join(files, ",") != "src/auth.ts,src/login.ts" || stats.ToolErrors != 1 || !stats.FirstAsk || stats.Bytes > continuityRecapBytes+2*continuityTextBytes {
		t.Fatalf("files=%v stats=%+v", files, stats)
	}
}
