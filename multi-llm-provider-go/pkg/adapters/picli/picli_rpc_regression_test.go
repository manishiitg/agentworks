package picli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-139/153: a dead CLI with inherited pipes, a silent model, and a busy
// stream must all terminate. The fixture speaks the actual RPC boundary.
func TestPiRPCProcessFailureDoesNotHang(t *testing.T) {
	for _, mode := range []string{"child_stdout", "child_stderr", "silent", "busy", "long_tool"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "pi")
			source := fmt.Sprintf(`#!/usr/bin/env python3
import json,sys,subprocess,time,threading
native=sys.argv[sys.argv.index('--session-id')+1]
def send(v): print(json.dumps(v),flush=True)
def run():
 mode=%q
 if mode.startswith('child'):
  subprocess.Popen(['sleep','30'])
  sys.exit(0)
 if mode=='long_tool':
  send({'type':'tool_execution_start','toolCallId':'one','toolName':'slow','args':{}})
  time.sleep(.4)
  send({'type':'tool_execution_end','toolCallId':'one','toolName':'slow','result':{'content':[{'type':'text','text':'ok'}]}})
  send({'type':'message_end','message':{'role':'assistant','content':[{'type':'text','text':'done'}]}})
  send({'type':'agent_settled'})
  return
 if mode=='busy':
  while True:
   send({'type':'message_update','assistantMessageEvent':{'type':'text_delta','delta':'x'}})
   time.sleep(.02)
 else: time.sleep(30)
for line in sys.stdin:
 req=json.loads(line)
 data={'sessionId':native,'sessionFile':''} if req['type']=='get_state' else {'disposition':'started'}
 send({'type':'response','id':req['id'],'command':req['type'],'success':True,'data':data})
 if req['type']=='prompt': run()
`, mode)
			if err := os.WriteFile(script, []byte(source), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(EnvPiBinary, script)
			t.Setenv("PI_STRUCTURED_MAX_TURN_DURATION", "600ms")
			if mode == "long_tool" {
				t.Setenv("PI_STRUCTURED_MAX_TURN_DURATION", "2s")
			}
			t.Setenv("PI_STRUCTURED_WEDGE_SILENCE", "200ms")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			_, err := NewPiCLIAdapter("", "google/test", &mockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hello")}, WithWorkingDir(dir))
			if mode == "long_tool" {
				if err != nil {
					t.Fatalf("healthy slow tool killed: %v", err)
				}
			} else if err == nil {
				t.Fatal("failed runtime reported success")
			}
			if time.Since(started) > 4*time.Second {
				t.Fatal("process shutdown waited for inherited pipes")
			}
		})
	}
}

// PLAT-872-20: agent_end is not final; tools, queued turns and retries can
// follow it. Only agent_settled closes the turn, and only current events count.
func TestPiRPCSettledControlsCompletionAndPreservesToolReceipts(t *testing.T) {
	r := &piRPC{events: make(chan piRPCRecord, 16), done: make(chan struct{})}
	s := &piRPCSession{nativeID: "test", model: "google/test"}
	lines := []string{
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Working"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Working"}],"usage":{"input":3,"output":2}}}`,
		`{"type":"tool_execution_start","toolCallId":"one","toolName":"read","args":{"path":"a"}}`,
		`{"type":"tool_execution_end","toolCallId":"one","toolName":"read","result":{"content":[{"type":"text","text":"value"}]},"isError":false}`,
		`{"type":"agent_end"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Final\n\n- item"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Final\n\n- item"}],"usage":{"input":4,"output":3,"cacheRead":5}}}`,
		`{"type":"agent_settled"}`,
	}
	for _, line := range lines {
		var record piRPCRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		record.piJSONEvent.Type = record.Type
		record.Raw = []byte(line)
		r.events <- record
	}
	var chunks []llmtypes.StreamChunk
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := (&PiCLIAdapter{}).consumeRPCTurn(ctx, s, r, func(c llmtypes.StreamChunk) { chunks = append(chunks, c) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Content != "Final\n\n- item" {
		t.Fatal("intermediate text became final")
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 5 || *resp.Usage.CacheTokens != 5 {
		t.Fatalf("wrong turn usage: %#v", resp.Usage)
	}
	ends := 0
	for _, ch := range chunks {
		if ch.Type == llmtypes.StreamChunkTypeToolCallEnd {
			ends++
			if ch.ToolCallID != "one" || !strings.Contains(ch.ToolResult, "value") {
				t.Fatalf("lost tool receipt: %#v", ch)
			}
		}
	}
	if ends != 1 {
		t.Fatal("missing tool result")
	}
}

// A repeated steering message must consume a distinct native user record.
func TestPiRPCDurabilityRequiresDistinctUserRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	row := []byte(`{"type":"message","message":{"role":"user","timestamp":123,"content":"repeat"}}` + "\n")
	if err := os.WriteFile(path, row, 0600); err != nil {
		t.Fatal(err)
	}
	if !piRPCUserPersisted(path, "repeat", 123, 1) || piRPCUserPersisted(path, "repeat", 123, 2) || piRPCUserPersisted(path, "repeat", 124, 1) {
		t.Fatal("stale/duplicate receipt accepted")
	}
	if err := os.WriteFile(path, append(row, row...), 0600); err != nil {
		t.Fatal(err)
	}
	if !piRPCUserPersisted(path, "repeat", 123, 2) {
		t.Fatal("second persisted occurrence rejected")
	}
}

// PLAT-872-20: only the selected account's login may enter the runtime;
// an explicit key must win over a saved login (the live invalid-key regression).
func TestPiRPCSelectedAccountAndExplicitKey(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "pi")
	source := `#!/usr/bin/env python3
import json,sys
native=sys.argv[sys.argv.index('--session-id')+1]
for line in sys.stdin:
 r=json.loads(line)
 print(json.dumps({'type':'response','id':r['id'],'success':True,'data':{'sessionId':native}}),flush=True)
`
	if err := os.WriteFile(binary, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPiBinary, binary)
	selected := filepath.Join(dir, "selected")
	ambient := filepath.Join(dir, "ambient")
	for _, home := range []string{selected, ambient} {
		if err := os.MkdirAll(filepath.Join(home, ".pi", "agent"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".pi", "agent", "auth.json"), []byte(`{"google":{"type":"api_key","key":"test-only-login"}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", ambient)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(ambient, ".pi", "agent"))
	for _, key := range []string{"", "test-only-explicit"} {
		opts := &llmtypes.CallOptions{}
		llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": selected, "PI_CODING_AGENT_DIR": filepath.Join(selected, ".pi", "agent")})(opts)
		native := generatePiNativeSessionID()
		a := &PiCLIAdapter{modelID: "google/test", apiKey: key}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		r, _, err := a.launchRPC(ctx, opts, dir, native, "")
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		agentDir, _ := piSessionRuntimeDirs(dir, native)
		auth := filepath.Join(agentDir, "auth.json")
		if key == "" {
			target, err := filepath.EvalSymlinks(auth)
			if err != nil || cleanPiWorkingDirKey(target) != cleanPiWorkingDirKey(filepath.Join(selected, ".pi", "agent", "auth.json")) {
				r.close()
				t.Fatal("runtime did not use selected login")
			}
			reads := piLandlockReads(r.cmd.Args, r.cmd.Env...)
			found := false
			for _, path := range reads {
				if path == target {
					found = true
				}
			}
			if !found {
				r.close()
				t.Fatal("selected login missing sandbox read grant")
			}
		} else {
			if _, err := os.Lstat(auth); !os.IsNotExist(err) {
				r.close()
				t.Fatal("saved login overrides explicit key")
			}
		}
		r.close()
	}
}

// PLAT-872-20: accepted input remains unflushed when its proof budget expires;
// caller cancellation is still an error. Cover both event and disk-write waits.
func TestPiRPCDurabilityBudgetDoesNotFailAcceptedInput(t *testing.T) {
	for _, observed := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			receipt := &piRPCReceipt{text: "queued", observed: make(chan struct{})}
			if observed {
				close(receipt.observed)
			}
			owner := t.Name()
			piRPCSessions.Lock()
			piRPCSessions.sessions[owner] = &piRPCSession{receipts: []*piRPCReceipt{receipt}}
			piRPCSessions.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			if cancelled {
				cancel()
			}
			ack, err := awaitPiRPCInputDurable(ctx, owner, "queued", 10*time.Millisecond)
			cancel()
			piRPCSessions.Lock()
			delete(piRPCSessions.sessions, owner)
			piRPCSessions.Unlock()
			if ack.Outcome != PiDurableAckUnflushed || (err != nil) != cancelled {
				t.Fatalf("observed=%v cancelled=%v: ack=%+v err=%v", observed, cancelled, ack, err)
			}
		}
	}
}
