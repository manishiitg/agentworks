package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A Team member's working copy on the server lives in the Crew's own folder, at team-copies/<member>/ (owner, 5 Nov:
// one Files view everywhere). Their Files opens on it through the ordinary workspace routes, their agents get it as
// their attached folder, and its sync record stays in the app's private state (crewTeamServerCopyDir). team-copies/
// is never part of what a Team shares or a Crew syncs (crewroot.SyncContentAllowed, crewTeamFolder.holds), and the
// Files listing shows each person only their own copy (crewroot.TeamCopyHidden).

// crewTeamMemberCopyRoot is the member's copy folder in the Crew's folder, on disk (crewTeamMemberFilesPath names the
// same folder as a workspace path).
func crewTeamMemberCopyRoot(project, member string) (string, error) {
	rec, ok := crewOwnerRecord(project)
	if !ok || rec.Folder == "" {
		return "", fmt.Errorf("this Crew's folder is unavailable")
	}
	roots, found, err := crewStorageRoots("Crew/" + rec.Folder)
	if err == nil && !found {
		err = fmt.Errorf("this Crew's folder is unavailable")
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(roots.Files, crewroot.TeamCopiesFolder, sanitizeUserIDForPath(member)), nil
}

// crewTeamMemberCopyFolder: a folder inside team-copies/ (only such a folder is ever removed with a member's copy).
func crewTeamMemberCopyFolder(root string) bool {
	return strings.Contains(filepath.ToSlash(root), "/"+crewroot.TeamCopiesFolder+"/")
}

// crewTeamMemberFilesPath is the root of the Files view for this person in the Crew at root (the server decides it).
// A Team member of a Crew on the server works in the Crew's own folder, so their Files opens on the one folder the
// Team shares (made if it is missing); a member on the web of a Mac Crew opens on their own copy; everyone else, and a
// Team of several folders or the Whole Crew, gets the Crew's files root.
func crewTeamMemberFilesPath(user, root string) string {
	files := crewFilesPath(root)
	if !crewTeamOnlyMember(root, user) {
		return files
	}
	rec, ok := crewOwnerRecordAt(root)
	if !ok {
		return files
	}
	t, err := readCrewTeam(rec.ProjectID)
	if err != nil || !t.edits(user) {
		return files
	}
	if crewRelayTwin(rec.ProjectID) {
		if _, onMac := t.memberMac(user); onMac {
			return files
		}
		return path.Join(files, crewroot.TeamCopiesFolder, sanitizeUserIDForPath(user))
	}
	if t.Folder.Whole || len(t.Folder.Folders) != 1 {
		return files
	}
	if roots, found, err := crewStorageRoots(root); err == nil && found {
		_ = os.MkdirAll(filepath.Join(roots.Files, filepath.FromSlash(t.Folder.Folders[0])), 0o770)
	}
	return path.Join(files, t.Folder.Folders[0])
}

// migrateCrewTeamMemberCopies moves each member copy still in the app's private state (<copy>/files) into the Crew's
// folder once: a tarball backup goes to team-working/backups first, then the files are copied, the record points at
// the new folder and the old copy is removed. Server only.
func migrateCrewTeamMemberCopies() {
	if IsLocalMode() {
		return
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dirs, _ := filepath.Glob(filepath.Join(state, "team-working", "server", "*", "*"))
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, dir := range dirs {
		b, err := readCrewTeamLocal(dir)
		old := filepath.Join(dir, "files")
		if err != nil || filepath.Clean(b.Root) != old {
			continue // the owner's copy (the Crew folder itself) or already moved
		}
		result, files := migrateCrewTeamMemberCopy(dir, old, stamp, &b)
		log.Printf("[CREW_TEAM] migrate_member_copy project=%s user=%s files=%d result=%s", b.Project, logUsernameForUserID(b.RemoteUser), files, result)
	}
}

func migrateCrewTeamMemberCopy(dir, old, stamp string, b *crewTeamLocal) (string, int) {
	unlock, err := wf.LockWorkspace(context.Background(), dir)
	if err != nil {
		return "lock_failed", 0
	}
	defer unlock()
	target, err := crewTeamMemberCopyRoot(b.Project, b.RemoteUser)
	if err != nil {
		return "no_crew_folder", 0
	}
	// The copy goes to a sibling folder and is renamed into place whole, so the target only ever holds a finished copy.
	// A target that exists while this record still points at the old copy is what a crash between the rename and
	// saving the record left: the migration resumes from it. Anything else in the target is not this copy's.
	resumed := false
	if entries, e := os.ReadDir(target); e == nil && len(entries) > 0 {
		if _, e := os.Stat(filepath.Join(target, crewTeamMigratedMarker)); e != nil {
			return "target_not_empty", 0
		}
		resumed = true
	}
	files := 0
	if !resumed {
		backups := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(dir))), "backups")
		files, err = tarCrewTeamCopy(old, filepath.Join(backups, filepath.Base(filepath.Dir(dir))+"-"+filepath.Base(dir)+"-"+stamp+".tar.gz"))
		if err != nil {
			return "backup_failed", files
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o770); err != nil {
			return "copy_failed", files
		}
		staging := target + ".migrating"
		_ = os.RemoveAll(staging)
		if err = copyCrewTeamTree(old, staging); err == nil {
			err = os.WriteFile(filepath.Join(staging, crewTeamMigratedMarker), nil, 0o600)
		}
		if err == nil {
			_ = os.Remove(target) // an empty target folder
			err = os.Rename(staging, target)
		}
		if err != nil {
			_ = os.RemoveAll(staging)
			return "copy_failed", files
		}
	}
	b.Root = target
	if err = saveCrewTeamLocal(dir, *b); err != nil {
		return "record_failed", files // the finished copy stays; the next start resumes from it
	}
	_ = os.Remove(filepath.Join(target, crewTeamMigratedMarker))
	if err = os.RemoveAll(old); err != nil {
		return "moved_old_not_removed", files
	}
	if resumed {
		return "moved_resumed", files
	}
	return "moved", files
}

// crewTeamMigratedMarker marks a finished migrated copy until its record points at it.
const crewTeamMigratedMarker = ".team-copy-migrated"

// copyCrewTeamTree copies a folder: files with their mode, folders, and links as links (os.CopyFS refuses links).
func copyCrewTeamTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(to, info.Mode().Perm()|0o700)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, to)
		case d.Type().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err = io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil // sockets, devices: not part of a copy
	})
}

// tarCrewTeamCopy writes a gzip tarball of the folder's regular files and returns how many it holds.
func tarCrewTeamCopy(src, dst string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)
	files := 0
	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err = tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		files++
		return err
	})
	for _, closeErr := range []error{tw.Close(), zw.Close(), out.Close()} {
		if walkErr == nil {
			walkErr = closeErr
		}
	}
	return files, walkErr
}
