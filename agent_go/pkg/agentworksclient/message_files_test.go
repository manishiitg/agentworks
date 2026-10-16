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

// Sign-in folders cannot be sent, whatever a peer message asks for.
func TestReadMessageFileRefusesSignInFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, dir := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "agentworks")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, "secret")
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadMessageFile(f); err == nil {
			t.Errorf("%s was readable for sending", f)
		}
	}
	ok := filepath.Join(home, "notes.txt")
	_ = os.WriteFile(ok, []byte("hello"), 0o600)
	if _, _, err := ReadMessageFile(ok); err != nil {
		t.Errorf("an ordinary file was refused: %v", err)
	}
}
