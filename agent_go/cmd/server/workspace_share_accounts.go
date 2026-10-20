package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// Builder-facing account names only. Sharing never changes the selected
// accounts or the permission checks those accounts already enforce.
func (api *StreamingAPI) workspaceShareAccounts(ctx context.Context, target dashboardTarget) []runShareAccount {
	origin := crewProfileID

	manifest, err := readCrewProjectManifests(ctx, origin, target.Root)
	if err != nil {
		return nil
	}
	accounts := []runShareAccount{}
	owner, _ := crewProjectOwnerID(target.Root)
	if cfg := manifest.Capabilities.LLMConfig; cfg != nil && cfg.BuilderLLM != nil {
		model := cfg.BuilderLLM
		id := strings.TrimSpace(model.ConnectionID)
		if id == "" {
			id = ownDefaultProviderAccountID(ctx, owner, model.Provider)
		}
		name := model.Provider + " · company/server account"
		if id != "" {
			name = model.Provider + " · selected account"
			providerConnectionsMu.Lock()
			records, _ := loadProviderConnections(ctx)
			providerConnectionsMu.Unlock()
			for _, record := range records {
				if record.ID == id {
					name = firstNonEmptyTrimmed(record.DisplayName, model.Provider) + " · " + firstNonEmptyTrimmed(record.Scope, "personal")
					break
				}
			}
		}
		accounts = append(accounts, runShareAccount{Kind: "Model", Name: name, Use: "Use consumes this account's provider quota and any API-key billing. Activity and costs are attributed to the person calling the project."})
	}
	attachments, _ := placeMCPAttachmentsFor(target.Root)
	for _, attachment := range attachments {
		servers, _ := listPlaceMCPServers(attachmentStore(attachment, target.Root))
		for _, server := range servers {
			if server.Name != attachment.Server {
				continue
			}
			kind, use := "MCP app", "Available through this project only under the account's existing access rules."
			if isPersonalSlack(server) {
				kind, use = "Slack", "The account owner can read and send. Other people can send messages and look people up; they cannot read or search this person's Slack."
			}
			accounts = append(accounts, runShareAccount{Kind: kind, Name: firstNonEmptyTrimmed(server.Label, server.Name), Use: use})
		}
	}
	if svc := services.GetGmailService(); svc != nil {
		for _, connection := range svc.ConnectionsUsableFrom(gmailToolScope(target.Root)) {
			accounts = append(accounts, runShareAccount{Kind: "Google", Name: firstNonEmptyTrimmed(connection.Email, connection.DisplayName), Use: "Existing Google service, read/write and caller grants still apply. Personal Gmail stays with its owner unless explicitly enabled for others."})
		}
	}
	return accounts
}
