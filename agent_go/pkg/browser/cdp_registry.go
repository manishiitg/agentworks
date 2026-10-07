package browser

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// CDP mode connects every workflow to the user's single real Chrome, so the
// headless SessionTracker (one Chrome per session, evictable) doesn't apply.
// This registry is the CDP-mode counterpart: it records which owners
// (workflow/agent sessions) are actively using each CDP port so that
// destructive operations (reset) and unbounded growth (tab creation) can be
// arbitrated across concurrent workflows instead of silently interfering —
// the failure mode where one workflow's reset killed the shared daemon and
// wiped every other workflow's tab state.

var (
	// MaxCDPTabsPerOwner caps how many labeled tabs a single workflow/agent
	// may keep in the shared Chrome. Overridden by MAX_CDP_TABS_PER_OWNER.
	MaxCDPTabsPerOwner = 4

	// cdpOwnerActiveWindow is how recently an owner must have issued a CDP
	// command to count as "active" when guarding destructive operations.
	cdpOwnerActiveWindow = 10 * time.Minute

	cdpOwnersMu sync.Mutex
	cdpOwners   = make(map[int]map[string]time.Time) // port → ownerID → lastUsed

	// Some diagnostics are daemon/session-wide rather than scoped to the
	// selected page. An owner lease prevents one workflow from replacing or
	// stopping another workflow's recording or capture.
	cdpExclusiveFeatureOwners = make(map[int]map[string]string) // port → feature → ownerID
)

func init() {
	if v, err := strconv.Atoi(os.Getenv("MAX_CDP_TABS_PER_OWNER")); err == nil && v > 0 {
		MaxCDPTabsPerOwner = v
		log.Printf("[CDP_REGISTRY] Per-owner tab limit set from env: %d", v)
	}
}

// touchCDPOwner records that an owner issued a command against a CDP port.
func touchCDPOwner(port int, ownerID string) {
	if ownerID == "" {
		return
	}
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	owners := cdpOwners[port]
	if owners == nil {
		owners = make(map[string]time.Time)
		cdpOwners[port] = owners
	}
	if _, exists := owners[ownerID]; !exists {
		log.Printf("[CDP_REGISTRY] New owner %q on CDP port %d (owners: %d)", ownerID, port, len(owners)+1)
	}
	owners[ownerID] = time.Now()
}

// removeCDPOwner drops an owner from a port's registry.
func removeCDPOwner(port int, ownerID string) {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	if owners := cdpOwners[port]; owners != nil {
		delete(owners, ownerID)
		if len(owners) == 0 {
			delete(cdpOwners, port)
		}
	}
	if features := cdpExclusiveFeatureOwners[port]; features != nil {
		for feature, owner := range features {
			if owner == ownerID {
				delete(features, feature)
			}
		}
		if len(features) == 0 {
			delete(cdpExclusiveFeatureOwners, port)
		}
	}
}

func claimCDPExclusiveFeature(port int, ownerID, feature string) (bool, error) {
	if ownerID == "" || feature == "" {
		return false, nil
	}
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	features := cdpExclusiveFeatureOwners[port]
	if features == nil {
		features = make(map[string]string)
		cdpExclusiveFeatureOwners[port] = features
	}
	if owner := features[feature]; owner != "" && owner != ownerID {
		return false, fmt.Errorf("cannot start %s on shared CDP port %d: another workflow (%s) owns the active capture. Wait for it to stop or use a different CDP browser/port", feature, port, owner)
	} else if owner == ownerID {
		return false, nil
	}
	features[feature] = ownerID
	return true, nil
}

func guardCDPExclusiveFeatureStop(port int, ownerID, feature string) error {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	if owner := cdpExclusiveFeatureOwners[port][feature]; owner != "" && owner != ownerID {
		return fmt.Errorf("cannot stop %s on shared CDP port %d: it is owned by another workflow (%s)", feature, port, owner)
	}
	return nil
}

func releaseCDPExclusiveFeature(port int, ownerID, feature string) {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	features := cdpExclusiveFeatureOwners[port]
	if features == nil {
		return
	}
	if ownerID == "" || features[feature] == ownerID {
		delete(features, feature)
	}
	if len(features) == 0 {
		delete(cdpExclusiveFeatureOwners, port)
	}
}

func clearCDPExclusiveFeaturesForPort(port int) {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	delete(cdpExclusiveFeatureOwners, port)
}

func activeCDPExclusiveFeatures(port int) []string {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	features := cdpExclusiveFeatureOwners[port]
	active := make([]string, 0, len(features))
	for feature, owner := range features {
		active = append(active, fmt.Sprintf("%s (%s)", feature, owner))
	}
	sort.Strings(active)
	return active
}

// guardCDPAutomaticRecovery is stricter than an explicit user-requested reset:
// a timeout should not silently destroy an in-progress recording/capture, even
// when it belongs to the workflow that observed the timeout.
func guardCDPAutomaticRecovery(port int, ownerID string) error {
	if active := activeCDPExclusiveFeatures(port); len(active) > 0 {
		return fmt.Errorf("shared CDP port %d has active diagnostic capture(s): %v", port, active)
	}
	return guardCDPReset(port, ownerID)
}

// otherActiveCDPOwners returns owners (excluding ownerID) that used the port
// within cdpOwnerActiveWindow. Stale entries are pruned as a side effect.
func otherActiveCDPOwners(port int, ownerID string) []string {
	cdpOwnersMu.Lock()
	defer cdpOwnersMu.Unlock()
	owners := cdpOwners[port]
	if owners == nil {
		return nil
	}
	active := otherActiveOwnersLocked(owners, ownerID, true)
	if len(owners) == 0 {
		delete(cdpOwners, port)
	}
	return active
}

// otherActiveOwnersLocked returns the owners in one shared browser's owner
// map (excluding ownerID) that issued a command within cdpOwnerActiveWindow,
// sorted. With prune, stale entries are deleted as a side effect. Shared by
// the CDP per-port registry and the managed-session per-conversation tab
// registry (session_tabs.go). Caller holds cdpOwnersMu.
func otherActiveOwnersLocked(owners map[string]time.Time, ownerID string, prune bool) []string {
	var active []string
	for owner, lastUsed := range owners {
		if time.Since(lastUsed) > cdpOwnerActiveWindow {
			if prune {
				delete(owners, owner)
			}
			continue
		}
		if owner != ownerID {
			active = append(active, owner)
		}
	}
	sort.Strings(active)
	return active
}

// guardCDPReset returns an error when a reset on the shared CDP browser would
// disrupt other active workflows. Reset kills the shared agent-browser daemon
// and clears every owner's tab selections on the port, so it is only allowed
// when the requesting owner is the sole active user.
func guardCDPReset(port int, ownerID string) error {
	others := otherActiveCDPOwners(port, ownerID)
	if len(others) == 0 {
		return nil
	}
	return fmt.Errorf(
		"refusing to reset shared CDP browser on port %d: %d other workflow(s) used it in the last %s (%v). "+
			"A reset kills the shared browser daemon and clears their tab state. "+
			"Instead, close your own tab with agent_browser(command=\"tab\", args=[\"--cdp\", \"<configured-endpoint>\", \"close\", \"<your-tab-label>\"]) "+
			"or create a fresh labeled tab with agent_browser(command=\"tab\", args=[\"--cdp\", \"<configured-endpoint>\", \"new\", \"--label\", \"<label>\", \"<url>\"]).",
		port, len(others), cdpOwnerActiveWindow, others)
}

// guardCDPTabCreation returns an error when an owner already has
// MaxCDPTabsPerOwner labeled tabs in the shared Chrome.
func guardCDPTabCreation(port int, ownerID string) error {
	// A caller with no resolvable per-workflow identity gets a fresh,
	// never-before-seen ownerID on every call (cdpUnidentifiedOwnerID) so it
	// can never collide with a real workflow's count -- but that same
	// freshness means countCDPTabAliasesForOwner always reads 0 for it,
	// silently bypassing the limit entirely rather than merely miscounting
	// it (PLAT-322 review). Refuse outright instead: an unenforceable quota
	// must fail loud, not fail open.
	if isCDPUnidentifiedOwner(ownerID) {
		return fmt.Errorf(
			"cannot create a labeled tab in the shared CDP browser: no workflow/session identity could be resolved for this call, so tab ownership cannot be tracked or limited for it. " +
				"This indicates missing session context upstream (a bug to fix at the caller, not a condition to retry).")
	}
	count := countCDPTabAliasesForOwner(port, ownerID)
	if count < MaxCDPTabsPerOwner {
		return nil
	}
	return fmt.Errorf(
		"cannot create another tab in the shared CDP browser: this workflow already has %d labeled tab(s) (max %d). "+
			"Reuse an existing tab by label, or close one first with agent_browser(command=\"tab\", args=[\"--cdp\", \"<configured-endpoint>\", \"close\", \"<label>\"]).",
		count, MaxCDPTabsPerOwner)
}
