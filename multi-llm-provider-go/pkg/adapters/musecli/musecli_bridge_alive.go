package musecli

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// museProc is one row of the process table: its parent and its command name.
type museProc struct {
	ppid int
	comm string
}

// museHasDescendantNamed reports whether any process below root (children, grandchildren, ...) has the given
// command name. Pure over the table so it can be tested without real processes.
func museHasDescendantNamed(root int, procs map[int]museProc, name string) bool {
	children := map[int][]int{}
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	seen := map[int]bool{root: true}
	queue := []int{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if seen[child] {
				continue
			}
			seen[child] = true
			if procs[child].comm == name {
				return true
			}
			queue = append(queue, child)
		}
	}
	return false
}

// museBridgeAlive reports whether the retained Muse session in tmux still has its mcpbridge process. Muse drops
// the MCP connection for good when one of its tool calls hits its own time limit (the bridge exits and the
// session answers every later call with "MCP stdio connection is closed"), so a retained session without a bridge
// can never recover on its own. known is false when this cannot be told (no tmux pane, not Linux): the caller must
// then leave the session alone rather than restart it on a guess.
func museBridgeAlive(ctx context.Context, tmuxName string) (alive, known bool) {
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-t", tmuxName, "-F", "#{pane_pid}").Output()
	if err != nil {
		return false, false
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return false, false
	}
	pane, err := strconv.Atoi(fields[0])
	if err != nil || pane <= 1 {
		return false, false
	}
	procs, ok := museReadProcessTable()
	if !ok {
		return false, false
	}
	if _, running := procs[pane]; !running {
		return false, false
	}
	return museHasDescendantNamed(pane, procs, "mcpbridge"), true
}
