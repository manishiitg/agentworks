package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// backupCrewFolders keeps a plain backup of Crew folders and transfer metadata.
func backupCrewFolders(roots crewroot.Roots) (string, error) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(state, "crew-backups")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	paths := []string{}
	for _, path := range []string{roots.Files, roots.Resources, roots.Private} {
		if _, e := os.Stat(path); path != "" && e == nil && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(state, "crew-sync", roots.ID+"*"))
	paths = append(paths, matches...)
	target := filepath.Join(dir, roots.ID+"-"+time.Now().UTC().Format("20060102T150405Z")+".tar.gz")
	// Unreadable caches (a slot's own sandbox files) are skipped; the archive itself must be written.
	out, err := exec.Command("tar", append([]string{"czf", target, "--ignore-failed-read"}, paths...)...).CombinedOutput()
	if info, statErr := os.Stat(target); statErr != nil || info.Size() == 0 {
		return "", fmt.Errorf("tar: %v %s", err, strings.TrimSpace(string(out)))
	}
	return target, nil
}
