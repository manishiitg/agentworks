package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func codexDesktopOwnerReady(ctx context.Context, remote, session string) bool {
	conn, call, err := openCodexDesktop(ctx, remote)
	if err != nil {
		return false
	}
	defer conn.Close()
	out, err := call("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": session}, 1, "")
	if err != nil {
		return false
	}
	var owner string
	var support struct {
		Untrusted bool `json:"supportsUntrustedAppInput"`
	}
	_ = json.Unmarshal(out["handledByClientId"], &owner)
	_ = json.Unmarshal(out["result"], &support)
	return owner != "" && support.Untrusted
}

// nativeChatWake reopens an exact saved chat that closed while a permitted
// message waits, then reconnects it so the normal delivery path takes over.
func nativeChatWake(root string, o *options) func(context.Context, agentworksclient.AgentAttachment, string) error {
	return func(ctx context.Context, a agentworksclient.AgentAttachment, remote string) error {
		if _, err := uuid.Parse(a.SessionID); err != nil {
			return err
		}
		var err error
		switch {
		case a.Provider == "claude" && strings.HasPrefix(remote, "claude+session://"):
			err = wakeClaudeChat(ctx, a)
		case a.Provider == "codex" && agentworksclient.IsCodexDesktopRemote(remote):
			err = wakeCodexDesktopChat(ctx, a, remote)
		default:
			return errors.New("this chat's app cannot reopen it automatically")
		}
		if err != nil {
			return err
		}
		return reconnectWokenChat(ctx, root, o, a)
	}
}

// claudeSessionRunning reports whether a live Claude process owns this exact
// session, through the same private registration the delivery path uses.
func claudeSessionRunning(session string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	for _, file := range files {
		record, _, err := nativeClaudeSession("claude+session://"+file, session)
		if err != nil {
			continue
		}
		if _, err = providerPIDHostKey("claude", record.PID); err == nil {
			return true
		}
	}
	return false
}

// Claude Code continues a closed session in the background under the same ID
// (claude --bg --resume). A session that is still running is never started a
// second time: Claude would start a copy instead.
func wakeClaudeChat(ctx context.Context, a agentworksclient.AgentAttachment) error {
	if !claudeSessionRunning(a.SessionID) {
		cmd := exec.CommandContext(ctx, "claude", "--bg", "--resume", a.SessionID)
		cmd.Env = claudeWakeEnv(os.Environ())
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Run(); err != nil {
			return err
		}
		log.Printf("[BRIDGE-WAKE] outcome=opened agent=%s attachment=%s route=claude_background reason=pending_message", a.AgentName, a.ID)
	}
	for !claudeSessionRunning(a.SessionID) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil
}

// The helper may have been started from inside another Claude chat. Drop that
// chat's own identity, socket and token so the woken session is not treated as
// nested in it; the person's own settings (auth, model, proxies) stay.
func claudeWakeEnv(env []string) []string {
	perChat := map[string]bool{"CLAUDECODE": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true, "AGENTWORKS_CLAUDE_CHANNEL": true,
		"CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_SESSION_ATTENDED": true, "CLAUDE_CODE_EXECPATH": true,
		"CLAUDE_CODE_MESSAGING_SOCKET": true, "CLAUDE_CODE_MESSAGING_TOKEN": true, "CLAUDE_CODE_CHILD_SESSION": true,
		"CLAUDE_CODE_CHILD_NOTES_DIR": true, "CLAUDE_CODE_SSE_PORT": true, "CLAUDE_CODE_TMUX_DIAGNOSTICS_DIR": true}
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !perChat[name] {
			out = append(out, kv)
		}
	}
	return out
}

func wakeCodexDesktopChat(ctx context.Context, a agentworksclient.AgentAttachment, remote string) error {
	probe, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	ready := codexDesktopOwnerReady(probe, remote, a.SessionID)
	cancel()
	if !ready {
		link := "codex://threads/" + a.SessionID
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.CommandContext(ctx, "open", "-g", link)
		case "windows":
			cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", link)
		default:
			cmd = exec.CommandContext(ctx, "xdg-open", link)
		}
		if err := cmd.Run(); err != nil {
			return err
		}
		log.Printf("[BRIDGE-WAKE] outcome=opened agent=%s attachment=%s reason=pending_message", a.AgentName, a.ID)
	}
	for !ready && ctx.Err() == nil {
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		ready = codexDesktopOwnerReady(probe, remote, a.SessionID)
		cancel()
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return ctx.Err()
	}
	return nil
}

func reconnectWokenChat(ctx context.Context, root string, o *options, a agentworksclient.AgentAttachment) error {
	probeOptions := *o
	probeOptions.attachedOnly = true
	live, err := discoverLiveAgents(ctx, root, &probeOptions, a)
	if err != nil {
		return err
	}
	for _, chat := range live {
		if chat.provider == a.Provider && chat.session == a.SessionID {
			// The exact native session was just verified. Its current local status
			// decides whether a concurrent hook already restored it or stopped it.
			request := agentworksclient.LocalBridgeRequest{Provider: chat.provider, SessionID: chat.session, HostKey: chat.host, NativeRemote: chat.remote}
			if err := autoConnectChat(ctx, root, o, request, chat.cwd); err != nil {
				return err
			}
			request.Action = "connection"
			status, err := agentworksclient.CallLocalBridge(ctx, root, request)
			if err != nil {
				return err
			}
			if !status.Attached || status.Offline || status.Stopped {
				return errors.New("native chat is not connected")
			}
			return nil
		}
	}
	return errors.New("resumed native chat unavailable")
}
