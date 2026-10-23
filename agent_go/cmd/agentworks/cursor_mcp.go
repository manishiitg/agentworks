package main

import (
	"context"
	"errors"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// cursorMessagingChild serves the messaging-only tools for Cursor. It binds to
// the verified kernel identity of the Cursor process it runs under and holds no
// account credentials; it cannot attach, detach or share. A CLI process has one
// chat, reported through its hooks. The desktop app runs many chats in one
// process, so each call must match a one-shot record that the app's
// beforeMCPExecution hook wrote for that exact chat and call.
func cursorMessagingChild(parent context.Context, o *options) error {
	process, err := cursorAncestor()
	if err != nil {
		return errors.New("AgentWorks cannot tell which Cursor app or command line is running it; it supports Cursor on macOS and Linux")
	}
	dir, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	session := func(string, map[string]any) (string, error) { return cursorSessionForProcess(dir, process) }
	if process.Kind == "desktop" {
		session = func(tool string, args map[string]any) (string, error) {
			return consumeCursorFence(dir, process.Host, tool, args, time.Now())
		}
	}
	bridge, err := agentworksclient.NewCursorPluginMCPServer(ctx, dir, process.Host, session, pluginAutoConnect(dir, o))
	if err != nil {
		return err
	}
	addChatTools(bridge, o, dir, "cursor", process.Host, func(_ context.Context, req mcp.CallToolRequest) (string, error) {
		return session(req.Params.Name, req.GetArguments())
	})
	return server.NewStdioServer(bridge).Listen(ctx, o.stdin, o.stdout)
}
