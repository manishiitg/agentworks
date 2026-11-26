package chatlog

import (
	"encoding/json"
	"os"
)

// RuntimeMetadata reads only the conversation header. It never opens the
// history log or decodes message content; context controls need no transcript.
func RuntimeMetadata(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var metadata struct {
		SessionID string          `json:"session_id"`
		Runtime   json.RawMessage `json:"runtime,omitempty"`
		Mode      string          `json:"workshop_mode,omitempty"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	return json.Marshal(metadata)
}
