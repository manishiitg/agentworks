package llmtypes

import (
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// StreamChunkTypeBackgroundOutput carries live output of a command a coding
// agent started, while it runs: during its turn and after the reply ended
// (#3131). The payload is StreamChunk.BackgroundOutput. Adapters deliver it
// through EmitBackgroundOutput, because a command can outlive the turn whose
// stream started it; no turn stream is open between turns.
const StreamChunkTypeBackgroundOutput StreamChunkType = "background_output"

// Bounds of one command's live output. A chunk holds at most
// BackgroundOutputChunkMax bytes (the newest output is kept and Dropped says
// earlier output was cut), and a command emits at most one chunk per
// BackgroundOutputInterval, so a chatty command costs a bounded tail, never
// its whole log. The final result stays the CLI's own tool result.
const (
	BackgroundOutputChunkMax = 8 * 1024
	BackgroundOutputInterval = 500 * time.Millisecond
	// backgroundOutputMaxCommands bounds the commands one stream follows.
	backgroundOutputMaxCommands = 64
)

// BackgroundOutput is one provider-neutral chunk of a running command's
// output.
type BackgroundOutput struct {
	// Provider is the adapter ("codex-cli", "claude-code", "cursor-cli").
	Provider string `json:"provider"`
	// Owner is the host's chat (the adapter's session owner).
	Owner string `json:"owner"`
	// ToolCallID is the command's tool call: the card the output belongs to.
	ToolCallID string `json:"tool_call_id"`
	// TaskID is the provider's own handle to stop the command (Claude task
	// id, Codex item id), empty when it cannot be stopped on its own.
	TaskID string `json:"task_id,omitempty"`
	// Text is the output written since the previous chunk.
	Text string `json:"text,omitempty"`
	// Dropped: output before Text was cut to stay within the bounds.
	Dropped bool `json:"dropped,omitempty"`
	// Done: the command ended; no more output follows.
	Done bool `json:"done,omitempty"`
	// CanStop: the command runs on its own (its turn ended) and TaskID stops it.
	CanStop bool `json:"can_stop,omitempty"`
}

// BackgroundOutputChunk wraps output as a typed stream chunk.
func BackgroundOutputChunk(output BackgroundOutput) StreamChunk {
	return StreamChunk{Type: StreamChunkTypeBackgroundOutput, ToolCallID: output.ToolCallID, Content: output.Text, BackgroundOutput: &output}
}

var backgroundOutputListener atomic.Pointer[func(BackgroundOutput)]

// SetBackgroundOutputListener receives every adapter's live command output,
// during turns and between them. The host shows it; nothing is decided there.
func SetBackgroundOutputListener(listener func(BackgroundOutput)) {
	if listener == nil {
		backgroundOutputListener.Store(nil)
		return
	}
	backgroundOutputListener.Store(&listener)
}

// EmitBackgroundOutput hands one chunk to the host, bounded to its tail.
func EmitBackgroundOutput(output BackgroundOutput) {
	if output.Owner == "" || output.ToolCallID == "" {
		return
	}
	if len(output.Text) > BackgroundOutputChunkMax {
		output.Text, output.Dropped = BackgroundOutputTail(output.Text, BackgroundOutputChunkMax), true
	}
	if listener := backgroundOutputListener.Load(); listener != nil {
		(*listener)(output)
	}
}

// BackgroundOutputTail keeps the last max bytes of text, starting on a whole
// UTF-8 character.
func BackgroundOutputTail(text string, max int) string {
	if len(text) <= max {
		return text
	}
	tail := text[len(text)-max:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return tail
}

// BackgroundOutputStream coalesces small output deltas of one chat's commands
// into bounded chunks: at most one per command per interval, each holding the
// newest BackgroundOutputChunkMax bytes.
type BackgroundOutputStream struct {
	provider, owner string
	interval        time.Duration
	emit            func(BackgroundOutput)

	mu      sync.Mutex
	pending map[string]*backgroundOutputPending
	order   []string
	timer   *time.Timer
	ignored map[string]bool
}

type backgroundOutputPending struct {
	taskID  string
	text    []byte
	dropped bool
	canStop bool
	// announce: CanStop changed and the host has not been told yet.
	announce bool
	done     bool
}

// NewBackgroundOutputStream follows one chat's commands for one adapter.
func NewBackgroundOutputStream(provider, owner string) *BackgroundOutputStream {
	return &BackgroundOutputStream{provider: provider, owner: owner, interval: BackgroundOutputInterval, emit: EmitBackgroundOutput, pending: map[string]*backgroundOutputPending{}, ignored: map[string]bool{}}
}

// Write adds output of one command.
func (s *BackgroundOutputStream) Write(toolCallID, taskID, text string) {
	if s == nil || toolCallID == "" || text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entryLocked(toolCallID)
	if p == nil {
		return
	}
	if taskID != "" {
		p.taskID = taskID
	}
	p.text = append(p.text, text...)
	if len(p.text) > BackgroundOutputChunkMax {
		p.text = []byte(BackgroundOutputTail(string(p.text), BackgroundOutputChunkMax))
		p.dropped = true
	}
	s.scheduleLocked()
}

// Detach says the command now runs on its own: its turn ended and taskID can
// stop it. The next chunk tells the host so, even with no new output.
func (s *BackgroundOutputStream) Detach(toolCallID, taskID string) {
	if s == nil || toolCallID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.entryLocked(toolCallID)
	if p == nil {
		return
	}
	p.taskID, p.canStop, p.announce = taskID, taskID != "", true
	s.scheduleLocked()
}

// End flushes the command's output with Done set.
func (s *BackgroundOutputStream) End(toolCallID string) {
	if s == nil || toolCallID == "" {
		return
	}
	s.mu.Lock()
	p := s.pending[toolCallID]
	delete(s.ignored, toolCallID)
	if p == nil {
		s.mu.Unlock()
		return
	}
	p.done = true
	s.mu.Unlock()
	s.Flush()
}

// Flush emits every pending chunk now.
func (s *BackgroundOutputStream) Flush() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var out []BackgroundOutput
	var keep []string
	for _, id := range s.order {
		p := s.pending[id]
		if p == nil {
			continue
		}
		if len(p.text) > 0 || p.done || p.announce {
			out = append(out, BackgroundOutput{Provider: s.provider, Owner: s.owner, ToolCallID: id, TaskID: p.taskID, Text: string(p.text), Dropped: p.dropped, Done: p.done, CanStop: p.canStop && !p.done})
		}
		if p.done {
			delete(s.pending, id)
			continue
		}
		p.text, p.dropped, p.announce = nil, false, false
		keep = append(keep, id)
	}
	s.order = keep
	emit := s.emit
	s.mu.Unlock()
	for _, chunk := range out {
		emit(chunk)
	}
}

// Running lists the commands the stream still follows.
func (s *BackgroundOutputStream) Running() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func (s *BackgroundOutputStream) entryLocked(toolCallID string) *backgroundOutputPending {
	if p := s.pending[toolCallID]; p != nil {
		return p
	}
	if s.ignored[toolCallID] || len(s.pending) >= backgroundOutputMaxCommands {
		s.ignored[toolCallID] = true
		return nil
	}
	p := &backgroundOutputPending{}
	s.pending[toolCallID] = p
	s.order = append(s.order, toolCallID)
	return p
}

func (s *BackgroundOutputStream) scheduleLocked() {
	if s.timer == nil {
		s.timer = time.AfterFunc(s.interval, s.Flush)
	}
}
