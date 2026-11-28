package acpcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3920: a message sent while an agy 1.5+ turn runs goes in as another session/prompt. agy answers the running prompt
// end_turn and carries the turn on, so that early answer does not end the turn: the latest prompt's does, with the
// usage of both, and the running tool is not cancelled.
func TestAgySteersARunningTurnWithAnotherPrompt(t *testing.T) {
	withAgyLaunch(t, "GEMINI_API_KEY=test-only")
	workdir, owner := t.TempDir(), "chat-agy-steer"
	peer := installReplay(t, "agy_steer", workdir)
	ctx := context.Background()

	if err := SteerTurn(ctx, owner, "agy", "too early"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("steer with no session = %v, want not sent", err)
	}
	type outcome struct {
		resp *llmtypes.ContentResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := Generate(ctx, "agy", "gemini-4-pro-high", human("run the build"), agyCall(owner, workdir, ""))
		done <- outcome{resp, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !TurnSteerable(owner, "agy") {
		if time.Now().After(deadline) {
			t.Fatal("the running turn never took messages")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := SteerTurn(ctx, owner, "agy", "also say BANANA"); err != nil {
		t.Fatalf("steer = %v", err)
	}
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the steered turn never ended")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if answer := got.resp.Choices[0].Content; !strings.Contains(answer, "BANANA") {
		t.Fatalf("answer = %q, want the steered reply", answer)
	}
	gi := got.resp.Choices[0].GenerationInfo
	if gi == nil || gi.PromptTokens == nil || *gi.PromptTokens != 15 || gi.TotalTokens == nil || *gi.TotalTokens != 18 {
		t.Fatalf("usage = %+v, want both prompts summed (15 in, 18 total)", gi)
	}
	if TurnSteerable(owner, "agy") {
		t.Fatal("a finished turn still takes messages")
	}
	if err := SteerTurn(ctx, owner, "agy", "after the turn"); !errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
		t.Fatalf("steer after the turn = %v, want not sent", err)
	}
	CloseAgentSession(owner, "agy")
	peer.wait(t)
}

// Only agy 1.5.0 and later read a mid-turn prompt as a steer; the 1.4 harness queues it, and an unknown version is
// treated as old.
func TestAgySteersWithPromptFromVersion150(t *testing.T) {
	agent, ok := lookupAny("agy")
	if !ok || agent.SteersWithPrompt == nil {
		t.Fatal("agy has no steering check")
	}
	for version, want := range map[string]bool{"1.5.0": true, "1.5.16": true, "v1.6": true, "2.0.1": true, "1.4.9": false, "1.4": false, "": false, "agy_acp_server_20261101_1": false} {
		if got := agent.SteersWithPrompt(version); got != want {
			t.Errorf("SteersWithPrompt(%q) = %v, want %v", version, got, want)
		}
	}
}

type finishedOutcome struct {
	refusalOutcome
	finished bool
}

func (o finishedOutcome) BeginFinished(string) func() bool { return func() bool { return o.finished } }

// T3 Code #44294: agy writes its whole reply and never answers session/prompt. Once the turn has been silent with
// nothing running, and agy's record shows the reply done, the turn ends with that reply instead of loading forever,
// and the stuck process is stopped so the next message starts it again.
func TestAgySilentPromptWithAFinishedReplyEndsTheTurn(t *testing.T) {
	previousAfter, previousEvery := silentPromptAfter, silentCheckEvery
	silentPromptAfter, silentCheckEvery = 100*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { silentPromptAfter, silentCheckEvery = previousAfter, previousEvery })
	SetLaunch("agy", func(LaunchRequest) (*LaunchSpec, error) {
		return &LaunchSpec{Env: []string{"GEMINI_API_KEY=test-only"}, AutoApprove: true, QuestionIDPrefix: "interaction_", NoSessionMCP: true, Outcome: finishedOutcome{finished: true}}, nil
	})
	t.Cleanup(func() { SetLaunch("agy", nil) })
	workdir, owner := t.TempDir(), "chat-agy-silent"
	peer := installReplay(t, "agy_silent_prompt", workdir)
	type outcome struct {
		resp *llmtypes.ContentResponse
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		resp, err := Generate(context.Background(), "agy", "gemini-4-pro-high", human("write it"), agyCall(owner, workdir, ""))
		done <- outcome{resp, err}
	}()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the silent turn never ended")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if answer := got.resp.Choices[0].Content; answer != "All written." {
		t.Fatalf("answer = %q, want the finished reply", answer)
	}
	peer.wait(t)
	CloseAgentSession(owner, "agy")
}

// T3 Code #41923: agy keeps its checklist with "Run update_steps" tool calls, not ACP plan updates. Each call becomes
// the chat's task list (done reads as completed) and never shows as a tool row; other tools still do.
func TestAgyUpdateStepsBecomesTheTaskList(t *testing.T) {
	agent, _ := lookupAny("agy")
	s := &session{agent: agent, sessionID: "agy_sess"}
	tools := map[string]*toolCall{}
	var text strings.Builder
	var chunks []llmtypes.StreamChunk
	emit := func(c llmtypes.StreamChunk) { chunks = append(chunks, c) }
	update := func(raw string) {
		t.Helper()
		var u sessionUpdate
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			t.Fatal(err)
		}
		s.handleUpdate(u, map[string]any{}, tools, &text, emit)
	}
	update(`{"sessionUpdate":"tool_call","toolCallId":"steps-1","title":"Run update_steps","kind":"other","status":"in_progress","rawInput":{"steps":[{"title":"Create a.txt","status":"in_progress"},{"title":"Create b.txt","status":"pending"}]}}`)
	update(`{"sessionUpdate":"tool_call_update","toolCallId":"steps-1","status":"completed"}`)
	update(`{"sessionUpdate":"tool_call","toolCallId":"steps-2","title":"Run update_steps","status":"in_progress","rawInput":{"steps":[{"title":"Create a.txt","status":"done"},{"title":"Create b.txt","status":"in_progress"}]}}`)
	update(`{"sessionUpdate":"tool_call","toolCallId":"cmd-1","title":"Run ls","kind":"execute","status":"in_progress","rawInput":{"CommandLine":"ls"}}`)

	var plans []string
	starts := 0
	for _, c := range chunks {
		switch c.Type {
		case llmtypes.StreamChunkTypeCodingAgentPlan:
			var row []string
			for _, item := range c.CodingAgentPlan.Items {
				row = append(row, item.Text+"="+item.Status)
			}
			if c.CodingAgentPlan.Provider != "agy-cli" {
				t.Fatalf("plan provider = %q", c.CodingAgentPlan.Provider)
			}
			plans = append(plans, strings.Join(row, ","))
		case llmtypes.StreamChunkTypeToolCallStart:
			starts++
			if c.ToolCallID != "cmd-1" {
				t.Fatalf("the checklist showed as a tool row: %+v", c)
			}
		}
	}
	want := []string{"Create a.txt=running,Create b.txt=pending", "Create a.txt=completed,Create b.txt=running"}
	if strings.Join(plans, "|") != strings.Join(want, "|") || starts != 1 {
		t.Fatalf("plans = %q, tool rows = %d; want %q and 1", plans, starts, want)
	}
}
