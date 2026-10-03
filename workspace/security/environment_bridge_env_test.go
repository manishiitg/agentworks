package security

import (
	"strings"
	"testing"
)

// Agent shells never inherit the server's bridge credentials: with the
// signing secret a shell could mint a tool-call token for any session.
func TestNativeEnvironmentDropsBridgeCredentials(t *testing.T) {
	for _, name := range []string{"MCP_API_TOKEN", "MCP_SERVER_API_TOKEN", "MCP_BRIDGE_TOKEN_SECRET", "WORKSPACE_API_TOKEN"} {
		t.Setenv(name, "secret-value")
	}
	for _, kv := range buildNativeEnvironment() {
		for _, name := range []string{"MCP_API_TOKEN=", "MCP_SERVER_API_TOKEN=", "MCP_BRIDGE_TOKEN_SECRET=", "WORKSPACE_API_TOKEN="} {
			if strings.HasPrefix(kv, name) {
				t.Errorf("%s leaked into the agent shell environment", strings.TrimSuffix(name, "="))
			}
		}
	}
}

// Agent shells never inherit AUTH_SECRET either: it signs session JWTs and
// derives stored-secret encryption keys.
func TestNativeEnvironmentDropsAuthSecret(t *testing.T) {
	t.Setenv("AUTH_SECRET", "fake-auth-secret")
	for _, kv := range buildNativeEnvironment() {
		if strings.HasPrefix(kv, "AUTH_SECRET=") {
			t.Error("AUTH_SECRET leaked into the agent shell environment")
		}
	}
}

// Agent shells never inherit the app's login password, the legacy user list or the deployment's global
// secrets, while ordinary tool configuration still passes through.
func TestNativeEnvironmentDropsLoginAndGlobalSecrets(t *testing.T) {
	t.Setenv("ACCESS_PASSWORD", "fake-access-password")
	t.Setenv("AUTH_USERS", "a@example.com:fake")
	t.Setenv("GLOBAL_SECRET_STRIPE_KEY", "fake-global")
	t.Setenv("MY_TOOL_CONFIG", "kept")
	kept := false
	for _, kv := range buildNativeEnvironment() {
		for _, name := range []string{"ACCESS_PASSWORD=", "AUTH_USERS=", "GLOBAL_SECRET_"} {
			if strings.HasPrefix(kv, name) {
				t.Fatalf("shell environment carries %s", name)
			}
		}
		if kv == "MY_TOOL_CONFIG=kept" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("ordinary configuration was dropped")
	}
}

func TestNativeEnvironmentDoesNotInheritVaultAuthority(t *testing.T) {
	for _, name := range []string{"GLOBAL_SECRET_SHARED_KEY", "CAPLAYER_SERVICE_TOKEN", "CAPLAYER_SERVICE_TOKEN_FILE", "GATEWAY_HUMAN_TOKEN"} {
		t.Setenv(name, "dummy-authority")
	}
	for _, kv := range buildNativeEnvironment() {
		for _, prefix := range []string{"GLOBAL_SECRET_", "CAPLAYER_SERVICE_", "GATEWAY_HUMAN_TOKEN"} {
			if strings.HasPrefix(kv, prefix) {
				t.Fatalf("Vault authority inherited through %s", prefix)
			}
		}
	}
}
