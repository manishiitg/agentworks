package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Company keys on a Mac (owner, 26 Nov: company keys reach everyone): a Mac signed in to its company server gets every
// company key there in its projects, beside its own keys, the same way online projects get them. A key the Mac
// already has under the same name wins. The server is the project's paired server, or the person's only server
// sign-in; with several unpaired sign-ins none is guessed. Values go only to the Mac, never to a web page.

// handleVaultMeCompanyKeys sends the company keys (names and values) to the person's Mac.
func (api *StreamingAPI) handleVaultMeCompanyKeys(w http.ResponseWriter, r *http.Request) {
	person, ok := vaultMeCaller(w, r)
	if !ok {
		return
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		writeUsersError(w, http.StatusForbidden, "Secret values are only sent to your Mac.")
		return
	}
	out := []resolvedSecret{}
	if companyKeysOn() {
		rows, err := permittedGlobalSecrets(r.Context(), person)
		if err != nil {
			writeUsersError(w, http.StatusServiceUnavailable, "Your Vault on this server can't be read right now; try again.")
			return
		}
		for _, row := range rows {
			if isCompanyKeyName(row.Name) && row.Value != "" {
				out = append(out, resolvedSecret{Name: row.Name, Value: row.Value})
			}
		}
	}
	log.Printf("[VAULT_ME] user=%s company_keys=%d", person, len(out))
	writeUsersJSON(w, http.StatusOK, map[string]any{"keys": out})
}

type macCompanyKeysCached struct {
	at   time.Time
	keys []resolvedSecret
}

var macCompanyKeysCache sync.Map // owner|connection -> macCompanyKeysCached

const (
	macCompanyKeysFresh    = time.Minute
	macCompanyKeysStaleMax = 10 * time.Minute
)

// addServerCompanyKeys adds, on a Mac, the company keys from the project owner's company server to a turn's keys. A
// name the turn already has keeps its value.
func (api *StreamingAPI) addServerCompanyKeys(ctx context.Context, userID, folder string, have []resolvedSecret) []resolvedSecret {
	if !IsLocalMode() || folder == "" || userID == "" {
		return have
	}
	owner := api.projectSecretsOwner(userID, folder)
	var conn desktopConnection
	if p, paired, err := pairedServerCrewFor(owner, folder); err == nil && paired {
		conn = p.conn
	} else if conns, err := desktopConnectionsOf(owner); err == nil && len(conns) == 1 {
		conn = conns[0]
	} else {
		return have
	}
	key := owner + "|" + conn.ID
	var keys []resolvedSecret
	var staleEntry macCompanyKeysCached
	hasStale := false
	if cached, ok := macCompanyKeysCache.Load(key); ok {
		entry := cached.(macCompanyKeysCached)
		if time.Since(entry.at) < macCompanyKeysFresh {
			keys = entry.keys
		} else if time.Since(entry.at) < macCompanyKeysStaleMax {
			staleEntry = entry
			hasStale = true
		}
	}
	if keys == nil {
		round, cancel := context.WithTimeout(ctx, serverVaultRequestTimeout)
		started := time.Now()
		body, result := serverVaultGet(round, owner, conn, "/api/vault/me/company-keys")
		cancel()
		logServerVault(owner, conn, "company_keys", "-", result, started)
		if result != "ok" {
			if result == "unreachable" && hasStale {
				keys = staleEntry.keys
			} else {
				return have
			}
		} else {
			var out struct {
				Keys []resolvedSecret `json:"keys"`
			}
			if json.Unmarshal(body, &out) != nil {
				return have
			}
			keys = out.Keys
			macCompanyKeysCache.Store(key, macCompanyKeysCached{at: time.Now(), keys: keys})
		}
	}
	got := map[string]bool{}
	for _, s := range have {
		got[s.Name] = true
	}
	for _, s := range keys {
		if s.Name != "" && s.Value != "" && !got[s.Name] {
			got[s.Name] = true
			have = append(have, s)
		}
	}
	return have
}
