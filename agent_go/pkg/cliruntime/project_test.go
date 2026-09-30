package cliruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkedProjectKeepsDataSharedAndRuntimePrivate(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "docs")
	project := filepath.Join(workspace, "Crew", "one")
	if err := os.MkdirAll(filepath.Join(project, "code"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "AGENTS.md"), []byte("user instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	prepare := func(user, session, provider, mode string) string {
		t.Helper()
		dir, err := PrepareLinkedProject(filepath.Join(root, "state"), workspace, user, project, session, provider, mode)
		if err != nil {
			t.Fatal(err)
		}
		return dir
	}
	builder := prepare("owner", "chat", "codex-cli", "builder")
	run := prepare("owner", "chat", "codex-cli", "run")
	if builder == run || CanResume(builder, run) {
		t.Fatal("modes share a runtime or continuation")
	}
	for _, values := range [][4]string{{"reader", "chat", "codex-cli", "run"}, {"owner", "other", "codex-cli", "builder"}, {"owner", "chat", "claude-code", "builder"}} {
		if dir := prepare(values[0], values[1], values[2], values[3]); dir == builder || dir == run {
			t.Fatal("different user/chat/provider shares runtime")
		}
	}
	if again := prepare("owner", "chat", "codex-cli", "builder"); again != builder {
		t.Fatal("restart changes runtime identity")
	}
	for dir, prompt := range map[string]string{builder: "Builder prompt", run: "Run prompt"} {
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(prompt), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	linkedFile := filepath.Join(builder, ProjectLink, "code", "new.txt")
	if err := os.WriteFile(linkedFile, []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(run, ProjectLink, "code", "new.txt")); err != nil || string(got) != "shared" {
		t.Fatalf("linked create missing in Run: %q, %v", got, err)
	}
	if err := os.Rename(linkedFile, filepath.Join(builder, ProjectLink, "code", "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(builder, ProjectLink, "code", "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "code", "renamed.txt")); !os.IsNotExist(err) {
		t.Fatalf("linked delete did not reach real project: %v", err)
	}
	// Cleanup of one private runtime unlinks its project alias; it must never
	// delete real data, user instructions, or the other mode's projection.
	if err := os.RemoveAll(builder); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{filepath.Join(project, "AGENTS.md"): "user instructions", filepath.Join(run, "AGENTS.md"): "Run prompt"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("runtime cleanup changed %s: %q, %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Fatal("generated skills entered the real project")
	}
}

func TestLinkedProjectRefusesRepointedLinkAndObstructions(t *testing.T) {
	for _, obstruction := range []string{"link", "file", "directory"} {
		t.Run(obstruction, func(t *testing.T) {
			root := t.TempDir()
			workspace := filepath.Join(root, "docs")
			project := filepath.Join(workspace, "Crew", "one")
			if err := os.MkdirAll(project, 0700); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(root, "state")
			dir, err := Prepare(state, workspace, "owner", project, "chat", "codex-cli", "run")
			if err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, ProjectLink)
			switch obstruction {
			case "link":
				err = os.Symlink(root, link)
			case "file":
				err = os.WriteFile(link, []byte("keep"), 0600)
			case "directory":
				err = os.Mkdir(link, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := PrepareLinkedProject(state, workspace, "owner", project, "chat", "codex-cli", "run"); err == nil || got != "" {
				t.Fatal("unsafe link was adopted")
			}
			if _, err := os.Lstat(link); err != nil {
				t.Fatal("obstruction was deleted")
			}
		})
	}
}
