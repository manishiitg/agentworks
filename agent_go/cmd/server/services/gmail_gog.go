package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
)

// gog (github.com/openclaw/gogcli) backend, selected via GmailConfig.UseGogBackend.
//
// Unlike gws, gog has no notion of "the account this process happens to be
// pointed at" — every invocation names its account/client explicitly
// (--access-token, or --account/--client), so there is no directory-per-
// connection isolation to manage: every call shares one GOG_HOME.
//
// New and migrated connections select gog through their AuthBackend marker.
// The top-level switch remains compatible with older deployments; un-migrated
// legacy connections can continue using gws.

// gogHomeDir is the single shared root gog stores every account and named
// client under, always passed explicitly via --home rather than relying on
// ambient GOG_HOME — so an operator's own shell environment cannot silently
// repoint the server at a different store than the one gmail_oauth_clients.go
// manages.
func gogHomeDir() string { return gogconfig.Home() }

// gogHomeForConnection is the store a connection's gog account lives in.
// A private (Code) connection has its own store beside the shared one, never
// inside it: the shared store is what trusted terminals, workflows and Crews
// are handed (gogconfig.Environment), so a private account kept there would
// be reachable by them.
func gogHomeForConnection(conn GmailConnection) string {
	if !conn.IsPrivate() {
		return gogHomeDir()
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(conn.OwnerID) + "\x00" + strings.TrimSpace(conn.ScopeWorkspace) + "\x00" + conn.ID))
	return filepath.Join(gogPrivateHomesDir(), hex.EncodeToString(sum[:16]))
}

// gogPrivateHomesDir holds every private connection's store.
func gogPrivateHomesDir() string {
	return filepath.Join(filepath.Dir(gogHomeDir()), "gog-private")
}

// home is the store cfg's account lives in (the shared one by default).
func (cfg *GmailConfig) home() string {
	if cfg != nil && strings.TrimSpace(cfg.gogHome) != "" {
		return cfg.gogHome
	}
	return gogHomeDir()
}

// gogArgsForAuth picks how one gog invocation authenticates, from the same
// per-connection knobs gmailConnectionConfig already resolves:
//   - cfg.Token set (server-managed OAuth, gmail_oauth.go) -> --access-token.
//     Takes priority, mirroring GOOGLE_WORKSPACE_CLI_TOKEN's precedence in
//     gmailChildEnv: a connection holding a live access token needs no
//     stored gog account at all.
//   - else cfg.gogAccountEmail/gogClientName set (a connection migrated via
//     GmailService.ImportLegacyOAuthClient or GmailService's
//     MigrateLegacyConnectionToGog, Phase 3) -> --account/--client, letting
//     gog manage its own refresh.
//   - else: nothing to authenticate with. Reported as an error rather than
//     falling back silently — sending under the wrong (or no) identity is
//     worse than failing loudly.
func gogArgsForAuth(cfg *GmailConfig) ([]string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Token) != "" {
		return []string{"--access-token", cfg.Token}, nil
	}
	if cfg != nil && strings.TrimSpace(cfg.gogAccountEmail) != "" && strings.TrimSpace(cfg.gogClientName) != "" {
		return []string{"--account", cfg.gogAccountEmail, "--client", cfg.gogClientName}, nil
	}
	return nil, fmt.Errorf("gog: this connection has no access token and is not migrated to a named account/client — reconnect it, or run the legacy-client import")
}

// gogBaseArgs prefixes every gog invocation with the store it runs against.
func gogBaseArgs(home string, authArgs []string) []string {
	args := make([]string, 0, len(authArgs)+2)
	args = append(args, "--home", home)
	args = append(args, authArgs...)
	return args
}

// computeAuthStatusGog is the gog counterpart of computeAuthStatus. It
// deliberately does not call `gog auth status`: that command reports on a
// stored account, but a server-managed connection (--access-token) has no
// stored account to report on at all. Instead it calls the same
// gmail.users.getProfile the gws path already used for identity discovery —
// gmail.readonly is already part of gmailOAuthScopes, so success at that call
// is both the liveness check and the identity lookup in one round trip.
func (g *GmailService) computeAuthStatusGog(ctx context.Context, gogPath string, cfg *GmailConfig) GmailAuthStatus {
	if gogPath == "" {
		gogPath = "gog"
	}

	st := GmailAuthStatus{Backend: "gog"}
	if _, err := exec.LookPath(gogPath); err != nil {
		st.Detail = "gog binary not found on PATH — install gogcli (brew install openclaw/tap/gogcli)"
		return st
	}
	st.GwsInstalled = true // field name predates gog; means "the active CLI backend is installed"

	authArgs, err := gogArgsForAuth(cfg)
	if err != nil {
		st.Detail = err.Error()
		return st
	}

	// For a server-managed token, Google's own tokeninfo verdict is the
	// liveness check AND the identity source. It works for every scope set —
	// crucially the send-only default (gmailOAuthScopesFor), where
	// gmail.users.getProfile is closed (it needs a read scope): were
	// getProfile still the probe, every default-scoped connection would read
	// as "reconnect" and Pulse would refuse to send through it.
	//
	// It also reports what was actually granted, not what we last asked for —
	// an operator (or the agent, via `gog auth add`) may have widened access
	// since this connection was created, and the UI's Scopes display is only
	// honest if it reflects that.
	if cfg != nil && strings.TrimSpace(cfg.Token) != "" {
		if scopes, tokenEmail, infoErr := googleTokenInfo(ctx, cfg.Token); infoErr == nil {
			st.Authenticated = true
			st.Scopes = scopes
			st.HasGmailScope = scopesGrantGmailSend(scopes)
			if !st.HasGmailScope {
				st.Detail = "authenticated, but this account was not granted a Gmail send scope — reconnect it and allow sending"
			}
			st.Email = tokenEmail
			if st.Email == "" {
				// Best-effort only (a token granted without userinfo.email):
				// a failure here leaves the address unknown, never the
				// connection unauthenticated — it can still send.
				if email, err := gogFetchGmailProfileEmail(ctx, gogPath, cfg.home(), authArgs); err == nil {
					st.Email = email
				}
			}
			return st
		}
		// tokeninfo unreachable (network partition, not a bad token): fall
		// through to the getProfile probe rather than reporting a healthy
		// connection as broken.
	}

	// Preserve the legacy explicitly supplied access-token fallback while
	// migrated connections use only gog's own token store.
	if cfg.Token != "" {
		email, err := gogFetchGmailProfileEmail(ctx, gogPath, cfg.home(), authArgs)
		if err != nil {
			st.Detail = "not authenticated — reconnect this account"
			return st
		}
		st.Authenticated, st.HasGmailScope, st.Email = true, true, email
		st.Scopes = gmailOAuthScopesFor(false, false, nil)
		return st
	}
	account, err := checkedGogAccount(ctx, gogPath, cfg.home(), cfg.gogAccountEmail, cfg.gogClientName)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	st.Authenticated = true
	st.Email = account.Email
	scopes := account.Scopes
	if len(scopes) == 0 {
		// Imported accounts are valid but carry no scope metadata in
		// gog's store: fall back to the connection's consent-time scopes.
		// Empty here means "unknown", never "revoked" — the check above
		// already proved the credential is live.
		scopes = cfg.storedScopes
	}
	st.Scopes = scopes
	st.HasGmailScope = scopesGrantGmailSend(scopes)
	if !st.HasGmailScope {
		st.Detail = "authenticated, but this account has no Gmail send scope"
	}

	return st
}

// googleTokenInfoURL is a var, not a constant, so tests can point it at a
// local httptest server instead of making a real network call to Google for
// every status check exercised in the suite.
var googleTokenInfoURL = "https://oauth2.googleapis.com/tokeninfo"

// googleTokenInfoClient bounds the tokeninfo call so a network partition
// cannot hang a status check indefinitely — computeAuthStatusGog runs on
// both a request-serving path and a detached background-refresh goroutine,
// and neither should be able to stall on this.
var googleTokenInfoClient = &http.Client{Timeout: 5 * time.Second}

// googleTokenInfo is the full tokeninfo lookup: the granted scopes — the
// only authoritative source, since a project's OAuth consent screen can
// silently drop a requested-but-unregistered scope (see the setup guide's
// Data Access gotcha) — plus the token's email when the userinfo.email scope
// is present (every connection requests it). A 200 with a non-empty scope is
// also the strongest possible liveness check — Google itself just said the
// token is valid — and unlike gmail.users.getProfile it works for a
// gmail.send-only token, which is why computeAuthStatusGog tries it first
// (see gmailOAuthScopesFor).
func googleTokenInfo(ctx context.Context, accessToken string) (scopes []string, email string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		googleTokenInfoURL+"?access_token="+url.QueryEscape(accessToken), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := googleTokenInfoClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("tokeninfo: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		Scope string `json:"scope"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", err
	}
	scopes = strings.Fields(body.Scope)
	if len(scopes) == 0 {
		return nil, "", fmt.Errorf("tokeninfo: empty scope")
	}
	return scopes, strings.TrimSpace(body.Email), nil
}

// scopesGrantGmailSend reports whether a granted scope set can send mail:
// gmail.send itself, or a superset (gmail.modify, full mail.google.com).
// Shared by the gws and gog status paths so "has a Gmail send scope" means
// one thing everywhere. gmail.readonly alone does not qualify.
func scopesGrantGmailSend(scopes []string) bool {
	for _, s := range scopes {
		if strings.Contains(s, "gmail.send") || strings.Contains(s, "gmail.modify") || strings.Contains(s, "mail.google.com") {
			return true
		}
	}
	return false
}

// gogFetchGmailProfileEmail calls gmail.users.getProfile through gog's
// generic Discovery API passthrough (`gog api call`), the direct equivalent
// of the `gws gmail users getProfile` call fetchGmailAccountEmail makes.
func gogFetchGmailProfileEmail(ctx context.Context, gogPath, home string, authArgs []string) (string, error) {
	args := gogBaseArgs(home, authArgs)
	args = append(args, "api", "call", "gmail", "v1", "gmail.users.getProfile",
		"--params", `{"userId":"me"}`, "--json")
	cmd := exec.CommandContext(ctx, gogPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(out, &profile); err != nil {
		return "", err
	}
	email := strings.TrimSpace(profile.EmailAddress)
	if email == "" {
		return "", fmt.Errorf("gog: empty emailAddress in getProfile response")
	}
	return email, nil
}

// sendGog is the gog counterpart of GmailService.send (`gog gmail send`,
// matching gws's `gmail +send`).
func (g *GmailService) sendGog(ctx context.Context, gogPath string, cfg *GmailConfig, to, subject, body string) (string, error) {
	if gogPath == "" {
		gogPath = "gog"
	}
	authArgs, err := gogArgsForAuth(cfg)
	if err != nil {
		return "", err
	}
	args := gogBaseArgs(cfg.home(), authArgs)
	args = append(args, "gmail", "send",
		"--to", to,
		"--subject", mime.QEncoding.Encode("UTF-8", subject),
		"--body", body,
		"--json")
	cmd := exec.CommandContext(ctx, gogPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gog gmail send failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseGwsMessageID(stdout.Bytes()), nil
}

// sendComposedGog delegates MIME construction to gog. Rich notifications send
// only --body-html: supplying both --body and --body-html creates a MIME
// alternative, which some recipients rendered as duplicate visible content.
func (g *GmailService) sendComposedGog(ctx context.Context, gogPath string, cfg *GmailConfig, to string, cc []string, subject, body, htmlBody string, attachments []string) (string, error) {
	if gogPath == "" {
		gogPath = "gog"
	}
	authArgs, err := gogArgsForAuth(cfg)
	if err != nil {
		return "", err
	}
	htmlBody = gmailHTMLBody(body, htmlBody)
	args := gogBaseArgs(cfg.home(), authArgs)
	args = append(args, "gmail", "send",
		"--to", to,
		"--subject", subject)
	if len(cc) > 0 {
		args = append(args, "--cc", strings.Join(normalizeEmailList(cc), ","))
	}
	args = append(args, "--body-html", htmlBody)
	var total int64
	for _, attachment := range attachments {
		info, statErr := os.Stat(attachment)
		if statErr != nil {
			return "", fmt.Errorf("attachment %q: %w", attachment, statErr)
		}
		total += info.Size()
		if total > maxGmailAttachmentBytes {
			return "", fmt.Errorf("attachments exceed the %d MB limit", maxGmailAttachmentBytes/(1024*1024))
		}
		args = append(args, "--attach", attachment)
	}
	args = append(args, "--json")
	cmd := exec.CommandContext(ctx, gogPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		return "", fmt.Errorf("gog gmail send failed: %w: %s", err, detail)
	}
	return parseGwsMessageID(stdout.Bytes()), nil
}

// ImportRefreshTokenIntoGog stores a token under its exact account/client pair.
// This is required for new sign-ins and used by legacy migration. Failure is
// returned to the caller; the application must not report a successful gog
// connection or retire legacy credentials until import and verification pass.
//
// home is the store the account goes into (gogHomeForConnection); a private
// connection's store gets its own copy of the OAuth client too.
func ImportRefreshTokenIntoGog(ctx context.Context, home, email, clientName, refreshToken string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	email = strings.TrimSpace(email)
	clientName = strings.TrimSpace(clientName)
	if email == "" || clientName == "" || strings.TrimSpace(refreshToken) == "" {
		return fmt.Errorf("gog import: email, client name, and refresh token are all required")
	}
	const gogPath = "gog"
	if _, err := exec.LookPath(gogPath); err != nil {
		return fmt.Errorf("gog binary not found on PATH: %w", err)
	}
	// Owner-only from the start: a private store is created here, on its
	// connection's first sign-in.
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("prepare gog store: %w", err)
	}

	// A refresh token is tied to its OAuth client. Register the exact named
	// client managed by AgentWorks before importing the token; a fresh server
	// has an empty gog home, and `auth import --client <name>` alone otherwise
	// creates a token bucket that cannot refresh. The file-keyring form is
	// deliberate for headless services; GOG_KEYRING_PASSWORD protects it.
	clientID, clientSecret, err := GetOAuthClientSecret(clientName)
	if err != nil {
		return err
	}
	secretJSON, err := json.Marshal(map[string]any{"installed": map[string]string{"client_id": clientID, "client_secret": clientSecret}})
	if err != nil {
		return err
	}
	if err := storeGogClient(ctx, home, clientName, secretJSON); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "gmail-gog-import-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	refreshPath := filepath.Join(tmpDir, "refresh_token")
	if err := os.WriteFile(refreshPath, []byte(refreshToken), 0o600); err != nil {
		return fmt.Errorf("write refresh token: %w", err)
	}

	args := gogBaseArgs(home, nil)
	args = append(args, "auth", "import",
		"--email", email,
		"--client", clientName,
		"--refresh-token-file", refreshPath,
		// Reconnect replaces the existing token for this exact account/client.
		// gog refuses an existing entry with exit status 2 unless --force is
		// explicit, even though replacing it is the purpose of this flow.
		"--no-input", "--force")
	cmd := exec.CommandContext(ctx, gogPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("gog auth import: %w: %s", err, detail)
		}
		return fmt.Errorf("gog auth import: %w", err)
	}
	return nil
}
