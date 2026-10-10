package server

import (
	"strings"
	"testing"
)

func TestRuntimeFrontendConfigJSDefaultsToAgentWorksWorkAndCode(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "true")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE", "")
	t.Setenv("AGENTWORKS_APP_NAME", "")
	t.Setenv("AGENTWORKS_FAVICON_URL", "")

	got := runtimeFrontendConfigJS(45678, "http://localhost:45679")
	want := "window.__APP_RUNTIME_CONFIG__ = {\n  apiBaseUrl: \"http://localhost:45678\",\n  workspaceApiBaseUrl: \"http://localhost:45679\",\n  cdpEnabled: true,\n  enabledProductSurfaces: [\"agentworks\", \"relays\", \"work\", \"code\", \"mcp-gateway\", \"knowledgebase\"],\n  defaultProductSurface: \"agentworks\"\n};\n"
	if got != want {
		t.Fatalf("a plain AgentWorks deployment must expose AgentWorks, Relays, Crew, Code, Vault, and Brain\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRuntimeFrontendLocalProfileCannotRestoreServerProducts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,relays,knowledgebase,mcp-gateway,llm-gateway,work,code")
	t.Setenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE", "mcp-gateway")
	got := runtimeFrontendConfigJS(45678, "http://localhost:45679")
	for _, want := range []string{`deploymentMode: "local"`, `enabledProductSurfaces: ["agentworks", "work", "code"]`, `defaultProductSurface: "agentworks"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	for _, configured := range []string{"", "relays,mcp-gateway"} {
		t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", configured)
		if got := runtimeFrontendConfigJS(45678, ""); !strings.Contains(got, `enabledProductSurfaces: ["agentworks", "work", "code"]`) {
			t.Fatalf("local fallback includes unavailable products: %s", got)
		}
	}
}

func TestRuntimeFrontendLocalOptInAdvertisesServerProducts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("CAPLAYER_SERVICE_URL", "http://127.0.0.1:18745/")
	got := runtimeFrontendConfigJS(45678, "http://localhost:45679")
	for _, want := range []string{`deploymentMode: "local"`, `localServerProducts: true`, `gatewayUrl: "http://127.0.0.1:18745"`, `"relays"`, `"knowledgebase"`, `"mcp-gateway"`, `"llm-gateway"`, `"code"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestRuntimeFrontendConfigJSEmitsProductSurfaceAndBrandingKeys(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", " sparkquill , sparkquill ")
	t.Setenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE", "sparkquill")
	t.Setenv("AGENTWORKS_APP_NAME", "SparkQuill")
	t.Setenv("AGENTWORKS_FAVICON_URL", "/sparkquill-favicon.svg")

	got := runtimeFrontendConfigJS(45778, "http://localhost:45779")
	for _, want := range []string{
		`cdpEnabled: false`,
		`enabledProductSurfaces: ["sparkquill", "sparkquill"]`,
		`defaultProductSurface: "sparkquill"`,
		`appName: "SparkQuill"`,
		`faviconUrl: "/sparkquill-favicon.svg"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in runtime config, got:\n%s", want, got)
		}
	}
}

func TestStaticFrontendDirDefaultsToRelativeStatic(t *testing.T) {
	t.Setenv("STATIC_DIR", "")
	if got := staticFrontendDir(); got != "./static/" {
		t.Fatalf("unset STATIC_DIR must preserve the historical cwd-relative default, got %q", got)
	}
	t.Setenv("STATIC_DIR", "/Applications/SparkQuill.app/Contents/Resources/static")
	if got := staticFrontendDir(); got != "/Applications/SparkQuill.app/Contents/Resources/static" {
		t.Fatalf("STATIC_DIR must override the default, got %q", got)
	}
}
