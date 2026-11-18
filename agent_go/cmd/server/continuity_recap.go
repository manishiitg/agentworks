package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A coding agent that starts without its own memory (an agent switch, a
// restart that cannot resume, a mode change) gets this recap of the chat
// (#3140). It shows what was said and what was done: commands with the end of
// their output, errors and edited files. Text alone made the new agent rerun
// commands or ask what "that failure" meant. The archive keeps everything.
const (
	continuityRecapBytes      = 16 * 1024
	continuityRecapLines      = 80
	continuityTextBytes       = 700
	continuityCallBytes       = 220
	continuityOutputBytes     = 300
	continuityChangedFilesMax = 30
)

// continuityRecapStats is logged with each recap; it carries counts only.
type continuityRecapStats struct {
	Messages, TextTurns, ToolSteps, ToolErrors, ChangedFiles, Lines, Bytes int
	FirstAsk                                                               bool
}

type continuityToolResult struct {
	content string
	isError bool
}

// continuityRecap returns the newest chat activity, oldest first, within the
// budget, and the files the chat changed (newest first).
func continuityRecap(history []llmtypes.MessageContent) ([]string, []string, continuityRecapStats) {
	stats := continuityRecapStats{Messages: len(history)}
	results := map[string]continuityToolResult{}
	for _, message := range history {
		for _, part := range message.Parts {
			if r, ok := part.(llmtypes.ToolCallResponse); ok && r.ToolCallID != "" {
				results[r.ToolCallID] = continuityToolResult{content: r.Content, isError: r.IsError}
			}
		}
	}
	var files []string
	seenFiles := map[string]bool{}
	var lines []string
	used := 0
	full := false
	firstAsk, firstAskIndex := "", -1
	for i := len(history) - 1; i >= 0; i-- {
		role := strings.ToLower(string(history[i].Role))
		var entry []string
		steps, errs := 0, 0
		for _, part := range history[i].Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if text := continuityText(role, p.Text); text != "" {
					if role == "user" || role == "human" {
						firstAsk, firstAskIndex = text, i
						entry = append(entry, "user: "+shortenContinuityTurn(text, continuityTextBytes))
					} else if role == "ai" || role == "assistant" {
						entry = append(entry, "assistant: "+shortenContinuityTurn(text, continuityTextBytes))
					}
				}
			case llmtypes.ToolCall:
				if p.FunctionCall == nil {
					continue
				}
				for _, path := range continuityEditedPaths(p.FunctionCall.Name, p.FunctionCall.Arguments) {
					if !seenFiles[path] && len(files) < continuityChangedFilesMax {
						seenFiles[path] = true
						files = append(files, path)
					}
				}
				if full {
					continue
				}
				step := "  did: " + continuityCallSummary(p.FunctionCall.Name, p.FunctionCall.Arguments)
				if r, ok := results[p.ID]; ok {
					if out := continuityResultSummary(p.FunctionCall.Name, r); out != "" {
						step += "\n    -> " + out
					}
					if r.isError {
						errs++
					}
				}
				entry = append(entry, step)
				steps++
			}
		}
		if full || len(entry) == 0 {
			continue
		}
		block := strings.Join(entry, "\n")
		if used+len(block)+1 > continuityRecapBytes || len(lines)+len(entry) > continuityRecapLines {
			full = true
			continue
		}
		for _, line := range entry {
			if strings.HasPrefix(line, "user: ") || strings.HasPrefix(line, "assistant: ") {
				stats.TextTurns++
			}
		}
		stats.ToolSteps += steps
		stats.ToolErrors += errs
		lines = append(lines, block)
		used += len(block) + 1
	}
	for l, r := 0, len(lines)-1; l < r; l, r = l+1, r-1 {
		lines[l], lines[r] = lines[r], lines[l]
	}
	// The chat's first request frames everything after it; keep it when the
	// budget cut it off.
	if full && firstAskIndex >= 0 && firstAsk != "" {
		lines = append([]string{"first request in this chat: " + shortenContinuityTurn(firstAsk, continuityTextBytes), "[... earlier activity left out; it is in the full history ...]"}, lines...)
		stats.FirstAsk = true
	}
	stats.ChangedFiles = len(files)
	stats.Lines = len(lines)
	for _, line := range lines {
		stats.Bytes += len(line) + 1
	}
	return lines, files, stats
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
	return strings.Join(strings.Fields(text), " ")
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

// continuityResultSummary keeps the end of command output (where results and
// errors land) and any error. Reads, searches and edits keep no output: the
// files themselves are current.
func continuityResultSummary(name string, r continuityToolResult) string {
	text := strings.Join(strings.Fields(r.content), " ")
	if text == "" {
		if r.isError {
			return "failed"
		}
		return ""
	}
	if r.isError {
		return "error: " + continuityTail(text, continuityOutputBytes)
	}
	switch strings.ToLower(name) {
	case "bash", "exec_command", "shell", "run_shell_command", "terminal", "mcp__api-bridge__terminal":
		return continuityTail(text, continuityOutputBytes)
	}
	return ""
}

func continuityTail(text string, max int) string {
	if len(text) <= max {
		return text
	}
	tail := text[len(text)-max:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "..." + tail
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
	bytes := 0
	for _, item := range items {
		bytes += len(item.Text)
	}
	logfWithContext(logCtx, "[CONVERSATION_HANDOFF] reason=%s action=native_items items=%d messages=%d text_turns=%d tool_steps=%d tool_errors=%d changed_files=%d first_request_kept=%t recap_bytes=%d",
		reason, len(items), stats.Messages, stats.TextTurns, stats.ToolSteps, stats.ToolErrors, stats.ChangedFiles, stats.FirstAsk, bytes)
	return cleaned
}
