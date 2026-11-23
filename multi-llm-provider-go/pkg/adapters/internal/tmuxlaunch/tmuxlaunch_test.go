package tmuxlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAcquireQueuesConcurrentStarts(t *testing.T) {
	t.Setenv(EnvStartConcurrency, "1")

	releaseFirst, err := Acquire(context.Background(), "test", "first")
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	defer releaseFirst()

	acquiredSecond := make(chan struct{})
	go func() {
		releaseSecond, err := Acquire(context.Background(), "test", "second")
		if err != nil {
			return
		}
		defer releaseSecond()
		close(acquiredSecond)
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second startup acquired before first released")
	case <-time.After(50 * time.Millisecond):
	}

	releaseFirst()

	select {
	case <-acquiredSecond:
	case <-time.After(time.Second):
		t.Fatal("second startup did not acquire after first released")
	}
}

func TestAcquireHonorsCancellationWhileQueued(t *testing.T) {
	t.Setenv(EnvStartConcurrency, "1")

	releaseFirst, err := Acquire(context.Background(), "test", "first")
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}
	defer releaseFirst()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if releaseSecond, err := Acquire(ctx, "test", "second"); err == nil {
		releaseSecond()
		t.Fatal("queued startup acquired despite canceled context")
	}
}

func TestPromptWaitUsesGlobalDefault(t *testing.T) {
	t.Setenv(EnvPromptWaitSeconds, "")
	t.Setenv("TEST_PROVIDER_PROMPT_WAIT_SECONDS", "")

	if got := PromptWait("TEST_PROVIDER_PROMPT_WAIT_SECONDS"); got != defaultPromptWait {
		t.Fatalf("PromptWait default = %v, want %v", got, defaultPromptWait)
	}
}

func TestPromptWaitUsesGlobalOverride(t *testing.T) {
	t.Setenv(EnvPromptWaitSeconds, "3")
	t.Setenv("TEST_PROVIDER_PROMPT_WAIT_SECONDS", "")

	if got := PromptWait("TEST_PROVIDER_PROMPT_WAIT_SECONDS"); got != 3*time.Second {
		t.Fatalf("PromptWait global override = %v, want 3s", got)
	}
}

func TestPromptWaitProviderOverrideWins(t *testing.T) {
	t.Setenv(EnvPromptWaitSeconds, "3")
	t.Setenv("TEST_PROVIDER_PROMPT_WAIT_SECONDS", "7")

	if got := PromptWait("TEST_PROVIDER_PROMPT_WAIT_SECONDS"); got != 7*time.Second {
		t.Fatalf("PromptWait provider override = %v, want 7s", got)
	}
}

func TestPromptWaitIgnoresInvalidProviderOverride(t *testing.T) {
	t.Setenv(EnvPromptWaitSeconds, "3")
	t.Setenv("TEST_PROVIDER_PROMPT_WAIT_SECONDS", "bad")

	if got := PromptWait("TEST_PROVIDER_PROMPT_WAIT_SECONDS"); got != 3*time.Second {
		t.Fatalf("PromptWait invalid provider override = %v, want global 3s", got)
	}
}

func TestWithHistoryLimitRunsBeforeNewSession(t *testing.T) {
	newSession := []string{"new-session", "-d", "-s", "test-session", "agent"}
	got := WithHistoryLimit(newSession, "20000")
	want := []string{
		"set-option", "-g", "history-limit", "20000", ";",
		"new-session", "-d", "-s", "test-session", "agent",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WithHistoryLimit() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(newSession, []string{"new-session", "-d", "-s", "test-session", "agent"}) {
		t.Fatalf("WithHistoryLimit mutated input: %#v", newSession)
	}
}

// PLAT-663: a tmux server started by a launch from the service (full environment) must not keep the
// service's own tokens in its global environment, which every pane inherits and `show-environment -g`
// prints. Real tmux on a private socket.
func TestLaunchLeavesNoServiceTokensInTmuxServer(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("GLOBAL_SECRET_PLAT663", "x")
	t.Setenv("SUPABASE_SERVICE_ROLE_KEY", "x")
	t.Setenv("PLAT663_ORDINARY", "kept")
	dir, err := os.MkdirTemp("/tmp", "p663") // a unix socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	args := WithHistoryLimit([]string{"new-session", "-d", "-s", "plat663", "sleep 30"}, "1000")
	if out, err := exec.Command(tmux, append([]string{"-f", "/dev/null", "-S", sock}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("start: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(tmux, "-S", sock, "kill-server").Run() })
	out, err := exec.Command(tmux, "-S", sock, "show-environment", "-g").Output()
	if err != nil {
		t.Fatalf("show-environment: %v", err)
	}
	env := "\n" + string(out)
	for _, name := range []string{"GLOBAL_SECRET_PLAT663", "SUPABASE_SERVICE_ROLE_KEY"} {
		if strings.Contains(env, "\n"+name+"=") {
			t.Errorf("tmux global environment still has %s", name)
		}
	}
	if !strings.Contains(env, "\nPLAT663_ORDINARY=kept") {
		t.Errorf("ordinary variables must stay in the tmux environment")
	}
}
