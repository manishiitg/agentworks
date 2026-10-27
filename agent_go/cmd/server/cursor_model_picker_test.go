package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// PLAT-872: a paid Cursor account lists 270 models, each family as up to twenty
// cards. Group by family with variant chips, keep every exact ID, fold older versions.
func TestCursorPickerGroupsFamiliesAndVariants(t *testing.T) {
	models, _ := parseCursorModelList(`Available models
auto - Auto (current, default)
claude-opus-5-1m-max-thinking-fast - Claude Opus 5 1M Max Thinking Fast
claude-opus-5-1m - Claude Opus 5 1M
claude-opus-4-8-1m-extra-high - Claude Opus 4.8 1M Extra High
claude-fable-5-1-1m-low - Claude Fable 5.1 1M Low (NO ZDR)
grok-4.10-medium-fast - Grok 4.10  Medium Fast​​
grok-4.9-medium - Grok 4.9 Medium
`)
	got := cursorModelChoices(models)
	type row struct {
		id, group, family, variant, note string
		older                            bool
	}
	var rows []row
	for _, m := range got {
		rows = append(rows, row{m.ModelID, m.Group, m.Family, m.Variant, m.FamilyNote, m.Older})
	}
	want := []row{
		{"auto", "Cursor", "Auto", "", "", false},
		{"claude-opus-5-1m-max-thinking-fast", "Anthropic", "Claude Opus 5", "Max · Thinking · Fast", "1M context", false},
		{"claude-opus-5-1m", "Anthropic", "Claude Opus 5", "Standard", "1M context", false},
		{"claude-opus-4-8-1m-extra-high", "Anthropic", "Claude Opus 4.8", "Extra High", "1M context", true},
		{"claude-fable-5-1-1m-low", "Anthropic", "Claude Fable 5.1", "Low", "1M context · NO ZDR", false},
		{"grok-4.10-medium-fast", "xAI", "Grok 4.10", "Medium · Fast", "", false},
		{"grok-4.9-medium", "xAI", "Grok 4.9", "Medium", "", true},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("choices =\n%+v\nwant\n%+v", rows, want)
	}
	if !got[0].IsDefault {
		t.Fatal("Auto lost its default mark")
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
	sdk := filepath.Join(bin, "sdk.cjs")
	if err := os.WriteFile(sdk, []byte(`exports.Cursor={models:{list:async()=>{
 if(process.env.CURSOR_AUTH_TOKEN)throw Error('ambient credential');
 const ids=process.env.CURSOR_API_KEY==='test-first-key'?['first-2','first-3']:process.env.CURSOR_API_KEY==='test-second-key'?['second-1']:[];
 return ids.map(id=>({id,displayName:id}));
 }}};`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", sdk)
	t.Setenv("CURSOR_AUTH_TOKEN", "ambient-token-must-not-reach-private-account")
	for _, transport := range []string{"sdk", "acp"} {
		t.Setenv("CURSOR_CLI_TRANSPORT", transport)

		for _, tc := range []struct {
			user, id, want string
			status         int
		}{
			{"alice", a.ID, "first-2,first-3", http.StatusOK},
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
				var ids []string
				for _, m := range response.Models {
					ids = append(ids, m.ModelID)
				}
				if strings.Join(ids, ",") != tc.want {
					t.Fatalf("wrong account inventory: %+v", response.Models)
				}
			}
		}
	}
	// PLAT-872-64: a signed-out SDK keeps only this account's previous catalog,
	// visibly disabled; another caller still cannot read it.
	t.Setenv("CURSOR_CLI_TRANSPORT", "sdk")
	if err := os.WriteFile(sdk, []byte(`exports.Cursor={models:{list:async()=>{throw Error('sign in again')}}};`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, id, want string
		status         int
	}{
		{"alice", a.ID, "first-2", http.StatusOK},
		{"alice", b.ID, "second-1", http.StatusOK},
		{"bob", a.ID, "", http.StatusForbidden},
	} {
		w := env.do(t, env.api.handleGetProviderModels, http.MethodGet, "/models?latest=true&connection_id="+tc.id, tc.user, nil, map[string]string{"provider": "cursor-cli"})
		if w.Code != tc.status {
			t.Fatalf("signed-out catalog status: %d", w.Code)
		}
		if w.Code != http.StatusOK {
			continue
		}
		var response dynamicModelsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if !response.SelectionDisabled || response.Error == "" || response.Source != "account_cache" || len(response.Models) == 0 || response.Models[0].ModelID != tc.want {
			t.Fatalf("wrong display-only catalog: %+v", response)
		}
	}
}
