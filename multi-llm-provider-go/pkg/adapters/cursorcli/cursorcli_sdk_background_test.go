package cursorcli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3131: the bridge follows a Cursor background shell's terminal file for the
// shell call of this chat that started it, reports its output without the
// header or exit footer, ends it when Cursor records the exit, and stops only
// a shell it follows.
func TestCursorBridgeFollowsBackgroundShellTerminal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge := filepath.Join(dir, "bridge.cjs")
	if err := os.WriteFile(bridge, cursorSDKBridge, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `
const fs=require('fs'),path=require('path');
const {createTerminalTail,cursorTerminalDirs}=require(process.argv[2]);
const data=process.argv[3], cwd='/work/my app';
const [terminals]=cursorTerminalDirs(cwd,{CURSOR_DATA_DIR:data},'/nohome');
if(terminals!==path.join(data,'projects','work-my-app','terminals')) throw new Error('dir '+terminals);
fs.mkdirSync(terminals,{recursive:true});
const header=(status,pid)=>'---\npid: '+pid+'\ncwd: "/work/my app"\ncommand: "npm run dev"\nstatus: '+status.padEnd(9,' ')+'\nstarted_at: 2026-01-01T00:00:00Z\nrunning_for_ms: 5   \n---\n';
fs.writeFileSync(path.join(terminals,'1.txt'),header('succeeded',11)+'old run\n');
const out=[],killed=[];
const tail=createTerminalTail(e=>out.push(e),{dirs:[terminals],pollMs:1e9,kill:(pid,sig)=>{killed.push([pid,sig]);if(pid<0)throw new Error('not leader');}});
tail.call('call-other','ls');
tail.call('call-dev','npm run dev');
const file=path.join(terminals,'2.txt');
fs.writeFileSync(file,header('running',4242)+'ready on 3000\n');
tail.poll();
fs.appendFileSync(file,'GET / 200\n');
tail.poll();
const stopped=tail.stop('2'), unknown=tail.stop('1');
fs.appendFileSync(file,'bye\n\n---\nexit_code: 0\nelapsed_ms: 9\nended_at: 2026-01-01T00:00:09Z\n---\n');
const body=fs.readFileSync(file,'utf8').replace('status: running  ','status: succeeded');
fs.writeFileSync(file,body);
tail.poll();
console.log(JSON.stringify({out,killed,stopped,unknown,running:tail.running()}));
`
	scriptPath := filepath.Join(dir, "check.cjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := exec.Command("node", scriptPath, bridge, filepath.Join(dir, "data")).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, raw)
	}
	var got struct {
		Out []struct {
			Type    string `json:"type"`
			CallID  string `json:"call_id"`
			ShellID string `json:"shell_id"`
			Label   string `json:"label"`
			Text    string `json:"text"`
			Done    bool   `json:"done"`
		} `json:"out"`
		Killed  [][]any  `json:"killed"`
		Stopped bool     `json:"stopped"`
		Unknown bool     `json:"unknown"`
		Running []string `json:"running"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	var text strings.Builder
	for _, e := range got.Out {
		if e.Type != "shell_output" || e.CallID != "call-dev" || e.ShellID != "2" || e.Label != "npm run dev" {
			t.Fatalf("unexpected event %+v (all %s)", e, raw)
		}
		text.WriteString(e.Text)
	}
	if text.String() != "ready on 3000\nGET / 200\nbye\n" {
		t.Fatalf("output = %q", text.String())
	}
	if len(got.Out) < 2 || !got.Out[len(got.Out)-1].Done || got.Out[0].Done {
		t.Fatalf("want a first start chunk and a final done chunk: %s", raw)
	}
	if !got.Stopped || got.Unknown || len(got.Killed) != 2 || got.Killed[0][0].(float64) != -4242 || got.Killed[1][0].(float64) != 4242 {
		t.Fatalf("stop: stopped=%t unknown=%t killed=%v", got.Stopped, got.Unknown, got.Killed)
	}
	if len(got.Running) != 0 {
		t.Fatalf("still running: %v", got.Running)
	}
}

// #3131: Go keeps a chat's running Cursor shells for the host (list, Stop) and
// hands their output on as stoppable background output until it ends.
func TestCursorBackgroundShellListStopAndOutput(t *testing.T) {
	var mu sync.Mutex
	var got []llmtypes.BackgroundOutput
	llmtypes.SetBackgroundOutputListener(func(o llmtypes.BackgroundOutput) { mu.Lock(); got = append(got, o); mu.Unlock() })
	defer llmtypes.SetBackgroundOutputListener(nil)

	reader, writer := io.Pipe()
	p := &cursorSDKProcess{stdin: writer, acks: map[string]chan bool{}, done: make(chan struct{})}
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			var request struct {
				Type    string `json:"type"`
				ID      string `json:"id"`
				ShellID string `json:"shell_id"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &request)
			p.mu.Lock()
			ack := p.acks[request.ID]
			p.mu.Unlock()
			if ack != nil {
				ack <- request.Type == "stop_shell" && request.ShellID == "7"
			}
		}
	}()
	owner := "chat-cursor-bg"
	recordCursorShellOutput(owner, p, cursorSDKEvent{Type: "shell_output", CallID: "call-1", ShellID: "7", Label: "npm run dev"})
	recordCursorShellOutput(owner, p, cursorSDKEvent{Type: "shell_output", CallID: "call-1", ShellID: "7", Label: "npm run dev", Text: "ready\n"})
	list := CursorBackgroundCommands(owner)
	if len(list) != 1 || list[0].ID != "7" || list[0].Label != "npm run dev" {
		t.Fatalf("list = %+v", list)
	}
	if !StopCursorBackgroundCommand(owner, "7") {
		t.Fatal("stop of a followed shell failed")
	}
	if StopCursorBackgroundCommand(owner, "8") || StopCursorBackgroundCommand("other-chat", "7") {
		t.Fatal("stop reached a shell this chat does not have")
	}
	endCursorShells(owner, p)
	if len(CursorBackgroundCommands(owner)) != 0 {
		t.Fatal("a closed bridge's shells are still listed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || got[1].Text != "ready\n" || !got[1].CanStop || got[1].TaskID != "7" || got[1].Provider != "cursor-cli" || got[1].ToolCallID != "call-1" || !got[2].Done || got[2].CanStop {
		t.Fatalf("output = %+v", got)
	}
}
