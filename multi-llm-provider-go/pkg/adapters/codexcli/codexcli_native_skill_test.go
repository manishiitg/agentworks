package codexcli

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
	"time"
)

// PLAT-746-1008: actual JSON-RPC serialization carries the native skill item,
// including an active-turn steer; another chat must receive only its text.
func TestCodexNativeSkillPromptAndSteerKeepExactSession(t *testing.T) {
	owner := "skill-chat"
	rpc, _ := goalFixtureRPC(nil)
	wire := &imageFrameWriter{rpc: rpc}
	rpc.stdin = wire
	session := &codexAppSession{owner: owner, rpc: rpc, threadID: "thread", turnID: "turn"}
	codexAppSessions.Lock()
	codexAppSessions.sessions[owner] = session
	codexAppSessions.Unlock()
	defer func() { codexAppSessions.Lock(); delete(codexAppSessions.sessions, owner); codexAppSessions.Unlock() }()
	skill := llmtypes.NativeSkill{Name: "review", Path: "/project/.agents/skills/review/SKILL.md"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	input, err := codexAppNativeInput(llmtypes.WithNativeSkill(ctx, owner, skill), owner, "Review changes")
	if err != nil {
		t.Fatal(err)
	}
	if err := rpc.call(ctx, "turn/start", map[string]any{"threadId": "thread", "input": input}, nil); err != nil {
		t.Fatal(err)
	}
	if err := SendCodexAppServerInput(llmtypes.WithNativeSkill(ctx, owner, skill), owner, "Also inspect tests"); err != nil {
		t.Fatal(err)
	}
	for _, frame := range wire.frames {
		var body struct {
			Input          []struct{ Type, Name, Path, Text string }
			ExpectedTurnID string `json:"expectedTurnId"`
		}
		if err := json.Unmarshal(frame.Params, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Input) != 2 || body.Input[1].Type != "skill" || body.Input[1].Path != skill.Path || body.Input[1].Name != skill.Name {
			t.Fatalf("skill missing from native input: %s", frame.Params)
		}
		if frame.Method == "turn/steer" && body.ExpectedTurnID != "turn" {
			t.Fatal("steer lost native turn fence")
		}
	}
	if len(wire.frames) != 2 {
		t.Fatal("native delivery missing")
	}
	input, err = codexAppNativeInput(llmtypes.WithNativeSkill(ctx, owner, skill), "child", "child text")
	if err != nil || len(input) != 1 {
		t.Fatal("skill crossed chat boundary")
	}
	if _, err = codexAppNativeInput(llmtypes.WithNativeSkill(ctx, owner, llmtypes.NativeSkill{Name: "review\n/other", Path: skill.Path}), owner, "text"); err == nil {
		t.Fatal("invalid command accepted")
	}
}
