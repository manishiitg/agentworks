package virtualtools

import (
	"context"
	"log"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetWorkspaceBrowserToolCategory returns the category name for workspace browser tools
func GetWorkspaceBrowserToolCategory() string {
	return "workspace_browser"
}

// CreateWorkspaceBrowserTools creates the single agent_browser virtual tool
func CreateWorkspaceBrowserTools() []llmtypes.Tool {
	return []llmtypes.Tool{browser.GetToolDefinition()}
}

// CreateWorkspaceBrowserToolExecutors creates the execution functions for workspace browser tools.
// Optional CDP ports authorize one or more independently-profiled Chrome browsers.
func CreateWorkspaceBrowserToolExecutors(cdpPort ...int) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	return CreateWorkspaceBrowserToolExecutorsWithSession("", cdpPort...)
}

// CreateWorkspaceBrowserToolExecutorsWithSession creates browser tool executors with chat session tracking.
// sessionID is the chat/workflow session ID — used to enforce per-session browser limits.
// Multiple ports are an explicit opt-in for separate login identities within
// one run; normal workflow concurrency should continue sharing one CDP port.
func CreateWorkspaceBrowserToolExecutorsWithSession(sessionID string, cdpPort ...int) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	mode := "headless"
	if len(cdpPort) > 0 {
		mode = "cdp"
	}
	return CreateWorkspaceBrowserToolExecutorsWithRuntime(
		sessionID,
		browser.NewBrowserRuntimeConfig(mode, cdpPort),
	)
}

// CreateWorkspaceBrowserToolExecutorsWithRuntime creates an executor backed by
// configured browser intent. In auto mode the shared runtime performs a live
// CDP status check for every status/action call; resolved availability is never
// copied into chat history or cached workshop prompts.
func CreateWorkspaceBrowserToolExecutorsWithRuntime(sessionID string, runtime *browser.BrowserRuntimeConfig) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	// Wire up the browser executor from the pkg/browser package
	browserClient := browser.NewClient(getWorkspaceAPIURL())
	browserExecutor := browser.NewExecutor(browserClient,
		browser.WithBrowserRuntimeConfig(runtime),
	)

	// Wrap executor to inject the workflow session ID. Delegated agents inherit
	// this browser session; tool-session isolation does not fork browser state.
	executors["agent_browser"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		if existingID, ok := ctx.Value(common.ChatSessionIDKey).(string); ok && existingID != "" {
			// Preserve the session injected by /s/{session_id}/tools/... routes.
			log.Printf("[BROWSER_TOOLS] Preserving context agent session: %s (parent workflow: %s)", existingID, sessionID)
		} else if sessionID != "" {
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
		}
		// Always set the workflow-level session to the parent (for per-workflow limits)
		if sessionID != "" {
			ctx = context.WithValue(ctx, common.WorkflowSessionIDKey, sessionID)
		}
		return browserExecutor.HandleAgentBrowser(ctx, args)
	}

	return executors
}
