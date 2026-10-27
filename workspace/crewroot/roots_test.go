package crewroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-856-145: selecting a folder must neither shadow its files with managed
// state nor let a file tool escape it or remove the selected root.
func TestBoundCrewPreservesProjectAndConfinesIssuedPaths(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "docs")
	project := filepath.Join(base, "project")
	state := filepath.Join(base, "state")
	for _, dir := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// macOS temp paths can have a canonical /private prefix.
	var err error
	docs, err = filepath.EvalSymlinks(docs)
	if err != nil {
		t.Fatal(err)
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "stable-id", LocalRoot: project}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "product.json"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("person's original file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{
		"Crew/example/_files":                         project,
		"Crew/example/_files/product.json":            filepath.Join(project, "product.json"),
		"Crew/example/product.json":                   filepath.Join(docs, "Crew", "example", "product.json"),
		"Crew/example/builder/conversation/chat.json": filepath.Join(docs, "Crew", "example", "builder", "conversation", "chat.json"),
		"Crew/example/skills/research/SKILL.md":       filepath.Join(project, ".agentworks", "skills", "research", "SKILL.md"),
		"Crew/example/db/db.sqlite":                   filepath.Join(project, ".agentworks", "db", "db.sqlite"),
	}
	for virtual, want := range cases {
		got, bound, err := Resolve(docs, virtual)
		if err != nil || !bound || got != want {
			t.Fatalf("%s: %q bound=%v err=%v", virtual, got, bound, err)
		}
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, virtual := range []string{"Crew/example/_files/../outside", "Crew/example/_files/escape/secret.txt"} {
		if _, _, err := Resolve(docs, virtual); err == nil {
			t.Fatalf("accepted escape %s", virtual)
		}
	}
	if _, bound, err := Resolve(docs, filepath.Join(project, "AGENTS.md")); bound || err != nil {
		t.Fatal("absolute input became an issued Crew path")
	}
	for _, root := range []string{project, filepath.Join(project, ".agentworks"), filepath.Join(docs, "Crew", "example")} {
		if err := CheckRootMutation(docs, root); err == nil {
			t.Fatalf("root mutation allowed: %s", root)
		}
	}
	if err := CheckRootMutation(docs, filepath.Join(project, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "product.json"} {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil || string(data) != "person's original file" {
			t.Fatalf("original file changed: %s", name)
		}
	}
	t.Setenv("LOCAL_MODE", "false")
	if _, bound, err := Resolve(docs, "Crew/example/_files"); bound || err != nil {
		t.Fatal("hosted mode admitted local binding")
	}
}
