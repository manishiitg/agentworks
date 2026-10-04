package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var errCrash = errors.New("simulated crash")

// crashAt makes the run stop dead at a named point, as a killed process would: no cleanup, the journal as it was.
func crashAt(point string) func(*crewMoveOptions) {
	return func(o *crewMoveOptions) {
		o.Hook = func(p string) error {
			if p == point {
				return errCrash
			}
			return nil
		}
	}
}

// A crash in the middle of the copy leaves the source untouched and the server free of any lock; running again finishes.
func TestCrewMoveCrashMidCopyThenResume(t *testing.T) {
	f := newCrewMoveFixture(t)
	before := snapshot(t, f.source("alice", cmCrewA))
	beforeDigest := treeDigestOf(t, f.source("alice", cmCrewA))
	_, err := f.run(apply, only(cmCrewA), crashAt("copy:3"))
	if !errors.Is(err, errCrewMoveIncomplete) {
		t.Fatalf("a crash at copy:3 returned %v", err)
	}
	if snapshot(t, f.source("alice", cmCrewA)) != before {
		t.Fatal("the source changed during a crashed copy")
	}
	if _, err := os.Stat(f.dest(cmCrewA)); !os.IsNotExist(err) {
		t.Fatalf("a half-copied Crew appeared at the shared root: %v", err)
	}
	j, _ := loadCrewMoveJournal(f.State, cmCrewA)
	if j == nil || j.State != crewMovePlanned || j.LastError == "" {
		t.Fatalf("journal after the crash = %+v", j)
	}
	if rec, ok := projectOwnersAt(f.State).Lookup("work", cmCrewA); ok && rec.Shared {
		t.Fatal("the registry says the Crew moved")
	}
	// Run again (no crash): resumes, finishes, and the Crew is whole.
	report := mustMove(t, f, only(cmCrewA))
	if strings.Join(report.Resumed, ",") != cmCrewA {
		t.Fatalf("report = %+v", report)
	}
	if treeDigestOf(t, f.dest(cmCrewA)) != beforeDigest {
		t.Fatal("the resumed move produced a different Crew")
	}
	if j, _ := loadCrewMoveJournal(f.State, cmCrewA); j == nil || j.State != crewMoveDone {
		t.Fatalf("journal after the resume = %+v", j)
	}
}

// A crash at EVERY named point of a Crew's move resumes to the same finished state, with nothing lost or duplicated.
func TestCrewMoveCrashAtEveryPointThenResume(t *testing.T) {
	points := []string{"copied", "staged", "verified", "before-switch", "after-staging-rename", "after-source-rename", "switched", "registered"}
	for _, point := range points {
		point := point
		t.Run(point, func(t *testing.T) {
			f := newCrewMoveFixture(t)
			beforeDigest := treeDigestOf(t, f.source("alice", cmCrewA))
			if _, err := f.run(apply, only(cmCrewA), crashAt(point)); !errors.Is(err, errCrewMoveIncomplete) {
				t.Fatalf("crash at %s returned %v", point, err)
			}
			// At every crash point the Crew exists, whole, in at least one place; never in neither.
			exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
			tomb := filepath.Join(f.Docs, "_users", "alice", "Chats", "Work", ".moved-to-crew", cmCrewA)
			if !exists(f.source("alice", cmCrewA)) && !exists(f.dest(cmCrewA)) {
				t.Fatalf("crash at %s lost the Crew from both places", point)
			}
			if exists(f.dest(cmCrewA)) && treeDigestOf(t, f.dest(cmCrewA)) != beforeDigest {
				t.Fatalf("crash at %s left a different Crew at the shared root", point)
			}
			_ = tomb
			// Resume.
			report, err := f.run(apply, only(cmCrewA))
			if err != nil || len(report.Failed) != 0 {
				t.Fatalf("resume after %s: %v %+v", point, err, report)
			}
			if treeDigestOf(t, f.dest(cmCrewA)) != beforeDigest {
				t.Fatalf("resume after %s produced a different Crew", point)
			}
			if exists(f.source("alice", cmCrewA)) {
				t.Fatalf("the old folder is still in the projects root after resuming from %s", point)
			}
			if !exists(tomb) || treeDigestOf(t, tomb) != beforeDigest {
				t.Fatalf("no tombstone with the original after resuming from %s", point)
			}
			if rec, ok := projectOwnersAt(f.State).Lookup("work", cmCrewA); !ok || !rec.Shared || rec.OwnerID != "alice" {
				t.Fatalf("registry after resuming from %s: %+v", point, rec)
			}
			if j, _ := loadCrewMoveJournal(f.State, cmCrewA); j == nil || j.State != crewMoveDone {
				t.Fatalf("journal after resuming from %s: %+v", point, j)
			}
			if _, err := os.Stat(filepath.Join(f.Docs, "Crew", ".migrating", cmCrewA)); !os.IsNotExist(err) {
				t.Fatalf("staging left behind after %s", point)
			}
		})
	}
}

func TestCrewMoveRollbackKeepsWorkDoneAfterTheMove(t *testing.T) {
	f := newCrewMoveFixture(t)
	beforeDigest := treeDigestOf(t, f.source("alice", cmCrewA))
	mustMove(t, f, only(cmCrewA))
	// Work done in the Crew after the move.
	newFile := filepath.Join(f.dest(cmCrewA), "code", "after-move.md")
	if err := os.WriteFile(newFile, []byte("written after the move"), 0o660); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA }); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(f.source("alice", cmCrewA), "code", "after-move.md")
	if raw, err := os.ReadFile(restored); err != nil || string(raw) != "written after the move" {
		t.Fatalf("the work done after the move was lost: %v", err)
	}
	if _, err := os.Stat(f.dest(cmCrewA)); !os.IsNotExist(err) {
		t.Fatal("the Crew is still at the shared root")
	}
	// Everything that was there before is there, unchanged.
	_ = os.Remove(restored)
	if treeDigestOf(t, f.source("alice", cmCrewA)) != beforeDigest {
		t.Fatal("the rolled-back Crew differs from the original")
	}
	if rec, ok := projectOwnersAt(f.State).Lookup("work", cmCrewA); !ok || rec.Shared || rec.OwnerID != "alice" {
		t.Fatalf("registry after rollback = %+v", rec)
	}
	if j, _ := loadCrewMoveJournal(f.State, cmCrewA); j == nil || j.State != crewMoveRolledBack {
		t.Fatalf("journal = %+v", j)
	}
	// The old spelling is the Crew again, owned by its path owner; the alias is inert.
	forgetCrewLocationCaches()
	if got := crewAliasesFromRegistry(projectOwnersAt(f.State)); len(got) != 0 {
		t.Fatalf("aliases after rollback = %v", got)
	}
	// A second rollback is a no-op, and the Crew can be moved again.
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA }); err != nil {
		t.Fatal(err)
	}
	mustMove(t, f, only(cmCrewA))
	if _, err := os.Stat(f.dest(cmCrewA)); err != nil {
		t.Fatalf("the re-move failed: %v", err)
	}
	if j, _ := loadCrewMoveJournal(f.State, cmCrewA); j == nil || j.State != crewMoveDone {
		t.Fatalf("journal after the re-move = %+v", j)
	}
}

func TestCrewMoveRollbackFromBackupRestoresADamagedCrew(t *testing.T) {
	f := newCrewMoveFixture(t)
	beforeDigest := treeDigestOf(t, f.source("alice", cmCrewA))
	mustMove(t, f, only(cmCrewA))
	// The Crew at the shared root is damaged.
	if err := os.Remove(filepath.Join(f.dest(cmCrewA), "db", "db.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA; o.FromBackup = true }); err != nil {
		t.Fatal(err)
	}
	if treeDigestOf(t, f.source("alice", cmCrewA)) != beforeDigest {
		t.Fatal("the restore from the backup does not match the original Crew")
	}
	if _, err := os.Stat(f.dest(cmCrewA)); !os.IsNotExist(err) {
		t.Fatal("the damaged Crew was left at the shared root")
	}
	// The damaged copy was kept under its own name, not deleted.
	matches, _ := filepath.Glob(filepath.Join(f.Docs, "_users", "alice", "Chats", "Work", ".moved-to-crew", cmCrewA+".replaced-*"))
	if len(matches) != 1 {
		t.Fatalf("the replaced Crew was not kept aside: %v", matches)
	}
}

func TestCrewMoveFinalizeRemovesOnlyFinishedTombstones(t *testing.T) {
	f := newCrewMoveFixture(t)
	mustMove(t, f, only(cmCrewA))
	tomb := filepath.Join(f.Docs, "_users", "alice", "Chats", "Work", ".moved-to-crew", cmCrewA)
	if _, err := os.Stat(tomb); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(func(o *crewMoveOptions) { o.Finalize = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tomb); !os.IsNotExist(err) {
		t.Fatal("finalize did not remove the old folder")
	}
	if treeDigestOf(t, f.dest(cmCrewA)) == "" {
		t.Fatal("finalize touched the moved Crew")
	}
	// A Crew that was never moved is not touched.
	if _, err := os.Stat(f.source("alice", cmCrewB)); err != nil {
		t.Fatal("finalize touched an unmoved Crew")
	}
	// Rolling back after a finalize has no old folder to use but still moves the Crew back.
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.source("alice", cmCrewA)); err != nil {
		t.Fatal(err)
	}
}

func TestCrewMoveBlocksCollisionsAndDuplicateNames(t *testing.T) {
	f := newCrewMoveFixture(t)
	// A collision at the shared root: nothing is merged, nothing is touched.
	if err := os.MkdirAll(filepath.Join(f.Docs, "Crew", cmCrewA), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Docs, "Crew", cmCrewA, "stranger.txt"), []byte("not ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The same folder name in two owners' trees.
	f.crew("bob", cmCrewB, "c-b-bob")
	before := snapshot(t, f.Docs)
	report, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]crewMovePlan{}
	for _, plan := range report.Crews {
		byKey[plan.Owner+"/"+plan.Folder] = plan
	}
	if plan := byKey["alice/"+cmCrewA]; len(plan.Blockers) == 0 || !strings.Contains(strings.Join(plan.Blockers, " "), "already exists") {
		t.Fatalf("the collision is not a blocker: %+v", plan)
	}
	for _, key := range []string{"alice/" + cmCrewB, "bob/" + cmCrewB} {
		if plan := byKey[key]; len(plan.Blockers) == 0 || !strings.Contains(strings.Join(plan.Blockers, " "), "unique") {
			t.Fatalf("%s: the duplicate folder name is not a blocker: %+v", key, plan)
		}
	}
	if len(byKey["bob/"+cmCrewC].Blockers) != 0 {
		t.Fatalf("an unrelated Crew is blocked: %+v", byKey["bob/"+cmCrewC])
	}
	// Apply moves what it can, skips the rest, never merges, and says it was incomplete.
	report, err = f.run(apply)
	if !errors.Is(err, errCrewMoveIncomplete) {
		t.Fatalf("apply with blocked Crews returned %v", err)
	}
	if strings.Join(report.Moved, ",") != cmCrewC || len(report.Skipped) != 3 {
		t.Fatalf("moved %v skipped %v", report.Moved, report.Skipped)
	}
	if raw, err := os.ReadFile(filepath.Join(f.Docs, "Crew", cmCrewA, "stranger.txt")); err != nil || string(raw) != "not ours" {
		t.Fatalf("the colliding folder was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Docs, "Crew", cmCrewA, "product.json")); !os.IsNotExist(err) {
		t.Fatal("the source was merged into the colliding folder")
	}
	if _, err := os.Stat(f.source("alice", cmCrewA)); err != nil {
		t.Fatal("a blocked Crew's source changed")
	}
	_ = before
}

func TestCrewMoveOwnerMismatchBlocksUntilResolved(t *testing.T) {
	f := newCrewMoveFixture(t)
	// A manifest copied from another account keeps its old owner_id.
	f.write("_users/alice/Chats/Work/projects/"+cmCrewB+"/product.json", `{"schema_version":1,"product":"work","id":"c-b","title":"x","session_id":"work:project:c-b","owner_id":"bob"}`, 0o660)
	report, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	var plan crewMovePlan
	for _, p := range report.Crews {
		if p.Folder == cmCrewB {
			plan = p
		}
	}
	if len(plan.Blockers) != 1 || !strings.Contains(plan.Blockers[0], "[OWNER_MISMATCH]") || plan.ManifestOwner != "bob" || plan.Owner != "alice" {
		t.Fatalf("plan = %+v", plan)
	}
	var text strings.Builder
	printCrewMoveReport(&text, report)
	if !strings.Contains(text.String(), "[OWNER_MISMATCH]") {
		t.Fatalf("the dry run output does not show the mismatch:\n%s", text.String())
	}
	// apply moves the others and refuses this one.
	if _, err := f.run(apply); !errors.Is(err, errCrewMoveIncomplete) {
		t.Fatalf("apply returned %v", err)
	}
	if _, err := os.Stat(f.source("alice", cmCrewB)); err != nil {
		t.Fatal("the mismatched Crew moved")
	}
	if _, err := os.Stat(f.dest(cmCrewB)); !os.IsNotExist(err) {
		t.Fatal("the mismatched Crew appeared at the shared root")
	}
	// Resolved by hand (owner_id set to the real owner), it moves.
	f.write("_users/alice/Chats/Work/projects/"+cmCrewB+"/product.json", `{"schema_version":1,"product":"work","id":"c-b","title":"x","session_id":"work:project:c-b","owner_id":"alice"}`, 0o660)
	mustMove(t, f)
	if _, err := os.Stat(f.dest(cmCrewB)); err != nil {
		t.Fatal(err)
	}
	if rec, _ := projectOwnersAt(f.State).Lookup("work", cmCrewB); rec.OwnerID != "alice" {
		t.Fatalf("registered owner = %q: the manifest must never decide it", rec.OwnerID)
	}
}

func TestCrewMoveRefusesACrewThatIsInUse(t *testing.T) {
	f := newCrewMoveFixture(t)
	probe := func(_ context.Context, _ string, crews map[string]string) (map[string][]string, error) {
		return map[string][]string{cmCrewA: {fmt.Sprintf("tmux session %q has a pane in %s", "mlp-claude-1", crews[cmCrewA])}}, nil
	}
	report, err := f.run(func(o *crewMoveOptions) { o.Probe = probe })
	if err != nil {
		t.Fatal(err)
	}
	var plan crewMovePlan
	for _, p := range report.Crews {
		if p.Folder == cmCrewA {
			plan = p
		}
	}
	if len(plan.Blockers) != 1 || !strings.Contains(plan.Blockers[0], "[ACTIVE]") {
		t.Fatalf("plan = %+v", plan)
	}
	before := snapshot(t, f.source("alice", cmCrewA))
	if _, err := f.run(apply, func(o *crewMoveOptions) { o.Probe = probe }); !errors.Is(err, errCrewMoveIncomplete) {
		t.Fatalf("apply returned %v", err)
	}
	if snapshot(t, f.source("alice", cmCrewA)) != before {
		t.Fatal("a Crew in use was changed")
	}
	if _, err := os.Stat(f.dest(cmCrewA)); !os.IsNotExist(err) {
		t.Fatal("a Crew in use was moved")
	}
}

// The real probe: a process whose working directory is in the Crew, or in a CLI runtime folder that links to it.
func TestDefaultCrewActivityProbeSeesProcessesAndRuntimes(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary")
	}
	_, procErr := os.Stat("/proc/self/cwd")
	if _, lsofErr := exec.LookPath("lsof"); procErr != nil && lsofErr != nil {
		t.Skip("neither /proc nor lsof is available")
	}
	f := newCrewMoveFixture(t)
	abs, _ := filepath.EvalSymlinks(f.source("alice", cmCrewA))
	// A CLI runtime folder that links to the Crew, with a process working in it.
	runtime := filepath.Join(f.State, "cli-runtimes", "v1", "digest1")
	if err := os.MkdirAll(runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(abs, filepath.Join(runtime, "project")); err != nil {
		t.Fatal(err)
	}
	// Do not let the probe touch the host's tmux: it is replaced by an empty answer.
	prior := runProbe
	runProbe = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "tmux" {
			return "", errors.New("no server running")
		}
		return prior(ctx, name, args...)
	}
	t.Cleanup(func() { runProbe = prior })
	inCrew := exec.Command("sleep", "30")
	inCrew.Dir = abs
	inRuntime := exec.Command("sleep", "30")
	inRuntime.Dir = runtime
	for _, cmd := range []*exec.Cmd{inCrew, inRuntime} {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmd := cmd
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	}
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		activity, err := defaultCrewActivityProbe(context.Background(), f.State, map[string]string{cmCrewA: abs, cmCrewB: filepath.Join(f.Docs, "elsewhere")})
		if err != nil {
			t.Fatal(err)
		}
		got = activity[cmCrewA]
		if len(got) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(got) < 2 {
		t.Fatalf("activity = %v, want the process in the Crew and the process in its CLI runtime", got)
	}
	if other := runtimeFoldersOf(f.State, abs); len(other) != 1 {
		t.Fatalf("runtime folders = %v", other)
	}
}

func runtimeFoldersOf(state, abs string) []string {
	return crewRuntimeFolders(state, map[string]string{"x": abs})["x"]
}

// The backup is verified readable BEFORE any move; a failure of any kind leaves everything as it was.
func TestCrewMoveBackupFailureAbortsBeforeAnyChange(t *testing.T) {
	t.Run("a backup that cannot be read back", func(t *testing.T) {
		f := newCrewMoveFixture(t)
		docsBefore := snapshot(t, f.Docs)
		corrupt := func(o *crewMoveOptions) {
			o.Hook = func(p string) error {
				if p == "backup-copied:"+cmCrewC {
					// Damage the backed-up copy before it is verified.
					return os.WriteFile(filepath.Join(f.Backup, "crew-move-20261004T120000Z", "crews", "bob", cmCrewC, "code", "notes.md"), []byte("corrupted"), 0o660)
				}
				return nil
			}
		}
		_, err := f.run(apply, corrupt)
		if err == nil || !strings.Contains(err.Error(), "backup failed, nothing was changed") {
			t.Fatalf("a corrupt backup did not abort the run: %v", err)
		}
		if snapshot(t, f.Docs) != docsBefore {
			t.Fatal("the docs tree changed although the backup failed")
		}
		// (the host-wide lock file is created by any apply; no journal, marker or registry entry may be)
		if left, _ := filepath.Glob(filepath.Join(f.State, "migrations", "crew-move", "*.json")); len(left) != 0 {
			t.Fatalf("journals or markers were written although the backup failed: %v", left)
		}
		if _, err := os.Stat(filepath.Join(f.State, "ownership")); !os.IsNotExist(err) {
			t.Fatal("the owner registry was written although the backup failed")
		}
		if _, err := os.Stat(filepath.Join(f.Docs, "Crew")); !os.IsNotExist(err) {
			t.Fatal("Crew/ was created although the backup failed")
		}
	})
	t.Run("too little space for the backup", func(t *testing.T) {
		f := newCrewMoveFixture(t)
		docsBefore := snapshot(t, f.Docs)
		_, err := f.run(apply, func(o *crewMoveOptions) {
			o.FreeBytes = func(p string) (uint64, error) {
				if strings.Contains(p, "backup") {
					return 1024, nil
				}
				return 1 << 40, nil
			}
		})
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || snapshot(t, f.Docs) != docsBefore {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("too little space on the docs volume", func(t *testing.T) {
		f := newCrewMoveFixture(t)
		docsBefore := snapshot(t, f.Docs)
		_, err := f.run(apply, func(o *crewMoveOptions) {
			o.FreeBytes = func(p string) (uint64, error) {
				if !strings.Contains(p, "backup") {
					return 1024, nil
				}
				return 1 << 40, nil
			}
		})
		if err == nil || !strings.Contains(err.Error(), "nothing was changed") || snapshot(t, f.Docs) != docsBefore {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("a backup directory that cannot be created", func(t *testing.T) {
		f := newCrewMoveFixture(t)
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		docsBefore := snapshot(t, f.Docs)
		if _, err := f.run(apply, func(o *crewMoveOptions) { o.BackupDir = filepath.Join(blocker, "sub") }); err == nil || snapshot(t, f.Docs) != docsBefore {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestCrewMoveRefusesASecondCommandOnTheSameHost(t *testing.T) {
	f := newCrewMoveFixture(t)
	unlock, err := lockCrewMove(f.State)
	if err != nil {
		t.Fatal(err)
	}
	docsBefore := snapshot(t, f.Docs)
	if _, err := f.run(apply, only(cmCrewA)); err == nil || !strings.Contains(err.Error(), "another Crew move is running") {
		t.Fatalf("a second command was not refused: %v", err)
	}
	if _, err := f.run(func(o *crewMoveOptions) { o.Rollback = cmCrewA }); err == nil || !strings.Contains(err.Error(), "another Crew move is running") {
		t.Fatalf("a rollback beside a running move was not refused: %v", err)
	}
	if snapshot(t, f.Docs) != docsBefore {
		t.Fatal("a refused command changed something")
	}
	// A dry run only reads: it is not refused.
	if _, err := f.run(); err != nil {
		t.Fatalf("a dry run beside a running move: %v", err)
	}
	unlock()
	mustMove(t, f, only(cmCrewA))
}

// A folder at the shared root the registry does not know is nobody's; the startup scan says so.
func TestStartupScanReportsOrphansAtTheSharedRoot(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	docs := t.TempDir()
	writeOwnerFixture(t, docs, "Crew/known-11111111/product.json", `{"product":"work","id":"k"}`)
	writeOwnerFixture(t, docs, "Crew/orphan-22222222/product.json", `{"product":"work","id":"o","owner_id":"alice"}`)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "known-11111111", OwnerID: "alice", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if report := migrateProductOwners(docs); report.SharedOrphans != 1 {
		t.Fatalf("report = %+v", report)
	}
	if owner := resolveProjectOwner(context.Background(), "Crew/orphan-22222222"); owner != "" {
		t.Fatalf("an orphan has owner %q (the manifest must not count)", owner)
	}
}
