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
		select {
		case r.events <- msg:
		case <-r.done:
			return
		default:
			r.finish(fmt.Errorf("codex app-server event consumer fell behind"))
			return
		}
	}
}

func (r *codexRPC) close() {
	r.finish(fmt.Errorf("codex app-server closed"))
	<-r.exited
}
