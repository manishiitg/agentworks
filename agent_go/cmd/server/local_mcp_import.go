package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Importing a person's own MCP servers (GitHub #4279). Claude Code, Codex, Cursor, agy and Claude Desktop each keep
// the MCP servers a person configured in their own files. A platform chat never loads those files (PLAT-418), so a
// chat imported from one of these apps loses its tools. On the person's own Mac, the owner of a project can import
// them into their own Vault and select them for the project:
//   - a URL server becomes an ordinary Vault connection; its headers become the connection's encrypted key;
//   - a command server becomes a local command connection that this Mac's Vault starts (mcp-gateway, transport
//     "stdio"); its program, arguments and environment are the connection's encrypted key.
//
// The screen only ever sees names, the program's base name and the names of keys, never values. An import names
// servers by the ID from the list; the server reads the files again and never takes a command from the request.

type localMCPCandidate struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Sources    []string `json:"sources"`
	Scope      string   `json:"scope"` // "project" (this folder) or "all" (every folder)
	Kind       string   `json:"kind"`  // "url" or "command"
	Program    string   `json:"program"`
	Keys       []string `json:"keys,omitempty"`
	InVault    bool     `json:"in_vault,omitempty"`
	Importable bool     `json:"importable"`
	Note       string   `json:"note,omitempty"`

	url       string
	transport string
	headers   map[string]string
	command   string
	args      []string
	env       map[string]string
	dir       string
}

// localMCPEntry is one server as Claude, Cursor, agy and Claude Desktop write it (Codex is read into the same shape).
type localMCPEntry struct {
	Type      string            `json:"type"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	ServerURL string            `json:"serverUrl"`
	HTTPURL   string            `json:"httpUrl"`
	Headers   map[string]string `json:"headers"`
	Cwd       string            `json:"cwd"`
	Disabled  bool              `json:"disabled"`
}

var localMCPVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expandLocalMCPVars resolves ${VAR} and ${VAR:-default} from the file itself only: the entry's own env values and
// the default written in the placeholder. Never from this process's environment, which is AgentWorks' and holds its
// keys, so a config file (a cloned repository's .mcp.json too) can't send them to a host it names. A placeholder
// with nothing in the file is imported empty and named on the row for the person to fill in.
func expandLocalMCPVars(value string, own map[string]string, missing map[string]bool) string {
	return localMCPVar.ReplaceAllStringFunc(value, func(match string) string {
		parts := localMCPVar.FindStringSubmatch(match)
		if v, ok := own[parts[1]]; ok && !localMCPVar.MatchString(v) {
			return v
		}
		if parts[2] != "" {
			return parts[3]
		}
		missing[parts[1]] = true
		return ""
	})
}

// localMCPOwnServer reports AgentWorks' own servers, which every chat already has.
func (api *StreamingAPI) localMCPOwnServer(name string, entry localMCPEntry) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "agentworks") || lower == "api-bridge" || lower == "api_bridge" {
		return true
	}
	switch strings.ToLower(filepath.Base(entry.Command)) {
	case "agentworks", "mcpbridge", "agentworks-mcp":
		return true
	}
	if base := strings.TrimRight(api.GetAPIURL(), "/"); base != "" && localMCPEntryURL(entry) != "" && strings.HasPrefix(localMCPEntryURL(entry), base) {
		return true
	}
	return false
}

func localMCPEntryURL(entry localMCPEntry) string {
	for _, u := range []string{entry.URL, entry.ServerURL, entry.HTTPURL} {
		if strings.TrimSpace(u) != "" {
			return strings.TrimSpace(u)
		}
	}
	return ""
}

// localMCPSearchPath is the PATH a command server gets: this process's, plus the usual install folders, since an app
// opened from the Dock often starts without the person's shell PATH.
func localMCPSearchPath() string {
	home, _ := os.UserHomeDir()
	dirs := filepath.SplitList(os.Getenv("PATH"))
	extra := []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".volta", "bin"), filepath.Join(home, ".deno", "bin"), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	if nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin")); len(nvm) > 0 {
		sort.Strings(nvm)
		extra = append(extra, nvm[len(nvm)-1])
	}
	seen := map[string]bool{}
	out := []string{}
	for _, dir := range append(dirs, extra...) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func resolveLocalMCPProgram(command, searchPath string) string {
	if command == "" {
		return ""
	}
	if strings.HasPrefix(command, "~/") {
		home, _ := os.UserHomeDir()
		command = filepath.Join(home, command[2:])
	}
	if filepath.IsAbs(command) {
		if info, err := os.Stat(command); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return command
		}
		return ""
	}
	if strings.ContainsRune(command, filepath.Separator) {
		return ""
	}
	for _, dir := range filepath.SplitList(searchPath) {
		candidate := filepath.Join(dir, command)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

// localMCPCandidateFrom turns one config entry into a candidate, resolving variables and the program.
func localMCPCandidateFrom(source, scope, name string, entry localMCPEntry, folder, searchPath string) localMCPCandidate {
	missing := map[string]bool{}
	c := localMCPCandidate{Name: name, Sources: []string{source}, Scope: scope}
	if raw := localMCPEntryURL(entry); raw != "" || entry.Type == "http" || entry.Type == "sse" {
		c.Kind = "url"
		c.url = expandLocalMCPVars(raw, entry.Env, missing)
		if entry.Type == "sse" {
			c.transport = "sse"
		}
		c.headers = map[string]string{}
		for k, v := range entry.Headers {
			c.headers[k] = expandLocalMCPVars(v, entry.Env, missing)
			c.Keys = append(c.Keys, k)
		}
		if u, err := url.Parse(c.url); err == nil && u.Host != "" {
			c.Program = u.Host
		}
		c.Importable = c.Program != ""
		if !c.Importable {
			c.Note = "Its address could not be read."
		}
	} else {
		c.Kind = "command"
		c.command = resolveLocalMCPProgram(expandLocalMCPVars(entry.Command, entry.Env, missing), searchPath)
		c.Program = filepath.Base(entry.Command)
		for _, arg := range entry.Args {
			c.args = append(c.args, expandLocalMCPVars(arg, entry.Env, missing))
		}
		c.env = map[string]string{}
		for k, v := range entry.Env {
			c.env[k] = expandLocalMCPVars(v, entry.Env, missing)
			c.Keys = append(c.Keys, k)
		}
		if _, ok := c.env["PATH"]; !ok {
			c.env["PATH"] = searchPath
		}
		c.dir = expandLocalMCPVars(entry.Cwd, entry.Env, missing)
		if c.dir == "" {
			c.dir = folder
		}
		if c.dir != "" && !filepath.IsAbs(c.dir) && folder != "" {
			c.dir = filepath.Join(folder, c.dir)
		}
		c.Importable = c.command != "" && c.Program != "" && c.Program != "."
		if !c.Importable {
			c.Note = "Program " + c.Program + " is not installed on this Mac."
		}
	}
	sort.Strings(c.Keys)
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		note := "Needs " + strings.Join(names, ", ") + ", which the file does not set; it is imported empty, so add it in Vault."
		c.Note = strings.TrimSpace(c.Note + " " + note)
	}
	sum := sha256.Sum256([]byte(c.Kind + "\x00" + name + "\x00" + c.url + "\x00" + entry.Command + "\x00" + strings.Join(entry.Args, "\x00")))
	c.ID = hex.EncodeToString(sum[:8])
	return c
}

func readLocalMCPJSON(path string) map[string]localMCPEntry {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 8<<20 {
		return nil
	}
	var doc struct {
		MCPServers map[string]localMCPEntry `json:"mcpServers"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return doc.MCPServers
}

// readCodexMCP reads Codex's [mcp_servers.<name>] tables.
func readCodexMCP(path string) map[string]localMCPEntry {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 8<<20 {
		return nil
	}
	var doc struct {
		MCPServers map[string]struct {
			Command           string            `toml:"command"`
			Args              []string          `toml:"args"`
			Env               map[string]string `toml:"env"`
			EnvVars           []string          `toml:"env_vars"`
			Cwd               string            `toml:"cwd"`
			URL               string            `toml:"url"`
			BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
			HTTPHeaders       map[string]string `toml:"http_headers"`
			EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
			Enabled           *bool             `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	if toml.Unmarshal(data, &doc) != nil {
		return nil
	}
	out := map[string]localMCPEntry{}
	for name, s := range doc.MCPServers {
		entry := localMCPEntry{Command: s.Command, Args: s.Args, Env: map[string]string{}, URL: s.URL, Headers: map[string]string{}, Cwd: s.Cwd, Disabled: s.Enabled != nil && !*s.Enabled}
		for k, v := range s.Env {
			entry.Env[k] = v
		}
		for _, k := range s.EnvVars {
			if _, set := entry.Env[k]; !set {
				entry.Env[k] = "${" + k + "}"
			}
		}
		for k, v := range s.HTTPHeaders {
			entry.Headers[k] = v
		}
		for k, v := range s.EnvHTTPHeaders {
			entry.Headers[k] = "${" + v + "}"
		}
		if s.BearerTokenEnvVar != "" {
			entry.Headers["Authorization"] = "Bearer ${" + s.BearerTokenEnvVar + "}"
		}
		out[name] = entry
	}
	return out
}

// localMCPCandidates reads every app's MCP servers for this folder (folder may be empty: only every-folder servers).
func (api *StreamingAPI) localMCPCandidates(folder string) []localMCPCandidate {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	searchPath := localMCPSearchPath()
	byID := map[string]int{}
	out := []localMCPCandidate{}
	add := func(source, scope string, servers map[string]localMCPEntry) {
		names := make([]string, 0, len(servers))
		for n := range servers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			entry := servers[name]
			if entry.Disabled || api.localMCPOwnServer(name, entry) {
				continue
			}
			c := localMCPCandidateFrom(source, scope, name, entry, folder, searchPath)
			if i, ok := byID[c.ID]; ok {
				if !localMCPHasSource(out[i].Sources, source) {
					out[i].Sources = append(out[i].Sources, source)
				}
				if scope == "project" {
					out[i].Scope = scope
				}
				continue
			}
			byID[c.ID] = len(out)
			out = append(out, c)
		}
	}
	claudeConfig := filepath.Join(home, ".claude.json")
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		claudeConfig = filepath.Join(dir, ".claude.json")
	}
	if data, err := os.ReadFile(claudeConfig); err == nil && len(data) < 64<<20 {
		var doc struct {
			MCPServers map[string]localMCPEntry `json:"mcpServers"`
			Projects   map[string]struct {
				MCPServers map[string]localMCPEntry `json:"mcpServers"`
			} `json:"projects"`
		}
		if json.Unmarshal(data, &doc) == nil {
			add("Claude", "all", doc.MCPServers)
			if folder != "" {
				add("Claude", "project", doc.Projects[folder].MCPServers)
			}
		}
	}
	if folder != "" {
		add("Claude", "project", readLocalMCPJSON(filepath.Join(folder, ".mcp.json")))
		add("Cursor", "project", readLocalMCPJSON(filepath.Join(folder, ".cursor", "mcp.json")))
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	add("Codex", "all", readCodexMCP(filepath.Join(codexHome, "config.toml")))
	add("Cursor", "all", readLocalMCPJSON(filepath.Join(home, ".cursor", "mcp.json")))
	add("agy", "all", readLocalMCPJSON(filepath.Join(home, ".gemini", "config", "mcp_config.json")))
	add("agy", "all", readLocalMCPJSON(filepath.Join(home, ".gemini", "antigravity", "mcp_config.json")))
	add("Claude Desktop", "all", readLocalMCPJSON(filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")))
	return out
}

func localMCPHasSource(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// localMCPLaunch is the encrypted key of a local command connection (mcp-gateway upstream.LocalCommand).
func (c localMCPCandidate) launch() (string, error) {
	data, err := json.Marshal(map[string]any{"command": c.command, "args": c.args, "env": c.env, "dir": c.dir})
	return string(data), err
}

var localMCPKeyCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func localMCPConnectionName(name string) string {
	key := strings.Trim(localMCPKeyCleaner.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if key == "" {
		key = "imported"
	}
	if len(key) > 40 {
		key = key[:40]
	}
	return key
}

var localMCPConnectionIDPattern = regexp.MustCompile(`c-[0-9a-f]{32}`)

type localMCPImportResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // added, needs_sign_in, failed
	Message      string `json:"message,omitempty"`
	AuthURL      string `json:"auth_url,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
}

// importLocalMCP adds one candidate to the person's own Vault and selects it for the project.
func (api *StreamingAPI) importLocalMCP(ctx context.Context, person, workspace string, c localMCPCandidate) localMCPImportResult {
	result := localMCPImportResult{ID: c.ID, Name: c.Name}
	fail := func(err error) localMCPImportResult {
		result.Status, result.Message = "failed", err.Error()
		return result
	}
	if !c.Importable {
		return fail(errors.New(strings.TrimSpace("Cannot import. " + c.Note)))
	}
	var id string
	switch c.Kind {
	case "url":
		args := map[string]interface{}{"_vault_only": true, "_screen": true, "name": localMCPConnectionName(c.Name), "label": c.Name, "url": c.url}
		if c.transport != "" {
			args["transport"] = c.transport
		}
		if len(c.headers) > 0 {
			args["credential"] = c.headers
		}
		out, err := api.mcpConnectionTool(ctx, person, "install_mcp_server", args)
		if err != nil {
			return fail(err)
		}
		var screen struct {
			ConnectionID   string `json:"connection_id"`
			AuthURL        string `json:"auth_url"`
			RequiresSignIn bool   `json:"requires_sign_in"`
			Message        string `json:"message"`
		}
		_ = json.Unmarshal([]byte(out), &screen)
		id = screen.ConnectionID
		if id == "" {
			id = localMCPConnectionIDPattern.FindString(out)
		}
		result.AuthURL = screen.AuthURL
		if screen.AuthURL != "" || screen.RequiresSignIn {
			result.Status, result.Message = "needs_sign_in", "Saved. Sign in to finish."
		}
	case "command":
		vaultID, err := ensurePersonalVault(ctx, person)
		if err != nil {
			return fail(err)
		}
		launch, err := c.launch()
		if err != nil {
			return fail(err)
		}
		payload := map[string]any{"Transport": "stdio", "Name": localMCPConnectionName(c.Name), "Label": c.Name, "Credential": launch}
		data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(vaultID)+"/connectors", vaultPromoteJSON(payload))
		if err != nil {
			return fail(err)
		}
		var made struct{ ID string }
		if json.Unmarshal(data, &made) == nil {
			id = made.ID
		}
	}
	if !vaultConnectionID.MatchString(id) {
		return fail(errors.New("Vault returned no connection"))
	}
	result.ConnectionID = id
	if err := api.selectVaultServer(ctx, person, workspace, id, true); err != nil {
		return fail(fmt.Errorf("added to your Vault, but not selected for this project: %w", err))
	}
	if result.Status == "" {
		result.Status, result.Message = "added", "Added and selected for this project."
	}
	return result
}

// handleLocalMCPImport is GET (list) and POST (import) /api/vault/local-mcp, on the person's own Mac only.
func (api *StreamingAPI) handleLocalMCPImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || !cliPersonalMac() || !localGitHubCLIAllowed(claims.UserID) {
		externalError(w, 404, "not_found", "Importing your own MCP servers works in the AgentWorks Mac app.")
		return
	}
	var in struct {
		WorkspacePath string   `json:"workspace_path"`
		ProjectID     string   `json:"project_id"`
		IDs           []string `json:"ids"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
			externalError(w, 400, "bad_request", "Send workspace_path or project_id and ids.")
			return
		}
	} else {
		in.WorkspacePath, in.ProjectID = r.URL.Query().Get("workspace_path"), r.URL.Query().Get("project_id")
	}
	workspace := strings.TrimSpace(in.WorkspacePath)
	if workspace == "" && strings.TrimSpace(in.ProjectID) != "" {
		crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, in.ProjectID)
		if !ok || crew.OwnerID != claims.UserID {
			externalError(w, 404, "not_found", "Choose a project you own on this computer.")
			return
		}
		workspace = crew.Binding.WorkspacePath
	}
	workspace = cleanProjectRoot(workspace)
	if workspace == "" || !canManageProjectApps(r.Context(), claims.UserID, workspace) {
		externalError(w, 403, "forbidden", "Only the project's owner can import MCP servers into it.")
		return
	}
	folder := ""
	if roots, bound, err := localCrewRoots(workspace); err == nil && bound {
		folder = roots.Files
	}
	candidates := api.localMCPCandidates(folder)
	if r.Method != http.MethodPost {
		if rows, err := vaultServersFor(r.Context(), claims.UserID); err == nil {
			for i := range candidates {
				for _, row := range rows {
					if strings.EqualFold(strings.TrimSpace(row.Label), candidates[i].Name) {
						candidates[i].InVault = true
					}
				}
			}
		}
		writeAgentProfileJSON(w, 200, map[string]any{"servers": candidates, "folder_read": folder != ""})
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 50 {
		externalError(w, 400, "bad_request", "Choose the servers to import.")
		return
	}
	byID := map[string]localMCPCandidate{}
	for _, c := range candidates {
		byID[c.ID] = c
	}
	results := []localMCPImportResult{}
	for _, id := range in.IDs {
		c, ok := byID[id]
		if !ok {
			results = append(results, localMCPImportResult{ID: id, Status: "failed", Message: "This server is no longer in your app settings. Refresh the list."})
			continue
		}
		res := api.importLocalMCP(r.Context(), claims.UserID, workspace, c)
		reason := ""
		if res.Status == "failed" {
			reason = " reason=" + strconv.Quote(res.Message)
		}
		log.Printf("[LOCAL_MCP_IMPORT] workspace=%s server=%s kind=%s sources=%s status=%s%s", workspace, c.Name, c.Kind, strings.Join(c.Sources, ","), res.Status, reason)
		results = append(results, res)
	}
	writeAgentProfileJSON(w, 200, map[string]any{"results": results})
}
