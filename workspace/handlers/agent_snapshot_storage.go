package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const agentMailboxFormat = "history-v1"

type agentMailboxPart struct {
	info    os.FileInfo
	content []byte
}

func agentMailboxHistoryPath(path string) string {
	return filepath.Join(filepath.Dir(path), "conversations.history.json")
}

func readAgentMailboxPart(path string, prior map[string]agentMailboxPart) (agentMailboxPart, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return agentMailboxPart{}, err
	}
	if !info.Mode().IsRegular() {
		return agentMailboxPart{}, fmt.Errorf("mailbox part is not a regular file")
	}
	if part, ok := prior[path]; ok && sameAgentSnapshotFile(info, part.info) {
		return part, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return agentMailboxPart{}, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return agentMailboxPart{}, err
	}
	if !sameAgentSnapshotFile(info, after) {
		// Do not associate bytes with metadata that changed during their read.
		// An atime-only change is harmless but also makes this part uncached.
		return agentMailboxPart{content: content}, nil
	}
	return agentMailboxPart{info: after, content: content}, nil
}

// Called with the byte-cache mutex held. Every request stats both files; a
// changed part is reread alone. The API and strong tag describe the assembled
// logical store, not the physical metadata/history split.
func loadAgentMailbox(path string, info os.FileInfo) ([]byte, string, error) {
	cache := &agentSnapshotFileCache
	if cache.path == path && sameAgentSnapshotFile(info, cache.info) {
		unchanged := true
		for partPath, prior := range cache.parts {
			current, err := os.Lstat(partPath)
			if err != nil {
				return nil, "", err
			}
			if !sameAgentSnapshotFile(current, prior.info) {
				unchanged = false
				break
			}
		}
		if unchanged {
			return cache.content, cache.tag, nil
		}
	}
	prior := cache.parts
	if cache.path != path {
		prior = nil
	}
	header, err := readAgentMailboxPart(path, prior)
	if err != nil {
		return nil, "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(header.content, &fields); err != nil {
		return nil, "", err
	}
	var format string
	_ = json.Unmarshal(fields["mailbox_storage"], &format)
	if format != agentMailboxFormat {
		return nil, "", fmt.Errorf("mailbox storage requires the stopped-writer split-agent-mailbox migration")
	}
	historyPath := agentMailboxHistoryPath(path)
	history, err := readAgentMailboxPart(historyPath, prior)
	if err != nil {
		return nil, "", err
	}
	var conversations []struct {
		Messages []struct {
			Sequence int `json:"agent_sequence"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(history.content, &conversations); err != nil {
		return nil, "", err
	}
	// Durable messages are the lower bound for the mailbox cursor. History
	// can commit before metadata fails; never reuse an already-durable position.
	var next int
	if raw := fields["next_agent_sequence"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, "", err
		}
	}
	for _, conversation := range conversations {
		for _, message := range conversation.Messages {
			next = max(next, message.Sequence)
		}
	}
	if next > 0 {
		fields["next_agent_sequence"], _ = json.Marshal(next)
	}
	delete(fields, "mailbox_storage")
	fields["conversations"] = history.content
	content, err := json.Marshal(fields)
	if err != nil {
		return nil, "", err
	}
	tag := fmt.Sprintf("\"%x\"", sha256.Sum256(content))
	cache.path, cache.info, cache.content, cache.tag = path, header.info, content, tag
	cache.parts = map[string]agentMailboxPart{path: header, historyPath: history}
	return content, tag, nil
}

// The document mutation lock serializes writers; the cache mutex keeps readers
// from observing a partially replaced pair. Presence-only writes touch metadata
// alone, while a history change rewrites only history (plus changed metadata).
func saveAgentSnapshotFile(path string, raw []byte) error {
	return saveAgentSnapshotParts(path, raw, writeFileAtomic)
}

func saveAgentSnapshotParts(path string, raw []byte, writePart func(string, []byte, os.FileMode) error) error {
	agentSnapshotFileCache.Lock()
	defer agentSnapshotFileCache.Unlock()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var conversations []json.RawMessage
	if err := json.Unmarshal(fields["conversations"], &conversations); err != nil {
		return err
	}
	history := fields["conversations"]
	if len(history) == 0 {
		history = json.RawMessage("null")
	}
	delete(fields, "conversations")
	fields["mailbox_storage"], _ = json.Marshal(agentMailboxFormat)
	metadata, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	cache := &agentSnapshotFileCache
	if info, err := os.Stat(path); err == nil {
		if _, _, err := loadAgentMailbox(path, info); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	prior := cache.parts
	if cache.path != path {
		prior = nil
	}
	historyPath := agentMailboxHistoryPath(path)
	for _, part := range []struct {
		path    string
		content []byte
	}{{historyPath, history}, {path, metadata}} {
		if old, ok := prior[part.path]; ok && bytes.Equal(old.content, part.content) {
			continue
		}
		if info, err := os.Lstat(part.path); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("mailbox part is not a regular file")
		}
		if err := writePart(part.path, part.content, 0600); err != nil {
			cache.info = nil
			return err
		}
	}
	cache.info = nil // The next reader validates both new files.
	return nil
}
