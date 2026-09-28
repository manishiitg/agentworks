package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Gmail is a single-user notification channel backed by the Google Workspace
// CLI (`gws`, https://github.com/googleworkspace/cli). Unlike Slack/WhatsApp
// it shells out to the CLI rather than using a Go SDK — `gws` owns the OAuth
// dance and stores encrypted credentials, so the server only needs an
// authenticated binary on the host.
//
// "Single user" means one shared Gmail account sends every notification. The
// account is whichever identity `gws` is authenticated as (via `gws auth
// login`, a service-account file, or a token env var). Per-request and
// per-user *recipient* routing still works through NotificationDestination /
// NotificationPreference, but all mail is sent FROM the one configured account.
//
// This connector is OUTBOUND-ONLY — it sends mail but does not read replies.
// Responses to feedback requests are collected through another channel.
//
// Config lives at <workspace-docs>/config/gmail-config.json and is loaded into
// memory on startup, mirroring the Slack config pattern.

func gmailConfigFilePath() string {
	return "config/gmail-config.json"
}

// GmailConfig is the on-disk configuration for the Gmail channel.
type GmailConfig struct {
	Enabled bool `json:"enabled"`

	// DefaultTo is the workspace-wide fallback recipient list (the equivalent of
	// Slack's default channel). Used when a notification carries no explicit
	// destination hint and the target user has no Gmail preference set. It may
	// name several addresses separated by commas; they are all addressed on the
	// same message.
	DefaultTo string `json:"default_to"`

	// BlockedRecipients is a denylist for outbound Gmail recipients. Explicit
	// destination hints, per-user Gmail preferences, To overrides, and CC
	// recipients matching this list are removed from the message; everyone else
	// on it still receives it. A message whose every recipient is blocked has
	// nowhere to go and is skipped.
	BlockedRecipients []string `json:"blocked_recipients,omitempty"`

	// GwsPath is the path to the gws binary. Empty means "gws" on $PATH.
	GwsPath string `json:"gws_path,omitempty"`

	// UseGogBackend switches every gws subprocess call (auth status, identity
	// lookup, send) to the gog CLI (gmail_gog.go) instead. Defaults to false
	// so existing deployments are unaffected until an operator explicitly
	// verifies a real send and opts in — this is a rollback switch during the
	// gws->gog migration, removed once every deployment has moved over
	// (see the migration plan's Phase 5).
	UseGogBackend bool `json:"use_gog_backend,omitempty"`
	// GogPath is the path to the gog binary. Empty means "gog" on $PATH.
	GogPath string `json:"gog_path,omitempty"`

	// Auth knobs (all optional). When set they are exported into the gws
	// child process environment so the server can pin a specific account
	// without relying on the invoking user's ~/.config/gws:
	//   ConfigHome      -> GOOGLE_WORKSPACE_CLI_CONFIG_DIR. gws does NOT read
	//                      XDG_CONFIG_HOME, and treats this as the config
	//                      directory itself, not a parent of one. Setting it
	//                      also pins the `file` keyring backend (see
	//                      gmailChildEnv) so the directory is a real account
	//                      boundary rather than a shared OS keyring.
	//   CredentialsFile -> GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE (a user or
	//                      service-account key; note a service account acts as
	//                      itself, as gws exposes no impersonation subject)
	//   Token           -> GOOGLE_WORKSPACE_CLI_TOKEN (pre-obtained token)
	// The OAuth *application* credential (client id/secret) is deliberately not
	// a field here: it is one registration shared by every account, and gws
	// takes it either from <config dir>/client_secret.json or from
	// GOOGLE_WORKSPACE_CLI_CLIENT_ID/_CLIENT_SECRET, which the child already
	// inherits from the server's own environment.
	ConfigHome      string `json:"config_home,omitempty"`
	CredentialsFile string `json:"credentials_file,omitempty"`
	Token           string `json:"token,omitempty"`

	// gog auth selectors (gmail_gog.go), populated only in-memory by
	// gmailConnectionConfig from the resolved connection's Email/ClientName —
	// never read from or written to disk. Used for --account/--client when
	// Token is unset (a connection migrated to gog's own stored refresh
	// token rather than a server-managed access token).
	gogAccountEmail string
	gogClientName   string
	// gogHome is the gog store this connection's account lives in: the
	// shared one, or a private connection's own (gogHomeForConnection).
	gogHome string
	// storedScopes carries the connection's consent-time scopes into the
	// status check, in-memory only like the selectors above. gog reports no
	// scope metadata for imported accounts, so the checked-account branch
	// falls back to these instead of reading empty as revoked.
	storedScopes []string

	// Connections is the multi-account registry (see gmail_connections.go).
	// The legacy auth fields above stay authoritative for delivery until the
	// send path resolves connections, so an un-migrated file keeps working and
	// adding connections changes nothing on its own.
	//
	// Recipient policy (DefaultTo, BlockedRecipients) stays account-wide and
	// deliberately does NOT move onto a connection: this registry governs who
	// mail is sent FROM, never who it goes TO.
	Connections []GmailConnection `json:"connections,omitempty"`

	// DefaultConnectionID names the connection used when a send selects none.
	// Empty means "no default": callers must fail rather than pick an
	// arbitrary account and send from the wrong identity.
	DefaultConnectionID string `json:"default_connection_id,omitempty"`

	// HostAccountDismissed records that the operator deleted the connection
	// AdoptHostAccount seeded from the host's own gws login, so an empty
	// registry is not re-seeded from it on the next listing.
	HostAccountDismissed bool `json:"host_account_dismissed,omitempty"`

	// ManuallyDisabled records that the operator switched the channel off in
	// the settings form. EnableIfAuthenticated then leaves it off: an
	// authenticated gws is otherwise reason enough to enable the channel
	// without a separate toggle, but not to override a deliberate choice.
	ManuallyDisabled bool `json:"manually_disabled,omitempty"`
}

// GmailService implements NotificationConnector (and UserNotificationConnector)
// by invoking `gws gmail +send`.
type GmailService struct {
	mu        sync.RWMutex
	config    *GmailConfig
	enabled   bool
	defaultTo string
	gwsPath   string
	// useGog and gogPath mirror gwsPath/gws usage for the gog backend
	// (gmail_gog.go), selected by GmailConfig.UseGogBackend.
	useGog  bool
	gogPath string

	// Cached `gws auth status` results, keyed by connection ID. That command
	// spawns a Node CLI and takes ~5.5s, and it is on the path of every
	// notification-settings read — so opening the Notify popup sat on a spinner
	// for the whole call. Auth changes only on an operator running
	// `gws auth login`/logout or a refresh token expiring, none of which are
	// sub-minute events.
	//
	// Keyed rather than scalar so one connection's expired credentials cannot
	// invalidate or disable another's. The empty-string key is the legacy
	// singleton config, which delivery still uses.
	authCaches map[string]*gmailAuthCacheEntry
}

// gmailAuthCacheEntry is one connection's cached auth status.
type gmailAuthCacheEntry struct {
	status     *GmailAuthStatus
	cachedAt   time.Time
	refreshing bool // a background refresh is already in flight for this key
}

// gmailAuthCacheTTL bounds how stale a cached auth status may be. Short enough
// that re-authenticating shows up almost immediately, long enough that opening
// a popup twice does not pay for two subprocesses.
const gmailAuthCacheTTL = 60 * time.Second

var (
	globalGmailService *GmailService
	gmailServiceMux    sync.RWMutex
)

// SetGmailService sets the global Gmail service instance.
func SetGmailService(service *GmailService) {
	gmailServiceMux.Lock()
	defer gmailServiceMux.Unlock()
	globalGmailService = service
}

// GetGmailService returns the global Gmail service instance (may be nil).
func GetGmailService() *GmailService {
	gmailServiceMux.RLock()
	defer gmailServiceMux.RUnlock()
	return globalGmailService
}

// InitGmailService initializes the Gmail service from the filesystem-backed
// config file. Called on server startup.
func InitGmailService() (*GmailService, error) {
	service := &GmailService{}
	if err := service.ReloadConfig(context.Background()); err != nil {
		SetGmailService(service)
		return service, err
	}
	SetGmailService(service)
	return service, nil
}

// loadGmailConfigFromDisk reads gmail-config.json from the workspace.
func loadGmailConfigFromDisk() (*GmailConfig, error) {
	ctx := context.Background()
	data, exists, err := readWorkspaceFile(ctx, workspaceAPIURL(), gmailConfigFilePath())
	if err != nil {
		return nil, err
	}
	if !exists {
		return &GmailConfig{Enabled: false}, nil
	}
	var cfg GmailConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse gmail-config.json: %w", err)
	}
	return &cfg, nil
}

// ReloadConfig reloads configuration from disk and recomputes enablement.
func (g *GmailService) ReloadConfig(ctx context.Context) error {
	cfg, err := loadGmailConfigFromDisk()
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	previous := g.config
	cfg = normalizeGmailConfig(cfg)
	g.config = cfg

	gwsPath := strings.TrimSpace(cfg.GwsPath)
	if gwsPath == "" {
		gwsPath = "gws"
	}
	gogPath := strings.TrimSpace(cfg.GogPath)
	if gogPath == "" {
		gogPath = "gog"
	}

	// Enabled requires: the flag on and a resolvable binary for whichever
	// backend is active. Without a binary every send would fail, so we
	// report disabled rather than register a dead connector.
	binaryOK := gmailConfiguredBinaryAvailable(cfg, gwsPath, gogPath)

	g.gwsPath = gwsPath
	g.gogPath = gogPath
	g.useGog = cfg.UseGogBackend
	g.defaultTo = strings.TrimSpace(cfg.DefaultTo)
	// Config changes can point at a different account (ConfigHome, Token,
	// CredentialsFile), so a cached status may describe the previous one.
	// Invalidate per connection rather than wholesale: a connection whose auth
	// knobs are unchanged keeps its cached status, so editing one account does
	// not force every other one back to a spinner.
	g.authCaches = retainedGmailAuthCaches(g.authCaches, previous, cfg)
	g.enabled = cfg.Enabled && binaryOK

	if cfg.Enabled && !g.enabled {
		log.Printf("[GMAIL] Service disabled: enabled=%v, hasDefaultTo=%v, gws=%s, gog=%s, binaryFound=%v",
			cfg.Enabled, g.defaultTo != "", gwsPath, gogPath, binaryOK)
	} else if g.enabled {
		log.Printf("[GMAIL] Service enabled: default_to=%s, blocked_recipients=%d, gws=%s, gog=%s", g.defaultTo, len(cfg.BlockedRecipients), gwsPath, gogPath)
	}
	return nil
}

// GetConfig returns a copy of the current on-disk config (never nil).
func (g *GmailService) GetConfig() *GmailConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.config == nil {
		return &GmailConfig{}
	}
	c := *g.config
	c.BlockedRecipients = append([]string(nil), g.config.BlockedRecipients...)
	// Deep-copy connections too: a shallow struct copy would hand callers the
	// same backing array, letting a registry edit mutate live service state
	// before SaveConfig validates it.
	if len(g.config.Connections) > 0 {
		c.Connections = make([]GmailConnection, 0, len(g.config.Connections))
		for _, conn := range g.config.Connections {
			c.Connections = append(c.Connections, conn.Clone())
		}
	}
	return &c
}

// SaveConfig persists the config to the workspace and reloads the service so
// enablement takes effect immediately. This is what the UI's enable/disable
// toggle calls — the user never touches gmail-config.json directly.
func (g *GmailService) SaveConfig(ctx context.Context, cfg *GmailConfig) error {
	if cfg == nil {
		cfg = &GmailConfig{}
	}
	cfg = normalizeGmailConfig(cfg)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gmail config: %w", err)
	}
	if err := writeWorkspaceFile(ctx, workspaceAPIURL(), gmailConfigFilePath(), string(out)); err != nil {
		return fmt.Errorf("write gmail config: %w", err)
	}
	return g.ReloadConfig(ctx)
}

// GmailAuthStatus reports what the UI needs to show connection state without
// the user inspecting any files: is the gws binary present, is it
// authenticated, and does the account hold a Gmail send scope.
type GmailAuthStatus struct {
	GwsInstalled bool `json:"gws_installed"`
	// Backend names which CLI this status was computed against — "gws" or
	// "gog" — so the UI can name the binary it actually checked for instead
	// of assuming gws. Always set, even when GwsInstalled is false.
	Backend       string   `json:"backend,omitempty"`
	Authenticated bool     `json:"authenticated"`
	HasGmailScope bool     `json:"has_gmail_scope"`
	Scopes        []string `json:"scopes,omitempty"`
	Detail        string   `json:"detail,omitempty"`

	// Email is the authenticated sending address. Current gws (0.22+) reports
	// it as `user` in `gws auth status`, which is preferred: it is local and
	// works with a send-only scope. Older gws carried no identity there, so
	// `gmail users getProfile` remains the fallback — but that is a Gmail API
	// call needing a read scope, so a gmail.send-only login gets nothing from
	// it (the exact reason Dominion showed "Address not known yet"). Empty when
	// unauthenticated or when both lookups fail, which is not an auth failure.
	Email string `json:"email,omitempty"`

	// Checking reports that no fresh result is known yet and a refresh is
	// running in the background. Callers that must not block (the notification
	// settings UI) render a pending state and re-read shortly after.
	Checking bool `json:"checking,omitempty"`
}

// AuthStatus shells out to `gws auth status` and interprets the result. It is
// best-effort: any failure surfaces as "not authenticated" with a hint rather
// than an error, so the UI can render a "Connect Gmail" prompt.
func (g *GmailService) AuthStatus(ctx context.Context) GmailAuthStatus {
	g.mu.RLock()
	cfg := g.config
	g.mu.RUnlock()
	return g.authStatusBlocking(ctx, "", cfg)
}

// AuthStatusForConnectionBlocking is the synchronous counterpart of
// AuthStatusForConnection, for callers that need a definitive answer now (a
// test send, a reconnect) rather than a cached badge.
func (g *GmailService) AuthStatusForConnectionBlocking(ctx context.Context, id string) (GmailAuthStatus, bool) {
	conn, ok := g.GetConnection(id)
	if !ok {
		return GmailAuthStatus{}, false
	}
	return g.authStatusBlocking(ctx, conn.ID, gmailConnectionConfig(conn)), true
}

// authStatusBlocking serves a fresh-enough cached value or computes one inline,
// writing the result into that key's slot so the async path sees it too.
func (g *GmailService) authStatusBlocking(ctx context.Context, key string, cfg *GmailConfig) GmailAuthStatus {
	g.mu.RLock()
	gwsPath := g.gwsPath
	useGog, gogPath := g.useGog, g.gogPath
	var cached *GmailAuthStatus
	var cachedAt time.Time
	if entry := g.authCaches[key]; entry != nil {
		cached, cachedAt = entry.status, entry.cachedAt
	}
	g.mu.RUnlock()

	if cached != nil && time.Since(cachedAt) < gmailAuthCacheTTL {
		return *cached
	}

	st := g.computeAuthStatus(ctx, gwsPath, useGog, gogPath, cfg)
	g.mu.Lock()
	if g.authCaches == nil {
		g.authCaches = map[string]*gmailAuthCacheEntry{}
	}
	entry := g.authCaches[key]
	if entry == nil {
		entry = &gmailAuthCacheEntry{}
		g.authCaches[key] = entry
	}
	entry.status, entry.cachedAt = &st, time.Now()
	g.mu.Unlock()
	if key != "" {
		g.syncConnectionScopes(ctx, key, st.Scopes)
	}
	return st
}

// computeAuthStatus runs `gws auth status` and interprets it, or dispatches to
// the gog backend (gmail_gog.go) when useGog is set. Split out so the caching
// wrapper stores exactly one result regardless of which branch returns.
func (g *GmailService) computeAuthStatus(ctx context.Context, gwsPath string, useGog bool, gogPath string, cfg *GmailConfig) GmailAuthStatus {
	if useGog || (cfg != nil && cfg.UseGogBackend) {
		return g.computeAuthStatusGog(ctx, gogPath, cfg)
	}
	if gwsPath == "" {
		gwsPath = "gws"
	}

	st := GmailAuthStatus{Backend: "gws"}
	if _, err := exec.LookPath(gwsPath); err != nil {
		st.Detail = "gws binary not found on PATH — install the Google Workspace CLI"
		return st
	}
	st.GwsInstalled = true

	// A connection the server authenticated itself holds no gws credentials, so
	// `gws auth status` would report it unauthenticated and the UI would offer a
	// reconnect that is not needed. The presence of a working access token IS
	// the auth state for these.
	if cfg != nil && strings.TrimSpace(cfg.Token) != "" {
		st.Authenticated = true
		st.HasGmailScope = true
		st.Scopes = gmailOAuthScopesFor(false, false, nil)
		// Same identity strategy as the gog path: tokeninfo's email works for
		// the send-only default, where getProfile is closed. Best-effort — a
		// tokeninfo failure changes nothing about the auth state here, since
		// holding a working token IS the auth state for these connections.
		if scopes, email, err := googleTokenInfo(ctx, cfg.Token); err == nil {
			st.Scopes = scopes
			st.HasGmailScope = scopesGrantGmailSend(scopes)
			st.Email = email
		}
		if st.Email == "" {
			st.Email = fetchGmailAccountEmail(ctx, gwsPath, cfg)
		}
		return st
	}

	cmd := exec.CommandContext(ctx, gwsPath, "auth", "status")
	cmd.Env = gmailChildEnv(cfg)
	out, err := cmd.Output()
	if err != nil {
		st.Detail = "not authenticated — run `gws auth login` on the host"
		return st
	}

	var raw struct {
		EncryptionValid          bool     `json:"encryption_valid"`
		HasRefreshToken          bool     `json:"has_refresh_token"`
		EncryptedCredentialsHave bool     `json:"encrypted_credentials_exists"`
		Scopes                   []string `json:"scopes"`
		// The authenticated identity, present in gws 0.22+ (from the openid /
		// userinfo.email scopes every login carries). Absent in older gws.
		User string `json:"user"`
	}
	if jsonErr := json.Unmarshal(out, &raw); jsonErr != nil {
		st.Detail = "could not parse `gws auth status` output"
		return st
	}
	st.Authenticated = raw.EncryptedCredentialsHave && raw.EncryptionValid
	st.Scopes = raw.Scopes
	st.HasGmailScope = scopesGrantGmailSend(raw.Scopes)
	if st.Authenticated && !st.HasGmailScope {
		st.Detail = "authenticated, but the account is missing a Gmail send scope — re-run `gws auth login -s gmail`"
	}
	// Only worth a second subprocess once we know a Gmail-scoped account is
	// actually present. A failure here leaves Email empty but must not flip the
	// connection to unauthenticated: it can still send.
	if st.Authenticated && st.HasGmailScope {
		// Prefer the identity `gws auth status` already reported: local, and
		// the only source that works for a gmail.send-only login (getProfile
		// needs a read scope). Fall back to getProfile for older gws.
		st.Email = strings.TrimSpace(raw.User)
		if st.Email == "" {
			st.Email = fetchGmailAccountEmail(ctx, gwsPath, cfg)
		}
	}
	return st
}

// fetchGmailAccountEmail resolves which identity gws is authenticated as via
// the Gmail API.
//
// The send path addresses the mailbox as `userId: "me"`, which Gmail resolves
// server-side, so nothing in a send ever reveals the sender. This is the
// fallback for a gws too old to report `user` in `gws auth status`; it needs a
// Gmail read scope (readonly/modify/metadata), so with a send-only login it
// returns "" — which is why callers try the auth-status identity first.
func fetchGmailAccountEmail(ctx context.Context, gwsPath string, cfg *GmailConfig) string {
	cmd := exec.CommandContext(ctx, gwsPath, "gmail", "users", "getProfile",
		"--params", `{"userId":"me"}`, "--format", "json")
	cmd.Env = gmailChildEnv(cfg)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(out, &profile); err != nil {
		return ""
	}
	return strings.TrimSpace(profile.EmailAddress)
}

// Name returns the connector name.
func (g *GmailService) Name() string { return "gmail" }

// IsEnabled reports whether Gmail notifications can be sent.
func (g *GmailService) IsEnabled() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// pickRecipient resolves where this Gmail notification should land.
// Precedence:
//  1. dest.Gmail.Email (explicit per-request hint)
//  2. per-user preference (looked up via dest.UserID)
//  3. workspace-wide default (g.defaultTo)
//
// Blocked addresses are removed from the resolved list rather than failing the
// whole send: a denylist means "never email this person", not "if this person
// appears, email nobody". Returns "" when Gmail has no destination at all, or
// when every resolved recipient was blocked — the caller treats that as
// "skip silently", which is correct because there is genuinely nowhere to send.
func (g *GmailService) pickRecipient(dest *NotificationDestination) string {
	return g.pickRecipientWithContext(context.Background(), dest)
}

func (g *GmailService) pickRecipientWithContext(ctx context.Context, dest *NotificationDestination) string {
	candidate := ""
	if dest != nil && dest.Gmail != nil && strings.TrimSpace(dest.Gmail.Email) != "" {
		candidate = strings.TrimSpace(dest.Gmail.Email)
	} else if dest != nil && dest.UserID != "" {
		if pref := getNotificationPreferences(dest.UserID); pref != nil && pref.GmailEmail != "" && !pref.GmailDisabled {
			candidate = strings.TrimSpace(pref.GmailEmail)
		}
	}
	if candidate == "" {
		g.mu.RLock()
		candidate = g.defaultTo
		g.mu.RUnlock()
	}
	if candidate == "" {
		// Last resort: the configured sending account's own inbox. Never pick
		// an arbitrary connection or override an explicitly supplied denylisted To.
		cfg := g.GetConfig()
		if len(cfg.Connections) > 0 {
			id := cfg.DefaultConnectionID
			if dest != nil && dest.Gmail != nil && len(dest.Gmail.ConnectionIDs) > 0 {
				if len(dest.Gmail.ConnectionIDs) != 1 {
					return ""
				}
				id = dest.Gmail.ConnectionIDs[0]
			}
			if conn, ok := g.GetConnection(id); ok && conn.Enabled && conn.Status == GmailConnectionConnected {
				candidate = strings.TrimSpace(conn.Email)
			}
		} else {
			authCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			status := g.AuthStatus(authCtx)
			cancel()
			if status.Authenticated && status.HasGmailScope {
				candidate = strings.TrimSpace(status.Email)
			}
		}
	}
	recipients, dropped := g.filterRecipients([]string{candidate}, destBlockedRecipients(dest)...)
	if len(dropped) > 0 {
		// Worth a line: a message going to fewer people than configured is
		// invisible otherwise, and this is the only place it is decided.
		log.Printf("[GMAIL] Skipped %d blocked To recipient(s): %s", len(dropped), strings.Join(dropped, ", "))
	}
	if len(recipients) == 0 {
		return ""
	}
	return strings.Join(recipients, ", ")
}

// filterCCRecipients drops blocked CC addresses and keeps the rest.
func (g *GmailService) filterCCRecipients(cc []string, extraBlocked ...string) []string {
	allowed, dropped := g.filterRecipients(cc, extraBlocked...)
	if len(dropped) > 0 {
		log.Printf("[GMAIL] Skipped %d blocked CC recipient(s): %s", len(dropped), strings.Join(dropped, ", "))
	}
	return allowed
}

// filterRecipients removes every recipient in the account-wide blocked list OR
// in extraBlocked (a per-notification/per-workflow denylist). The two lists are
// unioned — extraBlocked can only add to the block set, never remove from it.
func (g *GmailService) filterRecipients(recipients []string, extraBlocked ...string) (allowed, dropped []string) {
	blocked := g.blockedRecipients()
	if len(extraBlocked) > 0 {
		blocked = append(append([]string(nil), blocked...), extraBlocked...)
	}
	return filterRecipientsAgainstList(recipients, blocked)
}

// destBlockedRecipients extracts the per-notification Gmail denylist carried on
// the destination hint (nil-safe), used to augment the account-wide blocked list.
func destBlockedRecipients(dest *NotificationDestination) []string {
	if dest != nil && dest.Gmail != nil {
		return dest.Gmail.BlockedRecipients
	}
	return nil
}

// filterRecipientsAgainstList splits a recipient list into the addresses that
// may be emailed and the ones a denylist excludes. It never fails the send:
// dropping one blocked address must not silence the message for everyone else
// on the list, which is what an all-or-nothing check did once recipient lists
// could hold more than one person.
func filterRecipientsAgainstList(recipients []string, blockedRecipients []string) (allowed, dropped []string) {
	recipients = normalizeEmailList(recipients)
	blocked := map[string]bool{}
	for _, recipient := range normalizeEmailList(blockedRecipients) {
		blocked[recipient] = true
	}
	for _, recipient := range recipients {
		if blocked[recipient] {
			dropped = append(dropped, recipient)
			continue
		}
		allowed = append(allowed, recipient)
	}
	return allowed, dropped
}

// SendNotification implements NotificationConnector. It renders the feedback
// request as a plain email. Gmail is an OUTBOUND-ONLY connector: it notifies
// the recipient that input is needed, but the response itself is collected
// through another channel (Slack bot, web UI). Any options are listed in the
// body for context only — there is no email reply path.
func (g *GmailService) SendNotification(ctx context.Context, uniqueID string, message string, contextMsg string, buttonOptions *ButtonOptions, dest *NotificationDestination) (string, error) {
	if !g.IsEnabled() {
		return "", nil
	}
	to := g.pickRecipientWithContext(ctx, dest)
	if to == "" {
		return "", nil
	}

	subject := gmailSubject(message)
	body := strings.TrimSpace(message)
	if c := strings.TrimSpace(contextMsg); c != "" {
		body += "\n\n" + c
	}
	if opts := renderGmailButtonOptions(buttonOptions); opts != "" {
		body += "\n\n" + opts
	}

	// Typed Gmail content (if provided) overrides the derived subject/body and
	// can carry attachments.
	var attachments []string
	var cc []string
	var htmlBody string
	if gc := gmailContentFrom(dest); gc != nil {
		if s := strings.TrimSpace(gc.Subject); s != "" {
			subject = s
		}
		cc = gc.CC
		htmlBody = gc.HTMLBody
		attachments = gc.Attachments
	}
	cc = g.filterCCRecipients(cc, destBlockedRecipients(dest)...)

	return g.deliver(ctx, destGmailConnectionIDs(dest), destWorkflowName(dest), to, cc, subject, body, htmlBody, attachments)
}

// SendUserNotification sends a non-blocking informational email.
func (g *GmailService) SendUserNotification(ctx context.Context, message string, contextMsg string, dest *NotificationDestination) (string, error) {
	if !g.IsEnabled() {
		return "", nil
	}
	to := g.pickRecipientWithContext(ctx, dest)
	if to == "" {
		return "", nil
	}
	subject := gmailSubject(message)
	body := strings.TrimSpace(message)
	if c := strings.TrimSpace(contextMsg); c != "" {
		body += "\n\n" + c
	}
	var attachments []string
	var cc []string
	var htmlBody string
	if gc := gmailContentFrom(dest); gc != nil {
		if s := strings.TrimSpace(gc.Subject); s != "" {
			subject = s
		}
		cc = gc.CC
		htmlBody = gc.HTMLBody
		attachments = gc.Attachments
	}
	if strings.TrimSpace(body) == "" && strings.TrimSpace(htmlBody) == "" && len(attachments) == 0 {
		return "", nil
	}
	cc = g.filterCCRecipients(cc, destBlockedRecipients(dest)...)
	return g.deliver(ctx, destGmailConnectionIDs(dest), destWorkflowName(dest), to, cc, subject, body, htmlBody, attachments)
}

// maxGmailAttachmentBytes caps the total attachment payload per message to keep
// well under Gmail's raw-send limit and avoid pathological memory use.
const maxGmailAttachmentBytes = 20 * 1024 * 1024 // 20 MB

// deliver sends every notification through the HTML-capable path.
func (g *GmailService) deliver(ctx context.Context, connectionIDs []string, workflowName, to string, cc []string, subject, body, htmlBody string, attachments []string) (string, error) {
	// No senders named means "the default connection" — expressed as a
	// single-element run so the fan-out path below is the only send path and
	// cannot drift from the single-account one.
	if len(connectionIDs) == 0 {
		connectionIDs = []string{""}
	}

	messageIDs := make([]string, 0, len(connectionIDs))
	var failures []string

	for _, connectionID := range connectionIDs {
		// Resolve per account, because one disabled connection must not stop the
		// others: a fan-out that silently became a single send would be worse
		// than a partial failure the caller can see.
		cfg, resolvedID, err := g.resolveSendConfig(connectionID)
		if err != nil {
			g.recordSend("send_refused", connectionID, workflowName, to, cc, "", err)
			failures = append(failures, err.Error())
			continue
		}

		// All outbound notifications use the rich-message path. When a caller
		// supplies only text, sendRaw safely converts it to a single HTML body.
		msgID, err := g.sendRaw(ctx, cfg, to, cc, subject, body, htmlBody, attachments)
		g.recordSend("send", resolvedID, workflowName, to, cc, msgID, err)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", senderLabel(resolvedID), err))
			continue
		}
		messageIDs = append(messageIDs, msgID)
	}

	// Partial failure is reported as an error while still returning the IDs that
	// did send. Reporting success because *one* account delivered would hide
	// that a recipient never heard from the other.
	if len(failures) > 0 {
		return strings.Join(messageIDs, ", "), fmt.Errorf("gmail: %d of %d sends failed: %s",
			len(failures), len(connectionIDs), strings.Join(failures, "; "))
	}
	return strings.Join(messageIDs, ", "), nil
}

// senderLabel names a connection in an error, falling back to a readable phrase
// for the legacy singleton, which has no ID.
func senderLabel(resolvedID string) string {
	if strings.TrimSpace(resolvedID) == "" {
		return "default account"
	}
	return resolvedID
}

// sendRaw builds an RFC 2822 MIME message (body + attachments) and posts it via
// `gws gmail users messages send --json '{"raw": <base64url>}'` (or gog's
// `gmail send --raw-file`, see gmail_gog.go).
func (g *GmailService) sendRaw(ctx context.Context, cfg *GmailConfig, to string, cc []string, subject, body, htmlBody string, attachments []string) (string, error) {
	htmlBody = gmailHTMLBody(body, htmlBody)
	g.mu.RLock()
	gwsPath := g.gwsPath
	useGog, gogPath := g.useGog, g.gogPath
	g.mu.RUnlock()
	if useGog || (cfg != nil && cfg.UseGogBackend) {
		return g.sendComposedGog(ctx, gogPath, cfg, to, cc, subject, body, htmlBody, attachments)
	}
	if strings.TrimSpace(gwsPath) == "" {
		gwsPath = "gws"
	}

	mimeBytes, err := buildGmailMIME(to, cc, subject, body, htmlBody, attachments)
	if err != nil {
		return "", fmt.Errorf("build email: %w", err)
	}
	reqBody, err := json.Marshal(map[string]string{"raw": base64.URLEncoding.EncodeToString(mimeBytes)})
	if err != nil {
		return "", fmt.Errorf("marshal send request: %w", err)
	}

	// The raw users.messages.send API requires the userId path param ("me"); unlike
	// the `+send` helper it is not implied, so without it gws returns 400
	// "Required path parameter userId is missing".
	cmd := exec.CommandContext(ctx, gwsPath, "gmail", "users", "messages", "send", "--params", `{"userId":"me"}`, "--json", string(reqBody), "--format", "json")
	cmd.Env = gmailChildEnv(cfg)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// gws prints the API error JSON to stdout, so include both streams.
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		return "", fmt.Errorf("gws gmail users messages send failed: %w: %s", err, detail)
	}
	return parseGwsMessageID(stdout.Bytes()), nil
}

func gmailHTMLBody(body, htmlBody string) string {
	if strings.TrimSpace(htmlBody) != "" {
		return htmlBody
	}
	return `<div style="white-space: pre-wrap;">` + html.EscapeString(body) + `</div>`
}

// buildGmailMIME assembles a multipart/mixed RFC 2822 message: one UTF-8 body
// plus one base64 part per attachment. Rich notifications contain only the
// HTML body so clients cannot display a duplicate plain-text alternative.
func buildGmailMIME(to string, cc []string, subject, body, htmlBody string, attachments []string) ([]byte, error) {
	parts := &bytes.Buffer{}
	mw := multipart.NewWriter(parts)

	// Body part: HTML only when supplied, otherwise plain text.
	if strings.TrimSpace(htmlBody) != "" {
		htw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"text/html; charset=UTF-8"},
			"Content-Transfer-Encoding": {"8bit"},
		})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(htw, htmlBody); err != nil {
			return nil, err
		}
	} else {
		tw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {"text/plain; charset=UTF-8"},
			"Content-Transfer-Encoding": {"8bit"},
		})
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tw, body); err != nil {
			return nil, err
		}
	}

	var total int
	for _, p := range attachments {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", p, err)
		}
		total += len(data)
		if total > maxGmailAttachmentBytes {
			return nil, fmt.Errorf("attachments exceed the %d MB limit", maxGmailAttachmentBytes/(1024*1024))
		}
		name := filepath.Base(p)
		ctype := mime.TypeByExtension(filepath.Ext(name))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		aw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {ctype},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf("attachment; filename=%q", name)},
		})
		if err != nil {
			return nil, err
		}
		if err := writeBase64Wrapped(aw, data); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	full := &bytes.Buffer{}
	fmt.Fprintf(full, "To: %s\r\n", to)
	if len(cc) > 0 {
		fmt.Fprintf(full, "Cc: %s\r\n", strings.Join(normalizeEmailList(cc), ", "))
	}
	fmt.Fprintf(full, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", subject))
	full.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(full, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())
	full.Write(parts.Bytes())
	return full.Bytes(), nil
}

// writeBase64Wrapped writes standard base64 wrapped at 76 columns (RFC 2045).
func writeBase64Wrapped(w io.Writer, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		if _, err := io.WriteString(w, enc[i:end]+"\r\n"); err != nil {
			return err
		}
	}
	return nil
}

// SendTest sends a one-off test email, bypassing the enabled gate so the UI's
// "test connection" button works before the channel is saved/enabled. It still
// requires gws to be installed and authenticated.
func (g *GmailService) SendTest(ctx context.Context, to string) (string, error) {
	return g.sendTest(ctx, "", to, nil, false)
}

// SendTestFromConnection sends a test through one specific connection, so the
// user can verify the exact account they selected rather than the default.
func (g *GmailService) SendTestFromConnection(ctx context.Context, connectionID, to string) (string, error) {
	return g.sendTest(ctx, connectionID, to, nil, false)
}

// SendTestWithBlockedRecipients sends a one-off test email while validating
// against the caller's draft denylist. The settings UI uses this before saving
// so the test reflects what is currently typed in the form.
func (g *GmailService) SendTestWithBlockedRecipients(ctx context.Context, to string, blockedRecipients []string) (string, error) {
	return g.sendTest(ctx, "", to, blockedRecipients, true)
}

func (g *GmailService) sendTest(ctx context.Context, connectionID, to string, blockedRecipients []string, hasBlockedOverride bool) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		dest := &NotificationDestination{Gmail: &GmailDest{}}
		if connectionID != "" {
			dest.Gmail.ConnectionIDs = []string{connectionID}
		}
		to = g.pickRecipientWithContext(ctx, dest)
	}
	if to == "" {
		return "", fmt.Errorf("no recipient: set a default address first")
	}
	var recipients, dropped []string
	if hasBlockedOverride {
		recipients, dropped = filterRecipientsAgainstList([]string{to}, blockedRecipients)
	} else {
		recipients, dropped = g.filterRecipients([]string{to})
	}
	// Unlike a real notification, a test is an explicit action the user just
	// asked for, so a blocked address is reported instead of quietly skipped.
	if len(recipients) == 0 {
		if len(dropped) > 0 {
			return "", fmt.Errorf("every test recipient is in the blocked recipients list: %s", strings.Join(dropped, ", "))
		}
		return "", fmt.Errorf("no recipient: set a default address first")
	}
	to = strings.Join(recipients, ", ")

	cfg, resolvedID, err := g.resolveSendConfig(connectionID)
	if err != nil {
		return "", err
	}
	body := "This is a test from your agent's Gmail channel. If you received it, outbound Gmail is configured correctly."
	// Name the connection in the message itself. A test exists to prove which
	// account sends, and the recipient otherwise has to infer that from the
	// From header alone.
	if conn, ok := g.GetConnection(connectionID); ok {
		body += fmt.Sprintf("\n\nSent via connection %s (%s).", conn.ID, conn.DisplayName)
	}
	msgID, sendErr := g.sendRaw(ctx, cfg, to, nil, "[Agent] Gmail test message", body, "", nil)
	g.recordSend("test_send", resolvedID, "", to, nil, msgID, sendErr)
	return msgID, sendErr
}

func normalizeGmailConfig(cfg *GmailConfig) *GmailConfig {
	if cfg == nil {
		return &GmailConfig{}
	}
	out := *cfg
	// DefaultTo holds one OR MORE addresses. Split it the same way the send path
	// splits recipients so a pasted "a@x.com, b@x.com" is stored canonically
	// rather than as one malformed address that never delivers. Case is kept:
	// the local part of an address is case-sensitive in principle, and the send
	// path lowercases only for denylist comparison.
	out.DefaultTo = strings.Join(splitEmailListPreservingCase(out.DefaultTo), ", ")
	out.BlockedRecipients = normalizeEmailList(out.BlockedRecipients)
	// Registry upkeep runs on every read and write, so a pre-registry install
	// migrates on first load with no separate job, and hand-edited files get
	// their invariants repaired rather than rejected.
	migrateGmailConnections(&out)
	normalizeGmailConnections(&out)
	return &out
}

// splitEmailListPreservingCase splits a recipient string into individual
// addresses, dropping blanks and case-insensitive duplicates while leaving each
// surviving address spelled as the user typed it.
func splitEmailListPreservingCase(value string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		email := strings.TrimSpace(part)
		key := strings.ToLower(email)
		if email == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, email)
	}
	return out
}

func normalizeEmailList(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		}) {
			email := strings.ToLower(strings.TrimSpace(part))
			if email == "" || seen[email] {
				continue
			}
			seen[email] = true
			out = append(out, email)
		}
	}
	return out
}

func (g *GmailService) blockedRecipients() []string {
	g.mu.RLock()
	cfg := g.config
	g.mu.RUnlock()
	if cfg == nil {
		return nil
	}
	return normalizeEmailList(append([]string(nil), cfg.BlockedRecipients...))
}

// gmailChildEnv builds the environment for the gws child process, layering the
// optional auth knobs on top of the server's own environment.
//
// Every knob is opt-in: a config carrying none of them yields exactly
// os.Environ(), so a host authenticated the ordinary way (`gws auth login`
// against the default ~/.config/gws) is unaffected by anything here.
func gmailChildEnv(cfg *GmailConfig) []string {
	env := os.Environ()
	if cfg == nil {
		return env
	}
	if v := strings.TrimSpace(cfg.ConfigHome); v != "" {
		// gws reads GOOGLE_WORKSPACE_CLI_CONFIG_DIR. It does not read
		// XDG_CONFIG_HOME — exporting that (as this did until now) silently
		// left every invocation on the default ~/.config/gws, which made
		// ConfigHome a no-op.
		env = append(env, "GOOGLE_WORKSPACE_CLI_CONFIG_DIR="+v)

		// A config dir alone is not an account boundary. At the default
		// `keyring` backend gws keeps credentials in the shared OS keyring, so
		// two pinned directories would still resolve to the same account; the
		// `file` backend keeps credentials.enc inside the directory instead.
		//
		// Scoped to the ConfigHome branch on purpose. Forcing `file`
		// unconditionally would repoint hosts that authenticated against the
		// default keyring at a credentials.enc that does not exist, breaking a
		// working install. An operator who has set the variable themselves
		// keeps their choice — os/exec resolves duplicate keys last-wins, so
		// appending here would otherwise silently override them.
		if _, ok := os.LookupEnv("GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND"); !ok {
			env = append(env, "GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file")
		}
	}
	if v := strings.TrimSpace(cfg.CredentialsFile); v != "" {
		env = append(env, "GOOGLE_WORKSPACE_CLI_CREDENTIALS_FILE="+v)
	}
	if v := strings.TrimSpace(cfg.Token); v != "" {
		env = append(env, "GOOGLE_WORKSPACE_CLI_TOKEN="+v)
	}
	return env
}

// renderGmailButtonOptions turns ButtonOptions into a plain-text instruction
// line, since email has no interactive buttons.
func renderGmailButtonOptions(opts *ButtonOptions) string {
	if opts == nil {
		return ""
	}
	if len(opts.Options) > 0 {
		return "Options: " + strings.Join(opts.Options, " / ")
	}
	if opts.YesNoOnly {
		yes := opts.YesLabel
		if yes == "" {
			yes = "Approve"
		}
		no := opts.NoLabel
		if no == "" {
			no = "Reject"
		}
		return fmt.Sprintf("Options: %s / %s", yes, no)
	}
	return ""
}

// gmailSubject derives a concise subject line from the message body.
func gmailSubject(message string) string {
	line := strings.TrimSpace(message)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	// Only the auto-derived fallback is bounded (the agent's explicit email_subject
	// is used verbatim, no truncation). Truncate by RUNES, never bytes, so a
	// multi-byte char (em dash, emoji) is never cut in half into a broken character.
	const maxRunes = 150
	if r := []rune(line); len(r) > maxRunes {
		line = strings.TrimSpace(string(r[:maxRunes])) + "…"
	}
	if line == "" {
		return "[Agent] Action needed"
	}
	return "[Agent] " + line
}

// parseGwsMessageID best-effort extracts a message/thread ID from gws JSON
// output. gws emits structured JSON for agents; field names vary by command,
// so we probe the common ones and fall back to a non-empty sentinel so the
// notification manager logs the send as successful.
func parseGwsMessageID(out []byte) string {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return "sent"
	}
	var m map[string]any
	if err := json.Unmarshal(trimmed, &m); err == nil {
		for _, key := range []string{"id", "messageId", "message_id", "threadId", "thread_id"} {
			if v, ok := m[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return "sent"
}

// AuthStatusCached returns the last known auth status for the legacy singleton
// config without ever spawning a subprocess. On a miss it kicks off a
// background refresh and reports Checking.
//
// `gws auth status` takes ~5.5s — it is a Node CLI, not a network call — and it
// sat on the synchronous path of every notification-settings read, so opening
// the Notify popup showed a spinner for that entire time. The popup needs the
// recipient and channel configuration, all of which is already in memory; only
// the Gmail auth badge depends on gws, so only that badge should wait for it.
func (g *GmailService) AuthStatusCached() GmailAuthStatus {
	g.mu.RLock()
	cfg := g.config
	g.mu.RUnlock()
	return g.authStatusCachedFor("", cfg)
}

// AuthStatusForConnection reports one connection's auth state, cached
// independently of every other connection so an expired credential on one
// never marks another unhealthy.
func (g *GmailService) AuthStatusForConnection(id string) (GmailAuthStatus, bool) {
	conn, ok := g.GetConnection(id)
	if !ok {
		return GmailAuthStatus{}, false
	}
	return g.authStatusCachedFor(conn.ID, gmailConnectionConfig(conn)), true
}

// authStatusCachedFor is the shared cache-and-refresh body. key identifies the
// cache slot ("" for the legacy singleton, otherwise a connection ID); cfg
// supplies the auth knobs that slot's subprocess should run under.
func (g *GmailService) authStatusCachedFor(key string, cfg *GmailConfig) GmailAuthStatus {
	g.mu.RLock()
	gwsPath := g.gwsPath
	useGog, gogPath := g.useGog, g.gogPath
	var cached *GmailAuthStatus
	var cachedAt time.Time
	var refreshing bool
	if entry := g.authCaches[key]; entry != nil {
		cached, cachedAt, refreshing = entry.status, entry.cachedAt, entry.refreshing
	}
	g.mu.RUnlock()

	if cached != nil && time.Since(cachedAt) < gmailAuthCacheTTL {
		return *cached
	}

	if !refreshing {
		g.mu.Lock()
		entry := g.authCaches[key]
		if entry == nil {
			entry = &gmailAuthCacheEntry{}
			if g.authCaches == nil {
				g.authCaches = map[string]*gmailAuthCacheEntry{}
			}
			g.authCaches[key] = entry
		}
		if !entry.refreshing {
			entry.refreshing = true
			go func() {
				// Detached from the request: the caller has already returned.
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				st := g.computeAuthStatus(ctx, gwsPath, useGog, gogPath, cfg)
				g.mu.Lock()
				if e := g.authCaches[key]; e != nil {
					e.status, e.cachedAt, e.refreshing = &st, time.Now(), false
				}
				g.mu.Unlock()
				if key != "" {
					g.syncConnectionScopes(ctx, key, st.Scopes)
				}
			}()
		}
		g.mu.Unlock()
	}

	if cached != nil {
		// Stale but real: better than a pending badge while the refresh lands.
		return *cached
	}
	return GmailAuthStatus{Checking: true, Detail: "checking Gmail authorization…"}
}
