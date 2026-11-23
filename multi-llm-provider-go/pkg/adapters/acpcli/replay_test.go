package acpcli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A replay transcript (the T3 Code provider-testing format): a
// transcript_start header, then expect_outbound frames the client must send
// and emit_inbound frames the fake agent sends. Frames carry no JSON-RPC ids:
// a response belongs to the oldest unanswered request of its method.
// "<any>" matches any value and "<workspace>" is the session's cwd. Expected
// objects match as subsets, so a client may send extra fields.
type replayEntry struct {
	Type  string         `json:"type"`
	Label string         `json:"label"`
	Frame map[string]any `json:"frame"`
}

type replayPeer struct {
	t         *testing.T
	entries   []replayEntry
	workspace string
	in        *bufio.Reader // client -> agent
	out       io.Writer     // agent -> client
	requests  map[string][]json.RawMessage
	inbound   map[string]string // our request id -> method
	next      int
	mu        sync.Mutex
	failure   error
	finished  chan struct{}
}

func loadReplay(t *testing.T, name string) []replayEntry {
	t.Helper()
	raw, err := os.ReadFile("testdata/replay/" + name + ".ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var entries []replayEntry
	for i, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var e replayEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%s line %d: %v", name, i+1, err)
		}
		if i == 0 {
			var header map[string]any
			_ = json.Unmarshal([]byte(line), &header)
			if header["type"] != "transcript_start" || header["provider"] != ProviderID || header["protocol"] != "acp.ndjson-jsonrpc" {
				t.Fatalf("%s: bad header %v", name, header)
			}
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// installReplay makes the next agent launch talk to a peer that plays the
// transcript; it returns the peer so a test can check it finished.
func installReplay(t *testing.T, name, workspace string) *replayPeer {
	t.Helper()
	clientR, clientW := io.Pipe()
	agentR, agentW := io.Pipe()
	peer := &replayPeer{t: t, entries: loadReplay(t, name), workspace: workspace, in: bufio.NewReader(clientR), out: agentW,
		requests: map[string][]json.RawMessage{}, inbound: map[string]string{}, finished: make(chan struct{})}
	previous := startAgent
	t.Cleanup(func() { startAgent = previous })
	startAgent = func(agent Agent, opts *llmtypes.CallOptions, owner, workdir string, full bool, _ []mcpServer, spec *LaunchSpec) (*conn, []string, error) {
		c := newConn(owner, agent.ID, clientW)
		go c.read(agentR)
		go func() {
			peer.run()
			peer.mu.Lock()
			failed := peer.failure != nil
			peer.mu.Unlock()
			if failed {
				// Exit at once so a waiting client call fails instead of hanging.
				close(c.exited)
				_ = agentW.Close()
				_, _ = io.Copy(io.Discard, clientR)
				return
			}
			// Hold the line open until the client closes it, then exit.
			_, _ = io.Copy(io.Discard, clientR)
			_ = agentW.Close()
			close(c.exited)
		}()
		var env []string
		if spec != nil {
			env = spec.Env
		}
		return c, env, nil
	}
	return peer
}

func (p *replayPeer) fail(err error) {
	p.mu.Lock()
	if p.failure == nil {
		p.failure = err
	}
	p.mu.Unlock()
}

func (p *replayPeer) run() {
	defer close(p.finished)
	for _, e := range p.entries {
		var err error
		switch e.Type {
		case "expect_outbound":
			err = p.expect(e)
		case "emit_inbound":
			err = p.emit(e)
		default:
			err = fmt.Errorf("unknown entry type %q", e.Type)
		}
		if err != nil {
			p.fail(fmt.Errorf("%s: %w", e.Label, err))
			return
		}
	}
}

func (p *replayPeer) expect(e replayEntry) error {
	type result struct {
		line []byte
		err  error
	}
	got := make(chan result, 1)
	go func() { line, err := p.in.ReadBytes('\n'); got <- result{line, err} }()
	var r result
	select {
	case r = <-got:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("client sent nothing")
	}
	if r.err != nil {
		return r.err
	}
	var msg message
	if err := json.Unmarshal(r.line, &msg); err != nil {
		return err
	}
	frame := map[string]any{}
	switch {
	case msg.isRequest():
		frame["kind"], frame["method"] = "request", msg.Method
		p.requests[msg.Method] = append(p.requests[msg.Method], msg.ID)
	case msg.Method != "":
		frame["kind"], frame["method"] = "notification", msg.Method
	default:
		frame["kind"], frame["method"] = "response", p.inbound[string(msg.ID)]
		delete(p.inbound, string(msg.ID))
	}
	var body map[string]any
	_ = json.Unmarshal(r.line, &body)
	for _, key := range []string{"params", "result", "error"} {
		if v, ok := body[key]; ok {
			frame[key] = v
		}
	}
	if !matches(p.materialize(e.Frame), frame) {
		raw, _ := json.Marshal(frame)
		return fmt.Errorf("client frame did not match: got %s", raw)
	}
	return nil
}

func (p *replayPeer) emit(e replayEntry) error {
	f := e.Frame
	method, _ := f["method"].(string)
	wire := map[string]any{"jsonrpc": "2.0"}
	switch f["kind"] {
	case "request":
		p.next++
		id := fmt.Sprintf("peer-%d", p.next)
		raw, _ := json.Marshal(id)
		p.inbound[string(raw)] = method
		wire["id"], wire["method"], wire["params"] = id, method, f["params"]
	case "notification":
		wire["method"], wire["params"] = method, f["params"]
	case "response":
		queue := p.requests[method]
		if len(queue) == 0 {
			return fmt.Errorf("no open %s request to answer", method)
		}
		wire["id"] = queue[0]
		p.requests[method] = queue[1:]
		if errValue, ok := f["error"]; ok {
			wire["error"] = errValue
		} else {
			wire["result"] = f["result"]
		}
	default:
		return fmt.Errorf("unknown frame kind %v", f["kind"])
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	_, err = p.out.Write(append(raw, '\n'))
	return err
}

func (p *replayPeer) materialize(v any) any {
	switch x := v.(type) {
	case string:
		if x == "<workspace>" {
			return p.workspace
		}
	case map[string]any:
		out := map[string]any{}
		for k, item := range x {
			out[k] = p.materialize(item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = p.materialize(item)
		}
		return out
	}
	return v
}

func matches(want, got any) bool {
	if s, ok := want.(string); ok && s == "<any>" {
		return true
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range w {
			if gv, present := g[k]; !present || !matches(v, gv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !matches(w[i], g[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(want, got)
}

// wait checks the peer played the whole transcript.
func (p *replayPeer) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("replay did not finish")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failure != nil {
		t.Fatal(p.failure)
	}
}
