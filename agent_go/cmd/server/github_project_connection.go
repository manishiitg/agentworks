package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/oauth"
)

type githubProjectRootKey struct{}
type githubConnectionSecretKey struct{}

type githubProjectSelectionError string

func (e githubProjectSelectionError) Error() string { return string(e) }

func githubProjectID(ctx context.Context, root string) (string, error) {
	raw, found, err := readFileFromWorkspace(ctx, root+"/product.json")
	var manifest productProjectManifest
	if err != nil || !found || json.Unmarshal([]byte(raw), &manifest) != nil || manifest.Product != crewProfileID || manifest.ID == "" {
		return "", errors.New("This Crew is unavailable. Reopen it and try again.")
	}
	return manifest.ID, nil
}

func githubProjectRoot(ctx context.Context) string {
	root, _ := ctx.Value(githubProjectRootKey{}).(string)
	return root
}

// Every management path uses the same Builder and exact-project scope check.
func githubProjectContext(ctx context.Context, person, workspace string, write bool) (context.Context, error) {
	r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	scope, err := personalConnectionItemScope(r, workspace, write, "GitHub", "GitHub")
	if err != nil || scope.UserID != person || !isProjectWorkspacePath(scope.Workspace) {
		return ctx, errors.New("Open a Crew you build to choose its GitHub account.")
	}
	// Runtime resolves the main Crew owner's personal registry, not a joined
	// builder's. Never persist an ID that runtime would look up as someone else.
	if !crewProjectOwnedByCallerStrict(person, scope.Workspace) {
		return ctx, errors.New("Only this Crew's owner can choose its GitHub account.")
	}
	return context.WithValue(ctx, githubProjectRootKey{}, scope.Workspace), nil
}

func githubConnectionPath(person, id string) string {
	// IDs are opaque generated identifiers, never account labels or paths.
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return ""
	}
	return filepath.Join(mcpagentTokensRoot(), platformMCPTokenUserID, "github-connections", sanitizeUserIDForPath(person), id+".json")
}

func readGitHubConnection(person, id string) *githubAccountRecord {
	path := githubConnectionPath(person, id)
	if path == "" {
		return nil
	}
	data, err := oauth.ReadTokenFile(path)
	if err != nil {
		return nil
	}
	var rec githubAccountRecord
	if json.Unmarshal(data, &rec) != nil || rec.ID != id || rec.SecretName == "" {
		return nil
	}
	return &rec
}

func gitHubConnections(person string) []githubAccountRecord {
	entries, _ := os.ReadDir(filepath.Join(mcpagentTokensRoot(), platformMCPTokenUserID, "github-connections", sanitizeUserIDForPath(person)))
	out := []githubAccountRecord{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if rec := readGitHubConnection(person, strings.TrimSuffix(entry.Name(), ".json")); rec != nil {
			out = append(out, *rec)
		}
	}
	return out
}

func projectGitHubConnection(ctx context.Context, owner, workspace string) (*githubAccountRecord, error) {
	raw, found, err := readProjectRuntimeManifest(ctx, crewProfileID, workspace)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	var manifest struct {
		Capabilities struct {
			GitHubConnectionID string `json:"github_connection_id"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return nil, err
	}
	id := manifest.Capabilities.GitHubConnectionID
	if id == "" {
		return nil, nil
	}
	rec := readGitHubConnection(owner, id)
	if rec == nil {
		return nil, githubProjectSelectionError("This project's GitHub connection is unavailable. Choose or reconnect its account in GitHub settings.")
	}
	if githubTokenValue(rec) == "" {
		return nil, githubProjectSelectionError("This project's GitHub credentials are unavailable. Reconnect the selected account in GitHub settings.")
	}
	return rec, nil
}

func selectProjectGitHubConnection(ctx context.Context, owner, id string) error {
	root := githubProjectRoot(ctx)
	if root == "" {
		return errors.New("Choose a Crew for this GitHub connection.")
	}
	if !crewProjectOwnedByCallerStrict(owner, root) {
		return errors.New("Only this Crew's owner can choose its GitHub account.")
	}
	if id != "" && readGitHubConnection(owner, id) == nil {
		return errors.New("Choose one of your saved GitHub connections.")
	}
	raw, path, err := ensureProjectRuntimeManifest(ctx, crewProfileID, root)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	caps, _ := manifest["capabilities"].(map[string]any)
	if caps == nil {
		caps = map[string]any{}
		manifest["capabilities"] = caps
	}
	// Effective selections can have been saved by the general Secrets panel.
	// Keep connection credentials controlled by this one account selection so
	// disconnect cannot leave a previously materialized GitHub secret selected.
	githubNames := map[string]bool{}
	for _, rec := range gitHubConnections(owner) {
		githubNames[rec.SecretName] = true
	}
	if selected, ok := caps["selected_global_secret_names"].([]any); ok {
		filtered := []any{}
		for _, value := range selected {
			name, isName := value.(string)
			if !isName || (!githubNames[name] && vaultSecretShortName(name) != "GITHUB_TOKEN") {
				filtered = append(filtered, value)
			}
		}
		caps["selected_global_secret_names"] = filtered
	}
	if id == "" {
		delete(caps, "github_connection_id")
	} else {
		caps["github_connection_id"] = id
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, path, string(data)+"\n"); err != nil {
		return err
	}
	publishCrewPanelForPerson(owner)
	return nil
}
