package server

// The Mac app's "Where your AI programs run" overview (PLAT-856-441): one box per program and place (this Mac and
// each connected server) saying what a chat there will use. The backend decides every box; the page only shows it.
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// overviewCell is one box: State is ready, sign_in, off or absent; Use says what a chat will use there.
type overviewCell struct {
	State  string `json:"state"`
	Use    string `json:"use,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Action is the one next step: sign_in (this Mac), add_mine (copy your Mac account there), ask_admin.
	Action string `json:"action,omitempty"`
}

type overviewPlace struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SignedInAs string `json:"signed_in_as,omitempty"`
	Reachable  bool   `json:"reachable"`
}

// GET /api/desktop-connections/overview?providers=a,b -> {"places": [...], "cells": {"<program>": {"<place>": cell}}}
func (api *StreamingAPI) handleDesktopOverview(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	providers := []string{}
	for _, provider := range strings.Split(r.URL.Query().Get("providers"), ",") {
		if provider = strings.TrimSpace(provider); provider != "" && providerEnabled(provider) {
			providers = append(providers, provider)
		}
	}
	api.syncCompanyAccounts(r.Context(), owner)
	records, _ := loadProviderConnections(r.Context())
	desktopConnectionsMu.Lock()
	connections, _ := readDesktopConnections(owner)
	desktopConnectionsMu.Unlock()

	places := []overviewPlace{{ID: "local", Name: "This Mac", Reachable: true}}
	cells := map[string]map[string]overviewCell{}
	for _, provider := range providers {
		cells[provider] = map[string]overviewCell{}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// This Mac: the Mac's own login or key for each program.
	for _, provider := range providers {
		wg.Add(1)
		go func(provider string) {
			defer wg.Done()
			cell := overviewCell{State: "sign_in", Action: "sign_in"}
			status, ok := providerAccountStatus{}, false
			if entry, cached := cachedProviderAccountEntry("global:" + provider); cached && entry.configured != nil {
				status, ok = providerAccountStatus{State: map[bool]string{true: "signed_in", false: "signed_out"}[*entry.configured], Identity: entry.identity}, true
			} else if target, err := api.resolveProviderAccountTarget(ctx, "global:"+provider, provider); err == nil {
				status, ok = checkProviderAccountStatus(ctx, target, false), true
				rememberProviderAccountStatus("global:"+provider, status)
			}
			if ok && status.State == "signed_in" {
				cell = overviewCell{State: "ready", Use: "Your login", Detail: status.Identity}
			} else if ok && strings.Contains(status.Detail, "not installed") {
				cell = overviewCell{State: "absent", Detail: "Not installed"}
			}
			mu.Lock()
			cells[provider]["local"] = cell
			mu.Unlock()
		}(provider)
	}

	// Each server: the company account it lends you, and your own account there.
	for _, c := range connections {
		places = append(places, overviewPlace{ID: c.ID, Name: c.Name, SignedInAs: firstNonEmptyTrimmed(c.User.Email, c.User.Username)})
	}
	for index, c := range connections {
		wg.Add(1)
		go func(c desktopConnection, index int) {
			defer wg.Done()
			own, status, err := remoteAccountViews(ctx, c)
			reachable := err == nil && status == http.StatusOK
			mu.Lock()
			defer mu.Unlock()
			places[index].Reachable = reachable
			for _, provider := range providers {
				company := false
				for _, record := range records {
					if record.OwnerUserID == owner && record.ViaConnection == c.ID && record.Provider == provider {
						company = true
					}
				}
				offered, mine, mineReady := false, false, false
				for _, view := range own {
					if view.Provider != provider {
						continue
					}
					switch view.Relation {
					case "server":
						offered = true
					case "own":
						mine = true
						mineReady = mineReady || view.Configured == nil || *view.Configured
					}
				}
				cell := overviewCell{State: "absent", Detail: "Not on " + c.Name}
				switch {
				case !reachable && !company:
					cell = overviewCell{State: "absent", Detail: "Cannot reach " + c.Name}
				case company && mineReady:
					cell = overviewCell{State: "ready", Use: "Company + yours", Detail: "Uses the company account"}
				case company:
					cell = overviewCell{State: "ready", Use: "Company", Detail: c.Name + "'s account"}
				case mineReady:
					cell = overviewCell{State: "ready", Use: "Yours", Detail: "Your own account there"}
				case mine:
					cell = overviewCell{State: "sign_in", Use: "Yours", Detail: "Sign in needed", Action: "sign_in_there"}
				case offered:
					cell = overviewCell{State: "off", Use: "Company: off", Detail: "An admin can turn it on", Action: "add_mine"}
				}
				cells[provider][c.ID] = cell
			}
		}(c, index+1)
	}
	wg.Wait()
	desktopJSON(w, map[string]any{"places": places, "cells": cells})
}

// remoteAccountViews lists every account the person sees on a connected server (metadata only).
func remoteAccountViews(ctx context.Context, c desktopConnection) ([]providerAccountView, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+"/api/provider-connections", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var list struct {
		Connections []providerAccountView `json:"connections"`
	}
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list)
	}
	return list.Connections, resp.StatusCode, nil
}
