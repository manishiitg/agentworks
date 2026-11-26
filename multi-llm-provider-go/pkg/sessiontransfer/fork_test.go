package sessiontransfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746: importing a native chat must not share a writer with the terminal.
func TestForkJSONLKeepsOriginalAndSeparatesNativeIdentities(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	const child = "22222222-2222-4222-8222-222222222222"
	for _, provider := range []string{"claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			store, cwd := t.TempDir(), t.TempDir()
			name := "projects/" + ClaudeSlug(cwd) + "/" + id + ".jsonl"
			body := fmt.Sprintf("{\"type\":\"user\",\"sessionId\":%q,\"cwd\":%q,\"message\":{\"content\":%q}}\n", id, cwd, id)
			transport := "sdk"
			if provider == "codex-cli" {
				transport = "app-server"
				name = "sessions/2026/11/05/rollout-2026-11-05T00-00-00-" + id + ".jsonl"
				body = fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q,\"source\":{\"subagent\":{\"thread_spawn\":{\"parent_thread_id\":\"33333333-3333-4333-8333-333333333333\"}}}}}\n{\"type\":\"response_item\",\"payload\":{\"content\":[{\"text\":%q}]}}\n", id, cwd, id)
				childPath, err := Path(store, "sessions/2026/11/05/rollout-2026-11-05T00-00-00-"+child+".jsonl", true)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(childPath, []byte(fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q,\"source\":{\"subagent\":{\"thread_spawn\":{\"parent_thread_id\":%q}}}}}\n", child, cwd, id)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path, err := Path(store, name, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			source, err := ExportJSONL(provider, transport, store, cwd, id, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			fork, err := ForkJSONL(source, t.TempDir(), cwd)
			if err != nil {
				t.Fatal(err)
			}
			if fork.SessionID == id || source.SessionID != id {
				t.Fatal("fork shared source identity")
			}
			if err := ImportJSONL(store, cwd, fork); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil || string(original) != body {
				t.Fatal("fork overwrote original")
			}
			copied, err := os.ReadFile(filepath.Join(fork.Root, "session.jsonl"))
			if err != nil || !bytes.Contains(copied, []byte(id)) {
				t.Fatal("fork changed message text")
			}
			if provider == "codex-cli" {
				var row struct {
					Payload codexMeta `json:"payload"`
				}
				line := bytes.SplitN(copied, []byte("\n"), 2)[0]
				if err := json.Unmarshal(line, &row); err != nil {
					t.Fatal(err)
				}
				if codexParent(row.Payload) != "" {
					t.Fatal("fork root remains a child of the original conversation")
				}
			}
			for name := range fork.Files {
				if name == "children/"+child+".jsonl" {
					t.Fatal("fork shared child identity")
				}
			}
		})
	}
}
