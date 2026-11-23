//go:build linux

package musecli

import (
	"os"
	"strconv"
	"strings"
)

// museReadProcessTable reads every process's parent and command name from /proc.
func museReadProcessTable() (map[int]museProc, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	procs := make(map[int]museProc, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue // exited, or not readable: skipped like any other missing process
		}
		// "pid (comm) state ppid ...": comm may hold spaces and parentheses, so split at the last ')'.
		text := string(raw)
		open, closing := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
		if open < 0 || closing < open {
			continue
		}
		rest := strings.Fields(text[closing+1:])
		if len(rest) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(rest[1])
		if err != nil {
			continue
		}
		procs[pid] = museProc{ppid: ppid, comm: text[open+1 : closing]}
	}
	return procs, len(procs) > 0
}
