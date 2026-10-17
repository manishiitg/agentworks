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
	files := map[string][]byte{"code/run.py": []byte("print('published')\n"), "assets/image.png": {0x89, 'P', 'N', 'G', 0, 255}, ".env": []byte("PRIVATE_VALUE=fixture\n"), ".aws/credentials": []byte("fixture"), "id_rsa": []byte("fixture"), ".claude/settings.local.json": []byte("{}"), "node_modules/lib/index.js": []byte("generated"), ".claude/skills/answer/SKILL.md": []byte("Answer questions about invoices."), "private.txt": []byte("blocked by existing grant")}
	excludedNames := []string{"config/prod.env", "app.env", "credentials", "aws/credentials", "auth.json", "client_secret_example.json", "secrets.yaml", "secret.json", "id_dsa", "key.ppk", "key.jks", "key.keystore", "key.gpg", "key.asc", "vars.tfvars", "kubeconfig", ".htpasswd", "app-credentials.toml", "app-secret.cfg"}
	for _, name := range excludedNames {
		files[name] = []byte("fixture")
	}
	for _, name := range []string{"secrets.go", "credential_helpers.ts", "code/secretary/agent.py", "id_dsa.pub"} {
		files[name] = []byte("ordinary source")
	}
	files[".gitignore"] = []byte("config/*.yaml\n!config/keep.yaml\n")
	files["config/private-values.yaml"] = []byte("fixture")
	files["config/keep.yaml"] = []byte("kept")
	for p, b := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "private-fixture.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE_HARDLINK_MARKER"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Fatalf("create hard-link fixture: %v", err)
	}
	ignored, err := publication.GitIgnored(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !ignored["config/private-values.yaml"] || ignored["config/keep.yaml"] {
		t.Fatal("plain-folder ignore rules or negation were lost")
	}
	raw := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"source-session\"}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"Build an invoice assistant. Remember CONTEXT_856145.\"}]}}\n")
	archive, m, err := publication.Build(context.Background(), root, state, publication.Source{ProjectID: "same-project-on-two-laptops", History: publication.History{Provider: "codex", SessionID: "source-session", SourceCWD: root}, Raw: raw}, func(p string) bool { return p != "private.txt" && !ignored[p] })
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 8 || !strings.Contains(m.Handoff, "CONTEXT_856145") {
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
	for _, p := range []string{"code/run.py", "assets/image.png", ".claude/skills/answer/SKILL.md", "secrets.go", "credential_helpers.ts", "code/secretary/agent.py", "id_dsa.pub", "config/keep.yaml"} {
		b, e := os.ReadFile(filepath.Join(dest, p))
		if e != nil || !bytes.Equal(b, files[p]) {
			t.Fatalf("source %s did not survive", p)
		}
	}
	excludedNames = append(excludedNames, "linked.txt", "config/private-values.yaml")
	for _, p := range excludedNames {
		if _, e := os.Stat(filepath.Join(dest, p)); !os.IsNotExist(e) {
			t.Fatalf("credential, ignored or hard-linked file reached source: %s", p)
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
	website := []byte(`{"conversation_history":[{"Role":"human","Parts":[{"Text":"Remember WEBSITE_CONTEXT_856145"}]}]}`)
	if !strings.Contains(publication.HandoffTranscript(website), "WEBSITE_CONTEXT_856145") {
		t.Fatal("website history was omitted from the private handoff")
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := publication.GitIgnored(context.Background(), root); err == nil {
		t.Fatal("broken repository silently dropped ignore rules")
	}
	if err := os.Remove(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := publication.GitIgnored(context.Background(), root); err == nil {
		t.Fatal("missing Git silently dropped ignore rules")
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
