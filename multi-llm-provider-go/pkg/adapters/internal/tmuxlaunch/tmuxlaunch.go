package tmuxlaunch

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const EnvStartConcurrency = "CODING_SDK_TMUX_START_CONCURRENCY"
const EnvPromptWaitSeconds = "CODING_SDK_TMUX_PROMPT_WAIT_SECONDS"
const EnvRetentionSeconds = "CODING_SDK_TMUX_RETENTION_SECONDS"

const defaultStartConcurrency = 1
const defaultPromptWait = 300 * time.Second

var startGate = struct {
	sync.Mutex
	ch   chan struct{}
	size int
}{}

func Acquire(ctx context.Context, provider, sessionName string) (func(), error) {
	size := configuredStartConcurrency()
	startGate.Lock()
	if startGate.ch == nil || startGate.size != size {
		startGate.ch = make(chan struct{}, size)
		startGate.size = size
	}
	ch := startGate.ch
	startGate.Unlock()

	select {
	case ch <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-ch })
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("timed out waiting for tmux startup slot for %s session %q: %w", provider, sessionName, ctx.Err())
	}
}

func configuredStartConcurrency() int {
	value := strings.TrimSpace(os.Getenv(EnvStartConcurrency))
	if value == "" {
		return defaultStartConcurrency
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultStartConcurrency
	}
	return parsed
}

func PromptWait(providerEnvKey string) time.Duration {
	if parsed, ok := durationFromEnv(providerEnvKey); ok {
		return parsed
	}
	if parsed, ok := durationFromEnv(EnvPromptWaitSeconds); ok {
		return parsed
	}
	return defaultPromptWait
}

func Retention(fallback time.Duration) time.Duration {
	if parsed, ok := durationFromEnv(EnvRetentionSeconds); ok {
		return parsed
	}
	return fallback
}

// WithHistoryLimit configures tmux's global default immediately before the
// new-session command in the same tmux invocation. tmux copies history-limit
// into a pane when that pane is created; setting the session option after
// new-session is too late for the session's first pane.
func WithHistoryLimit(newSessionArgs []string, historyLimit string) []string {
	historyLimit = strings.TrimSpace(historyLimit)
	if historyLimit == "" {
		return append([]string(nil), newSessionArgs...)
	}
	args := []string{"set-option", "-g", "history-limit", historyLimit, ";"}
	return WithCleanServerEnv(append(args, newSessionArgs...))
}

// WithCleanServerEnv prefixes a tmux command list (normally ending in new-session) with
// `set-environment -g -u NAME ;` for every service-only variable in this process's environment
// (llmtypes.IsServiceOnlyEnvKey). tmux's server keeps the environment of the client that started it as
// its global environment, hands it to every new pane and prints it to anything that reaches the socket
// (`tmux show-environment -g`). The adapters run tmux with the host service's full environment, so
// whichever launch started the server left the service's tokens in it (PLAT-663). In one client call the
// unsets run inside the server before new-session, so the new pane and every later one start without
// them, whether this call started the server or it was already running (an older dirty server is
// cleaned on the next launch). Only names travel on the command line, never values.
func WithCleanServerEnv(args []string) []string {
	names := llmtypes.ServiceOnlyEnvNames(os.Environ())
	if len(names) == 0 {
		return append([]string(nil), args...)
	}
	out := make([]string, 0, len(names)*5+len(args))
	for _, name := range names {
		out = append(out, "set-environment", "-g", "-u", name, ";")
	}
	return append(out, args...)
}

// CleanClientEnv is this process's environment without the service-only variables, for an exec.Cmd that
// runs `tmux new-session` directly. A tmux server started by that call keeps none of them (PLAT-663). It is
// used where the command list must stay a bare new-session -- the slot front-end (slottmux) routes a launch
// to the slot's own tmux only when new-session is the first command -- so WithCleanServerEnv's prefix
// cannot be used there.
func CleanClientEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if !llmtypes.IsServiceOnlyEnvKey(key) {
			out = append(out, entry)
		}
	}
	return out
}

func durationFromEnv(key string) (time.Duration, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, false
	}
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return time.Duration(parsed) * time.Second, true
}
