package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// vaultSlackIdentity stores non-secret workspace and user display metadata for
// a connected Vault Slack account so the UI, sidebar, and prompt can show which
// Slack workspace and handle are connected. It never stores any token.
type vaultSlackIdentity struct {
	TeamName   string `json:"team_name,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
	TeamDomain string `json:"team_domain,omitempty"`
	UserName   string `json:"user_name,omitempty"`
	UserID     string `json:"user_id,omitempty"`
}

func (id vaultSlackIdentity) empty() bool {
	return strings.TrimSpace(id.TeamName) == "" && strings.TrimSpace(id.UserName) == ""
}

// formatSlackIdentity returns a concise display string like "Acme (@alex)".
func formatSlackIdentity(id vaultSlackIdentity) string {
	team := strings.TrimSpace(id.TeamName)
	user := strings.TrimPrefix(strings.TrimSpace(id.UserName), "@")
	switch {
	case team != "" && user != "":
		return team + " (@" + user + ")"
	case team != "":
		return team
	case user != "":
		return "@" + user
	default:
		return ""
	}
}

func slackWorkspaceDomain(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(u.Hostname()))
}

var (
	slackAuthTestURL        = "https://slack.com/api/auth.test"
	slackIdentityHTTPClient = func() *http.Client { return netguard.Client(5 * time.Second) }
	vaultSlackIdentityMiss  sync.Map // connectionID -> time.Time of last failed lookup
)

const vaultSlackIdentityMissTTL = 2 * time.Minute

func vaultSlackIdentityPath(id string) string {
	return getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id)+".slack-identity")
}

func readVaultSlackIdentity(id string) vaultSlackIdentity {
	if !vaultConnectionID.MatchString(id) {
		return vaultSlackIdentity{}
	}
	data, err := oauth.ReadTokenFile(vaultSlackIdentityPath(id))
	if err != nil {
		return vaultSlackIdentity{}
	}
	var out vaultSlackIdentity
	if json.Unmarshal(data, &out) != nil {
		return vaultSlackIdentity{}
	}
	return out
}

func writeVaultSlackIdentity(id string, identity vaultSlackIdentity) {
	if !vaultConnectionID.MatchString(id) || identity.empty() {
		return
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return
	}
	_ = oauth.WriteTokenFile(vaultSlackIdentityPath(id), data)
	vaultSlackIdentityMiss.Delete(id)
}

func fetchSlackAuthTestIdentity(ctx context.Context, accessToken string) (vaultSlackIdentity, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return vaultSlackIdentity{}, errors.New("empty Slack access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAuthTestURL, nil)
	if err != nil {
		return vaultSlackIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := slackIdentityHTTPClient().Do(req)
	if err != nil {
		return vaultSlackIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return vaultSlackIdentity{}, errors.New("slack auth.test failed")
	}
	var result struct {
		OK     bool   `json:"ok"`
		Team   string `json:"team"`
		TeamID string `json:"team_id"`
		URL    string `json:"url"`
		User   string `json:"user"`
		UserID string `json:"user_id"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) != nil || !result.OK {
		return vaultSlackIdentity{}, errors.New("slack auth.test returned not ok")
	}
	out := vaultSlackIdentity{
		TeamName:   strings.TrimSpace(result.Team),
		TeamID:     strings.TrimSpace(result.TeamID),
		TeamDomain: slackWorkspaceDomain(result.URL),
		UserName:   strings.TrimSpace(result.User),
		UserID:     strings.TrimSpace(result.UserID),
	}
	if out.empty() {
		return vaultSlackIdentity{}, errors.New("slack auth.test returned empty identity")
	}
	return out, nil
}

func (api *StreamingAPI) vaultSlackAccessToken(ctx context.Context, id string) string {
	tokenFile := getUserTokenFilePath(platformMCPTokenUserID, vaultCredentialName(id))
	if tok, err := oauth.NewTokenStore(tokenFile).Load(); err == nil && tok != nil && strings.TrimSpace(tok.AccessToken) != "" && tok.Valid() {
		return tok.AccessToken
	}
	cfg, err := api.vaultOAuthConfig(ctx, id, "Slack", "")
	if err != nil || cfg.OAuth == nil {
		return ""
	}
	mutex := platformMCPOAuthMutex("vault:" + id)
	mutex.Lock()
	defer mutex.Unlock()
	if cfg, err = vaultSavedOAuthConfig(id, cfg); err != nil || cfg.OAuth == nil {
		return ""
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	token, err := oauth.NewManager(cfg.OAuth, api.logger).GetAccessToken(refreshCtx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

// refreshVaultSlackIdentity fetches fresh workspace/user metadata from Slack's
// auth.test endpoint and caches it on disk. Used after OAuth sign-in/reconnect.
func (api *StreamingAPI) refreshVaultSlackIdentity(ctx context.Context, id string) vaultSlackIdentity {
	if !vaultConnectionID.MatchString(id) {
		return vaultSlackIdentity{}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	token := api.vaultSlackAccessToken(fetchCtx, id)
	if token == "" {
		vaultSlackIdentityMiss.Store(id, time.Now())
		return vaultSlackIdentity{}
	}
	identity, err := fetchSlackAuthTestIdentity(fetchCtx, token)
	if err != nil {
		vaultSlackIdentityMiss.Store(id, time.Now())
		return vaultSlackIdentity{}
	}
	writeVaultSlackIdentity(id, identity)
	return identity
}

// vaultSlackIdentityCached returns the cached workspace/user metadata for a
// Vault Slack connection, lazily fetching it once when allowFetch is true.
func (api *StreamingAPI) vaultSlackIdentityCached(ctx context.Context, id string, allowFetch bool) vaultSlackIdentity {
	if cached := readVaultSlackIdentity(id); !cached.empty() {
		return cached
	}
	if !allowFetch || !vaultConnectionID.MatchString(id) {
		return vaultSlackIdentity{}
	}
	if last, ok := vaultSlackIdentityMiss.Load(id); ok && time.Since(last.(time.Time)) < vaultSlackIdentityMissTTL {
		return vaultSlackIdentity{}
	}
	return api.refreshVaultSlackIdentity(ctx, id)
}
