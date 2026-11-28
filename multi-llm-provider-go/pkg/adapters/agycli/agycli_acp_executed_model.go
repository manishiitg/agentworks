package agycli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// agyTranscriptTailBytes bounds how much of a transcript's end is read for the executed model.
const agyTranscriptTailBytes = 256 * 1024

// ExecutedModel is the model agy's transcript names on the newest reply since the latest prompt (T3 Code #44189):
// agy reports the model it actually ran only there, not over ACP. A model with no known price is left out, so the
// turn keeps being priced at the chosen model rather than at nothing.
func (o agyACPOutcome) ExecutedModel(sessionID string) string {
	model := agyExecutedModel(o.home, sessionID)
	if model == "" {
		return ""
	}
	if meta, err := GetAgyModelMetadata(model); err != nil || (meta.InputCostPer1MTokens == 0 && meta.OutputCostPer1MTokens == 0) {
		return ""
	}
	return model
}

func agyExecutedModel(home, session string) string {
	if home == "" || !agyNativeSessionID.MatchString(session) {
		return ""
	}
	root, err := filepath.EvalSymlinks(home)
	if err != nil {
		return ""
	}
	path := filepath.Join(root, ".gemini", "antigravity-cli", "brain", session, ".system_generated", "logs", "transcript.jsonl")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	start := info.Size() - agyTranscriptTailBytes
	if start < 0 {
		start = 0
	}
	tail, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return ""
	}
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var step struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		}
		if json.Unmarshal(lines[i], &step) != nil {
			continue // blank, or a line cut by the tail
		}
		switch step.Type {
		case "USER_INPUT":
			return ""
		case "PLANNER_RESPONSE":
			if model := strings.TrimSpace(step.Model); model != "" {
				return model
			}
		}
	}
	return ""
}
