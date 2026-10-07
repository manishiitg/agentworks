package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// Providers that rejected this server's dynamic client registration (PLAT-708). Vercel only accepts known clients'
// callbacks, so a hosted server can never self-register there; Vault's Add app marks such apps "Needs admin setup"
// before anyone picks them. Keyed by registration endpoint and callback, because a rejection is about that pair; a
// later successful registration clears the entry. The record holds only the provider's public reason.

type registrationFailure struct {
	Reason string `json:"reason"`
	At     string `json:"at"`
}

var registrationFailuresMu sync.Mutex

func registrationFailuresPath() string {
	return filepath.Join(mcpagentTokensRoot(), platformMCPTokenUserID, "registration_failures.json")
}

func registrationFailureKey(endpoint, redirectURI string) string {
	return strings.TrimSpace(endpoint) + " " + strings.TrimSpace(redirectURI)
}

func readRegistrationFailuresLocked() map[string]registrationFailure {
	failures := map[string]registrationFailure{}
	if data, err := os.ReadFile(registrationFailuresPath()); err == nil {
		_ = json.Unmarshal(data, &failures)
	}
	return failures
}

func writeRegistrationFailuresLocked(failures map[string]registrationFailure) {
	path := registrationFailuresPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(failures)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// setRegistrationFailure records (reason != "") or clears (reason == "") a provider's rejection of this callback.
func setRegistrationFailure(endpoint, redirectURI, reason string) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(redirectURI) == "" {
		return
	}
	registrationFailuresMu.Lock()
	defer registrationFailuresMu.Unlock()
	failures := readRegistrationFailuresLocked()
	key := registrationFailureKey(endpoint, redirectURI)
	if _, had := failures[key]; reason == "" && !had {
		return
	}
	if reason == "" {
		delete(failures, key)
	} else {
		failures[key] = registrationFailure{Reason: reason, At: time.Now().UTC().Format(time.RFC3339)}
	}
	writeRegistrationFailuresLocked(failures)
}

func registrationFailed(endpoint, redirectURI string) bool {
	registrationFailuresMu.Lock()
	defer registrationFailuresMu.Unlock()
	_, failed := readRegistrationFailuresLocked()[registrationFailureKey(endpoint, redirectURI)]
	return failed
}

// oauthNeedsAdminSetup reports whether signing in to the named catalog server cannot start on its own here: no client
// is configured (in the config or as the deployment's sign-in app) and the provider either has no dynamic
// registration, needs a hand-registered confidential client, or already rejected this server's callback.
func oauthNeedsAdminSetup(config *mcpclient.MCPConfig, name, redirectURI string) bool {
	canonical, cfg, err := config.ResolveServer(name)
	if err != nil || cfg.OAuth == nil {
		return false
	}
	clientID, hasSecret := strings.TrimSpace(cfg.OAuth.ClientID), hasRegisteredMCPClientSecret(cfg.OAuth)
	if clientID == "" {
		if key := mcpAppKeyIndex(config.MCPServers)[canonical]; key != "" {
			if app, e := readMCPApp(key); e == nil && app != nil && app.ClientID != "" {
				clientID, hasSecret = app.ClientID, strings.TrimSpace(app.ClientSecret) != ""
			}
		}
	}
	if clientID != "" {
		return requiresRegisteredMCPClientSecret(canonical) && !hasSecret
	}
	if requiresRegisteredMCPClientSecret(canonical) || cfg.OAuth.RegistrationEndpoint == "" {
		return true
	}
	return registrationFailed(cfg.OAuth.RegistrationEndpoint, redirectURI)
}
