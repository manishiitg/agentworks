package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Claude stores children beside the exact parent's transcript, under that
// session's subagents directory. Never search unrelated projects for children.
func readClaudeNativeSubagents(sessionID, workingDir, accountHome string) []llmtypes.NativeSubagentTranscript {
	parentPath, err := resolveClaudeTranscriptPath(sessionID, workingDir, false, accountHome)
	if err != nil || parentPath == "" {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(parentPath, ".jsonl"), "subagents", "agent-*.jsonl"))
	var out []llmtypes.NativeSubagentTranscript
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		messages := parseClaudeTranscriptMessages(file, time.Time{}, true)
		_ = file.Close()
		file, err = os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 16*1024*1024)
		started := time.Time{}
		status := "running"
		for scanner.Scan() {
			var row struct {
				Type      string    `json:"type"`
				Timestamp time.Time `json:"timestamp"`
				Message   struct {
					StopReason string `json:"stop_reason"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) != nil {
				continue
			}
			if started.IsZero() {
				started = row.Timestamp
			}
			if row.Type == "assistant" {
				if row.Message.StopReason == "end_turn" {
					status = "completed"
				} else {
					status = "running"
				}
			}
		}
		info, statErr := file.Stat()
		_ = file.Close()
		if statErr != nil {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		name := "Native agent " + strings.TrimPrefix(id, "agent-")
		meta := readClaudeSubagentMeta(strings.TrimSuffix(path, ".jsonl") + ".meta.json")
		if meta.Description != "" {
			name = meta.Description
		}
		if meta.Finished && status == "running" {
			status = "completed"
		}
		out = append(out, llmtypes.NativeSubagentTranscript{ID: id, ParentID: sessionID, ToolUseID: meta.ToolUseID, Name: name, Provider: "claude-code", Status: status, StartedAt: started, UpdatedAt: info.ModTime(), Messages: messages})
	}
	return out
}

type claudeSubagentMeta struct {
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	Finished    bool   `json:"finished"`
}

// Claude writes agent-<id>.meta.json beside each child: the Agent call that
// started it and its short description.
func readClaudeSubagentMeta(path string) claudeSubagentMeta {
	var meta claudeSubagentMeta
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	return meta
}

// claudeTranscriptPlace is where one chat's Claude session keeps its
// transcript. It outlives the Claude process, so a finished child stays
// readable until the server restarts and the chat's next turn records it again.
type claudeTranscriptPlace struct{ nativeID, workingDir, accountHome string }

var claudeTranscriptPlaces sync.Map // owner -> claudeTranscriptPlace

func rememberClaudeTranscriptPlace(owner, nativeID, workingDir, accountHome string) {
	if owner = strings.TrimSpace(owner); owner != "" && nativeID != "" {
		claudeTranscriptPlaces.Store(owner, claudeTranscriptPlace{nativeID, workingDir, accountHome})
	}
}

// ClaudeSubagentConversation reads, as it is now, the conversation of the
// subagent this chat's Claude started with the Agent/Task call toolUseID
// (#3132). It only reads the chat's own Claude session folder.
func ClaudeSubagentConversation(owner, toolUseID string) (llmtypes.NativeSubagentTranscript, bool) {
	value, ok := claudeTranscriptPlaces.Load(strings.TrimSpace(owner))
	if !ok || strings.TrimSpace(toolUseID) == "" {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	place := value.(claudeTranscriptPlace)
	for _, child := range readClaudeNativeSubagents(place.nativeID, place.workingDir, place.accountHome) {
		if child.ToolUseID == toolUseID {
			return child, true
		}
	}
	return llmtypes.NativeSubagentTranscript{}, false
}
