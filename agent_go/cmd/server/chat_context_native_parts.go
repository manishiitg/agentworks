package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// nativeChatContextParts sizes what fills a Claude Code chat from its own transcript: Claude Code reports only the
// total, so this is the one way to show what the total is made of.
func (api *StreamingAPI) nativeChatContextParts(sessionID string, breakdown nativeBreakdown, usedTokens int) []chatContextPart {
	var ours int
	// Only Claude Code's setup is known to hold the chat's own instructions.
	if sizes, known := api.promptParts.Load(sessionID); known && breakdown.app == claudeCodeApp {
		s := sizes.(mcpagent.PromptPartSizes)
		ours = (s.Instructions + s.Added + s.Runtime) / charsPerToken
	}
	return nativeContextParts(breakdown.ContextBreakdown, breakdown.app, ours, usedTokens)
}

const claudeCodeApp, museApp = "Claude Code", "Muse"

// nativeBreakdown is a chat's breakdown and the app that reported it.
type nativeBreakdown struct {
	claudecode.ContextBreakdown
	app string
}

// nativeChatBreakdown reads what fills a Claude Code chat (its transcript) or a Muse chat (the request trace in its
// session log); false for other coding agents or when it cannot be read.
func (api *StreamingAPI) nativeChatBreakdown(ctx context.Context, owner string, tab chatContextTab) (nativeBreakdown, bool) {
	if nativeID, accountHome, ok := api.nativeChatSession(ctx, owner, tab, "muse-cli"); ok {
		dataHome := ""
		if accountHome != "" {
			dataHome = filepath.Join(accountHome, ".local", "share")
		}
		m, ok := musecli.ReadContextBreakdown(nativeID, dataHome)
		if !ok {
			return nativeBreakdown{}, false
		}
		// Muse's trace counts bytes; the parts are scaled to the total the chat reports, so only proportions matter.
		return nativeBreakdown{app: museApp, ContextBreakdown: claudecode.ContextBreakdown{
			Total: m.Total / charsPerToken, Setup: m.Setup / charsPerToken, Skills: m.Skills / charsPerToken,
			AppNotes: m.Context / charsPerToken, Conversation: (m.Messages + m.Replies + m.ToolResults) / charsPerToken,
		}}, true
	}
	nativeID, workingDir, accountHome, ok := api.nativeChatTranscript(ctx, owner, tab)
	if !ok {
		return nativeBreakdown{}, false
	}
	b, ok := claudecode.ContextBreakdownFromTranscript(nativeID, workingDir, accountHome)
	return nativeBreakdown{ContextBreakdown: b, app: claudeCodeApp}, ok
}

// nativeChatTranscript finds a Claude Code chat's own session and where its transcript is kept.
func (api *StreamingAPI) nativeChatTranscript(ctx context.Context, owner string, tab chatContextTab) (nativeID, workingDir, accountHome string, ok bool) {
	nativeID, accountHome, ok = api.nativeChatSession(ctx, owner, tab, "claude-code")
	if !ok {
		return "", "", "", false
	}
	runtime := chatContextRuntimeMetadata(owner, tab.Record)
	return nativeID, strings.TrimSpace(runtime.AgentSessionHandle.Provider.WorkingDir), accountHome, true
}

// nativeChatSession finds a chat's own native session when the chat runs on provider, and the HOME of its account.
func (api *StreamingAPI) nativeChatSession(ctx context.Context, owner string, tab chatContextTab, provider string) (nativeID, accountHome string, ok bool) {
	runtime := chatContextRuntimeMetadata(owner, tab.Record)
	if runtime == nil || runtime.AgentSessionHandle == nil {
		return "", "", false
	}
	handle := runtime.AgentSessionHandle
	if chatProviderKey(firstNonEmptyTrimmed(handle.Provider.Provider, runtime.Provider)) != chatProviderKey(provider) {
		return "", "", false
	}
	nativeID = firstNonEmptyTrimmed(handle.Provider.NativeSessionID, runtime.ExternalSessionID)
	if nativeID == "" {
		return "", "", false
	}
	if handle.ConnectionID != "" {
		scope := providerAccountScope{Principal: owner, WorkspacePath: tab.Record.WorkspacePath}
		if keys, err := api.connectionAPIKeys(ctx, scope, provider, handle.ConnectionID); err == nil && keys != nil {
			accountHome = keys.RuntimeEnvironment["HOME"]
		}
	}
	return nativeID, accountHome, true
}

// chatContextPartItem is one item behind a part of the Context pane's breakdown, with its text.
type chatContextPartItem struct {
	Label  string `json:"label"`
	Text   string `json:"text"`
	Tokens int    `json:"tokens"`
	Count  int    `json:"count,omitempty"`
}

// chatContextPartTexts are the parts whose items a Claude Code chat's transcript holds.
var chatContextPartTexts = map[string]bool{
	claudecode.ContextPartSetup: true, claudecode.ContextPartSkills: true, claudecode.ContextPartFiles: true,
	claudecode.ContextPartResent: true, claudecode.ContextPartSummary: true, claudecode.ContextPartNotes: true,
}

// GET /api/chat-context/part?session_id=…&key=skills lists what one part of the caller's own Claude Code chat is
// made of, with each item's text and rough size, biggest first.
func (api *StreamingAPI) handleChatContextPart(w http.ResponseWriter, r *http.Request) {
	owner := productWorkspaceUserID(r.Context())
	tab, found := chatContextTabForSession(r.Context(), owner, r.URL.Query().Get("session_id"))
	if !found {
		writeAgentProfileError(w, http.StatusNotFound, "Only this chat's owner can see what fills it.")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if !chatContextPartTexts[key] {
		writeAgentProfileError(w, http.StatusBadRequest, "This part has no saved text to show.")
		return
	}
	nativeID, workingDir, accountHome, ok := api.nativeChatTranscript(r.Context(), owner, tab)
	var texts map[string][]claudecode.ContextItem
	if ok {
		texts, ok = claudecode.ContextTextsFromTranscript(nativeID, workingDir, accountHome)
	}
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "This chat's saved log can't be read right now. Send a message and try again.")
		return
	}
	items := make([]chatContextPartItem, 0, len(texts[key]))
	for _, item := range texts[key] {
		items = append(items, chatContextPartItem{Label: item.Label, Text: item.Text, Tokens: max(1, item.Chars/charsPerToken), Count: item.Count})
	}
	out := map[string]any{"items": items}
	if len(items) == 0 && key == claudecode.ContextPartSetup {
		out["note"] = "This Claude Code version doesn't save its own rules and tools in the chat log, so only their size is known."
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
}

// nativeContextParts turns a transcript breakdown into the pane's parts, scaled to the total the chat reports now.
// ours is the size of the instructions AgentWorks put in the setup, when known.
func nativeContextParts(b claudecode.ContextBreakdown, app string, ours, usedTokens int) []chatContextPart {
	if b.Total <= 0 {
		return nil
	}
	scale := func(n int) int {
		if usedTokens <= 0 {
			return n
		}
		return int(int64(n) * int64(usedTokens) / int64(b.Total))
	}
	var parts []chatContextPart
	add := func(key, label string, tokens int, note string) {
		if tokens = scale(tokens); tokens > 0 {
			// Only Claude Code's transcript holds the parts' text.
			parts = append(parts, chatContextPart{Key: key, Label: label, Tokens: tokens, Note: note, Openable: app == claudeCodeApp && chatContextPartTexts[key]})
		}
	}
	setup := b.Setup
	if ours > 0 && ours < setup {
		add("instructions", "Your instructions", ours, "")
		setup -= ours
	}
	add("app_setup", app+"'s own rules and tools", setup, "")
	add("skills", "Skills list", b.Skills, "")
	add("files", "Instruction files (CLAUDE.md, AGENTS.md)", b.InstructionFiles, "")
	if b.ResentCount > 0 {
		note := "Your instructions changed while this chat was open, so the change was added to one of your messages. It stays in this chat until it compacts."
		label := "Instruction updates"
		if b.ResentCount > 1 {
			note = "Your instructions changed while this chat was open, so each change was added to one of your messages. They stay in this chat until it compacts."
			label = "Instruction updates (" + strconv.Itoa(b.ResentCount) + ")"
		}
		add("resent", label, b.Resent, note)
	}
	add("summary", "Summary of the earlier chat", b.Summary, "")
	notes := "Reminders Claude Code adds"
	if app != claudeCodeApp {
		notes = "Context " + app + " adds (memory, workspace notes)"
	}
	add("notes", notes, b.AppNotes, "")
	// The conversation takes what is left, so the parts add up to the reported total.
	target := usedTokens
	if target <= 0 {
		target = b.Total
	}
	rest := target
	for _, part := range parts {
		rest -= part.Tokens
	}
	if rest > 0 {
		parts = append(parts, chatContextPart{Key: "messages", Label: "Conversation", Tokens: rest})
	}
	return parts
}
