package clisandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func writeSessionFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// Each CLI's resumed session moves into the private home, and only that session.
func TestAdoptResumedSessionPerCLI(t *testing.T) {
	root := t.TempDir()
	account := filepath.Join(root, "account")
	private := filepath.Join(root, "private")
	policy := func(provider string, env map[string]string) *llmtypes.CLISecurityPolicy {
		return &llmtypes.CLISecurityPolicy{Provider: provider, PrivateHome: private, CredentialHome: account, CredentialEnv: env}
	}

	writeSessionFile(t, filepath.Join(account, ".local/share/muse/sessions/2026/09/30/mine/session.jsonl"))
	writeSessionFile(t, filepath.Join(account, ".local/share/muse/sessions/.msp-view-v1/mine/journal.bin"))
	writeSessionFile(t, filepath.Join(account, ".local/share/muse/sessions/2026/09/30/other/session.jsonl"))
	adoptResumedSession(policy("muse-cli", nil), []string{"env", "muse", "resume", "mine", "--trust-workspace"})
	for _, rel := range []string{".local/share/muse/sessions/2026/09/30/mine/session.jsonl", ".local/share/muse/sessions/.msp-view-v1/mine/journal.bin"} {
		if !exists(filepath.Join(private, rel)) {
			t.Fatalf("muse: %s not adopted", rel)
		}
	}
	if exists(filepath.Join(private, ".local/share/muse/sessions/2026/09/30/other")) {
		t.Fatal("muse: another session was copied")
	}

	// Codex keeps sessions under the account's own CODEX_HOME when it sets one.
	codexHome := filepath.Join(root, "codex-home")
	writeSessionFile(t, filepath.Join(codexHome, "sessions/2026/09/30/rollout-2026-09-30T10-00-00-abc123.jsonl"))
	writeSessionFile(t, filepath.Join(codexHome, "sessions/2026/09/30/rollout-2026-09-30T10-00-00-zzz999.jsonl"))
	adoptResumedSession(policy("codex-cli", map[string]string{"CODEX_HOME": codexHome}), []string{"codex", "exec", "resume", "abc123", "--json"})
	if !exists(filepath.Join(private, ".codex/sessions/2026/09/30/rollout-2026-09-30T10-00-00-abc123.jsonl")) {
		t.Fatal("codex: session not adopted")
	}
	if exists(filepath.Join(private, ".codex/sessions/2026/09/30/rollout-2026-09-30T10-00-00-zzz999.jsonl")) {
		t.Fatal("codex: another session was copied")
	}

	// Legacy ~/.cursor/chats goes where the private Cursor reads (.config/cursor/chats).
	writeSessionFile(t, filepath.Join(account, ".cursor/chats/hash1/chat-1/store.db"))
	writeSessionFile(t, filepath.Join(account, ".cursor/chats/hash1/chat-2/store.db"))
	adoptResumedSession(policy("cursor-cli", nil), []string{"cursor-agent", "--resume", "chat-1"})
	if !exists(filepath.Join(private, ".config/cursor/chats/hash1/chat-1/store.db")) || exists(filepath.Join(private, ".config/cursor/chats/hash1/chat-2")) {
		t.Fatal("cursor: only chat-1 should be adopted, into .config/cursor/chats")
	}
	// The account's XDG config folder (the server sets one on RTS) is where its chats really are.
	xdg := filepath.Join(root, "xdg-config")
	writeSessionFile(t, filepath.Join(xdg, "cursor/chats/hash2/chat-3/store.db"))
	adoptResumedSession(policy("cursor-cli", map[string]string{"XDG_CONFIG_HOME": xdg}), []string{"cursor-agent", "--resume", "chat-3"})
	if !exists(filepath.Join(private, ".config/cursor/chats/hash2/chat-3/store.db")) {
		t.Fatal("cursor: a chat under the account's XDG config was not adopted")
	}
}

func TestAdoptResumedSessionKeepsAnExistingPrivateCopy(t *testing.T) {
	root := t.TempDir()
	account, private := filepath.Join(root, "account"), filepath.Join(root, "private")
	writeSessionFile(t, filepath.Join(account, ".cursor/chats/h/c/store.db"))
	if err := os.MkdirAll(filepath.Join(private, ".config/cursor/chats/h/c"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, ".config/cursor/chats/h/c/mine.txt"), []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	adoptResumedSession(&llmtypes.CLISecurityPolicy{Provider: "cursor-cli", PrivateHome: private, CredentialHome: account}, []string{"--resume", "c"})
	if exists(filepath.Join(private, ".config/cursor/chats/h/c/store.db")) {
		t.Fatal("an existing private session was overwritten")
	}
}

// The interactive adapter puts the resume ID after its profile, model and
// config flags. Migration must parse that real argv, not the first option.
func TestAdoptCodexInteractiveResumeWithOptionsBeforeID(t *testing.T) {
	root := t.TempDir()
	account, private := filepath.Join(root, "account"), filepath.Join(root, "private")
	id := "01a0ced0-646a-7550-87d2-7d9a0f17da15"
	rel := filepath.Join(".codex", "sessions", "2026", "09", "23", "rollout-2026-09-23T17-09-18-"+id+".jsonl")
	writeSessionFile(t, filepath.Join(account, rel))
	other := filepath.Join(".codex", "sessions", "2026", "09", "23", "rollout-2026-09-23T17-09-18-other.jsonl")
	writeSessionFile(t, filepath.Join(account, other))
	policy := &llmtypes.CLISecurityPolicy{Provider: "codex-cli", PrivateHome: private, CredentialHome: account}
	args := []string{"codex", "resume", "--profile", "agentworks-test", "--no-alt-screen", "--model", "gpt-6.1-sol", "--sandbox", "danger-full-access", "--ask-for-approval", "never", "--disable", "shell_tool", "-c", `model_reasoning_effort="medium"`, id}
	adoptResumedSession(policy, args)
	if !exists(filepath.Join(private, rel)) {
		t.Fatal("interactive Codex resume did not adopt its session")
	}
	if exists(filepath.Join(private, other)) {
		t.Fatal("another Codex session was copied")
	}
}

func TestCodexResumeSessionIDSkipsOptionValues(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"codex", "resume", "--profile", "profile-id", "--model=model-id", "thread-id"}, "thread-id"},
		{[]string{"codex", "exec", "resume", "thread-id", "--json", "prompt"}, "thread-id"},
		{[]string{"codex", "resume", "--profile", "profile-id"}, ""},
		{[]string{"codex", "resume", "--last"}, ""},
	} {
		if got := resumeSessionID("codex-cli", test.args); got != test.want {
			t.Fatalf("%v: got %q, want %q", test.args, got, test.want)
		}
	}
}
