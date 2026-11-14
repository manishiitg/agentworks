package server

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Codex CLI keeps its own per-thread transcript ("rollout") as JSONL under
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<started>-<thread id>.jsonl. This is
// the Codex counterpart of the Claude Code reader in
// claude_native_transcript_sync.go: the PLAT-178 catch-up (retained live-input
// turns are never written to the durable builder conversation by the
// live-input path) was Claude-only, so a Codex builder chat continued through
// live input silently lost every later turn from Recent/Resume.

type codexRolloutEntry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexRolloutMessagePayload struct {
	Type    string                     `json:"type"`
	Role    string                     `json:"role"`
	Content []codexRolloutContentBlock `json:"content"`
}

type codexRolloutContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexSessionsRoot mirrors multi-llm-provider-go's codexcli adapter
// (codexcli_transcript_completion.go codexSessionsRoot): CODEX_HOME wins,
// else ~/.codex.
func codexSessionsRoot(accountHome ...string) (string, error) {
	if len(accountHome) > 0 && accountHome[0] != "" {
		return filepath.Join(accountHome[0], ".codex", "sessions"), nil
	}
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// resolveCodexNativeTranscriptPath locates the rollout for a Codex thread id.
// The date folders are the thread's *start* date, which the builder record
// does not carry, so the lookup is by file-name suffix across the tree. The
// newest match wins if the id somehow appears more than once.
func resolveCodexNativeTranscriptPath(nativeSessionID string, accountHome ...string) (string, error) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if nativeSessionID == "" {
		return "", nil
	}
	root, err := codexSessionsRoot(accountHome...)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(root); err != nil {
		return "", nil
	}

	wantSuffix := "-" + nativeSessionID + ".jsonl"
	type match struct {
		path    string
		modTime time.Time
	}
	matches := []match{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, wantSuffix) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		matches = append(matches, match{path: path, modTime: info.ModTime()})
		return nil
	})
	if len(matches) == 0 {
		return "", nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].modTime.After(matches[j].modTime) })
	return matches[0].path, nil
}

// readCodexTranscriptMessages parses a Codex rollout and returns the
// human-visible user/assistant messages in order, in the builder
// conversation's own shape, plus the newest timestamp seen.
//
// Only `response_item` rows whose payload is a `message` count. Reasoning,
// tool calls/outputs, token counts and the `event_msg` stream duplicates are
// execution detail. Codex also injects a few user-role messages that the
// user never typed (the AGENTS.md preamble, environment context, and any
// developer-role instructions); those are dropped so they never surface as
// chat turns.
func readCodexTranscriptMessages(path string) ([]builderConversationMessage, time.Time, error) {
	return readRecentNativeTranscript(path, "codex-cli", time.Time{})
}

func parseCodexTranscriptLine(line []byte) (builderConversationMessage, time.Time, bool) {
	var entry codexRolloutEntry
	if json.Unmarshal(line, &entry) != nil || entry.Type != "response_item" {
		return builderConversationMessage{}, time.Time{}, false
	}
	var payload codexRolloutMessagePayload
	if json.Unmarshal(entry.Payload, &payload) != nil || payload.Type != "message" {
		return builderConversationMessage{}, time.Time{}, false
	}
	role := ""
	switch payload.Role {
	case "user":
		role = "human"
	case "assistant":
		role = "ai"
	default:
		return builderConversationMessage{}, time.Time{}, false
	}
	text := extractCodexTranscriptText(payload.Content)
	if text == "" || (role == "human" && isCodexInjectedUserMessage(text)) {
		return builderConversationMessage{}, time.Time{}, false
	}
	stamp, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return builderConversationMessage{Role: role, Parts: []builderConversationPart{{Text: text}}}, stamp, true
}

func extractCodexTranscriptText(blocks []codexRolloutContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "input_text" && block.Type != "output_text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// isCodexInjectedUserMessage recognizes the user-role rows Codex writes on
// its own: the project-instructions preamble and XML-tagged context blocks.
func isCodexInjectedUserMessage(text string) bool {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "# AGENTS.md instructions") {
		return true
	}
	for _, tag := range []string{"<environment_context>", "<user_instructions>", "<permissions", "<turn_aborted>", "<app-instructions>", "<skills_instructions>"} {
		if strings.HasPrefix(trimmed, tag) {
			return true
		}
	}
	return false
}
