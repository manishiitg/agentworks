package acpcli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// message is one JSON-RPC 2.0 frame of the Agent Client Protocol.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

func (m message) isRequest() bool { return len(m.ID) > 0 && m.Method != "" }

// RPCError is an agent's JSON-RPC error answer.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("(%d): %s", e.Code, e.Message) }

// conn is one agent process. Every frame it reads goes to events in order;
// the session's pump hands responses to their waiting call.
type conn struct {
	process *llmtypes.AgentProcess
	stderr  *clisandbox.StderrTail
	owner   string
	agent   string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan message
	err     error
	next    atomic.Uint64
	events  chan message
	done    chan struct{}
	exited  chan struct{}
	stop    context.CancelFunc
	once    sync.Once
}

func newConn(owner, agent string, stdin io.WriteCloser) *conn {
	return &conn{owner: owner, agent: agent, stdin: stdin, pending: map[string]chan message{},
		events: make(chan message, 4096), done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
}

func (c *conn) write(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if stream, ok := v.(streamingParams); ok {
		err := stream.writeJSON(c.stdin)
		if err == nil {
			_, err = io.WriteString(c.stdin, "\n")
		}
		if err != nil {
			// A half-written frame leaves the line unusable.
			c.finish(fmt.Errorf("ACP image frame write failed"))
		}
		return err
	}
	return json.NewEncoder(c.stdin).Encode(v)
}

func (c *conn) call(ctx context.Context, method string, params any, result any) error {
	id := fmt.Sprintf("aw-%d", c.next.Add(1))
	key, _ := json.Marshal(id)
	ch := make(chan message, 1)
	c.mu.Lock()
	c.pending[string(key)] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, string(key)); c.mu.Unlock() }()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if image, ok := params.(*imageParams); ok {
		// Image bytes stream from their readers; a stuck write is bounded and
		// Stop (the caller's context) ends it.
		writeCtx, cancel := context.WithTimeout(image.writeCtx, 10*time.Second)
		written := make(chan error, 1)
		go func() { written <- c.write(&imageRequest{header: request, params: image}) }()
		var writeErr error
		select {
		case writeErr = <-written:
		case <-writeCtx.Done():
			writeErr = writeCtx.Err()
			c.finish(writeErr)
			<-written
		case <-c.done:
			writeErr = c.failure()
			<-written
		}
		cancel()
		finishImageDelivery(image, writeErr)
		image.close() // The prompt's answer may take minutes; readers end at write.
		image.wrote <- writeErr
		if writeErr != nil {
			return fmt.Errorf("ACP %s: %w", method, writeErr)
		}
	} else {
		request["params"] = params
		if err := c.write(request); err != nil {
			return fmt.Errorf("ACP %s: %w", method, err)
		}
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return fmt.Errorf("ACP %s %w", method, response.Error)
		}
		if result != nil && len(response.Result) > 0 && string(response.Result) != "null" {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("ACP %s: %w", method, c.failure())
	}
}

func (c *conn) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *conn) reply(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *conn) replyError(id json.RawMessage, code int, text string) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": text}})
}

func (c *conn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return fmt.Errorf("ACP connection closed")
}

func (c *conn) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
		c.stop()
		if c.cmd != nil && c.cmd.Process != nil {
			_ = procshutdown.KillGroup(c.cmd.Process.Pid)
		}
		_ = c.stdin.Close()
	})
}

func (c *conn) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		// Image data the agent echoes back (session/load replays) is dropped
		// while reading, so it never reaches the event queue.
		raw, err := readImageSafeFrame(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("[ACP_CLI] session=%s agent=%s action=failed reason=frame_limit_or_read_error", c.owner, c.agent)
			c.finish(fmt.Errorf("read ACP: %w", err))
			return
		}
		var msg message
		if len(raw) == 0 || json.Unmarshal(raw, &msg) != nil || (msg.Method == "" && len(msg.ID) == 0) {
			// Some agents print diagnostics on stdout; they are not protocol.
			if len(raw) > 0 {
				log.Printf("[ACP_CLI] session=%s agent=%s action=skipped reason=non_protocol_line", c.owner, c.agent)
			}
			continue
		}
		// Responses travel in the same ordered queue as notifications, so
		// every update an agent sent before a response (session/load replays
		// history) is handled before the waiting call returns.
		select {
		case c.events <- msg:
		case <-c.done:
			return
		}
	}
	<-c.exited
	c.finish(fmt.Errorf("ACP agent closed its output"))
}

// deliver hands a response to its waiting call.
func (c *conn) deliver(msg message) {
	c.mu.Lock()
	ch := c.pending[string(msg.ID)]
	c.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (c *conn) close() {
	c.finish(fmt.Errorf("ACP closed"))
	<-c.exited
}
