package codexcli

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Only native session metadata establishes parentage; a shared cwd or a recent
// mtime is never enough to import another conversation into an execution.
func readCodexNativeSubagents(parentID, workingDir, accountRoot string) []llmtypes.NativeSubagentTranscript {
	if parentID == "" {
		return nil
	}
	type candidate struct {
		path, id, parent, name string
		started                time.Time
	}
	var candidates []candidate
	_ = filepath.WalkDir(codexSessionsRoot(accountRoot), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 16*1024*1024)
		for scanner.Scan() {
			var row struct {
				Type      string    `json:"type"`
				Timestamp time.Time `json:"timestamp"`
				Payload   struct {
					ID            string `json:"id"`
					CWD           string `json:"cwd"`
					AgentNickname string `json:"agent_nickname"`
					Source        struct {
						Subagent struct {
							ThreadSpawn struct {
								ParentID string `json:"parent_thread_id"`
								Name     string `json:"agent_nickname"`
							} `json:"thread_spawn"`
						} `json:"subagent"`
					} `json:"source"`
				} `json:"payload"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) != nil {
				continue
			}
			if row.Type != "session_meta" {
				continue
			}
			if row.Payload.Source.Subagent.ThreadSpawn.ParentID != "" && sameCodexWorkingDir(row.Payload.CWD, workingDir) {
				name := row.Payload.AgentNickname
				if name == "" {
					name = row.Payload.Source.Subagent.ThreadSpawn.Name
				}
				if name == "" {
					name = "Native agent"
				}
				candidates = append(candidates, candidate{path, row.Payload.ID, row.Payload.Source.Subagent.ThreadSpawn.ParentID, name, row.Timestamp})
			}
			break
		}
		return nil
	})
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].started.Before(candidates[j].started) })
	known := map[string]bool{parentID: true}
	seen := map[string]bool{}
	var out []llmtypes.NativeSubagentTranscript
	for changed := true; changed; {
		changed = false
		for _, child := range candidates {
			if child.id == "" || seen[child.id] || !known[child.parent] {
				continue
			}
			seen[child.id], known[child.id], changed = true, true, true
			messages, _ := readCodexTranscriptMessagesFile(child.path, time.Time{}, true)
			status := "running"
			if text, _ := readCodexRolloutFinalAssistantText(child.path, time.Time{}); text != "" {
				status = "completed"
			}
			if readCodexRolloutTurnError(child.path, time.Time{}) != "" {
				status = "failed"
			}
			updated := time.Now()
			if info, err := os.Stat(child.path); err == nil {
				updated = info.ModTime()
			}
			out = append(out, llmtypes.NativeSubagentTranscript{ID: child.id, ParentID: child.parent, Name: child.name, Provider: "codex-cli", Status: status, StartedAt: child.started, UpdatedAt: updated, Messages: messages})
		}
	}
	return out
}
