package server

import (
	"bufio"
	"context"
	"log"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-663: the tmux server that hosts the coding CLIs keeps the environment of whichever client started it as
// its global environment, gives it to every new pane and prints it to anything that reaches its socket
// (`tmux show-environment -g`). The launches now start and clean it without the service-only variables
// (multi-llm-provider-go tmuxlaunch), but a server started before that fix survives service restarts with the
// service's tokens still in it. This check finds them by NAME (values are never read into a log or a
// message), removes them, and confirms none is left. It runs at startup and then periodically, so a server
// started by any other path is caught too.

const tmuxServerEnvCheckInterval = 15 * time.Minute

// tmuxServerEnvRunner runs one tmux command against the platform's own server and returns its standard output.
type tmuxServerEnvRunner func(ctx context.Context, args ...string) (string, error)

func defaultTmuxServerEnvRunner(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	// The command itself carries no service variable, so it can never be the client that starts a dirty server.
	cmd.Env = append(minimalChildEnv(), passthroughChildEnv("TMUX_TMPDIR")...)
	out, err := cmd.Output()
	return string(out), err
}

// tmuxServerServiceEnvNames lists the service-only variable names in tmux's global environment. An error means
// no server is running (nothing to check: the next launch starts a clean one) or tmux is missing.
func tmuxServerServiceEnvNames(ctx context.Context, run tmuxServerEnvRunner) ([]string, error) {
	out, err := run(ctx, "show-environment", "-g")
	if err != nil {
		return nil, err
	}
	var names []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "-") { // already marked removed
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		if ok && llmtypes.IsServiceOnlyEnvKey(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// scrubTmuxServerEnvironment removes the service-only variables from tmux's global environment and reports the
// names it found and the names still present afterwards (both empty on a clean server).
func scrubTmuxServerEnvironment(ctx context.Context, run tmuxServerEnvRunner) (found, left []string, err error) {
	found, err = tmuxServerServiceEnvNames(ctx, run)
	if err != nil || len(found) == 0 {
		return nil, nil, err
	}
	args := make([]string, 0, len(found)*5)
	for _, name := range found {
		args = append(args, "set-environment", "-g", "-u", name, ";")
	}
	if _, err := run(ctx, args[:len(args)-1]...); err != nil {
		return found, found, err
	}
	left, err = tmuxServerServiceEnvNames(ctx, run)
	return found, left, err
}

func checkTmuxServerEnvironmentOnce(run tmuxServerEnvRunner) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found, left, err := scrubTmuxServerEnvironment(ctx, run)
	switch {
	case err != nil && len(found) == 0:
		return // no tmux server yet
	case len(left) > 0:
		log.Printf("[TMUX_ENV] ERROR: tmux server global environment still holds service-only variables: %s (PLAT-663)", strings.Join(left, ", "))
	case len(found) > 0:
		log.Printf("[TMUX_ENV] removed service-only variables from the tmux server global environment: %s (PLAT-663)", strings.Join(found, ", "))
	}
}

// startTmuxServerEnvCheck runs the check now and then every tmuxServerEnvCheckInterval.
func startTmuxServerEnvCheck() {
	checkTmuxServerEnvironmentOnce(defaultTmuxServerEnvRunner)
	go func() {
		ticker := time.NewTicker(tmuxServerEnvCheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			checkTmuxServerEnvironmentOnce(defaultTmuxServerEnvRunner)
		}
	}()
}
