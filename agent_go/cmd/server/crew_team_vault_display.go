package server

import (
	"context"
	"log"
	"sync"
	"time"
)

// Team cards need names, not tool schemas or execution authority. Keep this
// cache separate from vaultServersFor: every agent call still checks live access.
type crewTeamVaultDisplay struct {
	servers  []vaultRuntimeServer
	personal map[string]bool
}

type crewTeamVaultDisplayEntry struct {
	value      *crewTeamVaultDisplay // immutable after publication
	at         time.Time
	checked    time.Time
	refreshing bool
}

var crewTeamVaultDisplays = struct {
	sync.Mutex
	entries map[string]*crewTeamVaultDisplayEntry
}{entries: make(map[string]*crewTeamVaultDisplayEntry)}

// A cold or unavailable Vault omits the optional card, never delays the Team
// response. The existing Team refresh fills it in; only names/IDs are retained.
func crewTeamVaultDisplayFor(person string) *crewTeamVaultDisplay {
	if !activeMCPPerson(person) {
		return nil
	}
	if !vaultConfigured() {
		return &crewTeamVaultDisplay{}
	}
	crewTeamVaultDisplays.Lock()
	defer crewTeamVaultDisplays.Unlock()
	entry := crewTeamVaultDisplays.entries[person]
	if entry == nil {
		if len(crewTeamVaultDisplays.entries) >= 64 {
			for key := range crewTeamVaultDisplays.entries {
				delete(crewTeamVaultDisplays.entries, key)
				break
			}
		}
		entry = &crewTeamVaultDisplayEntry{}
		crewTeamVaultDisplays.entries[person] = entry
	}
	if !entry.refreshing && time.Since(entry.at) >= 45*time.Second && time.Since(entry.checked) >= 10*time.Second {
		entry.refreshing = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, vaultRequestCallerKey{}, "crew_team_display")
			servers, err := vaultServersFor(ctx, person)
			value := &crewTeamVaultDisplay{personal: map[string]bool{}}
			if err == nil {
				var own personalVaultList
				own, err = personalVaults(ctx, person)
				for _, vault := range own.Vaults {
					if vault.Role == "owner" {
						for _, connection := range vault.Connections {
							value.personal[connection.ID] = true
						}
					}
				}
			}
			for _, server := range servers {
				value.servers = append(value.servers, vaultRuntimeServer{ID: server.ID, Name: server.Name, Label: server.Label})
			}
			crewTeamVaultDisplays.Lock()
			defer crewTeamVaultDisplays.Unlock()
			if err == nil && activeMCPPerson(person) {
				entry.value, entry.at = value, time.Now()
			}
			entry.checked, entry.refreshing = time.Now(), false
		}()
	}
	if time.Since(entry.at) >= time.Minute {
		return nil
	}
	return entry.value
}

func invalidateCrewTeamVaultDisplays() {
	crewTeamVaultDisplays.Lock()
	clear(crewTeamVaultDisplays.entries)
	crewTeamVaultDisplays.Unlock()
}

type vaultRequestCallerKey struct{}

func logSlowVaultRequest(ctx context.Context, kind string, started time.Time, status *int) {
	if elapsed := time.Since(started); elapsed >= time.Second {
		caller, _ := ctx.Value(vaultRequestCallerKey{}).(string)
		if caller == "" {
			caller = kind
		}
		log.Printf("[VAULT_REQUEST] caller=%s kind=%s status=%d elapsed_ms=%d context_err=%v", caller, kind, *status, elapsed.Milliseconds(), ctx.Err())
	}
}
