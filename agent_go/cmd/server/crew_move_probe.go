package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
)

// PLAT-442 step 4: before a Crew moves, nothing may be using it. The move refuses a Crew that a live tmux session or a
// running process works in, directly or through a CLI runtime folder whose project link points at the Crew (a Crew CLI
// starts in such a runtime folder, not in the Crew's own). A runtime folder with no live process is only informational:
// the move repoints its link.

// crewActivityProbe reports, per Crew folder name, what is live in it. crews maps the folder name to the Crew's
// absolute (symlink-resolved) path; stateRoot is the CLI runtime state root.
type crewActivityProbe func(ctx context.Context, stateRoot string, crews map[string]string) (map[string][]string, error)

// crewRuntimeFolders lists the CLI runtime folders whose project link points at each Crew.
func crewRuntimeFolders(stateRoot string, crews map[string]string) map[string][]string {
	out := map[string][]string{}
	for folder, abs := range crews {
		if dirs, err := cliruntime.ProjectLinksTo(stateRoot, abs); err == nil && len(dirs) > 0 {
			out[folder] = dirs
		}
	}
	return out
}

// defaultCrewActivityProbe looks at tmux panes and at the working directory of every process.
func defaultCrewActivityProbe(ctx context.Context, stateRoot string, crews map[string]string) (map[string][]string, error) {
	runtimes := crewRuntimeFolders(stateRoot, crews)
	// Compare resolved paths: a process reports its real working directory, a path in the state root may not be one
	// (macOS: /var is /private/var).
	real := func(p string) string {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return resolved
		}
		return p
	}
	owners := func(dir string) (string, bool) {
		dir = filepath.Clean(dir)
		for folder, abs := range crews {
			if crewMoveWithin(abs, dir) || crewMoveWithin(real(abs), dir) {
				return folder, true
			}
			for _, runtime := range runtimes[folder] {
				if crewMoveWithin(runtime, dir) || crewMoveWithin(real(runtime), dir) {
					return folder, true
				}
			}
		}
		return "", false
	}
	activity := map[string][]string{}
	if panes, err := runProbe(ctx, "tmux", "list-panes", "-a", "-F", "#{session_name}\t#{pane_current_path}"); err == nil {
		for _, line := range strings.Split(panes, "\n") {
			session, dir, found := strings.Cut(strings.TrimSpace(line), "\t")
			if !found || dir == "" {
				continue
			}
			if folder, ok := owners(dir); ok {
				activity[folder] = append(activity[folder], fmt.Sprintf("tmux session %q has a pane in %s", session, dir))
			}
		}
	}
	for _, p := range processWorkingDirs(ctx) {
		if p.pid == os.Getpid() {
			continue
		}
		if folder, ok := owners(p.dir); ok {
			activity[folder] = append(activity[folder], fmt.Sprintf("process %s (pid %d) works in %s", p.command, p.pid, p.dir))
		}
	}
	return activity, nil
}

func crewMoveWithin(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type processDir struct {
	pid     int
	command string
	dir     string
}

// processWorkingDirs lists the working directory of every process this account can see: /proc where it exists, else lsof.
func processWorkingDirs(ctx context.Context) []processDir {
	var out []processDir
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			dir, err := os.Readlink(filepath.Join("/proc", entry.Name(), "cwd"))
			if err != nil {
				continue
			}
			command, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
			out = append(out, processDir{pid: pid, command: strings.TrimSpace(string(command)), dir: dir})
		}
		if len(out) > 0 {
			return out
		}
	}
	listing, err := runProbe(ctx, "lsof", "-nP", "-d", "cwd", "-Fpcn")
	if err != nil {
		return nil
	}
	var current processDir
	scanner := bufio.NewScanner(strings.NewReader(listing))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			current = processDir{}
			current.pid, _ = strconv.Atoi(line[1:])
		case 'c':
			current.command = line[1:]
		case 'n':
			current.dir = line[1:]
			out = append(out, current)
		}
	}
	return out
}

// runProbe runs a read-only command and returns its output; tests replace it (tmux and lsof are not theirs to touch).
var runProbe = func(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
