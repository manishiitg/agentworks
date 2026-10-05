package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

var (
	sharedCDPLocks sync.Map // map[string]*sync.Mutex

	cdpTabSelectionsMu sync.RWMutex
	cdpTabSelections   = make(map[string]string)
	cdpActiveTabs      = make(map[int]string)
	cdpTabAliases      = make(map[string]string)
	cdpOwnedTabs       = make(map[string]cdpOwnedTab)
	cdpRecordingTabs   = make(map[string]cdpRecordingHandoff)
)

type cdpOwnedTab struct {
	Alias string
	TabID string
}

// cdpRecordingHandoff tracks agent-browser's temporary recording context.
// agent-browser cannot add video capture to an existing BrowserContext, so
// `record start` creates a new page (and usually a visible Chrome window). The
// managed adapter must route the owner to that page until recording stops;
// otherwise its normal selected-tab enforcement silently sends actions back to
// the unrecorded original tab.
type cdpRecordingHandoff struct {
	OriginalTab   string
	RecordingTab  string
	NeedsSnapshot bool
}

type newCDPTabRequest struct {
	Label string
	URL   string
}

func sharedCDPSessionName(port int) string {
	return common.PrefixBrowserSessionID(fmt.Sprintf("shared-cdp-%d", port))
}

// cdpSharedConnectionIdentityFor returns the value cdpOwnerID must never
// resolve an owner to, for the given call's mode/port -- see cdpOwnerID's
// own doc comment. Non-CDP callers get "", since `session` is a genuine
// identity for them, not a shared connection name.
func cdpSharedConnectionIdentityFor(isCdpMode bool, port int) string {
	if !isCdpMode {
		return ""
	}
	return sharedCDPSessionName(port)
}

func sharedCDPLock(port int) *sync.Mutex {
	key := fmt.Sprintf("cdp:%d", port)
	actual, _ := sharedCDPLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

func acquireSharedCDPLock(ctx context.Context, port int) (func(), error) {
	local := sharedCDPLock(port)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for !local.TryLock() {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for local CDP lock on port %d: %w", port, ctx.Err())
		case <-ticker.C:
		}
	}

	lockPath := sharedCDPFileLockPath(port)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		local.Unlock()
		return nil, fmt.Errorf("open shared CDP lock %s: %w", lockPath, err)
	}

	for {
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			lockFile.Close()
			local.Unlock()
			return nil, fmt.Errorf("acquire shared CDP lock %s: %w", lockPath, err)
		}
		select {
		case <-ctx.Done():
			lockFile.Close()
			local.Unlock()
			return nil, fmt.Errorf("timed out waiting for shared CDP lock on port %d: %w", port, ctx.Err())
		case <-ticker.C:
		}
	}

	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		local.Unlock()
	}, nil
}

func sharedCDPFileLockPath(port int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("mcp-agent-builder-cdp-%d.lock", port))
}

// tabScope names one browser whose tabs are shared by several owners
// (workflows or conversations). The same per-owner tab process -- selection,
// labels/aliases, ownership records -- serves both the operator's shared CDP
// Chrome (scope "cdp:<port>") and a managed headless agent-browser session
// shared by a Crew's or workflow's conversations (scope "session:<name>").
// Every key is "<scope>:<owner>[:<alias>]", so CDP keys stay exactly
// "cdp:<port>:<owner>[:<alias>]" as before the generalisation.
type tabScope string

func cdpTabScope(port int) tabScope {
	return tabScope(fmt.Sprintf("cdp:%d", port))
}

func (s tabScope) ownerKey(ownerID string) string {
	return string(s) + ":" + strings.TrimSpace(ownerID)
}

func (s tabScope) aliasKey(ownerID, alias string) string {
	return s.ownerKey(ownerID) + ":" + strings.TrimSpace(alias)
}

func (s tabScope) ownerPrefix(ownerID string) string {
	return s.ownerKey(ownerID) + ":"
}

func (s tabScope) prefix() string {
	return string(s) + ":"
}

func cdpTabSelectionKey(port int, ownerID string) string {
	return cdpTabScope(port).ownerKey(ownerID)
}

func cdpTabAliasKey(port int, ownerID, alias string) string {
	return cdpTabScope(port).aliasKey(ownerID, alias)
}

func getScopedTabSelection(scope tabScope, ownerID string) string {
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	return cdpTabSelections[scope.ownerKey(ownerID)]
}

func setScopedTabSelection(scope tabScope, ownerID, tab string) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	cdpTabSelections[scope.ownerKey(ownerID)] = tab
}

func clearScopedTabSelection(scope tabScope, ownerID string) {
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	delete(cdpTabSelections, scope.ownerKey(ownerID))
}

func getCDPTabSelection(port int, ownerID string) string {
	return getScopedTabSelection(cdpTabScope(port), ownerID)
}

func setCDPTabSelection(port int, ownerID, tab string) {
	setScopedTabSelection(cdpTabScope(port), ownerID, tab)
}

func clearCDPTabSelection(port int, ownerID string) {
	clearScopedTabSelection(cdpTabScope(port), ownerID)
}

// clearScopedTabsLocked drops every owner's selection, alias, ownership and
// recording record in one scope. Caller holds cdpTabSelectionsMu.
func clearScopedTabsLocked(scope tabScope) {
	prefix := scope.prefix()
	for key := range cdpTabSelections {
		if strings.HasPrefix(key, prefix) {
			delete(cdpTabSelections, key)
		}
	}
	for key := range cdpTabAliases {
		if strings.HasPrefix(key, prefix) {
			delete(cdpTabAliases, key)
		}
	}
	for key := range cdpOwnedTabs {
		if strings.HasPrefix(key, prefix) {
			delete(cdpOwnedTabs, key)
		}
	}
	for key := range cdpRecordingTabs {
		if strings.HasPrefix(key, prefix) {
			delete(cdpRecordingTabs, key)
		}
	}
}

func clearCDPTabSelectionsForPort(port int) {
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	clearScopedTabsLocked(cdpTabScope(port))
	delete(cdpActiveTabs, port)
}

func getCDPRecordingHandoff(port int, ownerID string) (cdpRecordingHandoff, bool) {
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	handoff, ok := cdpRecordingTabs[cdpTabSelectionKey(port, ownerID)]
	return handoff, ok
}

func setCDPRecordingHandoff(port int, ownerID string, handoff cdpRecordingHandoff) {
	handoff.OriginalTab = strings.TrimSpace(handoff.OriginalTab)
	handoff.RecordingTab = strings.TrimSpace(handoff.RecordingTab)
	if handoff.OriginalTab == "" || handoff.RecordingTab == "" {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	cdpRecordingTabs[cdpTabSelectionKey(port, ownerID)] = handoff
	cdpTabSelections[cdpTabSelectionKey(port, ownerID)] = handoff.RecordingTab
	cdpActiveTabs[port] = handoff.RecordingTab
}

func markCDPRecordingSnapshotReady(port int, ownerID string) {
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	key := cdpTabSelectionKey(port, ownerID)
	handoff, ok := cdpRecordingTabs[key]
	if !ok {
		return
	}
	handoff.NeedsSnapshot = false
	cdpRecordingTabs[key] = handoff
}

func clearCDPRecordingHandoff(port int, ownerID string) (cdpRecordingHandoff, bool) {
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	key := cdpTabSelectionKey(port, ownerID)
	handoff, ok := cdpRecordingTabs[key]
	if !ok {
		return cdpRecordingHandoff{}, false
	}
	delete(cdpRecordingTabs, key)
	if handoff.OriginalTab != "" {
		cdpTabSelections[key] = handoff.OriginalTab
	}
	if cdpActiveTabs[port] == handoff.RecordingTab {
		delete(cdpActiveTabs, port)
	}
	return handoff, true
}

// findCDPRecordingTab identifies the fresh page created by agent-browser's
// record start/restart operation. Prefer a newly-created active page, then the
// only new page, then an active page different from the original. The final
// fallback supports agent-browser versions that replace a target while
// retaining their logical tN numbering.
func findCDPRecordingTab(before, after []cdpTabInfo, originalTab string) (string, error) {
	beforeIDs := make(map[string]bool, len(before))
	for _, tab := range before {
		if tabID := strings.TrimSpace(tab.TabID); tabID != "" {
			beforeIDs[tabID] = true
		}
	}

	var newTabs []cdpTabInfo
	for _, tab := range after {
		tabID := strings.TrimSpace(tab.TabID)
		if tabID == "" || beforeIDs[tabID] {
			continue
		}
		newTabs = append(newTabs, tab)
		if tab.Active {
			return tabID, nil
		}
	}
	if len(newTabs) == 1 {
		return strings.TrimSpace(newTabs[0].TabID), nil
	}
	for _, tab := range after {
		tabID := strings.TrimSpace(tab.TabID)
		if tab.Active && tabID != "" && tabID != strings.TrimSpace(originalTab) {
			return tabID, nil
		}
	}
	return "", fmt.Errorf("record start succeeded, but the fresh recording tab could not be identified (original=%q, before=%d tab(s), after=%d tab(s))", originalTab, len(before), len(after))
}

func clearCDPActiveTabForPort(port int) {
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	delete(cdpActiveTabs, port)
}

// countCDPTabAliasesForOwner counts labeled tabs an owner has created in the
// shared CDP browser. Aliases are recorded when a labeled tab is selected or
// created, and removed when the tab is closed, so this approximates the
// owner's live tab count.
func countCDPTabAliasesForOwner(port int, ownerID string) int {
	prefix := cdpTabScope(port).ownerPrefix(ownerID)
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	count := 0
	for key := range cdpTabAliases {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

func getScopedTabAlias(scope tabScope, ownerID, alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" || isCDPTabID(alias) {
		return ""
	}
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	return cdpTabAliases[scope.aliasKey(ownerID, alias)]
}

func getCDPTabAlias(port int, ownerID, alias string) string {
	return getScopedTabAlias(cdpTabScope(port), ownerID, alias)
}

func getCDPTabSelectionForPrompt(port int, ownerID string) string {
	tab := getCDPTabSelection(port, ownerID)
	if alias := getCDPTabAlias(port, ownerID, tab); alias != "" {
		return alias
	}
	return tab
}

func setScopedTabAlias(scope tabScope, ownerID, alias, tabID string) {
	alias = strings.TrimSpace(alias)
	tabID = strings.TrimSpace(tabID)
	if alias == "" || tabID == "" || alias == tabID || isCDPTabID(alias) {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	cdpTabAliases[scope.aliasKey(ownerID, alias)] = tabID
}

func setCDPTabAlias(port int, ownerID, alias, tabID string) {
	setScopedTabAlias(cdpTabScope(port), ownerID, alias, tabID)
}

func clearCDPTabAlias(port int, ownerID, alias string) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	key := cdpTabAliasKey(port, ownerID, alias)
	delete(cdpTabAliases, key)
	delete(cdpOwnedTabs, key)
}

func markScopedTabOwned(scope tabScope, ownerID, alias, tabID string) {
	ownerID = strings.TrimSpace(ownerID)
	alias = strings.TrimSpace(alias)
	tabID = strings.TrimSpace(tabID)
	if ownerID == "" || alias == "" {
		return
	}
	if tabID == "" {
		tabID = alias
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	cdpOwnedTabs[scope.aliasKey(ownerID, alias)] = cdpOwnedTab{Alias: alias, TabID: tabID}
}

func markCDPTabOwned(port int, ownerID, alias, tabID string) {
	markScopedTabOwned(cdpTabScope(port), ownerID, alias, tabID)
}

func ownedScopedTabsForOwner(scope tabScope, ownerID string) []cdpOwnedTab {
	prefix := scope.ownerPrefix(ownerID)
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	tabs := make([]cdpOwnedTab, 0)
	for key, tab := range cdpOwnedTabs {
		if strings.HasPrefix(key, prefix) {
			tabs = append(tabs, tab)
		}
	}
	return tabs
}

func ownedCDPTabsForOwner(port int, ownerID string) []cdpOwnedTab {
	return ownedScopedTabsForOwner(cdpTabScope(port), ownerID)
}

func isScopedTabOwnedByOwner(scope tabScope, ownerID, alias, tabID string) bool {
	alias = strings.TrimSpace(alias)
	tabID = strings.TrimSpace(tabID)
	for _, owned := range ownedScopedTabsForOwner(scope, ownerID) {
		if (alias != "" && owned.Alias == alias) || (tabID != "" && owned.TabID == tabID) {
			return true
		}
	}
	return false
}

func isCDPTabOwnedByOwner(port int, ownerID, alias, tabID string) bool {
	return isScopedTabOwnedByOwner(cdpTabScope(port), ownerID, alias, tabID)
}

// clearScopedTabStateLocked removes selection, alias and ownership state for
// one tab of one owner, identified by either its label or its tab ID. It
// returns every reference (label and tab ID) it cleared so a scope with an
// extra active-tab record (CDP) can drop that too. Caller holds
// cdpTabSelectionsMu.
func clearScopedTabStateLocked(scope tabScope, ownerID, tab string) []string {
	prefix := scope.ownerPrefix(ownerID)
	selectionKey := scope.ownerKey(ownerID)
	cleared := []string{tab}

	selected := cdpTabSelections[selectionKey]
	for key, tabID := range cdpTabAliases {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		alias := strings.TrimPrefix(key, prefix)
		if alias != tab && tabID != tab {
			continue
		}
		delete(cdpTabAliases, key)
		delete(cdpOwnedTabs, key)
		if selected == alias || selected == tabID {
			delete(cdpTabSelections, selectionKey)
		}
		cleared = append(cleared, alias, tabID)
	}
	// Recording contexts are registered for crash-safe delayed cleanup without
	// adding a public alias. Remove any such ownership record by its real tab ID
	// after record stop closes the temporary page.
	for key, owned := range cdpOwnedTabs {
		if strings.HasPrefix(key, prefix) && (owned.Alias == tab || owned.TabID == tab) {
			delete(cdpOwnedTabs, key)
		}
	}
	if selected == tab {
		delete(cdpTabSelections, selectionKey)
	}
	// A tab ID can be used as the alias when agent-browser did not return a
	// separate label mapping. Remove that direct ownership record as well.
	delete(cdpOwnedTabs, scope.aliasKey(ownerID, tab))
	return cleared
}

// clearCDPTabStateForOwner removes selection, alias, active-tab, and ownership
// state for one tab. The caller may identify the tab by either its workflow
// label or the agent-browser tab ID returned when it was created.
func clearCDPTabStateForOwner(port int, ownerID, tab string) {
	ownerID = strings.TrimSpace(ownerID)
	tab = strings.TrimSpace(tab)
	if ownerID == "" || tab == "" {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	for _, ref := range clearScopedTabStateLocked(cdpTabScope(port), ownerID, tab) {
		if cdpActiveTabs[port] == ref {
			delete(cdpActiveTabs, port)
		}
	}
}

func getCDPActiveTab(port int) string {
	cdpTabSelectionsMu.RLock()
	defer cdpTabSelectionsMu.RUnlock()
	return cdpActiveTabs[port]
}

func setCDPActiveTab(port int, tab string) {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return
	}
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	cdpActiveTabs[port] = tab
}

func isCDPTabActive(port int, ownerID, tab string) bool {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return false
	}

	activeTab := getCDPActiveTab(port)
	if activeTab == tab {
		return true
	}

	// Tab selections are usually supplied by agents as stable labels, while
	// agent-browser reports the active tab as its resolved t<N> id.
	return activeTab != "" && activeTab == getCDPTabAlias(port, ownerID, tab)
}

func clearCDPActiveTab(port int, tab string) {
	tab = strings.TrimSpace(tab)
	cdpTabSelectionsMu.Lock()
	defer cdpTabSelectionsMu.Unlock()
	if tab == "" || cdpActiveTabs[port] == tab {
		delete(cdpActiveTabs, port)
	}
}

// cdpOwnerID resolves the identity that a workflow/agent's CDP tabs and
// artifacts are tracked under. sharedConnectionIdentity is the value
// (typically sharedCDPSessionName(port)) that `session` gets deliberately
// overwritten to for the purpose of choosing which real Chrome to talk to,
// when the caller is in CDP mode -- pass "" for a non-CDP caller, where
// `session` is a genuine identity and safe to use as a fallback.
//
// session must never be returned as the resolved owner when it equals
// sharedConnectionIdentity: that string is shared by construction across
// every workflow using the same CDP port (see sharedCDPSessionName), so
// treating it as one workflow's owner identity silently pools every such
// workflow's tabs and cleanup leases under one key (PLAT-322).
func cdpOwnerID(workflowSessionID, agentSessionID, session, sharedConnectionIdentity string) string {
	for _, candidate := range []string{agentSessionID, workflowSessionID} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if resolved := common.ResolveBrowserSessionID(candidate, "default"); resolved != "" && resolved != "default" {
			return resolved
		}
	}
	sharedConnectionIdentity = strings.TrimSpace(sharedConnectionIdentity)
	for _, candidate := range []string{workflowSessionID, agentSessionID, session} {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if sharedConnectionIdentity != "" && trimmed == sharedConnectionIdentity {
			continue
		}
		return trimmed
	}
	return cdpUnidentifiedOwnerID()
}

// cdpUnidentifiedOwnerPrefix marks an owner value returned when no
// per-workflow identity could be resolved at all. Callers that enforce a
// per-owner quota (guardCDPTabCreation) must check isCDPUnidentifiedOwner
// and refuse the operation outright rather than trust this value as a real
// owner -- a fresh, never-before-seen key always reads as "zero tabs used,"
// so treating it as a normal owner silently bypasses the quota it exists to
// enforce (PLAT-322 review). Fixed diagnostics/cleanup paths that only
// observe usage (not gate it) may still record against this value; it is
// still unique per call, so it will not pool with a genuine workflow's
// count or with another unidentified call's count.
const cdpUnidentifiedOwnerPrefix = "unidentified-"

var cdpUnidentifiedOwnerCounter atomic.Int64

// cdpUnidentifiedOwnerID returns a fresh identity for a caller with no
// resolvable per-workflow session at all -- the old behavior returned the
// fixed literal "default" here, which collided across every such caller the
// same way the shared connection identity did (PLAT-322): two genuinely
// unidentified workflows would still pool their tabs under one key. A
// unique value per call means it never collides with anyone else's real
// count. It must not be treated as a normal, quota-eligible owner by any
// caller that enforces a limit -- see isCDPUnidentifiedOwner.
func cdpUnidentifiedOwnerID() string {
	return fmt.Sprintf("%s%d-%d", cdpUnidentifiedOwnerPrefix, time.Now().UnixNano(), cdpUnidentifiedOwnerCounter.Add(1))
}

// isCDPUnidentifiedOwner reports whether ownerID came from
// cdpUnidentifiedOwnerID rather than a real, resolved per-workflow
// identity.
func isCDPUnidentifiedOwner(ownerID string) bool {
	return strings.HasPrefix(ownerID, cdpUnidentifiedOwnerPrefix)
}

func parseTabSelection(args []string) (tab string, clear bool, err error) {
	if len(args) == 0 {
		return "", false, nil
	}

	switch args[0] {
	case "list", "ls":
		return "", false, nil
	case "new":
		request, parseErr := parseNewCDPTabRequest(args)
		if parseErr != nil {
			return "", false, parseErr
		}
		return request.Label, false, nil
	case "close":
		if len(args) > 1 {
			return strings.TrimSpace(args[1]), true, nil
		}
		return "", false, nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return "", false, nil
		}
		return strings.TrimSpace(args[0]), false, nil
	}
}

func parseNewCDPTabRequest(args []string) (newCDPTabRequest, error) {
	if len(args) == 0 || args[0] != "new" {
		return newCDPTabRequest{}, fmt.Errorf("tab creation must begin with new")
	}
	var request newCDPTabRequest
	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--label":
			if i+1 >= len(args) {
				return newCDPTabRequest{}, fmt.Errorf("CDP shared-browser tab creation requires a value after --label")
			}
			i++
			request.Label = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-"):
			return newCDPTabRequest{}, fmt.Errorf("unsupported tab new option %q", arg)
		case arg != "":
			if request.URL != "" {
				return newCDPTabRequest{}, fmt.Errorf("tab new accepts at most one URL")
			}
			request.URL = arg
		}
	}
	if request.Label == "" {
		return newCDPTabRequest{}, fmt.Errorf("CDP shared-browser tab creation requires --label <label> so later commands can target it")
	}
	if request.URL != "" {
		parsed, err := url.Parse(request.URL)
		if err != nil || strings.TrimSpace(parsed.Scheme) == "" {
			return newCDPTabRequest{}, fmt.Errorf("tab new target %q must be an absolute URL with an explicit scheme such as https://", request.URL)
		}
		if (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.TrimSpace(parsed.Host) == "" {
			return newCDPTabRequest{}, fmt.Errorf("tab new target %q must include a host", request.URL)
		}
	}
	return request, nil
}

func canonicalNewCDPTabArgs(request newCDPTabRequest) []string {
	args := []string{"new", "--label", request.Label}
	if request.URL != "" {
		args = append(args, request.URL)
	}
	return args
}

func isTabListRequest(args []string) bool {
	if len(args) == 0 {
		return true
	}
	return len(args) == 1 && (args[0] == "list" || args[0] == "ls")
}

func selectedCDPTabMessage(port int, ownerID string) string {
	cdpURL := resolveCdpURL(port)
	if tab := getCDPTabSelectionForPrompt(port, ownerID); tab != "" {
		return fmt.Sprintf("Selected CDP tab: %s\nUse the configured CDP endpoint and tab inline on page actions, for example: args=[\"--cdp\", %q, \"tab\", %q, \"-i\"] for snapshot.", tab, cdpURL, tab)
	}
	return fmt.Sprintf("No selected CDP tab for this workflow yet. List tabs and reuse a relevant real tN id first. If none matches, request one stable labeled tab with agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"new\", \"--label\", \"<workflow-label>\", \"https://target.example\"], session=\"<session>\"). The backend performs an exact-URL reuse check before creating and returns the real tN id; use that id inline on later page actions.", cdpURL)
}

func fallbackCDPTabListMessage(port int, ownerID string, err error) string {
	message := "Could not refresh the real CDP tab list within the short timeout; falling back to remembered workflow tab state."
	if err != nil {
		message += " Last tab-list error: " + truncatePromptField(oneLine(err.Error()), 240)
	}
	return message + "\n\n" + selectedCDPTabMessage(port, ownerID)
}

type cdpTabListOutput struct {
	Data struct {
		Tabs     []cdpTabInfo `json:"tabs"`
		Active   bool         `json:"active"`
		Label    string       `json:"label"`
		TabID    string       `json:"tabId"`
		TargetID string       `json:"targetId"`
		Title    string       `json:"title"`
		URL      string       `json:"url"`
	} `json:"data"`
}

type cdpTabInfo struct {
	Active   bool   `json:"active"`
	Label    string `json:"label"`
	TabID    string `json:"tabId"`
	TargetID string `json:"targetId"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

func isCDPTabID(tab string) bool {
	tab = strings.TrimSpace(tab)
	if len(tab) < 2 || tab[0] != 't' {
		return false
	}
	_, err := strconv.Atoi(tab[1:])
	return err == nil
}

func findCDPTabID(output, tab string) string {
	tab = strings.TrimSpace(tab)
	if tab == "" {
		return ""
	}
	if isCDPTabID(tab) {
		return tab
	}
	var parsed cdpTabListOutput
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return ""
	}
	if strings.TrimSpace(parsed.Data.Label) == tab || strings.TrimSpace(parsed.Data.TabID) == tab {
		return strings.TrimSpace(parsed.Data.TabID)
	}
	for _, candidate := range parsed.Data.Tabs {
		if strings.TrimSpace(candidate.Label) == tab || strings.TrimSpace(candidate.TabID) == tab {
			return strings.TrimSpace(candidate.TabID)
		}
	}
	return ""
}

func findCDPTabByRef(tabs []cdpTabInfo, ref string) (cdpTabInfo, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return cdpTabInfo{}, false
	}
	for _, tab := range tabs {
		if strings.TrimSpace(tab.TabID) == ref || strings.TrimSpace(tab.Label) == ref {
			return tab, true
		}
	}
	return cdpTabInfo{}, false
}

func parseCDPTabs(output string) ([]cdpTabInfo, error) {
	var parsed cdpTabListOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		return nil, err
	}
	tabs := append([]cdpTabInfo(nil), parsed.Data.Tabs...)
	if directID := strings.TrimSpace(parsed.Data.TabID); directID != "" {
		tabs = append(tabs, cdpTabInfo{
			Active:   parsed.Data.Active,
			Label:    strings.TrimSpace(parsed.Data.Label),
			TabID:    directID,
			TargetID: parsed.Data.TargetID,
			Title:    strings.TrimSpace(parsed.Data.Title),
			URL:      strings.TrimSpace(parsed.Data.URL),
		})
	}
	return tabs, nil
}

func normalizedCDPTabURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func displayCDPTabURL(raw string) string {
	normalized := normalizedCDPTabURL(raw)
	parsed, err := url.Parse(normalized)
	if err != nil {
		return normalized
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String()
}

func urlsReferToSameCDPTab(left, right string) bool {
	left = normalizedCDPTabURL(left)
	right = normalizedCDPTabURL(right)
	return left != "" && left == right
}

func findReusableCDPTab(tabs []cdpTabInfo, port int, ownerID string, request newCDPTabRequest) (tab cdpTabInfo, navigate bool, collision *cdpTabInfo, ok bool) {
	for _, candidate := range tabs {
		if strings.TrimSpace(candidate.Label) != request.Label {
			continue
		}
		if urlsReferToSameCDPTab(candidate.URL, request.URL) || isCDPTabOwnedByOwner(port, ownerID, candidate.Label, candidate.TabID) {
			return candidate, request.URL != "" && !urlsReferToSameCDPTab(candidate.URL, request.URL), nil, true
		}
		copy := candidate
		collision = &copy
	}
	if request.URL != "" {
		for _, candidate := range tabs {
			if urlsReferToSameCDPTab(candidate.URL, request.URL) {
				return candidate, false, collision, true
			}
		}
	}
	return cdpTabInfo{}, false, collision, false
}

func findCreatedCDPTab(output, label string) (cdpTabInfo, bool) {
	tabs, err := parseCDPTabs(output)
	if err != nil {
		return cdpTabInfo{}, false
	}
	for _, tab := range tabs {
		if strings.TrimSpace(tab.Label) == strings.TrimSpace(label) {
			return tab, strings.TrimSpace(tab.TabID) != ""
		}
	}
	if len(tabs) == 1 && strings.TrimSpace(tabs[0].TabID) != "" {
		return tabs[0], true
	}
	return cdpTabInfo{}, false
}

func formatCDPTabIdentity(prefix string, tab cdpTabInfo) string {
	parts := []string{strings.TrimSpace(prefix)}
	if label := strings.TrimSpace(tab.Label); label != "" {
		parts = append(parts, fmt.Sprintf("label=%q", label))
	}
	if tabID := strings.TrimSpace(tab.TabID); tabID != "" {
		parts = append(parts, fmt.Sprintf("id=%s", tabID))
	}
	if tabURL := normalizedCDPTabURL(tab.URL); tabURL != "" {
		parts = append(parts, fmt.Sprintf("url=%q", tabURL))
	}
	return strings.Join(parts, " ")
}

func (e *Executor) reuseCDPTabForNew(ctx context.Context, session, cdpURL string, opts *ExecuteOptions, port int, ownerID string, request newCDPTabRequest) (string, bool, error) {
	output, err := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout))
	if err != nil {
		// The wrapped error below is what reaches the agent, and it has misled
		// diagnosis before: the CLI's own combined stdout+stderr can contain an
		// unrelated warning (e.g. a config-file notice) ahead of the actual
		// exit reason, so a reader stops at the first line rather than the one
		// that matters. Log the full context server-side so a recurrence can be
		// diagnosed from what actually happened -- session, port, and CDP
		// endpoint -- rather than from whichever line of CLI output looked most
		// like a cause.
		log.Printf("[BROWSER] CDP tab list refresh failed before creating label %q for %q: session=%s port=%d cdpURL=%s err=%v", request.Label, request.URL, session, port, cdpURL, err)
		return "", false, fmt.Errorf("CDP_TAB_REUSE_CHECK_UNAVAILABLE: cannot safely create label %q for %q because the real tab list could not be refreshed: %w", request.Label, request.URL, err)
	}
	tabs, err := parseCDPTabs(output)
	if err != nil {
		return "", false, fmt.Errorf("CDP_TAB_REUSE_CHECK_INVALID: cannot safely create label %q because the real tab list response was invalid: %w", request.Label, err)
	}
	candidate, navigate, collision, ok := findReusableCDPTab(tabs, port, ownerID, request)
	if !ok {
		if collision != nil {
			return "", false, fmt.Errorf("CDP_TAB_LABEL_CONFLICT: label %q already belongs to tab %s at %q. Select that tab explicitly, or choose a different workflow label; the backend will not create a duplicate label or silently navigate a pre-existing user tab", request.Label, collision.TabID, normalizedCDPTabURL(collision.URL))
		}
		return "", false, nil
	}

	tabID := strings.TrimSpace(candidate.TabID)
	if tabID == "" {
		return "", false, nil
	}
	// `agent-browser tab <id>` activates the visible Chrome window on macOS.
	// The real reuse-check above already tells us whether this tab is active, so
	// do not switch it again when no switch is needed.
	if !candidate.Active {
		selectedOutput, err := e.selectCDPTab(ctx, session, tabID, cdpURL, opts)
		if err != nil {
			return "", false, fmt.Errorf("reuse existing CDP tab %s: %w", tabID, err)
		}
		if resolved := findCDPTabID(selectedOutput, tabID); resolved != "" {
			tabID = resolved
		}
	}
	candidate.TabID = tabID

	if navigate {
		openOutput, openErr := e.Client.ExecuteCommand(ctx, []string{
			"--session", session,
			"open", request.URL,
			"--cdp", cdpURL,
			"--json",
		}, opts)
		if openErr != nil {
			return "", false, fmt.Errorf("navigate reused workflow-owned CDP tab %s to %q: %w", tabID, request.URL, openErr)
		}
		candidate.URL = request.URL
		if opened, found := findCreatedCDPTab(openOutput, ""); found && opened.URL != "" {
			candidate.URL = opened.URL
		}
	}

	setCDPTabAlias(port, ownerID, request.Label, tabID)
	setCDPTabSelection(port, ownerID, tabID)
	setCDPActiveTab(port, tabID)
	log.Printf("[BROWSER] CDP: reused tab label=%q id=%q url=%q owner=%q port=%d", request.Label, tabID, candidate.URL, ownerID, port)
	return formatCDPTabIdentity("Reused existing CDP tab", candidate) + "\n" + selectedCDPTabMessage(port, ownerID), true, nil
}

func formatCDPTabListForPrompt(output string) string {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return "(no tabs returned)"
	}

	tabs, err := parseCDPTabs(raw)
	if err != nil {
		return truncatePromptField(raw, 2000)
	}
	if len(tabs) == 0 {
		return "(no tabs found)"
	}

	const maxTabs = 20
	lines := make([]string, 0, min(len(tabs), maxTabs)+1)
	for i, tab := range tabs {
		if i >= maxTabs {
			lines = append(lines, fmt.Sprintf("... %d more tab(s) omitted", len(tabs)-maxTabs))
			break
		}

		tabID := oneLine(tab.TabID)
		if tabID == "" {
			tabID = fmt.Sprintf("tab-%d", i+1)
		}

		status := ""
		if tab.Active {
			status = " active"
		}

		line := fmt.Sprintf("- %s%s", tabID, status)
		if label := truncatePromptField(oneLine(tab.Label), 50); label != "" {
			line += fmt.Sprintf(" label=%q", label)
		}
		if title := truncatePromptField(oneLine(tab.Title), 90); title != "" {
			line += fmt.Sprintf(" title=%q", title)
		}
		if tabURL := truncatePromptField(oneLine(displayCDPTabURL(tab.URL)), 180); tabURL != "" {
			line += fmt.Sprintf(" url=%q", tabURL)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func truncatePromptField(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "..."
}

func stripRedundantTabCommandArg(command string, args []string) []string {
	if command != "tab" || len(args) == 0 {
		return args
	}
	cleaned := append([]string(nil), args...)
	for len(cleaned) > 1 && cleaned[0] == "tab" {
		cleaned = cleaned[1:]
	}
	return cleaned
}

func normalizeAgentBrowserCommandArgs(command string, args []string) []string {
	cleaned := stripRedundantCommandArg(command, args)
	if command == "wait" {
		cleaned = normalizeWaitDurationArgs(cleaned)
	}
	return cleaned
}

func normalizeOpenCommandArgs(command string, args []string) (tab string, cleaned []string, ok bool, err error) {
	cleaned = stripRedundantCommandArg(command, args)
	tab, cleaned, ok, err = stripInlineTabFromOpenArgs(cleaned)
	if err != nil || ok {
		return tab, cleaned, ok, err
	}
	return "", cleaned, false, nil
}

func stripRedundantCommandArg(command string, args []string) []string {
	command = strings.TrimSpace(command)
	if command == "" || len(args) <= 1 {
		return args
	}
	if strings.EqualFold(strings.TrimSpace(args[0]), command) {
		return append([]string(nil), args[1:]...)
	}
	return args
}

func normalizeWaitDurationArgs(args []string) []string {
	if len(args) != 1 {
		return args
	}
	raw := strings.TrimSpace(args[0])
	if raw == "" {
		return args
	}
	if _, err := strconv.Atoi(raw); err == nil {
		return args
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return args
	}
	return []string{fmt.Sprintf("%d", d.Milliseconds())}
}

func extractInlineCDPTab(args []string) (tab string, cleaned []string, err error) {
	cleaned = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "tab", "--tab":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", nil, fmt.Errorf("CDP shared-browser command requires %s <tab-id-or-label>", args[i])
			}
			if tab != "" {
				return "", nil, fmt.Errorf("CDP shared-browser command includes multiple tab selections; include exactly one")
			}
			tab = strings.TrimSpace(args[i+1])
			i++
		default:
			cleaned = append(cleaned, args[i])
		}
	}
	return tab, cleaned, nil
}

func stripInlineTabFromOpenArgs(args []string) (tab string, cleaned []string, ok bool, err error) {
	if len(args) == 0 {
		return "", args, false, nil
	}
	// Older/incorrect callers sometimes omit the literal "tab" marker and send
	// ["t9", "https://example.com"]. Passing that through makes agent-browser
	// navigate to https://t9/ and ignore the intended URL. Recover the obvious
	// tab-id form here; labels still require the explicit marker so ordinary URL
	// arguments cannot be mistaken for tab selections.
	if isCDPTabID(args[0]) {
		if len(args) < 2 {
			return "", nil, false, fmt.Errorf("open command received tab id %q without a URL; pass a URL-only open request or use tab %s <url>", args[0], args[0])
		}
		if err := validateOpenTargetURL(args[1]); err != nil {
			return "", nil, false, err
		}
		return strings.TrimSpace(args[0]), append([]string(nil), args[1:]...), true, nil
	}
	if args[0] != "tab" && args[0] != "--tab" {
		if err := validateOpenTargetURL(args[0]); err != nil {
			return "", nil, false, err
		}
		return "", args, false, nil
	}
	if len(args) < 3 || strings.TrimSpace(args[1]) == "" {
		return "", nil, false, fmt.Errorf("open command uses %s but is missing <tab-id-or-label> and URL", args[0])
	}
	if err := validateOpenTargetURL(args[2]); err != nil {
		return "", nil, false, err
	}
	return strings.TrimSpace(args[1]), append([]string(nil), args[2:]...), true, nil
}

func validateOpenTargetURL(raw string) error {
	target := strings.TrimSpace(raw)
	parsed, err := url.Parse(target)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" {
		return fmt.Errorf("browser navigation target %q must be an absolute URL with an explicit scheme such as https://; a CDP tab id such as t9 is not a URL", target)
	}
	if (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("browser navigation target %q must include a host", target)
	}
	return nil
}

func stringArgs(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, arg := range v {
			if s, ok := arg.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), v...)
	default:
		return nil
	}
}

func stripCDPArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--cdp" {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
