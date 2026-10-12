package main

import (
	"context"
	"errors"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/server"
)

// cursorMessagingChild serves the messaging-only tools for one Cursor CLI
// conversation. It binds to the verified kernel identity of the Cursor process it
// runs under and to the one native chat that process reported through its hooks;
// it holds no account credentials and cannot attach, detach or share. The shared
// desktop app is refused until it can prove which chat is asking.
func cursorMessagingChild(parent context.Context, o *options) error {
	process, err := cursorAncestor()
	if err != nil {
		return errors.New("AgentWorks cannot tell which Cursor chat this is; it supports the Cursor command line on macOS and Linux")
	}
	if process.Kind != "cli" {
		return errors.New("the Cursor desktop app is not supported for messaging tools yet; use the Cursor command line")
	}
	dir, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	bridge, err := agentworksclient.NewCursorPluginMCPServer(ctx, dir, process.Host, func() (string, error) { return cursorSessionForProcess(dir, process) })
	if err != nil {
		return err
	}
	return server.NewStdioServer(bridge).Listen(ctx, o.stdin, o.stdout)
}
