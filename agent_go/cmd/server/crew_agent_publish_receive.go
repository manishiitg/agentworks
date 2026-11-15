package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewAgentPublishAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Identity string `json:"identity,omitempty"`
	// Kind is whose account it is: "own" (the person's) or "company" (the server's, or one shared with them).
	Kind string `json:"kind,omitempty"`
}

type crewAgentPublishPreview struct {
	Ready       bool                      `json:"ready"`
	Message     string                    `json:"message,omitempty"`
	NeedsLogin  bool                      `json:"needs_login,omitempty"`
	Selection   crewAgentPublishSelection `json:"selection"`
	Accounts    []crewAgentPublishAccount `json:"accounts"`
	TransferID  string                    `json:"transfer_id,omitempty"`
	Files       int                       `json:"files"`
	Bytes       int64                     `json:"bytes"`
	NativeBytes int64                     `json:"native_bytes"`
	Callers     []string                  `json:"callers"`
	Warnings    []string                  `json:"warnings,omitempty"`
}

func (api *StreamingAPI) crewAgentPublishAccounts(ctx context.Context, claims *UserClaims, roots crewroot.Roots, provider, account string) (*crewAgentPublishPreview, error) {
	out := &crewAgentPublishPreview{Accounts: []crewAgentPublishAccount{}}
	server := "this server"
	if address, err := url.Parse(strings.TrimSpace(os.Getenv("PUBLIC_URL"))); err == nil && address.Host != "" {
		server = address.Host
	}
	program := providerAccountProductName(provider)
	if !providerEnabled(provider) {
		alternatives := []string{}
		for _, candidate := range getSupportedProviders() {
			if crewAgentPublishProgramSupported(candidate) {
				alternatives = append(alternatives, providerAccountProductName(candidate))
			}
		}
		out.Message = program + " isn't available on " + server + ". Ask its admin to turn it on"
		if len(alternatives) > 0 {
			out.Message += ", or switch this agent to an available program (" + strings.Join(alternatives, ", ") + ") first"
		}
		out.Message += "."
		return out, nil
	}
	// Your own account or one the company provides (owner, 13 Nov, crew_server_account.go).
	options, err := api.crewServerAccountOptions(ctx, claims, roots, provider)
	if err != nil {
		return nil, err
	}
	out.Accounts = options
	if account == "" {
		account = crewServerAccountPick(roots.ID, claims.UserID, provider, options)
	}
	if account == "" && len(options) == 1 {
		account = options[0].ID
	}
	if len(options) == 0 {
		out.NeedsLogin = true
		out.Message = "There is no " + program + " account you can use on " + server + ": sign in once in Providers → Add my account, or ask the admin to give you the company's account, then retry."
		return out, nil
	}
	if account == "" {
		out.Message = "Ask the person which account this agent uses on " + server + ", their own or the company's, then retry with account_id: " + crewServerAccountChoiceLine(options) + "."
		return out, nil
	}
	if !slices.ContainsFunc(options, func(o crewAgentPublishAccount) bool { return o.ID == account }) {
		return nil, fmt.Errorf("that account is not one you can use on %s; choose one of: %s", server, crewServerAccountChoiceLine(options))
	}
	admitted, err := api.admitProviderAccount(ctx, providerAccountScope{Principal: claims.UserID, WorkspacePath: roots.Virtual, Product: "crew"}, provider, account)
	if err != nil {
		return nil, err
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return nil, err
	}
	if err = claudeCodeQueryAuthenticationError(&resolvedAgentProfile{Definition: profile}, provider, admitted); err != nil {
		return nil, err
	}
	if readCrewServerAccounts(roots.ID, claims.UserID)[provider] != account {
		if err = writeCrewServerAccount(roots.ID, claims.UserID, provider, account); err != nil {
			return nil, err
		}
		logCrewServerAccount(roots.ID, claims.UserID, provider, account, "run_online")
	}
	out.Ready, out.Selection.AccountID = true, account
	return out, nil
}

func crewAgentPublishProgramSupported(provider string) bool {
	return provider == "claude-code" || provider == "codex-cli" || provider == "cursor-cli" || provider == "muse-cli" || provider == "pi-cli" || provider == "agy-cli"
}
