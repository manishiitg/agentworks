package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Agent messaging does not expand a guest's capabilities: it can read and
// explicitly reply to messages addressed to this session, never choose a new
// recipient. The transport separately authenticates inbox participation.
type agentReplyOnlyRegistrar struct{ definitionToolRegistrar }

func (r agentReplyOnlyRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	if name == "schedule_message_wakeup" {
		return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, execute, category)
	}
	if name != "send_message" && name != "read_agent_messages" {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, agentReplyExecutor(execute), category)
}

func (r agentReplyOnlyRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if name == "schedule_message_wakeup" {
		return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, execute, timeout, category)
	}
	if name != "send_message" && name != "read_agent_messages" {
		return nil
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, agentReplyExecutor(execute), timeout, category)
}

func agentReplyExecutor(execute func(context.Context, map[string]interface{}) (string, error)) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		inbox, _ := args["inbox_id"].(string)
		target, _ := args["target"].(string)
		if strings.TrimSpace(inbox) == "" || strings.TrimSpace(target) != "" {
			return "", fmt.Errorf("this Run-mode conversation can only read or reply to an existing inbox_id; it cannot select another agent")
		}
		return execute(ctx, args)
	}
}
