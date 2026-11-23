package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// idleReplyWriter records the replies the adapter writes to Codex's stdin.
type idleReplyWriter struct {
	mu      sync.Mutex
	replies []codexRPCMessage
	written chan codexRPCMessage
}

func (w *idleReplyWriter) Write(raw []byte) (int, error) {
	var msg codexRPCMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.replies = append(w.replies, msg)
	w.mu.Unlock()
	w.written <- msg
	return len(raw), nil
}
func (*idleReplyWriter) Close() error { return nil }

func idleFixture(t *testing.T, buffer int) (*codexRPC, *io.PipeWriter, *idleReplyWriter) {
	t.Helper()
	stdout, server := io.Pipe()
	writer := &idleReplyWriter{written: make(chan codexRPCMessage, 64)}
	r := &codexRPC{idle: true, stdin: writer, pending: map[string]chan codexRPCMessage{}, events: make(chan codexRPCMessage, buffer), done: make(chan struct{}), stop: func() {}}
	go r.read(stdout)
	t.Cleanup(func() { _ = server.Close() })
	return r, server, writer
}

func idleSend(t *testing.T, server io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(server, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func idleReply(t *testing.T, w *idleReplyWriter, id string) codexRPCMessage {
	t.Helper()
	select {
	case msg := <-w.written:
		if string(msg.ID) != id {
			t.Fatalf("reply for %s, want %s", msg.ID, id)
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatalf("no reply to server request %s", id)
	}
	return codexRPCMessage{}
}

// #3128 risk 1: a background command that keeps printing after its turn
// settled used to fill the 1024-slot buffer and close the app-server. Between
// turns the reader handles late output and requests itself, and the next turn
// still gets every event of its own.
func TestCodexAppServerSurvivesChattyBackgroundCommandBetweenTurns(t *testing.T) {
	r, server, writer := idleFixture(t, 1024)
	var goals []string
	r.setIdleNotify(func(msg codexRPCMessage) { goals = append(goals, msg.Method) })
	for i := 0; i < 5000; i++ {
		idleSend(t, server, fmt.Sprintf(`{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread","turnId":"turn1","itemId":"exec-1","delta":"line %d\n"}}`, i))
		if i == 2500 {
			idleSend(t, server, `{"id":"late-approval","method":"item/commandExecution/requestApproval","params":{"threadId":"thread","turnId":"turn1","itemId":"exec-2","command":"ls"}}`)
			reply := idleReply(t, writer, `"late-approval"`)
			if reply.Error == nil || reply.Result != nil {
				t.Fatalf("late request was not declined: %+v", reply)
			}
		}
	}
	idleSend(t, server, `{"method":"item/completed","params":{"threadId":"thread","turnId":"turn1","item":{"id":"exec-1","type":"commandExecution","command":"loop","status":"completed","aggregatedOutput":"done"}}}`)
	idleSend(t, server, `{"method":"thread/goal/updated","params":{"threadId":"thread","goal":{"objective":"x","status":"paused"}}}`)
	// The reader is sequential: once this request is answered, every earlier
	// line was handled.
	idleSend(t, server, `{"id":7,"method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"turn1","questions":[]}}`)
	idleReply(t, writer, `7`)
	select {
	case <-r.done:
		t.Fatalf("app-server closed between turns: %v", r.failure())
	default:
	}
	if len(r.events) != 0 {
		t.Fatalf("idle events were buffered: %d", len(r.events))
	}
	if len(goals) != 1 || goals[0] != "thread/goal/updated" {
		t.Fatalf("goal notification not applied between turns: %v", goals)
	}

	// The next turn: attach before the turn-starting RPC; nothing is lost.
	r.attachEvents()
	idleSend(t, server, `{"method":"turn/started","params":{"threadId":"thread","turn":{"id":"turn2","status":"inProgress"}}}`)
	idleSend(t, server, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread","turnId":"turn1","itemId":"exec-1","delta":"stale"}}`)
	idleSend(t, server, `{"method":"item/agentMessage/delta","params":{"threadId":"thread","turnId":"turn2","itemId":"m","delta":"next "}}`)
	idleSend(t, server, `{"method":"item/completed","params":{"threadId":"thread","turnId":"turn2","item":{"id":"m","type":"agentMessage","phase":"final_answer","text":"next answer"}}}`)
	idleSend(t, server, `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn2","status":"completed"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, &codexAppSession{threadID: "thread"}, r, "thread", "turn2", func(llmtypes.StreamChunk) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Choices[0].Content; got != "next answer" {
		t.Fatalf("next turn reply %q", got)
	}
	r.detachEvents()
	idleSend(t, server, `{"id":"after","method":"item/commandExecution/requestApproval","params":{"threadId":"thread","turnId":"turn2","itemId":"exec-3","command":"ls"}}`)
	idleReply(t, writer, `"after"`)
}

// Leftovers of a settled turn still in the buffer when the consumer returns
// are handled at detach: a pending server request is answered, not stranded.
func TestCodexAppServerDetachDeclinesBufferedServerRequest(t *testing.T) {
	writer := &idleReplyWriter{written: make(chan codexRPCMessage, 4)}
	r := &codexRPC{stdin: writer, pending: map[string]chan codexRPCMessage{}, events: make(chan codexRPCMessage, 4), done: make(chan struct{}), stop: func() {}}
	r.events <- codexRPCMessage{Method: "item/commandExecution/outputDelta", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn1","delta":"x"}`)}
	r.events <- codexRPCMessage{ID: json.RawMessage(`"q1"`), Method: "item/tool/requestUserInput", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn1"}`)}
	r.detachEvents()
	reply := idleReply(t, writer, `"q1"`)
	if reply.Error == nil || !strings.Contains(reply.Error.Message, "no active turn") {
		t.Fatalf("buffered request reply %+v", reply)
	}
	if len(r.events) != 0 {
		t.Fatal("detach left stale events for the next turn")
	}
	r.detachEvents() // idempotent
}

// The safety net remains for a consumer that owns events and stops reading.
func TestCodexAppServerStuckTurnConsumerStillFallsBehind(t *testing.T) {
	r, server, _ := idleFixture(t, 4)
	r.attachEvents()
	go func() {
		for i := 0; i < 10; i++ {
			if _, err := io.WriteString(server, `{"method":"item/agentMessage/delta","params":{"threadId":"thread","turnId":"turn","itemId":"m","delta":"x"}}`+"\n"); err != nil {
				return
			}
		}
	}()
	select {
	case <-r.done:
		if !strings.Contains(r.failure().Error(), "fell behind") {
			t.Fatalf("unexpected failure %v", r.failure())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stuck in-turn consumer did not trip the safety net")
	}
}
