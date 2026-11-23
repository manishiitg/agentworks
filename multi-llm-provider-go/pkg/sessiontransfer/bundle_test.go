package sessiontransfer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-856-442: a native import must keep the exact conversation, refuse
// foreign IDs/links, and preserve both copies when the offline branch differs.
func TestNativeJournalTransferRejectsForeignIDsLinksAndDivergence(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			id := "11111111-1111-4111-8111-111111111111"
			source, destination := t.TempDir(), t.TempDir()
			work := t.TempDir()
			targetWork := t.TempDir()
			var relative string
			var row []byte
			transport := "sdk"
			if provider == "claude-code" {
				relative = "projects/" + ClaudeSlug(work) + "/" + id + ".jsonl"
				row, _ = json.Marshal(map[string]any{"sessionId": id, "cwd": work, "message": "public test"})
			} else {
				transport = "app-server"
				relative = "sessions/2026/11/01/rollout-2026-11-01T00-00-00-" + id + ".jsonl"
				row, _ = json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": id, "cwd": work}})
			}
			row = append(row, '\n')
			p := filepath.Join(source, filepath.FromSlash(relative))
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, row, 0600); e != nil {
				t.Fatal(e)
			}
			bundle, e := ExportJSONL(provider, transport, source, work, id, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if e = ImportJSONL(destination, targetWork, bundle); e != nil {
				t.Fatal(e)
			}
			returned, e := ExportJSONL(provider, transport, destination, targetWork, id, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if returned.SessionID != id {
				t.Fatal("native identity changed")
			}
			if e = Ancestor(bundle, returned); e != nil {
				t.Fatal(e)
			}
			foreign := *bundle
			foreign.SessionID = "22222222-2222-4222-8222-222222222222"
			if e = ImportJSONL(t.TempDir(), targetWork, &foreign); e == nil {
				t.Fatal("foreign journal identity accepted")
			}
			linked := *bundle
			linked.Root = t.TempDir()
			if e = os.Symlink(p, filepath.Join(linked.Root, "session.jsonl")); e != nil {
				t.Fatal(e)
			}
			if e = linked.ValidateFiles(); e == nil {
				t.Fatal("native file link accepted")
			}
			// Install a local append, then try an independently appended remote copy.
			local := *returned
			local.Root = t.TempDir()
			local.Files = map[string]File{}
			remote := *bundle
			remote.Root = t.TempDir()
			remote.Files = map[string]File{}
			for _, branch := range []struct {
				bundle *Bundle
				text   string
			}{{&local, "local"}, {&remote, "remote"}} {
				content := append(append([]byte{}, row...), []byte(`{"branch":"`+branch.text+`"}`+"\n")...)
				file := filepath.Join(branch.bundle.Root, "session.jsonl")
				if e = os.WriteFile(file, content, 0600); e != nil {
					t.Fatal(e)
				}
				branch.bundle.Files["session.jsonl"], e = Inspect(file)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = ImportJSONL(destination, targetWork, &local); e != nil {
				t.Fatal(e)
			}
			if e = ImportJSONL(destination, targetWork, &remote); e == nil || !errors.Is(e, ErrDiverged) {
				t.Fatalf("fork accepted or wrong refusal: %v", e)
			}
			preserved, e := ExportJSONL(provider, transport, destination, targetWork, id, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if preserved.Files["session.jsonl"] != local.Files["session.jsonl"] {
				t.Fatal("local branch overwritten")
			}
		})
	}
}

// The same installation limit applies to descriptors as journal readers and
// the transport; a peer cannot send a larger file by changing its metadata.
func TestNativeCapacityBoundsInstallationConfigurationAndPayload(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int64
	}{{"", 2 << 30}, {"bad", 2 << 30}, {"1048575", 2 << 30}, {"34359738369", 2 << 30}, {"1048576", 1 << 20}, {"34359738368", 32 << 30}} {
		t.Setenv("AGENTWORKS_NATIVE_SYNC_MAX_BYTES", tc.env)
		if MaxBytes() != tc.want {
			t.Fatalf("unexpected capacity for %q", tc.env)
		}
		b := Bundle{Provider: "claude-code", Transport: "sdk", Format: "claude-jsonl", SessionID: "11111111-1111-4111-8111-111111111111", Workspace: t.TempDir(), Files: map[string]File{"session.jsonl": {Size: tc.want, SHA256: strings.Repeat("0", 64)}}}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		b.Files["session.jsonl"] = File{Size: tc.want + 1, SHA256: strings.Repeat("0", 64)}
		if err := b.Validate(); err == nil {
			t.Fatal("peer payload exceeded installation capacity")
		}
	}
}
