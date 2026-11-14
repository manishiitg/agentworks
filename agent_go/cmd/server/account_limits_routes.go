package server

import (
	"encoding/json"
	"net/http"
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
}

func (api *StreamingAPI) handleAccountLimits(w http.ResponseWriter, r *http.Request) {
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
	views := []accountLimitsView{}
	appendView := func(id, provider, name, owner string, server bool) {
		view := accountLimitsView{AccountID: id, Provider: provider, Name: name, Owner: owner, SharedServer: server}
		if snapshot, found := subscriptionLimits.get(provider, id); found {
			view.Snapshot = &snapshot
		}
		views = append(views, view)
	}
	for _, provider := range serverAccountProviders() {
		if !admin && !api.serverAccountAvailableToCaller(ctx, userID, provider) {
			continue
		}
		appendView("global:"+provider, provider, adminManagedProviderAccountName, "Shared server account", true)
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
		appendView(record.ID, record.Provider, record.DisplayName, logUsernameForUserID(record.OwnerUserID), false)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"accounts": views})
}
