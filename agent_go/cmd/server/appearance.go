package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// One personal document, shared by Settings and tools; no project/target selector.
type appearanceTheme struct {
	Name             string                       `json:"name"`
	Mode             string                       `json:"mode"`
	InterfaceFont    string                       `json:"interface_font"`
	CodeFont         string                       `json:"code_font"`
	InterfaceSize    int                          `json:"interface_size"`
	CodeSize         int                          `json:"code_size"`
	Colors           map[string]map[string]string `json:"colors"`
	CSS              string                       `json:"css"`
	BackgroundScript *appearanceScript            `json:"background_script,omitempty"`
	BackgroundShader *appearanceShader            `json:"background_shader,omitempty"`
	Decorations      []appearanceDecoration       `json:"decorations,omitempty"`
	BackgroundVideo  *appearanceVideo             `json:"background_video,omitempty"`
	BackgroundImage  *appearanceImage             `json:"background_image,omitempty"`
	// Materials change backgrounds only, never the opacity of text or controls.
	Materials map[string]appearanceMaterial `json:"materials,omitempty"`
	Loader    *appearanceLoader             `json:"loader,omitempty"`
	Icons     *appearanceIcons              `json:"icons,omitempty"`
	Borders   map[string]appearanceBorder   `json:"borders,omitempty"`
	Chat      *appearanceChat               `json:"chat,omitempty"`
	Layout    *appearanceLayout             `json:"layout,omitempty"`
}

// Personal passive media only; no theme script, file URL or background fetch proxy.
type appearanceVideo struct {
	URL          string  `json:"url,omitempty"`
	AssetID      string  `json:"asset_id,omitempty"`
	Dim          float64 `json:"dim"`
	PlaybackRate float64 `json:"playback_rate"`
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
type appearanceCSSFile struct {
	CrewID  string `json:"crew_id"`
	Path    string `json:"path"`
	AgentID string `json:"agent_id,omitempty"`
}
type appearanceRequest struct {
	ImagePath   string             `json:"image_path,omitempty"`
	ImageID     string             `json:"image_id,omitempty"`
	ImageFile   *appearanceCSSFile `json:"image_file,omitempty"`
	BlurPX      int                `json:"blur_px,omitempty"`
	WallpaperID string             `json:"wallpaper_id,omitempty"`
	CSSFile     *appearanceCSSFile `json:"css_file,omitempty"`
	Operation   string             `json:"operation"`
	Theme       map[string]any     `json:"theme,omitempty"`
	Name        string             `json:"name,omitempty"`
	NewName     string             `json:"new_name,omitempty"`
	Replace     bool               `json:"replace,omitempty"`
}

var appearanceInterfaceFonts = []string{"system", "new-york", "helvetica-neue", "avenir-next", "georgia", "menlo", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"}
var appearanceCodeFonts = []string{"system-mono", "jetbrains-mono", "fira-code", "ibm-plex-mono", "source-code-pro"}

var appearanceMu sync.Mutex

// appearanceKnownSaved is each person's saved-theme list as this server last wrote it (guarded by appearanceMu). A save
// only adds or updates its own theme; if the file read for a save comes back without a theme this server saved
// earlier (a stale or short read; GitHub #2851 lost seven saved themes), the missing ones are put back before writing.
// Only an explicit delete or rename removes or renames a saved theme.
var appearanceKnownSaved = map[string][]appearanceTheme{}

// appearanceRestoreMissing returns saved plus any theme of known that saved lacks (by name), and how many came back.
func appearanceRestoreMissing(saved, known []appearanceTheme) ([]appearanceTheme, int) {
	restored := 0
	for _, theme := range known {
		if len(saved) >= 20 {
			break
		}
		if appearanceSavedIndex(appearanceDocument{Saved: saved}, theme.Name) < 0 {
			saved = append(saved, theme)
			restored++
		}
	}
	return saved, restored
}

var appearanceHex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
var appearanceLoaderStyles = []string{"ring", "dots", "pulse", "orbit", "bar"}

// appearanceSurfaces are the app surfaces with a material; the page has an opacity only (it has no backdrop).
var appearanceSurfaces = []string{"page", "panel", "popover", "sidebar", "composer"}
var appearanceRoles = []string{"background", "foreground", "card", "muted", "muted_foreground", "border", "accent", "sidebar", "working", "waiting", "done", "error", "loader", "icon", "panel_border", "card_border", "composer_border", "input_border", "selected_agent_border", "running_agent_border", "user_background", "user_foreground", "assistant_background", "assistant_foreground"}

func defaultAppearanceTheme() appearanceTheme {
	return appearanceTheme{Name: "Default", Mode: "dark", InterfaceFont: "system", CodeFont: "system-mono", InterfaceSize: 14, CodeSize: 12, Colors: map[string]map[string]string{}, CSS: ""}
}
func validAppearanceTheme(t appearanceTheme) error {
	if err := validAppearanceImage(t); err != nil {
		return err
	}
	if err := validAppearanceLayout(t); err != nil {
		return err
	}
	if err := validAppearanceBackground(t); err != nil {
		return err
	}
	if len(strings.TrimSpace(t.Name)) == 0 || len(t.Name) > 60 {
		return errors.New("Give the theme a name up to 60 characters.")
	}
	if t.Mode != "light" && t.Mode != "dark" {
		return errors.New("Choose Light or Dark.")
	}
	if !slices.Contains(appearanceInterfaceFonts, t.InterfaceFont) || !slices.Contains(appearanceCodeFonts, t.CodeFont) {
		return errors.New("Choose an offered interface or code font.")
	}
	if t.InterfaceSize < 12 || t.InterfaceSize > 20 || t.CodeSize < 10 || t.CodeSize > 20 {
		return errors.New("Interface size must be 12–20; code size 10–20.")
	}
	if v := t.BackgroundVideo; v != nil {
		u, err := url.Parse(v.URL)
		if v.AssetID != "" && (v.URL != "" || !validAppearanceAssetID(v.AssetID)) {
			return errors.New("Use one valid personal asset_id or HTTPS URL.")
		}
		if v.AssetID == "" && (err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(v.URL) > 2048 || strings.ContainsAny(v.URL, "\r\n")) {
			return errors.New("Use a direct HTTPS video URL without a login or a local file path.")
		}
		if v.Dim < .35 || v.Dim > .95 || v.PlaybackRate < .25 || v.PlaybackRate > 1 {
			return errors.New("Video dim must be 0.35–0.95 and playback_rate 0.25–1.")
		}
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
		return errors.New("Custom CSS must be at most 131,072 bytes (128 KiB).")
	}
	if err := validAppearanceControls(t); err != nil {
		return err
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
		"chat":              map[string]any{"styles": []string{"bubble", "plain", "card"}, "fields": "user_style and assistant_style independently; spacing_px 0–32; reading_width_px 480–1600 responsive maximum, chat:null restores normal full width", "colors": "colors.light/dark.user_background, user_foreground, assistant_background, assistant_foreground", "coverage": "Core live and saved transcript message bodies. Receipts, actions, tool controls and authored documents retain their behavior."},
		"layout":            map[string]any{"density": []string{"compact", "normal", "roomy"}, "sidebar_width_px": "220–360; desktop default, still manually resizable", "right_panel_open": "true/false default for the work workspace, still manually toggleable; saved workspace return and explicit panel links take precedence", "text_size": "Use existing interface_size 12–20, not another size field", "clear": "layout:null restores app defaults"},
		"background_video":  map[string]any{"url": "Direct HTTPS MP4/WebM, or omit URL and use an own imported asset_id", "dim": "0.35–0.95", "playback_rate": "0.25–1; muted looping playback, hidden pause, reduced-motion still frame", "clear": "background_video:null", "mac_import": "mac_wallpapers → import_mac_wallpaper(wallpaper_id); own local OS account only, returns personal asset_id without applying"},
		"fonts":             appearanceInterfaceFonts,
		"code_fonts":        appearanceCodeFonts,
		"colour_roles":      appearanceRoles,
		"operations":        appearanceOperations,
		"loader":            map[string]any{"styles": appearanceLoaderStyles, "duration_ms": "400-4000; default 1000; lower is faster", "color": "colors.light.loader / colors.dark.loader; defaults to the caller's text or working colour"},
		"icons":             map[string]any{"styles": []string{"round", "square"}, "stroke_width": "0.75-3", "scale": "0.75-1.5; 1 keeps each glyph's existing size", "color": "colors.light.icon / colors.dark.icon", "coverage": "Existing Lucide outline glyphs only, not brand logos, authored documents or the separate loading indicator. This is stroke styling, not another icon library."},
		"borders":           map[string]any{"surfaces": appearanceBorderSurfaces, "width_px": "0-4", "radius_px": "0-9999; 0 sharp, 9999 pill", "styles": []string{"solid", "dashed", "dotted"}, "colors": "colors.light/dark.<surface>_border; card falls back to panel. Running overrides selected on a working selected agent. Only existing admitted state hooks, no new status queries."},
		"status_colors":     "colors.light or colors.dark: working, waiting, done, error. Shared agent marks, semantic status controls and toasts use these roles; unavailable/quiet is not done.",
		"background_script": map[string]any{"code": "1–65536 UTF-8 bytes JavaScript defining function render(frame). Runs only in an opaque sandbox Worker with OffscreenCanvas, not in the app. No DOM, account, network or imported libraries.", "fps": "1–30", "inputs": "frame: canvas, width, height, time and delta (seconds), colors (current palette hex only), working (boolean), reducedMotion (boolean). Runtime owns scheduling; do not create timers.", "runtime": "1280x720 max, <=1x DPR; hidden terminates Worker; reduced motion one still frame then terminates. Slow frames throttle then stop; hung Worker is terminated after 1500ms. CSS fallback. Clear with background_script:null or Disable script background in Appearance. Mutually exclusive with image/video/shader."},
		"background_shader": map[string]any{"code": "1–16384 bytes GLSL defining vec3 theme(vec2 uv). Uniforms u_time (seconds), u_resolution (pixels), u_accent (RGB 0–1), u_working (active admitted chat 0/1), u_intensity (0–1). No theme JS/network/textures.", "speed": "0–2", "intensity": "0–1", "runtime": "at most 1280x720 / 1x DPR / 30 fps; hidden pause, reduced-motion still; sustained slow frames throttle then CSS fallback. Cannot combine with background_video. Clear with null."},
		"decorations":       "Optional [{id,children:[…]}] tree. At most 64 inert divs, depth 6, unique IDs matching [a-z][a-z0-9_-]{0,31}. Style with [data-theme-decoration=ID] using existing CSS/3D. No HTML, React, scripts, events or controls. Layered behind UI.",
		"background_image":  map[string]any{"asset_id": "Own uploaded image ID", "fit": "cover/contain/tile", "position_x": "0–100, usually50", "position_y": "0–100, usually50", "brightness": "0.25–1.5; usually0.6 for dark or1 for light", "import": "import_image image_file={crew_id,path}, optional blur_px0–20 baked into copy. PNG/JPEG/WebP <=5MB, <=16M pixels; resized only above3840x2160. Original untouched. On your own Mac, import_image can instead use image_path with one absolute or ~/ path, behind the existing OS-owner gate. No picker UI or host scan. Clear other background fields with null."},
		"material_surfaces": map[string]string{
			"page": "opacity 0-1", "panel": "opacity 0-1, blur 0-24 px", "popover": "opacity 0-1, blur 0-24 px",
			"sidebar": "opacity 0-1, blur 0-24 px", "composer": "opacity 0-1, blur 0-24 px",
		},
		"hooks":         appearanceHookSelectors(),
		"hook_guide":    appearanceHookGuide,
		"css_max_bytes": appearanceCSSMaxBytes,
		"instructions":  "Personal app appearance only, never project files or provider settings. Read first: tools return current in full plus saved name/mode labels; export a saved name for its full body. apply merges theme fields into current; a patch of only interface_font/interface_size also updates the active saved theme; replace=true starts from Default so empty colour maps clear previous overrides (the UI sends full themes this way); save saves/updates a named preset and applies it; apply with name and no theme selects your saved theme. duplicate uses name (omit for current) and new_name; rename uses name and new_name. export returns the selected theme object (name, or current); write that object as JSON, and import it in theme with replace semantics. import and duplicate save without applying. Tool mutations return a short summary and changed field names; read again for current and saved labels; export a saved name for its body. delete removes a saved name; reset restores Default and keeps presets. Colours are #RRGGBB under colors.light or colors.dark; accent is the action colour. Loader: optional loader.style (ring/dots/pulse/orbit/bar), loader.duration_ms (400-4000; lower is faster); loader:null clears customization. colors.light/dark.loader changes shared loading/working glyphs, while working/waiting/done/error set semantic status roles. This slice covers core agent marks, turn working, workspace/modal loading and common controls, not every legacy feature spinner. Glyph motion pauses when hidden and with reduced motion; status labels remain. These fields never decide execution state. Prefer colour/font fields; CSS styles the app after normal styles. For large CSS, write a UTF-8 file in your workspace and pass css_file={crew_id,path} on apply/save instead of embedding theme.css in one generated tool argument. Existing file-read admission applies; no host paths or URLs. HTTPS images are allowed in url(), image-set() and other image declarations. HTTPS fonts are allowed in @font-face src and @import of HTTPS font stylesheets (plain URL or URL with a media query). Imported sheets supply font faces only, not interface rules. Embedded data images and local fragments are allowed. Scripts, relative/protocol-relative/javascript URLs and escaped resources are refused. Never hide, cover or disable controls. External hosts receive resource requests; ask before including private data in URLs. Network/CORS failures skip fonts without replacing the rest of the theme. Use stable hooks, not generated classes. Only Default Light and Default Dark are built in. Other looks are personal saved colours/materials/CSS, not shipped renderers or executable games. Background: use the stable ambient/main hooks and small static or transform/opacity CSS decorations. Materials: materials.<surface>.opacity (0 to 1; default 1) and materials.<surface>.blur (0 to 24 px, never on the page) make a surface see-through; they change the background only, so text stays solid. Read the appearance guide (read_skill panel-settings, references/appearance) before choosing colours or CSS: keep text readable against its background and the action label readable on the accent, prefer a still background, no blur over moving artwork, keep custom CSS short, never hide or cover controls. Agent state hooks reuse running/waiting/failed/done/quiet menu marks; quiet is not proof of idle or stopped, failed/done clear when opened. Team working/waiting hooks are booleans from its existing admitted summary; tool-state on web-search cards is historical running/ok/error, not a global live tool indicator. Do not invent blocked/stopped state. Animate only under prefers-reduced-motion: no-preference; never hide warnings or controls. Explicit Artifact/report designs keep their own theme. Never claim visual proof from a saved document; inspect the app or ask the person.",
	}
}
func (api *StreamingAPI) appearance(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	if request.CSSFile != nil || request.ImageFile != nil || request.ImagePath != "" {
		http.Error(w, "Use the appearance tool to read a Crew CSS file.", 400)
		return
	}
	api.appearanceResponse(w, r, request, false)
}

func (api *StreamingAPI) appearanceTool(w http.ResponseWriter, r *http.Request, request appearanceRequest) {
	result := httptest.NewRecorder()
	cssBytes := int64(0)
	source := "inline"
	reason := "none"
	readStatus := 0
	if css, ok := request.Theme["css"].(string); ok {
		cssBytes = int64(len(css))
	}
	defer func() {
		outcome := "accepted"
		if result.Code >= 400 {
			outcome = "refused"
		}
		operation := request.Operation
		if !slices.Contains(appearanceOperations, operation) {
			operation = "unknown"
		}
		if outcome == "refused" && reason == "none" {
			reason = "appearance_validation"
		}
		log.Printf("[APPEARANCE_CSS] operation=%s source=%s bytes=%d outcome=%s status=%d reason=%s read_status=%d", operation, source, cssBytes, outcome, result.Code, reason, readStatus)
		for key, values := range result.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(result.Code)
		_, _ = w.Write(result.Body.Bytes())
	}()
	if request.CSSFile != nil {
		source = "file"
		ref := request.CSSFile
		_, inline := request.Theme["css"]
		if (request.Operation != "apply" && request.Operation != "save") || inline || ref.CrewID == "" || ref.Path == "" {
			reason = "invalid_source"
			http.Error(result, "Use css_file for apply/save instead of theme.css, with an admitted Crew ID and relative path.", 400)
			return
		}
		readPath, pathErr := wf.CleanRelative(ref.Path)
		if pathErr != nil || readPath == "." {
			reason = "invalid_path"
			http.Error(result, "Use a Crew-relative CSS file path.", 400)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil {
			reason = "unauthenticated"
			http.Error(result, "Sign in to read a Crew CSS file.", 401)
			return
		}
		binding, _, _, admitted := api.externalCrewResolve(r.Context(), claims, ref.CrewID)
		if !admitted {
			reason = "crew_unavailable"
			http.Error(result, "Read the CSS from a Crew you can access.", 400)
			return
		}
		roots, bound, rootErr := localCrewRoots(binding.Binding.WorkspacePath)
		if rootErr != nil {
			reason = "binding_unavailable"
			http.Error(result, "The Crew folder is unavailable.", 400)
			return
		}
		// Native writes use the chosen folder. Local Crew resource paths are
		// separate; only the server-owned _files subtree reaches native files.
		if bound && roots.SeparateFiles() {
			readPath = crewroot.FilesSegment + "/" + readPath
		}
		fileReply := httptest.NewRecorder()
		// Reuse the actual read_crew_file admission: caller, Crew, publication,
		// private paths and symlinks stay fenced; no host paths or URLs are accepted.
		api.externalCrewCall(fileReply, r, "read_crew_file", map[string]any{"crew_id": ref.CrewID, "path": readPath, "agent_id": ref.AgentID})
		var file struct {
			Content   *string `json:"content"`
			Size      int64   `json:"size"`
			Truncated bool    `json:"truncated"`
		}
		readStatus = fileReply.Code
		if fileReply.Code >= 400 {
			reason = "file_read_refused"
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(fileReply.Body.Bytes(), &failure) == nil {
				switch failure.Error.Code {
				case "not_found", "forbidden", "insufficient_scope", "workspace_unavailable":
					reason = failure.Error.Code
				}
			}
			http.Error(result, "Read the CSS from a file in a Crew you can access.", 400)
			return
		}
		if json.Unmarshal(fileReply.Body.Bytes(), &file) != nil {
			reason = "invalid_file_reply"
			http.Error(result, "Read the CSS from a file in a Crew you can access.", 400)
			return
		}
		cssBytes = file.Size
		if file.Content == nil || file.Truncated || file.Size > appearanceCSSMaxBytes || int64(len(*file.Content)) != file.Size || !utf8.ValidString(*file.Content) {
			reason = "invalid_css_file"
			http.Error(result, "Use a complete UTF-8 CSS file of at most 131,072 bytes (128 KiB).", 400)
			return
		}
		theme := make(map[string]any, len(request.Theme)+1)
		for key, value := range request.Theme {
			theme[key] = value
		}
		theme["css"] = *file.Content
		request.Theme = theme
	}
	if request.Operation == "import_image" {
		api.appearanceImageFile(result, r, request)
		return
	}
	api.appearanceResponse(result, r, request, true)
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
	if request.Operation == "mac_wallpapers" || request.Operation == "import_mac_wallpaper" {
		api.appearanceMacWallpaper(w, r, request)
		return
	}
	if request.Operation == "list_images" || request.Operation == "delete_image" {
		api.appearanceImageLibrary(w, r, request)
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
	savedBefore, recovered, themeName := len(doc.Saved), 0, request.Name
	switch request.Operation {
	case "save", "import", "apply", "duplicate", "reset":
		doc.Saved, recovered = appearanceRestoreMissing(doc.Saved, appearanceKnownSaved[user])
	}
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
			themeName = next.Name
			if savedChanged, err = saveAppearanceTheme(&doc, next); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		if request.Operation == "import" {
			request.Name = next.Name
		} else {
			doc.Current = next
			// #2949: the picker's small typography patch also updates the active
			// saved look. Full-theme apply/preview keeps its existing semantics.
			if request.Operation == "apply" && !request.Replace && len(request.Theme) > 0 {
				typographyOnly := true
				for key := range request.Theme {
					typographyOnly = typographyOnly && (key == "interface_font" || key == "interface_size")
				}
				if i := appearanceSavedIndex(doc, before.Name); typographyOnly && i >= 0 {
					savedChanged = doc.Saved[i].InterfaceFont != next.InterfaceFont || doc.Saved[i].InterfaceSize != next.InterfaceSize
					doc.Saved[i].InterfaceFont, doc.Saved[i].InterfaceSize = next.InterfaceFont, next.InterfaceSize
				}
			}
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
		appearanceKnownSaved[user] = slices.Clone(doc.Saved)
		source := "screen"
		if forTool {
			source = "tool"
		}
		log.Printf("[APPEARANCE_CSS] event=saved operation=%s theme=%q saved_before=%d saved_after=%d recovered=%d actor=%s session=%s via=%s", request.Operation, themeName, savedBefore, len(doc.Saved), recovered, logUsernameForUserID(user), firstNonEmptyTrimmed(r.Header.Get("X-Session-ID"), "-"), source)
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
		"operation":    map[string]any{"type": "string", "enum": operations},
		"image_id":     externalString("Own image asset ID from list_images, for delete_image. Does not select another person."),
		"image_path":   externalString("import_image only: one absolute or ~/ local PNG/JPEG/WebP path on your OWN Mac. Same local OS-owner admission as Mac aerial import; never a server or another person path. Do not print it in logs."),
		"image_file":   map[string]any{"type": "object", "description": "import_image only: admitted Crew-relative PNG/JPEG/WebP up to5MB, no host path.", "properties": map[string]any{"crew_id": externalString("Crew ID"), "path": externalString("Crew-relative path"), "agent_id": externalString("Publication context if required")}},
		"blur_px":      map[string]any{"type": "integer", "description": "import_image only: static blur0–20px baked into your copy; omit for crisp wallpaper."},
		"wallpaper_id": externalString("Opaque ID from mac_wallpapers, for import_mac_wallpaper on your own local Mac. Creates a short personal clip without changing the theme."),
		"css_file":     map[string]any{"type": "object", "additionalProperties": false, "required": []any{"crew_id", "path"}, "properties": map[string]any{"crew_id": externalString("Crew containing the CSS file."), "path": externalString("Relative admitted UTF-8 CSS file path; at most 128 KiB. For apply/save instead of theme.css."), "agent_id": externalString("Publication agent context when required by read_crew_file.")}},
		"theme":        map[string]any{"type": "object", "description": "Partial current theme fields returned by read and its guide. Colours are nested under colors.light/colors.dark. icons controls the existing outline glyph stroke/scale, not another library; borders styles core shared surfaces and existing selected/running agent rows. materials.<page|panel|popover|sidebar|composer> has opacity 0-1 and blur 0-24 (not on page; use only over still artwork). Personal CSS supplies decoration; no shipped scenes. CSS max128 KiB (131,072 UTF-8 bytes). Unspecified fields stay as saved."},
		"replace":      map[string]any{"type": "boolean", "description": "Replace from Default instead of merging; use for a full theme or to clear omitted colour overrides."},
		"name":         externalString("Saved theme name for apply/delete/export/rename/duplicate; save/import may override the imported name. Omit to export/duplicate current. Up to60 characters."),
		"new_name":     externalString("New saved name for rename/duplicate; up to60 characters. Never overwrites a different saved theme."),
	})
}
