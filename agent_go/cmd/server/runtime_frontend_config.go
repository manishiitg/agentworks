package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

// runtimeFrontendConfigJS builds the window.__APP_RUNTIME_CONFIG__ bootstrap
// script every frontend page loads before anything else (runtime-config.js,
// registered in runServer). apiBaseUrl/workspaceApiBaseUrl are always
// present; cdpEnabled advertises the deployment-level browser capability; the
// product-surface and branding keys can be overridden via env. A plain AgentWorks
// server retains its shared products. Local launchers select workflows and Crew
// unless source development explicitly opts into server products; a shell or
// deployment running a different product (e.g. SparkQuill) sets
// AGENTWORKS_ENABLED_PRODUCT_SURFACES/AGENTWORKS_DEFAULT_PRODUCT_SURFACE so
// the frontend's product-surface switcher pins to its intended surface(s).
func runtimeFrontendConfigJS(actualPort int, workspaceURL string) string {
	// Managed workspace services require a private token held by the agent
	// proxy. Browsers must never call the service directly or receive its token.
	if strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")) != "" {
		workspaceURL = fmt.Sprintf("http://localhost:%d/api/wp", actualPort)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "window.__APP_RUNTIME_CONFIG__ = {\n  apiBaseUrl: \"http://localhost:%d\",\n  workspaceApiBaseUrl: %q", actualPort, workspaceURL)
	fmt.Fprintf(&b, ",\n  cdpEnabled: %t,\n  browserModes: %s", browser.CDPEnabled(), jsStringArrayLiteral(browser.VisibleModes()))
	local := localProductInstallation()
	localOptIn := localServerProductsEnabled()
	if local {
		b.WriteString(",\n  deploymentMode: \"local\"")
		fmt.Fprintf(&b, ",\n  localServerProducts: %t", localOptIn)
		if localOptIn && vaultConfigured() {
			fmt.Fprintf(&b, ",\n  gatewayUrl: %q", strings.TrimRight(strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")), "/"))
		}
	}
	surfaces := splitAndTrimCommaList(os.Getenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES"))
	if len(surfaces) == 0 {
		if local && !localOptIn {
			surfaces = []string{"agentworks", "work", "bullpen"}
		} else if localOptIn {
			surfaces = []string{"agentworks", "relays", "work", "mcp-gateway", "knowledgebase", "bullpen"}
			if strings.TrimSpace(os.Getenv("AGENTWORKS_GATEWAY_BINARY")) != "" {
				surfaces = append(surfaces, "llm-gateway")
			}
		} else {
			surfaces = []string{"agentworks", "relays", "work", "mcp-gateway", "knowledgebase"}
			if productEnabled("bullpen") {
				surfaces = append(surfaces, "bullpen")
			}
		}
	} else if local {
		available := make([]string, 0, len(surfaces))
		for _, surface := range surfaces {
			if installationProductAvailable(surface) {
				available = append(available, surface)
			}
		}
		surfaces = available
		if len(surfaces) == 0 {
			surfaces = []string{"agentworks", "work", "bullpen"}
		}
	}
	available := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if productEnabled(surface) {
			available = append(available, surface)
		}
	}
	surfaces = available
	fmt.Fprintf(&b, ",\n  enabledProductSurfaces: %s", jsStringArrayLiteral(surfaces))
	defaultSurface := strings.TrimSpace(os.Getenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE"))
	if defaultSurface == "" {
		defaultSurface = "agentworks"
	}
	if len(surfaces) > 0 && !allowlistHasProduct(strings.Join(surfaces, ","), defaultSurface) {
		defaultSurface = surfaces[0]
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

// The source launcher already selects the current browser frontend for CLI
// approval. Use that same origin for app pages instead of a stale static build.
// Packaged apps and deployed servers continue serving their bundled frontend.
func localFrontendRedirect(r *http.Request) string {
	if !IsLocalMode() || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return ""
	}
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") ||
		(filepath.Ext(r.URL.Path) != "" && r.URL.Path != "/index.html") {
		return ""
	}
	origin := "http://" + r.Host
	browser := cliOAuthBrowserOrigin(origin)
	if browser == origin {
		return ""
	}
	serverURL, _ := url.Parse(origin)
	browserURL, _ := url.Parse(browser)
	// localhost and numeric loopback addresses on the same port are the same
	// server. Redirecting between those aliases would loop forever.
	if browserURL.Port() == serverURL.Port() {
		return ""
	}
	return browser + r.URL.RequestURI()
}
