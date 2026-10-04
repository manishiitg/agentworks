package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ChatHistorySession is the metadata returned by the list endpoint.
type ChatHistorySession struct {
	SessionID        string                      `json:"session_id"`
	Title            string                      `json:"title,omitempty"`
	AgentMode        string                      `json:"agent_mode"`
	Runtime          *ChatHistoryAgentRuntime    `json:"runtime,omitempty"`
	WorkshopMode     string                      `json:"workshop_mode,omitempty"`
	Status           string                      `json:"status"`
	Query            string                      `json:"query,omitempty"`
	UserID           string                      `json:"user_id"`
	Username         string                      `json:"username,omitempty"`
	CanResume        bool                        `json:"can_resume"`
	CanDelete        bool                        `json:"can_delete"`
	WorkspacePath    string                      `json:"workspace_path,omitempty"`
	ConversationPath string                      `json:"conversation_path"`
	BotPlatform      string                      `json:"bot_platform,omitempty"`
	BotChannelID     string                      `json:"bot_channel_id,omitempty"`
	BotThreadTS      string                      `json:"bot_thread_ts,omitempty"`
	BotUserID        string                      `json:"bot_user_id,omitempty"`
	BotUserName      string                      `json:"bot_user_name,omitempty"`
	BotUserEmail     string                      `json:"bot_user_email,omitempty"`
	CreatedAt        string                      `json:"created_at"`
	UpdatedAt        string                      `json:"updated_at"`
	MessageCount     int                         `json:"message_count"`
	PreviewMessages  []ChatHistoryPreviewMessage `json:"preview_messages,omitempty"`
	// attributionVerified is internal list metadata. It prevents the legacy
	// attribution repair from reopening a complete transcript after the index
	// has already confirmed that "default" really does mean legacy data.
	attributionVerified bool
}

// ChatHistoryAgentRuntime records enough information to reopen a previous chat
// with its original runtime when that runtime supports native resume.
type ChatHistoryAgentRuntime struct {
	ChatPolicyKey string `json:"chat_policy_key,omitempty"`
	// ChatPolicyRoleKey is the role part of ChatPolicyKey (mode, origin,
	// capabilities) without definition/config drift. Only a role change may
	// replace the native coding-agent session.
	ChatPolicyRoleKey string `json:"chat_policy_role_key,omitempty"`
	AgentProfileKey   string `json:"agent_profile_key,omitempty"`
	// AgentToolsMode is the profile's agent_tools mode ("full", or "mcp_only";
	// older sessions may say "hybrid", read as full) the native session was
	// started with.
	AgentToolsMode     string                       `json:"agent_tools_mode,omitempty"`
	Kind               string                       `json:"kind,omitempty"`
	Provider           string                       `json:"provider,omitempty"`
	ModelID            string                       `json:"model_id,omitempty"`
	Transport          string                       `json:"transport,omitempty"`
	ExternalSessionID  string                       `json:"external_session_id,omitempty"`
	ResumeSupported    bool                         `json:"resume_supported"`
	ResumeFlag         string                       `json:"resume_flag,omitempty"`
	ProjectDirID       string                       `json:"project_dir_id,omitempty"`
	WorkspacePath      string                       `json:"workspace_path,omitempty"`
	WorkshopMode       string                       `json:"workshop_mode,omitempty"`
	CapturedAt         string                       `json:"captured_at,omitempty"`
	AgentSessionHandle *mcpagent.AgentSessionHandle `json:"agent_session_handle,omitempty"`
	// Dynamic prompts and resolved browser availability are intentionally not
	// persisted. A restored dead terminal is launched only after /api/query has
	// rebuilt current instructions and capabilities from the workflow manifest.
	// Persisting these values previously let a stale mode=headless snapshot
	// overwrite live CDP availability during native-session resume.
	ServerName    string   `json:"server_name,omitempty"`
	SelectedTools []string `json:"selected_tools,omitempty"`
}

type ChatHistoryPreviewMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type ChatHistoryBotMetadata struct {
	Platform  string `json:"bot_platform,omitempty"`
	ChannelID string `json:"bot_channel_id,omitempty"`
	ThreadTS  string `json:"bot_thread_ts,omitempty"`
	UserID    string `json:"bot_user_id,omitempty"`
	UserName  string `json:"bot_user_name,omitempty"`
	UserEmail string `json:"bot_user_email,omitempty"`
}

func chatHistoryBotMetadataFromQuery(req QueryRequest) *ChatHistoryBotMetadata {
	return normalizeChatHistoryBotMetadata(&ChatHistoryBotMetadata{
		Platform:  req.BotPlatform,
		ChannelID: req.BotChannelID,
		ThreadTS:  req.BotThreadTS,
		UserID:    req.BotUserID,
		UserName:  req.BotUserName,
		UserEmail: req.BotUserEmail,
	})
}

func normalizeChatHistoryBotMetadata(meta *ChatHistoryBotMetadata) *ChatHistoryBotMetadata {
	if meta == nil {
		return nil
	}
	normalized := &ChatHistoryBotMetadata{
		Platform:  strings.TrimSpace(meta.Platform),
		ChannelID: strings.TrimSpace(meta.ChannelID),
		ThreadTS:  strings.TrimSpace(meta.ThreadTS),
		UserID:    strings.TrimSpace(meta.UserID),
		UserName:  strings.TrimSpace(meta.UserName),
		UserEmail: strings.TrimSpace(meta.UserEmail),
	}
	if normalized.Platform == "" && normalized.ChannelID == "" && normalized.ThreadTS == "" &&
		normalized.UserID == "" && normalized.UserName == "" && normalized.UserEmail == "" {
		return nil
	}
	return normalized
}

func applyChatHistoryBotMetadata(record map[string]interface{}, meta *ChatHistoryBotMetadata) {
	meta = normalizeChatHistoryBotMetadata(meta)
	if record == nil || meta == nil {
		return
	}
	if meta.Platform != "" {
		record["bot_platform"] = meta.Platform
	}
	if meta.ChannelID != "" {
		record["bot_channel_id"] = meta.ChannelID
	}
	if meta.ThreadTS != "" {
		record["bot_thread_ts"] = meta.ThreadTS
	}
	if meta.UserID != "" {
		record["bot_user_id"] = meta.UserID
	}
	if meta.UserName != "" {
		record["bot_user_name"] = meta.UserName
	}
	if meta.UserEmail != "" {
		record["bot_user_email"] = meta.UserEmail
	}
}

func stringOrEmpty(meta *ChatHistoryBotMetadata, pick func(*ChatHistoryBotMetadata) string) string {
	meta = normalizeChatHistoryBotMetadata(meta)
	if meta == nil || pick == nil {
		return ""
	}
	return strings.TrimSpace(pick(meta))
}

type ChatHistoryCleanupResult struct {
	DeletedCount int      `json:"deleted_count"`
	DeletedPaths []string `json:"deleted_paths"`
	Cutoff       string   `json:"cutoff"`
	Scope        string   `json:"scope"`
}

type restoredChatHistoryPersistTarget struct {
	SessionID        string
	ConversationPath string
	History          []llmtypes.MessageContent
	Runtime          *ChatHistoryAgentRuntime
	WorkshopMode     string
}

// resolvedResumeTarget is the single server-owned description of a saved chat
// being resumed. Product routes resolve it once from the authenticated project
// and carry it through the shared runner without asking later stages to find the
// same conversation again from a path, workspace, or session id.
type resolvedResumeTarget struct {
	SessionID        string
	WorkspacePath    string
	ConversationPath string
	History          []llmtypes.MessageContent
	Runtime          *ChatHistoryAgentRuntime
	WorkshopMode     string
}

func (target *resolvedResumeTarget) persistTarget() *restoredChatHistoryPersistTarget {
	if target == nil {
		return nil
	}
	return &restoredChatHistoryPersistTarget{
		SessionID:        target.SessionID,
		ConversationPath: target.ConversationPath,
		History:          target.History,
		Runtime:          target.Runtime,
		WorkshopMode:     target.WorkshopMode,
	}
}

func (target *resolvedResumeTarget) provider() string {
	if target == nil || target.Runtime == nil {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(target.Runtime.Provider))
	if provider == "" && target.Runtime.AgentSessionHandle != nil {
		provider = strings.ToLower(strings.TrimSpace(target.Runtime.AgentSessionHandle.Provider.Provider))
	}
	return provider
}

func (target *resolvedResumeTarget) crossesProvider(currentProvider string) bool {
	savedProvider := target.provider()
	currentProvider = strings.ToLower(strings.TrimSpace(currentProvider))
	return savedProvider != "" && currentProvider != "" && savedProvider != currentProvider
}

func (target *resolvedResumeTarget) canPersist(currentWorkshopMode string) bool {
	if target == nil || strings.TrimSpace(target.SessionID) == "" || strings.TrimSpace(target.ConversationPath) == "" {
		return false
	}
	restoredMode := target.WorkshopMode
	if target.Runtime != nil {
		restoredMode = firstNonEmptyTrimmed(target.Runtime.WorkshopMode, restoredMode)
	}
	return chatHistoryResumeModesCompatible(restoredMode, currentWorkshopMode)
}

const (
	maxPersistedChatHistoryUIEvents = 200
	maxChatHistoryFallbackScan      = 1000
	// Ceiling for reading a persisted transcript back, NOT for writing one.
	//
	// These were briefly applied to the durable write path, which deletes the
	// oldest messages on every save instead of withholding them. The conversation
	// JSON is the canonical record: for a coding CLI the provider also keeps a
	// rollout/transcript, but an API-backed chat has nothing behind this file.
	// Bounding what is loaded or sent is lossless; bounding what is stored is not.
	//
	// The real hazard — an unbounded transcript becoming a prompt payload — is
	// handled by the fallback limits below, at the one call site that builds
	// prompt context.
	maxPersistedChatHistoryMessages = 1000
	maxPersistedChatHistoryBytes    = 4 * 1024 * 1024
	// A provider without a native continuation handle gets only recent context.
	// Cursor's structured CLI receives that context inside one positional argv
	// value. Linux limits a single argument to roughly 128 KiB even when
	// ARG_MAX is much larger, so leave ample room for the system prompt and the
	// current user message.
	maxCodingAgentFallbackMessages    = 48
	maxCodingAgentFallbackBytes       = 48 * 1024
	maxChatHistoryTerminalSnapshots   = 1
	maxChatHistoryTerminalBytes       = 512 * 1024
	maxChatHistoryTerminalLines       = 10000
	chatHistoryIndexVersion           = 1
	chatHistoryIndexFileName          = "chat-index.json"
	chatHistoryResumeSnapshotTurns    = 10
	maxChatHistoryResumeSnapshotBytes = 2 * 1024 * 1024
	maxChatHistoryResumeMessageBytes  = 128 * 1024
)

type chatHistoryIndex struct {
	Version   int                              `json:"version"`
	Complete  bool                             `json:"complete"`
	UpdatedAt string                           `json:"updated_at"`
	Entries   map[string]chatHistoryIndexEntry `json:"entries"`
}

type chatHistoryIndexEntry struct {
	Session                  ChatHistorySession `json:"session"`
	SourceSize               int64              `json:"source_size"`
	SourceModifiedAtUnixNano int64              `json:"source_modified_at_unix_nano,omitempty"`
	AttributionVerified      bool               `json:"attribution_verified,omitempty"`
}

var chatHistoryIndexLocks [64]sync.Mutex

// chatHistoryRoot returns the workspace-relative path to a user's chat_history root.
func chatHistoryRoot(userID string) string {
	return workspaceref.PhysicalPath(userID, "chat_history")
}

func chatHistoryConversationFileName(sessionID string) string {
	return fmt.Sprintf("session-%s-conversation.json", sanitizeChatHistorySessionID(sessionID))
}

func chatHistoryConversationDate(t time.Time) string {
	return t.Format("2006-01-02")
}

func chatHistoryConversationPath(userID, sessionID string, t time.Time) string {
	return pathpkg.Join(chatHistoryRoot(userID), chatHistoryConversationDate(t), chatHistoryConversationFileName(sessionID))
}

// chatHistoryResumeSnapshotPath returns a compact sibling that is safe to read
// during UI startup. The complete conversation remains the durable source of
// truth; this file is only a rebuildable projection of its newest turns.
func chatHistoryResumeSnapshotPath(conversationPath string) string {
	conversationPath = strings.TrimSpace(conversationPath)
	if conversationPath == "" {
		return ""
	}
	const suffix = "-conversation.json"
	if strings.HasSuffix(conversationPath, suffix) {
		return strings.TrimSuffix(conversationPath, suffix) + "-resume.json"
	}
	if pathpkg.Base(conversationPath) == "conversation.json" {
		return strings.TrimSuffix(conversationPath, "conversation.json") + "resume.json"
	}
	return conversationPath + ".resume.json"
}

func writeChatHistoryResumeSnapshot(ctx context.Context, conversationPath string, data []byte) error {
	snapshotPath := chatHistoryResumeSnapshotPath(conversationPath)
	if snapshotPath == "" || len(data) == 0 {
		return nil
	}
	projected := projectChatHistoryConversationForResumePage(data, chatHistoryResumeSnapshotTurns, 0)
	projected = boundChatHistoryResumeSnapshot(projected)
	return writeRawFileToWorkspace(ctx, snapshotPath, string(projected))
}

// boundChatHistoryResumeSnapshot makes the UI transfer contract independent of
// tool-result size. Prefer the formatted trace when it fits; otherwise retain
// readable recent messages and pagination metadata under a strict 2 MiB cap.
func boundChatHistoryResumeSnapshot(data []byte) []byte {
	if len(data) <= maxChatHistoryResumeSnapshotBytes {
		return data
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	delete(doc, "ui_events")
	delete(doc, "terminal_snapshots")
	if encoded, err := json.Marshal(doc); err == nil && len(encoded) <= maxChatHistoryResumeSnapshotBytes {
		return encoded
	}

	var history []json.RawMessage
	if json.Unmarshal(doc["conversation_history"], &history) != nil {
		return nil
	}
	messageBudget := maxChatHistoryResumeMessageBytes
	if len(history) > 0 {
		const totalMessageBudget = 1536 * 1024
		if fairShare := totalMessageBudget / len(history); fairShare < messageBudget {
			messageBudget = fairShare
		}
		if messageBudget < 4096 {
			messageBudget = 4096
		}
	}
	compact := make([]json.RawMessage, 0, len(history))
	for _, raw := range history {
		role, message := chatHistoryMessageRoleAndText(raw)
		if message == "" {
			continue
		}
		if len(message) > messageBudget {
			message = truncateUTF8Bytes(message, messageBudget) + "\n\n[Long message truncated for chat restore]"
		}
		minimal, err := json.Marshal(map[string]interface{}{
			"Role":  role,
			"Parts": []map[string]string{{"Text": message}},
		})
		if err == nil {
			compact = append(compact, minimal)
		}
	}
	encodedHistory, err := json.Marshal(compact)
	if err != nil {
		return nil
	}
	doc["conversation_history"] = encodedHistory
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	if len(encoded) <= maxChatHistoryResumeSnapshotBytes {
		return encoded
	}
	// Individually bounded messages can still exceed the total budget.
	// Drop the oldest until the complete response fits; those turns remain in
	// the canonical archive and can be requested explicitly.
	for len(compact) > 1 {
		compact = compact[1:]
		encodedHistory, _ = json.Marshal(compact)
		doc["conversation_history"] = encodedHistory
		encoded, err = json.Marshal(doc)
		if err == nil && len(encoded) <= maxChatHistoryResumeSnapshotBytes {
			return encoded
		}
	}
	return encoded
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func ReadChatHistoryResumeSnapshot(userID, sessionID, workspacePath string) (json.RawMessage, bool, error) {
	conversationPath, found, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath)
	if err != nil || !found {
		return nil, false, err
	}
	snapshotPath := chatHistoryResumeSnapshotPath(conversationPath)
	data, exists, err := readFileFromWorkspace(context.Background(), snapshotPath)
	if err != nil || !exists {
		return nil, false, err
	}
	return json.RawMessage(data), true, nil
}

// workProjectChatHistoryConversationPath keeps Work's Builder transcripts in
// the project, matching AgentWorks' workspace-owned conversation layout. Other
// product chats continue to use the user's central chat_history directory.
func workProjectChatHistoryConversationPath(userID, workspacePath, sessionID string, t time.Time) (string, bool) {
	canonicalWorkspace := canonicalChatHistoryWorkspacePath(userID, workspacePath)
	if !isProjectWorkspacePath(canonicalWorkspace) {
		return "", false
	}

	userWorkspacePath := workspaceref.PhysicalPath(userID, canonicalWorkspace)
	return workflowBuilderConversationLogPath(userWorkspacePath, sanitizeChatHistorySessionID(sessionID), t), true
}

// copyChatHistoryConversationIfNeeded seeds a project-local transcript from a
// legacy central transcript before the next save. Leaving the legacy file in
// place provides a rollback-compatible fallback while all subsequent writes go
// to the project-owned copy.
func copyChatHistoryConversationIfNeeded(sourcePath, destinationPath string) error {
	sourcePath = strings.Trim(strings.TrimSpace(sourcePath), "/")
	destinationPath = strings.Trim(strings.TrimSpace(destinationPath), "/")
	if sourcePath == "" || destinationPath == "" || sourcePath == destinationPath {
		return nil
	}
	lock := chatConversationMutex(destinationPath)
	lock.Lock()
	defer lock.Unlock()
	if _, exists, err := readFileFromWorkspace(context.Background(), destinationPath); err != nil {
		return err
	} else if exists {
		return nil
	}
	content, exists, err := readFileFromWorkspace(context.Background(), sourcePath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return writeRawFileToWorkspace(context.Background(), destinationPath, content)
}

func (api *StreamingAPI) persistChatConversationToPathWithTerminalSession(sessionID, terminalSnapshotSessionID, agentMode, userID string, persistedHistory []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime, uiEvents []internalevents.Event, conversationPath string, botMetaArg ...*ChatHistoryBotMetadata) {
	if len(persistedHistory) == 0 {
		return
	}
	if userID == "" {
		userID = "default"
	}
	logCtx := newServerLogContext("", "", agentMode, userID, "", sessionID)

	now := time.Now()
	var botMeta *ChatHistoryBotMetadata
	if len(botMetaArg) > 0 {
		botMeta = botMetaArg[0]
	}
	convData := map[string]interface{}{
		"session_id":           sessionID,
		"user_id":              userID,
		"username":             chatHistoryUsername(userID, ""),
		"agent_mode":           agentMode,
		"conversation_history": persistedHistory,
		"updated_at":           now.Format(time.RFC3339),
	}
	if runtime != nil {
		convData["runtime"] = runtime
	}
	applyChatHistoryBotMetadata(convData, botMeta)
	snapshotSessionID := strings.TrimSpace(terminalSnapshotSessionID)
	if snapshotSessionID == "" {
		snapshotSessionID = sessionID
	}
	if terminalSnapshots := api.captureChatHistoryTerminalSnapshots(snapshotSessionID, runtime); len(terminalSnapshots) > 0 {
		convData["terminal_snapshots"] = terminalSnapshots
	}
	convPath := strings.TrimSpace(conversationPath)
	if convPath == "" {
		convPath = chatHistoryConversationPath(userID, sessionID, now)
	}
	lock := chatConversationMutex(convPath)
	lock.Lock()
	defer lock.Unlock()
	// A user-defined title belongs to the durable conversation. Preserve it
	// when a later turn rewrites the transcript from the in-memory history.
	if existing, exists, readErr := readFileFromWorkspace(context.Background(), convPath); readErr != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Refusing overwrite after failed read %s: %v", convPath, readErr)
		return
	} else if exists {
		var previous map[string]interface{}
		var previousConv, incomingConv builderConversationLog
		incoming, _ := json.Marshal(convData)
		if json.Unmarshal([]byte(existing), &previous) != nil || json.Unmarshal([]byte(existing), &previousConv) != nil || json.Unmarshal(incoming, &incomingConv) != nil {
			return
		}
		if owner := stringFromRecord(previous, "user_id"); owner != "" && owner != userID {
			return
		}
		merged, err := mergeChatConversationSnapshots(previous, convData)
		if err != nil {
			logfWithContext(logCtx, "[CHAT_HISTORY] Refusing invalid snapshot merge for %s: %v", convPath, err)
			return
		}
		encoded, err := json.Marshal(merged)
		if err != nil || json.Unmarshal(encoded, &persistedHistory) != nil {
			return
		}
		convData["conversation_history"] = persistedHistory
		// Preserve metadata unknown to this writer, including pending submissions.
		for key, value := range previous {
			if _, exists := convData[key]; !exists {
				convData[key] = value
			}
		}
	}
	// A Work tab can be named as soon as the backend assigns its session ID,
	// before the first agent turn has produced a transcript. In that window the
	// rename endpoint records the title in the durable project index. Apply it to
	// the first transcript so the transcript remains authoritative thereafter.
	if _, titled := convData["title"]; !titled {
		if title := chatHistoryTitleFromPersistedIndex(userID, sessionID, convPath); title != "" {
			convData["title"] = title
		}
	}

	// The in-memory event store only knows about turns since this process
	// started. Writing it alone after a restart replaced the whole saved trace
	// with the newest turn, so a reopened chat lost its tool calls and the
	// restore-time ordering of older messages collapsed into that one turn.
	uiEvents = trimChatHistoryUIEvents(mergeChatHistoryUIEvents(readPersistedChatHistoryUIEvents(context.Background(), convPath), uiEvents))
	if len(uiEvents) > 0 {
		convData["ui_events"] = uiEvents
	}

	advanceChatConversationRevision(convData)
	convJSON, err := json.MarshalIndent(convData, "", "  ")
	if err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Failed to marshal conversation for %s: %v", sessionID, err)
		return
	}
	// A rebuild that has fewer user turns than the record on disk is a partial
	// one, and writing it destroys the fuller history (salesoutreach lost 242
	// user turns to 2 this way after a restart).
	if lossy, existingTurns, nextTurns := conversationOverwriteWouldLoseTurns(context.Background(), convPath, persistedHistory); lossy {
		refuseConversationOverwrite(convPath, existingTurns, nextTurns)
		return
	}
	if err := writeRawFileToWorkspace(context.Background(), convPath, string(convJSON)); err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Failed to write %s: %v", convPath, err)
		return
	}
	if err := writeChatHistoryResumeSnapshot(context.Background(), convPath, convJSON); err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Failed to write resume snapshot for %s: %v", convPath, err)
	}
	if err := updatePersistedChatHistoryIndex(userID, sessionID, agentMode, persistedHistory, runtime, convPath, int64(len(convJSON)), now, botMeta); err != nil {
		// The transcript remains the source of truth. A missing index entry is
		// repaired by the compatibility backfill on the next history listing.
		logfWithContext(logCtx, "[CHAT_HISTORY] Failed to update metadata index for %s: %v", convPath, err)
	}

	logfWithContext(logCtx, "[CHAT_HISTORY] Saved conversation (%d messages) to %s", len(persistedHistory), convPath)
}

func updatePersistedChatHistoryIndex(userID, sessionID, agentMode string, history []llmtypes.MessageContent, runtime *ChatHistoryAgentRuntime, conversationPath string, sourceSize int64, now time.Time, botMeta *ChatHistoryBotMetadata) error {
	return updatePersistedChatHistoryIndexWithCount(userID, sessionID, agentMode, history, len(history), runtime, conversationPath, sourceSize, now, botMeta)
}

// updatePersistedChatHistoryIndexWithCount indexes a conversation from a
// (possibly recent-only) slice of its history and its true message count.
func updatePersistedChatHistoryIndexWithCount(userID, sessionID, agentMode string, history []llmtypes.MessageContent, messageCount int, runtime *ChatHistoryAgentRuntime, conversationPath string, sourceSize int64, now time.Time, botMeta *ChatHistoryBotMetadata) error {
	indexPath := chatHistoryIndexWorkspacePath(userID, conversationPath)
	if indexPath == "" {
		return fmt.Errorf("cannot derive chat index path from %q", conversationPath)
	}

	mutex := chatHistoryIndexMutex(indexPath)
	mutex.Lock()
	defer mutex.Unlock()

	index := newChatHistoryIndex()
	if data, exists, err := readFileFromWorkspace(context.Background(), indexPath); err != nil {
		return err
	} else if exists {
		if err := json.Unmarshal([]byte(data), &index); err != nil {
			return fmt.Errorf("decode %s: %w", indexPath, err)
		}
		index.normalize()
	}

	createdAt := now.Format(time.RFC3339)
	title := ""
	if existing, ok := index.Entries[conversationPath]; ok {
		if strings.TrimSpace(existing.Session.CreatedAt) != "" {
			createdAt = existing.Session.CreatedAt
		}
		title = strings.TrimSpace(existing.Session.Title)
	}
	// A resumed conversation can move to a new date-bucket path while keeping
	// the same durable session ID. Custom titles belong to that session, not to
	// one dated file, so inherit the title from any older index entry.
	if title == "" {
		title = chatHistoryIndexTitleForSession(index, sessionID)
	}
	workshopMode := ""
	if runtime != nil {
		workshopMode = normalizeChatHistoryWorkshopMode(runtime.WorkshopMode)
	}
	query := latestHumanText(history)
	if len(query) > 200 {
		query = query[:200] + "..."
	}
	index.Entries[conversationPath] = chatHistoryIndexEntry{
		Session: ChatHistorySession{
			SessionID:        sessionID,
			Title:            title,
			AgentMode:        agentMode,
			Runtime:          runtime,
			WorkshopMode:     workshopMode,
			Status:           "completed",
			Query:            query,
			UserID:           userID,
			Username:         chatHistoryUsername(userID, ""),
			WorkspacePath:    chatHistoryWorkspacePathFromConversation(conversationPath),
			ConversationPath: conversationPath,
			BotPlatform:      stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.Platform }),
			BotChannelID:     stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.ChannelID }),
			BotThreadTS:      stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.ThreadTS }),
			BotUserID:        stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserID }),
			BotUserName:      stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserName }),
			BotUserEmail:     stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserEmail }),
			CreatedAt:        createdAt,
			UpdatedAt:        now.Format(time.RFC3339),
			MessageCount:     messageCount,
			PreviewMessages:  chatHistoryPreviewMessages(history),
		},
		SourceSize:          sourceSize,
		AttributionVerified: true,
	}
	index.Version = chatHistoryIndexVersion
	index.UpdatedAt = now.Format(time.RFC3339)

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", indexPath, err)
	}
	return writeRawFileToWorkspace(context.Background(), indexPath, string(data))
}

func chatHistoryIndexTitleForSession(index chatHistoryIndex, sessionID string) string {
	sessionID = sanitizeChatHistorySessionID(sessionID)
	if sessionID == "" {
		return ""
	}
	title := ""
	updatedAt := ""
	for _, entry := range index.Entries {
		if sanitizeChatHistorySessionID(entry.Session.SessionID) != sessionID {
			continue
		}
		candidate := strings.TrimSpace(entry.Session.Title)
		if candidate == "" {
			continue
		}
		if title == "" || entry.Session.UpdatedAt > updatedAt {
			title = candidate
			updatedAt = entry.Session.UpdatedAt
		}
	}
	return title
}

func chatHistoryTitleFromPersistedIndex(userID, sessionID, conversationPath string) string {
	indexPath := chatHistoryIndexWorkspacePath(userID, conversationPath)
	if indexPath == "" {
		return ""
	}
	mutex := chatHistoryIndexMutex(indexPath)
	mutex.Lock()
	defer mutex.Unlock()
	data, exists, err := readFileFromWorkspace(context.Background(), indexPath)
	if err != nil || !exists {
		return ""
	}
	index := newChatHistoryIndex()
	if json.Unmarshal([]byte(data), &index) != nil {
		return ""
	}
	index.normalize()
	return chatHistoryIndexTitleForSession(index, sessionID)
}

func preserveChatHistorySessionTitle(index *chatHistoryIndex, session *ChatHistorySession) bool {
	if index == nil || session == nil || strings.TrimSpace(session.Title) != "" {
		return false
	}
	title := chatHistoryIndexTitleForSession(*index, session.SessionID)
	if title == "" {
		return false
	}
	session.Title = title
	entry, ok := index.Entries[session.ConversationPath]
	if ok {
		entry.Session.Title = title
		index.Entries[session.ConversationPath] = entry
		index.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return true
}

func newChatHistoryIndex() chatHistoryIndex {
	return chatHistoryIndex{
		Version: chatHistoryIndexVersion,
		Entries: make(map[string]chatHistoryIndexEntry),
	}
}

func (index *chatHistoryIndex) normalize() {
	if index.Version == 0 {
		index.Version = chatHistoryIndexVersion
	}
	if index.Entries == nil {
		index.Entries = make(map[string]chatHistoryIndexEntry)
	}
}

func chatHistoryIndexMutex(indexPath string) *sync.Mutex {
	var hash uint64 = 1469598103934665603
	for i := 0; i < len(indexPath); i++ {
		hash ^= uint64(indexPath[i])
		hash *= 1099511628211
	}
	return &chatHistoryIndexLocks[hash%uint64(len(chatHistoryIndexLocks))]
}

func chatHistoryIndexWorkspacePath(userID, conversationPath string) string {
	conversationPath = strings.Trim(pathpkg.Clean(filepath.ToSlash(conversationPath)), "/")
	if marker := strings.Index(conversationPath, "/builder/"); marker > 0 {
		return pathpkg.Join(conversationPath[:marker], "builder", "conversation", chatHistoryIndexFileName)
	}
	root := chatHistoryRoot(userID)
	if conversationPath == root || strings.HasPrefix(conversationPath, root+"/") {
		return pathpkg.Join(root, chatHistoryIndexFileName)
	}
	return ""
}

func chatHistoryWorkspacePathFromConversation(conversationPath string) string {
	conversationPath = strings.Trim(pathpkg.Clean(filepath.ToSlash(conversationPath)), "/")
	if marker := strings.Index(conversationPath, "/builder/"); marker > 0 {
		return conversationPath[:marker]
	}
	return ""
}

var captureChatHistoryTerminalPaneLines = captureTerminalPaneLines

func (api *StreamingAPI) captureChatHistoryTerminalSnapshots(sessionID string, runtime *ChatHistoryAgentRuntime) []terminals.Snapshot {
	sessionID = strings.TrimSpace(sessionID)
	if api == nil || sessionID == "" {
		return nil
	}

	candidates := make([]terminals.Snapshot, 0)
	// A live-attach stream is the only snapshot that contains the raw terminal
	// history accumulated by xterm. Prefer it over a late capture-pane read: for
	// alternate-screen CLIs tmux commonly has history_size=0, so that late read
	// contains only the final viewport and makes retained Raw mode unscrollable.
	if snapshot, ok := api.captureChatHistoryStoredTmuxStreamSnapshot(sessionID, runtime); ok {
		candidates = append(candidates, snapshot)
	}
	if len(candidates) == 0 {
		if snapshot, ok := api.captureChatHistoryRuntimeTmuxSnapshot(sessionID, runtime); ok {
			candidates = append(candidates, snapshot)
		}
	}
	if len(candidates) == 0 && api.terminalStore != nil {
		stored := api.terminalStore.List(sessionID)
		// Prefer tmux-backed panes (workflow coding-agent steps run over a tmux
		// transport, so their snapshots are recapturable on restore). When no
		// tmux pane exists — e.g. older non-tmux terminal records or API-backed
		// panes with no tmux_session — fall back to any terminal pane. Without
		// this fallback the last capture is dropped at save time and the
		// restored terminal pane comes up empty after a server restart.
		candidates = collectChatHistoryTerminalSnapshots(stored, true)
		if len(candidates) == 0 {
			candidates = collectChatHistoryTerminalSnapshots(stored, false)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		iMain := chatHistoryTerminalSnapshotIsMainAgent(candidates[i])
		jMain := chatHistoryTerminalSnapshotIsMainAgent(candidates[j])
		if iMain != jMain {
			return iMain
		}
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})
	if len(candidates) > maxChatHistoryTerminalSnapshots {
		candidates = candidates[:maxChatHistoryTerminalSnapshots]
	}
	return candidates
}

func (api *StreamingAPI) captureChatHistoryStoredTmuxStreamSnapshot(sessionID string, runtime *ChatHistoryAgentRuntime) (terminals.Snapshot, bool) {
	if api == nil || api.terminalStore == nil {
		return terminals.Snapshot{}, false
	}
	wantedTmux := ""
	if runtime != nil {
		if tmuxSession, ok, _ := restoredRuntimeTmuxSession(runtime); ok {
			wantedTmux = strings.TrimSpace(tmuxSession)
		}
	}
	for _, snapshot := range api.terminalStore.List(sessionID) {
		if strings.ToLower(strings.TrimSpace(snapshot.ContentSource)) != "tmux_stream" {
			continue
		}
		if wantedTmux != "" && strings.TrimSpace(snapshot.TmuxSession) != wantedTmux {
			continue
		}
		if prepared, ok := prepareChatHistoryTerminalSnapshot(snapshot); ok {
			return prepared, true
		}
	}
	return terminals.Snapshot{}, false
}

// collectChatHistoryTerminalSnapshots prepares persistable terminal snapshots
// from the in-memory store list. When tmuxOnly is set it keeps only
// tmux-backed panes (the workflow coding-agent path); otherwise it keeps any
// non-empty terminal pane (the multi-agent non-tmux fallback).
func collectChatHistoryTerminalSnapshots(stored []terminals.Snapshot, tmuxOnly bool) []terminals.Snapshot {
	candidates := make([]terminals.Snapshot, 0, len(stored))
	for _, snapshot := range stored {
		if strings.TrimSpace(snapshot.Content) == "" {
			continue
		}
		if tmuxOnly && !chatHistoryTerminalSnapshotIsTmux(snapshot) {
			continue
		}
		if prepared, ok := prepareChatHistoryTerminalSnapshot(snapshot); ok {
			candidates = append(candidates, prepared)
		}
	}
	return candidates
}

func (api *StreamingAPI) captureChatHistoryRuntimeTmuxSnapshot(sessionID string, runtime *ChatHistoryAgentRuntime) (terminals.Snapshot, bool) {
	tmuxSession, ok, reason := restoredRuntimeTmuxSession(runtime)
	if !ok {
		if reason != "" && reason != "agent_session_handle_missing" && reason != "not_tmux_transport" {
			api.logRestoredTerminalInfof("terminal snapshot runtime fallback skipped session=%s reason=%s", sessionID, reason)
		}
		return terminals.Snapshot{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	content, err := captureChatHistoryTerminalPaneLines(ctx, tmuxSession, terminalDefaultDetailHistoryLines)
	if err != nil {
		api.logRestoredTerminalf("Failed to capture runtime tmux snapshot session=%s tmux_session=%s: %v", sessionID, tmuxSession, err)
		return terminals.Snapshot{}, false
	}

	provider := strings.TrimSpace(runtime.Provider)
	if provider == "" && runtime.AgentSessionHandle != nil {
		provider = strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider)
	}
	label := "Restored terminal"
	if provider != "" {
		label = "Restored " + provider
	}
	now := time.Now()
	snapshot := terminals.Snapshot{
		TerminalID:    sessionID + ":main:" + sessionID,
		SessionID:     sessionID,
		OwnerID:       "main:" + sessionID,
		ExecutionID:   "main:" + sessionID,
		ExecutionKind: "main_agent",
		Label:         label,
		Scope:         "main_agent",
		WorkflowPath:  strings.TrimSpace(runtime.WorkspacePath),
		StepID:        "main_agent:" + sessionID,
		StepTransport: "tmux",
		TmuxSession:   tmuxSession,
		Content:       content,
		ContentSource: "tmux_capture",
		Active:        false,
		State:         "stale",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return prepareChatHistoryTerminalSnapshot(snapshot)
}

func prepareChatHistoryTerminalSnapshot(snapshot terminals.Snapshot) (terminals.Snapshot, bool) {
	content := terminals.RedactSensitiveTerminalText(snapshot.Content)
	content = boundChatHistoryTerminalContent(content)
	if strings.TrimSpace(content) == "" {
		return terminals.Snapshot{}, false
	}
	snapshot.Content = content
	snapshot.Rows = nil
	snapshot.Active = false
	if strings.TrimSpace(snapshot.State) == "" || snapshot.State == "running" || snapshot.State == "closing" {
		snapshot.State = "stale"
	}
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	if snapshot.ContentSource == "" {
		snapshot.ContentSource = "tmux_capture"
	}
	return snapshot, true
}

func boundChatHistoryTerminalContent(content string) string {
	content = terminalContentTail(content, maxChatHistoryTerminalBytes)
	if maxChatHistoryTerminalLines <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxChatHistoryTerminalLines {
		return content
	}
	return strings.Join(lines[len(lines)-maxChatHistoryTerminalLines:], "\n")
}

func chatHistoryTerminalSnapshotIsTmux(snapshot terminals.Snapshot) bool {
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		return true
	}
	transport := strings.ToLower(strings.TrimSpace(snapshot.StepTransport))
	if transport == "tmux" {
		return true
	}
	source := strings.ToLower(strings.TrimSpace(snapshot.ContentSource))
	return source == "tmux_pipe" || source == "tmux_capture" || source == "tmux_stream"
}

func chatHistoryTerminalSnapshotIsMainAgent(snapshot terminals.Snapshot) bool {
	if strings.Contains(snapshot.TerminalID, ":turn-") {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(snapshot.ExecutionKind))
	scope := strings.ToLower(strings.TrimSpace(snapshot.Scope))
	return kind == "main_agent" || kind == "main" || kind == "chat" ||
		scope == "main_agent" || scope == "main" || scope == "chat" ||
		strings.HasPrefix(strings.TrimSpace(snapshot.OwnerID), "main:")
}

func (api *StreamingAPI) resolveRestoredCodingConversationPersistTarget(userID, currentSessionID, restoredConversationPath, restoredConversationSessionID, workspacePath, currentProvider, currentWorkshopMode string) (*restoredChatHistoryPersistTarget, bool, error) {
	currentSessionID = strings.TrimSpace(currentSessionID)
	restoredConversationPath = strings.TrimSpace(restoredConversationPath)
	restoredConversationSessionID = strings.TrimSpace(restoredConversationSessionID)
	if userID == "" {
		userID = "default"
	}

	var target *restoredChatHistoryPersistTarget
	var ok bool
	var err error
	if restoredConversationPath != "" {
		target, ok, err = readRestoredChatHistoryPersistTargetFromPath(userID, restoredConversationPath)
	} else if restoredConversationSessionID != "" {
		target, ok, err = readRestoredChatHistoryPersistTargetForSession(userID, restoredConversationSessionID, workspacePath)
	} else if api != nil {
		target, ok = api.rememberedRestoredConversationPersistTarget(currentSessionID)
		if ok {
			target, ok, err = readRestoredChatHistoryPersistTargetFromPath(userID, target.ConversationPath)
		}
	}
	if err != nil || !ok || target == nil {
		return nil, false, err
	}
	if strings.TrimSpace(target.SessionID) == "" || strings.TrimSpace(target.ConversationPath) == "" {
		return nil, false, nil
	}
	if !shouldPersistIntoRestoredCodingConversation(target.Runtime, currentProvider, currentWorkshopMode) {
		if api != nil {
			api.forgetRestoredConversationPersistTarget(currentSessionID)
		}
		return nil, false, nil
	}
	if api != nil && currentSessionID != "" {
		api.rememberRestoredConversationPersistTarget(currentSessionID, *target)
	}
	return target, true, nil
}

func shouldPersistIntoRestoredCodingConversation(runtime *ChatHistoryAgentRuntime, currentProvider, currentWorkshopMode string) bool {
	if runtime == nil || runtime.Kind != "coding_agent" {
		return false
	}
	hasAgentSessionHandle := runtime.AgentSessionHandle != nil && !runtime.AgentSessionHandle.Empty()
	if !runtime.ResumeSupported && !hasAgentSessionHandle {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if provider == "" && hasAgentSessionHandle {
		provider = strings.ToLower(strings.TrimSpace(runtime.AgentSessionHandle.Provider.Provider))
	}
	currentProvider = strings.ToLower(strings.TrimSpace(currentProvider))
	if provider == "" || (currentProvider != "" && provider != currentProvider) {
		return false
	}
	if !chatHistoryResumeModesCompatible(runtime.WorkshopMode, currentWorkshopMode) {
		return false
	}
	return restoredRuntimeCodingAgentTransport(runtime) != ""
}

func (api *StreamingAPI) rememberRestoredConversationPersistTarget(currentSessionID string, target restoredChatHistoryPersistTarget) {
	currentSessionID = strings.TrimSpace(currentSessionID)
	if api == nil || currentSessionID == "" || strings.TrimSpace(target.ConversationPath) == "" {
		return
	}
	api.restoredConversationPersistMux.Lock()
	defer api.restoredConversationPersistMux.Unlock()
	if api.restoredConversationPersistTargets == nil {
		api.restoredConversationPersistTargets = make(map[string]restoredChatHistoryPersistTarget)
	}
	api.restoredConversationPersistTargets[currentSessionID] = restoredChatHistoryPersistTarget{
		SessionID:        target.SessionID,
		ConversationPath: target.ConversationPath,
		Runtime:          target.Runtime,
		WorkshopMode:     target.WorkshopMode,
	}
}

// restoreResolvedResumeTarget hydrates UI history and persistence from the
// same target that will seed the provider-native continuation. It performs no
// path or session lookup; product ownership was already established by the
// authenticated profile route.
func (api *StreamingAPI) restoreResolvedResumeTarget(currentSessionID string, target *resolvedResumeTarget, currentWorkshopMode string) bool {
	if api == nil || target == nil || strings.TrimSpace(currentSessionID) == "" {
		return false
	}
	restoredMode := target.WorkshopMode
	if target.Runtime != nil {
		restoredMode = firstNonEmptyTrimmed(target.Runtime.WorkshopMode, restoredMode)
	}
	if !chatHistoryResumeModesCompatible(restoredMode, currentWorkshopMode) {
		return false
	}

	history := append([]llmtypes.MessageContent(nil), target.History...)
	if len(history) > 0 {
		api.conversationMux.Lock()
		if _, exists := api.conversationHistory[currentSessionID]; !exists {
			if api.conversationHistory == nil {
				api.conversationHistory = make(map[string][]llmtypes.MessageContent)
			}
			api.conversationHistory[currentSessionID] = history
		}
		api.conversationMux.Unlock()
	}
	if persistTarget := target.persistTarget(); persistTarget != nil {
		api.rememberRestoredConversationPersistTarget(currentSessionID, *persistTarget)
	}
	return len(history) > 0
}

func (api *StreamingAPI) rememberedRestoredConversationPersistTarget(currentSessionID string) (*restoredChatHistoryPersistTarget, bool) {
	currentSessionID = strings.TrimSpace(currentSessionID)
	if api == nil || currentSessionID == "" {
		return nil, false
	}
	api.restoredConversationPersistMux.RLock()
	defer api.restoredConversationPersistMux.RUnlock()
	target, ok := api.restoredConversationPersistTargets[currentSessionID]
	if !ok || strings.TrimSpace(target.ConversationPath) == "" {
		return nil, false
	}
	return &target, true
}

func (api *StreamingAPI) forgetRestoredConversationPersistTarget(currentSessionID string) {
	currentSessionID = strings.TrimSpace(currentSessionID)
	if api == nil || currentSessionID == "" {
		return
	}
	api.restoredConversationPersistMux.Lock()
	defer api.restoredConversationPersistMux.Unlock()
	delete(api.restoredConversationPersistTargets, currentSessionID)
}

func readRestoredChatHistoryPersistTargetFromPath(userID, conversationPath string) (*restoredChatHistoryPersistTarget, bool, error) {
	normalizedPath, ok := normalizeRestoredChatHistoryConversationPath(userID, conversationPath)
	if !ok {
		return nil, false, nil
	}
	data, exists, err := readChatHistoryConversationDataFromPath(normalizedPath)
	if err != nil || !exists {
		return nil, exists, err
	}
	return parseRestoredChatHistoryPersistTarget(data, chatHistorySessionIDFromConversationPath(userID, normalizedPath), normalizedPath)
}

func readRestoredChatHistoryPersistTargetForSession(userID, sessionID, workspacePath string) (*restoredChatHistoryPersistTarget, bool, error) {
	sessionID = sanitizeChatHistorySessionID(sessionID)
	if sessionID == "" {
		return nil, false, nil
	}
	data, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil {
		if strings.Contains(err.Error(), "conversation not found") {
			return nil, false, nil
		}
		return nil, false, err
	}
	conversationPath := ""
	if path, ok, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath); err != nil {
		return nil, false, err
	} else if ok {
		conversationPath = path
	}
	return parseRestoredChatHistoryPersistTarget([]byte(data), sessionID, conversationPath)
}

func parseRestoredChatHistoryPersistTarget(data []byte, fallbackSessionID, conversationPath string) (*restoredChatHistoryPersistTarget, bool, error) {
	var raw struct {
		SessionID string                    `json:"session_id"`
		Runtime   *ChatHistoryAgentRuntime  `json:"runtime,omitempty"`
		Mode      string                    `json:"workshop_mode,omitempty"`
		History   []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	sessionID := sanitizeChatHistorySessionID(raw.SessionID)
	if sessionID == "" {
		sessionID = sanitizeChatHistorySessionID(fallbackSessionID)
	}
	if sessionID == "" || strings.TrimSpace(conversationPath) == "" {
		return nil, false, nil
	}
	if raw.Runtime != nil && raw.Runtime.WorkshopMode == "" {
		raw.Runtime.WorkshopMode = normalizeChatHistoryWorkshopMode(raw.Mode)
	}
	normalizeChatHistoryRuntime(raw.Runtime)
	return &restoredChatHistoryPersistTarget{
		SessionID:        sessionID,
		ConversationPath: conversationPath,
		History:          raw.History,
		Runtime:          raw.Runtime,
		WorkshopMode:     normalizeChatHistoryWorkshopMode(raw.Mode),
	}, true, nil
}

func chatHistorySessionIDFromConversationPath(userID, conversationPath string) string {
	conversationPath = strings.Trim(pathpkg.Clean(filepath.ToSlash(conversationPath)), "/")
	if id, ok := chatHistorySessionIDFromWorkspacePath(chatHistoryRoot(userID), conversationPath); ok {
		return id
	}
	if id := chatHistorySessionIDFromFileName(pathpkg.Base(conversationPath)); id != "" {
		return id
	}
	if pathpkg.Base(conversationPath) == "conversation.json" {
		return sanitizeChatHistorySessionID(pathpkg.Base(pathpkg.Dir(conversationPath)))
	}
	return ""
}

func mergeRestoredChatHistory(existing, incoming []llmtypes.MessageContent) []llmtypes.MessageContent {
	if len(existing) == 0 {
		return incoming
	}
	if len(incoming) == 0 {
		return existing
	}
	if chatHistoryHasPrefix(incoming, existing) {
		return incoming
	}
	// The file often holds rows the in-memory history never saw (live input,
	// structured completion replies, native transcript catch-up), so incoming
	// is not a prefix extension. Align the two instead of concatenating: the
	// old fallback appended the whole history onto the file every turn.
	old, oldErr := chatHistoryToRaw(existing)
	next, nextErr := chatHistoryToRaw(incoming)
	if oldErr == nil && nextErr == nil {
		if aligned, err := alignChatHistories(old, next); err == nil {
			var merged []llmtypes.MessageContent
			if encoded, err := json.Marshal(aligned); err == nil && json.Unmarshal(encoded, &merged) == nil {
				return merged
			}
		}
	}
	// Never concatenate: keep the file and add only what it lacks at the end.
	return appendMissingChatHistoryTail(existing, incoming)
}

func chatHistoryToRaw(history []llmtypes.MessageContent) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(history))
	for _, message := range history {
		encoded, err := json.Marshal(message)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}

// appendMissingChatHistoryTail is the fallback when a history can't be
// encoded: keep existing and append the incoming rows after its last match.
func appendMissingChatHistoryTail(existing, incoming []llmtypes.MessageContent) []llmtypes.MessageContent {
	last := len(existing) - 1
	for start := 0; start < len(incoming); start++ {
		if chatHistoryMessagesEqual(existing[last], incoming[start]) {
			return append(append([]llmtypes.MessageContent{}, existing...), incoming[start+1:]...)
		}
	}
	return existing
}

// mergeNativeContinuationChatHistory keeps the durable/UI transcript
// cumulative when a coding provider owns conversational context through its
// native continuation handle. In that mode the new Agent wrapper correctly
// does not replay existing UI history, so GetHistory contains only the current
// turn (and its freshly rendered system prompt). Persisting it directly would
// silently replace every earlier turn.
//
// A system prompt is runtime identity, not a user-visible turn. Keep the newest
// one and join the prior and current human/assistant exchanges around it.
func mergeNativeContinuationChatHistory(existing, current []llmtypes.MessageContent) []llmtypes.MessageContent {
	if len(existing) == 0 {
		return current
	}
	if len(current) == 0 {
		return existing
	}
	if chatHistoryHasPrefix(current, existing) {
		return current
	}
	if existing[0].Role == llmtypes.ChatMessageTypeSystem && current[0].Role == llmtypes.ChatMessageTypeSystem {
		// Native providers may return either only the new exchange or their
		// cumulative exchange history. Compare the conversational body without
		// the regenerated system prompt so both shapes merge without duplicates.
		body := mergeRestoredChatHistory(existing[1:], current[1:])
		merged := make([]llmtypes.MessageContent, 0, len(body)+1)
		merged = append(merged, current[0])
		merged = append(merged, body...)
		return merged
	}
	return mergeRestoredChatHistory(existing, current)
}

// mergeCodingAgentFallbackChatHistory removes the historical messages that
// were injected only to seed a replacement coding-provider session, then joins
// the genuinely new exchange to the complete durable transcript.
func mergeCodingAgentFallbackChatHistory(existing, agentHistory []llmtypes.MessageContent, injectedAt, injectedCount int) []llmtypes.MessageContent {
	if injectedAt < 0 || injectedCount <= 0 || injectedAt+injectedCount > len(agentHistory) {
		return agentHistory
	}
	currentOnly := make([]llmtypes.MessageContent, 0, len(agentHistory)-injectedCount)
	currentOnly = append(currentOnly, agentHistory[:injectedAt]...)
	currentOnly = append(currentOnly, agentHistory[injectedAt+injectedCount:]...)
	return mergeNativeContinuationChatHistory(existing, currentOnly)
}

// mergeModeChangedChatHistory keeps the canonical UI transcript complete when
// a workflow session relaunches with a different prompt/tool/skill surface.
// The first current message is the synthetic pointer supplied only to the new
// agent process; it is transport context and must not appear as a user turn in
// the durable conversation JSON.
func mergeModeChangedChatHistory(previous, current []llmtypes.MessageContent) []llmtypes.MessageContent {
	if len(current) == 0 {
		return append([]llmtypes.MessageContent(nil), previous...)
	}
	merged := make([]llmtypes.MessageContent, 0, len(previous)+len(current)-1)
	merged = append(merged, previous...)
	merged = append(merged, current[1:]...)
	return merged
}

func modeChangeHistorySnapshot(userID, conversationPath string, inMemory []llmtypes.MessageContent) ([]llmtypes.MessageContent, error) {
	if len(inMemory) > 0 {
		return append([]llmtypes.MessageContent(nil), inMemory...), nil
	}
	target, found, err := readRestoredChatHistoryPersistTargetFromPath(userID, conversationPath)
	if err != nil || !found || target == nil {
		return nil, err
	}
	return append([]llmtypes.MessageContent(nil), target.History...), nil
}

// phaseUsageCumulativeKey separates cumulative coding-CLI counters by native
// process/session epoch. Run→Workshop transitions deliberately relaunch the
// CLI; reusing only the AgentWorks session id would subtract the previous
// process's final counters from the new process's first counters and could
// undercount a turn whenever the new counter happened to be larger.
func phaseUsageCumulativeKey(sessionID, turnID string, runtime *ChatHistoryAgentRuntime) string {
	if runtime != nil {
		if nativeID := strings.TrimSpace(runtime.ExternalSessionID); nativeID != "" {
			return sessionID + "|native:" + strings.ToLower(strings.TrimSpace(runtime.Provider)) + ":" + nativeID
		}
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		return sessionID + "|turn:" + turnID
	}
	return sessionID
}

// boundedChatHistoryTail returns the newest messages that fit the supplied
// limits. It is used only for durable UI storage and the explicit no-resume
// fallback; coding providers with a native continuation never receive this
// transcript as prompt context.
func boundedChatHistoryTail(history []llmtypes.MessageContent, maxMessages, maxBytes int) []llmtypes.MessageContent {
	if len(history) == 0 || maxMessages <= 0 || maxBytes <= 0 {
		return nil
	}
	start := len(history)
	usedBytes := 0
	kept := 0
	for i := len(history) - 1; i >= 0 && kept < maxMessages; i-- {
		encoded, err := json.Marshal(history[i])
		if err != nil {
			continue
		}
		if kept > 0 && usedBytes+len(encoded) > maxBytes {
			break
		}
		// Always retain the newest message, even when a single tool result is
		// larger than the budget. Dropping the current user request would make
		// a fallback incoherent.
		start = i
		usedBytes += len(encoded)
		kept++
	}
	if start == len(history) {
		return nil
	}
	return append([]llmtypes.MessageContent(nil), history[start:]...)
}

func chatHistoryHasPrefix(history, prefix []llmtypes.MessageContent) bool {
	if len(prefix) > len(history) {
		return false
	}
	for i := range prefix {
		if !chatHistoryMessagesEqual(history[i], prefix[i]) {
			return false
		}
	}
	return true
}

func chatHistoryMessagesEqual(a, b llmtypes.MessageContent) bool {
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && string(aJSON) == string(bJSON)
}

// collapseChatHistoryStreamingChunks drops transient streaming chunks in a
// consecutive run except the last one for that execution.
//
// Only transient chunks can collapse. Whole transcript messages are durable
// assistant updates and must survive alongside tool calls and final answers.
// A terminal snapshot is a full render of the pane at
// that instant, roughly a kilobyte apiece, and the UI consumes it by calling
// setOwnedStreamingTerminalSnapshot(key, chunkIndex, content), i.e. last
// writer wins. Nothing replays the intermediate frames, so persisting all of
// them stores the same screen dozens of times: one real two-turn conversation
// spent 74.5 KB across 75 chunks, 58% of its events, to preserve a single
// final pane that the last chunk already carries.
//
// Runs are keyed by execution so two terminals streaming concurrently do not
// collapse into each other, and the final chunk of each run is kept because
// structured (non-tmux) providers rebuild their synthetic terminal from it.
// This runs before the event cap, so the cap now spends its budget on real
// conversation events instead of discarding them to make room for duplicate
// screens.
func collapseChatHistoryStreamingChunks(uiEvents []internalevents.Event) []internalevents.Event {
	const streamingChunk = "streaming_chunk"
	collapsed := make([]internalevents.Event, 0, len(uiEvents))
	for i := 0; i < len(uiEvents); i++ {
		event := uiEvents[i]
		if event.Type != streamingChunk || internalevents.IsTranscriptMessage(event) {
			collapsed = append(collapsed, event)
			continue
		}
		// Advance to the last chunk of this run for this execution. A chunk
		// belonging to a different execution ends the run so its own frames
		// are not silently dropped.
		last := i
		for j := i + 1; j < len(uiEvents); j++ {
			if uiEvents[j].Type != streamingChunk || internalevents.IsTranscriptMessage(uiEvents[j]) || uiEvents[j].ExecutionID != event.ExecutionID {
				break
			}
			last = j
		}
		collapsed = append(collapsed, uiEvents[last])
		i = last
	}
	return collapsed
}

// readPersistedChatHistoryUIEvents returns the UI trace already saved for a
// conversation, or nil when there is none or it cannot be read.
func readPersistedChatHistoryUIEvents(ctx context.Context, conversationPath string) []internalevents.Event {
	if strings.TrimSpace(conversationPath) == "" {
		return nil
	}
	content, exists, err := readFileFromWorkspace(ctx, conversationPath)
	if err != nil || !exists || strings.TrimSpace(content) == "" {
		return nil
	}
	raw := chatHistoryUIEvents([]byte(content))
	if len(raw) == 0 {
		return nil
	}
	var persisted []internalevents.Event
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil
	}
	return persisted
}

// mergeChatHistoryUIEvents keeps the saved trace that predates what the live
// event store holds, then the live events. The live store is authoritative
// for anything it covers: a saved event is kept only when it is older than the
// oldest live event and not already present by id.
func mergeChatHistoryUIEvents(persisted, live []internalevents.Event) []internalevents.Event {
	if len(persisted) == 0 {
		return live
	}
	if len(live) == 0 {
		return persisted
	}
	oldest := live[0].Timestamp
	liveIDs := make(map[string]struct{}, len(live))
	for _, event := range live {
		if !event.Timestamp.IsZero() && (oldest.IsZero() || event.Timestamp.Before(oldest)) {
			oldest = event.Timestamp
		}
		if event.ID != "" {
			liveIDs[event.ID] = struct{}{}
		}
	}
	merged := make([]internalevents.Event, 0, len(persisted)+len(live))
	for _, event := range persisted {
		if event.ID != "" {
			if _, dup := liveIDs[event.ID]; dup {
				continue
			}
		}
		if !oldest.IsZero() && !event.Timestamp.Before(oldest) {
			continue
		}
		merged = append(merged, event)
	}
	return append(merged, live...)
}

func trimChatHistoryUIEvents(uiEvents []internalevents.Event) []internalevents.Event {
	uiEvents = collapseChatHistoryStreamingChunks(uiEvents)
	if len(uiEvents) <= maxPersistedChatHistoryUIEvents {
		return uiEvents
	}
	trimmed := make([]internalevents.Event, maxPersistedChatHistoryUIEvents)
	copy(trimmed, uiEvents[len(uiEvents)-maxPersistedChatHistoryUIEvents:])
	return trimmed
}

// ListChatHistorySessions returns persisted session metadata for a user, newest first.
func ListChatHistorySessions(userID string, limit, offset int, workspacePath string) ([]ChatHistorySession, error) {
	return listChatHistorySessions(userID, limit, offset, workspacePath, "")
}

// ListChatHistorySessionsByKind returns a page from one visible history kind.
// The kind filter must be applied before pagination: a busy schedule can write
// many newer transcripts than an ordinary builder chat, and paging the mixed
// list first made the Recent tab incorrectly appear empty.
func ListChatHistorySessionsByKind(userID, kind string, limit, offset int, workspacePath string) ([]ChatHistorySession, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" && kind != "chat" && kind != "schedule" && kind != "bot" {
		return nil, fmt.Errorf("unsupported chat history kind %q", kind)
	}
	return listChatHistorySessions(userID, limit, offset, workspacePath, kind)
}

func listChatHistorySessions(userID string, limit, offset int, workspacePath, kind string) ([]ChatHistorySession, error) {
	if userID == "" {
		userID = "default"
	}
	root := chatHistoryRoot(userID)
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)

	if workspacePath != "" {
		if sessions, ok, err := listWorkflowScopedChatHistorySessionsFromDisk(userID, root, workspacePath, limit, offset, kind); ok || err != nil {
			return sessions, err
		}
	}

	if sessions, ok, err := listChatHistorySessionsFromDisk(userID, root, "", limit, offset); ok || err != nil {
		return sessions, err
	}
	if sessions, ok, err := listChatHistorySessionsFromWorkspaceIndex(userID, workspacePath, limit, offset); ok || err != nil {
		return sessions, err
	}

	filePaths, err := listWorkspaceFilesRecursive(context.Background(), root)
	if err != nil {
		return nil, err
	}

	scheduleIDBySessionID := chatHistoryScheduleIDBySessionID(userID, workspacePath)
	sessionsByID := make(map[string]ChatHistorySession)
	for _, convPath := range filePaths {
		sessionID, ok := chatHistorySessionIDFromWorkspacePath(root, convPath)
		if !ok {
			continue
		}
		data, exists, err := readFileFromWorkspace(context.Background(), convPath)
		if err != nil || !exists {
			continue
		}

		if workspacePath != "" && !chatHistoryDataMatchesWorkspace(data, workspacePath) {
			continue
		}

		session, ok := parseLocalChatHistorySession(userID, root, workspacePath, sessionID, data, time.Now())
		if !ok {
			continue
		}
		session.ConversationPath = convPath
		displayKey := chatHistoryDisplayKey(session.SessionID, scheduleIDBySessionID)
		if existing, ok := sessionsByID[displayKey]; !ok || session.UpdatedAt > existing.UpdatedAt || (session.UpdatedAt == existing.UpdatedAt && session.ConversationPath > existing.ConversationPath) {
			sessionsByID[displayKey] = session
		}
	}

	sessions := make([]ChatHistorySession, 0, len(sessionsByID))
	for _, session := range sessionsByID {
		sessions = append(sessions, session)
	}

	// Sort by UpdatedAt descending
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	// Apply pagination
	if offset >= len(sessions) {
		return []ChatHistorySession{}, nil
	}
	sessions = sessions[offset:]
	if limit > 0 && limit < len(sessions) {
		sessions = sessions[:limit]
	}

	if kind != "" {
		filtered := sessions[:0]
		for _, session := range sessions {
			if chatHistorySessionMatchesKind(session.SessionID, kind) {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	return sessions, nil
}

func listChatHistorySessionsFromWorkspaceIndex(userID, workflowPath string, limit, offset int) ([]ChatHistorySession, bool, error) {
	indexPath := pathpkg.Join(chatHistoryRoot(userID), chatHistoryIndexFileName)
	if workflowPath != "" {
		indexPath = pathpkg.Join(workflowPath, "builder", "conversation", chatHistoryIndexFileName)
	}
	data, exists, err := readFileFromWorkspace(context.Background(), indexPath)
	if err != nil || !exists {
		return nil, false, err
	}
	var index chatHistoryIndex
	if err := json.Unmarshal([]byte(data), &index); err != nil {
		return nil, false, nil
	}
	index.normalize()
	if !index.Complete {
		return nil, false, nil
	}
	sessions := paginateChatHistorySessions(chatHistorySessionsFromIndex(index, userID, workflowPath), limit, offset)
	for i := range sessions {
		if !chatHistoryHasLocalCommandPreview(sessions[i]) {
			continue
		}
		source, found, readErr := readFileFromWorkspace(context.Background(), sessions[i].ConversationPath)
		if readErr == nil && found {
			if refreshed, ok := parseLocalChatHistorySession(userID, chatHistoryRoot(userID), workflowPath, sessions[i].SessionID, source, time.Time{}); ok {
				sessions[i].Query = refreshed.Query
				sessions[i].PreviewMessages = refreshed.PreviewMessages
				sessions[i].MessageCount = refreshed.MessageCount
				continue
			}
		}
		// Keep the row reachable even if the source is temporarily unavailable.
		if isClaudeLocalCommandRecord(sessions[i].Query) {
			sessions[i].Query = ""
		}
		clean := make([]ChatHistoryPreviewMessage, 0, len(sessions[i].PreviewMessages))
		for _, msg := range sessions[i].PreviewMessages {
			if !isClaudeLocalCommandMessage(msg.Role, msg.Text) {
				clean = append(clean, msg)
			}
		}
		sessions[i].PreviewMessages = clean
	}
	return sessions, true, nil
}

type localChatHistoryFile struct {
	sessionID     string
	dedupeKey     string
	convPath      string
	workspacePath string
	modTime       time.Time
	size          int64
}

// listChatHistorySessionsFromDisk avoids hundreds of workspace-API reads when
// the agent server and workspace docs are on the same machine. It sorts by the
// conversation file mtime first, then reads only the requested page.
func listChatHistorySessionsFromDisk(userID, workspaceRoot, workflowPath string, limit, offset int) ([]ChatHistorySession, bool, error) {
	baseDir, ok := resolveLocalChatHistoryDir(workspaceRoot)
	if !ok {
		return nil, false, nil
	}
	indexWorkspacePath := pathpkg.Join(workspaceRoot, chatHistoryIndexFileName)
	indexLocalPath := filepath.Join(baseDir, chatHistoryIndexFileName)
	mutex := chatHistoryIndexMutex(indexWorkspacePath)

	// See listWorkflowBuilderHistoryFromDisk: the index caches per-file previews,
	// it does not stand in for the directory.
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ChatHistorySession{}, true, nil
		}
		return nil, true, err
	}

	var scheduleIDBySessionID map[string]string
	filesBySession := make(map[string]localChatHistoryFile)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryName := entry.Name()

		// Legacy layout: chat_history/<session-id>/conversation.json
		legacyConvPath := filepath.Join(baseDir, entryName, "conversation.json")
		if info, err := os.Stat(legacyConvPath); err == nil && !info.IsDir() {
			addLocalChatHistoryFile(filesBySession, localChatHistoryFile{
				sessionID:     entryName,
				dedupeKey:     chatHistoryDisplayKey(entryName, scheduleIDBySessionID),
				convPath:      legacyConvPath,
				workspacePath: pathpkg.Join(workspaceRoot, entryName, "conversation.json"),
				modTime:       info.ModTime(),
				size:          info.Size(),
			})
		}

		// Date-bucket layout: chat_history/YYYY-MM-DD/session-<id>-conversation.json
		dateDir := filepath.Join(baseDir, entryName)
		matches, err := filepath.Glob(filepath.Join(dateDir, "session-*-conversation.json"))
		if err != nil {
			return nil, true, err
		}
		for _, convPath := range matches {
			info, err := os.Stat(convPath)
			if err != nil || info.IsDir() {
				continue
			}
			sessionID := chatHistorySessionIDFromFileName(filepath.Base(convPath))
			if sessionID == "" {
				continue
			}
			addLocalChatHistoryFile(filesBySession, localChatHistoryFile{
				sessionID:     sessionID,
				dedupeKey:     chatHistoryDisplayKey(sessionID, scheduleIDBySessionID),
				convPath:      convPath,
				workspacePath: pathpkg.Join(workspaceRoot, entryName, filepath.Base(convPath)),
				modTime:       info.ModTime(),
				size:          info.Size(),
			})
		}
	}

	files := make([]localChatHistoryFile, 0, len(filesBySession))
	for _, file := range filesBySession {
		files = append(files, file)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	mutex.Lock()
	defer mutex.Unlock()
	index := loadLocalChatHistoryIndex(indexLocalPath)

	sessions := make([]ChatHistorySession, 0, len(files))
	for _, file := range files {
		session, ok := indexedLocalChatHistorySession(index, file)
		if !ok {
			session, ok = readLocalChatHistorySession(userID, workspaceRoot, workflowPath, file)
			if ok {
				putLocalChatHistoryIndexEntry(&index, file, session)
			}
		}
		if ok {
			preserveChatHistorySessionTitle(&index, &session)
			sessions = append(sessions, session)
		}
	}
	index.Complete = true
	_ = writeLocalChatHistoryIndex(indexLocalPath, &index)
	return paginateChatHistorySessions(sessions, limit, offset), true, nil
}

func addLocalChatHistoryFile(filesBySession map[string]localChatHistoryFile, file localChatHistoryFile) {
	if file.sessionID == "" {
		return
	}
	key := strings.TrimSpace(file.dedupeKey)
	if key == "" {
		key = file.sessionID
	}
	if existing, ok := filesBySession[key]; !ok || file.modTime.After(existing.modTime) || (file.modTime.Equal(existing.modTime) && file.workspacePath > existing.workspacePath) {
		filesBySession[key] = file
	}
}

func chatHistorySessionIDFromFileName(fileName string) string {
	if !strings.HasPrefix(fileName, "session-") || !strings.HasSuffix(fileName, "-conversation.json") {
		return ""
	}
	sessionID := strings.TrimSuffix(strings.TrimPrefix(fileName, "session-"), "-conversation.json")
	return sanitizeChatHistorySessionID(sessionID)
}

func chatHistorySessionIDFromWorkspacePath(root, convPath string) (string, bool) {
	root = strings.Trim(pathpkg.Clean(root), "/")
	convPath = strings.Trim(pathpkg.Clean(filepath.ToSlash(convPath)), "/")
	if root == "" || convPath == "" || !strings.HasPrefix(convPath, root+"/") {
		return "", false
	}
	rel := strings.TrimPrefix(convPath, root+"/")
	parts := strings.Split(rel, "/")
	if len(parts) == 2 && parts[1] == "conversation.json" {
		sessionID := sanitizeChatHistorySessionID(parts[0])
		return sessionID, sessionID != ""
	}
	if len(parts) == 2 {
		sessionID := chatHistorySessionIDFromFileName(parts[1])
		return sessionID, sessionID != ""
	}
	return "", false
}

func listWorkflowScopedChatHistorySessionsFromDisk(userID, chatHistoryRootPath, workflowPath string, limit, offset int, kind string) ([]ChatHistorySession, bool, error) {
	all := make([]ChatHistorySession, 0)
	builderWorkspacePath := workflowPath
	// Work project paths sent by the browser are logical, while their files are
	// stored below the signed-in user's private _users/<id> root. Resolve that
	// owned storage path before scanning builder/conversation; otherwise the
	// project can have durable transcripts on disk while Recent returns none.
	if ownedPath, isWorkProject := ownedWorkProjectWorkspacePath(userID, workflowPath); isWorkProject {
		builderWorkspacePath = ownedPath
	}
	// Crew Run mode: a reader's history for someone else's crew comes from
	// the reader's own central store only. Never scan the owner's
	// builder/conversation transcripts from disk for another user.
	skipBuilderDisk := crewBuilderDiskHiddenFromUser(userID, workflowPath)

	// Workflow builder files are the most precise source for /resume inside a
	// workflow. Do not include global chat_history matches here: those can
	// mention this workflow in pasted context while belonging to another chat.
	// Pass the page budget so only the requested page of conversation files is
	// read+parsed (previews are expensive); the rest are only stat'd.
	readBudget := 0
	if limit > 0 {
		readBudget = limit + offset
	}
	if !skipBuilderDisk {
		if builderSessions, ok := listWorkflowBuilderHistoryFromDisk(userID, builderWorkspacePath, readBudget, kind); ok {
			all = append(all, builderSessions...)
		}
	}

	// Compatibility fallback for product conversations written before Work moved
	// to the same project-owned builder/conversation layout as AgentWorks. Match
	// structured workspace metadata so pasted paths cannot create false results.
	if productSessions, ok, err := listChatHistorySessionsFromDisk(userID, chatHistoryRootPath, "", 0, 0); err != nil {
		return nil, true, err
	} else if ok {
		for _, session := range productSessions {
			if !chatHistorySessionMatchesKind(session.SessionID, kind) || !chatHistorySessionMatchesWorkspace(userID, session, workflowPath) {
				continue
			}
			// Migrate legacy Work transcripts opportunistically when the project is
			// opened. This is a non-destructive copy; the central source remains as
			// a compatibility fallback until a later cleanup policy removes it.
			timestamp, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(session.UpdatedAt))
			if parseErr != nil {
				timestamp = time.Now()
			}
			if projectPath, isWorkProject := workProjectChatHistoryConversationPath(userID, workflowPath, session.SessionID, timestamp); isWorkProject {
				if err := copyChatHistoryConversationIfNeeded(session.ConversationPath, projectPath); err == nil {
					session.ConversationPath = projectPath
				}
			}
			all = append(all, session)
		}
	}

	bySessionID := make(map[string]ChatHistorySession, len(all))
	for _, session := range all {
		if existing, ok := bySessionID[session.SessionID]; !ok || session.UpdatedAt > existing.UpdatedAt {
			bySessionID[session.SessionID] = session
		}
	}
	deduped := make([]ChatHistorySession, 0, len(bySessionID))
	for _, session := range bySessionID {
		deduped = append(deduped, session)
	}
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].UpdatedAt > deduped[j].UpdatedAt
	})
	return paginateChatHistorySessions(deduped, limit, offset), true, nil
}

func chatHistorySessionMatchesWorkspace(userID string, session ChatHistorySession, workspacePath string) bool {
	candidate := strings.TrimSpace(session.WorkspacePath)
	if session.Runtime != nil && strings.TrimSpace(session.Runtime.WorkspacePath) != "" {
		candidate = session.Runtime.WorkspacePath
	}
	return canonicalChatHistoryWorkspacePath(userID, candidate) == canonicalChatHistoryWorkspacePath(userID, workspacePath)
}

func canonicalChatHistoryWorkspacePath(userID, workspacePath string) string {
	return workspaceref.CanonicalFor(userID, workspacePath)
}

// workspacePathsMatchForUser compares the public workspace identity used by
// clients with the per-user physical identity used by the runtime. It only
// removes the authenticated user's own prefix; a path owned by another user
// therefore never becomes equivalent to a public path.
func workspacePathsMatchForUser(userID, left, right string) bool {
	return workspaceref.MustParse(left).SameFor(userID, workspaceref.MustParse(right))
}

// listWorkflowBuilderHistoryFromDisk returns builder chat sessions for a workflow.
// readBudget caps how many conversation files are actually READ+PARSED (the costly
// part — preview building): we stat every file (cheap) and dedupe to the latest
// display row by filename+mtime WITHOUT reading, sort by mtime, then read only
// the top readBudget. readBudget<=0 reads all (unlimited list).
func listWorkflowBuilderHistoryFromDisk(userID, workflowPath string, readBudget int, kind string) ([]ChatHistorySession, bool) {
	workflowDir, ok := resolveLocalWorkflowDir(workflowPath)
	if !ok {
		return nil, false
	}
	indexWorkspacePath := pathpkg.Join(workflowPath, "builder", "conversation", chatHistoryIndexFileName)
	indexLocalPath := filepath.Join(workflowDir, "builder", "conversation", chatHistoryIndexFileName)
	mutex := chatHistoryIndexMutex(indexWorkspacePath)

	// The directory is always consulted, never index.Complete alone. The index is
	// a cache of the expensive part (reading and previewing each transcript),
	// keyed per file on size+mtime below; the stat pass that validates it is
	// cheap. Serving the index blind made any transcript written without a
	// matching index update permanently unreachable from /resume.
	matches, err := workflowBuilderConversationFiles(workflowDir)
	if err != nil {
		return nil, false
	}

	scheduleIDBySessionID := workflowScheduleIDBySessionID(workflowPath)

	// Cheap pass: stat only, dedupe to the latest file per display key (parsed
	// from the filename — no file read). Normal chats keep one row per session.
	// Schedule chats keep one row per schedule because repeated runs already
	// have detailed history in schedule-runs.json and may resume the same CLI
	// thread underneath.
	latest := make(map[string]localChatHistoryFile)
	for _, convPath := range matches {
		info, err := os.Stat(convPath)
		if err != nil {
			continue
		}
		sessionID := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(convPath), "session-"), "-conversation.json")
		if !chatHistorySessionMatchesKind(sessionID, kind) {
			continue
		}
		dedupeKey := workflowBuilderHistoryDisplayKey(sessionID, scheduleIDBySessionID)
		workspaceConversationPath := workflowRelativeConversationPath(workflowPath, workflowDir, convPath)
		if cur, ok := latest[dedupeKey]; !ok || info.ModTime().After(cur.modTime) {
			latest[dedupeKey] = localChatHistoryFile{
				sessionID:     sessionID,
				dedupeKey:     dedupeKey,
				convPath:      convPath,
				workspacePath: workspaceConversationPath,
				modTime:       info.ModTime(),
				size:          info.Size(),
			}
		}
	}

	type sessionRef struct {
		id  string
		ref localChatHistoryFile
	}
	refs := make([]sessionRef, 0, len(latest))
	for _, r := range latest {
		refs = append(refs, sessionRef{id: r.sessionID, ref: r})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].ref.modTime.After(refs[j].ref.modTime) })
	mutex.Lock()
	defer mutex.Unlock()
	index := loadLocalChatHistoryIndex(indexLocalPath)

	// The index is the normal path. Full transcript reads below are only the
	// one-time compatibility migration for conversations saved before the
	// index existed (or for an externally modified transcript).
	sessions := make([]ChatHistorySession, 0, len(refs))
	for _, sr := range refs {
		session, ok := indexedLocalChatHistorySession(index, sr.ref)
		if !ok {
			data, err := readConversationFileDirect(sr.ref.convPath)
			if err != nil {
				continue
			}
			session, ok = parseLocalChatHistorySession(userID, workflowPath, workflowPath, sr.id, string(data), sr.ref.modTime)
			if !ok {
				continue
			}
			session.ConversationPath = sr.ref.workspacePath
			putLocalChatHistoryIndexEntry(&index, sr.ref, session)
		}
		session.AgentMode = "workflow"
		preserveChatHistorySessionTitle(&index, &session)
		sessions = append(sessions, session)
	}
	index.Complete = true
	_ = writeLocalChatHistoryIndex(indexLocalPath, &index)
	if readBudget > 0 {
		return paginateChatHistorySessions(sessions, readBudget, 0), true
	}
	return sessions, true
}

func chatHistorySessionMatchesKind(sessionID, kind string) bool {
	if kind == "" {
		return true
	}
	isSchedule := strings.HasPrefix(sessionID, "schedule-") || strings.HasPrefix(sessionID, "sched_")
	isBot := strings.HasPrefix(sessionID, "bot-")
	switch kind {
	case "schedule":
		return isSchedule
	case "bot":
		return isBot
	case "chat":
		return !isSchedule && !isBot
	default:
		return false
	}
}

func readLocalChatHistoryIndex(indexPath string) (chatHistoryIndex, bool) {
	index := newChatHistoryIndex()
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return index, false
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return newChatHistoryIndex(), false
	}
	index.normalize()
	return index, true
}

func loadLocalChatHistoryIndex(indexPath string) chatHistoryIndex {
	if index, ok := readLocalChatHistoryIndex(indexPath); ok {
		return index
	}
	return newChatHistoryIndex()
}

func chatHistorySessionsFromIndex(index chatHistoryIndex, userID, workflowPath string) []ChatHistorySession {
	bySessionID := make(map[string]ChatHistorySession)
	for conversationPath, entry := range index.Entries {
		session := entry.Session
		session.attributionVerified = entry.AttributionVerified
		sessionID := sanitizeChatHistorySessionID(session.SessionID)
		if sessionID == "" {
			continue
		}
		session.SessionID = sessionID
		session.ConversationPath = conversationPath
		if session.UserID == "" {
			session.UserID = userID
		}
		if workflowPath != "" {
			session.WorkspacePath = workflowPath
			session.AgentMode = "workflow"
		}
		if existing, ok := bySessionID[sessionID]; !ok || session.UpdatedAt > existing.UpdatedAt || (session.UpdatedAt == existing.UpdatedAt && session.ConversationPath > existing.ConversationPath) {
			bySessionID[sessionID] = session
		}
	}
	sessions := make([]ChatHistorySession, 0, len(bySessionID))
	for _, session := range bySessionID {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions
}

func indexedLocalChatHistorySession(index chatHistoryIndex, file localChatHistoryFile) (ChatHistorySession, bool) {
	entry, ok := index.Entries[file.workspacePath]
	if !ok || entry.SourceSize != file.size || entry.Session.SessionID != file.sessionID || chatHistoryHasLocalCommandPreview(entry.Session) {
		return ChatHistorySession{}, false
	}
	if entry.SourceModifiedAtUnixNano != 0 && entry.SourceModifiedAtUnixNano != file.modTime.UnixNano() {
		return ChatHistorySession{}, false
	}
	session := entry.Session
	session.ConversationPath = file.workspacePath
	session.attributionVerified = entry.AttributionVerified
	return session, true
}

func putLocalChatHistoryIndexEntry(index *chatHistoryIndex, file localChatHistoryFile, session ChatHistorySession) {
	index.normalize()
	session.ConversationPath = file.workspacePath
	index.Entries[file.workspacePath] = chatHistoryIndexEntry{
		Session:                  session,
		SourceSize:               file.size,
		SourceModifiedAtUnixNano: file.modTime.UnixNano(),
		AttributionVerified:      true,
	}
	index.Version = chatHistoryIndexVersion
	index.UpdatedAt = time.Now().Format(time.RFC3339)
}

func writeLocalChatHistoryIndex(indexPath string, index *chatHistoryIndex) error {
	if index == nil {
		return nil
	}
	index.normalize()
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(indexPath), ".chat-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, indexPath)
}

func removeLocalChatHistoryIndexEntries(indexWorkspacePath, indexLocalPath, sessionID string, deletedPaths []string) error {
	mutex := chatHistoryIndexMutex(indexWorkspacePath)
	mutex.Lock()
	defer mutex.Unlock()
	index, exists := readLocalChatHistoryIndex(indexLocalPath)
	if !exists {
		return nil
	}
	removed := false
	for conversationPath, entry := range index.Entries {
		matches := sessionID != "" && entry.Session.SessionID == sessionID
		if !matches {
			for _, deletedPath := range deletedPaths {
				deletedPath = strings.Trim(pathpkg.Clean(filepath.ToSlash(deletedPath)), "/")
				if conversationPath == deletedPath || strings.HasPrefix(conversationPath, deletedPath+"/") {
					matches = true
					break
				}
			}
		}
		if matches {
			delete(index.Entries, conversationPath)
			removed = true
		}
	}
	if !removed {
		return nil
	}
	index.UpdatedAt = time.Now().Format(time.RFC3339)
	return writeLocalChatHistoryIndex(indexLocalPath, &index)
}

func workflowScheduleIDBySessionID(workflowPath string) map[string]string {
	runs, ok, err := readLocalScheduleRuns(workflowPath)
	if err != nil || !ok {
		return nil
	}
	return scheduleIDBySessionIDFromRuns(runs)
}

func chatHistoryScheduleIDBySessionID(_ string, workspacePath string) map[string]string {
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	if workspacePath != "" {
		return workflowScheduleIDBySessionID(workspacePath)
	}
	return nil
}

func scheduleIDBySessionIDFromRuns(runs []ScheduleRunEntry) map[string]string {
	out := make(map[string]string, len(runs))
	for _, run := range runs {
		sessionID := strings.TrimSpace(run.SessionID)
		scheduleID := strings.TrimSpace(run.ScheduleID)
		if sessionID != "" && scheduleID != "" {
			out[sessionID] = scheduleID
			if prefix := chatHistoryScheduleSessionPrefix(sessionID); prefix != "" {
				out["prefix:"+prefix] = scheduleID
			}
		}
	}
	return out
}

func workflowBuilderHistoryDisplayKey(sessionID string, scheduleIDBySessionID map[string]string) string {
	return chatHistoryDisplayKey(sessionID, scheduleIDBySessionID)
}

// chatHistoryDisplayKey identifies ONE row in the history list.
//
// Schedule sessions used to collapse to "schedule:<id>", one row per schedule,
// on the reasoning that repeated runs "already have detailed history in
// schedule-runs.json". They do not: a run entry carries only status,
// duration_ms, started_at and session_id -- no error text, and no route into
// the conversation. So every run after the newest was discarded along with the
// only record of what it did. One workflow here had four runs of one schedule
// in a day (312 / 21 / 355 / 309 messages, one of them a failure) and the list
// showed a single row, hiding both the failure and the entire previous day.
//
// Each run is its own session with its own file and its own outcome, so each
// gets its own row. Keying by session id still collapses the case the dedupe
// was really protecting against -- several files written for one session when
// a run resumes the same CLI thread underneath.
func chatHistoryDisplayKey(sessionID string, _ map[string]string) string {
	return "session:" + sessionID
}

func chatHistoryScheduleSessionPrefix(sessionID string) string {
	if strings.HasPrefix(sessionID, "schedule-") {
		parts := strings.SplitN(sessionID, "--", 2)
		if len(parts) != 2 {
			return ""
		}
		return chatHistorySchedulePrefixBeforeTimestamp(parts[1])
	}
	if strings.HasPrefix(sessionID, "sched_") {
		return chatHistorySchedulePrefixBeforeTimestamp(strings.TrimPrefix(sessionID, "sched_"))
	}
	return ""
}

func chatHistorySchedulePrefixBeforeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "_"); idx > 0 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

func workflowBuilderConversationFiles(workflowDir string) ([]string, error) {
	builderDir := filepath.Join(workflowDir, "builder")
	out := make([]string, 0)
	legacyRootMatches, err := filepath.Glob(filepath.Join(builderDir, "session-*-conversation.json"))
	if err != nil {
		return nil, err
	}
	out = append(out, legacyRootMatches...)
	conversationDir := filepath.Join(builderDir, "conversation")
	err = filepath.WalkDir(conversationDir, func(candidate string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "session-") && strings.HasSuffix(name, "-conversation.json") {
			out = append(out, candidate)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return out, nil
	}
	return out, err
}

func workflowRelativeConversationPath(workflowPath, workflowDir, convPath string) string {
	rel, err := filepath.Rel(workflowDir, convPath)
	if err != nil {
		return pathpkg.Join(workflowPath, "builder", filepath.Base(convPath))
	}
	return pathpkg.Join(workflowPath, filepath.ToSlash(rel))
}

func resolveLocalChatHistoryDir(workspaceRoot string) (string, bool) {
	candidates := []string{
		filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspaceRoot)),
		filepath.Join("workspace-docs", filepath.FromSlash(workspaceRoot)),
		filepath.Join("..", "workspace-docs", filepath.FromSlash(workspaceRoot)),
		filepath.Join("/app/workspace-docs", filepath.FromSlash(workspaceRoot)),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func resolveLocalWorkflowDir(workflowPath string) (string, bool) {
	workflowPath = normalizeChatHistoryWorkspacePath(workflowPath)
	if workflowPath == "" {
		return "", false
	}
	candidates := []string{
		filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workflowPath)),
		filepath.Join("workspace-docs", filepath.FromSlash(workflowPath)),
		filepath.Join("..", "workspace-docs", filepath.FromSlash(workflowPath)),
		filepath.Join("/app/workspace-docs", filepath.FromSlash(workflowPath)),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func readLocalChatHistorySession(userID, workspaceRoot, workflowPath string, file localChatHistoryFile) (ChatHistorySession, bool) {
	data, err := readConversationFileDirect(file.convPath)
	if err != nil {
		return ChatHistorySession{}, false
	}
	session, ok := parseLocalChatHistorySession(userID, workspaceRoot, workflowPath, file.sessionID, string(data), file.modTime)
	if ok && file.workspacePath != "" {
		session.ConversationPath = file.workspacePath
	}
	return session, ok
}

func parseLocalChatHistorySession(userID, workspaceRoot, workflowPath, fallbackSessionID, data string, fallbackUpdatedAt time.Time) (ChatHistorySession, bool) {
	var raw struct {
		SessionID    string                    `json:"session_id"`
		Title        string                    `json:"title,omitempty"`
		UserID       string                    `json:"user_id,omitempty"`
		Username     string                    `json:"username,omitempty"`
		AgentMode    string                    `json:"agent_mode"`
		Status       string                    `json:"status,omitempty"`
		Runtime      *ChatHistoryAgentRuntime  `json:"runtime,omitempty"`
		Mode         string                    `json:"workshop_mode,omitempty"`
		BotPlatform  string                    `json:"bot_platform,omitempty"`
		BotChannelID string                    `json:"bot_channel_id,omitempty"`
		BotThreadTS  string                    `json:"bot_thread_ts,omitempty"`
		BotUserID    string                    `json:"bot_user_id,omitempty"`
		BotUserName  string                    `json:"bot_user_name,omitempty"`
		BotUserEmail string                    `json:"bot_user_email,omitempty"`
		History      []llmtypes.MessageContent `json:"conversation_history"`
		CreatedAt    string                    `json:"created_at"`
		UpdatedAt    string                    `json:"updated_at"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return ChatHistorySession{}, false
	}
	if raw.SessionID == "" {
		raw.SessionID = fallbackSessionID
	}
	if raw.UpdatedAt == "" {
		raw.UpdatedAt = raw.CreatedAt
	}
	if raw.UpdatedAt == "" {
		raw.UpdatedAt = fallbackUpdatedAt.Format(time.RFC3339)
	}
	if raw.CreatedAt == "" {
		raw.CreatedAt = raw.UpdatedAt
	}
	if raw.Status == "" {
		raw.Status = "completed"
	}
	raw.Mode = normalizeChatHistoryWorkshopMode(raw.Mode)
	if raw.Runtime != nil && raw.Runtime.WorkshopMode == "" {
		raw.Runtime.WorkshopMode = raw.Mode
	}
	normalizeChatHistoryRuntime(raw.Runtime)

	query := latestHumanText(raw.History)
	if len(query) > 200 {
		query = query[:200] + "..."
	}
	botMeta := normalizeChatHistoryBotMetadata(&ChatHistoryBotMetadata{
		Platform:  raw.BotPlatform,
		ChannelID: raw.BotChannelID,
		ThreadTS:  raw.BotThreadTS,
		UserID:    raw.BotUserID,
		UserName:  raw.BotUserName,
		UserEmail: raw.BotUserEmail,
	})

	ownerID := strings.TrimSpace(raw.UserID)
	if ownerID == "" {
		// Personal chat roots are already isolated by user, so their owner is
		// unambiguous. Workflow-scoped legacy transcripts predate user_id and
		// must stay marked as legacy instead of being attributed to whoever
		// happened to list the shared folder first.
		if workspaceref.MustParse(workspaceRoot).HasOwner() {
			ownerID = userID
		} else {
			ownerID = "default"
		}
	}

	return ChatHistorySession{
		SessionID:           raw.SessionID,
		Title:               strings.TrimSpace(raw.Title),
		AgentMode:           raw.AgentMode,
		Runtime:             raw.Runtime,
		WorkshopMode:        raw.Mode,
		Status:              raw.Status,
		Query:               query,
		UserID:              ownerID,
		Username:            chatHistoryUsername(ownerID, raw.Username),
		WorkspacePath:       workflowPath,
		ConversationPath:    pathpkg.Join(workspaceRoot, raw.SessionID, "conversation.json"),
		BotPlatform:         stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.Platform }),
		BotChannelID:        stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.ChannelID }),
		BotThreadTS:         stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.ThreadTS }),
		BotUserID:           stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserID }),
		BotUserName:         stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserName }),
		BotUserEmail:        stringOrEmpty(botMeta, func(meta *ChatHistoryBotMetadata) string { return meta.UserEmail }),
		CreatedAt:           raw.CreatedAt,
		UpdatedAt:           raw.UpdatedAt,
		MessageCount:        len(raw.History),
		PreviewMessages:     chatHistoryPreviewMessages(raw.History),
		attributionVerified: true,
	}, true
}

// chatHistoryUsername resolves a durable author id to the current account
// name. The stored name is only a fallback for deleted/renamed accounts; it
// keeps historical rows understandable without making it an authorization
// input.
func chatHistoryUsername(userID, stored string) string {
	if rec := directoryUserFor(strings.TrimSpace(userID), "", ""); rec != nil {
		return rec.Username
	}
	if stored = strings.TrimSpace(stored); stored != "" {
		return stored
	}
	if strings.TrimSpace(userID) == "default" {
		return "System / legacy"
	}
	return "Former user"
}

// repairStaleChatHistoryAttribution corrects an index entry whose user_id/
// username were frozen as "default"/"System / legacy" at a session's first
// save -- before the real acting user was resolved -- while every later save
// correctly rewrote the conversation file itself (updatePersistedChatHistoryIndex
// logs and continues on a failed index write, so a transient failure there
// leaves the index permanently behind the file it's supposed to mirror).
//
// The existing "repaired on next listing" backfill (decorateChatHistorySessions
// calling chatHistoryUsername) can never fix this on its own: it only
// re-derives Username from the index's own (already wrong) UserID, which just
// re-confirms the same stale fallback forever. This reads the real attribution
// from the source conversation file and, when it disagrees, corrects both the
// in-memory session being returned and the persisted index entry, so the
// listing self-heals instead of staying wrong indefinitely.
//
// A no-op for genuinely legacy sessions (the conversation file itself also has
// no real user_id) -- those are correctly labeled "System / legacy".
func repairStaleChatHistoryAttribution(session *ChatHistorySession) {
	if session == nil || session.attributionVerified {
		return
	}
	userID := strings.TrimSpace(session.UserID)
	if userID != "" && userID != "default" {
		return
	}
	convPath := strings.TrimSpace(session.ConversationPath)
	if convPath == "" {
		return
	}
	// Serialize verification per index. The Recent panel issues overlapping
	// list requests; without taking this lock before the transcript read, both
	// requests can reopen the same large legacy files during migration.
	indexPath := chatHistoryIndexWorkspacePath(userID, convPath)
	if indexPath == "" {
		return
	}
	mutex := chatHistoryIndexMutex(indexPath)
	mutex.Lock()
	defer mutex.Unlock()

	indexData, exists, err := readFileFromWorkspace(context.Background(), indexPath)
	if err != nil || !exists {
		return
	}
	index := newChatHistoryIndex()
	if err := json.Unmarshal([]byte(indexData), &index); err != nil {
		return
	}
	index.normalize()
	entry, ok := index.Entries[convPath]
	if !ok || strings.TrimSpace(entry.Session.UserID) != userID {
		return
	}
	if entry.AttributionVerified {
		session.UserID = entry.Session.UserID
		session.Username = entry.Session.Username
		session.attributionVerified = true
		return
	}

	data, exists, err := readFileFromWorkspace(context.Background(), convPath)
	if err != nil || !exists {
		return
	}
	var attribution struct {
		UserID   string `json:"user_id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(data), &attribution); err != nil {
		return
	}
	realUserID := strings.TrimSpace(attribution.UserID)
	session.attributionVerified = true
	attributionChanged := realUserID != "" && realUserID != "default" && realUserID != userID
	if attributionChanged {
		realUsername := chatHistoryUsername(realUserID, attribution.Username)
		session.UserID = realUserID
		session.Username = realUsername
	}

	if attributionChanged {
		entry.Session.UserID = session.UserID
		entry.Session.Username = session.Username
	}
	entry.AttributionVerified = true
	index.Entries[convPath] = entry
	index.UpdatedAt = time.Now().Format(time.RFC3339)

	updated, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return
	}
	if err := writeRawFileToWorkspace(context.Background(), indexPath, string(updated)); err != nil {
		logfWithContext(newServerLogContext("", "", "", realUserID, "", session.SessionID),
			"[CHAT_HISTORY] Failed to repair stale index attribution for %s: %v", convPath, err)
	}
}

// normalizeChatHistoryWorkshopMode canonicalizes a mode string from any of
// the supported input forms into one of the two backend mode names:
// "workshop", "run", or "" (unknown / unset).
//
// MERGE NOTE: "builder", "optimizer", and "reporting" are legacy input
// names. All three map to "workshop", the unified mode introduced in Step 5
// of the prompt-restructure migration. Persisted sessions saved before the
// merge still arrive with the legacy names — they continue to load and
// behave like workshop sessions because the merged tool list is a strict
// superset of all three pre-merge surfaces.
func normalizeChatHistoryWorkshopMode(mode string) string {
	trimmed := strings.ToLower(strings.TrimSpace(mode))
	if trimmed == "" {
		return ""
	}
	// Only "run" and "workshop" exist. Every retired name — builder,
	// optimizer, reporting, eval, output, ask, debugger, runner — resolves
	// to one of them rather than being enumerated, so a session persisted
	// under any older name still loads.
	switch trimmed {
	case "run", "ask", "debugger", "runner":
		return "run"
	default:
		return "workshop"
	}
}

// chatHistoryResumeModesCompatible keeps provider-native context and durable
// replay inside the prompt/tool boundary that created it. Workflow chats saved
// before workshop_mode was persisted are legacy Builder chats, so they may
// continue in Workshop mode but must never be adopted by Run mode.
//
// A blank current mode is used by non-workflow product chats and has no
// Workshop/Run boundary to enforce.
func chatHistoryResumeModesCompatible(restoredMode, currentMode string) bool {
	current := normalizeChatHistoryWorkshopMode(currentMode)
	if current == "" {
		return true
	}
	restored := normalizeChatHistoryWorkshopMode(restoredMode)
	if restored == "" {
		return current == "workshop"
	}
	return restored == current
}

func paginateChatHistorySessions(sessions []ChatHistorySession, limit, offset int) []ChatHistorySession {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	if offset >= len(sessions) {
		return []ChatHistorySession{}
	}
	sessions = sessions[offset:]
	if limit > 0 && limit < len(sessions) {
		sessions = sessions[:limit]
	}
	return sessions
}

func chatHistoryDataMatchesWorkspace(data, workflowPath string) bool {
	workflowPath = normalizeChatHistoryWorkspacePath(workflowPath)
	if workflowPath == "" {
		return true
	}
	workflowName := pathpkg.Base(workflowPath)
	return strings.Contains(data, workflowPath) ||
		strings.Contains(data, filepath.ToSlash(filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workflowPath)))) ||
		strings.Contains(data, "/workspace-docs/"+workflowPath) ||
		strings.Contains(data, "Workflow/"+workflowName+"/")
}

func normalizeChatHistoryWorkspacePath(workspacePath string) string {
	workspacePath = strings.TrimSpace(strings.Trim(workspacePath, "/"))
	if workspacePath == "" {
		return ""
	}
	cleaned := pathpkg.Clean(workspacePath)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return cleaned
}

func latestHumanText(history []llmtypes.MessageContent) string {
	fallbackText := ""
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		role := strings.ToLower(strings.TrimSpace(string(msg.Role)))
		if role != "human" && role != "user" {
			continue
		}
		textParts := []string{}
		for _, part := range msg.Parts {
			if text := strings.TrimSpace(chatHistoryPartText(part)); text != "" {
				textParts = append(textParts, text)
			}
		}
		cleaned := cleanChatHistoryQuery(strings.Join(textParts, "\n\n"))
		if cleaned == "" || shouldSkipChatHistoryPreviewText(cleaned) || isClaudeLocalCommandRecord(cleaned) {
			continue
		}
		if fallbackText == "" {
			fallbackText = cleaned
		}
		if !isLowSignalChatHistoryQuery(cleaned) {
			return cleaned
		}
	}
	return fallbackText
}

func isLowSignalChatHistoryQuery(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, ".!?,;:-_ \t\n\r")
	if normalized == "" {
		return true
	}
	if len([]rune(normalized)) <= 2 {
		return true
	}
	lowSignal := map[string]bool{
		"hi":              true,
		"hello":           true,
		"hey":             true,
		"ok":              true,
		"okay":            true,
		"thanks":          true,
		"thank you":       true,
		"yes":             true,
		"no":              true,
		"done":            true,
		"lets start":      true,
		"let's start":     true,
		"start":           true,
		"continue":        true,
		"please continue": true,
	}
	return lowSignal[normalized]
}

func chatHistoryPreviewMessages(history []llmtypes.MessageContent) []ChatHistoryPreviewMessage {
	const maxPreviewMessages = 6
	const maxPreviewChars = 360

	messages := []ChatHistoryPreviewMessage{}
	for _, msg := range history {
		role := strings.ToLower(strings.TrimSpace(string(msg.Role)))
		if role != "human" && role != "user" && role != "ai" && role != "assistant" {
			continue
		}
		textParts := []string{}
		for _, part := range msg.Parts {
			if text := strings.TrimSpace(chatHistoryPartText(part)); text != "" {
				textParts = append(textParts, text)
			}
		}
		text := strings.TrimSpace(strings.Join(textParts, "\n\n"))
		if text == "" {
			continue
		}
		text = cleanChatHistoryQuery(text)
		if shouldSkipChatHistoryPreviewText(text) || isClaudeLocalCommandMessage(role, text) {
			continue
		}
		if len(text) > maxPreviewChars {
			text = strings.TrimSpace(text[:maxPreviewChars]) + "..."
		}
		displayRole := role
		if displayRole == "user" {
			displayRole = "human"
		}
		if displayRole == "assistant" {
			displayRole = "ai"
		}
		messages = append(messages, ChatHistoryPreviewMessage{
			Role: displayRole,
			Text: text,
		})
	}
	if len(messages) > maxPreviewMessages {
		messages = messages[len(messages)-maxPreviewMessages:]
	}
	return messages
}

func chatHistoryPartText(part interface{}) string {
	switch v := part.(type) {
	case llmtypes.TextContent:
		return v.Text
	case map[string]interface{}:
		if t, ok := v["Text"].(string); ok {
			return t
		}
		if t, ok := v["text"].(string); ok {
			return t
		}
	}
	return ""
}

func shouldSkipChatHistoryPreviewText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "[AUTO-NOTIFICATION]") ||
		strings.HasPrefix(trimmed, "[Previous tool call") ||
		strings.HasPrefix(trimmed, "[Previous tool result")
}

var restoredConversationContextMarkers = []string{
	"\n\nPrevious workflow-builder conversation file:",
	"\n\nPrevious builder chat file available:",
	"\n\nPrevious conversation file:",
}

func cleanChatHistoryQuery(text string) string {
	text = stripSessionMode(text)
	for _, marker := range restoredConversationContextMarkers {
		if idx := strings.Index(text, marker); idx >= 0 {
			return strings.TrimSpace(text[:idx])
		}
	}
	return strings.TrimSpace(text)
}

func appendRestoredConversationContext(query, path string) string {
	query = strings.TrimSpace(query)
	path = strings.TrimSpace(path)
	if path == "" {
		return query
	}
	for _, marker := range restoredConversationContextMarkers {
		if strings.Contains(query, strings.TrimSpace(marker)) {
			return query
		}
	}
	return query + "\n\nPrevious conversation file: " + path + "\nThis file is JSON with a top-level conversation_history array. User messages have Role \"human\" or \"user\" and text in Parts[].Text; assistant replies have Role \"ai\" or \"assistant\". Scan conversation_history from the end for recent user/assistant Text parts."
}

func shouldAttachRestoredConversationFallback(runtime *ChatHistoryAgentRuntime, currentProvider, currentWorkshopMode string) bool {
	if runtime == nil {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(runtime.Kind), "coding_agent") {
		return true
	}
	if !runtime.ResumeSupported || (strings.TrimSpace(runtime.ExternalSessionID) == "" && strings.TrimSpace(runtime.ProjectDirID) == "") {
		return true
	}

	runtimeProvider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	provider := strings.ToLower(strings.TrimSpace(currentProvider))
	if runtimeProvider == "" || (provider != "" && runtimeProvider != provider) {
		return true
	}

	runtimeMode := normalizeChatHistoryWorkshopMode(runtime.WorkshopMode)
	mode := normalizeChatHistoryWorkshopMode(currentWorkshopMode)
	if runtimeMode != "" && mode != "" && runtimeMode != mode {
		return true
	}

	return false
}

func FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath string) (string, bool, error) {
	sessionID = sanitizeChatHistorySessionID(sessionID)
	if sessionID == "" {
		return "", false, nil
	}
	sessions, err := ListChatHistorySessions(userID, maxChatHistoryFallbackScan, 0, workspacePath)
	if err != nil {
		return "", false, err
	}
	for _, session := range sessions {
		if session.SessionID == sessionID && strings.TrimSpace(session.ConversationPath) != "" {
			return session.ConversationPath, true, nil
		}
	}
	return "", false, nil
}

const maxChatHistoryTitleRunes = 120

func normalizeChatHistoryTitle(title string) (string, error) {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	if len([]rune(title)) > maxChatHistoryTitleRunes {
		return "", fmt.Errorf("title must be %d characters or fewer", maxChatHistoryTitleRunes)
	}
	return title, nil
}

// RenameChatHistorySession writes the title into both the transcript and its
// listing index. The transcript remains authoritative if an older deployment
// or an interrupted write leaves the index stale.
func RenameChatHistorySession(userID, sessionID, workspacePath, title string) error {
	if userID == "" {
		userID = "default"
	}
	sessionID = sanitizeChatHistorySessionID(sessionID)
	if sessionID == "" {
		return fmt.Errorf("conversation not found")
	}
	var err error
	title, err = normalizeChatHistoryTitle(title)
	if err != nil {
		return err
	}
	conversationPath, found, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath)
	if err != nil {
		return err
	}
	if !found {
		// Work creates a tab and assigns its product session before the first
		// transcript save. Record the requested title in that user's project index
		// instead of making rename wait for the running turn to finish. Workflow
		// chats retain the existing author check in the route and are not allowed
		// through this private Work-only fallback.
		var workProject bool
		conversationPath, workProject = workProjectChatHistoryConversationPath(userID, workspacePath, sessionID, time.Now())
		if !workProject {
			return fmt.Errorf("conversation not found")
		}
	}
	lock := chatConversationMutex(conversationPath)
	lock.Lock()
	defer lock.Unlock()
	raw, exists, err := readFileFromWorkspace(context.Background(), conversationPath)
	if err != nil {
		return err
	}
	if !exists {
		return persistChatHistoryTitleInIndex(userID, sessionID, conversationPath, title, 0, true)
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return fmt.Errorf("decode conversation: %w", err)
	}
	record["title"] = title
	advanceChatConversationRevision(record)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode conversation: %w", err)
	}
	if err := writeRawFileToWorkspace(context.Background(), conversationPath, string(encoded)); err != nil {
		return err
	}

	return persistChatHistoryTitleInIndex(userID, sessionID, conversationPath, title, int64(len(encoded)), true)
}

func persistChatHistoryTitleInIndex(userID, sessionID, conversationPath, title string, sourceSize int64, create bool) error {
	indexPath := chatHistoryIndexWorkspacePath(userID, conversationPath)
	if indexPath == "" {
		return nil
	}
	mutex := chatHistoryIndexMutex(indexPath)
	mutex.Lock()
	defer mutex.Unlock()
	index := newChatHistoryIndex()
	indexRaw, indexExists, err := readFileFromWorkspace(context.Background(), indexPath)
	if err != nil {
		return err
	}
	if indexExists {
		if err := json.Unmarshal([]byte(indexRaw), &index); err != nil {
			return fmt.Errorf("decode %s: %w", indexPath, err)
		}
	}
	index.normalize()
	updated := false
	for indexedPath, entry := range index.Entries {
		if sanitizeChatHistorySessionID(entry.Session.SessionID) != sessionID {
			continue
		}
		entry.Session.Title = title
		if indexedPath == conversationPath && sourceSize > 0 {
			entry.SourceSize = sourceSize
		}
		index.Entries[indexedPath] = entry
		updated = true
	}
	if !updated && create {
		now := time.Now().Format(time.RFC3339)
		index.Entries[conversationPath] = chatHistoryIndexEntry{
			Session: ChatHistorySession{
				SessionID:        sessionID,
				Title:            title,
				AgentMode:        "multi-agent",
				Status:           "pending",
				UserID:           userID,
				Username:         chatHistoryUsername(userID, ""),
				WorkspacePath:    chatHistoryWorkspacePathFromConversation(conversationPath),
				ConversationPath: conversationPath,
				CreatedAt:        now,
				UpdatedAt:        now,
			},
			SourceSize:          sourceSize,
			AttributionVerified: true,
		}
		updated = true
	}
	if !updated {
		return nil
	}
	index.Version = chatHistoryIndexVersion
	index.UpdatedAt = time.Now().Format(time.RFC3339)
	updatedIndex, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", indexPath, err)
	}
	return writeRawFileToWorkspace(context.Background(), indexPath, string(updatedIndex))
}

// cleanChatHistoryForPersistence removes hidden prompt context that the frontend
// appends for model-only use. Persisting that context causes chained /resume
// selections to point at older conversations instead of the visible user turn.
func cleanChatHistoryForPersistence(history []llmtypes.MessageContent) []llmtypes.MessageContent {
	if len(history) == 0 {
		return history
	}
	cleaned := make([]llmtypes.MessageContent, len(history))
	for i, msg := range history {
		cleaned[i] = msg
		role := strings.ToLower(strings.TrimSpace(string(msg.Role)))
		if role != "human" && role != "user" {
			continue
		}
		if len(msg.Parts) == 0 {
			continue
		}
		parts := make([]llmtypes.ContentPart, len(msg.Parts))
		copy(parts, msg.Parts)
		changed := false
		for partIndex, part := range parts {
			if textPart, ok := part.(llmtypes.TextContent); ok {
				cleanText := cleanChatHistoryQuery(textPart.Text)
				if cleanText != textPart.Text {
					parts[partIndex] = llmtypes.TextContent{Text: cleanText}
					changed = true
				}
			}
		}
		if changed {
			cleaned[i].Parts = parts
		}
	}
	return cleaned
}

// ReadChatHistoryConversation reads the persisted conversation JSON for a session.
func ReadChatHistoryConversation(userID, sessionID, workspacePath string) (json.RawMessage, error) {
	if userID == "" {
		userID = "default"
	}
	sessionID = sanitizeChatHistorySessionID(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("conversation not found")
	}
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	if workspacePath != "" {
		if data, ok, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath); ok || err != nil {
			return data, err
		}
		if data, ok, err := readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspacePath); ok || err != nil {
			return data, err
		}
		sessions, err := ListChatHistorySessions(userID, maxChatHistoryFallbackScan, 0, workspacePath)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if session.SessionID != sessionID || session.ConversationPath == "" {
				continue
			}
			data, exists, err := readFileFromWorkspace(context.Background(), session.ConversationPath)
			if err != nil {
				return nil, err
			}
			if exists {
				return json.RawMessage(data), nil
			}
		}
	}
	if data, ok, err := readUserChatHistoryConversationDirect(userID, sessionID); ok || err != nil {
		return data, err
	}
	if data, ok, err := readUserChatHistoryConversationFromWorkspace(userID, sessionID); ok || err != nil {
		return data, err
	}
	convPath := pathpkg.Join(chatHistoryRoot(userID), sessionID, "conversation.json")
	data, exists, err := readFileFromWorkspace(context.Background(), convPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("conversation not found")
	}
	return json.RawMessage(data), nil
}

func ReadChatHistoryRuntimeFromPath(userID, conversationPath string) (*ChatHistoryAgentRuntime, bool, error) {
	normalizedPath, ok := normalizeRestoredChatHistoryConversationPath(userID, conversationPath)
	if !ok {
		return nil, false, nil
	}

	data, exists, err := readChatHistoryConversationDataFromPath(normalizedPath)
	if err != nil || !exists {
		return nil, exists, err
	}

	runtime, err := chatHistoryRuntimeFromJSON(data)
	if err != nil {
		return nil, true, err
	}
	return runtime, true, nil
}

func ReadChatHistoryRuntimeForSession(userID, sessionID, workspacePath string) (*ChatHistoryAgentRuntime, bool, error) {
	data, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil {
		if strings.Contains(err.Error(), "conversation not found") {
			return nil, false, nil
		}
		return nil, false, err
	}
	runtime, err := chatHistoryRuntimeFromJSON(data)
	if err != nil {
		return nil, true, err
	}
	return runtime, true, nil
}

func ReadChatHistoryTerminalSnapshotsFromPath(userID, conversationPath string) ([]terminals.Snapshot, bool, error) {
	normalizedPath, ok := normalizeRestoredChatHistoryConversationPath(userID, conversationPath)
	if !ok {
		return nil, false, nil
	}

	data, exists, err := readChatHistoryConversationDataFromPath(normalizedPath)
	if err != nil || !exists {
		return nil, exists, err
	}

	snapshots, err := chatHistoryTerminalSnapshotsFromJSON(data)
	if err != nil {
		return nil, true, err
	}
	return snapshots, true, nil
}

func ReadChatHistoryTerminalSnapshotsForSession(userID, sessionID, workspacePath string) ([]terminals.Snapshot, bool, error) {
	data, err := ReadChatHistoryConversation(userID, sessionID, workspacePath)
	if err != nil {
		if strings.Contains(err.Error(), "conversation not found") {
			return nil, false, nil
		}
		return nil, false, err
	}
	snapshots, err := chatHistoryTerminalSnapshotsFromJSON(data)
	if err != nil {
		return nil, true, err
	}
	return snapshots, true, nil
}

func readChatHistoryConversationDataFromPath(normalizedPath string) ([]byte, bool, error) {
	normalizedPath = strings.TrimSpace(filepath.ToSlash(normalizedPath))
	if normalizedPath == "" {
		return nil, false, nil
	}
	localPath := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(normalizedPath))
	if localData, err := readConversationFileDirect(localPath); err == nil {
		return localData, true, nil
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	workspaceData, exists, err := readFileFromWorkspace(context.Background(), normalizedPath)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	return []byte(workspaceData), true, nil
}

func chatHistoryTerminalSnapshotsFromJSON(data []byte) ([]terminals.Snapshot, error) {
	var raw struct {
		TerminalSnapshots []terminals.Snapshot `json:"terminal_snapshots,omitempty"`
		TerminalSnapshot  *terminals.Snapshot  `json:"terminal_snapshot,omitempty"`
		SessionID         string               `json:"session_id,omitempty"`
		Runtime           *ChatHistoryAgentRuntime
		UIEvents          []chatHistoryTerminalUIEvent `json:"ui_events,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	snapshots := raw.TerminalSnapshots
	if len(snapshots) == 0 && raw.TerminalSnapshot != nil {
		snapshots = []terminals.Snapshot{*raw.TerminalSnapshot}
	}
	out := make([]terminals.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		prepared, ok := prepareChatHistoryTerminalSnapshot(snapshot)
		if ok {
			out = append(out, prepared)
		}
	}
	if len(out) == 0 {
		out = chatHistoryTerminalSnapshotsFromUIEvents(raw.SessionID, raw.Runtime, raw.UIEvents)
	}
	return out, nil
}

type chatHistoryTerminalUIEvent struct {
	Type          string                 `json:"type,omitempty"`
	Timestamp     time.Time              `json:"timestamp,omitempty"`
	SessionID     string                 `json:"session_id,omitempty"`
	ExecutionID   string                 `json:"execution_id,omitempty"`
	ExecutionKind string                 `json:"execution_kind,omitempty"`
	Data          *chatHistoryAgentEvent `json:"data,omitempty"`
}

type chatHistoryAgentEvent struct {
	Type      string                         `json:"type,omitempty"`
	Timestamp time.Time                      `json:"timestamp,omitempty"`
	SessionID string                         `json:"session_id,omitempty"`
	Content   string                         `json:"content,omitempty"`
	Metadata  map[string]interface{}         `json:"metadata,omitempty"`
	Data      *chatHistoryStreamingEventData `json:"data,omitempty"`
}

type chatHistoryStreamingEventData struct {
	Timestamp  time.Time              `json:"timestamp,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	Content    string                 `json:"content,omitempty"`
	ChunkIndex int                    `json:"chunk_index,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

func chatHistoryTerminalSnapshotsFromUIEvents(sessionID string, runtime *ChatHistoryAgentRuntime, events []chatHistoryTerminalUIEvent) []terminals.Snapshot {
	if len(events) == 0 {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)

	var selected terminals.Snapshot
	for _, event := range events {
		snapshot, ok := chatHistoryTerminalSnapshotFromUIEvent(sessionID, runtime, event)
		if !ok {
			continue
		}
		if selected.Content == "" || persistedTerminalSnapshotPreferred(snapshot, selected) {
			selected = snapshot
		}
	}
	if strings.TrimSpace(selected.Content) == "" {
		return nil
	}
	return []terminals.Snapshot{selected}
}

func chatHistoryTerminalSnapshotFromUIEvent(sessionID string, runtime *ChatHistoryAgentRuntime, event chatHistoryTerminalUIEvent) (terminals.Snapshot, bool) {
	if event.Data == nil {
		return terminals.Snapshot{}, false
	}
	metadata := event.Data.Metadata
	if event.Data.Data != nil && len(event.Data.Data.Metadata) > 0 {
		metadata = event.Data.Data.Metadata
	}
	if strings.ToLower(strings.TrimSpace(chatHistoryMetadataString(metadata, "kind"))) != "terminal" {
		return terminals.Snapshot{}, false
	}
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	agentEventType := strings.ToLower(strings.TrimSpace(event.Data.Type))
	if eventType != "streaming_chunk" && agentEventType != "streaming_chunk" {
		return terminals.Snapshot{}, false
	}
	tmuxSession := chatHistoryMetadataString(metadata,
		"tmux_session",
		"codex_interactive_session",
		"claude_code_interactive_session",
		"gemini_interactive_session",
	)
	// A tmux_session is no longer required: multi-agent chats
	// stream their coding-agent terminal over a non-tmux transport, so those
	// terminal events carry no tmux_session. Reconstruct them anyway (keyed by
	// session) so the last capture is restorable from ui_events on sessions
	// saved before terminal_snapshots persisted non-tmux panes.

	content := event.Data.Content
	chunkIndex := 0
	if event.Data.Data != nil {
		if strings.TrimSpace(event.Data.Data.Content) != "" {
			content = event.Data.Data.Content
		}
		chunkIndex = event.Data.Data.ChunkIndex
	}
	if strings.TrimSpace(content) == "" {
		return terminals.Snapshot{}, false
	}

	if sessionID == "" {
		sessionID = strings.TrimSpace(event.SessionID)
	}
	if sessionID == "" && event.Data.Data != nil {
		sessionID = strings.TrimSpace(event.Data.Data.SessionID)
	}
	if sessionID == "" {
		return terminals.Snapshot{}, false
	}

	provider := chatHistoryMetadataString(metadata, "provider")
	workflowPath := ""
	if runtime != nil {
		if provider == "" {
			provider = strings.TrimSpace(runtime.Provider)
		}
		workflowPath = strings.TrimSpace(runtime.WorkspacePath)
	}
	label := "Restored terminal"
	if provider != "" {
		label = "Restored " + provider
	}
	updatedAt := chatHistoryTerminalEventTime(event)
	snapshot := terminals.Snapshot{
		TerminalID:    sessionID + ":main:" + sessionID,
		SessionID:     sessionID,
		OwnerID:       "main:" + sessionID,
		ExecutionID:   strings.TrimSpace(event.ExecutionID),
		ExecutionKind: strings.TrimSpace(event.ExecutionKind),
		Label:         label,
		Scope:         "main_agent",
		WorkflowPath:  workflowPath,
		StepID:        "main_agent:" + sessionID,
		TmuxSession:   tmuxSession,
		Content:       content,
		ChunkIndex:    chunkIndex,
		Active:        false,
		State:         "stale",
		CreatedAt:     updatedAt,
		UpdatedAt:     updatedAt,
	}
	// Only stamp tmux transport/source when the event actually came from a tmux
	// pane. Non-tmux multi-agent terminals leave these empty so a restored
	// static snapshot isn't treated as live-tmux-recapturable.
	if tmuxSession != "" {
		snapshot.StepTransport = "tmux"
		snapshot.ContentSource = "tmux_capture"
	}
	if snapshot.ExecutionID == "" {
		snapshot.ExecutionID = "main:" + sessionID
	}
	if snapshot.ExecutionKind == "" {
		snapshot.ExecutionKind = "main_agent"
	}
	return prepareChatHistoryTerminalSnapshot(snapshot)
}

func chatHistoryTerminalEventTime(event chatHistoryTerminalUIEvent) time.Time {
	if !event.Timestamp.IsZero() {
		return event.Timestamp
	}
	if event.Data != nil {
		if !event.Data.Timestamp.IsZero() {
			return event.Data.Timestamp
		}
		if event.Data.Data != nil && !event.Data.Data.Timestamp.IsZero() {
			return event.Data.Data.Timestamp
		}
	}
	return time.Now()
}

func chatHistoryMetadataString(metadata map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func chatHistoryRuntimeFromJSON(data []byte) (*ChatHistoryAgentRuntime, error) {
	var raw struct {
		Runtime *ChatHistoryAgentRuntime `json:"runtime,omitempty"`
		Mode    string                   `json:"workshop_mode,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.Runtime == nil {
		return nil, nil
	}
	if raw.Runtime.WorkshopMode == "" {
		raw.Runtime.WorkshopMode = normalizeChatHistoryWorkshopMode(raw.Mode)
	}
	normalizeChatHistoryRuntime(raw.Runtime)
	return raw.Runtime, nil
}

func normalizeChatHistoryRuntime(runtime *ChatHistoryAgentRuntime) {
	if runtime == nil {
		return
	}
	runtime.Provider = strings.ToLower(strings.TrimSpace(runtime.Provider))
	runtime.ModelID = strings.TrimSpace(runtime.ModelID)
	runtime.Transport = strings.ToLower(strings.TrimSpace(runtime.Transport))
	if runtime.AgentSessionHandle != nil && !runtime.AgentSessionHandle.Empty() {
		handle := runtime.AgentSessionHandle.Provider
		if runtime.Provider == "" {
			runtime.Provider = strings.ToLower(strings.TrimSpace(handle.Provider))
		}
		if runtime.ModelID == "" {
			runtime.ModelID = strings.TrimSpace(handle.Model)
		}
		if runtime.Transport == "" {
			runtime.Transport = strings.ToLower(strings.TrimSpace(handle.Transport))
		}
		if runtime.ExternalSessionID == "" {
			runtime.ExternalSessionID = strings.TrimSpace(handle.NativeSessionID)
		}
		if runtime.ProjectDirID == "" {
			runtime.ProjectDirID = strings.TrimSpace(handle.ProjectDirID)
		}
		if runtime.ExternalSessionID != "" || runtime.ProjectDirID != "" {
			runtime.ResumeSupported = true
		}
	}
	if runtime.Transport == "" && runtime.Provider != "" {
		if contract, ok := llmproviders.GetCodingAgentProviderContract(llmproviders.Provider(runtime.Provider), runtime.ModelID); ok {
			runtime.Transport = strings.ToLower(string(contract.Transport))
		}
	}
	if runtime.ResumeSupported && strings.TrimSpace(runtime.ResumeFlag) == "" {
		runtime.ResumeFlag = defaultCodingAgentResumeFlag(runtime.Provider)
	}
}

func defaultCodingAgentResumeFlag(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude-code", "cursor-cli":
		return "--resume"
	case "codex-cli":
		return "resume"
	case "pi-cli":
		return "--session-id"
	default:
		return ""
	}
}

func normalizeRestoredChatHistoryConversationPath(userID, conversationPath string) (string, bool) {
	conversationPath = strings.TrimSpace(filepath.ToSlash(conversationPath))
	if conversationPath == "" {
		return "", false
	}
	if idx := strings.LastIndex(conversationPath, "/workspace-docs/"); idx >= 0 {
		conversationPath = conversationPath[idx+len("/workspace-docs/"):]
	}
	conversationPath = strings.TrimPrefix(conversationPath, "/")
	cleaned := pathpkg.Clean(conversationPath)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", false
	}
	userRoot := chatHistoryRoot(userID)
	if cleaned == userRoot || strings.HasPrefix(cleaned, userRoot+"/") {
		return cleaned, true
	}
	if ownedRef := workspaceref.MustParse(cleaned); ownedRef.OwnedBy(userID) && ownedRef.IsProject() && strings.Contains(cleaned, "/builder/conversation/") && strings.HasSuffix(cleaned, ".json") {
		return cleaned, true
	}
	if strings.HasPrefix(cleaned, "Workflow/") && strings.Contains(cleaned, "/builder/") && strings.HasSuffix(cleaned, ".json") {
		return cleaned, true
	}
	return "", false
}

func readUserChatHistoryConversationDirect(userID, sessionID string) (json.RawMessage, bool, error) {
	root := chatHistoryRoot(userID)
	baseDir, ok := resolveLocalChatHistoryDir(root)
	if !ok {
		return nil, false, nil
	}

	patterns := []string{
		filepath.Join(baseDir, "*", chatHistoryConversationFileName(sessionID)),
		filepath.Join(baseDir, sessionID, "conversation.json"),
	}
	type candidate struct {
		path      string
		modTime   time.Time
		updatedAt string
	}
	var latest *candidate
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, true, err
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			data, err := readConversationFileDirect(match)
			if err != nil {
				return nil, true, err
			}
			updatedAt := chatHistoryUpdatedAtFromJSON(string(data))
			if latest == nil || updatedAt > latest.updatedAt || (updatedAt == latest.updatedAt && info.ModTime().After(latest.modTime)) || (updatedAt == latest.updatedAt && info.ModTime().Equal(latest.modTime) && match > latest.path) {
				latest = &candidate{path: match, modTime: info.ModTime(), updatedAt: updatedAt}
			}
		}
	}
	if latest == nil {
		return nil, false, nil
	}
	data, err := readConversationFileDirect(latest.path)
	if err != nil {
		return nil, true, err
	}
	return json.RawMessage(data), true, nil
}

func readUserChatHistoryConversationFromWorkspace(userID, sessionID string) (json.RawMessage, bool, error) {
	root := chatHistoryRoot(userID)
	filePaths, err := listWorkspaceFilesRecursive(context.Background(), root)
	if err != nil {
		return nil, false, err
	}
	var latestData string
	var latestUpdatedAt string
	var latestPath string
	for _, convPath := range filePaths {
		candidateSessionID, ok := chatHistorySessionIDFromWorkspacePath(root, convPath)
		if !ok || candidateSessionID != sessionID {
			continue
		}
		data, exists, err := readFileFromWorkspace(context.Background(), convPath)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			continue
		}
		updatedAt := chatHistoryUpdatedAtFromJSON(data)
		if latestData == "" || updatedAt > latestUpdatedAt || (updatedAt == latestUpdatedAt && convPath > latestPath) {
			latestData = data
			latestUpdatedAt = updatedAt
			latestPath = convPath
		}
	}
	if latestData == "" {
		return nil, false, nil
	}
	return json.RawMessage(latestData), true, nil
}

func readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspacePath string) (json.RawMessage, bool, error) {
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	if sessionID == "" || workspacePath == "" {
		return nil, false, nil
	}

	ctx := context.Background()
	fileName := fmt.Sprintf("session-%s-conversation.json", sessionID)
	candidatePaths := []string{
		pathpkg.Join(workspacePath, "builder", fileName),
	}

	conversationRoot := pathpkg.Join(workspacePath, "builder", "conversation")
	dateFolders, err := listWorkspaceChildFolderNames(ctx, conversationRoot)
	if err != nil {
		return nil, false, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dateFolders)))
	for _, dateFolder := range dateFolders {
		dateFolder = strings.TrimSpace(dateFolder)
		if dateFolder == "" || dateFolder == "." || dateFolder == ".." {
			continue
		}
		candidatePaths = append(candidatePaths, pathpkg.Join(conversationRoot, dateFolder, fileName))
	}

	var latestData string
	var latestUpdatedAt string
	var latestPath string
	for _, candidatePath := range candidatePaths {
		data, exists, err := readFileFromWorkspace(ctx, candidatePath)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			continue
		}
		updatedAt := chatHistoryUpdatedAtFromJSON(data)
		if latestData == "" || updatedAt > latestUpdatedAt || (updatedAt == latestUpdatedAt && candidatePath > latestPath) {
			latestData = data
			latestUpdatedAt = updatedAt
			latestPath = candidatePath
		}
	}
	if latestData == "" {
		return nil, false, nil
	}
	return json.RawMessage(latestData), true, nil
}

func chatHistoryUpdatedAtFromJSON(data string) string {
	var raw struct {
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return ""
	}
	return strings.TrimSpace(raw.UpdatedAt)
}

func sanitizeChatHistorySessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "." || sessionID == ".." ||
		strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") {
		return ""
	}
	return sessionID
}

func readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath string) (json.RawMessage, bool, error) {
	workflowDir, ok := resolveLocalWorkflowDir(workspacePath)
	if !ok {
		return nil, false, nil
	}
	fileName := fmt.Sprintf("session-%s-conversation.json", sessionID)

	type candidate struct {
		path    string
		modTime time.Time
	}
	var latest *candidate
	matches, err := workflowBuilderConversationFiles(workflowDir)
	if err != nil {
		return nil, true, err
	}
	for _, match := range matches {
		if filepath.Base(match) != fileName {
			continue
		}
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		if latest == nil || info.ModTime().After(latest.modTime) || (info.ModTime().Equal(latest.modTime) && match > latest.path) {
			latest = &candidate{path: match, modTime: info.ModTime()}
		}
	}
	if latest == nil {
		return nil, false, nil
	}
	data, err := readConversationFileDirect(latest.path)
	if err != nil {
		return nil, true, err
	}
	return json.RawMessage(data), true, nil
}

func findWorkflowScopedChatHistoryConversationPath(sessionID, workspacePath string) (string, bool, error) {
	sessionID = sanitizeChatHistorySessionID(sessionID)
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	if sessionID == "" || workspacePath == "" {
		return "", false, nil
	}

	fileName := fmt.Sprintf("session-%s-conversation.json", sessionID)
	if workflowDir, ok := resolveLocalWorkflowDir(workspacePath); ok {
		type candidate struct {
			path    string
			modTime time.Time
		}
		var latest *candidate
		matches, err := workflowBuilderConversationFiles(workflowDir)
		if err != nil {
			return "", false, err
		}
		for _, match := range matches {
			if filepath.Base(match) != fileName {
				continue
			}
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			if latest == nil || info.ModTime().After(latest.modTime) || (info.ModTime().Equal(latest.modTime) && match > latest.path) {
				latest = &candidate{path: match, modTime: info.ModTime()}
			}
		}
		if latest != nil {
			return workspaceRelativePathFromLocalPath(latest.path), true, nil
		}
	}

	conversationRoot := pathpkg.Join(workspacePath, "builder", "conversation")
	filePaths, err := listWorkspaceFilesRecursive(context.Background(), conversationRoot)
	if err != nil {
		return "", false, err
	}
	latestPath := ""
	for _, candidatePath := range filePaths {
		candidatePath = filepath.ToSlash(candidatePath)
		if pathpkg.Base(candidatePath) != fileName {
			continue
		}
		if latestPath == "" || candidatePath > latestPath {
			latestPath = candidatePath
		}
	}
	if latestPath == "" {
		return "", false, nil
	}
	return workspaceRelativePathFromLocalPath(latestPath), true, nil
}

func workspaceRelativePathFromLocalPath(localPath string) string {
	slashPath := filepath.ToSlash(localPath)
	root := filepath.ToSlash(fsutil.WorkspaceDocsRoot())
	if rel, err := filepath.Rel(root, localPath); err == nil {
		rel = filepath.ToSlash(rel)
		if rel != "." && !strings.HasPrefix(rel, "../") && rel != ".." {
			return rel
		}
	}
	if idx := strings.LastIndex(slashPath, "/workspace-docs/"); idx >= 0 {
		return slashPath[idx+len("/workspace-docs/"):]
	}
	return strings.TrimPrefix(slashPath, "/")
}

func DeleteChatHistorySession(userID, sessionID, workspacePath string) (ChatHistoryCleanupResult, error) {
	if userID == "" {
		userID = "default"
	}
	sessionID = sanitizeChatHistorySessionID(sessionID)
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	result := ChatHistoryCleanupResult{
		DeletedPaths: []string{},
		Scope:        "global",
	}
	if workspacePath != "" {
		result.Scope = workspacePath
	}
	if sessionID == "" {
		return result, fmt.Errorf("invalid session id")
	}

	if workspacePath != "" {
		return deleteWorkspaceChatHistorySession(result, userID, sessionID, workspacePath)
	}
	return deleteUserChatHistorySession(result, userID, sessionID)
}

func ownedWorkProjectWorkspacePath(userID, workspacePath string) (string, bool) {
	canonical := workspaceref.MustParse(canonicalChatHistoryWorkspacePath(userID, workspacePath))
	// A path that kept another user's prefix is not this user's project.
	if canonical.HasOwner() || !canonical.IsProject() {
		return workspacePath, false
	}
	return canonical.Physical(userID), true
}

func deleteWorkspaceChatHistorySession(result ChatHistoryCleanupResult, userID, sessionID, workspacePath string) (ChatHistoryCleanupResult, error) {
	storageWorkspacePath, workProject := ownedWorkProjectWorkspacePath(userID, workspacePath)
	workflowDir, ok := resolveLocalWorkflowDir(storageWorkspacePath)
	if !ok {
		if workProject {
			return deleteUserChatHistorySession(result, userID, sessionID)
		}
		return result, nil
	}
	fileName := fmt.Sprintf("session-%s-conversation.json", sessionID)
	matches, err := workflowBuilderConversationFiles(workflowDir)
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, convPath := range matches {
		if filepath.Base(convPath) != fileName {
			continue
		}
		if seen[convPath] {
			continue
		}
		seen[convPath] = true
		info, err := os.Stat(convPath)
		if err != nil || info.IsDir() {
			continue
		}
		if err := os.Remove(convPath); err != nil {
			return result, err
		}
		result.DeletedCount++
		result.DeletedPaths = append(result.DeletedPaths, workflowRelativeConversationPath(workspacePath, workflowDir, convPath))
		parentDir := filepath.Dir(convPath)
		if filepath.Base(filepath.Dir(parentDir)) == "conversation" {
			_ = os.Remove(parentDir)
		}
	}
	if result.DeletedCount > 0 {
		indexWorkspacePath := pathpkg.Join(storageWorkspacePath, "builder", "conversation", chatHistoryIndexFileName)
		indexLocalPath := filepath.Join(workflowDir, "builder", "conversation", chatHistoryIndexFileName)
		if err := removeLocalChatHistoryIndexEntries(indexWorkspacePath, indexLocalPath, sessionID, nil); err != nil {
			return result, err
		}
	}
	// Work originally stored product chats in the user's central chat history.
	// Project opening copied those transcripts into the project for compatibility;
	// delete both so the fallback cannot restore a chat the user removed.
	if workProject {
		return deleteUserChatHistorySession(result, userID, sessionID)
	}
	return result, nil
}

func deleteUserChatHistorySession(result ChatHistoryCleanupResult, userID, sessionID string) (ChatHistoryCleanupResult, error) {
	deleteCodeSessionPin(sessionID)
	root := chatHistoryRoot(userID)
	baseDir, ok := resolveLocalChatHistoryDir(root)
	if !ok {
		return result, nil
	}
	patterns := []string{
		filepath.Join(baseDir, "*", chatHistoryConversationFileName(sessionID)),
		filepath.Join(baseDir, sessionID, "conversation.json"),
	}
	seen := map[string]bool{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return result, err
		}
		for _, convPath := range matches {
			if seen[convPath] {
				continue
			}
			seen[convPath] = true
			info, err := os.Stat(convPath)
			if err != nil || info.IsDir() {
				continue
			}

			parentDir := filepath.Dir(convPath)
			if filepath.Base(convPath) == "conversation.json" && filepath.Base(parentDir) == sessionID {
				if err := os.RemoveAll(parentDir); err != nil {
					return result, err
				}
				result.DeletedCount++
				result.DeletedPaths = append(result.DeletedPaths, pathpkg.Join(root, sessionID))
				continue
			}

			if err := os.Remove(convPath); err != nil {
				return result, err
			}
			result.DeletedCount++
			result.DeletedPaths = append(result.DeletedPaths, pathpkg.Join(root, filepath.Base(parentDir), filepath.Base(convPath)))
			_ = os.Remove(parentDir)
		}
	}
	if result.DeletedCount > 0 {
		if err := removeLocalChatHistoryIndexEntries(pathpkg.Join(root, chatHistoryIndexFileName), filepath.Join(baseDir, chatHistoryIndexFileName), sessionID, nil); err != nil {
			return result, err
		}
	}
	return result, nil
}

func DeleteChatHistoryOlderThan(userID string, olderThanDays int, workspacePath string) (ChatHistoryCleanupResult, error) {
	if userID == "" {
		userID = "default"
	}
	if olderThanDays <= 0 {
		olderThanDays = 14
	}
	root := chatHistoryRoot(userID)
	workspacePath = normalizeChatHistoryWorkspacePath(workspacePath)
	cutoff := time.Now().AddDate(0, 0, -olderThanDays)
	result := ChatHistoryCleanupResult{
		DeletedPaths: []string{},
		Cutoff:       cutoff.Format(time.RFC3339),
		Scope:        "global",
	}
	if workspacePath != "" {
		result.Scope = workspacePath
	}

	if workspacePath != "" {
		if _, workProject := ownedWorkProjectWorkspacePath(userID, workspacePath); workProject {
			return deleteOldWorkProjectConversations(userID, workspacePath, cutoff, result)
		}
		if err := deleteOldWorkflowBuilderConversations(&result, workspacePath, cutoff); err != nil {
			return result, err
		}
		if result.DeletedCount > 0 {
			if workflowDir, ok := resolveLocalWorkflowDir(workspacePath); ok {
				indexWorkspacePath := pathpkg.Join(workspacePath, "builder", "conversation", chatHistoryIndexFileName)
				indexLocalPath := filepath.Join(workflowDir, "builder", "conversation", chatHistoryIndexFileName)
				if err := removeLocalChatHistoryIndexEntries(indexWorkspacePath, indexLocalPath, "", result.DeletedPaths); err != nil {
					return result, err
				}
			}
		}
		return result, nil
	}

	baseDir, ok := resolveLocalChatHistoryDir(root)
	if !ok {
		return result, nil
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryName := entry.Name()
		entryDir := filepath.Join(baseDir, entryName)

		// Legacy layout: chat_history/<session-id>/conversation.json
		legacyConvPath := filepath.Join(entryDir, "conversation.json")
		if shouldDelete, err := chatHistoryFileCleanupCandidate(legacyConvPath, cutoff); err != nil {
			return result, err
		} else if shouldDelete {
			if err := os.RemoveAll(entryDir); err != nil {
				return result, err
			}
			result.DeletedCount++
			result.DeletedPaths = append(result.DeletedPaths, pathpkg.Join(root, entryName))
			continue
		}

		// Date-bucket layout: chat_history/YYYY-MM-DD/session-<id>-conversation.json
		matches, err := filepath.Glob(filepath.Join(entryDir, "session-*-conversation.json"))
		if err != nil {
			return result, err
		}
		for _, convPath := range matches {
			shouldDelete, err := chatHistoryFileCleanupCandidate(convPath, cutoff)
			if err != nil {
				return result, err
			}
			if !shouldDelete {
				continue
			}
			if err := os.Remove(convPath); err != nil {
				return result, err
			}
			result.DeletedCount++
			result.DeletedPaths = append(result.DeletedPaths, pathpkg.Join(root, entryName, filepath.Base(convPath)))
		}
		_ = os.Remove(entryDir)
	}
	if result.DeletedCount > 0 {
		if err := removeLocalChatHistoryIndexEntries(pathpkg.Join(root, chatHistoryIndexFileName), filepath.Join(baseDir, chatHistoryIndexFileName), "", result.DeletedPaths); err != nil {
			return result, err
		}
	}
	return result, nil
}

// deleteOldWorkProjectConversations bulk-deletes a Crew project's old,
// non-empty transcripts. Work sessions live in two places — the project dir
// and the user's central chat history — so it enumerates through the same
// session list the UI shows and deletes each old session with
// DeleteChatHistorySession, which already removes both copies.
func deleteOldWorkProjectConversations(userID, workspacePath string, cutoff time.Time, result ChatHistoryCleanupResult) (ChatHistoryCleanupResult, error) {
	sessions, err := listChatHistorySessions(userID, 0, 0, workspacePath, "")
	if err != nil {
		return result, err
	}
	for _, session := range sessions {
		if session.MessageCount == 0 {
			continue
		}
		updatedAt, err := time.Parse(time.RFC3339, session.UpdatedAt)
		if err != nil || !updatedAt.Before(cutoff) {
			continue
		}
		deleted, err := DeleteChatHistorySession(userID, session.SessionID, workspacePath)
		if err != nil {
			return result, err
		}
		result.DeletedCount += deleted.DeletedCount
		result.DeletedPaths = append(result.DeletedPaths, deleted.DeletedPaths...)
	}
	return result, nil
}

func deleteOldWorkflowBuilderConversations(result *ChatHistoryCleanupResult, workspacePath string, cutoff time.Time) error {
	workflowDir, ok := resolveLocalWorkflowDir(workspacePath)
	if !ok {
		return nil
	}
	matches, err := workflowBuilderConversationFiles(workflowDir)
	if err != nil {
		return err
	}
	for _, convPath := range matches {
		shouldDelete, err := chatHistoryFileCleanupCandidate(convPath, cutoff)
		if err != nil {
			return err
		}
		if !shouldDelete {
			continue
		}
		if err := os.Remove(convPath); err != nil {
			return err
		}
		result.DeletedCount++
		result.DeletedPaths = append(result.DeletedPaths, workflowRelativeConversationPath(workspacePath, workflowDir, convPath))
	}
	return nil
}

func chatHistoryFileCleanupCandidate(convPath string, cutoff time.Time) (bool, error) {
	info, err := os.Stat(convPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}

	data, err := readConversationFileDirect(convPath)
	if err != nil {
		return false, err
	}

	var raw struct {
		ConversationHistory []json.RawMessage `json:"conversation_history"`
		CreatedAt           string            `json:"created_at"`
		UpdatedAt           string            `json:"updated_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil
	}
	if len(raw.ConversationHistory) == 0 {
		return false, nil
	}

	timestamp := info.ModTime()
	if parsed, ok := parseChatHistoryCleanupTime(raw.UpdatedAt); ok {
		timestamp = parsed
	} else if parsed, ok := parseChatHistoryCleanupTime(raw.CreatedAt); ok {
		timestamp = parsed
	}

	return timestamp.Before(cutoff), nil
}

func parseChatHistoryCleanupTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed, true
	}
	parsed, err = time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

// appendLiveInputToPersistedChatHistory records a live-steered user message in
// the session's on-disk transcript.
//
// Live input never completes a query turn, so the sole writer of chat history —
// the tail of handleQuery — never runs for these messages. A tmux session driven
// entirely by steering therefore leaves its transcript frozen at the last real
// turn, and a crash or restart resumes showing that stale point. What this
// repairs is the UI record: the coding agent's own conversation is not at risk,
// because the CLI persists it independently and reloads it with --resume.
//
// It patches conversation_history in the existing file rather than rebuilding it
// through persistChatConversationToPathWithTerminalSession, which writes a whole
// new record from its arguments. Rebuilding from a live-input handler would drop
// the fields that handler has no way to supply — above all runtime, which holds
// the ExternalSessionID that --resume needs. Losing that would turn a stale
// transcript into an unresumable session, which is strictly worse than the bug.
//
// A session with no transcript yet is left alone: the first completed turn is
// what establishes the record, and inventing a runtime-less one here would poison
// the resume path the same way.
func (api *StreamingAPI) persistLiveInputUserMessage(ctx context.Context, sessionID, message string) {
	if api == nil {
		return
	}
	userID := GetUserIDFromContext(ctx)
	if api.internalLiveInputPersistenceHandler != nil {
		api.internalLiveInputPersistenceHandler(userID, sessionID, message)
		return
	}
	workspacePath := ""
	api.sessionWorkspaceMu.RLock()
	workspacePath = strings.TrimSpace(api.sessionWorkspaceFolders[sessionID])
	api.sessionWorkspaceMu.RUnlock()
	api.appendLiveInputToPersistedChatHistory(userID, sessionID, workspacePath, message)
}

func (api *StreamingAPI) appendLiveInputToPersistedChatHistory(userID, sessionID, workspacePath, message string) {
	message = strings.TrimSpace(message)
	if api == nil || sessionID == "" || message == "" {
		return
	}
	if userID == "" {
		userID = "default"
	}
	logCtx := newServerLogContext("", "", "", userID, "", sessionID)

	conversationPath, ok, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath)
	if strings.TrimSpace(workspacePath) != "" && (err != nil || !ok || strings.TrimSpace(conversationPath) == "") {
		conversationPath, ok, err = findWorkflowBuilderConversationPathForSession(context.Background(), userID, sessionID, workspacePath)
	}
	if err != nil || !ok || strings.TrimSpace(conversationPath) == "" {
		return
	}

	lock := chatConversationMutex(conversationPath)
	lock.Lock()
	defer lock.Unlock()
	// Read only the header and recent messages, then append the one new
	// message: the whole history is never downloaded or rewritten (a 115 MB
	// chat used to be decoded and rewritten twice per message).
	content, exists, readErr := readConversationTailFromWorkspace(context.Background(), conversationPath, chatLiveInputTailRows)
	if readErr != nil || !exists {
		return
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(content), &record); err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot decode %s: %v", conversationPath, err)
		return
	}

	owner := strings.TrimSpace(stringFromRecord(record, "user_id"))
	if owner == "" {
		owner = "default"
	}
	persistedSession := strings.TrimSpace(stringFromRecord(record, "session_id"))
	if owner != userID || (persistedSession != "" && persistedSession != sessionID) {
		return
	}

	var history []llmtypes.MessageContent
	if existing, present := record["conversation_history"]; present {
		encoded, err := json.Marshal(existing)
		if err != nil {
			logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot re-encode history for %s: %v", conversationPath, err)
			return
		}
		if err := json.Unmarshal(encoded, &history); err != nil {
			logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot decode history for %s: %v", conversationPath, err)
			return
		}
	}
	total := len(history)
	if value, ok := record["history_total"].(float64); ok && int(value) > total {
		total = int(value)
	}
	userMessage := llmtypes.MessageContent{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: message}},
	}
	encodedMessage, err := json.Marshal(userMessage)
	if err != nil {
		return
	}
	updatedAt := time.Now().Format(time.RFC3339)
	advanceChatConversationRevision(record)
	if err := appendConversationHistoryInWorkspace(context.Background(), conversationPath, []json.RawMessage{encodedMessage}, map[string]interface{}{
		"updated_at": updatedAt,
		"revision":   record["revision"],
	}); err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot append to %s: %v", conversationPath, err)
		return
	}
	history = append(history, userMessage)
	total++

	// The resume snapshot and index are refreshed from the recent slice.
	record["conversation_history"] = history
	record["updated_at"] = updatedAt
	delete(record, "history_tail")
	delete(record, "history_total")
	if encoded, err := json.Marshal(record); err == nil {
		if err := writeChatHistoryResumeSnapshot(context.Background(), conversationPath, encoded); err != nil {
			logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot update resume snapshot for %s: %v", conversationPath, err)
		}
	}
	// Keep the history index in step with the transcript, so a session steered
	// only by live input still shows its latest message — and stays listed at all
	// for callers that read the index without a local directory to fall back on.
	if err := updatePersistedChatHistoryIndexWithCount(
		userID,
		sessionID,
		stringFromRecord(record, "agent_mode"),
		history,
		total,
		runtimeFromRecord(record),
		conversationPath,
		int64(len(content)),
		time.Now(),
		botMetadataFromRecord(record),
	); err != nil {
		logfWithContext(logCtx, "[CHAT_HISTORY] Live-input append: cannot update index for %s: %v", conversationPath, err)
	}
	logfWithContext(logCtx, "[CHAT_HISTORY] Recorded live-input message (%d messages) in %s", total, conversationPath)
}

// chatLiveInputTailRows is how much recent history a live-input append reads:
// enough for the owner check, the resume snapshot (last 10 turns) and the
// index preview.
const chatLiveInputTailRows = 400

func stringFromRecord(record map[string]interface{}, key string) string {
	value, _ := record[key].(string)
	return strings.TrimSpace(value)
}

func botMetadataFromRecord(record map[string]interface{}) *ChatHistoryBotMetadata {
	if record == nil {
		return nil
	}
	return normalizeChatHistoryBotMetadata(&ChatHistoryBotMetadata{
		Platform:  stringFromRecord(record, "bot_platform"),
		ChannelID: stringFromRecord(record, "bot_channel_id"),
		ThreadTS:  stringFromRecord(record, "bot_thread_ts"),
		UserID:    stringFromRecord(record, "bot_user_id"),
		UserName:  stringFromRecord(record, "bot_user_name"),
		UserEmail: stringFromRecord(record, "bot_user_email"),
	})
}

// runtimeFromRecord re-decodes the runtime block the caller deliberately left as
// opaque JSON. Returning nil on any problem is safe: updatePersistedChatHistoryIndex
// only reads WorkshopMode off it, and the transcript keeps the authoritative copy.
func runtimeFromRecord(record map[string]interface{}) *ChatHistoryAgentRuntime {
	value, ok := record["runtime"]
	if !ok || value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var runtime ChatHistoryAgentRuntime
	if err := json.Unmarshal(encoded, &runtime); err != nil {
		return nil
	}
	return &runtime
}

// readConversationFileDirect reads a chat conversation straight from disk in
// its legacy single-record form (assembled from header + history log when it
// is stored in that format); any other file is read as-is.
func readConversationFileDirect(path string) ([]byte, error) {
	if chatlog.IsConversationPath(path) {
		return chatlog.Load(path)
	}
	return os.ReadFile(path)
}
