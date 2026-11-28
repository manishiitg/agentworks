package agycli

import (
	"context"
	"errors"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func agyGoalUpdates(ch chan llmtypes.StreamChunk) []llmtypes.NativeGoalUpdate {
	var out []llmtypes.NativeGoalUpdate
	for {
		select {
		case chunk := <-ch:
			if chunk.Type == llmtypes.StreamChunkTypeNativeGoal && chunk.NativeGoal != nil {
				out = append(out, *chunk.NativeGoal)
			}
		default:
			return out
		}
	}
}

// #4604: a goal turn sends agy its own `/goal <objective>` in place of the person's pending text, records the goal
// active when it starts, complete when agy ends the turn and paused (stopped) when the turn is stopped or fails.
// Without a goal command nothing changes.
func TestAgyGoalTurnSendsAgysGoalCommandAndRecordsItsEnd(t *testing.T) {
	owner := "chat-agy-goal"
	history := []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Be brief."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hi"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "hello"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "/goal make the tests pass"}}},
	}
	ch := make(chan llmtypes.StreamChunk, 8)
	opts := &llmtypes.CallOptions{StreamChan: ch}

	same, goal, err := agyGoalTurn(context.Background(), owner, "agy_sess", history, opts)
	if err != nil || goal != nil || len(same) != len(history) {
		t.Fatalf("a turn without a goal command changed: goal=%v err=%v", goal, err)
	}

	ctx := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: owner, Action: "set", Objective: "make the tests pass"})
	messages, goal, err := agyGoalTurn(ctx, owner, "agy_sess", history, opts)
	if err != nil || goal == nil {
		t.Fatalf("goal=%v err=%v", goal, err)
	}
	if len(messages) != 4 || messages[2].Role != llmtypes.ChatMessageTypeAI || messages[3].Parts[0].(llmtypes.TextContent).Text != "/goal make the tests pass" {
		t.Fatalf("messages = %+v", messages)
	}
	goal.finish(ctx, nil, nil)
	updates := agyGoalUpdates(ch)
	if len(updates) != 2 || updates[0].Goal.Status != "active" || updates[0].Reason != "intent_set" || updates[1].Goal.Status != "complete" || updates[1].Provider != "agy-cli" || updates[1].NativeSessionID != "agy_sess" || updates[1].Goal.TimeUsedSeconds == nil {
		t.Fatalf("updates = %+v", updates)
	}

	resume := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: owner, Action: "resume", Objective: "make the tests pass"})
	stopped, cancel := context.WithCancel(resume)
	_, goal, err = agyGoalTurn(stopped, owner, "agy_sess", history, opts)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	goal.finish(stopped, nil, errors.New("turn stopped"))
	updates = agyGoalUpdates(ch)
	if len(updates) != 2 || updates[0].Reason != "intent_resume" || updates[1].Goal.Status != "paused" {
		t.Fatalf("stopped updates = %+v", updates)
	}

	for _, action := range []string{"pause", "clear", "show"} {
		control := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: owner, Action: action})
		if _, _, err := agyGoalTurn(control, owner, "agy_sess", history, opts); err == nil {
			t.Fatalf("/goal %s reached agy", action)
		}
	}
}
