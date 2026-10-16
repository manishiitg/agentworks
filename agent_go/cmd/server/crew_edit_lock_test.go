package server

import (
	"strings"
	"testing"
	"time"
)

// The Crew edit lock (owner, 2026-10-16): one owner at a time; memory and run output are free; it frees when the
// holder's chat ends or after the window without a change.
func TestCrewEditLockOneOwnerAtATime(t *testing.T) {
	t.Setenv("AGENTWORKS_CREW_LOCK_MINUTES", "2")
	const root = "Crew/lock-demo-12345678"
	t.Cleanup(func() { crewEditLockRelease(root, "y"); crewEditLockRelease(root, "x") })
	now := time.Now()
	if msg := crewEditLockTake(root, "y", "s-y", now); msg != "" {
		t.Fatalf("first owner refused: %q", msg)
	}
	msg := crewEditLockTake(root, "x", "", now.Add(time.Minute))
	if !strings.Contains(msg, "is editing right now") || !strings.Contains(msg, "2 minutes of no changes") {
		t.Fatalf("second owner not told who has it: %q", msg)
	}
	// The holder's own change keeps the lock and moves the expiry.
	if msg := crewEditLockTake(root, "y", "", now.Add(90*time.Second)); msg != "" {
		t.Fatalf("holder refused: %q", msg)
	}
	if got := crewEditLockStatus(root, now.Add(2*time.Minute)); !got.Held {
		t.Fatal("a change by the holder did not move the expiry")
	}
	if !crewEditLockExempt("memory/notes.md") || !crewEditLockExempt("runs/1/out.json") || crewEditLockExempt("skills/a/SKILL.md") || crewEditLockExempt("") {
		t.Fatal("memory and run output are free; skills and settings are locked")
	}
	// The holder's chat ends: free at once.
	crewEditLockReleaseSession("s-y")
	if msg := crewEditLockTake(root, "x", "", now.Add(2*time.Minute)); msg != "" {
		t.Fatalf("lock held after the chat ended: %q", msg)
	}
	// No change for the window: free.
	if msg := crewEditLockTake(root, "y", "", now.Add(10*time.Minute)); msg != "" {
		t.Fatalf("lock held past the window: %q", msg)
	}
}
