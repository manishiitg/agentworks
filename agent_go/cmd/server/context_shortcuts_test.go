package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func shortcutFixture(t *testing.T) *externalToolsFixture {
	t.Helper()
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks,relays,work,code")
	withMemoryUserDirectory(t, `{"users":[
 {"id":"owner","products":["agentworks","relays","work","code"]},
 {"id":"reader","products":["agentworks","code"],"can_create":false},
 {"id":"outsider","products":["agentworks","relays","work","code"]}
 ]}`)
	return f
}

func shortcutRequest(f *externalToolsFixture, user, method, product, query, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := adminRequest(method, "/api/workspace-shortcuts/"+product+query, body, &UserClaims{UserID: user}, nil)
	f.api.handleContextShortcuts(w, mux.SetURLVars(r, map[string]string{"product": product}))
	return w
}

func TestContextShortcutsUserProductIsolationAndRevision(t *testing.T) {
	f := shortcutFixture(t)
	body := `{"version":1,"revision":0,"shortcuts":["files","workshop:chats","report"]}`
	w := shortcutRequest(f, "owner", "PUT", "agentworks", "", body)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w = shortcutRequest(f, "owner", "GET", "agentworks", "", "")
	var value contextShortcuts
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || value.Revision != 1 || strings.Join(value.Shortcuts, ",") != "files,workshop:chats,report" {
		t.Fatalf("restore ordered shortcuts: %s (%v)", w.Body, err)
	}
	for _, scope := range [][2]string{{"outsider", "agentworks"}, {"owner", "relays"}, {"owner", "work"}, {"owner", "code"}} {
		w := shortcutRequest(f, scope[0], "GET", scope[1], "", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), `"files"`) {
			t.Fatalf("preferences crossed scopes %v: %d %s", scope, w.Code, w.Body)
		}
	}
	if w := shortcutRequest(f, "owner", "PUT", "agentworks", "", body); w.Code != 409 {
		t.Fatalf("stale save: %d %s", w.Code, w.Body)
	}
	// Read-only accounts can customize their own presentation without create/write grants.
	if w := shortcutRequest(f, "reader", "PUT", "code", "", `{"version":1,"revision":0,"shortcuts":[]}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"shortcuts":[]`) {
		t.Fatalf("reader's empty layout: %d %s", w.Code, w.Body)
	}
	if w := shortcutRequest(f, "reader", "PUT", "code", "", `{"version":1,"revision":1,"shortcuts":null}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"shortcuts":null`) {
		t.Fatalf("reset defaults: %d %s", w.Code, w.Body)
	}
}

func TestContextShortcutsRejectsForeignFieldsInvalidDataAndUnavailableProducts(t *testing.T) {
	f := shortcutFixture(t)
	for _, body := range []string{
		`{"version":1,"revision":0,"shortcuts":[],"user_id":"outsider"}`,
		`{"version":1,"revision":0,"shortcuts":["../secrets"]}`,
		`{"version":1,"revision":0,"shortcuts":["files","files"]}`,
		`{"version":2,"revision":0,"shortcuts":[]}`,
		`{"version":1,"revision":0,"shortcuts":[]} {}`,
		`{"version":1,"revision":0,"shortcuts":"files"}`,
	} {
		if w := shortcutRequest(f, "owner", "PUT", "agentworks", "", body); w.Code != 400 {
			t.Fatalf("invalid body accepted %s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := shortcutRequest(f, "reader", "GET", "work", "", ""); w.Code != 403 {
		t.Fatalf("account product gate: %d %s", w.Code, w.Body)
	}
	if w := shortcutRequest(f, "owner", "GET", "../../other", "", ""); w.Code != 400 {
		t.Fatalf("unknown product: %d %s", w.Code, w.Body)
	}
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	if w := shortcutRequest(f, "owner", "GET", "code", "", ""); w.Code != 403 {
		t.Fatalf("deployment product gate: %d %s", w.Code, w.Body)
	}
	w := httptest.NewRecorder()
	f.api.handleContextShortcuts(w, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/workspace-shortcuts/agentworks", nil), map[string]string{"product": "agentworks"}))
	if w.Code != 401 {
		t.Fatalf("anonymous request: %d", w.Code)
	}
}

func TestContextShortcutsExplicitProjectMergeInheritance(t *testing.T) {
	f := shortcutFixture(t)
	if w := shortcutRequest(f, "owner", "PUT", "work", "", `{"version":1,"revision":0,"shortcuts":["memory","dashboard"]}`); w.Code != 200 {
		t.Fatalf("legacy save: %d %s", w.Code, w.Body)
	}
	// Product disappearance alone must not mix previously separate preferences.
	t.Setenv("AGENT_PRODUCTS", "agentworks,code")
	w := shortcutRequest(f, "owner", "GET", "code", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "memory") {
		t.Fatalf("implicit inheritance: %d %s", w.Code, w.Body)
	}
	w = shortcutRequest(f, "owner", "GET", "code", "?legacy=work", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"inherited_from":"work"`) || !strings.Contains(w.Body.String(), `"revision":0`) || !strings.Contains(w.Body.String(), "memory") {
		t.Fatalf("explicit inheritance: %d %s", w.Code, w.Body)
	}
	if w = shortcutRequest(f, "owner", "PUT", "code", "", `{"version":1,"revision":0,"shortcuts":["files"]}`); w.Code != 200 {
		t.Fatalf("canonical save: %d %s", w.Code, w.Body)
	}
	w = shortcutRequest(f, "owner", "GET", "code", "?legacy=work", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "memory") || strings.Contains(w.Body.String(), "inherited_from") || !strings.Contains(w.Body.String(), "files") {
		t.Fatalf("canonical should win: %d %s", w.Code, w.Body)
	}
	if w := shortcutRequest(f, "outsider", "GET", "code", "?legacy=work", ""); w.Code != 200 || strings.Contains(w.Body.String(), "memory") {
		t.Fatalf("inherited another user's layout: %d %s", w.Code, w.Body)
	}
	if w := shortcutRequest(f, "owner", "GET", "code", "?legacy=agentworks", ""); w.Code != 400 {
		t.Fatalf("unrelated migration: %d %s", w.Code, w.Body)
	}
}

func TestContextShortcutsCorruptionRecoversAndCanBeReplaced(t *testing.T) {
	f := shortcutFixture(t)
	for _, raw := range []string{`{broken`, `{"version":99,"shortcuts":["files"]}`, strings.Repeat("x", 8193)} {
		f.write(t, contextShortcutsPath("owner", "work"), raw)
		w := shortcutRequest(f, "owner", "GET", "work", "", "")
		var value contextShortcuts
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Version != 1 || value.Revision != 0 || value.Shortcuts != nil {
			t.Fatal("corrupt file blocked recovery", w.Code, w.Body)
		}
		w = shortcutRequest(f, "owner", "PUT", "work", "", `{"version":1,"revision":0,"shortcuts":["files"]}`)
		if w.Code != 200 {
			t.Fatal("reset could not be saved", w.Code, w.Body)
		}
	}
}
