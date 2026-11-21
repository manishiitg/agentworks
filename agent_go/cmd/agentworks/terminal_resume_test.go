package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chatterminal"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-569: only append-only descendants may replace a local journal;
// unrelated branches and linked paths must never be overwritten.
func TestTerminalResumeKeepsDifferingJournalAndRefusesLinks(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	root := t.TempDir()
	stage := t.TempDir()
	store := t.TempDir()
	raw := []byte(`{"type":"user","sessionId":"` + id + `","message":{"role":"user","content":"Hello"}}` + "\n")
	sum := sha256.Sum256(raw)
	if e := os.WriteFile(filepath.Join(stage, "session.jsonl"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: id, Workspace: root, Root: stage, Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}}}
	dest, e := installTerminalJournal(store, root, b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(store, root, b); e != nil {
		t.Fatal("identical journal must be reusable", e)
	}
	// After attach-back and a platform turn, a second resume grows this same journal.
	grown := append(append([]byte{}, raw...), []byte(`{"type":"assistant","sessionId":"`+id+`","message":{"role":"assistant","content":"Answer"}}`+"\n")...)
	if e = os.WriteFile(filepath.Join(stage, "session.jsonl"), grown, 0600); e != nil {
		t.Fatal(e)
	}
	sum = sha256.Sum256(grown)
	b.Files["session.jsonl"] = sessiontransfer.File{Size: int64(len(grown)), SHA256: hex.EncodeToString(sum[:])}
	if _, e = installTerminalJournal(store, root, b); e != nil {
		t.Fatal("append-only descendant refused", e)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(grown) {
		t.Fatal("second resume did not append native history")
	}
	private := []byte("an unrelated local conversation")
	if e = os.WriteFile(dest, private, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(store, root, b); e == nil {
		t.Fatal("differing local journal replaced")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(private) {
		t.Fatal("local conversation was overwritten")
	}
	linked := t.TempDir()
	if e = os.Symlink(store, filepath.Join(linked, "projects")); e != nil {
		t.Fatal(e)
	}
	if _, e = installTerminalJournal(linked, root, b); e == nil {
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

// PLAT-856-569: an old handoff receipt must not permanently intercept normal
// attach, and a successful return must clear it. Unknown cwd is not proof.
func TestTerminalReturnClearsReceiptAndRequiresExactFolder(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	folder := t.TempDir()
	folder, _ = filepath.EvalSymlinks(folder)
	store := t.TempDir()
	raw := []byte(`{"type":"user","sessionId":"` + id + `","message":{"role":"user","content":"Hello"}}` + "\n")
	native, e := sessiontransfer.Path(store, "projects/"+sessiontransfer.ClaudeSlug(folder)+"/"+id+".jsonl", true)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(native, raw, 0600); e != nil {
		t.Fatal(e)
	}
	out := true
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") == "status" {
			_ = json.NewEncoder(w).Encode(map[string]bool{"out": out})
			return
		}
		if r.URL.Query().Get("action") != "return" {
			t.Errorf("unexpected action: %s", r.URL.Query().Get("action"))
			w.WriteHeader(400)
			return
		}
		calls++
		incoming, e := chatterminal.Read(r.Body, t.TempDir())
		if e != nil || incoming.Bundle.SessionID != id {
			t.Errorf("invalid own return: %v", e)
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"out":false}`))
	}))
	defer server.Close()
	o := &options{configPath: filepath.Join(t.TempDir(), "config.json"), serverURL: server.URL, stdout: io.Discard, stderr: io.Discard, getenv: func(k string) string {
		if k == "AGENTWORKS_TOKEN" {
			return "test-token"
		}
		if k == "CLAUDE_CONFIG_DIR" {
			return store
		}
		return ""
	}}
	receiptPath, e := terminalReceiptPath(o, id)
	if e != nil {
		t.Fatal(e)
	}
	save := func() {
		t.Helper()
		raw, _ := json.Marshal(terminalReceipt{Reference: chatterminal.Reference{Server: server.URL, Key: "chat", Project: "project"}, Receipt: "receipt", Session: "platform", Native: id, Folder: folder})
		if e := os.WriteFile(receiptPath, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	chosen := liveAgentSession{provider: "claude", session: id, cwd: folder}
	save()
	noFolder := chosen
	noFolder.cwd = ""
	if handled, e := returnTerminalChat(context.Background(), o, noFolder); !handled || e == nil {
		t.Fatal("unknown cwd accepted")
	}
	if calls != 0 {
		t.Fatal("returned before proving exact folder")
	}
	if handled, e := returnTerminalChat(context.Background(), o, chosen); !handled || e != nil {
		t.Fatalf("own return: %v %v", handled, e)
	}
	if _, e := os.Stat(receiptPath); !os.IsNotExist(e) {
		t.Fatal("successful return kept intercepting attach")
	}
	if calls != 1 {
		t.Fatal("own return did not reach endpoint")
	}
	save()
	out = false
	if handled, e := returnTerminalChat(context.Background(), o, chosen); handled || e != nil {
		t.Fatal("recovered terminal blocked normal new-chat attach", handled, e)
	}
	if _, e := os.Stat(receiptPath); !os.IsNotExist(e) {
		t.Fatal("superseded receipt was not removed")
	}
}
