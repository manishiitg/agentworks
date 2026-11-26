package server

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
		return "", fmt.Errorf("this project's folder is unavailable")
	}
	roots, found, err := crewStorageRoots("Crew/" + rec.Folder)
	if err == nil && !found {
		err = fmt.Errorf("this project's folder is unavailable")
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
// Team of several chosen folders gets the Crew's files root.
func crewTeamMemberFilesPath(user, root string) string {
	files := crewFilesPath(root)
	if !crewIsCoOwner(root, user) {
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
	if len(t.Folder.Folders) != 1 {
		return files
	}
	if roots, found, err := crewStorageRoots(root); err == nil && found {
		_ = os.MkdirAll(filepath.Join(roots.Files, filepath.FromSlash(t.Folder.Folders[0])), 0o770)
	}
	return path.Join(files, t.Folder.Folders[0])
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
