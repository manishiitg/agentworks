package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

func (api *StreamingAPI) nativeSkillsRoute(write bool) http.HandlerFunc {
	return workspaceSkillRoute(write, func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserFromContext(r.Context())
		workspace := r.URL.Query().Get("workspace_path")
		crew, ok := resolveCrewPath(r.Context(), claims.UserID, workspace)
		if !ok || crew.OwnerID != sanitizeUserIDForPath(claims.UserID) {
			http.Error(w, "Open a Crew you own to discover native skills.", 403)
			return
		}
		if token := claims.AccessToken; token != nil {
			roots, bound, err := crewStorageRoots(workspace)
			scope := "crews:read"
			if write {
				scope = "crews:write"
			}
			// Same native-file admission as localProjectAccess: a file-limited
			// or another-Crew token cannot expand into personal/native folders.
			if err != nil || !bound || token.FileGuard != nil || !token.Allows(scope) || !token.AllowsCrew(roots.ID) {
				http.Error(w, "Use a connection with full access to this own Crew.", 403)
				return
			}
		}
		api.discoverNativeSkills(w, r)
	})
}

// This is discovery for explicit project import, not a native invocation or a
// second skill store. Server/Run callers never inspect the app account's home.
func (api *StreamingAPI) nativeSkillSources(r *http.Request) ([]skills.NativeSource, string) {
	user := GetUserIDFromContext(r.Context())
	workspace := r.URL.Query().Get("workspace_path")
	crew, ok := resolveCrewPath(r.Context(), user, workspace)
	if !ok || crewAccessFor(GetUserFromContext(r.Context()), crew) != crewAccessOwner {
		return nil, "Open a Crew you build to discover native skills."
	}
	provider := r.URL.Query().Get("provider")
	directory := map[string]string{"claude-code": ".claude", "codex-cli": ".codex", "cursor-cli": ".cursor"}[provider]
	if directory == "" {
		return nil, "Choose Claude, Codex or Cursor for native folder discovery. Other programs can still use project imports."
	}
	sources := []skills.NativeSource{}
	if roots, bound, err := crewStorageRoots(workspace); err == nil && bound && roots.Owner == user {
		for _, dir := range []string{path.Join(directory, "skills"), ".agents/skills"} {
			sources = append(sources, skills.NativeSource{Root: roots.Files, Directory: dir, Label: "Project · " + provider})
		}
	}
	if !cliPersonalMac() {
		return sources, "Personal native discovery is available on your own Mac. Import from GitHub or a ZIP here."
	}
	// Reuse native account admission. A shared/lent login's private home is
	// never admitted by localNativeSources; only this person's own accounts are.
	accounts, err := api.localNativeSources(r.Context(), user, workspace)
	if err != nil {
		return sources, "Personal accounts could not be checked. Project sources are shown."
	}
	for _, account := range accounts {
		if account.Provider != provider {
			continue
		}
		config := filepath.Dir(account.Root)
		if provider == "cursor-cli" {
			config = account.Root
		}
		sources = append(sources, skills.NativeSource{Root: config, Directory: "skills", Label: "Personal · " + account.Label})
		if provider == "codex-cli" && strings.HasPrefix(account.ConnectionID, "global:") {
			home, err := os.UserHomeDir()
			if err == nil {
				sources = append(sources, skills.NativeSource{Root: home, Directory: ".agents/skills", Label: "Personal · shared skills"})
			}
		}
		if provider == "claude-code" {
			sources = append(sources, claudeImportPluginSources(config, account.Label)...)
		}
	}
	return sources, "Discovered files are not copied or proof of execution. Import only the skills you choose."
}

// Import can inspect installed plugin skills without enabling that plugin.
// Only registry entries inside the admitted account's plugin folder are read;
// arbitrary installPath, credentials, commands and hooks are never followed.
func claudeImportPluginSources(config, label string) []skills.NativeSource {
	var registry struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
			Scope       string `json:"scope"`
		} `json:"plugins"`
	}
	file, err := os.ReadFile(filepath.Join(config, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(file, &registry) != nil {
		return nil
	}
	out := []skills.NativeSource{}
	for key, entries := range registry.Plugins {
		for _, entry := range entries {
			if entry.Scope != "user" {
				continue
			}
			rel, err := filepath.Rel(config, entry.InstallPath)
			if err != nil || !strings.HasPrefix(filepath.ToSlash(rel), "plugins/") || strings.Contains(filepath.ToSlash(rel), "../") {
				continue
			}
			out = append(out, skills.NativeSource{Root: config, Directory: path.Join(filepath.ToSlash(rel), "skills"), Label: "Plugin · " + key + " · " + label, Prefix: strings.Split(key, "@")[0] + ":"})
		}
	}
	return out
}

func (api *StreamingAPI) discoverNativeSkills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sources, message := api.nativeSkillSources(r)
	rows := skills.DiscoverNativeSkills(sources)
	id := r.URL.Query().Get("id")
	if r.Method == http.MethodPost {
		var body struct {
			ID string `json:"id"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil {
			http.Error(w, "Choose a discovered skill", 400)
			return
		}
		id = body.ID
	}
	if id == "" {
		writeUsersJSON(w, 200, map[string]any{"skills": rows, "message": message})
		return
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		files, err := skills.ReadDiscoveredSkill(row)
		if err != nil {
			http.Error(w, "This skill cannot be copied. Check its files and try again.", 400)
			return
		}
		if r.Method == http.MethodPost {
			name := path.Base(row.Directory)
			if err := skills.InstallSkillFilesIn(getWorkspaceAPIURL(), r.URL.Query().Get("workspace_path"), name, files); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeUsersJSON(w, 200, map[string]any{"success": true, "skill_name": name})
			return
		}
		row.Document = string(files[skills.SkillFileName])
		for name := range files {
			row.Files = append(row.Files, name)
		}
		sort.Strings(row.Files)
		writeUsersJSON(w, 200, row)
		return
	}
	http.Error(w, "This skill is no longer available in your project or own accounts. Refresh discovery.", 404)
}
