package server

import (
	"context"
	"log"
	"strings"
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
