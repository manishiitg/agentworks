package services

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Additional Google Workspace services a Gmail connection (in practice a
// general Google account connection — the type is still named GmailConnection
// for a minimal diff) can be authorized for, beyond Gmail send/read.
//
// Unlike Gmail, where send is the whole point and read is a narrow opt-in,
// these services have no single obvious operation: the agent drives gogcli
// directly (see virtual-tools' google_cli_tool.go), so the grant that matters
// is per-service read-vs-write, mirroring gogcli's own --readonly flag rather
// than a fixed scope list per operation.

// GoogleServiceGrant is one service a connection is authorized for.
type GoogleServiceGrant struct {
	// Service is a key into googleServiceCatalog (e.g. "drive", "sheets").
	Service string `json:"service"`
	// Write requests the mutating scope. Off by default: read-only is the
	// safer grant, and matches gogcli's own --readonly enforcement — a
	// read-only grant runs every invocation for this service with
	// --readonly, so even a misbehaving agent call cannot mutate it.
	Write bool `json:"write,omitempty"`
}

type googleServiceDef struct {
	DisplayName string
	ReadScope   string
	WriteScope  string
}

// googleServiceCatalog is the single source of truth for which services this
// connect flow offers and the OAuth scope each grant level requests. Adding a
// service gogcli already supports is one entry here — no other file needs to
// know the scope URI.
var googleServiceCatalog = map[string]googleServiceDef{
	"drive": {
		DisplayName: "Drive",
		ReadScope:   "https://www.googleapis.com/auth/drive.readonly",
		WriteScope:  "https://www.googleapis.com/auth/drive",
	},
	"sheets": {
		DisplayName: "Sheets",
		ReadScope:   "https://www.googleapis.com/auth/spreadsheets.readonly",
		WriteScope:  "https://www.googleapis.com/auth/spreadsheets",
	},
	"docs": {
		DisplayName: "Docs",
		ReadScope:   "https://www.googleapis.com/auth/documents.readonly",
		WriteScope:  "https://www.googleapis.com/auth/documents",
	},
	"slides": {
		DisplayName: "Slides",
		ReadScope:   "https://www.googleapis.com/auth/presentations.readonly",
		WriteScope:  "https://www.googleapis.com/auth/presentations",
	},
	"calendar": {
		DisplayName: "Calendar",
		ReadScope:   "https://www.googleapis.com/auth/calendar.readonly",
		WriteScope:  "https://www.googleapis.com/auth/calendar",
	},
}

// GmailReadonlyScope is the mailbox-read grant used by the shared Google CLI
// tool. Gmail stays outside googleServiceCatalog because its opt-in is stored
// as GmailConnection.AllowReadAccess rather than as an additional service.
const GmailReadonlyScope = "https://www.googleapis.com/auth/gmail.readonly"

// GmailComposeScope is the least-privilege Gmail mutation grant that covers
// draft creation plus sending and replying. It deliberately does not grant
// label management, deletion, or other mailbox-wide mutations.
const GmailComposeScope = "https://www.googleapis.com/auth/gmail.compose"

// GoogleServiceCatalog lists the services the connect UI may offer, keyed by
// the identifier a GoogleServiceGrant.Service names, to display name.
func GoogleServiceCatalog() map[string]string {
	out := make(map[string]string, len(googleServiceCatalog))
	for key, def := range googleServiceCatalog {
		out[key] = def.DisplayName
	}
	return out
}

// normalizeGoogleServiceGrants drops unknown services and duplicates (last
// write wins), and sorts for a stable persisted order.
func normalizeGoogleServiceGrants(in []GoogleServiceGrant) []GoogleServiceGrant {
	byService := make(map[string]GoogleServiceGrant, len(in))
	for _, g := range in {
		service := strings.ToLower(strings.TrimSpace(g.Service))
		if _, ok := googleServiceCatalog[service]; !ok {
			continue
		}
		byService[service] = GoogleServiceGrant{Service: service, Write: g.Write}
	}
	if len(byService) == 0 {
		return nil
	}
	out := make([]GoogleServiceGrant, 0, len(byService))
	for _, g := range byService {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// GoogleServiceScopeURI resolves one service+level to the scope URI Google
// would need to have granted, plus its display name. Exported for callers
// that need to check whether one specific grant is actually reflected in a
// connection's live scopes (see list_gmail_connections), as opposed to
// GoogleServiceScopeURIs, which builds the full list for a (re)connect
// request.
func GoogleServiceScopeURI(service string, write bool) (scope, displayName string, ok bool) {
	def, ok := googleServiceCatalog[strings.ToLower(strings.TrimSpace(service))]
	if !ok {
		return "", "", false
	}
	if write {
		return def.WriteScope, def.DisplayName, true
	}
	return def.ReadScope, def.DisplayName, true
}

// GoogleScopesGrant recognizes the full-service scopes used by our connection
// settings. A write grant also allows reads. Restricted grants such as
// drive.file or gmail.metadata must not stand in for account-wide access.
// This only describes Google's grants; stored connection/workflow write
// restrictions are still enforced independently when tools execute.
func GoogleScopesGrant(scopes []string, required string) bool {
	for _, granted := range scopes {
		if granted == required {
			return true
		}
		for name, service := range googleServiceCatalog {
			if required == service.ReadScope && granted == service.WriteScope {
				return true
			}
			// Sheets, Docs and Slides also accept the broader Drive scopes.
			if name == "sheets" || name == "docs" || name == "slides" {
				drive := googleServiceCatalog["drive"]
				if (required == service.ReadScope && (granted == drive.ReadScope || granted == drive.WriteScope)) ||
					(required == service.WriteScope && granted == drive.WriteScope) {
					return true
				}
			}
		}
		if required == "https://www.googleapis.com/auth/gmail.readonly" &&
			(granted == "https://www.googleapis.com/auth/gmail.modify" || granted == "https://mail.google.com/") {
			return true
		}
		if required == GmailComposeScope &&
			(granted == "https://www.googleapis.com/auth/gmail.modify" || granted == "https://mail.google.com/") {
			return true
		}
	}
	return false
}

// GoogleServiceScopeURIs resolves a connection's extra-service grants to the
// OAuth scopes to request alongside Gmail's own scopes. Exported for the
// gmail_oauth_routes.go call site, which builds the scope list for a
// (re)connect flow from the stored connection's grants.
func GoogleServiceScopeURIs(grants []GoogleServiceGrant) []string {
	var out []string
	for _, g := range grants {
		def, ok := googleServiceCatalog[strings.ToLower(strings.TrimSpace(g.Service))]
		if !ok {
			continue
		}
		if g.Write {
			out = append(out, def.WriteScope)
		} else {
			out = append(out, def.ReadScope)
		}
	}
	return out
}

// GoogleCLIAccess is what a caller needs to run one gogcli invocation on
// behalf of a connection: which binary, account/client, and which services (and
// at what access level) that connection is actually authorized for.
type GoogleCLIAccess struct {
	Account string
	Client  string
	GogPath string
	// Home is the gog store the account lives in (a private connection's own).
	Home  string
	Token string // legacy connections only; gog accounts supply Account/Client
	// Grants maps service -> write-allowed. A service absent from this map is
	// not authorized at all for this connection.
	Grants map[string]bool
}

// GoogleCLIAccessForConnection resolves authentication and the grant set for one
// connection (or the default connection, when id is empty), for the generic
// Google CLI tool (virtual-tools' google_cli_tool.go) to shell out with.
//
// Deliberately independent of GmailConfig.UseGogBackend: that flag picks
// which backend serves Gmail send/status, but Drive/Sheets/Slides/etc. have
// no gws equivalent, so this always resolves to gog.
func (g *GmailService) GoogleCLIAccessForConnection(ctx context.Context, connectionID string) (GoogleCLIAccess, error) {
	return g.GoogleCLIAccessForConnectionIn(ctx, connectionID, GmailUseScope{})
}

// GoogleCLIAccessForConnectionIn is GoogleCLIAccessForConnection for a use in
// scope: a Code session reaches only its own private accounts (as its owner),
// every other session only shared ones.
func (g *GmailService) GoogleCLIAccessForConnectionIn(ctx context.Context, connectionID string, scope GmailUseScope) (GoogleCLIAccess, error) {
	conn, err := g.ConnectionForScope(connectionID, scope)
	if err != nil {
		return GoogleCLIAccess{}, err
	}
	if !conn.Enabled {
		return GoogleCLIAccess{}, fmt.Errorf("Google account connection %q (%s) is disabled — reconnect it before use", conn.ID, conn.DisplayName)
	}
	var token string
	if conn.AuthBackend != "gog" {
		var err error
		token, err = accessTokenForConnection(ctx, conn.ID, conn.ClientName)
		if err != nil {
			return GoogleCLIAccess{}, err
		}
	} else if conn.Email == "" || conn.ClientName == "" {
		return GoogleCLIAccess{}, fmt.Errorf("Google connection is missing its account or client")
	}

	cfg := g.GetConfig()
	gogPath := "gog"
	if cfg != nil {
		if v := strings.TrimSpace(cfg.GogPath); v != "" {
			gogPath = v
		}
	}

	// Cross-check every requested grant against Google's actually-granted
	// scopes, not the stored request. AllowReadAccess/Services only record
	// what was asked for at connect time — a user who requested Drive access
	// but never completed reconnect, or whose grant was later revoked or
	// downgraded in Google, must not receive a misleading "authorized"
	// result. AuthStatusForConnectionBlocking performs a real gog/Google
	// check (serving a fresh cache within gmailAuthCacheTTL, computing inline
	// otherwise), so this stays live without paying a subprocess on every
	// tool call.
	status, _ := g.AuthStatusForConnectionBlocking(ctx, conn.ID)
	liveScopes := status.Scopes

	grants := make(map[string]bool, len(conn.Services)+1)
	for _, grant := range conn.Services {
		if grant.Write {
			if scope, _, ok := GoogleServiceScopeURI(grant.Service, true); ok && GoogleScopesGrant(liveScopes, scope) {
				grants[grant.Service] = true
				continue
			}
		}
		// Write was not requested, or was requested but not actually granted:
		// fall back to whatever read access Google did grant for this
		// service, rather than an all-or-nothing denial.
		if scope, _, ok := GoogleServiceScopeURI(grant.Service, false); ok && GoogleScopesGrant(liveScopes, scope) {
			grants[grant.Service] = false
		}
	}
	// Agent Gmail writes require both the explicit stored opt-in and Google's
	// live compose grant. A broader legacy grant can satisfy the latter, but
	// never bypasses the per-connection opt-in.
	if conn.AllowAgentWriteAccess && GoogleScopesGrant(liveScopes, GmailComposeScope) {
		grants["gmail"] = true
	} else if conn.AllowReadAccess && GoogleScopesGrant(liveScopes, GmailReadonlyScope) {
		// Mailbox reads are forced through gog's --readonly runtime mode.
		grants["gmail"] = false
	}
	if len(grants) == 0 {
		return GoogleCLIAccess{}, fmt.Errorf("connection %q (%s) has no Google read/service grant available to agents — enable Gmail read access or another service in Bots settings and reconnect", conn.ID, conn.DisplayName)
	}
	return GoogleCLIAccess{GogPath: gogPath, Home: gogHomeForConnection(conn), Token: token, Account: conn.Email, Client: conn.ClientName, Grants: grants}, nil
}

// sortedGoogleServiceNames lists catalog keys for an error message enumerating
// valid choices.
func sortedGoogleServiceNames() []string {
	out := make([]string, 0, len(googleServiceCatalog)+1)
	out = append(out, "gmail")
	for key := range googleServiceCatalog {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// googleCLIDisallowedArgs are flags the caller must not set: identity and
// enforcement are this function's job, not the agent's. Checked as a whole
// argument or as an "=value" prefix (e.g. --access-token=...).
var googleCLIDisallowedArgs = []string{"--access-token", "--account", "-a", "--client", "--home"}

// googleCLITimeout bounds one invocation so a hung gogcli process (network
// partition, an interactive prompt it's waiting on) cannot stall the calling
// agent turn indefinitely.
const googleCLITimeout = 60 * time.Second

// googleCLIOutputLimit truncates output before it reaches the agent's
// context — a `drive ls` on a large folder or a `sheets values get`
// on a big range can otherwise return megabytes.
const googleCLIOutputLimit = 20000

// RunGoogleCLI executes one gogcli invocation on behalf of a connection.
//
// args must start with the target service (e.g. "drive", "sheets") and must
// not include --access-token/--account/--client/--home — those are added
// here, from the resolved connection's own credential, so the caller (the
// google_workspace_cli agent tool) never sees or chooses the token. When the
// connection's grant for that service is read-only, --readonly is appended
// too, so gogcli itself refuses any mutating call rather than trusting the
// caller not to attempt one.
func RunGoogleCLI(ctx context.Context, connectionID string, args []string) (string, error) {
	return RunGoogleCLIIn(ctx, connectionID, args, GmailUseScope{})
}

// RunGoogleCLIIn is RunGoogleCLI for a use in scope (see GmailUseScope).
func RunGoogleCLIIn(ctx context.Context, connectionID string, args []string, scope GmailUseScope) (string, error) {
	trimmed := make([]string, 0, len(args))
	for _, a := range args {
		if v := strings.TrimSpace(a); v != "" {
			trimmed = append(trimmed, v)
		}
	}
	if len(trimmed) == 0 {
		return "", fmt.Errorf("args must start with a supported Google service name; read the installed gog service skill for current syntax")
	}
	for _, a := range trimmed {
		lower := strings.ToLower(a)
		for _, bad := range googleCLIDisallowedArgs {
			if lower == bad || strings.HasPrefix(lower, bad+"=") {
				return "", fmt.Errorf("the %q flag is set automatically for the authorized connection and must not be passed", bad)
			}
		}
	}

	service := strings.ToLower(trimmed[0])
	if service != "gmail" {
		if _, ok := googleServiceCatalog[service]; !ok {
			return "", fmt.Errorf("unknown Google service %q — the first argument must be one of: %s", service, strings.Join(sortedGoogleServiceNames(), ", "))
		}
	}

	svc := GetGmailService()
	if svc == nil {
		return "", fmt.Errorf("Google account connections are not configured on this server")
	}
	access, err := svc.GoogleCLIAccessForConnectionIn(ctx, connectionID, scope)
	if err != nil {
		return "", err
	}
	writeAllowed, granted := access.Grants[service]
	if !granted {
		return "", fmt.Errorf("this connection is not authorized for %q — enable it in workflow bots settings and reconnect", service)
	}

	home := access.Home
	if home == "" {
		home = gogHomeDir()
	}
	finalArgs := append([]string{"--home", home}, trimmed...)
	if access.Token != "" {
		finalArgs = append(finalArgs, "--access-token", access.Token)
	} else {
		finalArgs = append(finalArgs, "--account", access.Account, "--client", access.Client)
	}
	if !writeAllowed {
		finalArgs = append(finalArgs, "--readonly")
	}
	if service == "gmail" && !writeAllowed {
		// Keep gog's Gmail-specific send guard as defense in depth unless the
		// connection explicitly resolved to live, opted-in agent write access.
		finalArgs = append(finalArgs, "--gmail-no-send")
	}

	cmdCtx, cancel := context.WithTimeout(ctx, googleCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, access.GogPath, finalArgs...)
	out, runErr := cmd.CombinedOutput()
	text := string(out)
	if len(text) > googleCLIOutputLimit {
		text = text[:googleCLIOutputLimit] + "\n...[truncated]"
	}
	if runErr != nil {
		return text, fmt.Errorf("gog exited with an error: %w", runErr)
	}
	return text, nil
}
