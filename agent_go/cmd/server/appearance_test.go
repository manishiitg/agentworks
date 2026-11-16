package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	saved := invoke("owner", `{"operation":"save","theme":{"name":"Warm","interface_font":"fira-code","code_font":"ibm-plex-mono","colors":{"light":{"background":"#faf5ed"}},"icons":{"style":"square","stroke_width":1.5,"scale":1.25},"borders":{"composer":{"width_px":2,"radius_px":18,"style":"dashed"}},"css":"[data-chat-composer-card]{border-radius:20px}"}}`)
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
	if json.Unmarshal(read.Body.Bytes(), &doc) != nil || doc.Current.InterfaceFont != "fira-code" || len(doc.Saved) != 1 || doc.Saved[0].CSS == "" {
		t.Fatalf("not persisted: %s", read.Body.String())
	}
	toolRead := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "read"})
	if toolRead.Code != 200 || !strings.Contains(toolRead.Body.String(), "fira-code") {
		t.Fatalf("same tool document unavailable: %d %s", toolRead.Code, toolRead.Body.String())
	}
	// PLAT-746-1040: a tool read keeps one current body and saved labels only;
	// HTTP still has full saved themes, and an explicit own export reads one body.
	var toolDoc struct {
		Current appearanceTheme  `json:"current"`
		Saved   []map[string]any `json:"saved"`
	}
	if json.Unmarshal(toolRead.Body.Bytes(), &toolDoc) != nil || toolDoc.Current.CSS != doc.Current.CSS || len(toolDoc.Saved) != 1 || toolDoc.Saved[0]["name"] != "Warm" || toolDoc.Saved[0]["mode"] != doc.Saved[0].Mode || toolDoc.Saved[0]["css"] != nil || toolDoc.Saved[0]["colors"] != nil {
		t.Fatal("tool read lost current or included saved payloads")
	}
	toolExport := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "export", "name": "Warm"})
	var exportedTheme struct {
		Theme appearanceTheme `json:"theme"`
	}
	if toolExport.Code != 200 || json.Unmarshal(toolExport.Body.Bytes(), &exportedTheme) != nil || exportedTheme.Theme.CSS != doc.Saved[0].CSS || exportedTheme.Theme.Icons == nil || exportedTheme.Theme.Icons.Style != "square" || exportedTheme.Theme.Borders["composer"].WidthPX == nil || *exportedTheme.Theme.Borders["composer"].WidthPX != 2 {
		t.Fatal("explicit tool export lost the saved body")
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
	// Same claims-owned tool writes decorative fields only; typed values cannot
	// carry CSS/selectors, and clearing them must not leave the old account style.
	controls := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "apply", "theme": map[string]any{
		"icons":   map[string]any{"style": "round", "stroke_width": 0.75, "scale": 1.5},
		"borders": map[string]any{"selected_agent": map[string]any{"radius_px": 9999}, "running_agent": map[string]any{"width_px": 2, "style": "dotted"}},
		"colors":  map[string]any{"light": map[string]any{"icon": "#345678", "running_agent_border": "#008877"}},
	}})
	controlRead := invoke("owner", `{"operation":"read"}`)
	if controls.Code != 200 || json.Unmarshal(controlRead.Body.Bytes(), &doc) != nil || doc.Current.Icons == nil || doc.Current.Icons.Scale == nil || *doc.Current.Icons.Scale != 1.5 || doc.Current.Borders["selected_agent"].RadiusPX == nil || *doc.Current.Borders["selected_agent"].RadiusPX != 9999 || doc.Current.Colors["light"]["icon"] != "#345678" {
		t.Fatalf("shared icon/border fields not persisted: %d %s", controls.Code, controlRead.Body.String())
	}
	for _, body := range []string{
		`{"operation":"apply","theme":{"icons":{"style":"filled"}}}`,
		`{"operation":"apply","theme":{"icons":{"scale":0}}}`,
		`{"operation":"apply","theme":{"icons":{"stroke_width":4}}}`,
		`{"operation":"apply","theme":{"icons":{"url":"https://example.com/icons"}}}`,
		`{"operation":"apply","theme":{"borders":{"body":{"width_px":1}}}}`,
		`{"operation":"apply","theme":{"borders":{"input":{"width_px":1.5}}}}`,
		`{"operation":"apply","theme":{"borders":{"input":{"style":"url(https://example.com)"}}}}`,
		`{"operation":"apply","theme":{"borders":{"composer":{"radius_px":-1}}}}`,
		`{"operation":"apply","theme":{"colors":{"light":{"icon":"url(https://example.com)"}}}}`,
	} {
		if got := invoke("owner", body); got.Code != 400 {
			t.Fatalf("unvalidated icon/border fields: %s %d", body, got.Code)
		}
	}
	clearedControls := invoke("owner", `{"operation":"apply","theme":{"icons":null,"borders":null}}`)
	doc.Current.Icons, doc.Current.Borders = nil, nil
	if clearedControls.Code != 200 || json.Unmarshal(clearedControls.Body.Bytes(), &doc) != nil || doc.Current.Icons != nil || doc.Current.Borders != nil || strings.Contains(clearedControls.Body.String(), `"borders":null`) {
		t.Fatal("icon/border fields did not clear canonically")
	}

	// #2768: the same caller-owned screen/tool document carries typed chat/layout;
	// import/export roundtrips them, false/zero survive, and invalid CSS cannot enter numbers.
	layoutSave := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "save", "theme": map[string]any{
		"name": "Compact chat", "chat": map[string]any{"user_style": "bubble", "assistant_style": "plain", "spacing_px": 0, "reading_width_px": 760},
		"layout": map[string]any{"density": "compact", "sidebar_width_px": 280, "right_panel_open": false},
		"colors": map[string]any{"dark": map[string]any{"user_background": "#123456", "assistant_foreground": "#abcdef"}},
	}})
	layoutRead := invoke("owner", `{"operation":"read"}`)
	if layoutSave.Code != 200 || json.Unmarshal(layoutRead.Body.Bytes(), &doc) != nil || doc.Current.Chat == nil || doc.Current.Chat.SpacingPX == nil || *doc.Current.Chat.SpacingPX != 0 || doc.Current.Layout == nil || doc.Current.Layout.RightPanelOpen == nil || *doc.Current.Layout.RightPanelOpen || *doc.Current.Layout.SidebarWidthPX != 280 {
		t.Fatalf("chat/layout not persisted: %s / %s", layoutSave.Body.String(), layoutRead.Body.String())
	}
	layoutExport := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "export", "name": "Compact chat"})
	var roundtrip struct {
		Theme map[string]any `json:"theme"`
	}
	if json.Unmarshal(layoutExport.Body.Bytes(), &roundtrip) != nil {
		t.Fatal("layout export missing")
	}
	layoutImported := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "import", "name": "Copied chat", "theme": roundtrip.Theme})
	if layoutImported.Code != 200 {
		t.Fatalf("layout import refused: %s", layoutImported.Body.String())
	}
	foreignLayout := invoke("other", `{"operation":"read"}`)
	if json.Unmarshal(foreignLayout.Body.Bytes(), &foreign) != nil || foreign.Current.Chat != nil || foreign.Current.Layout != nil {
		t.Fatal("layout crossed people")
	}
	for _, body := range []string{
		`{"operation":"apply","theme":{"chat":{"user_style":"url(example)"}}}`,
		`{"operation":"apply","theme":{"chat":{"spacing_px":-1}}}`,
		`{"operation":"apply","theme":{"chat":{"spacing_px":33}}}`,
		`{"operation":"apply","theme":{"chat":{"reading_width_px":479}}}`,
		`{"operation":"apply","theme":{"chat":{"reading_width_px":1601}}}`,
		`{"operation":"apply","theme":{"chat":{"spacing_px":1.5}}}`,
		`{"operation":"apply","theme":{"layout":{"density":"hidden"}}}`,
		`{"operation":"apply","theme":{"layout":{"sidebar_width_px":361}}}`,
		`{"operation":"apply","theme":{"layout":{"right_panel_open":"false"}}}`,
		`{"operation":"apply","theme":{"layout":{"selector":"body"}}}`,
	} {
		if got := invoke("owner", body); got.Code != 400 {
			t.Fatalf("invalid layout accepted: %s: %d", body, got.Code)
		}
	}
	clearedLayout := invoke("owner", `{"operation":"apply","theme":{"chat":null,"layout":null}}`)
	// Reset the decode destination: omitted optional fields must not look retained.
	doc.Current.Chat, doc.Current.Layout = nil, nil
	if clearedLayout.Code != 200 || json.Unmarshal(clearedLayout.Body.Bytes(), &doc) != nil || doc.Current.Chat != nil || doc.Current.Layout != nil || strings.Contains(clearedLayout.Body.String(), `"chat":null`) {
		t.Fatal("chat/layout did not clear canonically")
	}
	for _, name := range []string{"Compact chat", "Copied chat"} {
		if got := invoke("owner", `{"operation":"delete","name":"`+name+`"}`); got.Code != 200 {
			t.Fatal("layout fixture cleanup failed")
		}
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
	if roundTrip.Code != 200 || json.Unmarshal(roundTrip.Body.Bytes(), &pack) != nil || pack.Theme.CSS != "[data-chat-composer-card]{border-radius:20px}" || pack.Theme.Icons == nil || pack.Theme.Icons.Style != "square" || pack.Theme.Borders["composer"].Style != "dashed" {
		t.Fatal("export/import/copy/rename lost CSS or typed controls")
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

	// PLAT-746-1040: expressive CSS can exceed the former 64KiB cap. Its JSON
	// envelope must also fit when quotes/HTML characters are escaped by clients.
	largeCSS := "/*" + strings.Repeat("\"<&雪", 16000) + "*/"
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

// PLAT-746-1040: large CSS must not depend on one model-generated JSON string.
// Exercise real Crew file reads and both tool adapters, with private test state.
func TestAppearanceLargeCSSFileUsesAdmittedCrewAcrossToolPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	server := serveExternalMCP(t, f.api, crewWriter("owner"))
	client := dialExternalMCP(t, t.Context(), server.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), client)
	panels := &recordingRegistrar{}
	if err := f.api.registerWorkspacePanelChatTools(panels, nil, "owner", "css-test", externalCommandCrewRoot, "work"); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{40 << 10, appearanceCSSMaxBytes} {
		css := "/*" + strings.Repeat("x", size-4) + "*/"
		f.write(t, externalCommandCrewRoot+"/theme.css", css)
		for _, operation := range []string{"save", "apply"} {
			args := map[string]any{"action": "appearance", "operation": operation, "name": "Fixture", "css_file": map[string]any{"crew_id": "team", "path": "theme.css"}}
			reply := f.call(t, "owner", "settings", args)
			if reply.Code != 200 {
				t.Fatalf("MCP file %d %s: %d %s", size, operation, reply.Code, reply.Body.String())
			}
			requireRemoteSuccess(t, callRemoteTool(t, t.Context(), client, "call_tool", map[string]any{"name": "settings", "arguments": args}), "large CSS file over MCP")
			delete(args, "action")
			if _, err := panels.tools["appearance"].exec(ctx, args); err != nil {
				t.Fatalf("panel file %d %s: %v", size, operation, err)
			}
			exported := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "export", "name": "Fixture"})
			var pack struct {
				Theme appearanceTheme `json:"theme"`
			}
			if exported.Code != 200 || json.Unmarshal(exported.Body.Bytes(), &pack) != nil || pack.Theme.CSS != css {
				t.Fatal("file content did not roundtrip exactly")
			}
			// Inline structured JSON also works at both sizes; no inferred13KB cap.
			inline := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": operation, "theme": map[string]any{"name": "Fixture", "css": css}})
			if inline.Code != 200 {
				t.Fatalf("inline %d: %d", size, inline.Code)
			}
		}
	}
	for _, path := range []string{"../../config/users.json", "/etc/passwd", "https://example.com/theme.css"} {
		reply := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "apply", "css_file": map[string]any{"crew_id": "team", "path": path}})
		if reply.Code < 400 {
			t.Fatal("CSS file escaped Crew admission")
		}
	}
	// The fixture starts shared. Make it private before testing cross-person refusal.
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	if denied := f.call(t, "other", "settings", map[string]any{"action": "appearance", "operation": "apply", "css_file": map[string]any{"crew_id": "team", "path": "theme.css"}}); denied.Code < 400 {
		t.Fatal("foreign private Crew CSS accepted")
	}
	f.write(t, externalCommandCrewRoot+"/oversize.css", strings.Repeat("x", appearanceCSSMaxBytes+1))
	for _, args := range []map[string]any{
		{"operation": "apply", "css_file": map[string]any{"crew_id": "team", "path": "oversize.css"}},
		{"operation": "apply", "css_file": map[string]any{"crew_id": "team", "path": "theme.css"}, "theme": map[string]any{"css": "ambiguous"}},
		{"operation": "read", "css_file": map[string]any{"crew_id": "team", "path": "theme.css"}},
		{"operation": "apply", "css_file": map[string]any{"crew_id": "private-missing", "path": "theme.css"}},
	} {
		args["action"] = "appearance"
		if reply := f.call(t, "owner", "settings", args); reply.Code < 400 {
			t.Fatal("invalid CSS file source accepted")
		}
	}
}

// PLAT-746-1040: a normal native write lands in the selected project folder,
// not its .agentworks resource tree. The real Crew read guard still applies.
func TestAppearanceCSSFileReadsNativeSelectedFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	project := filepath.Join(t.TempDir(), "native-project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	selected, err := validateNewLocalCrewRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	const logical = "Crew/native-css"
	if _, err := writeCrewCreationManifests(ctx, "owner", workCrewTestProfile(nil), "work", "", "Native CSS", "", "native-css", logical, nil, crewCreationOptions{LocalRoot: selected}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "themes"), 0700); err != nil {
		t.Fatal(err)
	}
	css := "/*" + strings.Repeat("native-file", 4000) + "*/"
	if err := os.WriteFile(filepath.Join(project, "themes", "theme.css"), []byte(css), 0600); err != nil {
		t.Fatal(err)
	}
	panels := &recordingRegistrar{}
	if err := f.api.registerWorkspacePanelChatTools(panels, nil, "owner", "native-css-session", logical, "work"); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"apply", "save"} {
		args := map[string]any{"action": "appearance", "operation": operation, "name": "Native fixture", "css_file": map[string]any{"crew_id": "native-css", "path": "themes/theme.css"}}
		reply := f.call(t, "owner", "settings", args)
		if reply.Code != 200 {
			t.Fatalf("native file read: %d %s", reply.Code, reply.Body.String())
		}
		delete(args, "action")
		if _, err := panels.tools["appearance"].exec(ctx, args); err != nil {
			t.Fatal(err)
		}
	}
	exported := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "export", "name": "Native fixture"})
	var pack struct {
		Theme appearanceTheme `json:"theme"`
	}
	if json.Unmarshal(exported.Body.Bytes(), &pack) != nil || pack.Theme.CSS != css {
		t.Fatal("native CSS did not roundtrip")
	}
	outside := filepath.Join(t.TempDir(), "outside.css")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "themes", "escape.css")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../outside.css", outside, "themes/escape.css"} {
		reply := f.call(t, "owner", "settings", map[string]any{"action": "appearance", "operation": "apply", "css_file": map[string]any{"crew_id": "native-css", "path": p}})
		if reply.Code < 400 {
			t.Fatalf("CSS file escaped selected folder: %s", p)
		}
	}
}

// PLAT-746-1040: video sources cannot turn personal appearance into local-file/script access.
func TestAppearanceVideoSourceValidation(t *testing.T) {
	for _, raw := range []string{
		`{"background_video":{"url":"file:///private/video.mov","dim":0.7,"playback_rate":0.5}}`,
		`{"background_video":{"url":"javascript:alert(1)","dim":0.7,"playback_rate":0.5}}`,
		`{"background_video":{"url":"https://user:password@example.com/video.mp4","dim":0.7,"playback_rate":0.5}}`,
		`{"background_video":{"url":"https://example.com/video.mp4","dim":0,"playback_rate":0.5}}`,
	} {
		var patch map[string]any
		if err := json.Unmarshal([]byte(raw), &patch); err != nil {
			t.Fatal(err)
		}
		if _, err := patchAppearanceTheme(defaultAppearanceTheme(), patch); err == nil {
			t.Fatal("unsafe/invalid video accepted")
		}
	}
	theme, err := patchAppearanceTheme(defaultAppearanceTheme(), map[string]any{"background_video": map[string]any{"url": "https://example.com/video.mp4", "dim": .7, "playback_rate": .5}})
	if err != nil || theme.BackgroundVideo == nil {
		t.Fatal("valid direct video refused", err)
	}
	cleared, err := patchAppearanceTheme(theme, map[string]any{"background_video": nil})
	if err != nil || cleared.BackgroundVideo != nil {
		t.Fatal("video cannot be removed", err)
	}
}

// GitHub #2851: a save only adds or updates its own theme; saving B never removes A, even when the file read for
// that save is stale or short; only an explicit delete removes a saved theme.
func TestSavingAThemeNeverRemovesOtherSavedThemes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	invoke := func(body string) appearanceDocument {
		r := httptest.NewRequest(http.MethodPut, "/api/appearance", strings.NewReader(body))
		r = r.WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner-2851"}))
		w := httptest.NewRecorder()
		f.api.handleAppearance(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		var doc appearanceDocument
		if json.Unmarshal(w.Body.Bytes(), &doc) != nil {
			t.Fatal("unreadable response", w.Body.String())
		}
		return doc
	}
	names := func(doc appearanceDocument) string {
		out := []string{}
		for _, theme := range doc.Saved {
			out = append(out, theme.Name)
		}
		return strings.Join(out, ",")
	}
	invoke(`{"operation":"save","theme":{"name":"A"}}`)
	if got := names(invoke(`{"operation":"save","theme":{"name":"B"}}`)); got != "A,B" {
		t.Fatalf("saving B changed the saved list: %s", got)
	}
	// A stale or short file read (the lost-themes case): the file now lacks A, and the next save must bring it back.
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath("owner-2851", "preferences"), "appearance.json"))
	short, _ := json.Marshal(appearanceDocument{Current: defaultAppearanceTheme(), Saved: []appearanceTheme{{Name: "B", Mode: "dark"}}})
	if err := writeRawFileToWorkspace(t.Context(), path, string(short)); err != nil {
		t.Fatal(err)
	}
	if got := names(invoke(`{"operation":"save","theme":{"name":"C"}}`)); got != "B,A,C" {
		t.Fatalf("a short read lost saved themes: %s", got)
	}
	// Removal is explicit and stays removed.
	if got := names(invoke(`{"operation":"delete","name":"A"}`)); got != "B,C" {
		t.Fatalf("delete: %s", got)
	}
	if got := names(invoke(`{"operation":"save","theme":{"name":"D"}}`)); got != "B,C,D" {
		t.Fatalf("a deleted theme came back: %s", got)
	}
}
