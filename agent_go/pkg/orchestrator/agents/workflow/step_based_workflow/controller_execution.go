package step_based_workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// KnowledgebaseFolderName is the name of the persistent knowledgebase folder at workspace root
// This folder is never deleted during cleanup operations and is shared across all runs
const KnowledgebaseFolderName = "knowledgebase"

// KnowledgebaseContextFolderName stores user-supplied runtime business context.
// It is read by steps when knowledgebase_access grants read, but excluded from
// automatic KB note reorganization/consolidation because the content is user-owned.
const KnowledgebaseContextFolderName = "context"

// DBFolderName is the name of the persistent structured-data folder at workspace root.
// Always created on workspace init (no preset toggle, unlike knowledgebase). All scripted steps
// get read+write by default. State lives in db/db.sqlite (one table per entity); steps should upsert via
// INSERT ... ON CONFLICT rather than recreate tables. See docs/workflow/persistent_stores_design.md section 1.
const DBFolderName = "db"

// DBAssetsFolderName stores durable binary/media assets referenced by db rows,
// reports, or later steps. Keep metadata in a db/db.sqlite table and the actual files here.
const DBAssetsFolderName = "assets"

// getKnowledgebasePath returns the full path to the knowledgebase folder
// Path format: {workspaceRoot}/knowledgebase/
// Knowledgebase is at workspace root level (same as runs/, planning/, learnings/) to be shared across all runs
func getKnowledgebasePath(workspaceRoot string) string {
	return fmt.Sprintf("%s/%s", workspaceRoot, KnowledgebaseFolderName)
}

// getDBPath returns the full path to the db folder.
// Path format: {workspaceRoot}/db/
func getDBPath(workspaceRoot string) string {
	return fmt.Sprintf("%s/%s", workspaceRoot, DBFolderName)
}

// Standard workflow subfolder names. Kept as constants so every site that needs to
// reference them (folder-guard allow-lists, prompt paths, pre-creation scaffolds)
// goes through a single source. When adding a new workflow subfolder, update the
// constant AND WorkflowWritableSubfolders below — skipping either half is what
// produced the reports/ + db/ drift bug previously.
const (
	ReportsFolderName   = "reports"
	PlanningFolderName  = "planning"
	ExecutionFolderName = "execution"
	LearningsFolderName = "learnings"
	ScriptsFolderName   = "scripts"
	RunsFolderName      = "runs"
)

// WorkflowWritableSubfolders is the canonical list of subfolders under a workflow's
// root directory that any workflow-scoped agent session should be able to write.
// Trailing slashes are included so the list can drop directly into folder-guard
// allow-list construction (prefix-match behavior is slash-sensitive in several
// callers). Consumed by server.go's chat-agent / phase-chat / delegation setup
// paths — DO NOT duplicate this list in those callers.
//
// `planning/` is deliberately EXCLUDED. plan.json / step_config.json /
// workflow_layout.json are managed through typed plan-mod tools that serialize
// the full struct to JSON; raw writes would corrupt them. isProtectedPlanningPath
// (base_orchestrator_folder_guard.go) is the runtime backstop for custom tools,
// but the cleanest enforcement is keeping planning/ off this list entirely.
var WorkflowWritableSubfolders = []string{
	KnowledgebaseFolderName + "/",
	DBFolderName + "/",
	ReportsFolderName + "/",
	ExecutionFolderName + "/",
	LearningsFolderName + "/",
	ScriptsFolderName + "/",
	RunsFolderName + "/",
}

// Knowledgebase access modes — see docs/workflow/persistent_stores_design.md section 3.
const (
	KBAccessReadWrite = "read-write"
	KBAccessRead      = "read"
	KBAccessWrite     = "write"
	KBAccessNone      = "none"

	knowledgebaseAccessDescription = "Access to workflow knowledgebase/. Defaults to 'read'; use write, read-write or none as required. A write grant permits explicit task contributions to notes/; it adds no automatic reflection turn. context/ remains user owned."
)

// resolveKnowledgebaseAccess resolves the effective KB access mode for a step.
//
// resolveKnowledgebaseAccess returns the effective knowledgebase_access for a
// step. Explicit value wins. Unset uses the workflow knowledge setting:
// default pattern rather than introducing a new one: read by default (every
// step sees KB context, same as learnings), auto-promoted to read-write only
// when a contribution is already staged.
//
// PLAT-055 / K. Before this, unset silently meant KBAccessNone — no read, no
// write — which is a genuine trap distinct from an operator's deliberate
// KBAccessNone: latency-report's two worst-offending steps had no other legitimate
// destination for their infra-terrain and cost-baseline findings during
// execution, because nothing was ever configured either way. This does not
// touch that case (kb_access:"none" is an explicit value and always wins) — it
// only removes the trap for steps nobody configured at all.
func resolveKnowledgebaseAccess(stepConfig *AgentConfigs, presetEnabled bool) string {
	if !presetEnabled {
		return KBAccessNone
	}
	if stepConfig != nil && stepConfig.KnowledgebaseAccess != "" {
		switch stepConfig.KnowledgebaseAccess {
		case KBAccessRead, KBAccessWrite, KBAccessReadWrite, KBAccessNone:
			return stepConfig.KnowledgebaseAccess
		}
	}
	if stepConfig != nil && strings.TrimSpace(stepConfig.KnowledgebaseContribution) != "" {
		return KBAccessReadWrite
	}
	return KBAccessRead
}

// DB access modes. All workflow execution steps currently receive managed
// read-write access to their workflow database. DBAccessRead remains a persisted
// compatibility value, but it no longer narrows runtime capability: maintaining
// different parent/child DB projections repeatedly left writer children without
// mutate_workflow_db. Raw SQLite file access remains blocked for agentic steps;
// reads and writes go through query_workflow_db / mutate_workflow_db.
const (
	DBAccessReadWrite = "read-write"
	DBAccessRead      = "read"
	DBAccessNone      = "none"
)

// resolveDBAccess returns the uniform execution-step DB capability. The config
// argument is retained while older step_config.json files still carry db_access.
func resolveDBAccess(_ *AgentConfigs) string {
	return DBAccessReadWrite
}

// kbAccessAllowsRead reports whether the given access mode grants read access.
func kbAccessAllowsRead(mode string) bool {
	return mode == KBAccessRead || mode == KBAccessReadWrite
}

// Legacy objectives are archived by workflow_upgrade. Explicit contribution
// instructions now live in the authored task, never in hidden runtime guidance.
func kbContributionForPrompt(*AgentConfigs) string { return "" }

// kbAccessAllowsWrite reports whether the mode is eligible for KB contribution.
// This gates whether KB writes happen at all for the step; the mechanism
// (post-step agent vs step-level direct upserts) is picked separately by
// the retired agent-mode KB writer.
func kbAccessAllowsWrite(mode string) bool {
	return mode == KBAccessWrite || mode == KBAccessReadWrite
}

// kbAccessLabel returns a human-readable label for prompt display.
func kbAccessLabel(mode string) string {
	switch mode {
	case KBAccessReadWrite:
		return "READ/WRITE"
	case KBAccessRead:
		return "READ"
	case KBAccessWrite:
		return "WRITE"
	default:
		return "NONE"
	}
}

// isValidationFailure checks if validation failed (triggers human feedback)
// Returns true only if ExecutionStatus is "FAILED"
// Does NOT trigger on PARTIAL, COMPLETED, or INCOMPLETE status
//
//nolint:unused // retained for follow-up validation routing cleanup.
func isValidationFailure(validationResponse *ValidationResponse) bool {
	if validationResponse == nil {
		return false
	}
	return validationResponse.ExecutionStatus == "FAILED"
}

// StepPathInfo contains a positional top-level step number. Nested Agent paths
// are stable-ID paths and carry their parent index in execution context instead.
type StepPathInfo struct {
	ParentStepNumber  int  // 1-based top-level owner step number
	IsNestedExecution bool // True for child route/sub-agent execution paths
}

// parseStepPath extracts the number from a positional top-level path such as
// "step-1". It intentionally does not recognize retired flattened child paths.
func parseStepPath(stepPath string) StepPathInfo {
	regularStepRegex := regexp.MustCompile(`^step-(\d+)$`)
	if matches := regularStepRegex.FindStringSubmatch(stepPath); matches != nil {
		stepNumber := 0
		fmt.Sscanf(matches[1], "%d", &stepNumber)
		return StepPathInfo{ParentStepNumber: stepNumber}
	}
	return StepPathInfo{IsNestedExecution: strings.Contains(stepPath, string(filepath.Separator))}
}

const maxInlineFileSize = 15 * 1024  // 15 KB — inline small text files into LLM prompt
const maxTotalInlineSize = 50 * 1024 // 50 KB — total budget across all inlined deps

// isLikelyTextContent checks if content is text (not binary) by scanning for null bytes.
func isLikelyTextContent(content string) bool {
	checkLen := len(content)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		if content[i] == 0 {
			return false
		}
	}
	return true
}

func formatFileSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	kb := float64(bytes) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.1f KB", kb)
	}
	mb := kb / 1024.0
	return fmt.Sprintf("%.1f MB", mb)
}

// formatContextDependenciesWithContent resolves context dependency paths and inlines
// small text file contents directly into the prompt. Large or binary files are listed
// as paths only. This saves the LLM one tool call per inlined file.
func (hcpo *StepBasedWorkflowOrchestrator) formatContextDependenciesWithContent(
	ctx context.Context,
	resolvedContextDeps []string,
	docsRoot string,
) (string, error) {
	if len(resolvedContextDeps) == 0 {
		return "", nil
	}
	var sb strings.Builder
	totalInlined := 0
	for i, absPath := range resolvedContextDeps {
		if i > 0 {
			sb.WriteString("\n")
		}
		if !filepath.IsAbs(absPath) {
			sb.WriteString(fmt.Sprintf("**File**: `%s`\n", absPath))
			continue
		}
		relPath := workflowAPIPath(hcpo.GetWorkspacePath(), absPath)
		content, readErr := hcpo.ReadWorkspaceFile(ctx, relPath)
		if readErr != nil {
			return "", fmt.Errorf(
				"input file not found: %s\n(produced by a prior step — check that the previous step completed successfully)", absPath)
		}

		contentLen := len(content)
		if contentLen <= maxInlineFileSize && totalInlined+contentLen <= maxTotalInlineSize && isLikelyTextContent(content) {
			totalInlined += contentLen
			sb.WriteString(fmt.Sprintf("**File**: `%s` (inlined, %s)\n<content>\n%s\n</content>\n", absPath, formatFileSize(contentLen), content))
		} else {
			sb.WriteString(fmt.Sprintf("**File**: `%s` (%s — read via tool)\n", absPath, formatFileSize(contentLen)))
		}
	}
	return sb.String(), nil
}

func getArtifactFolderName(stepID string, stepPath string) string {
	// Nested runtime artifacts carry their complete parent-relative path. Never
	// collapse them back to the logical step ID: that would escape the owning
	// Agent subtree and make concurrent calls overwrite one another.
	if strings.Contains(strings.TrimSpace(stepPath), string(filepath.Separator)) {
		return filepath.Clean(stepPath)
	}
	stepID = strings.TrimSpace(stepID)
	if stepID != "" {
		return stepID
	}
	return stepPath
}

func getExecutionArtifactFolderOverride(execCtx *ExecutionContext) string {
	if execCtx == nil {
		return ""
	}
	return strings.TrimSpace(execCtx.ArtifactFolderNameOverride)
}

func getExecutionArtifactIdentity(stepID string, stepPath string, execCtx *ExecutionContext) (string, string) {
	if override := getExecutionArtifactFolderOverride(execCtx); override != "" {
		return "", override
	}
	return stepID, stepPath
}

// getExecutionFolderPath returns the execution folder path based on the stable step ID when available.
// stepPath remains the control-flow identifier and fallback for older callers.
func getExecutionFolderPath(executionWorkspacePath string, stepID string, stepPath string) string {
	return fmt.Sprintf("%s/%s", executionWorkspacePath, getArtifactFolderName(stepID, stepPath))
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveDependencyPathsWithWorkspace(
	ctx context.Context,
	deps []string,
	stepIndex int,
	stepPath string,
	allSteps []PlanStepInterface,
	executionWorkspacePath string,
	docsRoot string,
	variableValues map[string]string,
) []string {
	if len(deps) == 0 {
		return nil
	}

	appendUniqueCandidates := func(base []string, extras []string) []string {
		seen := make(map[string]bool, len(base))
		for _, item := range base {
			seen[item] = true
		}
		for _, item := range extras {
			if item == "" || seen[item] {
				continue
			}
			base = append(base, item)
			seen[item] = true
		}
		return base
	}

	resolvedPaths := make([]string, 0, len(deps))
	for _, dep := range deps {
		candidates := ResolveDependencyPathCandidates(dep, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, variableValues)
		candidates = appendUniqueCandidates(candidates, hcpo.findExecutionPlanProducerCandidates(ctx, dep, executionWorkspacePath, docsRoot, variableValues))
		if len(candidates) == 0 {
			resolvedPaths = append(resolvedPaths, dep)
			continue
		}

		chosen := candidates[0]
		for _, candidate := range candidates {
			if !filepath.IsAbs(candidate) {
				continue
			}
			relPath := workflowAPIPath(hcpo.GetWorkspacePath(), candidate)
			if relPath == candidate {
				continue
			}
			if _, err := hcpo.ReadWorkspaceFile(ctx, relPath); err == nil {
				chosen = candidate
				break
			}
		}

		resolvedPaths = append(resolvedPaths, chosen)
	}
	return resolvedPaths
}

func (hcpo *StepBasedWorkflowOrchestrator) findExecutionPlanProducerCandidates(
	ctx context.Context,
	dep string,
	executionWorkspacePath string,
	docsRoot string,
	variableValues map[string]string,
) []string {
	if executionPlanFromContext(ctx) == nil || dep == "" || filepath.IsAbs(dep) || strings.Contains(dep, "/") {
		return nil
	}

	allPlanSteps := collectAllSteps(executionPlanFromContext(ctx).Steps)
	candidates := make([]string, 0, 4)
	seen := make(map[string]bool)

	for _, info := range allPlanSteps {
		step := info.Step
		if step == nil {
			continue
		}
		output := ResolveVariables(step.GetContextOutput().String(), variableValues)
		matchesOutput := contextOutputMatchesDependency(output, dep)
		matchesProducerID := strings.TrimSpace(step.GetID()) == strings.TrimSpace(dep)
		if !matchesOutput && !matchesProducerID {
			continue
		}

		stepID := strings.TrimSpace(step.GetID())
		if stepID == "" {
			continue
		}

		appendCandidate := func(candidate string) {
			if candidate == "" || seen[candidate] {
				return
			}
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}

		outputNames := []string{dep}
		if matchesProducerID && !matchesOutput {
			outputNames = contextOutputFileNames(output)
		}
		// Prefer the stable artifact folder keyed by step_id.
		for _, outputName := range outputNames {
			appendCandidate(workflowAbsPath(getExecutionFolderPath(executionWorkspacePath, stepID, stepID), outputName))
		}

	}

	return candidates
}

// =============================================================================
// IMPORTANT: Workspace File/Folder Operations
// =============================================================================
// NEVER use os.MkdirAll, os.WriteFile, os.Remove, or other os.* functions directly
// for workspace file/folder operations. Always use the Workspace API instead.
//
// Reason: The Workspace API ensures consistency between:
// - Folder/file creation
// - workspace bridge/shell views used by LLM agents
// - report/file panels that read from the workspace API
//
// Using os.* directly can cause "folder does not exist" errors because the
// Workspace API may have a different root path than the Go agent's filesystem.
//
// Use these functions instead:
// - createFolderViaAPI() - for creating folders
// - WriteWorkspaceFile() - for creating/updating files (auto-creates parent dirs)
// - Workspace API endpoints directly when needed
// =============================================================================

// getWorkspaceAPIURL returns the workspace API base URL from environment or default
func getWorkspaceAPIURL() string {
	if url := os.Getenv("WORKSPACE_API_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8081"
}

// normalizePathForWorkspaceAPI normalizes a relative path to be relative to workspace-docs root.
//
// The Workspace API expects all paths relative to workspace-docs root (e.g., "Workflow/ICICI.../runs/...").
// This function handles two input path formats:
//
//  1. Paths relative to workflow workspace (e.g., "learnings/step-1", "runs/iteration-1")
//     - Prepends the workspacePath to create full relative path
//
//  2. Paths already relative to workspace-docs root (e.g., "Workflow/ICICI.../runs/...")
//     - Returns as-is (already in correct format)
//
// IMPORTANT: Absolute paths are NOT allowed and will return empty string (triggering an error).
// All paths should be relative to the workspace. If you have an absolute path, that's a bug.
//
// Parameters:
//   - path: The relative path to normalize (must NOT be absolute)
//   - workspacePath: The workflow workspace path relative to workspace-docs root
//     (e.g., "Workflow/ICICI Bank Account Opening"). Pass empty string if path is already
//     relative to workspace-docs root.
//
// Returns the path relative to workspace-docs root, suitable for Workspace API calls.
func normalizePathForWorkspaceAPI(path string, workspacePath string) string {
	if path == "" {
		return ""
	}

	// Clean the path to remove redundant separators and dots
	path = filepath.Clean(path)

	// REJECT absolute paths - this is always a bug
	if filepath.IsAbs(path) {
		panic(fmt.Sprintf("normalizePathForWorkspaceAPI: Absolute paths are not allowed: %s. All paths must be relative to workspace (e.g., 'Workflow/...'). Fix the caller.", path))
	}

	// Remove leading slash if present (relative paths should not start with /)
	path = strings.TrimPrefix(path, "/")

	// If path is already relative to workspace-docs root (starts with workspacePath),
	// return it as-is
	if workspacePath != "" {
		cleanWorkspacePath := strings.TrimPrefix(filepath.Clean(workspacePath), "/")
		if strings.HasPrefix(path, cleanWorkspacePath) {
			return path
		}

		// Path is relative to workflow workspace - prepend workspacePath
		// e.g., "learnings/step-1" -> "Workflow/ICICI.../learnings/step-1"
		return filepath.Join(cleanWorkspacePath, path)
	}

	return path
}

// createFolderViaAPI creates a folder via the Workspace API (POST /api/folders).
//
// The folderPath parameter can be in any format - this function normalizes it internally.
// If workspacePath is provided, it will be used to convert workflow-relative paths
// to workspace-docs-relative paths.
//
// Parameters:
//   - ctx: Context for the HTTP request
//   - folderPath: Path to create (absolute, workspace-relative, or workflow-relative)
//   - workspacePath: Optional workflow workspace path for normalization (e.g., "Workflow/ICICI...").
//     Pass empty string if folderPath is already relative to workspace-docs root.
func createFolderViaAPI(ctx context.Context, folderPath string, workspacePath ...string) error {
	// Normalize the path for the Workspace API
	wp := ""
	if len(workspacePath) > 0 {
		wp = workspacePath[0]
	}
	normalizedPath := normalizePathForWorkspaceAPI(folderPath, wp)

	if normalizedPath == "" {
		return fmt.Errorf("cannot create folder: path is empty after normalization")
	}

	apiURL := getWorkspaceAPIURL() + "/api/folders"

	// Debug logging
	fmt.Printf("[DEBUG createFolderViaAPI] Creating folder via API: %s (original: %s, workspacePath: %s) at %s\n",
		normalizedPath, folderPath, wp, apiURL)

	// Prepare request body with normalized path
	requestBody := map[string]string{
		"folder_path": normalizedPath,
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		fmt.Printf("[DEBUG createFolderViaAPI] Failed to marshal request body: %v\n", err)
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonBody))
	if err != nil {
		fmt.Printf("[DEBUG createFolderViaAPI] Failed to create request: %v\n", err)
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}

	// Set timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[DEBUG createFolderViaAPI] Failed to call workspace API: %v\n", err)
		return fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("[DEBUG createFolderViaAPI] Failed to read response: %v\n", err)
		return fmt.Errorf("failed to read response: %w", err)
	}

	fmt.Printf("[DEBUG createFolderViaAPI] Response status: %d, body: %s\n", resp.StatusCode, string(body))

	// Check HTTP status - 201 Created or 409 Conflict (folder already exists) are both OK
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		fmt.Printf("[DEBUG createFolderViaAPI] Unexpected status code: %d\n", resp.StatusCode)
		return fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("[DEBUG createFolderViaAPI] Successfully created folder: %s\n", normalizedPath)
	return nil
}

// deleteFolderViaAPI deletes a folder (and all its contents) via the Workspace API (DELETE /api/folders/{path}).
// The folderPath should be relative to workspace-docs root (e.g., "Workflow/X/learnings/step-1/code").
func deleteFolderViaAPI(ctx context.Context, folderPath string) error {
	pathSegments := strings.Split(folderPath, "/")
	encodedSegments := make([]string, len(pathSegments))
	for i, segment := range pathSegments {
		encodedSegments[i] = url.PathEscape(segment)
	}
	encodedPath := strings.Join(encodedSegments, "/")

	apiURL := getWorkspaceAPIURL() + "/api/folders/" + encodedPath + "?confirm=true"
	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if token := strings.TrimSpace(os.Getenv("WORKSPACE_API_TOKEN")); token != "" {
		req.Header.Set("X-Workspace-Token", token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil // already gone
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ensureStepExecutionFolderExists ensures the step execution folder exists by creating it if needed.
// This is called when a step starts running to ensure the folder exists even if it was previously deleted.
// Creates folder via Workspace API only (ensures consistency with workspace listings).
//
// The stepExecutionPath can be in any format - the function normalizes it internally using
// the orchestrator's workspace path.
func (hcpo *StepBasedWorkflowOrchestrator) ensureStepExecutionFolderExists(ctx context.Context, stepExecutionPath string) error {
	if stepExecutionPath == "" {
		return fmt.Errorf("invalid step execution path: empty")
	}

	fmt.Printf("[DEBUG ensureStepExecutionFolderExists] Called with stepExecutionPath: %s\n", stepExecutionPath)

	// Create folder via Workspace API - normalization happens inside createFolderViaAPI
	// Pass empty workspacePath since stepExecutionPath is already relative to workspace-docs root
	if err := createFolderViaAPI(ctx, stepExecutionPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to create step execution folder via API: %s: %v", stepExecutionPath, err))
		return fmt.Errorf("failed to create step execution folder: %w", err)
	}

	return nil
}

// getValidationFolderPath returns the logs folder path based on the stable step ID when available.
func getValidationFolderPath(validationWorkspacePath string, stepID string, stepPath string) string {
	return fmt.Sprintf("%s/logs/%s", validationWorkspacePath, getArtifactFolderName(stepID, stepPath))
}

// getExecutionFolderPathForLogs returns the execution logs folder path based on the stable step ID when available.
func getExecutionFolderPathForLogs(validationWorkspacePath string, stepID string, stepPath string) string {
	return fmt.Sprintf("%s/logs/%s/execution", validationWorkspacePath, getArtifactFolderName(stepID, stepPath))
}

const finalExecutionSummaryFilename = "execution-final-summary.json"

// addCompletedStepIndex safely adds a step index to the completed list, preventing duplicates
// This is important when decision steps route back to previous steps, which can cause
// the same step index to be added multiple times if not checked
func (hcpo *StepBasedWorkflowOrchestrator) addCompletedStepIndex(progress *StepProgress, stepIndex int) {
	// Check if already in list to prevent duplicates
	for _, idx := range progress.CompletedStepIndices {
		if idx == stepIndex {
			hcpo.GetLogger().Debug(fmt.Sprintf("⚠️ Step %d already in completed list, skipping duplicate", stepIndex+1))
			return // Already exists, don't add duplicate
		}
	}
	// Not found, safe to append
	progress.CompletedStepIndices = append(progress.CompletedStepIndices, stepIndex)
	hcpo.GetLogger().Debug(fmt.Sprintf("✅ Added step %d to completed list (total: %d)", stepIndex+1, len(progress.CompletedStepIndices)))
}

// executeConditionalStep is now in controller_conditional.go

// saveExecutionConversationLogs saves execution result, conversation history, and prompts to log files.
// Called on both success and failure/cancellation paths so partial conversations from interrupted
// executions can be inspected via debug_step or direct log file access.
// Uses context.Background() internally so writes succeed even when the caller's context is canceled.
func (hcpo *StepBasedWorkflowOrchestrator) saveExecutionConversationLogs(
	stepIndex int, stepID string, stepPath string, retryAttempt int, loopIterationCount int,
	executionResult string, executionLLM string,
	conversationHistory []llmtypes.MessageContent,
	executionAgent agents.OrchestratorAgent,
	toolCalls []orchestrator.ToolCallEntry,
	llmCalls []orchestrator.LLMCallEntry,
	attemptStartedAt time.Time,
	attemptCompletedAt time.Time,
	attemptDuration time.Duration,
) error {
	// Recover any orphaned tool call before this evidence is written (PLAT-142/143).
	backfillMissingToolResults(hcpo.sessionID, toolCalls, executionAgent)

	// Use background context so saves succeed even when execution was canceled/stopped by user
	saveCtx := context.Background()

	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}
	logDir := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)
	filenameBase := fmt.Sprintf("execution-attempt-%d-iteration-%d", retryAttempt, loopIterationCount)
	// PLAT-176. retryAttempt and loopIterationCount are counters LOCAL to one
	// dispatch of this step -- both restart at 1 every time the step is
	// dispatched again -- so a re-dispatched step recomputes this exact path and
	// the writes below silently destroy the previous dispatch's evidence. Move
	// the prior files aside first; canonical names keep holding the newest
	// dispatch, so every existing reader is unaffected.
	hcpo.archiveSupersededExecutionLogs(saveCtx, logDir, filenameBase)
	if attemptCompletedAt.IsZero() {
		attemptCompletedAt = time.Now().UTC()
	}
	if attemptStartedAt.IsZero() {
		attemptStartedAt = attemptCompletedAt.Add(-attemptDuration)
	}
	toolTiming := normalizeToolTimingEntries(toolCalls, attemptStartedAt)
	llmTiming := normalizeLLMTimingEntries(llmCalls, attemptStartedAt)
	agentName := ""
	if executionAgent != nil && executionAgent.GetBaseAgent() != nil {
		agentName = executionAgent.GetBaseAgent().GetName()
	}
	traceSpans, timingBreakdown := buildTimingTrace(stepID, agentName, executionLLM, attemptStartedAt, attemptCompletedAt, attemptDuration, llmTiming, toolTiming)
	timingData := map[string]interface{}{
		"schema_version": 2,
		"step_index":     stepIndex + 1,
		"step_id":        stepID,
		"step_path":      stepPath,
		"retry_attempt":  retryAttempt,
		"loop_iteration": loopIterationCount,
		"run_folder":     hcpo.selectedRunFolder,
		"agent": map[string]interface{}{
			"name":                          agentName,
			"model":                         executionLLM,
			"started_at":                    formatRFC3339UTC(attemptStartedAt),
			"completed_at":                  formatRFC3339UTC(attemptCompletedAt),
			"duration_ns":                   int64(attemptDuration),
			"duration_ms":                   durationToMillis(attemptDuration),
			"llm_call_count":                llmTiming.Count,
			"llm_duration_ms":               llmTiming.TotalDurationMs,
			"llm_time_to_first_response_ms": llmTiming.TimeToFirstResponseMs,
		},
		"llm":         llmTiming,
		"tools":       toolTiming,
		"trace_spans": traceSpans,
		"breakdown":   timingBreakdown,
	}

	// Save execution result
	resultPath := fmt.Sprintf("%s/%s.json", logDir, filenameBase)
	resultData := map[string]interface{}{
		"step_index":                    stepIndex + 1,
		"step_id":                       stepID,
		"step_path":                     stepPath,
		"retry_attempt":                 retryAttempt,
		"loop_iteration":                loopIterationCount,
		"execution_result":              executionResult,
		"model":                         executionLLM,
		"started_at":                    formatRFC3339UTC(attemptStartedAt),
		"completed_at":                  formatRFC3339UTC(attemptCompletedAt),
		"duration_ms":                   durationToMillis(attemptDuration),
		"duration_ns":                   int64(attemptDuration),
		"llm_call_count":                llmTiming.Count,
		"llm_duration_ms":               llmTiming.TotalDurationMs,
		"llm_time_to_first_response_ms": llmTiming.TimeToFirstResponseMs,
		"tool_call_count":               toolTiming.Count,
		"tool_duration_ms":              toolTiming.TotalDurationMs,
		"tracked_union_duration_ms":     timingBreakdown.TrackedUnionDurationMs,
		"untracked_duration_ms":         timingBreakdown.UntrackedDurationMs,
		"total_input_tokens":            timingBreakdown.TotalInputTokens,
		"total_output_tokens":           timingBreakdown.TotalOutputTokens,
		"total_tokens":                  timingBreakdown.TotalTokens,
		"tool_args_bytes":               timingBreakdown.ToolArgsBytes,
		"tool_result_bytes":             timingBreakdown.ToolResultBytes,
		"timing":                        timingData,
		"timestamp":                     attemptCompletedAt.Format(time.RFC3339),
	}
	resultJSON, err := json.MarshalIndent(resultData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal required execution result %s: %w", resultPath, err)
	}
	if err := hcpo.WriteWorkspaceFile(saveCtx, resultPath, string(resultJSON)); err != nil {
		return fmt.Errorf("write required execution result %s: %w", resultPath, err)
	}

	// Save conversation history
	convPath := fmt.Sprintf("%s/%s-conversation.json", logDir, filenameBase)
	convData := map[string]interface{}{
		"step_index":           stepIndex + 1,
		"step_id":              stepID,
		"step_path":            stepPath,
		"retry_attempt":        retryAttempt,
		"loop_iteration":       loopIterationCount,
		"conversation_history": conversationHistory,
		"llm_calls":            llmCalls,
		"tool_calls":           toolCalls,
		"llm_call_count":       llmTiming.Count,
		"tool_call_count":      toolTiming.Count,
		"timing":               timingData,
		"timestamp":            attemptCompletedAt.Format(time.RFC3339),
	}
	if handle := currentAgentSessionHandle(executionAgent); handle != nil {
		convData["agent_session_handle"] = handle
	}
	if convJSON, err := json.MarshalIndent(convData, "", "  "); err == nil {
		if err := hcpo.WriteWorkspaceFile(saveCtx, convPath, string(convJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write conversation history to %s: %v", convPath, err))
		}
	}

	// Save system prompt (overwrites the pre-execution save with the final rendered prompt)
	var capturedSystemPrompt string
	if executionAgent != nil {
		if ba := executionAgent.GetBaseAgent(); ba != nil && ba.Agent() != nil {
			capturedSystemPrompt = ba.Agent().Definition().Instructions
		}
	}
	promptsPath := fmt.Sprintf("%s/%s-prompts.json", logDir, filenameBase)
	promptsData := map[string]interface{}{
		"step_index":    stepIndex + 1,
		"step_id":       stepID,
		"step_path":     stepPath,
		"system_prompt": capturedSystemPrompt,
		"saved_at":      "post_execution",
		"timestamp":     attemptCompletedAt.Format(time.RFC3339),
	}
	if promptsJSON, err := json.MarshalIndent(promptsData, "", "  "); err == nil {
		if err := hcpo.WriteWorkspaceFile(saveCtx, promptsPath, string(promptsJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write prompts to %s: %v", promptsPath, err))
		}
	}

	timingPath := fmt.Sprintf("%s/%s-timing.json", logDir, filenameBase)
	if timingJSON, err := json.MarshalIndent(timingData, "", "  "); err == nil {
		if err := hcpo.WriteWorkspaceFile(saveCtx, timingPath, string(timingJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to write timing summary to %s: %v", timingPath, err))
		}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("💾 Saved execution logs for step %d (%s) attempt %d", stepIndex+1, stepPath, retryAttempt))
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) saveFinalExecutionSummary(stepID, stepPath, executionResult string) error {
	trimmed := strings.TrimSpace(executionResult)
	if trimmed == "" {
		return nil
	}

	validationWorkspacePath := hcpo.GetWorkspacePath()
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", validationWorkspacePath, hcpo.selectedRunFolder)
	}
	logDir := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)
	resultPath := filepath.Join(logDir, finalExecutionSummaryFilename)
	resultData := map[string]interface{}{
		"schema_version":   1,
		"step_id":          stepID,
		"step_path":        stepPath,
		"run_folder":       hcpo.selectedRunFolder,
		"status":           "completed",
		"execution_result": trimmed,
		"completed_at":     formatRFC3339UTC(time.Now().UTC()),
	}
	resultJSON, err := json.MarshalIndent(resultData, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal final execution summary %s: %w", resultPath, err)
	}
	if err := hcpo.WriteWorkspaceFile(context.Background(), resultPath, string(resultJSON)); err != nil {
		return fmt.Errorf("write final execution summary %s: %w", resultPath, err)
	}
	return nil
}

// loadSingleStepResultFromLogs reads the execution result for a single step (1-based stepNumber)
// from its log files. Returns the result string and true if found, or "" and false otherwise.
func (hcpo *StepBasedWorkflowOrchestrator) loadSingleStepResultFromLogs(ctx context.Context, stepNumber int) (string, bool) {
	var validationWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		validationWorkspacePath = fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		validationWorkspacePath = hcpo.GetWorkspacePath()
	}

	stepPath := fmt.Sprintf("step-%d", stepNumber)
	stepID := stepPath
	var plannedStep PlanStepInterface
	if executionPlanFromContext(ctx) != nil && stepNumber >= 1 && stepNumber <= len(executionPlanFromContext(ctx).Steps) {
		plannedStep = executionPlanFromContext(ctx).Steps[stepNumber-1]
		if id := plannedStep.GetID(); id != "" {
			stepID = id
		}
	}

	// Human-input steps don't write execution-attempt-*.json files. Their answer
	// is saved to the step's context_output file (e.g. step-N.json) with schema
	// {"response": "...", ...}. Without this branch, downstream routing steps
	// that run in a separate invocation (e.g. workshop execute_step) would see
	// an empty prior result and route blindly.

	executionLogsFolderPath := getExecutionFolderPathForLogs(validationWorkspacePath, stepID, stepPath)

	var latestExecutionResult string
	var latestAttempt, latestIteration int
	var latestCompletedAt time.Time
	var haveLatestCompletedAt bool

	for attempt := 1; attempt <= 10; attempt++ {
		for iteration := 0; iteration <= 10; iteration++ {
			executionResultFilePath := fmt.Sprintf("%s/execution-attempt-%d-iteration-%d.json", executionLogsFolderPath, attempt, iteration)
			content, err := hcpo.ReadWorkspaceFile(ctx, executionResultFilePath)
			if err != nil {
				continue
			}

			var executionData map[string]interface{}
			if err := json.Unmarshal([]byte(content), &executionData); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("Failed to parse execution result from %s: %v", executionResultFilePath, err))
				continue
			}

			execResult, ok := executionData["execution_result"].(string)
			if !ok {
				continue
			}

			// attempt/iteration are counters LOCAL to one dispatch, reset to 1/0
			// on every fresh run of this step (same shape as PLAT-176) -- across
			// dispatches they are not a time-ordered sequence, so a superseded
			// dispatch that happened to reach a higher attempt number than a
			// later, successful dispatch would otherwise win here and silently
			// resurrect a stale (and possibly failed) result as if it were
			// current. completed_at is wall-clock time and is the only field
			// that is actually comparable across dispatches; the attempt/
			// iteration pair remains a tie-breaker for same-instant files, and
			// stays the sole ordering when a legacy file predates completed_at
			// being recorded (this branch's own value would be its zero time,
			// which correctly loses to anything with a real timestamp below).
			var completedAt time.Time
			var hasCompletedAt bool
			if raw, ok := executionData["completed_at"].(string); ok {
				// RFC3339Nano parses a plain-RFC3339 (whole-second) legacy
				// value fine too, so this covers both old and new files.
				if parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); parseErr == nil && !parsed.IsZero() {
					completedAt = parsed
					hasCompletedAt = true
				}
			}

			better := false
			switch {
			case hasCompletedAt && haveLatestCompletedAt:
				better = completedAt.After(latestCompletedAt) ||
					(completedAt.Equal(latestCompletedAt) && (attempt > latestAttempt || (attempt == latestAttempt && iteration > latestIteration)))
			case hasCompletedAt && !haveLatestCompletedAt:
				better = true
			case !hasCompletedAt && !haveLatestCompletedAt:
				better = attempt > latestAttempt || (attempt == latestAttempt && iteration > latestIteration)
			default: // !hasCompletedAt && haveLatestCompletedAt: never prefer an undated file over a dated one
				better = false
			}

			if better {
				latestExecutionResult = execResult
				latestAttempt = attempt
				latestIteration = iteration
				latestCompletedAt = completedAt
				haveLatestCompletedAt = hasCompletedAt
			}
		}
	}

	if latestExecutionResult != "" {
		hcpo.GetLogger().Info(fmt.Sprintf("Loaded execution result from logs for step %d (attempt %d, iteration %d, completed_at=%s)", stepNumber, latestAttempt, latestIteration, latestCompletedAt.Format(time.RFC3339)))
		return latestExecutionResult, true
	}
	return "", false
}

// loadExecutionResultsFromLogs loads execution results from logs folder for previous steps
// This is a shared/reusable function that can be called from anywhere in the controller
// It's used when resuming from a step or running a single step, where execution results aren't in memory
// Returns an array of execution results indexed by step index (0-based)
// For each step, it finds the latest execution result file (highest attempt, then highest iteration)
func (hcpo *StepBasedWorkflowOrchestrator) loadExecutionResultsFromLogs(ctx context.Context, allSteps []PlanStepInterface, currentStepIndex int) []string {
	executionResults := make([]string, currentStepIndex)

	for i := 0; i < currentStepIndex && i < len(allSteps); i++ {
		if result, ok := hcpo.loadSingleStepResultFromLogs(ctx, i+1); ok {
			executionResults[i] = result
		}
	}

	return executionResults
}

// buildPreviousStepsSummary builds a formatted summary of previous completed steps
// This provides context to the execution agent about what steps have already been executed
// previousExecutionResults: array of execution outputs from previous steps (indexed by step index)
func (hcpo *StepBasedWorkflowOrchestrator) buildPreviousStepsSummary(allSteps []PlanStepInterface, currentStepIndex int, previousContextFiles []string, previousExecutionResults []string) string {
	if len(allSteps) == 0 || currentStepIndex == 0 || len(previousContextFiles) == 0 {
		return "" // No previous steps
	}

	// Create a map of context output files to step indices for quick lookup
	contextFileToStepIndex := make(map[string]int)
	for i := 0; i < currentStepIndex && i < len(allSteps); i++ {
		contextOutput := allSteps[i].GetContextOutput().String()
		if contextOutput != "" {
			// Resolve variables in context output to match what's in previousContextFiles
			resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
			contextFileToStepIndex[resolvedOutput] = i
		}
	}

	// Build summary for steps that have context outputs in previousContextFiles
	var summary strings.Builder
	summary.WriteString("## 📋 Previous Steps Context\n\n")
	summary.WriteString("The following steps have been completed before this step:\n\n")

	// Compute execution workspace path for building full output file paths
	var executionWorkspacePath string
	if hcpo.selectedRunFolder != "" {
		executionWorkspacePath = fmt.Sprintf("%s/runs/%s/execution", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
	} else {
		executionWorkspacePath = fmt.Sprintf("%s/execution", hcpo.GetWorkspacePath())
	}

	stepCount := 0
	for i := 0; i < currentStepIndex && i < len(allSteps); i++ {
		step := allSteps[i]
		contextOutput := step.GetContextOutput().String()
		if contextOutput == "" {
			continue // Skip steps without context output
		}

		// Check if this step's context output is in previousContextFiles
		resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
		found := false
		for _, prevFile := range previousContextFiles {
			if prevFile == resolvedOutput {
				found = true
				break
			}
		}

		if !found {
			continue // Skip steps whose context output is not in previousContextFiles
		}

		// Resolve variables in title and description
		resolvedTitle := ResolveVariables(step.GetTitle(), hcpo.variableValues)
		resolvedDescription := ResolveVariables(step.GetDescription(), hcpo.variableValues)

		// Truncate description if too long (keep first 200 characters)
		description := resolvedDescription
		if len(description) > 200 {
			description = description[:200] + "..."
		}

		// Compute the step execution folder path
		stepPath := fmt.Sprintf("step-%d", i+1)
		stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, step.GetID(), stepPath)

		summary.WriteString(fmt.Sprintf("**Step %d: %s**\n", i+1, resolvedTitle))
		summary.WriteString(fmt.Sprintf("- **Description**: %s\n", description))
		// Strip workflow root prefix so paths are relative to working directory
		relStepExecPath := strings.TrimPrefix(stepExecutionPath, hcpo.GetWorkspacePath()+"/")
		summary.WriteString(fmt.Sprintf("- **Output File**: `%s/%s`\n", relStepExecPath, resolvedOutput))
		summary.WriteString("\n")

		stepCount++
	}

	if stepCount == 0 {
		return "" // No previous steps with context outputs
	}

	summary.WriteString("Use this context to understand the workflow progression and what has been accomplished so far.\n")

	// Include the most recent execution result.
	// Include the most recent non-human-input execution result
	for idx := currentStepIndex - 1; idx >= 0; idx-- {
		if idx >= len(previousExecutionResults) || previousExecutionResults[idx] == "" {
			continue
		}
		execOutput := previousExecutionResults[idx]
		if len(execOutput) > 2000 {
			execOutput = execOutput[:2000] + "\n... (truncated)"
		}
		var stepTitle string
		if idx < len(allSteps) {
			stepTitle = ResolveVariables(allSteps[idx].GetTitle(), hcpo.variableValues)
		} else {
			stepTitle = fmt.Sprintf("Step %d", idx+1)
		}
		summary.WriteString("\n## Previous Step Execution Output\n\n")
		summary.WriteString(fmt.Sprintf("**Step %d: %s** execution result:\n\n", idx+1, stepTitle))
		summary.WriteString(fmt.Sprintf("```\n%s\n```\n", execOutput))
		summary.WriteString("\nUse this execution output to understand what the immediately previous step accomplished.\n")
		break
	}

	return summary.String()
}

// executeSingleStep executes a single step with full functionality (execution, validation, learning, human feedback)
// This is a reusable function for top-level steps and nested executions.
func (hcpo *StepBasedWorkflowOrchestrator) executeSingleStep(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string, // e.g., "step-1" or "step-2-sub-login"
	totalSteps int,
	iteration int,
	previousContextFiles []string,
	progress *StepProgress,
	isNestedExecution bool, // nested executions do not advance top-level progress
	execCtx *ExecutionContext, // Execution context with flags (skipHumanInput, etc.)
	allSteps []PlanStepInterface, // All steps in the plan
	isSubAgent bool, // true if this is a sub-agent from an orchestration step (never requests human feedback)
	previousExecutionResults []string, // Execution outputs from previous steps (indexed by step index)
	orchestrationRoutes []OrchestrationRoute, // Optional: orchestration routes (sub-agents) - only used when isSubAgent is true
) (executionResult string, updatedContextFiles []string, err error) {
	// Initialize updated context files as copy of previous context files
	updatedContextFiles = make([]string, len(previousContextFiles))
	copy(updatedContextFiles, previousContextFiles)

	if err := validateStepRequiredVariables(step, hcpo.variableValues); err != nil {
		return "", updatedContextFiles, err
	}

	artifactStepID, artifactStepPath := getExecutionArtifactIdentity(step.GetID(), stepPath, execCtx)

	// PLAT-174. Everything below reads step.AgentConfigs (folder guard scope,
	// scripted-mode detection, and eventually the execution agent's LLM), and
	// until now that field was a snapshot taken exactly once, at run start
	// (controller.go's populateRuntimeFields sweep over every plan step). A
	// step_config.json change made mid-run — even minutes before this exact
	// step began, well after the change landed on disk — never reached it,
	// because the run was already in flight. "Before this step started" was
	// never the boundary that mattered; "before the run started" was, and by
	// the time any step but the first one runs, that boundary has passed.
	hcpo.refreshStepAgentConfigsBeforeExecution(ctx, step)

	// Emit step_started event (also emits step progress with status="start")
	hcpo.emitStepStartedEvent(ctx, step, stepIndex, stepPath)

	// Guarantee the step leaves "running" on EVERY return path. This function has
	// many early returns between here and the bottom (scripted fast-path failures,
	// dependency/agent errors, context cancellations) — without this defer they'd
	// skip the finished event and the UI would show the step stuck on "running"
	// even after it completed or pre-validation failed. Emit "failed" on an error
	// return, "end" otherwise. WithoutCancel preserves execution-local event
	// ownership while allowing a cancellation-driven return to reach the bridge.
	defer func() {
		emitCtx := context.WithoutCancel(ctx)
		if err != nil {
			hcpo.emitStepFailedEvent(emitCtx, step, stepIndex, stepPath, err.Error())
		} else {
			hcpo.emitStepFinishedEvent(emitCtx, step, stepIndex, stepPath)
		}
	}()

	// Narrow the session-level folder guard to this step's paths for the duration of
	// the step. Session guard is set workspace-wide by the interactive builder and batch
	// execution (server.go:4002, controller_batch_execution.go:323). Because shell commands
	// prefer session config over context keys (execute_shell_command.go:146), a broad
	// session guard would otherwise let a step in iteration-N read/write other iterations
	// via `cat`/`cp`. Snapshot and restore the prior config so the builder's out-of-step
	// chat shell commands keep their broader access.
	// Sub-agents receive a dedicated session-level guard when their execution
	// agent is created. Mutating the parent's shared session here would make
	// parallel children overwrite and restore one another's access policy.
	if sessionID := hcpo.GetMCPSessionID(); sessionID != "" && !isSubAgent {
		narrowAgentCfg := getAgentConfigs(step)
		narrowKBAccess := resolveKnowledgebaseAccess(narrowAgentCfg, hcpo.UseKnowledgebase())
		narrowRead, narrowWrite := hcpo.setupExecutionFolderGuard(artifactStepPath, artifactStepID, narrowKBAccess, hcpo.resolveDBAccess(narrowAgentCfg), narrowAgentCfg)
		narrowRead = common.DeduplicateStrings(appendDescriptionReferenceReadPaths(narrowRead, hcpo.GetWorkspacePath(), step.GetDescription()))
		var prevRead, prevWrite []string
		if prevCfg := common.GetSessionShellConfig(sessionID); prevCfg != nil {
			prevRead = prevCfg.ReadPaths
			prevWrite = prevCfg.WritePaths
		}
		common.SetSessionFolderGuard(sessionID, narrowRead, narrowWrite)
		hcpo.grantSessionCDPHostDownloadsReadWrite(sessionID)
		hcpo.GetLogger().Info(fmt.Sprintf("🔒 [FOLDER_GUARD_STEP] Narrowed session %s for step %s: read=%v write=%v", sessionID, step.GetID(), narrowRead, narrowWrite))
		defer func() {
			common.SetSessionFolderGuard(sessionID, prevRead, prevWrite)
			hcpo.grantSessionCDPHostDownloadsReadWrite(sessionID)
			hcpo.GetLogger().Info(fmt.Sprintf("🔓 [FOLDER_GUARD_STEP] Restored session %s after step %s", sessionID, step.GetID()))
		}()
	}

	isScriptedMode := isScriptedStep(step)
	hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] step=%s type=%s scripted=%v", step.GetID(), step.StepType(), isScriptedMode))
	if isScriptedMode {
		var finishInspection func(string, error)
		ctx, finishInspection = hcpo.beginScriptInspection(ctx, step)
		defer func() { finishInspection(executionResult, err) }()
		hcpo.recordInspectionMode(ctx, "script")
	}

	// Initialize variables for step execution.
	// Scripted steps get 3 retries on pre-validation failure.
	// Sub-agents and workshop single-step mode get 2 — if it can't fix in 2 tries,
	// better to return to the orchestrator/user with feedback than burn more tokens.
	maxRetryAttempts := 3
	if isSubAgent {
		maxRetryAttempts = 3
	}
	if hcpo.runSingleStepOnly {
		maxRetryAttempts = 3
	}
	var executionConversationHistory []llmtypes.MessageContent // Saved execution evidence and same-conversation repair context
	stepCompleted := false

	// Outer loop: Handle re-execution with human feedback
	for !stepCompleted {
		// Check for context cancellation before retry
		select {
		case <-ctx.Done():
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled during retry loop for step %d", stepIndex+1))
			return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
		default:
		}

		// Prepare template variables for this specific step with individual fields
		// RESOLVE VARIABLES: Replace {{VARS}} with actual values for execution
		// Execution agent workspace path includes run folder: workspacePath/runs/{selectedRunFolder}/execution
		runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
		executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
		// Determine code execution mode: step config > workflow/preset default
		// Provider-based auto-enable is handled in applyStepConfigToAgentConfig.
		var isCodeExecutionMode bool
		agentConfigs := getAgentConfigs(step)
		if (agentConfigs == nil || agentConfigs.UseCodeExecutionMode == nil) && step.GetID() != "" {
			if stepConfigs, err := hcpo.ReadStepConfigs(ctx); err == nil {
				for _, sc := range stepConfigs {
					if sc.ID == step.GetID() && sc.AgentConfigs != nil {
						agentConfigs = sc.AgentConfigs
						break
					}
				}
			}
		}
		if agentConfigs != nil && agentConfigs.UseCodeExecutionMode != nil {
			isCodeExecutionMode = *agentConfigs.UseCodeExecutionMode
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using step-specific code execution mode: %v", isCodeExecutionMode))
		} else {
			isCodeExecutionMode = hcpo.GetUseCodeExecutionMode()
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Using workflow/preset code execution mode: %v", isCodeExecutionMode))
		}
		// Scripted code mode implies code execution mode
		if isScriptedMode {
			isCodeExecutionMode = true
			hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] Persistent scripted mode enabled for step %d — forcing code execution mode", stepIndex+1))
		}

		// Always use learnings folder (unified folder for all learning types)
		// Get execution folder path for this step. Sub-agent calls may override the
		// artifact folder while keeping the stable step ID for configs/learnings.
		stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, artifactStepID, artifactStepPath)
		if isScriptedMode && hcpo.usesCodeTree() {
			if err := createFolderViaAPI(ctx, hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)); err != nil {
				return "", updatedContextFiles, err
			}
		}
		// Ensure step execution folder exists (create if it was previously deleted)
		if err := hcpo.ensureStepExecutionFolderExists(ctx, stepExecutionPath); err != nil {
			// Non-blocking: log warning but continue execution (folder will be created when files are written)
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to ensure step execution folder exists: %v (continuing - folder will be created when files are written)", err))
		}
		// Resolve KB access mode for this step (explicit step config > preset default)
		kbAccess := resolveKnowledgebaseAccess(agentConfigs, hcpo.UseKnowledgebase())
		useKnowledgebase := kbAccess != KBAccessNone
		knowledgebasePath := ""
		if useKnowledgebase {
			knowledgebasePath = getKnowledgebasePath(hcpo.GetWorkspacePath())
		}

		// Get folder guard paths for template (so agent knows exact paths it can access)
		dbAccess := hcpo.resolveDBAccess(agentConfigs)
		folderGuardReadPaths, folderGuardWritePaths := hcpo.setupExecutionFolderGuard(artifactStepPath, artifactStepID, kbAccess, dbAccess, agentConfigs)
		// PLAT-556: what the description names under Inputs/Guides is readable (never writable).
		folderGuardReadPaths = common.DeduplicateStrings(appendDescriptionReferenceReadPaths(folderGuardReadPaths, hcpo.GetWorkspacePath(), step.GetDescription()))

		// Learn code mode: add code/ subdir to write paths so LLM can write main.py there
		if isScriptedMode {
			stepExecutionPathForGuard := getExecutionFolderPath(executionWorkspacePath, artifactStepID, artifactStepPath)
			folderGuardWritePaths = append(folderGuardWritePaths, stepExecutionPathForGuard+"/code")
		}

		// Build absolute paths for agent prompts using the workspace docs root.
		// Absolute paths are unambiguous — agents can use them directly in shell commands.
		// e.g., "Workflow/HRMS/runs/iteration-1/group-1/execution/step-3"
		//     → "/app/workspace-docs/Workflow/HRMS/runs/iteration-1/group-1/execution/step-3"
		workflowRoot := hcpo.GetWorkspacePath()
		docsRoot := GetPromptDocsRoot()
		toAbsPath := func(path string) string {
			if path == "" || docsRoot == "" {
				return path
			}
			if filepath.IsAbs(path) {
				return path
			}
			return workflowAbsPath(path)
		}

		stepTitleForPrompt := ResolveVariables(step.GetTitle(), hcpo.variableValues)
		stepDescriptionForPrompt := ResolveVariables(step.GetDescription(), hcpo.variableValues)
		if isScriptedMode {
			// In scripted mode the saved script is reused across groups/users, so keep
			// template placeholders in the task description instead of
			// injecting current-run values that the model might copy into main.py.
			stepDescriptionForPrompt = step.GetDescription()
		}

		contextDeps := step.GetContextDependencies()
		resolvedContextDeps := []string{}
		if len(contextDeps) > 0 {
			resolvedDeps := ResolveVariablesArray(contextDeps, hcpo.variableValues)
			resolvedContextDeps = hcpo.resolveDependencyPathsWithWorkspace(ctx, resolvedDeps, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, hcpo.variableValues)
		}

		// Inject VAR_GROUP_NAME early so it appears in the snapshotWorkspaceEnv used by ScriptedEnvVarNames.
		if hcpo.currentGroupName != "" {
			if envRef := hcpo.GetWorkspaceEnvRef(); envRef != nil {
				hcpo.LockWorkspaceEnv()
				envRef["VAR_GROUP_NAME"] = hcpo.currentGroupName
				hcpo.UnlockWorkspaceEnv()
			}
		}
		kbNotesPathForPrompt := toAbsPath(filepath.Join(getKnowledgebasePath(hcpo.GetWorkspacePath()), KBNotesFolderName))
		templateVars := map[string]string{
			"StepTitle":                 stepTitleForPrompt,
			"StepDescription":           stepDescriptionForPrompt,
			"StepSuccessCriteria":       "",
			"StepContextOutput":         ResolveVariables(step.GetContextOutput().String(), hcpo.variableValues),
			"WorkspacePath":             toAbsPath(executionWorkspacePath),             // Absolute execution folder path (e.g., "/app/workspace-docs/Workflow/HRMS/runs/...")
			"KnowledgebasePath":         toAbsPath(knowledgebasePath),                  // Absolute knowledgebase folder path
			"DBPath":                    toAbsPath(getDBPath(hcpo.GetWorkspacePath())), // Absolute db folder path (always enabled)
			"DBAccess":                  dbAccess,
			"DBDirectAccess":            fmt.Sprintf("%v", isScriptedMode),
			"UseKnowledgebase":          fmt.Sprintf("%v", useKnowledgebase),                                                                                  // Whether knowledgebase is enabled (deprecated, retained for backward compat)
			"KbAccess":                  kbAccess,                                                                                                             // KB access mode: "read" | "write" | "read-write" | "none"
			"KbAccessLabel":             kbAccessLabel(kbAccess),                                                                                              // Human-readable label for prompt display
			"KnowledgebaseContribution": kbContributionForPrompt(agentConfigs),                                                                                // Author's contribution instruction (direct mode surfaces it to the step)
			"KBGuidanceBlock":           sharedStepKBGuidance(hcpo.GetWorkspacePath(), kbAccess, kbContributionForPrompt(agentConfigs), kbNotesPathForPrompt), // Direct-mode-only KB contribution guidance
			"IsCodeExecutionMode":       fmt.Sprintf("%v", isCodeExecutionMode),                                                                               // Code execution mode flag (step-specific or preset)
			"StepNumber":                stepPath,                                                                                                             // Step identifier (e.g., "step-8" or "step-3-sub-fetch")
			"StepExecutionPath":         toAbsPath(stepExecutionPath),                                                                                         // Absolute step execution folder path
			"FolderGuardReadPaths":      folderGuardPromptText(hcpo.GetWorkspacePath(), folderGuardReadPaths, false),                                          // Absolute folder guard read paths
			"FolderGuardWritePaths":     folderGuardPromptText(hcpo.GetWorkspacePath(), folderGuardWritePaths, true),                                          // Absolute folder guard write paths
			"WorkflowRoot":              toAbsPath(workflowRoot),                                                                                              // Absolute workflow root path (e.g., "/app/workspace-docs/Workflow/HRMS")
			"IsScriptedMode":            fmt.Sprintf("%v", isScriptedMode),
			"GroupName":                 hcpo.currentGroupName,
			"ReferencedGuides":          hcpo.referencedGuidesForStep(ctx, step.GetID(), step.GetDescription()),
		}
		applySharedKBPrompt(hcpo.GetWorkspacePath(), templateVars)
		if scriptedDelegation, ok := scriptedDelegationFromContext(ctx); ok {
			templateVars["ScriptedDelegationInstructions"] = scriptedDelegation.Instructions
			if payload, marshalErr := json.Marshal(scriptedDelegation.Parameters); marshalErr == nil && len(scriptedDelegation.Parameters) > 0 {
				templateVars["ScriptedParameterValues"] = string(payload)
			}
		}

		// Inject workflow variables as VAR_* env vars and workspace path as VAR_WORKSPACE_PATH.
		// VAR_* passes through the shell whitelist (MCP_*, SECRET_*, VAR_*).
		// Available via os.environ["VAR_NAME"] in Python or $VAR_NAME in bash.
		if envRef := hcpo.GetWorkspaceEnvRef(); envRef != nil {
			hcpo.LockWorkspaceEnv()
			for k, v := range hcpo.variableValues {
				envRef["VAR_"+k] = v
			}
			// Also inject the workflow workspace path as an absolute Docker-visible path
			// so Python/shell code can use it directly without guessing the docs root.
			if wp := hcpo.GetWorkspacePath(); wp != "" {
				envRef["VAR_WORKSPACE_PATH"] = toAbsPath(wp)
			}
			hcpo.UnlockWorkspaceEnv()
		}

		// Add context dependencies with full absolute paths and inline small file contents.
		// This validates existence (pre-flight) and inlines small text files into the prompt
		// so the LLM doesn't waste tool calls reading them.
		if len(resolvedContextDeps) > 0 {
			formattedDeps, depsErr := hcpo.formatContextDependenciesWithContent(ctx, resolvedContextDeps, docsRoot)
			if depsErr != nil {
				return "", updatedContextFiles, depsErr
			}
			templateVars["StepContextDependencies"] = formattedDeps
		} else {
			templateVars["StepContextDependencies"] = ""
		}

		// Add variable names if available (same format as other agents)
		if variableNames := FormatVariableNames(hcpo.variablesManifest); variableNames != "" {
			templateVars["VariableNames"] = variableNames
		}

		// Build previous steps summary from completed steps (include execution outputs)
		previousStepsSummary := hcpo.buildPreviousStepsSummary(allSteps, stepIndex, previousContextFiles, previousExecutionResults)

		templateVars["PreviousStepsSummary"] = previousStepsSummary
		templateVars["PlanPosition"] = hcpo.buildStepPlacement(ctx, allSteps, stepIndex)
		if execCtx != nil && execCtx.WorkshopHumanInput != "" {
			templateVars["WorkshopHumanInput"] = execCtx.WorkshopHumanInput
			hcpo.GetLogger().Info(fmt.Sprintf("[WORKSHOP] Injecting human_input into step %q prompt (%d chars)", step.GetID(), len(execCtx.WorkshopHumanInput)))
		} else {
			templateVars["WorkshopHumanInput"] = ""
		}

		// Add validation schema to template variables so execution agent knows expected file structure
		validationSchema := getValidationSchema(step)
		if validationSchema != nil {
			validationSchemaJSON, err := json.MarshalIndent(validationSchema, "", "  ")
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to marshal validation schema for step %d: %v", stepIndex+1, err))
				templateVars["ValidationSchema"] = ""
			} else {
				templateVars["ValidationSchema"] = string(validationSchemaJSON)
			}
		} else {
			templateVars["ValidationSchema"] = ""
		}

		// Hand the step its own previous validation failures, the way a failed
		// scripted step is handed ScriptedPriorError. Without this an agentic step
		// receives the schema, writes output that violates it, prevalidation runs
		// afterwards and files a concern, and the next run reads an identical
		// prompt — so it writes the same wrong shape indefinitely.
		// deliver-briefing did exactly that: five concerns at seen_count 3, all
		// "$.delivery_status must exist but was not found", across three runs.
		//
		// Best-effort: a step that cannot read its own history should still run.
		templateVars["PriorValidationFailures"] = ""

		// Inner loop: Automatic retry logic
		var validationResponse *ValidationResponse

		// Learn code mode: attempt fast path execution with saved script (before any LLM work).
		if isScriptedMode {
			fastResult := hcpo.tryRunSavedScriptedScript(ctx, step, stepIndex, stepPath, allSteps,
				stepExecutionPath, executionWorkspacePath, dbAccess)
			savedScriptPath := filepath.Join(hcpo.scriptedSourceAbsolute(step.GetID()), "main.py")
			if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil {
				savedScriptPath = workflowAbsPath(stepExecutionPath, "code", "main.py")
			}
			if fastResult.RanScript {
				// Emit UI event for the saved-script execution attempt
				hcpo.emitScriptedExecutionEvent(ctx, step, stepIndex, stepPath,
					savedScriptPath, fastResult.Success, fastResult.ExitCode, fastResult.Output, fastResult.Error, 0, true)
			}
			// Retain the log receipt even for a pre-launch refusal.
			hcpo.saveScriptedFastPathLog(ctx, stepIndex, artifactStepID, artifactStepPath, savedScriptPath, fastResult)
			if fastResult.HarnessTimeout {
				return fastResult.Output, updatedContextFiles, fmt.Errorf("script for step %q exceeded the workspace timeout; check its saved run before retrying: %s", step.GetID(), fastResult.TimeoutError+". Log file: "+fastResult.LogFile)
			}
			if fastResult.HarnessFailure {
				return fastResult.Output, updatedContextFiles, fmt.Errorf("script for step %q has an unknown execution outcome; check its saved run before retrying: %s", step.GetID(), fastResult.HarnessError+". Log file: "+fastResult.LogFile)
			}
			if !fastResult.RanScript {
				return "", updatedContextFiles, fmt.Errorf("step %q has no saved main.py; ask this chat to create it. Log file: %s", step.GetID(), fastResult.LogFile)
			}
			if !fastResult.Success {
				return fastResult.Output, updatedContextFiles, fmt.Errorf("script for step %q failed: %s. Ask this chat to fix it. Log file: %s", step.GetID(), fastResult.Error, fastResult.LogFile)
			}
			executionResult = fastResult.Output
			validationResponse = &ValidationResponse{IsSuccessCriteriaMet: true, ExecutionStatus: "COMPLETED", Reasoning: "saved script executed and validated"}
		}

		// Main execution (single execution, no loop)
		// NOTE: No conversation history is passed to execution agent - all context via template variables
		if !isScriptedMode {
			// Check for context cancellation before execution
			select {
			case <-ctx.Done():
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled for step %d", stepIndex+1))
				return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
			default:
			}

			// Signal whether this workflow has agent-browser or CDP available. Downstream prompt builders use this to gate
			// the browser-specific main.py authoring rules + DOM probe so non-browser workflows
			// don't pay the prompt tax. Note: empty browserMode means "auto-detect", NOT "no
			// browser" — HasBrowserCapability() checks registered servers+skills directly.
			templateVars["HasBrowserAccess"] = fmt.Sprintf("%t", hcpo.HasBrowserCapability())

			// Resolve variables in step title before using in agent name
			resolvedTitle := ResolveVariables(step.GetTitle(), hcpo.variableValues)
			sanitizedTitle := hcpo.sanitizeTitleForAgentName(resolvedTitle)

			// Track if validation failed after exhausting all retry attempts
			validationFailedAfterMaxRetries := false
			var validationFailures []validationFailureConcern

			// Track which LLM model was used for execution (to be stored in learning metadata)
			var executionLLM string

			// executionAgent persists across retries so retry N>1 can continue the
			// existing conversation (sending validation feedback as a user message)
			// instead of re-running the whole step from scratch.
			var executionAgent agents.OrchestratorAgent

			// Retry loop: Execute with validation feedback, reusing the same learning history
			// Fires an [AUTO-NOTIFICATION] into the builder chat on the FIRST
			// pre-validation failure for this step this run — before retries
			// are exhausted — so the builder can assess and intervene while
			// the step is still retrying, rather than only learning about it
			// once the whole step eventually fails outright. Deliberately
			// once-per-step, not once-per-attempt: firing on every attempt
			// would spam the chat for a step that recovers on retry 2.
			preValidationNotifiedThisStep := false
			for retryAttempt := 1; retryAttempt <= maxRetryAttempts; retryAttempt++ {
				// Check for context cancellation before retry attempt
				select {
				case <-ctx.Done():
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled during retry attempt %d for step %d", retryAttempt, stepIndex+1))
					return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
				default:
				}

				hcpo.GetLogger().Info(fmt.Sprintf("🔄 Executing step %d/%d (attempt %d/%d): %s", stepIndex+1, totalSteps, retryAttempt, maxRetryAttempts, step.GetTitle()))

				// Add validation feedback to template variables if this is a retry
				if retryAttempt > 1 && validationResponse != nil {
					contextStr := fmt.Sprintf("Validation Feedback (Retry Attempt %d)", retryAttempt)
					templateVars["ValidationFeedback"] = hcpo.formatValidationResponseForTemplate(validationResponse, contextStr)
				} else {
					templateVars["ValidationFeedback"] = "" // No validation feedback for first attempt
				}

				// Step 2: Create and execute Execution-Only Agent with learning history (reused from above)
				executionAgentName := fmt.Sprintf("%s-execution-%s", stepPath, sanitizedTitle)
				// Add validation retry suffix if this is a retry after validation failure (val-2, val-3, etc.)
				if retryAttempt > 1 {
					executionAgentName = fmt.Sprintf("%s-val-%d", executionAgentName, retryAttempt)
				}
				// Add learning history to template vars for execution-only agent (reused for all retry attempts)
				// If a saved main.py exists for this step, point to the execution/code/ copy (not learnings/)
				// so the LLM doesn't reference or hardcode learnings paths in generated scripts.
				savedScriptGuidance := ""
				execCodeMainPyRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"
				if _, mainPyReadErr := hcpo.ReadWorkspaceFile(ctx, execCodeMainPyRelPath); mainPyReadErr == nil {
					execCodeMainPyAbsPath := filepath.Join(toAbsPath(hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)), "main.py")
					if savedScriptGuidance != "" {
						savedScriptGuidance += "\n\n"
					}
					savedScriptGuidance += fmt.Sprintf("📜 **Saved script available** at `%s` — this is a working implementation from a previous run. Read it before starting, then use `diff_patch_workspace_file` to update it rather than rewriting the entire file.", execCodeMainPyAbsPath)
				}
				templateVars["SavedScriptGuidance"] = savedScriptGuidance

				// Check for context cancellation before creating execution agent
				select {
				case <-ctx.Done():
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled before creating execution agent for step %d", stepIndex+1))
					return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
				default:
				}

				agentConfigs := getAgentConfigs(step)
				executionAgentCtx := ctx
				var capturedToolCalls []orchestrator.ToolCallEntry
				var capturedLLMCalls []orchestrator.LLMCallEntry
				var attemptStartedAt time.Time
				var attemptCompletedAt time.Time
				var attemptDuration time.Duration
				var timingCaptureCtx context.Context

				shouldContinue := retryAttempt > 1
				if shouldContinue && (executionAgent == nil || len(executionConversationHistory) == 0) {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step %d attempt %d: continuation requested but agent/history missing — falling back to fresh agent", stepIndex+1, retryAttempt))
					shouldContinue = false
				}

				timingCaptureCtx = executionAgentCtx

				if !shouldContinue {
					// Fresh-agent path: close prior (if any) and create a new execution agent.
					if executionAgent != nil {
						_ = executionAgent.Close()
						executionAgent = nil
					}

					// Pass stepPath to createExecutionOnlyAgent so nested sub-agent folders resolve correctly.
					// For learnings / metadata selection, use the concrete step ID so sub-agents align with their own learnings folder.
					// allSteps is already []PlanStepInterface - no conversion needed
					executionAgent, err = hcpo.createExecutionOnlyAgent(executionAgentCtx, "execution_only", stepPath, executionAgentName, agentConfigs, step, step.GetID(), getExecutionArtifactFolderOverride(execCtx))
					if err != nil {
						return "", updatedContextFiles, fmt.Errorf("failed to create execution-only agent for step %d: %w", stepIndex+1, err)
					}

					// Check for context cancellation before executing agent
					select {
					case <-ctx.Done():
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled before agent execution for step %d", stepIndex+1))
						return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
					default:
					}

					// Sync the transport-level code execution flag from the resolved agent config.
					if executionAgent.GetConfig() != nil {
						templateVars["IsCodeExecutionMode"] = fmt.Sprintf("%v", executionAgent.GetConfig().UseCodeExecutionMode)
					}

					// Pre-save prompts.json so get_step_prompts works during execution (not just after)
					// Include appended supplementary prompts (skills, browser/CDP, secrets) to match
					// what the agent actually sees at runtime (SetSystemPrompt re-appends them).
					if eoa, ok := executionAgent.(*WorkflowExecutionOnlyAgent); ok {
						preSystemPrompt := eoa.executionOnlySystemPromptProcessor(templateVars)
						if ba := executionAgent.GetBaseAgent(); ba != nil {
							if ba.Agent() != nil {
								if err := ba.ApplyInstructions(ctx, preSystemPrompt, true); err != nil {
									return "", updatedContextFiles, fmt.Errorf("prepare execution agent instructions: %w", err)
								}
								preSystemPrompt = ba.Agent().Definition().Instructions
							}
						}
						preUserMessage := eoa.executionOnlyUserMessageProcessor(templateVars)
						preExecLLM := agentConfigModelLabel(executionAgent.GetConfig())
						fb := fmt.Sprintf("execution-attempt-%d-iteration-%d", retryAttempt, 0)
						hcpo.preSavePromptsJSON(stepIndex, artifactStepID, artifactStepPath, "execution_only", preSystemPrompt, preUserMessage, preExecLLM, fb+"-prompts.json")
					}

					// Learn code mode: ensure code/ subdirectory exists (don't clean — LLM's
					// previous fix may be there and will be overwritten only if the LLM writes a new version).
					if isScriptedMode {
						codeDirRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)
						if mkErr := createFolderViaAPI(ctx, codeDirRelPath); mkErr != nil {
							hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to pre-create code/ dir for step %d: %v", stepIndex+1, mkErr))
						}
					}

					if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
						timingCaptureCtx = cab.StartTimingCaptureFor(timingCaptureCtx)
					}
					attemptStartedAt = time.Now().UTC()
					hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseMainExecution, workflowContinuationStatusRunning, "", executionAgent)
					// Execute execution-only agent with learning history (reused from learning reading above)
					executionResult, executionConversationHistory, err = hcpo.withWorkshopMessageTarget(timingCaptureCtx, step.GetID(), "execution", executionAgent, func() (string, []llmtypes.MessageContent, error) {
						return executionAgent.Execute(timingCaptureCtx, templateVars, []llmtypes.MessageContent{})
					})
				} else {
					// Continuation path: send validation feedback as a follow-up user
					// message on the existing agent. The system prompt, tool state, and
					// working memory from prior attempts are all preserved — the agent
					// sees its own prior tool calls + outputs and can fix surgically.
					feedbackUserMsg := buildValidationContinuationUserMessage(validationResponse, retryAttempt)
					hcpo.GetLogger().Info(fmt.Sprintf("🔁 Step %d attempt %d/%d: continuing existing execution agent with validation feedback (history=%d turns)",
						stepIndex+1, retryAttempt, maxRetryAttempts, len(executionConversationHistory)))

					ba := executionAgent.GetBaseAgent()
					if ba == nil {
						return "", updatedContextFiles, fmt.Errorf("execution agent has no base agent for continuation on step %d attempt %d", stepIndex+1, retryAttempt)
					}
					if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
						timingCaptureCtx = cab.StartTimingCaptureFor(timingCaptureCtx)
					}
					attemptStartedAt = time.Now().UTC()
					hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseMainExecution, workflowContinuationStatusRunning, "", executionAgent)
					executionResult, executionConversationHistory, err = hcpo.withWorkshopMessageTarget(timingCaptureCtx, step.GetID(), "validation-retry", executionAgent, func() (string, []llmtypes.MessageContent, error) {
						return ba.Execute(timingCaptureCtx, feedbackUserMsg, executionConversationHistory, "", false)
					})
				}
				attemptCompletedAt = time.Now().UTC()
				attemptDuration = attemptCompletedAt.Sub(attemptStartedAt)
				if cab, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge); ok {
					timingCapture := cab.DrainTimingCaptureFor(timingCaptureCtx)
					capturedToolCalls = timingCapture.ToolCalls
					capturedLLMCalls = timingCapture.LLMCalls
				}

				// Capture conversation history for callers that need it (e.g., get_sub_agent_conversation tool)
				if execCtx != nil && execCtx.ConversationHistoryCapture != nil {
					*execCtx.ConversationHistoryCapture = executionConversationHistory
				}

				// CAPTURE EXECUTION LLM: Get the model used for execution (to be stored in learning metadata)
				if executionAgent != nil && executionAgent.GetConfig() != nil {
					config := executionAgent.GetConfig()
					if config.LLMConfig.Primary.ModelID != "" {
						executionLLM = fmt.Sprintf("%s/%s", config.LLMConfig.Primary.Provider, config.LLMConfig.Primary.ModelID)
					}
				}

				// CAPTURE TURN COUNT: Calculate total LLM turns from conversation history
				// Each turn consists of a user message and an assistant response (including tool calls)

				if err != nil {
					// Execution errors
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step %d execution failed (attempt %d): %v", stepIndex+1, retryAttempt, err))
					hcpo.recordWorkflowContinuationPhase(context.Background(), artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseMainExecution, workflowContinuationStatusFailed, err.Error(), executionAgent)
					// Save partial conversation history on failure/cancellation so users can inspect
					// tool responses from interrupted executions via debug_step or log files.
					if len(executionConversationHistory) > 0 {
						hcpo.GetLogger().Info(fmt.Sprintf("[PARTIAL-LOGS] Saving partial execution logs for step %d (%s) — %d conversation entries, error: %v", stepIndex+1, stepPath, len(executionConversationHistory), err))
						if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
							fmt.Sprintf("FAILED: %v", err), executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
							hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
						}
					} else {
						hcpo.GetLogger().Warn(fmt.Sprintf("[PARTIAL-LOGS] No conversation history to save for step %d (%s) — execution failed before any LLM turns", stepIndex+1, stepPath))
					}

					if retryAttempt >= maxRetryAttempts {
						hcpo.GetLogger().Error(fmt.Sprintf("❌ Step %d execution failed after %d attempts, exiting retry loop", stepIndex+1, maxRetryAttempts), nil)
						break // Exit retry loop - will proceed to human feedback
					}
					continue // Retry on next attempt
				}

				hcpo.GetLogger().Info(fmt.Sprintf("✅ Step %d execution completed successfully (attempt %d)", stepIndex+1, retryAttempt))
				executionResult = withValidationFailureConcern(executionResult, validationFailures, 0, false)
				hcpo.recordWorkflowContinuationPhase(context.Background(), artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseMainExecution, workflowContinuationStatusCompleted, "", executionAgent)

				// Save execution logs (result, conversation history, system prompt)
				if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
					executionResult, executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
					hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
					hcpo.GetLogger().Warn(fmt.Sprintf("Step %s completed, but its required execution result could not be persisted: %v", artifactStepID, logErr))
				}

				// Run pre-validation (code-based structural checks) -- always active, independent of LLM validation.
				agentConfigs = getAgentConfigs(step)
				hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhasePreValidation, workflowContinuationStatusRunning, "", executionAgent)
				preValidationSchema := getValidationSchema(step)
				var preValidationResults *WorkspaceVerificationResult
				var preValidationErr error
				if preValidationResults == nil {
					preValidationResults, preValidationErr = RunPreValidation(ctx, preValidationSchema, stepExecutionPath, hcpo.BaseOrchestrator)
				}
				if preValidationErr != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("Pre-validation error for step %d: %v", stepIndex+1, preValidationErr))
					preValidationResults = &WorkspaceVerificationResult{
						OverallPass:  false,
						FilesChecked: []FileCheckResult{},
						Summary: ValidationSummary{
							TotalChecks:  0,
							PassedChecks: 0,
							FailedChecks: 1,
							SchemaErrors: 0,
							Errors: []ValidationError{{
								File:      "",
								Path:      "",
								CheckType: "pre_validation_error",
								Expected:  "pre-validation to run successfully",
								Actual:    "error occurred",
								Message:   fmt.Sprintf("Pre-validation failed to run: %v", preValidationErr),
							}},
							SchemaWarnings: []ValidationError{},
						},
					}
				} else if preValidationResults == nil && (preValidationSchema == nil || len(preValidationSchema.Files) == 0) {
					hcpo.GetLogger().Info(fmt.Sprintf("Pre-validation skipped for step %d (no validation schema)", stepIndex+1))
				}
				hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, isNestedExecution, preValidationResults)
				preValidationStatus := workflowContinuationStatusCompleted
				preValidationError := ""
				if preValidationResults != nil && !preValidationResults.OverallPass {
					preValidationStatus = workflowContinuationStatusFailed
					preValidationError = formatWorkspaceResults(preValidationResults)
				}
				hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhasePreValidation, preValidationStatus, preValidationError, executionAgent)

				if hcpo.selectedRunFolder != "" {
					preValLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
					mode := "agentic"
					if isScriptedMode {
						mode = "scripted"
					}
					SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValLogPath, step.GetID(), stepPath, preValidationResults, preValidationSchema, hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
						PreValidationAttempt{ExecutionMode: mode, ValidationPhase: "final-gate", ExecutionAttempt: retryAttempt, ValidationAttempt: 1})
				}

				// Build validation response based on pre-validation results
				if !preValidationResults.OverallPass {
					hcpo.GetLogger().Warn(fmt.Sprintf("Pre-validation failed for step %d - rejecting", stepIndex+1))
					// One [AUTO-NOTIFICATION] per step per run, on the FIRST
					// failure only (see preValidationNotifiedThisStep above the
					// retry loop) -- not the same case as
					// completeAgentItemNotification's deliberately
					// silent per-ITEM prevalidation gate (controller_message_
					// sequence.go): that one fires up to once per item and the
					// step's own eventual completion notification already
					// covers it. This is the step's overall gate, fires at
					// most once total, and specifically exists so the builder
					// hears about a real structural problem while the step is
					// still burning retries -- not only after all of them are
					// exhausted.
					if !preValidationNotifiedThisStep && hcpo.workshopExecutionNotifier != nil {
						preValidationNotifiedThisStep = true
						notifyID := fmt.Sprintf("prevalidation-warn-%s-%d", step.GetID(), time.Now().UnixNano())
						notifyName := fmt.Sprintf("Pre-validation check: %s", step.GetTitle())
						hcpo.workshopExecutionNotifier.OnExecutionStart(WorkshopExecutionStart{
							ID:                notifyID,
							ParentExecutionID: currentWorkshopParentExecutionID(ctx),
							Name:              notifyName,
							Kind:              "prevalidation_warning",
							Metadata:          map[string]string{"step_id": step.GetID(), "step_path": stepPath},
						})
						hcpo.workshopExecutionNotifier.OnExecutionComplete(notifyID, notifyName, formatWorkspaceResults(preValidationResults),
							map[string]string{"step_id": step.GetID(), "step_path": stepPath},
							fmt.Errorf("pre-validation failed on attempt %d/%d — will retry unless attempts are exhausted", retryAttempt, maxRetryAttempts))
					}
					validationResponse = &ValidationResponse{
						IsSuccessCriteriaMet: false,
						ExecutionStatus:      "FAILED",
						Reasoning:            formatWorkspaceResults(preValidationResults) + "\n\nPre-validation failed - structural issues must be fixed before the step can complete.",
						Feedback: []ValidationFeedback{{
							Type:        "structural_validation",
							Description: "Pre-validation failed - output structure does not meet requirements",
							Severity:    "HIGH",
						}},
					}
				} else {
					hcpo.GetLogger().Info(fmt.Sprintf("Pre-validation passed for step %d - auto-approving", stepIndex+1))

					// Validation completes the task; no hidden learning turn follows.
					validationResponse = &ValidationResponse{
						IsSuccessCriteriaMet: true,
						ExecutionStatus:      "COMPLETED",
						Reasoning:            "Pre-validation passed - step auto-approved",
					}

				}

				// Check if success criteria was met
				// Check IsSuccessCriteriaMet instead of just ExecutionStatus - PARTIAL/INCOMPLETE can also mean criteria not met
				if validationResponse != nil && validationResponse.IsSuccessCriteriaMet {
					if len(validationFailures) > 0 {
						executionResult = withValidationFailureConcern(executionResult, validationFailures, retryAttempt, false)
						if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
							executionResult, executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
							hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
						}
					}

					hcpo.GetLogger().Info(fmt.Sprintf("✅ Step %d passed validation - success criteria met (Status: %s)", stepIndex+1, validationResponse.ExecutionStatus))

					break // Exit retry loop and continue to next step
				} else {
					statusStr := "unknown"
					if validationResponse != nil {
						statusStr = validationResponse.ExecutionStatus
					}
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step %d failed validation - success criteria not met (Status: %s, attempt %d/%d)", stepIndex+1, statusStr, retryAttempt, maxRetryAttempts))
					validationFailures = append(validationFailures, newValidationFailureConcern(retryAttempt, validationResponse))
					exhausted := retryAttempt >= maxRetryAttempts
					executionResult = withValidationFailureConcern(executionResult, validationFailures, 0, exhausted)
					if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
						executionResult, executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
						hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
					}

					// Increment validation failure count for UI display
					if err := hcpo.IncrementValidationFailureCount(ctx, stepPath); err != nil {
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to increment validation failure count for %s: %v", stepPath, err))
					}

					if exhausted {
						hcpo.GetLogger().Error(fmt.Sprintf("❌ Step %d failed validation after %d attempts", stepIndex+1, maxRetryAttempts), nil)
						// Mark that validation failed after exhausting all retries
						validationFailedAfterMaxRetries = true
						break
					} else {
						hcpo.GetLogger().Info(fmt.Sprintf("🔄 Retrying step %d execution with validation feedback", stepIndex+1))
						// Note: conversation history is preserved from previous attempts for context
						// Explicitly continue to next retry attempt
						continue
					}
				}
			} // End of retry loop

			// Exit immediately if validation failed after exhausting all retry attempts
			if validationFailedAfterMaxRetries {
				hcpo.GetLogger().Error(fmt.Sprintf("🛑 Step %d failed validation after %d attempts - exiting workflow", stepIndex+1, maxRetryAttempts), nil)
				var validationDetails string
				if validationResponse != nil {
					validationDetails = fmt.Sprintf("Success Criteria Met: %v, Status: %s", validationResponse.IsSuccessCriteriaMet, validationResponse.ExecutionStatus)
					if validationResponse.Reasoning != "" {
						validationDetails += fmt.Sprintf(", Reasoning: %s", validationResponse.Reasoning)
					}
				} else {
					validationDetails = "No validation response available"
				}
				err := fmt.Errorf("step %d failed validation after %d retry attempts. %s. Please review the execution results and update the plan if needed", stepIndex+1, maxRetryAttempts, validationDetails)
				return executionResult, updatedContextFiles, err
			}

			// Exit immediately if execution failed after exhausting all retry attempts.
			// Without this check, sub-agent execution errors are silently swallowed:
			// the error is non-nil but the function returns nil error at the end,
			// causing the orchestrator to think the sub-agent completed successfully.
			if err != nil {
				hcpo.GetLogger().Error(fmt.Sprintf("🛑 Step %d execution failed after %d attempts - propagating error", stepIndex+1, maxRetryAttempts), err)
				return executionResult, updatedContextFiles, fmt.Errorf("step %d execution failed after %d retry attempts: %w", stepIndex+1, maxRetryAttempts, err)
			}

		} // End of main execution block

		// BLOCKING HUMAN FEEDBACK - Ask user if they want to continue to next step
		// If user provides feedback (doesn't approve), stop workflow and ask user to manually update plan
		// SKIP HUMAN INPUT MODE: Skip human feedback but keep learning enabled
		// DECISION INNER STEP: Skip human feedback on success (decision step will handle routing)
		// SUB-AGENT: Never request human feedback (sub-agents run automatically)
		// NORMAL MODE: Always request human feedback before moving to next step
		isSkipHumanInput := execCtx.SkipHumanInput

		var approved bool
		var feedback string

		// For sub-agents, never request human feedback (they run automatically as part of orchestration)
		if isSubAgent {
			hcpo.GetLogger().Info(fmt.Sprintf("🤖 Sub-agent %d - auto-approving without human feedback (sub-agents never request human feedback)", stepIndex+1))
			approved = true
			feedback = "" // No feedback for sub-agents
		} else if hcpo.runSingleStepOnly {
			// Single-step mode (workshop / run-single-step UI): no next step to continue with
			hcpo.GetLogger().Info(fmt.Sprintf("🔧 Single-step mode: Auto-approving step %d without human feedback (no next step)", stepIndex+1))
			approved = true
			feedback = ""
		} else if isSkipHumanInput {
			hcpo.GetLogger().Info(fmt.Sprintf("⚡ Skip human input mode: Auto-approving step %d without human feedback (learning will still run)", stepIndex+1))
			approved = true
			feedback = "" // No feedback in skip human input mode
		} else {
			// Auto-approve: no human feedback between steps — execution continues automatically
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Step %d/%d completed — auto-approving (no inter-step human feedback)", stepIndex+1, totalSteps))
			approved = true
			feedback = ""
		}

		// Store human feedback for future steps (even if approved, user might have provided guidance)
		// Note: humanFeedbackHistory is not available in this function scope, so we skip storing it
		// It will be handled by the caller if needed

		if approved {
			// User approved - mark step as completed and exit outer loop
			// Nested executions belong to their parent and do not advance top-level progress.
			if !isNestedExecution {
				hcpo.addCompletedStepIndex(progress, stepIndex)
				// Always save progress after marking a step as completed (both fast and normal mode)
				if err := hcpo.saveStepProgress(ctx, progress); err != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save step progress: %v", err))
				} else {
					hcpo.GetLogger().Info(fmt.Sprintf("✅ Step %d/%d marked as completed and saved - Total completed: %d/%d", stepIndex+1, totalSteps, len(progress.CompletedStepIndices), progress.TotalSteps))
				}

				// Emit step token usage summary
				stepTitle := step.GetTitle()
				if stepTitle == "" {
					stepTitle = fmt.Sprintf("Step %d", stepIndex+1)
				}
				stepID := step.GetID()
				if stepID == "" {
					stepID = fmt.Sprintf("step-%d", stepIndex+1)
				}
				hcpo.EmitStepTokenUsage(ctx, "execution", stepIndex, stepID, stepTitle, false) // Don't clear - keep for potential future queries
				// Note: Token usage is now persisted in real-time on each token_usage event, not just at step completion
			} else {
				hcpo.GetLogger().Info(fmt.Sprintf("✅ Nested execution %d completed (not updating top-level progress)", stepIndex+1))
			}
			stepCompleted = true
		} else {
			// User provided feedback (didn't approve) - stop workflow and ask user to manually update plan
			hcpo.GetLogger().Info(fmt.Sprintf("🛑 User provided feedback - stopping workflow. Feedback: %s", feedback))
			planPath := fmt.Sprintf("%s/planning/plan.json", hcpo.GetWorkspacePath())
			return executionResult, updatedContextFiles, fmt.Errorf("workflow stopped: user feedback received. please manually update the plan at %s with the following feedback, then restart the workflow: %s", planPath, feedback)
		}
	} // End of outer loop for step execution

	// Append step's context output to context files if it exists
	contextOutput := step.GetContextOutput().String()
	if contextOutput != "" {
		updatedContextFiles = append(updatedContextFiles, contextOutput)
	}
	finalExecutionResult := executionResult
	if strings.TrimSpace(finalExecutionResult) == "" {
		finalExecutionResult = "STATUS: COMPLETED"
	}
	if persistErr := hcpo.saveFinalExecutionSummary(artifactStepID, artifactStepPath, finalExecutionResult); persistErr != nil {
		hcpo.recordRunPersistenceError(context.Background(), artifactStepID, persistErr)
		hcpo.GetLogger().Warn(fmt.Sprintf("Step %s completed, but its final execution summary could not be persisted: %v", artifactStepID, persistErr))
	}

	// step_finished (status="end") is emitted by the defer at the top of this
	// function, which covers this success path AND every early return.
	return executionResult, updatedContextFiles, nil
}

// ============================================================================
// STEP TYPE DETECTION HELPERS (for PlanStepInterface)
// ============================================================================
// These helper functions provide a cleaner way to detect step types from PlanStepInterface
// boolean flags, making the execution routing logic more maintainable and
// preparing for future migration to type-safe step types.

const validationConcernLinePrefix = "CONCERNS: Validation history:"

type validationFailureConcern struct {
	Attempt int
	Detail  string
}

func newValidationFailureConcern(attempt int, response *ValidationResponse) validationFailureConcern {
	detail := "success criteria were not met"
	if response != nil {
		switch {
		case strings.TrimSpace(response.Reasoning) != "":
			detail = compactValidationConcernText(response.Reasoning, 360)
		case len(response.Feedback) > 0:
			parts := make([]string, 0, len(response.Feedback))
			for _, feedback := range response.Feedback {
				if description := strings.TrimSpace(feedback.Description); description != "" {
					parts = append(parts, description)
				}
			}
			if len(parts) > 0 {
				detail = compactValidationConcernText(strings.Join(parts, "; "), 360)
			}
		case strings.TrimSpace(response.ExecutionStatus) != "":
			detail = "validator returned status " + strings.TrimSpace(response.ExecutionStatus)
		}
	}
	return validationFailureConcern{Attempt: attempt, Detail: detail}
}

func compactValidationConcernText(value string, maxRunes int) string {
	compact := strings.Join(strings.Fields(value), " ")
	if compact == "" || maxRunes <= 0 {
		return compact
	}
	runes := []rune(compact)
	if len(runes) <= maxRunes {
		return compact
	}
	return string(runes[:maxRunes]) + "..."
}

// withValidationFailureConcern adds one controller-owned concern immediately
// before the terminal STATUS line. It replaces an earlier controller validation
// line so the same attempt can first be persisted as retry-pending and later as
// corrected or unresolved without accumulating duplicate concerns.
func withValidationFailureConcern(result string, failures []validationFailureConcern, resolvedAttempt int, exhausted bool) string {
	trimmed := strings.TrimSpace(result)
	if len(failures) == 0 {
		return trimmed
	}

	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		parts = append(parts, fmt.Sprintf("attempt %d failed - %s", failure.Attempt, failure.Detail))
	}
	outcome := "retry pending"
	if resolvedAttempt > 0 {
		outcome = fmt.Sprintf("corrected on attempt %d", resolvedAttempt)
	} else if exhausted {
		outcome = fmt.Sprintf("unresolved after %d attempt(s)", failures[len(failures)-1].Attempt)
	}
	concernLine := validationConcernLinePrefix + " " + compactValidationConcernText(strings.Join(parts, "; ")+"; "+outcome+".", 1200)

	lines := strings.Split(trimmed, "\n")
	kept := make([]string, 0, len(lines)+1)
	validationPrefixUpper := strings.ToUpper(validationConcernLinePrefix)
	for _, line := range lines {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), validationPrefixUpper) {
			continue
		}
		kept = append(kept, line)
	}

	insertAt := len(kept)
	for i := len(kept) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(kept[i])), "STATUS:") {
			insertAt = i
			break
		}
	}
	kept = append(kept, "")
	copy(kept[insertAt+1:], kept[insertAt:])
	kept[insertAt] = concernLine
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func summarizeExecutionResultForNotification(result string) string {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return ""
	}

	const maxSummaryRunes = 4000
	runes := []rune(trimmed)
	if len(runes) > maxSummaryRunes {
		return "… (earlier response omitted)\n" + string(runes[len(runes)-maxSummaryRunes:])
	}
	return trimmed
}

func buildDirectModeCompletionSummary(mainSummary, kbSummary, learningsSummary string) string {
	var parts []string
	if s := strings.TrimSpace(mainSummary); s != "" {
		parts = append(parts, fmt.Sprintf("Execution: %s", s))
	}
	if s := strings.TrimSpace(kbSummary); s != "" {
		parts = append(parts, fmt.Sprintf("KB review: %s", s))
	}
	if s := strings.TrimSpace(learningsSummary); s != "" {
		parts = append(parts, fmt.Sprintf("Learnings: %s", s))
	}
	return strings.Join(parts, "\n")
}

// isDelegatingAgentStep returns true if the step is a todo task step (orchestrator with todo list management)
func isDelegatingAgentStep(step PlanStepInterface) bool {
	agent, ok := step.(*AgentPlanStep)
	return ok && len(agent.PredefinedRoutes) > 0
}

// isRoutingStep returns true if the step is a deterministic N-way switch --
// either a routing step (now the "route"/major-fork concept) or a branch
// step (the small in-flow decision). Both share the exact same executor and
// deterministic-only behavior (no learnings, no agent execution); see
// routeSwitchStep in planning_agent.go and PLAT-259.
func isRoutingStep(step PlanStepInterface) bool {
	switch step.(type) {
	case *RoutingPlanStep, *BranchPlanStep:
		return true
	default:
		return false
	}
}

// nextStepIDForSelectedRoute resolves where execution continues after a
// routing/branch step's route has been selected -- the ID it returns becomes
// the next step in the run. Empty for anything that isn't a routeSwitchStep,
// or a route_id that doesn't match any of the step's routes. Extracted as a
// standalone function (was inline in the execution loop) specifically so it
// has direct test coverage for both RoutingPlanStep and BranchPlanStep,
// after an independent review found the inline version only ever
// type-asserted *RoutingPlanStep and silently left a branch step's target
// empty. See PLAT-259.
func nextStepIDForSelectedRoute(step PlanStepInterface, selectedRouteID string) string {
	routingStep, ok := step.(routeSwitchStep)
	if !ok {
		return ""
	}
	for _, route := range routingStep.GetRoutes() {
		if route.RouteID == selectedRouteID {
			return route.NextStepID
		}
	}
	return ""
}

func isAgentStep(step PlanStepInterface) bool {
	_, ok := step.(*AgentPlanStep)
	return ok
}

// executionLogEvidenceSuffixes are the four files saveExecutionConversationLogs
// writes for one dispatch, all sharing a filenameBase.
var executionLogEvidenceSuffixes = []string{"", "-conversation", "-timing", "-prompts"}

// archiveSupersededExecutionLogs moves a previous dispatch's execution evidence
// into a `superseded/` subfolder so the imminent write cannot destroy it.
//
// PLAT-176. Execution evidence is named
// `execution-attempt-{retryAttempt}-iteration-{loopIteration}`, and both counters
// are local to a single dispatch of a step: retryAttempt comes from this
// function's own `for retryAttempt := 1; ...` loop, and agent passes a
// literal 1 with a per-entry turn number. Nothing in the name identifies WHICH
// dispatch produced it, so a step that runs again -- a route re-entry, an
// operator re-run, a gate sending work back -- recomputes the identical path and
// overwrites the earlier run in place.
//
// Confirmed live on customer-login (2026-08-22): `execute-browser-and-capture-apis`
// was dispatched 5 times in one run and every dispatch clobbered the last.
// Reading one timing file minutes apart returned two different runs entirely
// (2675000ms/21 tool calls, then 104248ms/14 tool calls). The run's own logs
// could not show the step had run more than once, so a diagnosis built on them
// was wrong, and the real history had to be recovered from server_debug.log.
// That is the cost this prevents: a looping run erases the evidence that it
// looped, defeating exactly the after-the-fact review meant to catch it.
//
// Archived copies go into a subfolder rather than a sibling name on purpose:
// readers list this directory with an `execution-attempt-*` prefix glob
// (cmd/server/workflow.go) and parse the counters back out with Sscanf, so a
// stamped sibling would show up as a phantom extra attempt and misparse. A
// subfolder is invisible to those globs while staying trivially discoverable.
//
// Best-effort by design: this is evidence retention, not correctness. A failure
// here is logged and execution continues -- losing an archive is bad, failing a
// step because an archive could not be written would be worse.
func (hcpo *StepBasedWorkflowOrchestrator) archiveSupersededExecutionLogs(ctx context.Context, logDir, filenameBase string) {
	primary := fmt.Sprintf("%s/%s.json", logDir, filenameBase)
	exists, err := hcpo.CheckWorkspaceFileExists(ctx, primary)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [LOG_RETENTION] Could not check %s before overwrite: %v (proceeding; a prior dispatch's evidence may be lost)", primary, err))
		return
	}
	if !exists {
		// First dispatch, the overwhelmingly common case: nothing to preserve.
		return
	}

	// A timestamp keeps the archive human-readable; the UUID makes the archive
	// collision-proof even when a fast failure/re-dispatch cycle finishes more
	// than once inside the same second. The workspace move endpoint rejects an
	// existing destination, so timestamp-only names can silently lose the newer
	// dispatch's evidence when the canonical file is subsequently overwritten.
	stamp := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405Z"), uuid.NewString())
	archived := 0
	for _, suffix := range executionLogEvidenceSuffixes {
		src := fmt.Sprintf("%s/%s%s.json", logDir, filenameBase, suffix)
		if ok, checkErr := hcpo.CheckWorkspaceFileExists(ctx, src); checkErr != nil || !ok {
			continue
		}
		dst := fmt.Sprintf("%s/superseded/%s%s-%s.json", logDir, filenameBase, suffix, stamp)
		if moveErr := hcpo.MoveWorkspaceFile(ctx, src, dst); moveErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [LOG_RETENTION] Failed to preserve %s before overwrite: %v", src, moveErr))
			continue
		}
		archived++
	}
	if archived > 0 {
		hcpo.GetLogger().Info(fmt.Sprintf("📚 [LOG_RETENTION] Step re-dispatched: preserved %d file(s) from the previous dispatch under %s/superseded/ (stamp %s) instead of overwriting them", archived, logDir, stamp))
	}
}

// refreshStepAgentConfigsBeforeExecution re-reads step_config.json and
// repopulates this one step's runtime AgentConfigs immediately before it
// executes, so an update_step_config change made after the run started —
// but before this step's own dispatch — actually takes effect.
//
// PLAT-174. Before this, step_config.json was read exactly once per run
// (controller.go's populateRuntimeFields sweep, at run start), and every
// step for the rest of that run carried whatever snapshot existed at that
// moment. A change landing on disk after the run started never reached a
// step that had not begun yet, no matter how far ahead of that step's own
// turn it was made — confirmed live when a pin to execute-browser-and-capture-apis,
// saved 16 minutes before that step began, still ran on the pre-run-start
// default for all three of that step's executions.
//
// A read failure here (transient I/O, a mid-write file) must not abort step
// dispatch or blank out config the step already has — it logs and keeps
// whatever populateRuntimeFields already set at run start, the same
// fail-open behavior controller.go uses for the run-start read itself.
func (hcpo *StepBasedWorkflowOrchestrator) refreshStepAgentConfigsBeforeExecution(ctx context.Context, step PlanStepInterface) {
	if step == nil || strings.TrimSpace(step.GetID()) == "" {
		return
	}
	stepConfigs, err := hcpo.ReadStepConfigs(ctx)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [CONFIG_REFRESH] Failed to re-read step_config.json before executing step %s: %v (keeping config from run start)", step.GetID(), err))
		return
	}
	if err := populateRuntimeFields(step, stepConfigs); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [CONFIG_REFRESH] Failed to refresh runtime config for step %s: %v (keeping config from run start)", step.GetID(), err))
	}
}

// getAgentConfigs returns AgentConfigs from a PlanStepInterface
func getAgentConfigs(step PlanStepInterface) *AgentConfigs {
	switch s := step.(type) {
	case *ScriptedPlanStep:
		return s.AgentConfigs
	case *RoutingPlanStep:
		return s.AgentConfigs
	case *BranchPlanStep:
		return s.AgentConfigs
	case *AgentPlanStep:
		return s.AgentConfigs
	default:
		return nil
	}
}

// getValidationSchema returns ValidationSchema from a PlanStepInterface
func getValidationSchema(step PlanStepInterface) *ValidationSchema {
	return step.GetValidationSchema()
}

var _ = getScriptedPlanStep

// getScriptedPlanStep returns a pointer to ScriptedPlanStep if the step is a scripted step, nil otherwise
// This allows modification of step fields
func getScriptedPlanStep(step PlanStepInterface) *ScriptedPlanStep {
	if regularStep, ok := step.(*ScriptedPlanStep); ok {
		return regularStep
	}
	return nil
}

// LastExecutedStepOutcome captures the exact in-memory result of the most
// recently executed step within one runExecutionPhase call. It exists so a
// caller that dispatched exactly one step (e.g. the workshop's
// ExecuteStepForWorkshop) can use the result it just produced directly,
// instead of re-reading the step's log folder afterward -- a re-read has no
// way to distinguish "the file this exact call just wrote" from a file a
// concurrent, unrelated dispatch of the same step wrote in the meantime
// (PLAT-182 review). Found is false when no step that populates
// previousExecutionResults ran (e.g. only a routing or todo_task step ran,
// neither of which produces a result string this way) -- the caller must
// fall back to a log-folder read in that case.
type LastExecutedStepOutcome struct {
	StepIndex       int
	StepID          string
	ExecutionResult string
	Found           bool
}

// runExecutionPhase executes the plan steps one by one. lastOutcome, if
// non-nil, is populated in place with the final executed step's exact
// in-memory result -- see LastExecutedStepOutcome. Callers that process a
// whole plan/group (not a single targeted step) have no use for this and
// pass nil.
func (hcpo *StepBasedWorkflowOrchestrator) runExecutionPhase(
	ctx context.Context,
	breakdownSteps []PlanStepInterface,
	iteration int,
	progress *StepProgress,
	startFromStep int,
	execCtx *ExecutionContext,
	lastOutcome *LastExecutedStepOutcome,
) error {
	// Run folder should already be resolved early (after plan approval)
	if executionPlanFromContext(ctx) == nil {
		ctx = withExecutionPlan(ctx, &PlanningResponse{Steps: breakdownSteps})
	}
	if hcpo.selectedRunFolder == "" {
		return fmt.Errorf(fmt.Sprintf("run folder not resolved - this should have been set after plan approval"), nil)
	}
	validationStart := startFromStep
	if execCtx.RunSingleStepOnly {
		validationStart = execCtx.SingleStepTarget
	}
	if err := validateRequiredLaunchVariables(breakdownSteps, hcpo.variableValues, hcpo.requiredLaunchRouteSelections(), validationStart, execCtx.RunSingleStepOnly); err != nil {
		return err
	}
	// Record completed work even when a later step fails.
	receiptFolder, receiptID, receiptRevision := hcpo.beginCompletionReceipts(ctx, breakdownSteps, execCtx)
	defer func() {
		hcpo.finishCompletionReceipts(ctx, receiptFolder, receiptID, receiptRevision, breakdownSteps, progress)
	}()
	// Route conversations (msgSeqRoutes) are scoped to this execution phase.
	// Drain them — and close their runtimes — however the phase exits, so a
	// reused orchestrator instance starts the next iteration/run with fresh
	// route memory instead of resuming this run's conversations.
	defer hcpo.clearAllMsgSeqRouteSessions("execution phase finished")

	if err := hcpo.seedRouteSelectionsForRun(ctx, breakdownSteps); err != nil {
		return fmt.Errorf("failed to seed route selections: %w", err)
	}

	// Track execution results in memory (instead of reading from files)
	// Keep prior outputs available to subsequent steps without rereading logs.
	previousExecutionResults := make([]string, 0)

	// If starting from a step > 0 or running a single step, load execution results from logs for previous steps
	// This ensures we have execution results available for buildPreviousStepsSummary
	// Single step mode: if target step > 0, we need previous steps' results
	// Resume mode: if startFromStep > 0, we need previous steps' results
	stepsToLoad := startFromStep
	if execCtx.RunSingleStepOnly && execCtx.SingleStepTarget > 0 {
		// Use the higher of the two (in case both are set)
		if execCtx.SingleStepTarget > stepsToLoad {
			stepsToLoad = execCtx.SingleStepTarget
		}
	}
	if stepsToLoad > 0 {
		previousExecutionResults = hcpo.loadExecutionResultsFromLogs(ctx, breakdownSteps, stepsToLoad)
	}

	// Execute each step one by one
	// Use traditional for loop to allow jumping to different steps
	for i := 0; i < len(breakdownSteps); i++ {
		step := breakdownSteps[i]
		// Check for context cancellation before each step
		select {
		case <-ctx.Done():
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Workflow execution canceled before step %d/%d: %s", i+1, len(breakdownSteps), step.GetTitle()))
			return fmt.Errorf("workflow execution canceled: %w", ctx.Err())
		default:
		}

		// Skip if step is already completed
		if i < startFromStep {
			hcpo.GetLogger().Info(fmt.Sprintf("⏭️ Skipping step %d/%d (already completed): %s", i+1, len(breakdownSteps), step.GetTitle()))
			continue
		}

		// Check if step is in completed list
		// BUT: Force execution if:
		//  1. Single-step mode and this is the target step, OR
		//  2. This is the explicit resume step (startFromStep) - user wants to re-run it
		isCompleted := false
		forceExecution := false
		if execCtx.RunSingleStepOnly && i == execCtx.SingleStepTarget {
			// Force execution of target step even if completed
			forceExecution = true
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single-step mode: forcing execution of target step %d even if previously completed", i+1))
		} else if i == startFromStep {
			// This is the explicit resume step - user wants to re-run it even if marked as completed
			// (Cleanup should have removed it, but force execution as safety net)
			for _, completedIdx := range progress.CompletedStepIndices {
				if completedIdx == i {
					isCompleted = true
					break
				}
			}
			if isCompleted {
				forceExecution = true
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Explicit resume step %d: forcing execution even though marked as completed (cleanup may have failed)", i+1))
			}
		} else {
			for _, completedIdx := range progress.CompletedStepIndices {
				if completedIdx == i {
					isCompleted = true
					break
				}
			}
		}
		if isCompleted && !forceExecution {
			hcpo.GetLogger().Info(fmt.Sprintf("⏭️ Skipping step %d/%d (marked as completed): %s", i+1, len(breakdownSteps), step.GetTitle()))
			continue
		}

		if err := validateStepRequiredVariables(step, hcpo.variableValues); err != nil {
			return err
		}

		// Build context files from previous steps
		previousContextFiles := make([]string, 0)
		for prevIdx := 0; prevIdx < i; prevIdx++ {
			if prevIdx < len(breakdownSteps) {
				contextOutput := breakdownSteps[prevIdx].GetContextOutput().String()
				if contextOutput != "" {
					// Resolve variables in context output.
					resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
					previousContextFiles = append(previousContextFiles, resolvedOutput)
				} else {
					hcpo.GetLogger().Info(fmt.Sprintf("⚠️ Step %d (%s) has no context_output - skipping", prevIdx+1, breakdownSteps[prevIdx].GetTitle()))
				}
			}
		}

		// Set current step ID on context-aware bridge so ALL events have step info in metadata
		stepID := step.GetID()
		if stepID == "" {
			stepID = fmt.Sprintf("step-%d", i+1) // Fallback to step index if no ID
		}
		stepExecCtx := executionContextForStep(execCtx, stepID)
		if bridge := hcpo.GetContextAwareBridge(); bridge != nil {
			if stepBridge, ok := bridge.(interface {
				SetCurrentStepContext(stepID, stepType string)
			}); ok {
				stepBridge.SetCurrentStepContext(stepID, effectiveRuntimeStepType(step))
			} else if stepBridge, ok := bridge.(interface {
				SetCurrentStepID(stepID string)
			}); ok {
				stepBridge.SetCurrentStepID(stepID)
			}
			// Attach rich step context (name, index, total, parent, attempt,
			// triggered_by) so the terminal pane's meta row can render
			// "step 3/7 · attempt 1 · triggered by workflow_executor".
			// Don't set ParentStepID to the previous step — that's
			// data-flow semantics ("step 3 consumes step 2's output"),
			// but the frontend's terminal tree builder uses parent_step_id
			// for hierarchy and would render sequential steps as an
			// ever-deepening chain (step1 → step2 → step3 nested under
			// each other) instead of siblings at the workflow level.
			// ParentStepID is reserved for true sub-agent / nested
			// spawning relationships (see controller_todo_task.go,
			// planning_agent.go), not sequential order.
			if cab, ok := bridge.(*orchestrator.ContextAwareEventBridge); ok {
				rich := orchestrator.RichStepContext{
					StepName:    step.GetTitle(),
					StepType:    effectiveRuntimeStepType(step),
					StepIndex:   i + 1,
					StepTotal:   len(breakdownSteps),
					Attempt:     1,
					TriggeredBy: "workflow_executor",
				}
				cab.SetRichStepContext(rich)
			}
		}

		// Route execution based on step type using helper functions
		// Check if this is a routing step
		if isRoutingStep(step) {
			hcpo.GetLogger().Info(fmt.Sprintf("🔀 Starting routing step execution: %s", step.GetTitle()))
			selectedRouteID, _, err := hcpo.executeRoutingStep(ctx, step, i, progress, previousContextFiles, iteration, stepExecCtx, breakdownSteps, previousExecutionResults)
			if err != nil {
				if isWorkflowCancellationErr(ctx, err) {
					hcpo.GetLogger().Info(fmt.Sprintf("Routing step %d canceled", i+1))
					return err
				}
				if strings.Contains(err.Error(), "WORKFLOW_END") {
					hcpo.GetLogger().Info(fmt.Sprintf("🏁 Routing step %d signaled workflow termination - ending workflow", i+1))
					hcpo.addCompletedStepIndex(progress, i)
					if err := hcpo.saveStepProgress(ctx, progress); err != nil {
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after routing step termination: %v", err))
					}
					break
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Routing step %d execution failed: %v", i+1, err), nil)
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "routing-step-execution", fmt.Sprintf("Execute routing step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("routing step %d execution failed: %w", i+1, err)
			}

			hcpo.GetLogger().Info(fmt.Sprintf("✅ Routing step %d completed: selected route %s", i+1, selectedRouteID))

			// Mark step as completed
			hcpo.addCompletedStepIndex(progress, i)
			if err := hcpo.saveStepProgress(ctx, progress); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after routing step: %v", err))
			}

			// Check single step mode
			if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
				hcpo.SetRunSingleStepMode(false, -1)
				break
			}

			// Find next step based on selected route
			nextStepID := nextStepIDForSelectedRoute(step, selectedRouteID)

			// Track routing evaluations to prevent infinite loops
			if progress.RoutingEvaluationCounts == nil {
				progress.RoutingEvaluationCounts = make(RoutingEvaluationCount)
			}
			routingKey := fmt.Sprintf("%s:%s", step.GetID(), selectedRouteID)
			currentCount := progress.RoutingEvaluationCounts[routingKey]
			newCount := currentCount + 1
			progress.RoutingEvaluationCounts[routingKey] = newCount
			hcpo.GetLogger().Info(fmt.Sprintf("📊 Routing evaluation count for %s: %d", routingKey, newCount))

			if newCount > 2 {
				errorMsg := fmt.Sprintf("infinite loop detected: routing step '%s' (ID: %s) has selected route %s %d times", step.GetTitle(), step.GetID(), selectedRouteID, newCount)
				hcpo.GetLogger().Error(errorMsg, nil)
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "routing-step-loop-detection", fmt.Sprintf("Routing step: %s", step.GetTitle()), errorMsg, i, iteration)
				return fmt.Errorf("workflow error: %s", errorMsg)
			}

			if err := hcpo.saveStepProgress(ctx, progress); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after routing evaluation: %v", err))
			}

			// Handle next step navigation. The per-route evaluation count above is
			// routing's own loop guard, so the generic jump-repeat guard is disabled
			// (maxRepeats=0).
			outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), nextStepID, breakdownSteps, progress, &i, &startFromStep, 0)
			if navErr != nil {
				return navErr
			}
			if outcome == "end" {
				hcpo.GetLogger().Info(fmt.Sprintf("🏁 Routing step %d specified 'end' - terminating workflow", i+1))
				break
			}
			continue
		}

		// An agent with predefined_routes is the canonical agent step:
		// the same persistent conversation runs its authored turns and decides
		// whether/when to call its bounded specialist routes. Adapt it to the
		// delegation runtime when routes are present.

		if isDelegatingAgentStep(step) {
			// Execute todo task step - manages todo list and delegates to sub-agents
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Starting todo task step execution: %s", step.GetTitle()))
			// Generate step path for todo task step
			orchestratorStepPath := fmt.Sprintf("step-%d", i+1)

			successCriteriaMet, nextStepID, err := hcpo.executeDelegatingAgentStep(ctx, step, i, progress, previousContextFiles, previousExecutionResults, iteration, stepExecCtx, breakdownSteps, orchestratorStepPath)
			if err != nil {
				if isWorkflowCancellationErr(ctx, err) {
					hcpo.GetLogger().Info(fmt.Sprintf("Todo task step %d canceled", i+1))
					return err
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Todo task step %d execution failed: %v", i+1, err), nil)
				// Emit error event using centralized method
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "todo-task-step-execution", fmt.Sprintf("Execute todo task step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("todo task step %d execution failed: %w", i+1, err)
			}

			hcpo.GetLogger().Info(fmt.Sprintf("✅ Todo task step %d completed successfully: %s (SuccessCriteriaMet: %t)", i+1, step.GetTitle(), successCriteriaMet))

			// Mark todo task step as completed
			hcpo.addCompletedStepIndex(progress, i)
			if err := hcpo.saveStepProgress(ctx, progress); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after todo task step: %v", err))
			} else {
				hcpo.GetLogger().Info(fmt.Sprintf("💾 Saved progress: todo task step %d marked as completed", i+1))
			}

			// Check if we're in single step mode and should stop
			if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
				hcpo.SetRunSingleStepMode(false, -1) // Reset mode
				break
			}

			// Handle next step navigation. Identical source→target jumps are capped:
			// the orchestrator is LLM-driven, so an unbounded next_step_id cycle
			// burns tokens on every pass.
			outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), nextStepID, breakdownSteps, progress, &i, &startFromStep, maxLLMJumpRepeats)
			if navErr != nil {
				return navErr
			}
			if outcome == "end" {
				hcpo.GetLogger().Info(fmt.Sprintf("🏁 Todo task step %d specified 'end' - terminating workflow", i+1))
				break
			}
			// "jump" repointed i; "none" falls through to the next sequential step.
			continue
		}

		sequenceExecutionStep := step
		if isAgentStep(sequenceExecutionStep) {
			hcpo.GetLogger().Info(fmt.Sprintf("💬 Starting agent step execution: %s", sequenceExecutionStep.GetTitle()))
			stepPath := fmt.Sprintf("step-%d", i+1)
			callOptions := agentSequenceCallOptions{
				Source: "configured_queue",
			}
			if stepExecCtx != nil {
				callOptions.Restart = stepExecCtx.AgentRestart
				if strings.TrimSpace(stepExecCtx.WorkshopHumanInput) != "" {
					callOptions.Source = "builder_resume"
					callOptions.ReentryMessage = stepExecCtx.WorkshopHumanInput
				}
			}
			stepScopedCtx, finishSequence := hcpo.beginAgentExecution(ctx, sequenceExecutionStep)
			executionResult, _, err := hcpo.executeAgentStep(stepScopedCtx, sequenceExecutionStep, i, stepPath, progress, stepExecCtx, breakdownSteps, callOptions)
			finishSequence(executionResult, err)
			if err != nil {
				if isWorkflowCancellationErr(ctx, err) {
					hcpo.GetLogger().Info(fmt.Sprintf("Agent step %d canceled", i+1))
					return err
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Agent step %d execution failed: %v", i+1, err), nil)
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "agent-step-execution", fmt.Sprintf("Execute agent step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("agent step %d execution failed: %w", i+1, err)
			}
			previousExecutionResults = append(previousExecutionResults, executionResult)
			hcpo.addCompletedStepIndex(progress, i)
			if err := hcpo.saveStepProgress(ctx, progress); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after agent step: %v", err))
			}
			// KB contributions for agent steps are owned by the sequence's
			// own closing turn under direct mode. The post-step KB update agent that
			// used to cover write_method=agent here is retired — see
			// the retired agent-mode KB writer.
			_ = sequenceExecutionStep
			if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
				hcpo.SetRunSingleStepMode(false, -1)
				break
			}
			// Honor next_step_id so a route converges to its shared downstream step
			// (skipping sibling route targets that sit after it in the list) instead of
			// falling through in list order. Without this, after a router selected one
			// route the selected route ran but execution then spilled into the next
			// non-selected route target.
			if seqStep, ok := sequenceExecutionStep.(*AgentPlanStep); ok && strings.TrimSpace(seqStep.NextStepID) != "" {
				outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), seqStep.NextStepID, breakdownSteps, progress, &i, &startFromStep, maxLLMJumpRepeats)
				if navErr != nil {
					return navErr
				}
				if outcome == "end" {
					hcpo.GetLogger().Info(fmt.Sprintf("🏁 agent step %d next_step_id=end - terminating workflow", i+1))
					break
				}
			}
			continue
		}

		// Execute scripted step using executeSingleStep
		// Note: previousContextFiles is still needed for executeSingleStep (for context dependencies)
		// Previous execution outputs are kept in memory for downstream context.
		previousContextFiles = make([]string, 0)
		for prevIdx := 0; prevIdx < i; prevIdx++ {
			if prevIdx < len(breakdownSteps) {
				contextOutput := breakdownSteps[prevIdx].GetContextOutput().String()
				if contextOutput != "" {
					// Resolve variables in context output.
					resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
					previousContextFiles = append(previousContextFiles, resolvedOutput)
				}
			}
		}

		stepPath := fmt.Sprintf("step-%d", i+1)
		// Allow workshop inner steps to use a custom step path (e.g., "step-3-sub-login-expert")
		// so they don't collide with top-level step folders.
		if execCtx.StepPathOverride != "" && execCtx.RunSingleStepOnly && i == execCtx.SingleStepTarget {
			hcpo.GetLogger().Info(fmt.Sprintf("[STEP-PATH] Overriding step path from %q to %q (inner step, target=%d)", stepPath, execCtx.StepPathOverride, i))
			stepPath = execCtx.StepPathOverride
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("[STEP-PATH] Using default step path %q for step index %d (override=%q, singleStep=%v, target=%d)",
				stepPath, i, execCtx.StepPathOverride, execCtx.RunSingleStepOnly, execCtx.SingleStepTarget))
		}
		// Same contract as the agent queue: a canceled run must not
		// START another step, regardless of what the previous one reported
		// (PLAT-130). A step failing for its own reasons already aborts the loop;
		// this covers the case where cancellation never became an error.
		if ctx != nil && ctx.Err() != nil {
			hcpo.GetLogger().Info(fmt.Sprintf("[STOP] refusing to start step %d of %d (%q) — run is canceled: %v",
				i+1, len(breakdownSteps), step.GetTitle(), ctx.Err()))
			return fmt.Errorf("workflow halted before step %d: %w", i+1, ctx.Err())
		}
		// Quota pacing (opt-in per workflow). Spread the run across a window
		// reset rather than racing into the wall — but only when the account is
		// actually near it, so a healthy run pays nothing.
		if pacingErr := hcpo.applyQuotaPacingBeforeStep(ctx, i+1, len(breakdownSteps), step, stepPath); pacingErr != nil {
			return pacingErr
		}
		executionResult, _, err := hcpo.executeSingleStep(
			ctx,
			step,
			i,
			stepPath,
			len(breakdownSteps),
			iteration,
			previousContextFiles,
			progress,
			false, // isNestedExecution = false
			stepExecCtx,
			breakdownSteps,           // allSteps
			false,                    // isSubAgent = false (scripted step)
			previousExecutionResults, // Execution outputs from previous steps
			nil,                      // orchestrationRoutes - nil for scripted steps (not sub-agents)
		)
		if err != nil {
			// A provider capacity wall is not a step failure. The steps before
			// this one completed and did real work; the remaining ones will
			// succeed once the window reopens. Recording a durable wait and
			// returning it typed lets the scheduler suspend this run at exactly
			// this step instead of marking it failed and letting the next cron
			// tick replay the completed steps' side effects (PLAT-101).
			stepTitleForWait := step.GetTitle()
			stepIDForWait := step.GetID()
			if stepIDForWait == "" {
				stepIDForWait = fmt.Sprintf("step-%d", i+1)
			}
			if waitErr := hcpo.recordWorkflowCapacityWait(ctx, err, i+1, len(breakdownSteps), stepIDForWait, stepPath, stepTitleForWait); waitErr != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("⏸️ Step %d suspended on provider capacity: %v", i+1, waitErr))
				return waitErr
			}
			hcpo.GetLogger().Error(fmt.Sprintf("❌ Step %d execution failed: %v", i+1, err), nil)
			return fmt.Errorf("step %d execution failed: %w", i+1, err)
		}

		// Log execution result (for debugging)
		hcpo.GetLogger().Info(fmt.Sprintf("✅ Step %d execution completed (result length: %d chars)", i+1, len(executionResult)))

		// Track execution result in memory for downstream steps.
		// Ensure slice is large enough (pad with empty strings if needed)
		for len(previousExecutionResults) <= i {
			previousExecutionResults = append(previousExecutionResults, "")
		}
		previousExecutionResults[i] = executionResult
		hcpo.GetLogger().Info(fmt.Sprintf("💾 Stored execution result for downstream steps after step %d", i+1))
		if lastOutcome != nil {
			*lastOutcome = LastExecutedStepOutcome{StepIndex: i, StepID: step.GetID(), ExecutionResult: executionResult, Found: true}
		}

		// Check if we're in single step mode and should stop
		if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
			hcpo.SetRunSingleStepMode(false, -1) // Reset mode
			break
		}

		// Note: Progress tracking is handled inside executeSingleStep
		// Scripted steps may explicitly chain within a selected route and then
		// converge on a shared downstream step. Empty preserves legacy
		// sequential execution.
		if regularStep, ok := step.(*ScriptedPlanStep); ok {
			outcome, navErr := hcpo.navigateAfterScriptedStep(ctx, regularStep, breakdownSteps, progress, &i, &startFromStep)
			if navErr != nil {
				return navErr
			}
			if outcome == "end" {
				hcpo.GetLogger().Info(fmt.Sprintf("🏁 scripted step %d next_step_id=end - terminating workflow", i+1))
				break
			}
		}
		continue
	}

	// Clear current step ID on context-aware bridge (cleanup after execution ends)
	if bridge := hcpo.GetContextAwareBridge(); bridge != nil {
		if stepBridge, ok := bridge.(interface {
			ClearCurrentStepID()
		}); ok {
			stepBridge.ClearCurrentStepID()
		}
	}

	// Final save to ensure all completed steps are persisted
	// This is a safety measure to catch any steps that might have been missed
	if err := hcpo.saveStepProgress(ctx, progress); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save final step progress: %v", err))
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("💾 Final progress save completed: %d/%d steps completed", len(progress.CompletedStepIndices), progress.TotalSteps))
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ All steps execution completed"))
	return nil
}

func (hcpo *StepBasedWorkflowOrchestrator) navigateAfterScriptedStep(
	ctx context.Context,
	step *ScriptedPlanStep,
	breakdownSteps []PlanStepInterface,
	progress *StepProgress,
	i *int,
	startFromStep *int,
) (string, error) {
	if step == nil || strings.TrimSpace(step.NextStepID) == "" {
		return "none", nil
	}
	return hcpo.navigateToNextStepID(ctx, step.GetID(), step.NextStepID, breakdownSteps, progress, i, startFromStep, maxLLMJumpRepeats)
}

// sanitizeTitleForAgentName sanitizes a step title for use in agent names
// - Removes step number prefixes (e.g., "Step 4:", "Step 5 -", "Step 3.")
// - Removes/replaces special characters (colons, slashes, etc.)
// - Normalizes whitespace and converts to lowercase
// - Removes multiple consecutive dashes
func (hcpo *StepBasedWorkflowOrchestrator) sanitizeTitleForAgentName(title string) string {
	sanitized := strings.TrimSpace(title)

	// Remove step number prefixes (case-insensitive)
	// Matches: "Step N:", "Step N -", "Step N.", "Step N ", etc.
	stepNumberPattern := regexp.MustCompile(`(?i)^step\s+\d+\s*[:.\-]*\s*`)
	sanitized = stepNumberPattern.ReplaceAllString(sanitized, "")

	// Replace spaces with dashes
	sanitized = strings.ReplaceAll(sanitized, " ", "-")

	// Remove or replace special characters that aren't safe for agent names
	// Keep: letters, numbers, dashes, underscores
	// Remove: colons, slashes, backslashes, pipes, etc.
	specialCharPattern := regexp.MustCompile(`[^a-zA-Z0-9\-_]`)
	sanitized = specialCharPattern.ReplaceAllString(sanitized, "-")

	// Normalize multiple consecutive dashes to single dash
	multiDashPattern := regexp.MustCompile(`-+`)
	sanitized = multiDashPattern.ReplaceAllString(sanitized, "-")

	// Remove leading/trailing dashes
	sanitized = strings.Trim(sanitized, "-")

	// Convert to lowercase for consistency
	sanitized = strings.ToLower(sanitized)

	// Ensure we have something left (fallback if everything was removed)
	if sanitized == "" {
		sanitized = "step"
	}

	return sanitized
}

// buildValidationContinuationUserMessage formats a pre-validation failure as a
// follow-up user message so the existing execution agent can fix the issues
// in-place rather than re-running the entire step from a fresh conversation.
// The agent already has the full task context in its system prompt + prior
// turns, so the message only needs to carry the new failure information.
func buildValidationContinuationUserMessage(vr *ValidationResponse, attempt int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Pre-validation failed (retry attempt %d)\n\n", attempt))
	if vr != nil {
		if vr.Reasoning != "" {
			sb.WriteString(vr.Reasoning)
			sb.WriteString("\n\n")
		}
		if len(vr.Feedback) > 0 {
			sb.WriteString("**Feedback:**\n")
			for _, fb := range vr.Feedback {
				sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", fb.Severity, fb.Type, fb.Description))
			}
			sb.WriteString("\n")
		}
	}
	sb.WriteString("Fix the specific issues above and re-produce the required outputs. ")
	sb.WriteString("Do not restart from scratch — your prior tool calls and outputs are still valid where they passed; only address the listed failures.")
	return sb.String()
}
