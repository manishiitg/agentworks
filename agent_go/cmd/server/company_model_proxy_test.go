package server

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Mac use of a company account counts toward the person's token limits only if the proxy reads the reply's usage:
// from the final event of a stream (split across writes) or from a plain JSON reply.
func TestProxyUsageReadsFinalUsage(t *testing.T) {
	stream := &proxyUsage{stream: true}
	_, _ = stream.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":120,"))
	_, _ = stream.Write([]byte("\"output_tokens\":30,\"input_tokens_details\":{\"cached_tokens\":100},\"output_tokens_details\":{\"reasoning_tokens\":7}}}}\n\n"))
	if in, out, reasoning, cached, ok := stream.tokens(); !ok || in != 120 || out != 30 || reasoning != 7 || cached != 100 {
		t.Fatalf("stream usage = %d %d %d %d %v", in, out, reasoning, cached, ok)
	}
	// Anthropic: input (with cache reads) on message_start, the output total on the last message_delta.
	claude := &proxyUsage{stream: true}
	_, _ = claude.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":90,\"output_tokens\":1}}}\n\n"))
	_, _ = claude.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":42}}\n\n"))
	if in, out, _, cached, ok := claude.tokens(); !ok || in != 100 || out != 42 || cached != 90 {
		t.Fatalf("anthropic stream usage = %d %d %d %v", in, out, cached, ok)
	}
	plain := &proxyUsage{}
	_, _ = plain.Write([]byte(`{"id":"r1","usage":{"input_tokens":5,"output_tokens":2}}`))
	if in, out, _, _, ok := plain.tokens(); !ok || in != 5 || out != 2 {
		t.Fatalf("plain usage = %d %d %v", in, out, ok)
	}
}

// The server refuses a Cursor run on a model the account does not allow (PLAT-856-474); the model sits in the
// hex-encoded client message of the run's BidiAppend, captured from a real Cursor SDK run.
func TestCursorAppendModel(t *testing.T) {
	field := func(num int, value []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(num<<3|2))
		out = binary.AppendUvarint(out, uint64(len(value)))
		return append(out, value...)
	}
	run := append(field(2, field(1, field(1, []byte("hello")))), field(9, field(1, []byte("composer-2.5")))...)
	body := append(field(1, []byte(hex.EncodeToString(field(1, run)))), field(2, field(1, []byte("request-id")))...)
	if got := cursorAppendModel(body); got != "composer-2.5" {
		t.Fatalf("model = %q", got)
	}
	if got := cursorAppendModel(field(1, []byte(hex.EncodeToString(field(2, []byte("tool result")))))); got != "" {
		t.Fatalf("non-run append model = %q", got)
	}
}

// A used-up model with no other allowed model left (applyModelTokenLimit returns an error) was let through
// unlimited (review finding 1); both outcomes of the limit check refuse.
func TestModelLimitRefuses(t *testing.T) {
	if !modelLimitRefuses(true, nil) || !modelLimitRefuses(false, errors.New("no other model")) || modelLimitRefuses(false, nil) {
		t.Fatal("a used-up model must be refused whether or not another model is left")
	}
}

// A person at their token limit keeps the company account on the Mac (review of 6): the server answers a limit with
// 429, not the 403 that means "no longer yours", and the Mac leaves the account as it is on a 429.
func TestTokenLimitKeepsCompanyAccount(t *testing.T) {
	if got := admissionStatus(&sharedAccountTokenLimitError{"limit"}); got != http.StatusTooManyRequests {
		t.Fatalf("limit status = %d", got)
	}
	if got := admissionStatus(errors.New("not available to you")); got != http.StatusForbidden {
		t.Fatalf("refusal status = %d", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer server.Close()
	if _, known := companyAccountOffered(context.Background(), desktopConnection{Server: server.URL, Name: "test"}, "codex-cli"); known {
		t.Fatal("a 429 must leave the company account as it is")
	}
}
