package picli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-987: Pi owns this retry loop; report its lifecycle and honor recovery.
func TestPiRPCNativeRetryLifecycleAndFinalFailure(t *testing.T) {
	for _, recovered := range []bool{true, false} {
		r := &piRPC{events: make(chan piRPCRecord, 8), done: make(chan struct{})}
		s := &piRPCSession{nativeID: "retry-session", model: "example/model"}
		lines := []string{
			`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"upstream busy"}}`,
			`{"type":"auto_retry_start","attempt":2,"maxAttempts":3,"delayMs":1000}`,
		}
		if recovered {
			lines = append(lines, `{"type":"auto_retry_end","success":true}`, `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"recovered"}]}}`)
		} else {
			lines = append(lines, `{"type":"auto_retry_end","success":false,"finalError":"No API key found for example"}`)
		}
		lines = append(lines, `{"type":"agent_settled"}`)
		for _, line := range lines {
			var record piRPCRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			record.piJSONEvent.Type = record.Type
			record.Raw = []byte(line)
			r.events <- record
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var notices []*llmtypes.NativeRetry
		response, err := (&PiCLIAdapter{}).consumeRPCTurn(ctx, s, r, func(chunk llmtypes.StreamChunk) {
			if chunk.NativeRetry != nil {
				notices = append(notices, chunk.NativeRetry)
			}
		}, nil)
		cancel()
		if recovered && (err != nil || response == nil || response.Choices[0].Content != "recovered") {
			t.Fatalf("recovered native retry failed: %v", err)
		}
		if !recovered && (response != nil || llmerrors.KindOf(err) != llmerrors.KindAuth) {
			t.Fatalf("failed retry looked finished: %v", err)
		}
		if len(notices) != 2 || notices[0].Phase != "start" || notices[0].Attempt != 2 || notices[0].DelayMS != 1000 || notices[1].Phase != "end" || notices[1].Success != recovered || notices[0].NativeSessionID != "retry-session" {
			t.Fatalf("native retry not preserved: %+v", notices)
		}
	}
}

func TestPiRPCSeparatesCredentialsFromUnavailableModelWithoutRewritingHostErrors(t *testing.T) {
	for _, tc := range []struct {
		message string
		kind    llmerrors.Kind
	}{
		{"No API key found for example. Use /login", llmerrors.KindAuth},
		{"Model example/model not found", llmerrors.KindModelNotFound},
	} {
		native := &piRPCCommandError{Command: "set_model", Message: tc.message}
		if got := piRPCRequestFailure("example/model", "native", native, false); llmerrors.KindOf(got) != tc.kind {
			t.Fatalf("wrong failure %q: %v", tc.message, got)
		}
		host := errors.New(tc.message)
		if got := piRPCRequestFailure("example/model", "native", host, false); got != host {
			t.Fatal("host error rewritten as native rejection")
		}
	}
}
