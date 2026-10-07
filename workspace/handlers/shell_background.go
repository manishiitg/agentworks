package handlers

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// shellBackgroundGrace is how long a command's output may stay open after its shell has exited before the
// command is treated as having left something running in the background (PLAT-645). Ordinary commands close
// their output with the shell; only a process the command started in the background (`server &`, a daemon
// that keeps its stdout) holds it longer.
var shellBackgroundGrace = 500 * time.Millisecond

// shellOutputCapture reads one of a command's output streams from a pipe the service made itself. Go's own
// pipes (cmd.Stdout = &buffer) make cmd.Wait wait until every process holding the pipe has closed it, so a
// command with a background job never returned until that job ended, and the chat looked stuck. With our own
// pipe, Wait returns when the shell exits; this reader keeps draining for as long as a background process writes
// (so it never blocks on a full pipe), but once the call has returned it stops keeping the output.
type shellOutputCapture struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	detached bool
	done     chan struct{}
}

func newShellOutputCapture(r *os.File) *shellOutputCapture {
	c := &shellOutputCapture{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer r.Close()
		chunk := make([]byte, 32*1024)
		for {
			n, err := r.Read(chunk)
			if n > 0 {
				c.mu.Lock()
				if !c.detached {
					c.buf.Write(chunk[:n])
				}
				c.mu.Unlock()
			}
			if err != nil { // io.EOF once every holder has closed it
				return
			}
		}
	}()
	return c
}

func (c *shellOutputCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// detach stops keeping output: what was read so far is the call's result, later output is discarded.
func (c *shellOutputCapture) detach() {
	c.mu.Lock()
	c.detached = true
	c.mu.Unlock()
}

// waitShellOutputs waits up to grace for every capture to reach end of file; false means some process still holds
// the command's output (it was left running in the background).
func waitShellOutputs(grace time.Duration, captures ...*shellOutputCapture) bool {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	for _, c := range captures {
		select {
		case <-c.done:
		case <-deadline.C:
			return false
		}
	}
	return true
}

// shellProcessGroupMembers lists the live processes in process group pgid (the command's own group: a job started
// with `&` by a non-interactive shell stays in it).
func shellProcessGroupMembers(pgid int) []int {
	if pgid <= 0 {
		return nil
	}
	out, err := exec.Command("ps", "-Ao", "pid=,pgid=").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		group, err2 := strconv.Atoi(fields[1])
		if err1 == nil && err2 == nil && group == pgid && pid != pgid {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// shellBackgroundNotice is what the caller (and the model) is told when a command left processes running.
func shellBackgroundNotice(pids []int, slotted bool) string {
	who := "Find them with `ps` or `pgrep`"
	if len(pids) > 0 {
		parts := make([]string, len(pids))
		for i, pid := range pids {
			parts[i] = strconv.Itoa(pid)
		}
		who = fmt.Sprintf("pid %s; stop with `kill %s`", strings.Join(parts, ", "), strings.Join(parts, " "))
	} else if slotted {
		who = "they run as your account; find them with `ps` or `pgrep`"
	}
	return "[background] The command finished, but process(es) it started in the background are still running (" + who +
		"). This shell is free: later commands run right away. Output they write from now on is not captured here; " +
		"to read it later, start them with their output sent to a file (for example `cmd > server.log 2>&1 &`).\n"
}
