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

// scheduleWorkflowBuilderNativeTranscriptSync closes the persistence gap for
// retained live-input turns. The turn-completion observer must stay fast, so
// transcript I/O runs off-path and is coalesced per session. Claude normally
// usually flushes its native transcript before it signals completion. Cursor
// can acknowledge completion first and flush the final assistant message a few
// seconds later, so keep reconciling for a bounded window even when an earlier
// attempt recovered progress messages.
func (api *StreamingAPI) scheduleWorkflowBuilderNativeTranscriptSync(sessionID string) {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	api.sessionWorkspaceMu.RLock()
	workspacePath := strings.TrimSpace(api.sessionWorkspaceFolders[sessionID])
	api.sessionWorkspaceMu.RUnlock()
	if workspacePath == "" {
		return
	}
	userID := ""
	if api.eventStore != nil {
		userID = strings.TrimSpace(api.eventStore.GetSessionOwner(sessionID))
	}
	if userID == "" {
		api.activeSessionsMux.RLock()
		if session := api.activeSessions[sessionID]; session != nil {
			userID = strings.TrimSpace(session.UserID)
		}
		api.activeSessionsMux.RUnlock()
	}
	if userID == "" {
		log.Printf("[CHAT_HISTORY] Refusing native recovery without session owner session=%s", sessionID)
		return
	}

	api.scheduleOwnedNativeTranscriptSync(userID, sessionID, workspacePath)
}

func (api *StreamingAPI) scheduleOwnedNativeTranscriptSync(userID, sessionID, workspacePath string) {
	if err := persistNativeTranscriptRecoveryDemand(nativeTranscriptRecoveryDemand{UserID: userID, SessionID: sessionID, WorkspacePath: workspacePath, RequestedAt: time.Now().UTC(), State: "unresolved"}); err != nil {
		log.Printf("[CHAT_HISTORY] Native recovery demand could not be persisted session=%s: %v", sessionID, err)
	}

	syncKey := nativeTranscriptRecoveryKey(userID, sessionID, workspacePath)
	api.nativeTranscriptSyncMu.Lock()
	if api.nativeTranscriptSyncInFlight == nil {
		api.nativeTranscriptSyncInFlight = make(map[string]bool)
	}
	if api.nativeTranscriptSyncPending == nil {
		api.nativeTranscriptSyncPending = make(map[string]bool)
	}
	api.nativeTranscriptSyncPending[syncKey] = true
	if api.nativeTranscriptSyncInFlight[syncKey] {
		api.nativeTranscriptSyncMu.Unlock()
		return
	}
	api.nativeTranscriptSyncInFlight[syncKey] = true
	api.nativeTranscriptSyncMu.Unlock()

	go api.runNativeTranscriptSyncWorker(syncKey, func() {
		delays := []time.Duration{0, 300 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
		for _, delay := range delays {
			if delay > 0 {
				time.Sleep(delay)
			}
			_, supported := api.syncWorkflowBuilderConversationFromNativeTranscript(context.Background(), userID, sessionID, workspacePath)
			if !supported {
				break
			}
		}
	})
}

func (api *StreamingAPI) runNativeTranscriptSyncWorker(sessionID string, reconcileWindow func()) {
	for {
		api.nativeTranscriptSyncMu.Lock()
		api.nativeTranscriptSyncPending[sessionID] = false
		api.nativeTranscriptSyncMu.Unlock()
		reconcileWindow()
		// The demand check and relinquishing worker ownership are atomic.
		api.nativeTranscriptSyncMu.Lock()
		if api.nativeTranscriptSyncPending[sessionID] {
			api.nativeTranscriptSyncMu.Unlock()
			continue
		}
		delete(api.nativeTranscriptSyncPending, sessionID)
		delete(api.nativeTranscriptSyncInFlight, sessionID)
		api.nativeTranscriptSyncMu.Unlock()
		return
	}
}

// syncWorkflowBuilderConversationFromNativeTranscript reconciles one existing
// workflow builder record, then updates its metadata index in the same pass.
// The returned supported value is false for providers without a transcript
// reader (see nativeTranscriptSyncSupportedProvider), avoiding needless
// retries for formats this package cannot parse.
func (api *StreamingAPI) syncWorkflowBuilderConversationFromNativeTranscript(ctx context.Context, userID, sessionID, workspacePath string) (changed, supported bool) {
	if strings.TrimSpace(userID) == "" {
		return false, false
	}
	raw, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		return false, true
	}
	if !claudeNativeTranscriptSyncSupported(raw) {
		return false, false
	}
	var ownership struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(raw, &ownership) != nil {
		return false, true
	}
	owner := strings.TrimSpace(ownership.UserID)
	if owner == "" {
		owner = "default"
	}
	if owner != userID || (ownership.SessionID != "" && ownership.SessionID != sessionID) {
		return false, false
	}
	conversationPath, found, err := findWorkflowBuilderConversationPathForSession(ctx, userID, sessionID, workspacePath)
	if err != nil || !found || strings.TrimSpace(conversationPath) == "" {
		return false, true
	}

	var current builderConversationLog
	if err := json.Unmarshal(raw, &current); err != nil {
		return false, true
	}
	refreshed := api.refreshLatestBuilderConversationFromNativeTranscript(ctx, conversationPath, string(raw), current)
	if builderConversationHistoriesEqual(refreshed.ConversationHistory, current.ConversationHistory) && refreshed.UpdatedAt == current.UpdatedAt {
		// No canonical history changed, so there is no newly recovered reply to
		// publish. Replaying a recent canonical window here is unsafe after an
		// EventStore restart: its empty in-memory counts make historical replies
		// look new and duplicate them in both the open chat and durable UI trace.
		// The browser restores old replies from conversation_history directly.
		// Keep changed=false so the completion scheduler still performs its
		// short retries while the current provider answer flushes.
		return false, true
	}

	// refreshLatest... writes the full record while preserving runtime and other
	// opaque fields. Re-read that canonical result before rebuilding the index.
	// If the write failed, do not advertise a transcript we did not persist.
	persistedRaw, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil || len(persistedRaw) == 0 {
		return false, true
	}
	var persistedRecord map[string]interface{}
	if err := json.Unmarshal(persistedRaw, &persistedRecord); err != nil {
		return false, true
	}
	var persistedHistory []llmtypes.MessageContent
	if history, ok := persistedRecord["conversation_history"]; ok {
		encoded, err := json.Marshal(history)
		if err != nil || json.Unmarshal(encoded, &persistedHistory) != nil {
			return false, true
		}
	}
	ownerID := stringFromRecord(persistedRecord, "user_id")
	if strings.TrimSpace(ownerID) == "" {
		ownerID = userID
	}
	if ownerID != userID {
		return false, false
	}
	if err := updatePersistedChatHistoryIndex(
		ownerID,
		sessionID,
		stringFromRecord(persistedRecord, "agent_mode"),
		persistedHistory,
		runtimeFromRecord(persistedRecord),
		conversationPath,
		int64(len(persistedRaw)),
		time.Now(),
		botMetadataFromRecord(persistedRecord),
	); err != nil {
		log.Printf("[CHAT_HISTORY] Native transcript sync: cannot update index for %s: %v", conversationPath, err)
	}
	api.publishOwnedNativeTranscriptRecoveredAssistantMessages(
		userID,
		sessionID,
		current.ConversationHistory,
		refreshed.ConversationHistory,
		readPersistedChatHistoryUIEvents(ctx, conversationPath),
	)
	return true, true
}

// publishNativeTranscriptRecoveredAssistantMessages closes the live-display
// half of transcript recovery. Persisting a provider-native reply makes it
// visible after refresh, but an already-open Chat only observes EventStore.
// Publish each recovered assistant message as the same whole-message
// transcript chunk used by normal CLI streaming. This deliberately is not a
// second completion event: a completion would settle the next queued retained
// turn when several user messages were submitted together.
func (api *StreamingAPI) publishNativeTranscriptRecoveredAssistantMessages(sessionID string, current, refreshed []builderConversationMessage, durableUIEvents []storeevents.Event) int {
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
	now := time.Now()
	published := 0
	for index, message := range refreshed {
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

func claudeNativeTranscriptSyncSupported(raw []byte) bool {
	var record struct {
		Runtime claudeNativeTranscriptRuntime `json:"runtime"`
	}
	if json.Unmarshal(raw, &record) != nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(record.Runtime.Provider))
	if provider == "" && record.Runtime.AgentSessionHandle != nil && record.Runtime.AgentSessionHandle.Provider != nil {
		provider = strings.ToLower(strings.TrimSpace(record.Runtime.AgentSessionHandle.Provider.Provider))
	}
	return nativeTranscriptSyncSupportedProvider(provider)
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
		filtered = append(filtered, message)
	}
	return filtered
}

// refreshLatestBuilderConversationFromNativeTranscript catches a persisted
// builder conversation snapshot up with the coding CLI's own on-disk
// transcript (PLAT-178) -- Claude Code's project JSONL or Codex's rollout,
// per nativeTranscriptMessagesForRuntime.
//
// Best-effort throughout: any resolution failure (no runtime info, no
// matching transcript file, nothing new) returns log unchanged. A
// successful catch-up is also persisted back to path so later reads of the
// same file -- not just this one restore response -- see it too.
func (api *StreamingAPI) refreshLatestBuilderConversationFromNativeTranscript(ctx context.Context, path, rawContent string, conv builderConversationLog) builderConversationLog {
	lock := chatConversationMutex(path)
	lock.Lock()
	defer lock.Unlock()
	// Callers may have read before a concurrent live append finished. Re-read
	// inside the same critical section that protects the eventual replacement.
	latest, exists, readErr := readFileFromWorkspace(ctx, path)
	if readErr != nil || !exists {
		return conv
	}
	var latestConv builderConversationLog
	if json.Unmarshal([]byte(latest), &latestConv) != nil {
		return conv
	}
	// The caller authorized the original identity. Do not substitute a record
	// that changed owners/sessions between that read and this writer lock.
	originalOwner, latestOwner := strings.TrimSpace(conv.UserID), strings.TrimSpace(latestConv.UserID)
	if originalOwner == "" {
		originalOwner = "default"
	}
	if latestOwner == "" {
		latestOwner = "default"
	}
	if (conv.SessionID != "" && latestConv.SessionID != conv.SessionID) || latestOwner != originalOwner {
		return conv
	}
	rawContent, conv = latest, latestConv
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(rawContent), &record); err != nil {
		return conv
	}
	runtimeRaw, ok := record["runtime"]
	if !ok {
		return conv
	}
	runtimeBytes, err := json.Marshal(runtimeRaw)
	if err != nil {
		return conv
	}
	var runtime claudeNativeTranscriptRuntime
	if err := json.Unmarshal(runtimeBytes, &runtime); err != nil {
		return conv
	}

	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	nativeSessionID := strings.TrimSpace(runtime.ExternalSessionID)
	workingDir := ""
	if runtime.AgentSessionHandle != nil && runtime.AgentSessionHandle.Provider != nil {
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider))
		}
		if nativeSessionID == "" {
			nativeSessionID = strings.TrimSpace(runtime.AgentSessionHandle.Provider.NativeSessionID)
		}
		workingDir = strings.TrimSpace(runtime.AgentSessionHandle.Provider.WorkingDir)
	}
	// Do not use conv.UpdatedAt as a transcript cursor. The live-input path
	// advances updated_at when it persists each human message, but it cannot
	// persist the corresponding assistant reply. A later human message can
	// therefore advance updated_at past an earlier missing reply. Reading the
	// full native transcript and sequence-merging it is what recovers those
	// interleaved replies without duplicating the already-persisted humans.
	accountHome := ""
	if runtime.AgentSessionHandle != nil && runtime.AgentSessionHandle.ConnectionID != "" {
		userID := GetUserIDFromContext(ctx)
		if userID == "" {
			userID, _ = record["user_id"].(string)
		}
		keys, err := api.connectionAPIKeys(ctx, providerAccountScope{Principal: userID, WorkspacePath: path}, provider, runtime.AgentSessionHandle.ConnectionID)
		if err != nil {
			return conv
		}
		accountHome = keys.RuntimeEnvironment["HOME"]
	}
	nativeMessages, maxTimestamp, transcriptPath, ok, err := nativeTranscriptMessagesForRuntime(provider, nativeSessionID, workingDir, accountHome)
	if err != nil {
		log.Printf("[CHAT_HISTORY] Native transcript catch-up: failed to read %s transcript for %s: %v", provider, nativeSessionID, err)
		return conv
	}
	if !ok || len(nativeMessages) == 0 {
		return conv
	}

	originalConv := conv
	persistedHistory := conv.ConversationHistory
	originalHistory := persistedHistory
	if end := trimNativeTranscriptStaleTail(persistedHistory, nativeMessages); end < len(persistedHistory) {
		// Check the raw shape before removing text-only replay rows: the readable
		// projection intentionally omits tool metadata.
		if rawHistory, valid := builderConversationRawHistory(record); valid && len(rawHistory) == len(persistedHistory) {
			plain := true
			for _, raw := range rawHistory[end:] {
				var message map[string]json.RawMessage
				if json.Unmarshal(raw, &message) != nil {
					plain = false
					break
				}
				var parts []map[string]json.RawMessage
				if json.Unmarshal(message["Parts"], &parts) != nil || len(parts) != 1 || len(parts[0]) != 1 || parts[0]["Text"] == nil {
					plain = false
					break
				}
			}
			if plain {
				persistedHistory = persistedHistory[:end]
				record["conversation_history"] = rawHistory[:end]
			}
		}
	}
	// Report native additions relative to the repaired base. Comparing against
	// the original length can produce a misleading negative "missing message"
	// count when two stale replay rows are removed and one reply is restored.
	previousCount := len(persistedHistory)
	merged := mergeBuilderConversationHistory(persistedHistory, nativeMessages)
	if builderConversationHistoriesEqual(merged, originalHistory) {
		return conv
	}
	conv.ConversationHistory = merged
	if current := parseBuilderConversationUpdatedAt(conv.UpdatedAt); maxTimestamp.After(current) {
		conv.UpdatedAt = maxTimestamp.Format(time.RFC3339Nano)
	}

	// Keep the original raw entries for persisted messages. builderConversationLog
	// deliberately exposes only readable Role/Parts.Text fields; serializing it
	// wholesale would erase structured tool calls/results from the durable record
	// every time a retained live turn finishes.
	record["conversation_history"] = mergeBuilderConversationRecordHistory(record, persistedHistory, nativeMessages)
	record["updated_at"] = conv.UpdatedAt
	advanceChatConversationRevision(record)
	conv.Revision = record["revision"].(uint64)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return originalConv
	}
	if err := writeRawFileToWorkspace(ctx, path, string(encoded)); err != nil {
		log.Printf("[CHAT_HISTORY] Native transcript catch-up: merged %d missing message(s) from %s but failed to persist to %s: %v", len(merged)-previousCount, transcriptPath, path, err)
		return originalConv
	} else {
		if snapshotErr := writeChatHistoryResumeSnapshot(ctx, path, encoded); snapshotErr != nil {
			log.Printf("[CHAT_HISTORY] Native transcript catch-up: failed to update resume snapshot for %s: %v", path, snapshotErr)
		}
		log.Printf("[CHAT_HISTORY] Native transcript catch-up: merged %d missing message(s) into %s from %s (native transcript through %s)",
			len(merged)-previousCount, path, transcriptPath, maxTimestamp.Format(time.RFC3339))
	}

	return conv
}

// mergeBuilderConversationHistory returns a shortest practical union of the
// persisted and native message sequences. Persisted-only messages are kept;
// native-only messages (most importantly assistant replies omitted by the
// asynchronous live-input writer) are inserted at their native position; and
// matching live-input human messages are not appended a second time.
//
// Positions are indexed by a normalized role+text key, so matching is linear
// apart from binary searches and repeated identical messages retain their
// multiplicity in sequence order.
func mergeBuilderConversationHistory(persisted, native []builderConversationMessage) []builderConversationMessage {
	if len(persisted) == 0 {
		return append([]builderConversationMessage(nil), native...)
	}
	if len(native) == 0 {
		return append([]builderConversationMessage(nil), persisted...)
	}

	refs := builderConversationMergeRefs(persisted, native)
	merged := make([]builderConversationMessage, 0, len(refs))
	for _, ref := range refs {
		if ref.persisted {
			merged = append(merged, persisted[ref.index])
		} else {
			merged = append(merged, native[ref.index])
		}
	}
	return merged
}

// mergeBuilderConversationRecordHistory applies the same ordered union as the
// readable-message merge, but retains each persisted entry as raw JSON. Native
// messages are the only newly encoded entries. This preserves structured tool
// calls/results and provider metadata in the canonical conversation file.
func mergeBuilderConversationRecordHistory(record map[string]interface{}, persisted, native []builderConversationMessage) []json.RawMessage {
	rawHistory, ok := builderConversationRawHistory(record)
	if !ok || len(rawHistory) != len(persisted) {
		return marshalBuilderConversationMessages(mergeBuilderConversationHistory(persisted, native))
	}
	if len(native) == 0 {
		return rawHistory
	}

	refs := builderConversationMergeRefs(persisted, native)
	merged := make([]json.RawMessage, 0, len(refs))
	for _, ref := range refs {
		if ref.persisted {
			merged = append(merged, rawHistory[ref.index])
		} else {
			merged = append(merged, marshalBuilderConversationMessages([]builderConversationMessage{native[ref.index]})...)
		}
	}
	return merged
}

type builderConversationMergeRef struct {
	persisted bool
	index     int
}

// builderConversationMergeRefs computes a shortest common supersequence from
// the persisted and native transcripts. The earlier greedy first-anchor merge
// chose the first occurrence of repeated text; after a provider restart with a
// replayed history tail, that misaligned the entire tail and appended old
// assistant replies as new messages. LCS alignment maximizes reused messages
// and therefore preserves real repeats without manufacturing replay copies.
func builderConversationMergeRefs(persisted, native []builderConversationMessage) []builderConversationMergeRef {
	n, m := len(persisted), len(native)
	// Normalizing a message walks all of its text. This merge is an O(n*m)
	// dynamic-programming pass, so computing keys inside the matrix turned a
	// long transcript into O(n*m*message-size) work and allocation. Recovery
	// replays amplified that enough to saturate production CPUs. Materialize
	// each key once and keep the matrix comparisons constant-time.
	persistedKeys := builderConversationMessageKeys(persisted)
	nativeKeys := builderConversationMessageKeys(native)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if persistedKeys[i] == nativeKeys[j] {
				dp[i][j] = 1 + dp[i+1][j+1]
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	refs := make([]builderConversationMergeRef, 0, n+m-dp[0][0])
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i == n:
			refs = append(refs, builderConversationMergeRef{index: j})
			j++
		case j == m:
			refs = append(refs, builderConversationMergeRef{persisted: true, index: i})
			i++
		case persistedKeys[i] == nativeKeys[j]:
			refs = append(refs, builderConversationMergeRef{persisted: true, index: i})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			refs = append(refs, builderConversationMergeRef{persisted: true, index: i})
			i++
		default:
			refs = append(refs, builderConversationMergeRef{index: j})
			j++
		}
	}
	return refs
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

func marshalBuilderConversationMessages(messages []builderConversationMessage) []json.RawMessage {
	encoded := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		messageJSON, err := json.Marshal(message)
		if err == nil {
			encoded = append(encoded, messageJSON)
		}
	}
	return encoded
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

func builderConversationMessageKeys(messages []builderConversationMessage) []string {
	keys := make([]string, len(messages))
	for index := range messages {
		keys[index] = builderConversationMessageKey(messages[index])
	}
	return keys
}

func builderConversationHistoriesEqual(left, right []builderConversationMessage) bool {
	if len(left) != len(right) {
		return false
	}
	leftKeys := builderConversationMessageKeys(left)
	rightKeys := builderConversationMessageKeys(right)
	for index := range leftKeys {
		if leftKeys[index] != rightKeys[index] {
			return false
		}
	}
	return true
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

// Startup recovery may run before a browser restores a session. Persist its
// history, but publish live events only to an already registered matching owner.
func (api *StreamingAPI) publishOwnedNativeTranscriptRecoveredAssistantMessages(owner, session string, current, refreshed []builderConversationMessage, durableUIEvents []storeevents.Event) int {
	if api == nil || api.eventStore == nil || owner == "" || api.eventStore.GetSessionOwner(session) != owner {
		return 0
	}
	return api.publishNativeTranscriptRecoveredAssistantMessages(session, current, refreshed, durableUIEvents)
}
