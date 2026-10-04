package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// capture is a Builder command, never an upstream CLI command. Resolve its
// workspace and permissions from the invoking session, not tool arguments.
func (e *Executor) handleCapture(ctx context.Context, session string, args []string, cfg *common.SessionShellConfig, guard *FolderGuardConfig) (string, error) {
	if len(args) != 1 || (args[0] != "start" && args[0] != "stop" && args[0] != "status") {
		return "", fmt.Errorf("usage: agent_browser(command=\"capture\", args=[\"start\"|\"stop\"|\"status\"], session=\"main\"); workspace destination is automatic")
	}
	if cfg == nil || guard == nil || !guard.Enabled {
		return "", fmt.Errorf("CAPTURE_ACCESS_DENIED: capture requires a workflow session with workspace permissions")
	}
	// Crew projects capture into their own project root, exactly like
	// workflows capture into Workflow/<name>. The classifier is shared so
	// the next product cannot fall through another hand-rolled check.
	workspace := captureWorkspace(common.SessionUserIDFromContext(ctx), cfg)
	if workspace == "" {
		return "", fmt.Errorf("CAPTURE_ACCESS_DENIED: no owning workflow is configured for this session")
	}
	owner, _ := ctx.Value(common.WorkflowSessionIDKey).(string)
	if owner == "" {
		owner, _ = ctx.Value(common.ChatSessionIDKey).(string)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"action": args[0], "workspace_path": workspace, "owner_session": owner,
		"working_directory": cfg.WorkingDir, "folder_guard": guard,
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Client.WorkspaceAPIURL+"/api/browser/live/"+url.PathEscape(session)+"/recording", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-Token", os.Getenv("WORKSPACE_API_TOKEN"))
	resp, err := e.Client.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("capture request failed; check capture status before retrying: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || !json.Valid(body) {
		return "", fmt.Errorf("invalid capture response; check capture status before retrying")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("capture returned HTTP %d: %s", resp.StatusCode, body)
	}
	var state struct {
		Recording bool   `json:"recording"`
		Owner     string `json:"owner_session"`
	}
	if json.Unmarshal(body, &state) == nil {
		GetSessionTracker().SetCapture(session, state.Recording, state.Owner, workspace)
	}
	return string(body), nil
}

// captureWorkspace returns the session's owning root for capture binding:
// Workflow/<name>, or the Crew project root. Older sessions may carry only
// a trusted working directory; ownership is still never inferred from an
// agent's arguments. Empty when the session has no capturable home.
func captureWorkspace(userID string, cfg *common.SessionShellConfig) string {
	if cfg == nil {
		return ""
	}
	for _, candidate := range []string{cfg.WorkflowPath, cfg.WorkingDir} {
		canonical := common.CanonicalSessionWorkspace(userID, candidate)
		// Classification recognizes projects under any owner for Crew routing.
		// Capture needs ownership: only the caller's physical prefix is stripped
		// by canonicalization, so a remaining user prefix belongs to someone else.
		if workspaceref.MustParse(canonical).HasOwner() {
			continue
		}
		if _, root := common.ClassifySessionWorkspace(userID, canonical); root != "" {
			return root
		}
	}
	return ""
}
