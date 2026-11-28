package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// fakeClaude installs a `claude` that records each prompt (its stdin) and prints frames.
func fakeClaude(t *testing.T, frames string) (dir, prompts string) {
	t.Helper()
	dir = t.TempDir()
	prompts = filepath.Join(dir, "prompts")
	script := "#!/bin/sh\n{ cat; echo; echo '---'; } >> " + prompts + "\ncat <<'EVENTS'\n" + frames + "EVENTS\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir, prompts
}

func runClaudeGoalTurn(t *testing.T, ctx context.Context, dir, owner, resume, text string) (*llmtypes.ContentResponse, []llmtypes.StreamChunk) {
	t.Helper()
	custom := map[string]interface{}{MetadataKeyWorkingDir: dir, MetadataKeyInteractiveSessionID: owner}
	if resume != "" {
		custom[MetadataKeyResumeSessionID] = resume
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ch := make(chan llmtypes.StreamChunk, 128)
	a := NewClaudeCodeInteractiveAdapter("claude-opus-5-5", &MockLogger{})
	resp, err := a.generateContentStructured(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}}, &llmtypes.CallOptions{StreamChan: ch, Metadata: &llmtypes.Metadata{Custom: custom}})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []llmtypes.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}
	return resp, chunks
}

// #4626: a goal turn sends Claude Code's own `/goal <objective>`, shows Claude's unmet checks on the goal, keeps
// Claude's "Goal set" line out of the reply, and records the goal done when the turn ends with its answer.
func TestClaudeGoalTurnSendsNativeGoalAndRecordsChecks(t *testing.T) {
	dir, prompts := fakeClaude(t, `{"type":"system","subtype":"init","session_id":"native-1"}
{"type":"assistant","message":{"id":"s1","model":"<synthetic>","content":[{"type":"text","text":"Goal set: ship the fix"}]}}
{"type":"assistant","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"Fixed it."}]}}
{"type":"user","isSynthetic":true,"message":{"role":"user","content":[{"type":"text","text":"Stop hook feedback:\n[ship the fix]: The tests have not been run."}]}}
{"type":"assistant","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"text","text":"Tests pass."}]}}
{"type":"result","subtype":"success","is_error":false,"result":"Tests pass.","session_id":"native-1"}
`)
	owner := "claude-goal-chat"
	t.Cleanup(func() { claudeNativeGoals.Delete(owner) })
	ctx := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: owner, Action: "set", Objective: "ship   the fix"})
	resp, chunks := runClaudeGoalTurn(t, ctx, dir, owner, "", "/goal ship the fix")

	sent, _ := os.ReadFile(prompts)
	if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(string(sent)), "---")) != "/goal ship the fix" {
		t.Fatalf("prompt = %q", sent)
	}
	var goals []llmtypes.NativeGoalUpdate
	var reply strings.Builder
	for _, chunk := range chunks {
		if chunk.NativeGoal != nil {
			goals = append(goals, *chunk.NativeGoal)
		}
		if chunk.Type == llmtypes.StreamChunkTypeContent {
			reply.WriteString(chunk.Content)
		}
	}
	if len(goals) != 3 || goals[0].Goal.Status != "active" || goals[1].Goal.LastCheck != "The tests have not been run." || *goals[1].Goal.Checks != 1 || goals[2].Goal.Status != "complete" || goals[2].NativeSessionID != "native-1" || goals[2].Goal.Objective != "ship the fix" {
		t.Fatalf("goal updates = %+v", goals)
	}
	if strings.Contains(reply.String(), "Goal set") || strings.Contains(resp.Choices[0].Content, "Goal set") {
		t.Fatalf("Claude's goal line reached the reply: %q / %q", reply.String(), resp.Choices[0].Content)
	}
	if left, _ := claudeNativeGoals.Load(owner); left != false {
		t.Fatal("a met goal is still remembered as set")
	}
}

// #4626: Claude keeps a goal on its session after its turn was stopped, so an ordinary turn clears it first with
// `/goal clear`, silently, and then sends the person's message.
func TestClaudeOrdinaryTurnClearsALeftoverNativeGoal(t *testing.T) {
	dir, prompts := fakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"Hello.","session_id":"native-2"}
`)
	owner := "claude-goal-left"
	claudeNativeGoals.Store(owner, true)
	t.Cleanup(func() { claudeNativeGoals.Delete(owner) })
	resp, chunks := runClaudeGoalTurn(t, context.Background(), dir, owner, "native-2", "hello")
	sent, _ := os.ReadFile(prompts)
	turns := strings.Split(strings.TrimSpace(string(sent)), "---")
	if len(turns) != 3 || strings.TrimSpace(turns[0]) != "/goal clear" || !strings.Contains(turns[1], "hello") {
		t.Fatalf("prompts = %q", sent)
	}
	for _, chunk := range chunks {
		if chunk.NativeGoal != nil {
			t.Fatalf("an ordinary turn recorded a goal: %+v", chunk.NativeGoal)
		}
	}
	if resp.Choices[0].Content != "Hello." {
		t.Fatalf("reply = %q", resp.Choices[0].Content)
	}
	if left, _ := claudeNativeGoals.Load(owner); left != false {
		t.Fatal("goal still remembered as set after clearing")
	}
	// No goal left: no extra turn.
	_ = os.Remove(prompts)
	runClaudeGoalTurn(t, context.Background(), dir, owner, "native-2", "again")
	if sent, _ := os.ReadFile(prompts); strings.Contains(string(sent), "/goal clear") {
		t.Fatalf("cleared again: %q", sent)
	}
}

func TestClaudeTranscriptGoalState(t *testing.T) {
	lines := map[string]claudeTranscriptGoal{
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>Goal set: ship</local-command-stdout>"}`:     claudeTranscriptGoalActive,
		`{"type":"attachment","attachment":{"type":"goal_status","met":false,"sentinel":true,"condition":"ship"}}`:                claudeTranscriptGoalActive,
		`{"type":"attachment","attachment":{"type":"goal_status","met":true,"condition":"ship"}}`:                                 claudeTranscriptGoalNone,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>Goal cleared: ship</local-command-stdout>"}`: claudeTranscriptGoalNone,
		`{"type":"system","subtype":"informational","content":"Goal cleared after an unrecoverable error (context limit)"}`:       claudeTranscriptGoalNone,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"I wrote Goal set: in the notes"}]}}`:                    claudeTranscriptGoalActive,
	}
	for line, want := range lines {
		if got := claudeTranscriptGoalLine(line, claudeTranscriptGoalActive); got != want {
			t.Fatalf("%s: got %d want %d", line, got, want)
		}
	}
	if claudeTranscriptGoalLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"Goal set: ship"}]}}`, claudeTranscriptGoalNone) != claudeTranscriptGoalNone {
		t.Fatal("a reply mentioning a goal set one")
	}
}
