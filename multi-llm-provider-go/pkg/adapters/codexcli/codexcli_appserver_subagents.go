package codexcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3132: a chat opens the conversation of a helper agent Codex spawned, by
// the spawnAgent call that started it, and reads it again while it runs.

var codexChatHomes sync.Map    // owner -> Codex account home of the chat's process
var codexChildThreads sync.Map // owner + "\x00" + call ID -> child thread ID

func codexAccountHome(opts *llmtypes.CallOptions) string {
	home := llmtypes.CLIHomeEnvironment(opts)["CODEX_HOME"]
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	return home
}

func rememberCodexChatHome(owner string, opts *llmtypes.CallOptions) {
	if owner = strings.TrimSpace(owner); owner != "" {
		codexChatHomes.Store(owner, codexAccountHome(opts))
	}
}

// rememberCodexChild records which thread a spawnAgent call started.
func rememberCodexChild(owner string, raw json.RawMessage) {
	var item struct {
		ID                string   `json:"id"`
		Type              string   `json:"type"`
		Tool              string   `json:"tool"`
		ReceiverThreadIDs []string `json:"receiverThreadIds"`
		Kind              string   `json:"kind"`
		AgentThreadID     string   `json:"agentThreadId"`
	}
	if owner == "" || json.Unmarshal(raw, &item) != nil || item.ID == "" {
		return
	}
	// Codex 0.170+: a subAgentActivity "started" item carries the child thread.
	if item.Type == "subAgentActivity" && item.Kind == "started" && item.AgentThreadID != "" {
		item.Type, item.Tool, item.ReceiverThreadIDs = "collabAgentToolCall", "spawnAgent", []string{item.AgentThreadID}
	}
	if item.Type != "collabAgentToolCall" || item.Tool != "spawnAgent" || len(item.ReceiverThreadIDs) == 0 {
		return
	}
	codexChildThreads.Store(owner+"\x00"+item.ID, item.ReceiverThreadIDs[0])
}

// CodexSubagentConversation reads, as it is now, the conversation of the
// helper agent this chat's Codex started with the spawnAgent call callID.
// Only the chat's own account folder is read.
func CodexSubagentConversation(owner, callID string) (llmtypes.NativeSubagentTranscript, bool) {
	owner, callID = strings.TrimSpace(owner), strings.TrimSpace(callID)
	thread, ok := codexChildThreads.Load(owner + "\x00" + callID)
	if !ok {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	home, _ := codexChatHomes.Load(owner)
	root, _ := home.(string)
	path := findCodexRolloutForThread(thread.(string), root)
	if path == "" {
		return llmtypes.NativeSubagentTranscript{}, false
	}
	messages, _ := readCodexTranscriptMessagesFile(path, time.Time{}, true)
	status := "running"
	if text, _ := readCodexRolloutFinalAssistantText(path, time.Time{}); text != "" {
		status = "completed"
	}
	if readCodexRolloutTurnError(path, time.Time{}) != "" {
		status = "failed"
	}
	started, updated := time.Time{}, time.Now()
	if info, err := os.Stat(path); err == nil {
		updated = info.ModTime()
	}
	name := "Helper agent"
	if meta := readCodexChildMeta(path); meta.name != "" {
		name, started = meta.name, meta.started
	}
	return llmtypes.NativeSubagentTranscript{ID: thread.(string), ToolUseID: callID, Name: name, Provider: "codex-cli", Status: status, StartedAt: started, UpdatedAt: updated, Messages: messages}, true
}

type codexChildMeta struct {
	name    string
	started time.Time
}

func readCodexChildMeta(path string) codexChildMeta {
	f, err := os.Open(path)
	if err != nil {
		return codexChildMeta{}
	}
	defer f.Close()
	var row struct {
		Type      string    `json:"type"`
		Timestamp time.Time `json:"timestamp"`
		Payload   struct {
			AgentNickname string `json:"agent_nickname"`
			Source        struct {
				Subagent struct {
					ThreadSpawn struct {
						Name string `json:"agent_nickname"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			} `json:"source"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&row) != nil || row.Type != "session_meta" {
		return codexChildMeta{}
	}
	name := row.Payload.AgentNickname
	if name == "" {
		name = row.Payload.Source.Subagent.ThreadSpawn.Name
	}
	return codexChildMeta{name: name, started: row.Timestamp}
}
