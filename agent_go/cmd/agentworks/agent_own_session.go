package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
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
			return liveAgentSession{}, true, errCursorDesktop
		}
		return own, true, nil
	}
	provider, pid, err := ownAgentAncestor(ctx)
	if err != nil {
		// The Codex desktop sandbox blocks ps, so the calling process cannot be
		// walked. The desktop app-server itself can still confirm this exact thread.
		if own, ok := codexDesktopOwnSession(ctx, o); ok {
			return own, true, nil
		}
		if o.getenv("CODEX_THREAD_ID") != "" {
			// A typed /agentworks attach reaches this command only when the chat's hook did not run it.
			return liveAgentSession{}, true, fmt.Errorf("could not confirm this Codex chat from here (detail: %w). If AgentWorks hooks are not active in this chat, trust them in Settings > Hooks and reload it, then ask again or type /agentworks attach; otherwise the Codex app may be busy, so try again in a moment", err)
		}
	}
	if err != nil || provider == "" {
		return liveAgentSession{}, provider != "", err
	}
	if provider == "cursor" {
		return liveAgentSession{}, true, errCursorDesktop
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
	if provider == "claude" {
		session := claudeSessionIDForHost(host)
		if session == "" {
			// Hook-only/background sessions need no native idle socket. The
			// helper already holds their exact hook attachment. Its lookup
			// refuses ambiguity instead of picking a sibling chat.
			out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "status", Provider: provider, HostKey: host})
			if err != nil {
				return liveAgentSession{}, true, err
			}
			if !out.Attached && !out.Offline {
				return liveAgentSession{}, true, errors.New("this Claude chat has no AgentWorks connection; ask to connect it or type /agentworks attach")
			}
			session = out.Attachment.SessionID
		}
		if _, err := uuid.Parse(session); err != nil {
			return liveAgentSession{}, true, errors.New("cannot verify this Claude chat's exact session")
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

// codexDesktopHostPrefix marks a process key derived from the Codex desktop's
// own connection instead of a ps entry, for tool calls whose sandbox blocks ps.
const codexDesktopHostPrefix = "codex-desktop-"

// codexDesktopSocket is the desktop app-server's local socket.
func codexDesktopSocket() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return "desktop+unix://" + filepath.Join(home, ".codex", "ipc", "ipc.sock"), nil
}

// codexDesktopOwnSession verifies this tool call's exact Codex thread with the
// running desktop app-server when ps is unavailable. It needs matching, valid
// CODEX_THREAD_ID and CODEX_SESSION_ID values and a desktop owner that accepts
// untrusted app input for that conversation; otherwise it selects nothing.
func codexDesktopOwnSession(ctx context.Context, o *options) (liveAgentSession, bool) {
	session := o.getenv("CODEX_THREAD_ID")
	if session == "" || session != o.getenv("CODEX_SESSION_ID") {
		return liveAgentSession{}, false
	}
	if _, err := uuid.Parse(session); err != nil {
		return liveAgentSession{}, false
	}
	remote, err := codexDesktopSocket()
	if err != nil || validateCodexRemote(remote) != nil {
		return liveAgentSession{}, false
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, call, err := openCodexDesktop(probe, remote)
	if err != nil {
		return liveAgentSession{}, false
	}
	defer conn.Close()
	reply, err := call("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": session}, 1, "")
	if err != nil {
		return liveAgentSession{}, false
	}
	var owner string
	var support struct {
		Untrusted bool `json:"supportsUntrustedAppInput"`
	}
	_ = json.Unmarshal(reply["handledByClientId"], &owner)
	_ = json.Unmarshal(reply["result"], &support)
	if owner == "" || !support.Untrusted {
		return liveAgentSession{}, false
	}
	cwd, _ := os.Getwd()
	sum := sha256.Sum256([]byte(remote + "\x00" + owner))
	return liveAgentSession{provider: "codex", session: session, host: codexDesktopHostPrefix + hex.EncodeToString(sum[:]), cwd: cwd, remote: remote}, true
}

func ownAgentAncestor(ctx context.Context) (string, int, error) {
	if runtime.GOOS == "windows" {
		return winOwnAgentAncestor()
	}
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
	if providerProcessMatches("claude", executable) {
		return "claude"
	}
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
