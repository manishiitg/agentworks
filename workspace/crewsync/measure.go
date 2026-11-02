package crewsync

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// Measure counts what a first sync would send, without reading file contents,
// so the owner sees the size against the limits before turning Cloud on
// (PLAT-856-509 readiness). It walks with Capture's rules; a test keeps them equal.
type Measurement struct {
	Files    int          `json:"files"`
	Bytes    int64        `json:"bytes"`
	Largest  []FolderSize `json:"largest_folders,omitempty"`
	TooLarge []string     `json:"files_over_limit,omitempty"`
	// Dependency files whose packages the server installs itself (node_modules and the like never sync).
	Dependencies []string `json:"dependency_files,omitempty"`
	MaxFiles     int      `json:"max_files"`
	MaxBytes     int64    `json:"max_bytes"`
	MaxFile      int64    `json:"max_file_bytes"`
	byFolder     map[string]int64
}

type FolderSize struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

func Measure(ctx context.Context, roots crewroot.Roots) (Measurement, error) {
	out := Measurement{MaxFiles: publication.MaxFiles, MaxBytes: publication.MaxBytes, MaxFile: publication.MaxFileBytes, byFolder: map[string]int64{}}
	areas := []struct{ name, root string }{{"files", roots.Files}, {"resources", roots.Resources}}
	if !roots.SeparateFiles() {
		areas = areas[1:]
	}
	for _, area := range areas {
		ignored, err := publication.GitIgnored(ctx, area.root)
		if err != nil {
			return out, err
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
			if (area.name == "files" && p == roots.Resources) || !Allowed(area.name+"/"+rel, entry.IsDir()) || ignored[rel] {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			out.Files++
			out.Bytes += info.Size()
			if info.Size() > publication.MaxFileBytes && len(out.TooLarge) < 10 {
				out.TooLarge = append(out.TooLarge, rel)
			}
			switch path.Base(rel) {
			case "package.json", "requirements.txt", "pyproject.toml", "Pipfile", "go.mod", "Gemfile", "Cargo.toml", "composer.json":
				if len(out.Dependencies) < 10 {
					out.Dependencies = append(out.Dependencies, rel)
				}
			}
			top, _, _ := strings.Cut(rel, "/")
			if top != rel {
				out.byFolder[top] += info.Size()
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	for path, size := range out.byFolder {
		out.Largest = append(out.Largest, FolderSize{path, size})
	}
	sort.Slice(out.Largest, func(i, j int) bool { return out.Largest[i].Bytes > out.Largest[j].Bytes })
	if len(out.Largest) > 5 {
		out.Largest = out.Largest[:5]
	}
	return out, nil
}

// Fits reports whether a first sync is within today's limits.
func (m Measurement) Fits() bool {
	return m.Files <= m.MaxFiles && m.Bytes <= m.MaxBytes && len(m.TooLarge) == 0
}
