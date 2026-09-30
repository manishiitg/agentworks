package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

// Sign-in apps (docs/design/code_private_mcp.md, "Sign-in apps"). Providers
// without automatic app registration (Google, GitHub, Slack, ...) need an
// OAuth app. An admin sets one up per provider once for the deployment; every
// person's Connect then goes straight to the provider's consent screen and
// signs in as themselves. The app only identifies this server to the provider.
//
// The app (client ID and secret) is kept in one sealed file under the tokens
// root (the credential sealer covers it, bound to its path); a personal
// server records only the app's key and reads the app when it connects or
// refreshes, so rotating the app reaches everyone. A client a person enters
// for their own app always wins.

var mcpAppKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// mcpAppKeyFor groups catalog servers that share one provider app: every
// Google server uses the same OAuth app, as do a provider's other servers.
// Anything else is its own app, named after the catalog entry.
func mcpAppKeyFor(catalogName string, cfg *oauth.OAuthConfig) string {
	if cfg != nil {
		// The parsed host, never a substring: "notslack.com" is not Slack.
		if u, err := url.Parse(cfg.AuthURL); err == nil && u.Scheme == "https" {
			host := strings.ToLower(u.Hostname())
			switch {
			case host == "accounts.google.com":
				return "google"
			case host == "github.com" && strings.HasPrefix(u.Path, "/login/oauth"):
				return "github"
			case host == "slack.com" || strings.HasSuffix(host, ".slack.com"):
				return "slack"
			}
		}
	}
	key := strings.Trim(personalMCPCatalogNameCleaner.ReplaceAllString(strings.ToLower(catalogName), "_"), "_")
	if len(key) > 40 {
		key = key[:40]
	}
	return key
}

var mcpAppLabels = map[string]string{"google": "Google", "github": "GitHub", "slack": "Slack"}

// mcpAppFocusKeys are the only providers whose sign-in app an admin manages here. The
// product focuses on Google apps (Gmail, Drive, Calendar, ...) and GitHub (owner
// decision 2026-09-30); listing every provider that has no automatic registration
// (Slack, Atlassian, ...) only added cards nobody sets up. A connector of another
// provider still works if the person brings their own OAuth app when connecting.
var mcpAppFocusKeys = map[string]bool{"google": true, "github": true}

// mcpApp is what an admin stores for one provider.
type mcpApp struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	UpdatedBy    string `json:"updated_by,omitempty"`
}

func mcpAppFile(key string) (string, error) {
	if !mcpAppKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid app name")
	}
	return filepath.Join(mcpagentTokensRoot(), platformMCPTokenUserID, "apps", key+".json"), nil
}

// readMCPApp returns the deployment's app for a provider, or nil when none
// has been set up.
func readMCPApp(key string) (*mcpApp, error) {
	path, err := mcpAppFile(key)
	if err != nil {
		return nil, err
	}
	data, err := oauth.ReadTokenFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sign-in app: %w", err)
	}
	var app mcpApp
	if err := json.Unmarshal(data, &app); err != nil || app.ClientID == "" {
		return nil, fmt.Errorf("unreadable sign-in app")
	}
	return &app, nil
}

// writeMCPApp seals the app for its own path and replaces the file
// atomically (temp file in the same folder, then rename), so a read during a
// save sees the old app or the new one, never a torn file.
func writeMCPApp(key string, app mcpApp) error {
	path, err := mcpAppFile(key)
	if err != nil {
		return err
	}
	data, err := json.Marshal(app)
	if err != nil {
		return err
	}
	sealed, err := (platformClientSealer{}).Seal(path, data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".app-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; cleans up on failure
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(sealed); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// mcpAppKeyIndex maps each catalog server to its sign-in app key, leaving out
// a server whose key is shared with another server that signs in at a
// different token URL (two catalog names can clean to one key): one app's
// secret must never go to another provider's token endpoint.
func mcpAppKeyIndex(servers map[string]mcpclient.MCPServerConfig) map[string]string {
	tokenURLs := map[string]map[string]bool{}
	keys := map[string]string{}
	for name, cfg := range servers {
		if cfg.OAuth == nil {
			continue
		}
		key := mcpAppKeyFor(name, cfg.OAuth)
		keys[name] = key
		if tokenURLs[key] == nil {
			tokenURLs[key] = map[string]bool{}
		}
		tokenURLs[key][cfg.OAuth.TokenURL] = true
	}
	for name, key := range keys {
		if len(tokenURLs[key]) > 1 {
			keys[name] = ""
		}
	}
	return keys
}

// mcpAppGroup is one provider's servers as the admin card lists them.
type mcpAppGroup struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Servers    []string `json:"servers"`
	Configured bool     `json:"configured"`
	ClientID   string   `json:"client_id,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	// Required: the provider has no dynamic registration, so people cannot
	// connect without an app (their own or this one).
	Required bool `json:"required"`
}

// mcpAppGroups lists the providers of the catalog's remote sign-in servers
// that need an app, and whether the deployment has set one up.
func (api *StreamingAPI) mcpAppGroups() []mcpAppGroup {
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil
	}
	return mcpAppGroupsFor(catalog.MCPServers)
}

// mcpAppGroupsFor groups a catalog's sign-in servers by the app they need and
// marks which of those apps the deployment has set up.
func mcpAppGroupsFor(servers map[string]mcpclient.MCPServerConfig) []mcpAppGroup {
	byKey := map[string]*mcpAppGroup{}
	keys := mcpAppKeyIndex(servers)
	for name, cfg := range servers {
		if cfg.OAuth == nil || cfg.URL == "" || len(cfg.Headers) > 0 {
			continue
		}
		if cfg.OAuth.RegistrationEndpoint != "" || cfg.OAuth.ClientID != "" {
			continue // registers itself, or carries its own app
		}
		key := keys[name]
		if key == "" {
			continue // its key is shared with a server that signs in elsewhere
		}
		if !mcpAppFocusKeys[key] {
			continue // only Google and GitHub sign-in apps are managed here
		}
		group := byKey[key]
		if group == nil {
			label := mcpAppLabels[key]
			if label == "" {
				label = name
			}
			group = &mcpAppGroup{Key: key, Label: label, Required: true}
			byKey[key] = group
		}
		group.Servers = append(group.Servers, name)
	}
	out := make([]mcpAppGroup, 0, len(byKey))
	for key, group := range byKey {
		sort.Strings(group.Servers)
		if app, err := readMCPApp(key); err == nil && app != nil {
			group.Configured, group.ClientID, group.UpdatedAt = true, app.ClientID, app.UpdatedAt
		}
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// GET /api/admin/mcp-apps: the providers that need a sign-in app, whether one
// is set up, and the callback URL to register on it. Never the secret.
func (api *StreamingAPI) handleListMCPApps(w http.ResponseWriter, r *http.Request) {
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{
		"apps":         api.mcpAppGroups(),
		"redirect_uri": deriveOAuthRedirectURI(r),
	})
}

// PUT /api/admin/mcp-apps/{key} {client_id, client_secret}; DELETE removes it.
func (api *StreamingAPI) handlePutMCPApp(w http.ResponseWriter, r *http.Request) {
	key := strings.ToLower(strings.TrimSpace(mux.Vars(r)["key"]))
	known := false
	for _, group := range api.mcpAppGroups() {
		if group.Key == key {
			known = true
		}
	}
	// Removing is allowed for any valid key, so a stored app can always be
	// cleared even after its provider leaves the catalog.
	if !known && r.Method != http.MethodDelete {
		writeAgentProfileError(w, http.StatusNotFound, "no sign-in app is needed for that provider")
		return
	}
	if r.Method == http.MethodDelete {
		path, err := mcpAppFile(key)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
			return
		}
		api.closePersonalConnectionsForApp(key)
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"removed": key})
		return
	}
	var body struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeAgentProfileError(w, http.StatusBadRequest, "invalid request")
		return
	}
	app := mcpApp{ClientID: strings.TrimSpace(body.ClientID), ClientSecret: strings.TrimSpace(body.ClientSecret),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339), UpdatedBy: sanitizeUserIDForPath(strings.TrimSpace(GetUserIDFromContext(r.Context())))}
	if app.ClientID == "" || len(app.ClientID) > 300 || strings.ContainsAny(app.ClientID, " \r\n\t") {
		writeAgentProfileError(w, http.StatusBadRequest, "enter the app's client ID")
		return
	}
	if app.ClientSecret == "" || len(app.ClientSecret) > 300 {
		writeAgentProfileError(w, http.StatusBadRequest, "enter the app's client secret")
		return
	}
	if err := writeMCPApp(key, app); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A rotated app reaches everyone: pooled connections reconnect with it.
	api.closePersonalConnectionsForApp(key)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"saved": key, "client_id": app.ClientID})
}

// closePersonalConnectionsForApp drops the pooled connections of every
// personal server that uses the app, so they reconnect with the current one.
func (api *StreamingAPI) closePersonalConnectionsForApp(key string) {
	root, err := personalMCPRoot()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var servers []personalMCPServer
		if readPersonalMCPJSON(filepath.Join(root, entry.Name(), "servers.json"), &servers) != nil {
			continue
		}
		for _, server := range servers {
			if server.AppKey == key {
				mcpclient.GetSessionRegistry().CloseSessionServer("global", "u"+entry.Name()+"__"+server.Name)
			}
		}
	}
}

// mcpAppKeyOf is a catalog server's sign-in app key, or "" when it has none
// or its key is shared with a server at another token URL.
func (api *StreamingAPI) mcpAppKeyOf(catalogName string) string {
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return ""
	}
	return mcpAppKeyIndex(catalog.MCPServers)[catalogName]
}
