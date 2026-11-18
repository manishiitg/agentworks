package cursorcli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-999: late SDK callbacks after the foreground result must remain
// observable, but diagnostics must never copy commands, messages or results.
func TestCursorSDKObservesLateNativeMetadataWithoutPayload(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
exports.Agent={create:async()=>({agentId:'agent-parent',send:async(text,opts)=>{
 let listener;
 const run={id:'run-parent',status:'running',onDidChangeStatus(fn){listener=fn;return()=>{listener=undefined;};},
 async *stream(){
  opts.onDelta({update:{type:'turn-ended'}});
  yield {type:'tool_call',agent_id:'agent-child',run_id:'run-child',call_id:'call-stream',name:'shell',status:'completed',args:{command:'PRIVATE_COMMAND'},result:'PRIVATE_OUTPUT'};
  yield {type:'task',run_id:'run-parent',status:'PRIVATE_STATUS',text:'PRIVATE_TASK'};
 },
 async wait(){setTimeout(()=>{
  opts.onDelta({update:{type:'tool-call-completed',callId:'call-late',toolCall:{type:'shell',result:{status:'success',value:{stdout:'PRIVATE_STDOUT'}}}}});
  run.status='finished';listener?.('finished');
  opts.onDelta({update:{type:'text-delta',text:'PRIVATE_TEXT'}});
 },30);return {status:'finished',result:''};},cancel:async()=>{}};
 return run;
}})};
`
	for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
		if e := os.WriteFile(name, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	_, _ = io.WriteString(stdin, `{"type":"start","model":{"id":"composer","params":[{"id":"fast","value":"false"}]}}`+"\n"+`{"type":"prompt","text":"hello"}`+"\n")
	var metadata []map[string]string
	lines := bufio.NewScanner(stdout)
	sawResult, sawLate, sawStatus, sawChild := false, false, false, false
	for lines.Scan() {
		raw, ok := strings.CutPrefix(lines.Text(), cursorSDKMarker)
		if !ok {
			continue
		}
		var kind struct {
			Type string `json:"type"`
		}
		if e := json.Unmarshal([]byte(raw), &kind); e != nil {
			t.Fatal(e)
		}
		if kind.Type == "result" {
			sawResult = true
			continue
		}
		if kind.Type != "background_meta" {
			continue
		}
		var m map[string]string
		if e := json.Unmarshal([]byte(raw), &m); e != nil {
			t.Fatal(e)
		}
		metadata = append(metadata, m)
		if strings.Contains(raw, "PRIVATE_") {
			t.Fatal("diagnostics exposed native payload", raw)
		}
		if m["run_id"] == "run-child" && m["parent_run_id"] == "run-parent" {
			sawChild = true
		}
		if m["call_id"] == "call-late" && m["phase"] == "after_result" {
			sawLate = sawResult
		}
		if m["kind"] == "status" && m["event_type"] == "changed" && m["status"] == "finished" && m["phase"] == "after_result" {
			sawStatus = true
		}
		if sawLate && sawStatus && sawChild {
			break
		}
	}
	if !sawResult || !sawLate || !sawStatus || !sawChild {
		t.Fatalf("late native evidence missing: result=%t late=%t status=%t child=%t metadata=%v", sawResult, sawLate, sawStatus, sawChild, metadata)
	}
}

// Diagnostics must not fill a retained process's foreground queue while no
// prompt is consuming it, otherwise the evidence itself can stall the bridge.
func TestCursorSDKMetadataBypassesIdleForegroundQueue(t *testing.T) {
	const count = 5000
	input := strings.Repeat(cursorSDKMarker+`{"type":"background_meta","event_type":"changed","status":"finished"}`+"\n", count) + cursorSDKMarker + `{"type":"ready"}` + "\n"
	observed := 0
	p := &cursorSDKProcess{events: make(chan cursorSDKEvent, 1), done: make(chan struct{}), observe: func(e cursorSDKEvent) {
		if e.Type == "background_meta" {
			observed++
		}
	}}
	done := make(chan struct{})
	go func() { p.read(strings.NewReader(input)); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(p.done)
		t.Fatal("metadata blocked the idle foreground reader")
	}
	if observed != count || (<-p.events).Type != "ready" {
		t.Fatal("metadata dropped or entered foreground queue", observed)
	}
}
