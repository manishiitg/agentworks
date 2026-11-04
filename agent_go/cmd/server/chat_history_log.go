package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Crew chats and the AgentWorks home chat are stored as a chat folder
// (PLAT-856-521): <chat root>/session-<id>/header.json + turns.jsonl. The
// conversation path every caller carries is that folder's header.json; a read
// of it returns the assembled conversation record, and saves append turns
// through the workspace server (the one writer, see workspace/chatlog).

// chatHistoryChatFolder is the folder name for a session: session-<id>.
func chatHistoryChatFolder(sessionID string) string {
	return strings.TrimSuffix(chatHistoryConversationFileName(sessionID), "-conversation.json")
}

// chatHistoryHeaderPath is <root>/session-<id>/header.json.
func chatHistoryHeaderPath(root, sessionID string) string {
	return pathpkg.Join(strings.Trim(strings.TrimSpace(root), "/"), chatHistoryChatFolder(sessionID), chatlog.HeaderFile)
}

// crewChatHeaderPath is a Crew chat's header under the Crew's builder folder.
func crewChatHeaderPath(workspacePath, sessionID string) string {
	return chatHistoryHeaderPath(pathpkg.Join(strings.Trim(strings.TrimSpace(workspacePath), "/"), "builder", "conversation"), sessionID)
}

// chatHistorySessionIDFromHeaderPath returns the session of a chat header path.
func chatHistorySessionIDFromHeaderPath(path string) string {
	path = strings.TrimSuffix(strings.Trim(filepathToSlash(path), "/"), "/"+chatlog.HeaderFile)
	return chatHistorySessionIDFromFileName(pathpkg.Base(path) + "-conversation.json")
}

func filepathToSlash(path string) string { return strings.ReplaceAll(path, "\\", "/") }

func isChatHeaderPath(path string) bool { return chatlog.IsChatHeaderPath(filepathToSlash(path)) }

func workspaceDocumentURL(filePath, suffix string) string {
	segments := strings.Split(filePath, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return getWorkspaceAPIURL() + "/api/documents/" + strings.Join(segments, "/") + suffix
}

type chatTurnAppend struct {
	Turns       []chatlog.Turn             `json:"turns,omitempty"`
	Patch       map[string]json.RawMessage `json:"patch,omitempty"`
	Mode        string                     `json:"mode,omitempty"`
	OnlyMissing bool                       `json:"only_missing,omitempty"`
}

// appendChatTurnInWorkspace appends turns and patches the header of a chat.
func appendChatTurnInWorkspace(ctx context.Context, headerPath string, request chatTurnAppend) (chatlog.SaveStats, error) {
	var stats chatlog.SaveStats
	noteWorkspaceMutation(headerPath, false)
	body, err := json.Marshal(request)
	if err != nil {
		return stats, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, workspaceDocumentURL(headerPath, "/append-turn"), strings.NewReader(string(body)))
	if err != nil {
		return stats, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return stats, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return stats, fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(raw))
	}
	var envelope struct {
		Data chatlog.SaveStats `json:"data"`
	}
	_ = json.Unmarshal(raw, &envelope)
	return envelope.Data, nil
}

// patchChatHeaderInWorkspace changes header fields only (title, metadata).
func patchChatHeaderInWorkspace(ctx context.Context, headerPath string, patch map[string]interface{}) error {
	encoded := make(map[string]json.RawMessage, len(patch))
	for key, value := range patch {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encoded[key] = raw
	}
	_, err := appendChatTurnInWorkspace(ctx, headerPath, chatTurnAppend{Patch: encoded})
	return err
}

// readChatPageFromWorkspace returns the conversation record holding only the
// newest turns of a chat (the resume view and paged MCP reads).
func readChatPageFromWorkspace(ctx context.Context, headerPath string, turns int) (string, bool, error) {
	return readFileFromWorkspaceQuery(ctx, headerPath, fmt.Sprintf("turns=%d", turns))
}

// storedChatTail is what a save needs from the stored chat: its owner, title
// and the newest turns (to tell which in-memory messages are new).
type storedChatTail struct {
	UserID string               `json:"user_id"`
	Title  string               `json:"title"`
	Turns  []storedChatTailTurn `json:"history_page_lines"`
}

type storedChatTailTurn struct {
	ID       string                 `json:"id"`
	Messages []json.RawMessage      `json:"messages"`
	UIEvents []internalevents.Event `json:"ui_events"`
}

func readStoredChatTail(ctx context.Context, headerPath string, turns int) (*storedChatTail, bool, error) {
	raw, found, err := readFileFromWorkspaceQuery(ctx, headerPath, fmt.Sprintf("turns=%d&lines=1", turns))
	if err != nil || !found || strings.TrimSpace(raw) == "" {
		return nil, false, err
	}
	var tail storedChatTail
	if err := json.Unmarshal([]byte(raw), &tail); err != nil {
		return nil, false, err
	}
	return &tail, true, nil
}

func canonicalChatMessage(message llmtypes.MessageContent) string {
	encoded, _ := json.Marshal(message)
	return string(encoded)
}

func canonicalStoredChatMessage(raw json.RawMessage) string {
	var message llmtypes.MessageContent
	if json.Unmarshal(raw, &message) != nil {
		return string(raw)
	}
	return canonicalChatMessage(message)
}

// newChatTurnMessages picks the messages of the turn being saved out of the
// in-memory history: everything after the newest stored message that memory
// still holds. When the turn id is already stored (an amend), the stored copy
// of that turn is replaced, so the anchor is the turn before it. When memory
// shares nothing with the stored tail (a thin rebuild after a restart), the
// turn starts at the newest person message.
func newChatTurnMessages(history []llmtypes.MessageContent, stored []storedChatTailTurn, turnID string) []llmtypes.MessageContent {
	body := history
	if len(body) > 0 && body[0].Role == llmtypes.ChatMessageTypeSystem {
		body = body[1:]
	}
	var anchors []string
	for _, turn := range stored {
		if turn.ID == turnID {
			break
		}
		for _, raw := range turn.Messages {
			anchors = append(anchors, canonicalStoredChatMessage(raw))
		}
	}
	for a := len(anchors) - 1; a >= 0; a-- {
		for i := len(body) - 1; i >= 0; i-- {
			if canonicalChatMessage(body[i]) == anchors[a] {
				return body[i+1:]
			}
		}
	}
	if len(anchors) == 0 && len(stored) == 0 {
		return body // a new chat: everything is this turn
	}
	for i := len(body) - 1; i >= 0; i-- {
		if body[i].Role == llmtypes.ChatMessageTypeHuman {
			return body[i:]
		}
	}
	return nil
}

// newChatTurnEvents keeps the live UI events that no other stored turn has.
func newChatTurnEvents(live []internalevents.Event, stored []storedChatTailTurn, turnID string) []json.RawMessage {
	seen := map[string]bool{}
	var newest time.Time
	for _, turn := range stored {
		if turn.ID == turnID {
			continue
		}
		for _, event := range turn.UIEvents {
			if event.ID != "" {
				seen[event.ID] = true
			}
			if event.Timestamp.After(newest) {
				newest = event.Timestamp
			}
		}
	}
	out := make([]json.RawMessage, 0, len(live))
	for _, event := range live {
		if (event.ID != "" && seen[event.ID]) || (!newest.IsZero() && event.Timestamp.Before(newest)) {
			continue
		}
		if raw, err := json.Marshal(event); err == nil {
			out = append(out, raw)
		}
	}
	return out
}

func chatMessagesToRaw(messages []llmtypes.MessageContent) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		if raw, err := json.Marshal(message); err == nil {
			out = append(out, raw)
		}
	}
	return out
}

func chatHeaderPatch(fields map[string]interface{}) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		if raw, err := json.Marshal(value); err == nil {
			out[key] = raw
		}
	}
	return out
}

// settleTurnThenSave records the turn's final status (done, failed, canceled)
// before its history is saved (PLAT-856-521): the person sees the answer as
// done at once, and the save, an append, follows. The next turn of the chat
// still waits for the save (it holds the chat's writer lock, and the turn's
// input lane is released only after it).
func settleTurnThenSave(settle func() (string, bool), save func()) (string, bool) {
	status, completed := settle()
	save()
	return status, completed
}

// chatEventWindow reads the UI events of the turns that hold events newer
// than seq after (chatlog.EventsAfter: a poll with nothing new reads no lines;
// each turn's current line comes whole, so a turn's earlier events come with
// it). With sender set, lines another sender started are skipped (the turn
// line's sender_user_id); readers still apply their own per-turn audience
// check. ok is false when the chat is not a local chat folder, so the caller
// reads the whole conversation instead.
func chatEventWindow(userID, sessionID, workspacePath string, after int, sender string) ([]json.RawMessage, bool, error) {
	conversationPath, found, err := FindChatHistoryConversationPathForSession(userID, sessionID, workspacePath)
	if err != nil || !found || !isChatHeaderPath(conversationPath) {
		return nil, false, err
	}
	dir, ok := localChatDir(conversationPath)
	if !ok {
		return nil, false, nil
	}
	started := time.Now()
	var keep func(chatlog.Turn) bool
	if sender != "" {
		keep = func(turn chatlog.Turn) bool { return turn.SenderUserID == "" || turn.SenderUserID == sender }
	}
	events, scanned, err := chatlog.EventsAfter(dir, after, keep)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if ms := time.Since(started).Milliseconds(); ms > 50 {
		log.Printf("[CHAT_HISTORY] read window session=%s after=%d lines_scanned=%d events=%d ms=%d", sessionID, after, scanned, len(events), ms)
	}
	return events, true, nil
}

// localChatDir is the chat folder on this machine for a chat header path.
func localChatDir(conversationPath string) (string, bool) {
	physical, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), conversationPath)
	if err != nil {
		return "", false
	}
	if !bound {
		physical = filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(chatHistoryStoragePath(conversationPath)))
	}
	if info, err := os.Stat(filepath.Join(filepath.Dir(physical), chatlog.HeaderFile)); err != nil || info.IsDir() {
		return "", false
	}
	return filepath.Dir(physical), true
}
