package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/dashboardmotion"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

const dashboardMotionTimeout = 10 * time.Minute

// Export is an authoring execution, like preview. It never grants a Run person
// shell access or invokes a product run as a side effect of loading a page.
func validDashboardMotionPath(composition string) bool {
	return path.Clean(composition) == composition && strings.HasPrefix(composition, "code/motion/") && !strings.ContainsAny(composition, "\\\x00")
}

func dashboardMotionGuard(claims *UserClaims, target dashboardTarget, composition, id string) (*workspace.FolderGuardConfig, error) {
	output := "db/assets/motion/" + id
	cache := ".sandbox-cache/dashboard-motion"
	guard := &workspace.FolderGuardConfig{Enabled: true, ReadPaths: []string{path.Join(target.Root, composition)}, WritePaths: []string{path.Join(target.Root, output), path.Join(target.Root, cache)}}
	if grants := dashboardFileGuard(claims); grants != nil {
		if !grants.Allows(composition, false) || !grants.Allows(output, true) || !grants.Allows(cache, true) {
			return nil, fmt.Errorf("This connection cannot read the animation and save its export. Use Builder chat or a connection with those project folders selected.")
		}
		for _, p := range grants.BlockedPaths {
			guard.BlockedPaths = append(guard.BlockedPaths, path.Join(target.Root, p))
		}
		for _, p := range append(append([]string{}, grants.ReadOnlyPaths...), grants.BlockedWritePaths...) {
			guard.BlockedWritePaths = append(guard.BlockedWritePaths, path.Join(target.Root, p))
		}
	}
	return guard, nil
}

func (api *StreamingAPI) renderDashboardMotion(parentCtx context.Context, claims *UserClaims, target dashboardTarget, composition, id string, requested ...dashboardExportOptions) (rendered map[string]any, renderErr error) {
	if !dashboardAuthoringAllowed(claims, &target) {
		return nil, fmt.Errorf("Ask this project's Builder to export the animation.")
	}
	var options dashboardExportOptions
	if len(requested) > 0 {
		options = requested[0]
	}
	faithful := options.SourceMode == "dashboard"
	if faithful {
		// The queued source is an exact immutable publication. Recheck its
		// readability as this person before opening the rendering session.
		checked, err := resolveDashboardExportSource(parentCtx, target, options)
		if err != nil || checked.Document != options.Document || checked.SourcePath != composition {
			return nil, fmt.Errorf("This published dashboard is no longer available. Choose a current dashboard and export again.")
		}
	} else if !validDashboardMotionPath(composition) {
		return nil, fmt.Errorf("Save the animation in a folder under code/motion/ first.")
	}
	guard, err := dashboardMotionGuard(claims, target, composition, id)
	if err != nil {
		return nil, err
	}
	output := "db/assets/motion/" + id
	cache := ".sandbox-cache/dashboard-motion"
	config := map[string]any{"composition_path": composition, "output_path": output, "cache_path": cache, "caller": claims.UserID, "renderer_version": dashboardmotion.RendererVersion}
	var previewToken string
	if faithful {
		previewToken, err = mintReportPreviewToken(claims, target.Root, options.Document)
		if err != nil {
			return nil, err
		}
		config["source_mode"], config["dashboard_id"], config["revision"], config["document"] = "dashboard", options.Dashboard, options.Revision, options.Document
		config["preview_origin"], config["workspace"] = api.GetCodeExecAPIURL(), target.Root
		config["theme"], config["width"], config["height"] = options.Theme, options.Width, options.Height
		config["duration_s"], config["fps"] = options.Duration, options.FPS
	}
	args, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	session := "dashboard-motion-" + id
	if parent, _ := parentCtx.Value(common.ChatSessionIDKey).(string); parent != "" {
		if policy := common.GetSessionShellConfig(parent); policy != nil {
			guard.BlockedPaths = append(guard.BlockedPaths, policy.BlockedPaths...)
			guard.BlockedWritePaths = append(guard.BlockedWritePaths, policy.BlockedWritePaths...)
			guard.StrictAllowlist, guard.DenyNetwork = policy.StrictAllowlist, policy.DenyNetwork
		}
	}
	common.SetSessionWorkingDir(session, target.Root)
	common.SetSessionFolderGuard(session, guard.ReadPaths, guard.WritePaths)
	common.SetSessionFolderGuardBlockedPaths(session, guard.BlockedPaths)
	common.SetSessionFolderGuardBlockedWritePaths(session, guard.BlockedWritePaths)
	common.SetSessionSandbox(session, guard.StrictAllowlist, guard.DenyNetwork)
	common.BindSessionBrowserIsolationForSession(session)
	defer common.ClearSessionShellConfig(session)
	parentCtx = context.WithValue(parentCtx, common.ChatSessionIDKey, session)
	parentCtx = context.WithValue(parentCtx, common.WorkflowSessionIDKey, session)
	parentCtx = context.WithValue(parentCtx, common.UserIDKey, claims.UserID)
	ctx, cancel := context.WithTimeout(parentCtx, dashboardMotionTimeout)
	defer cancel()
	seconds := int(dashboardMotionTimeout / time.Second)
	started := time.Now()
	status := "failed"
	defer func() {
		elapsed := time.Since(started).Milliseconds()
		log.Printf("[DASHBOARD_MOTION_EXPORT] caller=%s project=%s composition=%s output=%s duration_ms=%d bytes=%v result=%s", claims.UserID, target.Root, composition, output, elapsed, rendered["bytes"], status)
		if faithful {
			log.Printf("[DASHBOARD_EXPORT] dashboard=%s revision=%s by=%s frames=%v result=%s", options.Dashboard, options.Revision, claims.UserID, rendered["frames"], status)
		}
		if api.costLedger != nil {
			binding := costProductForWorkspace(parentCtx, target.Root)
			entry := costledger.Entry{
				Attribution: costledger.Attribution{AttributionVersion: costledger.AttributionVersion, ActorID: claims.UserID, ProductKind: binding.Kind, ProductID: binding.ID, ProductName: binding.Name, ProductOwnerID: binding.OwnerID, Activity: "dashboard_motion_export"},
				EventID:     output, Timestamp: time.Now().UTC(), UserID: claims.UserID, WorkflowID: target.Root, Scope: "tool", Component: "hyperframes", ToolName: "export_dashboard_motion", BillingBasis: "unpriced",
				OperationMetadata: map[string]any{"duration_ms": elapsed, "bytes": rendered["bytes"], "result": status, "output": output},
			}
			if err := api.costLedger.Append(entry); err != nil {
				log.Printf("[COST_LEDGER] dashboard export=%s caller=%s error=%v", output, claims.UserID, err)
			}
		}
	}()
	command := dashboardmotion.Command(args)
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(claims.UserID))
	if faithful {
		// Never put this credential in command arguments or saved source.
		client.SetExtraEnv("SECRET_DASHBOARD_EXPORT_TOKEN", previewToken)
	}
	result, execErr := client.ExecuteShellCommand(ctx, workspace.ExecuteShellCommandParams{
		Command: command, WorkingDirectory: target.Root, Timeout: &seconds,
		FolderGuard: guard,
	})
	if execErr != nil {
		return nil, fmt.Errorf("animation export: %w", execErr)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &rendered); err != nil {
		return nil, fmt.Errorf("The animation could not be exported. Check that the server has HyperFrames, Chrome and FFmpeg available.")
	}
	if result.ExitCode != 0 || rendered["success"] != true {
		diagnostic := result.Stderr
		if len(diagnostic) > 4000 {
			diagnostic = diagnostic[len(diagnostic)-4000:]
		}
		log.Printf("[DASHBOARD_MOTION_EXPORT_ERROR] export=%s error=%v stderr=%s", id, rendered["error"], diagnostic)
		return rendered, fmt.Errorf("%v", rendered["error"])
	}
	status = "completed"
	return rendered, nil
}
