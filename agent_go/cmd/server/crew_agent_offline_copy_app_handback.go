package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type crewOfflineAppReturns struct {
	Binding crewOfflineBinding          `json:"binding"`
	Records []crewOfflineReturnedAnswer `json:"records"`
}

func crewOfflineAppReturnsPath(user string, record ProductConversationRecord) (string, error) {
	if record.ProfileID != crewProfileID || !strings.HasPrefix(record.ConversationID, "conversation-") || !productConversationKeyPattern.MatchString(record.ConversationID) || record.SessionID == "" {
		return "", fmt.Errorf("source record unavailable")
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || roots.Owner != user {
		return "", fmt.Errorf("source owner changed")
	}
	return crewOfflineAppReturnsPathIn(roots.Private, record)
}

func crewOfflineAppReturnsPathIn(dir string, record ProductConversationRecord) (string, error) {
	for _, part := range []string{"offline-source-journals", record.ConversationID, crewTransferDigest(record.SessionID)[:16]} {
		dir = filepath.Join(dir, part)
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("source private journal unavailable")
		}
	}
	path := filepath.Join(dir, "handback.json")
	if st, err := os.Lstat(path); err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
		return "", fmt.Errorf("source return file changed")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return path, nil
}

func crewOfflineAppReturnsText(records []crewOfflineReturnedAnswer) string {
	if len(records) == 0 {
		return ""
	}
	raw, _ := json.Marshal(records)
	sum := sha256.Sum256(raw)
	// A later saved HUMAN record, not hook output or an assistant echo, is the receipt.
	return crewOfflineReturnedText(records) + "\n> AgentWorks offline receipt: " + hex.EncodeToString(sum[:]) + "\n"
}

func readCrewOfflineAppReturns(path string, b crewOfflineBinding) (crewOfflineAppReturns, error) {
	var state crewOfflineAppReturns
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return crewOfflineAppReturns{Binding: b}, nil
	}
	if err != nil {
		return state, err
	}
	if err = json.Unmarshal(raw, &state); err != nil || state.Binding != b {
		return state, fmt.Errorf("source return binding changed")
	}
	return state, nil
}

// Only the source owner's own normal turn consumes the local private queue.
// This changes USER content, never system/project instructions or native files.
func crewOfflineAppHandbackInput(ctx context.Context, user string, record ProductConversationRecord, message string) string {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != user || claims.AccessToken != nil || claims.Scope != "" {
		return message
	}
	var path string
	var err error
	if IsLocalMode() {
		path, err = crewOfflineAppReturnsPath(user, record)
	} else {
		path, err = crewOfflineCloudHandback(ctx, user, record)
	}
	if err != nil {
		return message
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	state, err := readCrewOfflineAppReturns(path, b)
	if err != nil || len(state.Records) == 0 {
		return message
	}
	var saved struct {
		UserID  string                    `json:"user_id"`
		Session string                    `json:"session_id"`
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if raw, e := ReadChatHistoryConversation(user, record.SessionID, record.WorkspacePath); e == nil && json.Unmarshal(raw, &saved) == nil && saved.UserID == user && saved.Session == record.SessionID && crewOfflineAppReturnsAccepted(saved.History, crewOfflineAppReturnsText(state.Records)) {
		return message
	}
	log.Printf("[AGENT_OFFLINE_COPY] session=%s event=handback_injected count=%d", record.SessionID, len(state.Records))
	return crewOfflineAppReturnsText(state.Records) + "\n" + message
}

func syncCrewOfflineAppReturns(ctx context.Context, user string, c desktopConnection, route string, record ProductConversationRecord, history []llmtypes.MessageContent) error {
	path, err := crewOfflineAppReturnsPath(user, record)
	if err != nil {
		return err
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	state, err := readCrewOfflineAppReturns(path, b)
	if err != nil {
		return err
	}
	text := crewOfflineAppReturnsText(state.Records)
	accepted := crewOfflineAppReturnsAccepted(history, text)
	if accepted {
		ids := make([]string, 0, len(state.Records))
		for _, r := range state.Records {
			ids = append(ids, r.ID)
		}
		var ack struct {
			Acknowledged bool `json:"acknowledged"`
		}
		if err := crewTeamRemoteJSON(ctx, user, c, http.MethodPost, route+"handback-ack", map[string]any{"provider": b.Provider, "session": b.Session, "attachment": b.Attachment, "ids": ids}, &ack); err != nil {
			return err
		}
		if !ack.Acknowledged {
			return fmt.Errorf("source return acknowledgement unavailable")
		}
	}
	var pending struct {
		Records []crewOfflineReturnedAnswer `json:"records"`
	}
	if err := crewTeamRemoteJSON(ctx, user, c, http.MethodPost, route+"handback", b, &pending); err != nil {
		return err
	}
	if len(pending.Records) > 200 {
		return fmt.Errorf("source return batch too large")
	}
	state = crewOfflineAppReturns{Binding: b, Records: pending.Records}
	if err := fsutil.WriteJSONAtomic(path, state, 0600); err != nil {
		return err
	}
	log.Printf("[AGENT_OFFLINE_COPY] session=%s event=handback_cached count=%d acknowledged=%t", record.SessionID, len(state.Records), accepted)
	return nil
}

func crewOfflineAppReturnsAccepted(history []llmtypes.MessageContent, text string) bool {
	if text == "" {
		return false
	}
	for _, m := range history {
		if m.Role != llmtypes.ChatMessageTypeHuman {
			continue
		}
		for _, part := range m.Parts {
			if p, ok := part.(llmtypes.TextContent); ok && strings.Contains(p.Text, text) {
				return true
			}
		}
	}
	return false
}
