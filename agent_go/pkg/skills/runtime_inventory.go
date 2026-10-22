package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

type RuntimeFile struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
	Error      string `json:"error,omitempty"`
}

type RuntimeSkill struct {
	Skill
	Files []RuntimeFile `json:"files"`
}

type RuntimeInventory struct {
	Skills   []RuntimeSkill `json:"skills"`
	Folders  []string       `json:"folders"`
	Warnings []string       `json:"warnings"`
}

// ReadRuntimeInventory reads physical projection files, without registry defaults,
// name filters, deduplication, or traversing the linked project/CLI home caches.
// Projection roots are discovered by their directory structure, not provider or
// skill names: skills/, .skills/, and <provider-directory>/skills/.
func ReadRuntimeInventory(dir string) (*RuntimeInventory, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result := &RuntimeInventory{Skills: []RuntimeSkill{}, Folders: []string{}, Warnings: []string{}}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		candidate := entry.Name()
		if candidate != "skills" && candidate != ".skills" {
			candidate = path.Join(candidate, "skills")
		}
		info, err := root.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			result.Warnings = append(result.Warnings, candidate+": "+err.Error())
			continue
		}
		if !info.IsDir() {
			result.Warnings = append(result.Warnings, candidate+": not a regular skill directory")
			continue
		}
		result.Folders = append(result.Folders, candidate)
		var documents []string
		err = fs.WalkDir(root.FS(), candidate, func(file string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				result.Warnings = append(result.Warnings, file+": "+walkErr.Error())
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				result.Warnings = append(result.Warnings, file+": symbolic link is not traversed")
				return nil
			}
			if !d.IsDir() && d.Name() == "SKILL.md" {
				documents = append(documents, file)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		for _, document := range documents {
			folder := path.Dir(document)
			name := path.Base(folder)
			body, readErr := readRuntimeText(root, document)
			skill, parseErr := ParseSkillFromContent(body, name, document)
			if parseErr != nil {
				skill = &Skill{Frontmatter: SkillFrontmatter{Name: name}, FolderName: name, FilePath: document, Content: body, ValidationError: parseErr.Error()}
			}
			if readErr != nil {
				skill.ValidationError = readErr.Error()
			}
			item := RuntimeSkill{Skill: *skill, Files: []RuntimeFile{}}
			_ = fs.WalkDir(root.FS(), folder, func(file string, d fs.DirEntry, err error) error {
				if file == folder {
					return nil
				}
				if err != nil {
					item.Files = append(item.Files, RuntimeFile{Path: file, Error: err.Error()})
					return nil
				}
				if d.IsDir() {
					return nil
				}
				f := RuntimeFile{Path: file}
				if d.Type()&os.ModeSymlink != 0 {
					f.Error = "Symbolic link is not opened"
				} else if info, err := d.Info(); err != nil {
					f.Error = err.Error()
				} else {
					f.Size = info.Size()
					f.ModifiedAt = info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
				}
				item.Files = append(item.Files, f)
				return nil
			})
			result.Skills = append(result.Skills, item)
		}
	}
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].FilePath < result.Skills[j].FilePath })
	return result, nil
}

func readRuntimeText(root *os.Root, file string) (string, error) {
	f, err := root.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	const limit = 2 << 20
	if info.Size() > limit {
		return "", fmt.Errorf("file exceeds the 2 MiB reader limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", fmt.Errorf("file exceeds the 2 MiB reader limit")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", fmt.Errorf("binary file; text preview unavailable")
	}
	return string(data), nil
}

// ReadRuntimeSkillFile only opens a file present in the current skill inventory.
// OpenRoot also prevents a path/symlink replacement from escaping this runtime.
func ReadRuntimeSkillFile(dir, file string, inventory *RuntimeInventory) (string, error) {
	for _, skill := range inventory.Skills {
		for _, item := range skill.Files {
			if item.Path != file {
				continue
			}
			if item.Error != "" {
				return "", fmt.Errorf("%s", item.Error)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				return "", err
			}
			defer root.Close()
			return readRuntimeText(root, file)
		}
	}
	return "", fmt.Errorf("file is not in this session's skill inventory")
}
