package tmuxcapture

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCaptureAgentTailUsesBuilderCompatibleTmuxCapture(t *testing.T) {
	var arguments []string
	capturer := Capturer{Run: func(_ context.Context, args ...string) (string, error) {
		arguments = append([]string(nil), args...)
		return "\x1b[31mfirst\x1b[0m   \n\n\n\n\x1b[32mlatest\x1b[0m\n\n", nil
	}}
	snapshot, err := capturer.CaptureAgentTail(context.Background(), "builder-pane", Options{})
	if err != nil {
		t.Fatalf("CaptureAgentTail() error = %v", err)
	}
	wantArgs := "capture-pane -p -e -J -t builder-pane -S -80"
	if got := strings.Join(arguments, " "); got != wantArgs {
		t.Fatalf("tmux args = %q, want %q", got, wantArgs)
	}
	if snapshot.Text != "first\n\n\nlatest" {
		t.Fatalf("snapshot text = %q", snapshot.Text)
	}
	if !strings.Contains(snapshot.PaneContent, "\x1b[31mfirst\x1b[0m") {
		t.Fatalf("pane content lost ANSI styling: %q", snapshot.PaneContent)
	}
	if snapshot.Truncated || snapshot.CaptureLines != DefaultAgentTailLines || snapshot.CapturedAt.IsZero() {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
}

func TestCaptureAgentTailBoundsLinesAndUTF8Text(t *testing.T) {
	capturer := Capturer{Run: func(_ context.Context, _ ...string) (string, error) {
		return "old line\n" + strings.Repeat("界", 100), nil
	}}
	snapshot, err := capturer.CaptureAgentTail(context.Background(), "pane", Options{
		Lines:    MaxCaptureLines + 1,
		MaxBytes: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CaptureLines != MaxCaptureLines || !snapshot.Truncated {
		t.Fatalf("snapshot bounds = %#v", snapshot)
	}
	if !utf8.ValidString(snapshot.Text) || !strings.HasPrefix(snapshot.Text, "[terminal output truncated;") {
		t.Fatalf("bounded text = %q", snapshot.Text)
	}
}

func TestCaptureAgentTailRejectsMissingSessionAndWrapsRunnerError(t *testing.T) {
	if _, err := (Capturer{}).CaptureAgentTail(context.Background(), "", Options{}); err == nil {
		t.Fatal("missing session error = nil")
	}
	capturer := Capturer{Run: func(_ context.Context, _ ...string) (string, error) {
		return "", errors.New("session disappeared")
	}}
	if _, err := capturer.CaptureAgentTail(context.Background(), "pane", Options{}); err == nil || !strings.Contains(err.Error(), "session disappeared") {
		t.Fatalf("runner error = %v", err)
	}
}

func TestCollapseBlankRunsPreservesPromptBoxAndPrunesSpinnerFragments(t *testing.T) {
	input := strings.Join([]string{
		"real output",
		"oading",
		"king..",
		"────────────────────────",
		"›",
		"────────────────────────",
		"cursor artifact",
	}, "\n")
	got := CollapseBlankRuns(input)
	if strings.Contains(got, "oading") || strings.Contains(got, "king") {
		t.Fatalf("spinner fragments survived: %q", got)
	}
	for _, want := range []string{"real output", "›", "cursor artifact"} {
		if !strings.Contains(got, want) {
			t.Fatalf("CollapseBlankRuns() removed %q: %q", want, got)
		}
	}
}
