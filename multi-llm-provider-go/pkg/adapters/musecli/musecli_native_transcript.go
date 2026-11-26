package musecli

import (
	"os"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// NativeTranscript is muse's on-disk session transcript read back after the
// fact for chat-history resync. Mirrors cursorcli.NativeTranscript and
// picli.NativeTranscript so mcp-agent-builder-go's
// nativeTranscriptMessagesForRuntime can treat every tmux-backed provider
// the same way -- muse had no equivalent exported reader at all, so any
// assistant message not captured by the live streaming path (e.g. one that
// arrived after the stream closed, or during a backend restart) was never
// backfilled the way it would be for claude-code/codex-cli/cursor-cli/pi-cli.
type NativeTranscript struct {
	// Path is the session.jsonl the messages came from.
	Path string
	// Messages holds Human/AI text-only messages in chronological order.
	Messages []llmtypes.MessageContent
	// UpdatedAt is the session.jsonl's modification time.
	UpdatedAt time.Time
}

// ReadNativeTranscript reads muse's own session.jsonl for nativeSessionID
// back into a NativeTranscript. ok is false when no session log exists for
// this id (muse never ran, or it's on a different XDG data home) -- resync
// callers then leave the persisted chat-history record as-is, the same
// contract as picli.ReadNativeTranscript.
func ReadNativeTranscript(nativeSessionID string, accountDataHome ...string) (NativeTranscript, bool, error) {
	path := museSessionLogPath(nativeSessionID, accountDataHome...)
	if path == "" {
		return NativeTranscript{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return NativeTranscript{}, false, err
	}
	messages, ok := readMuseTranscriptMessages(path, "")
	if !ok {
		return NativeTranscript{}, false, nil
	}
	return NativeTranscript{Path: path, Messages: messages, UpdatedAt: info.ModTime()}, true, nil
}
