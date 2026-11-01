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

	"github.com/gorilla/mux"
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

	// The connected servers only (the Servers page); this Mac's own logins are shown on each program's page.
	places := []overviewPlace{}
	cells := map[string]map[string]overviewCell{}
	for _, provider := range providers {
		cells[provider] = map[string]overviewCell{}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// Each server: the company account it lends you, and your own account there.
	for _, c := range connections {
		places = append(places, overviewPlace{ID: c.ID, Name: friendlyServerName(c.Name, c.Server), SignedInAs: firstNonEmptyTrimmed(c.User.Email, c.User.Username)})
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
						// The server has the program only when its own account is set up there.
						offered = offered || view.Configured == nil || *view.Configured
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
					cell = overviewCell{State: "off", Use: "Company: off", Action: "add_mine"}
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

// friendlyServerName turns an address like agents.example.com into "Example"; a name that is not an address stays.
func friendlyServerName(name, server string) string {
	host := strings.TrimSpace(name)
	if host == "" || strings.Contains(host, ".") {
		if host == "" {
			host = strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://")
		}
		labels := strings.Split(strings.Split(host, "/")[0], ".")
		for _, label := range labels {
			switch strings.ToLower(label) {
			case "agents", "video", "www", "app", "ai":
				continue
			}
			if label != "" {
				return strings.ToUpper(label[:1]) + label[1:]
			}
		}
	}
	return host
}

// GET /api/desktop-connections/{connection}/token-usage — your token usage and limits on a connected server, for a
// company account from that server (its limits apply there).
func (api *StreamingAPI) handleDesktopTokenUsage(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	c, err := desktopConnectionFor(GetUserFromContext(r.Context()).UserID, mux.Vars(r)["connection"])
	if err != nil {
		desktopJSONError(w, 404, "Connection is unavailable.")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.Server+"/api/me/token-usage", nil)
	if err != nil {
		desktopJSONError(w, 500, "Cannot reach the server.")
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := desktopHTTPClient().Do(req)
	if err != nil {
		desktopJSONError(w, 502, "Cannot reach "+c.Name+".")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
