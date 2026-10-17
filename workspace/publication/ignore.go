package publication

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git's own ignore matcher avoids a second, subtly different .gitignore parser.
// No hooks, fsmonitor or project executable is run by this inspection.
func GitIgnored(ctx context.Context, root string) map[string]bool {
	out := map[string]bool{}
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if Excluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			paths = append(paths, rel)
		}
		return nil
	})
	cmd := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=", "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	if ignored, err := cmd.Output(); err == nil {
		for _, p := range bytes.Split(ignored, []byte{0}) {
			if len(p) > 0 {
				out[string(p)] = true
			}
		}
	}
	return out
}
