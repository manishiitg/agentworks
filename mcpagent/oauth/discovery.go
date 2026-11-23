package oauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DiscoverFromResponse extracts OAuth endpoints from a 401 Unauthorized response.
// It parses the WWW-Authenticate and Link headers to find authorization and token URLs.
// It also supports RFC 9728 (Protected Resource Metadata) via the resource_metadata parameter.
//
// Example headers:
//
//	WWW-Authenticate: Bearer realm="https://auth.example.com"
//	WWW-Authenticate: Bearer resource_metadata="https://server.example.com/.well-known/oauth-protected-resource"
//	Link: <https://auth.example.com/token>; rel="token_endpoint"
func DiscoverFromResponse(resp *http.Response) (*OAuthEndpoints, error) {
	return Discoverer{}.DiscoverFromResponse(resp)
}

// DiscoverFromResponse follows a 401's WWW-Authenticate to the protected
// resource and authorization server metadata with d's HTTP client.
func (d Discoverer) DiscoverFromResponse(resp *http.Response) (*OAuthEndpoints, error) {
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("expected 401 response, got %d", resp.StatusCode)
	}

	endpoints := &OAuthEndpoints{}

	// Get base URL from response request
	baseURL := ""
	if resp.Request != nil && resp.Request.URL != nil {
		base := resp.Request.URL
		baseURL = fmt.Sprintf("%s://%s", base.Scheme, base.Host)
	}

	// Parse WWW-Authenticate header
	authHeader := resp.Header.Get("WWW-Authenticate")
	if authHeader == "" {
		return nil, ErrNoAuthHeader
	}

	// First, check for RFC 9728 Protected Resource Metadata (resource_metadata parameter)
	// This is the modern approach used by servers like Smithery
	resourceMetadataURL := extractResourceMetadata(authHeader)
	if resourceMetadataURL != "" {
		// Fetch the protected resource metadata
		resourceMetadata, err := d.FetchProtectedResourceMetadata(resourceMetadataURL)
		if err == nil && len(resourceMetadata.AuthorizationServers) > 0 {
			// Use the first authorization server
			authServerURL := resourceMetadata.AuthorizationServers[0]

			// Discover endpoints from the authorization server
			discoveredEndpoints, _, err := d.DiscoverFromAuthorizationServer(authServerURL)
			if err == nil && discoveredEndpoints != nil {
				// Propagate resource and scopes from Protected Resource Metadata
				discoveredEndpoints.Resource = resourceMetadata.Resource
				discoveredEndpoints.ScopesSupported = resourceMetadata.ScopesSupported
				return discoveredEndpoints, nil
			}
		}
	}

	// Fall back to traditional realm-based discovery
	// Extract realm from WWW-Authenticate (this is usually the base auth URL or issuer)
	realm := extractRealm(authHeader)
	if realm != "" {
		// Check if realm is a valid URL (starts with http/https) or looks like one
		// Some servers return non-URL realm values like "OAuth" or "Bearer"
		if strings.HasPrefix(realm, "http://") || strings.HasPrefix(realm, "https://") {
			// Realm is already a full URL
			endpoints.Issuer = realm

			// Derive authorization URL from realm
			// Common patterns: realm might be the issuer or authorization URL itself
			if strings.Contains(realm, "/oauth/authorize") || strings.Contains(realm, "/authorize") {
				endpoints.AuthURL = realm
			} else {
				// Append /oauth/authorize as common pattern
				endpoints.AuthURL = strings.TrimSuffix(realm, "/") + "/oauth/authorize"
			}
		} else if strings.HasPrefix(realm, "/") {
			// Realm is a relative path - make it absolute
			endpoints.Issuer = baseURL + realm
			if strings.Contains(realm, "/oauth/authorize") || strings.Contains(realm, "/authorize") {
				endpoints.AuthURL = baseURL + realm
			} else {
				endpoints.AuthURL = baseURL + strings.TrimSuffix(realm, "/") + "/oauth/authorize"
			}
		} else {
			// Realm is not a URL (e.g., "OAuth", "Bearer") - use base URL with standard paths
			// This is common for servers that don't follow the full OAuth discovery spec
			endpoints.Issuer = baseURL
			endpoints.AuthURL = baseURL + "/authorize"
		}
	}

	// Parse Link header for token endpoint
	linkHeader := resp.Header.Get("Link")
	if linkHeader != "" {
		tokenURL := extractLinkRelation(linkHeader, "token_endpoint")
		if tokenURL != "" {
			endpoints.TokenURL = makeAbsoluteURL(tokenURL, baseURL)
		}
	}

	// If token URL not found via Link header, derive from issuer or base URL
	if endpoints.TokenURL == "" {
		if endpoints.Issuer != "" {
			endpoints.TokenURL = strings.TrimSuffix(endpoints.Issuer, "/") + "/token"
		} else if baseURL != "" {
			endpoints.TokenURL = baseURL + "/token"
		}
	}

	// Validate we got at least the essential endpoints
	if endpoints.AuthURL == "" && endpoints.TokenURL == "" {
		return nil, ErrEndpointsNotFound
	}

	return endpoints, nil
}

// makeAbsoluteURL converts a relative URL to absolute using the base URL
func makeAbsoluteURL(urlStr, baseURL string) string {
	// If already absolute (has scheme), return as-is
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		return urlStr
	}

	// If relative, combine with base URL
	if baseURL != "" {
		// Ensure path starts with /
		if !strings.HasPrefix(urlStr, "/") {
			urlStr = "/" + urlStr
		}
		return baseURL + urlStr
	}

	return urlStr
}

// extractRealm extracts the realm value from a WWW-Authenticate header
// Example: Bearer realm="https://auth.example.com" -> https://auth.example.com
func extractRealm(authHeader string) string {
	// Match: realm="..." or realm='...' or realm=...
	realmRegex := regexp.MustCompile(`realm=["']?([^"'\s,]+)["']?`)
	matches := realmRegex.FindStringSubmatch(authHeader)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractResourceMetadata extracts the resource_metadata URL from a WWW-Authenticate header
// Example: Bearer resource_metadata="https://server.example.com/.well-known/oauth-protected-resource"
func extractResourceMetadata(authHeader string) string {
	// Match: resource_metadata="..." or resource_metadata='...' or resource_metadata=...
	metadataRegex := regexp.MustCompile(`resource_metadata=["']?([^"'\s,]+)["']?`)
	matches := metadataRegex.FindStringSubmatch(authHeader)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// extractLinkRelation extracts a URL from Link header with specific rel value
// Example: <https://example.com/token>; rel="token_endpoint" -> https://example.com/token
func extractLinkRelation(linkHeader, rel string) string {
	// Split multiple Link headers
	links := strings.Split(linkHeader, ",")

	for _, link := range links {
		link = strings.TrimSpace(link)

		// Check if this link has the desired relation
		if !strings.Contains(link, fmt.Sprintf(`rel="%s"`, rel)) &&
			!strings.Contains(link, fmt.Sprintf(`rel='%s'`, rel)) &&
			!strings.Contains(link, fmt.Sprintf("rel=%s", rel)) {
			continue
		}

		// Extract URL from <...>
		urlRegex := regexp.MustCompile(`<([^>]+)>`)
		matches := urlRegex.FindStringSubmatch(link)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

// AuthServerMetadata represents OAuth 2.0 Authorization Server Metadata (RFC 8414)
type AuthServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	ScopesSupported       []string `json:"scopes_supported,omitempty"`
}

// ProtectedResourceMetadata represents OAuth 2.0 Protected Resource Metadata (RFC 9728)
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// Discoverer runs OAuth discovery and registration with one HTTP client. The
// zero value uses http.DefaultClient; a user-supplied server (a personal MCP
// server) passes a public-only client (netguard) so discovery, metadata and
// registration can never reach the platform's own network.
type Discoverer struct {
	Client *http.Client
}

// maxDiscoveryBody bounds a metadata or registration response.
const maxDiscoveryBody = 1 << 20

func (d Discoverer) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return http.DefaultClient
}

func (d Discoverer) get(u string) (*http.Response, error) { return d.client().Get(u) }

func (d Discoverer) post(u, contentType string, body io.Reader) (*http.Response, error) {
	return d.client().Post(u, contentType, body)
}

// DiscoverFromWellKnown is Discoverer{}.DiscoverFromWellKnown.
func DiscoverFromWellKnown(serverURL string) (*OAuthEndpoints, error) {
	return Discoverer{}.DiscoverFromWellKnown(serverURL)
}

// FetchProtectedResourceMetadata is Discoverer{}.FetchProtectedResourceMetadata.
func FetchProtectedResourceMetadata(resourceMetadataURL string) (*ProtectedResourceMetadata, error) {
	return Discoverer{}.FetchProtectedResourceMetadata(resourceMetadataURL)
}

// DiscoverFromAuthorizationServer is Discoverer{}.DiscoverFromAuthorizationServer.
func DiscoverFromAuthorizationServer(authServerURL string) (*OAuthEndpoints, *AuthServerMetadata, error) {
	return Discoverer{}.DiscoverFromAuthorizationServer(authServerURL)
}

// FetchAuthServerMetadata is Discoverer{}.FetchAuthServerMetadata.
func FetchAuthServerMetadata(serverURL string) (*AuthServerMetadata, error) {
	return Discoverer{}.FetchAuthServerMetadata(serverURL)
}

// RegisterClient is Discoverer{}.RegisterClient.
func RegisterClient(registrationEndpoint, redirectURI string) (*ClientRegistrationResponse, error) {
	return Discoverer{}.RegisterClient(registrationEndpoint, redirectURI)
}

// DiscoverFromWellKnown attempts to discover OAuth endpoints using RFC 8414
// (OAuth 2.0 Authorization Server Metadata) from the server's base URL
func (d Discoverer) DiscoverFromWellKnown(serverURL string) (*OAuthEndpoints, error) {
	metadata, err := d.FetchAuthServerMetadata(serverURL)
	if err != nil {
		return nil, err
	}

	return &OAuthEndpoints{
		Issuer:   metadata.Issuer,
		AuthURL:  metadata.AuthorizationEndpoint,
		TokenURL: metadata.TokenEndpoint,
	}, nil
}

// FetchProtectedResourceMetadata fetches the OAuth 2.0 Protected Resource Metadata (RFC 9728)
// from the given resource_metadata URL
func (d Discoverer) FetchProtectedResourceMetadata(resourceMetadataURL string) (*ProtectedResourceMetadata, error) {
	//nolint:gosec // G107: Discovery URLs are dynamic by design
	resp, err := d.get(resourceMetadataURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch protected resource metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("protected resource metadata endpoint returned %d", resp.StatusCode)
	}

	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBody)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse protected resource metadata: %w", err)
	}

	if len(metadata.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("protected resource metadata missing authorization_servers")
	}

	return &metadata, nil
}

// DiscoverFromAuthorizationServer discovers OAuth endpoints from an authorization server URL
// by trying both path-specific and root-level well-known endpoints
func (d Discoverer) DiscoverFromAuthorizationServer(authServerURL string) (*OAuthEndpoints, *AuthServerMetadata, error) {
	// Parse the auth server URL
	parsedURL, err := url.Parse(authServerURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid auth server URL: %w", err)
	}

	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

	// Try path-specific well-known first (e.g., https://auth.example.com/.well-known/oauth-authorization-server/twitter)
	if parsedURL.Path != "" && parsedURL.Path != "/" {
		pathSpecificURL := baseURL + "/.well-known/oauth-authorization-server" + parsedURL.Path
		metadata, err := d.fetchAuthServerMetadataFromURL(pathSpecificURL)
		if err == nil {
			return &OAuthEndpoints{
				Issuer:               metadata.Issuer,
				AuthURL:              metadata.AuthorizationEndpoint,
				TokenURL:             metadata.TokenEndpoint,
				RegistrationEndpoint: metadata.RegistrationEndpoint,
			}, metadata, nil
		}
	}

	// Try root well-known
	rootURL := baseURL + "/.well-known/oauth-authorization-server"
	metadata, err := d.fetchAuthServerMetadataFromURL(rootURL)
	if err == nil {
		return &OAuthEndpoints{
			Issuer:               metadata.Issuer,
			AuthURL:              metadata.AuthorizationEndpoint,
			TokenURL:             metadata.TokenEndpoint,
			RegistrationEndpoint: metadata.RegistrationEndpoint,
		}, metadata, nil
	}

	// Try OpenID Connect Discovery fallback
	oidcURL := baseURL + "/.well-known/openid-configuration"
	metadata, err = d.fetchAuthServerMetadataFromURL(oidcURL)
	if err == nil {
		return &OAuthEndpoints{
			Issuer:               metadata.Issuer,
			AuthURL:              metadata.AuthorizationEndpoint,
			TokenURL:             metadata.TokenEndpoint,
			RegistrationEndpoint: metadata.RegistrationEndpoint,
		}, metadata, nil
	}

	// If all well-known discovery fails, try to construct endpoints from the auth server URL directly
	// Some auth servers (like Smithery) use the auth server URL as the authorize endpoint directly
	return &OAuthEndpoints{
		Issuer:   authServerURL,
		AuthURL:  authServerURL + "/authorize",
		TokenURL: authServerURL + "/token",
	}, nil, nil
}

// fetchAuthServerMetadataFromURL fetches auth server metadata from a specific URL
func (d Discoverer) fetchAuthServerMetadataFromURL(wellKnownURL string) (*AuthServerMetadata, error) {
	//nolint:gosec // G107: Discovery URLs are dynamic by design
	resp, err := d.get(wellKnownURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch well-known metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("well-known endpoint returned %d", resp.StatusCode)
	}

	var metadata AuthServerMetadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBody)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("metadata missing required endpoints")
	}

	return &metadata, nil
}

// FetchAuthServerMetadata fetches the OAuth 2.0 Authorization Server Metadata
func (d Discoverer) FetchAuthServerMetadata(serverURL string) (*AuthServerMetadata, error) {
	// Parse the server URL to get the base
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// Construct well-known URL: https://server.com/.well-known/oauth-authorization-server
	baseURL := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
	wellKnownURL := baseURL + "/.well-known/oauth-authorization-server"

	// Try to fetch the metadata
	//nolint:gosec // G107: Discovery URLs are dynamic by design
	resp, err := d.get(wellKnownURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch well-known metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("well-known endpoint returned %d", resp.StatusCode)
	}

	// Parse the JSON response
	var metadata AuthServerMetadata

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBody)).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Validate we got the required endpoints
	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return nil, fmt.Errorf("metadata missing required endpoints")
	}

	return &metadata, nil
}

// ClientRegistrationRequest represents a Dynamic Client Registration request (RFC 7591)
type ClientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
}

// ClientRegistrationResponse represents the response from Dynamic Client Registration
type ClientRegistrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt   int64    `json:"client_secret_expires_at,omitempty"`
	RedirectURIs            []string `json:"redirect_uris,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
}

// What a remote server's consent screen shows for the app that asks for access. A registration is
// cached per server, so a change reaches only servers authorized after it.
const (
	ClientDisplayName = "AgentWorks"
	ClientHomepage    = "https://agentworkshq.com"
)

// RegistrationError is a rejected Dynamic Client Registration. It carries the
// provider's RFC 7591 error (for example invalid_redirect_uri) and a bounded
// copy of the body, so the failure can be logged and shown. A rejection body
// never holds a client secret; only a successful registration issues one.
type RegistrationError struct {
	StatusCode  int
	Code        string // RFC 7591 "error"
	Description string // RFC 7591 "error_description"
	Body        string // the response body, truncated to one line
}

// maxRegistrationErrorBody bounds the body kept on a RegistrationError.
const maxRegistrationErrorBody = 300

func newRegistrationError(resp *http.Response) *RegistrationError {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	e := &RegistrationError{StatusCode: resp.StatusCode}
	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(raw, &body) == nil {
		e.Code, e.Description = body.Error, body.Description
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > maxRegistrationErrorBody {
		text = text[:maxRegistrationErrorBody] + "..."
	}
	e.Body = text
	return e
}

func (e *RegistrationError) Error() string {
	msg := fmt.Sprintf("registration failed with status %d", e.StatusCode)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// Reason is the provider's own account of the rejection, for a person to read.
func (e *RegistrationError) Reason() string {
	switch {
	case e.Description != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", strings.TrimRight(e.Description, "."), e.Code)
	case e.Description != "":
		return strings.TrimRight(e.Description, ".")
	case e.Code != "":
		return e.Code
	}
	return fmt.Sprintf("the provider answered HTTP %d", e.StatusCode)
}

// RegisterClient performs Dynamic Client Registration (RFC 7591) to obtain a client_id
func (d Discoverer) RegisterClient(registrationEndpoint, redirectURI string) (*ClientRegistrationResponse, error) {
	// Prepare registration request
	regRequest := ClientRegistrationRequest{
		RedirectURIs:            []string{redirectURI},
		TokenEndpointAuthMethod: "none", // Public client (PKCE)
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              ClientDisplayName,
		ClientURI:               ClientHomepage,
	}

	// Encode request as JSON
	requestBody, err := json.Marshal(regRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to encode registration request: %w", err)
	}

	// Send POST request to registration endpoint
	//nolint:gosec // G107: Discovery URLs are dynamic by design
	resp, err := d.post(registrationEndpoint, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to send registration request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, newRegistrationError(resp)
	}

	// Parse response
	var regResponse ClientRegistrationResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscoveryBody)).Decode(&regResponse); err != nil {
		return nil, fmt.Errorf("failed to parse registration response: %w", err)
	}

	if regResponse.ClientID == "" {
		return nil, fmt.Errorf("registration response missing client_id")
	}

	return &regResponse, nil
}
