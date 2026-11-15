package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// One personal document, shared by Settings and tools; no project/target selector.
type appearanceTheme struct {
	Name          string                       `json:"name"`
	Mode          string                       `json:"mode"`
	InterfaceFont string                       `json:"interface_font"`
	CodeFont      string                       `json:"code_font"`
	InterfaceSize int                          `json:"interface_size"`
	CodeSize      int                          `json:"code_size"`
	Colors        map[string]map[string]string `json:"colors"`
	CSS           string                       `json:"css"`
	// Materials change backgrounds only, never the opacity of text or controls.
	Materials map[string]appearanceMaterial `json:"materials,omitempty"`
	Loader    *appearanceLoader             `json:"loader,omitempty"`
}

// appearanceLoader selects a shared decorative glyph, never the state or lifetime of work.
type appearanceLoader struct {
	Style      string `json:"style,omitempty"`
	DurationMS *int   `json:"duration_ms,omitempty"`
}

// appearanceMaterial is one surface's see-through level (0 to 1) and, for panels over a still background, a blur in px.
type appearanceMaterial struct {
	Opacity *float64 `json:"opacity,omitempty"`
	Blur    *int     `json:"blur,omitempty"`
}
type appearanceDocument struct {
	Current appearanceTheme   `json:"current"`
	Saved   []appearanceTheme `json:"saved"`
}
type appearanceRequest struct {
	Operation string         `json:"operation"`
	Theme     map[string]any `json:"theme,omitempty"`
	Name      string         `json:"name,omitempty"`
	NewName   string         `json:"new_name,omitempty"`
	Replace   bool           `json:"replace,omitempty"`
}

var appearanceMu sync.Mutex
var appearanceHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var appearanceLoaderStyles = []string{"ring", "dots", "pulse", "orbit", "bar"}

// appearanceSurfaces are the app surfaces with a material; the page has an opacity only (it has no backdrop).
var appearanceSurfaces = []string{"page", "panel", "popover", "sidebar", "composer"}
var appearanceRoles = []string{"background", "foreground", "card", "muted", "muted_foreground", "border", "accent", "sidebar", "working", "waiting", "done", "error", "loader"}

func defaultAppearanceTheme() appearanceTheme {
	return appearanceTheme{Name: "Default", Mode: "dark", InterfaceFont: "system", CodeFont: "system-mono", InterfaceSize: 14, CodeSize: 12, Colors: map[string]map[string]string{}, CSS: ""}
}
func validAppearanceTheme(t appearanceTheme) error {
	if len(strings.TrimSpace(t.Name)) == 0 || len(t.Name) > 60 {
		return errors.New("Give the theme a name up to 60 characters.")
	}
	if t.Mode != "light" && t.Mode != "dark" {
		return errors.New("Choose Light or Dark.")
	}
	fonts := map[string]bool{"system": true, "jetbrains-mono": true, "fira-code": true, "ibm-plex-mono": true, "source-code-pro": true}
	if !fonts[t.InterfaceFont] || (t.CodeFont != "system-mono" && (!fonts[t.CodeFont] || t.CodeFont == "system")) {
		return errors.New("Choose an offered interface or code font.")
	}
	if t.InterfaceSize < 12 || t.InterfaceSize > 20 || t.CodeSize < 10 || t.CodeSize > 20 {
		return errors.New("Interface size must be 12–20; code size 10–20.")
	}
	if t.Loader != nil {
		if t.Loader.Style != "" && !slices.Contains(appearanceLoaderStyles, t.Loader.Style) {
			return errors.New("Choose a loading indicator: ring, dots, pulse, orbit or bar.")
		}
		if t.Loader.DurationMS != nil && (*t.Loader.DurationMS < 400 || *t.Loader.DurationMS > 4000) {
			return errors.New("Loading indicator duration must be 400–4000 milliseconds.")
		}
	}
	if len(t.CSS) > appearanceCSSMaxBytes {
		return errors.New("Custom CSS must be at most 65,536 bytes (64 KiB).")
	}
	for surface, material := range t.Materials {
		if !slices.Contains(appearanceSurfaces, surface) {
			return fmt.Errorf("Choose a surface from page, panel, popover, sidebar or composer: %s", surface)
		}
		if material.Opacity != nil && (*material.Opacity < 0 || *material.Opacity > 1) {
			return fmt.Errorf("Opacity for %s must be between 0 and 1.", surface)
		}
		if material.Blur != nil && (*material.Blur < 0 || *material.Blur > 24) {
			return fmt.Errorf("Blur for %s must be between 0 and 24 px.", surface)
		}
		if material.Blur != nil && *material.Blur > 0 {
			if surface == "page" {
				return errors.New("The page has an opacity only; blur belongs to panel, popover, sidebar or composer.")
			}

		}
	}
	if t.Colors == nil {
		return errors.New("Use a colours object, not null.")
	}
	for mode, colors := range t.Colors {
		if colors == nil {
			return errors.New("Each colour variant must be an object, not null.")
		}
		if mode != "light" && mode != "dark" {
			return errors.New("Colours belong to light or dark.")
		}
		for role, color := range colors {
			found := false
			for _, allowed := range appearanceRoles {
				if role == allowed {
					found = true
				}
			}
			if !found || !appearanceHex.MatchString(color) {
				return fmt.Errorf("Use an offered colour role and a six-digit hex colour: %s", role)
			}
		}
	}
	return nil
}
func mergeAppearanceMap(base, patch map[string]any) {
	for key, value := range patch {
		if nested, ok := value.(map[string]any); ok {
			if target, ok := base[key].(map[string]any); ok {
				mergeAppearanceMap(target, nested)
				continue
			}
		}
		base[key] = value
	}
}
func patchAppearanceTheme(current appearanceTheme, patch map[string]any) (appearanceTheme, error) {
	raw, _ := json.Marshal(current)
	var base map[string]any
	_ = json.Unmarshal(raw, &base)
	mergeAppearanceMap(base, patch)
	raw, _ = json.Marshal(base)
	var out appearanceTheme
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil {
		return out, errors.New("Use the offered theme fields.")
	}
	return out, validAppearanceTheme(out)
}
func appearanceGuide() map[string]any {
	return map[string]any{
		"fonts":         []string{"system", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"},
		"code_fonts":    []string{"system-mono", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"},
		"colour_roles":  appearanceRoles,
		"operations":    appearanceOperations,
		"loader":        map[string]any{"styles": appearanceLoaderStyles, "duration_ms": "400-4000; default 1000; lower is faster", "color": "colors.light.loader / colors.dark.loader; defaults to the caller's text or working colour"},
		"status_colors": "colors.light or colors.dark: working, waiting, done, error. Shared agent marks, semantic status controls and toasts use these roles; unavailable/quiet is not done.",
		"material_surfaces": map[string]string{
			"page": "opacity 0-1", "panel": "opacity 0-1, blur 0-24 px", "popover": "opacity 0-1, blur 0-24 px",
			"sidebar": "opacity 0-1, blur 0-24 px", "composer": "opacity 0-1, blur 0-24 px",
		},
		"hooks":         appearanceHookSelectors(),
		"hook_guide":    appearanceHookGuide,
		"css_max_bytes": appearanceCSSMaxBytes,
		"instructions":  "Personal app appearance only, never project files or provider settings. Read first: tools return current in full plus saved name/mode labels; export a saved name for its full body. apply merges theme fields into current; replace=true starts from Default so empty colour maps clear previous overrides (the UI sends full themes this way); save saves/updates a named preset and applies it; apply with name and no theme selects your saved theme. duplicate uses name (omit for current) and new_name; rename uses name and new_name. export returns the selected theme object (name, or current); write that object as JSON, and import it in theme with replace semantics. import and duplicate save without applying. Tool mutations return a short summary and changed field names; read again for current and saved labels; export a saved name for its body. delete removes a saved name; reset restores Default and keeps presets. Colours are #RRGGBB under colors.light or colors.dark; accent is the action colour. Loader: optional loader.style (ring/dots/pulse/orbit/bar), loader.duration_ms (400-4000; lower is faster); loader:null clears customization. colors.light/dark.loader changes shared loading/working glyphs, while working/waiting/done/error set semantic status roles. This slice covers core agent marks, turn working, workspace/modal loading and common controls, not every legacy feature spinner. Glyph motion pauses when hidden and with reduced motion; status labels remain. These fields never decide execution state. Prefer colour/font fields; CSS styles the app after normal styles. No external resources: @import, external url(), image-set() or escaped resource values are removed in the browser. Embedded data images are allowed. Use stable hooks, not generated classes. Only Default Light and Default Dark are built in. Other looks are personal saved colours/materials/CSS, not shipped renderers or executable games. Background: use the stable ambient/main hooks and small static or transform/opacity CSS decorations. Materials: materials.<surface>.opacity (0 to 1; default 1) and materials.<surface>.blur (0 to 24 px, never on the page) make a surface see-through; they change the background only, so text stays solid. Read the appearance guide (read_skill panel-settings, references/appearance) before choosing colours or CSS: keep text readable against its background and the action label readable on the accent, prefer a still background, no blur over moving artwork, keep custom CSS short, never hide or cover controls. Agent state hooks reuse running/waiting/failed/done/quiet menu marks; quiet is not proof of idle or stopped, failed/done clear when opened. Team working/waiting hooks are booleans from its existing admitted summary; tool-state on web-search cards is historical running/ok/error, not a global live tool indicator. Do not invent blocked/stopped state. Animate only under prefers-reduced-motion: no-preference; never hide warnings or controls. Explicit Artifact/report designs keep their own theme. Never claim visual proof from a saved document; inspect the app or ask the person.",
	}
}
func (api *StreamingAPI) appearance(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	api.appearanceResponse(w, r, request, false)
}

func (api *StreamingAPI) appearanceTool(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	api.appearanceResponse(w, r, request, true)
}

// HTTP and all tool transports share the same claims-owned read/validation/write.
// Only the response projection differs: tools need a summary, not every saved CSS.
func (api *StreamingAPI) appearanceResponse(w http.ResponseWriter, r *http.Request, request appearanceRequest, forTool bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "Sign in to change your appearance.", 401)
		return
	}
	user := claims.UserID
	if request.Operation == "" {
		request.Operation = "read"
	}
	if !slices.Contains(appearanceOperations, request.Operation) {
		http.Error(w, "Choose an offered appearance operation.", 400)
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.NewName = strings.TrimSpace(request.NewName)
	appearanceMu.Lock()
	defer appearanceMu.Unlock()
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "appearance.json"))
	doc := appearanceDocument{Current: defaultAppearanceTheme(), Saved: []appearanceTheme{}}
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil || (exists && json.Unmarshal([]byte(raw), &doc) != nil) {
		http.Error(w, "Could not read your appearance. Try again.", 503)
		return
	}
	before := doc.Current
	savedChanged := false
	selected := func(name string) (appearanceTheme, bool) {
		if name == "" {
			return doc.Current, true
		}
		if i := appearanceSavedIndex(doc, name); i >= 0 {
			return doc.Saved[i], true
		}
		return appearanceTheme{}, false
	}
	switch request.Operation {
	case "export":
		theme, ok := selected(request.Name)
		if !ok {
			http.Error(w, "That saved theme is not available. Read your themes and choose one.", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"theme": theme, "summary": "Exported your theme object. Save the theme object as JSON; no settings changed."})
		return
	case "apply", "save", "import":
		if request.Operation == "apply" && request.Name != "" && len(request.Theme) == 0 {
			theme, ok := selected(request.Name)
			if !ok {
				http.Error(w, "That saved theme is not available. Read your themes and choose one.", 404)
				return
			}
			if err := validAppearanceTheme(theme); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			doc.Current = theme
			break
		}
		if request.Operation == "import" {
			name, _ := request.Theme["name"].(string)
			if strings.TrimSpace(name) == "" && request.Name == "" {
				http.Error(w, "Import a theme object with a name, or supply name.", 400)
				return
			}
		}
		base := doc.Current
		if request.Replace || request.Operation == "import" {
			base = defaultAppearanceTheme()
		}
		next, err := patchAppearanceTheme(base, request.Theme)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Name != "" {
			next.Name = request.Name
		}
		next.Name = strings.TrimSpace(next.Name)
		if err = validAppearanceTheme(next); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Operation == "save" || request.Operation == "import" {
			if savedChanged, err = saveAppearanceTheme(&doc, next); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		if request.Operation == "import" {
			request.Name = next.Name
		} else {
			doc.Current = next
		}
	case "duplicate", "rename":
		if request.NewName == "" {
			http.Error(w, "Give the theme a new_name up to 60 characters.", 400)
			return
		}
		theme, ok := selected(request.Name)
		if !ok || (request.Operation == "rename" && request.Name == "") {
			http.Error(w, "Choose one of your saved themes by name.", 404)
			return
		}
		sourceName := theme.Name
		theme.Name = request.NewName
		if err = validAppearanceTheme(theme); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		existing := appearanceSavedIndex(doc, request.NewName)
		source := appearanceSavedIndex(doc, sourceName)
		if existing >= 0 && (request.Operation == "duplicate" || existing != source) {
			http.Error(w, "That theme name is already used. Choose a different new_name.", 400)
			return
		}
		if request.Operation == "rename" {
			savedChanged = doc.Saved[source].Name != theme.Name
			doc.Saved[source] = theme
			if strings.EqualFold(doc.Current.Name, sourceName) {
				doc.Current.Name = theme.Name
			}
		} else if savedChanged, err = saveAppearanceTheme(&doc, theme); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "delete":
		if request.Name == "" {
			http.Error(w, "Name the saved theme to remove.", 400)
			return
		}
		kept := []appearanceTheme{}
		for _, theme := range doc.Saved {
			if !strings.EqualFold(theme.Name, request.Name) {
				kept = append(kept, theme)
			}
		}
		savedChanged = len(kept) != len(doc.Saved)
		doc.Saved = kept
	case "reset":
		doc.Current = defaultAppearanceTheme()
	}
	if request.Operation != "read" {
		data, _ := json.Marshal(doc)
		if err = writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
			http.Error(w, "Could not save your appearance. Try again.", 503)
			return
		}
		livefeed.Default.PublishToUser(livefeed.Appearance, user, "")
	}
	w.Header().Set("Content-Type", "application/json")
	if forTool && request.Operation == "read" {
		// Tools edit current in full; don't spend the agent's context on every saved
		// CSS document. export(name) remains the explicit own-theme read path.
		type savedLabel struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		}
		saved := make([]savedLabel, 0, len(doc.Saved))
		for _, theme := range doc.Saved {
			saved = append(saved, savedLabel{Name: theme.Name, Mode: theme.Mode})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"current": doc.Current, "saved": saved, "guide": appearanceGuide()})
		return
	}
	if forTool && request.Operation != "read" {
		changed := appearanceChangedFields(before, doc.Current)
		if savedChanged {
			changed = append(changed, "saved")
		}
		summary := appearanceMutationSummary(request.Operation, request.Name, request.NewName, doc.Current)
		if len(changed) == 0 {
			summary = "No appearance settings changed. The actual appearance has not been visually checked."
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"summary": summary, "current": map[string]string{"name": doc.Current.Name, "mode": doc.Current.Mode}, "changed_fields": changed})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"current": doc.Current, "saved": doc.Saved, "guide": appearanceGuide()})
}
func (api *StreamingAPI) handleAppearance(w http.ResponseWriter, r *http.Request) {
	request := appearanceRequest{Operation: "read"}
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, appearanceRequestMaxBytes))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil {
			http.Error(w, "Send the offered appearance fields.", 400)
			return
		}
	}
	api.appearance(w, r, request)
}
func appearanceToolDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	operations := make([]any, len(appearanceOperations))
	for i, operation := range appearanceOperations {
		operations[i] = operation
	}
	add("appearance", "Change how the app looks: read and change your own personal theme, colours, fonts, materials and custom CSS. Read first for current, saved and guide; for how to choose well (contrast, speed, CSS hooks) read_skill panel-settings, references/appearance. apply previews persistently, save saves a named preset and applies it, delete removes a saved name, reset restores Default. duplicate/rename use new_name; export returns a theme object for JSON, import stores that theme object without applying. Apply a saved theme by name. Mutation replies summarize changed fields; read returns the full guide/document. No project/account selector; changes only the signed-in person's interface, never agent execution or project files.", true, false, map[string]any{
		"operation": map[string]any{"type": "string", "enum": operations},
		"theme":     map[string]any{"type": "object", "description": "Partial current theme fields returned by read. Colours are nested under colors.light/colors.dark; materials.<page|panel|popover|sidebar|composer> has opacity 0-1 and blur 0-24 (not on page; use only over still artwork). Personal CSS supplies decoration; no shipped scenes. CSS max64 KiB (65,536 UTF-8 bytes). Unspecified fields stay as saved."},
		"replace":   map[string]any{"type": "boolean", "description": "Replace from Default instead of merging; use for a full theme or to clear omitted colour overrides."},
		"name":      externalString("Saved theme name for apply/delete/export/rename/duplicate; save/import may override the imported name. Omit to export/duplicate current. Up to60 characters."),
		"new_name":  externalString("New saved name for rename/duplicate; up to60 characters. Never overwrites a different saved theme."),
	})
}
