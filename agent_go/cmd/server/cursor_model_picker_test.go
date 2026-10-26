package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// PLAT-872: the screenshot showed duplicate effort/speed cards and older
// generations. Use numeric versions, retain distinct families and exact IDs.
func TestCursorPickerLatestFamiliesRetainRunnableSelectors(t *testing.T) {
	models, _ := parseCursorModelList(`Available models
composer-2.5 - Composer 2.5 (default)
grok-4.9-medium - Grok 4.9 Medium
grok-4.10-low - Grok 4.10 Low
grok-4.10-medium-fast - Grok 4.10  Medium Fast​​
grok-4.10-medium - Grok 4.10 Medium
grok-4.10-high - Grok 4.10 High
claude-sonnet-5 - Claude Sonnet 5
claude-opus-5 - Claude Opus 5
auto - Auto
`)
	got := latestCursorModelFamilies(models)
	var ids, names []string
	for _, m := range got {
		ids = append(ids, m.ModelID)
		names = append(names, m.ModelName)
	}
	want := []string{"composer-2.5", "grok-4.10-medium", "claude-sonnet-5", "claude-opus-5", "auto"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("picker selectors = %v; want %v", ids, want)
	}
	if names[1] != "Grok 4.10" || !got[0].IsDefault {
		t.Fatalf("labels/default lost: %+v", got)
	}
	if len(latestCursorModelFamilies(nil)) != 0 {
		t.Fatal("failed discovery must not invent models")
	}
}

// PLAT-387/872: discover the selected login only; another person's catalog
// must be refused before running the CLI. The fake is only the CLI boundary.
func TestCursorPickerUsesAuthorizedAccountInventory(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	a := env.addAccount(t, "alice", map[string]interface{}{"provider": "cursor-cli", "display_name": "First", "credential": "test-first-key"})
	b := env.addAccount(t, "alice", map[string]interface{}{"provider": "cursor-cli", "display_name": "Second", "credential": "test-second-key"})
	bin := t.TempDir()
	script := `#!/bin/sh
if [ -n "$CURSOR_AUTH_TOKEN" ]; then exit 10; fi
case "$CURSOR_API_KEY" in
 test-first-key) printf 'first-2 - First 2\nfirst-3 - First 3\n';;
 test-second-key) printf 'second-1 - Second 1\n';;
 *) exit 11;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "cursor-agent"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CURSOR_AUTH_TOKEN", "ambient-token-must-not-reach-private-account")
	for _, tc := range []struct {
		user, id, want string
		status         int
	}{
		{"alice", a.ID, "first-3", http.StatusOK},
		{"alice", b.ID, "second-1", http.StatusOK},
		{"bob", a.ID, "", http.StatusForbidden},
	} {
		w := env.do(t, env.api.handleGetProviderModels, http.MethodGet, "/models?latest=true&connection_id="+tc.id, tc.user, nil, map[string]string{"provider": "cursor-cli"})
		if w.Code != tc.status {
			t.Fatalf("%s account %s: HTTP %d: %s", tc.user, tc.id, w.Code, w.Body.String())
		}
		if tc.status == http.StatusOK {
			var response dynamicModelsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Models) != 1 || response.Models[0].ModelID != tc.want {
				t.Fatalf("wrong account inventory: %+v", response.Models)
			}
		}
	}
}
