package orchestrator

import (
	"context"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"os"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// ShouldFilterWriteTool checks if a write tool should be filtered out (not registered)
// Returns true if the tool is a write tool and there's no write access (folder guard enabled but no write paths)
func (bo *BaseOrchestrator) ShouldFilterWriteTool(toolName string) bool {
	return shouldFilterWriteToolWithPaths(bo.folderGuardReadPaths, bo.folderGuardWritePaths, toolName)
}

// ShouldFilterWriteToolWithPaths is like ShouldFilterWriteTool but uses explicit paths
// instead of the shared orchestrator state. Used when per-agent paths are available.
func shouldFilterWriteToolWithPaths(readPaths, writePaths []string, toolName string) bool {
	useFolderGuardPaths := len(readPaths) > 0 || len(writePaths) > 0
	if !useFolderGuardPaths {
		return false
	}

	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
	}

	if writeTools[toolName] && len(writePaths) == 0 {
		return true
	}

	return false
}

// EnhanceToolDescriptionWithFolderGuard enhances a tool description with directory access information
// based on folder guard settings. Returns the original description if folder guard is disabled.
func (bo *BaseOrchestrator) EnhanceToolDescriptionWithFolderGuard(toolName, originalDescription string) string {
	// Special tools that don't operate on specific directories - skip directory access restrictions
	// GitHub tools operate on the entire workspace/repository, not specific file paths
	// Human feedback is an interactive tool that doesn't use file paths
	// Note: human_feedback may be included in WorkspaceTools (combined in server.go createCustomTools)
	specialTools := map[string]bool{
		"human_feedback": true,
	}
	if specialTools[toolName] {
		return originalDescription
	}

	// Check if folder guard paths are set
	useFolderGuardPaths := len(bo.folderGuardReadPaths) > 0 || len(bo.folderGuardWritePaths) > 0
	workspacePath := bo.GetWorkspacePath()

	// If no folder guard paths and no workspace path, return original description
	if !useFolderGuardPaths && workspacePath == "" {
		return originalDescription
	}

	// Tool classification (same as in WrapWorkspaceToolsWithFolderGuard)
	readOnlyTools := map[string]bool{
		"execute_shell_command": true,
		"read_image":            true,
	}

	writeTools := map[string]bool{
		"diff_patch_workspace_file": true,
	}

	// Determine tool type
	isReadOnly := readOnlyTools[toolName]
	isWrite := writeTools[toolName]

	// Build directory access information with clear LLM instructions
	var accessInfo strings.Builder
	accessInfo.WriteString("\n\n📁 **DIRECTORY ACCESS RESTRICTIONS:**")

	if useFolderGuardPaths {
		if isWrite {
			// Write operations use writePaths only
			if len(bo.folderGuardWritePaths) > 0 {
				accessInfo.WriteString("\n\n⚠️ **IMPORTANT:** You can ONLY write to these directories. All file paths in your tool calls must be within these directories:\n")
				accessInfo.WriteString(strings.Join(bo.folderGuardWritePaths, "\n"))
				accessInfo.WriteString("\n\nUse ONLY these directories when calling this tool. Paths outside these directories will be rejected.")
			} else {
				accessInfo.WriteString("\n\n⚠️ **RESTRICTED:** You have NO write access to restricted directories.")
			}
		} else if isReadOnly {
			// Read operations can use both readPaths AND writePaths
			// Combine readPaths and writePaths, removing duplicates
			allowedPathsMap := make(map[string]bool)
			for _, path := range bo.folderGuardReadPaths {
				allowedPathsMap[path] = true
			}
			for _, path := range bo.folderGuardWritePaths {
				allowedPathsMap[path] = true
			}
			// Convert map back to slice
			allowedPaths := make([]string, 0, len(allowedPathsMap))
			for path := range allowedPathsMap {
				allowedPaths = append(allowedPaths, path)
			}
			if len(allowedPaths) > 0 {
				accessInfo.WriteString("\n\n⚠️ **IMPORTANT:** You can ONLY read from these directories. All file/folder paths in your tool calls must be within these directories:\n")
				accessInfo.WriteString(strings.Join(allowedPaths, "\n"))
				accessInfo.WriteString("\n\nUse ONLY these directories when calling this tool. Paths outside these directories will be rejected.")
			} else {
				accessInfo.WriteString("\n\n⚠️ **RESTRICTED:** You have NO read access to restricted directories.")
			}
		} else {
			// Unknown tool type - show both read and write paths
			if len(bo.folderGuardReadPaths) > 0 || len(bo.folderGuardWritePaths) > 0 {
				accessInfo.WriteString("\n\n⚠️ **IMPORTANT:** You can ONLY access these directories. All paths in your tool calls must be within these directories:\n")
				if len(bo.folderGuardReadPaths) > 0 {
					accessInfo.WriteString("\n**Read access:**\n")
					accessInfo.WriteString(strings.Join(bo.folderGuardReadPaths, "\n"))
				}
				if len(bo.folderGuardWritePaths) > 0 {
					accessInfo.WriteString("\n**Write access:**\n")
					accessInfo.WriteString(strings.Join(bo.folderGuardWritePaths, "\n"))
				}
				accessInfo.WriteString("\n\nUse ONLY these directories when calling this tool. Paths outside these directories will be rejected.")
			} else {
				accessInfo.WriteString("\n\n⚠️ **RESTRICTED:** You have NO access to restricted directories.")
			}
		}
	} else {
		// Fallback to workspacePath (single path mode)
		if workspacePath != "" {
			accessInfo.WriteString("\n\n⚠️ **IMPORTANT:** You can ONLY access files within this workspace directory:\n")
			accessInfo.WriteString(workspacePath)
			accessInfo.WriteString("\n\nUse ONLY paths within this workspace when calling this tool.")
		} else {
			// No restrictions - don't add confusing message
			return originalDescription
		}
	}

	return originalDescription + accessInfo.String()
}

// WrapWorkspaceToolsWithFolderGuard wraps workspace tool executors with path validation
// Uses folderGuardReadPaths and folderGuardWritePaths if set, otherwise falls back to workspacePath
// IMPORTANT: Snapshots folder guard paths at wrap time (not at call time) to prevent race conditions
// when multiple parallel agents share the same orchestrator instance.
func (bo *BaseOrchestrator) WrapWorkspaceToolsWithFolderGuard(executors map[string]interface{}) map[string]interface{} {
	// Snapshot folder guard paths at wrap time to avoid race conditions with parallel sub-agents.
	snapshotReadPaths := make([]string, len(bo.folderGuardReadPaths))
	copy(snapshotReadPaths, bo.folderGuardReadPaths)
	snapshotWritePaths := make([]string, len(bo.folderGuardWritePaths))
	copy(snapshotWritePaths, bo.folderGuardWritePaths)

	return bo.wrapWorkspaceToolsWithPaths(snapshotReadPaths, snapshotWritePaths, executors)
}

// WrapWorkspaceToolsWithExplicitPaths wraps workspace tool executors with path validation
// using explicitly provided paths instead of the shared orchestrator state.
// Used when per-agent folder guard paths are available (e.g., from OrchestratorAgentConfig).
func (bo *BaseOrchestrator) WrapWorkspaceToolsWithExplicitPaths(readPaths, writePaths []string, executors map[string]interface{}) map[string]interface{} {
	return bo.wrapWorkspaceToolsWithPaths(readPaths, writePaths, executors)
}

// wrapWorkspaceToolsWithPaths is the shared implementation for folder guard wrapping.
func (bo *BaseOrchestrator) wrapWorkspaceToolsWithPaths(snapshotReadPaths, snapshotWritePaths []string, executors map[string]interface{}) map[string]interface{} {
	// Check if folder guard paths are set
	useFolderGuardPaths := len(snapshotReadPaths) > 0 || len(snapshotWritePaths) > 0
	workspacePath := bo.GetWorkspacePath()

	// If no folder guard paths and no workspace path, return executors unchanged
	if !useFolderGuardPaths && workspacePath == "" {
		return executors
	}

	// Tools that need path validation with their parameter names
	// Classify as read-only or write operations
	readOnlyTools := map[string][]string{
		"execute_shell_command": {},
		"read_image":            {"filepath"},
	}

	writeTools := map[string][]string{
		"diff_patch_workspace_file": {"filepath"},
	}

	// Combine all tools for iteration
	toolsToValidate := make(map[string][]string)
	for tool, params := range readOnlyTools {
		toolsToValidate[tool] = params
	}
	for tool, params := range writeTools {
		toolsToValidate[tool] = params
	}

	wrappedExecutors := make(map[string]interface{})

	for toolName, executor := range executors {
		paramsToValidate, needsValidation := toolsToValidate[toolName]

		if !needsValidation {
			// Tool doesn't need validation - pass through unchanged
			wrappedExecutors[toolName] = executor
			continue
		}

		// Type assert executor to function type
		originalExecutor, ok := executor.(func(ctx context.Context, args map[string]interface{}) (string, error))
		if !ok {
			// Type assertion failed - pass through unchanged
			wrappedExecutors[toolName] = executor
			continue
		}

		// Determine if this is a read-only or write tool
		_, isReadOnly := readOnlyTools[toolName]
		_, isWrite := writeTools[toolName]

		// Create wrapper function with proper variable capture
		toolNameCopy := toolName
		paramsToValidateCopy := paramsToValidate
		isReadOnlyCopy := isReadOnly
		isWriteCopy := isWrite
		wrappedExecutor := func(ctx context.Context, args map[string]interface{}) (string, error) {
			// Determine which paths to use for validation
			// Uses snapshotted paths (captured at wrap time) to prevent race conditions
			// when parallel sub-agents share the same orchestrator instance
			var allowedPaths []string

			if useFolderGuardPaths {
				if isWriteCopy {
					// Write operations use writePaths only
					allowedPaths = snapshotWritePaths
				} else if isReadOnlyCopy {
					// Read operations can use both readPaths AND writePaths (if you can write, you can read)
					// Combine readPaths and writePaths, removing duplicates
					allowedPathsMap := make(map[string]bool)
					for _, path := range snapshotReadPaths {
						allowedPathsMap[path] = true
					}
					for _, path := range snapshotWritePaths {
						allowedPathsMap[path] = true
					}
					// Convert map back to slice
					allowedPaths = make([]string, 0, len(allowedPathsMap))
					for path := range allowedPathsMap {
						allowedPaths = append(allowedPaths, path)
					}
				} else {
					// Unknown tool type - use readPaths + writePaths as default (read-like behavior)
					allowedPathsMap := make(map[string]bool)
					for _, path := range snapshotReadPaths {
						allowedPathsMap[path] = true
					}
					for _, path := range snapshotWritePaths {
						allowedPathsMap[path] = true
					}
					allowedPaths = make([]string, 0, len(allowedPathsMap))
					for path := range allowedPathsMap {
						allowedPaths = append(allowedPaths, path)
					}
				}
			} else {
				// Fallback to workspacePath (single path mode)
				if workspacePath != "" {
					allowedPaths = []string{workspacePath}
				}
			}

			// Validate and normalize all path parameters
			for _, paramName := range paramsToValidateCopy {
				if paramValue, exists := args[paramName]; exists {
					if pathStr, ok := paramValue.(string); ok {
						// Empty string or "." means workspace root
						if pathStr == "" || pathStr == "." {
							// A "*** Begin Patch" names its own files; the workspace
							// client resolves and guard-checks each of them.
							if pathStr == "" && paramName == "filepath" && toolNameCopy == "diff_patch_workspace_file" && len(workspace.DiffPatchTargetPaths(args)) > 0 {
								continue
							}
							// When folder guard is active with specific allowed paths,
							// reject empty/root paths to prevent unrestricted workspace access
							if useFolderGuardPaths && len(allowedPaths) > 0 {
								return "", fmt.Errorf("parameter '%s' cannot be empty when folder guard is enabled for tool %s; you must specify a path within the allowed directories: %v", paramName, toolNameCopy, allowedPaths)
							}
							args[paramName] = ""
							continue
						}

						// Normalize the path (strip absolute workspace prefixes, make relative for API)
						// Path validation is handled at the isolator/shell level, not here
						normalizedPath, _, err := normalizePathForAllowedPaths(allowedPaths, pathStr)
						if err != nil {
							// Normalization failed — try stripping common absolute prefixes as fallback.
							// LLM-generated tool args may contain absolute paths (e.g. "/app/workspace-docs/Workflow/...")
							// that need to be converted to relative paths for the workspace API.
							// WORKSPACE_DOCS_PATH is checked first for desktop deployments where the
							// workspace root is a native Mac path instead of the Docker default.
							knownPrefixes := []string{"/app/workspace-docs/", "/workspace-docs/"}
							if envRoot := os.Getenv("WORKSPACE_DOCS_PATH"); envRoot != "" {
								knownPrefixes = append([]string{strings.TrimSuffix(envRoot, "/") + "/"}, knownPrefixes...)
							}
							for _, prefix := range knownPrefixes {
								if strings.HasPrefix(pathStr, prefix) {
									normalizedPath = strings.TrimPrefix(pathStr, prefix)
									err = nil
									break
								}
							}
							if err != nil {
								bo.GetLogger().Warn(fmt.Sprintf("⚠️ Path normalization failed for tool %s, parameter %s: %v", toolNameCopy, paramName, err))
								return "", err
							}
						}
						// Update the args with normalized path
						args[paramName] = normalizedPath
					}
				} else if useFolderGuardPaths && len(allowedPaths) > 0 {
					// Path parameter not provided - reject if folder guard is active
					// to prevent unrestricted workspace-root access
					return "", fmt.Errorf("parameter '%s' is required when folder guard is enabled for tool %s; you must specify a path within the allowed directories: %v", paramName, toolNameCopy, allowedPaths)
				}
			}

			// Block write tools from modifying system-managed planning files.
			// plan.json, step_config.json, and workflow_layout.json must only be modified
			// via dedicated tools (update_scripted_step, update_step_config, etc.) that
			// serialize the full struct to JSON. Raw file writes (diff_patch)
			// can corrupt these files — e.g. when diff hunks fail to match and the
			// fallback appends fragments, producing invalid JSON.
			if isWriteCopy {
				for _, paramName := range paramsToValidateCopy {
					if pathStr, ok := args[paramName].(string); ok && isProtectedPlanningPath(pathStr) {
						return "", fmt.Errorf("ACCESS DENIED: %q is a system-managed planning file. Use the dedicated plan/step update tools (update_scripted_step, update_step_config, etc.) instead of %s. For workflow objective + success criteria, write to soul/soul.md directly — it is the canonical source, not plan.json", pathStr, toolNameCopy)
					}
				}
			}

			// Inject event emitter into context before calling executor
			ctx = context.WithValue(ctx, virtualtools.WorkspaceEventEmitterKey, bo.contextAwareBridge)
			// Explicit guard paths are capabilities, including an intentionally empty
			// write list. In legacy workspace-path mode, leave these keys absent so the
			// executor uses the validated workspacePath grant instead of interpreting
			// empty, non-nil slices as a deny-all contract.
			if useFolderGuardPaths {
				ctx = context.WithValue(ctx, virtualtools.FolderGuardReadPathsKey, snapshotReadPaths)
				ctx = context.WithValue(ctx, virtualtools.FolderGuardWritePathsKey, snapshotWritePaths)
			}
			// Inject browser downloads path into context for agent-browser executor
			if bo.browserDownloadsPath != "" {
				ctx = context.WithValue(ctx, common.BrowserDownloadsPathKey, bo.browserDownloadsPath)
			}

			// All validations passed and paths normalized - call original executor
			return originalExecutor(ctx, args)
		}

		wrappedExecutors[toolName] = wrappedExecutor
	}

	return wrappedExecutors
}

// PrepareWorkspaceToolsWithFolderGuard wraps executors and enhances tool descriptions with folder guard information
// This is a convenience method that combines WrapWorkspaceToolsWithFolderGuard and EnhanceToolDescriptionWithFolderGuard
// Returns wrapped executors and enhanced tools (tools are modified in-place)
func (bo *BaseOrchestrator) PrepareWorkspaceToolsWithFolderGuard(tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	// Wrap executors with folder guard
	wrappedExecutors := bo.WrapWorkspaceToolsWithFolderGuard(executors)

	return tools, wrappedExecutors
}

// PrepareWorkspaceToolsWithExplicitPaths is like PrepareWorkspaceToolsWithFolderGuard but uses
// explicitly provided paths instead of the shared orchestrator state.
func (bo *BaseOrchestrator) PrepareWorkspaceToolsWithExplicitPaths(readPaths, writePaths []string, tools []llmtypes.Tool, executors map[string]interface{}) ([]llmtypes.Tool, map[string]interface{}) {
	wrappedExecutors := bo.WrapWorkspaceToolsWithExplicitPaths(readPaths, writePaths, executors)

	// NOTE: Description enhancement disabled - the runtime path validation via
	// WrapWorkspaceToolsWithFolderGuard is sufficient. The description enhancement
	// was causing massive token waste because tool descriptions are mutated in-place
	// on shared WorkspaceTools, accumulating guards from every step in the workflow.
	// for i := range tools {
	// 	if tools[i].Function != nil {
	// 		tools[i].Function.Description = bo.EnhanceToolDescriptionWithFolderGuard(
	// 			tools[i].Function.Name,
	// 			tools[i].Function.Description,
	// 		)
	// 	}
	// }

	return tools, wrappedExecutors
}

// isProtectedPlanningPath checks if a workspace-relative path points to a
// system-managed planning file that must not be modified via raw file writes.
// These files are managed by dedicated tools that serialize full structs to JSON.
func isProtectedPlanningPath(relPath string) bool {
	// Normalize: strip leading slashes
	normalized := strings.TrimLeft(relPath, "/")
	parts := strings.Split(normalized, "/")
	for i, part := range parts {
		if part == "planning" && i+1 < len(parts) {
			filename := parts[i+1]
			if filename == "plan.json" || filename == "step_config.json" || filename == "workflow_layout.json" {
				return true
			}
		}
	}
	return false
}
