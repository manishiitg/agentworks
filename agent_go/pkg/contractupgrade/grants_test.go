package contractupgrade

import (
	"sync"
	"testing"
)

func TestConsumeRequiresAGrant(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	if Consume("session-a", "1.0.21") {
		t.Fatal("stamp allowed without any grant")
	}
	Mint("session-a", "1.0.21")
	if !Consume("session-a", "1.0.21") {
		t.Fatal("stamp refused inside its own granted turn")
	}
}

// The customer-login 2026-08-12 case: the scheduler adjudicated the turn, and
// ten minutes later the same session stamped from an unrelated Pulse turn.
func TestStampAfterAdjudicationIsRefused(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	Revoke("session-a")
	if Consume("session-a", "1.0.21") {
		t.Fatal("stamp allowed after the granting turn was adjudicated")
	}
}

func TestConsumeRejectsAVersionOtherThanTheOpenTarget(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	if Consume("session-a", "1.0.22") {
		t.Fatal("stamp allowed for a version the turn did not ask for")
	}
	if !Consume("session-a", "1.0.21") {
		t.Fatal("the granted version should still be spendable")
	}
}

func TestGrantIsOneShotPerVersion(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	if !Consume("session-a", "1.0.21") {
		t.Fatal("first stamp refused")
	}
	if Consume("session-a", "1.0.21") {
		t.Fatal("grant was spendable twice")
	}
}

// Only the pre-run preflight mints, and it opens one rung at a time, so a
// session holds at most one authorization. Minting again supersedes it.
func TestMintSupersedesThePreviousTarget(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	Mint("session-a", "1.0.22")
	if Consume("session-a", "1.0.21") {
		t.Fatal("a superseded target was still spendable")
	}
	if got := Granted("session-a"); got != "1.0.22" {
		t.Fatalf("Granted() = %q, want the current target", got)
	}
	if !Consume("session-a", "1.0.22") {
		t.Fatal("the current target should be spendable")
	}
}

func TestGrantsAreScopedToOneSession(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a"); Revoke("session-b") })

	Mint("session-a", "1.0.21")
	if Consume("session-b", "1.0.21") {
		t.Fatal("one session's grant authorized another session's stamp")
	}
}

func TestMintWithABlankTargetRevokes(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	Mint("session-a", "  ")
	if Consume("session-a", "1.0.21") {
		t.Fatal("a grant survived being re-minted with no usable target")
	}
}

func TestBlankIdentifiersAreRefused(t *testing.T) {
	Mint("", "1.0.21")
	if Consume("", "1.0.21") {
		t.Fatal("blank session ID authorized a stamp")
	}
	Mint("session-a", "1.0.21")
	t.Cleanup(func() { Revoke("session-a") })
	if Consume("session-a", "") {
		t.Fatal("blank version consumed a grant")
	}
}

// The store is reached from the scheduler goroutine and from tool executors
// serving concurrent sessions.
func TestConcurrentConsumeSpendsAGrantExactlyOnce(t *testing.T) {
	t.Cleanup(func() { Revoke("session-a") })

	Mint("session-a", "1.0.21")
	var wg sync.WaitGroup
	wins := make(chan bool, 16)
	for range 16 {
		wg.Go(func() {
			wins <- Consume("session-a", "1.0.21")
		})
	}
	wg.Wait()
	close(wins)
	won := 0
	for ok := range wins {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("grant spent %d times, want exactly 1", won)
	}
}
