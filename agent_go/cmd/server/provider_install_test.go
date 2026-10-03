package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderInstallUsesRegistryOnLocalMacOnly(t *testing.T) {
	original := providerInstallHostOS
	t.Cleanup(func() { providerInstallHostOS = original })
	t.Setenv("MULTI_USER_MODE", "false")
	providerInstallHostOS = "darwin"
	spec, err := providerSetupCommandFor("codex-cli", "install")
	if err != nil || spec.command != "/bin/bash" || len(spec.args) != 4 || spec.args[3] != providerInstallCommand("codex-cli") {
		t.Fatalf("registry installer = %+v, %v", spec, err)
	}
	for _, provider := range []string{"openai", "unknown; echo unsafe"} {
		if providerInstallAvailable(provider) {
			t.Fatalf("unregistered installer allowed: %s", provider)
		}
	}
	providerInstallHostOS = "linux"
	if _, err := providerSetupCommandFor("codex-cli", "install"); err == nil {
		t.Fatal("server allowed installation")
	}
	providerInstallHostOS = "darwin"
	t.Setenv("MULTI_USER_MODE", "true")
	if _, err := providerSetupCommandFor("codex-cli", "install"); err == nil {
		t.Fatal("multi-user host allowed installation")
	}
}

func TestProviderInstallRejectsServerAndAccountScopedRequests(t *testing.T) {
	original := providerInstallHostOS
	t.Cleanup(func() { providerInstallHostOS = original })
	t.Setenv("MULTI_USER_MODE", "false")
	for _, test := range []struct{ host, body string }{
		{"linux", `{"provider":"codex-cli","action":"install"}`},
		{"darwin", `{"provider":"codex-cli","action":"install","connection_id":"private-account"}`},
		{"darwin", `{"provider":"codex-cli","action":"install","workspace_path":"Chats/SparkQuill"}`},
	} {
		providerInstallHostOS = test.host
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/provider-setup", strings.NewReader(test.body))
		(&StreamingAPI{}).handleStartProviderSetup(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d", test.body, recorder.Code)
		}
	}
}
