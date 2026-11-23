package musecli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// museRPC is one `muse serve` process speaking the Muse Session Protocol
// (MSP): JSON-RPC 2.0, one message per line on stdio. Responses go to their
// waiting call; requests the host makes of the client (approval/request,
// userInput/request) and notifications go to the events channel in order.
// No terminal text takes part in this protocol.
type museRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *museRPCError   `json:"error,omitempty"`
}

type museRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type museRPC struct {
	// process logs and counts this process's launch, reuses and exit (#2886);
	// set right after launch, read under mu.
	process *llmtypes.AgentProcess
	logger  interfaces.Logger
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan museRPCMessage
	err     error
	next    atomic.Uint64
	events  chan museRPCMessage
	done    chan struct{}
	exited  chan struct{}
	// exitInfo closes once exitErr (how the process ended) is stored; the output reader waits for it at EOF.
	exitInfo chan struct{}
	exitErr  error
	stop     context.CancelFunc
	once     sync.Once
}

func (r *museRPC) write(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if stream, ok := v.(museStreamingParams); ok {
		err := stream.writeJSON(r.stdin)
		if err == nil {
			_, err = io.WriteString(r.stdin, "\n")
		}
		if err != nil {
			r.finish(fmt.Errorf("Muse image request write failed"))
		}
		return err
	}
	return json.NewEncoder(r.stdin).Encode(v)
}

// call sends a request and decodes its result into result (when non-nil).
func (r *museRPC) call(ctx context.Context, method string, params any, result any) error {
	id := fmt.Sprintf("aw-%d", r.next.Add(1))
	key, _ := json.Marshal(id)
	ch := make(chan museRPCMessage, 1)
	r.mu.Lock()
	r.pending[string(key)] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, string(key)); r.mu.Unlock() }()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	var frame any = request
	image, streaming := params.(*museImageParams)
	if streaming {
		frame = &museImageRequest{header: request, params: image}
	} else if params != nil {
		request["params"] = params
	}
	if streaming {
		written := make(chan error, 1)
		go func() { written <- r.write(frame) }()
		select {
		case err := <-written:
			if err != nil {
				return fmt.Errorf("muse serve %s: %w", method, err)
			}
		case <-ctx.Done():
			r.finish(ctx.Err())
			<-written
			return ctx.Err()
		case <-r.done:
			<-written
			return r.failure()
		}
	} else if err := r.write(frame); err != nil {
		return fmt.Errorf("muse serve %s: %w", method, err)
	}
	select {
	case response := <-ch:
		if response.Error != nil {
			return &museMSPError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
		}
		if result != nil && len(response.Result) > 0 {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.failure()
	}
}

// reply answers a request the host made of this client.
func (r *museRPC) reply(id json.RawMessage, result any) error {
	return r.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (r *museRPC) notify(method string) error {
	return r.write(map[string]any{"jsonrpc": "2.0", "method": method})
}

type museMSPError struct {
	Method  string
	Code    int
	Message string
}

func (e *museMSPError) Error() string {
	return fmt.Sprintf("muse serve %s (%d): %s", e.Method, e.Code, e.Message)
}

func (r *museRPC) failure() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	return fmt.Errorf("muse serve connection closed")
}

func (r *museRPC) finish(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
		r.stop()
		_ = r.stdin.Close()
	})
}

func (r *museRPC) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg museRPCMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			r.finish(fmt.Errorf("invalid muse serve JSON: %w", err))
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
		select {
		case r.events <- msg:
		case <-r.done:
			return
		default:
			r.finish(fmt.Errorf("muse serve event consumer fell behind"))
			return
		}
	}
	if err := scanner.Err(); err != nil {
		r.finish(fmt.Errorf("read muse serve: %w", err))
	} else {
		// The pipe closes before the process is reaped: wait briefly for how it ended, so the person reads the exit
		// status, not a bare EOF.
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
}

func (r *museRPC) close() {
	r.finish(fmt.Errorf("muse serve closed"))
	select {
	case <-r.exited:
	case <-time.After(10 * time.Second):
	}
}

// museCommandID is a fresh UUIDv7, the only command id MSP accepts.
func museCommandID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ms := uint64(time.Now().UnixMilli())
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], ms)
	copy(b[0:6], t[2:8])
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
