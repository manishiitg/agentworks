package dashboards

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"path"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// TSX is compiled from the submitted bundle only. No host files, npm installs,
// package scripts or authored server code execute while saving a dashboard.
func prepareTSXSnapshot(snap *Snapshot) error {
	if _, ok := snap.Files["index.json"]; ok {
		return fail(400, "JSON dashboards have been removed. Supply index.tsx and remove index.json in the same update; use the saved-dashboard migration for existing pages.")
	}
	if _, ok := snap.Files["index.tsx"]; !ok {
		return nil // Existing HTML dashboards retain their established path.
	}
	previousHTML := snap.Files["index.html"]
	const entry = `import Dashboard, * as dashboard from './index.tsx'; import { mount } from '@agentworks/dashboard'; mount({...dashboard, default: Dashboard});`
	modules := map[string]bool{"react": true, "react-dom": true, "react-dom/client": true, "react/jsx-runtime": true, "react/jsx-dev-runtime": true, "@agentworks/dashboard": true, "recharts": true}
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{"aw:entry"}, Bundle: true, Write: false,
		Outfile: "dashboard-app.js", Format: api.FormatIIFE, Platform: api.PlatformBrowser,
		Target: api.ES2022, JSX: api.JSXAutomatic, LogLevel: api.LogLevelSilent,
		Define: map[string]string{"process.env.NODE_ENV": `"production"`},
		Plugins: []api.Plugin{{Name: "dashboard-bundle", Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if args.Kind == api.ResolveEntryPoint && args.Path == "aw:entry" {
					return api.OnResolveResult{Path: "entry", Namespace: "dashboard-entry"}, nil
				}
				// CSS browser resources stay browser resources, as in existing
				// HTML dashboards. The compiler never reads them from the network.
				if args.Kind == api.ResolveCSSURLToken || args.Kind == api.ResolveCSSImportRule {
					lower := strings.ToLower(args.Path)
					if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "#") {
						return api.OnResolveResult{Path: args.Path, External: true}, nil
					}
				}
				if modules[args.Path] {
					return api.OnResolveResult{Path: args.Path, Namespace: "dashboard-runtime"}, nil
				}
				if !strings.HasPrefix(args.Path, "./") && !strings.HasPrefix(args.Path, "../") && args.Kind != api.ResolveCSSURLToken && args.Kind != api.ResolveCSSImportRule {
					return api.OnResolveResult{}, fmt.Errorf("unsupported dashboard import %q; use bundled relative files or a runtime module from get_dashboard_catalog", args.Path)
				}
				base := path.Clean(path.Join(path.Dir(args.Importer), args.Path))
				if strings.HasPrefix(base, "../") || strings.HasPrefix(base, "/") || strings.HasPrefix(base, "scripts/") {
					return api.OnResolveResult{}, fmt.Errorf("dashboard import must stay inside its browser bundle: %s", args.Path)
				}
				for _, candidate := range []string{base, base + ".tsx", base + ".ts", base + ".js", base + ".mjs", base + ".json", base + "/index.tsx", base + "/index.ts", base + "/index.js"} {
					if _, ok := snap.Files[candidate]; ok && safeFile(candidate) {
						return api.OnResolveResult{Path: candidate, Namespace: "dashboard-source"}, nil
					}
				}
				return api.OnResolveResult{}, fmt.Errorf("dashboard import not found: %s", args.Path)
			})
			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "dashboard-entry"}, func(api.OnLoadArgs) (api.OnLoadResult, error) {
				return api.OnLoadResult{Contents: stringPtr(entry), Loader: api.LoaderJS}, nil
			})
			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "dashboard-runtime"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				name, _ := json.Marshal(args.Path)
				source := "module.exports = window.__agentworksDashboard.modules[" + string(name) + "];"
				return api.OnLoadResult{Contents: &source, Loader: api.LoaderJS}, nil
			})
			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "dashboard-source"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				content := snap.Files[args.Path]
				loader := map[string]api.Loader{".tsx": api.LoaderTSX, ".ts": api.LoaderTS, ".js": api.LoaderJS, ".mjs": api.LoaderJS, ".json": api.LoaderJSON, ".css": api.LoaderCSS, ".txt": api.LoaderText, ".csv": api.LoaderText}[path.Ext(args.Path)]
				if loader == api.LoaderNone {
					data, err := assetBytes(args.Path, content)
					if err != nil {
						return api.OnLoadResult{}, err
					}
					content, loader = string(data), api.LoaderDataURL
				}
				return api.OnLoadResult{Contents: &content, Loader: loader}, nil
			})
		}}},
	})
	if len(result.Errors) != 0 {
		return fail(400, "Dashboard TSX could not compile:\n"+strings.Join(api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage, Color: false}), "\n"))
	}
	delete(snap.Files, "dashboard-app.js")
	delete(snap.Files, "dashboard-app.css")
	css := ""
	for _, file := range result.OutputFiles {
		name := path.Base(file.Path)
		snap.Files[name] = string(file.Contents)
		if name == "dashboard-app.css" {
			css = string(file.Contents)
		}
	}
	// The report host renders srcdoc, so project-relative script/link URLs do
	// not resolve as web assets. Embed the compiled output in the saved page;
	// retain separate generated files for inspection and exact-revision reads.
	js, _ := json.Marshal(snap.Files["dashboard-app.js"])
	styles, _ := json.Marshal(css)
	snap.Files["index.html"] = `<!doctype html><html lang="en" data-report-renderer="tsx"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(snap.Title) + `</title><link rel="stylesheet" href="/dashboard.css"></head><body><div id="dashboard-root"><p role="status">Loading dashboard…</p></div><script defer src="/dashboard-react.js" onerror="document.documentElement.dataset.reportState='error';document.getElementById('dashboard-root').textContent='Dashboard renderer unavailable.'"></script><script>document.addEventListener('DOMContentLoaded',function(){var css=document.createElement('style');css.textContent=` + string(styles) + `;document.head.append(css);var app=document.createElement('script');app.textContent=` + string(js) + `;document.body.append(app)},{once:true});</script></body></html>`
	// A source edit removes the automatic-page marker. Only the exact generated
	// source/body pair can retain live platform updates.
	body := snap.Files["index.html"]
	sum := sha256.Sum256([]byte(body))
	marked := body + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n"
	if previousHTML == marked {
		snap.Files["index.html"] = marked
	}
	return nil
}

// CompileTSX uses the same restricted compiler for defaults and the one-off
// saved-page migration. It does not execute authored code or change access.
func CompileTSX(title string, files map[string]string) (map[string]string, error) {
	copy := make(map[string]string, len(files))
	for name, source := range files {
		copy[name] = source
	}
	snap := Snapshot{Title: title, Files: copy}
	if err := prepareTSXSnapshot(&snap); err != nil {
		return nil, err
	}
	return snap.Files, nil
}

func stringPtr(value string) *string { return &value }

// ComponentCatalog describes the actual shared TSX props for app and MCP.
//
//go:embed components-catalog.json
var ComponentCatalog []byte

func AuthoringCatalog() map[string]any {
	var catalog map[string]any
	_ = json.Unmarshal(ComponentCatalog, &catalog)
	catalog["default_format"] = "tsx"
	catalog["motion"] = map[string]any{"discovery": "Set motion=true with create_dashboard or update_dashboard to list an interactive playback page under Motion dashboards. This is revisioned metadata on the same dashboard, not a separate product or access rule. Publish the new revision to update its live category. Existing pages can be labelled without replacing their files.", "playback": "Use report.motion.create with a stable id, duration and synchronous seekable render callback. Use the shared MotionControls toolbar by default: pass the clock, use an authored position:relative container with space for its floating controls, or placement=inline. Supply positionLabel/detail from real observations; customize className/style, visible controls and speeds. The toolbar subscribes to the clock itself; drive charts and values through React state. Load real dated data before playback; never invent history. Layout, charts and controls stay customizable.", "export": "Optional export_dashboard_motion renders the published layout, selected theme and frozen data to MP4 with pinned HyperFrames. A page without seekable motion produces a still video; motion=true alone does not animate it."}
	var names []string
	if components, ok := catalog["components"].(map[string]any); ok {
		for name := range components {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	catalog["tsx_components"] = map[string]any{"module": "@agentworks/dashboard", "components": names, "exports": []string{"components", "RefreshButton", "UploadZone", "MediaGallery", "MediaPreview", "useDashboardRefresh", "createDashboardStore", "useDashboardStore", "useDashboardBinding"}, "contract": "Import shared shadcn components from @agentworks/dashboard. Common components have named exports; the complete catalog is available as components.Name. Use direct props and children, onPress for Button, on={{event: callback}} for component events, and bindings for optional shared state. Props match this catalog; nullable props may be omitted. Use React for conditions, loops, state and layout. Charts use Recharts; import recharts directly for bespoke charts. AskButton and ChatPanel render only for shared Run users."}
	catalog["formats"] = map[string]any{
		"tsx": map[string]any{"entry": "index.tsx", "runtime_modules": []string{"react", "react-dom", "react-dom/client", "react/jsx-runtime", "@agentworks/dashboard", "recharts"},
			"contract": "Default-export a React component receiving {data, report}. Use shared platform components from @agentworks/dashboard, including components.Name for the full catalog. Use React composition; JSON dashboard specifications are not supported. Optionally export async loadData(report) for initial data and refresh. Relative TSX/TS/JS/JSON/CSS/assets are bundled by the server. TypeScript is transpiled; type checking is the author's responsibility. No separate npm app or server. Shared UploadZone, MediaGallery and MediaPreview support Crew references and saved media/document results. UploadZone uses the signed-in Crew attachment route and its current limits; onUploaded receives saved paths. MediaGallery accepts {id, kind, title, revision, payload} items loaded from ui_presentations. Its mounted player and selection survive unrelated live data updates. Use ordinary CSS; arbitrary Tailwind classes are not generated. Generated index.html/dashboard-app.js/dashboard-app.css are replaced on every save. Imports outside the runtime modules and submitted files are unsupported.",
			"example":  "import { Card, Table, AskButton, RefreshButton } from '@agentworks/dashboard';\nexport async function loadData(report) { return report.query('SELECT name FROM sqlite_master WHERE type = ?', ['table']); }\nexport default function Dashboard({data}) { return <Card title=\"Project records\"><Table columns={['Name']} rows={data.map(row => [row.name])}/><RefreshButton/><AskButton label=\"Ask about this dashboard\"/></Card>; }"},
		"html": map[string]any{"entry": "index.html", "contract": "A quick shareable page, such as a decision, a report or a file view. HTML pages render, edit, publish, share and export like TSX. Use TSX for live dashboards with data, buttons and functions. JSON dashboards are not supported."},
	}
	return catalog
}
