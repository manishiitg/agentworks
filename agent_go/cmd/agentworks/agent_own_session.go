package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// A person can ask an agent to connect its own chat. Never turn a model tool
// call into the terminal picker, a plugin installer or a technical override.
// Process metadata and host-supplied session hints are routing fences, not
// isolation against another process with the same OS user's credentials.
func ownAgentSession(ctx context.Context, root string, o *options) (liveAgentSession, bool, error) {
	// Cursor's Linux launcher appears as MainThread, so its native executor
	// hint selects the dedicated kernel/launcher/observation verifier.
	if o.getenv("CURSOR_CONVERSATION_ID") != "" {
		own, err := cursorCallingContext()
		if err != nil {
			return liveAgentSession{}, true, err
		}
		process, err := cursorProcessForContext(own.host)
		if err != nil {
			return liveAgentSession{}, true, err
		}
		if process.Kind != "cli" {
			return liveAgentSession{}, true, errors.New("Cursor desktop own attachment requires a per-conversation fence; use its verified person hook")
		}
		return own, true, nil
	}
	provider, pid, err := ownAgentAncestor(ctx)
	if err != nil || provider == "" {
		return liveAgentSession{}, provider != "", err
	}
	if provider == "cursor" {
		return liveAgentSession{}, true, errors.New("Cursor own attachment needs its native CLI conversation context; desktop own attachment is unavailable")
	}
	host, err := providerPIDHostKey(provider, pid)
	if err != nil {
		return liveAgentSession{}, true, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return liveAgentSession{}, true, err
	}
	if provider == "codex" {
		session := o.getenv("CODEX_THREAD_ID")
		if session != o.getenv("CODEX_SESSION_ID") {
			return liveAgentSession{}, true, errors.New("Codex session hints disagree; own-session routing cannot select another thread")
		}
		if _, err := uuid.Parse(session); err != nil {
			return liveAgentSession{}, true, errors.New("this Codex tool call has no valid CODEX_THREAD_ID; cannot select another chat")
		}
		return liveAgentSession{provider: provider, session: session, host: host, cwd: cwd, remote: refreshNativeRoute(provider, session, host, "")}, true, nil
	}
	// Claude publishes native session registrations. Other SDK adapters can
	// publish their exact active UUID; absence is not permission to guess one.
	sessions, err := discoverLiveAgents(ctx, root, o)
	if err != nil {
		return liveAgentSession{}, true, err
	}
	var matches []liveAgentSession
	for _, candidate := range sessions {
		if candidate.provider == provider && candidate.host == host {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return liveAgentSession{}, true, fmt.Errorf("cannot identify one exact %s session for this tool call; expose native session metadata or reload its adapter, then retry (no other chat was selected)", provider)
	}
	return matches[0], true, nil
}

func ownAgentAncestor(ctx context.Context) (string, int, error) {
	pid := os.Getppid()
	for i := 0; i < 32 && pid > 1; i++ {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		out, err := exec.CommandContext(probe, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=", "-o", "args=").Output()
		cancel()
		if err != nil {
			return "", 0, errors.New("cannot verify the calling process")
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 {
			return "", 0, errors.New("cannot verify the calling process")
		}
		// Examine the executable and a Node launcher script, never prompt words
		// elsewhere in argv (a prompt can mention another provider).
		for _, executable := range fields[1:min(len(fields), 4)] {
			if provider := ownProviderExecutable(executable); provider != "" {
				return provider, pid, nil
			}
		}
		pid, err = strconv.Atoi(fields[0])
		if err != nil {
			return "", 0, err
		}
	}
	if pid > 1 {
		return "", 0, errors.New("cannot verify the calling process ancestry")
	}
	return "", 0, nil
}

func ownProviderExecutable(executable string) string {
	name := strings.ToLower(filepath.Base(executable))
	switch name {
	case "codex", "codexcli":
		return "codex"
	case "claude", "muse", "pi", "agy":
		return name
	case "cursor", "cursor-agent":
		return "cursor"
	case "pi.mjs":
		return "pi"
	}
	if strings.HasPrefix(name, "muse-bin-") {
		return "muse"
	}
	if name == "cli.js" {
		if strings.Contains(filepath.ToSlash(executable), "/claude-code/") {
			return "claude"
		}
		if strings.Contains(filepath.ToSlash(executable), "/pi-coding-agent/") {
			return "pi"
		}
	}
	return ""
}
