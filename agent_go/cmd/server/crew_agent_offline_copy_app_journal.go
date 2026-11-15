package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The app's portable journal covers every app AI program without copying
// credentials, root instructions or provider configuration. Each line has the
// existing llmtypes.MessageContent JSON shape. Transport still sends only its
// appended tail, not this local snapshot or the whole native conversation.
func crewOfflineAppLines(history []llmtypes.MessageContent) ([]byte, error) {
	var out bytes.Buffer
	for _, message := range history {
		if message.Role == llmtypes.ChatMessageTypeSystem {
			continue
		}
		raw, err := json.Marshal(message)
		if err != nil {
			return nil, err
		}
		var value any
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		redactCrewOfflineValue(value)
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

func redactCrewOfflineValue(v any) {
	switch value := v.(type) {
	case map[string]any:
		for k, x := range value {
			if text, ok := x.(string); ok {
				value[k] = redaction.Text(text)
			} else {
				redactCrewOfflineValue(x)
			}
		}
	case []any:
		for i, x := range value {
			if text, ok := x.(string); ok {
				value[i] = redaction.Text(text)
			} else {
				redactCrewOfflineValue(x)
			}
		}
	}
}

// Called only for a record resolved from the source person's own registry,
// after the completed turn was saved. Never accepts a request-supplied path.
// Rewritten (compacted) history is appended after the acknowledged bytes, never replacing them.
func crewOfflineAppJournal(roots crewroot.Roots, record ProductConversationRecord, history []llmtypes.MessageContent) (string, error) {
	if !strings.HasPrefix(record.ConversationID, "conversation-") || !productConversationKeyPattern.MatchString(record.ConversationID) || record.SessionID == "" || record.ProfileID != crewProfileID || record.WorkspacePath != roots.Virtual || isSharedThreadKey(record.ConversationKey) {
		return "", fmt.Errorf("source app conversation unavailable")
	}
	dir := roots.Private
	for _, part := range []string{"offline-source-journals", record.ConversationID, crewTransferDigest(record.SessionID)[:16]} {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private source journal unavailable")
		}
	}
	path := filepath.Join(dir, "messages.jsonl")
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private source journal changed")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	raw, err := crewOfflineAppLines(history)
	if err != nil {
		return "", err
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	// The journal only grows. source.json remembers the last history it covered (size and digest, no content); a
	// compacted or reset history appends whole after the kept bytes, so the copy's acknowledged cursor stays valid.
	statePath := filepath.Join(dir, "source.json")
	seen := crewOfflineAppSource{Size: len(old), Digest: crewTransferDigest(string(old))}
	if b, e := os.ReadFile(statePath); e == nil {
		if json.Unmarshal(b, &seen) != nil || seen.Size < 0 {
			return "", fmt.Errorf("private source journal changed")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	tail := raw
	if seen.Size <= len(raw) && crewTransferDigest(string(raw[:seen.Size])) == seen.Digest {
		tail = raw[seen.Size:]
	} else {
		log.Printf("[AGENT_OFFLINE_COPY] key=%s event=source_rewritten kept_bytes=%d appended_bytes=%d", record.ConversationKey, len(old), len(raw))
	}
	if len(tail) == 0 {
		return path, nil
	}
	if err = crewOfflineAppWrite(dir, path, append(old, tail...)); err != nil {
		return "", err
	}
	state, err := json.Marshal(crewOfflineAppSource{Size: len(raw), Digest: crewTransferDigest(string(raw))})
	if err != nil {
		return "", err
	}
	if err = crewOfflineAppWrite(dir, statePath, state); err != nil {
		return "", err
	}
	return path, nil
}

type crewOfflineAppSource struct {
	Size   int    `json:"size"`
	Digest string `json:"digest"`
}

func crewOfflineAppWrite(dir, path string, data []byte) error {
	temp, err := os.CreateTemp(dir, ".journal-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	if e := temp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
