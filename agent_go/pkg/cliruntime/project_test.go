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

// PLAT-442 step 4: a project that moved inside the workspace keeps the runtime folder (hence the native CLI session)
// it always had, and the runtime's link follows it; without the legacy path the move would start a fresh runtime.
func TestPrepareLinkedProjectMovedKeepsTheRuntimeFolder(t *testing.T) {
	state, docs := t.TempDir(), t.TempDir()
	oldRel := "_users/alice/Chats/Work/projects/sde-1a2b"
	newRel := "Crew/sde-1a2b"
	oldDir := filepath.Join(docs, filepath.FromSlash(oldRel))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := PrepareLinkedProject(state, docs, "alice", oldDir, "chat-1", "claude-code", "builder")
	if err != nil {
		t.Fatal(err)
	}
	// The project moves.
	newDir := filepath.Join(docs, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	// An ordinary launch of the moved project is a different runtime (the digest names the path).
	fresh, err := PrepareLinkedProject(state, docs, "alice", newDir, "chat-1", "claude-code", "builder")
	if err != nil || fresh == before {
		t.Fatalf("fresh runtime = %q (before %q) err=%v; the digest was expected to follow the path", fresh, before, err)
	}
	// With the legacy path the runtime is the same, and its link was repointed to the project's new place.
	after, err := PrepareLinkedProjectMoved(state, docs, "alice", newDir, oldRel, "chat-1", "claude-code", "builder")
	if err != nil || after != before {
		t.Fatalf("moved runtime = %q (before %q) err=%v", after, before, err)
	}
	resolvedNew, _ := filepath.EvalSymlinks(newDir)
	if saved, _ := os.Readlink(filepath.Join(after, ProjectLink)); saved != resolvedNew {
		t.Fatalf("the runtime link points at %q, want %q", saved, resolvedNew)
	}
	// And it keeps working: a second launch is the same runtime with the same link.
	if again, err := PrepareLinkedProjectMoved(state, docs, "alice", newDir, oldRel, "chat-1", "claude-code", "builder"); err != nil || again != before {
		t.Fatalf("second launch = %q err=%v", again, err)
	}
	// Another session, user, provider or mode is another runtime, as before.
	if other, err := PrepareLinkedProjectMoved(state, docs, "alice", newDir, oldRel, "chat-2", "claude-code", "builder"); err != nil || other == before {
		t.Fatalf("another session shares the runtime: %q err=%v", other, err)
	}
}

// Only the exact old target is repointed: a link that points anywhere else still fails the launch.
func TestPrepareLinkedProjectMovedRefusesAForeignLink(t *testing.T) {
	state, docs, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	oldRel := "_users/alice/Chats/Work/projects/p"
	oldDir := filepath.Join(docs, filepath.FromSlash(oldRel))
	newDir := filepath.Join(docs, "Crew", "p")
	for _, d := range []string{oldDir, newDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := PrepareLinkedProject(state, docs, "alice", oldDir, "s", "claude-code", "builder")
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ProjectLink)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareLinkedProjectMoved(state, docs, "alice", newDir, oldRel, "s", "claude-code", "builder"); err == nil {
		t.Fatal("a link to somewhere else was repointed")
	}
	if saved, _ := os.Readlink(link); saved != elsewhere {
		t.Fatalf("the foreign link was changed to %q", saved)
	}
}

func TestRepointProjectLinks(t *testing.T) {
	state, docs := t.TempDir(), t.TempDir()
	oldDir := filepath.Join(docs, "_users", "alice", "Chats", "Work", "projects", "p")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := PrepareLinkedProject(state, docs, "alice", oldDir, "s1", "claude-code", "builder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareLinkedProject(state, docs, "alice", oldDir, "s2", "codex", "run"); err != nil {
		t.Fatal(err)
	}
	resolvedOld, _ := filepath.EvalSymlinks(oldDir)
	runtimes, err := ProjectLinksTo(state, resolvedOld)
	if err != nil || len(runtimes) != 2 {
		t.Fatalf("runtimes linking the project = %v err=%v", runtimes, err)
	}
	n, err := RepointProjectLinks(state, resolvedOld, "/new/place")
	if err != nil || n != 2 {
		t.Fatalf("repointed %d err=%v", n, err)
	}
	if saved, _ := os.Readlink(filepath.Join(a, ProjectLink)); saved != "/new/place" {
		t.Fatalf("link = %q", saved)
	}
	if n, _ := RepointProjectLinks(state, resolvedOld, "/x"); n != 0 {
		t.Fatalf("repointed %d links that no longer match", n)
	}
}
