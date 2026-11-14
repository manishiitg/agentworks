package codexcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type compactionMailWriter struct {
	rpc    *codexRPC
	t      *testing.T
	owner  string
	starts int
}

func (*compactionMailWriter) Close() error { return nil }
func (w *compactionMailWriter) notify(method, body string) {
	w.rpc.events <- codexRPCMessage{Method: method, Params: json.RawMessage(body)}
}
func (w *compactionMailWriter) Write(raw []byte) (int, error) {
	var q struct {
		ID     json.RawMessage
		Method string
		Params json.RawMessage
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return 0, err
	}
	result := `{}`
	switch q.Method {
	case "thread/start", "thread/resume":
		result = `{"thread":{"id":"123e4567-e89b-12d3-a456-426614174000"}}`
	case "thread/compact/start":
		if InteractiveTurnActive(w.owner) {
			w.t.Fatal("maintenance is steerable")
		}
		if err := SendCodexAppServerInput(context.Background(), w.owner, "waiting first input"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
			w.t.Fatal("maintenance accepted person input")
		}
		w.notify("turn/started", `{"threadId":"123e4567-e89b-12d3-a456-426614174000","turn":{"id":"compact"}}`)
		w.notify("turn/completed", `{"threadId":"123e4567-e89b-12d3-a456-426614174000","turn":{"id":"compact","status":"completed"}}`)
	case "turn/start":
		w.starts++
		id := fmt.Sprintf("message-%d", w.starts)
		result = fmt.Sprintf(`{"turn":{"id":%q}}`, id)
		// A late duplicate maintenance completion must not settle either message.
		w.notify("turn/completed", `{"threadId":"123e4567-e89b-12d3-a456-426614174000","turn":{"id":"compact","status":"completed"}}`)
		w.notify("item/agentMessage/delta", fmt.Sprintf(`{"threadId":"123e4567-e89b-12d3-a456-426614174000","turnId":%q,"itemId":"answer","delta":"reply"}`, id))
		w.notify("item/completed", fmt.Sprintf(`{"threadId":"123e4567-e89b-12d3-a456-426614174000","turnId":%q,"item":{"id":"answer","type":"agentMessage","phase":"final_answer","text":"reply"}}`, id))
		w.notify("turn/completed", fmt.Sprintf(`{"threadId":"123e4567-e89b-12d3-a456-426614174000","turn":{"id":%q,"status":"completed"}}`, id))
	case "turn/steer":
		w.t.Fatal("input was sent into compaction")
	}
	response := codexRPCMessage{Result: json.RawMessage(result)}
	w.rpc.mu.Lock()
	ch := w.rpc.pending[string(q.ID)]
	w.rpc.mu.Unlock()
	ch <- response
	return len(raw), nil
}

// PLAT-746-941: compaction cannot consume incoming mail, and its late terminal
// notification cannot settle a subsequent message. Both sends start real turns.
func TestCodexMessagesAfterCompactionStartTheirOwnTurns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", t.TempDir())
	owner := "compact-mail-regression"
	prior := startCodexAppServer
	t.Cleanup(func() { startCodexAppServer = prior; closeCodexAppSession(owner) })
	var writer *compactionMailWriter
	startCodexAppServer = func(_ *CodexCLIAdapter, _ context.Context, _ *llmtypes.CallOptions, _ string) (*codexRPC, error) {
		r := &codexRPC{pending: map[string]chan codexRPCMessage{}, events: make(chan codexRPCMessage, 32), done: make(chan struct{}), exited: make(chan struct{}), stop: func() {}}
		close(r.exited)
		writer = &compactionMailWriter{rpc: r, t: t, owner: owner}
		r.stdin = writer
		return r, nil
	}
	adapter := NewCodexCLIAdapter("fixture-account", "test", &MockLogger{})
	makeOpts := func(compact bool) *llmtypes.CallOptions {
		o := &llmtypes.CallOptions{}
		for _, f := range []llmtypes.CallOption{WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), WithProjectDirID(dir), WithResumeSessionID("123e4567-e89b-12d3-a456-426614174000")} {
			f(o)
		}
		if compact {
			llmtypes.WithCodingProviderIdleCompact()(o)
		}
		return o
	}
	if _, err := adapter.generateContentAppServer(context.Background(), nil, makeOpts(true)); err != nil {
		t.Fatal(err)
	}
	if InteractiveTurnActive(owner) {
		t.Fatal("retained idle process is steerable")
	}
	for _, text := range []string{"first message", "second message"} {
		response, err := adapter.generateContentAppServer(context.Background(), []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, text)}, makeOpts(false))
		if err != nil {
			t.Fatal(err)
		}
		if len(response.Choices) != 1 || response.Choices[0].Content != "reply" {
			t.Fatal("message settled without its own reply")
		}
	}
	if writer.starts != 2 {
		t.Fatalf("started %d message turns, want 2", writer.starts)
	}
	writer.rpc.finish(nil)
	if InteractiveTurnActive(owner) {
		t.Fatal("dead process is steerable")
	}
}
