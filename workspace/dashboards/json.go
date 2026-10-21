package dashboards

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// JSONCatalog is generated from the exact pinned upstream component definitions.
// Both the browser and Go service use this catalog, not a hand-maintained subset.
//
//go:embed json-render-catalog.json
var JSONCatalog []byte

type JSONElement struct {
	Type     string                     `json:"type"`
	Props    map[string]any             `json:"props"`
	Children []string                   `json:"children"`
	Slots    map[string][]string        `json:"slots,omitempty"`
	On       map[string]json.RawMessage `json:"on,omitempty"`
	Watch    map[string]json.RawMessage `json:"watch,omitempty"`
	Visible  any                        `json:"visible,omitempty"`
	Repeat   any                        `json:"repeat,omitempty"`
}
type JSONData struct {
	StatePath string `json:"statePath"`
	Method    string `json:"method"`
	SQL       string `json:"sql,omitempty"`
	Path      string `json:"path,omitempty"`
	Params    []any  `json:"params,omitempty"`
	Args      any    `json:"args,omitempty"`
}
type JSONMotion struct {
	Duration  float64 `json:"duration"`
	StatePath string  `json:"statePath"`
}
type JSONSpec struct {
	Root     string                 `json:"root"`
	Elements map[string]JSONElement `json:"elements"`
	State    map[string]any         `json:"state,omitempty"`
	Data     []JSONData             `json:"data,omitempty"`
	Styles   string                 `json:"styles,omitempty"`
	Motion   *JSONMotion            `json:"motion,omitempty"`
}

var jsonValidators struct {
	sync.Once
	schemas map[string]*jsonschema.Schema
	events  map[string][]string
	slots   map[string][]string
	err     error
}

func jsonPointer(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "__proto__" || part == "constructor" || part == "prototype" {
			return false
		}
	}
	return true
}

func safeStateBindings(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "$state" || key == "$bindState" {
				if p, ok := child.(string); ok && !jsonPointer(p) {
					return false
				}
			}
			if !safeStateBindings(child) {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !safeStateBindings(child) {
				return false
			}
		}
	}
	return true
}

// Nullable upstream props may be omitted. Expressions are resolved by the
// upstream renderer; they are values, never executable code.
func expressionProps(v any) any {
	obj, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if properties, ok := obj["properties"].(map[string]any); ok {
		required, _ := obj["required"].([]any)
		keep := []any{}
		for _, key := range required {
			p, _ := properties[key.(string)].(map[string]any)
			nullable := false
			if choices, ok := p["anyOf"].([]any); ok {
				for _, c := range choices {
					if x, ok := c.(map[string]any); ok && x["type"] == "null" {
						nullable = true
					}
				}
			}
			if !nullable {
				keep = append(keep, key)
			}
		}
		obj["required"] = keep
		for key, p := range properties {
			literal := expressionProps(p)
			expressions := []any{literal}
			for _, name := range []string{"$state", "$bindState", "$item", "$bindItem", "$template"} {
				expressions = append(expressions, map[string]any{"type": "object", "required": []string{name}, "properties": map[string]any{name: map[string]any{"type": "string"}}, "additionalProperties": false})
			}
			expressions = append(expressions, map[string]any{"type": "object", "required": []string{"$index"}, "properties": map[string]any{"$index": map[string]any{"const": true}}, "additionalProperties": false})
			expressions = append(expressions, map[string]any{"type": "object", "required": []string{"$cond", "$then", "$else"}, "properties": map[string]any{"$cond": map[string]any{}, "$then": map[string]any{}, "$else": map[string]any{}}, "additionalProperties": false})
			properties[key] = map[string]any{"anyOf": expressions}
		}
	}
	if items, ok := obj["items"].(map[string]any); ok {
		obj["items"] = expressionProps(items)
	}
	return obj
}

func compileJSONCatalog() {
	jsonValidators.schemas = map[string]*jsonschema.Schema{}
	jsonValidators.events = map[string][]string{}
	jsonValidators.slots = map[string][]string{}
	var doc struct {
		Components map[string]struct {
			Props  map[string]any `json:"props"`
			Events []string       `json:"events"`
			Slots  []string       `json:"slots"`
		} `json:"components"`
	}
	if err := json.Unmarshal(JSONCatalog, &doc); err != nil {
		jsonValidators.err = err
		return
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	for name, def := range doc.Components {
		jsonValidators.events[name] = def.Events
		jsonValidators.slots[name] = def.Slots
		delete(def.Props, "$schema")
		uri := "https://dashboard.invalid/" + name
		encoded, err := json.Marshal(expressionProps(def.Props))
		if err != nil {
			jsonValidators.err = err
			return
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
		if err != nil {
			jsonValidators.err = err
			return
		}
		if err := compiler.AddResource(uri, document); err != nil {
			jsonValidators.err = err
			return
		}
		compiled, err := compiler.Compile(uri)
		if err != nil {
			jsonValidators.err = err
			return
		}
		jsonValidators.schemas[name] = compiled
	}
}

func validJSONActions(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	var check func(any) bool
	check = func(v any) bool {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				if !check(item) {
					return false
				}
			}
			return true
		case map[string]any:
			if !safeStateBindings(x) {
				return false
			}
			name, _ := x["action"].(string)
			if params, ok := x["params"].(map[string]any); ok {
				if p, ok := params["statePath"].(string); ok && !jsonPointer(p) {
					return false
				}
			}
			switch name {
			case "setState", "pushState", "removeState", "validateForm", "refreshData":
			default:
				return false
			}
			return true
		}
		return false
	}
	return check(v)
}

// ValidateJSONSpec runs on save and publication, including direct workspace
// operations. Product data and execution access remain with existing APIs.
func ValidateJSONSpec(data []byte) (JSONSpec, error) {
	var spec JSONSpec
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&spec) != nil || d.Decode(new(any)) != io.EOF {
		return spec, fail(400, "invalid dashboard JSON specification")
	}
	jsonValidators.Do(compileJSONCatalog)
	if jsonValidators.err != nil {
		return spec, fmt.Errorf("dashboard catalog: %w", jsonValidators.err)
	}
	if len(spec.Elements) == 0 || len(spec.Elements) > 2000 {
		return spec, fail(400, "dashboard needs 1–2000 elements")
	}
	if _, ok := spec.Elements[spec.Root]; !ok {
		return spec, fail(400, "dashboard root element is missing")
	}
	for id, el := range spec.Elements {
		validator := jsonValidators.schemas[el.Type]
		if validator == nil {
			return spec, fail(400, "unknown dashboard component: "+el.Type)
		}
		if el.Props == nil {
			return spec, fail(400, "dashboard elements need a props object")
		}
		if el.Children == nil {
			return spec, fail(400, "dashboard elements need a children array (empty for a leaf)")
		}
		if !safeStateBindings(el.Props) {
			return spec, fail(400, "dashboard state binding has an unsafe path")
		}
		if err := validator.Validate(el.Props); err != nil {
			return spec, fail(400, fmt.Sprintf("dashboard element %s (%s): %v", id, el.Type, err))
		}
		if len(el.Children) > 0 && !contains(jsonValidators.slots[el.Type], "default") {
			return spec, fail(400, "dashboard component has no child slot: "+el.Type)
		}
		for slot := range el.Slots {
			if slot == "default" || !contains(jsonValidators.slots[el.Type], slot) {
				return spec, fail(400, "unknown dashboard slot: "+slot)
			}
		}
		for event := range el.On {
			if !contains(jsonValidators.events[el.Type], event) {
				return spec, fail(400, "unknown dashboard component event: "+event)
			}
		}
		if el.Type == "ChatPanel" {
			if n, _ := el.Props["height"].(float64); n <= 0 {
				return spec, fail(400, "ChatPanel needs a positive height")
			}
		}
		if href, ok := el.Props["href"]; ok && href != nil {
			s, ok := href.(string)
			u, err := url.Parse(s)
			if !ok || err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
				return spec, fail(400, "dashboard links need a static http, https or mailto address")
			}
		}
		// Images always pass through the existing project file API. A bound value
		// cannot become a URL that sends project data to another host.
		if src, ok := el.Props["src"].(string); ok && src != "" && !strings.HasPrefix(src, "db/") && !strings.HasPrefix(src, "knowledgebase/") && !strings.HasPrefix(src, "docs/") && !strings.HasPrefix(src, "{{dashboard_assets}}/") && !strings.HasPrefix(src, "data:image/") {
			return spec, fail(400, "dashboard image source must be a project asset or inline image")
		}
		for _, bindings := range []map[string]json.RawMessage{el.On, el.Watch} {
			for _, binding := range bindings {
				if !validJSONActions(binding) {
					return spec, fail(400, "dashboard event contains an unknown action")
				}
			}
		}
	}
	colors := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		el, ok := spec.Elements[id]
		if !ok {
			return fail(400, "dashboard child element missing: "+id)
		}
		if colors[id] == 1 {
			return fail(400, "dashboard elements contain a cycle")
		}
		if colors[id] == 2 {
			return nil
		}
		colors[id] = 1
		children := append([]string{}, el.Children...)
		for _, slot := range el.Slots {
			children = append(children, slot...)
		}
		for _, child := range children {
			if err := visit(child); err != nil {
				return err
			}
		}
		colors[id] = 2
		return nil
	}
	for id := range spec.Elements {
		if err := visit(id); err != nil {
			return spec, err
		}
	}
	for _, binding := range spec.Data {
		if !jsonPointer(binding.StatePath) {
			return spec, fail(400, "dashboard data needs a safe statePath")
		}
		switch binding.Method {
		case "query":
			if strings.TrimSpace(binding.SQL) == "" {
				return spec, fail(400, "dashboard query needs SQL")
			}
		case "get":
			if binding.Path == "" {
				return spec, fail(400, "dashboard get needs a project path")
			}
		case "run":
			if !(strings.HasPrefix(binding.Path, "code/") || strings.HasPrefix(binding.Path, "{{dashboard_scripts}}/")) || strings.Contains(binding.Path, "..") || !(strings.HasSuffix(binding.Path, ".py") || strings.HasSuffix(binding.Path, ".js") || strings.HasSuffix(binding.Path, ".mjs")) {
				return spec, fail(400, "dashboard run needs a script under code/")
			}
		default:
			return spec, fail(400, "dashboard data method must be query, get or run")
		}
	}
	if spec.Motion != nil && (spec.Motion.Duration <= 0 || spec.Motion.Duration > 600 || !jsonPointer(spec.Motion.StatePath)) {
		return spec, fail(400, "dashboard motion needs duration (0–600 seconds) and a statePath")
	}
	return spec, nil
}

func JSONHTML(title string, source []byte) (string, error) {
	spec, err := ValidateJSONSpec(source)
	if err != nil {
		return "", err
	}
	// json.Marshal escapes HTML delimiters, including a literal </script> in
	// data or authored CSS. No specification value becomes executable markup.
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return `<!doctype html><html lang="en" data-report-renderer="json-render"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'none'; base-uri 'none'"><title>` + html.EscapeString(title) + `</title><link rel="stylesheet" href="/dashboard-json.css"><script id="__dashboard_spec" type="application/json">` + string(encoded) + `</script></head><body><div id="dashboard-root"><p role="status">Loading dashboard…</p></div><script defer src="/dashboard-json.js" onerror="document.documentElement.dataset.reportState='error';document.getElementById('dashboard-root').textContent='Dashboard renderer unavailable.'"></script></body></html>`, nil
}

func prepareJSONSnapshot(snap *Snapshot) error {
	if source, ok := snap.Files["index.json"]; ok {
		page, err := JSONHTML(snap.Title, []byte(source))
		if err != nil {
			return err
		}
		// Preserve the platform's automatic-page fingerprint only while the
		// JSON source still produces exactly that page. An owner changing the
		// spec loses the marker even if they left the old HTML file untouched.
		sum := sha256.Sum256([]byte(page))
		automatic := page + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n"
		if snap.Files["index.html"] == automatic {
			page = automatic
		}
		snap.Files["index.html"] = page
	}
	return nil
}

// JSONSpecFromHTML identifies only the deterministic wrapper, leaving authored
// HTML documents on their established validation and rendering path.
func JSONSpecFromHTML(page string) (JSONSpec, bool, error) {
	if !strings.Contains(page, `data-report-renderer="json-render"`) {
		return JSONSpec{}, false, nil
	}
	const start = `<script id="__dashboard_spec" type="application/json">`
	i := strings.Index(page, start)
	if i < 0 {
		return JSONSpec{}, true, fail(400, "dashboard JSON payload missing")
	}
	source := page[i+len(start):]
	end := strings.Index(source, "</script>")
	if end < 0 {
		return JSONSpec{}, true, fail(400, "dashboard JSON payload incomplete")
	}
	spec, err := ValidateJSONSpec([]byte(source[:end]))
	return spec, true, err
}
