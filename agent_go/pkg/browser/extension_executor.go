package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func extensionBinding(ctx context.Context) *browserrelay.Binding {
	user := common.SessionUserIDFromContext(ctx)
	if user == "" {
		return nil
	}
	for _, key := range []interface{}{common.ChatSessionIDKey, common.WorkflowSessionIDKey} {
		id, _ := ctx.Value(key).(string)
		if scope := common.SandboxBrowserSession(id); scope != "" {
			if b := browserrelay.Default.Lookup(user, scope); b != nil && (b.Profile() == "code" || b.Profile() == "work" || b.Profile() == "workflow") {
				return b
			}
		}
	}
	return nil
}

func browserExecuteOptions(ctx context.Context, agentSessionID, workflowSessionID string, timeout time.Duration) (*ExecuteOptions, *common.SessionShellConfig, error) {
	var folderGuard *FolderGuardConfig
	workingDir := "."

	// Look up per-session config (working dir + folder guard)
	var sessionCfg *common.SessionShellConfig
	if agentSessionID != "" {
		sessionCfg = common.GetSessionShellConfig(agentSessionID)
	}
	// Fallback: try workflow session if agent session didn't have config
	if sessionCfg == nil && workflowSessionID != "" && workflowSessionID != agentSessionID {
		sessionCfg = common.GetSessionShellConfig(workflowSessionID)
	}

	// Working directory priority: browser downloads path > session config > default
	if downloadsPath, ok := ctx.Value(common.BrowserDownloadsPathKey).(string); ok && downloadsPath != "" {
		workingDir = downloadsPath
	} else if sessionCfg != nil && sessionCfg.WorkingDir != "" {
		workingDir = sessionCfg.WorkingDir
	}

	// Folder guard priority: session config > context system 1 > context system 2
	if sessionCfg != nil && (sessionCfg.FolderGuardSet || len(sessionCfg.ReadPaths) > 0 || len(sessionCfg.WritePaths) > 0 ||
		len(sessionCfg.BlockedPaths) > 0 || len(sessionCfg.BlockedWritePaths) > 0) {
		readPaths := sessionCfg.WritePaths
		if len(sessionCfg.ReadPaths) > 0 {
			readPaths = common.DeduplicateStrings(append(sessionCfg.ReadPaths, sessionCfg.WritePaths...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        sessionCfg.WritePaths,
			ReadPaths:         readPaths,
			BlockedPaths:      sessionCfg.BlockedPaths,
			BlockedWritePaths: sessionCfg.BlockedWritePaths,
		}
	} else if allowedWrites, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string); ok {
		// Context System 1: chat/plan/prototype mode
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := allowedWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = common.DeduplicateStrings(append(ctxReads, allowedWrites...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        allowedWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      browserContextGuardPaths(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: browserContextGuardPaths(ctx, common.FolderGuardBlockedWritePathsKey),
		}
	} else if ctxWrites, ok := ctx.Value(common.FolderGuardWritePathsKey).([]string); ok {
		// Context System 2: workflow orchestrator
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := ctxWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = common.DeduplicateStrings(append(ctxReads, ctxWrites...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        ctxWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      browserContextGuardPaths(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: browserContextGuardPaths(ctx, common.FolderGuardBlockedWritePathsKey),
		}
	}
	if folderGuard != nil && len(folderGuard.ReadPaths) == 0 && len(folderGuard.WritePaths) == 0 {
		return nil, nil, fmt.Errorf("ACCESS DENIED: agent_browser has no granted workspace paths")
	}
	if folderGuard != nil {
		folderGuard.BrowserSession = common.SandboxBrowserSession(agentSessionID)
		if folderGuard.BrowserSession == "" && workflowSessionID != "" {
			folderGuard.BrowserSession = common.SandboxBrowserSession(workflowSessionID)
		}
	}

	return &ExecuteOptions{UserID: common.SessionUserIDFromContext(ctx), Timeout: timeout, FolderGuard: folderGuard, WorkingDirectory: workingDir}, sessionCfg, nil
}

func (e *Executor) handleExtensionBrowser(ctx context.Context, args map[string]interface{}, b *browserrelay.Binding) (string, error) {
	command, _ := args["command"].(string)
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "status" {
		status := b.Status()
		result := map[string]interface{}{"configured_mode": "extension", "effective_mode": "extension", "connected": status.Connected, "shared_tabs": status.Tabs, "instruction": "Chrome extension selected. Create project tabs with open or tab new; manual sharing of existing tabs is optional. Call agent_browser with ordinary commands and no --cdp. tab lists only shared tabs. Tabs stay in the background by default; set active=true on a tab selection or other command only when visible activation is needed. Console/errors are scoped to the selected shared tab. Screenshot requires an explicit project-relative path inside the granted writable workspace; /tmp and global tool_output_folder paths are outside that grant. Files/download transfer, teaching and recording are unavailable. If disconnected, ask the user to reconnect Chrome; do not use another browser."}
		agent, _ := ctx.Value(common.ChatSessionIDKey).(string)
		workflow, _ := ctx.Value(common.WorkflowSessionIDKey).(string)
		if opts, _, err := browserExecuteOptions(ctx, agent, workflow, time.Second); err == nil && opts.FolderGuard != nil {
			result["screenshot_write_paths"] = opts.FolderGuard.WritePaths
			result["working_directory"] = opts.WorkingDirectory
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	}
	allowed := map[string]bool{"open": true, "navigate": true, "snapshot": true, "click": true, "dblclick": true, "fill": true, "type": true, "press": true, "keydown": true, "keyup": true, "hover": true, "scroll": true, "scrollintoview": true, "select": true, "check": true, "uncheck": true, "get": true, "find": true, "wait": true, "tab": true, "back": true, "forward": true, "reload": true, "screenshot": true, "console": true, "errors": true, "eval": true}
	if !allowed[command] {
		return "", fmt.Errorf("CHROME_EXTENSION_UNSUPPORTED: %s is unavailable through the extension", command)
	}
	values := stringArgs(args["args"])
	full, values := extractFullSnapshotArg(values)
	for _, arg := range values {
		for _, flag := range []string{"--cdp", "--session", "--profile", "--provider", "--executable-path", "--extension", "--auto-connect", "--connect", "--engine", "--args", "--proxy", "--headers", "--state", "--user-data-dir", "--socket-dir", "--config", "--namespace", "--init-script", "--enable", "--restore", "--restore-save", "--restore-check-url", "--restore-check-text", "--restore-check-fn", "--session-name", "--download-path", "--screenshot-dir", "--ca-cert", "--action-policy", "--allowed-domains", "--debug", "--verbose", "-v", "-p"} {
			if arg == flag || strings.HasPrefix(arg, flag+"=") || (flag == "-p" && strings.HasPrefix(arg, "-p")) {
				return "", fmt.Errorf("CHROME_EXTENSION_ENDPOINT: connection and launch options are backend-owned")
			}
		}
	}
	agent, _ := ctx.Value(common.ChatSessionIDKey).(string)
	workflow, _ := ctx.Value(common.WorkflowSessionIDKey).(string)
	owner := workflow
	if owner == "" {
		owner = agent
	}
	if owner == "" {
		return "", fmt.Errorf("CHROME_EXTENSION_ACCESS: a trusted chat identity is required")
	}
	active, _ := args["active"].(bool)
	endpoint, release, err := b.AcquireForActive(ctx, owner, active)
	if err != nil {
		return "", err
	}
	defer release()
	opts, _, err := browserExecuteOptions(ctx, agent, workflow, getTimeoutForCommand(command))
	if err != nil {
		return "", err
	}
	if opts.FolderGuard != nil {
		opts.FolderGuard.BrowserSession = b.Session()
	}
	values = stripRedundantTabCommandArg(command, values)
	tab, values, err := extractInlineCDPTab(values)
	if err != nil {
		return "", err
	}
	createdFirstTab := false
	firstTabLabel := ""
	if b.Status().Tabs == 0 {
		if command == "tab" && (len(values) == 0 || (len(values) == 1 && values[0] == "list")) {
			return `{"success":true,"data":{"tabs":[]}}`, nil
		}
		createsTab := command == "open" || command == "navigate" || (command == "tab" && len(values) > 0 && values[0] == "new")
		if !createsTab {
			return "", fmt.Errorf("CHROME_EXTENSION_NO_TABS: create a project tab with tab new or open; sharing an existing tab is optional")
		}
		targetURL := "about:blank"
		hasURL := false
		if command == "tab" {
			for i := 1; i < len(values); i++ {
				if values[i] == "--label" && i+1 < len(values) {
					i++
					firstTabLabel = strings.TrimSpace(values[i])
					if firstTabLabel == "" {
						return "", fmt.Errorf("--label requires a non-empty value")
					}
					continue
				}
				if strings.HasPrefix(values[i], "-") || hasURL {
					return "", fmt.Errorf("tab new accepts one URL and an optional --label")
				}
				targetURL = values[i]
				hasURL = true
			}
		}
		parsed, parseErr := url.Parse(targetURL)
		if parseErr != nil || (targetURL != "about:blank" && (parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "")) {
			return "", fmt.Errorf("Only HTTP(S) pages and about:blank are supported")
		}
		createCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		bootstrapURL := targetURL
		if firstTabLabel != "" {
			bootstrapURL = "about:blank"
		}
		bootstrapTarget, createErr := b.CreateTarget(createCtx, bootstrapURL)
		cancel()
		if createErr != nil {
			return "", fmt.Errorf("Create project browser tab: %w", createErr)
		}
		createdFirstTab = command == "tab" && firstTabLabel == ""
		if firstTabLabel != "" {
			// Native CLI labels are written by tab new. Give it a temporary bootstrap
			// target, then let the ordinary new command create the labeled project tab.
			defer func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = b.CloseTarget(closeCtx, bootstrapTarget)
			}()
		}
	}
	// Let a fresh CLI attach to an explicitly shared tab before making its
	// binding strict. Once pinned, a closed tab must fail instead of selecting
	// another shared tab while retaining stale element references.
	listed, bootstrapErr := e.Client.ExecuteCommand(ctx, []string{"--session", b.Session(), "tab", "--cdp", endpoint, "--json"}, opts)
	if bootstrapErr != nil {
		return "", fmt.Errorf("Chrome connection failed: %s", strings.ReplaceAll(bootstrapErr.Error(), endpoint, "[private Chrome connection]"))
	}
	tabs, listErr := parseCDPTabs(listed)
	if listErr != nil {
		return "", fmt.Errorf("Chrome tab list could not be verified")
	}
	var selected cdpTabInfo
	for _, current := range tabs {
		if current.Active {
			selected = current
		}
	}
	if createdFirstTab {
		output, marshalErr := json.Marshal(map[string]interface{}{"success": true, "data": selected})
		return string(output), marshalErr
	}
	if tab != "" && command != "tab" && selected.TabID != tab && selected.Label != tab && selected.TargetID != tab {
		if _, err = e.Client.ExecuteCommand(ctx, []string{"--session", b.Session(), "tab", tab, "--cdp", endpoint, "--json"}, opts); err != nil {
			return "", fmt.Errorf("Chrome tab selection failed: %s", strings.ReplaceAll(err.Error(), endpoint, "[private Chrome connection]"))
		}
		selected = cdpTabInfo{}
		for _, current := range tabs {
			if current.TabID == tab || current.Label == tab || current.TargetID == tab {
				selected = current
				break
			}
		}
	}
	if command == "console" || command == "errors" {
		if selected.TargetID == "" {
			return "", fmt.Errorf("Select a shared tab before reading browser diagnostics")
		}
		clear := len(values) == 1 && values[0] == "--clear"
		if len(values) > 0 && !clear {
			return "", fmt.Errorf("Browser diagnostics accept only an optional --clear")
		}
		data, err := b.Diagnostics(selected.TargetID, command, clear)
		if err != nil {
			return "", err
		}
		data["tabId"] = selected.TabID
		output, err := json.Marshal(map[string]interface{}{"success": true, "data": data})
		return string(output), err
	}
	values = normalizeAgentBrowserCommandArgs(command, values)
	if command == "screenshot" && (opts.FolderGuard == nil || !opts.FolderGuard.Enabled) {
		return "", fmt.Errorf("Screenshot files require a workspace folder guard")
	}
	var artifactPlan *browserArtifactPlan
	if opts.FolderGuard != nil && opts.FolderGuard.Enabled {
		plan, err := prepareBrowserArtifact(command, values, b.Session(), b.Session())
		if err != nil {
			return "", err
		}
		if command == "screenshot" && plan == nil {
			return "", fmt.Errorf("Screenshot requires an explicit workspace output path")
		}
		if plan != nil {
			artifactPlan = plan
			defer os.Remove(plan.StagedPath)
			values = plan.RewrittenArgs
			opts.ArtifactTransfer = plan.Transfer
		}
	}
	cli := []string{"--session", b.Session(), command}
	cli = append(cli, values...)
	cli = append(cli, "--cdp", endpoint, "--pin-tab", "--json")
	output, err := e.Client.ExecuteCommand(ctx, cli, opts)
	// Never expose the private relay capability through CLI diagnostics.
	output = strings.ReplaceAll(rewriteBrowserArtifactOutput(output, artifactPlan), endpoint, "[private Chrome connection]")
	if err != nil {
		return output, fmt.Errorf("Chrome extension command failed: %s", strings.ReplaceAll(err.Error(), endpoint, "[private Chrome connection]"))
	}
	if command == "snapshot" {
		if _, err := e.handleOversizedSnapshot(ctx, &output, full); err != nil {
			return "", err
		}
	}
	return output, nil
}
