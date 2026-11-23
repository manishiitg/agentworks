package oauth

// OAuthConfig defines the OAuth 2.1 configuration for MCP servers
type OAuthConfig struct {
	// Auto-discovery settings
	AutoDiscover bool `json:"auto_discover,omitempty"` // Auto-discover endpoints from 401 responses

	// OAuth endpoints (required if not using auto-discovery)
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"` // Optional for public clients (PKCE)
	// ClientSecretFile keeps the secret out of the config: a credential file
	// ({"client_secret": ...}, sealed when a token sealer claims the path)
	// read when ClientSecret is empty. The config then holds only a reference.
	ClientSecretFile string `json:"client_secret_file,omitempty"`
	AuthURL      string `json:"auth_url,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	// RegistrationEndpoint enables Dynamic Client Registration (RFC 7591) for
	// servers that issue a client_id on demand instead of accepting a Client ID
	// Metadata Document URL. Set it and leave ClientID empty; the caller
	// registers through RegisterClient and fills ClientID in at connect time.
	RegistrationEndpoint string   `json:"registration_endpoint,omitempty"`
	RedirectURL          string   `json:"redirect_url,omitempty"` // Default: http://localhost:8080/callback
	Scopes               []string `json:"scopes,omitempty"`

	// RFC 8707 Resource Indicator
	Resource string `json:"resource,omitempty"` // Resource URI for token audience restriction

	// ExtraAuthParams are added to the authorization URL. Google, for one,
	// issues a refresh token only with access_type=offline (and prompt=consent
	// on a repeat sign-in); without them its logins expire after an hour.
	ExtraAuthParams map[string]string `json:"extra_auth_params,omitempty"`

	// Security & Storage
	UsePKCE   bool   `json:"use_pkce,omitempty"`   // Default: true (recommended)
	TokenFile string `json:"token_file,omitempty"` // Path to cache tokens

	// PublicOnly marks a user-supplied server (a personal MCP server): every
	// OAuth request (token exchange, refresh) goes through a public-only HTTP
	// client (netguard), and the authorization URL must be https.
	PublicOnly bool `json:"public_only,omitempty"`
}

// SetDefaults sets default values for optional fields
func (c *OAuthConfig) SetDefaults() {
	if c.RedirectURL == "" {
		c.RedirectURL = "http://localhost:8080/callback"
	}

	// Always use PKCE for security unless explicitly disabled
	if !c.UsePKCE {
		c.UsePKCE = true
	}

	if c.TokenFile == "" {
		c.TokenFile = "~/.config/mcpagent/tokens/default.json"
	}
}

// Validate checks if the OAuth configuration is valid
func (c *OAuthConfig) Validate() error {
	// If not auto-discovering, require endpoints
	if !c.AutoDiscover {
		if c.AuthURL == "" {
			return ErrMissingAuthURL
		}
		if c.TokenURL == "" {
			return ErrMissingTokenURL
		}
	}

	return nil
}

// OAuthEndpoints contains discovered OAuth endpoint URLs
type OAuthEndpoints struct {
	AuthURL              string   // Authorization endpoint
	TokenURL             string   // Token endpoint
	Issuer               string   // Issuer URL (optional)
	RegistrationEndpoint string   // Dynamic Client Registration endpoint (optional)
	Resource             string   // RFC 8707 resource indicator (from Protected Resource Metadata)
	ScopesSupported      []string // Scopes supported (from Protected Resource Metadata)
}
