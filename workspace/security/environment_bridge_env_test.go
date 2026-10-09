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

// A user's shell must not see who else may sign in, other people's account ids, the gateway's settings or the
// service's SSH agent (server B 2026-10-06: they were all in a Code terminal's env).
func TestNativeEnvironmentDropsServerIdentityAndAccountData(t *testing.T) {
	t.Setenv("NATIVE_WORKSPACE", "true")
	for _, key := range []string{"AUTH_ALLOWED_EMAILS", "AUTH_PROVIDERS", "GATEWAY_DISABLE_PASSWORD_GATE", "GATEWAY_USER_ID", "AGENTWORKS_SLOT_CLI_USERS", "SSH_AUTH_SOCK"} {
		t.Setenv(key, "leak")
	}
	for _, entry := range BuildSafeEnvironment() {
		for _, key := range []string{"AUTH_ALLOWED_EMAILS=", "AUTH_PROVIDERS=", "GATEWAY_DISABLE_PASSWORD_GATE=", "GATEWAY_USER_ID=", "AGENTWORKS_SLOT_CLI_USERS=", "SSH_AUTH_SOCK="} {
			if strings.HasPrefix(entry, key) {
				t.Fatalf("leaked %s", entry)
			}
		}
	}
}

// A person's slot shell gets an allowlist of the service environment: tool and platform settings pass, anything
// else (a server setting added tomorrow, other people's ids) does not (PLAT-622).
func TestSlotShellEnvKeepsToolsAndDropsTheRest(t *testing.T) {
	got := strings.Join(SlotShellEnv([]string{"PATH=/bin", "HOME=/h", "AGENT_API_URL=x", "PIP_CACHE_DIR=x", "TERM=xterm", "NEW_SERVER_SETTING=x", "SUPABASE_URL=x", "AGENTWORKS_SLOT_CLI_USERS=a,b", "FRONTEND_DIR=/srv"}), " ")
	if got != "PATH=/bin HOME=/h AGENT_API_URL=x PIP_CACHE_DIR=x TERM=xterm" {
		t.Fatalf("slot shell env = %q", got)
	}
}
