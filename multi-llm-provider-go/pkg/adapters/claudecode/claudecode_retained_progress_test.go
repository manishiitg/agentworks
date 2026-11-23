package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Replay the six narration commits from the 2026-09-12 Posted Tweets turn.
// Each arrived while tools were still running; no generation stream exists
// when the prompt is submitted directly to the retained terminal.
func TestClaudeRetainedProgressArrivesBeforeFinalAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const nativeID = "83de1833-360c-4df8-9bfb-db7d10b014ad"
	workingDir := t.TempDir()
	path := filepath.Join(home, ".claude", "projects", claudeTranscriptProjectSlug(workingDir), nativeID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	old := claudeInteractivePersistentRegistry.Replace(map[string]*claudeInteractivePersistentSession{
		t.Name(): {nativeSessionID: nativeID, workingDir: workingDir},
	})
	t.Cleanup(func() { claudeInteractivePersistentRegistry.Replace(old) })
	start := time.Date(2026, 9, 12, 9, 35, 42, 0, time.UTC)
	appendText := func(text, stop string, timestamp time.Time) {
		t.Helper()
		row := map[string]interface{}{
			"type": "assistant", "timestamp": timestamp.Format(time.RFC3339Nano),
			"message": map[string]interface{}{
				"stop_reason": stop,
				"content":     []map[string]string{{"type": "text", "text": text}},
			},
		}
		if err := json.NewEncoder(file).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	appendText("Previous turn's answer.", "end_turn", start.Add(-time.Minute))
	if got := ReadRetainedTurnProgressMessages(t.Name(), start); len(got) != 0 {
		t.Fatalf("replayed previous turn: %+v", got)
	}
	narration := []string{
		"Good, all the helpers I need exist. Now let's add the tab: button, panel, render function, and wiring.",
		"Now inserting the new render function right before `initTabs()`.",
		"Now wiring `renderPostedTweets()` into `safeRender()` and `focusRequestedTab()`.",
		"Now validating and previewing the report.",
		"Valid, no errors. Now previewing to confirm it actually renders.",
		"This is a timing issue — my new render call sits at the very end of an already-long sequential chain, so it just hasn't finished by the preview's settle timeout. Since it's independent of the other panels' data, let me fire it in parallel instead of waiting its turn.",
	}
	for i, text := range narration {
		appendText(text, "tool_use", start.Add(time.Duration(i+1)*time.Second))
		got := ReadRetainedTurnProgressMessages(t.Name(), start)
		if len(got) != 1 || got[0].Parts[0].(llmtypes.TextContent).Text != text {
			t.Fatalf("live update %d missing: %+v", i, got)
		}
		if repeated := ReadRetainedTurnProgressMessages(t.Name(), start); len(repeated) != 0 {
			t.Fatalf("live update %d repeated: %+v", i, repeated)
		}
		if final := ReadRetainedTurnMessages(t.Name(), start); len(final) != 0 {
			t.Fatalf("update %d prematurely completed the turn: %+v", i, final)
		}
	}
	appendText("Fixed and validated all tabs.", "end_turn", start.Add(time.Minute))
	if final := ReadRetainedTurnMessages(t.Name(), start); len(final) != 1 {
		t.Fatalf("missing final answer: %+v", final)
	}
	if finalChunk := ReadRetainedTurnProgressMessages(t.Name(), start); len(finalChunk) != 1 {
		t.Fatalf("missing final progress flush: %+v", finalChunk)
	}
}
