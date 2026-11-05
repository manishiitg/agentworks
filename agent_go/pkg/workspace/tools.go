package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
)

// Helper to convert generic map args to typed struct
func mapToStruct(args map[string]interface{}, v interface{}) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// The basic file tools (read/list/update/delete/move_workspace_file) were
// removed on 2026-08-05. They had not been reachable by an agent since the
// basic/advanced split: the server only ever registers the advanced set, and
// the "workspace_tools" category resolves to the advanced registry plus the
// browser. They survived as an executor map used by one dev harness, and cost
// more than they were worth — a reader (human or agent) auditing which tool
// results are bounded finds them in this file and reasonably concludes agents
// can call them. Reading a workspace file is execute_shell_command's job; it
// is capped and can be sliced with head/tail/sed. The typed Client methods
// (ReadWorkspaceFile and friends) are unaffected — the server uses them
// throughout to read files in Go.

// NewAdvancedExecutor creates executors for advanced workspace tools.
func NewAdvancedExecutor(client *Client) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	executors["execute_shell_command"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		var params ExecuteShellCommandParams
		if err := mapToStruct(args, &params); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		result, err := client.ExecuteShellCommand(ctx, params)
		if err != nil {
			return "", err
		}
		// Only the agent-facing result is capped. Scripted steps call
		// client.ExecuteShellCommand directly and parse stdout as schema-validated
		// JSON, so they must keep every byte.
		//
		// The cap is enforced on the encoded payload, not on the raw streams:
		// JSON escaping happens after any stream-level check and can multiply the
		// delivered size several times over.
		return marshalCappedShellResultForAgent(result)
	}

	executors["diff_patch_workspace_file"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		var params DiffPatchWorkspaceFileParams
		if err := mapToStruct(args, &params); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		result, err := client.DiffPatchWorkspaceFile(ctx, params)
		if err != nil {
			return "", err
		}
		// Intentionally no workspace_file_operation event: tool receipts already
		// provide the canonical operation record, and that legacy event was removed.
		return marshalResult(result)
	}

	return executors
}

// NewBrowserExecutor creates executors for browser tools
func NewBrowserExecutor(client *Client) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	executors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	browserClient := browser.NewClient(client.BaseURL)
	browserExecutor := browser.NewExecutor(browserClient)
	executors["agent_browser"] = browserExecutor.HandleAgentBrowser

	return executors
}
