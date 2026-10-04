package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// rollback puts one Crew back in its owner's tree. By default the Crew's CURRENT folder (with everything written to it
// since the move) is renamed back, so nothing done after the move is lost; --from-backup restores the Crew as it was
// at the move from the verified backup instead, for a Crew whose new folder is damaged. The registry stops treating the
// Crew as shared (its alias stays recorded and is inert), CLI runtime links follow the folder back, and the journal
// says rolled_back. The old folder's tombstone is kept under its own name.
func (m *crewMover) rollback(ctx context.Context, folder string) error {
	opts := m.opts
	folder = strings.TrimSpace(folder)
	j, err := loadCrewMoveJournal(opts.StateRoot, folder)
	if err != nil {
		return err
	}
	if j == nil {
		return fmt.Errorf("there is no move journal for %q: nothing to roll back", folder)
	}
	if j.State == crewMoveRolledBack {
		fmt.Fprintf(opts.Out, "%s was already rolled back\n", folder)
		return nil
	}
	if err := writeCrewMoveActive(opts.StateRoot, folder); err != nil {
		return err
	}
	defer clearCrewMoveActive(opts.StateRoot, folder)
	exists := func(rel string) bool { _, err := m.docs.Lstat(rel); return err == nil }

	switch {
	case opts.FromBackup:
		if err := m.restoreFromBackup(j); err != nil {
			return err
		}
	case exists(j.Source) && !exists(j.Dest):
		// Never switched (the source is where it was): only the partial copy has to go.
	case exists(j.Dest) && !exists(j.Source):
		if err := m.docs.MkdirAll(filepath.ToSlash(filepath.Dir(j.Source)), 0o770); err != nil {
			return err
		}
		if err := m.docs.Rename(j.Dest, j.Source); err != nil {
			return fmt.Errorf("move %s back to %s: %w", j.Dest, j.Source, err)
		}
	case !exists(j.Dest) && !exists(j.Source) && exists(j.Tombstone):
		if err := m.docs.Rename(j.Tombstone, j.Source); err != nil {
			return fmt.Errorf("restore %s from its tombstone: %w", j.Source, err)
		}
	case exists(j.Dest) && exists(j.Source):
		return fmt.Errorf("both %s and %s exist: resolve by hand (nothing was changed)", j.Source, j.Dest)
	default:
		return fmt.Errorf("neither %s nor %s nor the tombstone exists: use --from-backup with the backup %q", j.Source, j.Dest, j.BackupRun)
	}
	_ = m.docs.RemoveAll(j.Staging)

	// The registry: not shared any more.
	if rec, ok := m.registry.Lookup("work", folder); ok && rec.Shared {
		if err := m.registry.SetShared("work", folder, false); err != nil {
			return fmt.Errorf("cannot update the owner registry: %w", err)
		}
	}
	forgetCrewLocationCaches()
	// CLI runtimes follow the folder back.
	if n, err := cliruntime.RepointProjectLinks(opts.StateRoot, j.NewAbs, j.OldAbs); err != nil {
		return fmt.Errorf("cannot repoint the CLI runtimes: %w", err)
	} else if n > 0 {
		fmt.Fprintf(opts.Out, "repointed %d CLI runtime link(s) back to %s\n", n, j.OldAbs)
	}
	j.State = crewMoveRolledBack
	j.RolledBackAt = opts.now().Format("2006-01-02T15:04:05Z")
	j.LastError = ""
	if err := j.save(opts.StateRoot, opts.now()); err != nil {
		return err
	}
	// Confirm the Crew is where it should be.
	src, err := openProjectDir(m.docs, j.Owner, workspaceref.CrewProjectsRoot, j.Folder)
	if err != nil {
		return fmt.Errorf("after the rollback %s does not open: %w", j.Source, err)
	}
	_ = src.Close()
	if owner, ok := crewProjectOwnerID(j.Source); !ok || owner != j.Owner {
		return fmt.Errorf("after the rollback the owner of %s resolves to %q", j.Source, owner)
	}
	fmt.Fprintf(opts.Out, "rolled back %s to %s\n", folder, j.Source)
	_ = ctx
	return nil
}

// restoreFromBackup copies the Crew as the verified backup holds it into a new folder at the source path, never over
// an existing folder (a damaged current Crew at the source or at the shared root is moved aside by name first).
func (m *crewMover) restoreFromBackup(j *crewMoveJournal) error {
	opts := m.opts
	if strings.TrimSpace(opts.BackupDir) == "" || j.BackupRun == "" {
		return fmt.Errorf("--from-backup needs --backup-dir and a journal that names its backup run")
	}
	runDir := filepath.Join(opts.BackupDir, j.BackupRun)
	rel := filepath.ToSlash(filepath.Join("crews", j.Owner, j.Folder))
	backupRoot, err := os.OpenRoot(filepath.Join(runDir, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("open the backup of %s: %w", j.Folder, err)
	}
	defer backupRoot.Close()
	entries, _, err := walkCrewTree(backupRoot)
	if err != nil {
		return err
	}
	exists := func(p string) bool { _, err := m.docs.Lstat(p); return err == nil }
	stamp := opts.now().Format("20060102T150405")
	for _, occupied := range []string{j.Source, j.Dest} {
		if exists(occupied) {
			aside := filepath.ToSlash(filepath.Join(filepath.Dir(j.Tombstone), filepath.Base(occupied)+".replaced-"+stamp))
			if err := m.docs.MkdirAll(filepath.ToSlash(filepath.Dir(aside)), 0o700); err != nil {
				return err
			}
			if err := m.docs.Rename(occupied, aside); err != nil {
				return err
			}
		}
	}
	staging := j.Staging + ".restore"
	if err := m.docs.MkdirAll(filepath.ToSlash(filepath.Dir(staging)), 0o700); err != nil {
		return err
	}
	_ = m.docs.RemoveAll(staging)
	if err := m.docs.Mkdir(staging, 0o700); err != nil {
		return err
	}
	dst, err := m.docs.OpenRoot(staging)
	if err != nil {
		return err
	}
	copied, err := copyCrewTree(backupRoot, dst, entries, copyOptions{})
	if err != nil && !strings.Contains(err.Error(), "cannot keep group") {
		_ = dst.Close()
		return err
	}
	got, hashErr := hashCrewTree(dst)
	_ = dst.Close()
	if hashErr != nil {
		return hashErr
	}
	if err != nil {
		copied = hashedLike(entries, got)
	}
	if err := compareCrewTrees(stripGroup(copied), stripGroup(got)); err != nil {
		return fmt.Errorf("the restored copy differs from the backup: %w", err)
	}
	if err := m.docs.Rename(staging, j.Source); err != nil {
		return err
	}
	return nil
}

// finalize deletes the tombstones (the old folders kept beside their owners' projects) and any leftover staging folder
// of Crews whose move is done, for one Crew or, with no selector, every finished one. Only done Crews are touched.
func (m *crewMover) finalize() error {
	opts := m.opts
	journals, err := listCrewMoveJournals(opts.StateRoot)
	if err != nil {
		return err
	}
	for _, j := range journals {
		if j.State != crewMoveDone || !selectedCrew(crewMovePlan{Folder: j.Folder, ProjectID: j.ProjectID}, opts.Crews) {
			continue
		}
		// The new folder must be whole before the old one goes.
		if err := m.verifyMoved(j); err != nil {
			m.report.Failed[j.Folder] = "not finalized, the moved Crew does not verify: " + err.Error()
			continue
		}
		if _, err := m.docs.Lstat(j.Tombstone); err == nil {
			if err := m.docs.RemoveAll(j.Tombstone); err != nil {
				m.report.Failed[j.Folder] = err.Error()
				continue
			}
		}
		_ = m.docs.RemoveAll(j.Staging)
		j.Finalized = true
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		m.report.Moved = append(m.report.Moved, j.Folder)
		fmt.Fprintf(opts.Out, "finalized %s: the old folder %s was removed\n", j.Folder, j.Tombstone)
	}
	if len(m.report.Failed) > 0 {
		return errCrewMoveIncomplete
	}
	return nil
}
