package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3140 (T3 Code handoff): a new agent got cut turns and the tail of command
// output, so after a switch it reran commands or asked what "that failure"
// meant. Items are now whole or left out and listed by their saved line; the
// last request, last reply and first request come first; lines skip the
// system prompt, edits never repeat written content, and an earlier recap is
// not handed over again.
func TestContinuityHandoffKeepsWholeItemsAndListsTheRest(t *testing.T) {
	call := func(id, name, args string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: id, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: name, Arguments: args}}}}
	}
	result := func(id, content string, isError bool) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: id, Content: content, IsError: isError}}}
	}
	text := func(role llmtypes.ChatMessageType, s string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: s}}}
	}
	noise := strings.Repeat("noise line\n", 2000) // longer than one chat_history_read page
	history := []llmtypes.MessageContent{
		text(llmtypes.ChatMessageTypeSystem, "system prompt"),
		text(llmtypes.ChatMessageTypeHuman, "fix the login bug"), // line 1
	}
	for i := 0; i < 40; i++ { // lines 2-41: older work that overflows the budget
		history = append(history, text(llmtypes.ChatMessageTypeAI, strings.Repeat("older work ", 400)))
	}
	history = append(history,
		call("1", "Edit", `{"file_path":"src/login.ts","old_string":"secret-old","new_string":"secret-new"}`), // 42
		result("1", "ok", false),
		call("2", "Bash", `{"command":"npm test"}`), // 44
		result("2", noise+"FAIL login.test.ts expected 200 got 401", true),
		text(llmtypes.ChatMessageTypeAI, "the test fails with 401"),
		text(llmtypes.ChatMessageTypeHuman, continuityNoticeOpen+"\nold recap\n"+continuityNoticeClose+sessionModeSplit+"ok try another approach"),
	)
	selected, omitted, files, stats := continuityHandoff(history, 0, continuityHandoffBytes)
	got := renderContinuityItems(selected)
	for _, want := range []string{"[line 1 · user · message]\nfix the login bug", "[line 42 · assistant · edit]\nEdited: src/login.ts", "Command: npm test\nStatus: failed\n" + noise + "FAIL login.test.ts", "the test fails with 401", "ok try another approach"} {
		if !strings.Contains(got, want) {
			t.Fatalf("handoff misses %q", want)
		}
	}
	for _, unwanted := range []string{"secret-new", "old recap", "system prompt"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("handoff repeats %q", unwanted)
		}
	}
	if len(omitted) == 0 || omitted[0] != 2 || !strings.HasPrefix(continuityOmittedLine(omitted), "Left out (") || stats.Bytes > continuityHandoffBytes {
		t.Fatalf("omitted=%v stats=%+v", omitted, stats)
	}
	if strings.Join(files, ",") != "src/login.ts" || stats.ToolErrors != 1 || !stats.FirstAsk {
		t.Fatalf("files=%v stats=%+v", files, stats)
	}
	// chat_history_read reads the lines the handoff names: same numbering,
	// the whole edit and the whole output, and search finds the newest first.
	edit, _ := readChatHistory(history, map[string]interface{}{"line": float64(42)})
	if l := edit.(chatHistoryLine); !strings.Contains(l.Text, "secret-new") || l.Role != "assistant" {
		t.Fatalf("line 42 = %+v", l)
	}
	page, _ := readChatHistory(history, map[string]interface{}{"line": float64(44)})
	first := page.(chatHistoryLine)
	rest, _ := readChatHistory(history, map[string]interface{}{"line": float64(44), "offset": float64(first.NextOffset)})
	if first.NextOffset == 0 || !strings.Contains(first.Text+rest.(chatHistoryLine).Text, "FAIL login.test.ts expected 200 got 401") {
		t.Fatalf("line 44 next_offset=%d", first.NextOffset)
	}
	found, _ := readChatHistory(history, map[string]interface{}{"query": "LOGIN"})
	if m := found.(chatHistorySearchResult).Matches; len(m) < 2 || m[0].Line <= m[len(m)-1].Line || m[len(m)-1].Line != 1 {
		t.Fatalf("search = %+v", m)
	}
}

// #3140: Codex gets the handoff as conversation entries: an opening note,
// then each turn under its own role, with the user's message left clean.
func TestCodexHandoffBecomesConversationEntries(t *testing.T) {
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "fix login"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "running tests"}, llmtypes.ToolCall{ID: "1", FunctionCall: &llmtypes.FunctionCall{Name: "Bash", Arguments: `{"command":"npm test"}`}}}},
		{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: "1", Content: "FAIL login", IsError: true}}},
	}
	t.Setenv("CODEX_CLI_TRANSPORT", "")
	ctx := context.Background()
	query := handOffToCodingAgent(&ctx, serverLogContext{}, "agent_switch", "codex-cli", "s1", "fix that failure", "chats/c.json", history, continuityHandoffBytes)
	items := llmtypes.ClaimNativeHistoryForSession(ctx, "s1")
	if query != "fix that failure" || len(items) != 4 || items[0].Role != "user" || items[1].Role != "user" || !strings.HasSuffix(items[1].Text, "\nfix login") || items[2].Role != "assistant" || !strings.Contains(items[3].Text, "Command: npm test\nStatus: failed\nFAIL login") {
		t.Fatalf("query=%q items=%+v", query, items)
	}
	ctx = context.Background()
	if q := handOffToCodingAgent(&ctx, serverLogContext{}, "agent_switch", "claude-code", "s1", "fix that failure", "chats/c.json", history, continuityHandoffBytes); !strings.HasPrefix(q, continuityNoticeOpen) || llmtypes.ClaimNativeHistoryForSession(ctx, "s1") != nil {
		t.Fatalf("other agents keep the text recap: %q", q)
	}
}
