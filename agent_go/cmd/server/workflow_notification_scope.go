package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Relays share the workflow runner but do not receive workflow notifications.
// Missing or unreadable metadata cannot grant this capability.
func workflowNotificationsForPath(path string) bool {
	if path == "" {
		return false
	}
	content, found, err := readFileFromWorkspace(context.Background(), manifestPath(path))
	if err != nil || !found {
		return false
	}
	// Crew/Code also store workflow.json but identify themselves by product.
	// Inspect only scope metadata without treating their manifests as workflows
	// or applying workflow defaults to them.
	var scope struct {
		Kind    string `json:"kind"`
		Product string `json:"product"`
	}
	if json.Unmarshal([]byte(content), &scope) != nil || strings.TrimSpace(scope.Product) != "" {
		return false
	}
	return strings.TrimSpace(scope.Kind) == "" || scope.Kind == "workflow"
}

func restrictWorkflowNotificationTools(tools []llmtypes.Tool, executors map[string]interface{}, categories map[string]string, enabled bool) []llmtypes.Tool {
	if enabled {
		return tools
	}
	kept := make([]llmtypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil && (tool.Function.Name == "notify_user" || tool.Function.Name == "send_email" || tool.Function.Name == "record_summary") {
			continue
		}
		kept = append(kept, tool)
	}
	delete(executors, "notify_user")
	delete(categories, "notify_user")
	delete(executors, "send_email")
	delete(categories, "send_email")
	delete(executors, "record_summary")
	delete(categories, "record_summary")
	return kept
}
