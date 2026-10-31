package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

// The real startup registry parses embedded Builder schemas as well as the
// public catalog. A malformed schema must fail here before service startup.
func TestExternalToolCatalogInitializes(t *testing.T) {
	// PLAT-856-433: catalog initialization is cached. Each installation must
	// start in a fresh process, just as the owner's local launcher does.
	if os.Getenv("AGENTWORKS_TEST_CATALOG_CHILD") == "" {
		for _, mode := range []string{"local", "server", "multi-user"} {
			t.Run(mode, func(t *testing.T) {
				deployment, multi := "local", "false"
				if mode == "server" {
					deployment = "server"
				}
				if mode == "multi-user" {
					multi = "true"
				}
				t.Setenv("AGENTWORKS_TEST_CATALOG_CHILD", mode)
				t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", deployment)
				t.Setenv("LOCAL_MODE", "true")
				t.Setenv("MULTI_USER_MODE", multi)
				t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
				cmd := exec.Command(os.Args[0], "-test.run=^TestExternalToolCatalogInitializes$", "-test.v")
				cmd.Env = os.Environ()
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("startup catalog: %v\n%s", err, output)
				}
			})
		}
		return
	}
	t.Setenv("DEFAULT_USER_ID", "owner")
	t.Setenv("AGENT_PRODUCTS", "")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","admin":true},{"id":"other","username":"other","admin":true}]}`)

	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) == 0 {
		t.Fatal("startup tool catalog is empty")
	}
	assertStartupComputerUseExposure(t)
	// Being in the schema catalog is insufficient: Crew integrations also filters
	// tools through the resolved product feature policy.
	profile := workproduct.BuiltinAgentProfile()
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	if !containsExactString(profile.ToolPolicy.Enabled, "configure_agent_inbox") {
		t.Fatal("Crew inbox configuration is missing from the live integration tool policy")
	}
}

func assertStartupComputerUseExposure(t *testing.T) {
	t.Helper()
	for _, user := range []string{"owner", "other"} {
		claims := &UserClaims{UserID: user, Username: user}
		request := httptest.NewRequest(http.MethodGet, "/api/external/v1/tools", nil)
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, claims))
		recorder := httptest.NewRecorder()
		(&StreamingAPI{}).handleExternalTools(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("catalog HTTP status=%d: %s", recorder.Code, recorder.Body.String())
		}
		var body struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range body.Tools {
			if tool.Name == "computer_use" {
				found = true
			}
		}
		want := os.Getenv("AGENTWORKS_TEST_CATALOG_CHILD") == "local" && user == "owner"
		if found != want {
			t.Fatalf("computer_use listed=%v want=%v for %s", found, want, user)
		}
	}
}
