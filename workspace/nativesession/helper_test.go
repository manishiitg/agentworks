package nativesession

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-856-498: the assigned owner can move the selected native session, while
// another owner, a linked journal and changed bytes cannot cross the boundary.
func TestAssignedNativeSessionStreamsOnlyItsJournalAndRefusesForeignPaths(t *testing.T) {
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	table := filepath.Join(root, "slots.json")
	if err := os.WriteFile(table, []byte(`{"slots":{"slot01":"owner","slot02":"other"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := slots.ExecConfig{SlotStateRoot: root, SlotTable: table, NativeOwners: map[string]string{"slot01": "owner", "slot02": "other"}}
	req := slots.NativeSessionRequest{OwnerID: "owner", RuntimeID: strings.Repeat("1", 64), SessionID: "11111111-1111-4111-8111-111111111111", Operation: "export", Transport: "sdk"}
	// The private master table is unavailable to the assigned account. The
	// helper must rely on the root-owned readable config projection instead.
	if err := os.Remove(table); err != nil {
		t.Fatal(err)
	}
	runtime, err := req.NativeRuntime(cfg, "slot01")
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(runtime, ".sandbox-cache/cli-home/claude-code/.claude")
	path := filepath.Join(store, "projects", sessiontransfer.ClaudeSlug(runtime), req.SessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	row, _ := json.Marshal(map[string]string{"sessionId": req.SessionID, "cwd": runtime, "message": "remember selected journal"})
	row = append(row, '\n')
	if err := os.WriteFile(path, row, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "credentials.json"), []byte("PRIVATE_ACCOUNT_MARKER"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(r slots.NativeSessionRequest, b *sessiontransfer.Bundle) (*bytes.Buffer, error) {
		var in, out bytes.Buffer
		json.NewEncoder(&in).Encode(r)
		if b != nil {
			if err := WriteFiles(&in, b); err != nil {
				return nil, err
			}
		}
		err := Run(&in, &out, cfg, "slot01")
		return &out, err
	}
	out, err := run(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte("PRIVATE_ACCOUNT_MARKER")) {
		t.Fatal("export included account configuration")
	}
	b, err := ReadBundle(out, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if b.SessionID != req.SessionID || len(b.Files) != 1 {
		t.Fatalf("wrong journal set: %+v", b)
	}
	foreign := req
	foreign.OwnerID = "other"
	if _, err := run(foreign, nil); err == nil {
		t.Fatal("another owner's native session accepted")
	}
	bad := req
	bad.RuntimeID = "../outside"
	if _, err := run(bad, nil); err == nil {
		t.Fatal("runtime traversal accepted")
	}
	next := req
	next.RuntimeID = strings.Repeat("2", 64)
	next.Operation = "import"
	next.Bundle, _ = json.Marshal(b)
	target, err := next.NativeRuntime(cfg, "slot01")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(next, b); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(target, ".sandbox-cache/cli-home/claude-code/.claude/projects", sessiontransfer.ClaudeSlug(target), req.SessionID+".jsonl")
	got, err := os.ReadFile(copied)
	if err != nil || !bytes.Contains(got, []byte("remember selected journal")) {
		t.Fatalf("native import missing: %s %v", got, err)
	}
	// A newer local journal is a fork, never overwritten by the older import.
	if err := os.WriteFile(copied, append(got, []byte(`{"sessionId":"`+req.SessionID+`","message":"local branch"}`+"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(next, b); err == nil {
		t.Fatal("older import overwrote a changed local journal")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copied, path); err != nil {
		t.Fatal(err)
	}
	if _, err := run(req, nil); err == nil {
		t.Fatal("journal symlink accepted")
	}
	var answer bytes.Buffer
	if err := Run(strings.NewReader(`{"owner_id":"owner","unexpected":true}`+"\n"), &answer, cfg, "slot01"); err == nil {
		t.Fatal("unknown native header field accepted")
	}
}
