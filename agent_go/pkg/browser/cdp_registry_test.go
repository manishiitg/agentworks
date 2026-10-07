package browser

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func resetCDPRegistryForTest(t *testing.T) {
	t.Helper()
	resetCDPTabCleanupForTest()
	cdpOwnersMu.Lock()
	cdpOwners = make(map[int]map[string]time.Time)
	cdpExclusiveFeatureOwners = make(map[int]map[string]string)
	cdpOwnersMu.Unlock()
	cdpTabSelectionsMu.Lock()
	cdpTabSelections = make(map[string]string)
	cdpActiveTabs = make(map[int]string)
	cdpTabAliases = make(map[string]string)
	cdpOwnedTabs = make(map[string]cdpOwnedTab)
	cdpRecordingTabs = make(map[string]cdpRecordingHandoff)
	cdpTabSelectionsMu.Unlock()
	t.Cleanup(func() {
		resetCDPTabCleanupForTest()
		cdpOwnersMu.Lock()
		cdpOwners = make(map[int]map[string]time.Time)
		cdpExclusiveFeatureOwners = make(map[int]map[string]string)
		cdpOwnersMu.Unlock()
		cdpTabSelectionsMu.Lock()
		cdpTabSelections = make(map[string]string)
		cdpActiveTabs = make(map[int]string)
		cdpTabAliases = make(map[string]string)
		cdpOwnedTabs = make(map[string]cdpOwnedTab)
		cdpRecordingTabs = make(map[string]cdpRecordingHandoff)
		cdpTabSelectionsMu.Unlock()
	})
}

func TestCDPExclusiveFeatureOwnership(t *testing.T) {
	resetCDPRegistryForTest(t)
	if claimed, err := claimCDPExclusiveFeature(9222, "workflow-a", "video recording"); err != nil || !claimed {
		t.Fatalf("first owner should claim recording: %v", err)
	}
	if _, err := claimCDPExclusiveFeature(9222, "workflow-b", "video recording"); err == nil || !strings.Contains(err.Error(), "workflow-a") {
		t.Fatalf("second owner should be blocked, got: %v", err)
	}
	if err := guardCDPExclusiveFeatureStop(9222, "workflow-b", "video recording"); err == nil {
		t.Fatal("another workflow must not stop the active recording")
	}
	if err := guardCDPExclusiveFeatureStop(9222, "workflow-a", "video recording"); err != nil {
		t.Fatalf("recording owner should be allowed to stop: %v", err)
	}
	releaseCDPExclusiveFeature(9222, "workflow-a", "video recording")
	if claimed, err := claimCDPExclusiveFeature(9222, "workflow-b", "video recording"); err != nil || !claimed {
		t.Fatalf("released feature should be claimable: %v", err)
	}
	if err := guardCDPAutomaticRecovery(9222, "workflow-b"); err == nil || !strings.Contains(err.Error(), "video recording") {
		t.Fatalf("automatic recovery must not destroy an active recording, got: %v", err)
	}
}

func TestGuardCDPResetSoleOwnerAllowed(t *testing.T) {
	resetCDPRegistryForTest(t)
	touchCDPOwner(9222, "workflow-a")
	if err := guardCDPReset(9222, "workflow-a"); err != nil {
		t.Fatalf("sole owner should be allowed to reset, got: %v", err)
	}
}

func TestGuardCDPResetBlockedByOtherActiveOwner(t *testing.T) {
	resetCDPRegistryForTest(t)
	touchCDPOwner(9222, "workflow-a")
	touchCDPOwner(9222, "workflow-b")
	err := guardCDPReset(9222, "workflow-a")
	if err == nil {
		t.Fatal("reset should be refused while another workflow is active on the port")
	}
	if !strings.Contains(err.Error(), "workflow-b") {
		t.Fatalf("error should name the other active owner, got: %v", err)
	}
}

func TestGuardCDPResetIgnoresStaleOwners(t *testing.T) {
	resetCDPRegistryForTest(t)
	touchCDPOwner(9222, "workflow-a")
	touchCDPOwner(9222, "workflow-stale")
	cdpOwnersMu.Lock()
	cdpOwners[9222]["workflow-stale"] = time.Now().Add(-cdpOwnerActiveWindow - time.Minute)
	cdpOwnersMu.Unlock()
	if err := guardCDPReset(9222, "workflow-a"); err != nil {
		t.Fatalf("stale owners should not block reset, got: %v", err)
	}
	// Stale entry should have been pruned.
	if others := otherActiveCDPOwners(9222, "workflow-a"); len(others) != 0 {
		t.Fatalf("expected stale owner pruned, got: %v", others)
	}
}

func TestGuardCDPResetIsolatedPerPort(t *testing.T) {
	resetCDPRegistryForTest(t)
	touchCDPOwner(9222, "workflow-a")
	touchCDPOwner(9223, "workflow-b")
	if err := guardCDPReset(9222, "workflow-a"); err != nil {
		t.Fatalf("owner on a different port should not block reset, got: %v", err)
	}
}

func TestRemoveCDPOwner(t *testing.T) {
	resetCDPRegistryForTest(t)
	touchCDPOwner(9222, "workflow-a")
	touchCDPOwner(9222, "workflow-b")
	removeCDPOwner(9222, "workflow-b")
	if err := guardCDPReset(9222, "workflow-a"); err != nil {
		t.Fatalf("removed owner should not block reset, got: %v", err)
	}
}

func TestGuardCDPTabCreationEnforcesLimit(t *testing.T) {
	resetCDPRegistryForTest(t)
	cdpTabSelectionsMu.Lock()
	cdpTabAliases = make(map[string]string)
	cdpTabSelectionsMu.Unlock()
	t.Cleanup(func() {
		cdpTabSelectionsMu.Lock()
		cdpTabAliases = make(map[string]string)
		cdpTabSelectionsMu.Unlock()
	})

	for i := 0; i < MaxCDPTabsPerOwner; i++ {
		setCDPTabAlias(9222, "workflow-a", fmt.Sprintf("label-%d", i), fmt.Sprintf("t%d", i))
	}
	err := guardCDPTabCreation(9222, "workflow-a")
	if err == nil {
		t.Fatalf("tab creation should be blocked at the per-owner limit (%d)", MaxCDPTabsPerOwner)
	}
	if !strings.Contains(err.Error(), "close") {
		t.Fatalf("error should tell the agent how to recover, got: %v", err)
	}

	// A different owner on the same port is unaffected.
	if err := guardCDPTabCreation(9222, "workflow-b"); err != nil {
		t.Fatalf("other owner should not be limited, got: %v", err)
	}
	// Closing a tab (alias cleared) frees a slot.
	clearCDPTabAlias(9222, "workflow-a", "label-0")
	if err := guardCDPTabCreation(9222, "workflow-a"); err != nil {
		t.Fatalf("freeing a tab slot should allow creation again, got: %v", err)
	}
}

// PLAT-322 review. cdpUnidentifiedOwnerID returns a fresh, never-before-seen
// value on every call so it never collides with a real workflow's count --
// but that same freshness means countCDPTabAliasesForOwner always reads 0
// for it, which would silently bypass the limit entirely (unlimited tab
// creation) rather than merely miscount it, if guardCDPTabCreation trusted
// it as a normal owner. It must refuse outright instead.
func TestGuardCDPTabCreationRejectsUnidentifiedOwnerInsteadOfBypassingTheLimit(t *testing.T) {
	resetCDPRegistryForTest(t)

	for i := 0; i < MaxCDPTabsPerOwner*3; i++ {
		owner := cdpUnidentifiedOwnerID()
		if err := guardCDPTabCreation(9222, owner); err == nil {
			t.Fatalf("call %d: an unidentified owner must be rejected, not silently allowed past the %d-tab limit", i, MaxCDPTabsPerOwner)
		}
	}
}
