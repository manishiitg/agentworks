package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A coding agent that starts without its own memory (an agent switch, a
// restart that cannot resume, a mode change) gets the chat handed over as
// whole items (#3140, after T3 Code's context handoff): each user message,
// reply, command with its full output, error and edit is either included
// intact or left out and listed by its line in the saved history, where the
// agent can read it. Cutting turns and output short made the new agent rerun
// commands or ask what "that failure" meant.
const (
	continuityHandoffBytes    = 64 * 1024
	continuityCallBytes       = 220
	continuityChangedFilesMax = 30
	continuityOmittedRanges   = 30
)

// continuityRecapStats is logged with each handoff; it carries counts only.
type continuityRecapStats struct {
	Messages, Items, Included, Omitted, ToolSteps, ToolErrors, ChangedFiles, Bytes int
	FirstAsk                                                                       bool
}

// continuityItem is one piece of the chat: Line is its 1-based line in the
// saved history (system prompts are not saved there), Kind says what it is.
type continuityItem struct {
	Line       int
	Role, Kind string
	Text       string
	isError    bool
}

func (item continuityItem) render() string {
	return fmt.Sprintf("[line %d · %s · %s]\n%s", item.Line, item.Role, item.Kind, item.Text)
}

type continuityToolResult struct {
	content string
	isError bool
}

// continuityItems turns history[from:] into items. Lines count from the
// start of history so they match the saved log.
func continuityItems(history []llmtypes.MessageContent, from int) ([]continuityItem, []string) {
	results := map[string]continuityToolResult{}
	for _, message := range history[from:] {
		for _, part := range message.Parts {
			if r, ok := part.(llmtypes.ToolCallResponse); ok && r.ToolCallID != "" {
				results[r.ToolCallID] = continuityToolResult{content: r.Content, isError: r.IsError}
			}
		}
	}
	var items []continuityItem
	var files []string
	seenFiles := map[string]bool{}
	line := 0
	for i, message := range history {
		role := strings.ToLower(string(message.Role))
		if role == "system" {
			continue
		}
		line++
		if i < from {
			continue
		}
		for _, part := range message.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				text := continuityText(role, p.Text)
				if text == "" {
					continue
				}
				switch role {
				case "user", "human":
					items = append(items, continuityItem{Line: line, Role: "user", Kind: "message", Text: text})
				case "ai", "assistant":
					items = append(items, continuityItem{Line: line, Role: "assistant", Kind: "message", Text: text})
				}
			case llmtypes.ToolCall:
				if p.FunctionCall == nil {
					continue
				}
				edited := continuityEditedPaths(p.FunctionCall.Name, p.FunctionCall.Arguments)
				for _, path := range edited {
					if !seenFiles[path] {
						seenFiles[path] = true
						files = append(files, path)
					}
				}
				r, hasResult := results[p.ID]
				items = append(items, continuityToolItem(line, p.FunctionCall.Name, p.FunctionCall.Arguments, edited, r, hasResult))
			}
		}
	}
	// Newest first, as the agent reads the list.
	for l, r := 0, len(files)-1; l < r; l, r = l+1, r-1 {
		files[l], files[r] = files[r], files[l]
	}
	if len(files) > continuityChangedFilesMax {
		files = files[:continuityChangedFilesMax]
	}
	return items, files
}

// continuityToolItem keeps a command's full output and any error. Edits name
// their files, never the content written; reads and searches keep no output,
// since the files themselves are current.
func continuityToolItem(line int, name, arguments string, edited []string, r continuityToolResult, hasResult bool) continuityItem {
	item := continuityItem{Line: line, Role: "assistant", Kind: "tool", isError: r.isError}
	output := strings.TrimSpace(r.content)
	if command := continuityRawArg(continuityArgs(arguments), "command", "cmd"); command != "" && continuityIsShell(name) {
		item.Kind = "command"
		status := "finished"
		if !hasResult {
			status = "no result saved"
		} else if r.isError {
			status = "failed"
		}
		item.Text = "Command: " + strings.TrimSpace(command) + "\nStatus: " + status
		if output != "" {
			item.Text += "\n" + output
		}
		return item
	}
	if len(edited) > 0 {
		item.Kind, item.Text = "edit", "Edited: "+strings.Join(edited, ", ")
	} else {
		item.Text = "Used " + continuityCallSummary(name, arguments)
	}
	if r.isError {
		item.Kind = "error"
		item.Text += "\nError: " + output
	}
	return item
}

func continuityIsShell(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "exec_command", "shell", "run_shell_command", "terminal", "mcp__api-bridge__terminal":
		return true
	}
	return false
}

// selectContinuityItems picks whole items within budget, the way T3 Code
// does: the last request, the last reply and the first request first, then
// newest to oldest. Returned in chat order, with the lines left out.
func selectContinuityItems(items []continuityItem, budget int) ([]continuityItem, []int, bool) {
	chosen := make([]bool, len(items))
	used := 0
	try := func(i int) {
		if i < 0 || chosen[i] {
			return
		}
		cost := len(items[i].render()) + 2
		if used+cost > budget {
			return
		}
		chosen[i], used = true, used+cost
	}
	lastOf := func(role, kind string) int {
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Role == role && items[i].Kind == kind {
				return i
			}
		}
		return -1
	}
	firstAsk := -1
	for i, item := range items {
		if item.Role == "user" {
			firstAsk = i
			break
		}
	}
	try(lastOf("user", "message"))
	try(lastOf("assistant", "message"))
	try(firstAsk)
	for i := len(items) - 1; i >= 0; i-- {
		try(i)
	}
	var selected []continuityItem
	var omitted []int
	for i, item := range items {
		if chosen[i] {
			selected = append(selected, item)
		} else if len(omitted) == 0 || omitted[len(omitted)-1] != item.Line {
			omitted = append(omitted, item.Line)
		}
	}
	keptFirst := firstAsk >= 0 && chosen[firstAsk] && len(omitted) > 0 && omitted[0] > items[firstAsk].Line
	return selected, omitted, keptFirst
}

// continuityHandoff builds the handoff for history[from:] within budget.
func continuityHandoff(history []llmtypes.MessageContent, from, budget int) ([]continuityItem, []int, []string, continuityRecapStats) {
	items, files := continuityItems(history, from)
	selected, omitted, keptFirst := selectContinuityItems(items, budget)
	stats := continuityRecapStats{Messages: len(history) - from, Items: len(items), Included: len(selected), Omitted: len(items) - len(selected), ChangedFiles: len(files), FirstAsk: keptFirst}
	for _, item := range items {
		if item.Kind == "command" || item.Kind == "tool" || item.Kind == "edit" || item.Kind == "error" {
			stats.ToolSteps++
		}
		if item.isError {
			stats.ToolErrors++
		}
	}
	for _, item := range selected {
		stats.Bytes += len(item.render()) + 2
	}
	return selected, omitted, files, stats
}

func renderContinuityItems(items []continuityItem) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = item.render()
	}
	return strings.Join(parts, "\n\n")
}

// continuityOmittedLine lists the lines left out as ranges ("1-40, 52").
func continuityOmittedLine(omitted []int) string {
	if len(omitted) == 0 {
		return ""
	}
	var ranges []string
	for i := 0; i < len(omitted); {
		j := i
		for j+1 < len(omitted) && omitted[j+1] <= omitted[j]+1 {
			j++
		}
		if len(ranges) == continuityOmittedRanges {
			ranges = append(ranges, "and more")
			break
		}
		if i == j {
			ranges = append(ranges, fmt.Sprint(omitted[i]))
		} else {
			ranges = append(ranges, fmt.Sprintf("%d-%d", omitted[i], omitted[j]))
		}
		i = j + 1
	}
	return fmt.Sprintf("Left out (%d items), by line: %s.\n", len(omitted), strings.Join(ranges, ", "))
}

// continuityText returns a message's own text: an earlier recap carried in a
// user turn keeps only the user's words, and internal handoffs are skipped.
func continuityText(role, text string) string {
	text = strings.TrimSpace(text)
	if role == "user" || role == "human" {
		text = cleanChatHistoryQuery(text)
		if strings.HasPrefix(text, continuityNoticeOpen) {
			if idx := strings.LastIndex(text, continuityNoticeClose+sessionModeSplit); idx >= 0 {
				text = strings.TrimSpace(text[idx+len(continuityNoticeClose+sessionModeSplit):])
			} else {
				text = ""
			}
		}
	}
	if text == "" || strings.HasPrefix(text, "[WORKFLOW CHAT HANDOFF]") || shouldSkipChatHistoryPreviewText(text) {
		return ""
	}
	return text
}

func continuityArgs(arguments string) map[string]any {
	var args map[string]any
	_ = json.Unmarshal([]byte(arguments), &args)
	return args
}

func continuityArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.Join(strings.Fields(v), " ")
		}
	}
	return ""
}

// continuityCallSummary names the tool and its key argument: the command,
// the file or the query. Content being written is never repeated.
func continuityCallSummary(name, arguments string) string {
	args := continuityArgs(arguments)
	if name == "apply_patch" {
		if paths := continuityEditedPaths(name, arguments); len(paths) > 0 {
			return "edited " + strings.Join(paths, ", ")
		}
		return "applied a patch"
	}
	switch strings.ToLower(name) {
	case "edit", "write", "multiedit", "notebookedit":
		return strings.ToLower(name) + " " + continuityArg(args, "file_path", "notebook_path", "path")
	}
	if key := continuityArg(args, "command", "cmd"); key != "" {
		return "ran " + shortenContinuityTurn(key, continuityCallBytes)
	}
	if key := continuityArg(args, "file_path", "path", "pattern", "query", "url", "tool_name", "action", "description"); key != "" {
		return name + " " + shortenContinuityTurn(key, continuityCallBytes)
	}
	return name
}

// continuityEditedPaths returns the files a tool call wrote.
func continuityEditedPaths(name, arguments string) []string {
	switch strings.ToLower(name) {
	case "edit", "write", "multiedit", "notebookedit":
		if p := continuityArg(continuityArgs(arguments), "file_path", "notebook_path", "path"); p != "" {
			return []string{p}
		}
	case "apply_patch":
		text := arguments
		if args := continuityArgs(arguments); args != nil {
			text = continuityRawArg(args, "input", "patch")
		}
		var paths []string
		for _, line := range strings.Split(text, "\n") {
			for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
				if strings.HasPrefix(line, prefix) {
					paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
				}
			}
		}
		return paths
	}
	return nil
}

func continuityRawArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key].(string); ok {
			return v
		}
	}
	return ""
}

func continuityChangedFilesLine(files []string) string {
	if len(files) == 0 {
		return ""
	}
	more := ""
	if len(files) == continuityChangedFilesMax {
		more = " (and possibly more)"
	}
	return fmt.Sprintf("Files this chat changed, newest first: %s%s\n", strings.Join(files, ", "), more)
}

// codexTakesNativeHistory reports whether a new Codex session can take the
// handoff as conversation entries (its app-server, the default transport).
func codexTakesNativeHistory(provider string) bool {
	if strings.ToLower(strings.TrimSpace(provider)) != "codex-cli" {
		return false
	}
	transport := strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_CLI_TRANSPORT")))
	return transport == "" || transport == "app-server"
}

// handOffToCodingAgent gives a coding agent starting without its own memory
// the recap: Codex gets it as real conversation entries on its new thread
// (the provider falls back to text if it cannot), every other agent as text
// in front of the user's message.
func handOffToCodingAgent(ctx *context.Context, logCtx serverLogContext, reason, provider, sessionID, query, conversationPath, workspacePath string, history []llmtypes.MessageContent) string {
	cleaned := cleanChatHistoryQuery(query)
	if ctx == nil || !codexTakesNativeHistory(provider) || strings.TrimSpace(conversationPath) == "" || strings.HasPrefix(strings.TrimSpace(cleaned), continuityNoticeOpen) {
		return prependCodingAgentContinuityNotice(logCtx, reason, query, conversationPath, workspacePath, history)
	}
	items, stats := codingAgentContinuityItems(conversationPath, workspacePath, history)
	*ctx = llmtypes.WithNativeHistory(*ctx, sessionID, items)
	logContinuityHandoff(logCtx, reason, "native_items", stats)
	return cleaned
}

func logContinuityHandoff(logCtx serverLogContext, reason, action string, stats continuityRecapStats) {
	logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=%s action=%s messages=%d items=%d included=%d omitted=%d tool_steps=%d tool_errors=%d changed_files=%d first_request_kept=%t handoff_bytes=%d",
		reason, action, stats.Messages, stats.Items, stats.Included, stats.Omitted, stats.ToolSteps, stats.ToolErrors, stats.ChangedFiles, stats.FirstAsk, stats.Bytes)
}
