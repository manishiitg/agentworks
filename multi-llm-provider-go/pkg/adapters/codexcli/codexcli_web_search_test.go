package codexcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real codex-cli 0.160.1 records of one web search (2026-10-07, results cut to
// two): the `exec --json` item.completed event and the rollout row the tmux
// transcript stream reads. Both must reach the chat's search card as the query
// plus a `Links: [...]` result (frontend webSearchToolCall.ts, PLAT-667).
func TestCodexWebSearchCarriesQueryAndSources(t *testing.T) {
	const execLine = `{"type":"item.completed","item":{"id":"item_1","type":"web_search","id":"exec-6e128f73-ad2b-47eb-8026-f4d0f331aa6d","query":"latest Go release version site:go.dev","action":{"type":"search","query":"latest Go release version site:go.dev"},"results":[{"type":"text_result","domain":"go.dev","ref_id":"turn0search0","snippet":"The latest Go release, version 1.27, arrives in August 2026,","title":"Go 1.27 Release Notes - The Go Programming Language","url":"https://go.dev/doc/go1.27"},{"type":"text_result","domain":"go.dev","ref_id":"turn0search1","snippet":"Each major Go release is supported until there are two newer","title":"Release History - The Go Programming Language","url":"https://go.dev/doc/devel/release"}]}}`
	const rolloutLine = `{"timestamp":"2026-10-07T10:35:21.448Z","ordinal":14,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Extension","kind":"web.search","id":"exec-6e128f73-ad2b-47eb-8026-f4d0f331aa6d","query":"latest Go release version site:go.dev","action":{"type":"search","query":"latest Go release version site:go.dev","queries":null},"results":[{"type":"text_result","domain":"go.dev","ref_id":"turn0search0","snippet":"The latest Go release, version 1.27, arrives in August 2026,","title":"Go 1.27 Release Notes - The Go Programming Language","url":"https://go.dev/doc/go1.27"},{"type":"text_result","domain":"go.dev","ref_id":"turn0search1","snippet":"Each major Go release is supported until there are two newer","title":"Release History - The Go Programming Language","url":"https://go.dev/doc/devel/release"}]},"started_at_ms":1791369318273,"completed_at_ms":1791369321448}}`
	const wantResult = `Web search results for query: "latest Go release version site:go.dev"` + "\n\n" +
		`Links: [{"title":"Go 1.27 Release Notes - The Go Programming Language","url":"https://go.dev/doc/go1.27"},{"title":"Release History - The Go Programming Language","url":"https://go.dev/doc/devel/release"}]`

	var event codexExecEvent
	if err := json.Unmarshal([]byte(execLine), &event); err != nil {
		t.Fatal(err)
	}
	if got := codexToolItemResult(event.Item); got != wantResult {
		t.Fatalf("exec result:\n%s", got)
	}
	if args := codexToolItemArgs(event.Item); !strings.Contains(args, `"query":"latest Go release version site:go.dev"`) {
		t.Fatalf("exec args: %s", args)
	}

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(rolloutLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	events, _, err := readCodexTranscriptEventsFromFile(path, 0, time.Time{}, map[string]time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ToolName != "web_search" || !events[1].IsToolEnd || events[0].ToolCallID != events[1].ToolCallID {
		t.Fatalf("want one web_search start/end pair, got %+v", events)
	}
	if !strings.Contains(events[0].ToolArgs, `"query":"latest Go release version site:go.dev"`) {
		t.Fatalf("rollout args: %s", events[0].ToolArgs)
	}
	if events[1].ToolResult != wantResult || events[1].ToolDuration != 3175*time.Millisecond {
		t.Fatalf("rollout end: %q %v", events[1].ToolResult, events[1].ToolDuration)
	}
}
