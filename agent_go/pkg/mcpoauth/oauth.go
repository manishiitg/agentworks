// Package mcpoauth is the shared OAuth authorization server for MCP clients,
// used by the AgentWorks server and the MCP Gateway.
//
// It implements the MCP client authorization shape both products expose:
// protected-resource + authorization-server metadata, dynamic client
// registration, authorize with PKCE (S256), human consent, code exchange,
// refresh-token families with reuse detection, revocation, and a connections
// list. The AgentWorks CLI device flow shares the same store.
//
// Hosts parameterize the package through Config and resolve two seams
// themselves: token storage location (OpenStore takes an explicit sqlite
// path) and the currently logged-in human (CurrentUser hook used by the
// consent and connections endpoints).
package mcpoauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// User is the human behind a consent decision. Hosts map their own session
// (AgentWorks JWT claims, gateway IdP session) to this shape.
type User struct {
	ID       string
	Username string
	Email    string
	Provider string
}

// Grant is an issued token family member: who it acts as and what it may do.
type Grant struct {
	FamilyID string
	ClientID string
	Resource string
	Scopes   []string
	UserID   string
	Username string
	Email    string
	Provider string
	Expires  int64 // unix seconds, access-token expiry
}

// Client is a dynamically registered OAuth client.
type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// AuthRequest is a pending authorization request awaiting consent.
type AuthRequest struct {
	ClientID    string
	RedirectURI string
	Resource    string
	State       string
	Scopes      []string
	Challenge   string
	ExpiresUnix int64
}

// Connection is one revocable grant family shown to the user.
type Connection struct {
	ID         string    `json:"id"`
	ClientName string    `json:"client_name"`
	Scopes     []string  `json:"scopes"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Config parameterizes one authorization server deployment.
type Config struct {
	// PublicURL is the deployment origin, e.g. https://host. HTTPS, or
	// loopback HTTP for local sign-in. Never derived from Host headers.
	PublicURL string
	// ResourcePath is the protected MCP endpoint path, e.g. /mcp.
	ResourcePath string
	// Scopes is the full scope vocabulary clients may request.
	Scopes []string
	// AccessPrefix and RefreshPrefix tag issued tokens.
	AccessPrefix  string
	RefreshPrefix string
	// CLIClientID enables the device flow when non-empty, with its own
	// token prefixes and resource path.
	CLIClientID      string
	CLIClientName    string
	CLIAccessPrefix  string
	CLIRefreshPrefix string
	CLIResourcePath  string
	CLIDefaultScopes []string
	// ConsentUIPath is the frontend page authorize redirects to, e.g.
	// /oauth/consent?request=<id>. The host renders it; the JSON consent
	// API lives wherever the host mounts HandleConsent.
	ConsentUIPath string
	// Mounted endpoint paths, echoed in metadata and challenges.
	ProtectedResourcePath string
	RegisterPath          string
	AuthorizePath         string
	TokenPath             string
	// MaxClients caps dynamic registrations (0 = 10000).
	MaxClients int
	// OpenStore opens the deployment's sqlite store. Hosts resolve the
	// path (including any secret-derived isolation rules) themselves.
	OpenStore func() (*Store, error)
	// CurrentUser resolves the logged-in human for consent/connections.
	// Returning ok=false denies with access_denied.
	CurrentUser func(r *http.Request) (*User, bool)
	// CLIBrowserOrigin overrides the device-flow verification host (local
	// dev). Nil means the server origin.
	CLIBrowserOrigin func(serverOrigin string) string
	// CLIBrowserPath is the approval page path, e.g. /oauth/cli.
	CLIBrowserPath string
}

var (
	// ErrReuse signals refresh-token replay; the family is revoked.
	ErrReuse = errors.New("refresh token reuse detected")
	// ErrPending, ErrDenied, ErrSlowDown drive device-flow polling codes.
	ErrPending  = errors.New("authorization_pending")
	ErrDenied   = errors.New("access_denied")
	ErrSlowDown = errors.New("slow_down")
)

// OriginResource validates the origin and returns origin + resource.
// HTTPS, or loopback HTTP for local sign-in only.
func OriginResource(publicURL, resourcePath string) (origin, resource string, ok bool) {
	origin = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" ||
		(u.Scheme != "https" && (u.Scheme != "http" || !IsLoopback(u.Hostname()))) {
		return "", "", false
	}
	return origin, origin + resourcePath, true
}

// IsLoopback reports whether host is localhost or a loopback IP.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidRedirect accepts https callbacks, or loopback http for local clients.
func ValidRedirect(raw string) bool {
	if raw == "" || len(raw) > 1024 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && IsLoopback(u.Hostname())
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
