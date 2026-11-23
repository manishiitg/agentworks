package picli

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

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
)

// Pi RPC uses LF-delimited JSON, not terminal output. Replies are dispatched
// independently of events so live steer/abort cannot wait behind a model turn.
type piRPCRecord struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
	piJSONEvent
	Message json.RawMessage `json:"message"`
	Raw     json.RawMessage `json:"-"`
}
type piRPCCommandError struct{ Command, Message string }

func (e *piRPCCommandError) Error() string { return fmt.Sprintf("Pi RPC %s: %s", e.Command, e.Message) }

type piRPC struct {
	// process logs and counts this process's launch, reuses and exit (#2886).
	process      *llmtypes.AgentProcess
	cmd          *exec.Cmd
	nativeID     string
	stdin        io.WriteCloser
	writeMu      sync.Mutex
	mu           sync.Mutex
	pending      map[string]chan piRPCRecord
	err          error
	next         atomic.Uint64
	events       chan piRPCRecord
	done, exited chan struct{}
	// exitInfo closes once exitErr (how the process ended) is stored; the output reader waits for it at EOF.
	exitInfo chan struct{}
	exitErr  error
	stop     context.CancelFunc
	once     sync.Once
}

func (r *piRPC) write(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if stream, ok := v.(piStreamingParams); ok {
		err := stream.writeJSON(r.stdin)
		if err == nil {
			_, err = io.WriteString(r.stdin, "\n")
		}
		if err != nil {
			r.finish(fmt.Errorf("Pi image request write failed"))
		}
		return err
	}
	return json.NewEncoder(r.stdin).Encode(v)
}
func (r *piRPC) call(ctx context.Context, command string, params map[string]any, result any) error {
	return r.callPrepared(ctx, command, params, result)
}
func (r *piRPC) callPrepared(ctx context.Context, command string, params any, result any) error {
	id := fmt.Sprintf("aw-%d", r.next.Add(1))
	base := map[string]any{"id": id, "type": command}
	var request any = base
	switch p := params.(type) {
	case *piImageParams:
		for k, v := range p.base {
			base[k] = v
		}
		request = &piImageParams{base: base, images: p.images}
	case map[string]any:
		for k, v := range p {
			base[k] = v
		}
	}
	ch := make(chan piRPCRecord, 1)
	r.mu.Lock()
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	written := make(chan error, 1)
	go func() { written <- r.write(request) }()
	select {
	case err := <-written:
		if err != nil {
			return fmt.Errorf("Pi RPC %s: %w", command, err)
		}
	case <-ctx.Done():
		if _, imageWrite := request.(*piImageParams); imageWrite {
			r.finish(ctx.Err())
			<-written
		}
		return ctx.Err()
	case <-r.done:
		if _, imageWrite := request.(*piImageParams); imageWrite {
			<-written
		}
		return r.failure()
	}
	select {
	case msg := <-ch:
		if !msg.Success {
			if command == "steer" {
				return fmt.Errorf("%w: Pi rejected steer", llmtypes.ErrCodingAgentInputNotSent)
			}
			return &piRPCCommandError{Command: command, Message: msg.Error}
		}
		if result != nil && len(msg.Data) > 0 {
			return json.Unmarshal(msg.Data, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.failure()
	}
}
func (r *piRPC) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return fmt.Errorf("Pi RPC closed")
}
func (r *piRPC) finish(err error) {
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
func (r *piRPC) read(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		raw, err := readPiImageSafeFrame(reader)
		if err != nil {
			if err != io.EOF {
				log.Printf("[PI_RPC] session=%s action=failed reason=invalid_or_oversized_frame", r.nativeID)
				r.finish(fmt.Errorf("read Pi RPC: %w", err))
			} else {
				// The pipe closes before the process is reaped: wait briefly for how it ended, so the person reads the
				// exit status, not a bare EOF.
				if r.exitInfo != nil {
					select {
					case <-r.exitInfo:
					case <-time.After(3 * time.Second):
					}
				}
				r.mu.Lock()
				exitErr := r.exitErr
				r.mu.Unlock()
				if exitErr == nil {
					exitErr = io.EOF
				}
				r.finish(exitErr)
			}
			return
		}
		var msg piRPCRecord
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[PI_RPC] session=%s action=failed reason=invalid_json_frame", r.nativeID)
			r.finish(fmt.Errorf("invalid Pi RPC JSON: %w", err))
			return
		}
		msg.piJSONEvent.Type = msg.Type
		if msg.Type == "response" {
			r.mu.Lock()
			ch := r.pending[msg.ID]
			r.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg:
				default:
				}
			}
			continue
		}
		msg.Raw = append(json.RawMessage(nil), raw...)
		select {
		case r.events <- msg:
		case <-r.done:
			return
		default:
			r.finish(fmt.Errorf("Pi RPC event consumer fell behind"))
			return
		}
	}
}

func (r *piRPC) close() { r.finish(fmt.Errorf("Pi RPC closed")); <-r.exited }
