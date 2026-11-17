package server

import (
	"encoding/json"
	"net/http"
	"sort"
)

// This quota-only projection deliberately does not embed ProviderConnection:
// admin visibility here grants neither account management nor model use.
type accountLimitsView struct {
	AccountID    string                `json:"account_id"`
	Provider     string                `json:"provider"`
	Name         string                `json:"name"`
	Owner        string                `json:"owner"`
	SharedServer bool                  `json:"shared_server"`
	Snapshot     *accountLimitSnapshot `json:"snapshot,omitempty"`
	// SignIn is set while the account's login is being rejected.
	SignIn *accountSignIn `json:"sign_in,omitempty"`
	// Status is the one line people read first: Sign-in needed, Limit
	// reached, Ready or Not checked yet.
	Status string `json:"status"`
	// AllowedModels is the account's model list; empty means every model.
	AllowedModels []string `json:"allowed_models,omitempty"`
	// FromServer marks a Mac account that runs on a connected server's login,
	// so "This Mac" can list only the Mac's own logins.
	FromServer bool `json:"from_server,omitempty"`
}

func accountStatus(view accountLimitsView) string {
	if view.SignIn != nil && view.SignIn.Needed {
		return "Sign-in needed"
	}
	if view.Snapshot == nil {
		return "Not checked yet"
	}
	for _, window := range view.Snapshot.Windows {
		if window.UsedPercent >= 100 {
			return "Limit reached"
		}
	}
	return "Ready"
}

func (api *StreamingAPI) handleAccountLimits(w http.ResponseWriter, r *http.Request) {
	api.serveAccountLimits(w, r, true)
}

// handleAccountSignInNeeded is the header badge's count. It reuses the same
// visibility but never starts quota probes, so polling it costs nothing.
func (api *StreamingAPI) handleAccountSignInNeeded(w http.ResponseWriter, r *http.Request) {
	api.serveAccountLimits(w, r, false)
}

func (api *StreamingAPI) serveAccountLimits(w http.ResponseWriter, r *http.Request, probe bool) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	admin := currentUserIsAdmin(r)
	ctx := r.Context()
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		http.Error(w, "cannot load account limits", http.StatusInternalServerError)
		return
	}
	settings, _ := loadProviderAccountSettings(ctx)
	views := []accountLimitsView{}
	appendView := func(id, provider, name, owner, probePrincipal string, server bool, models []string, fromServer bool) {
		// Admission above chooses this quota-only probe target. Provider calls remain
		// asynchronous, using only the selected account environment.
		if probe {
			api.probedChatLimits(probePrincipal, provider, id, userID)
		}
		view := accountLimitsView{AccountID: id, Provider: provider, Name: name, Owner: owner, SharedServer: server, AllowedModels: models, FromServer: fromServer}
		if snapshot, found := subscriptionLimits.get(provider, id); found {
			view.Snapshot = &snapshot
		}
		if signIn, needed := accountSignIns.get(provider, id); needed {
			view.SignIn = &signIn
		}
		view.Status = accountStatus(view)
		views = append(views, view)
	}
	for _, provider := range serverAccountProviders() {
		if !admin && !api.serverAccountAvailableToCaller(ctx, userID, provider) {
			continue
		}
		appendView("global:"+provider, provider, adminManagedProviderAccountName, "Shared server account", userID, true, serverAccountModels(settings, provider), false)
	}
	run := api.describeProviderAccountRun(ctx, providerAccountScope{Principal: userID})
	for _, record := range records {
		if !providerEnabled(record.Provider) {
			continue
		}
		own := record.OwnerUserID == userID
		if !admin && !own && (personalProviderConnectionsLocked(record.Provider) || api.userAccountAdmission(ctx, record, userID, run) == "") {
			continue
		}
		probePrincipal := userID
		if admin {
			// Quota-only admin access is not general account-use admission. The
			// selected owner's environment is resolved inside the guarded probe;
			// neither credentials nor a runtime handle leave this handler.
			probePrincipal = record.OwnerUserID
		}
		appendView(record.ID, record.Provider, record.DisplayName, logUsernameForUserID(record.OwnerUserID), probePrincipal, false, record.AllowedModels, record.ViaConnection != "")
	}
	// Accounts that need someone come first; the rest keep their order.
	sort.SliceStable(views, func(i, j int) bool {
		return views[i].SignIn != nil && views[j].SignIn == nil
	})
	if !probe {
		_ = json.NewEncoder(w).Encode(map[string]any{"sign_in_needed": signInNeededAccounts(views)})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"accounts": views, "sign_in_needed": signInNeededAccounts(views)})
}
