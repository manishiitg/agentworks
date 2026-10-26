//go:build coding_cli_p0_live

package types

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/mcpagent/llm"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is opt-in real CLI certification, separate from deterministic process
// tests. It requires two dedicated credentials per selected provider and uses
// the existing two-step MCP workflow P0 runner for each account binding.
// Codex also accepts CODING_P0_CODEX_CLI_A_HOME/B_HOME (CODEX_HOME paths)
// instead of keys; it requires two distinct ChatGPT identities and runs A/B/A.
func TestCodingCLIWorkflowP0MultipleAccounts(t *testing.T) {
	matrix := codingCLIP0Providers(t)
	for _, base := range requestedCodingCLIP0Providers(t, matrix) {
		t.Run(base.name, func(t *testing.T) {
			prefix := "CODING_P0_" + strings.ToUpper(strings.ReplaceAll(base.name, "-", "_"))
			a, b := os.Getenv(prefix+"_A_KEY"), os.Getenv(prefix+"_B_KEY")
			loginHomes := map[string]string{"A": os.Getenv(prefix + "_A_HOME"), "B": os.Getenv(prefix + "_B_HOME")}
			useLogins := base.name == "codex-cli" && (loginHomes["A"] != "" || loginHomes["B"] != "")
			if useLogins {
				identities := map[string]bool{}
				for _, home := range loginHomes {
					if home == "" || !filepath.IsAbs(home) {
						t.Fatal("both Codex login homes must be absolute paths")
					}
					data, err := os.ReadFile(filepath.Join(home, "auth.json"))
					if err != nil {
						t.Fatal("each Codex login home must contain saved file credentials")
					}
					var auth struct {
						Tokens struct {
							AccountID string `json:"account_id"`
						} `json:"tokens"`
					}
					if json.Unmarshal(data, &auth) != nil || auth.Tokens.AccountID == "" {
						t.Fatal("each Codex login must be a signed-in ChatGPT account")
					}
					identities[auth.Tokens.AccountID] = true
				}
				if len(identities) != 2 {
					t.Fatal("two distinct signed-in Codex identities are required")
				}
			} else if a == "" || b == "" || a == b {
				t.Fatalf("set distinct dedicated credentials in %s_A_KEY and %s_B_KEY", prefix, prefix)
			}
			fixtureRoot := t.TempDir()
			for _, slot := range []struct{ id, key string }{{"A", a}, {"B", b}, {"A", a}} {
				t.Run(slot.id, func(t *testing.T) {
					provider := base
					provider.connectionID = "test-" + base.name + "-" + slot.id
					home := filepath.Join(fixtureRoot, "account-"+slot.id)
					keys := &llm.ProviderAPIKeys{RuntimeEnvironment: map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_DATA_HOME": filepath.Join(home, ".local", "share"), "XDG_STATE_HOME": filepath.Join(home, ".local", "state"), "CODEX_HOME": filepath.Join(home, ".codex"), "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude")}}
					for _, dir := range keys.RuntimeEnvironment {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
					}
					switch base.name {
					case "claude-code":
						keys.ClaudeCodeOAuthToken = &slot.key
					case "codex-cli":
						keys.CodexCLI = &slot.key
						if useLogins {
							empty := ""
							keys.CodexCLI = &empty
							keys.RuntimeEnvironment["CODEX_HOME"] = loginHomes[slot.id]
						}
					case "cursor-cli":
						keys.CursorCLI = &slot.key
					case "muse-cli":
						keys.MuseCLI = &slot.key
					case "pi-cli":
						keys.PiProviderKeys = map[string]string{"google": slot.key}
					case "agy-cli":
						keys.RuntimeEnvironment["GEMINI_API_KEY"] = slot.key
						keys.RuntimeEnvironment["GOOGLE_API_KEY"] = slot.key
					}
					provider.apiKeys = &llm.ProviderAPIKeys{ResolveConnection: func(ctx context.Context, p llm.Provider, id string) (*llm.ProviderAPIKeys, error) {
						if p != base.provider || id != provider.connectionID {
							return nil, fmt.Errorf("unexpected workflow account binding")
						}
						return keys.Clone(), nil
					}}
					t.Cleanup(func() { provider.cleanup(context.Background()) })
					runCodingCLIWorkflowP0(t, provider)
				})
			}
		})
	}
}
