package codexcli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type backgroundOutputRecorder struct {
	mu     sync.Mutex
	chunks []llmtypes.BackgroundOutput
}

func recordBackgroundOutput(t *testing.T, owner string) *backgroundOutputRecorder {
	t.Helper()
	rec := &backgroundOutputRecorder{}
	llmtypes.SetBackgroundOutputListener(func(output llmtypes.BackgroundOutput) {
		if output.Owner != owner {
			return
		}
		rec.mu.Lock()
		rec.chunks = append(rec.chunks, output)
		rec.mu.Unlock()
	})
	t.Cleanup(func() { llmtypes.SetBackgroundOutputListener(nil) })
	return rec
}

// wait returns the chunks once ok holds for them, or fails after 5 s.
func (rec *backgroundOutputRecorder) wait(t *testing.T, what string, ok func([]llmtypes.BackgroundOutput) bool) []llmtypes.BackgroundOutput {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec.mu.Lock()
		chunks := append([]llmtypes.BackgroundOutput(nil), rec.chunks...)
		rec.mu.Unlock()
		if ok(chunks) {
			return chunks
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: got %+v", what, chunks)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func joinedBackgroundText(chunks []llmtypes.BackgroundOutput, id string) string {
	var b strings.Builder
	for _, c := range chunks {
		if c.ToolCallID == id {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// #3131: a command that outlives its turn keeps showing its output after the
// reply ended, is listed as background work, and a person can stop it.
func TestCodexBackgroundCommandStreamsOutputAfterTurnAndStops(t *testing.T) {
	const owner = "chat-3131-codex"
	rec := recordBackgroundOutput(t, owner)
	r, server, writer := idleFixture(t, 64)
	s := &codexAppSession{owner: owner, threadID: "thread", rpc: r}
	codexAppSessions.Lock()
	codexAppSessions.sessions[owner] = s
	codexAppSessions.Unlock()
	t.Cleanup(func() {
		codexAppSessions.Lock()
		delete(codexAppSessions.sessions, owner)
		codexAppSessions.Unlock()
	})
	r.setIdleCommands(func(msg codexRPCMessage) bool { return observeCodexCommandEvent(s, r, msg.Method, msg.Params) })

	r.attachEvents()
	idleSend(t, server, `{"method":"item/started","params":{"threadId":"thread","turnId":"turn1","item":{"id":"exec-1","type":"commandExecution","command":"npm run dev","processId":"4242","status":"inProgress"}}}`)
	idleSend(t, server, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread","turnId":"turn1","itemId":"exec-1","delta":"starting\n"}}`)
	idleSend(t, server, `{"method":"item/completed","params":{"threadId":"thread","turnId":"turn1","item":{"id":"m","type":"agentMessage","phase":"final_answer","text":"The dev server is starting."}}}`)
	idleSend(t, server, `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn1","status":"completed"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var toolEnds int
	response, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, s, r, "thread", "turn1", func(chunk llmtypes.StreamChunk) {
		if chunk.Type == llmtypes.StreamChunkTypeToolCallEnd {
			toolEnds++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Choices[0].Content; got != "The dev server is starting." {
		t.Fatalf("reply %q", got)
	}
	if toolEnds != 0 {
		t.Fatal("a running command was ended with its turn")
	}
	// #4602: the turn's end asks Codex which commands still run; this one does.
	answerCodexTerminalList(t, writer, server, `[{"itemId":"exec-1","processId":"4242","command":"npm run dev"}]`)
	r.detachEvents()

	// Between turns: Codex keeps sending the command's output.
	for _, line := range []string{"ready on port 3000\n", "GET / 200\n"} {
		delta, _ := json.Marshal(line)
		idleSend(t, server, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"thread","turnId":"turn1","itemId":"exec-1","delta":`+string(delta)+`}}`)
	}
	// A request answered means every earlier line was read.
	idleSend(t, server, `{"id":"sync","method":"item/tool/requestUserInput","params":{"threadId":"thread","turnId":"turn1","questions":[]}}`)
	idleReply(t, writer, `"sync"`)
	chunks := rec.wait(t, "late output", func(chunks []llmtypes.BackgroundOutput) bool {
		return strings.Contains(joinedBackgroundText(chunks, "exec-1"), "GET / 200")
	})
	if got := joinedBackgroundText(chunks, "exec-1"); got != "starting\nready on port 3000\nGET / 200\n" {
		t.Fatalf("live output %q", got)
	}
	last := chunks[len(chunks)-1]
	if !last.CanStop || last.TaskID != "exec-1" || last.Provider != "codex-cli" || last.Done {
		t.Fatalf("background chunk %+v", last)
	}
	listed := CodexBackgroundCommands(owner)
	if len(listed) != 1 || listed[0].ID != "exec-1" || listed[0].Label != "npm run dev" {
		t.Fatalf("background commands %+v", listed)
	}

	stopped := make(chan bool, 1)
	go func() { stopped <- StopCodexBackgroundCommand(owner, "exec-1") }()
	request := <-writer.written
	var params struct {
		ThreadID  string `json:"threadId"`
		ProcessID string `json:"processId"`
	}
	if request.Method != "thread/backgroundTerminals/terminate" || json.Unmarshal(request.Params, &params) != nil || params.ThreadID != "thread" || params.ProcessID != "4242" {
		t.Fatalf("terminate request %+v", request)
	}
	idleSend(t, server, `{"id":`+string(request.ID)+`,"result":{"terminated":true}}`)
	if !<-stopped {
		t.Fatal("stop reported failure")
	}
	rec.wait(t, "done chunk", func(chunks []llmtypes.BackgroundOutput) bool {
		return len(chunks) > 0 && chunks[len(chunks)-1].Done && chunks[len(chunks)-1].ToolCallID == "exec-1"
	})
	if len(CodexBackgroundCommands(owner)) != 0 {
		t.Fatal("stopped command still listed")
	}
	if StopCodexBackgroundCommand(owner, "exec-1") {
		t.Fatal("stopping an ended command succeeded")
	}
}

// A command that ends inside its turn is never background work, and the
// process closing ends every command it ran.
func TestCodexCommandOutputEndsWithItemOrProcess(t *testing.T) {
	const owner = "chat-3131-codex-end"
	rec := recordBackgroundOutput(t, owner)
	r := &codexRPC{stdin: &idleReplyWriter{written: make(chan codexRPCMessage, 4)}, pending: map[string]chan codexRPCMessage{}, events: make(chan codexRPCMessage, 4), done: make(chan struct{}), stop: func() {}}
	s := &codexAppSession{owner: owner, threadID: "thread"}
	send := func(method, params string) {
		observeCodexCommandEvent(s, r, method, json.RawMessage(params))
	}
	send("item/started", `{"threadId":"thread","turnId":"t","item":{"id":"quick","type":"commandExecution","command":"ls","processId":"1","status":"inProgress"}}`)
	send("item/started", `{"threadId":"thread","turnId":"t","item":{"id":"long","type":"commandExecution","command":"sleep 100","processId":"2","status":"inProgress"}}`)
	send("item/commandExecution/outputDelta", `{"threadId":"thread","turnId":"t","itemId":"quick","delta":"a.txt\n"}`)
	send("item/commandExecution/outputDelta", `{"threadId":"other","turnId":"t","itemId":"long","delta":"not ours"}`)
	send("item/completed", `{"threadId":"thread","turnId":"t","item":{"id":"quick","type":"commandExecution","status":"completed"}}`)
	s.commandTracker().detachTurn("t")
	if listed := s.commands.background(); len(listed) != 1 || listed[0].ID != "long" {
		t.Fatalf("background %+v", listed)
	}
	close(r.done)
	chunks := rec.wait(t, "ends", func(chunks []llmtypes.BackgroundOutput) bool {
		done := 0
		for _, c := range chunks {
			if c.Done {
				done++
			}
		}
		return done == 2
	})
	if got := joinedBackgroundText(chunks, "quick"); got != "a.txt\n" {
		t.Fatalf("quick output %q", got)
	}
	if strings.Contains(joinedBackgroundText(chunks, "long"), "not ours") {
		t.Fatal("another thread's output was shown")
	}
	if len(s.commands.background()) != 0 {
		t.Fatal("commands of a closed process still listed")
	}
}

func answerCodexTerminalList(t *testing.T, writer *idleReplyWriter, server io.Writer, data string) {
	t.Helper()
	request := <-writer.written
	if request.Method != "thread/backgroundTerminals/list" {
		t.Fatalf("expected the live terminals list, got %+v", request)
	}
	idleSend(t, server, `{"id":`+string(request.ID)+`,"result":{"data":`+data+`,"nextCursor":null}}`)
}

// #4602: Codex can start a command and never report it finished; once its
// turn ended and Codex no longer runs it, it stops being listed as running.
func TestCodexCommandCodexNoLongerRunsEndsAfterItsTurn(t *testing.T) {
	const owner = "chat-4602-codex-exited"
	rec := recordBackgroundOutput(t, owner)
	r, server, writer := idleFixture(t, 64)
	s := &codexAppSession{owner: owner, threadID: "thread", rpc: r}
	codexAppSessions.Lock()
	codexAppSessions.sessions[owner] = s
	codexAppSessions.Unlock()
	t.Cleanup(func() {
		codexAppSessions.Lock()
		delete(codexAppSessions.sessions, owner)
		codexAppSessions.Unlock()
	})
	r.attachEvents()
	idleSend(t, server, `{"method":"item/started","params":{"threadId":"thread","turnId":"turn1","item":{"id":"exec-2","type":"commandExecution","command":"make test","processId":"77","status":"inProgress"}}}`)
	idleSend(t, server, `{"method":"turn/completed","params":{"threadId":"thread","turn":{"id":"turn1","status":"completed"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := (&CodexCLIAdapter{}).consumeAppTurn(ctx, s, r, "thread", "turn1", func(llmtypes.StreamChunk) {}); err != nil {
		t.Fatal(err)
	}
	answerCodexTerminalList(t, writer, server, `[]`)
	rec.wait(t, "done chunk", func(chunks []llmtypes.BackgroundOutput) bool {
		return len(chunks) > 0 && chunks[len(chunks)-1].Done && chunks[len(chunks)-1].ToolCallID == "exec-2"
	})
	if len(CodexBackgroundCommands(owner)) != 0 {
		t.Fatal("a command Codex no longer runs is still listed")
	}
}
