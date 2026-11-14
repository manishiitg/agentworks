package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// Called under the existing Team writer lock. A Team rename reuses the normal
// chunk commit/history, including deletion events at the old path.
var errCrewTeamRenameForbidden = errors.New("This connection cannot rename those shared files.")

func crewTeamPathRenameChanges(project string, before, after crewTeamFolder, claims *UserClaims) ([]crewTeamChange, error) {
	changed := false
	for _, old := range before.Paths {
		for _, next := range after.Paths {
			if old.ID == next.ID && old.Path != next.Path {
				changed = true
			}
		}
	}
	if !changed {
		return nil, nil
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return nil, err
	}
	changes := []crewTeamChange{}
	skillNames := map[string]string{}
	for _, old := range before.Paths {
		for _, next := range after.Paths {
			if old.ID != next.ID || old.Path == next.Path {
				continue
			}
			if strings.HasPrefix(old.Path, "skills/") {
				skillNames[strings.TrimPrefix(old.Path, "skills/")] = strings.TrimPrefix(next.Path, "skills/")
			}
			for rel, v := range state.Versions {
				if v.File == nil || !crewTeamUnder(rel, old.Path) {
					continue
				}
				target := next.Path + strings.TrimPrefix(rel, old.Path)
				if !crewTeamGuardAllows(claims, rel, true) || !crewTeamGuardAllows(claims, target, true) {
					return nil, errCrewTeamRenameForbidden
				}
				if existing := state.Versions[target]; existing.File != nil {
					return nil, fmt.Errorf("the Team already has files at %s", target)
				}
				version := &chunkedFile{Hash: v.File.Hash, Size: v.File.Size, Exec: v.File.Executable, Chunks: v.Chunks}
				changes = append(changes, crewTeamChange{Path: rel, Before: v.File.Hash}, crewTeamChange{Path: target, After: version})
			}
		}
	}
	if len(skillNames) > 0 && state.Versions[crewTeamSetupFile].File != nil {
		if !crewTeamGuardAllows(claims, crewTeamSetupFile, false) || !crewTeamGuardAllows(claims, crewTeamSetupFile, true) {
			return nil, errCrewTeamRenameForbidden
		}
		root, e := crewTeamFilesRoot(project)
		if e != nil {
			return nil, e
		}
		raw, e := os.ReadFile(filepath.Join(root, crewTeamSetupFile))
		if e != nil {
			return nil, e
		}
		var setup crewTeamSetup
		if e = json.Unmarshal(raw, &setup); e != nil {
			return nil, e
		}
		updated := false
		for i, name := range setup.SelectedSkills {
			if next, ok := skillNames[name]; ok {
				setup.SelectedSkills[i] = next
				updated = true
			}
		}
		if updated {
			store, e := crewTeamChunkStore(project)
			if e != nil {
				return nil, e
			}
			raw = setup.encode()
			version, e := chunkReader(bytes.NewReader(raw), int64(len(raw)), false, func(id string, data []byte) error { return putChunk(store, id, data) })
			if e != nil {
				return nil, e
			}
			changes = append(changes, crewTeamChange{Path: crewTeamSetupFile, Before: state.Versions[crewTeamSetupFile].File.Hash, After: &version})
		}
	}
	return changes, nil
}

func applyCrewTeamPathRenames(ctx context.Context, project, actor string, changes []crewTeamChange) error {

	// Respect the protocol's bounded commits; a large rename is still one named
	// folder identity and ordinary file-history revisions.
	for len(changes) > 0 {
		count := min(len(changes), crewTeamCommitBatch)
		batch := changes[:count]
		raw, _ := json.Marshal(batch)
		if _, err := commitCrewTeamChanges(ctx, project, actor, newCrewTeamTransfer(), crewTeamSetupHash(raw), batch); err != nil {
			return err
		}
		changes = changes[count:]
	}
	return nil
}

// Owner-path identities let an ordinary same-parent filesystem rename keep its
// server-issued ID. A member's local rename is restored instead (reconcileMounts).
func (b *crewTeamLocal) reconcileOwnedPaths(ctx context.Context, owner string, c desktopConnection, team *crewTeam) error {
	if b.Mounted || c.User.ID != team.OwnerID {
		return nil
	}
	if b.OwnedPaths == nil {
		b.OwnedPaths = map[string]crewTeamMount{}
	}
	previous := maps.Clone(b.OwnedPaths)
	changed := false
	for i, entry := range team.Folder.Paths {
		old := b.OwnedPaths[entry.ID]
		root := b.diskRoot(entry.Path)
		rel := entry.Path
		p, err := crewTeamMountPath(root, rel)
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if old.Local != "" && old.Local != entry.Path {
			from, e := crewTeamMountPath(root, old.Local)
			if e != nil {
				return e
			}
			if oldInfo, e := os.Lstat(from); e == nil {
				stamp := crewsync.StampOf(oldInfo)
				if stamp.Inode != old.Inode || stamp.Device != old.Device {
					return fmt.Errorf("the original shared folder was replaced; reconnect it in Team")
				}
				if err == nil {
					return fmt.Errorf("the renamed Team folder would replace an existing local path")
				}
				if !os.IsNotExist(err) {
					return err
				}
				if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
					return e
				}
				if e = os.Rename(from, p); e != nil {
					return e
				}
				info, err = os.Lstat(p)
			}
		}
		if os.IsNotExist(err) && old.Inode != 0 && old.Remote == entry.Path {
			siblings, _ := os.ReadDir(filepath.Dir(p))
			for _, sibling := range siblings {
				candidate := filepath.Join(filepath.Dir(p), sibling.Name())
				si, e := os.Lstat(candidate)
				if e != nil || si.Mode()&os.ModeSymlink != 0 {
					continue
				}
				stamp := crewsync.StampOf(si)
				if stamp.Device != old.Device || stamp.Inode != old.Inode {
					continue
				}
				name, e := filepath.Rel(root, candidate)
				if e != nil {
					return e
				}
				name = filepath.ToSlash(name)
				if !crewTeamAllowed(name + "/file.txt") {
					return fmt.Errorf("the new shared folder name is private")
				}
				team.Folder.Paths[i].Path = name
				for j, folder := range team.Folder.Folders {
					if folder == entry.Path {
						team.Folder.Folders[j] = name
					}
				}
				changed = true
				rel = name
				info = si
				err = nil
				break
			}
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if info != nil {
			stamp := crewsync.StampOf(info)
			b.OwnedPaths[entry.ID] = crewTeamMount{Remote: rel, Local: rel, Device: stamp.Device, Inode: stamp.Inode}
		}
	}
	saved := *team
	if changed {
		if err := crewTeamRemoteJSON(ctx, owner, c, http.MethodPut, "/api/crew-teams/"+url.PathEscape(team.ProjectID), *team, &saved); err != nil {
			return err
		}
	}
	for _, p := range saved.Folder.Paths {
		for rel, v := range b.Files {
			old := previous[p.ID]
			// Rekey baselines by the identity's previous acknowledged path.
			if old.Local != "" && old.Local != p.Path && crewTeamUnder(rel, old.Local) {
				delete(b.Files, rel)
				b.Files[p.Path+strings.TrimPrefix(rel, old.Local)] = v
			}
		}
	}
	*team = saved
	return nil
}
