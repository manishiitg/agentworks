package step_based_workflow

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeScriptedSuccessCountsRepairsCapturedAgenticAttribution(t *testing.T) {
	// Captured from build-in-public/step-route-selector after four successful
	// scripted_fast_path executions. The runtime had attributed all four to
	// "agentic", even though no agentic fallback ran.
	var metadata ScriptedMetadata
	if err := json.Unmarshal([]byte(`{
  "step_id":"step-route-selector",
  "script_version":5,
  "total_runs":4,
  "successful_runs":{"agentic":4},
  "failed_runs":0,
  "relearn_count":4
}`), &metadata); err != nil {
		t.Fatal(err)
	}
	normalizeScriptedSuccessCounts(&metadata)
	want := map[string]int{"scripted": 4}
	if !reflect.DeepEqual(metadata.SuccessfulRuns, want) {
		t.Fatalf("successful_runs = %#v, want %#v", metadata.SuccessfulRuns, want)
	}
}

func TestNormalizeScriptedSuccessCountsDoesNotDoubleCountMirroredLegacyKeys(t *testing.T) {
	metadata := ScriptedMetadata{
		TotalRuns:      9,
		FailedRuns:     0,
		SuccessfulRuns: map[string]int{"agentic": 9, "scripted": 9},
	}
	normalizeScriptedSuccessCounts(&metadata)
	if got := metadata.SuccessfulRuns["scripted"]; got != 9 {
		t.Fatalf("scripted successes = %d, want 9", got)
	}
}

// The local 2 MiB receipt could not be read by the native agent. A truncated
// preview must preserve the actual last traceback rather than only its tail.
func TestScriptOutputPreviewKeepsCompleteFinalTraceback(t *testing.T) {
	traceback := "Traceback (most recent call last):\n" + strings.Repeat("  diagnostic frame\n", 600) + "RuntimeError: final failure\n"
	output := "first-line\n" + strings.Repeat("noise\n", 350000) + traceback
	preview := scriptOutputPreview(output, "/run/script.log")
	if len(preview) > scriptOutputHeadBytes+scriptTracebackBytes+512 || !strings.HasPrefix(preview, "first-line") || !strings.HasSuffix(preview, traceback) || !strings.Contains(preview, "/run/script.log") {
		t.Fatalf("lost traceback or unbounded preview: %d bytes", len(preview))
	}
}
