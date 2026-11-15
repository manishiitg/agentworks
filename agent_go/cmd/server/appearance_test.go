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
	toolRead := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "read"})
	if toolRead.Code != 200 || !strings.Contains(toolRead.Body.String(), "fira-code") {
		t.Fatalf("same tool document unavailable: %d %s", toolRead.Code, toolRead.Body.String())
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
	// Personal materials remain validated after removing the scene field/catalog.
	still := invoke("owner", `{"operation":"apply","replace":true,"theme":{"name":"Frosted","materials":{"sidebar":{"opacity":0.8,"blur":12},"page":{"opacity":1}}}}`)
	if json.Unmarshal(still.Body.Bytes(), &doc) != nil || still.Code != 200 || doc.Current.Materials["sidebar"].Blur == nil || *doc.Current.Materials["sidebar"].Blur != 12 {
		t.Fatalf("material theme refused: %d %s", still.Code, still.Body.String())
	}
	for _, body := range []string{
		`{"operation":"apply","theme":{"scene":"galaxy"}}`,
		`{"operation":"apply","theme":{"materials":{"sidebar":{"opacity":1.5}}}}`,
		`{"operation":"apply","theme":{"materials":{"hero":{"opacity":0.5}}}}`,
		`{"operation":"apply","theme":{"materials":{"page":{"blur":4}}}}`,
		`{"operation":"apply","theme":{"materials":{"panel":{"blur":40}}}}`,
	} {
		if got := invoke("owner", body); got.Code != 400 {
			t.Fatalf("removed scene or invalid material accepted: %s %d", body, got.Code)
		}
	}
	// Optional materials must not serialize null: the browser refuses null and
	// would otherwise discard a saved personal look during the scene conversion.
	nullMaterial := invoke("owner", `{"operation":"apply","theme":{"materials":null}}`)
	if nullMaterial.Code != 200 || strings.Contains(nullMaterial.Body.String(), `"materials":null`) {
		t.Fatalf("noncanonical optional materials: %s", nullMaterial.Body.String())
	}

	// PLAT-746-1040: all transports store the same validated decorative loader;
	// arbitrary CSS/paths cannot enter its typed fields or status colours.
	customLoader := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "apply", "theme": map[string]any{
		"loader": map[string]any{"style": "dots", "duration_ms": 800},
		"colors": map[string]any{"dark": map[string]any{"working": "#123456", "waiting": "#654321", "done": "#008800", "error": "#aa0000", "loader": "#abcdef"}},
	}})
	if customLoader.Code != 200 || !strings.Contains(customLoader.Body.String(), `"loader"`) {
		t.Fatalf("common tool loader or summary: %d %s", customLoader.Code, customLoader.Body.String())
	}
	loaderRead := invoke("owner", `{"operation":"read"}`)
	if json.Unmarshal(loaderRead.Body.Bytes(), &doc) != nil || doc.Current.Loader == nil || doc.Current.Loader.Style != "dots" || doc.Current.Loader.DurationMS == nil || *doc.Current.Loader.DurationMS != 800 || doc.Current.Colors["dark"]["error"] != "#aa0000" {
		t.Fatalf("typed loader did not persist: %s", loaderRead.Body.String())
	}
	for _, body := range []string{
		`{"operation":"apply","theme":{"loader":{"style":"url(https://example.com)"}}}`,
		`{"operation":"apply","theme":{"loader":{"duration_ms":0}}}`,
		`{"operation":"apply","theme":{"loader":{"duration_ms":4001}}}`,
		`{"operation":"apply","theme":{"loader":{"duration_ms":800.5}}}`,
		`{"operation":"apply","theme":{"loader":{"script":"example"}}}`,
		`{"operation":"apply","theme":{"colors":{"light":{"working":"red"}}}}`,
	} {
		if got := invoke("owner", body); got.Code != 400 {
			t.Fatalf("unvalidated loader: %s %d", body, got.Code)
		}
	}
	clearedLoader := invoke("owner", `{"operation":"apply","theme":{"loader":null}}`)
	doc.Current.Loader = nil
	if clearedLoader.Code != 200 || json.Unmarshal(clearedLoader.Body.Bytes(), &doc) != nil || doc.Current.Loader != nil {
		t.Fatal("loader did not clear")
	}
	if got := invoke("", `{"operation":"read"}`); got.Code != 401 {
		t.Fatal("anonymous appearance allowed")
	}
	reset := invoke("owner", `{"operation":"reset"}`)
	if json.Unmarshal(reset.Body.Bytes(), &doc) != nil || doc.Current.InterfaceFont != "system" || len(doc.Saved) != 1 {
		t.Fatal("reset lost presets or kept current")
	}
	// Agentic saved-theme operations keep the same own-person document. Export is
	// read-only; imports/copies do not unexpectedly replace the applied look.
	sub.Drain()
	exported := invoke("owner", `{"operation":"export","name":"Warm"}`)
	var pack struct {
		Theme appearanceTheme `json:"theme"`
	}
	if exported.Code != 200 || json.Unmarshal(exported.Body.Bytes(), &pack) != nil || pack.Theme.InterfaceFont != "fira-code" {
		t.Fatalf("own export: %d %s", exported.Code, exported.Body.String())
	}
	if notices, _ := sub.Drain(); len(notices) != 0 {
		t.Fatal("export published a mutation")
	}
	if got := invoke("other", `{"operation":"export","name":"Warm"}`); got.Code != 404 {
		t.Fatal("foreign person's named theme exported")
	}
	applied := invoke("owner", `{"operation":"apply","name":"Warm"}`)
	if applied.Code != 200 || json.Unmarshal(applied.Body.Bytes(), &doc) != nil || doc.Current.InterfaceFont != "fira-code" {
		t.Fatal("apply by saved name did not select the saved content")
	}
	var theme map[string]any
	rawTheme, _ := json.Marshal(pack.Theme)
	if json.Unmarshal(rawTheme, &theme) != nil {
		t.Fatal("export theme object")
	}
	imported := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "import", "name": "Imported", "theme": theme})
	var reply struct {
		Summary string         `json:"summary"`
		Current map[string]any `json:"current"`
		Changed []string       `json:"changed_fields"`
		Saved   any            `json:"saved"`
	}
	if imported.Code != 200 || json.Unmarshal(imported.Body.Bytes(), &reply) != nil || reply.Summary == "" || reply.Current["name"] != "Warm" || reply.Current["css"] != nil || reply.Saved != nil {
		t.Fatalf("short import reply or current selection: %d %s", imported.Code, imported.Body.String())
	}
	copied := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "duplicate", "name": "Imported", "new_name": "Copy"})
	if copied.Code != 200 {
		t.Fatalf("duplicate: %s", copied.Body.String())
	}
	renamed := invoke("owner", `{"operation":"rename","name":"Copy","new_name":"Renamed"}`)
	if renamed.Code != 200 {
		t.Fatalf("rename: %s", renamed.Body.String())
	}
	roundTrip := invoke("owner", `{"operation":"export","name":"Renamed"}`)
	if roundTrip.Code != 200 || json.Unmarshal(roundTrip.Body.Bytes(), &pack) != nil || pack.Theme.CSS != "[data-chat-composer-card]{border-radius:20px}" {
		t.Fatal("export/import/copy/rename lost CSS")
	}
	if got := invoke("owner", `{"operation":"rename","name":"Renamed","new_name":"Warm"}`); got.Code != 400 {
		t.Fatal("rename overwrote another saved name")
	}
	if got := invoke("other", `{"operation":"duplicate","name":"Warm","new_name":"Stolen"}`); got.Code != 404 {
		t.Fatal("duplicate selected another person's saved theme")
	}
	if got := invoke("owner", `{"operation":"import","theme":{"name":"Bad","unexpected":true}}`); got.Code != 400 {
		t.Fatal("import skipped field validation")
	}
	noChange := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "apply", "name": "Warm"})
	if noChange.Code != 200 || json.Unmarshal(noChange.Body.Bytes(), &reply) != nil || len(reply.Changed) != 0 {
		t.Fatalf("no-op claimed changes: %s", noChange.Body.String())
	}

	// PLAT-746-1040: expressive CSS can exceed the former 20KB cap. Its JSON
	// envelope must also fit when quotes/HTML characters are escaped by clients.
	largeCSS := "/*" + strings.Repeat("\"<&雪", 8000) + "*/"
	largeBody, _ := json.Marshal(map[string]any{"operation": "apply", "theme": map[string]any{"css": largeCSS}})
	largeReply := invoke("owner", string(largeBody))
	var largeDoc appearanceDocument
	if len(largeBody) <= 100000 || largeReply.Code != 200 || json.Unmarshal(largeReply.Body.Bytes(), &largeDoc) != nil || largeDoc.Current.CSS != largeCSS {
		t.Fatalf("valid escaped CSS refused: body=%d code=%d", len(largeBody), largeReply.Code)
	}
	tooLarge, _ := json.Marshal(map[string]any{"operation": "apply", "theme": map[string]any{"css": strings.Repeat("雪", appearanceCSSMaxBytes/3+1)}})
	if got := invoke("owner", string(tooLarge)); got.Code != 400 {
		t.Fatal("CSS UTF-8 byte cap was not enforced")
	}

}
