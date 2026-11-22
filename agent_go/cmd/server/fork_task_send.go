package server

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

// agentSendHolder keeps this chat's own send_message tool, so fork_project_chat can send a fork its first task
// through exactly the path messages action=ask takes (same admission, the "From" label, the reply back to this chat).
type agentSendHolder struct {
	send func(context.Context, map[string]interface{}) (string, error)
}

// agentSendCaptureRegistrar registers every tool unchanged and keeps send_message's executor in holder.
type agentSendCaptureRegistrar struct {
	definitionToolRegistrar
	holder *agentSendHolder
}

func (r agentSendCaptureRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	if name == "send_message" && r.holder != nil {
		r.holder.send = execute
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, execute, category)
}

func (r agentSendCaptureRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if name == "send_message" && r.holder != nil {
		r.holder.send = execute
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, execute, timeout, category)
}

// sendForkTask sends a new fork its first task as this chat's AgentBridge ask, so the fork starts working at once and
// its answer comes back here (owner, 22 Nov: a fork waited until the person typed). forkResult is the fork tool's
// JSON; the returned JSON adds what happened to the task. A failed send keeps the fork and says how to send it by hand.
func (api *StreamingAPI) sendForkTask(ctx context.Context, userID string, project codeChatProject, messages *agentSendHolder, task string, forkResult []byte) string {
	out := map[string]interface{}{}
	if err := json.Unmarshal(forkResult, &out); err != nil {
		return string(forkResult)
	}
	key, _ := out["conversation_key"].(string)
	address := ""
	if key != "" {
		if _, err := api.registerHostedBridgeChat(ctx, userID, hostedBridgeBinding{Profile: project.ProfileID, Project: project.ProjectID, Chat: key}); err == nil {
			address = bridgeChatAddress(userID, project.ProfileID, project.ProjectID, key)
		} else {
			log.Printf("[CHAT_FORK] fork %s: task not sent, address unavailable: %v", key, err)
		}
	}
	if address != "" && messages != nil && messages.send != nil {
		receipt, err := messages.send(ctx, map[string]interface{}{"target": address, "message": task, "message_kind": "ask", "expects_reply": true})
		if err == nil {
			var sent map[string]interface{}
			if json.Unmarshal([]byte(receipt), &sent) == nil {
				out["task_inbox_id"] = sent["inbox_id"]
			}
			out["task_sent"] = true
			out["next"] = "The fork is working on the task; its answer comes back to you in that inbox. Tell the person it has started."
			log.Printf("[CHAT_FORK] fork %s: task sent as an ask", key)
			raw, _ := json.Marshal(out)
			return string(raw)
		}
		log.Printf("[CHAT_FORK] fork %s: task not sent: %v", key, err)
		out["task_error"] = err.Error()
	}
	out["task_sent"] = false
	out["target"] = address
	out["next"] = "The fork was made but its task was not sent. Send it with messages action=ask to the fork (find it with messages action=list_agents)."
	raw, _ := json.Marshal(out)
	return string(raw)
}
