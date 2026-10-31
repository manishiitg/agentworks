// Package crewsync transfers authored project content between two admitted Crew
// roots. Private state, provider logins and native conversations never enter it.
package crewsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type File struct {
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}
type Manifest map[string]File
type Change struct {
	Path   string `json:"path"`
	Before *File  `json:"before,omitempty"`
	After  *File  `json:"after,omitempty"`
	Data   []byte `json:"data,omitempty"`
}
type Snapshot struct {
	Git     *GitSnapshot `json:"git,omitempty"`
	Files   Manifest     `json:"files"`
	Changes []Change     `json:"changes"`
}

// Both send and receive use this policy. Project ignore rules are additionally
// evaluated at the source. Runtime outputs are not authored source.
func Allowed(key string, directory bool) bool { return crewroot.SyncContentAllowed(key, directory) }

func rootFor(roots crewroot.Roots, key string) (string, string, error) {
	if !Allowed(key, false) {
		return "", "", fmt.Errorf("this file is not project content: %s", key)
	}
	area, rel, _ := strings.Cut(key, "/")
	if area == "files" {
		return roots.Files, rel, nil
	}
	return roots.Resources, rel, nil
}

func fingerprint(data []byte, executable bool) File {
	sum := sha256.Sum256(data)
	return File{Hash: hex.EncodeToString(sum[:]), Size: int64(len(data)), Executable: executable}
}

func readFile(root, rel string) ([]byte, File, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, File{}, err
	}
	defer dir.Close()
	info, err := dir.Lstat(rel)
	if err != nil {
		return nil, File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
		return nil, File{}, fmt.Errorf("unsupported file or file larger than 64 MiB: %s", rel)
	}
	f, err := dir.Open(rel)
	if err != nil {
		return nil, File{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, publication.MaxFileBytes+1))
	if err != nil || int64(len(data)) > publication.MaxFileBytes {
		return nil, File{}, fmt.Errorf("cannot read project file: %s", rel)
	}
	return data, fingerprint(data, info.Mode()&0111 != 0), nil
}

// Capture sends only changed bytes. Expected fingerprints are retained for
// every deletion/replacement so edits outside AgentWorks are never overwritten.
func Capture(ctx context.Context, roots crewroot.Roots, previous Manifest) (Snapshot, error) {
	out := Snapshot{Files: Manifest{}, Changes: []Change{}}
	var total int64
	ignoredFiles := map[string]bool{}
	areas := []struct{ name, root string }{{"files", roots.Files}, {"resources", roots.Resources}}
	if !roots.SeparateFiles() {
		areas = areas[1:]
	}
	for _, area := range areas {
		ignored, err := publication.GitIgnored(ctx, area.root)
		if err != nil {
			return out, err
		}
		for rel := range ignored {
			ignoredFiles[area.name+"/"+rel] = true
		}
		err = filepath.WalkDir(area.root, func(p string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == area.root {
				return nil
			}
			rel, _ := filepath.Rel(area.root, p)
			rel = filepath.ToSlash(rel)
			key := area.name + "/" + rel
			// The resources tree is captured independently; never descend through it twice.
			if (area.name == "files" && p == roots.Resources) || !Allowed(key, entry.IsDir()) || ignored[rel] {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			data, file, err := readFile(area.root, rel)
			if err != nil {
				return err
			}
			total += file.Size
			if total > publication.MaxBytes || len(out.Files) >= publication.MaxFiles {
				return fmt.Errorf("project exceeds 256 MiB or 10,000 files; exclude generated files before syncing")
			}
			out.Files[key] = file
			before, exists := previous[key]
			if !exists || before != file {
				change := Change{Path: key, After: &file, Data: data}
				if exists {
					change.Before = &before
				}
				out.Changes = append(out.Changes, change)
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	for key, before := range previous {
		if _, exists := out.Files[key]; !exists {
			// Keep the last reconciled version while excluded. If this file is
			// included again, its unchanged destination is still a valid base.
			if ignoredFiles[key] {
				out.Files[key] = before
				continue
			}
			// If exclusions changed, leave the old copy alone rather than sending
			// a mutation for a path the current policy no longer admits.
			if Allowed(key, false) {
				out.Changes = append(out.Changes, Change{Path: key, Before: &before})
			}
		}
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Path < out.Changes[j].Path })
	return out, nil
}

func checkChange(roots crewroot.Roots, change Change) error {
	root, rel, err := rootFor(roots, change.Path)
	if err != nil {
		return err
	}
	if _, err = crewroot.ResolveContained(root, rel); err != nil {
		return err
	}
	_, actual, err := readFile(root, rel)
	if os.IsNotExist(err) {
		if change.Before == nil || change.After == nil {
			return nil
		}
		return fmt.Errorf("%s was removed here; keep or resolve your changes before syncing", change.Path)
	}
	if err != nil {
		return err
	}
	// A retry after a partial transfer can safely accept an already-applied file.
	if change.After != nil && actual == *change.After {
		return nil
	}
	if change.Before == nil || actual != *change.Before {
		return fmt.Errorf("%s changed here; keep or resolve your changes before syncing", change.Path)
	}
	return nil
}

// Apply validates the complete batch before its first write. Callers serialize
// this with the existing project mutation lock and save its manifest on success.
func Apply(ctx context.Context, roots crewroot.Roots, snapshot Snapshot) error {
	if len(snapshot.Changes) > publication.MaxFiles || len(snapshot.Files) > publication.MaxFiles {
		return fmt.Errorf("too many project files")
	}
	seen := map[string]bool{}
	var total int64
	for _, change := range snapshot.Changes {
		if seen[change.Path] {
			return fmt.Errorf("duplicate project file")
		}
		seen[change.Path] = true
		if change.After != nil {
			total += int64(len(change.Data))
			if int64(len(change.Data)) > publication.MaxFileBytes || total > publication.MaxBytes || fingerprint(change.Data, change.After.Executable) != *change.After || snapshot.Files[change.Path] != *change.After {
				return fmt.Errorf("invalid project file content")
			}
		} else if _, exists := snapshot.Files[change.Path]; exists {
			return fmt.Errorf("deleted file remains in snapshot")
		}
		if err := checkChange(roots, change); err != nil {
			return err
		}
	}
	if err := CheckGit(ctx, roots.Files, snapshot.Git); err != nil {
		return err
	}
	if err := ApplyGit(ctx, roots.Files, snapshot.Git); err != nil {
		return err
	}
	for _, change := range snapshot.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkChange(roots, change); err != nil {
			return err
		}
		root, rel, _ := rootFor(roots, change.Path)
		dir, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		if change.After == nil {
			err = dir.Remove(rel)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = dir.MkdirAll(path.Dir(rel), 0770)
			if err == nil {
				mode := os.FileMode(0660)
				if change.After.Executable {
					mode = 0770
				}
				var f *os.File
				f, err = dir.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
				if err == nil {
					info, statErr := f.Stat()
					err = statErr
					if err == nil && (info.Mode()&0111 != 0) != change.After.Executable {
						targetMode := info.Mode().Perm() &^ 0111
						if change.After.Executable {
							targetMode |= 0110
						}
						err = f.Chmod(targetMode)
					}
					if err == nil {
						_, err = f.Write(change.Data)
					}
					closeErr := f.Close()
					if err == nil {
						err = closeErr
					}
				}
			}
		}
		dir.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
