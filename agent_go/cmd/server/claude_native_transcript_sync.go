package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// claudeNativeTranscriptRuntime is the minimal subset of a persisted builder
// conversation's "runtime" object needed to locate the coding CLI's own
// on-disk transcript for the session that produced it.
type claudeNativeTranscriptRuntime struct {
	Provider           string `json:"provider"`
	ExternalSessionID  string `json:"external_session_id"`
	AgentSessionHandle *struct {
		ConnectionID string `json:"connection_id,omitempty"`
		Provider     *struct {
			Provider        string `json:"provider"`
			NativeSessionID string `json:"native_session_id"`
			WorkingDir      string `json:"working_dir"`
		} `json:"provider"`
	} `json:"agent_session_handle"`
}

type claudeTranscriptEntry struct {
	Type       string          `json:"type"`
	Timestamp  string          `json:"timestamp"`
	Message    json.RawMessage `json:"message"`
	Attachment json.RawMessage `json:"attachment"`
}

type claudeTranscriptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeTranscriptContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeTranscriptAttachment struct {
	Type      string `json:"type"`
	Prompt    string `json:"prompt"`
	HumanTurn bool   `json:"humanTurn"`
	Origin    *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

// publishNativeTranscriptRecoveredAssistantMessages closes the live-display
// half of transcript recovery. Persisting a provider-native reply makes it
// visible after refresh, but an already-open Chat only observes EventStore.
// Publish each recovered assistant message as the same whole-message
// transcript chunk used by normal CLI streaming. This deliberately is not a
// second completion event: a completion would settle the next queued retained
// turn when several user messages were submitted together.
func (api *StreamingAPI) publishNativeTranscriptRecoveredAssistantMessages(sessionID string, current, refreshed []builderConversationMessage, durableUIEvents []storeevents.Event) int {
	return api.publishNativeTranscriptRecoveredMessages(sessionID, current, refreshed, durableUIEvents, false)
}

func (api *StreamingAPI) publishNativeTranscriptRecoveredMessages(sessionID string, current, refreshed []builderConversationMessage, durableUIEvents []storeevents.Event, includeUsers bool) int {
	if api == nil || api.eventStore == nil || strings.TrimSpace(sessionID) == "" {
		return 0
	}

	currentCounts := assistantMessageCounts(current)
	eventCounts := liveAssistantMessageCounts(api.eventStore.GetAllEventsRaw(sessionID))
	// The in-memory EventStore starts empty after every server restart. Durable
	// UI events are what the browser will restore, so include them in the same
	// visibility gate. Use the larger count rather than adding the two sources:
	// they normally contain the same reply while the server is running.
	for key, count := range liveAssistantMessageCounts(durableUIEvents) {
		if count > eventCounts[key] {
			eventCounts[key] = count
		}
	}
	refreshedCounts := make(map[string]int)
	currentUsers := make(map[string]int)
	refreshedUsers := make(map[string]int)
	visibleUsers := nativeTerminalEventUserCounts(api.eventStore, sessionID)
	for _, text := range nativeTerminalUsers(current) {
		currentUsers[text]++
	}
	durableUsers := make(map[string]int)
	for _, row := range durableUIEvents {
		if row.Type == "user_message" {
			if text, ok := eventPayloadMap(row)["content"].(string); ok {
				durableUsers[strings.TrimSpace(text)]++
			}
		}
	}
	for text, count := range durableUsers {
		if count > visibleUsers[text] {
			visibleUsers[text] = count
		}
	}
	now := time.Now()
	published := 0
	for index, message := range refreshed {
		if includeUsers && (message.Role == "human" || message.Role == "user") {
			text := strings.TrimSpace(builderConversationMessageText(message))
			if text == "" {
				continue
			}
			refreshedUsers[text]++
			if refreshedUsers[text] <= currentUsers[text] || refreshedUsers[text] <= visibleUsers[text] {
				continue
			}
			id := fmt.Sprintf("native-transcript-sync-user-%s-%d", sessionID, index)
			user := agentevents.NewUserMessageEvent(0, text, "user")
			user.Timestamp = now
			user.Metadata = map[string]interface{}{"source": "native_transcript_sync", "message_id": id, "delivery_status": "sent_to_cli", "recovered_live": true}
			wrapped := agentevents.NewAgentEvent(user)
			wrapped.SessionID = sessionID
			api.eventStore.AddEvent(sessionID, storeevents.Event{ID: id, Type: "user_message", Timestamp: now, SessionID: sessionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + sessionID, Data: wrapped})
			visibleUsers[text]++
			published++
			continue
		}
		if !builderConversationRoleIsAssistant(message.Role) {
			continue
		}
		text := builderConversationMessageText(message)
		key := normalizedAssistantMessageText(text)
		if key == "" {
			continue
		}
		refreshedCounts[key]++
		ordinal := refreshedCounts[key]
		if ordinal <= currentCounts[key] || ordinal <= eventCounts[key] {
			continue
		}

		chunk := &agentevents.StreamingChunkEvent{
			BaseEventData: agentevents.BaseEventData{
				Timestamp: now,
				SessionID: sessionID,
				Component: "coding_agent",
				Metadata: map[string]interface{}{
					"source":         "native_transcript_sync",
					"recovered_live": true,
				},
			},
			Content:    text,
			ChunkIndex: index,
			Source:     agentevents.StreamingChunkSourceTranscript,
		}
		agentEvent := agentevents.NewAgentEvent(chunk)
		agentEvent.SessionID = sessionID
		agentEvent.Component = "coding_agent"
		api.eventStore.AddEvent(sessionID, storeevents.Event{
			ID:              fmt.Sprintf("native-transcript-sync-%s-%d", sessionID, index),
			Type:            string(agentevents.StreamingChunk),
			Timestamp:       now,
			SessionID:       sessionID,
			ExecutionKind:   "main_agent",
			TerminalOwnerID: "main:" + sessionID,
			Data:            agentEvent,
		})
		eventCounts[key]++
		published++
		log.Printf("[CHAT_HISTORY] Published recovered native assistant reply to live chat session=%s history_index=%d chars=%d", sessionID, index, len(text))
	}
	return published
}

func builderConversationRoleIsAssistant(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "ai", "assistant":
		return true
	default:
		return false
	}
}

func normalizedAssistantMessageText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func assistantMessageCounts(messages []builderConversationMessage) map[string]int {
	counts := make(map[string]int)
	for _, message := range messages {
		if !builderConversationRoleIsAssistant(message.Role) {
			continue
		}
		if key := normalizedAssistantMessageText(builderConversationMessageText(message)); key != "" {
			counts[key]++
		}
	}
	return counts
}

func liveAssistantMessageCounts(events []storeevents.Event) map[string]int {
	counts := make(map[string]int)
	for _, event := range events {
		if event.ExecutionKind != "" && event.ExecutionKind != "main_agent" {
			continue
		}
		payload := eventPayloadMap(event)
		if len(payload) == 0 {
			continue
		}
		var text string
		switch event.Type {
		case "streaming_chunk":
			if source, _ := payload["source"].(string); !strings.EqualFold(strings.TrimSpace(source), agentevents.StreamingChunkSourceTranscript) {
				continue
			}
			text, _ = payload["content"].(string)
		case "llm_generation_end":
			text, _ = payload["content"].(string)
			if strings.TrimSpace(text) == "" {
				text, _ = payload["result"].(string)
			}
		case "unified_completion", "conversation_end":
			text, _ = payload["final_result"].(string)
			if strings.TrimSpace(text) == "" {
				text, _ = payload["result"].(string)
			}
		default:
			continue
		}
		if key := normalizedAssistantMessageText(text); key != "" {
			counts[key]++
		}
	}
	return counts
}

// findWorkflowBuilderConversationPathForSession normally resolves through the
// history index. The folder-list fallback covers a newly written transcript
// before that index exists (and remote workspace deployments without the local
// directory fast path).
func findWorkflowBuilderConversationPathForSession(ctx context.Context, userID, sessionID, workspacePath string) (string, bool, error) {
	if strings.TrimSpace(userID) == "" {
		userID = "default"
	}
	if path, found, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath); err != nil || found {
		return path, found, err
	}
	listing, exists, err := listWorkspaceFolder(ctx, strings.Trim(strings.TrimSpace(workspacePath), "/")+"/builder/conversation", 5)
	if err != nil || !exists {
		return "", false, err
	}
	paths := []string{}
	collectWorkspaceFilePaths(listing, &paths)
	wantFileName := "session-" + sanitizeChatHistorySessionID(sessionID) + "-conversation.json"
	for _, path := range paths {
		if filepath.Base(path) == wantFileName && isWorkflowBuilderConversationLogPath(workspacePath, path) {
			return path, true, nil
		}
	}
	return "", false, nil
}

// nativeTranscriptSyncSupportedProvider: the coding CLIs whose on-disk
// transcript can be read back -- Claude Code and Codex by readers in this
// package (claude_native_transcript_sync.go, codex_native_transcript_sync.go),
// Cursor, Pi, and Muse by the adapters' own exported readers in
// multi-llm-provider-go (cursorcli.ReadNativeTranscript,
// picli.ReadNativeTranscript, musecli.ReadNativeTranscript), since those
// formats (a sqlite blob store and two session JSONL variants) are already
// parsed there for turn completion.
func nativeTranscriptSyncSupportedProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli", "agy-cli":
		return true
	}
	return false
}

// builderConversationMessagesFromLLMTypes projects an adapter's text-only
// transcript into the builder conversation's own shape. Messages without
// text (tool-only turns) are dropped; system messages never reach here.
func builderConversationMessagesFromLLMTypes(messages []llmtypes.MessageContent) []builderConversationMessage {
	out := make([]builderConversationMessage, 0, len(messages))
	for _, message := range messages {
		role := ""
		switch message.Role {
		case llmtypes.ChatMessageTypeHuman:
			role = "human"
		case llmtypes.ChatMessageTypeAI:
			role = "ai"
		default:
			continue
		}
		texts := make([]string, 0, len(message.Parts))
		for _, part := range message.Parts {
			if text, ok := part.(llmtypes.TextContent); ok && strings.TrimSpace(text.Text) != "" {
				texts = append(texts, strings.TrimSpace(text.Text))
			}
		}
		text := strings.TrimSpace(strings.Join(texts, "\n\n"))
		if text == "" {
			continue
		}
		out = append(out, builderConversationMessage{Role: role, Parts: []builderConversationPart{{Text: text}}})
	}
	return out
}

// nativeTranscriptMessagesForRuntime reads the CLI's own transcript for a
// builder session. ok is false when the provider has no reader or no
// transcript could be found; callers then leave the persisted record as-is.
func nativeTranscriptMessagesForRuntime(provider, nativeSessionID, workingDir string, accountHome ...string) (messages []builderConversationMessage, maxTimestamp time.Time, transcriptPath string, ok bool, err error) {
	messages, maxTimestamp, transcriptPath, ok, err = nativeTranscriptMessagesForRuntimeUncapped(provider, nativeSessionID, workingDir, accountHome...)
	return filterNativeContinuityMessages(messages), maxTimestamp, transcriptPath, ok, err
}

// Input observation needs the full user sequence to distinguish repeated
// prompts even when the persisted chat's bounded window is full.
func nativeTranscriptMessagesForRuntimeUncapped(provider, nativeSessionID, workingDir string, accountHome ...string) (messages []builderConversationMessage, maxTimestamp time.Time, transcriptPath string, ok bool, err error) {
	// Landlocked chats keep their CLI files in the chat's private home, not
	// the connected account's home. Resolve the exact native session there
	// first, including records saved before private-home metadata existed.
	// Never search another chat's home or substitute a different native ID.
	if filepath.IsAbs(workingDir) && nativeSessionID != "" {
		privateHome := filepath.Join(workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(provider))
		if info, statErr := os.Stat(privateHome); statErr == nil && info.IsDir() {
			messages, maxTimestamp, transcriptPath, ok, err = readNativeTranscriptMessagesForHome(provider, nativeSessionID, workingDir, privateHome)
			if ok || err != nil {
				return
			}
		}
	}
	return readNativeTranscriptMessagesForHome(provider, nativeSessionID, workingDir, accountHome...)
}

func readNativeTranscriptMessagesForHome(provider, nativeSessionID, workingDir string, accountHome ...string) (messages []builderConversationMessage, maxTimestamp time.Time, transcriptPath string, ok bool, err error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy-cli":
		if nativeSessionID == "" {
			return nil, time.Time{}, "", false, nil
		}
		transcript, found, err := agycli.ReadNativeTranscript(nativeSessionID, accountHome...)
		if err != nil || !found {
			return nil, time.Time{}, "", false, err
		}
		return filterNativeContinuityMessagesUncapped(builderConversationMessagesFromLLMTypes(transcript.Messages)), transcript.UpdatedAt, transcript.Path, true, nil
	case "claude-code":
		if nativeSessionID == "" || workingDir == "" {
			return nil, time.Time{}, "", false, nil
		}
		transcriptPath, err = resolveClaudeNativeTranscriptPath(workingDir, nativeSessionID, accountHome...)
		if err != nil || transcriptPath == "" {
			return nil, time.Time{}, "", false, err
		}
		messages, maxTimestamp, err = readNewClaudeTranscriptMessages(transcriptPath, time.Time{})
		messages = filterNativeContinuityMessagesUncapped(messages)
		return messages, maxTimestamp, transcriptPath, err == nil, err
	case "codex-cli":
		if nativeSessionID == "" {
			return nil, time.Time{}, "", false, nil
		}
		transcriptPath, err = resolveCodexNativeTranscriptPath(nativeSessionID, accountHome...)
		if err != nil || transcriptPath == "" {
			return nil, time.Time{}, "", false, err
		}
		messages, maxTimestamp, err = readCodexTranscriptMessages(transcriptPath)
		messages = filterNativeContinuityMessagesUncapped(messages)
		return messages, maxTimestamp, transcriptPath, err == nil, err
	case "cursor-cli":
		if nativeSessionID == "" || workingDir == "" {
			return nil, time.Time{}, "", false, nil
		}
		transcript, found, err := cursorcli.ReadNativeTranscript(workingDir, nativeSessionID, accountHome...)
		if err != nil || !found {
			return nil, time.Time{}, "", false, err
		}
		return filterNativeContinuityMessagesUncapped(builderConversationMessagesFromLLMTypes(transcript.Messages)), transcript.UpdatedAt, transcript.Path, true, nil
	case "pi-cli":
		if nativeSessionID == "" || workingDir == "" {
			return nil, time.Time{}, "", false, nil
		}
		transcript, found, err := picli.ReadNativeTranscriptFromWorkingDir(workingDir, nativeSessionID)
		if err != nil || !found {
			return nil, time.Time{}, "", false, err
		}
		return filterNativeContinuityMessagesUncapped(builderConversationMessagesFromLLMTypes(transcript.Messages)), transcript.UpdatedAt, transcript.Path, true, nil
	case "muse-cli":
		if nativeSessionID == "" {
			return nil, time.Time{}, "", false, nil
		}
		dataHome := ""
		if len(accountHome) > 0 && accountHome[0] != "" {
			dataHome = filepath.Join(accountHome[0], ".local", "share")
		}
		transcript, found, err := musecli.ReadNativeTranscript(nativeSessionID, dataHome)
		if err != nil || !found {
			return nil, time.Time{}, "", false, err
		}
		return filterNativeContinuityMessagesUncapped(builderConversationMessagesFromLLMTypes(transcript.Messages)), transcript.UpdatedAt, transcript.Path, true, nil
	}
	return nil, time.Time{}, "", false, nil
}

func filterNativeContinuityMessages(messages []builderConversationMessage) []builderConversationMessage {
	filtered := filterNativeContinuityMessagesUncapped(messages)
	if len(filtered) > maxPersistedChatHistoryMessages {
		filtered = filtered[len(filtered)-maxPersistedChatHistoryMessages:]
	}
	return filtered
}

func filterNativeContinuityMessagesUncapped(messages []builderConversationMessage) []builderConversationMessage {
	filtered := make([]builderConversationMessage, 0, len(messages))
	for _, message := range messages {
		message = stripSessionModeFromMessage(message)
		text := strings.TrimSpace(builderConversationMessageText(message))
		if strings.HasPrefix(text, "[AGENTWORKS CONVERSATION CONTINUITY]") || strings.HasPrefix(text, "[WORKFLOW CHAT HANDOFF]") {
			continue
		}
		// Agy has no system-prompt flag, so its first message is built as "System instructions:\n<system
		// prompt>\n\n<user message>" (agycli_exec.go). The person's own message is already in the chat;
		// publishing this transcript row added a second "user" row holding the whole system prompt,
		// dated now (Confida, Saurabh's chat, 2026-09-30).
		if strings.HasPrefix(text, "System instructions:\n") {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func builderConversationRawHistory(record map[string]interface{}) ([]json.RawMessage, bool) {
	history, exists := record["conversation_history"]
	if !exists {
		return nil, false
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		return nil, false
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

func builderConversationMessageKey(message builderConversationMessage) string {
	texts := make([]string, 0, len(message.Parts))
	for _, part := range message.Parts {
		if text := strings.Join(strings.Fields(part.Text), " "); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.ToLower(strings.TrimSpace(message.Role)) + "\x00" + strings.Join(texts, "\n")
}

// resolveClaudeNativeTranscriptPath locates Claude Code's own JSONL
// transcript for a session, mirroring the working-directory-to-project-slug
// scheme multi-llm-provider-go's claudecode adapter uses
// (pkg/adapters/claudecode/claudecode_transcript_path.go) plus its
// session-ID glob fallback for when that escaping scheme has changed across
// CLI versions. Duplicated here (rather than imported) because that
// resolver is unexported and this is a narrow, self-contained lookup.
func resolveClaudeNativeTranscriptPath(workingDir, sessionID string, accountHome ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if len(accountHome) > 0 && accountHome[0] != "" {
		home = accountHome[0]
	}
	candidates := pathidentity.Candidates(workingDir)

	seen := make(map[string]struct{}, len(candidates))
	for _, dir := range candidates {
		slug := claudeNativeTranscriptProjectSlug(dir)
		if slug == "" {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		path := filepath.Join(home, ".claude", "projects", slug, sessionID+".jsonl")
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			return path, nil
		}
	}

	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", err
	}
	return matches[0], nil
}

func claudeNativeTranscriptProjectSlug(workingDir string) string {
	workingDir = filepath.Clean(strings.TrimSpace(workingDir))
	if workingDir == "" || workingDir == "." {
		return ""
	}
	return strings.NewReplacer(
		"/", "-",
		"\\", "-",
		"_", "-",
		".", "-",
		":", "-",
	).Replace(workingDir)
}

// readNewClaudeTranscriptMessages parses Claude Code's JSONL transcript and
// returns only the human-visible user/assistant messages timestamped after
// since, converted to the same builderConversationMessage shape the rest of
// the builder conversation log already uses.
func readNewClaudeTranscriptMessages(path string, since time.Time) ([]builderConversationMessage, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}

	var messages []builderConversationMessage
	maxTimestamp := since
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry claudeTranscriptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" && entry.Type != "attachment" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			continue
		}
		if !ts.After(since) {
			continue
		}
		role := "ai"
		if entry.Type == "user" {
			role = "human"
		}
		text := ""
		if entry.Type == "attachment" {
			var attachment claudeTranscriptAttachment
			if len(entry.Attachment) == 0 || json.Unmarshal(entry.Attachment, &attachment) != nil ||
				attachment.Type != "queued_command" || !attachment.HumanTurn || attachment.Origin == nil ||
				!strings.EqualFold(strings.TrimSpace(attachment.Origin.Kind), "human") {
				continue
			}
			role = "human"
			text = unwrapClaudeQueuedCommandPrompt(attachment.Prompt)
		} else {
			if len(entry.Message) == 0 {
				continue
			}
			var msg claudeTranscriptMessage
			if err := json.Unmarshal(entry.Message, &msg); err != nil {
				continue
			}
			text = extractClaudeTranscriptText(msg.Content)
		}
		if text == "" || isClaudeLocalCommandMessage(role, text) {
			continue
		}

		messages = append(messages, builderConversationMessage{
			Role:  role,
			Parts: []builderConversationPart{{Text: text}},
		})
		if ts.After(maxTimestamp) {
			maxTimestamp = ts
		}
	}
	return messages, maxTimestamp, nil
}

// Claude Code records a prompt typed while the agent is busy as a
// queued_command attachment instead of a normal user message. The prompt is
// commonly wrapped in a provider-only pasted_content envelope; the terminal
// shows only its body, so persist that same reader-visible text.
func unwrapClaudeQueuedCommandPrompt(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if !strings.HasPrefix(prompt, "<pasted_content") {
		return prompt
	}
	openEnd := strings.Index(prompt, ">")
	closeStart := strings.LastIndex(prompt, "</pasted_content")
	if openEnd < 0 || closeStart <= openEnd {
		return prompt
	}
	return strings.TrimSpace(prompt[openEnd+1 : closeStart])
}

// extractClaudeTranscriptText pulls the human-visible text out of a
// transcript entry's message.content, which is either a plain string (a
// real typed message) or a list of content blocks (thinking/text/tool_use
// for assistant turns, tool_result for a user-role entry that is actually
// just a tool's output echoed back, not something the user typed). Only
// plain-string content and "text"-typed blocks are kept -- tool_use,
// tool_result, and thinking are execution detail the chat history was never
// meant to display, and including them would clutter it, not fix it.
func extractClaudeTranscriptText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(content, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var blocks []claudeTranscriptContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
