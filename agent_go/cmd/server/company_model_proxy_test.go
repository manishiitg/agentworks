package server

import "testing"

// Mac use of a company account counts toward the person's token limits only if the proxy reads the reply's usage:
// from the final event of a stream (split across writes) or from a plain JSON reply.
func TestProxyUsageReadsFinalUsage(t *testing.T) {
	stream := &proxyUsage{stream: true}
	_, _ = stream.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":120,"))
	_, _ = stream.Write([]byte("\"output_tokens\":30,\"input_tokens_details\":{\"cached_tokens\":100},\"output_tokens_details\":{\"reasoning_tokens\":7}}}}\n\n"))
	if in, out, reasoning, cached, ok := stream.tokens(); !ok || in != 120 || out != 30 || reasoning != 7 || cached != 100 {
		t.Fatalf("stream usage = %d %d %d %d %v", in, out, reasoning, cached, ok)
	}
	plain := &proxyUsage{}
	_, _ = plain.Write([]byte(`{"id":"r1","usage":{"input_tokens":5,"output_tokens":2}}`))
	if in, out, _, _, ok := plain.tokens(); !ok || in != 5 || out != 2 {
		t.Fatalf("plain usage = %d %d %v", in, out, ok)
	}
}
