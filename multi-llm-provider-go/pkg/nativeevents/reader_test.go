package nativeevents

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are format/identity regressions. Passing replay is deliberately not
// labelled as acceptance of an installed, signed-in coding provider.
func TestNativeRecordConformance(t *testing.T) {
	cases := []struct {
		provider, rows string
		want           []string
	}{
		// PLAT-746-764: an async dispatch acknowledgement and turn completion
		// must not settle or discard a still-unanswered question.
		{"codex-cli", `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-question"}}
{"type":"response_item","payload":{"type":"function_call","name":"request_user_input_async","call_id":"call-question","arguments":"{\"questions\":[{\"title\":\"Which folder?\",\"options\":[\"Existing\",\"New\"]}]}"}}
{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-question","output":"{\"accepted\":true}"}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-question"}}
`, []string{"turn:started", "question:requested", "turn:completed"}},
		{"claude-code", `{"type":"user","uuid":"u1","sessionId":"session-test","origin":{"kind":"human"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"tool_use","id":"tool1","name":"Agent"},{"type":"thinking","thinking":"PRIVATE_THOUGHT"}]}}
{"type":"user","uuid":"result1","parentUuid":"a1","toolUseResult":{"agentId":"task1","isAsync":true},"message":{"content":[{"type":"tool_result","tool_use_id":"tool1"}]}}
{"type":"user","uuid":"notice1","parentUuid":"result1","turnOrigin":"task_notification","origin":{"kind":"task-notification"},"message":{"content":"<task-notification><task-id>task1</task-id><tool-use-id>tool1</tool-use-id><status>completed</status></task-notification>"}}
{"type":"assistant","uuid":"a2t","parentUuid":"notice1","message":{"id":"msg-helper","content":[{"type":"thinking","thinking":"PRIVATE_THOUGHT"}]}}
{"type":"assistant","uuid":"a2","parentUuid":"a2t","message":{"id":"msg-helper","content":[{"type":"text","text":"The helper finished."}]}}
`, []string{"turn:started", "tool:started", "helper:started", "tool:finished", "helper:started", "helper:finished", "turn:started", "reply:committed"}},
		{"claude-code", `{"type":"user","uuid":"u1","origin":{"kind":"human"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"tool_use","id":"tool1","name":"Agent"}]}}
{"type":"user","uuid":"result1","parentUuid":"a1","toolUseResult":{"agentId":"task1","isAsync":false},"message":{"content":[{"type":"tool_result","tool_use_id":"tool1"}]}}
`, []string{"turn:started", "tool:started", "helper:started", "tool:finished", "helper:finished"}},
		{"claude-code", `{"type":"user","uuid":"u1","origin":{"kind":"human"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":[{"type":"tool_use","id":"bash1","name":"Bash","input":{"run_in_background":false}},{"type":"tool_use","id":"todo1","name":"TaskUpdate"}]}}
{"type":"user","uuid":"result1","parentUuid":"a1","toolUseResult":{"backgroundTaskId":"command1","timedOutAfterMs":1000},"message":{"content":[{"type":"tool_result","tool_use_id":"bash1"}]}}
{"type":"user","uuid":"todo-result","parentUuid":"result1","toolUseResult":{"taskId":"todo1"},"message":{"content":[{"type":"tool_result","tool_use_id":"todo1"}]}}
{"type":"user","uuid":"notice","parentUuid":"todo-result","queueTranscriptOnly":true,"origin":{"kind":"task-notification"},"message":{"content":"<task-notification><task-id>command1</task-id><status>stopped</status></task-notification>"}}
`, []string{"turn:started", "tool:started", "tool:started", "background_command:started", "tool:finished", "background_command:stopped"}},
		{"codex-cli", `{"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"message1","text":"Done"}}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","error":{"code":"capacity"}}}
`, []string{"turn:started", "reply:committed", "turn:failed"}},
		{"pi-cli", `{"type":"session","id":"session-test"}
{"type":"message","id":"p1","message":{"role":"user","content":[{"type":"text","text":"Question"}]}}
{"type":"message","id":"p2","message":{"role":"assistant","content":[{"type":"thinking","thinking":"PRIVATE_THOUGHT"},{"type":"text","text":"Answer"}]}}
`, []string{"reply:committed"}},
		{"muse-cli", `{"sequence":1,"transaction_id":"tx1","payload":{"kind":"run","run_id":"r1","event":{"kind":"assistant_message_committed","message_id":"m1","text":"Answer"}}}
`, []string{"reply:committed"}},
		{"muse-cli", `{"sequence":2,"payload":{"kind":"task","run_id":"r1","event":{"kind":"status","task_id":"task1","details":{"phase":"opening_stream","facets":[{"kind":"external_attempt","attempt":1}]}}}}
{"sequence":3,"payload":{"kind":"task","run_id":"r1","event":{"kind":"status","task_id":"task1","details":{"phase":"opening_stream","facets":[{"kind":"external_attempt","attempt":2}]}}}}
{"sequence":4,"payload":{"kind":"task","run_id":"r1","event":{"kind":"status","task_id":"task1","details":{"phase":"stream_succeeded","facets":[{"kind":"external_attempt","attempt":2}]}}}}
{"sequence":5,"payload":{"kind":"run","run_id":"r1","event":{"kind":"terminal","terminal":"completed","turn_duration_ms":500}}}
`, []string{"status:opening_stream", "retry:retrying", "status:stream_succeeded", "turn:completed"}},
		// A Muse task that fails or is cancelled closes its row like a completed one.
		{"muse-cli", `{"sequence":6,"payload":{"kind":"task","run_id":"r2","event":{"kind":"task_started","task_id":"t1"}}}
{"sequence":7,"payload":{"kind":"task","run_id":"r2","event":{"kind":"task_failed","task_id":"t1"}}}
{"sequence":8,"payload":{"kind":"task","run_id":"r2","event":{"kind":"task_cancelled","task_id":"t2"}}}
`, []string{"helper:started", "helper:failed", "helper:stopped"}},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(p, []byte(tc.rows), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := Read(context.Background(), tc.provider, "session-test", p, Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range b.Records {
				got = append(got, r.Kind+":"+r.State)
				// PLAT-746: the synthetic helper follow-up's committed reply is
				// also streamed as a native block. Preserve that exact identity,
				// though Claude writes the thinking and text blocks on separate
				// lines (a mismatch here showed the reply three times).
				if r.Provider == "claude-code" && r.Origin == "helper_finished" && r.Kind == "reply" && r.AssistantBlockID != "claude:msg-helper:1" {
					t.Fatalf("lost native helper reply block identity: %+v", r)
				}
				if strings.Contains(r.Text, "PRIVATE_THOUGHT") {
					t.Fatal("reasoning escaped")
				}
				if r.ID == "" {
					t.Fatal("missing identity")
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v want %v gaps=%d unknown=%v", got, tc.want, b.Gaps, b.Unknown)
			}
			again, err := Read(context.Background(), tc.provider, "session-test", p, b.Next)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Records) != 0 {
				t.Fatal("replayed committed records")
			}
		})
	}
}

func TestNativeCursorPartialAndRebind(t *testing.T) {
	p := filepath.Join(t.TempDir(), "session.jsonl")
	first := `{"type":"message","id":"one","message":{"role":"assistant","content":[{"type":"text","text":"first"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(first+`{"type":"message"`), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := Read(context.Background(), "pi-cli", "selected-session", p, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Next.Offset != int64(len(first)) {
		t.Fatal("partial record was consumed")
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("x", len(first)+100)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), "pi-cli", "selected-session", p, b.Next); err == nil {
		t.Fatal("rewritten source was silently accepted")
	}
}

// PLAT-746-899: a Codex command that finished after its turn ended was closed
// from the rollout with an empty card, because the decoder dropped its output.
func TestCodexCommandCompletionCarriesItsOutput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "session.jsonl")
	// #3132: a helper's finish written after its parent's turn ended (Codex
	// 0.170 rollout) is a notice the host wakes the parent with; one inside
	// the turn (the parent waited for it) and a start are not.
	rows := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","exit_code":0,"aggregated_output":"VERIFY: PASSED\n"}}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"SubAgentActivity","id":"call_s","kind":"started","agent_thread_id":"child-0","agent_path":"/root/waited"}}}
{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"SubAgentActivity","id":"subagent-completed-0","kind":"completed","agent_thread_id":"child-0","agent_path":"/root/waited"}}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"SubAgentActivity","id":"subagent-completed-1","kind":"completed","agent_thread_id":"child-1","agent_path":"/root/helper"}}}
`
	if err := os.WriteFile(p, []byte(rows), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := Read(context.Background(), "codex-cli", "session-test", p, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var helpers []llmtypes.NativeRecord
	for _, r := range b.Records[1:] {
		if r.Kind == "notice" {
			helpers = append(helpers, r)
		}
	}
	if b.Records[0].Name != "Command" || b.Records[0].Output != "VERIFY: PASSED\n" || len(helpers) != 1 {
		t.Fatalf("records=%+v", b.Records)
	}
	if h := helpers[0]; h.Kind != "notice" || h.State != "subagent_finished" || h.Origin != "codex_subagent" || h.TaskID != "child-1" || h.Name != "/root/helper" || h.TurnID != "turn-1" || h.ID != "subagent-completed-1" {
		t.Fatalf("helper finish = %+v", h)
	}
	if data, _ := json.Marshal(b.Records[0]); strings.Contains(string(data), "VERIFY") {
		t.Fatalf("command output must not be serialized: %s", data)
	}
}

// PLAT-746-941: a task id alone is not an intentional or timed-out background shell.
func TestClaudeBackgroundShellClassification(t *testing.T) {
	for _, tc := range []struct{ name, input, result, mode string }{
		{"ordinary", `{"command":"gh pr list","run_in_background":false}`, `{"backgroundTaskId":"bg"}`, ""},
		{"explicit", `{"command":"TOKEN=private deploy.sh verify\nignored","run_in_background":true}`, `{"backgroundTaskId":"bg"}`, "explicit"},
		{"automatic", `{"command":"deploy.sh verify"}`, `{"backgroundTaskId":"bg","timedOutAfterMs":1000}`, "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "session.jsonl")
			first := `{"type":"user","uuid":"u","origin":{"kind":"human"}}` + "\n" + `{"type":"assistant","uuid":"a","parentUuid":"u","message":{"content":[{"type":"tool_use","id":"bash","name":"Bash","input":` + tc.input + `}]}}` + "\n"
			if err := os.WriteFile(p, []byte(first), 0600); err != nil {
				t.Fatal(err)
			}
			initial, err := Read(context.Background(), "claude-code", "session-test", p, Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			// Real observers persist their cursor between the tool start and result.
			raw, err := json.Marshal(initial.Next)
			if err != nil {
				t.Fatal(err)
			}
			var cursor Cursor
			if err := json.Unmarshal(raw, &cursor); err != nil {
				t.Fatal(err)
			}
			rest := `{"type":"user","uuid":"r","parentUuid":"a","toolUseResult":` + tc.result + `,"message":{"content":[{"type":"tool_result","tool_use_id":"bash"}]}}` + "\n" + `{"type":"user","uuid":"n","parentUuid":"r","origin":{"kind":"task-notification"},"message":{"content":"<task-notification><task-id>bg</task-id><tool-use-id>bash</tool-use-id><status>completed</status></task-notification>"}}` + "\n"
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(rest)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			batch, err := Read(context.Background(), "claude-code", "session-test", p, cursor)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, r := range batch.Records {
				if r.Kind != "background_command" {
					continue
				}
				count++
				if r.BackgroundMode != tc.mode || r.CommandLabel != "deploy.sh verify" {
					t.Fatalf("incorrect shell label/classification: %+v", r)
				}
			}
			want := 2
			if tc.mode == "" {
				want = 0
			}
			if count != want {
				t.Fatalf("background rows=%d want %d", count, want)
			}
		})
	}
}

// PLAT-746-941: start foreground helpers before their result so a long one can
// appear live; presentation hides the short foreground lifetime, not tool steps.
func TestClaudeHelperMetadataStartsBeforeForegroundResult(t *testing.T) {
	for _, asynchronous := range []bool{true, false} {
		t.Run(fmt.Sprint(asynchronous), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "helpers.jsonl")
			rows := `{"type":"user","uuid":"u","timestamp":"2026-11-14T10:00:00Z","origin":{"kind":"human"}}` + "\n" + `{"type":"assistant","uuid":"a","parentUuid":"u","timestamp":"2026-11-14T10:00:00Z","message":{"content":[{"type":"tool_use","id":"helper-tool","name":"Agent","input":{"subagent_type":"Explore","description":"TOKEN=private Grepping coding.go\nDo not retain this line","run_in_background":` + fmt.Sprint(asynchronous) + `}}]}}` + "\n"
			if err := os.WriteFile(p, []byte(rows), 0600); err != nil {
				t.Fatal(err)
			}
			first, err := Read(context.Background(), "claude-code", "session-test", p, Cursor{})
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Records) != 3 || first.Records[1].Kind != "tool" || first.Records[2].Kind != "helper" || first.Records[2].State != "started" {
				t.Fatalf("missing live helper start: %+v", first.Records)
			}
			cursor, err := json.Marshal(first.Next)
			if err != nil {
				t.Fatal(err)
			}
			var next Cursor
			if err = json.Unmarshal(cursor, &next); err != nil {
				t.Fatal(err)
			}
			result := `{"type":"user","uuid":"r","parentUuid":"a","timestamp":"2026-11-14T10:00:05Z","toolUseResult":{"agentId":"helper-id","isAsync":` + fmt.Sprint(asynchronous) + `},"message":{"content":[{"type":"tool_result","tool_use_id":"helper-tool"}]}}` + "\n"
			f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(result)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			final, err := Read(context.Background(), "claude-code", "session-test", p, next)
			if err != nil {
				t.Fatal(err)
			}
			if len(final.Records) != 2 || final.Records[0].Kind != "tool" {
				t.Fatalf("lost normal step: %+v", final.Records)
			}
			for _, h := range []llmtypes.NativeRecord{first.Records[2], final.Records[1]} {
				if h.HelperType != "Explore" || h.HelperDescription != "Grepping coding.go" || h.Tokens != nil {
					t.Fatalf("wrong helper facts: %+v", h)
				}
			}
			want := "finished"
			if asynchronous {
				want = "started"
			}
			if final.Records[1].State != want {
				t.Fatalf("state=%s", final.Records[1].State)
			}
		})
	}
}

// Native joined terminal sessions use the same task reducer without persisting
// task text or tool arguments in their observer checkpoint.
func TestClaudeNativePlanAcrossIncrementalReads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "native.jsonl")
	first := `{"type":"user","uuid":"u","origin":{"kind":"human"}}
{"type":"assistant","uuid":"a","parentUuid":"u","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"PRIVATE_PLAN"}}]}}
`
	if err := os.WriteFile(p, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	one, err := Read(context.Background(), "claude-code", "native", p, Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	rest := `{"type":"user","uuid":"r","parentUuid":"a","toolUseResult":{"task":{"id":"1","subject":"PRIVATE_PLAN"}},"message":{"content":[{"type":"tool_result","tool_use_id":"c","content":"Task #1 created successfully: PRIVATE_PLAN"}]}}
{"type":"assistant","uuid":"b","parentUuid":"r","message":{"content":[{"type":"tool_use","id":"up","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}
{"type":"user","uuid":"s","parentUuid":"b","message":{"content":[{"type":"tool_result","tool_use_id":"up","content":"Updated task #1 status"}]}}
`
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(rest)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	// The actual app persists this checkpoint before the next read/restart.
	rawCursor, _ := json.Marshal(one.Next)
	var restored Cursor
	if err := json.Unmarshal(rawCursor, &restored); err != nil {
		t.Fatal(err)
	}
	two, err := Read(context.Background(), "claude-code", "native", p, restored)
	if err != nil {
		t.Fatal(err)
	}
	var plans []*llmtypes.CodingAgentPlan
	for _, r := range two.Records {
		if r.Plan != nil {
			if r.TurnID != "u" {
				t.Fatal("wrong launching turn")
			}
			plans = append(plans, r.Plan)
		}
	}
	if len(plans) != 2 || plans[0].Items[0].Status != "pending" || plans[1].Items[0].Status != "completed" {
		t.Fatalf("plans=%+v", plans)
	}
	raw, _ := json.Marshal(one.Next)
	raw2, _ := json.Marshal(two)
	if strings.Contains(string(raw), "PRIVATE_PLAN") || strings.Contains(string(raw2), "PRIVATE_PLAN") {
		t.Fatal("plan text leaked into checkpoint/activity")
	}
	// A second restore must recover the existing task itself, not just its inputs.
	checkpoint, _ := json.Marshal(two.Next)
	if err := json.Unmarshal(checkpoint, &restored); err != nil {
		t.Fatal(err)
	}
	f, err = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","uuid":"c","parentUuid":"s","message":{"content":[{"type":"tool_use","id":"again","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}]}}
{"type":"user","uuid":"t","parentUuid":"c","message":{"content":[{"type":"tool_result","tool_use_id":"again","content":"Updated task #1 status"}]}}
`)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	three, err := Read(context.Background(), "claude-code", "native", p, restored)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, r := range three.Records {
		if r.Plan != nil {
			count++
			if len(r.Plan.Items) != 1 || r.Plan.Items[0].Status != "running" {
				t.Fatal("restored task lost its state")
			}
		}
	}
	if count != 1 {
		t.Fatal("restored plan was not updated")
	}
	// Reading a candidate batch must not mutate the caller's committed cursor.
	if len(one.Next.Plan.Snapshot().Items) != 0 {
		t.Fatal("uncommitted plan mutated committed cursor")
	}
}
