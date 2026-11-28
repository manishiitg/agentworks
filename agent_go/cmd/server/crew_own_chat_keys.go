package server

import (
	"context"
	"log"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/agent/codeexec"
)

// Your own keys in your own chat (owner, 26 Nov, #4165): in an online project the person typing also gets the keys in
// their own personal vault, beside the project's vault and company keys. The project's key wins on a name clash, and
// the person's own key wins over a company key. Schedules, workflows, webhooks, bots and other people's asks to a
// shared agent keep the project's and company keys only. A User gets only their own keys, never the main owner's.

// personTypedChatTurn is a turn a person typed in their own chat: not a schedule or other automation, not a bot or
// webhook delivery, not an agent message or an ask from someone else, not an automatic notification.
func personTypedChatTurn(ctx context.Context, req QueryRequest, sessionID string, askOnly bool) bool {
	if askOnly || req.IsAutoNotification || strings.TrimSpace(req.BotPlatform) != "" || isScheduledCrewChatDelivery(ctx) ||
		isScheduledSessionIdentity(sessionID, req.TriggeredBy) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.TriggeredBy)) {
	case "", "manual", "interactive", "workflow_builder":
		return true
	}
	return false
}

// personalVaultSecrets are the keys in the person's own personal vault ("My accounts"), under the names they gave
// them. Nothing is created: a person without a personal vault has none.
func personalVaultSecrets(ctx context.Context, person string) []resolvedSecret {
	list, err := personalVaults(ctx, person)
	if err != nil {
		return nil
	}
	prefix := ""
	for _, v := range list.Vaults {
		if v.Role == "owner" && v.Group.Name == personalVaultName {
			if full, err := vaultSecretName(v.Group.ID, "x"); err == nil {
				prefix = strings.TrimSuffix(full, "x")
			}
		}
	}
	if prefix == "" {
		return nil
	}
	out := []resolvedSecret{}
	for _, row := range visibleGlobalSecrets(ctx, person) {
		if strings.HasPrefix(row.Name, prefix) && strings.TrimSpace(row.Value) != "" {
			out = append(out, resolvedSecret{Name: vaultSecretShortName(row.Name), Value: row.Value})
		}
	}
	return out
}

// mergeOwnChatKeys adds the person's own keys to an online project's turn keys: a name the project's vault has stays
// the project's; a company key with the same name gives way to the person's own.
func mergeOwnChatKeys(turn, personal []resolvedSecret, projectNames map[string]bool) []resolvedSecret {
	if len(personal) == 0 {
		return turn
	}
	at := map[string]int{}
	for i, s := range turn {
		at[s.Name] = i
	}
	out := append([]resolvedSecret{}, turn...)
	for _, p := range personal {
		if projectNames[p.Name] {
			continue
		}
		if i, ok := at[p.Name]; ok {
			out[i] = p // a company key with this name: the person's own wins
			continue
		}
		at[p.Name] = len(out)
		out = append(out, p)
	}
	return out
}

// withOwnChatKeys is an online project's turn keys plus, for a turn the person typed in their own chat, their own
// personal keys. Anything else is returned unchanged.
func (api *StreamingAPI) withOwnChatKeys(ctx context.Context, person, workspacePath string, typed bool, turn []resolvedSecret) []resolvedSecret {
	project := serverCrewForPath(workspacePath)
	if !typed || project == "" || strings.TrimSpace(person) == "" {
		return turn
	}
	projectNames := map[string]bool{}
	if contents, err := api.readCrewVault(ctx, project); err == nil {
		for _, name := range contents.SecretNames {
			projectNames[vaultSecretShortName(name)] = true
		}
	}
	// Read as the typing person, never as the Crew vault reader (a User's turn reads the project vault as its owner).
	personal := personalVaultSecrets(context.Background(), person)
	out := mergeOwnChatKeys(turn, personal, projectNames)
	if added := len(out) - len(turn); len(personal) > 0 {
		log.Printf("[CREW_OWN_KEYS] project=%s person=%s own_keys=%d added=%d", project, logUsernameForUserID(person), len(personal), added)
	}
	return out
}

// Your own sign-in apps in your own chat (#4165): the apps in the typing person's personal vault join an online
// project's turn the same way their keys do. A turn is marked when it starts (markOwnChatTurn); the session's agent
// keeps those personal apps wired with OwnApp delegation tokens, and per-call guards (installOwnChatAppServerGuard
// for discovery/schema lookup and handleVaultRuntimeMCP for execution) hide and refuse them on any turn the person did
// not type — without restarting the coding CLI or losing the prompt cache (#4560). The project's app wins over one of
// yours for the same service, and your own apps are always started and called as you, never as the project's owner.

var ownChatTurns sync.Map // session -> person, while the current turn is one they typed in an online project

func markOwnChatTurn(sessionID, person, workspacePath string, typed bool) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if typed && serverCrewForPath(workspacePath) != "" && strings.TrimSpace(person) != "" {
		ownChatTurns.Store(sessionID, person)
		return
	}
	ownChatTurns.Delete(sessionID)
}

func ownChatTurnPerson(sessionID string) string {
	person, _ := ownChatTurns.Load(sessionID)
	s, _ := person.(string)
	return s
}

// installOwnChatAppServerGuard gates personal Vault app servers on sessionID so
// search_tools and get_api_spec only surface them on turns ownPerson actually
// typed, allowing the coding CLI to stay warm across typed and automated turns (#4560).
func installOwnChatAppServerGuard(sessionID, ownPerson string, ownServers map[string]bool) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	if len(ownServers) == 0 || strings.TrimSpace(ownPerson) == "" {
		codeexec.SetSessionServerGuard(sessionID, nil)
		return
	}
	guarded := make(map[string]bool, len(ownServers))
	for name := range ownServers {
		guarded[name] = true
	}
	codeexec.SetSessionServerGuard(sessionID, func(serverName string) bool {
		if !guarded[serverName] && !guarded[vaultSelectionName(serverName)] {
			return true
		}
		return ownChatTurnPerson(sessionID) == ownPerson
	})
}

type ownChatAppsKey struct{}

// ownChatApps are the apps a turn took from the typing person's personal vault, by connection ID.
type ownChatApps struct {
	person string
	ids    map[string]bool
}

func ownChatAppsFrom(ctx context.Context) ownChatApps {
	own, _ := ctx.Value(ownChatAppsKey{}).(ownChatApps)
	return own
}

// personalVaultAppIDs are the connections in the person's own personal vault ("My accounts").
func personalVaultAppIDs(ctx context.Context, person string) map[string]bool {
	ids := map[string]bool{}
	list, err := personalVaults(ctx, person)
	if err != nil {
		return ids
	}
	for _, v := range list.Vaults {
		if v.Role == "owner" && v.Group.Name == personalVaultName {
			for _, c := range v.Connections {
				if strings.TrimSpace(c.ID) != "" {
					ids[c.ID] = true
				}
			}
		}
	}
	return ids
}

// addOwnChatApps adds, for a live turn the person typed in an online project's chat, the apps in their personal vault.
func addOwnChatApps(ctx context.Context, sessionID, reader string, rows []vaultRuntimeServer, names []string) (context.Context, []vaultRuntimeServer, []string) {
	person := ownChatTurnPerson(sessionID)
	if person == "" {
		return ctx, rows, names
	}
	return addOwnChatAppsForPerson(ctx, sessionID, person, reader, rows, names)
}

// addOwnChatAppsForPerson adds person's personal vault apps to an online project's chat picks.
// reader is whose inventory rows came from: a User's turn reads the project vault as its owner, so
// their own apps are read as themselves and added to the rows here (never to the owner's cached inventory).
func addOwnChatAppsForPerson(ctx context.Context, sessionID, person, reader string, rows []vaultRuntimeServer, names []string) (context.Context, []vaultRuntimeServer, []string) {
	person = strings.TrimSpace(person)
	if person == "" {
		return ctx, rows, names
	}
	ids := personalVaultAppIDs(ctx, person)
	if len(ids) == 0 {
		return ctx, rows, names
	}
	own := rows
	if reader != person {
		access, err := vaultAccessFor(ctx, person)
		if err != nil {
			return ctx, rows, names
		}
		own = access.Servers
	}
	projectProviders := map[string]bool{}
	for _, row := range rows {
		if slices.Contains(names, vaultServerName(row.ID)) && !ids[row.ID] && row.Provider != "" {
			projectProviders[strings.ToLower(row.Provider)] = true
		}
	}
	taken := map[string]bool{}
	for _, row := range own {
		name := vaultServerName(row.ID)
		if !ids[row.ID] || slices.Contains(names, name) || projectProviders[strings.ToLower(row.Provider)] {
			continue // the project's own app for this service wins
		}
		if reader != person {
			rows = append(rows, row)
		}
		names = append(names, name)
		taken[row.ID] = true
	}
	if len(taken) == 0 {
		return ctx, rows, names
	}
	log.Printf("[CREW_OWN_APPS] session=%s person=%s own_apps=%d added=%d", sessionID, logUsernameForUserID(person), len(ids), len(taken))
	return context.WithValue(ctx, ownChatAppsKey{}, ownChatApps{person: person, ids: taken}), rows, names
}
