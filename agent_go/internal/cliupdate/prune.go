package cliupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// versionStores are the self-updaters that download each release beside the
// previous ones and never delete the old ones (cursor-agent keeps every
// release, ~0.5 GB each). Only these exact layouts are pruned: a generic
// "versions" directory can be a node version manager's installs.
var versionStores = map[string]string{
	"cursor-agent": filepath.Join("cursor-agent", "versions"),
	"claude":       filepath.Join("claude", "versions"),
}

// runningCommandLines lists every process's command line; a release a live
// chat still runs must survive the prune. Replaced in tests.
var runningCommandLines = func(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "args=").Output()
	return string(out), err
}

// pruneOldVersions keeps one release: the one the verified executable
// resolves to. Releases a running process still uses are kept until a later
// check finds them idle. Returns the removed release names.
func pruneOldVersions(ctx context.Context, provider, executable string) ([]string, error) {
	store, ok := versionStores[provider]
	if !ok || executable == "" {
		return nil, nil
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, nil
	}
	marker := string(filepath.Separator) + store + string(filepath.Separator)
	i := strings.Index(resolved, marker)
	if i < 0 {
		return nil, nil
	}
	versionsDir := resolved[:i+len(marker)-1]
	current := strings.SplitN(resolved[i+len(marker):], string(filepath.Separator), 2)[0]
	entries, err := os.ReadDir(versionsDir)
	if err != nil || current == "" {
		return nil, err
	}
	var stale []string
	for _, entry := range entries {
		if name := entry.Name(); name != current && !strings.HasPrefix(name, ".") {
			stale = append(stale, name)
		}
	}
	if len(stale) == 0 {
		return nil, nil
	}
	running, err := runningCommandLines(ctx)
	if err != nil {
		return nil, nil // cannot prove a release is idle: keep everything
	}
	var removed []string
	for _, name := range stale {
		path := filepath.Join(versionsDir, name)
		if strings.Contains(running, path) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}
