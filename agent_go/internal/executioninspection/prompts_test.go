package executioninspection

import (
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"testing"
	"time"
)

func TestPromptVersionsJoinExactOwnerAndTurnWithoutInferringMissingSessions(t *testing.T) {
	system := func(at int, turn, text string) PromptEvent {
		return PromptEvent{ExecutionID: "work", Event: orchevents.BackgroundAgentTranscriptEvent{Timestamp: time.Unix(int64(at), 0), Type: "system_message", Role: "system", TurnID: turn, Text: text}}
	}
	marker := func(at int, owner, turn, handle string) PromptEvent {
		return PromptEvent{ExecutionID: owner, Event: orchevents.BackgroundAgentTranscriptEvent{Timestamp: time.Unix(int64(at), 0), Type: "runtime_session", TurnID: turn, RuntimeSessionID: handle}}
	}
	recorded := []PromptEvent{
		marker(6, "work", "three", "codex-cli:C"), // Late async writes need not arrive in order.
		system(1, "one", "First prompt"), marker(2, "work", "one", "codex-cli:A"),
		system(3, "two", "First prompt"), marker(4, "work", "two", "codex-cli:B"),
		system(5, "three", "Changed prompt"),
		system(7, "unknown", "Older evidence"), marker(8, "another-execution", "unknown", "codex-cli:foreign"),
	}
	got := PromptVersions(recorded)
	if len(got.Versions) != 3 || got.CurrentIndex != 2 {
		t.Fatalf("wrong versions: %+v", got)
	}
	if got.Versions[0].SessionLabel != "Used in 2 sessions" {
		t.Fatalf("identical text became another version or lost its sessions: %+v", got.Versions[0])
	}
	if got.Versions[1].Label != "Version 2 of 3 · new session" || got.Versions[1].SessionCount != 1 {
		t.Fatalf("recorded handle change not explained: %+v", got.Versions[1])
	}
	if got.Versions[2].Label != "Version 3 of 3" || got.Versions[2].SessionLabel != "Session not recorded" {
		t.Fatalf("inferred an unrecorded restart or imported foreign evidence: %+v", got.Versions[2])
	}
	if recorded[0].Event.RuntimeSessionID != "codex-cli:C" {
		t.Fatal("changed raw evidence")
	}
}
