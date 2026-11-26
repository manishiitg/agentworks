package musecli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func readMuseNativeSubagents(sessionID string) []llmtypes.NativeSubagentTranscript {
	parentLog := museSessionLogPath(sessionID)
	if parentLog == "" {
		return nil
	}
	root := filepath.Join(filepath.Dir(parentLog), "subagent")
	var out []llmtypes.NativeSubagentTranscript
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "session.jsonl" {
			return nil
		}
		messages, ok := readMuseTranscriptMessages(path, "", true)
		if !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		status := "running"
		raw, _ := os.ReadFile(path)
		var started time.Time
		for _, line := range strings.Split(string(raw), "\n") {
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			if stamp, ok := row["recorded_at"].(float64); ok && started.IsZero() {
				started = time.UnixMicro(int64(stamp))
			}
			if terminal := museLogString(row, "payload", "event", "terminal"); terminal != "" {
				status = "completed"
				if terminal != "completed" && terminal != "success" {
					status = "failed"
				}
			}
		}
		id := filepath.Base(filepath.Dir(path))
		out = append(out, llmtypes.NativeSubagentTranscript{ID: id, ParentID: sessionID, Name: "Native agent " + id, Provider: "muse-cli", Status: status, StartedAt: started, UpdatedAt: info.ModTime(), Messages: messages})
		return nil
	})
	return out
}
