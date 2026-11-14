package chatterminal

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-569: one owner's selected session must not become a provider-home
// archive, a path traversal or an unverified/truncated journal at the receiver.
func TestTerminalTransferChecksExactJournalAndRejectsExtraFiles(t *testing.T) {
	root := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	raw := []byte(`{"type":"user","sessionId":"` + id + `","message":{"role":"user","content":"Question"}}` + "\n")
	h := sha256.Sum256(raw)
	if e := os.WriteFile(filepath.Join(root, "session.jsonl"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	b := &sessiontransfer.Bundle{Provider: "claude-code", Format: "claude-jsonl", Transport: "structured", SessionID: id, Workspace: root, Root: root, Files: map[string]sessiontransfer.File{"session.jsonl": {Size: int64(len(raw)), SHA256: hex.EncodeToString(h[:])}}}
	transfer := Transfer{Receipt: "receipt", Session: "chat", Bundle: b}
	var encoded bytes.Buffer
	if e := Write(&encoded, transfer); e != nil {
		t.Fatal(e)
	}
	got, e := Read(bytes.NewReader(encoded.Bytes()), t.TempDir())
	if e != nil || got.Bundle.SessionID != id {
		t.Fatalf("valid transfer: %+v %v", got, e)
	}
	corrupt := bytes.Replace(encoded.Bytes(), []byte("Question"), []byte("Tampered"), 1)
	if _, e = Read(bytes.NewReader(corrupt), t.TempDir()); e == nil {
		t.Fatal("corrupt journal accepted")
	}
	for _, name := range []string{"../credentials.json", "account.json", "session.jsonl"} {
		var bad bytes.Buffer
		tw := tar.NewWriter(&bad)
		descriptor, _ := json.Marshal(transfer)
		for _, f := range []struct {
			name string
			data []byte
		}{{"handoff.json", descriptor}, {"session.jsonl", raw}, {name, []byte("not session data")}} {
			if e = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0600, Size: int64(len(f.data))}); e != nil {
				t.Fatal(e)
			}
			_, _ = tw.Write(f.data)
		}
		_ = tw.Close()
		if _, e = Read(&bad, t.TempDir()); e == nil {
			t.Fatalf("extra %s accepted", name)
		}
	}
}
