package server

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/hex"
	"errors"
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

// Cursor from the Mac counts toward the person's token limits only if the relay reads the usage Cursor puts at the
// end of an agent run: gzip Connect frames carrying protobuf (update 14), then an end-of-stream trailer. Numbers
// as on a live run (1 Nov 2026), checked against the Cursor SDK's own usage.
func TestCursorRunUsage(t *testing.T) {
	varint := func(b []byte, v uint64) []byte { return binary.AppendUvarint(b, v) }
	field := func(num int, payload []byte) []byte {
		return append(varint(varint(nil, uint64(num<<3|2)), uint64(len(payload))), payload...)
	}
	number := func(num int, v uint64) []byte { return varint(varint(nil, uint64(num<<3)), v) }
	usage := append(append(append(number(1, 7865), number(2, 40)...), number(3, 2816)...), number(4, 0)...)
	message := field(1, field(14, usage))
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	_, _ = gz.Write(message)
	_ = gz.Close()
	frame := func(flag byte, body []byte) []byte {
		head := []byte{flag, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(head[1:], uint32(len(body)))
		return append(head, body...)
	}
	text := field(1, field(1, field(1, []byte("ok"))))
	stream := append(append(frame(0, text), frame(1, zipped.Bytes())...), frame(2, []byte(`{}`))...)
	if in, out, read, write, _, ok := cursorRunUsage(stream); !ok || in != 7865 || out != 40 || read != 2816 || write != 0 {
		t.Fatalf("cursor usage = %d %d %d %d %v", in, out, read, write, ok)
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

// Muse's serve lane asks for muse-spark-1.3 and a background model while the admin allows only the contributor
// model; the company proxy moves those to an allowed model instead of failing the turn (only allowed models run).
func TestAllowedSubstitute(t *testing.T) {
	allowed := []string{"muse-spark-1.3-contributor"}
	for model, want := range map[string]string{"muse-spark-1.3": "muse-spark-1.3-contributor", "muse-wonder-1.0-contributor": "muse-spark-1.3-contributor"} {
		if got := allowedSubstitute(model, allowed); got != want {
			t.Fatalf("%s -> %q, want %q", model, got, want)
		}
	}
	if got := allowedSubstitute("x", nil); got != "" {
		t.Fatalf("nothing allowed -> %q", got)
	}
}

// A used-up model with no other allowed model left (applyModelTokenLimit returns an error) was let through
// unlimited (review finding 1); both outcomes of the limit check refuse.
func TestModelLimitRefuses(t *testing.T) {
	if !modelLimitRefuses(true, nil) || !modelLimitRefuses(false, errors.New("no other model")) || modelLimitRefuses(false, nil) {
		t.Fatal("a used-up model must be refused whether or not another model is left")
	}
}

// A Cursor run's model is read from its start (review finding 3): a compressed start is read uncompressed, a body
// that cannot be decoded is reported unreadable (and refused), and the request id links it to the run's usage.
func TestCursorAppendModelRead(t *testing.T) {
	field := func(num int, value []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(num<<3|2))
		out = binary.AppendUvarint(out, uint64(len(value)))
		return append(out, value...)
	}
	body := append(field(1, []byte(hex.EncodeToString(field(1, field(9, field(1, []byte("composer-2.5"))))))), field(2, field(1, []byte("run-1")))...)
	var zipped bytes.Buffer
	gz := gzip.NewWriter(&zipped)
	_, _ = gz.Write(body)
	_ = gz.Close()
	if model, ok := cursorAppendModelRead(zipped.Bytes(), "gzip"); !ok || model != "composer-2.5" {
		t.Fatalf("gzip body = %q %v", model, ok)
	}
	if _, ok := cursorAppendModelRead(field(1, []byte("not hex")), ""); ok {
		t.Fatal("an undecodable body must be unreadable")
	}
	if id := cursorAppendRequestID(body); id != "run-1" {
		t.Fatalf("request id = %q", id)
	}
}
