package cursorcli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The only stand-in is the @cursor/sdk module: the real bridge and adapter run in node. It models a
// local run that waits at a tool step until steered, as the live SDK does (PLAT-872, owner chose the
// SDK because ACP held a mid-reply message until the reply ended).
const fakeCursorSDK = `
exports.Cursor = { auth: { status: async () => ({status:'logged-in'}) }, models: {list: async () => [{id:'composer-2.5',variants:[{isDefault:true,params:[{id:'fast',value:'false'}]}]}]} };
let steered = '', cancelled = false, settleAfterWait;
function agent(id) {
  return {
    agentId: id,
    async send(text, opts) {
      check(opts);
      let done, resolveDone = new Promise(r => { done = r; });
      const run = {
        async steer(t) {
          if (t === 'LATER') { done(); return 'revert_to_followup'; }
          // The real SDK settles a steer it had queued only as its run ends, after run.wait() returned.
          if (t === 'QUEUED') { done(); return new Promise(r => { settleAfterWait = () => setTimeout(() => r('revert_to_followup'), 50); }); }
          steered = t; done(); return 'complete_delivered';
        },
        async *stream() {
          yield { type: 'tool_call', call_id: 'c1', name: 'shell', status: 'running', args: { command: 'sleep' } };
          await Promise.race([resolveDone, new Promise(r => setTimeout(r, 5000))]);
          yield { type: 'tool_call', call_id: 'c1', name: 'shell', status: 'completed', result: 'SLEPT' };
          opts.onDelta({ update: { type: 'text-delta', text: 'answer to ' + text.slice(-5) + (steered ? ' +' + steered : '') } });
        },
        async wait() { settleAfterWait?.(); settleAfterWait = undefined; return { status: cancelled ? 'cancelled' : 'finished', result: '' }; },
        async cancel() { cancelled = true; done(); },
      };
      return run;
    },
  };
}
// PLAT-872-64: the real bridge must apply the account's default settings before launch/resume.
function check(opts) { if (JSON.stringify(opts.model.params) !== '[{"id":"fast","value":"false"}]') throw Error('Model unavailable on Plus. Switch to an included model to continue.'); }
exports.Agent = { create: async opts => {check(opts); return agent('agent-test');}, resume: async (id,opts) => {check(opts); return agent(id);} };
`

// PLAT-872-64: discovery failure cannot block a chat; explicit choices win,
// and a second start in the same account process reuses the discovered defaults.
func TestCursorSDKModelDiscoveryDoesNotBlockOrOverrideSelection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, tc := range []struct{ name, lookup, model, expected string }{
		{"cached default", `if (++calls > 1) throw Error('second lookup'); return [{id:'composer',variants:[{isDefault:true,params:[{id:'fast',value:'false'}]}]}]`, `{"id":"composer"}`, `[{"id":"fast","value":"false"}]`},
		{"explicit selection", `throw Error('must not discover')`, `{"id":"composer","params":[{"id":"fast","value":"true"}]}`, `[{"id":"fast","value":"true"}]`},
		{"unavailable catalog", `throw Error('network unavailable')`, `{"id":"composer"}`, `undefined`},
		// PLAT-872-93: a lookup that fails once is asked again within the start's wait.
		{"catalog fails once", `if (++calls === 1) throw Error('network blip'); return [{id:'composer',variants:[{isDefault:true,params:[{id:'fast',value:'false'}]}]}]`, `{"id":"composer"}`, `[{"id":"fast","value":"false"}]`},
		{"stalled catalog", `return new Promise(() => {})`, `{"id":"composer"}`, `undefined`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
			// The SDK looks for its bundled ripgrep above argv[1]; from the temp bridge folder every
			// grep/glob failed "Ripgrep path not configured" on server B (2026-10-28).
			sdk := `if(require('fs').realpathSync(process.argv[1])!==__filename) throw Error('Ripgrep path not configured'); let calls=0; exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>{` + tc.lookup + `}}}; exports.Agent={create:async opts=>{if(JSON.stringify(opts.model.params)!==JSON.stringify(` + tc.expected + `))throw Error('wrong model settings');return {agentId:'ready'};}};`
			for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", bridge, entry)
			start := `{"type":"start","catalog_wait_ms":1500,"model":` + tc.model + `}` + "\n"
			cmd.Stdin = strings.NewReader(start + start + "{\"type\":\"close\"}\n")
			out, err := cmd.CombinedOutput()
			if err != nil || strings.Count(string(out), `"type":"ready"`) != 2 {
				t.Fatalf("starts failed: %v, %s", err, out)
			}
		})
	}
}

// PLAT-872-64: an owner got a reply, then a plan refusal on the next message
// using the same native agent. Keep the selected params explicit on both sends,
// even when the SDK's retained default differs after a run.
func TestCursorSDKResumedChatKeepsSelectedModelAcrossTwoTurns(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[{id:'composer-2.5',variants:[{isDefault:true,params:[{id:'fast',value:'false'}]}]}]}};
exports.Agent={
  create:async()=>{throw Error('must retain the native conversation');},
  resume:async(id,options)=>{
    let retained=options.model, turns=0;
    return {agentId:id, send:async(text,sendOptions)=>{
      const model=sendOptions.model??retained;
      if(model.id!=='composer-2.5'||JSON.stringify(model.params)!=='[{"id":"fast","value":"false"}]') throw Error('Model unavailable on Plus. Switch to an included model to continue.');
      return {async *stream(){},wait:async()=>{
        retained={id:'composer-2.5'};
        return {status:'finished',result:'reply '+(++turns)};
      }};
    }};
  }
};`
	for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	cmd.Stdin = strings.NewReader(`{"type":"start","agent_id":"agent-existing","model":{"id":"composer-2.5"}}
{"type":"prompt","text":"hi"}
{"type":"prompt","text":"ok"}
{"type":"close"}
`)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"agent_id":"agent-existing","resumed":true`) || strings.Count(string(out), `"status":"finished"`) != 2 || !strings.Contains(string(out), `"result":"reply 2"`) {
		t.Fatalf("resumed two-turn conversation failed: %v, %s", err, out)
	}
}

// PLAT-872: a server restart killed the bridge mid-run (Excellence 2026-10-27 17:11), the run stayed
// active in Cursor's local store, and every later turn of that chat failed "Agent ... already has active
// run". A resumed agent's stale run is cancelled; if it cannot be, the chat starts a new native agent.
func TestCursorSDKResumeClearsStaleActiveRun(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, tc := range []struct{ name, cancel, want string }{
		{"cancelled", `active=false`, `"agent_id":"agent-existing","resumed":true,"stale":"cancelled"`},
		{"new agent", `throw Error('store locked')`, `"agent_id":"agent-new","resumed":false,"stale":"new_agent","stale_agent":"agent-existing"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
			sdk := `
let active=true;
const agent=id=>({agentId:id, send:async()=>{
  if(id==='agent-existing'&&active) throw Error('Agent '+id+' already has active run');
  return {async *stream(){}, wait:async()=>({status:'finished',result:'reply'})};
}});
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
exports.Agent={
  resume:async id=>agent(id), create:async()=>agent('agent-new'),
  listRuns:async id=>({items:id==='agent-existing'&&active?[{id:'run-stale',status:'running'}]:[{id:'run-old',status:'finished'}]}),
  cancelRun:async runId=>{if(runId!=='run-stale') throw Error('wrong run'); ` + tc.cancel + `},
};`
			for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", bridge, entry)
			cmd.Stdin = strings.NewReader(`{"type":"start","agent_id":"agent-existing","catalog_wait_ms":500,"model":{"id":"composer-2.5"}}
{"type":"prompt","text":"hi"}
{"type":"close"}
`)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), `"status":"finished"`) {
				t.Fatalf("stale active run was not cleared: %v, %s", err, out)
			}
		})
	}
}

func TestCursorSDKSteersTheRunningTurn(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	if err := os.WriteFile(entry, []byte(fakeCursorSDK), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	c := NewCursorCLIAdapter("", "composer-2.5", nil)
	stream := make(chan llmtypes.StreamChunk, 64)
	owner := "chat-steer-test"
	type result struct {
		resp *llmtypes.ContentResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := c.GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello world")},
			WithWorkingDir(dir), WithInteractiveSessionID(owner), llmtypes.WithStreamingChan(stream))
		done <- result{resp, err}
	}()
	var chunks []llmtypes.StreamChunk
	steered := false
	for chunk := range stream {
		chunks = append(chunks, chunk)
		if chunk.Type == llmtypes.StreamChunkTypeToolCallStart && !steered {
			steered = true
			if err := SendCursorSDKInput(context.Background(), owner, "STEER"); err != nil {
				t.Fatalf("steer refused while the turn runs: %v", err)
			}
		}
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if text := r.resp.Choices[0].Content; !strings.Contains(text, "+STEER") {
		t.Fatalf("steer did not reach the running turn: %q", text)
	}
	var ended bool
	for _, chunk := range chunks {
		ended = ended || (chunk.Type == llmtypes.StreamChunkTypeToolCallEnd && chunk.ToolResult == "SLEPT")
	}
	if !ended {
		t.Fatal("tool result was not streamed")
	}
	if err := SendCursorSDKInput(context.Background(), owner, "late"); err == nil {
		t.Fatal("a steer with no running turn must report not sent")
	}
}

// The Stop button (Escape) must reach the SDK bridge, not only ACP sessions.
func TestCursorSDKStopCancelsTheRunningTurn(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	if err := os.WriteFile(entry, []byte(fakeCursorSDK), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	c := NewCursorCLIAdapter("", "composer-2.5", nil)
	stream := make(chan llmtypes.StreamChunk, 64)
	owner := "chat-stop-test"
	done := make(chan error, 1)
	go func() {
		_, err := c.GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello world")},
			WithWorkingDir(dir), WithInteractiveSessionID(owner), llmtypes.WithStreamingChan(stream))
		done <- err
	}()
	for chunk := range stream {
		if chunk.Type == llmtypes.StreamChunkTypeToolCallStart {
			if err := SendCursorInteractiveControlKey(context.Background(), owner, "Escape"); err != nil {
				t.Fatalf("stop did not reach the SDK turn: %v", err)
			}
		}
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("turn was not cancelled: %v", err)
	}
}

// RTS 2026-10-27: a message sent 2s into a turn, before the SDK run had started, fell back to an
// after-turn injection and showed twice. It must join this turn, and text after a tool step must not
// be glued to the previous sentence ("ready.Yes.").
func TestCursorSDKEarlySteerJoinsTheSameTurn(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	if err := os.WriteFile(entry, []byte(fakeCursorSDK), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	c := NewCursorCLIAdapter("", "composer-2.5", nil)
	stream := make(chan llmtypes.StreamChunk, 256)
	owner := "chat-early-steer"
	done := make(chan *llmtypes.ContentResponse, 1)
	go func() {
		resp, err := c.GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello world")},
			WithWorkingDir(dir), WithInteractiveSessionID(owner), llmtypes.WithStreamingChan(stream))
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	sent := false
	for start := time.Now(); !sent && time.Since(start) < 3*time.Second; time.Sleep(20 * time.Millisecond) {
		sent = SendCursorSDKInput(context.Background(), owner, "STEER") == nil
	}
	if !sent {
		t.Fatal("an early steer was refused while the turn was starting")
	}
	for range stream {
	}
	resp := <-done
	if resp == nil {
		return
	}
	if text := resp.Choices[0].Content; !strings.Contains(text, "STEER") {
		t.Fatalf("early steer was not answered in the same turn: %q", text)
	}
}

// A steer the SDK does not take becomes a follow-up run in the same turn; its text starts a new paragraph.
// QUEUED: on Excellence (2026-10-28) a second chat message was accepted mid-turn, the SDK settled it
// only after the run ended, and the bridge had already closed the turn, so it was never answered.
func TestCursorSDKFollowUpRunStartsNewParagraph(t *testing.T) {
	for _, steer := range []string{"LATER", "QUEUED"} {
		t.Run(steer, func(t *testing.T) { testCursorSDKFollowUpRun(t, steer) })
	}
}

func testCursorSDKFollowUpRun(t *testing.T, steer string) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	if err := os.WriteFile(entry, []byte(fakeCursorSDK), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	c := NewCursorCLIAdapter("", "composer-2.5", nil)
	stream := make(chan llmtypes.StreamChunk, 256)
	owner := "chat-followup-paragraph-" + steer
	done := make(chan *llmtypes.ContentResponse, 1)
	go func() {
		resp, _ := c.GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello world")},
			WithWorkingDir(dir), WithInteractiveSessionID(owner), llmtypes.WithStreamingChan(stream))
		done <- resp
	}()
	steered := false
	for chunk := range stream {
		if chunk.Type == llmtypes.StreamChunkTypeToolCallStart && !steered {
			steered = true
			if err := SendCursorSDKInput(context.Background(), owner, steer); err != nil {
				t.Fatal(err)
			}
		}
	}
	resp := <-done
	if resp == nil || !strings.Contains(resp.Choices[0].Content, "answer to world\n\nanswer to "+steer[len(steer)-5:]) {
		t.Fatalf("follow-up text was glued to the first answer: %#v", resp)
	}
}

// Reviewer 2026-10-27: a steer sent while the bridge launches must never be acknowledged and then lost
// when the launch fails; it is reported not sent so the host keeps it for the next turn.
func TestCursorSDKSteerDuringFailedLaunchIsNotSent(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	broken := "exports.Cursor = { auth: { status: async () => ({status: 'logged-in'}) }, models: {list: async () => []} }; exports.Agent = { create: async () => { await new Promise(r => setTimeout(r, 300)); throw new Error('login required'); } };"
	if err := os.WriteFile(entry, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	c := NewCursorCLIAdapter("", "composer-2.5", nil)
	owner := "chat-failed-launch"
	done := make(chan error, 1)
	go func() {
		_, err := c.GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello")},
			WithWorkingDir(dir), WithInteractiveSessionID(owner))
		done <- err
	}()
	var steerErr error
	for start := time.Now(); time.Since(start) < 3*time.Second; time.Sleep(10 * time.Millisecond) {
		cursorSDKSessions.Lock()
		s := cursorSDKSessions.sessions[owner]
		cursorSDKSessions.Unlock()
		if s != nil {
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()
			if running {
				steerErr = SendCursorSDKInput(context.Background(), owner, "keep me")
				break
			}
		}
	}
	if err := <-done; err == nil {
		t.Fatal("a failed launch must fail the turn")
	}
	if !errors.Is(steerErr, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("steer during a failed launch = %v, want not sent", steerErr)
	}
}

// PLAT-872-93: a later chat of the same Cursor account starts with the settings an earlier start found, without
// the lookup; an explicit choice, another account or a day-old entry does not use them.
func TestCursorSDKRememberedParamsSkipTheLookup(t *testing.T) {
	now := time.Now()
	rememberParams("acct-a", "composer-2.5", "fast=false", now)
	for _, tc := range []struct {
		name, account, model string
		at                   time.Time
		want                 bool
	}{
		{"same account", "acct-a", "composer-2.5", now, true},
		{"explicit choice", "acct-a", "composer-2.5[fast=true]", now, false},
		{"other account", "acct-b", "composer-2.5", now, false},
		{"after a day", "acct-a", "composer-2.5", now.Add(25 * time.Hour), false},
	} {
		model, used := withRememberedParams(cursorSDKModel(tc.model), tc.account, tc.at)
		if used != tc.want {
			t.Fatalf("%s: remembered=%v, want %v (model %v)", tc.name, used, tc.want, model)
		}
		if used && fmt.Sprint(model["params"]) != "[map[id:fast value:false]]" {
			t.Fatalf("%s: params %v", tc.name, model["params"])
		}
	}

	// The account is the one the launch really uses: a selected account's key or home replaces the adapter's key.
	adapter := &CursorCLIAdapter{apiKey: "server-key"}
	key := func(options ...llmtypes.CallOption) string {
		opts := &llmtypes.CallOptions{}
		for _, option := range options {
			option(opts)
		}
		return adapter.cursorAccountKey(opts)
	}
	account := func(apiKey string) string {
		return key(llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": "/home/shared"}),
			llmtypes.WithProviderAccountCredentials(map[string]string{"CURSOR_API_KEY": apiKey}))
	}
	if account("key-a") == account("key-b") {
		t.Fatal("two selected API keys share remembered settings")
	}
	if account("key-a") != account("key-a") {
		t.Fatal("the same account got a different key")
	}
	if key(llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": "/home/a"})) ==
		key(llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": "/home/b"})) {
		t.Fatal("two login homes share remembered settings")
	}
}

// RTS 2026-11-03: Cursor ended its run after createPlan and the person saw only "Retrying CreatePlan".
func TestCursorSDKPlanTextBecomesReply(t *testing.T) {
	got := cursorSDKPlanText("createPlan", `{"name":"x","plan":"# Plan\n1. Do it","todos":[]}`)
	// #3711: marked as a plan, so the chat folds it to one line like Codex's.
	if !strings.HasPrefix(got, llmtypes.NativePlanFence+"\n# Plan\n1. Do it\n~~~~~\n\n") {
		t.Fatalf("plan reply = %q", got)
	}
	if cursorSDKPlanText("Shell", `{"plan":"no"}`) != "" || cursorSDKPlanText("createPlan", `{"plan":" "}`) != "" {
		t.Fatal("only a non-empty createPlan becomes the reply")
	}
}

// RTS 12 Nov: the answer streamed, Cursor said its turn ended, then the SDK kept the run open (its
// resume-from-checkpoint retries) and the chat waited 15 minutes for an error. A run left open after
// the turn ended, or ended with the SDK's "no progress" error, finishes with the streamed answer.
func TestCursorSDKTurnEndedRunThatStaysOpenFinishesWithTheAnswer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for name, scenario := range map[string]struct {
		updates, ending, status string
		settle                  bool
	}{
		"run stays open":                               {ending: `await new Promise(r => { cancelRun = () => r({status:'cancelled', result:''}); })`, status: "finished", settle: true},
		"no progress error":                            {ending: `({status:'error', error:{message:'Agent turn stopped after repeated resume attempts made no progress'}})`, status: "finished", settle: true},
		"live shell survives quiet window":             {updates: `opts.onDelta({update:{type:'tool-call-started',callId:'shell',toolCall:{type:'shell'}}});`, ending: `await new Promise(r => { cancelRun = () => r({status:'cancelled'}); setTimeout(()=>r({status:'finished',result:'background finished'}),500); })`, status: "finished"},
		"detached task survives quiet window":          {updates: `opts.onDelta({update:{type:'tool-call-completed',callId:'task',toolCall:{type:'task',result:{status:'success',value:{isBackground:true}}}}});`, ending: `await new Promise(r => { cancelRun = () => r({status:'cancelled'}); setTimeout(()=>r({status:'finished',result:'background finished'}),500); })`, status: "finished"},
		"finished foreground task becomes quiet again": {updates: `opts.onDelta({update:{type:'tool-call-started',callId:'task',toolCall:{type:'task'}}});`, ending: `await new Promise(r => { cancelRun = () => r({status:'cancelled'}); setTimeout(()=>opts.onDelta({update:{type:'tool-call-completed',callId:'task',toolCall:{type:'task',result:{status:'success',value:{isBackground:false}}}}}),300); })`, status: "finished", settle: true},
		"non-shell delta completes and becomes quiet":  {updates: `opts.onDelta({update:{type:'tool-call-delta',callId:'read',taskUpdate:{type:'text-delta',text:'working'}}}); opts.onDelta({update:{type:'tool-call-completed',callId:'read',toolCall:{type:'read'}}});`, ending: `await new Promise(r => { cancelRun = () => r({status:'cancelled'}); })`, status: "finished", settle: true},
		"native task failure is not hidden":            {updates: `opts.onDelta({update:{type:'tool-call-completed',callId:'task',toolCall:{type:'task',result:{status:'success',value:{isBackground:true}}}}});`, ending: `({status:'error',error:{message:'Agent turn stopped after repeated resume attempts made no progress'}})`, status: "error"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
			sdk := `
let cancelRun = () => {};
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
exports.Agent={create:async()=>({agentId:'a1', send:async(text, opts)=>({
  async *stream(){ ` + scenario.updates + ` opts.onDelta({update:{type:'text-delta', text:'the answer'}}); opts.onDelta({update:{type:'turn-ended'}}); },
  wait: async()=>` + scenario.ending + `,
  cancel: async()=>cancelRun(),
})})};`
			for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
				if err := os.WriteFile(name, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// The adapter keeps the bridge's input open for the whole chat; closing it cancels the run.
			cmd := exec.CommandContext(ctx, "node", bridge, entry)
			stdin, _ := cmd.StdinPipe()
			stdout, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
			_, _ = io.WriteString(stdin, `{"type":"start","turn_end_quiet_ms":200,"model":{"id":"composer","params":[{"id":"fast","value":"false"}]}}`+"\n"+`{"type":"prompt","text":"hi"}`+"\n")
			var out strings.Builder
			lines := bufio.NewScanner(stdout)
			for lines.Scan() && !strings.Contains(lines.Text(), `"type":"result"`) {
				out.WriteString(lines.Text() + "\n")
			}
			out.WriteString(lines.Text())
			if ctx.Err() != nil {
				t.Fatalf("the turn did not finish: %s", out.String())
			}
			if !strings.Contains(out.String(), `"type":"result","status":"`+scenario.status+`"`) || strings.Contains(out.String(), `"type":"turn_end_settled"`) != scenario.settle {
				t.Fatalf("turn did not finish with its answer: %s", out.String())
			}
		})
	}
}

// agentworks#3128: an SDK promise nobody awaits must not end the bridge. The stall detector's abort is the
// SDK's own retry and the run goes on; any other fault fails only that turn, a fault after Cursor ended its
// turn keeps the answer, and the same bridge answers the next prompt.
func TestCursorSDKUnhandledRejectionDoesNotKillTheBridge(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `
let sends = 0, cancelRun = () => {};
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
const later = () => new Promise(r => setTimeout(r, 50));
exports.Agent={create:async()=>({agentId:'a1', send:async(text, opts)=>{ const n = ++sends; return {
  async *stream(){
    if (n === 1) {
      const stall = new Error('This operation was aborted'); stall.name = 'AbortError';
      stall.stack = 'AbortError: This operation was aborted\n    at Timeout.onStall (sdk.js:1:1)';
      Promise.reject(stall); await later();
      opts.onDelta({update:{type:'text-delta', text:'after stall'}});
    } else if (n === 2) {
      opts.onDelta({update:{type:'text-delta', text:'partial'}});
      Promise.reject(new Error('internal boom')); await later();
    } else if (n === 3) {
      opts.onDelta({update:{type:'text-delta', text:'the answer'}}); opts.onDelta({update:{type:'turn-ended'}});
      setTimeout(() => { throw new Error('timer boom'); }, 10); await later();
    } else opts.onDelta({update:{type:'text-delta', text:'still serving'}});
  },
  wait: async()=> n === 2 || n === 3 ? await new Promise(r => { cancelRun = () => r({status:'cancelled'}); }) : ({status:'finished', result:''}),
  cancel: async()=>cancelRun(),
}}})};`
	for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	_, _ = io.WriteString(stdin, `{"type":"start","turn_end_quiet_ms":60000,"model":{"id":"composer","params":[{"id":"fast","value":"false"}]}}`+"\n")
	lines := bufio.NewScanner(stdout)
	turn := func() string {
		_, _ = io.WriteString(stdin, `{"type":"prompt","text":"hi"}`+"\n")
		var out strings.Builder
		for lines.Scan() {
			out.WriteString(lines.Text() + "\n")
			if strings.Contains(lines.Text(), `"type":"result"`) {
				return out.String()
			}
		}
		t.Fatalf("the bridge ended (ctx=%v): %s", ctx.Err(), out.String())
		return ""
	}
	for i, want := range []struct{ has, not []string }{
		{has: []string{`"type":"sdk_rejection","kind":"unhandled_rejection","action":"recovering"`, `"type":"result","status":"finished"`}},
		{has: []string{`"type":"sdk_rejection","kind":"unhandled_rejection","action":"run_failed","error":"internal boom"`, `"type":"result","status":"error"`, `"error":"Cursor SDK internal error: internal boom"`}},
		{has: []string{`"type":"sdk_rejection","kind":"uncaught_exception","action":"run_failed","error":"timer boom"`, `"action":"sdk_rejection"`, `"type":"result","status":"finished"`}},
		{has: []string{`still serving`, `"type":"result","status":"finished"`}, not: []string{`sdk_rejection`}},
	} {
		out := turn()
		for _, has := range want.has {
			if !strings.Contains(out, has) {
				t.Fatalf("turn %d missing %s: %s", i+1, has, out)
			}
		}
		for _, not := range want.not {
			if strings.Contains(out, not) {
				t.Fatalf("turn %d has %s: %s", i+1, not, out)
			}
		}
	}
}

// agentworks#3135 (SDK 1.3): one bridge serves a model change on the next message (send({ model })), a run left
// active by a crashed process is expired with send({ local: { force } }), typed usage-limit fields reach Go, the
// context fill is read from the agent's checkpoint, and summarize() compacts the kept agent.
func TestCursorSDK13BridgeModelSwitchUsageLimitContextAndSummarize(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `
let creates = 0, wedged = true, summarized = false;
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[{id:'composer-2.5',variants:[{isDefault:true,params:[{id:'fast',value:'false'}]}]}]}};
exports.createAgentPlatform=async opts=>{ if(!opts.localStore) throw Error('private store expected');
  return {checkpointStore:{loadLatest:async id=>({tokenDetails:{usedTokens: summarized ? 1200 : 9000, maxTokens: 200000}})}}; };
exports.Agent={listRuns:async()=>({items:[]}), cancelRun:async()=>{}, create:async()=>{ if(++creates>1) throw Error('second agent'); return {agentId:'a1',
  send:async(text,opts)=>{
    if(wedged && !opts.local?.force) throw Error('Agent a1 already has active run');
    wedged = false;
    if(opts.model.id==='blocked') throw Object.assign(new Error("You've hit your usage limit"),{name:'RateLimitError',code:'PRO_USER_USAGE_LIMIT',status:429});
    return {async *stream(){}, wait:async()=>({status:'finished',result:'model '+opts.model.id+' '+JSON.stringify(opts.model.params||[])})};
  },
  summarize:async()=>({async *stream(){ yield {type:'summary',status:'running'}; yield {type:'summary',status:'completed'}; },
    wait:async()=>{ summarized = true; return {status:'finished',result:'summary'}; }}),
}; }};`
	files := map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk), filepath.Join(dir, "sqlite.js"): []byte(`exports.SqliteLocalAgentStore={open:async()=>({private:true})};`)}
	for name, data := range files {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", bridge, entry)
	cmd.Stdin = strings.NewReader(`{"type":"start","cwd":` + fmt.Sprintf("%q", dir) + `,"store_root":` + fmt.Sprintf("%q", dir) + `,"catalog_wait_ms":1500,"model":{"id":"composer-2.5"}}
{"type":"prompt","text":"hi","model":{"id":"composer-2.5"}}
{"type":"prompt","text":"two","model":{"id":"gpt-6"}}
{"type":"prompt","text":"three","model":{"id":"blocked"}}
{"type":"summarize"}
{"type":"close"}
`)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("bridge failed: %v, %s", err, out)
	}
	for _, want := range []string{
		`"type":"stale","agent_id":"a1","action":"forced"`,
		`"result":"model composer-2.5 [{\"id\":\"fast\",\"value\":\"false\"}]"`,
		`"type":"model","model_id":"gpt-6","from":"composer-2.5"`,
		`"result":"model gpt-6 []"`,
		`"error_code":"PRO_USER_USAGE_LIMIT","error_status":429,"error_name":"RateLimitError"`,
		`"type":"context","used":9000,"max":200000`,
		`"type":"compaction","status":"running"`,
		`"type":"context","used":1200,"max":200000`,
		`"type":"summarize_result","status":"finished","summary_status":"completed","before_used":9000,"after_used":1200`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
	if strings.Count(out, `"type":"ready"`) != 1 || strings.Count(out, `"type":"model"`) != 2 {
		t.Fatalf("one bridge must serve every model (ready once, two switches): %s", out)
	}
}

// agentworks#3135: a model change no longer restarts the Cursor bridge or its native agent, and idle compaction
// runs Cursor's own summarize on the kept agent.
func TestCursorSDKModelChangeKeepsTheBridgeAndIdleCompacts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "fake-sdk.cjs")
	sdk := `
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[{id:'composer-2.5'},{id:'gpt-6'}]}};
exports.Agent={create:async()=>({agentId:'agent-'+process.pid,
  send:async(text,opts)=>({async *stream(){}, wait:async()=>({status:'finished',result:process.pid+' '+opts.model.id})}),
  summarize:async()=>({async *stream(){ yield {type:'summary',status:'running'}; yield {type:'summary',status:'completed'}; }, wait:async()=>({status:'finished',result:'summary'})}),
})};`
	if err := os.WriteFile(entry, []byte(sdk), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	owner := "chat-model-switch"
	defer closeCursorSDKInstance(owner, nil)
	call := func(model string, extra ...llmtypes.CallOption) (*llmtypes.ContentResponse, []llmtypes.StreamChunk) {
		stream := make(chan llmtypes.StreamChunk, 64)
		options := append([]llmtypes.CallOption{WithWorkingDir(dir), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true), llmtypes.WithStreamingChan(stream)}, extra...)
		var resp *llmtypes.ContentResponse
		var err error
		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, err = NewCursorCLIAdapter("", model, nil).GenerateContent(context.Background(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello")}, options...)
		}()
		var chunks []llmtypes.StreamChunk
		for chunk := range stream {
			chunks = append(chunks, chunk)
		}
		<-done
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		return resp, chunks
	}
	first, _ := call("composer-2.5")
	second, _ := call("gpt-6")
	pid, _, _ := strings.Cut(first.Choices[0].Content, " ")
	if second.Choices[0].Content != pid+" gpt-6" {
		t.Fatalf("model change restarted the bridge or kept the old model: %q then %q", first.Choices[0].Content, second.Choices[0].Content)
	}
	resp, chunks := call("gpt-6", llmtypes.WithCodingProviderIdleCompact())
	result, ok := llmtypes.IdleCompactResultFromResponse(resp)
	if !ok || !result.Compacted {
		t.Fatalf("idle compaction did not compact: %#v", result)
	}
	var phases []llmtypes.ContextCompactionPhase
	for _, chunk := range chunks {
		if chunk.ContextCompaction != nil {
			phases = append(phases, chunk.ContextCompaction.Phase)
		}
	}
	if fmt.Sprint(phases) != "[start end]" {
		t.Fatalf("compaction events = %v", phases)
	}
}
