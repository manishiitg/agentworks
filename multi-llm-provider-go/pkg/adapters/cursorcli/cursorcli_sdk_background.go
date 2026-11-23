package cursorcli

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live output of Cursor's own background shells, and Stop for one (#3131).
// The bridge follows each shell's terminal file and reports "shell_output"
// lines, during a turn and between turns; Go hands them to the host as
// llmtypes.BackgroundOutput and keeps the chat's running shells so the host
// can list and stop them.

// cursorShellStopTimeout bounds how long Stop waits for the bridge's answer.
const cursorShellStopTimeout = 5 * time.Second

// CursorBackgroundCommand is one of a chat's Cursor shells running in the
// background.
type CursorBackgroundCommand struct {
	ID        string
	Label     string
	StartedAt time.Time
}

type cursorRunningShell struct {
	callID    string
	label     string
	startedAt time.Time
	proc      *cursorSDKProcess
}

var cursorShells = struct {
	sync.Mutex
	byOwner map[string]map[string]*cursorRunningShell
}{byOwner: map[string]map[string]*cursorRunningShell{}}

var cursorShellStopSeq atomic.Uint64

// recordCursorShellOutput keeps the shell's state and hands its output to the
// host. Cursor's background shells always run on their own, so a running one
// can be stopped.
func recordCursorShellOutput(owner string, p *cursorSDKProcess, event cursorSDKEvent) {
	owner = strings.TrimSpace(owner)
	if owner == "" || event.CallID == "" || event.ShellID == "" {
		return
	}
	cursorShells.Lock()
	shells := cursorShells.byOwner[owner]
	if shells == nil {
		shells = map[string]*cursorRunningShell{}
		cursorShells.byOwner[owner] = shells
	}
	_, known := shells[event.ShellID]
	if event.Done {
		delete(shells, event.ShellID)
		if len(shells) == 0 {
			delete(cursorShells.byOwner, owner)
		}
	} else if !known {
		shells[event.ShellID] = &cursorRunningShell{callID: event.CallID, label: event.Label, startedAt: time.Now(), proc: p}
	}
	cursorShells.Unlock()
	if !known && !event.Done {
		log.Printf("[CURSOR_BACKGROUND] owner=%s shell=%s call=%s action=following", owner, event.ShellID, event.CallID)
	}
	if event.Done {
		log.Printf("[CURSOR_BACKGROUND] owner=%s shell=%s call=%s action=ended", owner, event.ShellID, event.CallID)
	}
	llmtypes.EmitBackgroundOutput(llmtypes.BackgroundOutput{Provider: "cursor-cli", Owner: owner, ToolCallID: event.CallID, TaskID: event.ShellID,
		Text: event.Text, Dropped: event.Dropped, Done: event.Done, CanStop: !event.Done})
}

// endCursorShells ends the output of the shells a closed bridge followed:
// nothing reports on them any more.
func endCursorShells(owner string, p *cursorSDKProcess) {
	owner = strings.TrimSpace(owner)
	cursorShells.Lock()
	var ended []cursorSDKEvent
	for id, shell := range cursorShells.byOwner[owner] {
		if shell.proc == p {
			ended = append(ended, cursorSDKEvent{CallID: shell.callID, ShellID: id, Done: true})
		}
	}
	cursorShells.Unlock()
	for _, event := range ended {
		recordCursorShellOutput(owner, p, event)
	}
}

// CursorBackgroundCommands lists this chat's Cursor shells that run in the
// background.
func CursorBackgroundCommands(owner string) []CursorBackgroundCommand {
	cursorShells.Lock()
	defer cursorShells.Unlock()
	var out []CursorBackgroundCommand
	for id, shell := range cursorShells.byOwner[strings.TrimSpace(owner)] {
		out = append(out, CursorBackgroundCommand{ID: id, Label: shell.label, StartedAt: shell.startedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// StopCursorBackgroundCommand stops one of this chat's background shells.
// False when the chat has no such shell or the bridge could not signal it;
// its output ends when Cursor records the exit.
func StopCursorBackgroundCommand(owner, id string) bool {
	owner = strings.TrimSpace(owner)
	cursorShells.Lock()
	shell := cursorShells.byOwner[owner][id]
	var p *cursorSDKProcess
	if shell != nil {
		p = shell.proc
	}
	cursorShells.Unlock()
	if p == nil {
		return false
	}
	request := fmt.Sprintf("shell-stop-%d", cursorShellStopSeq.Add(1))
	ack := make(chan bool, 1)
	p.mu.Lock()
	p.acks[request] = ack
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.acks, request)
		p.mu.Unlock()
	}()
	if err := p.send(map[string]any{"type": "stop_shell", "id": request, "shell_id": id}); err != nil {
		log.Printf("[CURSOR_BACKGROUND] owner=%s shell=%s action=stop_failed reason=%q", owner, id, err.Error())
		return false
	}
	select {
	case stopped := <-ack:
		log.Printf("[CURSOR_BACKGROUND] owner=%s shell=%s action=stop signalled=%t", owner, id, stopped)
		return stopped
	case <-p.done:
	case <-time.After(cursorShellStopTimeout):
	}
	log.Printf("[CURSOR_BACKGROUND] owner=%s shell=%s action=stop_failed reason=no_answer", owner, id)
	return false
}
