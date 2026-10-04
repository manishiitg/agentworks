package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// The same access assertions as before the move, run against the Crew the move command really moved (real files, the
// real registry it wrote), and against a mixed server where one Crew moved and another did not.
func TestAccessAssertionsAfterARealMove(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "moved"
		if mixed {
			name = "mixed: one moved, one not"
		}
		t.Run(name, func(t *testing.T) {
			f := newMultiUserFixture(t, legacyIdentityLayout())
			if mixed {
				f.withLegacyCrew()
			}
			opts := crewMoveOptions{
				DocsRoot: f.Docs, StateRoot: f.State, BackupDir: filepath.Join(t.TempDir(), "backup"), Apply: true, Crews: []string{fixtureCrewFolder},
				Out:       os.Stderr,
				Probe:     func(context.Context, string, map[string]string) (map[string][]string, error) { return nil, nil },
				FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
				Readers:   func() []string { return nil },
			}
			report, err := runCrewMove(context.Background(), opts)
			if err != nil || len(report.Moved) != 1 {
				t.Fatalf("move: %v %+v", err, report)
			}
			resetCrewLocationCaches()
			f.SyncMockFromDisk()
			moved := sharedIdentityLayout()
			f.Layout = moved
			assertMultiUserAccess(t, f, moved)
			checkStoredReferencesToAMovedCrew(t, f)
			if mixed {
				// The Crew that stayed is untouched and still resolves the old way.
				legacy := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureLegacyCrewFolder)
				f.WithSharing(true)
				own, err := resolveCrewProjectBinding(f.Ctx(fixtureUserA), fixtureUserA, f.Crew, fixtureLegacyCrewID, "")
				if err != nil || !own.OwnedByCaller || own.Binding.WorkspacePath != legacy {
					t.Fatalf("the Crew that did not move: %+v err=%v", own, err)
				}
				if got := followCrewAlias(fixtureUserA, legacy); got != legacy {
					t.Fatalf("an unmoved Crew's path was translated to %q", got)
				}
			}
		})
	}
}

// The CLI runtime folder is named by a digest of the project path; the move keeps the Crew's runtime folder (and with it
// the native CLI session that lives there) and repoints its link, so existing chats are not started afresh.
func TestCrewMoveKeepsTheCLIRuntimeFolderAndItsNativeSession(t *testing.T) {
	f := newCrewMoveFixture(t)
	t.Setenv("AGENTWORKS_ISOLATE_WORKFLOW_CLI", "false")
	oldFolder := "_users/alice/Chats/Work/projects/" + cmCrewA
	session := "work:project:c-a"
	builderBefore, err := crewCLIWorkingDir(oldFolder, "alice", session, "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	runBefore, err := crewCLIWorkingDir(oldFolder, "alice", session, "claude-code", true)
	if err != nil {
		t.Fatal(err)
	}
	otherBefore, err := crewCLIWorkingDir("_users/alice/Chats/Work/projects/"+cmCrewB, "alice", "work:project:c-b", "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	// The CLI's native session state lives in the runtime folder.
	native := filepath.Join(builderBefore, ".claude", "projects", "sess.jsonl")
	if err := os.MkdirAll(filepath.Dir(native), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(`{"native":"session"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if builderBefore == runBefore {
		t.Fatal("builder and run share a runtime")
	}

	mustMove(t, f, only(cmCrewA))
	resetCrewLocationCaches()

	newFolder := "Crew/" + cmCrewA
	builderAfter, err := crewCLIWorkingDir(newFolder, "alice", session, "claude-code", false)
	if err != nil {
		t.Fatal(err)
	}
	runAfter, err := crewCLIWorkingDir(newFolder, "alice", session, "claude-code", true)
	if err != nil {
		t.Fatal(err)
	}
	if builderAfter != builderBefore || runAfter != runBefore {
		t.Fatalf("the runtime folders changed with the move:\n builder %s -> %s\n run     %s -> %s", builderBefore, builderAfter, runBefore, runAfter)
	}
	if raw, err := os.ReadFile(native); err != nil || string(raw) != `{"native":"session"}` {
		t.Fatalf("the native session state is gone: %v", err)
	}
	// The runtime's link follows the Crew.
	resolvedDest, _ := filepath.EvalSymlinks(f.dest(cmCrewA))
	for _, dir := range []string{builderAfter, runAfter} {
		if saved, err := os.Readlink(filepath.Join(dir, "project")); err != nil || saved != resolvedDest {
			t.Fatalf("%s/project -> %q (err %v), want %q", dir, saved, err, resolvedDest)
		}
	}
	j, _ := loadCrewMoveJournal(f.State, cmCrewA)
	if j == nil || j.LinksRepointed != 2 {
		t.Fatalf("journal = %+v: both runtimes' links should have been repointed", j)
	}
	// Other Crews' runtimes are not touched, and a Crew created at the shared root later gets its own.
	if otherAfter, err := crewCLIWorkingDir("_users/alice/Chats/Work/projects/"+cmCrewB, "alice", "work:project:c-b", "claude-code", false); err != nil || otherAfter != otherBefore {
		t.Fatalf("an unmoved Crew's runtime changed: %v %v", otherAfter, err)
	}
	if err := os.MkdirAll(filepath.Join(f.Docs, "Crew", "fresh-99999999"), 0o770); err != nil {
		t.Fatal(err)
	}
	fresh, err := crewCLIWorkingDir("Crew/fresh-99999999", "alice", session, "claude-code", false)
	if err != nil || fresh == builderBefore {
		t.Fatalf("a new Crew shares a runtime with a moved one: %v %v", fresh, err)
	}
	// After a rollback the Crew is back at its old path with the same runtime and its link back at the old folder.
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA }); err != nil {
		t.Fatal(err)
	}
	resetCrewLocationCaches()
	back, err := crewCLIWorkingDir(oldFolder, "alice", session, "claude-code", false)
	if err != nil || back != builderBefore {
		t.Fatalf("after the rollback the runtime is %q (err %v), want %q", back, err, builderBefore)
	}
	resolvedOld, _ := filepath.EvalSymlinks(f.source("alice", cmCrewA))
	if saved, _ := os.Readlink(filepath.Join(back, "project")); saved != resolvedOld {
		t.Fatalf("after the rollback the link is %q, want %q", saved, resolvedOld)
	}
}

// While a Crew is being moved the server binds nothing to it and lets nobody write it; a dead command's marker locks nothing.
func TestCrewMoveLocksTheCrewAgainstTheServerWhileItMoves(t *testing.T) {
	f := newCrewMoveFixture(t)
	oldRoot := "_users/alice/Chats/Work/projects/" + cmCrewA
	store := productProjectStore{
		listPaths: func(_ context.Context, root string) ([]string, bool, error) {
			if root != "_users/alice/Chats/Work/projects" {
				return nil, false, nil
			}
			return []string{oldRoot + "/product.json", "_users/alice/Chats/Work/projects/" + cmCrewB + "/product.json"}, true, nil
		},
		read: func(_ context.Context, p string) (string, bool, error) {
			switch p {
			case oldRoot + "/product.json":
				return `{"product":"work","id":"c-a","title":"A","session_id":"s-a"}`, true, nil
			case "_users/alice/Chats/Work/projects/" + cmCrewB + "/product.json":
				return `{"product":"work","id":"c-b","title":"B","session_id":"s-b"}`, true, nil
			}
			return "", false, nil
		},
	}
	profile := agentprofiles.Profile{ID: "work", Runtime: agentprofiles.RuntimePolicy{Workspace: agentprofiles.WorkspacePolicy{ProjectsRoot: workspaceref.CrewProjectsRoot}}}
	checked := false
	hook := func(o *crewMoveOptions) {
		o.Hook = func(point string) error {
			if point != "copy:1" {
				return nil
			}
			checked = true
			crewMoveCache.mu.Lock()
			crewMoveCache.read = crewMoveCache.read.Add(-10 * 1e9)
			crewMoveCache.mu.Unlock()
			if !crewMoveInProgress(cmCrewA) || crewMoveInProgress(cmCrewB) {
				t.Errorf("the lock marker is wrong while %s is copied", cmCrewA)
			}
			if _, err := resolveProductProjectBindingWithStore(context.Background(), "alice", profile, "c-a", store); err == nil || err.Error() != errCrewBeingMoved.Error() {
				t.Errorf("a turn was bound to a Crew being moved: %v", err)
			}
			if _, err := resolveProductProjectBindingWithStore(context.Background(), "alice", profile, "c-b", store); err != nil {
				t.Errorf("another Crew was refused: %v", err)
			}
			for _, spelling := range []string{oldRoot + "/code/x", "Crew/" + cmCrewA + "/code/x"} {
				if reason := (workspaceProxyPolicy{write: true, admin: true}).denies("path", spelling); reason != errCrewBeingMoved.Error() {
					t.Errorf("a write to %s while it moves: %q", spelling, reason)
				}
			}
			if reason := (workspaceProxyPolicy{write: false, admin: true}).denies("path", oldRoot+"/code/x"); reason != "" {
				t.Errorf("a read was refused: %q", reason)
			}
			if reason := (workspaceProxyPolicy{write: true, admin: true}).denies("path", "_users/alice/Chats/Work/projects/"+cmCrewB+"/code/x"); reason != "" {
				t.Errorf("a write to another Crew was refused: %q", reason)
			}
			return nil
		}
	}
	mustMove(t, f, only(cmCrewA), hook)
	if !checked {
		t.Fatal("the hook never ran")
	}
	// Released afterwards.
	crewMoveCache.mu.Lock()
	crewMoveCache.read = crewMoveCache.read.Add(-10 * 1e9)
	crewMoveCache.mu.Unlock()
	if crewMoveInProgress(cmCrewA) {
		t.Fatal("the lock marker outlived the move")
	}
	// A marker whose process is gone (a crashed command) locks nothing.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip("cannot run true")
	}
	if err := writeCrewMoveActive(f.State, cmCrewB); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	if err := os.WriteFile(filepath.Join(crewMoveStateDir(f.State), crewMoveActiveFile), []byte(`{"folder":"`+cmCrewB+`","pid":`+strconv.Itoa(cmd.Process.Pid)+`,"host":"`+host+`","started_at":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	crewMoveCache.mu.Lock()
	crewMoveCache.read = crewMoveCache.read.Add(-10 * 1e9)
	crewMoveCache.mu.Unlock()
	if crewMoveInProgress(cmCrewB) {
		t.Fatal("a marker of a dead process locked the Crew")
	}
}
