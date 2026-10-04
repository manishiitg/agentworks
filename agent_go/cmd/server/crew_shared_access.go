package server

import (
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// crewSharedRootEnabled is the server-side switch for where NEW crews are created (PLAT-442 step 4):
// AGENTWORKS_CREW_SHARED_ROOT=on creates them at Crew/<slug>-<id8>; anything else (the default) keeps creating them in
// the owner's tree. Reading and resolving Crew/<id> never depends on it: a server is safe half-migrated, flag on or
// off. Turn it on only after the server's migration has been applied and verified.
func crewSharedRootEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_CREW_SHARED_ROOT")), "on")
}

// crewCreationRoot is the folder new Crews of a profile are created in: the shared root when the switch is on and the
// profile is the Crew product, else the owner's projects root (runtimeRoot, as before).
func crewCreationRoot(profileID, runtimeRoot string) string {
	if strings.EqualFold(strings.TrimSpace(profileID), crewProfileID) && crewSharedRootEnabled() {
		return workspaceref.SharedCrewRoot
	}
	return runtimeRoot
}

// sharedCrewRootMode is the mode of the Crew/ folder itself: other accounts may traverse it (a slot account must
// reach its owner's Crew below it) but not list it; each Crew's own folder carries its owner's slot group and no
// access for anyone else, exactly as a folder in the owner's tree does (deploy/common/provision-slots.sh).
const sharedCrewRootMode fs.FileMode = 0o711

// ensureSharedCrewRoot creates the Crew/ folder with the mode above when it does not exist (best effort: the
// workspace service creates it too, with the default mode, when a Crew is first created through the API).
func ensureSharedCrewRoot(docsRoot string) error {
	root := filepath.Join(docsRoot, workspaceref.SharedCrewRoot)
	if err := os.MkdirAll(root, sharedCrewRootMode); err != nil {
		return err
	}
	return os.Chmod(root, sharedCrewRootMode)
}

// ensureSharedCrewFolderAccess gives a Crew created at Crew/<folder> the access its owner's tree would have given it:
// the owner's slot group, group read/write, setgid folders, nothing for anyone else. Without it a Crew created by the
// app account at the shared root would be owned by the app's own group and the owner's slot account (the bridge shell
// tool runs as the caller's slot) could not write in it, or, with a lax umask, every slot could read it. A host without
// slots, or an owner without a slot, needs nothing. Best effort and logged: the app account keeps working either way.
func ensureSharedCrewFolderAccess(folder, ownerID string) {
	slot := slotHeldBy(ownerID)
	if slot == "" {
		return
	}
	group, err := user.LookupGroup(slot)
	if err != nil {
		log.Printf("[CREW_ACCESS] %s: group %q of the owner's slot is unknown: %v", folder, slot, err)
		return
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return
	}
	docsRoot := fsutil.WorkspaceDocsRoot()
	if err := ensureSharedCrewRoot(docsRoot); err != nil {
		log.Printf("[CREW_ACCESS] cannot prepare %s: %v", workspaceref.SharedCrewRoot, err)
	}
	root := filepath.Join(docsRoot, workspaceref.SharedCrewRoot, folder)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		if err := os.Lchown(path, -1, gid); err != nil {
			return err
		}
		mode := info.Mode().Perm()&^0o007 | 0o060
		if entry.IsDir() {
			mode = 0o770 | fs.ModeSetgid
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		log.Printf("[CREW_ACCESS] %s: could not give the owner's slot %s access: %v", root, slot, err)
	}
}
