package step_based_workflow

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/contractupgrade"
)

func TestAppendContractUpgradeHistoryRecordsFirstAppliedTime(t *testing.T) {
	manifest := map[string]interface{}{}
	first := time.Date(2026, time.September, 22, 9, 30, 0, 0, time.FixedZone("IST", 5*60*60+30*60))
	if err := appendContractUpgradeHistory(manifest, "1.0.44", first); err != nil {
		t.Fatal(err)
	}
	if err := appendContractUpgradeHistory(manifest, "1.0.44", first.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	history, ok := manifest[contractUpgradeHistoryField].([]interface{})
	if !ok || len(history) != 1 {
		t.Fatalf("history = %#v", manifest[contractUpgradeHistoryField])
	}
	entry := history[0].(map[string]interface{})
	if got := entry["applied_at"]; got != "2026-09-22T04:00:00Z" {
		t.Fatalf("applied_at = %v", got)
	}
}

// scheduledSession claims a session for the scheduler, as a scheduled run does
// for its whole lifecycle. The stamp fence binds only claimed sessions.
func scheduledSession(t *testing.T, id string) {
	t.Helper()
	contractupgrade.MarkScheduled(id)
	t.Cleanup(func() { contractupgrade.ClearScheduled(id) })
}

func TestStampIsAllowedInsideItsOwnUpgradeTurn(t *testing.T) {
	scheduledSession(t, "schedule-session")

	contractupgrade.Mint("schedule-session", "1.0.21")
	refusal, ok := authorizeContractVersionStamp("schedule-session", "1.0.21", nil)
	if !ok {
		t.Fatalf("stamp refused inside its own turn: %s", refusal)
	}
	if refusal != "" {
		t.Fatalf("permitted stamp returned a refusal: %s", refusal)
	}
}

// The customer-login 2026-08-12 case: the scheduler adjudicated the 1.0.21 turn
// as failed at 08:01:35 and the same session stamped 1.0.21 at 08:11:10 from an
// unrelated Pulse turn, via a shell curl to the MCP bridge.
func TestStampFromAClosedTurnIsRefused(t *testing.T) {
	scheduledSession(t, "schedule-session")

	contractupgrade.Mint("schedule-session", "1.0.21")
	contractupgrade.Revoke("schedule-session") // scheduler adjudicates the turn

	refusal, ok := authorizeContractVersionStamp("schedule-session", "1.0.21", nil)
	if ok {
		t.Fatal("a closed turn was allowed to stamp the version it had declined")
	}
	// The refusal has to tell the agent what actually resolves this, or it will
	// read as a platform fault and get retried or worked around.
	for _, want := range []string{"schedules may run", "never authorize or perform migrations", "Update workflow"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("refusal missing %q: %s", want, refusal)
		}
	}
}

// An operator working in the workflow builder is the authorization: they asked
// for the migration and can see what the agent does. Binding them too removed
// the only way a person could unblock a workflow that keeps declining its own
// upgrade — which is the state customer-login was left in.
func TestOperatorSessionMayStampTheNextPendingMigration(t *testing.T) {
	next := func() (string, string, error) { return "1.0.21", "upgrade-current-artifact-contract", nil }
	if _, ok := authorizeContractVersionStamp("workflow-builder-chat", "1.0.21", next); !ok {
		t.Fatal("an interactive builder session could not stamp, so a human has no way to unblock a stalled upgrade")
	}
}

// Without a scheduler grant pinning the target, nothing else stops a stamp
// jumping to the newest version and skipping the migrations in between. The
// version is the record that the work happened.
func TestOperatorSessionCannotSkipAheadInTheLadder(t *testing.T) {
	next := func() (string, string, error) { return "1.0.21", "upgrade-current-artifact-contract", nil }
	refusal, ok := authorizeContractVersionStamp("workflow-builder-chat", "1.0.25", next)
	if ok {
		t.Fatal("an operator stamp skipped three migrations")
	}
	for _, want := range []string{"1.0.21", "1.0.25", "one at a time", "without its work being performed"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("refusal missing %q: %s", want, refusal)
		}
	}
}

func TestOperatorStampRefusedWhenNothingIsOwed(t *testing.T) {
	next := func() (string, string, error) { return "", "", nil }
	refusal, ok := authorizeContractVersionStamp("workflow-builder-chat", "1.0.25", next)
	if ok {
		t.Fatal("stamped a version on a workflow that owes no migration")
	}
	if !strings.Contains(refusal, "owes no contract migration") {
		t.Errorf("unexpected refusal: %s", refusal)
	}
}

// A lookup failure is not evidence of a bad stamp, and this is the operator's
// only manual route.
func TestOperatorStampSurvivesALadderLookupFailure(t *testing.T) {
	next := func() (string, string, error) { return "", "", errors.New("workspace api down") }
	if _, ok := authorizeContractVersionStamp("workflow-builder-chat", "1.0.21", next); !ok {
		t.Fatal("a ladder lookup failure blocked the operator's manual upgrade")
	}
}

func TestStampOfAnotherVersionNamesWhatThisTurnMayStamp(t *testing.T) {
	scheduledSession(t, "schedule-session")

	contractupgrade.Mint("schedule-session", "1.0.21")
	refusal, ok := authorizeContractVersionStamp("schedule-session", "1.0.25", nil)
	if ok {
		t.Fatal("a turn stamped a version other than its own target")
	}
	if !strings.Contains(refusal, "1.0.21") || !strings.Contains(refusal, "1.0.25") {
		t.Fatalf("refusal should name both the granted and the attempted version: %s", refusal)
	}
}

func TestStampIsSpentOnce(t *testing.T) {
	scheduledSession(t, "schedule-session")

	contractupgrade.Mint("schedule-session", "1.0.21")
	if _, ok := authorizeContractVersionStamp("schedule-session", "1.0.21", nil); !ok {
		t.Fatal("first stamp refused")
	}
	if _, ok := authorizeContractVersionStamp("schedule-session", "1.0.21", nil); ok {
		t.Fatal("the same authorization was spent twice")
	}
}

// Releasing a session at the end of a scheduled run must also drop any unspent
// grant, so nothing outlives the run holding an authorization.
func TestClearingASessionDropsItsGrant(t *testing.T) {
	contractupgrade.MarkScheduled("schedule-session")
	contractupgrade.Mint("schedule-session", "1.0.21")
	contractupgrade.ClearScheduled("schedule-session")
	contractupgrade.MarkScheduled("schedule-session")
	t.Cleanup(func() { contractupgrade.ClearScheduled("schedule-session") })

	if _, ok := authorizeContractVersionStamp("schedule-session", "1.0.21", nil); ok {
		t.Fatal("a grant survived the end of its scheduled run")
	}
}

func TestNestedAgentArtifactStampRequiresCodeLayoutOne(t *testing.T) {
	for name, manifest := range map[string]map[string]interface{}{
		"missing": {},
		"legacy":  {"code_layout_version": float64(0)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateContractVersionStampPrerequisites(NestedAgentArtifactsContractVersion, manifest); err == nil {
				t.Fatal("nested artifact contract was stampable without code_layout_version=1")
			}
		})
	}
	if err := validateContractVersionStampPrerequisites(NestedAgentArtifactsContractVersion, map[string]interface{}{"code_layout_version": float64(1)}); err != nil {
		t.Fatalf("code layout 1 rejected: %v", err)
	}
	if err := validateContractVersionStampPrerequisites("1.0.43", map[string]interface{}{}); err != nil {
		t.Fatalf("unrelated historical stamp gained a new prerequisite: %v", err)
	}
}
