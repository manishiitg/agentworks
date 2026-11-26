package llmtypes

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestProviderAccountEnvironmentConcurrentProcesses(t *testing.T) {
	t.Setenv("HOME", "/global/home")
	t.Setenv("CODEX_HOME", "/global/codex")
	type result struct {
		want, got string
		err       error
	}
	results := make(chan result, 2)
	for _, account := range []string{"A", "B"} {
		account := account
		go func() {
			root := "/private/" + account
			source := map[string]string{"HOME": root, "CODEX_HOME": root + "/.codex", "PATH": "/attacker/path"}
			opts := &CallOptions{}
			WithProviderAccountEnvironment(source)(opts)
			source["HOME"] = "/changed"
			cmd := exec.CommandContext(context.Background(), "/bin/sh", "-c", `printf '%s|%s' "$HOME" "$CODEX_HOME"`)
			cmd.Env = MergeCodingAgentSecretEnvironment(os.Environ(), opts)
			output, err := cmd.Output()
			results <- result{root + "|" + root + "/.codex", string(output), err}
		}()
	}
	for range 2 {
		r := <-results
		if r.err != nil || r.got != r.want {
			t.Fatalf("account child environment mismatch: %v", r.err)
		}
	}
	if os.Getenv("HOME") != "/global/home" || os.Getenv("CODEX_HOME") != "/global/codex" {
		t.Fatal("process-wide environment changed")
	}
}

func TestProviderAccountScopeFingerprintAndTmuxPlan(t *testing.T) {
	a, b := &CallOptions{}, &CallOptions{}
	WithProviderAccountEnvironment(map[string]string{"HOME": "/A"})(a)
	WithProviderAccountEnvironment(map[string]string{"HOME": "/B"})(b)
	if CodingAgentScopeFingerprint(a) == CodingAgentScopeFingerprint(b) {
		t.Fatal("different accounts share retained-session fingerprint")
	}
	env, _ := ScopedCodingAgentEnvironmentPlan(nil, nil, b)
	if strings.Join(env, "\n") != "HOME=/B" {
		t.Fatal("tmux launch lost account home")
	}
	got := ProviderAccountEnvironment(b)
	got["HOME"] = "/mutated"
	if ProviderAccountEnvironment(b)["HOME"] != "/B" {
		t.Fatal("runtime environment is mutable through returned map")
	}
}

func TestProviderAccountCredentialsOverrideAndLoginCannotInheritKey(t *testing.T) {
	opts := &CallOptions{}
	WithProviderAccountEnvironment(map[string]string{"HOME": "/B"})(opts)
	base := []string{"HOME=/global", "META_API_KEY=global-key", "OPENAI_API_KEY=global-openai", "PATH=/bin", "CURSOR_AUTH_TOKEN=global-cursor", "GEMINI_API_KEY=global-gemini", "GOOGLE_API_KEY=global-google"}
	login := strings.Join(MergeCodingAgentSecretEnvironment(base, opts), "\n")
	if strings.Contains(login, "global-key") || strings.Contains(login, "global-openai") || strings.Contains(login, "global-cursor") || strings.Contains(login, "global-gemini") || strings.Contains(login, "global-google") {
		t.Fatal("file-login account inherited server credential")
	}
	WithProviderAccountCredentials(map[string]string{"META_API_KEY": "private-B"})(opts)
	keyed := strings.Join(MergeCodingAgentSecretEnvironment(base, opts), "\n")
	if !strings.Contains(keyed, "META_API_KEY=private-B") || strings.Contains(keyed, "global-key") {
		t.Fatal("selected key did not win")
	}
	exports, unset := ScopedCodingAgentEnvironmentPlan(base, nil, opts)
	if !strings.Contains(strings.Join(exports, "\n"), "META_API_KEY=private-B") || !slices.Contains(unset, "META_API_KEY") {
		t.Fatal("tmux launch does not scrub ambient identity")
	}
}

// On a Mac (Seatbelt, no Landlock runner) an account's own home is its only login source: an empty CredentialHome
// made the sandbox link the person's own ~/.codex/auth.json into a company account's home (PLAT-856-441).
func TestSeatbeltAccountHomeIsItsLoginSource(t *testing.T) {
	opts := &CallOptions{CLISecurity: &CLISecurityPolicy{Provider: "codex-cli"}}
	WithProviderAccountEnvironment(map[string]string{"HOME": "/accounts/a/home", "CODEX_HOME": "/accounts/a/home/.codex"})(opts)
	if opts.CLISecurity.PrivateHome != "/accounts/a/home" || opts.CLISecurity.CredentialHome != "/accounts/a/home" || opts.CLISecurity.CredentialEnv["CODEX_HOME"] != "/accounts/a/home/.codex" {
		t.Fatalf("policy = %+v", opts.CLISecurity)
	}
}
