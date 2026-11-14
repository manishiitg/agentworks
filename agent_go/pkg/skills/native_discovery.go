package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// NativeSource is supplied by admitted project/account discovery, never by a
// browser path. OpenRoot keeps skill references inside that source.
type NativeSource struct {
	Root, Directory, Label, Prefix string
}

type DiscoveredSkill struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Source        string   `json:"source"`
	Directory     string   `json:"-"`
	Root          string   `json:"-"`
	Document      string   `json:"document,omitempty"`
	Files         []string `json:"files,omitempty"`
	UserInvocable bool     `json:"user_invocable"`
}

// DiscoverNativeSkills reads SKILL.md only; it does not install, execute or
// claim that the CLI loaded anything. Disabled/user-only policy is evaluated
// separately by the provider's invocation resolver.
func DiscoverNativeSkills(sources []NativeSource) []DiscoveredSkill {
	out := []DiscoveredSkill{}
	seen := map[string]bool{}
	for _, source := range sources {
		root, err := os.OpenRoot(source.Root)
		if err != nil {
			continue
		}
		if !nativeOrdinaryPath(root, source.Directory) {
			root.Close()
			continue
		}
		entries, err := fs.ReadDir(root.FS(), source.Directory)
		if err != nil {
			root.Close()
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			dir := path.Join(source.Directory, entry.Name())
			if !nativeOrdinaryPath(root, path.Join(dir, SkillFileName)) {
				continue
			}
			file, err := root.Open(path.Join(dir, SkillFileName))
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(file, 256*1024+1))
			file.Close()
			if err != nil || len(data) > 256*1024 {
				continue
			}
			front, _, err := ParseSkillFile(string(data))
			if err != nil {
				continue
			}
			name := front.Name
			if name == "" {
				name = entry.Name()
			}
			if _, err := ValidateSkillName(entry.Name()); err != nil {
				continue
			}
			hash := sha256.Sum256([]byte(source.Root + "\x00" + dir + "\x00" + source.Prefix))
			id := hex.EncodeToString(hash[:16])
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, DiscoveredSkill{ID: id, Name: source.Prefix + name, Description: front.Description, Source: source.Label, Root: source.Root, Directory: dir, UserInvocable: front.UserInvocable == nil || *front.UserInvocable})
		}
		root.Close()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Source < out[j].Source
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ReadDiscoveredSkill is shared by preview and explicit import. It preserves
// references/scripts as bytes, rejects links and never executes skill content.
func ReadDiscoveredSkill(skill DiscoveredSkill) (map[string][]byte, error) {
	root, err := os.OpenRoot(skill.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if !nativeOrdinaryPath(root, skill.Directory) {
		return nil, fmt.Errorf("skill folder changed; refresh discovery")
	}
	files := map[string][]byte{}
	total := 0
	err = fs.WalkDir(root.FS(), skill.Directory, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill contains a link; import a folder with ordinary files instead")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("skill contains a special file")
		}
		if len(files) >= 256 {
			return fmt.Errorf("skill has more than 256 files")
		}
		f, err := root.Open(file)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, 10*1024*1024+1))
		f.Close()
		if err != nil {
			return err
		}
		total += len(data)
		if total > 10*1024*1024 {
			return fmt.Errorf("skill is larger than 10 MB")
		}
		rel := file[len(skill.Directory)+1:]
		files[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, _, err := ParseSkillFile(string(files[SkillFileName])); err != nil {
		return nil, err
	}
	return files, nil
}

func nativeOrdinaryPath(root *os.Root, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}
