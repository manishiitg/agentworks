package codexcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3140: an agent switch to Codex sent the earlier chat as text in front of
// the user's message. A fresh Codex thread now gets it as real conversation
// entries; a Codex without thread/inject_items, and a resumed thread, get the
// text so the history is never lost, and it is added only once.
func TestCodexHistoryInjectedNativelyWithTextFallback(t *testing.T) {
	items := []llmtypes.NativeHistoryItem{{Role: "user", Text: "Earlier conversation follows."}, {Role: "user", Text: "fix login"}, {Role: "assistant", Text: "did: ran npm test -> FAIL"}}
	run := func(newThread bool, reply func(codexRPCMessage) codexRPCMessage) (string, []codexRPCMessage) {
		writer := &idleReplyWriter{written: make(chan codexRPCMessage, 4)}
		r := &codexRPC{stdin: writer, pending: map[string]chan codexRPCMessage{}, events: make(chan codexRPCMessage, 4), done: make(chan struct{}), stop: func() {}}
		go func() {
			select {
			case msg := <-writer.written:
				r.mu.Lock()
				ch := r.pending[string(msg.ID)]
				r.mu.Unlock()
				ch <- reply(msg)
			case <-time.After(5 * time.Second):
			}
		}()
		ctx := llmtypes.WithNativeHistory(context.Background(), "owner", items)
		c := &CodexCLIAdapter{}
		prompt := c.injectCodexHistory(ctx, r, "owner", "thread-1", newThread, "fix that failure")
		if again := c.injectCodexHistory(ctx, r, "owner", "thread-1", newThread, "next"); again != "next" {
			t.Fatalf("history added twice: %q", again)
		}
		writer.mu.Lock()
		defer writer.mu.Unlock()
		return prompt, append([]codexRPCMessage(nil), writer.replies...)
	}
	ok := func(msg codexRPCMessage) codexRPCMessage {
		return codexRPCMessage{ID: msg.ID, Result: json.RawMessage(`{}`)}
	}

	prompt, sent := run(true, ok)
	if prompt != "fix that failure" || len(sent) != 1 || sent[0].Method != "thread/inject_items" {
		t.Fatalf("native injection: prompt=%q sent=%+v", prompt, sent)
	}
	var params struct {
		ThreadID string `json:"threadId"`
		Items    []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"items"`
	}
	if err := json.Unmarshal(sent[0].Params, &params); err != nil || params.ThreadID != "thread-1" || len(params.Items) != 3 || params.Items[2].Role != "assistant" || params.Items[2].Content[0].Type != "output_text" {
		t.Fatalf("items %v %+v", err, params)
	}

	prompt, _ = run(true, func(msg codexRPCMessage) codexRPCMessage {
		return codexRPCMessage{ID: msg.ID, Error: &codexRPCError{Code: -32601, Message: "unknown method"}}
	})
	if !strings.Contains(prompt, "assistant: did: ran npm test") || !strings.HasSuffix(prompt, "fix that failure") {
		t.Fatalf("older Codex lost the history: %q", prompt)
	}

	prompt, sent = run(false, ok)
	if len(sent) != 0 || !strings.Contains(prompt, "fix login") {
		t.Fatalf("resumed thread: prompt=%q sent=%d", prompt, len(sent))
	}
}
