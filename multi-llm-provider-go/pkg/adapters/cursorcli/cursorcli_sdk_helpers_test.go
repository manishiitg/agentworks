package cursorcli

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Official TaskSuccess.isBackground=true acknowledges a launch, not completion.
// This also exercises late completion after the foreground queue has ended.
func TestCursorSDKHelperRowsUseActualTaskCompletion(t *testing.T) {
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
 exports.Agent={create:async()=>({agentId:'native-parent',send:async(text,opts)=>{
 opts.onDelta({update:{type:'tool-call-started',callId:'task-one',toolCall:{type:'task',args:{prompt:'PRIVATE_PROMPT'}}}});
 return {id:'launch-run',status:'running',onDidChangeStatus(){return()=>{}},async *stream(){
  opts.onDelta({update:{type:'tool-call-completed',callId:'task-one',toolCall:{type:'task',result:{status:'success',value:{isBackground:true,resultSuffix:'PRIVATE_OUTPUT'}}}}});
 },async wait(){setTimeout(()=>opts.onDelta({update:{type:'tool-call-completed',callId:'task-one',toolCall:{type:'task',result:{status:'success',value:{isBackground:false,durationMs:25,resultSuffix:'PRIVATE_OUTPUT'}}}}}),25);return {status:'finished',result:''}},cancel:async()=>{}};
 }})};`
	for path, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	var rows []llmtypes.NativeRecord
	SetSDKHelperListener(func(owner string, r llmtypes.NativeRecord) {
		if owner != "owner-chat" {
			t.Errorf("wrong owner %s", owner)
		}
		rows = append(rows, r)
	})
	defer SetSDKHelperListener(nil)
	adapter := &CursorCLIAdapter{}
	observe := adapter.sdkHelperObserver("owner-chat")
	io.WriteString(stdin, `{"type":"start","model":{"id":"composer","params":[{"id":"fast","value":"false"}]}}`+"\n"+`{"type":"prompt","text":"hello"}`+"\n")
	sawResult := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		raw, ok := strings.CutPrefix(scanner.Text(), cursorSDKMarker)
		if !ok {
			continue
		}
		var event cursorSDKEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "result" {
			sawResult = true
		}
		if event.Type != "helper" {
			continue
		}
		if strings.Contains(raw, "PRIVATE_") {
			t.Fatal("helper record copied private payload")
		}
		if event.Status == "launch_ack" && len(rows) != 1 {
			t.Fatal("launch acknowledgement completed helper")
		}
		observe(event)
		if event.Status == "finished" {
			if !sawResult {
				t.Fatal("fixture did not exercise late completion")
			}
			break
		}
	}
	if len(rows) != 2 || rows[0].State != "started" || rows[1].State != "finished" {
		t.Fatalf("rows=%+v", rows)
	}
	for _, r := range rows {
		if r.TurnID != "launch-run" || r.SessionID != "native-parent" || r.TaskID != "task-one" || r.Tokens != nil {
			t.Fatalf("identity or unreported usage changed: %+v", r)
		}
	}
}

func TestCursorSDKHelperRuntimeLossClosesOnlyKnownWork(t *testing.T) {
	var rows []llmtypes.NativeRecord
	SetSDKHelperListener(func(_ string, r llmtypes.NativeRecord) { rows = append(rows, r) })
	defer SetSDKHelperListener(nil)
	observe := (&CursorCLIAdapter{}).sdkHelperObserver("chat")
	observe(cursorSDKEvent{Type: "helper", AgentID: "native", RunID: "run-a", CallID: "one", Status: "started"})
	observe(cursorSDKEvent{Type: "helper", AgentID: "native", RunID: "run-b", CallID: "one", Status: "finished"})
	observe(cursorSDKEvent{Type: "helper", AgentID: "native", RunID: "run-b", CallID: "two", Status: "started"})
	observe(cursorSDKEvent{Type: "helper_runtime_closed"})
	observe(cursorSDKEvent{Type: "helper", AgentID: "native", CallID: "unknown", Status: "finished"})
	if len(rows) != 4 || rows[1].TurnID != "run-a" || rows[3].State != "interrupted" || rows[3].TaskID != "two" {
		t.Fatalf("wrong task settled: %+v", rows)
	}
}
