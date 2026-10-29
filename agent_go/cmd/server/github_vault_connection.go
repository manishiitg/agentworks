package server

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"regexp"
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
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG", "GH_PROMPT_DISABLED", "GH_FORCE_TTY":
			continue
		}
		env = append(env, value)
	}
	return append(env, "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
}

var githubCLIAccountLine = regexp.MustCompile(`Logged in to github\.com account ([A-Za-z0-9][A-Za-z0-9-]{0,38})(?:\s|$)`)

// Only account names leave this reader. Raw CLI output/errors may contain
// credential details and must never reach a response or log.
var githubReadCLIAccounts = func(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", "github.com")
	cmd.Env = githubCLIEnvironment()
	output, _ := cmd.CombinedOutput()
	accounts := []string{}
	seen := map[string]bool{}
	for _, match := range githubCLIAccountLine.FindAllStringSubmatch(string(output), -1) {
		login := match[1]
		if !seen[strings.ToLower(login)] {
			accounts = append(accounts, login)
			seen[strings.ToLower(login)] = true
		}
	}
	if len(accounts) == 0 {
		return accounts, errors.New("No signed-in GitHub CLI accounts were found. Sign in with GitHub CLI or use a personal access token.")
	}
	return accounts, nil
}

var githubReadCLIToken = func(ctx context.Context, login string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com", "--user", login)
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
func (api *StreamingAPI) saveGitHubAccount(ctx context.Context, person, token, scopes, source, expectedLogin string) (*githubAccountRecord, error) {
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
	if expectedLogin != "" && !strings.EqualFold(login, expectedLogin) {
		return nil, errors.New("GitHub returned a different account. Choose your account again.")
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

func (api *StreamingAPI) connectGitHubAccount(ctx context.Context, person, method, token, login, redirect string) (map[string]any, error) {
	if !personVaultActive(person) {
		return nil, errors.New("Your account cannot keep its own Vault items.")
	}
	expectedLogin := ""
	switch method {
	case "oauth":
		link, err := startGitHubConnect(person, redirect)
		return map[string]any{"auth_url": link}, err
	case "gh":
		if !localGitHubCLIAllowed(person) {
			return nil, errors.New("Use a personal access token or browser sign-in here. This computer's gh login is available only to its local owner.")
		}
		accounts, err := githubReadCLIAccounts(ctx)
		if err != nil {
			return nil, err
		}
		login = strings.TrimSpace(login)
		if login == "" && len(accounts) == 1 {
			login = accounts[0]
		}
		for _, account := range accounts {
			if strings.EqualFold(account, login) {
				expectedLogin = account
				break
			}
		}
		if expectedLogin == "" {
			return nil, errors.New("Choose which GitHub CLI account to use.")
		}
		token, err = githubReadCLIToken(ctx, expectedLogin)
		if err != nil {
			return nil, errors.New("No saved gh login could be read. Use a personal access token or sign in with GitHub CLI first.")
		}
	case "pat":
	default:
		return nil, errors.New("Choose gh, a personal access token (pat), or browser sign-in (oauth).")
	}
	rec, err := api.saveGitHubAccount(ctx, person, token, "", method, expectedLogin)
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
