package agycli

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

type agyConfigWireWriter func([]byte) (int, error)

func (w agyConfigWireWriter) Write(b []byte) (int, error) { return w(b) }
func (w agyConfigWireWriter) Close() error                { return nil }

// PLAT-746-831: changing effort on the same model was ignored, thought option
// ids were assumed, and ACP refusal was silently dropped. Exercise the real RPC.
func TestAgyACPReassertsThoughtCategoryEveryTurnAndLogsRefusal(t *testing.T) {
	rpc := &agyACP{pending: map[string]chan agyACPMessage{}, done: make(chan struct{})}
	s := &agyACPSession{rpc: rpc, sessionID: "selected-session"}
	var thoughtValues []string
	modelCalls := 0
	failThought := false
	rpc.stdin = agyConfigWireWriter(func(raw []byte) (int, error) {
		var request struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params map[string]string `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request.Method != "session/set_config_option" || request.Params["sessionId"] != "selected-session" {
			t.Fatal("wrong scoped RPC")
		}
		reply := agyACPMessage{ID: request.ID, Result: json.RawMessage(`{"configOptions":[{"id":"thought_level","category":"other","type":"select"},{"id":"model-thinking","category":"thought_level","type":"select"}]}`)}
		if request.Params["configId"] == "model" {
			modelCalls++
		} else {
			if request.Params["configId"] != "model-thinking" {
				t.Fatal("thought option selected by id instead of category")
			}
			thoughtValues = append(thoughtValues, request.Params["value"])
			if failThought {
				reply.Error = &agyACPError{Code: -32602, Message: "PRIVATE_VALUE must not be logged"}
			}
		}
		rpc.mu.Lock()
		ch := rpc.pending[string(request.ID)]
		rpc.mu.Unlock()
		ch <- reply
		return len(raw), nil
	})
	ctx := context.Background()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, thought := range []string{"low", "high"} {
		s.turnMu.Lock()
		err := s.applySelection(ctx, "same-model", "model-option", thought)
		s.turnMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if modelCalls != 1 || strings.Join(thoughtValues, ",") != "low,high" {
		t.Fatalf("model=%d thinking=%v", modelCalls, thoughtValues)
	}
	// Every accepted level is logged, so a chosen level that reaches agy leaves a trace (the log carries no values).
	if !strings.Contains(output.String(), "[AGY_CONFIG] action=applied lane=acp level=low model=same-model") || !strings.Contains(output.String(), "action=applied lane=acp level=high") {
		t.Fatalf("missing applied log: %q", output.String())
	}
	output.Reset()
	failThought = true
	s.turnMu.Lock()
	err := s.applySelection(ctx, "same-model", "model-option", "high")
	s.turnMu.Unlock()
	if err != nil {
		t.Fatal("thought failure added a fail-closed turn gate")
	}
	if !strings.Contains(output.String(), "action=refused lane=acp thought_level_set_failed=true code=-32602") || strings.Contains(output.String(), "PRIVATE_VALUE") {
		t.Fatalf("missing or unsafe error log: %q", output.String())
	}
	// Missing capability metadata must not discard a previously supported setting.
	unknownRPC := &agyACP{pending: map[string]chan agyACPMessage{}, done: make(chan struct{})}
	unknown := &agyACPSession{rpc: unknownRPC, sessionID: "selected-session", model: "same-model"}
	called := false
	unknownRPC.stdin = agyConfigWireWriter(func(raw []byte) (int, error) {
		var request struct {
			ID     json.RawMessage   `json:"id"`
			Params map[string]string `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if request.Params["configId"] != "thought_level" || request.Params["value"] != "low" {
			t.Fatal("missing configOptions discarded the thought setting")
		}
		called = true
		unknownRPC.mu.Lock()
		ch := unknownRPC.pending[string(request.ID)]
		unknownRPC.mu.Unlock()
		ch <- agyACPMessage{Result: json.RawMessage(`{}`)}
		return len(raw), nil
	})
	unknown.turnMu.Lock()
	err = unknown.applySelection(ctx, "same-model", "model-option", "low")
	unknown.turnMu.Unlock()
	if err != nil || !called {
		t.Fatalf("unknown-capability thought setting: called=%t err=%v", called, err)
	}

}
