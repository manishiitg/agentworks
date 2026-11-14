package server

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Identity belongs to the Team store. A path is its current name, never its key.
type crewTeamSharedPath struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	File bool   `json:"file,omitempty"`
}

type crewTeamFolderWire struct {
	PathIDsVersion int                  `json:"path_ids_version"`
	ID             string               `json:"id"`
	Whole          bool                 `json:"whole,omitempty"`
	Folders        []crewTeamSharedPath `json:"folders,omitempty"`
	Paths          []crewTeamSharedPath `json:"paths,omitempty"`
}

func (f crewTeamFolder) MarshalJSON() ([]byte, error) {
	w := crewTeamFolderWire{PathIDsVersion: 1, ID: f.ID, Whole: f.Whole, Paths: f.Paths}
	for _, name := range f.Folders {
		entry := crewTeamSharedPath{Path: name}
		for _, p := range f.Paths {
			if p.Path == name {
				entry.ID = p.ID
				break
			}
		}
		w.Folders = append(w.Folders, entry)
	}
	return json.Marshal(w)
}

// There is deliberately no string-list decoder. Existing servers are converted
// once, with a backup, before running this schema.
func (f *crewTeamFolder) UnmarshalJSON(raw []byte) error {
	var w crewTeamFolderWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	*f = crewTeamFolder{ID: w.ID, Whole: w.Whole, Paths: w.Paths}
	for _, p := range w.Folders {
		f.Folders = append(f.Folders, p.Path)
		if at := slices.IndexFunc(f.Paths, func(q crewTeamSharedPath) bool { return p.ID != "" && q.ID == p.ID }); at >= 0 {
			f.Paths[at].Path = p.Path
			continue
		}
		if !slices.ContainsFunc(f.Paths, func(q crewTeamSharedPath) bool { return q.Path == p.Path }) {
			f.Paths = append(f.Paths, p)
		}
	}
	return nil
}

// Settings may retain a server-issued identity when the owner renames it. New
// identities are always issued here; clients cannot choose or recycle an ID.
func (f *crewTeamFolder) assignPaths(previous crewTeamFolder, owner bool) error {
	requested := slices.Clone(f.Paths)
	f.Paths = slices.Clone(previous.Paths)
	seen := map[string]bool{}
	for _, p := range requested {
		if p.ID == "" {
			continue
		}
		if seen[p.ID] {
			return fmt.Errorf("Choose each shared folder identity once.")
		}
		seen[p.ID] = true
		at := slices.IndexFunc(previous.Paths, func(q crewTeamSharedPath) bool { return q.ID == p.ID })
		if at < 0 || previous.Paths[at].Path == p.Path {
			continue
		}
		old := previous.Paths[at]
		if !owner {
			return fmt.Errorf("Only the Team owner renames shared folders and skills.")
		}
		if !crewTeamAllowed(p.Path+"/file.txt") || !(crewTeamFolder{Whole: true}).holds(p.Path+"/file.txt") || crewTeamUnder(p.Path, old.Path) || crewTeamUnder(old.Path, p.Path) {
			return fmt.Errorf("Choose a separate ordinary folder name.")
		}
		if strings.HasPrefix(old.Path, "skills/") != strings.HasPrefix(p.Path, "skills/") {
			return fmt.Errorf("A shared skill stays in the skills folder.")
		}
		if slices.ContainsFunc(f.Paths, func(q crewTeamSharedPath) bool { return q.ID != p.ID && q.Path == p.Path }) {
			return fmt.Errorf("The Team already has a folder with that name.")
		}
		f.Paths[at].Path = p.Path
	}
	for _, name := range f.Folders {
		if !crewTeamAllowed(name + "/file.txt") {
			return fmt.Errorf("Choose an ordinary shared folder.")
		}
		if slices.ContainsFunc(f.Paths, func(p crewTeamSharedPath) bool { return p.Path == name }) {
			continue
		}
		id := ""
		for _, p := range requested {
			if p.Path == name {
				id = p.ID
				break
			}
		}
		at := slices.IndexFunc(f.Paths, func(p crewTeamSharedPath) bool { return id != "" && p.ID == id })
		if at >= 0 {
			if !owner {
				return fmt.Errorf("Only the Team owner renames shared folders.")
			}
			f.Paths[at].Path = name
		} else {
			f.Paths = append(f.Paths, crewTeamSharedPath{ID: uuid.NewString(), Path: name})
		}
	}
	return nil
}

func (f *crewTeamFolder) addFilePaths(paths []string) bool {
	changed := false
	for _, rel := range paths {
		if !f.holds(rel) || crewTeamSetupPath(rel) && !strings.HasPrefix(rel, "skills/") {
			continue
		}
		name := ""
		if strings.HasPrefix(rel, "skills/") {
			rest := strings.TrimPrefix(rel, "skills/")
			skill, _, _ := strings.Cut(rest, "/")
			name = "skills/" + skill
		} else if f.Whole {
			name, _, _ = strings.Cut(rel, "/")
		}
		if name == "" || slices.ContainsFunc(f.Paths, func(p crewTeamSharedPath) bool { return p.Path == name }) {
			continue
		}
		f.Paths = append(f.Paths, crewTeamSharedPath{ID: uuid.NewString(), Path: name, File: rel == name})
		changed = true
	}
	return changed
}

func (f crewTeamFolder) mounts() []crewTeamSharedPath {
	out := []crewTeamSharedPath{}
	for _, p := range f.Paths {
		if p.Path == "skills" || f.Whole && strings.Contains(p.Path, "/") && !strings.HasPrefix(p.Path, "skills/") {
			continue
		}
		if p.ID != "" && (f.Whole || slices.Contains(f.Folders, p.Path) || strings.HasPrefix(p.Path, "skills/")) {
			out = append(out, p)
		}
	}
	return out
}
