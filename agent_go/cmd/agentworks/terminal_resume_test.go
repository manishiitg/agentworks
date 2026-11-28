package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chatterminal"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-569: the terminal gets a copy with its own session ID; an existing
// local journal and linked provider paths are never overwritten.
func TestTerminalResumeCopiesWithNewIDAndNeverOverwrites(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	root := t.TempDir()
	stage := t.TempDir()
	store := t.TempDir()
	raw := []byte(`{"type":"user","sessionId":"` + id + `","cwd":"/elsewhere","message":{"role":"user","content":"Hello"}}` + "\n")
	sum := sha256.Sum256(raw)
	if e := os.WriteFile(filepath.Join(stage, "session.jsonl"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: id, Workspace: "/elsewhere", Root: stage, Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}}}
	copied, e := sessiontransfer.ForkJSONL(b, filepath.Join(t.TempDir(), "copy"), root)
	if e != nil {
		t.Fatal(e)
	}
	if copied.SessionID == id {
		t.Fatal("terminal copy reused the chat's session ID")
	}
	dest, e := installTerminalJournal(store, root, copied)
	if e != nil {
		t.Fatal(e)
	}
	if got, _ := os.ReadFile(dest); !strings.Contains(string(got), copied.SessionID) || strings.Contains(string(got), id) {
		t.Fatal("installed journal does not carry the copy's own session ID")
	}
	private := []byte("an unrelated local conversation")
	if e = os.WriteFile(dest, private, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(store, root, copied); e == nil {
		t.Fatal("existing local journal replaced")
	}
	if got, _ := os.ReadFile(dest); string(got) != string(private) {
		t.Fatal("local conversation was overwritten")
	}
	linked := t.TempDir()
	if e = os.Symlink(store, filepath.Join(linked, "projects")); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(linked, root, copied); e == nil {
		t.Fatal("linked provider path accepted")
	}
}

// Local IDs alone are not a cross-server mapping. The saved pair, including
// its server origin, must name this exact remote Crew.
func TestTerminalResumeResolvesOnlyExactSavedProjectPair(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	if e := os.MkdirAll(filepath.Join(state, "ownership"), 0700); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	record := crewroot.Record{ID: "local-project", Product: "work", Folder: "folder", Owner: "owner", LocalRoot: root}
	writeRecord := func() {
		raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/folder": record}})
		if e := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	writeRecord()
	ref := chatterminal.Reference{Server: "https://example.com", Project: "local-project", Key: "chat"}
	if _, e := terminalLocalFolder(ref, root); e == nil {
		t.Fatal("unpaired same local ID accepted as a remote mapping")
	}
	ref.Project = "remote-project"
	if e := crewroot.WriteSync(record.ID, crewroot.SyncBinding{PairID: "pair", Side: "local", Editing: "server", PeerProjectID: ref.Project, PeerServer: ref.Server}); e != nil {
		t.Fatal(e)
	}
	if got, e := terminalLocalFolder(ref, "/a/remote/untrusted/path"); e != nil || got != root {
		t.Fatalf("saved local pair: %q %v", got, e)
	}
	wrong := ref
	wrong.Server = "https://different.example"
	if _, e := terminalLocalFolder(wrong, root); e == nil {
		t.Fatal("same remote ID on a different server accepted")
	}
	// Managed Crews use the app's configured docs root, not a guessed CWD.
	docs := t.TempDir()
	managed := filepath.Join(docs, "Crew", record.Folder)
	if e := os.MkdirAll(managed, 0700); e != nil {
		t.Fatal(e)
	}
	settings, _ := json.Marshal(map[string]any{"docsDir": docs})
	if e := os.WriteFile(filepath.Join(filepath.Dir(state), "config.json"), settings, 0600); e != nil {
		t.Fatal(e)
	}
	record.LocalRoot = ""
	writeRecord()
	managed, _ = filepath.EvalSymlinks(managed)
	if got, e := terminalLocalFolder(ref, ""); e != nil || got != managed {
		t.Fatalf("managed folder: %q %v", got, e)
	}
}
