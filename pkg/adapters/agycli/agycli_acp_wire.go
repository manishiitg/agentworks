package agycli

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

// agyACP is one `agy acp` process: JSON-RPC 2.0 lines on stdio (Agent Client
// Protocol). Responses go to their waiting call; requests the agent makes of
// the client and notifications go to events in order, never dropped.
type agyACPMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *agyACPError    `json:"error,omitempty"`
}

type agyACPError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *agyACPError) Error() string { return fmt.Sprintf("(%d): %s", e.Code, e.Message) }

type agyACP struct {
	// process logs and counts this process's launch, reuses and exit (#2886).
	process *llmtypes.AgentProcess
	stderr  *clisandbox.StderrTail
	owner   string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan agyACPMessage
	err     error
	next    atomic.Uint64
	events  chan agyACPMessage
	done    chan struct{}
	exited  chan struct{}
	stop    context.CancelFunc
	once    sync.Once
}

func (r *agyACP) write(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if stream, ok := v.(agyStreamingParams); ok {
		err := stream.writeJSON(r.stdin)
		if err == nil {
			_, err = io.WriteString(r.stdin, "\n")
		}
		if err != nil {
			r.finish(fmt.Errorf("agy image frame write failed"))
		}
		return err
	}
	return json.NewEncoder(r.stdin).Encode(v)
}

func (r *agyACP) call(ctx context.Context, method string, params any, result any) error {
	id := fmt.Sprintf("aw-%d", r.next.Add(1))
	key, _ := json.Marshal(id)
	ch := make(chan agyACPMessage, 1)
	r.mu.Lock()
	r.pending[string(key)] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, string(key)); r.mu.Unlock() }()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if image, ok := params.(*agyImageParams); ok {
		writeCtx, cancel := context.WithTimeout(image.writeCtx, 10*time.Second)
		written := make(chan error, 1)
		go func() { written <- r.write(&agyImageRequest{header: request, params: image}) }()
		var writeErr error
		select {
		case writeErr = <-written:
		case <-writeCtx.Done():
			writeErr = writeCtx.Err()
			r.finish(writeErr)
			<-written
		case <-r.done:
			writeErr = r.failure()
			<-written
		}
		cancel()
		finishAgyImageDelivery(image.owner, image, writeErr)
		image.close() // Prompt response may take minutes; readers end at write.
		image.wrote <- writeErr
		if writeErr != nil {
			return fmt.Errorf("agy ACP %s: %w", method, writeErr)
		}
	} else {
		request["params"] = params
		if err := r.write(request); err != nil {
			return fmt.Errorf("agy ACP %s: %w", method, err)
		}
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return fmt.Errorf("agy ACP %s %w", method, response.Error)
		}
		if result != nil && len(response.Result) > 0 {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return fmt.Errorf("agy ACP %s: %w", method, r.failure())
	}
}

func (r *agyACP) notify(method string, params any) error {
	return r.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (r *agyACP) reply(id json.RawMessage, result any) error {
	return r.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (r *agyACP) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return fmt.Errorf("agy ACP connection closed")
}

func (r *agyACP) finish(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
		r.stop()
		if r.cmd != nil && r.cmd.Process != nil {
			_ = procshutdown.KillGroup(r.cmd.Process.Pid)
		}
		_ = r.stdin.Close()
	})
}

func (r *agyACP) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		raw, err := readAgyImageSafeFrame(reader)
		if err != nil {
			if err == io.EOF {
				<-r.exited
				return
			}
			log.Printf("[AGY_ACP] session=%s action=failed reason=frame_limit_or_read_error", r.owner)
			r.finish(fmt.Errorf("read agy ACP: %w", err))
			return
		}
		var msg agyACPMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[AGY_ACP] session=%s action=skipped reason=non_protocol_line", r.owner)
			continue // Preserve agy's harmless diagnostic-line tolerance.
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
		select {
		case r.events <- msg:
		case <-r.done:
			return
		}
	}
}

func (r *agyACP) close() {
	r.finish(fmt.Errorf("agy ACP closed"))
	<-r.exited
}
