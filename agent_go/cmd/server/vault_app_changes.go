package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// A coding app keeps the tool list it started with: follow-ups go straight into the running app, so a Vault app
// granted mid-chat is not there until the chat is rebuilt (#3528). Rebuilding on every change would lose the prompt
// cache each time, so the chat shows what changed and the person picks when to reload. Chats with no app screen
// (Slack, WhatsApp, schedules) have nobody to click, so they reload on their next message by themselves.

// launchedVaultApps is what a chat's tools were built from: whose Vault, the product's own picks, and the Vault apps
// (name -> label) that came out of them.
type launchedVaultApps struct {
	person string
	lender string
	picks  []string
	apps   map[string]string
	// own: the chat was built with apps from the typing person's personal vault (#4165).
	own bool
}

var (
	launchedVaultAppsBySession sync.Map // session -> launchedVaultApps
	vaultReloadRequested       sync.Map // session -> true, until the next rebuild
)

// vaultAppCandidates applies the default rule of scopeAgentMCP to a Vault inventory: picked apps when the product
// picked some, none on an explicit none, otherwise every authorized app (and, on a linked Mac, the server's).
func (api *StreamingAPI) vaultAppCandidates(ctx context.Context, person, lender, sessionID string, names []string) (context.Context, []string, []vaultRuntimeServer, []vaultRuntimeServer, error) {
	if root := api.projectRootForSession(sessionID); serverCrewForPath(root) != "" {
		project := serverCrewForPath(root)
		ctx = withCrewVaultScope(ctx, crewVaultID(project)) // a server Crew's turn sees its own Crew vault, and no other
		api.ensureCrewVaultForTurn(ctx, root)
		// The turn takes the vault's apps as they are now (it may have been made just above), not the binding's earlier copy.
		names = api.crewVaultServers(ctx, project, names)
	}
	// A User of a server Crew uses its apps but is not one of the vault's owners: the turn reads the Crew vault as the
	// main owner and keeps only that vault's own apps (crewVaultReader), never one of the owner's personal apps.
	reader := crewVaultReader(ctx, lender)
	rows, inventoryErr := vaultServersFor(ctx, reader)
	if reader != lender {
		rows = crewVaultAppRowsOnly(rows, names)
	}
	ctx = context.WithValue(ctx, vaultInventoryKey{}, vaultInventory{reader, rows, inventoryErr})
	names = append([]string(nil), names...)
	if inventoryErr == nil && serverCrewForPath(api.projectRootForSession(sessionID)) != "" && strings.TrimSpace(person) != "" && !isScheduledSessionIdentity(sessionID, "") {
		ctx, rows, names = addOwnChatAppsForPerson(ctx, sessionID, person, reader, rows, names) // your own apps in your own chat (#4165), gated per turn (#4560)
	}
	picksVault := slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(strings.TrimSpace(name), "vault_") })
	explicitNone := slices.Contains(names, mcpclient.NoServers)
	if inventoryErr == nil && !picksVault && !explicitNone {
		for _, row := range rows {
			names = append(names, vaultServerName(row.ID))
		}
	}
	if inventoryErr == nil && picksVault {
		names = actingVaultPicks(ctx, rows, names)
	}
	ctx, remote := api.withServerVaultInventory(ctx, lender, sessionID)
	if !picksVault && !explicitNone {
		for _, row := range remote {
			names = append(names, row.Name)
		}
	}
	return ctx, names, rows, remote, inventoryErr
}

// vaultAppLabels keeps the Vault apps among names, with the label people know them by.
func vaultAppLabels(names []string, rows, remote []vaultRuntimeServer) map[string]string {
	labels := map[string]string{}
	for _, row := range rows {
		labels[vaultServerName(row.ID)] = firstNonEmptyTrimmed(row.Label, row.Provider, row.ID)
	}
	for _, row := range remote {
		labels[row.Name] = firstNonEmptyTrimmed(row.Label, row.Provider, row.Name)
	}
	out := map[string]string{}
	for _, name := range names {
		name = vaultSelectionName(strings.TrimSpace(name))
		if governedVaultName(name) {
			out[name] = firstNonEmptyTrimmed(labels[name], name)
		}
	}
	return out
}

// recordLaunchedVaultApps remembers the apps a rebuilt chat got; a rebuild also answers any pending reload.
func recordLaunchedVaultApps(sessionID, person, lender string, picks []string, apps map[string]string, own bool, inventoryErr error) {
	if sessionID == "" || inventoryErr != nil {
		return
	}
	launchedVaultAppsBySession.Store(sessionID, launchedVaultApps{person: person, lender: lender, picks: picks, apps: apps, own: own})
	vaultReloadRequested.Delete(sessionID)
}

func launchedVaultAppsFor(sessionID string) (launchedVaultApps, bool) {
	value, ok := launchedVaultAppsBySession.Load(sessionID)
	if !ok {
		return launchedVaultApps{}, false
	}
	launched, ok := value.(launchedVaultApps)
	return launched, ok
}

// vaultAppChanges compares a chat's launched Vault apps with what it would get now. Unknown chats and a Vault that
// cannot be read report no change.
func (api *StreamingAPI) vaultAppChanges(ctx context.Context, sessionID string) (launchedVaultApps, []string, []string, bool) {
	launched, ok := launchedVaultAppsFor(sessionID)
	if !ok {
		return launchedVaultApps{}, nil, nil, false
	}
	// The same context mark the chat's own build had, so both sides compare like with like.
	if api.projectRootForSession(sessionID) != "" {
		ctx = context.WithValue(ctx, placeScopedKey{}, true)
	}
	_, names, rows, remote, err := api.vaultAppCandidates(ctx, launched.person, launched.lender, sessionID, launched.picks)
	if err != nil {
		return launched, nil, nil, false
	}
	now := vaultAppLabels(names, rows, remote)
	var added, removed []string
	for name, label := range now {
		if _, had := launched.apps[name]; !had {
			added = append(added, label)
		}
	}
	for name, label := range launched.apps {
		if _, has := now[name]; !has {
			removed = append(removed, label)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return launched, added, removed, len(added)+len(removed) > 0
}

// vaultAppsReloadDue decides whether this message rebuilds the chat instead of going into the running app: the
// person asked to reload, or a chat with no app screen sees changed Vault apps.
// Personal Vault apps on online projects (#4165) are gated per turn in discovery and handleVaultRuntimeMCP so
// switching between typed and automated turns reuses the warm CLI and prompt cache (#4560).
func (api *StreamingAPI) vaultAppsReloadDue(ctx context.Context, sessionID string, req QueryRequest) bool {
	if _, asked := vaultReloadRequested.Load(sessionID); asked {
		log.Printf("[VAULT_APPS] session=%s action=reload reason=person_asked", sessionID)
		return true
	}
	if strings.TrimSpace(req.BotPlatform) == "" && !isScheduledCrewChatDelivery(ctx) {
		return false
	}
	if _, added, removed, changed := api.vaultAppChanges(ctx, sessionID); changed {
		log.Printf("[VAULT_APPS] session=%s action=reload reason=no_app_screen added=%d removed=%d", sessionID, len(added), len(removed))
		return true
	}
	return false
}

type vaultAppChangesResponse struct {
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Requested bool     `json:"reload_requested"`
}

// handleSessionToolChanges answers GET (what changed) and POST (reload on the next message) for the chat's own person.
func (api *StreamingAPI) handleSessionToolChanges(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	// Only the chat's own person, checked before any Vault lookup: naming someone else's session reads nothing.
	var added, removed []string
	if launched, ok := launchedVaultAppsFor(sessionID); ok && launched.person != "" && launched.person == GetUserIDFromContext(r.Context()) {
		checkCtx := r.Context()
		if r.Method == http.MethodGet {
			checkCtx = withServerVaultAllowStale(checkCtx)
		}
		_, added, removed, _ = api.vaultAppChanges(checkCtx, sessionID)
	}
	_, requested := vaultReloadRequested.Load(sessionID)
	if r.Method == http.MethodPost {
		if len(added)+len(removed) == 0 {
			http.Error(w, "This chat already has your current apps.", http.StatusConflict)
			return
		}
		vaultReloadRequested.Store(sessionID, true)
		requested = true
		log.Printf("[VAULT_APPS] session=%s action=reload_requested added=%d removed=%d", sessionID, len(added), len(removed))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(vaultAppChangesResponse{Added: nonNilStrings(added), Removed: nonNilStrings(removed), Requested: requested && len(added)+len(removed) > 0})
}
