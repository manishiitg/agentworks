package codexcli

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live output of Codex commands, during their turn and after the reply ended,
// and stopping a command that outlived its turn (#3131). Codex keeps running a
// command past its turn (a dev server, a long build) and keeps sending its
// output deltas; the turn consumer and the idle reader both hand them here.
// The late completion still reaches the agent through the rollout file
// (PLAT-746-899); this only shows the output and lets a person stop it.

// CodexBackgroundCommand is a command that outlived its turn and still runs.
type CodexBackgroundCommand struct {
	ID        string    // the command's tool call (Codex item id)
	Label     string    // the command line, as Codex reported it
	StartedAt time.Time // when its item started
}

type codexRunningCommand struct {
	id, processID, label, turn, thread string
	rpc                                *codexRPC
	startedAt                          time.Time
	detached                           bool
}

type codexCommands struct {
	mu      sync.Mutex
	owner   string
	output  *llmtypes.BackgroundOutputStream
	running map[string]*codexRunningCommand
	watched map[*codexRPC]bool
}

// codexCommandStopTimeout bounds one terminate request.
var codexCommandStopTimeout = 10 * time.Second

func (s *codexAppSession) commandTracker() *codexCommands {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commands == nil {
		s.commands = &codexCommands{owner: s.owner, output: llmtypes.NewBackgroundOutputStream("codex-cli", s.owner), running: map[string]*codexRunningCommand{}, watched: map[*codexRPC]bool{}}
	}
	return s.commands
}

// observeCodexCommandEvent follows command items and their output deltas of
// this thread, whichever turn they belong to. It reports whether msg was one.
func observeCodexCommandEvent(s *codexAppSession, r *codexRPC, method string, params json.RawMessage) bool {
	switch method {
	case "item/commandExecution/outputDelta", "item/started", "item/completed":
	default:
		return false
	}
	var event codexAppEvent
	if json.Unmarshal(params, &event) != nil {
		return false
	}
	s.mu.Lock()
	thread := s.threadID
	s.mu.Unlock()
	if event.ThreadID == "" || event.ThreadID != thread {
		return false
	}
	t := s.commandTracker()
	if method == "item/commandExecution/outputDelta" {
		if event.ItemID == "" {
			return false
		}
		t.mu.Lock()
		taskID := ""
		if c := t.running[event.ItemID]; c != nil && c.detached {
			taskID = c.id
		}
		t.mu.Unlock()
		t.output.Write(event.ItemID, taskID, event.Delta)
		return true
	}
	var item struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Command   string          `json:"command"`
		ProcessID json.RawMessage `json:"processId"`
		Status    string          `json:"status"`
	}
	if json.Unmarshal(event.Item, &item) != nil || item.Type != "commandExecution" || item.ID == "" {
		return false
	}
	// Codex reports the process id as a string; accept a number too.
	processID := strings.Trim(strings.TrimSpace(string(item.ProcessID)), `"`)
	if processID == "null" {
		processID = ""
	}
	if method == "item/completed" || (item.Status != "" && item.Status != "inProgress") {
		t.mu.Lock()
		delete(t.running, item.ID)
		t.mu.Unlock()
		t.output.End(item.ID)
		return true
	}
	t.mu.Lock()
	if _, known := t.running[item.ID]; !known {
		t.running[item.ID] = &codexRunningCommand{id: item.ID, processID: processID, label: item.Command, turn: event.TurnID, thread: event.ThreadID, rpc: r, startedAt: time.Now()}
	}
	watch := r != nil && !t.watched[r]
	if watch {
		t.watched[r] = true
	}
	t.mu.Unlock()
	if watch {
		// Codex's commands end with its process.
		go func() {
			<-r.done
			t.endProcess(r)
		}()
	}
	return true
}

// detachTurn marks the turn's still-running commands as background commands
// once the turn ended: a person can now stop them on their own.
func (t *codexCommands) detachTurn(turn string) {
	if t == nil || turn == "" {
		return
	}
	t.mu.Lock()
	var detached []*codexRunningCommand
	for _, c := range t.running {
		if c.turn == turn && !c.detached && c.processID != "" {
			c.detached = true
			detached = append(detached, c)
		}
	}
	t.mu.Unlock()
	for _, c := range detached {
		t.output.Detach(c.id, c.id)
	}
	if len(detached) > 0 {
		log.Printf("[CODEX_BACKGROUND] owner=%s action=detached commands=%d turn=%s", t.owner, len(detached), turn)
		go t.settleExited(detached)
	}
}

// settleExited ends the detached commands Codex no longer runs. Codex can
// start a command and never report it finished, which left it listed as
// running for good (#4602); its live terminals say which still run. If Codex
// can't answer, nothing changes.
func (t *codexCommands) settleExited(detached []*codexRunningCommand) {
	r := detached[0].rpc
	if r == nil {
		return
	}
	live := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), codexCommandStopTimeout)
	defer cancel()
	for cursor := ""; ; {
		params := map[string]any{"threadId": detached[0].thread}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ProcessID string `json:"processId"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err := r.call(ctx, "thread/backgroundTerminals/list", params, &page); err != nil {
			log.Printf("[CODEX_BACKGROUND] owner=%s action=settle_skipped reason=%q", t.owner, err.Error())
			return
		}
		for _, terminal := range page.Data {
			live[terminal.ProcessID] = true
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	var ended []string
	t.mu.Lock()
	for _, c := range detached {
		if !live[c.processID] && t.running[c.id] == c {
			delete(t.running, c.id)
			ended = append(ended, c.id)
		}
	}
	t.mu.Unlock()
	for _, id := range ended {
		t.output.End(id)
	}
	if len(ended) > 0 {
		log.Printf("[CODEX_BACKGROUND] owner=%s action=settled_exited commands=%d", t.owner, len(ended))
	}
}

func (t *codexCommands) endProcess(r *codexRPC) {
	t.mu.Lock()
	delete(t.watched, r)
	var ended []string
	for id, c := range t.running {
		if c.rpc == r {
			delete(t.running, id)
			ended = append(ended, id)
		}
	}
	t.mu.Unlock()
	for _, id := range ended {
		t.output.End(id)
	}
}

func (t *codexCommands) background() []CodexBackgroundCommand {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []CodexBackgroundCommand
	for _, c := range t.running {
		if c.detached {
			out = append(out, CodexBackgroundCommand{ID: c.id, Label: c.label, StartedAt: c.startedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// CodexBackgroundCommands lists the commands of this chat that outlived
// their turn and still run.
func CodexBackgroundCommands(owner string) []CodexBackgroundCommand {
	s := codexAppSessionForOwner(strings.TrimSpace(owner))
	if s == nil {
		return nil
	}
	s.mu.Lock()
	t := s.commands
	s.mu.Unlock()
	return t.background()
}

// StopCodexBackgroundCommand stops one command of this chat that outlived its
// turn (Codex thread/backgroundTerminals/terminate). False when the chat has
// no such command or Codex did not stop it.
func StopCodexBackgroundCommand(owner, id string) bool {
	s := codexAppSessionForOwner(strings.TrimSpace(owner))
	if s == nil {
		return false
	}
	s.mu.Lock()
	t, thread := s.commands, s.threadID
	s.mu.Unlock()
	if t == nil {
		return false
	}
	t.mu.Lock()
	c := t.running[id]
	var command codexRunningCommand
	if c != nil {
		command = *c
	}
	t.mu.Unlock()
	if c == nil || !command.detached || command.processID == "" || command.rpc == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexCommandStopTimeout)
	defer cancel()
	var result struct {
		Terminated bool `json:"terminated"`
	}
	err := command.rpc.call(ctx, "thread/backgroundTerminals/terminate", map[string]any{"threadId": thread, "processId": command.processID}, &result)
	if err != nil {
		log.Printf("[CODEX_BACKGROUND] owner=%s command=%s action=stop_failed reason=%q", t.owner, id, err.Error())
		return false
	}
	log.Printf("[CODEX_BACKGROUND] owner=%s command=%s action=stopped terminated=%t", t.owner, id, result.Terminated)
	if result.Terminated {
		t.mu.Lock()
		delete(t.running, id)
		t.mu.Unlock()
		t.output.End(id)
	}
	return result.Terminated
}
