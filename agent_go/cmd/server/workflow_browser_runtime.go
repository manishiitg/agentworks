package server

import (
	"context"
	"fmt"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

// Step tools execute through fresh HTTP bridge contexts, outside the builder's
// LLMAgentWrapper. Bind the same authenticated owner/controller here too, before
// looking up the account-private extension. Keep the child's ChatSessionID so
// browser artifacts still use its narrow filesystem grants.
func (api *StreamingAPI) bindWorkflowBrowserExecutors(requestCtx context.Context, sessionID string, req QueryRequest, readOnly bool, executors codingAgentToolExecutors) codingAgentToolExecutors {
	resolve := api.bindToolExecutionContext(requestCtx, sessionID, req, readOnly)
	bound := make(codingAgentToolExecutors, len(executors))
	for name, execute := range executors {
		bound[name] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			ctx, err := resolve(ctx, name)
			if err != nil {
				return "", err
			}
			return execute(ctx, args)
		}
	}
	return bound
}

// Persistent CLI turns reuse their registered tools. Read browser intent at
// invocation time so saving workflow settings takes effect without a new chat.
// An invalid or missing configuration remains disabled, even with the tool present.
func workflowBrowserExecutors(sessionID, workspacePath string, readManifest func(context.Context, string) (*WorkflowManifest, bool, error)) codingAgentToolExecutors {
	return codingAgentToolExecutors{"agent_browser": func(ctx context.Context, args map[string]interface{}) (string, error) {
		manifest, found, err := readManifest(ctx, workspacePath)
		if err != nil {
			return "", fmt.Errorf("read workflow browser configuration: %w", err)
		}
		mode := "none"
		var ports []int
		if found && manifest != nil {
			switch strings.ToLower(strings.TrimSpace(manifest.Capabilities.BrowserMode)) {
			case "", "none", "auto", "headless", "cdp":
				mode = effectiveWorkspaceBrowserMode(manifest.Capabilities.BrowserMode)
			}
			ports = configuredCDPPortsForMode(mode, nil, manifest.Capabilities.CDPPorts)
		}
		execs := virtualtools.CreateWorkspaceBrowserToolExecutorsWithRuntime(sessionID, browser.NewBrowserRuntimeConfig(mode, ports))
		return execs["agent_browser"](ctx, args)
	}}
}
