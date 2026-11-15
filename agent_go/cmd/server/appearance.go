package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
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
}
type appearanceDocument struct {
	Current appearanceTheme   `json:"current"`
	Saved   []appearanceTheme `json:"saved"`
}
type appearanceRequest struct {
	Operation string         `json:"operation"`
	Theme     map[string]any `json:"theme,omitempty"`
	Name      string         `json:"name,omitempty"`
	Replace   bool           `json:"replace,omitempty"`
}

var appearanceMu sync.Mutex
var appearanceHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var appearanceRoles = []string{"background", "foreground", "card", "muted", "muted_foreground", "border", "accent", "sidebar"}

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
	if len(t.CSS) > 20000 {
		return errors.New("Custom CSS must be at most 20,000 bytes.")
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
		"fonts":        []string{"system", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"},
		"code_fonts":   []string{"system-mono", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"},
		"colour_roles": appearanceRoles,
		"hooks":        []string{"[data-app-main]", "[data-theme-surface]", "[data-theme-agent-state]", "[data-theme-agent-selected]", "[data-theme-team-working]", "[data-theme-team-waiting]", "[data-theme-tool-state]", "[data-theme-tools-running]", "[data-theme-turn-live]", "nav[aria-label=\"Workspace navigation\"]", "[data-chat-find-scope]", ".conversation-markdown", "[data-testid=\"transcript-user-bubble\"]", "[data-chat-composer-card]", "[data-product-settings]", "[role=\"menu\"]", "[role=\"dialog\"]"},
		"instructions": "Personal app appearance only, never project files or provider settings. Read first. apply merges theme fields into current; replace=true starts from Default so empty colour maps clear previous overrides (the UI sends full themes this way); save saves/updates a named preset and applies it; delete removes a saved name; reset restores Default and keeps presets. Colours are #RRGGBB under colors.light or colors.dark; accent is the action colour. Prefer colour/font fields; CSS styles the app after normal styles. No external resources: @import, external url(), image-set() or escaped resource values are removed in the browser. Embedded data images are allowed. Use stable hooks, not generated classes. Background-only material CSS: --aw-panel-opacity, --aw-popover-opacity, --aw-sidebar-opacity, --aw-composer-opacity (0 to 1; default 1) and matching --aw-<surface>-backdrop (none or blur). Never fade the whole element or its text. Agent state hooks reuse running/waiting/failed/done/quiet menu marks; quiet is not proof of idle or stopped, failed/done clear when opened. Team working/waiting hooks are booleans from its existing admitted summary; tool-state on web-search cards is historical running/ok/error, not a global live tool indicator. Do not invent blocked/stopped state. Animate only under prefers-reduced-motion: no-preference; never hide warnings or controls. Explicit Artifact/report designs keep their own theme. Never claim visual proof from a saved document; inspect the app or ask the person.",
	}
}
func (api *StreamingAPI) appearance(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
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
	switch request.Operation {
	case "read", "apply", "save", "delete", "reset":
	default:
		http.Error(w, "Choose read, apply, save, delete or reset.", 400)
		return
	}
	appearanceMu.Lock()
	defer appearanceMu.Unlock()
	path := filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "appearance.json"))
	doc := appearanceDocument{Current: defaultAppearanceTheme(), Saved: []appearanceTheme{}}
	raw, exists, err := readFileFromWorkspace(r.Context(), path)
	if err != nil || (exists && json.Unmarshal([]byte(raw), &doc) != nil) {
		http.Error(w, "Could not read your appearance. Try again.", 503)
		return
	}
	switch request.Operation {
	case "apply", "save":
		base := doc.Current
		if request.Replace {
			base = defaultAppearanceTheme()
		}
		next, err := patchAppearanceTheme(base, request.Theme)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if request.Name != "" {
			next.Name = strings.TrimSpace(request.Name)
			if err = validAppearanceTheme(next); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		next.Name = strings.TrimSpace(next.Name)
		doc.Current = next
		if request.Operation == "save" {
			found := false
			for i, t := range doc.Saved {
				if strings.EqualFold(t.Name, next.Name) {
					doc.Saved[i] = next
					found = true
					break
				}
			}
			if !found {
				if len(doc.Saved) >= 20 {
					http.Error(w, "Keep up to 20 saved themes. Remove one first.", 400)
					return
				}
				doc.Saved = append(doc.Saved, next)
			}
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
	_ = json.NewEncoder(w).Encode(map[string]any{"current": doc.Current, "saved": doc.Saved, "guide": appearanceGuide()})
}
func (api *StreamingAPI) handleAppearance(w http.ResponseWriter, r *http.Request) {
	request := appearanceRequest{Operation: "read"}
	if r.Method == http.MethodPut {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 100000))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&request) != nil {
			http.Error(w, "Send the offered appearance fields.", 400)
			return
		}
	}
	api.appearance(w, r, request)
}
func appearanceToolDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("appearance", "Read and change your own app theme, fonts and custom CSS. Read first for current, saved and guide. apply previews persistently, save saves a named preset and applies it, delete removes a saved name, reset restores Default. No project/account selector; changes only the signed-in person's interface, never agent execution or project files.", true, false, map[string]any{
		"operation": map[string]any{"type": "string", "enum": []any{"read", "apply", "save", "delete", "reset"}},
		"theme":     map[string]any{"type": "object", "description": "Partial current theme fields returned by read. Colours are nested under colors.light/colors.dark. CSS max20k bytes. Unspecified fields stay as saved."},
		"replace":   map[string]any{"type": "boolean", "description": "Replace from Default instead of merging; use for a full theme or to clear omitted colour overrides."},
		"name":      externalString("Preset name for save/delete; up to60 characters."),
	})
}
