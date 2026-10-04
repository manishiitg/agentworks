package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestRegistry(t *testing.T) *projectOwnerRegistry {
	t.Helper()
	return &projectOwnerRegistry{dir: filepath.Join(t.TempDir(), projectOwnerRegistryDir)}
}

func TestProjectOwnerRegistryRegisterLookupConflictAndAliases(t *testing.T) {
	r := newTestRegistry(t)
	if _, ok := r.Lookup("work", "sde-1a2b"); ok {
		t.Fatal("empty registry found something")
	}
	if err := r.Register(projectOwnerRecord{Product: "Work", Folder: "sde-1a2b", OwnerID: "alice", ProjectID: "p1"}); err != nil {
		t.Fatal(err)
	}
	rec, ok := r.Lookup("work", "sde-1a2b")
	if !ok || rec.OwnerID != "alice" || rec.ProjectID != "p1" || rec.CreatedAt == "" {
		t.Fatalf("record = %+v ok=%v", rec, ok)
	}
	// Never another owner, and the refusal changes nothing.
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "sde-1a2b", OwnerID: "bob"}); !errors.Is(err, errProjectOwnerConflict) {
		t.Fatalf("conflicting registration: %v", err)
	}
	if rec, _ := r.Lookup("work", "sde-1a2b"); rec.OwnerID != "alice" {
		t.Fatalf("owner changed to %q", rec.OwnerID)
	}
	// Idempotent for the same owner; merges aliases and the shared flag; keeps the creation time.
	created := rec.CreatedAt
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "sde-1a2b", OwnerID: "alice", Shared: true, Aliases: []string{"_users/alice/Chats/Work/projects/sde-1a2b"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "sde-1a2b", OwnerID: "alice", Aliases: []string{"_users/alice/Chats/Work/projects/sde-1a2b"}}); err != nil {
		t.Fatal(err)
	}
	rec, _ = r.Lookup("work", "sde-1a2b")
	if !rec.Shared || len(rec.Aliases) != 1 || rec.CreatedAt != created {
		t.Fatalf("merged record = %+v", rec)
	}
	if err := r.SetShared("work", "sde-1a2b", false); err != nil {
		t.Fatal(err)
	}
	if rec, _ := r.Lookup("work", "sde-1a2b"); rec.Shared || len(rec.Aliases) != 1 {
		t.Fatalf("rollback record = %+v", rec)
	}
	if err := r.SetShared("work", "nope", true); err == nil {
		t.Fatal("SetShared on an unregistered project succeeded")
	}
	// Invalid registrations are refused.
	for _, bad := range []projectOwnerRecord{{Product: "work", Folder: "", OwnerID: "a"}, {Product: "work", Folder: "a/b", OwnerID: "a"}, {Product: "work", Folder: ".hidden", OwnerID: "a"}, {Product: "work", Folder: "x"}, {Folder: "x", OwnerID: "a"}} {
		if err := r.Register(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := r.Remove("work", "sde-1a2b"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Lookup("work", "sde-1a2b"); ok {
		t.Fatal("removed project still registered")
	}
}

func TestProjectOwnerRegistryFilesAreServerPrivate(t *testing.T) {
	r := newTestRegistry(t)
	if err := r.Register(projectOwnerRecord{Product: "code", Folder: "app-1", OwnerID: "alice"}); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(r.dir)
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("registry dir mode = %v err=%v", dirInfo.Mode(), err)
	}
	fileInfo, err := os.Stat(r.path())
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("registry file mode = %v err=%v", fileInfo.Mode(), err)
	}
	if left, _ := filepath.Glob(filepath.Join(r.dir, ".projects-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// The registry lives under the state root, never under the docs root: the workspace proxy and a CLI's folder
// guard cannot reach it. (The default location is the state root's ownership/ folder.)
func TestDefaultProjectOwnersLiveInTheStateAreaNotTheDocsRoot(t *testing.T) {
	state, docs := t.TempDir(), t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "w-1", OwnerID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "ownership", "projects.json")); err != nil {
		t.Fatalf("registry not in the state area: %v", err)
	}
	if entries, _ := os.ReadDir(docs); len(entries) != 0 {
		t.Fatalf("something was written under the docs root: %v", entries)
	}
}

// A symlink where the registry file or its lock should be is never written through (PLAT-450 applies to the
// server's own writers too).
func TestProjectOwnerRegistryNeverWritesThroughASymlink(t *testing.T) {
	r := newTestRegistry(t)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlinked registry file is refused for reading (not regular) ...
	if err := os.Symlink(victim, r.path()); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "w-1", OwnerID: "alice"}); err == nil {
		t.Fatal("registered through a symlinked registry file")
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "victim" {
		t.Fatalf("the victim file was written: %q", raw)
	}
	_ = os.Remove(r.path())
	_ = os.Remove(filepath.Join(r.dir, ".lock"))
	// ... and a symlinked lock file is refused.
	if err := os.Symlink(victim, filepath.Join(r.dir, ".lock")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "w-1", OwnerID: "alice"}); err == nil {
		t.Fatal("locked through a symlink")
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "victim" {
		t.Fatalf("the victim file was written: %q", raw)
	}
}

func TestProjectOwnerRegistryNeverRewritesACorruptFile(t *testing.T) {
	r := newTestRegistry(t)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "w-1", OwnerID: "alice"}); err == nil {
		t.Fatal("rewrote a corrupt registry")
	}
	if raw, _ := os.ReadFile(r.path()); string(raw) != "{not json" {
		t.Fatalf("corrupt registry was replaced: %q", raw)
	}
	// Lookups fall back (nothing found), they do not panic or invent an owner.
	if _, ok := r.Lookup("work", "w-1"); ok {
		t.Fatal("found an owner in a corrupt registry")
	}
}

// The server and the migration command write the same file from different processes: writers are serialized by a
// file lock (two registry values over one directory stand in for two processes), and a reader sees a write made by
// the other without a restart.
func TestProjectOwnerRegistryConcurrentWritersAndFreshReads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), projectOwnerRegistryDir)
	server, migration := &projectOwnerRegistry{dir: dir}, &projectOwnerRegistry{dir: dir}
	if err := server.Register(projectOwnerRecord{Product: "work", Folder: "first", OwnerID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := server.Lookup("work", "first"); !ok { // warm the server's cache
		t.Fatal("first not found")
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer := server
			if i%2 == 1 {
				writer = migration
			}
			if err := writer.Register(projectOwnerRecord{Product: "work", Folder: fmt.Sprintf("crew-%02d", i), OwnerID: "alice"}); err != nil {
				t.Errorf("register %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	all, err := server.All()
	if err != nil || len(all) != 41 {
		t.Fatalf("registry holds %d entries (err %v), want 41: a concurrent write was lost", len(all), err)
	}
	// A write by the other process shows through the cache.
	if err := migration.Register(projectOwnerRecord{Product: "work", Folder: "late", OwnerID: "bob"}); err != nil {
		t.Fatal(err)
	}
	if rec, ok := server.Lookup("work", "late"); !ok || rec.OwnerID != "bob" {
		t.Fatalf("server did not see the migration's write: %+v ok=%v", rec, ok)
	}
}

func TestProjectOwnerRegistryWithoutAStateAreaFindsNothingAndRefusesWrites(t *testing.T) {
	r := &projectOwnerRegistry{}
	if _, ok := r.Lookup("work", "x"); ok {
		t.Fatal("found an entry without a state area")
	}
	if err := r.Register(projectOwnerRecord{Product: "work", Folder: "x", OwnerID: "alice"}); err == nil {
		t.Fatal("registered without a state area")
	}
}
