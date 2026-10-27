package crewroot

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// PLAT-856-383: only platform records move. Existing Git worktrees and laptop
// roots must keep their real paths; native grants may never reopen private state.
func TestPrivateStateMigrationPreservesGitAndNativeBoundary(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "hosted", true: "selected-folder"}[local], func(t *testing.T) {
			base := t.TempDir()
			docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
			old := filepath.Join(docs, "Crew", "example")
			files := old
			resources := old
			if local {
				files = filepath.Join(base, "selected")
				resources = filepath.Join(files, ".agentworks")
			}
			for _, p := range []string{old, resources, filepath.Join(state, "ownership")} {
				if e := os.MkdirAll(p, 0700); e != nil {
					t.Fatal(e)
				}
			}
			docs, _ = filepath.EvalSymlinks(docs)
			old = filepath.Join(docs, "Crew", "example")
			files, _ = filepath.EvalSymlinks(files)
			resources, _ = filepath.EvalSymlinks(resources)
			t.Setenv("AGENTWORKS_STATE_ROOT", state)
			t.Setenv("LOCAL_MODE", "true")
			record := Record{Product: "work", Folder: "example", Owner: "owner", ID: "stable-id"}
			if local {
				record.LocalRoot = files
			}
			raw, _ := json.Marshal(map[string]any{"projects": map[string]Record{"work/example": record}})
			if e := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			write := func(root, rel, body string) {
				t.Helper()
				p := filepath.Join(root, rel)
				if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(p, []byte(body), 0600); e != nil {
					t.Fatal(e)
				}
			}
			write(old, "product.json", "platform")
			write(old, "builder/conversation/chat.json", "private chat")
			write(resources, "db/db.sqlite", "database")
			write(resources, "db/assets/chart.csv", "person data")
			write(resources, "triggers/deliveries/event.json", "platform event")
			write(resources, "triggers/custom.js", "person code")
			write(resources, "skills/research/SKILL.md", "person skill")
			write(files, "notes.txt", "person file")
			// PLAT-856-383: old slot-created cache descendants can be unreadable.
			write(resources, ".sandbox-cache/locked/login-placeholder", "cache retained")
			lockedCache := filepath.Join(resources, ".sandbox-cache/locked")
			privateCache := filepath.Join(docs, "_system/crew-data/stable-id/.sandbox-cache/locked")
			if err := os.Chmod(lockedCache, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(lockedCache, 0700); _ = os.Chmod(privateCache, 0700) })
			if os.Geteuid() != 0 {
				protected := filepath.Join(old, "builder/conversation")
				if err := os.Chmod(protected, 0); err != nil {
					t.Fatal(err)
				}
				_, _, denied := MigratePrivateState(docs)
				_ = os.Chmod(protected, 0700)
				if !os.IsPermission(denied) {
					t.Fatalf("unreadable platform record must abort: %v", denied)
				}
			}
			git := func(dir string, args ...string) string {
				t.Helper()
				c := exec.Command("git", args...)
				c.Dir = dir
				c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				out, e := c.CombinedOutput()
				if e != nil {
					t.Fatalf("git %v: %s %v", args, out, e)
				}
				return string(out)
			}
			git(files, "init", "-q")
			git(files, "add", "notes.txt")
			git(files, "-c", "user.name=Example", "-c", "user.email=owner@example.com", "commit", "-qm", "person work")
			worktree := filepath.Join(base, "linked")
			git(files, "worktree", "add", "-qb", "linked", worktree)
			head, linked := git(files, "rev-parse", "HEAD"), git(worktree, "rev-parse", "--git-common-dir")
			count, backup, e := MigratePrivateState(docs)
			if e != nil {
				t.Fatal(e)
			}
			if count != 5 || backup == "" {
				t.Fatalf("moved=%d backup=%s", count, backup)
			}
			roots, found, e := Lookup(docs, "Crew/example")
			if e != nil || !found {
				t.Fatal(e)
			}
			for _, rel := range []string{"product.json", "builder/conversation/chat.json", "db/db.sqlite", "triggers/deliveries/event.json"} {
				p, b, e := Resolve(docs, "Crew/example/"+rel)
				if e != nil || !b || p != filepath.Join(roots.Private, rel) {
					t.Fatalf("resolve %s: %s %v", rel, p, e)
				}
				if _, e = os.Stat(p); e != nil {
					t.Fatal(e)
				}
			}
			for _, rel := range []string{"db/assets/chart.csv", "triggers/custom.js", "skills/research/SKILL.md"} {
				p, b, e := Resolve(docs, "Crew/example/"+rel)
				if e != nil || !b || p != filepath.Join(resources, rel) {
					t.Fatalf("resource %s: %s %v", rel, p, e)
				}
			}
			if git(files, "rev-parse", "HEAD") != head || git(worktree, "rev-parse", "--git-common-dir") != linked {
				t.Fatal("repository identity changed")
			}
			write(files, "new-folder/hello.txt", "folder creation works")
			git(files, "add", "new-folder/hello.txt")
			git(files, "-c", "user.name=Example", "-c", "user.email=owner@example.com", "commit", "-qm", "new file")
			write(worktree, "linked.txt", "linked edit")
			git(worktree, "add", "linked.txt")
			_, writes, _ := NativeGrants(docs, []string{"Crew/example"}, []string{"Crew/example", "Crew/example/builder", "Crew/example/db/db.sqlite", roots.Private, filepath.Join(roots.Private, "product.json"), "Crew/example/runs/current/output", roots.Files})
			want := []string{"Crew/example", "Crew/example/runs/current/output", roots.Files, filepath.Join(roots.Private, ".sandbox-cache")}
			if !reflect.DeepEqual(writes, want) {
				t.Fatalf("native writes: %v", writes)
			}
			scoped := []string{"Crew/example/runs/current/output", "Crew/example/skills/custom/SKILL.md"}
			_, narrowWrites, _ := NativeGrants(docs, []string{"Crew/example"}, scoped)
			if !reflect.DeepEqual(narrowWrites, scoped) {
				t.Fatalf("narrow grants acquired private tool cache: %v", narrowWrites)
			}
			if os.Geteuid() != 0 {
				data, err := os.ReadFile(filepath.Join(backup, "journal.json"))
				if err != nil {
					t.Fatal(err)
				}
				var journal PrivateMigration
				if err := json.Unmarshal(data, &journal); err != nil {
					t.Fatal(err)
				}
				omitted := 0
				for _, entry := range journal.Entries {
					if entry.UnbackedCache {
						omitted++
						if !isPrivateCache(entry.Source) || entry.Saved != "" {
							t.Fatal("platform backup omitted", entry)
						}
					}
				}
				if omitted != 1 {
					t.Fatalf("unreadable cache count=%d", omitted)
				}
			}
			if n, b, e := MigratePrivateState(docs); e != nil || n != 0 || b != "" {
				t.Fatalf("repeat conversion: %d %s %v", n, b, e)
			}
			if e := RestorePrivateStateBackup(backup); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(filepath.Join(old, "product.json")); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(filepath.Join(files, "new-folder/hello.txt")); e != nil {
				t.Fatal("restore touched person files", e)
			}
			if n, nextBackup, e := MigratePrivateState(docs); e != nil || n != count || nextBackup == backup {
				t.Fatalf("retry after restore: moved=%d freshBackup=%t error=%v", n, nextBackup != backup, e)
			}
		})
	}
}
