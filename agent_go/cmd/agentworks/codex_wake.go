package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os/exec"
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

func nativeCodexWake(root string, o *options) func(context.Context, agentworksclient.AgentAttachment, string) error {
	return func(ctx context.Context, a agentworksclient.AgentAttachment, remote string) error {
		if a.Provider != "codex" || !strings.HasPrefix(remote, "desktop+unix://") {
			return errors.New("native Codex desktop required")
		}
		if _, err := uuid.Parse(a.SessionID); err != nil {
			return err
		}
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
}
