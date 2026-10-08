package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// Oversized chat rows keep a bounded summary in SQLite and the complete event
// in a private artifact file beside the journal. Artifact and session
// directory names are hashes, so no caller-controlled text reaches a path.
const (
	chatArtifactDirName      = "chat-artifacts"
	liveArtifactSummaryBytes = 16 * 1024
)

var ErrChatArtifactNotFound = errors.New("chat artifact not found")

var chatArtifactIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func chatArtifactHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

// ChatArtifactID is the stable artifact id for one event.
func ChatArtifactID(eventID string) string { return chatArtifactHash("event:" + eventID) }

func (j *SQLiteEventJournal) artifactRoot() string {
	return filepath.Join(filepath.Dir(j.path), chatArtifactDirName)
}

func (j *SQLiteEventJournal) sessionArtifactDir(sessionID string) string {
	return filepath.Join(j.artifactRoot(), chatArtifactHash("session:"+sessionID))
}

func (j *SQLiteEventJournal) writeChatArtifact(sessionID, eventID string, payload []byte) (string, error) {
	artifactID := ChatArtifactID(eventID)
	dir := j.sessionArtifactDir(sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".artifact-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, artifactID+".json")); err != nil {
		return "", err
	}
	return artifactID, nil
}

// ReadChatArtifact returns the complete event JSON behind a summarized row.
func (j *SQLiteEventJournal) ReadChatArtifact(sessionID, artifactID string) ([]byte, error) {
	if j == nil || sessionID == "" || !chatArtifactIDPattern.MatchString(artifactID) {
		return nil, ErrChatArtifactNotFound
	}
	payload, err := os.ReadFile(filepath.Join(j.sessionArtifactDir(sessionID), artifactID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrChatArtifactNotFound
	}
	return payload, err
}

func (j *SQLiteEventJournal) removeSessionArtifacts(sessionID string) error {
	return os.RemoveAll(j.sessionArtifactDir(sessionID))
}

// spillOversizedEvent moves an event whose encoded row exceeds the journal
// bound into an artifact and returns the summary row that replaces it.
func (j *SQLiteEventJournal) spillOversizedEvent(sessionID string, event Event) (Event, error) {
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) <= maxDurableChatEventBytes || event.ID == "" {
		return event, err
	}
	artifactID, err := j.writeChatArtifact(sessionID, event.ID, encoded)
	if err != nil {
		return Event{}, fmt.Errorf("write chat artifact: %w", err)
	}
	return summarizeChatArtifactEvent(event, artifactID, len(encoded), liveArtifactSummaryBytes), nil
}

// summarizeChatArtifactEvent keeps what the transcript needs to render a row
// (type, identity, tool pairing, the first textLimit bytes of its text) and
// marks it truncated with the artifact that holds the rest.
func summarizeChatArtifactEvent(event Event, artifactID string, originalSize, textLimit int) Event {
	payload := eventPayloadMap(&event)
	fields := make(map[string]interface{}, 16)
	for _, key := range []string{"content", "final_result", "result", "error", "message", "question", "tool_name", "tool_call_id", "name", "status", "role", "source", "is_delta"} {
		value, exists := payload[key]
		if !exists {
			continue
		}
		if text, ok := value.(string); ok {
			fields[key] = truncateArtifactSummary(text, textLimit)
		} else if value == nil || isDurableScalar(value) {
			fields[key] = value
		}
	}
	if metadata, ok := payload["metadata"].(map[string]interface{}); ok {
		bounded := make(map[string]interface{})
		for _, key := range []string{"kind", "message_id", "client_message_id", "display_content", "turn_id", "provider", "confirmation", "delivery_status", "source", "sender_label"} {
			if value, exists := metadata[key]; exists {
				if text, isText := value.(string); isText {
					bounded[key] = truncateArtifactSummary(text, 2*1024)
				} else if isDurableScalar(value) {
					bounded[key] = value
				}
			}
		}
		if len(bounded) > 0 {
			fields["metadata"] = bounded
		}
	}
	fields["truncated"] = true
	fields["artifact_id"] = artifactID
	fields["original_size_bytes"] = originalSize
	event.Data = &pkgevents.AgentEvent{
		Type:      pkgevents.EventType(event.Type),
		Timestamp: event.Timestamp,
		SessionID: event.SessionID,
		Data:      NewGenericEventData(event.Type, fields),
	}
	return event
}

// IsChatArtifactSummary reports whether a row was already replaced by an
// artifact summary, so compaction never summarizes a summary.
func IsChatArtifactSummary(event Event) bool {
	id, _ := eventPayloadMap(&event)["artifact_id"].(string)
	return id != ""
}

func truncateArtifactSummary(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
