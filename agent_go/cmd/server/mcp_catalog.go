package server

import (
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/netguard"
)

// The MCP server catalog offered when someone adds a connection.

// mcpCatalogServer is a platform catalog server a person can add as
// their own: remote, public, and signed into per person (OAuth) or open. A
// server whose credentials are platform headers is not offered.
type mcpCatalogServer struct {
	Name        string `json:"name"`
	Catalog     string `json:"catalog"`
	Description string `json:"description,omitempty"`
	SignIn      bool   `json:"sign_in"`
	// NeedsClient: the provider has no dynamic registration and the catalog
	// carries no client, so the person enters their OAuth app's client.
	NeedsClient bool `json:"needs_client"`
	// Group is the sign-in app key the server shares (google, github, ...):
	// servers with the same group are offered together and share one login.
	Group  string `json:"group,omitempty"`
	config mcpclient.MCPServerConfig
}

func (api *StreamingAPI) mcpCatalog() []mcpCatalogServer {
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil
	}
	out := []mcpCatalogServer{}
	keys := mcpAppKeyIndex(catalog.MCPServers)
	for name, cfg := range catalog.MCPServers {
		protocol := cfg.GetProtocol()
		if cfg.URL == "" || len(cfg.Headers) > 0 || (protocol != mcpclient.ProtocolHTTP && protocol != mcpclient.ProtocolSSE) {
			continue
		}
		if netguard.CheckURL(cfg.URL, true) != nil {
			continue
		}
		// Google and GitHub are not offered as MCP connectors (owner decision 2026-09-30).
		// Google apps (Gmail, Drive, Calendar, Docs, Sheets, Slides) are connected through the
		// gog integration (the Gmail tab): Google's Workspace MCP servers are a Developer Preview
		// that needs Google-side enrollment, while gog works with a normal OAuth client and keeps
		// the token on the server. GitHub is a personal access token used with git and the API.
		// An existing connection to either keeps working; new ones are not offered.
		if mcpCatalogHiddenKeys[keys[name]] {
			continue
		}
		local := strings.Trim(mcpCatalogNameCleaner.ReplaceAllString(strings.ToLower(name), "_"), "_")
		if len(local) > 40 {
			local = local[:40]
		}
		if !placeMCPNamePattern.MatchString(local) {
			continue
		}
		entry := mcpCatalogServer{Name: local, Catalog: name, Description: cfg.Description, SignIn: cfg.OAuth != nil, Group: keys[name], config: cfg}
		if cfg.OAuth != nil {
			entry.NeedsClient = cfg.OAuth.ClientID == "" && cfg.OAuth.RegistrationEndpoint == ""
			if entry.NeedsClient {
				// An app the admin set up for the provider is enough.
				if key := keys[name]; key != "" {
					if app, err := readMCPApp(key); err == nil && app != nil {
						entry.NeedsClient = false
					}
				}
			}
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Catalog < out[j].Catalog })
	return out
}

var mcpCatalogNameCleaner = regexp.MustCompile(`[^a-z0-9_]+`)

func (api *StreamingAPI) mcpCatalogEntry(name string) (mcpCatalogServer, bool) {
	name = strings.TrimSpace(name)
	entries := api.mcpCatalog()
	for _, entry := range entries {
		if entry.Catalog == name {
			return entry, true
		}
	}
	var match mcpCatalogServer
	found := false
	for _, entry := range entries {
		if strings.EqualFold(entry.Catalog, name) || strings.EqualFold(entry.Name, name) {
			if found { // Sanitized/case-folded provider aliases must be unique.
				return mcpCatalogServer{}, false
			}
			match, found = entry, true
		}
	}
	return match, found
}

// GET /api/me/mcp/catalog: the catalog servers a person can add as their own.
func (api *StreamingAPI) handleMCPCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := placeMCPUser(w, r); !ok {
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"servers": api.mcpCatalog()})
}
