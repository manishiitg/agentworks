package cursorcli

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/toolclock"
)

// cursorTranscriptStreamPollInterval is how often the tailer checks Cursor's
// store.db for new messages. Cursor commits its sqlite root asynchronously
// (sometimes seconds after the pane settles), so this is best-effort and laggier
// than the JSONL adapters.
const cursorTranscriptStreamPollInterval = 400 * time.Millisecond

// cursorInteractiveStreamTranscriptEnabled reports whether to poll Cursor's
// store.db for structured streaming. Opt-in (default OFF), set per call via
// WithStreamTranscript — there is no environment-variable fallback.
func cursorInteractiveStreamTranscriptEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTranscript].(bool); ok {
			return v
		}
	}
	return false
}

// cursorTranscriptStreamState polls the current turn's Cursor store.db and emits
// assistant-text and tool-call-start StreamChunks as new message blobs are
// committed. It uses a DISTINCT owner key (ownerSessionID+suffix) so its
// incremental blob dedup (cursorReturnedBlobs) is independent of the post-turn
// sidecar read — both see every blob. Cursor closes the StreamChan in the
// adapter, so this runs as a goroutine that is stopped (with a final flush)
// before any close.
type cursorTranscriptStreamState struct {
	accountHome      string
	accountConfigDir string
	workingDir       string
	nativeSessionID  string
	streamKey        string
	// baseline is set when this turn actually starts (after session
	// acquisition). Cursor spawns warmup "OK" readiness turns during acquisition,
	// each with its own store.db; only store.db modified at/after baseline belong
	// to the real turn, so we skip the warmups.
	baseline time.Time
	seenTool map[string]bool
	// toolStartedAt records wall-clock observation time (time.Now() when this
	// process first sees the tool-call blob), not a blob-carried timestamp —
	// cursor's message blobs have no per-message time field. Mirrors the
	// pattern the structured cursor adapter already uses for the same reason.
	toolStartedAt map[string]time.Time
}

func cursorTranscriptStreamKey(ownerSessionID string) string {
	return ownerSessionID + "\x00transcript-stream"
}

func newCursorTranscriptStreamState(turnStart time.Time, workingDir, ownerSessionID, nativeSessionID string, accountHome ...string) *cursorTranscriptStreamState {
	_ = turnStart // baseline is "now" (real-turn start), tighter than turnStart which predates warmups
	s := &cursorTranscriptStreamState{
		workingDir:      workingDir,
		nativeSessionID: strings.TrimSpace(nativeSessionID),
		streamKey:       cursorTranscriptStreamKey(ownerSessionID),
		baseline:        time.Now().Add(-1 * time.Second), // small slack for clock/mtime skew
		seenTool:        map[string]bool{},
		toolStartedAt:   map[string]time.Time{},
	}
	if len(accountHome) > 0 {
		s.accountHome = accountHome[0]
		if len(accountHome) > 1 {
			s.accountConfigDir = accountHome[1]
		}
	}
	s.primeSeenBlobs()
	return s
}

// primeSeenBlobs records every message blob ALREADY on disk for this working dir
// as "already returned", before the first poll can emit anything.
//
// Without this, the only thing standing between a caller and the entire chat
// history is cursorReturnedBlobs — an in-process map. Cursor's store.db root is
// cumulative across every turn of a chat, so on a fresh process (a server
// restart, a new CLI invocation) that map starts empty while the transcript
// still holds every prior turn's assistant text. The first poll then classifies
// all of it as new and streams the whole backlog, which a UI appending deltas
// renders as the entire history of "thinking" text duplicated into the current
// reply. Found live in SparkQuill: 22 historical narration blobs replayed on the
// first message after each restart.
//
// Prime only this native conversation, before sending its next prompt.
func (s *cursorTranscriptStreamState) primeSeenBlobs() {
	var paths []string
	if s.nativeSessionID != "" {
		home, _ := os.UserHomeDir()
		if s.accountHome != "" {
			home = s.accountHome
		}
		paths = nil
		if path := cursorStoreDBForNativeSession(home, s.workingDir, s.nativeSessionID, s.accountConfigDir); path != "" {
			paths = []string{path}
		}
	}
	for _, path := range paths {
		// Discards the messages: the point is the side effect of recording their
		// blob IDs against s.streamKey inside cursorReturnedBlobs.
		_ = readCursorStoreDBMessages(path, s.streamKey)
	}
}

// cursorStoreDBEffectiveModTime returns the freshest mtime among store.db and
// its -wal/-shm siblings: SQLite writes may not touch store.db until checkpoint.
func cursorStoreDBEffectiveModTime(p string, d fs.DirEntry) time.Time {
	fi, err := d.Info()
	if err != nil {
		return time.Time{}
	}
	latest := fi.ModTime()
	for _, suffix := range []string{"-wal", "-shm"} {
		if sfi, err := os.Stat(p + suffix); err == nil && sfi.ModTime().After(latest) {
			latest = sfi.ModTime()
		}
	}
	return latest
}

// run polls on a ticker until ctx is cancelled, doing one final flush on stop to
// catch blobs cursor committed right at the end of the turn.
func (s *cursorTranscriptStreamState) run(ctx context.Context, streamChan chan<- llmtypes.StreamChunk) {
	if streamChan == nil {
		return
	}
	ticker := time.NewTicker(cursorTranscriptStreamPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.poll(context.Background(), streamChan) // final flush (single-threaded now)
			return
		case <-ticker.C:
			s.poll(ctx, streamChan)
		}
	}
}

func (s *cursorTranscriptStreamState) poll(ctx context.Context, streamChan chan<- llmtypes.StreamChunk) {
	// Every turn stays bound to the native ID issued before launch.
	var path string
	if s.nativeSessionID != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		if s.accountHome != "" {
			home = s.accountHome
		}
		path = cursorStoreDBForNativeSession(home, s.workingDir, s.nativeSessionID, s.accountConfigDir)
	}
	if path == "" {
		return
	}
	msgs := readCursorStoreDBMessages(path, s.streamKey)
	for _, chunk := range cursorMessagesToChunks(msgs, s.seenTool, s.toolStartedAt) {
		select {
		case streamChan <- chunk:
		case <-ctx.Done():
			return
		}
	}
}

// cursorMessagesToChunks maps new transcript messages to stream chunks:
// assistant text -> Content, tool-call -> ToolCallStart (deduped by call id),
// tool-result -> ToolCallEnd carrying the real result text and a duration
// measured from this process's own observation of the matching start (see
// toolStartedAt's doc comment for why: cursor's blobs carry no per-message
// timestamp). Blob-level dedup already prevents a message being returned
// twice, so no content dedup is needed here. Pure/testable — no sqlite.
func cursorMessagesToChunks(msgs []llmtypes.MessageContent, seenTool map[string]bool, toolStartedAt map[string]time.Time) []llmtypes.StreamChunk {
	var out []llmtypes.StreamChunk
	meta := map[string]interface{}{"cursor_cli_stream_source": "transcript"}
	for _, m := range msgs {
		for _, p := range m.Parts {
			switch v := p.(type) {
			case llmtypes.TextContent:
				if strings.TrimSpace(v.Text) == "" {
					continue
				}
				out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: v.Text, Metadata: meta})
			case llmtypes.ToolCall:
				name := ""
				if v.FunctionCall != nil {
					name = v.FunctionCall.Name
				}
				if v.ID != "" {
					if seenTool[v.ID] {
						continue
					}
					seenTool[v.ID] = true
					if toolStartedAt != nil {
						toolStartedAt[v.ID] = time.Now()
					}
				}
				args := ""
				if v.FunctionCall != nil {
					args = v.FunctionCall.Arguments
				}
				out = append(out, llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolName: name, ToolCallID: v.ID, ToolArgs: args, Metadata: meta})
			case llmtypes.ToolCallResponse:
				out = append(out, llmtypes.StreamChunk{
					Type:         llmtypes.StreamChunkTypeToolCallEnd,
					ToolCallID:   v.ToolCallID,
					ToolResult:   v.Content,
					ToolDuration: toolclock.Elapsed(toolStartedAt, v.ToolCallID),
					Metadata:     meta,
				})
			}
		}
	}
	return out
}
