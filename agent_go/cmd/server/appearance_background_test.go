package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// #2770: all transports keep shaders/decorations in the caller's own document;
// no markup, handlers, path or other person's identity can enter decoration nodes.
func TestAppearanceShaderAndDecorationsArePersonal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	invoke := func(user, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/appearance", strings.NewReader(body))
		r = r.WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: user}))
		w := httptest.NewRecorder()
		f.api.handleAppearance(w, r)
		return w
	}
	saved := invoke("owner", `{"operation":"save","theme":{"name":"Hologram","background_shader":{"code":"vec3 theme(vec2 uv){return vec3(uv,u_working);}","speed":0.5,"intensity":0.4},"decorations":[{"id":"ship","children":[{"id":"hull"}]}]}}`)
	var doc appearanceDocument
	if saved.Code != 200 || json.Unmarshal(saved.Body.Bytes(), &doc) != nil || doc.Current.BackgroundShader == nil || doc.Current.BackgroundShader.Speed != .5 || len(doc.Current.Decorations) != 1 {
		t.Fatalf("shader save failed: %d %s", saved.Code, saved.Body.String())
	}
	other := invoke("other", `{"operation":"read"}`)
	var foreign appearanceDocument
	if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &foreign) != nil || foreign.Current.BackgroundShader != nil || len(foreign.Current.Decorations) > 0 {
		t.Fatal("personal background crossed people")
	}
	for _, patch := range []map[string]any{
		{"background_shader": map[string]any{"code": strings.Repeat("x", 16385), "speed": 1, "intensity": 1}},
		{"background_shader": map[string]any{"speed": 3}},
		{"background_shader": map[string]any{"intensity": -1}},
		{"background_video": map[string]any{"url": "https://example.com/a.mp4", "dim": .7, "playback_rate": .5}},
		{"decorations": []any{map[string]any{"id": "ship", "onClick": "example"}}},
		{"decorations": []any{map[string]any{"id": "ship", "html": "<button>example</button>"}}},
		{"decorations": []any{map[string]any{"id": "../ship"}}},
		{"decorations": []any{map[string]any{"id": "ship"}, map[string]any{"id": "ship"}}},
	} {
		body, _ := json.Marshal(map[string]any{"operation": "apply", "theme": patch})
		if got := invoke("owner", string(body)); got.Code != 400 {
			t.Fatalf("unvalidated background: %d %s", got.Code, got.Body.String())
		}
	}
	nodes := []appearanceDecoration{}
	for i := 0; i < 65; i++ {
		nodes = append(nodes, appearanceDecoration{ID: fmt.Sprintf("node%d", i)})
	}
	if err := validAppearanceBackground(appearanceTheme{Decorations: nodes}); err == nil {
		t.Fatal("unbounded decoration count")
	}
	nested := appearanceDecoration{ID: "a"}
	for i := 0; i < 6; i++ {
		nested = appearanceDecoration{ID: strings.Repeat("b", i+1), Children: []appearanceDecoration{nested}}
	}
	if err := validAppearanceBackground(appearanceTheme{Decorations: []appearanceDecoration{nested}}); err == nil {
		t.Fatal("unbounded decoration depth")
	}
	read := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "read"})
	if read.Code != 200 || !strings.Contains(read.Body.String(), "background_shader") {
		t.Fatal("tool lost shared fields")
	}
	cleared := invoke("owner", `{"operation":"apply","theme":{"background_shader":null,"decorations":null}}`)
	doc.Current.BackgroundShader = nil
	doc.Current.Decorations = nil
	if cleared.Code != 200 || json.Unmarshal(cleared.Body.Bytes(), &doc) != nil || doc.Current.BackgroundShader != nil || len(doc.Current.Decorations) > 0 {
		t.Fatal("background did not clear")
	}
}
