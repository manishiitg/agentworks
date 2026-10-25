package virtualtools

import (
	"context"
	"fmt"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var slackMessageHandler struct {
	sync.RWMutex
	send func(context.Context, map[string]interface{}) (string, error)
}

func SetSlackMessageHandler(send func(context.Context, map[string]interface{}) (string, error)) {
	slackMessageHandler.Lock()
	defer slackMessageHandler.Unlock()
	slackMessageHandler.send = send
}
func handleSlackMessage(ctx context.Context, args map[string]interface{}) (string, error) {
	slackMessageHandler.RLock()
	send := slackMessageHandler.send
	slackMessageHandler.RUnlock()
	if send == nil {
		return "", fmt.Errorf("Slack messaging service unavailable")
	}

	return send(ctx, args)
}
func createSlackMessageTool() llmtypes.Tool {
	return llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "send_slack_message", Description: "Post through a configured workflow/project Slack route. Credentials stay backend-owned. Omit thread_ref to start a message (Slack-origin children inherit their source thread). Pass the returned opaque thread_ref to reply, including from later scripted steps via context_dependency. A stable idempotency_key is required per logical send. route_id is the exact configured channel route ID; arbitrary channels and cross-route references are rejected.", Parameters: llmtypes.NewParameters(map[string]interface{}{
		"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
			"route_id": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string", "maxLength": 3000}, "thread_ref": map[string]interface{}{"type": "string"}, "idempotency_key": map[string]interface{}{"type": "string"},
		}, "required": []string{"route_id", "message", "idempotency_key"},
	})}}
}
