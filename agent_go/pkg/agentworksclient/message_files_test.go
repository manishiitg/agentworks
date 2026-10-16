package agentworksclient

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A received file is saved inside the chosen folder under its base name and never over
// a file that is already there.
func TestSaveReceivedFileStaysInFolderAndKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	body, _ := json.Marshal(map[string]any{"file_name": "../../evil.txt", "size_bytes": 2, "content_base64": base64.StdEncoding.EncodeToString([]byte("hi"))})
	for i, want := range []string{"evil.txt", "evil (1).txt"} {
		out := LocalBridgeReply{Result: body}
		if err := SaveReceivedFile(&out, dir); err != nil {
			t.Fatal(err)
		}
		var got struct {
			SavedTo string `json:"saved_to"`
		}
		_ = json.Unmarshal(out.Result, &got)
		if got.SavedTo != filepath.Join(dir, want) {
			t.Fatalf("save %d went to %q, want %q", i, got.SavedTo, filepath.Join(dir, want))
		}
		if data, err := os.ReadFile(got.SavedTo); err != nil || string(data) != "hi" {
			t.Fatalf("saved content wrong: %q %v", data, err)
		}
	}
}
