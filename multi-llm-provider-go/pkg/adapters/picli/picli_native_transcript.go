package picli

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// NativeTranscript is the human-visible conversation pi keeps for one of its
// own sessions: typed user messages and assistant prose, in order, with
// tool calls, thinking and the embedded system prompt left out. A caller
// that persists a durable conversation record uses it to catch that record
// up after turns that never passed through the adapter's normal completion
// path (retained live-input turns).
type NativeTranscript struct {
	// Path is the session JSONL the messages came from.
	Path string
	// Messages holds Human/AI text-only messages in chronological order.
	Messages []llmtypes.MessageContent
	// UpdatedAt is the newest record timestamp seen.
	UpdatedAt time.Time
}

// ReadNativeTranscript reads pi's own transcript for sessionID (the value
// `--session-id` was given). Resolution honours PI_CODING_AGENT_SESSION_DIR /
// PI_CODING_AGENT_DIR the way the adapter's own transcript reader does. A
// missing transcript is not an error: ok is false and the transcript is
// empty.
func ReadNativeTranscript(sessionID string) (transcript NativeTranscript, ok bool, err error) {
	return readNativeTranscriptPath(latestPiTranscriptPath(sessionID))
}

// ReadNativeTranscriptFromWorkingDir resolves transcripts from the same
// session-scoped Pi runtime directory used at launch. Callers that know the
// workspace should prefer this so transcript recovery survives process and
// server restarts without depending on a shared HOME.
func ReadNativeTranscriptFromWorkingDir(workingDir, sessionID string) (transcript NativeTranscript, ok bool, err error) {
	_, sessionDir := piSessionRuntimeDirs(workingDir, sessionID)
	return readNativeTranscriptPath(latestPiTranscriptPathInDir(sessionDir, sessionID))
}

func readNativeTranscriptPath(path string) (transcript NativeTranscript, ok bool, err error) {
	if path == "" {
		return NativeTranscript{}, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return NativeTranscript{}, false, err
	}
	defer f.Close()

	transcript.Path = path
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev piTranscriptRecord
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil || ev.Type != "message" || ev.Message == nil {
			continue
		}
		role, ok := piTranscriptRole(ev.Message.Role)
		if !ok || (role != llmtypes.ChatMessageTypeHuman && role != llmtypes.ChatMessageTypeAI) {
			continue
		}
		text := piTranscriptText(ev.Message.Content)
		if text == "" {
			continue
		}
		// The adapter has no separate system-prompt channel for pi; it
		// prefixes the prompt onto the first user message as "System: ...".
		// That is not something the user typed.
		if role == llmtypes.ChatMessageTypeHuman && strings.HasPrefix(text, "System: ") {
			continue
		}
		transcript.Messages = append(transcript.Messages, llmtypes.MessageContent{
			Role:  role,
			Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
		})
		if ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil && ts.After(transcript.UpdatedAt) {
			transcript.UpdatedAt = ts
		}
	}
	if err := scanner.Err(); err != nil {
		return NativeTranscript{}, false, err
	}
	return transcript, true, nil
}

// piTranscriptText joins a message's text blocks; thinking and toolCall
// blocks are execution detail and are skipped.
func piTranscriptText(content []piTranscriptContent) string {
	texts := make([]string, 0, len(content))
	for _, part := range content {
		if t := strings.TrimSpace(part.Type); t != "" && t != "text" {
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}
