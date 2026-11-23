// Package pathidentity distinguishes filesystem identity from path spelling.
package pathidentity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Same compares existing filesystem objects, including symlinks and case aliases.
// Missing paths only match by absolute, cleaned spelling; case folding alone is
// never identity evidence on a filesystem that can hold both names.
func Same(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	if ae == nil && be == nil {
		return os.SameFile(ai, bi)
	}
	return Key(a) == Key(b)
}

// Resolve returns an existing path's physical directory-entry spelling. Unlike
// EvalSymlinks, it also normalizes case aliases on case-insensitive filesystems.
// It does not change the process cwd or case-fold distinct filesystem objects.
func Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty filesystem path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return Spelling(path)
}

// Spelling normalizes directory-entry case while retaining intentional symlink
// components. Use Resolve for identity keys; this is for legacy CLI trust keys.
func Spelling(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty filesystem path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(path); err != nil {
		return "", err
	}
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	current := root
	for _, part := range strings.Split(strings.TrimPrefix(path, root), string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return "", err
		}
		actual := ""
		for _, entry := range entries {
			if entry.Name() == part {
				actual = part
				break
			}
		}
		if actual == "" {
			target, err := os.Stat(filepath.Join(current, part))
			if err != nil {
				return "", err
			}
			for _, entry := range entries {
				// EvalSymlinks has already removed links; do not canonicalize through
				// a different symlink that happens to point at this object.
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				info, err := entry.Info()
				if err == nil && os.SameFile(target, info) {
					actual = entry.Name()
					break
				}
			}
		}
		if actual == "" {
			return "", fmt.Errorf("cannot resolve filesystem spelling of %s", path)
		}
		current = filepath.Join(current, actual)
	}
	return current, nil
}

// Key is a best-effort canonical map key. Use Resolve when failure must reject
// a path (for example a containment check), rather than permit a fallback.
func Key(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if resolved, err := Resolve(path); err == nil {
		return resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

// Candidates preserves legacy spellings alongside the physical path for CLI
// formats that encode the path in a JSON key or transcript directory name.
func Candidates(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	paths := []string{path}
	if absolute, err := filepath.Abs(path); err == nil {
		paths = append(paths, absolute)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		paths = append(paths, resolved)
	}
	if resolved, err := Resolve(path); err == nil {
		paths = append(paths, resolved)
	}
	var result []string
	seen := map[string]bool{}
	for _, p := range paths {
		if !seen[p] {
			result = append(result, p)
			seen[p] = true
		}
	}
	return result
}
