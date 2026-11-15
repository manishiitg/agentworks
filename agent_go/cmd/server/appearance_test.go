package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PLAT-746: the screen/tool path has no target identity; real workspace writes
// save only the caller's theme and owner-only notices carry no CSS/content.
func TestAppearanceIsPersonalAndUsesOneValidatedDocument(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	invoke := func(user, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/appearance", strings.NewReader(body))
		if user != "" {
			r = r.WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: user}))
		}
		w := httptest.NewRecorder()
		f.api.handleAppearance(w, r)
		return w
	}
	sub := livefeed.Default.Subscribe()
	defer livefeed.Default.Unsubscribe(sub)
	saved := invoke("owner", `{"operation":"save","theme":{"name":"Warm","interface_font":"fira-code","code_font":"ibm-plex-mono","colors":{"light":{"background":"#faf5ed"}},"css":"[data-chat-composer-card]{border-radius:20px}"}}`)
	if saved.Code != 200 {
		t.Fatalf("allowed save: %d %s", saved.Code, saved.Body.String())
	}
	notices, _ := sub.Drain()
	found := false
	for _, n := range notices {
		if n.Kind == livefeed.Appearance {
			found = true
			if !liveFeedNoticeForUser(n, &UserClaims{UserID: "owner"}) || liveFeedNoticeForUser(n, &UserClaims{UserID: "other"}) {
				t.Fatal("notice crossed people")
			}
		}
	}
	if !found {
		t.Fatal("no appearance wake")
	}
	read := invoke("owner", `{"operation":"read"}`)
	var doc appearanceDocument
	if json.Unmarshal(read.Body.Bytes(), &doc) != nil || doc.Current.InterfaceFont != "fira-code" || len(doc.Saved) != 1 {
		t.Fatalf("not persisted: %s", read.Body.String())
	}
	other := invoke("other", `{"operation":"read"}`)
	var foreign appearanceDocument
	if json.Unmarshal(other.Body.Bytes(), &foreign) != nil || len(foreign.Saved) != 0 || foreign.Current.InterfaceFont != "system" {
		t.Fatalf("other person saw theme: %s", other.Body.String())
	}
	cleared := invoke("owner", `{"operation":"apply","replace":true,"theme":{"name":"Clean","colors":{"light":{}}}}`)
	if json.Unmarshal(cleared.Body.Bytes(), &doc) != nil || len(doc.Current.Colors["light"]) != 0 {
		t.Fatalf("cleared colour returned: %s", cleared.Body.String())
	}
	for _, body := range []string{`{"operation":"apply","theme":{"colors":null}}`, `{"operation":"apply","theme":{"colors":{"light":null}}}`, `{"operation":"apply","user":"owner"}`, `{"operation":"apply","theme":{"unknown":true}}`, `{"operation":"apply","theme":{"colors":{"light":{"background":"url(https://example.com)"}}}}`, `{"operation":"apply","theme":{"code_font":"system"}}`} {
		if got := invoke("other", body); got.Code != 400 {
			t.Fatalf("invalid body accepted: %s %d", body, got.Code)
		}
	}
	if got := invoke("", `{"operation":"read"}`); got.Code != 401 {
		t.Fatal("anonymous appearance allowed")
	}
	reset := invoke("owner", `{"operation":"reset"}`)
	if json.Unmarshal(reset.Body.Bytes(), &doc) != nil || doc.Current.InterfaceFont != "system" || len(doc.Saved) != 1 {
		t.Fatal("reset lost presets or kept current")
	}
}
