package llmtypes

import (
	"strings"
	"testing"
)

// A TUI draws a markdown table with box-drawing bars rather than echoing the
// pipes the model wrote. Each row contributes one glyph per column boundary, so
// a table of any length buries the word ratio and the clean markdown gets
// discarded in favour of the rendered one -- the single biggest source of
// unreadable stored answers on tmux transports.
//
// Shaped after the real cursor-agent reply that exposed this: a schedules
// table whose pane held 42 box-drawing bars, scoring 0.826 against the 0.90
// threshold.
func TestReconcileFinalAnswerKeepsMarkdownTables(t *testing.T) {
	var pane, transcript strings.Builder
	pane.WriteString("You have 31 schedules total — 20 enabled, 11 disabled.\n\nOn now (enabled)\n\n")
	pane.WriteString("Schedule                      │ When                   │ Area\n")
	transcript.WriteString("You have **31 schedules** total — **20 enabled**, **11 disabled**.\n\n### On now (enabled)\n\n")
	transcript.WriteString("| Schedule | When | Area |\n|---|---|---|\n")
	for _, row := range [][3]string{
		{"Social media daily execution", "10:00 / 15:00 / 20:00 IST", "Social media"},
		{"Auto-enrich memory", "Every 3 hours (UTC)", "System"},
		{"LinkedIn Engage", "10:00 UTC daily", "LinkedIn"},
		{"Inbox triage", "Every 30 minutes", "Email"},
		{"Weekly report", "Monday 09:00 IST", "Reporting"},
	} {
		pane.WriteString(row[0] + "  │ " + row[1] + " │ " + row[2] + "\n")
		transcript.WriteString("| " + row[0] + " | " + row[1] + " | " + row[2] + " |\n")
	}

	want := strings.TrimSpace(transcript.String())
	got := ReconcileFinalAnswer(pane.String(), transcript.String())
	if got != want {
		t.Errorf("reconcile picked the rendered pane over the clean markdown table.\n"+
			"sameWords = %v\n--- got ---\n%s", sameWords(pane.String(), transcript.String()), got)
	}
}

// The guard this function exists for must survive the normalisation: a
// genuinely different turn's reply, table or not, must still lose to the pane.
func TestReconcileFinalAnswerStillRejectsADifferentTurn(t *testing.T) {
	pane := "| Schedule | When |\n| Nightly backup | 02:00 |\nThat is the full backup schedule."
	other := "| Item | Price |\n| Widget | $4.00 |\nHere is the pricing table you asked for."

	if got := ReconcileFinalAnswer(pane, other); got != pane {
		t.Errorf("a different turn's reply was accepted; got:\n%s", got)
	}
}

// Normalisation is for COMPARISON only -- whichever candidate wins must be
// returned byte-for-byte, box-drawing and all.
func TestReconcileFinalAnswerReturnsCandidatesVerbatim(t *testing.T) {
	pane := "Totals │ 42\nDone."
	if got := ReconcileFinalAnswer(pane, ""); got != pane {
		t.Errorf("pane was rewritten: %q", got)
	}
	transcript := "| Totals | 42 |\n\nDone."
	if got := ReconcileFinalAnswer("", transcript); got != transcript {
		t.Errorf("transcript was rewritten: %q", got)
	}
}
