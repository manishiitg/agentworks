package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestSessionInstructionsArePrivateAndDurable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	const prompt = "## Relay Builder\nUse the relay-builder skill.\n"
	if err := saveSessionInstructions("alice", "chat/../../relay", "Chats/Work/projects/test", prompt); err != nil {
		t.Fatal(err)
	}
	path, err := sessionInstructionPath("alice", "chat/../../relay")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(root, "instruction-snapshots")+string(os.PathSeparator)) {
		t.Fatal("path escaped private state")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions: %v, %v", info, err)
	}
	// A fresh API instance can read the owner's last prepared prompt after restart.
	api := &StreamingAPI{}
	for _, user := range []string{"alice", "bob"} {
		req := requestWithUserForSessionAccess(user)
		req = mux.SetURLVars(req, map[string]string{"session_id": "chat/../../relay"})
		w := httptest.NewRecorder()
		api.handleGetSessionInstructions(w, req)
		if user == "bob" {
			if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "Relay Builder") {
				t.Fatal("another user read private instructions")
			}
			continue
		}
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("read failed: %d %s", w.Code, w.Body.String())
		}
		var snapshot sessionInstructionSnapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil || snapshot.Content != prompt {
			t.Fatalf("wrong prompt: %+v %v", snapshot, err)
		}
	}
	if err := saveSessionInstructions("alice", "chat/../../relay", "Chats/Work/projects/test", "Updated Relay prompt"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Updated Relay prompt") {
		t.Fatalf("stale snapshot: %s %v", data, err)
	}
	if err := deleteSessionInstructions("alice", "chat/../../relay"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("deleted chat retained its prompt")
	}
}
