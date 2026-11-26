package cursorcli

import (
	"crypto/md5" //nolint:gosec // md5 here mirrors cursor CLI's own directory naming, not crypto
	"encoding/hex"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	cursorReturnedBlobsMu sync.Mutex
	cursorReturnedBlobs   = map[string]map[string]struct{}{}
)

func readCursorTranscriptMessagesAndStoreDB(turnStart time.Time, workingDir string, baseline map[string]struct{}, knownNativeSessionID string, accountHome ...string) ([]llmtypes.MessageContent, string) {
	if strings.TrimSpace(workingDir) == "" {
		return nil, ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ""
	}
	if len(accountHome) > 0 && accountHome[0] != "" {
		home = accountHome[0]
	}
	var pickedPath string
	// A persisted native ID is authoritative. Never fall back to a different
	// chat merely because this transcript is missing or has not flushed yet.
	if id := strings.TrimSpace(knownNativeSessionID); id != "" {
		pickedPath = cursorStoreDBForNativeSession(home, workingDir, id, cursorTmuxConfigArg(accountHome)...)
	}
	if pickedPath == "" {
		return nil, ""
	}

	// Cursor commits its sqlite root blob asynchronously, sometimes
	// many seconds AFTER the tmux pane settles (observed 19s on a
	// trivial "Reply OK" turn — cursor does background bookkeeping).
	// Poll for up to ~4s, returning as soon as we see an assistant
	// or tool message. For trivial fast turns the commit may never
	// arrive in time and we return empty; for real tool-using turns
	// commits happen mid-turn so the data is usually there by
	// return. This is best-effort by design.
	deadline := time.Now().Add(4 * time.Second)
	for {
		msgs := readCursorStoreDBMessagesAfter(pickedPath, baseline)
		if hasUsableCursorTurn(msgs) || time.Now().After(deadline) {
			return msgs, pickedPath
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func latestCursorAssistantText(msgs []llmtypes.MessageContent) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		var b strings.Builder
		for _, part := range msgs[i].Parts {
			if txt, ok := part.(llmtypes.TextContent); ok {
				b.WriteString(txt.Text)
			}
		}
		// An assistant turn can be tool-calls only, with no prose; keep walking
		// back rather than returning "" and losing the reply that precedes it.
		if strings.TrimSpace(b.String()) != "" {
			return b.String()
		}
	}
	return ""
}

func cursorNativeSessionIDFromStoreDBPath(storeDBPath string) string {
	if strings.TrimSpace(storeDBPath) == "" {
		return ""
	}
	if filepath.Base(storeDBPath) != "store.db" {
		return ""
	}
	return filepath.Base(filepath.Dir(storeDBPath))
}

func hasUsableCursorTurn(msgs []llmtypes.MessageContent) bool {
	for _, m := range msgs {
		if m.Role == llmtypes.ChatMessageTypeAI || m.Role == llmtypes.ChatMessageTypeTool {
			return true
		}
	}
	return false
}

func readCursorStoreDBMessages(dbPath string, ownerSessionID string) []llmtypes.MessageContent {
	// Display-only cursor for live streaming: remembers what this stream has
	// already shown. It never decides what a turn records (see
	// readCursorStoreDBMessagesAfter).
	var seen map[string]struct{}
	if ownerSessionID != "" {
		cursorReturnedBlobsMu.Lock()
		current := cursorReturnedBlobs[ownerSessionID]
		seen = make(map[string]struct{}, len(current))
		for ref := range current {
			seen[ref] = struct{}{}
		}
		cursorReturnedBlobsMu.Unlock()
	}
	msgs, refs := cursorStoreMessagesExcept(dbPath, seen)
	if ownerSessionID != "" && refs != nil {
		cursorReturnedBlobsMu.Lock()
		bucket := cursorReturnedBlobs[ownerSessionID]
		if bucket == nil {
			bucket = make(map[string]struct{}, len(refs))
			cursorReturnedBlobs[ownerSessionID] = bucket
		}
		for _, r := range refs {
			bucket[r] = struct{}{}
		}
		cursorReturnedBlobsMu.Unlock()
	}
	return msgs
}

func cursorStoreDBForNativeSession(home, workingDir, id string, configDir ...string) string {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return ""
	}
	paths := append([]string{pathidentity.Key(workingDir)}, pathidentity.Candidates(workingDir)...)
	seen := map[string]bool{}
	for _, path := range paths {
		sum := md5.Sum([]byte(filepath.Clean(path))) //nolint:gosec
		hash := hex.EncodeToString(sum[:])
		if seen[hash] {
			continue
		}
		seen[hash] = true
		for _, root := range cursorTmuxChatsRoots(home, configDir...) {
			candidate := filepath.Join(root, hash, id, "store.db")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}
