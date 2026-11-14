package server

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type crewTeamMount struct {
	Remote string `json:"remote"`
	Local  string `json:"local"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func (b crewTeamLocal) separateSkills() bool {
	return b.SkillRoot != "" && filepath.Clean(b.SkillRoot) != filepath.Join(b.Root, "skills")
}

func (b crewTeamLocal) diskRoot(remote string) string {
	if b.SkillRoot != "" && crewTeamUnder(remote, "skills") {
		return filepath.Dir(b.SkillRoot)
	}
	return b.Root
}

func (b *crewTeamLocal) setSkillRoot(root string) {
	previous := b.SkillRoot
	if previous == "" {
		previous = filepath.Join(b.Root, "skills")
	}
	if filepath.Clean(previous) != filepath.Clean(root) {
		for id, m := range b.Mounts {
			if crewTeamUnder(m.Remote, "skills") {
				delete(b.Mounts, id)
			}
		}
		for rel := range b.Files {
			if crewTeamUnder(rel, "skills") {
				delete(b.Files, rel)
			}
		}
		b.Downloaded = false
		b.Revision = 0
	}
	b.SkillRoot = root
}

func crewTeamUnder(rel, prefix string) bool {
	return rel == prefix || strings.HasPrefix(rel, prefix+"/")
}

// diskRel and remoteRel translate only admitted mounts and the two shared setup
// files. An unmapped private sibling has no remote path and can never upload.
func (b crewTeamLocal) diskRel(rel string) string {
	if !b.Mounted {
		return rel
	}
	best, result := "", ""
	for _, m := range b.Mounts {
		if crewTeamUnder(rel, m.Remote) && len(m.Remote) > len(best) {
			best = m.Remote
			result = m.Local + strings.TrimPrefix(rel, m.Remote)
		}
	}
	if result != "" {
		return result
	}
	if rel == "PROJECT_INSTRUCTIONS.md" || rel == crewTeamSetupFile {
		return rel
	}
	if !b.ProtectPrivate {
		return rel
	}
	return ""
}

func (b crewTeamLocal) remoteRel(rel string) string {
	if !b.Mounted {
		return rel
	}
	best, result := "", ""
	for _, m := range b.Mounts {
		if crewTeamUnder(rel, m.Local) && len(m.Local) > len(best) {
			best = m.Local
			result = m.Remote + strings.TrimPrefix(rel, m.Local)
		}
	}
	if result != "" {
		return result
	}
	if rel == "PROJECT_INSTRUCTIONS.md" || rel == crewTeamSetupFile {
		return rel
	}
	for _, m := range b.Mounts {
		if m.Remote != m.Local && crewTeamUnder(rel, m.Remote) {
			return ""
		}
	}
	if !b.ProtectPrivate {
		return rel
	}
	return ""
}

func (b crewTeamLocal) walksDisk(rel string, share crewTeamFolder) bool {
	if !b.Mounted {
		return share.walks(rel)
	}
	if remote := b.remoteRel(rel); remote != "" && share.walks(remote) {
		return true
	}
	return slices.ContainsFunc(b.activeMounts(share), func(m crewTeamMount) bool { return strings.HasPrefix(m.Local, rel+"/") })
}

func (b crewTeamLocal) activeMounts(share crewTeamFolder) []crewTeamMount {
	out := []crewTeamMount{}
	for _, p := range share.mounts() {
		if m, ok := b.Mounts[p.ID]; ok {
			out = append(out, m)
		}
	}
	return out
}

func (b crewTeamLocal) diskPurpose() crewTeamOnPurpose {
	op := crewTeamOnPurpose{}
	for rel := range b.onPurpose() {
		if local := b.diskRel(rel); local != "" {
			op[local] = true
		}
	}
	return op
}

func (b crewTeamLocal) checkIgnore(ctx context.Context, paths []string) (map[string]bool, error) {
	if !b.Mounted {
		return crewTeamCheckIgnore(ctx, b.Root, paths)
	}
	out := map[string]bool{}
	for _, m := range b.Mounts {
		group := []string{}
		for _, p := range paths {
			if strings.HasPrefix(p, m.Local+"/") {
				group = append(group, strings.TrimPrefix(p, m.Local+"/"))
			}
		}
		ignored, err := crewTeamCheckIgnore(ctx, filepath.Join(b.Root, filepath.FromSlash(m.Local)), group)
		if err != nil {
			return nil, err
		}
		for p, v := range ignored {
			out[m.Local+"/"+p] = v
		}
	}
	return out, nil
}

// Refuse links in every existing parent. A collision must never make sync follow
// a private link out of the registered project root.
func crewTeamMountPath(root, rel string) (string, error) {
	if _, err := publication.CleanPath(rel); err != nil || !crewsync.Allowed(crewTeamKey(rel), false) || !(crewTeamFolder{Whole: true}).holds(rel) {
		return "", fmt.Errorf("invalid Team folder path")
	}
	current := root
	for _, part := range strings.Split(rel, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("the Team folder path contains a link")
		}
		if current != filepath.Join(root, filepath.FromSlash(rel)) && !info.IsDir() {
			return "", fmt.Errorf("the Team folder's parent is a file")
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func crewTeamFreeMount(root, remote, current string) (string, error) {
	for n := 0; n < 1000; n++ {
		candidate := remote
		if n == 1 {
			candidate += " (team)"
		} else if n > 1 {
			candidate += fmt.Sprintf(" (team %d)", n)
		}
		if candidate == current {
			return candidate, nil
		}
		p, err := crewTeamMountPath(root, candidate)
		if err != nil {
			return "", err
		}
		if current != "" {
			if existing, e := os.Lstat(p); e == nil {
				if own, e := os.Lstat(filepath.Join(root, filepath.FromSlash(current))); e == nil && os.SameFile(existing, own) {
					return candidate, nil
				}
			}
		}
		if _, err = os.Lstat(p); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("choose a project with room for the Team folder names")
}

// reconcileMounts runs before any upload. A missing/renamed member folder cannot
// be mistaken for deleting the Team's contents. The Team's identity/name wins.
func (b *crewTeamLocal) reconcileMounts(share crewTeamFolder) error {
	if !b.Mounted {
		return nil
	}
	if b.Mounts == nil {
		b.Mounts = map[string]crewTeamMount{}
	}
	active := map[string]bool{}
	for _, entry := range share.mounts() {
		active[entry.ID] = true
		root := b.diskRoot(entry.Path)
		old, had := b.Mounts[entry.ID]
		if had && old.Inode != 0 && !entry.File {
			if info, e := os.Lstat(filepath.Join(root, filepath.FromSlash(old.Local))); e == nil {
				stamp := crewsync.StampOf(info)
				if stamp.Inode != old.Inode || stamp.Device != old.Device {
					// A different local folder at the same spelling is private. Its
					// bytes must never inherit this Team identity or its upload base.
					delete(b.Mounts, entry.ID)
					had = false
					old = crewTeamMount{}
					for rel := range b.Files {
						if crewTeamUnder(rel, entry.Path) {
							delete(b.Files, rel)
						}
					}
					b.Downloaded = false
					b.Revision = 0
				}
			}
		}
		if !had && !b.ProtectPrivate {
			for rel := range b.Files {
				if crewTeamUnder(rel, entry.Path) {
					old.Local = entry.Path
					break
				}
			}
		}
		local, err := crewTeamFreeMount(root, entry.Path, old.Local)
		if err != nil {
			return err
		}
		to, err := crewTeamMountPath(root, local)
		if err != nil {
			return err
		}
		if had {
			from, err := crewTeamMountPath(root, old.Local)
			if err != nil {
				return err
			}
			info, statErr := os.Lstat(from)
			if os.IsNotExist(statErr) && old.Inode != 0 {
				// A same-parent local rename is restored using its physical identity,
				// never by matching names or file contents.
				siblings, _ := os.ReadDir(filepath.Dir(from))
				for _, sibling := range siblings {
					p := filepath.Join(filepath.Dir(from), sibling.Name())
					if si, e := os.Lstat(p); e == nil && si.Mode()&os.ModeSymlink == 0 {
						stamp := crewsync.StampOf(si)
						if stamp.Inode == old.Inode && stamp.Device == old.Device {
							from = p
							info = si
							statErr = nil
							break
						}
					}
				}
			}
			if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			}
			if statErr == nil && from != to {
				if err = os.MkdirAll(filepath.Dir(to), 0755); err != nil {
					return err
				}
				if err = os.Rename(from, to); err != nil {
					return err
				}
			}
			if info == nil {
				// Re-download rather than upload deletes when a member moved a mount
				// somewhere outside its parent or removed the whole local folder.
				for rel := range b.Files {
					if crewTeamUnder(rel, old.Remote) {
						delete(b.Files, rel)
					}
				}
				b.Downloaded = false
				b.Revision = 0
			}
			if old.Remote != entry.Path {
				for rel, v := range b.Files {
					if crewTeamUnder(rel, old.Remote) {
						delete(b.Files, rel)
						b.Files[entry.Path+strings.TrimPrefix(rel, old.Remote)] = v
					}
				}
			}
		}
		// Files in Whole Crew can be roots as well; the incoming writer creates
		// files, so only create the parent here.
		if err = os.MkdirAll(filepath.Dir(to), 0755); err != nil {
			return err
		}
		if !entry.File {
			if err = os.MkdirAll(to, 0755); err != nil {
				return err
			}
		}
		next := crewTeamMount{Remote: entry.Path, Local: local}
		if info, e := os.Lstat(to); e == nil {
			stamp := crewsync.StampOf(info)
			next.Inode, next.Device = stamp.Inode, stamp.Device
		}
		b.Mounts[entry.ID] = next
	}
	for id, m := range b.Mounts {
		if active[id] {
			continue
		}
		delete(b.Mounts, id)
		for rel := range b.Files {
			if crewTeamUnder(rel, m.Remote) {
				delete(b.Files, rel)
			}
		}
	}
	return nil
}

func crewTeamMountParent(rel string) string {
	if parent := path.Dir(rel); parent != "." {
		return parent
	}
	return ""
}
