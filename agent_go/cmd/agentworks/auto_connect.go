package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Native adapters and verified live-session discovery call this same path.
// New chats get their own stable address, never another folder chat's inbox.
// Existing chats keep their exact saved login, even if the default changed.
func autoConnectChat(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest, cwd string) error {
	if autoAttachDisabled() || autoAttachRead(root, autoAttachKey(req.Provider, cwd)).Off {
		return nil
	}
	if _, err := uuid.Parse(req.SessionID); err != nil || req.HostKey == "" {
		return nil
	}
	if req.Provider != "claude" && req.Provider != "codex" && req.Provider != "muse" && req.Provider != "cursor" && req.Provider != "pi" {
		return nil
	}
	if req.Provider == "pi" {
		record, err := piSessionForHost(root, req.HostKey, req.SessionID)
		if err != nil || record.Mode != "tui" {
			return nil
		}
	}
	var err error
	if err = ensureLocalBridge(ctx, root, o); err != nil {
		return err
	}
	status := req
	status.Action, status.RefreshRoute = "connection", false
	out, err := agentworksclient.CallLocalBridge(ctx, root, status)
	if err != nil {
		return err
	}
	if out.Stopped {
		return nil
	}
	if out.Attached && !out.Offline {
		if req.Provider == "pi" {
			return activatePiSession(ctx, root, req.HostKey, req.SessionID, out.Attachment.ID)
		}
		return nil
	}
	if out.Attachment.ID != "" {
		if reason := reconnectOffline(ctx, root, req); reason != "" {
			return fmt.Errorf("%s", reason)
		}
		return nil
	}
	path, err := o.path()
	if err != nil {
		return err
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil {
		return err
	}
	if cfg.Server == "" || cfg.Token == "" && cfg.RefreshToken == "" {
		return nil
	} // sign-in stays a browser choice
	name, _, err := proposedAgentIdentity(req.Provider, req.SessionID, cwd)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	req.Action, req.AgentName, req.ConfigPath = "attach", name, path
	req.Automatic, req.Setup, req.OwnAttach = true, false, req.Provider == "codex"
	req.WorkspaceDir = cwd // presence context only; never a file/terminal grant
	if req.NativeRemote == "" {
		req.NativeRemote = discoverNativeRemote(req.Provider, req.SessionID, req.HostKey)
	}
	out, err = agentworksclient.CallLocalBridge(ctx, root, req)
	if err == nil && out.Attached && req.Provider == "pi" {
		return activatePiSession(ctx, root, req.HostKey, req.SessionID, out.Attachment.ID)
	}
	return err
}

// A provider restart changes its process key while the native conversation
// remains the same. Recheck saved chats without waiting for a person to type.
// Absence is a local discovery result, not a reason to call the server again.
func savedChatReconnector(root string, o *options, discover func(context.Context, string, *options, ...agentworksclient.AgentAttachment) ([]liveAgentSession, error)) func(context.Context, []agentworksclient.AgentAttachment) {
	type retry struct {
		next   time.Time
		delay  time.Duration
		absent bool
	}
	attempts := map[string]retry{} // memory only; the helper runs one recovery at a time
	return func(ctx context.Context, saved []agentworksclient.AgentAttachment) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		probe := *o
		probe.attachedOnly = true
		live, err := discover(ctx, root, &probe, saved...)
		if err != nil || ctx.Err() != nil {
			return
		}
		for _, attachment := range saved {
			var found *liveAgentSession
			for i := range live {
				if live[i].provider == attachment.Provider && live[i].session == attachment.SessionID {
					found = &live[i]
					break
				}
			}
			state := attempts[attachment.ID]
			if found == nil {
				if !state.absent {
					log.Printf("[BRIDGE-RECOVERY] outcome=still_absent agent=%s attachment=%s", attachment.AgentName, attachment.ID)
				}
				attempts[attachment.ID] = retry{absent: true}
				continue
			}
			if time.Now().Before(state.next) {
				continue
			}
			req := agentworksclient.LocalBridgeRequest{Provider: found.provider, SessionID: found.session, HostKey: found.host, NativeRemote: found.remote, AttachmentID: attachment.ID}
			err := autoConnectChat(ctx, root, o, req, found.cwd)
			if err == nil {
				req.Action = "connection"
				var status agentworksclient.LocalBridgeReply
				status, err = agentworksclient.CallLocalBridge(ctx, root, req)
				if err == nil && (!status.Attached || status.Offline || status.Stopped) {
					err = fmt.Errorf("saved chat remains disconnected")
				}
			}
			if err != nil {
				req.Action = "connection"
				if status, e := agentworksclient.CallLocalBridge(ctx, root, req); e == nil && status.Stopped {
					delete(attempts, attachment.ID)
					continue // explicit disconnect or the server's replaced-attachment refusal
				}
				delay := min(max(state.delay*2, 30*time.Second), 10*time.Minute)
				attempts[attachment.ID] = retry{next: time.Now().Add(delay), delay: delay}
				log.Printf("[BRIDGE-RECOVERY] outcome=refused agent=%s attachment=%s retry_in=%s", attachment.AgentName, attachment.ID, delay)
				continue
			}
			delete(attempts, attachment.ID)
			log.Printf("[BRIDGE-RECOVERY] outcome=reconnected agent=%s attachment=%s", attachment.AgentName, attachment.ID)
		}
	}
}

// A lifecycle operation connects the currently verified live chats once. No
// directory polling, plugin installation, hook approval or extra-network join.
func connectLiveChats(ctx context.Context, o *options) {
	root, err := piBridgeRoot()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	sessions, err := discoverLiveAgents(ctx, root, o)
	if err != nil {
		fmt.Fprintln(o.stderr, "AgentWorks chat discovery:", err)
		return
	}
	for _, s := range sessions {
		if ctx.Err() != nil {
			return
		}
		if err := autoConnectChat(ctx, root, o, agentworksclient.LocalBridgeRequest{Provider: s.provider, SessionID: s.session, HostKey: s.host, NativeRemote: s.remote}, s.cwd); err != nil {
			fmt.Fprintf(o.stderr, "AgentWorks could not connect one %s chat: %v\n", s.provider, err)
		}
	}
}

func pluginAutoConnect(root string, o *options) func(context.Context, agentworksclient.LocalBridgeRequest) error {
	return func(ctx context.Context, req agentworksclient.LocalBridgeRequest) error {
		cwd, _ := os.Getwd()
		return autoConnectChat(ctx, root, o, req, cwd)
	}
}
