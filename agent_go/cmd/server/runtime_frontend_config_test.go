package server

import (
	"strings"
	"testing"
)

func TestRuntimeFrontendManagedWorkspaceUsesAgentProxy(t *testing.T) {
	t.Setenv("WORKSPACE_API_TOKEN", "private-workspace-test-token")
	got := runtimeFrontendConfigJS(45678, "http://localhost:45679")
	if !strings.Contains(got, `workspaceApiBaseUrl: "http://localhost:45678/api/wp"`) {
		t.Fatalf("managed workspace requests must use the authenticated agent proxy: %s", got)
	}
	for _, privateValue := range []string{"private-workspace-test-token", "http://localhost:45679"} {
		if strings.Contains(got, privateValue) {
			t.Fatalf("runtime config exposes private workspace connection details: %s", got)
		}
	}
}

func TestRuntimeFrontendLocalProfileCannotRestoreServerProducts(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,relays,knowledgebase,mcp-gateway,llm-gateway,work,code")
	t.Setenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE", "mcp-gateway")
	got := runtimeFrontendConfigJS(45678, "http://localhost:45679")
	for _, want := range []string{`deploymentMode: "local"`, `enabledProductSurfaces: ["agentworks", "work"]`, `defaultProductSurface: "agentworks"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	for _, configured := range []string{"", "code", "relays,mcp-gateway"} {
		t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", configured)
		if got := runtimeFrontendConfigJS(45678, ""); !strings.Contains(got, `enabledProductSurfaces: ["agentworks", "work", "bullpen"]`) {
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
	for _, want := range []string{`deploymentMode: "local"`, `localServerProducts: true`, `gatewayUrl: "http://127.0.0.1:18745"`, `"relays"`, `"knowledgebase"`, `"mcp-gateway"`, `"code"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}

func TestRuntimeFrontendPrivateProductRequiresLocalOptInAndComposedBinary(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE", "llm-gateway")
	for _, tc := range []struct {
		name, optIn, binary, surfaces string
		wantLLM                       bool
	}{
		{"ordinary local", "0", "/composed-host", "", false},
		{"public opt-in", "1", "", "", false},
		{"private opt-in", "1", "/composed-host", "", true},
		{"explicit exclusion", "1", "/composed-host", "agentworks,work", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", tc.optIn)
			t.Setenv("AGENTWORKS_GATEWAY_BINARY", tc.binary)
			t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", tc.surfaces)
			got := runtimeFrontendConfigJS(45678, "")
			if hasLLM := strings.Contains(got, `"llm-gateway"`); hasLLM != tc.wantLLM {
				t.Fatalf("LLM visibility = %v, want %v: %s", hasLLM, tc.wantLLM, got)
			}
			if tc.wantLLM && !strings.Contains(got, `defaultProductSurface: "llm-gateway"`) {
				t.Fatalf("installed LLM product cannot be the default: %s", got)
			}
		})
	}
}

func TestStaticFrontendDirDefaultsToRelativeStatic(t *testing.T) {
	t.Setenv("STATIC_DIR", "")
	if got := staticFrontendDir(); got != "./static/" {
		t.Fatalf("unset STATIC_DIR must preserve the historical cwd-relative default, got %q", got)
	}
	t.Setenv("STATIC_DIR", "/Applications/ExampleApp.app/Contents/Resources/static")
	if got := staticFrontendDir(); got != "/Applications/ExampleApp.app/Contents/Resources/static" {
		t.Fatalf("STATIC_DIR must override the default, got %q", got)
	}
}
