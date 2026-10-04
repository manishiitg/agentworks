package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// ---- a temp docs tree and state root for the Crew move ----

type crewMoveFixture struct {
	t      *testing.T
	Docs   string
	State  string
	Backup string
	Out    *bytes.Buffer
	opts   crewMoveOptions
}

const (
	cmCrewA = "sde-aaaa1111" // alice
	cmCrewB = "ops-bbbb2222" // alice
	cmCrewC = "mkt-cccc3333" // bob
	cmCode  = "app-dddd4444" // carol's Code: never moved
)

func newCrewMoveFixture(t *testing.T) *crewMoveFixture {
	t.Helper()
	root := t.TempDir()
	f := &crewMoveFixture{t: t, Docs: filepath.Join(root, "docs"), State: filepath.Join(root, "state"), Backup: filepath.Join(root, "backup"), Out: &bytes.Buffer{}}
	for _, d := range []string{f.Docs, f.State} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", f.State)
	t.Setenv("WORKSPACE_DOCS_PATH", f.Docs)
	t.Setenv("MULTI_USER_MODE", "true")
	resetCrewLocationCaches()
	t.Cleanup(resetCrewLocationCaches)
	f.opts = crewMoveOptions{
		DocsRoot: f.Docs, StateRoot: f.State, BackupDir: f.Backup, Out: f.Out,
		Probe:     func(context.Context, string, map[string]string) (map[string][]string, error) { return nil, nil },
		FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
		Readers:   func() []string { return []string{"alice", "bob"} },
		Now:       func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
	}
	f.crew("alice", cmCrewA, "c-a")
	f.crew("alice", cmCrewB, "c-b")
	f.crew("bob", cmCrewC, "c-c")
	// A Code (never moved) and a Goal that attaches a Crew by its old path.
	f.write(fmt.Sprintf("_users/carol/Chats/Code/projects/%s/product.json", cmCode), `{"schema_version":1,"product":"code","id":"code-1","title":"App","session_id":"code:project:code-1"}`, 0o660)
	f.write("Workflow/g1/workflow.json", `{"id":"g1","label":"G","workflow_context_paths":["_users/alice/Chats/Work/projects/`+cmCrewA+`","Chats/Work/projects/`+cmCrewA+`"]}`, 0o664)
	f.write("_users/alice/chat_history/product-conversations.json", `{"entries":{"x":{"workspace_path":"Chats/Work/projects/`+cmCrewA+`"}}}`, 0o660)
	return f
}

func (f *crewMoveFixture) write(rel, content string, mode fs.FileMode) {
	f.t.Helper()
	full := filepath.Join(f.Docs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o770); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(full, mode); err != nil {
		f.t.Fatal(err)
	}
}

// crew builds one Crew in its owner's tree: nested folders, a binary database, an executable, an empty folder, a
// private-mode file, an internal symlink, and group read/write modes with no access for others (as a slot tree has).
func (f *crewMoveFixture) crew(owner, folder, id string) {
	f.t.Helper()
	root := fmt.Sprintf("_users/%s/Chats/Work/projects/%s", owner, folder)
	f.write(root+"/product.json", fmt.Sprintf(`{"schema_version":1,"product":"work","id":"%s","title":"%s","session_id":"work:project:%s","owner_id":"%s"}`, id, folder, id, owner), 0o660)
	f.write(root+"/workflow.json", fmt.Sprintf(`{"schema_version":1,"id":"%s","label":"%s","capabilities":{}}`, id, folder), 0o660)
	f.write(root+"/code/notes.md", "# notes of "+folder+"\n", 0o660)
	f.write(root+"/code/deep/er/file.txt", strings.Repeat("deep ", 100), 0o640)
	f.write(root+"/code/run.sh", "#!/bin/sh\necho hi\n", 0o750)
	f.write(root+"/builder/conversation/session-1.json", `{"messages":[]}`, 0o660)
	blob := make([]byte, 200*1024)
	if _, err := rand.Read(blob); err != nil {
		f.t.Fatal(err)
	}
	f.write(root+"/db/db.sqlite", string(blob), 0o660)
	if err := os.MkdirAll(filepath.Join(f.Docs, filepath.FromSlash(root), "empty"), 0o770); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink("code/notes.md", filepath.Join(f.Docs, filepath.FromSlash(root), "latest")); err != nil {
		f.t.Fatal(err)
	}
	// Group read/write, setgid folders, nothing for others.
	_ = filepath.WalkDir(filepath.Join(f.Docs, filepath.FromSlash(root)), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o770|fs.ModeSetgid)
		}
		return nil
	})
}

func (f *crewMoveFixture) source(owner, folder string) string {
	return filepath.Join(f.Docs, "_users", owner, "Chats", "Work", "projects", folder)
}

func (f *crewMoveFixture) dest(folder string) string { return filepath.Join(f.Docs, "Crew", folder) }

func (f *crewMoveFixture) run(opts ...func(*crewMoveOptions)) (*crewMoveReport, error) {
	f.t.Helper()
	o := f.opts
	for _, apply := range opts {
		apply(&o)
	}
	return runCrewMove(context.Background(), o)
}

func apply(o *crewMoveOptions) { o.Apply = true }
func only(folders ...string) func(*crewMoveOptions) {
	return func(o *crewMoveOptions) { o.Crews = folders }
}

// snapshot is every path below dir with its type, size, mode and mtime: two equal snapshots mean nothing changed.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(dir, p)
		target := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			target, _ = os.Readlink(p)
		}
		lines = append(lines, fmt.Sprintf("%s|%v|%d|%d|%s", rel, info.Mode(), info.Size(), info.ModTime().UnixNano(), target))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// treeDigestOf hashes a folder on disk the way the move does (content, modes (minus group bits), links).
func treeDigestOf(t *testing.T, dir string) string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries, err := hashCrewTree(root)
	if err != nil {
		t.Fatal(err)
	}
	return crewTreeDigest(entries)
}

func mustMove(t *testing.T, f *crewMoveFixture, opts ...func(*crewMoveOptions)) *crewMoveReport {
	t.Helper()
	report, err := f.run(append([]func(*crewMoveOptions){apply}, opts...)...)
	if err != nil {
		t.Fatalf("apply: %v\nreport: %+v\n%s", err, report, f.Out.String())
	}
	return report
}

func TestCrewMoveDryRunChangesNothingAndReportsEverything(t *testing.T) {
	f := newCrewMoveFixture(t)
	docsBefore, stateBefore := snapshot(t, f.Docs), snapshot(t, f.State)
	report, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot(t, f.Docs) != docsBefore || snapshot(t, f.State) != stateBefore {
		t.Fatal("the dry run changed the docs tree or the state area")
	}
	if _, err := os.Stat(f.Backup); err == nil {
		t.Fatal("the dry run created the backup directory")
	}
	if report.Mode != "dry-run" || len(report.Crews) != 3 {
		t.Fatalf("report = %+v", report)
	}
	byFolder := map[string]crewMovePlan{}
	for _, plan := range report.Crews {
		byFolder[plan.Folder] = plan
	}
	a := byFolder[cmCrewA]
	if a.Owner != "alice" || a.Source != "_users/alice/Chats/Work/projects/"+cmCrewA || a.Dest != "Crew/"+cmCrewA || a.Files < 7 || a.Bytes < 200*1024 || a.Symlinks != 1 || a.RegistryState != "none" || a.ManifestOwner != "alice" || len(a.Blockers) != 0 || a.State != "not started" {
		t.Fatalf("plan of %s = %+v", cmCrewA, a)
	}
	if byFolder[cmCrewC].Owner != "bob" {
		t.Fatalf("plan of %s = %+v", cmCrewC, byFolder[cmCrewC])
	}
	// Stored references that will be served through the alias.
	kinds := map[string]int{}
	for _, hit := range a.References {
		kinds[hit.Kind] = hit.Files
	}
	if kinds["workflow attachments and context paths"] != 1 || kinds["chat history, conversations and schedule state"] != 1 {
		t.Fatalf("references of %s = %+v", cmCrewA, a.References)
	}
	if len(a.Readers) != 2 {
		t.Fatalf("readers = %v", a.Readers)
	}
	// The Code is not a Crew.
	if _, ok := byFolder[cmCode]; ok {
		t.Fatal("a Code was listed")
	}
	var text bytes.Buffer
	printCrewMoveReport(&text, report)
	for _, want := range []string{"dry run", "Nothing was changed", cmCrewA, "Crew/" + cmCrewA, "references:", "alias", "readers:", "owner alice full"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the printed report lacks %q:\n%s", want, text.String())
		}
	}
	if !strings.Contains(text.String(), "(owner alice)") || !strings.Contains(text.String(), "(owner bob)") {
		t.Errorf("owners are not printed:\n%s", text.String())
	}
}

func TestCrewMoveApplyMovesOneCrewWithBackupJournalAndVerification(t *testing.T) {
	f := newCrewMoveFixture(t)
	beforeDigest := treeDigestOf(t, f.source("alice", cmCrewA))
	otherBefore := snapshot(t, f.source("alice", cmCrewB))
	bobBefore := snapshot(t, f.source("bob", cmCrewC))
	codeBefore := snapshot(t, filepath.Join(f.Docs, "_users", "carol"))
	report := mustMove(t, f, only(cmCrewA))
	if strings.Join(report.Moved, ",") != cmCrewA || strings.Join(report.Verified, ",") != cmCrewA {
		t.Fatalf("report = %+v", report)
	}
	// Content, modes, links: the moved Crew is the same Crew.
	if got := treeDigestOf(t, f.dest(cmCrewA)); got != beforeDigest {
		t.Fatalf("digest after the move %s, before %s", got, beforeDigest)
	}
	if target, err := os.Readlink(filepath.Join(f.dest(cmCrewA), "latest")); err != nil || target != "code/notes.md" {
		t.Fatalf("the internal symlink is %q err=%v (must be copied as a symlink)", target, err)
	}
	if info, _ := os.Stat(filepath.Join(f.dest(cmCrewA), "code", "run.sh")); info == nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("run.sh mode = %v", info)
	}
	if info, _ := os.Stat(filepath.Join(f.dest(cmCrewA), "code", "deep", "er", "file.txt")); info == nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("file.txt mode = %v", info)
	}
	// Nobody outside the owner's group gets anything: no "other" bit anywhere in the moved Crew, setgid folders kept.
	_ = filepath.WalkDir(f.dest(cmCrewA), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, _ := d.Info()
		if info.Mode()&fs.ModeSymlink == 0 && info.Mode().Perm()&0o007 != 0 {
			t.Errorf("%s has permissions for others: %v", p, info.Mode())
		}
		if d.IsDir() && info.Mode()&fs.ModeSetgid == 0 {
			t.Errorf("%s lost the setgid bit: %v", p, info.Mode())
		}
		return nil
	})
	if info, err := os.Stat(filepath.Join(f.Docs, "Crew")); err != nil || info.Mode().Perm() != 0o711 {
		t.Fatalf("Crew/ mode = %v err=%v, want 0711 (traversable, not listable)", info, err)
	}
	// The old folder is kept as a tombstone beside its owner's projects, not deleted and not left in the listing.
	if _, err := os.Stat(f.source("alice", cmCrewA)); !os.IsNotExist(err) {
		t.Fatalf("the old folder is still in the projects root: %v", err)
	}
	tomb := filepath.Join(f.Docs, "_users", "alice", "Chats", "Work", ".moved-to-crew", cmCrewA)
	if treeDigestOf(t, tomb) != beforeDigest {
		t.Fatal("the tombstone does not hold the original Crew")
	}
	if _, err := os.Stat(filepath.Join(f.Docs, "Crew", ".migrating", cmCrewA)); !os.IsNotExist(err) {
		t.Fatal("the staging folder is still there")
	}
	// Nothing else moved.
	if snapshot(t, f.source("alice", cmCrewB)) != otherBefore || snapshot(t, f.source("bob", cmCrewC)) != bobBefore || snapshot(t, filepath.Join(f.Docs, "_users", "carol")) != codeBefore {
		t.Fatal("a Crew that was not selected (or a Code) changed")
	}
	// Journal, registry, alias.
	j, err := loadCrewMoveJournal(f.State, cmCrewA)
	if err != nil || j == nil || j.State != crewMoveDone || j.Tree.Digest == "" || j.Tree.Files < 7 || j.BackupRun == "" {
		t.Fatalf("journal = %+v err=%v", j, err)
	}
	registry := projectOwnersAt(f.State)
	if rec, ok := registry.Lookup("work", cmCrewA); !ok || rec.OwnerID != "alice" || !rec.Shared || len(rec.Aliases) != 1 || rec.Aliases[0] != "_users/alice/Chats/Work/projects/"+cmCrewA {
		t.Fatalf("registry = %+v ok=%v", rec, ok)
	}
	if rec, ok := registry.Lookup("work", cmCrewB); !ok && false || (ok && rec.Shared) {
		t.Fatalf("an unmoved Crew is marked shared: %+v", rec)
	}
	// The verified backup of exactly that Crew (and the registry).
	runDir := report.BackupRun
	var summary crewBackupSummary
	raw, err := os.ReadFile(filepath.Join(runDir, "backup.json"))
	if err != nil || json.Unmarshal(raw, &summary) != nil || len(summary.Crews) != 1 || summary.Crews[0].Folder != cmCrewA || summary.Crews[0].Tree.Digest != j.Tree.Digest {
		t.Fatalf("backup.json = %s err=%v", raw, err)
	}
	if treeDigestOf(t, filepath.Join(runDir, "crews", "alice", cmCrewA)) != beforeDigest {
		t.Fatal("the backup differs from the Crew as it was")
	}
	if _, err := os.Stat(filepath.Join(runDir, "crews", "alice", cmCrewB)); !os.IsNotExist(err) {
		t.Fatal("the backup holds a Crew that was not going to move")
	}
}

func TestCrewMoveIsIdempotentAndMovesTheRest(t *testing.T) {
	f := newCrewMoveFixture(t)
	mustMove(t, f, only(cmCrewA))
	destDigest := treeDigestOf(t, f.dest(cmCrewA))
	// A second run with no selector moves the others and leaves the done one alone.
	second := mustMove(t, f)
	if strings.Join(second.Moved, ",") != cmCrewB+","+cmCrewC {
		t.Fatalf("second run moved %v", second.Moved)
	}
	if treeDigestOf(t, f.dest(cmCrewA)) != destDigest {
		t.Fatal("a finished move was disturbed by a re-run")
	}
	// A third run changes nothing at all.
	docsBefore, stateBefore := snapshot(t, f.Docs), snapshot(t, f.State)
	third, err := f.run(apply)
	if err != nil || len(third.Moved) != 0 || len(third.Resumed) != 0 {
		t.Fatalf("third run = %+v err=%v", third, err)
	}
	if snapshot(t, f.Docs) != docsBefore {
		t.Fatal("an idempotent re-run changed the docs tree")
	}
	_ = stateBefore
}

func TestCrewMoveRefusesApplyWithoutABackupDir(t *testing.T) {
	f := newCrewMoveFixture(t)
	docsBefore := snapshot(t, f.Docs)
	if _, err := f.run(apply, func(o *crewMoveOptions) { o.BackupDir = "" }); err == nil || !strings.Contains(err.Error(), "backup-dir") {
		t.Fatalf("apply without a backup dir: %v", err)
	}
	if snapshot(t, f.Docs) != docsBefore {
		t.Fatal("something changed")
	}
}

var _ = syscall.Getuid
var _ = errors.New
var _ = workspaceref.SharedCrewRoot
