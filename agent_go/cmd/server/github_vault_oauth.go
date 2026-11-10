package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/oauth"
)

// A person's own GitHub account through the deployment's GitHub OAuth App (PLAT-746-158 B8; the decisions log, 2026-10-28).
// An admin stores the app once (sign-in app "github"); a person connects their own GitHub account in the browser.
// Connections are reusable personal Vault items. Each Crew explicitly selects
// one connection; no person-wide default is injected into other Crews.
//
// The browser comes back from GitHub without our sign-in (it is a header, not a cookie), so the callback only keeps
// GitHub's one-time code; the signed-in app then completes, and only for the person who started the connection.
// Nothing here logs a code, a token or GitHub's response body.

const (
	githubAppKey       = "github"
	githubStatePrefix  = "ghv_"
	githubConnectTTL   = 10 * time.Minute
	githubCompleteParm = "github_connect"
	// repo: clone, push and open pull requests on private repositories; read:org: see organisation repositories and
	// membership (gh pr and repo commands); workflow: push changes to .github/workflows files.
	githubOAuthScopes = "repo read:org workflow"
)

var (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubAPIURL       = "https://api.github.com"
	githubHTTPClient   = &http.Client{Timeout: 20 * time.Second}
	// githubStoreToken saves the token as the person's own Vault secret and returns its vault and full secret name.
	githubStoreToken = func(ctx context.Context, api *StreamingAPI, person, token string) (string, string, error) {
		vaultID, err := ensurePersonalVault(ctx, person)
		if err != nil {
			return "", "", err
		}
		secretName, _ := ctx.Value(githubConnectionSecretKey{}).(string)
		if secretName == "" {
			return "", "", errors.New("Choose a GitHub connection before saving it.")
		}
		if err := api.setVaultSecret(ctx, person, vaultID, secretName, token, true); err != nil {
			return "", "", err
		}
		full, err := vaultSecretName(vaultID, secretName)
		return vaultID, full, err
	}
	// githubTokenValue reads the stored token, to revoke it at GitHub.
	githubTokenValue = func(rec *githubAccountRecord) string {
		for _, secret := range getGlobalSecrets() {
			if secret.Name == rec.SecretName {
				return secret.Value
			}
		}
		return ""
	}
)

type githubPendingConnect struct {
	person, redirect, code, providerError string
	workspace                             string
	projectID                             string
	created                               time.Time
}

var githubPending = struct {
	sync.Mutex
	m map[string]*githubPendingConnect
}{m: map[string]*githubPendingConnect{}}

// githubAccountRecord is what status shows; it never holds the token.
type githubAccountRecord struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	Source      string `json:"source,omitempty"`
	Scopes      string `json:"scopes"`
	VaultID     string `json:"vault_id"`
	SecretName  string `json:"secret_name"`
	ConnectedAt string `json:"connected_at"`
	// Orgs the account reaches through this app, and those that need an approval first (a refreshed cache).
	Orgs          []string         `json:"orgs,omitempty"`
	NeedsApproval []githubOrgBlock `json:"needs_approval,omitempty"`
	OrgsCheckedAt string           `json:"orgs_checked_at,omitempty"`
}

// githubOrgBlock is an organisation the account belongs to that the app cannot reach yet: "approval" when the org
// restricts OAuth Apps (an org owner approves the app once; the person can click Request on GitHub), "sso" when it
// uses SAML single sign-on (the person authorizes the token for it on GitHub).
type githubOrgBlock struct {
	Org string `json:"org"`
	Why string `json:"why"`
}

const githubOrgRefresh = 5 * time.Minute

// githubOrgCheckBudget bounds the whole organisation check, so a slow GitHub cannot hold up a status reply.
var githubOrgCheckBudget = 5 * time.Second

// githubOrgAccess lists the person's organisations and checks each with one cheap call, a few at a time within
// githubOrgCheckBudget; an organisation not answered in time is reported as "unchecked". It never logs the token.
func githubOrgAccess(ctx context.Context, token string) (reach []string, blocked []githubOrgBlock) {
	ctx, cancel := context.WithTimeout(ctx, githubOrgCheckBudget)
	defer cancel()
	get := func(path string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		return githubHTTPClient.Do(req)
	}
	resp, err := get("/user/memberships/orgs?per_page=50")
	if err != nil {
		return nil, nil
	}
	var memberships []struct {
		Organization struct {
			Login string `json:"login"`
		} `json:"organization"`
	}
	ok := resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&memberships) == nil
	resp.Body.Close()
	if !ok {
		log.Printf("[GITHUB_VAULT] organisation list failed status=%d", resp.StatusCode)
		return nil, nil
	}
	// why: "" reachable, "sso", "approval", "unchecked" (no answer within the budget), "other" (not shown).
	whys := make([]string, len(memberships))
	slots := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, m := range memberships {
		whys[i] = "unchecked"
		if m.Organization.Login == "" {
			continue
		}
		wg.Add(1)
		go func(i int, org string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			check, err := get("/orgs/" + url.PathEscape(org) + "/repos?per_page=1")
			if err != nil {
				return
			}
			body, _ := io.ReadAll(io.LimitReader(check.Body, 8<<10))
			check.Body.Close()
			switch {
			case check.StatusCode == http.StatusOK:
				whys[i] = ""
			case check.Header.Get("X-GitHub-SSO") != "":
				whys[i] = "sso"
			case check.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(string(body)), "access restrictions"):
				whys[i] = "approval"
			default:
				whys[i] = "other"
			}
		}(i, m.Organization.Login)
	}
	wg.Wait()
	for i, m := range memberships {
		switch org := m.Organization.Login; {
		case org == "" || whys[i] == "other":
		case whys[i] == "":
			reach = append(reach, org)
		default:
			blocked = append(blocked, githubOrgBlock{Org: org, Why: whys[i]})
		}
	}
	return reach, blocked
}

// githubAccessSummary says in plain words where the account works and what is left to do.
func githubAccessSummary(rec *githubAccountRecord) string {
	parts := []string{"GitHub: " + rec.Login}
	works := append([]string{}, rec.Orgs...)
	works = append(works, "your own repositories")
	if rec.Source == "pat" || rec.Source == "gh" {
		parts = append(parts, "repository access follows this token's permissions")
	} else {
		parts = append(parts, "works in: "+strings.Join(works, ", "))
	}
	var approval, sso, unchecked []string
	for _, b := range rec.NeedsApproval {
		switch b.Why {
		case "sso":
			sso = append(sso, b.Org)
		case "unchecked":
			unchecked = append(unchecked, b.Org)
		default:
			approval = append(approval, b.Org)
		}
	}
	if len(approval) > 0 {
		parts = append(parts, "needs approval: "+strings.Join(approval, ", ")+" (ask an owner of that organisation to approve AgentWorks, or click Request next to it on GitHub's page for this app)")
	}
	if len(sso) > 0 {
		parts = append(parts, "needs single sign-on: "+strings.Join(sso, ", ")+" (on GitHub's page for this app, authorize it for that organisation's single sign-on)")
	}
	if len(unchecked) > 0 {
		parts = append(parts, "not checked yet: "+strings.Join(unchecked, ", ")+" (GitHub was slow; checked again next time)")
	}
	return strings.Join(parts, " · ")
}

func writeGitHubRecord(person string, rec githubAccountRecord) error {
	path := githubConnectionPath(person, rec.ID)
	if path == "" {
		return errors.New("Invalid GitHub connection.")
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return oauth.WriteTokenFile(path, data)
}

func githubApp() (clientID, clientSecret string, ok bool) {
	app, err := readMCPApp(githubAppKey)
	if err != nil || app == nil || strings.TrimSpace(app.ClientID) == "" || strings.TrimSpace(app.ClientSecret) == "" {
		return "", "", false
	}
	return strings.TrimSpace(app.ClientID), strings.TrimSpace(app.ClientSecret), true
}

const githubNoAppMessage = "Browser sign-in is not configured. Use an existing gh login on your local installation, or add a personal access token. OAuth setup is optional."

// startGitHubConnect returns the GitHub page where the person approves the connection.
func startGitHubConnect(person, redirect, root, projectID string) (string, error) {
	if !personVaultActive(person) {
		return "", errors.New("Your account cannot keep its own Vault items.")
	}
	clientID, _, ok := githubApp()
	if !ok {
		return "", errors.New(githubNoAppMessage)
	}
	if redirect == "" {
		return "", errors.New("This server has no public address set, so GitHub cannot send you back. Ask an administrator.")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := githubStatePrefix + hex.EncodeToString(raw)
	githubPending.Lock()
	for key, pending := range githubPending.m {
		if time.Since(pending.created) > githubConnectTTL {
			delete(githubPending.m, key)
		}
	}
	if root == "" {
		githubPending.Unlock()
		return "", errors.New("Choose a Crew for this GitHub connection.")
	}
	githubPending.m[state] = &githubPendingConnect{person: person, redirect: redirect, workspace: root, projectID: projectID, created: time.Now()}
	githubPending.Unlock()
	query := url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "scope": {githubOAuthScopes}, "state": {state}, "allow_signup": {"false"}}
	log.Printf("[GITHUB_VAULT] connect started person=%s", person)
	return githubAuthorizeURL + "?" + query.Encode(), nil
}

// handleGitHubOAuthCallback keeps GitHub's one-time code for the connection the state names and sends the browser
// back into the app, which completes it signed in. It stores no token.
func (api *StreamingAPI) handleGitHubOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	githubPending.Lock()
	pending := githubPending.m[state]
	if pending != nil && (time.Since(pending.created) > githubConnectTTL || pending.code != "" || pending.providerError != "") {
		delete(githubPending.m, state)
		pending = nil
	}
	if pending != nil {
		pending.code = r.URL.Query().Get("code")
		pending.providerError = r.URL.Query().Get("error")
		if pending.code == "" && pending.providerError == "" {
			pending.providerError = "no_code"
		}
	}
	githubPending.Unlock()
	if pending == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>GitHub</title></head><body style="font-family: sans-serif; text-align: center; padding: 48px;"><h1>This GitHub link has expired</h1><p>Start again with Connect GitHub in your Vault.</p></body></html>`)
		return
	}
	http.Redirect(w, r, "/?"+githubCompleteParm+"="+url.QueryEscape(state), http.StatusFound)
}

// completeGitHubConnect finishes a connection for the signed-in person. Only the person who started it may finish
// it: a link sent to a colleague must not put the colleague's GitHub account into the starter's vault.
func (api *StreamingAPI) completeGitHubConnect(ctx context.Context, person, state string) (*githubAccountRecord, error) {
	githubPending.Lock()
	pending := githubPending.m[state]
	if pending != nil && time.Since(pending.created) > githubConnectTTL {
		delete(githubPending.m, state)
		pending = nil
	}
	if pending == nil {
		githubPending.Unlock()
		return nil, errors.New("This GitHub connection has expired. Start again with Connect GitHub.")
	}
	if pending.person != person {
		// The rightful person can still finish it.
		githubPending.Unlock()
		name := pending.person
		if claims := userClaimsForDirectoryID(pending.person); claims != nil && claims.Username != "" {
			name = claims.Username
		}
		log.Printf("[GITHUB_VAULT] completion refused: signed-in person=%s is not the person who started it", person)
		return nil, fmt.Errorf("This GitHub connection was started by %s. Sign in as %s and try again.", name, name)
	}
	if pending.code == "" && pending.providerError == "" {
		githubPending.Unlock()
		return nil, errors.New("GitHub has not sent you back yet. Approve the connection on GitHub first.")
	}
	delete(githubPending.m, state)
	githubPending.Unlock()
	if pending.providerError != "" {
		log.Printf("[GITHUB_VAULT] connect not approved person=%s github_error=%s", person, pending.providerError)
		return nil, errors.New("GitHub did not connect your account. Try again, and choose Authorize on GitHub.")
	}
	clientID, clientSecret, ok := githubApp()
	if !ok {
		return nil, errors.New(githubNoAppMessage)
	}
	ctx, scopeErr := githubProjectContext(ctx, person, pending.workspace, true)
	if scopeErr != nil {
		return nil, scopeErr
	}
	currentID, err := githubProjectID(ctx, pending.workspace)
	if err != nil || currentID != pending.projectID {
		return nil, errors.New("The project where GitHub sign-in started has changed. Start again from that project.")
	}
	token, scopes, err := exchangeGitHubCode(ctx, clientID, clientSecret, pending.code, pending.redirect)
	if err != nil {
		return nil, err
	}
	return api.saveGitHubAccount(ctx, person, token, scopes, "oauth", "")
}

func exchangeGitHubCode(ctx context.Context, clientID, clientSecret, code, redirect string) (token, scopes string, err error) {
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "code": {code}, "redirect_uri": {redirect}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return "", "", errors.New("GitHub could not be reached. Try again.")
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil || resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		log.Printf("[GITHUB_VAULT] code exchange failed status=%d github_error=%s", resp.StatusCode, out.Error)
		return "", "", errors.New("GitHub did not accept the connection. Start again with Connect GitHub.")
	}
	return out.AccessToken, out.Scope, nil
}

func githubLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL+"/user", nil)
	if err != nil {
		return "", errors.New("GitHub could not be reached. Try again.")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return "", errors.New("GitHub could not be reached. Try again.")
	}
	defer resp.Body.Close()
	var out struct {
		Login   string `json:"login"`
		Message string `json:"message"`
	}
	decoded := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) == nil
	if resp.StatusCode != http.StatusOK || !decoded || out.Login == "" {
		// GitHub's own short answer ("Bad credentials", "Requires authentication") is shown with the explanation,
		// so the person sees what GitHub said. It never contains the token.
		said := strings.TrimSpace(out.Message)
		if token != "" && strings.Contains(said, token) {
			said = "" // never repeat a credential, whatever the server answered
		}
		if len(said) > 120 {
			said = said[:120]
		}
		log.Printf("[GITHUB_VAULT] account lookup failed status=%d github_message=%q", resp.StatusCode, said)
		detail := ""
		if said != "" {
			detail = fmt.Sprintf(" (GitHub said: %s)", said)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// GitHub does not recognise the token: a partial copy, an expired or deleted token. Retrying cannot help.
			return "", fmt.Errorf("GitHub did not accept this token%s. Check that you copied the whole token and that it has not expired, or create a new one.", detail)
		}
		return "", fmt.Errorf("GitHub did not return your account%s. Try again.", detail)
	}
	return out.Login, nil
}

// gitHubStatus is what the project's Vault panel and chat show: whether GitHub can be connected here, which account
// is, and which of its organisations it reaches (refreshed when the cached check is older than a few minutes).
func gitHubStatus(ctx context.Context, person string) map[string]any {
	clientID, _, ready := githubApp()
	out := map[string]any{"app_ready": ready, "connected": false, "gh_available": githubCLIAvailable(person), "method_order": []string{"gh", "pat", "oauth"}}
	if out["gh_available"] == true {
		accounts, err := githubReadCLIAccounts(ctx)
		out["gh_accounts"] = accounts
		if err != nil {
			out["gh_accounts_note"] = err.Error()
		}
	}
	if rec, _ := projectGitHubConnection(ctx, person, githubProjectRoot(ctx)); rec != nil {
		if checked, err := time.Parse(time.RFC3339, rec.OrgsCheckedAt); err != nil || time.Since(checked) > githubOrgRefresh {
			if token := githubTokenValue(rec); token != "" {
				rec.Orgs, rec.NeedsApproval = githubOrgAccess(ctx, token)
				rec.OrgsCheckedAt = time.Now().UTC().Format(time.RFC3339)
				_ = writeGitHubRecord(person, *rec)
			}
		}
		out["connected"], out["login"], out["scopes"], out["connected_at"] = true, rec.Login, rec.Scopes, rec.ConnectedAt
		out["orgs"], out["needs_approval"], out["summary"] = rec.Orgs, rec.NeedsApproval, githubAccessSummary(rec)
		out["source"] = rec.Source
		if len(rec.NeedsApproval) > 0 && clientID != "" && rec.Source != "pat" && rec.Source != "gh" {
			out["approval_page"] = "https://github.com/settings/connections/applications/" + url.PathEscape(clientID)
		}
	}
	connections := []map[string]string{}
	for _, rec := range gitHubConnections(person) {
		connections = append(connections, map[string]string{"id": rec.ID, "login": rec.Login, "source": rec.Source})
	}
	out["connections"] = connections
	if rec, err := projectGitHubConnection(ctx, person, githubProjectRoot(ctx)); err != nil {
		out["selection_error"] = err.Error()
	} else if rec != nil {
		out["connection_id"] = rec.ID
	}
	if !ready {
		out["next"] = githubNoAppMessage
	}
	return out
}

// Disconnecting a project unselects its account; it never revokes a reusable
// connection or removes credentials that another project may still use.
func (api *StreamingAPI) disconnectGitHub(ctx context.Context, person string) (bool, error) {
	return false, selectProjectGitHubConnection(ctx, person, "")
}

// HTTP: GET status, POST connect, POST complete, DELETE disconnect, for the signed-in person.
func (api *StreamingAPI) handleGitHubVault(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil {
		writeAgentProfileError(w, http.StatusForbidden, "Sign in to AgentWorks to manage your GitHub account.")
		return
	}
	person := claims.UserID
	if !strings.HasSuffix(r.URL.Path, "/complete") {
		ctx, err := githubProjectContext(r.Context(), person, r.URL.Query().Get("workspace_path"), r.Method != http.MethodGet)
		if err != nil {
			writeAgentProfileError(w, http.StatusForbidden, err.Error())
			return
		}
		r = r.WithContext(ctx)
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet:
		if r.URL.Query().Get("view") == "summary" {
			// The menu shows the saved connection. Account choices and fresh
			// organization checks belong to the full connection settings page.
			out := map[string]any{"connected": false}
			if rec, _ := projectGitHubConnection(r.Context(), person, githubProjectRoot(r.Context())); rec != nil {
				out["connected"], out["login"] = true, rec.Login
			}
			writeAgentProfileJSON(w, http.StatusOK, out)
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, gitHubStatus(r.Context(), person))
	case r.Method == http.MethodDelete:
		revoked, err := api.disconnectGitHub(r.Context(), person)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"removed": true, "revoked_at_github": revoked, "message": "Disconnected from this project. Saved connections and other projects are unchanged."})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
		var body struct {
			Method       string `json:"method"`
			Login        string `json:"login"`
			Token        string `json:"token"`
			ConnectionID string `json:"connection_id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body) != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "Choose a GitHub connection method.")
			return
		}
		if body.Method == "saved" {
			if err := selectProjectGitHubConnection(r.Context(), person, body.ConnectionID); err != nil {
				writeAgentProfileError(w, 400, err.Error())
				return
			}
			writeAgentProfileJSON(w, 200, gitHubStatus(r.Context(), person))
			return
		}
		result, err := api.connectGitHubAccount(r.Context(), person, body.Method, body.Token, body.Login, deriveOAuthRedirectURI(r))
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, result)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete"):
		var body struct {
			State string `json:"state"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body) != nil || !strings.HasPrefix(body.State, githubStatePrefix) {
			writeAgentProfileError(w, http.StatusBadRequest, "This GitHub connection link is not valid.")
			return
		}
		rec, err := api.completeGitHubConnect(r.Context(), person, body.State)
		if err != nil {
			writeAgentProfileError(w, http.StatusForbidden, err.Error())
			return
		}
		writeAgentProfileJSON(w, http.StatusOK, map[string]any{"connected": true, "login": rec.Login, "scopes": rec.Scopes, "summary": githubAccessSummary(rec)})
	default:
		writeAgentProfileError(w, http.StatusMethodNotAllowed, "unsupported")
	}
}

// githubVaultOperation is the same for chat and MCP (vault action=github_status, github_connect, github_disconnect).
func (api *StreamingAPI) githubVaultOperation(ctx context.Context, person, operation string, args map[string]interface{}) (string, error) {
	var out any
	var err error
	ctx, err = githubProjectContext(ctx, person, argString(args, "workspace_path"), operation != "github_status")
	if err != nil {
		return "", err
	}
	switch operation {
	case "github_status":
		out = gitHubStatus(ctx, person)
	case "github_connect":
		if argString(args, "method") == "saved" {
			if err := selectProjectGitHubConnection(ctx, person, argString(args, "connection_id")); err != nil {
				return "", err
			}
			out = gitHubStatus(ctx, person)
			break
		}
		var err error
		out, err = api.connectGitHubAccount(ctx, person, argString(args, "method"), argString(args, "token"), argString(args, "login"), api.mcpToolOAuthRedirect(ctx, person))
		if err != nil {
			return "", err
		}
	case "github_disconnect":
		revoked, err := api.disconnectGitHub(ctx, person)
		if err != nil {
			return "", err
		}
		out = map[string]any{"removed": true, "revoked_at_github": revoked, "next": "Disconnected from this project. Saved connections and other projects are unchanged."}
	default:
		return "", fmt.Errorf("unknown GitHub operation")
	}
	data, err := json.Marshal(out)
	return string(data), err
}
