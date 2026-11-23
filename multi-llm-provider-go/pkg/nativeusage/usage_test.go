package nativeusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-872: mirrors, growing message usage and inclusive snapshots must not
// create a second charge. Fixtures retain the actual CLI accounting shapes.
func TestNativeUsageConservation(t *testing.T) {
	cases := []struct {
		provider, rows, child string
		count                 int
		input, output, cache  int64
	}{
		{"claude-code", `{"type":"user","uuid":"turn1","sessionId":"native"}
{"type":"assistant","sessionId":"native","timestamp":"2026-10-25T12:00:00Z","message":{"id":"msg1","model":"model-a","content":[{"type":"text","text":"partial"}],"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":4}}}
{"type":"assistant","sessionId":"native","timestamp":"2026-10-25T12:01:00Z","message":{"id":"msg1","model":"model-a","content":[{"type":"tool_use","id":"tool1","name":"Agent"}],"usage":{"input_tokens":10,"output_tokens":8,"cache_read_input_tokens":4}}}
{"type":"assistant","sessionId":"native","message":{"id":"child1","model":"model-b","usage":{"input_tokens":3,"output_tokens":2}}}
{"type":"cost-state","sessionId":"native","modelUsage":{"model-a":{"inputTokens":10,"outputTokens":8},"model-b":{"inputTokens":3,"outputTokens":2}}}
`, `{"type":"assistant","sessionId":"native","message":{"id":"child1","model":"model-b","usage":{"input_tokens":3,"output_tokens":2}}}
`, 2, 13, 10, 4},
		{"codex-cli", `{"type":"session_meta","payload":{"id":"native"}}
{"type":"turn_context","payload":{"turn_id":"turn1","model":"model-a"}}
{"type":"token_usage_record","payload":{"turn_id":"turn1","response_id":"response1","usage":{"input_tokens":13,"cached_input_tokens":4,"output_tokens":8}}}
{"type":"token_usage_record","payload":{"turn_id":"turn1","response_id":"response1","usage":{"input_tokens":13,"cached_input_tokens":4,"output_tokens":8}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":13,"cached_input_tokens":4,"output_tokens":8}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":13,"cached_input_tokens":4,"output_tokens":8}}}}
`, "", 1, 13, 8, 4},
		{"muse-cli", `{"sequence":1,"payload":{"kind":"run","run_id":"run1","event":{"kind":"goal_usage_attribution","record":{"usage_family":"provider","usage_id":"usage1","owner":{"owner_type":"main_root","run_id":"run1"},"quantity":{"reported":true,"unit":"tokens","input_tokens":13,"cached_tokens":4,"output_tokens":8}}}}}
{"sequence":2,"payload":{"kind":"run","run_id":"run1","event":{"kind":"model_completed","model":"model-a","usage":{"input_tokens":13,"cached_tokens":4,"output_tokens":8}}}}
{"sequence":3,"payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"usage_family":"tool","usage_id":"tool1","quantity":{"reported":true,"unit":"tokens","input_tokens":99}}}}}
`, `{"sequence":1,"payload":{"kind":"run","run_id":"child1","event":{"kind":"goal_usage_attribution","record":{"usage_family":"provider","usage_id":"usage2","owner":{"owner_type":"reminder_child","run_id":"child1"},"quantity":{"reported":true,"unit":"tokens","input_tokens":3,"output_tokens":2}}}}}
{"sequence":2,"payload":{"kind":"run","run_id":"child1","event":{"kind":"model_completed","model":"model-b","usage":{"input_tokens":3,"output_tokens":2}}}}
`, 2, 16, 10, 4},
		{"pi-cli", `{"type":"session","id":"native"}
{"type":"message","id":"msg1","message":{"role":"assistant","model":"model-a","usage":{"input":13,"output":8,"cacheRead":4}}}
`, "", 1, 13, 8, 4},
		{"cursor-cli", ``, "", 0, 0, 0, 0},
		{"agy-cli", ``, "", 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native.jsonl")
			if err := os.WriteFile(path, []byte(tc.rows), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.child != "" {
				child := filepath.Join(filepath.Dir(path), "subagent", "child1", "session.jsonl")
				if tc.provider == "claude-code" {
					child = filepath.Join(path[:len(path)-len(".jsonl")], "subagents", "agent-child1.jsonl")
				}
				if err := os.MkdirAll(filepath.Dir(child), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(child, []byte(tc.child), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := Read(context.Background(), tc.provider, "native", path)
			if err != nil {
				t.Fatal(err)
			}
			if !s.CaughtUp || s.Gaps != 0 || len(s.Records) != tc.count {
				t.Fatalf("coverage %+v", s)
			}
			var input, output, cache int64
			keys := map[string]bool{}
			for _, r := range s.Records {
				if tc.provider == "claude-code" && r.ID == "msg1" && r.Timestamp.Minute() != 0 {
					t.Fatal("growing usage moved to a later generation")
				}
				if tc.provider == "claude-code" && r.ID == "child1" && r.OwnerType != "helper" {
					t.Fatal("helper identity must come from its own file")
				}
				input += r.Input
				output += r.Output
				cache += r.CacheRead
				key := r.Key(tc.provider, "account")
				if keys[key] {
					t.Fatal("source charged twice")
				}
				keys[key] = true
			}
			if input != tc.input || output != tc.output || cache != tc.cache {
				t.Fatalf("usage=%d/%d/%d want=%d/%d/%d", input, output, cache, tc.input, tc.output, tc.cache)
			}
			// A second reader (foreground vs runtime) must produce the same keys.
			again, err := Read(context.Background(), tc.provider, "native", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range again.Records {
				if !keys[r.Key(tc.provider, "account")] {
					t.Fatal("reader changed source identity")
				}
			}
		})
	}
}
