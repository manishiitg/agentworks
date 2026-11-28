package cursorcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #4623: a Cursor goal turn reports Cursor's own goal from the agent's checkpoint; the message after a Stop asks
// the agent to end a goal Cursor still has active, and says nothing when the goal is already over.
func TestCursorSDKGoalStateFromCheckpointAndEndAfterStop(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `
let goal, sent = [];
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
exports.createAgentPlatform=async opts=>{ if(!opts.localStore) throw Error('private store expected');
  return {checkpointStore:{loadLatest:async id=>({goalState:goal})}}; };
exports.Agent={listRuns:async()=>({items:[]}), cancelRun:async()=>{}, create:async()=>({agentId:'a1', async [Symbol.asyncDispose](){},
  send:async(text,opts)=>{
    sent.push(text);
    if(/CreateGoal/.test(text) && !/end it/.test(text)) goal={objective:'ship it',status:'GOAL_STATUS_ACTIVE',activeDurationMs:'9000'};
    return {async *stream(){
      opts.onDelta({update:{type:'text-delta',text:'working'}});
      opts.onDelta({update:{type:'turn-ended'}});
      if(/stopped and done/.test(text)) goal={...goal,status:3};
    }, wait:async()=>({status:'finished',result:'ok'}), cancel:async()=>{}};
  }})};
process.on('exit',()=>process.stderr.write('SENT='+JSON.stringify(sent)+'\n'));`
	files := map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk), filepath.Join(dir, "sqlite.js"): []byte(`exports.SqliteLocalAgentStore={open:async()=>({private:true})};`)}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	end := fmt.Sprintf("%q", cursorGoalEndNote)
	cmd.Stdin = strings.NewReader(`{"type":"start","cwd":` + fmt.Sprintf("%q", dir) + `,"store_root":` + fmt.Sprintf("%q", dir) + `,"catalog_wait_ms":100,"model":{"id":"composer-2.5"}}
{"type":"prompt","text":` + fmt.Sprintf("%q", cursorGoalPrompt("set", "ship it")) + `,"goal_report":true}
{"type":"prompt","text":"what now","goal_end":` + end + `}
{"type":"prompt","text":"stopped and done","goal_end":` + end + `}
{"type":"prompt","text":"plain"}
{"type":"close"}
`)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("bridge failed: %v, %s", err, out)
	}
	if !strings.Contains(out, `"type":"goal","status":"GOAL_STATUS_ACTIVE","text":"ship it","duration_ms":9000,"done":true`) {
		t.Fatalf("the goal turn did not report Cursor's goal: %s", out)
	}
	if strings.Count(out, `"type":"goal"`) != 2 || strings.Count(out, `"type":"goal_ended","action":"asked"`) != 2 {
		t.Fatalf("only the goal turn reports, and only an active goal is ended: %s", out)
	}
	sent := out[strings.Index(out, "SENT="):]
	if !strings.Contains(sent, `"<system_notification source=\"agentworks\">The user stopped the active goal.`) || !strings.Contains(sent, `"plain"`) {
		t.Fatalf("sent: %s", sent)
	}
}

func TestCursorGoalTurnAndUpdate(t *testing.T) {
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "earlier"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "reply"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "/goal ship it"}}},
	}
	if out, goal, end, err := cursorGoalTurn(context.Background(), "chat", history); err != nil || goal != nil || end || len(out) != 3 {
		t.Fatalf("a plain turn changed: %v %v %v", goal, end, err)
	}
	ctx := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: "chat", Action: "set", Objective: "ship it"})
	out, goal, _, err := cursorGoalTurn(ctx, "chat", history)
	if err != nil || goal == nil || len(out) != 3 || !strings.Contains(extractTextFromMessage(out[2]), "Call CreateGoal") || !strings.HasSuffix(extractTextFromMessage(out[2]), "ship it") {
		t.Fatalf("set turn: %v %+v", err, out)
	}
	if _, _, _, err := cursorGoalTurn(llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: "chat", Action: "pause"}), "chat", history); err == nil {
		t.Fatal("pause reached a Cursor turn")
	}
	if _, _, end, _ := cursorGoalTurn(llmtypes.WithNativeGoalTurnPause(context.Background(), llmtypes.NativeGoalTurnPause{SessionID: "chat", Reason: "goal_stopped"}), "chat", history); !end {
		t.Fatal("the message after Stop does not end Cursor's goal")
	}
	for _, tc := range []struct {
		status string
		done   bool
		want   string
		ok     bool
	}{
		{"GOAL_STATUS_ACTIVE", false, "active", true},
		{"1", true, "paused", true}, // the run ended with the goal still active: stopped, Start again picks it up
		{"3", true, "complete", true},
		{"GOAL_STATUS_COMPLETE", true, "complete", true},
		{"", false, "", false},
		{"", true, "none", true}, // no goal created: the bar is cleared
		{"unknown", false, "", false},
		{"unknown", true, "complete", true},
	} {
		update, ok := cursorGoalUpdate("a1", goal, cursorSDKEvent{Status: tc.status, Done: tc.done, DurationMS: 9000})
		got := "none"
		if update.Goal != nil {
			got = update.Goal.Status
		}
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("%q done=%t: %s %t", tc.status, tc.done, got, ok)
		}
		if ok && update.Goal != nil && (update.Goal.Objective != "ship it" || *update.Goal.TimeUsedSeconds != 9 || update.Provider != "cursor-cli") {
			t.Fatalf("update = %+v", update)
		}
	}
	if _, ok := cursorGoalUpdate("a1", nil, cursorSDKEvent{Status: "1", Done: true}); ok {
		t.Fatal("a report outside a goal turn changed the chat's goal")
	}
}
