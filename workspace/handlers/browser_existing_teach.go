package handlers

import (
	"context"
	"fmt"
)

// Replay and tab inspection use the daemon that owns the user's page. A CLI
// resolved from another PATH can upgrade/restart it before executing a command.
// This adapter exposes only the finite operations needed by reviewed teaching.
func runExistingTeachCommand(ctx context.Context, socket, session string, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("missing teaching operation")
	}
	request := map[string]any{"id": "teach-action"}
	switch args[0] {
	case "tab":
		request["action"] = "tab_list"
		if len(args) > 1 {
			switch args[1] {
			case "new":
				request["action"] = "tab_new"
				if len(args) > 2 {
					request["url"] = args[2]
				}
			case "close":
				request["action"] = "tab_close"
				if len(args) > 2 {
					request["tabId"] = args[2]
				}
			default:
				request["action"] = "tab_switch"
				request["tabId"] = args[1]
			}
		}
	case "get":
		if len(args) != 2 {
			return nil, fmt.Errorf("invalid teaching inspection")
		}
		switch args[1] {
		case "cdp-url":
			request["action"] = "cdp_url"
		case "url":
			request["action"] = "url"
		default:
			return nil, fmt.Errorf("unsupported teaching inspection")
		}
	case "frame", "eval", "open", "press", "click", "focus", "check", "uncheck":
		if len(args) != 2 {
			return nil, fmt.Errorf("invalid teaching operation")
		}
		action, field := args[0], "selector"
		switch action {
		case "eval":
			action, field = "evaluate", "script"
		case "open":
			action, field = "navigate", "url"
		case "press":
			field = "key"
		case "frame":
			if args[1] == "main" {
				action, field = "mainframe", ""
			}
		}
		request["action"] = action
		if field != "" {
			request[field] = args[1]
		}
	case "fill", "select":
		if len(args) != 3 {
			return nil, fmt.Errorf("invalid teaching input")
		}
		request["action"] = args[0]
		request["selector"] = args[1]
		if args[0] == "fill" {
			request["value"] = args[2]
		} else {
			request["values"] = args[2]
		}
	default:
		return nil, fmt.Errorf("unsupported teaching operation")
	}
	return existingBrowserCommand(ctx, socket, session, request)
}
