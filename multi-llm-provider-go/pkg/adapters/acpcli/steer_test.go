package acpcli

import (
	"context"
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
