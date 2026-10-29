package server

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The OS user's gh account belongs only to the default owner of a local,
// single-user installation. A hosted server must never borrow its operator's login.
func localGitHubCLIAllowed(person string) bool {
	return localProductInstallation() && IsLocalMode() && !IsMultiUserMode() && person == GetDefaultUserID()
}

func githubCLIEnvironment() []string {
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG", "GH_PROMPT_DISABLED":
			continue
		}
		env = append(env, value)
	}
	return append(env, "GH_PROMPT_DISABLED=1")
}

var githubReadCLIToken = func(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com")
	cmd.Env = githubCLIEnvironment()
	output, err := cmd.Output()
	if err != nil {
		// Neither stdout, stderr nor the command error is safe to return: they
		// may contain credentials. Reading never changes gh's saved login.
		return "", errors.New("No saved gh login was found. Sign in with GitHub CLI or use a personal access token below.")
	}
	return strings.TrimSpace(string(output)), nil
}

func githubCLIAvailable(person string) bool {
	if !localGitHubCLIAllowed(person) {
		return false
	}
	_, err := exec.LookPath("gh")
	return err == nil
}

// Every connection source writes the same owner-scoped encrypted secret and
// account record. PR status and the existing Crew secret selection reuse it.
func (api *StreamingAPI) saveGitHubAccount(ctx context.Context, person, token, scopes, source string) (*githubAccountRecord, error) {
	if !personVaultActive(person) {
		return nil, errors.New("Your account cannot keep its own Vault items.")
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("Enter a valid GitHub access token.")
	}
	login, err := githubLogin(ctx, token)
	if err != nil {
		return nil, err
	}
	vaultID, secretName, err := githubStoreToken(ctx, api, person, token)
	if err != nil {
		log.Printf("[GITHUB_VAULT] saving the account failed person=%s", person)
		return nil, errors.New("Your GitHub account could not be saved. Try again.")
	}
	rec := githubAccountRecord{Login: login, Source: source, Scopes: scopes, VaultID: vaultID, SecretName: secretName, ConnectedAt: time.Now().UTC().Format(time.RFC3339)}
	// Organisation access is informational, never a condition of connecting.
	rec.Orgs, rec.NeedsApproval = githubOrgAccess(ctx, token)
	rec.OrgsCheckedAt = rec.ConnectedAt
	if err := writeGitHubRecord(person, rec); err != nil {
		return nil, errors.New("Your GitHub connection details could not be saved. Try again.")
	}
	log.Printf("[GITHUB_VAULT] connected person=%s github_login=%s source=%s", person, login, source)
	return &rec, nil
}

func (api *StreamingAPI) connectGitHubAccount(ctx context.Context, person, method, token, redirect string) (map[string]any, error) {
	if !personVaultActive(person) {
		return nil, errors.New("Your account cannot keep its own Vault items.")
	}
	switch method {
	case "oauth":
		link, err := startGitHubConnect(person, redirect)
		return map[string]any{"auth_url": link}, err
	case "gh":
		if !localGitHubCLIAllowed(person) {
			return nil, errors.New("Use a personal access token or browser sign-in here. This computer's gh login is available only to its local owner.")
		}
		var err error
		token, err = githubReadCLIToken(ctx)
		if err != nil {
			return nil, errors.New("No saved gh login could be read. Use a personal access token or sign in with GitHub CLI first.")
		}
	case "pat":
	default:
		return nil, errors.New("Choose gh, a personal access token (pat), or browser sign-in (oauth).")
	}
	rec, err := api.saveGitHubAccount(ctx, person, token, "", method)
	if err != nil {
		return nil, err
	}
	return map[string]any{"connected": true, "login": rec.Login, "source": rec.Source, "summary": githubAccessSummary(rec)}, nil
}

func githubDisconnectNote(source string, revoked bool) string {
	switch source {
	case "gh":
		return "Disconnected from AgentWorks. Your gh login is unchanged."
	case "pat":
		return "Disconnected from AgentWorks. The token itself is unchanged; you can revoke it in GitHub settings."
	}
	if revoked {
		return "GitHub disconnected."
	}
	return "Removed from AgentWorks. You can also remove the app in your GitHub settings."
}
