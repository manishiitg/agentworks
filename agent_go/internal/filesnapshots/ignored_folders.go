package filesnapshots

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ignoredFolders is which folders in the top two levels of the project the
// project's own .gitignore rules leave out (#3878 "Ignore" on the Git row).
// Walks skip them, so they are neither counted toward the file limit nor
// searched for checkouts. Loose files deeper down were already filtered by the
// same rules (filterLoosePaths); this only stops walking what is ignored.
// Git decides the matching, through the private store, as for loose files.
func (s Store) ignoredFolders(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if _, err := os.Stat(filepath.Join(s.Root, ".gitignore")); err != nil {
		return out
	}
	if _, err := os.Stat(filepath.Join(s.gitDir(), "HEAD")); err != nil {
		return out // no private store yet: loose files are still filtered after the walk
	}
	var folders []string
	for f := range s.layoutFolders() {
		folders = append(folders, f+"/")
	}
	if len(folders) == 0 {
		return out
	}
	cmd := s.command(ctx, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(folders, "\x00") + "\x00")
	raw, _ := cmd.Output() // exit 1: nothing ignored; other failures ignore nothing here
	for _, p := range bytes.Split(raw, []byte{0}) {
		if p := strings.TrimSuffix(string(p), "/"); p != "" {
			out[p] = true
		}
	}
	return out
}

// ignoreSignature changes when the project's root .gitignore changes, so the
// stored too-many-files verdict is decided again (file_limit.go).
func (s Store) ignoreSignature() string {
	info, err := os.Stat(filepath.Join(s.Root, ".gitignore"))
	if err != nil {
		return ""
	}
	return strconv.FormatInt(info.ModTime().UnixNano(), 36) + ":" + strconv.FormatInt(info.Size(), 10)
}

// underIgnored reports whether rel is an ignored folder or inside one.
func underIgnored(ignored map[string]bool, rel string) bool {
	for p := rel; p != "." && p != ""; p = filepath.ToSlash(filepath.Dir(p)) {
		if ignored[p] {
			return true
		}
	}
	return false
}
