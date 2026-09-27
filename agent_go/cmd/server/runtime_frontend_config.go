package server

import (
	"fmt"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

// runtimeFrontendConfigJS builds the window.__APP_RUNTIME_CONFIG__ bootstrap
// script every frontend page loads before anything else (runtime-config.js,
// registered in runServer). apiBaseUrl/workspaceApiBaseUrl are always
// present; cdpEnabled advertises the deployment-level browser capability; the
// product-surface and branding keys can be overridden via env — see
// docs/design/sparkquill_desktop_on_platform_plan.md P0. A plain AgentWorks
// server exposes AgentWorks, Relays, and its built-in Crew coding surface; a shell or
// deployment running a different product (e.g. SparkQuill) sets
// AGENTWORKS_ENABLED_PRODUCT_SURFACES/AGENTWORKS_DEFAULT_PRODUCT_SURFACE so
// the frontend's product-surface switcher pins to its intended surface(s).
func runtimeFrontendConfigJS(actualPort int, workspaceURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "window.__APP_RUNTIME_CONFIG__ = {\n  apiBaseUrl: \"http://localhost:%d\",\n  workspaceApiBaseUrl: %q", actualPort, workspaceURL)
	fmt.Fprintf(&b, ",\n  cdpEnabled: %t", browser.CDPEnabled())
	surfaces := splitAndTrimCommaList(os.Getenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES"))
	if len(surfaces) == 0 {
		surfaces = []string{"agentworks", "relays", "work", "code"}
	}
	fmt.Fprintf(&b, ",\n  enabledProductSurfaces: %s", jsStringArrayLiteral(surfaces))
	defaultSurface := strings.TrimSpace(os.Getenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE"))
	if defaultSurface == "" {
		defaultSurface = "agentworks"
	}
	fmt.Fprintf(&b, ",\n  defaultProductSurface: %q", defaultSurface)
	if v := strings.TrimSpace(os.Getenv("AGENTWORKS_APP_NAME")); v != "" {
		fmt.Fprintf(&b, ",\n  appName: %q", v)
	}
	if v := strings.TrimSpace(os.Getenv("AGENTWORKS_FAVICON_URL")); v != "" {
		fmt.Fprintf(&b, ",\n  faviconUrl: %q", v)
	}
	b.WriteString("\n};\n")
	return b.String()
}

func splitAndTrimCommaList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	raw := strings.Split(v, ",")
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if t := strings.TrimSpace(r); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func jsStringArrayLiteral(items []string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = fmt.Sprintf("%q", item)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// staticFrontendDir resolves the directory the SPA catch-all serves from
// (spaStaticFileHandler in runServer). Defaults to the historical
// cwd-relative "./static/" so every existing deployment is unaffected;
// STATIC_DIR lets a desktop shell pass an absolute Resources path instead of
// having to chdir there itself (docs/design/sparkquill_desktop_on_platform_plan.md
// P0 — this is what lets the shell drop its log-symlink cwd workaround).
func staticFrontendDir() string {
	if override := strings.TrimSpace(os.Getenv("STATIC_DIR")); override != "" {
		return override
	}
	return "./static/"
}
