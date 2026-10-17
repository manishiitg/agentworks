package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// This exercises the actual archive exporter/importer, not a second manifest implementation.
func TestPublicationPackageRoundTrip(t *testing.T) {
	root, state, dest, history := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	files := map[string][]byte{"code/run.py": []byte("print('published')\n"), "assets/image.png": {0x89, 'P', 'N', 'G', 0, 255}, ".env": []byte("PRIVATE_VALUE=fixture\n"), ".aws/credentials": []byte("fixture"), "id_rsa": []byte("fixture"), ".claude/settings.local.json": []byte("{}"), ".git/config": []byte("private"), "node_modules/lib/index.js": []byte("generated"), ".claude/skills/answer/SKILL.md": []byte("Answer questions about invoices."), "private.txt": []byte("blocked by existing grant")}
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"source-session\"}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Build an invoice assistant. Remember CONTEXT_856145.\"}]}}\n")
	archive, m, err := publication.Build(context.Background(), root, state, publication.Source{ProjectID: "same-project-on-two-laptops", History: publication.History{Provider: "codex", SessionID: "source-session", SourceCWD: root}, Raw: raw}, func(p string) bool { return p != "private.txt" })
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 3 || !strings.Contains(m.Handoff, "CONTEXT_856145") {
		t.Fatalf("wrong exported source/summary: files=%d", len(m.Files))
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	got, err := publication.Unpack(context.Background(), f, dest, history)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != m.ProjectID {
		t.Fatal("stable project identity changed")
	}
	for _, p := range []string{"code/run.py", "assets/image.png", ".claude/skills/answer/SKILL.md"} {
		b, e := os.ReadFile(filepath.Join(dest, p))
		if e != nil || !bytes.Equal(b, files[p]) {
			t.Fatalf("source %s did not survive", p)
		}
	}
	for _, p := range []string{".env", ".aws/credentials", "id_rsa", ".claude/settings.local.json", ".git/config", "node_modules/lib/index.js", "private.txt", "raw.jsonl", "history/raw.jsonl"} {
		if _, e := os.Stat(filepath.Join(dest, p)); !os.IsNotExist(e) {
			t.Fatalf("private or generated file reached source tree: %s", p)
		}
	}
	b, err := os.ReadFile(filepath.Join(history, "raw.jsonl"))
	if err != nil || !bytes.Equal(b, raw) {
		t.Fatal("raw Builder history changed or was lost")
	}
	// A corrupt byte must prevent activation by making import fail.
	b, err = os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("print('published')"), []byte("print('corrupted')"), 1)
	if _, err = publication.Unpack(context.Background(), bytes.NewReader(b), t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("corrupt source was accepted")
	}
}
