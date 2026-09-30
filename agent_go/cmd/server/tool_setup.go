package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// extractWorkspacePathFromObjective extracts the workspace path from the objective string
// Looks for pattern: "📁 Files in context: Workflow/[FolderName]"
func extractWorkspacePathFromObjective(objective string) string {
	// Look for pattern: "📁 Files in context: Workflow/[FolderName]"
	// This is the standard pattern used by workflow orchestrator
	prefix := "📁 Files in context: "
	if idx := strings.Index(objective, prefix); idx != -1 {
		// Find the start of the workspace path
		start := idx + len(prefix)
		// Find the end of the workspace path (typically before a newline or end of string)
		end := strings.Index(objective[start:], "\n")
		var line string
		if end == -1 {
			line = strings.TrimSpace(objective[start:])
		} else {
			line = strings.TrimSpace(objective[start : start+end])
		}
		if comma := strings.Index(line, ","); comma != -1 {
			line = strings.TrimSpace(line[:comma])
		}
		return line
	}
	return ""
}

// extractFileContextWriteFolders parses "📁 Files in context: path1, path2" from query string
// and returns paths that should be granted write access in the FolderGuard.
// Files (last component contains '.') are returned as-is (exact match).
// Folders are returned as-is (prefix match in isPathAllowed).
// Skips Chats/ (already handled) and root-level files (no '/' in path).
func extractFileContextWriteFolders(query string) []string {
	prefix := "📁 Files in context: "
	idx := strings.Index(query, prefix)
	if idx == -1 {
		return nil
	}

	start := idx + len(prefix)
	end := strings.Index(query[start:], "\n")
	var line string
	if end == -1 {
		line = strings.TrimSpace(query[start:])
	} else {
		line = strings.TrimSpace(query[start : start+end])
	}

	if line == "" {
		return nil
	}

	seen := make(map[string]bool)
	var result []string

	parts := strings.Split(line, ",")
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		// Clean the path
		p = filepath.Clean(p)

		// Skip protected/already-handled paths
		pLower := strings.ToLower(p)
		if strings.HasPrefix(pLower, "chats") {
			continue
		}

		// Skip root-level files (no directory component) — don't grant root write access
		if !strings.Contains(p, string(filepath.Separator)) && !strings.Contains(p, "/") {
			continue
		}

		// Deduplicate
		if seen[p] {
			continue
		}
		seen[p] = true
		result = append(result, p)
	}

	return result
}

// extractWorkflowContextFolders normalizes workflow context paths selected via the #workflow picker
// so they can participate in folder guard setup just like @context paths.
// handleQuery authorizes these paths against live workflow permissions first.
// This helper only normalizes paths; it is not an authorization boundary.
func extractWorkflowContextFolders(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result []string

	for _, raw := range paths {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}

		p = filepath.Clean(p)
		if p == "." || p == string(filepath.Separator) || p == "/" || filepath.IsAbs(p) {
			continue
		}
		if strings.HasPrefix(p, ".."+string(filepath.Separator)) || p == ".." || strings.HasPrefix(p, "../") {
			continue
		}

		pLower := strings.ToLower(p)
		if strings.HasPrefix(pLower, "chats") {
			continue
		}

		if seen[p] {
			continue
		}
		seen[p] = true
		result = append(result, p)
	}

	return result
}

// collectSplitFolderGuardFolders returns separate write and read-only folder lists.
// @file context paths get write access; #workflow context paths get read-only access.
func collectSplitFolderGuardFolders(query string, workflowContextPaths []string) (writeFolders, readOnlyFolders []string) {
	writeFolders = common.DeduplicateStrings(extractFileContextWriteFolders(query))
	readOnlyFolders = common.DeduplicateStrings(extractWorkflowContextFolders(workflowContextPaths))
	return
}

// extractRootCauseError returns the raw error message without any processing
// It unwraps the error chain to find the deepest/most specific error
func extractRootCauseError(err error) string {
	if err == nil {
		return "unknown error"
	}

	// Unwrap the error chain to find the deepest error (the actual root cause)
	currentErr := err
	deepestErr := err
	maxDepth := 20 // Limit depth to prevent infinite loops

	for i := 0; i < maxDepth; i++ {
		// Try to unwrap using errors.Unwrap
		unwrapped := errors.Unwrap(currentErr)
		if unwrapped == nil {
			break
		}
		deepestErr = unwrapped
		currentErr = unwrapped
	}

	// Return the raw error message from the deepest error (no pattern matching, no filtering)
	return deepestErr.Error()
}

// isToolBackedChatMode reports whether the request is a tool-backed chat
// session (the "multi-agent" mode that the chat UI uses) rather than a
// workflow run or a workflow-phase chat.
//
// "simple" is the legacy name kept for backward compat — old clients and
// persisted requests may still pass it. Treated as a synonym for the
// canonical "multi-agent". Empty string defaults to the same.
func isToolBackedChatMode(agentMode string) bool {
	switch strings.TrimSpace(agentMode) {
	case "", "multi-agent", "simple":
		return true
	default:
		return false
	}
}

// normalizeAgentMode canonicalizes legacy agent-mode strings to the current
// vocabulary. Use at every input boundary so downstream code only ever sees
// canonical values: "multi-agent", "workflow", or "workflow_phase".
func normalizeAgentMode(agentMode string) string {
	switch strings.TrimSpace(agentMode) {
	case "", "simple", "multi-agent":
		return "multi-agent"
	case "workflow":
		return "workflow"
	case "workflow_phase":
		return "workflow_phase"
	default:
		return strings.TrimSpace(agentMode)
	}
}

// createCustomTools creates workspace and human tools for orchestrator/workflow agents
// workflowMode: if true, includes advanced + human + todo tools for workflow mode
//
//	if false, workspace_advanced + chat-safe human tools (shell, image, web fetch, PDF, notify/submit answer)
//
// Returns: tools, executors, and a map of tool names to their categories
// Workspace registry tools get category "workspace_advanced"
// All tools from CreateHumanTools() get category "human_tools"
func createCustomTools(workflowMode bool, sessionInfo ...string) ([]llmtypes.Tool, map[string]interface{}, map[string]string) {
	// sessionInfo: optional [userID, sessionID] for session-aware workspace executors
	var userID, sessionID string
	if len(sessionInfo) >= 2 {
		userID = sessionInfo[0]
		sessionID = sessionInfo[1]
	}

	var allTools []llmtypes.Tool
	allExecutors := make(map[string]interface{})
	toolCategories := make(map[string]string)

	workspaceTools, workspaceExecutors, workspaceCategories, _ := virtualtools.CreateWorkspaceToolRegistryUntyped(virtualtools.WorkspaceToolRegistryConfig{
		WorkspaceAPIURL: getWorkspaceAPIURL(),
		UserID:          userID,
		SessionID:       sessionID,
	})
	allTools = append(allTools, workspaceTools...)
	for name, executor := range workspaceExecutors {
		allExecutors[name] = executor
	}
	for name, category := range workspaceCategories {
		toolCategories[name] = category
	}

	humanToolAllowed := map[string]bool{}
	// Build the complete human-tool pool here because workflow execution agents
	// consume it too. The primary Workflow Builder registration filters out
	// human_feedback later; execution agents retain that blocking primitive.
	for _, tool := range virtualtools.CreateHumanTools() {
		if tool.Function != nil {
			humanToolAllowed[tool.Function.Name] = true
		}
	}
	for _, tool := range virtualtools.CreateHumanTools() {
		if tool.Function != nil && humanToolAllowed[tool.Function.Name] {
			allTools = append(allTools, tool)
		}
	}
	for name, executor := range virtualtools.CreateHumanToolExecutors() {
		if !humanToolAllowed[name] {
			continue
		}
		allExecutors[name] = executor

		toolCategories[name] = "human_tools"
	}

	if workflowMode {
		workflowDBRegistry := virtualtools.CreateWorkflowDBToolRegistry(getWorkspaceAPIURL(), userID, sessionID)
		allTools = append(allTools, workflowDBRegistry.Tools...)
		for name, executor := range workflowDBRegistry.Executors {
			allExecutors[name] = executor
		}
		for name, category := range workflowDBRegistry.Categories {
			toolCategories[name] = category
		}

		// PLAT-298. The one supported way to switch this workflow's
		// code_layout_version -- see workflow_manifest.go's
		// SetWorkflowCodeLayoutVersion and code-authoring.md's "Deliberate
		// migration to code/".
		codeLayoutRegistry := virtualtools.CreateCodeLayoutToolRegistry(sessionID, SetWorkflowCodeLayoutVersion)
		allTools = append(allTools, codeLayoutRegistry.Tools...)
		for name, executor := range codeLayoutRegistry.Executors {
			allExecutors[name] = executor
		}
		for name, category := range codeLayoutRegistry.Categories {
			toolCategories[name] = category
		}

		// PLAT-184. This workflow's own per-workspace cost ledger, readable
		// through its normal folder-guard-scoped tools -- unlike the global
		// human-facing Cost Analysis ledger, which sits outside every
		// workflow's own folder and is not reachable by any agent at all.
		workflowCostsRegistry := virtualtools.CreateWorkflowCostsToolRegistry(getWorkspaceAPIURL(), userID, sessionID)
		allTools = append(allTools, workflowCostsRegistry.Tools...)
		for name, executor := range workflowCostsRegistry.Executors {
			allExecutors[name] = executor
		}
		for name, category := range workflowCostsRegistry.Categories {
			toolCategories[name] = category
		}

		reportHumanInputTools, reportHumanInputExecutors, reportHumanInputCategories := createReportHumanInputTools()
		allTools = append(allTools, reportHumanInputTools...)
		for name, executor := range reportHumanInputExecutors {
			allExecutors[name] = executor
		}
		for name, category := range reportHumanInputCategories {
			toolCategories[name] = category
		}

		goalTools, goalExecutors, goalCategories := createGoalMetricTools()
		allTools = append(allTools, goalTools...)
		for name, executor := range goalExecutors {
			allExecutors[name] = executor
		}
		for name, category := range goalCategories {
			toolCategories[name] = category
		}

		platformTools, platformExecutors, platformCategories := createPulsePlatformTools()
		allTools = append(allTools, platformTools...)
		for name, executor := range platformExecutors {
			allExecutors[name] = executor
		}
		for name, category := range platformCategories {
			toolCategories[name] = category
		}

		pulseWorklistTools, pulseWorklistExecutors, pulseWorklistCategories := createPulseWorklistTools()
		allTools = append(allTools, pulseWorklistTools...)
		for name, executor := range pulseWorklistExecutors {
			allExecutors[name] = executor
		}
		for name, category := range pulseWorklistCategories {
			toolCategories[name] = category
		}

	}

	// Note: Todo tools (create_todo, complete_todo, etc.) have been removed.
	// The todo task orchestrator manages tasks directly via shell commands.
	//
	// Note: Browser tools are NOT added unconditionally here.
	// They are added conditionally based on preset.EnableBrowserAccess in workflow initialization.
	// See the workflow initialization section where browser tools are added if enabled.

	return allTools, allExecutors, toolCategories
}

// enhanceToolDescriptionForWorkflowPhase augments workspace tool descriptions for
// workflow-builder/run/optimizer sessions. Workflow phase is not normal chat output:
// durable artifacts belong under the active workflow folder, not _users/.../Chats.
func enhanceToolDescriptionForWorkflowPhase(toolName, originalDescription, workflowFolder string) string {
	workflowFolder = filepath.Clean(strings.TrimSpace(workflowFolder))
	if workflowFolder == "" || workflowFolder == "." {
		workflowFolder = "the active Workflow/<name> folder"
	} else if !strings.HasSuffix(workflowFolder, "/") {
		workflowFolder += "/"
	}

	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
		"execute_shell_command":     true,
	}

	var accessInfo strings.Builder
	accessInfo.WriteString("\n\n📁 **DIRECTORY ACCESS RESTRICTIONS (WORKFLOW BUILDER):**")

	if writeTools[toolName] {
		accessInfo.WriteString(fmt.Sprintf("\n\n⚠️ **IMPORTANT:** Write workflow artifacts under `%s`. Raw writes under `%splanning/` are blocked; use workflow/builder tools for plan/config changes.", workflowFolder, workflowFolder))
		accessInfo.WriteString("\nUse `Downloads/` only for scratch downloads/browser artifacts. Use dedicated configuration tools for config changes; do not read or write `config/` with file tools. Do NOT write workflow artifacts to `_users/<user>/Chats/`; Chats is for normal chat sessions, not workflow state.")
	} else {
		accessInfo.WriteString(fmt.Sprintf("\n\nYou have READ access to workspace folders and workflow WRITE access under `%s` except the raw-write-blocked `planning/` subtree.", workflowFolder))
	}

	return originalDescription + accessInfo.String()
}

// enhanceToolDescriptionForMultiAgentMode augments workspace tool descriptions for multi-agent plan mode.
// chatsFolder is the full per-user path (e.g. "_users/default/Chats").
//
// multiAgentPlacementGuidance returns the artifact-layout lines for one tool.
// A product declares its own per tool in runtime.workspace.placement; a session
// with no profile falls back to AgentWorks' own layout.
//
// This used to be hardcoded and appended to every write tool alike, so Video
// Studio — which has no plan folders and does not carry the Pulse tools at all
// — was told on every get_api_spec call to save plan outputs to
// {plan_id}/output.txt and org goals to pulse/goals.html, contradicting the
// uploads//work//outputs/ layout its own system prompt had just given it.
//
// A profile that declares nothing for a tool gets no placement lines, which is
// the correct answer rather than a reason to fall back: inheriting another
// product's conventions is the bug this replaced.
func multiAgentPlacementGuidance(toolName, chatsFolder string, profile *resolvedAgentProfile) []string {
	// Only a project-scoped profile's own declared placement map applies.
	if profile != nil && !isGlobalScopedProfile(profile) {
		return profile.Definition.Runtime.Workspace.Placement[toolName]
	}
	switch toolName {
	case "diff_patch_workspace_file", "execute_shell_command":
		return []string{
			fmt.Sprintf("Save plan outputs inside the plan folder (e.g. '%s/{plan_id}/output.txt').", chatsFolder),
		}
	}
	return nil
}

// enhanceToolDescriptionForMultiAgentMode augments workspace tool descriptions for multi-agent plan mode.
// chatsFolder is the full per-user path (e.g. "_users/default/Chats").
// profile is the active agent profile, nil for a plain AgentWorks session.
func enhanceToolDescriptionForMultiAgentMode(toolName, originalDescription, chatsFolder string, profile *resolvedAgentProfile) string {
	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
		"execute_shell_command":     true,
	}

	var accessInfo strings.Builder
	accessInfo.WriteString("\n\n📁 **DIRECTORY ACCESS RESTRICTIONS (MULTI-AGENT MODE):**")

	if writeTools[toolName] {
		accessInfo.WriteString(fmt.Sprintf("\n\n⚠️ **IMPORTANT:** You can write to '%s/' (primary). All other folders are read-only unless explicitly allowed. Use dedicated configuration tools for config changes; do not read or write `config/` with file tools.", chatsFolder))
		for _, line := range multiAgentPlacementGuidance(toolName, chatsFolder, profile) {
			accessInfo.WriteString("\n" + line)
		}
	} else {
		// Read-only tools name the writable scopes so the model knows where a
		// companion write tool may put things. Only AgentWorks' own layout adds
		// pulse/ here; a product's writable scope is its project folder.
		writable := fmt.Sprintf("`%s/`", chatsFolder)
		if profile == nil || isGlobalScopedProfile(profile) {
			writable = fmt.Sprintf("`%s/`, `pulse/`,", chatsFolder)
		}
		accessInfo.WriteString(fmt.Sprintf("\n\nYou have READ access to allowed workspace folders. WRITE access is restricted to %s and any explicitly allowed subfolders. Configuration data is available through dedicated tools.", writable))
	}

	return originalDescription + accessInfo.String()
}

type folderGuardContextMode int

const (
	folderGuardContextChat folderGuardContextMode = iota
	folderGuardContextWorkflow
)

func cleanFolderGuardFolders(folders []string) []string {
	seen := make(map[string]bool, len(folders))
	cleanedFolders := make([]string, 0, len(folders))
	for _, folder := range folders {
		folder = strings.TrimSpace(folder)
		if folder == "" {
			continue
		}
		cleaned := filepath.Clean(folder)
		if cleaned == "" || cleaned == "." {
			continue
		}
		key := strings.ToLower(filepath.ToSlash(cleaned))
		if seen[key] {
			continue
		}
		seen[key] = true
		cleanedFolders = append(cleanedFolders, cleaned)
	}
	return cleanedFolders
}

func isChatsWriteFolder(folder string) bool {
	cleaned := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(folder))))
	if cleaned == "" || cleaned == "." {
		return false
	}
	if cleaned == "chats" || strings.HasPrefix(cleaned, "chats/") {
		return true
	}

	parts := strings.Split(cleaned, "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "_users" && parts[i+2] == "chats" {
			return true
		}
	}
	return false
}

func isExactFolderGuardFilePath(folder string) bool {
	base := filepath.Base(filepath.Clean(strings.TrimSpace(folder)))
	return strings.Contains(base, ".")
}

func isPathAllowedByFolderGuard(cleanedPath, allowedPath string) bool {
	allowedClean := filepath.Clean(allowedPath)
	if cleanedPath == allowedClean {
		return true
	}
	if isExactFolderGuardFilePath(allowedPath) {
		return false
	}
	return strings.HasPrefix(cleanedPath, allowedClean+"/") ||
		strings.HasPrefix(cleanedPath, allowedClean+"\\")
}

func chatModeWriteFolders(additionalWriteFolders ...string) []string {
	allowedWriteFolders := []string{"Downloads/"}
	allowedWriteFolders = append(allowedWriteFolders, additionalWriteFolders...)
	return cleanFolderGuardFolders(allowedWriteFolders)
}

func workflowPhaseWriteFolders(workflowFolder string, additionalWriteFolders ...string) []string {
	allowedWriteFolders := []string{"Downloads/"}
	workflowFolder = strings.TrimSpace(workflowFolder)
	if workflowFolder != "" {
		allowedWriteFolders = append(allowedWriteFolders, workflowFolder)
	}
	for _, folder := range additionalWriteFolders {
		if isChatsWriteFolder(folder) {
			continue
		}
		allowedWriteFolders = append(allowedWriteFolders, folder)
	}
	return cleanFolderGuardFolders(allowedWriteFolders)
}

// wrapExecutorsWithChatModeFolderGuard wraps workspace tool executors to restrict chat mode writes.
// The default writable folder is Downloads/ only — the per-user Chats folder is supplied by callers
// via additionalWriteFolders so each session writes only to its own _users/<id>/Chats/ subtree.
// Pass additionalWriteFolders to allow extra folders (e.g. "_users/<id>/Chats/", "skills/custom/").
// Pass blockedWriteFolders to deny writes to specific paths within otherwise-allowed prefixes.
// Reads remain allowed on blockedWriteFolders.
func wrapExecutorsWithChatModeFolderGuard(executors map[string]func(ctx context.Context, args map[string]interface{}) (string, error), readOnlyFolders []string, blockedWriteFolders []string, additionalWriteFolders ...string) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	return wrapExecutorsWithFolderGuard(
		executors,
		"CHAT MODE FOLDER GUARD",
		"CHAT FOLDER GUARD WRAPPER",
		folderGuardContextChat,
		readOnlyFolders,
		blockedWriteFolders,
		chatModeWriteFolders(additionalWriteFolders...),
	)
}

// wrapExecutorsWithWorkflowPhaseFolderGuard wraps workspace tool executors for workflow-builder
// phases. Workflow-builder writes are independent from normal Chats storage: durable workflow
// artifacts go under the active Workflow/<name> folder, while _users/<id>/Chats remains read-only.
func wrapExecutorsWithWorkflowPhaseFolderGuard(executors map[string]func(ctx context.Context, args map[string]interface{}) (string, error), workflowFolder string, readOnlyFolders []string, blockedWriteFolders []string, additionalWriteFolders ...string) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	return wrapExecutorsWithFolderGuard(
		executors,
		"WORKFLOW PHASE FOLDER GUARD",
		"WORKFLOW FOLDER GUARD WRAPPER",
		folderGuardContextWorkflow,
		readOnlyFolders,
		blockedWriteFolders,
		workflowPhaseWriteFolders(workflowFolder, additionalWriteFolders...),
	)
}

// wrapExecutorsWithFolderGuard is the shared low-level path check. Mode-specific
// wrappers above own the policy: which roots are writable and which context key
// is injected for shell execution.
func wrapExecutorsWithFolderGuard(executors map[string]func(ctx context.Context, args map[string]interface{}) (string, error), logPrefix string, wrapperLogPrefix string, contextMode folderGuardContextMode, readOnlyFolders []string, blockedWriteFolders []string, allowedWriteFolders []string) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	// No protected folders — all users share the same filesystem
	protectedFolders := []string{}

	// blockedWritePrefixes denies writes (tool path params + shell write patterns) even when
	// the path is under an allowed write prefix. Workflow-builder uses this to block raw writes
	// to Workflow/<name>/planning/ while keeping the rest of the workflow writable.
	blockedWritePrefixes := make([]string, 0, len(blockedWriteFolders))
	for _, f := range blockedWriteFolders {
		cleaned := filepath.Clean(f)
		if cleaned != "" && cleaned != "." {
			blockedWritePrefixes = append(blockedWritePrefixes, cleaned)
		}
	}
	allowedWriteFolders = cleanFolderGuardFolders(allowedWriteFolders)

	// For shell sandboxing, pass all allowed write folders
	shellAllowedFolders := make([]string, len(allowedWriteFolders))
	copy(shellAllowedFolders, allowedWriteFolders)

	// Write tools that should be restricted
	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
	}

	// Path parameters to check for all tools (both read and write)
	allPathParams := []string{"filepath", "source_filepath", "destination_filepath", "folder", "pattern"}

	// Path parameters specific to write operations
	writePathParams := []string{"filepath", "source_filepath", "destination_filepath"}

	// Helper: check if a cleaned path is within a protected folder
	isPathProtected := func(cleanedPath string) bool {
		pathLower := strings.ToLower(cleanedPath)
		for _, folder := range protectedFolders {
			folderLower := strings.ToLower(folder)
			if pathLower == folderLower ||
				strings.HasPrefix(pathLower, folderLower+"/") ||
				strings.HasPrefix(pathLower, folderLower+"\\") {
				return true
			}
		}
		return false
	}

	// Helper: check if a cleaned path is within any allowed write folder
	isPathAllowed := func(cleanedPath string) bool {
		for _, folder := range allowedWriteFolders {
			if isPathAllowedByFolderGuard(cleanedPath, folder) {
				return true
			}
		}
		return false
	}

	// Helper: check if a cleaned path is under any blocked-write prefix. Reads remain
	// allowed; this is the exception-list that pairs with broad write prefixes like
	// Workflow/<name>/ so planning/ stays non-writable even inside the workflow folder.
	isPathBlockedWrite := func(cleanedPath string) bool {
		for _, prefix := range blockedWritePrefixes {
			if cleanedPath == prefix ||
				strings.HasPrefix(cleanedPath, prefix+"/") ||
				strings.HasPrefix(cleanedPath, prefix+"\\") {
				return true
			}
		}
		return false
	}

	wrappedExecutors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	for toolName, executor := range executors {
		toolNameCopy := toolName
		originalExecutor := executor

		wrappedExecutor := func(ctx context.Context, args map[string]interface{}) (string, error) {
			// FIRST: Check for protected folder access (applies to ALL tools)
			for _, paramName := range allPathParams {
				if paramValue, exists := args[paramName]; exists {
					if pathStr, ok := paramValue.(string); ok && pathStr != "" {
						cleanedPath := filepath.Clean(pathStr)
						if isPathProtected(cleanedPath) {
							log.Printf("[%s] Blocked access to protected folder '%s' for tool %s", logPrefix, pathStr, toolNameCopy)
							return "", fmt.Errorf("access denied: '%s' is a protected system folder", pathStr)
						}
					}
				}
			}

			// For shell commands, check for protected folder references and restrict writes
			if toolNameCopy == "execute_shell_command" {
				if cmdValue, exists := args["command"]; exists {
					if cmdStr, ok := cmdValue.(string); ok {
						cmdLower := strings.ToLower(cmdStr)

						// Check if shell command references protected folders
						for _, folder := range protectedFolders {
							folderLower := strings.ToLower(folder)
							if strings.Contains(cmdLower, folderLower+"/") ||
								strings.Contains(cmdLower, folderLower+" ") ||
								strings.Contains(cmdLower, " "+folderLower) ||
								strings.Contains(cmdLower, "/"+folderLower) ||
								strings.HasSuffix(cmdLower, folderLower) {
								log.Printf("[%s] Blocked shell command referencing protected folder %s: %s", logPrefix, folder, cmdStr)
								return "", fmt.Errorf("access denied: shell commands cannot reference '%s/' folder (protected system folder)", folder)
							}
						}

					}
				}
				// Inject allowed write folders for kernel-level sandboxing
				if contextMode == folderGuardContextWorkflow {
					ctx = context.WithValue(ctx, common.FolderGuardWritePathsKey, shellAllowedFolders)
				} else {
					ctx = context.WithValue(ctx, common.FolderGuardAllowedWriteFolderKey, shellAllowedFolders)
				}
				readFolders := []string{"Downloads/", "skills/", "subagents/", "Workflow/"}
				readFolders = append(readFolders, shellAllowedFolders...)
				readFolders = append(readFolders, readOnlyFolders...)
				readFolders = cleanFolderGuardFolders(readFolders)
				ctx = context.WithValue(ctx, common.FolderGuardReadPathsKey, readFolders)
				// Default working directory for guarded shell modes — workspace root
				ctx = context.WithValue(ctx, common.DefaultWorkingDirKey, "")
				fmt.Printf("[%s] Injected write paths=%v read paths=%v for %s\n", wrapperLogPrefix, shellAllowedFolders, readFolders, toolNameCopy)
			}

			// For WRITE tools (diff_patch_workspace_file primarily), check blocked-write
			// prefixes first and then allowed-write prefixes. Shell commands are handled
			// via the isolator's BlockedPaths (kernel-level enforcement, set up at
			// SetSessionFolderGuard call site) — no string-scanning needed here.
			if writeTools[toolNameCopy] {
				targetArgs, targetParams := writeTargetArgs(args, writePathParams)
				for _, paramName := range targetParams {
					if paramValue, exists := targetArgs[paramName]; exists {
						if pathStr, ok := paramValue.(string); ok && pathStr != "" {
							cleanedPath := filepath.Clean(pathStr)

							// Allow entries are workspace-relative ("Workflow/social-media"),
							// so an absolute argument could never prefix-match and was denied
							// even when it pointed inside an allowed folder. The workspace
							// client normalizes the same way, but only after this guard runs,
							// so it never got the chance. Prompts actively encourage absolute
							// paths, which is how a legitimate write to
							// A retired Pulse HTML path was rejected against
							// an allow list that contained Workflow/social-media.
							if relPath, ok := orchestrator.NormalizeAbsoluteWorkspaceDocsPath(cleanedPath); ok {
								cleanedPath = filepath.Clean(relPath)
							}

							if isPathBlockedWrite(cleanedPath) {
								log.Printf("[%s] Blocked WRITE to '%s' (cleaned: '%s') for tool %s — path is under a blocked-write prefix (%v)", logPrefix, pathStr, cleanedPath, toolNameCopy, blockedWritePrefixes)
								return "", fmt.Errorf("access denied: '%s' is under a blocked-write prefix (%v) — this folder is read-only even though its parent is writable%s", pathStr, blockedWritePrefixes, readOnlyRefusalHint(ctx))
							}

							if !isPathAllowed(cleanedPath) {
								log.Printf("[%s] Blocked WRITE to '%s' (cleaned: '%s') for tool %s - allowed folders: %v", logPrefix, pathStr, cleanedPath, toolNameCopy, allowedWriteFolders)
								return "", fmt.Errorf("access denied: cannot write to '%s' (allowed write folders: %v)%s", pathStr, allowedWriteFolders, readOnlyRefusalHint(ctx))
							}
						}
					}
				}
			}

			// Call original executor
			return originalExecutor(ctx, args)
		}

		wrappedExecutors[toolNameCopy] = wrappedExecutor
	}

	log.Printf("[%s] Wrapped %d executors - protected folders: %v, allowed write folders: %v", logPrefix, len(wrappedExecutors), protectedFolders, allowedWriteFolders)
	return wrappedExecutors
}

// wrapExecutorsWithPlanFolderGuard wraps workspace tool executors to restrict writes to a specific plan folder.
// Like wrapExecutorsWithChatModeFolderGuard but uses the plan folder (e.g. "Chats/{planID}")
// instead of the whole "Chats/" tree as the allowed write folder. This ensures sub-agents only write to their assigned plan folder.
func wrapExecutorsWithPlanFolderGuard(executors map[string]func(ctx context.Context, args map[string]interface{}) (string, error), planFolder string, readOnlyFolders []string, additionalWriteFolders ...string) map[string]func(ctx context.Context, args map[string]interface{}) (string, error) {
	protectedFolders := []string{}

	// Use the plan folder as the allowed write folder (instead of Chats/).
	// An empty plan folder is "no write root" (the same convention the
	// workflow-phase guard uses for read-only identities): only the
	// additional folders stay writable, and reads come from
	// readOnlyFolders. Callers pass the project root through readOnlyFolders
	// in that case so the turn keeps inspecting it.
	allowedWriteFolders := []string{}
	if strings.TrimSpace(planFolder) != "" {
		allowedWriteFolders = append(allowedWriteFolders, strings.TrimSuffix(planFolder, "/")+"/")
	}
	allowedWriteFolders = append(allowedWriteFolders, additionalWriteFolders...)

	shellAllowedFolders := make([]string, len(allowedWriteFolders))
	copy(shellAllowedFolders, allowedWriteFolders)

	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
	}

	allPathParams := []string{"filepath", "source_filepath", "destination_filepath", "folder", "pattern"}
	writePathParams := []string{"filepath", "source_filepath", "destination_filepath"}

	isPathProtected := func(cleanedPath string) bool {
		pathLower := strings.ToLower(cleanedPath)
		for _, folder := range protectedFolders {
			folderLower := strings.ToLower(folder)
			if pathLower == folderLower || strings.HasPrefix(pathLower, folderLower+"/") {
				return true
			}
		}
		return false
	}

	isWriteAllowed := func(cleanedPath string) bool {
		pathLower := strings.ToLower(cleanedPath)
		for _, folder := range allowedWriteFolders {
			if isPathAllowedByFolderGuard(pathLower, strings.ToLower(folder)) {
				return true
			}
		}
		return false
	}

	wrappedExecutors := make(map[string]func(ctx context.Context, args map[string]interface{}) (string, error))

	for toolName, executor := range executors {
		toolNameCopy := toolName
		originalExecutor := executor

		wrappedExecutor := func(ctx context.Context, args map[string]interface{}) (string, error) {
			if toolNameCopy == "execute_shell_command" {
				ctx = context.WithValue(ctx, common.FolderGuardAllowedWriteFolderKey, shellAllowedFolders)
				// Set mode-specific read paths so the shell isolator scopes reads
				// to the relevant folders instead of the entire workspace (".").
				// The write folder is always readable; add common shared folders
				// (skills, subagents) for plan mode so sub-agents can read resources.
				shellReadFolders := make([]string, 0, len(shellAllowedFolders)+2+len(readOnlyFolders))
				shellReadFolders = append(shellReadFolders, shellAllowedFolders...)
				shellReadFolders = append(shellReadFolders, readOnlyFolders...)
				// For chat-backed plan mode (planFolder starts with "Chats"), add shared resources.
				// For prototype mode (planFolder starts with "Projects/"), the project
				// folder is self-contained — no extra reads needed.
				if strings.HasPrefix(planFolder, "Chats") {
					shellReadFolders = append(shellReadFolders, "skills/", "subagents/", "Downloads/")
				}
				ctx = context.WithValue(ctx, common.FolderGuardReadPathsKey, shellReadFolders)
				// Inject the session-level default working directory so execute_shell_command
				// can substitute it when the LLM passes ".".
				// planFolder is "Projects/{name}" for prototype mode, "Chats/{name}" for plan mode.
				// execute_shell_command reads this via DefaultWorkingDirKey (execute_shell_command.go).
				defaultDir := strings.TrimSuffix(planFolder, "/")
				ctx = context.WithValue(ctx, common.DefaultWorkingDirKey, defaultDir)
			}

			if writeTools[toolNameCopy] {
				targetArgs, targetParams := writeTargetArgs(args, writePathParams)
				for _, paramName := range targetParams {
					if pathValue, exists := targetArgs[paramName]; exists {
						if pathStr, ok := pathValue.(string); ok {
							cleanedPath := filepath.Clean(pathStr)
							if !isWriteAllowed(cleanedPath) {
								// Seen live: a model retried this same rejection 5 times in a
								// row with different guesses (a bare relative path, the real
								// host filesystem path, "./..." ) without ever landing on the
								// one thing that actually works -- prefixing its intended
								// relative path with the allowed folder shown here. Spell out
								// that combination concretely so the next call gets it right
								// on the first retry instead of guessing again.
								suggestion := ""
								if len(allowedWriteFolders) > 0 {
									relativePart := strings.TrimPrefix(strings.TrimPrefix(cleanedPath, "/"), "./")
									suggestion = fmt.Sprintf(" Prefix your intended path with the allowed folder, e.g. %q.", strings.TrimSuffix(allowedWriteFolders[0], "/")+"/"+relativePart)
								}
								return "", fmt.Errorf("access denied: writes restricted to %v (got: %s).%s", allowedWriteFolders, cleanedPath, suggestion)
							}
						}
					}
				}
			}

			for _, paramName := range allPathParams {
				if pathValue, exists := args[paramName]; exists {
					if pathStr, ok := pathValue.(string); ok {
						cleanedPath := filepath.Clean(pathStr)
						if isPathProtected(cleanedPath) {
							return "", fmt.Errorf("access denied: cannot access protected folder")
						}
					}
				}
			}

			return originalExecutor(ctx, args)
		}

		wrappedExecutors[toolNameCopy] = wrappedExecutor
	}

	log.Printf("[PLAN FOLDER GUARD] Wrapped %d executors - writes restricted to: %v", len(wrappedExecutors), allowedWriteFolders)
	return wrappedExecutors
}

// extractStepSummary parses plan JSON and returns a compact step summary string.
// Format: "1. step-id [type] - Title\n2. ..."
// For todo_task steps, also lists sub-agent routes indented.
func extractStepSummary(planJSON string) string {
	var plan struct {
		Steps []json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return ""
	}
	if len(plan.Steps) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, raw := range plan.Steps {
		var step struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
			// For todo_task steps
			PredefinedRoutes []struct {
				RouteID      string `json:"route_id"`
				SubAgentStep struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"sub_agent_step"`
			} `json:"predefined_routes,omitempty"`
		}
		if err := json.Unmarshal(raw, &step); err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("%d. `%s` [%s] — %s\n", i+1, step.ID, step.Type, step.Title))

		// Show sub-agents for todo_task steps
		for _, route := range step.PredefinedRoutes {
			if route.SubAgentStep.ID != "" {
				sb.WriteString(fmt.Sprintf("   ↳ `%s` — %s\n", route.SubAgentStep.ID, route.SubAgentStep.Title))
			}
		}
	}
	return sb.String()
}

// writeTargetArgs returns the path arguments a write check must cover: the
// named path parameters, plus every file a multi-file "*** Begin Patch" in
// args["diff"] names (diff_patch_workspace_file writes those too, and a check
// of filepath alone would let them past blocked-write prefixes such as a
// non-owner's workflow.json).
func writeTargetArgs(args map[string]interface{}, params []string) (map[string]interface{}, []string) {
	targets := workspace.DiffPatchTargetPaths(args)
	if len(targets) == 0 {
		return args, params
	}
	merged := make(map[string]interface{}, len(args)+len(targets))
	for key, value := range args {
		merged[key] = value
	}
	names := append([]string(nil), params...)
	for i, path := range targets {
		key := fmt.Sprintf("patch_target_%d", i)
		merged[key] = path
		names = append(names, key)
	}
	return merged, names
}
