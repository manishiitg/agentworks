package tmuxcontrol

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// reapGraceTimeout is how long we wait after SIGTERM before SIGKILLing
// survivors of a reaped process tree.
const reapGraceTimeout = 500 * time.Millisecond

// panePIDs returns the foreground process PID of every pane in sessionName.
// Returns nil if the session is gone or tmux has no server.
func panePIDs(ctx context.Context, sessionName string) []int {
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-t", sessionName, "-F", "#{pane_pid}").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(field)); convErr == nil && pid > 1 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// processTree returns root plus every transitive child PID, parents first.
// Walks the live process table via `pgrep -P`, so it captures grandchildren
// (e.g. MCP-server node processes a CLI spawned) that are not direct panes.
func processTree(ctx context.Context, root int) []int {
	seen := map[int]bool{}
	order := make([]int, 0, 8)
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 1 || seen[pid] {
			continue
		}
		seen[pid] = true
		order = append(order, pid)
		out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid)).Output()
		if err != nil {
			continue // no children, or pgrep unavailable
		}
		for _, field := range strings.Fields(string(out)) {
			if child, convErr := strconv.Atoi(strings.TrimSpace(field)); convErr == nil {
				queue = append(queue, child)
			}
		}
	}
	return order
}

// ReapSessionProcessTree force-kills the full process tree backing every pane of
// sessionName. `tmux kill-session` only delivers SIGHUP to each pane's
// foreground process; any children that process spawned (most importantly the
// MCP-server node subprocesses a coding-agent CLI launches) survive, reparent to
// launchd/init, and leak — accumulating across sessions and backend restarts.
//
// This resolves pane PID -> descendants from the live process table and SIGTERMs
// then SIGKILLs them so nothing outlives the session. Best-effort: a missing
// session, dead PIDs, or absent pgrep are all ignored. Call BEFORE kill-session,
// while the panes (and thus the PID linkage) still exist.
func ReapSessionProcessTree(ctx context.Context, sessionName string) {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return
	}
	var all []int
	for _, root := range panePIDs(ctx, sessionName) {
		all = append(all, processTree(ctx, root)...)
	}
	if len(all) == 0 {
		return
	}
	for _, pid := range all {
		_ = termProcess(pid)
	}
	timer := time.NewTimer(reapGraceTimeout)
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	timer.Stop()
	// Reverse order: children before parents, so grandchildren can't briefly
	// reparent to init between their parent's death and their own.
	for i := len(all) - 1; i >= 0; i-- {
		_ = killProcess(all[i])
	}
}

// listSessions returns all tmux session names on the default server, or nil when
// no server is running.
func listSessions(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			names = append(names, s)
		}
	}
	return names
}

// SweepInteractiveSessions reaps the process tree of, and kills, every tmux
// session whose name starts with one of prefixes. It returns the number of
// sessions swept.
//
// Intended for startup recovery: an ungraceful backend exit (crash, SIGKILL,
// machine sleep) leaves coding-agent tmux sessions and their orphaned process
// trees behind, and the in-process registries that the CleanupX* paths rely on
// are empty on a fresh boot — so they find nothing. This locates leftovers by
// name prefix instead.
//
// CAUTION: prefix matching also catches sessions owned by a *concurrent* backend
// or test sharing the same tmux server. Only call when single-instance ownership
// is guaranteed (e.g. the desktop app on a user's machine), not from library or
// test code.
func SweepInteractiveSessions(ctx context.Context, prefixes []string) int {
	swept := 0
	for _, name := range listSessions(ctx) {
		for _, prefix := range prefixes {
			if prefix == "" || !strings.HasPrefix(name, prefix) {
				continue
			}
			ReapSessionProcessTree(ctx, name)
			_ = exec.CommandContext(ctx, "tmux", "kill-session", "-t", name).Run()
			swept++
			break
		}
	}
	return swept
}
