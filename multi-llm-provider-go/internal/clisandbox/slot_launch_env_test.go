package clisandbox

import (
	"reflect"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-86: a slot launcher held the platform's WORKSPACE_API_TOKEN and other programs' company logins for its
// whole run, readable by any process of the slot's uid. A slot launch keeps only its own login.
func TestSlotLaunchEnvKeepsOnlyOwnLogin(t *testing.T) {
	env := []string{"PATH=/usr/bin", "WORKSPACE_API_TOKEN=x", "MCP_BRIDGE_TOKEN_SECRET=x", "GLOBAL_SECRET_DB=x",
		"CLAUDE_CODE_OAUTH_TOKEN=x", "CODEX_API_KEY=x", "OPENAI_API_KEY=x", "SOME_VENDOR_API_KEY=x", "SECRET_GITHUB=x",
		"GOG_KEYRING_PASSWORD=x", "SUPABASE_ANON_KEY=x", "CAPLAYER_SERVICE_TOKEN_FILE=x", "ADMIN_USERS=x", "AUTH_ALLOWED_EMAILS=x"}
	got := slotLaunchEnv(&llmtypes.CLISecurityPolicy{Provider: "codex-cli"}, env)
	want := []string{"PATH=/usr/bin", "CODEX_API_KEY=x", "OPENAI_API_KEY=x", "SECRET_GITHUB=x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slot launch env = %v, want %v", got, want)
	}
}
