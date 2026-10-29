package server

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A Crew that clones repositories keeps files far below its root. The file
// tools must reach them by name (glob) and by content (search), paginated,
// and never expose the Crew's private areas.
func TestExternalCrewFilesFindDeepFilesAndHidePrivateAreas(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	write := func(rel, content string) {
		full := filepath.Join(docs, filepath.FromSlash(linkBetaPath), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("repos/customer-website/src/pages/account/forgot-password.razor", `<input data-testid="forgot-email" />`)
	write("notes/plan.md", "plan")
	write("db/db.sqlite", "secret rows")
	write("product.json", `{"id":"beta"}`)
	write("builder/conversation/s.json", "transcript")

	claims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	result := func(out map[string]any) string {
		raw, _ := json.Marshal(out["result"])
		return string(raw)
	}

	code, out := externalCrewRequest(t, env, claims, "list_crew_files", map[string]any{"crew_id": "beta", "glob": "**/*forgot-password*", "depth": 8})
	if code != 200 || !strings.Contains(result(out), "repos/customer-website/src/pages/account/forgot-password.razor") {
		t.Fatalf("find by name = %d %s", code, result(out))
	}
	code, out = externalCrewRequest(t, env, claims, "search_crew_files", map[string]any{"crew_id": "beta", "query": "forgot-email", "depth": 8})
	if code != 200 || !strings.Contains(result(out), "forgot-password.razor") {
		t.Fatalf("search = %d %s", code, result(out))
	}
	code, out = externalCrewRequest(t, env, claims, "list_crew_files", map[string]any{"crew_id": "beta", "depth": 8})
	listing := result(out)
	if code != 200 || !strings.Contains(listing, "notes/plan.md") {
		t.Fatalf("list = %d %s", code, listing)
	}
	for _, private := range []string{"db/db.sqlite", "product.json", "builder/"} {
		if strings.Contains(listing, private) {
			t.Fatalf("private %q listed: %s", private, listing)
		}
	}
	if code, _ := externalCrewRequest(t, env, claims, "search_crew_files", map[string]any{"crew_id": "beta", "query": "secret rows", "path": "db"}); code != 403 {
		t.Fatalf("searching a private folder must be refused, got %d", code)
	}
}

// PLAT-746-200: the Files review's Git reads reach chat and MCP with the Crew
// read rules: a connection bound to another Crew, or limited to other
// folders, is refused; an allowed one gets the same branch comparison as the
// panel (merge-base with the default branch).
func TestExternalCrewGitReadsFollowCrewReadAccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	env := newTriggerLinkEnv(t)
	env.api.agentProfiles = env.svc.registry
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "false")
	registry := filepath.Join(os.Getenv("AGENTWORKS_STATE_ROOT"), "ownership", "projects.json")
	raw, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	// Bind beta's private storage as the server registry does; restore the
	// shared fixture registry afterwards so later tests see it unchanged.
	original := append([]byte(nil), raw...)
	t.Cleanup(func() { _ = os.WriteFile(registry, original, 0600) })
	var owners map[string]any
	if err := json.Unmarshal(raw, &owners); err != nil {
		t.Fatal(err)
	}
	owners["projects"].(map[string]any)["work/beta"].(map[string]any)["project_id"] = "beta"
	raw, _ = json.Marshal(owners)
	if err := os.WriteFile(registry, raw, 0600); err != nil {
		t.Fatal(err)
	}
	private, _ := crewroot.PrivateRoot(docs, "beta")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(docs, filepath.FromSlash(linkBetaPath), "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "first")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "feature")

	args := map[string]any{"crew_id": "beta", "repo": "app"}
	otherCrew := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"alpha"}}}
	if code, _ := externalCrewRequest(t, env, otherCrew, "crew_git_branch_compare", args); code != 404 {
		t.Fatalf("a connection bound to another Crew read its Git: %d", code)
	}
	otherFolder := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}, FileGuard: &wf.FolderGuard{ReadPaths: []string{"notes"}}}}
	if code, _ := externalCrewRequest(t, env, otherFolder, "crew_git_branch_compare", args); code != 404 {
		t.Fatalf("a connection limited to other folders read the repository: %d", code)
	}
	allowed := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"beta"}}}
	code, out := externalCrewRequest(t, env, allowed, "crew_git_branch_compare", args)
	encoded, _ := json.Marshal(out)
	if code != 200 || out["branch"] != "feature" || out["base"] != "main" || !strings.Contains(string(encoded), `"path":"feature.txt"`) || strings.Contains(string(encoded), "a.txt") {
		t.Fatalf("compare: %d %s", code, encoded)
	}
	code, out = externalCrewRequest(t, env, allowed, "crew_git_branch_diff", map[string]any{"crew_id": "beta", "repo": "app", "file": "feature.txt", "base_sha": out["base_sha"], "head_sha": out["head_sha"]})
	if code != 200 || !strings.Contains(fmt.Sprint(out["diff"]), "+feature") {
		t.Fatalf("branch diff: %d %v", code, out)
	}
}
