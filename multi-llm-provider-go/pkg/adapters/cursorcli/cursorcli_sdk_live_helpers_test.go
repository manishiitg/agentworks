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

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3132: a running Cursor helper's steps, streamed on its task call, become its
// live conversation, readable only by its own chat and call; the child the SDK
// returns at the end replaces it.
func TestCursorHelperConversationIsLiveThenFinal(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
	sdk := `exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[]}};
 exports.Agent={create:async()=>({agentId:'native-parent',send:async(text,opts)=>{
 const d=u=>opts.onDelta({update:u});
 d({type:'tool-call-started',callId:'task-1',toolCall:{type:'task',args:{description:'Find tests',prompt:'look for tests'}}});
 d({type:'tool-call-delta',callId:'task-1',modelCallId:'m',taskUpdate:{type:'text-delta',text:'Looking '}});
 d({type:'tool-call-delta',callId:'task-1',modelCallId:'m',taskUpdate:{type:'text-delta',text:'now.'}});
 d({type:'tool-call-delta',callId:'task-1',modelCallId:'m',taskUpdate:{type:'tool-call-started',callId:'c1',toolCall:{type:'grep',args:{pattern:'Test'}}}});
 d({type:'tool-call-delta',callId:'task-1',modelCallId:'m',taskUpdate:{type:'tool-call-completed',callId:'c1',toolCall:{type:'grep',args:{pattern:'Test'},result:{status:'success',value:{matches:3}}}}});
 return {id:'run',status:'running',onDidChangeStatus(){return()=>{}},async *stream(){},async wait(){return {status:'finished',result:''}},cancel:async()=>{}};
 }})};`
	for path, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
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
	owner := "chat-cursor-helper"
	io.WriteString(stdin, `{"type":"start","model":{"id":"composer","params":[{"id":"fast","value":"false"}]}}`+"\n"+`{"type":"prompt","text":"hello"}`+"\n")
	scanner := bufio.NewScanner(stdout)
	steps := 0
	for scanner.Scan() {
		raw, ok := strings.CutPrefix(scanner.Text(), cursorSDKMarker)
		if !ok {
			continue
		}
		var event cursorSDKEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "helper_progress" {
			steps++
			recordCursorHelperProgress(owner, event.AgentID, event)
		}
		if event.Type == "result" {
			break
		}
	}
	if steps != 5 {
		t.Fatalf("helper steps = %d, want 5", steps)
	}
	child, ok := CursorSubagentConversation(owner, "task-1")
	if !ok || child.Status != "running" || child.Name != "Find tests" || child.ParentID != "native-parent" || child.ToolUseID != "task-1" {
		t.Fatalf("live child = %+v ok=%t", child, ok)
	}
	if len(child.Messages) != 4 {
		t.Fatalf("messages = %+v", child.Messages)
	}
	if text := child.Messages[1].Parts[0].(llmtypes.TextContent).Text; text != "Looking now." {
		t.Fatalf("text = %q", text)
	}
	if call := child.Messages[2].Parts[0].(llmtypes.ToolCall); call.ID != "task-1:c1" || call.FunctionCall.Name != "grep" {
		t.Fatalf("tool call = %+v", call)
	}
	if resp := child.Messages[3].Parts[0].(llmtypes.ToolCallResponse); resp.ToolCallID != "task-1:c1" || !strings.Contains(resp.Content, "3") {
		t.Fatalf("tool result = %+v", resp)
	}
	if _, ok := CursorSubagentConversation("other-chat", "task-1"); ok {
		t.Fatal("another chat read this helper")
	}
	if _, ok := CursorSubagentConversation(owner, "task-2"); ok {
		t.Fatal("an unknown call returned a helper")
	}
	finishCursorHelper(owner, "task-1", llmtypes.NativeSubagentTranscript{ID: "agent-child", Name: "Find tests", Provider: "cursor-cli", Status: "completed",
		StartedAt: time.Now(), Messages: []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "Done.")}})
	recordCursorHelperProgress(owner, "native-parent", cursorSDKEvent{CallID: "task-1", Kind: "text", Text: "late"})
	final, _ := CursorSubagentConversation(owner, "task-1")
	if final.Status != "completed" || final.ID != "agent-child" || final.ToolUseID != "task-1" || len(final.Messages) != 1 || final.StartedAt.After(child.StartedAt) {
		t.Fatalf("final child = %+v", final)
	}
	recordCursorHelperProgress(owner, "", cursorSDKEvent{CallID: "task-3", Kind: "started"})
	endCursorHelpers(owner)
	if stopped, _ := CursorSubagentConversation(owner, "task-3"); stopped.Status != "interrupted" {
		t.Fatalf("a closed bridge's helper = %s", stopped.Status)
	}
}
