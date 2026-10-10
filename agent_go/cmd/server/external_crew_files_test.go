package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
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
	if code != 200 || !strings.Contains(result(out), "ForgotPassword.razor") {
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
