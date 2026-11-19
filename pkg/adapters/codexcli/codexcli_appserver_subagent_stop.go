package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3245: stop one helper agent a chat's Codex started, without stopping the
// chat. Its rollout says which turn it is running (task_started without a
// task_complete); the chat's app-server interrupts that turn of the child
// thread.

// codexRolloutOpenTurn is the turn a rollout started and has not finished.
func codexRolloutOpenTurn(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	open := ""
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if text := string(line); !strings.Contains(text, `"task_`) && !strings.Contains(text, `"turn_aborted"`) {
			continue
		}
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "event_msg" {
			continue
		}
		switch row.Payload.Type {
		case "task_started":
			if row.Payload.TurnID != "" {
				open = row.Payload.TurnID
			}
		case "task_complete", "turn_aborted":
			// An aborted turn may not name itself; it ends the open one.
			if row.Payload.TurnID == "" || open == row.Payload.TurnID {
				open = ""
			}
		}
	}
	return open
}

// codexSubagentOpenTurn is the child thread and running turn of the helper
// the chat's spawnAgent call callID started.
func codexSubagentOpenTurn(owner, callID string) (thread, turn string) {
	owner, callID = strings.TrimSpace(owner), strings.TrimSpace(callID)
	child, ok := codexChildThreads.Load(owner + "\x00" + callID)
	if !ok {
		return "", ""
	}
	home, _ := codexChatHomes.Load(owner)
	root, _ := home.(string)
	path := findCodexRolloutForThread(child.(string), root)
	if path == "" {
		return child.(string), ""
	}
	return child.(string), codexRolloutOpenTurn(path)
}

// CodexSubagentCanStop reports whether the helper is running a turn the
// chat's live Codex app-server can interrupt.
func CodexSubagentCanStop(owner, callID string) bool {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return false
	}
	s.mu.Lock()
	live := s.rpc != nil
	s.mu.Unlock()
	_, turn := codexSubagentOpenTurn(owner, callID)
	return live && turn != ""
}

// InterruptCodexSubagent stops the running turn of one helper agent; the
// chat and its other helpers keep running.
func InterruptCodexSubagent(ctx context.Context, owner, callID string) error {
	s := codexAppSessionForOwner(owner)
	if s == nil {
		return fmt.Errorf("%w: no Codex app-server session for owner", llmtypes.ErrCodingAgentInputNotSent)
	}
	thread, turn := codexSubagentOpenTurn(owner, callID)
	if thread == "" || turn == "" {
		return fmt.Errorf("this Codex helper is not running")
	}
	s.mu.Lock()
	r := s.rpc
	s.mu.Unlock()
	if r == nil {
		return fmt.Errorf("no active Codex app-server")
	}
	return r.call(ctx, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, nil)
}
