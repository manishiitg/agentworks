package server

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// #3617: a missed or failed Crew workflow run shows one owner-only card per workflow and kind. Misses of the same
// workflow join one card, a new failure replaces the older one, and a finished run clears the failure card.
func TestRunProblemCardsMergeReplaceAndStayWithTheOwner(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	store := &runProblemStore{}
	job := productScheduleJob{UserID: "owner", CrewOwner: "owner", ProjectID: "crew-1",
		Schedule: productschedule.Schedule{ID: "daily", Name: "Daily report", WorkflowID: "wf-1"}}
	noKeepAwake := func(string) string { return "" }
	nine := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

	store.noteMissed(job, nine)
	store.noteMissed(job, nine) // the same slot twice is one miss
	store.noteMissed(job, nine.Add(24*time.Hour))
	store.noteOutcome(job, "failed", "run-1", "sign-in expired\nmore detail")
	store.noteOutcome(job, "failed", "run-2", "the site did not answer")

	cards := store.cardsFor("crew-1", "owner", noKeepAwake)
	if len(cards) != 2 {
		t.Fatalf("want one missed and one failed card, got %+v", cards)
	}
	var missed, failed runProblemCard
	for _, c := range cards {
		if c.Kind == runProblemMissed {
			missed = c
		} else {
			failed = c
		}
	}
	if !strings.Contains(missed.Line, "2 times") || missed.Buttons[0] != "Run now" || missed.Buttons[len(missed.Buttons)-1] != "Dismiss" {
		t.Fatalf("misses did not join one card: %+v", missed)
	}
	if !strings.Contains(failed.Line, "did not answer") || strings.Contains(failed.Line, "sign-in") {
		t.Fatalf("a new failure must replace the older one: %+v", failed)
	}
	if got := store.cardsFor("crew-1", "teammate", noKeepAwake); len(got) != 0 {
		t.Fatalf("another person saw the owner's run cards: %+v", got)
	}
	if got := store.cardsFor("crew-2", "owner", noKeepAwake); len(got) != 0 {
		t.Fatalf("another Crew's cards leaked in: %+v", got)
	}

	store.noteOutcome(job, "completed", "run-3", "")
	if got := store.cardsFor("crew-1", "owner", noKeepAwake); len(got) != 1 || got[0].Kind != runProblemMissed {
		t.Fatalf("a finished run must clear only the failure card: %+v", got)
	}
	if err := store.dismiss("crew-1", runProblemMissed+":daily"); err != nil {
		t.Fatal(err)
	}
	if got := store.cardsFor("crew-1", "owner", noKeepAwake); len(got) != 0 {
		t.Fatalf("Dismiss did not clear the card: %+v", got)
	}
}
