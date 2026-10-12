package agentusage

import (
	"strings"
	"testing"
)

func TestClaudeNativeUsage(t *testing.T) {
	raw := `{"type":"user","sessionId":"session","uuid":"turn"}
{"type":"assistant","sessionId":"session","message":{"id":"call","model":"model","content":[{"text":"DO NOT EXPORT"}],"usage":{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":10,"cache_creation_input_tokens":3}}}
{"type":"assistant","sessionId":"session","message":{"id":"call","model":"model","usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":10,"cache_creation_input_tokens":3}}}`
	n, e := Claude(strings.NewReader(raw), "session")
	if e != nil || n.Input != 15 || n.Output != 4 || n.Cached != 13 || n.Calls == nil || *n.Calls != 1 || n.Turns == nil || *n.Turns != 1 {
		t.Fatalf("numeric projection: %+v %v", n, e)
	}
	ledger := &Ledger{}
	first := strings.Split(raw, "\n")
	prefix := strings.Join(first[:2], "\n") + "\n"
	if _, e = ClaudeChunk(strings.NewReader(prefix), "session", ledger); e != nil {
		t.Fatal(e)
	}
	position := ledger.Offset
	n, e = ClaudeChunk(strings.NewReader(first[2]+"\n"), "session", ledger)
	if e != nil || n.Input != 15 || n.Output != 4 || *n.Calls != 1 || ledger.Offset <= position {
		t.Fatal("numeric-only bounded catch-up duplicated or lost an updated call")
	}
	oversized := strings.Repeat("x", MaxLineBytes+20) + "\n" + `{"type":"assistant","message":{"usage":{"input_tokens":999}}}` + "\n" + first[2] + "\n"
	skipped := &Ledger{}
	n, e = ClaudeChunk(strings.NewReader(oversized), "session", skipped)
	if e != nil || n.SkippedRecords != 2 || n.Input != 15 || skipped.Offset != int64(len(oversized)) {
		t.Fatal("skipped record stalled numeric catch-up")
	}
	if _, e = Claude(strings.NewReader(raw), "other"); e != ErrIdentity {
		t.Fatal("foreign session accepted")
	}
}

func TestCodexNativeUsage(t *testing.T) {
	raw := `{"type":"session_meta","payload":{"id":"session"}}
{"type":"turn_context","payload":{"model":"model"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":5,"cached_input_tokens":80},"last_token_usage":{"input_tokens":1000}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"output_tokens":9,"cached_input_tokens":90}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"output_tokens":9,"cached_input_tokens":90}}}}`
	n, e := Codex(strings.NewReader(raw), "session")
	if e != nil || n.Input != 120 || n.Output != 9 || n.Cached != 90 || n.Model != "model" {
		t.Fatalf("native cumulative total: %+v %v", n, e)
	}
	d, ok := n.Counters.Difference(Counters{100, 5, 80})
	if !ok || d.Input != 20 || d.Output != 4 || d.Cached != 10 {
		t.Fatal("hourly difference incorrect")
	}
	if _, e = Codex(strings.NewReader(raw), "other"); e != ErrIdentity {
		t.Fatal("foreign rollout accepted")
	}
}

func TestMuseNativeUsage(t *testing.T) {
	line := `{"id":"event","stream":{"kind":"session","id":"session"},"payload":{"kind":"run","run_id":"turn","event":{"kind":"goal_usage_attribution","record":{"usage_family":"provider","quantity":{"unit":"tokens","reported":true,"input_tokens":12,"output_tokens":3,"cached_tokens":8}}}}}`
	raw := line + "\n" + line + "\n" + `{"id":"ignored","payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"usage_family":"heuristic","quantity":{"reported":true,"input_tokens":999}}}}}`
	n, e := Muse(strings.NewReader(raw), "session")
	if e != nil || n.Input != 12 || n.Output != 3 || n.Cached != 8 || n.Calls == nil || *n.Calls != 1 {
		t.Fatalf("native attribution: %+v %v", n, e)
	}
	if _, e = Muse(strings.NewReader(raw), "other"); e != ErrIdentity {
		t.Fatal("foreign stream accepted")
	}
}

func TestPiNativeUsage(t *testing.T) {
	// SDK totals already include cache warming, compaction and tool results.
	raw := `{"sessionId":"session","model":"model","userMessages":2,"tokens":{"input":4,"output":9,"cacheRead":20,"cacheWrite":3,"total":36},"cost":999,"sessionFile":"DO NOT EXPORT"}`
	n, e := PiStats(strings.NewReader(raw), "session")
	if e != nil || n.Input != 27 || n.Output != 9 || n.Cached != 23 || n.Turns == nil || *n.Turns != 2 {
		t.Fatalf("native SDK total: %+v %v", n, e)
	}
	if _, e = PiStats(strings.NewReader(raw), "other"); e != ErrIdentity {
		t.Fatal("foreign SDK session accepted")
	}
}
