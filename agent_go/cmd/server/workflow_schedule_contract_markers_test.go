package server

import (
	"strings"
	"testing"
)

// The markers exist to spot SQL and shell embedded in a schedule message. As
// bare substrings they also matched ordinary English, which made legitimate
// schedules unsavable: customer-login's reconciliation schedule was rejected for
// "contains procedure marker \"update \"" over a sentence about updating issue
// status.
func TestScheduleProseIsNotMistakenForProcedure(t *testing.T) {
	prose := []string{
		"Update the GitHub issue status once the run finishes.",
		"Select the customer-staging group and wait for it to complete.",
		"Insert a short summary into the run notes.",
		"Delete nothing; leave prior results in place.",
		// "git " is a substring of "digit ".
		"Report the four-digit reference for each failed check.",
		// "step 1" mid-sentence is not a numbered procedure.
		"Re-run the failing step 1 more time before reporting it.",
	}
	for _, message := range prose {
		if needs, signal := scheduleMessagesNeedExplicitReason([]string{message}); needs {
			t.Errorf("prose flagged as procedure (%s): %q", signal, message)
		}
	}
}

func TestScheduleProcedureMarkersStillCatchEmbeddedCode(t *testing.T) {
	cases := map[string]string{
		"sql at line start":    "Collect the results.\nSELECT run_id, passed FROM smoke_run_summary;",
		"shell at line start":  "Back up the workspace.\ngit add -A && git commit -m 'backup'",
		"numbered procedure":   "Step 1: open the dashboard.\nStep 2: export the CSV.",
		"numbered list marker": "1. update the plan\n2. push the branch",
		"sqlite anywhere":      "Run sqlite3 db/db.sqlite to check the totals.",
		"curl anywhere":        "Then curl the staging health endpoint.",
	}
	for name, message := range cases {
		if needs, _ := scheduleMessagesNeedExplicitReason([]string{message}); !needs {
			t.Errorf("%s: embedded procedure not detected in %q", name, message)
		}
	}
}

// A rejected save has to name what tripped it, or the agent cannot tell which
// sentence to change.
func TestScheduleRejectionNamesTheOffendingMessage(t *testing.T) {
	err := validateScheduleMessages([]string{"Run the nightly check.", "SELECT * FROM runs;"}, "")
	if err == nil {
		t.Fatal("embedded SQL should require an explicit reason")
	}
	if !strings.Contains(err.Error(), "messages[1]") || !strings.Contains(err.Error(), "SELECT ... FROM") {
		t.Errorf("rejection does not identify the offending message: %v", err)
	}
	if err := validateScheduleMessages([]string{"Run the nightly check.", "SELECT * FROM runs;"}, "kept deliberately: ad-hoc query owned by this schedule"); err != nil {
		t.Errorf("an explicit reason should satisfy the contract: %v", err)
	}
}
