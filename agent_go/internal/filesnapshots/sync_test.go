package filesnapshots

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// PLAT-856-440: bundles must preserve byte-exact content and external edits;
// sync refs must outlive reply pruning without exporting private reply history.
func TestSyncBundlesPreserveReplicaEditsAndUndoHistory(t *testing.T) {
	roots := func() crewroot.Roots {
		r := crewroot.Roots{Files: t.TempDir(), Resources: t.TempDir(), Private: t.TempDir()}
		return r
	}
	a, b := roots(), roots()
	sa := Store{Root: a.Files, Private: a.Private}
	sb := Store{Root: b.Files, Private: b.Private}
	write := func(root, name, value string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(a.Files, "note.txt", "baseline")
	write(a.Files, ".env", "private")
	// A private turn contains content that is absent from the sync tree.
	if _, err := sa.Warm(time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Begin(t.Context(), "turn"); err != nil {
		t.Fatal(err)
	}
	write(a.Files, "note.txt", "edited")
	if _, err := sa.Finish(t.Context(), "turn"); err != nil {
		t.Fatal(err)
	}
	capture := func(r crewroot.Roots, s Store, previous crewsync.Manifest) crewsync.Snapshot {
		t.Helper()
		snap, err := crewsync.Capture(t.Context(), r, previous)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.EncodeSync(t.Context(), "pair", &snap); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		var wire crewsync.Snapshot
		if err = json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		return wire
	}
	first := capture(a, sa, nil)
	if err := sb.DecodeSync(t.Context(), "pair", &first); err != nil {
		t.Fatal(err)
	}
	if err := crewsync.Apply(t.Context(), b, first); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Store{sa, sb} {
		if err := s.AckSync(t.Context(), "pair", first.Content); err != nil {
			t.Fatal(err)
		}
	}
	write(b.Files, "note.txt", "server edit")
	back := capture(b, sb, first.Files)
	if err := sa.DecodeSync(t.Context(), "pair", &back); err != nil {
		t.Fatal(err)
	}
	write(a.Files, "note.txt", "outside edit")
	if err := crewsync.Apply(t.Context(), a, back); err == nil {
		t.Fatal("overwrote conflicting replica edit")
	}
	write(a.Files, "note.txt", "edited")
	if err := crewsync.Apply(t.Context(), a, back); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Store{sa, sb} {
		if err := s.AckSync(t.Context(), "pair", back.Content); err != nil {
			t.Fatal(err)
		}
	}
	// Retry after local ack but before receipt delivery still has prerequisites.
	if err := sa.DecodeSync(t.Context(), "pair", &back); err != nil {
		t.Fatal(err)
	}
	sa.prune(t.Context())
	if err := sa.command(t.Context(), "gc", "--prune=now").Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.syncBase(t.Context(), "pair"); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Compare(t.Context(), ID("turn"), "note.txt", func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if raw, err := sa.command(t.Context(), "for-each-ref", "--format=%(refname)", "refs/sync").Output(); err != nil || !strings.Contains(string(raw), "/baseline") {
		t.Fatalf("sync baseline pruned: %s %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(b.Files, ".env")); !os.IsNotExist(err) {
		t.Fatal("private file crossed sync")
	}
}

// PLAT-856-440: Git trees are untrusted; reject links and case collisions before
// rooted Apply, even when Git considers the bundle structurally valid.
func TestSyncBundleRejectsLinksAndCaseCollisions(t *testing.T) {
	for _, mode := range []string{"120000", "160000", "case"} {
		t.Run(mode, func(t *testing.T) {
			a := Store{Root: t.TempDir(), Private: t.TempDir()}
			b := Store{Root: t.TempDir(), Private: t.TempDir()}
			base, err := a.syncBase(t.Context(), "pair")
			if err != nil {
				t.Fatal(err)
			}
			cmd := a.command(t.Context(), "hash-object", "-w", "--stdin")
			cmd.Stdin = strings.NewReader("outside")
			blob, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			oid := strings.TrimSpace(string(blob))
			entryMode, kind := mode, "blob"
			if mode == "160000" {
				kind = "commit"
				oid = base
			}
			if mode == "case" {
				entryMode = "100644"
			}
			input := entryMode + " " + kind + " " + oid + "\tnote.txt\n"
			if mode == "case" {
				input += "100644 blob " + oid + "\tNOTE.txt\n"
			}
			cmd = a.command(t.Context(), "mktree")
			cmd.Stdin = strings.NewReader(input)
			tree, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			cmd = a.command(t.Context(), "mktree")
			cmd.Stdin = strings.NewReader("040000 tree " + strings.TrimSpace(string(tree)) + "\tfiles\n")
			top, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			cmd = a.command(t.Context(), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit-tree", strings.TrimSpace(string(top)), "-p", base, "-m", "sync")
			head, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			h := strings.TrimSpace(string(head))
			if err = a.command(t.Context(), "update-ref", syncRef("pair", "pending"), h).Run(); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "test.bundle")
			if err = a.command(t.Context(), "bundle", "create", file, syncRef("pair", "pending"), "^"+base).Run(); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(file)
			snap := crewsync.Snapshot{Content: &crewsync.ContentSnapshot{Base: base, Head: h, Bundle: data}, Files: crewsync.Manifest{"files/note.txt": {}, "files/NOTE.txt": {}}}
			if err = b.DecodeSync(t.Context(), "pair", &snap); err == nil {
				t.Fatal("accepted unsafe tree")
			}
		})
	}
}
