package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/netguard"
	"github.com/manishiitg/mcpagent/oauth"
)

// Personal MCP servers (docs/design/code_private_mcp.md): a person's own
// remote MCP servers, logins and secrets, used only in that person's own chats
// in the Codes where they switched them on. One store per person, server-owned
// and outside every workspace:
//
//	<state root>/personal-mcp/<store id>/   (0700)
//	  servers.json   the person's servers (no secrets)
//	  enabled.json   { "<code root>": ["linear", ...] }
//	  secrets.json   { "<name>": "<AES-GCM ciphertext>" }
//	  tokens/        OAuth tokens, sealed at rest (personalMCPTokenSealer)
//
// It reuses the platform pieces: the state root (workflowCLIStateRoot), the
// secrets key and AES-GCM helpers (encryptSecretValueWithAAD), mcpagent's
// OAuth token store (sealed through oauth.SetTokenSealer) and its public-only
// connections (MCPServerConfig.PublicOnly).

const personalMCPDirName = "personal-mcp"

// Letters, digits and underscores only: the bridge lowercases server names
// and turns "-" into "_" in tool paths, so a name must survive that unchanged
// to resolve exactly.
var personalMCPNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,39}$`)

// personalMCPServer is one of a person's servers. It never holds a secret:
// credential headers name a personal secret, resolved at connect time.
type personalMCPServer struct {
	Name      string                       `json:"name"`
	URL       string                       `json:"url"`
	Transport string                       `json:"transport"` // "http" or "sse"
	OAuth     *oauth.OAuthConfig           `json:"oauth,omitempty"`
	Headers   map[string]personalMCPHeader `json:"headers,omitempty"`
	AddedAt   string                       `json:"added_at,omitempty"`
	Catalog   string                       `json:"catalog,omitempty"` // set by the server, never by the request
	// AppKey names the deployment's sign-in app this server signs in
	// through (set by the server from the catalog, never by the request).
	AppKey string `json:"app_key,omitempty"`
}

// personalMCPHeader is a credential header built from one personal secret:
// Format holds "{}" where the value goes (default: the value itself).
type personalMCPHeader struct {
	Secret string `json:"secret"`
	Format string `json:"format,omitempty"`
}

var personalMCPMu sync.Mutex

func personalMCPRoot() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, personalMCPDirName), nil
}

// personalMCPStoreID names a person's store without putting the user id in a
// path or a connection name.
// placeStoreOwner is the person a place's store belongs to (place:<owner>:
// <root>); ok is false for a person's own store id.
func placeStoreOwner(storeID string) (string, bool) {
	rest, ok := strings.CutPrefix(storeID, "place:")
	if !ok {
		return "", false
	}
	owner, _, found := strings.Cut(rest, ":")
	return owner, found && owner != ""
}

// placeStoreRoot is the workflow, Crew or Code root a place's store belongs to.
func placeStoreRoot(storeID string) (string, bool) {
	rest, ok := strings.CutPrefix(storeID, "place:")
	if !ok {
		return "", false
	}
	_, root, found := strings.Cut(rest, ":")
	return root, found && root != ""
}

// projectSecretReader reads one secret of a project (workflow, Crew or Code)
// from the shared project secret store. Set when the server starts.
var projectSecretReader func(root, name string) (string, error)

// headerSecretValue is the value of a header server's secret. A place's
// connection uses the place's own project secrets (Setup > Secrets), exactly
// like the rest of that Crew, Code or workflow; only a person's own store
// (legacy, migrated away) uses their personal secrets.
func headerSecretValue(storeID, name string) (string, error) {
	if root, ok := placeStoreRoot(storeID); ok {
		if projectSecretReader == nil {
			return "", fmt.Errorf("project secrets are unavailable")
		}
		return projectSecretReader(root, name)
	}
	return personalSecretValue(storeID, name)
}

func personalMCPStoreID(userID string) string {
	id := strings.TrimSpace(userID)
	// A place's id (place:<owner>:<root>) carries colons and slashes, which
	// sanitizeUserIDForPath maps to "default": every place would then share
	// one store, and two places' same-named connections (each Crew's gmail)
	// would overwrite each other's login. Hash the whole id instead.
	if _, isPlace := placeStoreOwner(id); !isPlace {
		id = sanitizeUserIDForPath(id)
	}
	sum := sha256.Sum256([]byte("personal-mcp\x00" + id))
	return hex.EncodeToString(sum[:16])
}

func personalMCPDir(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("personal MCP servers need a signed-in person")
	}
	root, err := personalMCPRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, personalMCPStoreID(userID))
	if err := os.MkdirAll(filepath.Join(dir, "tokens"), 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// personalMCPInternalName is the name every cache, pooled connection and
// generated package uses for a person's server, so a personal "linear" never
// shares a cached tool list or connection with the platform "linear" or with
// another person's. It carries the person's whole 128-bit store id: pooled
// connections are keyed by this name alone, so a shorter id could let two
// people collide on one connection (and its login). The UI shows the plain name.
func personalMCPInternalName(userID, name string) string {
	return "u" + personalMCPStoreID(userID) + "__" + name
}

func readPersonalMCPJSON(path string, into any) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is inside the person's own store
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, into)
}

// writePersonalMCPJSON replaces a store file atomically, owner-only.
func writePersonalMCPJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".personal-mcp-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// validatePersonalMCPServer applies the personal-server rules: a plain name,
// a remote transport and a public https URL. Headers may name only a secret
// (resolved from this person's own secrets), never carry a value.
func validatePersonalMCPServer(server *personalMCPServer) error {
	server.Name = strings.ToLower(strings.TrimSpace(server.Name))
	if !personalMCPNamePattern.MatchString(server.Name) {
		return fmt.Errorf("server name must be 1-40 lowercase letters, digits or underscores")
	}
	server.URL = strings.TrimSpace(server.URL)
	if err := netguard.CheckURL(server.URL, true); err != nil {
		return fmt.Errorf("server URL refused: %w", err)
	}
	switch server.Transport = strings.ToLower(strings.TrimSpace(server.Transport)); server.Transport {
	case "":
		server.Transport = "http"
	case "http", "sse":
	default:
		return fmt.Errorf("personal MCP servers are remote only (http or sse)")
	}
	for header, ref := range server.Headers {
		if strings.TrimSpace(header) == "" || strings.ContainsAny(header, "\r\n:") {
			return fmt.Errorf("invalid header name %q", header)
		}
		if !personalSecretNamePattern.MatchString(ref.Secret) {
			return fmt.Errorf("header %q must name one of your personal secrets", header)
		}
		if ref.Format != "" && !strings.Contains(ref.Format, "{}") {
			return fmt.Errorf("header %q format must contain {} where the secret goes", header)
		}
	}
	if server.OAuth != nil {
		copied := *server.OAuth
		copied.TokenFile = ""                                 // set per person at resolve time, never stored
		copied.ClientSecret, copied.ClientSecretFile = "", "" // the sealed client file, or the deployment's app, holds the client
		copied.PublicOnly = true
		server.OAuth = &copied
	}
	return nil
}

func listPersonalMCPServers(userID string) ([]personalMCPServer, error) {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	return listPersonalMCPServersLocked(userID)
}

func listPersonalMCPServersLocked(userID string) ([]personalMCPServer, error) {
	dir, err := personalMCPDir(userID)
	if err != nil {
		return nil, err
	}
	var servers []personalMCPServer
	if err := readPersonalMCPJSON(filepath.Join(dir, "servers.json"), &servers); err != nil {
		return nil, err
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers, nil
}

// addPersonalMCPServer adds or replaces one of the person's servers.
func addPersonalMCPServer(userID string, server personalMCPServer) (personalMCPServer, error) {
	if err := validatePersonalMCPServer(&server); err != nil {
		return personalMCPServer{}, err
	}
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	servers, err := listPersonalMCPServersLocked(userID)
	if err != nil {
		return personalMCPServer{}, err
	}
	server.AddedAt = time.Now().UTC().Format(time.RFC3339)
	kept := servers[:0]
	for _, existing := range servers {
		if existing.Name != server.Name {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, server)
	dir, _ := personalMCPDir(userID)
	return server, writePersonalMCPJSON(filepath.Join(dir, "servers.json"), kept)
}

// removePersonalMCPServer deletes the server, its login and every Code's
// switch for it. Open connections are closed by the caller.
func removePersonalMCPServer(userID, name string) error {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	servers, err := listPersonalMCPServersLocked(userID)
	if err != nil {
		return err
	}
	kept := servers[:0]
	found := false
	for _, existing := range servers {
		if existing.Name == name {
			found = true
			continue
		}
		kept = append(kept, existing)
	}
	if !found {
		return fmt.Errorf("you have no MCP server named %q", name)
	}
	dir, _ := personalMCPDir(userID)
	if err := writePersonalMCPJSON(filepath.Join(dir, "servers.json"), kept); err != nil {
		return err
	}
	_ = os.Remove(personalMCPTokenFile(dir, userID, name))
	enabled := map[string][]string{}
	if err := readPersonalMCPJSON(filepath.Join(dir, "enabled.json"), &enabled); err != nil {
		return err
	}
	for root, names := range enabled {
		enabled[root] = removeString(names, name)
		if len(enabled[root]) == 0 {
			delete(enabled, root)
		}
	}
	return writePersonalMCPJSON(filepath.Join(dir, "enabled.json"), enabled)
}

func personalMCPTokenFile(dir, userID, name string) string {
	return filepath.Join(dir, "tokens", personalMCPInternalName(userID, name)+".json")
}

// setPersonalMCPEnabledNames replaces the names switched on in one Code (the
// migration records what it has not moved yet).
func setPersonalMCPEnabledNames(userID, codeRoot string, names []string) error {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	dir, err := personalMCPDir(userID)
	if err != nil {
		return err
	}
	enabled := map[string][]string{}
	if err := readPersonalMCPJSON(filepath.Join(dir, "enabled.json"), &enabled); err != nil {
		return err
	}
	root := cleanCodeRoot(codeRoot)
	if len(names) == 0 {
		delete(enabled, root)
	} else {
		enabled[root] = append([]string(nil), names...)
	}
	return writePersonalMCPJSON(filepath.Join(dir, "enabled.json"), enabled)
}

// ---- legacy personal secrets -------------------------------------------
// Header servers now use their project's secrets (headerSecretValue). A
// person's own secrets remain only so the migration can copy them into the
// Code that used them (personal_mcp_migrate.go).

var personalSecretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func personalSecretAAD(userID, name string) []byte {
	return []byte("personal-secret\x00" + personalMCPStoreID(userID) + "\x00" + name)
}

func setPersonalSecret(userID, name, value string) error {
	if !personalSecretNamePattern.MatchString(name) {
		return fmt.Errorf("secret names are UPPER_SNAKE_CASE, up to 64 characters")
	}
	if value == "" {
		return fmt.Errorf("a secret needs a value")
	}
	sealed, err := encryptSecretValueWithAAD(value, personalSecretAAD(userID, name))
	if err != nil {
		return err
	}
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	dir, err := personalMCPDir(userID)
	if err != nil {
		return err
	}
	secrets := map[string]string{}
	if err := readPersonalMCPJSON(filepath.Join(dir, "secrets.json"), &secrets); err != nil {
		return err
	}
	secrets[name] = sealed
	return writePersonalMCPJSON(filepath.Join(dir, "secrets.json"), secrets)
}

// personalSecretValue resolves one of the person's own legacy secrets.
func personalSecretValue(userID, name string) (string, error) {
	personalMCPMu.Lock()
	defer personalMCPMu.Unlock()
	dir, err := personalMCPDir(userID)
	if err != nil {
		return "", err
	}
	secrets := map[string]string{}
	if err := readPersonalMCPJSON(filepath.Join(dir, "secrets.json"), &secrets); err != nil {
		return "", err
	}
	sealed, ok := secrets[name]
	if !ok {
		return "", fmt.Errorf("you have no personal secret named %s", name)
	}
	return decryptSecretValueWithAAD(sealed, personalSecretAAD(userID, name))
}

// ---- resolution ---------------------------------------------------------

// personalMCPServerConfig builds the connection config for one server of a
// store (a place's, e.g. place:<owner>:<root>): public-only (mcpagent refuses
// anything but a public https remote), credential headers from the project's
// secrets, OAuth tokens in the store's own sealed token file.
func personalMCPServerConfig(userID, name string) (string, mcpclient.MCPServerConfig, error) {
	return personalMCPServerConfigFor(userID, name, false)
}

// personalMCPServerConfigFor builds the connect config. forSignIn points a
// grouped server at its group's login even if it still has its own, so the
// sign-in moves it to the shared login.
func personalMCPServerConfigFor(userID, name string, forSignIn bool) (string, mcpclient.MCPServerConfig, error) {
	servers, err := listPersonalMCPServers(userID)
	if err != nil {
		return "", mcpclient.MCPServerConfig{}, err
	}
	for _, server := range servers {
		if server.Name != name {
			continue
		}
		cfg := mcpclient.MCPServerConfig{URL: server.URL, Protocol: mcpclient.ProtocolType(server.Transport), PublicOnly: true}
		if len(server.Headers) > 0 {
			cfg.Headers = map[string]string{}
			for header, ref := range server.Headers {
				value, err := headerSecretValue(userID, ref.Secret)
				if err != nil {
					return "", mcpclient.MCPServerConfig{}, err
				}
				if ref.Format != "" {
					value = strings.ReplaceAll(ref.Format, "{}", value)
				}
				cfg.Headers[header] = value
			}
		}
		if server.OAuth != nil {
			dir, err := personalMCPDir(userID)
			if err != nil {
				return "", mcpclient.MCPServerConfig{}, err
			}
			copied := *server.OAuth
			copied.PublicOnly = true
			copied.TokenFile = personalMCPTokenFile(dir, userID, name)
			if group := personalMCPGroupOf(dir, userID, server); group != "" {
				groupFile := personalMCPGroupTokenFile(dir, group)
				// A server with its own login keeps it until the next
				// sign-in moves it to the group's.
				if forSignIn || !fileExists(copied.TokenFile) || fileExists(groupFile) {
					copied.TokenFile = groupFile
					copied.Scopes = personalMCPGroupScopes(dir, userID, group, servers)
				}
			}
			copied.ClientSecretFile = "" // a personal server reads only its own client file
			// The client (registered, entered by the person, or copied
			// from the catalog) lives sealed beside the token; a refresh
			// needs it as much as the first sign-in.
			if copied.ClientID == "" {
				client, err := readPersonalMCPClient(dir, userID, name)
				if err != nil {
					return "", mcpclient.MCPServerConfig{}, err
				}
				if client != nil {
					copied.ClientID, copied.ClientSecret = client.ClientID, client.ClientSecret
				} else if appKey := personalMCPAppKey(server); appKey != "" {
					// The deployment's sign-in app, read live so a rotated
					// app reaches everyone. A client the person entered
					// (above) always wins.
					app, err := readMCPApp(appKey)
					if err != nil {
						return "", mcpclient.MCPServerConfig{}, err
					}
					if app != nil {
						copied.ClientID, copied.ClientSecret = app.ClientID, app.ClientSecret
					}
				}
			}
			cfg.OAuth = &copied
		}
		return personalMCPInternalName(userID, name), cfg, nil
	}
	return "", mcpclient.MCPServerConfig{}, fmt.Errorf("you have no MCP server named %q", name)
}

// readPersonalMCPClient returns the person's OAuth client for a server, or
// nil when there is none yet.
func readPersonalMCPClient(dir, userID, name string) (*registeredClient, error) {
	data, err := oauth.ReadTokenFile(personalMCPClientFile(dir, userID, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sign-in client: %w", err)
	}
	var client registeredClient
	if err := json.Unmarshal(data, &client); err != nil || client.ClientID == "" {
		return nil, fmt.Errorf("unreadable sign-in client")
	}
	return &client, nil
}

// writePersonalMCPClient stores a client ID and secret sealed in the
// person's store.
func writePersonalMCPClient(userID, name string, client registeredClient) error {
	dir, err := personalMCPDir(userID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(client)
	if err != nil {
		return err
	}
	return oauth.WriteTokenFile(personalMCPClientFile(dir, userID, name), data)
}

// ---- tokens at rest ----------------------------------------------------

// personalMCPTokenSealer encrypts every token file under the personal store
// (mcpagent's oauth.SetTokenSealer), bound to its own path so a sealed token
// cannot be moved to another person's store and opened there.
type personalMCPTokenSealer struct{}

func (personalMCPTokenSealer) Handles(path string) bool {
	root, err := personalMCPRoot()
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func personalMCPTokenAAD(path string) []byte {
	root, _ := personalMCPRoot()
	rel, _ := filepath.Rel(root, filepath.Clean(path))
	return []byte("personal-mcp-token\x00" + filepath.ToSlash(rel))
}

func (personalMCPTokenSealer) Seal(path string, plaintext []byte) ([]byte, error) {
	sealed, err := encryptSecretValueWithAAD(string(plaintext), personalMCPTokenAAD(path))
	return []byte(sealed), err
}

func (personalMCPTokenSealer) Open(path string, sealed []byte) ([]byte, error) {
	plaintext, err := decryptSecretValueWithAAD(strings.TrimSpace(string(sealed)), personalMCPTokenAAD(path))
	return []byte(plaintext), err
}

func init() { oauth.SetTokenSealer(credentialSealer{}) }

// credentialSealer is the process-wide token sealer: personal MCP files and
// platform OAuth client files (platform_client_sealing.go), each bound to its
// own path.
type credentialSealer struct{}

func (credentialSealer) Handles(path string) bool {
	return personalMCPTokenSealer{}.Handles(path) || platformClientSealer{}.Handles(path)
}

func (credentialSealer) Seal(path string, plaintext []byte) ([]byte, error) {
	if (personalMCPTokenSealer{}).Handles(path) {
		return personalMCPTokenSealer{}.Seal(path, plaintext)
	}
	return platformClientSealer{}.Seal(path, plaintext)
}

func (credentialSealer) Open(path string, sealed []byte) ([]byte, error) {
	if (personalMCPTokenSealer{}).Handles(path) {
		return personalMCPTokenSealer{}.Open(path, sealed)
	}
	return platformClientSealer{}.Open(path, sealed)
}

// personalMCPAppKey is the sign-in app a personal server uses: the key
// recorded when it was added, or, for a catalog server added before apps
// existed, the key its own sign-in endpoints imply.
func personalMCPAppKey(server personalMCPServer) string {
	if server.AppKey != "" {
		return server.AppKey
	}
	if server.Catalog != "" && server.OAuth != nil && server.OAuth.RegistrationEndpoint == "" {
		return mcpAppKeyFor(server.Catalog, server.OAuth)
	}
	return ""
}
