package agycli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

func TestAgyIsolatedHomesKeepSessionCredentialsSeparate(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", base)
	settingsPath := agySettingsPath(base)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"allow":["view_file"]},"trustedWorkspaces":["/work"],"hooks":{"foreign":{"command":"/bin/false"}},"statusLine":{"type":"command","command":"sh /tmp/gone/statusline.sh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := func(token string) []agyMCPServer {
		return []agyMCPServer{{name: "api-bridge", command: "/bin/true", env: map[string]string{"MCP_API_TOKEN": token}}}
	}
	homeA, releaseA, err := agyIsolatedHome(server("secret-A"))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()
	homeB, releaseB, err := agyIsolatedHome(server("secret-B"))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	if homeA == homeB {
		t.Fatal("concurrent sessions reused a home")
	}
	for home, secrets := range map[string][2]string{homeA: {"secret-A", "secret-B"}, homeB: {"secret-B", "secret-A"}} {
		own, other := secrets[0], secrets[1]
		path := filepath.Join(home, ".gemini", "config", "mcp_config.json")
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private config mode = %v, %v", info, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), own) || strings.Contains(string(raw), other) {
			t.Fatalf("credentials crossed homes: err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli", "conversations")); err != nil {
			t.Fatal(err)
		}
		privateSettings, err := os.ReadFile(filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"))
		if err != nil || strings.Contains(string(privateSettings), "foreign") {
			t.Fatalf("user-level hooks copied into private home: %v, %s", err, privateSettings)
		}
		if strings.Contains(string(privateSettings), "statusline.sh") {
			t.Fatalf("the person's status line command was copied into the private home: %s", privateSettings)
		}
		if _, err := exec.LookPath("agy"); err == nil {
			cmd := exec.CommandContext(context.Background(), "agy", "mcp", "list")
			cmd.Env = append(os.Environ(), "HOME="+home)
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "agentworks-api-bridge-") {
				t.Fatalf("AGY did not read its private config: %v", err)
			}
		}
	}
	global, err := os.ReadFile(settingsPath)
	if err != nil || strings.Contains(string(global), "secret-") {
		t.Fatalf("global settings changed or leaked credentials: %v", err)
	}
}

func TestAgyQuotaFailureIsTyped(t *testing.T) {
	for _, body := range []string{
		`{"status":"FAILED","error":"RESOURCE_EXHAUSTED: quota exceeded"}`,
		`{"status":"429","error":"rate limit exceeded"}`,
		`{"status":"FAILED","error":{"message":"The provider says you have reached your quota for today"}}`,
	} {
		_, err := agyParseExecEnvelope([]byte(body))
		if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
			t.Fatalf("quota error kind = %q, err=%v", llmerrors.KindOf(err), err)
		}
	}
	if got := agyQuotaError("", "I can explain what quota exceeded means"); got != nil {
		t.Fatalf("ordinary explanation classified as quota: %v", got)
	}
	for _, body := range []string{
		`{"status":"FAILED","response":"I can explain what quota exceeded means"}`,
		`{"status":"FAILED","response":"The provider says you have reached your quota for today"}`,
		`{"status":"FAILED","response":"RESOURCE_EXHAUSTED: quota exceeded"}`,
	} {
		_, err := agyParseExecEnvelope([]byte(body))
		if llmerrors.KindOf(err) == llmerrors.KindQuotaExhausted {
			t.Fatalf("assistant text classified as quota: %v", err)
		}
	}
}

func TestAgyQuotaOnlyUsesExplicitStderrAndPaneErrors(t *testing.T) {
	for _, output := range []string{
		"MCP bridge child: HTTP 429 quota exceeded\n",
		"The agent explained what rate limit exceeded means\n",
		"Error: HTTP 429 quota exceeded in a child tool\n",
	} {
		if err := agyQuotaStderrError("", output); err != nil {
			t.Fatalf("child output classified as quota: %v", err)
		}
	}
	if err := agyQuotaStderrError("", "tool output: quota exceeded\nError: RESOURCE_EXHAUSTED: quota exceeded\n"); llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
		t.Fatalf("explicit stderr error kind = %q, err=%v", llmerrors.KindOf(err), err)
	}
	if err := agyQuotaPaneError("", "Error: quota exceeded\n"+strings.Repeat("ordinary status\n", 12)); err != nil {
		t.Fatalf("old pane text classified as quota: %v", err)
	}
	if err := agyQuotaPaneError("", strings.Repeat("ordinary status\n", 12)+"Error: quota exceeded\n"); llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted {
		t.Fatalf("recent TUI error kind = %q, err=%v", llmerrors.KindOf(err), err)
	}
}

func TestAgyPrivateHomeKeyModeLive(t *testing.T) {
	if os.Getenv("AGY_LIVE_PRIVATE_HOME") != "1" {
		t.Skip("set AGY_LIVE_PRIVATE_HOME=1 for the live API probe")
	}
	if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("Gemini API key unavailable")
	}
	if _, err := exec.LookPath("agy"); err != nil {
		t.Skip("agy unavailable")
	}
	home, cleanup, err := agyIsolatedHome(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agy", "--output-format", "json", "-p=Reply with exactly AGY_PRIVATE_HOME_OK", "--model", "gemini-3.7-flash-low")
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("private AGY key-mode probe failed: %v; output tail: %s", err, agyOutputTail(string(out)))
	}
	if !strings.Contains(string(out), "AGY_PRIVATE_HOME_OK") {
		t.Fatalf("private AGY response did not contain the probe marker: %s", agyOutputTail(string(out)))
	}
}
