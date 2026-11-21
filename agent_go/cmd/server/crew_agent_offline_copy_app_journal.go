package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
		line, err := crewOfflineRedactedLine(raw)
		if err != nil {
			return nil, err
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

// Every completed turn passes the whole saved history here, and redacting
// every string of every earlier message again was a sixth of a busy server's
// CPU (#3156 profile, 20 Nov). Redaction depends only on the message bytes,
// so each message's redacted line is remembered by its digest.
var crewOfflineRedacted = struct {
	sync.Mutex
	lines map[[sha256.Size]byte][]byte
	bytes int
}{lines: map[[sha256.Size]byte][]byte{}}

const crewOfflineRedactedBudget = 64 << 20

func crewOfflineRedactedLine(raw []byte) ([]byte, error) {
	key := sha256.Sum256(raw)
	crewOfflineRedacted.Lock()
	line, ok := crewOfflineRedacted.lines[key]
	crewOfflineRedacted.Unlock()
	if ok {
		return line, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	redactCrewOfflineValue(value)
	line, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	crewOfflineRedacted.Lock()
	if crewOfflineRedacted.bytes+len(line) > crewOfflineRedactedBudget {
		crewOfflineRedacted.lines = map[[sha256.Size]byte][]byte{}
		crewOfflineRedacted.bytes = 0
	}
	if _, dup := crewOfflineRedacted.lines[key]; !dup {
		crewOfflineRedacted.lines[key] = line
		crewOfflineRedacted.bytes += len(line)
	}
	crewOfflineRedacted.Unlock()
	return line, nil
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
	if !strings.HasPrefix(record.ConversationID, "conversation-") || !productConversationKeyPattern.MatchString(record.ConversationID) || record.SessionID == "" || record.ProfileID != crewProfileID || record.WorkspacePath != roots.Virtual {
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
	var journalBytes int64
	if st, err := os.Lstat(path); err == nil {
		journalBytes = st.Size()
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
	// The journal only grows. source.json remembers the last history it covered (size, digest and short per-line
	// hashes, no content). An extended history appends its new bytes. A rewritten one (a trimmed window, a compaction
	// or an edited earlier message) appends from its first line the journal has not covered, never the whole history
	// again: re-appending everything each turn grew one Mac Main chat's journal past 1.6 GB in a day.
	statePath := filepath.Join(dir, "source.json")
	seen := crewOfflineAppSource{Size: int(journalBytes)}
	if seen.Size < 0 || int64(seen.Size) != journalBytes {
		return "", fmt.Errorf("private source journal too large")
	}
	// With no coverage metadata, use the file length, never read accumulated history.
	if journalBytes <= int64(len(raw)) {
		seen.Size = int(journalBytes)
		seen.Digest = crewOfflineAppDigest(raw[:seen.Size])
	}
	if b, e := os.ReadFile(statePath); e == nil {
		if json.Unmarshal(b, &seen) != nil || seen.Size < 0 {
			return "", fmt.Errorf("private source journal changed")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	lines := crewOfflineAppLineHashes(raw)
	tail := raw
	if seen.Size <= len(raw) && crewOfflineAppDigest(raw[:seen.Size]) == seen.Digest {
		tail = raw[seen.Size:]
	} else {
		tail = raw[crewOfflineAppKnownPrefix(raw, lines, seen.Lines):]
		log.Printf("[AGENT_OFFLINE_COPY] key=%s event=source_rewritten kept_bytes=%d history_bytes=%d appended_bytes=%d", record.ConversationKey, journalBytes, len(raw), len(tail))
	}
	if len(tail) == 0 {
		return path, nil
	}
	f, err := openCrewOfflineAppAppend(path)
	if err != nil {
		return "", err
	}
	st, err := f.Stat()
	if err == nil && !st.Mode().IsRegular() {
		err = fmt.Errorf("private source journal changed")
	}
	if err == nil {
		_, err = f.Write(tail)
	}
	if err == nil {
		st, err = f.Stat()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	log.Printf("[AGENT_OFFLINE_COPY] key=%s event=append bytes=%d journal_bytes=%d", record.ConversationKey, len(tail), st.Size())
	state, err := json.Marshal(crewOfflineAppSource{Size: len(raw), Digest: crewOfflineAppDigest(raw), Lines: lines})
	if err != nil {
		return "", err
	}
	if err = crewOfflineAppWrite(dir, statePath, state); err != nil {
		return "", err
	}
	return path, nil
}

type crewOfflineAppSource struct {
	Size   int      `json:"size"`
	Digest string   `json:"digest"`
	Lines  []string `json:"lines,omitempty"`
}

// crewOfflineAppLineHashes returns a short hash per journal line (each line ends in '\n').
func crewOfflineAppLineHashes(raw []byte) []string {
	var out []string
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, '\n') + 1
		if end == 0 {
			end = len(raw)
		}
		out = append(out, crewOfflineAppDigest(raw[:end])[:16])
		raw = raw[end:]
	}
	return out
}

// crewOfflineAppKnownPrefix is the byte length of the leading lines of raw that the journal already covered. A
// trimmed window starts with covered lines; a compaction summary or edited message ends the run.
func crewOfflineAppKnownPrefix(raw []byte, lines, covered []string) int {
	known := make(map[string]struct{}, len(covered))
	for _, h := range covered {
		known[h] = struct{}{}
	}
	n := 0
	for _, h := range lines {
		if _, ok := known[h]; !ok {
			break
		}
		n += bytes.IndexByte(raw[n:], '\n') + 1
	}
	return n
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

func crewOfflineAppDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
