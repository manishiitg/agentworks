package orchestrator

import (
	"testing"
	"time"
)

func TestCumulativeSessionUsageAddsOnlyNewTurnDelta(t *testing.T) {
	file := &PhaseTokenUsageFile{}
	now := time.Now()

	ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{
		InputTokens: 1000, OutputTokens: 500, LLMCallCount: 1,
	}, now)
	delta := ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{
		InputTokens: 2200, OutputTokens: 1100, LLMCallCount: 2,
	}, now)

	if delta.InputTokens != 1200 || delta.OutputTokens != 600 || delta.LLMCallCount != 1 {
		t.Fatalf("second turn delta = %+v, want 1200 input / 600 output / 1 call", delta)
	}
	got := file.ByModel["claude-sonnet-5-5"]
	if got.InputTokens != 2200 || got.OutputTokens != 1100 || got.LLMCallCount != 2 {
		t.Fatalf("same-session aggregate = %+v, want cumulative 2200/1100/2", got)
	}
}

func TestCumulativeSessionUsagePreservesOtherChatsUsingSameModel(t *testing.T) {
	file := &PhaseTokenUsageFile{}
	now := time.Now()

	ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{
		InputTokens: 2200, OutputTokens: 1100, LLMCallCount: 2,
	}, now)
	delta := ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-b", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{
		InputTokens: 700, OutputTokens: 300, LLMCallCount: 1,
	}, now)

	if delta.InputTokens != 700 || delta.OutputTokens != 300 {
		t.Fatalf("new chat delta = %+v, want its full first snapshot", delta)
	}
	got := file.ByModel["claude-sonnet-5-5"]
	if got.InputTokens != 2900 || got.OutputTokens != 1400 || got.LLMCallCount != 3 {
		t.Fatalf("cross-chat aggregate = %+v, want 2900/1400/3", got)
	}
}

func TestCumulativeSessionUsageAttributesOnlyNewDeltaAfterModelChange(t *testing.T) {
	file := &PhaseTokenUsageFile{}
	now := time.Now()

	ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{
		InputTokens: 1000, OutputTokens: 500, LLMCallCount: 1,
	}, now)
	ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-opus-5", &ModelTokenUsage{
		InputTokens: 1600, OutputTokens: 800, LLMCallCount: 2,
	}, now)

	sonnet := file.ByModel["claude-sonnet-5-5"]
	opus := file.ByModel["claude-opus-5"]
	if sonnet.InputTokens != 1000 || opus.InputTokens != 600 {
		t.Fatalf("model split sonnet=%+v opus=%+v, want 1000 then 600 input tokens", sonnet, opus)
	}
}

func TestCumulativeSessionUsageTreatsCounterResetAsFreshEpoch(t *testing.T) {
	file := &PhaseTokenUsageFile{}
	now := time.Now()
	ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{InputTokens: 1000, LLMCallCount: 2}, now)
	delta := ApplyCumulativeSessionModelUsageToPhaseTokenUsageFile(file, "chat-a", "workflow-builder", "claude-sonnet-5-5", &ModelTokenUsage{InputTokens: 250, LLMCallCount: 1}, now)
	if delta.InputTokens != 250 || file.ByModel["claude-sonnet-5-5"].InputTokens != 1250 {
		t.Fatalf("reset delta=%+v aggregate=%+v, want 250 and 1250", delta, file.ByModel["claude-sonnet-5-5"])
	}
}
