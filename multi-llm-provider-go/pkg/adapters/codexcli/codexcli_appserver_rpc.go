package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Each owned process carries one native thread. Responses are dispatched before
// notifications, so turn/steer and turn/interrupt work while GenerateContent is
// consuming the turn's stream. No terminal text participates in this protocol.
type codexRPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *codexRPCError  `json:"error,omitempty"`
}

type codexRPCRequestError struct {
	Method  string
	Code    int
	Message string
}

func (e *codexRPCRequestError) Error() string {
	return fmt.Sprintf("codex app-server %s (%d): %s", e.Method, e.Code, e.Message)
}

type codexRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type codexRPC struct {
	// version: the app-server's Codex version from initialize ("0.172.0"), "" when unread.
	version                 string
	logger                  interfaces.Logger
	cmd                     *exec.Cmd
	stdin                   io.WriteCloser
	writeMu                 sync.Mutex
	mu                      sync.Mutex
	pending                 map[string]chan codexRPCMessage
	err                     error
	next                    atomic.Uint64
	events                  chan codexRPCMessage
	done                    chan struct{}
	exited                  chan struct{}
	stop                    context.CancelFunc
	cleanup                 func()
	releaseBridgeCredential func()
	once                    sync.Once
	// process logs and counts this app-server's launch, reuses and exit (#2886).
	process *llmtypes.AgentProcess
	// idle (guarded by mu) means no turn consumer reads events: Codex keeps
	// sending output and requests for commands that outlive their turn, so
	// the reader handles them itself instead of filling events (#3128).
	idle bool
	// idleNotify applies thread-scoped notifications (native goal) between turns.
	idleNotify func(codexRPCMessage)
	// idleCommands follows command items and output deltas between turns:
	// a command that outlived its turn keeps showing its output (#3131).
	idleCommands func(codexRPCMessage) bool
	// idleMu orders inline idle handling after the drain in detachEvents.
	idleMu    sync.Mutex
	idleStats codexIdleStats
}

// Counters only: idle handling keeps no per-item state.
type codexIdleStats struct {
	dropped, declined, notified, commands int
}

func (r *codexRPC) write(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return json.NewEncoder(r.stdin).Encode(v)
}

// Only image requests use the streaming path; small ordinary RPCs are unchanged.
func (r *codexRPC) writeRequest(id, method string, params any) error {
	p, ok := params.(codexStreamingParams)
	if !ok {
		return r.write(map[string]any{"id": id, "method": method, "params": params})
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	err := func() error {
		if _, err := io.WriteString(r.stdin, `{"id":`); err != nil {
			return err
		}
		if err := writeCodexJSON(r.stdin, id); err != nil {
			return err
		}
		if _, err := io.WriteString(r.stdin, `,"method":`); err != nil {
			return err
		}
		if err := writeCodexJSON(r.stdin, method); err != nil {
			return err
		}
		if _, err := io.WriteString(r.stdin, `,"params":`); err != nil {
			return err
		}
		if err := p.writeJSON(r.stdin); err != nil {
			return err
		}
		_, err := io.WriteString(r.stdin, "}\n")
		return err
	}()
	if err != nil {
		r.finish(fmt.Errorf("codex image request write failed"))
	}
	return err
}

func (r *codexRPC) call(ctx context.Context, method string, params any, result any) error {
	id := fmt.Sprintf("aw-%d", r.next.Add(1))
	key, _ := json.Marshal(id)
	ch := make(chan codexRPCMessage, 1)
	r.mu.Lock()
	r.pending[string(key)] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, string(key)); r.mu.Unlock() }()
	if err := r.writeRequest(id, method, params); err != nil {
		return fmt.Errorf("codex app-server %s: %w", method, err)
	}
	select {
	case response := <-ch:
		if r.logger != nil {
			r.logger.Debugf("codex app-server response method=%s id=%s error=%t", method, id, response.Error != nil)
		}
		if response.Error != nil {
			if method == "turn/steer" {
				return fmt.Errorf("%w: Codex rejected steer (%d)", llmtypes.ErrCodingAgentInputNotSent, response.Error.Code)
			}
			return &codexRPCRequestError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
		}
		if result != nil {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.failure()
	}
}

func (r *codexRPC) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return fmt.Errorf("codex app-server connection closed")
}

func (r *codexRPC) finish(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
		r.stop()
		_ = r.stdin.Close()
		if r.releaseBridgeCredential != nil {
			r.releaseBridgeCredential()
		}
	})
}

func (r *codexRPC) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		raw, err := readCodexImageSafeFrame(reader)
		if err != nil {
			if err == io.EOF {
				r.finish(io.EOF)
			} else {
				r.finish(fmt.Errorf("read codex app-server: %w", err))
			}
			return
		}
		if len(raw) == 0 {
			continue
		}
		var msg codexRPCMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			r.finish(fmt.Errorf("invalid codex app-server JSON: %w", err))
			return
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			r.mu.Lock()
			ch := r.pending[string(msg.ID)]
			r.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		// Legacy codex/event notifications duplicate the typed v2 events.
		if len(msg.Method) >= 12 && msg.Method[:12] == "codex/event/" {
			continue
		}
		r.mu.Lock()
		if r.idle {
			r.mu.Unlock()
			r.idleMu.Lock()
			r.handleIdleLocked(msg)
			r.idleMu.Unlock()
			continue
		}
		// A turn consumer owns events. Only a consumer that stops reading
		// mid-turn can fill the buffer; that stays fatal.
		select {
		case r.events <- msg:
			r.mu.Unlock()
		case <-r.done:
			r.mu.Unlock()
			return
		default:
			r.mu.Unlock()
			r.finish(fmt.Errorf("codex app-server event consumer fell behind"))
			return
		}
	}
}

// attachEvents hands events to a turn consumer. Call it before the RPC that
// starts the turn, so that turn's turn/started is buffered, never dropped.
func (r *codexRPC) attachEvents() {
	r.mu.Lock()
	wasIdle := r.idle
	r.idle = false
	r.mu.Unlock()
	if !wasIdle {
		return
	}
	r.idleMu.Lock()
	stats := r.idleStats
	r.idleStats = codexIdleStats{}
	r.idleMu.Unlock()
	if r.logger != nil && stats != (codexIdleStats{}) {
		r.logger.Infof("[CODEX_IDLE_EVENTS] action=handover dropped=%d declined=%d notified=%d commands=%d reason=turn_starting", stats.dropped, stats.declined, stats.notified, stats.commands)
	}
}

// detachEvents runs when no consumer reads events (between turns, retry
// backoff). Buffered leftovers of settled turns are handled now, in order,
// and later messages are handled by the reader itself.
func (r *codexRPC) detachEvents() {
	r.idleMu.Lock()
	defer r.idleMu.Unlock()
	r.mu.Lock()
	if r.idle {
		r.mu.Unlock()
		return
	}
	r.idle = true
	var backlog []codexRPCMessage
	for drained := false; !drained; {
		select {
		case msg := <-r.events:
			backlog = append(backlog, msg)
		default:
			drained = true
		}
	}
	r.mu.Unlock()
	for _, msg := range backlog {
		r.handleIdleLocked(msg)
	}
}

func (r *codexRPC) setIdleNotify(handle func(codexRPCMessage)) {
	r.mu.Lock()
	r.idleNotify = handle
	r.mu.Unlock()
}

func (r *codexRPC) setIdleCommands(handle func(codexRPCMessage) bool) {
	r.mu.Lock()
	r.idleCommands = handle
	r.mu.Unlock()
}

func codexCommandMethod(method string) bool {
	return method == "item/commandExecution/outputDelta" || method == "item/started" || method == "item/completed"
}

// handleIdleLocked (idleMu held) never blocks on a consumer. A server request
// gets the same error reply an in-turn consumer gives a request for another
// turn; goal notifications update the session; command items and output
// deltas feed the command's live output (#3131); everything else (other item
// and turn events of settled turns) is dropped.
func (r *codexRPC) handleIdleLocked(msg codexRPCMessage) {
	select {
	case <-r.done:
		return
	default:
	}
	if len(msg.ID) > 0 {
		r.idleStats.declined++
		if r.logger != nil {
			r.logger.Infof("[CODEX_IDLE_EVENTS] action=declined method=%s reason=no_active_turn", msg.Method)
		}
		reply := map[string]any{"id": msg.ID, "error": map[string]any{"code": -32602, "message": "no active turn is waiting for this request"}}
		if err := r.write(reply); err != nil {
			r.finish(err)
		}
		return
	}
	if msg.Method == "thread/goal/updated" || msg.Method == "thread/goal/cleared" {
		r.mu.Lock()
		handle := r.idleNotify
		r.mu.Unlock()
		if handle != nil {
			r.idleStats.notified++
			handle(msg)
			return
		}
	}
	if codexCommandMethod(msg.Method) {
		r.mu.Lock()
		handle := r.idleCommands
		r.mu.Unlock()
		if handle != nil && handle(msg) {
			r.idleStats.commands++
			return
		}
	}
	r.idleStats.dropped++
	if r.idleStats.dropped == 1 && r.logger != nil {
		r.logger.Infof("[CODEX_IDLE_EVENTS] action=draining method=%s reason=no_active_turn", msg.Method)
	}
}

func (r *codexRPC) close() {
	r.finish(fmt.Errorf("codex app-server closed"))
	<-r.exited
}

// codexUserAgentVersion reads the Codex version from initialize's userAgent
// ("agentworks/0.172.0 (Mac OS ...) ..."); "" when it has none.
func codexUserAgentVersion(userAgent string) string {
	_, rest, ok := strings.Cut(strings.TrimSpace(userAgent), "/")
	if !ok {
		return ""
	}
	version, _, _ := strings.Cut(rest, " ")
	return version
}

// codexCompactionKeepsTurnContext: Codex 0.171 and later keep a turn's
// additionalContext (where changed AgentWorks instructions go) in the
// compacted history, so sending them again after a compaction doubles them.
// Older or unread versions drop it, and the next turn carries it again.
func codexCompactionKeepsTurnContext(r *codexRPC) bool {
	if r == nil {
		return false
	}
	var parts [3]int
	for i, field := range strings.SplitN(r.version, ".", 3) {
		digits := field
		if end := strings.IndexFunc(field, func(c rune) bool { return c < '0' || c > '9' }); end >= 0 {
			digits = field[:end]
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		parts[i] = n
	}
	if parts[0] != 0 {
		return parts[0] > 0
	}
	return parts[1] >= 171
}
