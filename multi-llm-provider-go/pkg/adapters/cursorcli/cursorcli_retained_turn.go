package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A retained input owns the transcript suffix after its newly submitted query.
// Reading it is repeatable: polling must not consume a tool call or final reply.
type cursorRetainedInput struct {
	storeDB  string
	query    string
	baseline map[string]struct{}
	// message is the text as sent (query is whitespace-normalized) and sentAt
	// when, for matching the submitted turn in its exact native conversation.
	message string
	sentAt  time.Time
}

func (s *cursorInteractiveSession) setRetainedStore(nativeID string) {
	s.retainedMu.Lock()
	defer s.retainedMu.Unlock()
	s.retainedWorkingDir = s.workingDir
	if nativeID != "" && nativeID != s.retainedNativeID {
		s.retainedNativeID = nativeID
		s.retainedStoreDB = ""
	}
	// Initial discovery happens after launch, once Cursor has a transcript.
	if s.retainedNativeID != "" {
		s.resolveRetainedStoreLocked()
	}
}

func (s *cursorInteractiveSession) resolveRetainedStoreLocked() string {
	if s.retainedStoreDB != "" {
		return s.retainedStoreDB
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if s.accountHome != "" {
		home = s.accountHome
	}
	if s.retainedNativeID != "" {
		s.retainedStoreDB = cursorStoreDBForNativeSession(home, s.retainedWorkingDir, s.retainedNativeID, s.accountConfigDir)

	}
	return s.retainedStoreDB
}

func newCursorRetainedInput(storeDB, query string) *cursorRetainedInput {
	input := &cursorRetainedInput{storeDB: storeDB, query: strings.Join(strings.Fields(query), " "), baseline: map[string]struct{}{}, message: query, sentAt: time.Now()}
	if storeDB == "" {
		return input
	}
	db, err := sql.Open("sqlite", "file:"+storeDB+"?mode=ro")
	if err != nil {
		return input
	}
	defer db.Close()
	refs, _ := cursorStoreLatestRootRefs(context.Background(), db)
	for _, ref := range refs {
		input.baseline[ref] = struct{}{}
	}
	return input
}

// ReadRetainedTurnMessages reads the submitted query's own structured trail.
// The native store is pinned when the session starts/completes; nested image
// or helper agents in the same directory must never supply its final answer.
func ReadRetainedTurnMessages(ownerSessionID string, _ time.Time) []llmtypes.MessageContent {
	return readRetainedTurnMessages(ownerSessionID, true)
}

// ReadRetainedTurnProgressMessages consumes newly committed messages using the
// normal stream's blob cursor. Progress belongs to the native session, not just
// its latest query: a steer can be submitted before earlier narration commits.
// Callers must serialize this read with delivery and publication; a discarded
// read would otherwise consume messages without publishing them.
func ReadRetainedTurnProgressMessages(ownerSessionID string) []llmtypes.MessageContent {
	session, ok := cursorPersistentRegistry.Get(strings.TrimSpace(ownerSessionID))
	if !ok || session == nil {
		return nil
	}
	session.retainedMu.Lock()
	path := session.resolveRetainedStoreLocked()
	session.retainedMu.Unlock()
	if path == "" {
		return nil
	}
	return readCursorStoreDBMessages(path, cursorTranscriptStreamKey(ownerSessionID))
}

// A restored runtime may accept a retained send before a normal stream has
// started in this process. Prime only once, before delivery; never advance an
// existing cursor, since it may still have unpublished previous-turn messages.
func primeCursorRetainedProgress(ownerSessionID string, input *cursorRetainedInput) {
	key := cursorTranscriptStreamKey(ownerSessionID)
	cursorReturnedBlobsMu.Lock()
	defer cursorReturnedBlobsMu.Unlock()
	if _, exists := cursorReturnedBlobs[key]; exists {
		return
	}
	seen := make(map[string]struct{}, len(input.baseline))
	for ref := range input.baseline {
		seen[ref] = struct{}{}
	}
	cursorReturnedBlobs[key] = seen
}

func readRetainedTurnMessages(ownerSessionID string, requireIdle bool) []llmtypes.MessageContent {
	session, ok := cursorPersistentRegistry.Get(strings.TrimSpace(ownerSessionID))
	if !ok || session == nil {
		return nil
	}
	session.retainedMu.Lock()
	input := session.retainedInput
	if input != nil && input.storeDB == "" {
		copy := *input
		copy.storeDB = session.resolveRetainedStoreLocked()
		input = &copy
	}
	session.retainedMu.Unlock()
	if input == nil {
		return nil
	}
	// Cursor can commit standalone commentary before a later tool-call blob.
	// Its idle composer is required in addition to the query-bound transcript.
	if requireIdle {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pane, err := captureCursorPane(ctx, session.tmuxSessionName)
		if err != nil || !PaneReadyForInput(pane) {
			return nil
		}
	}
	if messages := readCursorRetainedInput(input); messages != nil {
		return messages
	}
	// The issued store may first appear after the input was sent. Resolve only
	// that native ID; never select another conversation with matching text.
	if path := resolveCursorStoreNoMu(session); path != "" && input.storeDB == "" {
		updated := *input
		updated.storeDB = path
		return readCursorRetainedInput(&updated)
	}
	return nil
}

func readCursorRetainedInput(input *cursorRetainedInput) []llmtypes.MessageContent {
	if input == nil || input.storeDB == "" || input.query == "" {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+input.storeDB+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	ctx := context.Background()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return nil
	}
	var out []llmtypes.MessageContent
	matched := false
	for _, ref := range refs {
		data := readCursorBlob(ctx, db, ref)
		var msg cursorMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			query := cursorUserQueryFromContent(msg.Content)
			if query == "" {
				continue
			}
			_, old := input.baseline[ref]
			isOurs := !old && strings.Join(strings.Fields(query), " ") == input.query
			if matched && !isOurs {
				// A later user row after our query is a live input (steer) sent
				// during this turn: Cursor absorbs it and continues, and the
				// turn's answer is the prose after it. Keep it in this trail;
				// resetting here dropped the steered turn's final answer (store
				// held one complete line; the pane fallback returned only its
				// last wrapped line).
				out = append(out, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, query))
				continue
			}
			matched = isOurs
			out = nil
			if matched {
				out = append(out, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, query))
			}
		case "assistant":
			if matched {
				if parts := cursorAssistantPartsFromContent(msg.Content); len(parts) > 0 {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: parts})
				}
			}
		case "tool":
			if matched {
				if parts := cursorToolPartsFromContent(msg.Content); len(parts) > 0 {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: parts})
				}
			}
		}
	}
	return out
}

// cursorTrailState classifies one query's store trail for turn completion.
type cursorTrailState int

const (
	// cursorTrailNotTaken: no user row for the query yet (not taken in).
	cursorTrailNotTaken cursorTrailState = iota
	// cursorTrailTaken: the query is recorded but no reply has committed.
	cursorTrailTaken
	// cursorTrailPendingTool: the latest assistant tool call has no result.
	cursorTrailPendingTool
	// cursorTrailAwaitingReply: the latest tool returned; no reply followed.
	cursorTrailAwaitingReply
	// cursorTrailFinished: the trail ends in assistant prose -- the answer.
	cursorTrailFinished
)

// cursorTurnTrailState reports where one query's trail stands and, when
// finished, its final assistant text. Cursor writes no end-of-turn marker, so
// the shape of the trail is the proof: a trailing tool call means a tool is
// still running, a trailing tool result means the reply is missing, and
// trailing prose (with no tool call in it) is the finished answer.
func cursorTurnTrailState(trail []llmtypes.MessageContent) (string, cursorTrailState) {
	if len(trail) == 0 {
		return "", cursorTrailNotTaken
	}
	last := trail[len(trail)-1]
	switch last.Role {
	case llmtypes.ChatMessageTypeTool:
		return "", cursorTrailAwaitingReply
	case llmtypes.ChatMessageTypeAI:
	default:
		return "", cursorTrailTaken
	}
	var text strings.Builder
	for _, part := range last.Parts {
		switch p := part.(type) {
		case llmtypes.TextContent:
			text.WriteString(p.Text)
		case llmtypes.ToolCall:
			return "", cursorTrailPendingTool
		}
	}
	answer := strings.TrimSpace(text.String())
	if answer == "" {
		return "", cursorTrailTaken
	}
	return answer, cursorTrailFinished
}
