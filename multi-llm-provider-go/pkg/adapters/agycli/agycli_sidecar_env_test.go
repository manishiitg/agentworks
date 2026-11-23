package agycli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgySidecarScrubsTmuxServerCredentials(t *testing.T) {
	dir := t.TempDir()
	tmuxArgsPath := filepath.Join(dir, "tmux-args")
	tmuxLogPath := filepath.Join(dir, "tmux-log")
	childEnvPath := filepath.Join(dir, "child-env")
	for name, body := range map[string]string{
		"tmux": "#!/bin/sh\ncase \"$1\" in\nnew-session) printf '%s\\n' \"$@\" > \"$AGY_TEST_TMUX_ARGS\"; for last; do :; done; /bin/sh -c \"$last\" > \"$AGY_TEST_TMUX_LOG\" 2>&1;;\ncapture-pane) printf '? for shortcuts\\n> \\n';;\nhas-session|kill-session) exit 0;;\nesac\n",
		"agy":  "#!/bin/sh\nenv > \"$AGY_TEST_CHILD_ENV\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(shelllaunch.EnvShellMode, "direct")
	t.Setenv("AGY_TEST_TMUX_ARGS", tmuxArgsPath)
	t.Setenv("AGY_TEST_TMUX_LOG", tmuxLogPath)
	t.Setenv("AGY_TEST_CHILD_ENV", childEnvPath)
	t.Setenv("SECRET_AMBIENT", "must-not-reach-child")
	t.Setenv("MCP_API_TOKEN", "must-not-reach-child")
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCodingAgentSecretEnvironment(map[string]string{"SECRET_ALLOWED": "declared-value"})(opts)
	privateHome := filepath.Join(dir, "private-home")
	if err := os.Mkdir(privateHome, 0o700); err != nil {
		t.Fatal(err)
	}
	session, err := bootAgyInteractiveSession(context.Background(), "env-test", dir, DefaultModelID, "", privateHome, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(session.ownerSessionID, "test done") })
	args, err := os.ReadFile(tmuxArgsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "declared-value") || strings.Contains(string(args), "must-not-reach-child") {
		t.Fatal("sidecar credentials appeared in tmux argv")
	}
	env, err := os.ReadFile(childEnvPath)
	if err != nil {
		log, _ := os.ReadFile(tmuxLogPath)
		t.Fatalf("read child env: %v; tmux args: %q; log: %q", err, string(args), string(log))
	}
	got := string(env)
	if !strings.Contains(got, "SECRET_ALLOWED=declared-value") || !strings.Contains(got, "HOME="+privateHome) {
		t.Fatal("sidecar did not receive declared scope and private home")
	}
	if strings.Contains(got, "SECRET_AMBIENT=") || strings.Contains(got, "MCP_API_TOKEN=") {
		t.Fatal("sidecar inherited tmux server credentials outside its scope")
	}
}
