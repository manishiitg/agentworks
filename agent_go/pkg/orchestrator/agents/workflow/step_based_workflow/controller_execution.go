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
// Always created on workspace init (no preset toggle, unlike knowledgebase). All regular steps
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

// SoulFolderName is the name of the builder's long-term memory folder at workspace root.
// Contains soul.md — a free-form markdown file maintained by the workflow interactive builder
// across sessions.
//
// WRITE is builder-only: soul/ never appears in an execution step's write paths, so a
// constraint has exactly one author. READ is granted to every execution step
// unconditionally (see setupExecutionFolderGuard, which puts soul/ in readPaths alongside
// execution/ and builder/), and `## Objective` / `## Success Criteria` / `## Constraints`
// are extracted and injected into agent prompts — see soul_helpers.go. An earlier version
// of this comment claimed soul/ was "not read by execution, learning, or KB
// agents"; that was never true of the folder guard and is not true of prompt injection.
//
// Kept outside planning/ because planning/ is read-only to the builder (modifications go
// through typed plan-mod tools); soul.md is updated by shell write directly.
// See docs/workflow/persistent_stores_design.md section 8.
const SoulFolderName = "soul"

// Standard workflow subfolder names. Kept as constants so every site that needs to
// reference them (folder-guard allow-lists, prompt paths, pre-creation scaffolds)
// goes through a single source. When adding a new workflow subfolder, update the
// constant AND WorkflowWritableSubfolders below — skipping either half is what
// produced the reports/ + db/ + soul/ drift bug previously.
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
	SoulFolderName + "/",
	ReportsFolderName + "/",
	ExecutionFolderName + "/",
	LearningsFolderName + "/",
	ScriptsFolderName + "/",
	RunsFolderName + "/",
}

// SoulFileName is the fixed markdown filename inside soul/.
const SoulFileName = "soul.md"

// getSoulPath returns the full path to the soul folder.
// Path format: {workspaceRoot}/soul/
//
//nolint:unused // reserved for the upcoming soul-store read/write helpers.
func getSoulPath(workspaceRoot string) string {
	return fmt.Sprintf("%s/%s", workspaceRoot, SoulFolderName)
}

// getSoulFilePath returns the full path to soul/soul.md.
//
//nolint:unused // reserved for the upcoming soul-store read/write helpers.
func getSoulFilePath(workspaceRoot string) string {
	return fmt.Sprintf("%s/%s/%s", workspaceRoot, SoulFolderName, SoulFileName)
}

// Knowledgebase access modes — see docs/workflow/persistent_stores_design.md section 3.
const (
	KBAccessReadWrite = "read-write"
	KBAccessRead      = "read"
	KBAccessWrite     = "write"
	KBAccessNone      = "none"

	knowledgebaseAccessDescription = "Access mode for this step against knowledgebase/ (per-topic notes/ + notes/_index.json registry). Defaults to 'read' so steps can consume shared context; use 'none' to opt out. 'write' / 'read-write' may contribute: the step agent writes notes/ inline with diff_patch_workspace_file and closes with a self-review turn against its knowledgebase_contribution. Granting write without a knowledgebase_contribution results in no KB writes at all."
)

// resolveKnowledgebaseAccess resolves the effective KB access mode for a step.
//
// resolveKnowledgebaseAccess returns the effective knowledgebase_access for a
// step. Explicit value wins. Unset mirrors resolveLearningsAccess's already-safe
// default pattern rather than introducing a new one: read by default (every
// step sees KB context, same as learnings), auto-promoted to read-write only
// when a contribution is already staged.
//
// PLAT-055 / K. Before this, unset silently meant KBAccessNone — no read, no
// write — which is a genuine trap distinct from an operator's deliberate
// KBAccessNone: rtslatency's two worst-offending steps had no other legitimate
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

// kbContributionForPrompt returns the step's knowledgebase_contribution string
// safely — nil step config and unset field both map to the empty string.
func kbContributionForPrompt(stepConfig *AgentConfigs) string {
	if stepConfig == nil {
		return ""
	}
	return stepConfig.KnowledgebaseContribution
}

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
		relPath := strings.TrimPrefix(absPath, docsRoot+"/")
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
			relPath := strings.TrimPrefix(candidate, docsRoot+"/")
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
			appendCandidate(filepath.Join(docsRoot, getExecutionFolderPath(executionWorkspacePath, stepID, stepID), outputName))
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

// getLearningFolderPathByStepID returns the RELATIVE learning folder path using step ID.
// All steps share the "learnings/{stepID}/" namespace; step-ID uniqueness
// within plan.json guarantees no collision.
// Returns a RELATIVE path for use with workspace functions — they auto-prepend workspacePath.
// baseWorkspacePath and stepPath are retained for call-site compatibility.
func getLearningFolderPathByStepID(baseWorkspacePath string, stepID string, stepPath string) string {
	_ = baseWorkspacePath
	_ = stepPath
	return fmt.Sprintf("learnings/%s", stepID)
}

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

// getEffectiveLearningPathIdentifier returns the step ID for metadata tracking.
// The learning agent always writes to the _global skill folder (controlled by the template),
// but metadata is tracked per step for clarity.
func getEffectiveLearningPathIdentifier(stepID string, stepPath string, agentConfigs *AgentConfigs) string {
	return stepID
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

// saveFinalExecutionSummary persists the authoritative compact result after all
// execution, validation, KB, and learnings turns have finished. Attempt logs stay
// immutable diagnostic evidence; Pulse reads this stable file for completed steps.
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
	if plannedStep != nil && plannedStep.StepType() == StepTypeHumanInput {
		executionWorkspacePath := fmt.Sprintf("%s/execution", validationWorkspacePath)
		stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, stepID, stepPath)
		contextOutput := plannedStep.GetContextOutput().String()
		if contextOutput == "" {
			contextOutput = fmt.Sprintf("step-%d.json", stepNumber)
		}
		resolvedContextOutput := ResolveVariables(contextOutput, hcpo.variableValues)
		responseFilePath := filepath.Join(stepExecutionPath, resolvedContextOutput)
		if content, err := hcpo.ReadWorkspaceFile(ctx, responseFilePath); err == nil {
			var responseData map[string]interface{}
			if err := json.Unmarshal([]byte(content), &responseData); err == nil {
				if response, ok := responseData["response"].(string); ok && response != "" {
					hcpo.GetLogger().Info(fmt.Sprintf("Loaded human_input response for step %d from %s (length=%d)", stepNumber, responseFilePath, len(response)))
					return response, true
				}
			}
		}
		// Fall through to the execution-attempt scan below — harmless, won't find anything.
	}

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

// buildPlanPositionSummary tells the step where it sits in the plan.
//
// Steps cannot read planning/plan.json — it is not on their folder-guard read
// paths — so without this a step has no idea whether it is the first of nine or
// the last, or what runs next. It only ever saw backward context via
// buildPreviousStepsSummary. Knowing the shape of the run is what lets a step
// judge how much to do here versus leave for a later step, and recognize when
// its own output is the thing another step is waiting on.
//
// Consumers are listed ONLY when a later step actually declares a dependency on
// this step's context_output. Most plans leave context_dependencies empty, and
// inventing a consumer would be worse than saying nothing.
func buildPlanPositionSummary(allSteps []PlanStepInterface, currentStepIndex int) string {
	total := len(allSteps)
	if total == 0 || currentStepIndex < 0 || currentStepIndex >= total {
		return ""
	}
	current := allSteps[currentStepIndex]
	if current == nil {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are step %d of %d in this workflow's plan.\n", currentStepIndex+1, total)

	if currentStepIndex+1 < total {
		if next := allSteps[currentStepIndex+1]; next != nil {
			if title := strings.TrimSpace(next.GetTitle()); title != "" {
				fmt.Fprintf(&b, "- Runs after you: %s\n", title)
			}
		}
	} else {
		b.WriteString("- You are the final step.\n")
	}

	output := strings.TrimSpace(current.GetContextOutput().String())
	if output != "" {
		var consumers []string
		for i := currentStepIndex + 1; i < total; i++ {
			later := allSteps[i]
			if later == nil {
				continue
			}
			for _, dep := range later.GetContextDependencies() {
				if strings.Contains(dep, output) {
					title := strings.TrimSpace(later.GetTitle())
					if title == "" {
						title = later.GetID()
					}
					consumers = append(consumers, title)
					break
				}
			}
		}
		if len(consumers) > 0 {
			fmt.Fprintf(&b, "- Your output `%s` is consumed by: %s\n", output, strings.Join(consumers, ", "))
		}
	}

	return strings.TrimSpace(b.String())
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

	// Include ALL human_input step results (regardless of position) as critical context,
	// plus the most recent non-human-input execution result for general context.
	// This matches the routing agent's behavior in controller_routing.go.
	humanFeedbackIncluded := false
	for idx := 0; idx < currentStepIndex && idx < len(previousExecutionResults); idx++ {
		if previousExecutionResults[idx] == "" {
			continue
		}
		if idx < len(allSteps) && allSteps[idx].StepType() == StepTypeHumanInput {
			stepTitle := ResolveVariables(allSteps[idx].GetTitle(), hcpo.variableValues)
			execOutput := previousExecutionResults[idx]
			if len(execOutput) > 2000 {
				execOutput = execOutput[:2000] + "\n... (truncated)"
			}
			summary.WriteString(fmt.Sprintf("\n## 🚨 HUMAN FEEDBACK (CRITICAL - READ CAREFULLY)\n\n"))
			summary.WriteString(fmt.Sprintf("The human provided the following feedback/input in **Step %d: %s**.\n", idx+1, stepTitle))
			summary.WriteString("**You MUST incorporate this human feedback into your work. This takes priority over other context.**\n\n")
			summary.WriteString(fmt.Sprintf("```\n%s\n```\n", execOutput))
			humanFeedbackIncluded = true
		}
	}

	// Include the most recent non-human-input execution result
	for idx := currentStepIndex - 1; idx >= 0; idx-- {
		if idx >= len(previousExecutionResults) || previousExecutionResults[idx] == "" {
			continue
		}
		if idx < len(allSteps) && allSteps[idx].StepType() == StepTypeHumanInput {
			continue // Already included above
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
		if humanFeedbackIncluded {
			summary.WriteString(fmt.Sprintf("\n## 📤 Most Recent Step Execution Output\n\n"))
		} else {
			summary.WriteString(fmt.Sprintf("\n## 📤 Previous Step Execution Output\n\n"))
		}
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

	// Crew steps run only as top-level steps through executeCrewStep; a crew
	// step nested under an orchestrator route lands here via the sub-agent
	// fallthrough and must fail clearly instead of running as an agent step.
	if isCrewStep(step) {
		return "", updatedContextFiles, fmt.Errorf("crew step %q runs only as a top-level step", step.GetID())
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
		narrowLearningsAccess := resolveExecutionLearningsAccess(narrowAgentCfg, step)
		narrowRead, narrowWrite := hcpo.setupExecutionFolderGuard(artifactStepPath, artifactStepID, narrowKBAccess, narrowLearningsAccess, resolveDBAccess(narrowAgentCfg), narrowAgentCfg)
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

	// Scripted code mode — determined once per step invocation (persists across outer-loop iterations).
	// Check embedded plan AgentConfigs first; fall back to step_configs.json so that workshop-saved
	// configs (use_code_execution_mode) also take effect for sub-agent steps whose embedded plan
	// config may not have the flag.
	isScriptedMode := false
	{
		// The plan type decides (PLAT-287). The config only matters for the
		// transitional legacy-agentic shim, and the embedded plan config may
		// not carry that raw key for a sub-agent step, so consult
		// step_config.json by id when the embedded one says nothing.
		agentCfgs := getAgentConfigs(step)
		if agentCfgs.legacyDeclared() == "" && step.GetID() != "" {
			if stepConfigs, err := hcpo.ReadStepConfigs(ctx); err == nil {
				if sc := MatchStepConfigByID(step.GetID(), stepConfigs); sc.legacyDeclared() != "" {
					agentCfgs = sc
				}
			}
		}
		isScriptedMode = isScriptedStep(step, agentCfgs)
		hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] step=%s type=%s scripted=%v",
			step.GetID(), step.StepType(), isScriptedMode))
	}
	if execCtx != nil && execCtx.SavedScriptOnly && !isScriptedMode {
		return "", updatedContextFiles, fmt.Errorf("step %q is not in scripted code mode", step.GetID())
	}
	learnCodePriorScript := ""          // old script content when saved script failed (LLM relearn context)
	learnCodePriorError := ""           // error from failed saved script (LLM relearn context)
	learnCodeScriptNeedsSaving := false // set after LLM writes main.py and pre-validation passes
	learnCodeFastPathDone := false      // set when saved script ran successfully (skip execution loop)

	// Initialize variables for step execution.
	// Regular steps get 3 retries on pre-validation failure.
	// Sub-agents and workshop single-step mode get 2 — if it can't fix in 2 tries,
	// better to return to the orchestrator/user with feedback than burn more tokens.
	maxRetryAttempts := 3
	if isSubAgent {
		maxRetryAttempts = 3
	}
	if hcpo.runSingleStepOnly {
		maxRetryAttempts = 3
	}
	var executionConversationHistory []llmtypes.MessageContent // Only used for learning agents after execution
	var learnCodePreValidationResultsOverride *WorkspaceVerificationResult
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
		learningsPath := fmt.Sprintf("%s/learnings", hcpo.GetWorkspacePath())
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
		learningsAccess := resolveExecutionLearningsAccess(agentConfigs, step)
		dbAccess := resolveDBAccess(agentConfigs)
		folderGuardReadPaths, folderGuardWritePaths := hcpo.setupExecutionFolderGuard(artifactStepPath, artifactStepID, kbAccess, learningsAccess, dbAccess, agentConfigs)

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
			return filepath.Join(docsRoot, path)
		}
		toAbsPathSlice := func(paths []string) []string {
			result := make([]string, len(paths))
			for i, p := range paths {
				result[i] = toAbsPath(p)
			}
			return result
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

		learnCodeInputArgsForPrompt := ""
		if isScriptedMode && len(resolvedContextDeps) > 0 {
			// Reuse the exact same resolved dependency list for both the saved prompt
			// and the later python3 main.py invocation so they cannot drift.
			learnCodeInputArgsForPrompt = strings.Join(resolvedContextDeps, "\n")
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
		scriptedEnv := hcpo.snapshotWorkspaceEnv()
		scriptedEnv = hcpo.codeRuntimeEnv(scriptedEnv)
		scriptedEnv = appendScriptedDelegationEnv(ctx, scriptedEnv)
		if scriptedEnv == nil {
			scriptedEnv = make(map[string]string)
		}
		// A parameterized script sees the same environment shape while being
		// authored/repaired and while running on the fast path. A top-level test
		// run has no orchestrator-supplied values, so expose an empty object rather
		// than making the contract's environment variable disappear.
		if len(scriptedParameterDefinitions(step)) > 0 {
			if _, present := scriptedEnv[ScriptedParametersEnv]; !present {
				scriptedEnv[ScriptedParametersEnv] = "{}"
			}
		}
		_, _, _, folderEnv := appendWorkflowFolderAccess(hcpo.GetWorkspacePath(), nil, nil, kbAccessAllowsRead(resolveKnowledgebaseAccess(getAgentConfigs(step), hcpo.UseKnowledgebase())))
		for key, value := range folderEnv {
			scriptedEnv[key] = value
		}
		templateVars := map[string]string{
			"StepTitle":                 stepTitleForPrompt,
			"StepDescription":           stepDescriptionForPrompt,
			"StepSuccessCriteria":       "",
			"StepContextOutput":         ResolveVariables(step.GetContextOutput().String(), hcpo.variableValues),
			"WorkspacePath":             toAbsPath(executionWorkspacePath),             // Absolute execution folder path (e.g., "/app/workspace-docs/Workflow/HRMS/runs/...")
			"LearningsPath":             toAbsPath(learningsPath),                      // Absolute learnings folder path
			"KnowledgebasePath":         toAbsPath(knowledgebasePath),                  // Absolute knowledgebase folder path
			"DBPath":                    toAbsPath(getDBPath(hcpo.GetWorkspacePath())), // Absolute db folder path (always enabled)
			"DBAccess":                  dbAccess,
			"DBDirectAccess":            fmt.Sprintf("%v", isScriptedMode),
			"UseKnowledgebase":          fmt.Sprintf("%v", useKnowledgebase),                                                                  // Whether knowledgebase is enabled (deprecated, retained for backward compat)
			"KbAccess":                  kbAccess,                                                                                             // KB access mode: "read" | "write" | "read-write" | "none"
			"KbAccessLabel":             kbAccessLabel(kbAccess),                                                                              // Human-readable label for prompt display
			"KnowledgebaseContribution": kbContributionForPrompt(agentConfigs),                                                                // Author's contribution instruction (direct mode surfaces it to the step)
			"KBGuidanceBlock":           BuildStepKBGuidanceWithTarget(kbAccess, kbContributionForPrompt(agentConfigs), kbNotesPathForPrompt), // Direct-mode-only KB contribution guidance
			"IsCodeExecutionMode":       fmt.Sprintf("%v", isCodeExecutionMode),                                                               // Code execution mode flag (step-specific or preset)
			"StepNumber":                stepPath,                                                                                             // Step identifier (e.g., "step-8" or "step-3-sub-fetch")
			"StepExecutionPath":         toAbsPath(stepExecutionPath),                                                                         // Absolute step execution folder path
			"FolderGuardReadPaths":      strings.Join(toAbsPathSlice(folderGuardReadPaths), ", "),                                             // Absolute folder guard read paths
			"FolderGuardWritePaths":     strings.Join(toAbsPathSlice(folderGuardWritePaths), ", "),                                            // Absolute folder guard write paths
			"WorkflowRoot":              toAbsPath(workflowRoot),                                                                              // Absolute workflow root path (e.g., "/app/workspace-docs/Workflow/HRMS")
			"IsScriptedMode":            fmt.Sprintf("%v", isScriptedMode),
			"ScriptedWorkingDir":        toAbsPath(hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)),
			"DirectCodeSource":          fmt.Sprintf("%v", hcpo.usesCodeTree()),
			"IsScriptedLocked":          fmt.Sprintf("%v", isScriptedMode && getAgentConfigs(step) != nil && getAgentConfigs(step).LockCode != nil && *getAgentConfigs(step).LockCode),
			"IsRelearnMode":             fmt.Sprintf("%v", isScriptedMode && learnCodePriorScript != ""),
			"ScriptedPriorScript":       learnCodePriorScript,
			"ScriptedPriorError":        learnCodePriorError,
			"ScriptedInputArgs":         learnCodeInputArgsForPrompt,
			"ScriptedEnvVarNames":       buildScriptedEnvVarNamesForPrompt(isScriptedMode, scriptedEnv),
			"ScriptedParameterSchema":   formatScriptParameterContract(scriptedParameterDefinitions(step)),
			"ScriptedVarMapping":        buildScriptedVarMappingForPrompt(isCodeExecutionMode || isScriptedMode, hcpo.variablesManifest),
			"GroupName":                 hcpo.currentGroupName,
		}
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
		templateVars["PlanPosition"] = buildPlanPositionSummary(allSteps, stepIndex)
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
		if priorFailures, priorErr := LoadPriorPreValidationFailures(ctx, hcpo.GetWorkspacePath(), step.GetID(), 10); priorErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Could not load prior validation failures for step %s: %v", step.GetID(), priorErr))
		} else if rendered := FormatPriorPreValidationFailures(priorFailures); rendered != "" {
			templateVars["PriorValidationFailures"] = rendered
			hcpo.GetLogger().Info(fmt.Sprintf("🔁 Step %s carries %d unresolved validation failure(s) into this run", step.GetID(), len(priorFailures)))
		}

		// Inner loop: Automatic retry logic
		var validationResponse *ValidationResponse
		// PLAT-055. The merged reflection turn is one-shot per step execution.
		// Declared outside the retry loop so validation retries don't re-fire it,
		// and so the post-step learning-agent trigger further down can see that
		// direct reflection already handled this step.
		var reflectionPerformed bool
		// Direct-mode UI should still emit one final step notification, but its
		// summary must include the main execution plus the reflection turn.
		var mainExecutionSummary string
		var directLearningsSummary string

		// Learn code mode: attempt fast path execution with saved script (before any LLM work).
		if isScriptedMode && !learnCodeFastPathDone {
			fastResult := hcpo.tryRunSavedScriptedScript(ctx, step, stepIndex, stepPath, allSteps,
				stepExecutionPath, executionWorkspacePath, dbAccess)
			if fastResult.RanScript {
				// Emit UI event for the saved-script execution attempt
				savedScriptPath := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), hcpo.scriptedSourceDir(step.GetID()), "main.py")
				hcpo.emitScriptedExecutionEvent(ctx, step, stepIndex, stepPath,
					savedScriptPath, fastResult.Success, fastResult.ExitCode, fastResult.Output, fastResult.Error, 0, true)
				// Save execution log so debug_step and direct file inspection can see fast-path output
				hcpo.saveScriptedFastPathLog(ctx, stepIndex, artifactStepID, artifactStepPath, savedScriptPath, fastResult)
			}
			scriptedDecision := decideScriptedFastPath(fastResult)
			if scriptedDecision.HarnessTimeout {
				return "", updatedContextFiles, fmt.Errorf(
					"scripted step %q started main.py but exceeded the workspace harness timeout. Its partial output was preserved, and the code-repair loop was not started because a harness kill does not prove a script defect. Timeout detail: %s",
					step.GetID(), scriptedDecision.TimeoutError)
			}
			// The workspace refused to start the script, so there is nothing to
			// validate and nothing for the LLM to repair. Fail the step rather
			// than falling through: the relearn path would tell the agent to fix
			// a bug that does not exist and then persist its rewrite over working
			// code. This is infrastructure, not workflow content — it belongs in
			// the step-failure notification, not in the run's concerns.
			if scriptedDecision.HarnessFailure {
				return "", updatedContextFiles, fmt.Errorf(
					"scripted step %q could not run: the workspace refused to start main.py, so it produced no output and its behavior is untested by this run. "+
						"This is a harness/infrastructure failure, not a fault in the script — do not modify main.py in response to it. Underlying error: %s",
					step.GetID(), scriptedDecision.HarnessError)
			}
			// Exit code 2 always stops automatic repair, but does not by itself
			// establish that a safety guard fired. Report only supplied evidence.
			if scriptedDecision.TerminalRefusal {
				return "", updatedContextFiles, scriptedTerminalStopError(step.GetID(), scriptedDecision.TerminalRefusalReason)
			}
			learnCodePriorScript = scriptedDecision.PriorScript
			learnCodePriorError = scriptedDecision.PriorError
			switch {
			case scriptedDecision.FastPathDone:
				// Saved script executed and validated — skip LLM entirely
				learnCodeFastPathDone = true
				executionResult = fastResult.Output
				validationResponse = &ValidationResponse{
					IsSuccessCriteriaMet: true,
					ExecutionStatus:      "COMPLETED",
					Reasoning:            "scripted: saved script executed and validated (0 LLM tokens)",
				}
				// If the script in execution/code/ differs from learnings (e.g., LLM-fixed version
				// from a previous attempt), save the working version back to learnings.
				learnCodeScriptNeedsSaving = true
				hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Fast path succeeded for step %d — skipping execution loop", stepIndex+1))
			case fastResult.RanScript:
				// Script ran but failed — fall through to LLM for relearn
				hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Script failed for step %d — falling back to LLM with error context", stepIndex+1))
			case scriptedDecision.PriorScript != "":
				// A saved script exists but wasn't executed successfully. Pass it to the LLM
				// so it can adapt the working script rather than rewriting from scratch.
				hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Step %d found an existing saved script — LLM will update it in place", stepIndex+1))
			}

			// templateVars were built before the fast-path check. Refresh the scripted
			// prompt fields here so a saved-script reuse/failure can surface the saved script
			// and prior error in the actual rendered prompt for this same execution pass.
			templateVars["IsRelearnMode"] = fmt.Sprintf("%v", isScriptedMode && learnCodePriorScript != "")
			templateVars["ScriptedPriorScript"] = learnCodePriorScript
			templateVars["ScriptedPriorError"] = learnCodePriorError

			// On failure, point the relearn agent to script_metadata.json so it can
			// read run history, failure patterns, per-group stats, and streaks itself.
			if learnCodePriorError != "" {
				metaRelPath := hcpo.scriptedSourceDir(step.GetID()) + "/script_metadata.json"
				templateVars["ScriptedMetadataPath"] = metaRelPath
			}

			if execCtx != nil && execCtx.SavedScriptOnly {
				if fastResult.RanScript && fastResult.Success {
					hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] Saved-script-only run succeeded for step %d", stepIndex+1))
				} else if fastResult.RanScript {
					return "", updatedContextFiles, fmt.Errorf("saved main.py failed for step %q:\n%s", step.GetID(), fastResult.Error)
				} else {
					return "", updatedContextFiles, fmt.Errorf("no saved main.py found for scripted step %q in %s/main.py", step.GetID(), hcpo.scriptedSourceDir(step.GetID()))
				}
			}
		}

		// Main execution (single execution, no loop)
		// NOTE: No conversation history is passed to execution agent - all context via template variables
		if !learnCodeFastPathDone {
			// Check for context cancellation before execution
			select {
			case <-ctx.Done():
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Step execution canceled for step %d", stepIndex+1))
				return "", updatedContextFiles, fmt.Errorf("step execution canceled: %w", ctx.Err())
			default:
			}

			// Set loop template vars to empty (loop feature removed)
			templateVars["HasLoop"] = "false"
			templateVars["LoopCondition"] = ""
			templateVars["LoopDescription"] = ""
			templateVars["CurrentIteration"] = ""
			templateVars["MaxIterations"] = ""
			templateVars["PreviousIterationOutput"] = ""

			// Signal whether this workflow has agent-browser or CDP available. Downstream prompt builders use this to gate
			// the browser-specific main.py authoring rules + DOM probe so non-browser workflows
			// don't pay the prompt tax. Note: empty browserMode means "auto-detect", NOT "no
			// browser" — HasBrowserCapability() checks registered servers+skills directly.
			templateVars["HasBrowserAccess"] = fmt.Sprintf("%t", hcpo.HasBrowserCapability())

			// Resolve variables in step title before using in agent name
			resolvedTitle := ResolveVariables(step.GetTitle(), hcpo.variableValues)
			sanitizedTitle := hcpo.sanitizeTitleForAgentName(resolvedTitle)

			// Run learning reading agent ONCE per main loop iteration (before retry loop)
			// This ensures learning is only discovered once, even if validation fails and we retry
			// Always reads fresh learnings (no caching)
			var formattedLearningHistory string
			agentConfigs := getAgentConfigs(step)

			// Learnings READ gate — controlled by learnings_access.
			// Default is "read": every step sees _global/SKILL.md in its prompt.
			// Only routing steps or explicit learnings_access="none" opt out.
			// Contribution (write) is a separate gate further below.
			if !canReadLearnings(agentConfigs, step) {
				formattedLearningHistory = ""
				hcpo.GetLogger().Info(fmt.Sprintf("⏭️ Learnings read disabled for step %d (learnings_access=%s) - skipping _global/ injection", stepIndex+1, resolveLearningsAccess(agentConfigs)))
			} else {
				// Learning is enabled - read from global learning skill
				formattedLearningHistory, err = hcpo.readGlobalLearningHistory(ctx)
				if err != nil {
					return "", updatedContextFiles, fmt.Errorf("failed to read learning history for step %d: %w", stepIndex+1, err)
				}
			}

			// Track if validation failed after exhausting all retry attempts
			validationFailedAfterMaxRetries := false
			var validationFailures []validationFailureConcern

			// Track which LLM model was used for execution (to be stored in learning metadata)
			var executionLLM string

			// executionAgent persists across retries so retry N>1 can continue the
			// existing conversation (sending validation feedback as a user message)
			// instead of re-running the whole step from scratch.
			var executionAgent agents.OrchestratorAgent
			// If scripted was ever active in any attempt, the conversation is polluted
			// with main.py authoring turns — fall back to fresh agents instead of continuing.
			learnCodeActiveInAnyAttempt := false
			// Tier selection belongs to explicit configuration and Pulse Architecture
			// review. Never derive a runtime override from learning/run counters.

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
				stepLearningHistory := formattedLearningHistory
				execCodeMainPyRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"
				if _, mainPyReadErr := hcpo.ReadWorkspaceFile(ctx, execCodeMainPyRelPath); mainPyReadErr == nil {
					execCodeMainPyAbsPath := filepath.Join(toAbsPath(hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)), "main.py")
					if stepLearningHistory != "" {
						stepLearningHistory += "\n\n"
					}
					stepLearningHistory += fmt.Sprintf("📜 **Saved script available** at `%s` — this is a working implementation from a previous run. Read it before starting, then use `diff_patch_workspace_file` to update it rather than rewriting the entire file.", execCodeMainPyAbsPath)
				}
				templateVars["LearningHistory"] = stepLearningHistory
				// Set HasLearnings flag to explicitly indicate whether learnings exist (prevents agent from searching)
				templateVars["HasLearnings"] = fmt.Sprintf("%t", stepLearningHistory != "")

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

				// Track scripted presence to poison continuation in future attempts.
				if isScriptedMode {
					learnCodeActiveInAnyAttempt = true
				}

				// Decide whether to continue the prior conversation or start fresh.
				// Continuation reuses the existing agent + feeds validation feedback as a
				// user message — cheaper and keeps the agent's working memory.
				//
				// In normal flow, the ONLY trigger that lands in the fresh-agent branch
				// AFTER a pre-validation failure is scripted: its authoring turns must
				// not mix with validation-fix turns, and its inner fix loop creates its
				// own repair agents. Attempt #1 also takes the fresh path, but with no
				// validation feedback (it's just the initial execution).
				//
				// The agent / history guards below are defensive — they catch an
				// Execute() that errored before producing any LLM turns (saved in the
				// `err != nil` branch above without running pre-validation). They are
				// not the primary semantic; see the scripted condition for that.
				mustRestartForScripted := isScriptedMode || learnCodeActiveInAnyAttempt
				shouldContinue := retryAttempt > 1 && !mustRestartForScripted
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
				turnCount := len(executionConversationHistory)

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
				// Carry validation failures from earlier attempts into every later
				// execution result. Pulse Gate intentionally reads the latest/final
				// attempt, so keeping the concern only on the failed attempt would
				// hide a recovered validation failure.
				executionResult = withValidationFailureConcern(executionResult, validationFailures, 0, false)
				mainExecutionSummary = summarizeExecutionResultForNotification(executionResult)
				hcpo.recordWorkflowContinuationPhase(context.Background(), artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhaseMainExecution, workflowContinuationStatusCompleted, "", executionAgent)

				// Save execution logs (result, conversation history, system prompt)
				if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
					executionResult, executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
					hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
					hcpo.GetLogger().Warn(fmt.Sprintf("Step %s completed, but its required execution result could not be persisted: %v", artifactStepID, logErr))
				}

				// Learn code mode: inner fix loop — run main.py and feed errors back as user messages
				// in the same conversation chain (no new agent, no system-prompt reset).
				if isScriptedMode {
					// If learnings are locked, skip the fix loop entirely — the LLM's rewrite
					// won't be saved back anyway, so there's no point spending tokens on repair.
					// Set maxFixIter to -1 so the fix loop is skipped and we fall through
					// to the agentic fallback below.
					isCodeLockedForFixLoop := agentConfigs != nil && agentConfigs.LockCode != nil && *agentConfigs.LockCode
					maxFixIter := 3
					if isCodeLockedForFixLoop {
						hcpo.GetLogger().Info(fmt.Sprintf("🔒 [scripted] Code locked for step %d — skipping fix loop, will fall back to agentic mode", stepIndex+1))
						maxFixIter = -1
					}
					// PLAT-061 removed learn_code_max_fix_iterations. Every stored value
					// was a migration artifact, not a judgment: the migration defaulted
					// retries to 0 and only raised it when a legacy message-sequence item
					// declared repair_with_llm — so five hetznerssh steps carried 0 with no
					// reasoning behind it, silently disabling script repair. lock_code
					// remains the deliberate way to skip the fix loop.
					codeDirAbsPath := toAbsPath(hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath))
					mainPyPath := filepath.Join(codeDirAbsPath, "main.py")
					var lastLcResult *ScriptedFastPathResult
					// Check if pre-validation already passes (LLM may have run main.py or produced outputs).
					// If outputs are valid AND main.py exists, skip the fix loop — no need to re-run.
					preValResults, _ := RunPreValidation(ctx, getValidationSchema(step), stepExecutionPath, hcpo.BaseOrchestrator)
					if preValResults != nil && hcpo.selectedRunFolder != "" {
						preValLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
						SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValLogPath, step.GetID(), stepPath, preValResults, getValidationSchema(step), hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
							PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "initial-check", ExecutionAttempt: retryAttempt, ValidationAttempt: 1})
					}
					mainPyRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"
					_, mainPyExistsErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath)
					// PUL-4FE07CD2: valid-looking outputs must not override a script
					// whose latest run failed (a fail-closed gate exiting 1 left the
					// step "completed"). Only a detected non-zero exit blocks
					// acceptance; the saved-script fast path already fails closed.
					_, lastExitCode, lastRunFound := extractLastMainPyRunOutput(executionConversationHistory, mainPyPath)
					lastRunFailed := lastRunFound && lastExitCode != 0
					if lastRunFailed {
						hcpo.GetLogger().Info(fmt.Sprintf("🧪 [scripted] Outputs validate but main.py's latest run exited %d for step %d — not accepting; entering fix loop", lastExitCode, stepIndex+1))
					}
					if !hcpo.usesCodeTree() && !lastRunFailed && preValResults != nil && preValResults.OverallPass && mainPyExistsErr == nil {
						learnCodePreValidationResultsOverride = preValResults
						// Try to get exit code from LLM self-run detection (optional — for logging)
						var exitCode int
						var output string
						if selfRun := detectSuccessfulLLMScriptedSelfRun(executionConversationHistory, mainPyPath); selfRun != nil {
							exitCode = selfRun.ExitCode
							output = selfRun.Output
						}
						lastLcResult = &ScriptedFastPathResult{
							RanScript: true,
							Success:   true,
							ExitCode:  exitCode,
							Output:    output,
						}
						hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] Pre-validation passed and main.py exists for step %d — skipping fix loop", stepIndex+1))
						hcpo.emitScriptedExecutionEvent(ctx, step, stepIndex, stepPath,
							mainPyPath, true, lastLcResult.ExitCode, lastLcResult.Output, "", 0, false)
					} else if preValResults != nil && preValResults.OverallPass {
						hcpo.GetLogger().Info(fmt.Sprintf("🧪 [scripted] Pre-validation passed but main.py not found for step %d — entering fix loop to generate script", stepIndex+1))
					}

					// Track script content before each fix iteration to generate diffs
					prevFixScript := ""
					if s, readErr := hcpo.ReadWorkspaceFile(ctx, hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath)+"/main.py"); readErr == nil {
						prevFixScript = s
					}

					// Fix loop: check pre-validation after each LLM turn.
					// The LLM writes and runs main.py itself — the controller only checks if
					// the outputs are valid. If not, feed validation errors back and let the LLM fix.
					for fixIter := 0; fixIter <= maxFixIter && (lastLcResult == nil || !lastLcResult.Success); fixIter++ {
						// Run canonical source after each authoring/repair turn through
						// the same executor used by schedules and builder execute_step.
						var canonicalResult *ScriptedFastPathResult
						if hcpo.usesCodeTree() {
							canonicalResult = hcpo.tryRunSavedScriptedScript(ctx, step, stepIndex, stepPath, allSteps, stepExecutionPath, executionWorkspacePath, dbAccess)
							if canonicalResult.HarnessFailure {
								return "", updatedContextFiles, fmt.Errorf("script runtime unavailable: %s", canonicalResult.HarnessError)
							}
							if canonicalResult.TerminalRefusal {
								return "", updatedContextFiles, scriptedTerminalStopError(step.GetID(), canonicalResult.TerminalRefusalReason)
							}
							if canonicalResult.Success {
								lastLcResult = canonicalResult
								break
							}
						}
						// Check for context cancellation before each fix iteration
						select {
						case <-ctx.Done():
							hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Step execution canceled during fix iteration %d for step %d", fixIter+1, stepIndex+1))
							return "", updatedContextFiles, fmt.Errorf("step execution canceled during scripted fix loop: %w", ctx.Err())
						default:
						}

						// Check pre-validation — did the LLM produce valid outputs?
						fixPreValResults, _ := RunPreValidation(ctx, getValidationSchema(step), stepExecutionPath, hcpo.BaseOrchestrator)
						if fixPreValResults != nil && hcpo.selectedRunFolder != "" {
							preValLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
							SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValLogPath, step.GetID(), stepPath, fixPreValResults, getValidationSchema(step), hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
								PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "repair-check", ExecutionAttempt: retryAttempt, ValidationAttempt: fixIter + 1})
						}
						mainPyRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"
						_, mainPyErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath)

						_, fixLastExitCode, fixLastRunFound := extractLastMainPyRunOutput(executionConversationHistory, mainPyPath)
						fixLastRunFailed := fixLastRunFound && fixLastExitCode != 0
						if !hcpo.usesCodeTree() && !fixLastRunFailed && fixPreValResults != nil && fixPreValResults.OverallPass && mainPyErr == nil {
							// Outputs valid + main.py exists → success
							lastLcResult = &ScriptedFastPathResult{RanScript: true, Success: true}
							hcpo.emitScriptedExecutionEvent(ctx, step, stepIndex, stepPath,
								mainPyPath, true, 0, "", "", fixIter, false)
							hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] Pre-validation passed for step %d on fix iteration %d", stepIndex+1, fixIter))
							learnCodePreValidationResultsOverride = fixPreValResults
							break
						}

						if fixIter == maxFixIter {
							if canonicalResult != nil {
								lastLcResult = canonicalResult
								break
							}
							// Record the failure for the outer loop — include execution output
							// so the next retry attempt knows what the script actually did
							var errMsg string
							if mainPyErr != nil {
								errMsg = "main.py was not written"
							} else if fixPreValResults != nil {
								errMsg = formatWorkspaceResults(fixPreValResults)
							} else {
								errMsg = "pre-validation could not run"
							}
							// Append last execution output so the next retry has full context
							if lastRunOutput, lastRunExitCode, lastRunFound := extractLastMainPyRunOutput(executionConversationHistory, mainPyPath); lastRunFound {
								outputSnippet := lastRunOutput
								if len(outputSnippet) > 4000 {
									outputSnippet = outputSnippet[:2000] + "\n... (truncated) ...\n" + outputSnippet[len(outputSnippet)-2000:]
								}
								errMsg = fmt.Sprintf("%s\n\nLast execution output (exit code %d):\n%s", errMsg, lastRunExitCode, outputSnippet)
							}
							// Use the latest main.py from execution/code/ (LLM may have rewritten it during fix loop)
							latestScript := ""
							if mainPyErr == nil {
								if s, readErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath); readErr == nil {
									latestScript = s
								}
							}
							lastLcResult = &ScriptedFastPathResult{RanScript: mainPyErr == nil, Success: false, Error: errMsg, ExistingScript: latestScript}
							hcpo.emitScriptedExecutionEvent(ctx, step, stepIndex, stepPath,
								mainPyPath, false, 1, "", errMsg, fixIter, false)
							break // exhausted fix attempts
						}

						// Build feedback message with validation errors
						var feedbackMsg string
						stepDesc := templateVars["StepDescription"]
						var sb strings.Builder
						sb.WriteString(fmt.Sprintf("## Task\n%s\n\n", stepDesc))
						if canonicalResult != nil {
							sb.WriteString("\nActual runner failure:\n" + decideScriptedFastPath(canonicalResult).PriorError + "\n")
						}

						if mainPyErr != nil {
							// main.py not written yet
							if priorCtx := BuildScriptedPriorContext(templateVars["ScriptedPriorScript"], templateVars["ScriptedPriorError"], templateVars["ScriptedMetadataPath"], templateVars["IsScriptedLocked"] == "true"); priorCtx != "" {
								sb.WriteString(priorCtx)
								sb.WriteString("\n")
							}
							sb.WriteString(fmt.Sprintf("main.py was not found at %s/main.py.\n\n", codeDirAbsPath))
							sb.WriteString("Write the complete solution to main.py there, run it, and ensure the output files are produced.")
						} else {
							// main.py exists but outputs are invalid
							var actualScript string
							if s, readErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath); readErr == nil && s != "" {
								actualScript = s
								sb.WriteString(fmt.Sprintf("### Your Script\n\nRead the current script at `%s/main.py` before making changes.\n\n", codeDirAbsPath))
							}
							// Static code review: catch anti-patterns before they get saved to learnings
							if actualScript != "" {
								// Pass declared env vars so the review can distinguish declared vs invented vars
								var declaredVars []string
								if envNames := templateVars["ScriptedEnvVarNames"]; envNames != "" {
									declaredVars = strings.Split(envNames, "\n")
								}
								if codeIssues := reviewMainPyScript(actualScript, declaredVars...); len(codeIssues) > 0 {
									sb.WriteString(fmt.Sprintf("**⚠️ Code review found %d issue(s) that MUST be fixed:**\n", len(codeIssues)))
									for idx, issue := range codeIssues {
										sb.WriteString(fmt.Sprintf("%d. %s\n", idx+1, issue))
									}
									sb.WriteString("\nThese issues will cause failures when the script is reused for other groups/users. Fix them before running.\n\n")
									hcpo.GetLogger().Warn(fmt.Sprintf("🔍 [review-code] Found %d issue(s) in main.py for step %d", len(codeIssues), stepIndex+1))
								}
							}
							// Include the last main.py execution output so the fix agent knows what happened
							if lastRunOutput, lastRunExitCode, lastRunFound := extractLastMainPyRunOutput(executionConversationHistory, mainPyPath); lastRunFound {
								sb.WriteString(fmt.Sprintf("**Last execution output (exit code %d):**\n```\n", lastRunExitCode))
								// Truncate to avoid oversized prompts
								if len(lastRunOutput) > 8000 {
									sb.WriteString(lastRunOutput[:4000])
									sb.WriteString("\n... (truncated) ...\n")
									sb.WriteString(lastRunOutput[len(lastRunOutput)-4000:])
								} else {
									sb.WriteString(lastRunOutput)
								}
								sb.WriteString("\n```\n\n")
							}
							sb.WriteString(fmt.Sprintf("**Output validation failed (attempt %d/%d).** The script did not produce the correct outputs. Re-read the task requirements, check what your script actually did, and fix it.\n\n", fixIter+1, maxFixIter))
							sb.WriteString("**CRITICAL: Your script must actually fetch/compute data by calling MCP tools or APIs or processing real input files. Do NOT hardcode, fabricate, or hallucinate output data — every value in the output must come from a real data source.**\n\n")
							sb.WriteString("Fix main.py, run it again, and ensure all required output files are produced correctly.")
						}
						feedbackMsg = sb.String()
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] validation failed for step %d (fix attempt %d/%d) — continuing conversation with feedback", stepIndex+1, fixIter+1, maxFixIter))

						// Create a fresh repair agent for each fix iteration — each attempt gets
						// a clean conversation with the latest script + errors as context.
						// This avoids accumulated confusion from prior failed attempts.
						repairAgentName := fmt.Sprintf("%s-fix-%d-high", executionAgentName, fixIter+1)
						// Force Tier 1 (High) for repair agents — they need to fix a failure,
						// so they should use at least the same tier as the original execution.
						repairCtx := context.WithValue(ctx, WorkshopTierOverrideKey, int(TierHigh))
						repairAgent, repairErr := hcpo.createExecutionOnlyAgent(repairCtx, "execution_only", stepPath, repairAgentName, agentConfigs, step, step.GetID(), getExecutionArtifactFolderOverride(execCtx))
						if repairErr != nil {
							hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] failed to create repair agent for step %d fix %d: %v", stepIndex+1, fixIter+1, repairErr))
							break
						}
						if repairCfg := repairAgent.GetConfig(); repairCfg != nil && repairCfg.LLMConfig.Primary.ModelID != "" {
							executionLLM = fmt.Sprintf("%s/%s", repairCfg.LLMConfig.Primary.Provider, repairCfg.LLMConfig.Primary.ModelID)
						}
						repairSystemPrompt := ""
						if repairEOA, ok := repairAgent.(*WorkflowExecutionOnlyAgent); ok {
							repairSystemPrompt = repairEOA.executionOnlySystemPromptProcessor(templateVars)
						}
						if executionAgent != nil {
							_ = executionAgent.Close()
						}
						executionAgent = repairAgent
						hcpo.GetLogger().Info(fmt.Sprintf("🔁 [scripted] created fresh repair agent for step %d fix %d: %s", stepIndex+1, fixIter+1, executionLLM))

						// Fresh conversation each time — feedback message already contains
						// the full script + execution output + validation errors as context.
						if ba := executionAgent.GetBaseAgent(); ba != nil {
							_, executionConversationHistory, err = hcpo.withWorkshopMessageTarget(ctx, step.GetID(), "script-repair", executionAgent, func() (string, []llmtypes.MessageContent, error) {
								return ba.Execute(ctx, feedbackMsg, nil, repairSystemPrompt, false)
							})
							if err != nil {
								hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] agent error during fix attempt %d: %v", fixIter+1, err))
								break
							}
						} else {
							break
						}

						// Capture diff between fix iterations for debugging
						mainPyFixRelPath := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"
						if curScript, readErr := hcpo.ReadWorkspaceFile(ctx, mainPyFixRelPath); readErr == nil && prevFixScript != "" && curScript != prevFixScript {
							fixDiffsRelPath := stepExecutionPath + "/code/fix-diffs"
							if mkErr := createFolderViaAPI(ctx, fixDiffsRelPath); mkErr == nil {
								diff := generateSimpleDiff("main.py", prevFixScript, curScript)
								diffFile := fmt.Sprintf("fix-%d-to-%d.diff", fixIter, fixIter+1)
								if writeErr := hcpo.WriteWorkspaceFile(ctx, fixDiffsRelPath+"/"+diffFile, diff); writeErr == nil {
									hcpo.GetLogger().Info(fmt.Sprintf("📝 [scripted] Saved fix diff %s for step %d", diffFile, stepIndex+1))
								}
							}
							prevFixScript = curScript
						} else if readErr == nil {
							prevFixScript = curScript
						}
					}

					// The saved learnings script already failed — any LLM-produced script is a
					// newer attempt and likely better. Save it to learnings regardless of success
					// so the next run starts from the latest version, not the known-broken one.
					// BUT: don't save scripts with syntax errors — those are definitely worse.
					// AND: don't save if code is locked — the user froze the script intentionally.
					isCodeLocked := agentConfigs != nil && agentConfigs.LockCode != nil && *agentConfigs.LockCode
					if isCodeLocked {
						hcpo.GetLogger().Info(fmt.Sprintf("🔒 [scripted] Code locked for step %d — NOT saving script back to learnings", stepIndex+1))
						learnCodeScriptNeedsSaving = false
					} else if mainPyRelCheck := hcpo.scriptedWorkingDir(step.GetID(), stepExecutionPath) + "/main.py"; !hcpo.usesCodeTree() {
						if scriptContent, checkErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelCheck); checkErr == nil && scriptContent != "" {
							// Quick syntax check: run python3 -c "compile(...)" to catch syntax errors
							hasSyntaxError := false
							if lastLcResult != nil && !lastLcResult.Success && lastLcResult.Error != "" {
								if strings.Contains(lastLcResult.Error, "SyntaxError") {
									hasSyntaxError = true
								}
							}
							if hasSyntaxError {
								hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] NOT saving main.py to learnings for step %d — script has syntax errors", stepIndex+1))
							} else {
								hcpo.saveScriptedScriptToLearnings(step, toAbsPath(stepExecutionPath))
								hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Saved LLM-produced main.py to learnings for step %d (replaces known-broken version)", stepIndex+1))
								learnCodeScriptNeedsSaving = false // already saved, don't duplicate later
							}
						}
					}

					if lastLcResult == nil || !lastLcResult.Success {
						var errMsg string
						if lastLcResult == nil {
							errMsg = fmt.Sprintf("main.py was never written to %s", codeDirAbsPath)
						} else {
							errMsg = fmt.Sprintf("main.py still failing after %d fix attempts:\n%s", maxFixIter, lastLcResult.Error)
						}
						hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] step %d failed after fix loop: %s", stepIndex+1, errMsg))
						if hcpo.usesCodeTree() {
							return "", updatedContextFiles, fmt.Errorf("canonical scripted execution failed: %s", errMsg)
						}
						// Fallback: disable scripted mode for remaining retries.
						// The LLM couldn't write a working main.py — let it use tools
						// directly in normal agentic mode to complete the task.
						isScriptedMode = false
						templateVars["IsScriptedMode"] = "false"
						hcpo.GetLogger().Info(fmt.Sprintf("🔄 [scripted] Switching step %d to normal agentic mode for remaining retries", stepIndex+1))
						// Add explicit guidance for the agentic fallback — tell the LLM
						// that the scripted approach failed and it should use tools directly.
						fallbackGuidance := fmt.Sprintf(
							"%s\n\n"+
								"**IMPORTANT: The scripted main.py approach failed after multiple attempts. "+
								"Do NOT write a main.py script this time. Instead, complete the task by calling "+
								"MCP tools directly via the API step by step. Use the tools to understand the "+
								"current state first (e.g. read files, take browser snapshots, inspect pages), "+
								"then perform the required actions interactively. Focus on completing the task, "+
								"not on writing reusable code.**",
							errMsg)
						validationResponse = &ValidationResponse{
							IsSuccessCriteriaMet: false,
							ExecutionStatus:      "FAILED",
							Reasoning:            fallbackGuidance,
						}
						validationFailures = append(validationFailures, newValidationFailureConcern(retryAttempt, validationResponse))
						exhausted := retryAttempt >= maxRetryAttempts
						executionResult = withValidationFailureConcern(executionResult, validationFailures, 0, exhausted)
						mainExecutionSummary = summarizeExecutionResultForNotification(executionResult)
						if logErr := hcpo.saveExecutionConversationLogs(stepIndex, artifactStepID, artifactStepPath, retryAttempt, 0,
							executionResult, executionLLM, executionConversationHistory, executionAgent, capturedToolCalls, capturedLLMCalls, attemptStartedAt, attemptCompletedAt, attemptDuration); logErr != nil {
							hcpo.recordRunPersistenceError(context.Background(), artifactStepID, logErr)
						}
						if exhausted {
							validationFailedAfterMaxRetries = true
						}
						continue
					}
					turnCount = len(executionConversationHistory)
					hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] main.py executed successfully for step %d — proceeding to validation", stepIndex+1))
				}

				// Run pre-validation (code-based structural checks) -- always active, independent of LLM validation.
				agentConfigs = getAgentConfigs(step)
				hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhasePreValidation, workflowContinuationStatusRunning, "", executionAgent)
				preValidationSchema := getValidationSchema(step)
				preValidationResults := learnCodePreValidationResultsOverride
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
				learnCodePreValidationResultsOverride = nil
				hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, isNestedExecution, preValidationResults)
				preValidationStatus := workflowContinuationStatusCompleted
				preValidationError := ""
				if preValidationResults != nil && !preValidationResults.OverallPass {
					preValidationStatus = workflowContinuationStatusFailed
					preValidationError = formatWorkspaceResults(preValidationResults)
				}
				hcpo.recordWorkflowContinuationPhase(ctx, artifactStepID, artifactStepPath, workflowContinuationOwnerStepExecution, workflowContinuationPhasePreValidation, preValidationStatus, preValidationError, executionAgent)

				// Persist pre-validation results for Pulse Bug Review and diagnostics.
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
					// completeMessageSequenceItemNotification's deliberately
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

					// PLAT-055. One reflection turn replaces the KB self-review turn
					// followed by the direct-learnings turn. The split was the defect:
					// during the KB turn the agent has not yet worked out what it
					// learned, and by the learnings turn the KB door has closed and the
					// database was never reachable at all — so "which store owns this?"
					// was decided by whichever door happened to be open rather than by
					// the agent. See runStepReflectionTurn.
					if !reflectionPerformed && executionAgent != nil {
						reflectionPerformed = true
						reflection := hcpo.runStepReflectionTurn(
							ctx, step, stepIndex, stepPath, artifactStepID, artifactStepPath,
							getAgentConfigs(step), executionAgent, executionConversationHistory,
							isScriptedMode, turnCount, executionLLM,
						)
						directLearningsSummary = reflection.Summary
						if reflection.Executed {
							executionConversationHistory = reflection.History
							hcpo.GetLogger().Info(fmt.Sprintf("🧠 Reflection completed for step %d (history=%d turns)", stepIndex+1, len(executionConversationHistory)))
						}
					}
					// Step summaries are retained run evidence. Pulse reviews that
					// evidence directly; the runtime must not scrape prose into the
					// Pulse observation store or attempt text-based deduplication.

					if combinedSummary := buildDirectModeCompletionSummary(mainExecutionSummary, "", directLearningsSummary); combinedSummary != "" {
						executionResult = combinedSummary
					}

					validationResponse = &ValidationResponse{
						IsSuccessCriteriaMet: true,
						ExecutionStatus:      "COMPLETED",
						Reasoning:            "Pre-validation passed - step auto-approved",
					}
					// Learn code mode: mark script for saving to learnings after the execution loop
					if isScriptedMode {
						learnCodeScriptNeedsSaving = true
					}
				}

				// LEARNING PHASE: Runs for ALL agents regardless of validation status
				// Validation being disabled does NOT prevent learning from running
				// Learning runs only when learnings_access is read-write and an objective exists.
				// LEARNING DISABLED: Skip learning agents entirely
				// Learnings WRITE gate — controlled by learnings_access="read-write" AND
				// non-empty learning_objective (the extraction target for the writer).
				// No more code-exec force-enable — that was papering over the fact that
				// "empty objective" used to also kill read access; with learnings_access
				// split, write is honest opt-in and read is default-on.
				agentConfigs = getAgentConfigs(step)
				isLearningDisabled := !canWriteLearnings(agentConfigs, step)
				if isScriptedMode {
					hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted_code] Step %d — main.py remains executable truth; SKILL.md writes gated by learnings_access=%s", stepIndex+1, resolveLearningsAccess(agentConfigs)))
				}
				// Pre-validation result drives validationResponse (set above).
				// Safety guard: if somehow nil, default to success so learning + KB can proceed.
				if validationResponse == nil {
					hcpo.GetLogger().Info(fmt.Sprintf("Pre-validation result missing for step %d - defaulting to success for post-step phases", stepIndex+1))
					validationResponse = &ValidationResponse{
						IsSuccessCriteriaMet: true,
						ExecutionStatus:      "COMPLETED",
						Reasoning:            "LLM validation disabled - step auto-approved for post-step phases",
					}
				}

				// Runtime-field population for post-step phases used to happen here,
				// feeding the learning agent and the KB update agent. Both are retired
				// and the direct-mode turns run inline earlier in this method with the
				// step's own live config, so nothing needs re-populating.

				// Post-step learning AGENT (the separate analytical reviewer that
				// reads the step trace and writes SKILL.md as its own LLM turn) is
				// retired. All steps now use direct-mode learning: the step agent
				// itself writes SKILL.md inline during its dedicated post-completion
				// turn (gated earlier in this method by shouldDirectWriteLearnings
				// and the learningsDirectPerformed flag). No background launch happens.
				if isLearningDisabled {
					hcpo.GetLogger().Info(fmt.Sprintf("⏭️ Learning disabled: Skipping learning for step %d", stepIndex+1))
				}

				// Post-step KB update AGENT (the separate reviewer that re-read the step
				// trace from disk and wrote notes/ as its own LLM conversation) is
				// retired, mirroring the learning agent above. All KB writes now go
				// through direct mode: the step agent writes notes/ inline and closes
				// with the contribution self-review turn (BuildKBContributionReviewMessage),
				// gated earlier in this method. No background launch happens here.

				// Check if success criteria was met
				// Check IsSuccessCriteriaMet instead of just ExecutionStatus - PARTIAL/INCOMPLETE can also mean criteria not met
				if validationResponse != nil && validationResponse.IsSuccessCriteriaMet {
					if len(validationFailures) > 0 {
						executionResult = withValidationFailureConcern(executionResult, validationFailures, retryAttempt, false)
						mainExecutionSummary = summarizeExecutionResultForNotification(executionResult)
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
					mainExecutionSummary = summarizeExecutionResultForNotification(executionResult)
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

		// Learn code mode: save the newly written main.py to learnings (only when LLM generated it)
		// Skip if code is locked — the user froze the script intentionally.
		isCodeLockedForSave := agentConfigs != nil && agentConfigs.LockCode != nil && *agentConfigs.LockCode
		if isScriptedMode && learnCodeScriptNeedsSaving && !isCodeLockedForSave {
			hcpo.saveScriptedScriptToLearnings(step, toAbsPath(stepExecutionPath))
			learnCodeScriptNeedsSaving = false
		}

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

// isHumanInputStep returns true if the step is a human input step (asks question and blocks for input)
func isHumanInputStep(step PlanStepInterface) bool {
	_, ok := step.(*HumanInputPlanStep)
	return ok
}

// isCrewStep returns true if the step invokes a Crew trigger and waits for
// its final response.
func isCrewStep(step PlanStepInterface) bool {
	_, ok := step.(*CrewPlanStep)
	return ok
}

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

	// The execution prompt deliberately puts the human-readable outcome and any
	// non-fatal CONCERNS immediately before the final STATUS line. Keeping only
	// the last line therefore turns a useful completion into little more than
	// "STATUS: COMPLETED" and drops the evidence that downstream steps and Pulse
	// need. Preserve the final response as the compact notification summary. The
	// prompt already limits it to a few sentences; the defensive tail cap keeps a
	// misbehaving provider response from making notifications unbounded while
	// retaining the contractually important final lines.
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

// isOrchestratorStep returns true if the step is a todo task step (orchestrator with todo list management)
func isOrchestratorStep(step PlanStepInterface) bool {
	_, ok := step.(*OrchestratorPlanStep)
	return ok
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

func isMessageSequenceStep(step PlanStepInterface) bool {
	_, ok := step.(*MessageSequencePlanStep)
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
// function's own `for retryAttempt := 1; ...` loop, and message_sequence passes a
// literal 1 with a per-entry turn number. Nothing in the name identifies WHICH
// dispatch produced it, so a step that runs again -- a route re-entry, an
// operator re-run, a gate sending work back -- recomputes the identical path and
// overwrites the earlier run in place.
//
// Confirmed live on confida-login (2026-08-22): `execute-browser-and-capture-apis`
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
	case *RegularPlanStep:
		return s.AgentConfigs
	case *OrchestratorPlanStep:
		return s.AgentConfigs
	case *HumanInputPlanStep:
		return s.AgentConfigs
	case *RoutingPlanStep:
		return s.AgentConfigs
	case *BranchPlanStep:
		return s.AgentConfigs
	case *MessageSequencePlanStep:
		return s.AgentConfigs
	default:
		return nil
	}
}

// getValidationSchema returns ValidationSchema from a PlanStepInterface
func getValidationSchema(step PlanStepInterface) *ValidationSchema {
	return step.GetValidationSchema()
}

var _ = getRegularPlanStep

// getRegularPlanStep returns a pointer to RegularPlanStep if the step is a regular step, nil otherwise
// This allows modification of step fields
func getRegularPlanStep(step PlanStepInterface) *RegularPlanStep {
	if regularStep, ok := step.(*RegularPlanStep); ok {
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

		// A message_sequence with predefined_routes is the canonical agent step:
		// the same persistent conversation runs its authored turns and decides
		// whether/when to call its bounded specialist routes. Adapt it to the
		// legacy orchestrator wrapper until that compatibility type is removed.
		orchestratorExecutionStep := step
		delegatingSequence := false
		if sequence, ok := step.(*MessageSequencePlanStep); ok && len(sequence.PredefinedRoutes) > 0 {
			orchestratorExecutionStep = delegatingMessageSequenceAsOrchestrator(sequence)
			delegatingSequence = true
		}

		// Check if this is a legacy orchestrator or a delegating agent sequence.
		if isOrchestratorStep(step) || delegatingSequence {
			// Execute todo task step - manages todo list and delegates to sub-agents
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Starting todo task step execution: %s", step.GetTitle()))
			// Generate step path for todo task step
			orchestratorStepPath := fmt.Sprintf("step-%d", i+1)

			successCriteriaMet, nextStepID, err := hcpo.executeOrchestratorStep(ctx, orchestratorExecutionStep, i, progress, previousContextFiles, previousExecutionResults, iteration, stepExecCtx, breakdownSteps, orchestratorStepPath)
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
		if shouldNormalizeRegularStepToMessageSequence(step) {
			sequenceExecutionStep = normalizeRegularStepToMessageSequence(step.(*RegularPlanStep))
			hcpo.GetLogger().Info(fmt.Sprintf("💬 Normalizing non-scripted regular step %q to the message-sequence runtime", step.GetID()))
		}
		if isMessageSequenceStep(sequenceExecutionStep) {
			hcpo.GetLogger().Info(fmt.Sprintf("💬 Starting message sequence step execution: %s", sequenceExecutionStep.GetTitle()))
			stepPath := fmt.Sprintf("step-%d", i+1)
			callOptions := messageSequenceCallOptions{
				Source: "configured_queue",
			}
			if stepExecCtx != nil {
				callOptions.Restart = stepExecCtx.MessageSequenceRestart
				if strings.TrimSpace(stepExecCtx.WorkshopHumanInput) != "" {
					callOptions.Source = "builder_resume"
					callOptions.ReentryMessage = stepExecCtx.WorkshopHumanInput
				}
			}
			stepScopedCtx, finishSequence := hcpo.beginMessageSequenceExecution(ctx, sequenceExecutionStep)
			executionResult, _, err := hcpo.executeMessageSequenceStep(stepScopedCtx, sequenceExecutionStep, i, stepPath, progress, stepExecCtx, breakdownSteps, callOptions)
			finishSequence(executionResult, err)
			if err != nil {
				if isWorkflowCancellationErr(ctx, err) {
					hcpo.GetLogger().Info(fmt.Sprintf("Message sequence step %d canceled", i+1))
					return err
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Message sequence step %d execution failed: %v", i+1, err), nil)
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "message-sequence-step-execution", fmt.Sprintf("Execute message sequence step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("message sequence step %d execution failed: %w", i+1, err)
			}
			previousExecutionResults = append(previousExecutionResults, executionResult)
			hcpo.addCompletedStepIndex(progress, i)
			if err := hcpo.saveStepProgress(ctx, progress); err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to save progress after message sequence step: %v", err))
			}
			// KB contributions for message-sequence steps are owned by the sequence's
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
			if seqStep, ok := sequenceExecutionStep.(*MessageSequencePlanStep); ok && strings.TrimSpace(seqStep.NextStepID) != "" {
				outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), seqStep.NextStepID, breakdownSteps, progress, &i, &startFromStep, maxLLMJumpRepeats)
				if navErr != nil {
					return navErr
				}
				if outcome == "end" {
					hcpo.GetLogger().Info(fmt.Sprintf("🏁 message_sequence step %d next_step_id=end - terminating workflow", i+1))
					break
				}
			}
			continue
		}

		// Check if this is a human input step
		if isHumanInputStep(step) {
			// Execute human input step - asks question and blocks for input
			hcpo.GetLogger().Info(fmt.Sprintf("👤 Starting human input step execution: %s", step.GetTitle()))

			// Build context files from previous steps
			previousContextFiles := make([]string, 0)
			for prevIdx := 0; prevIdx < i; prevIdx++ {
				if prevIdx < len(breakdownSteps) {
					contextOutput := breakdownSteps[prevIdx].GetContextOutput().String()
					if contextOutput != "" {
						// Resolve variables in context output
						resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
						previousContextFiles = append(previousContextFiles, resolvedOutput)
					}
				}
			}

			_, err := hcpo.executeHumanInputStep(ctx, step, i, progress, previousContextFiles, stepExecCtx, breakdownSteps)
			if err != nil {
				if isWorkflowCancellationErr(ctx, err) {
					hcpo.GetLogger().Info(fmt.Sprintf("Human input step %d canceled", i+1))
					return err
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Human input step %d execution failed: %v", i+1, err), nil)
				// Emit error event using centralized method
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "human-input-step-execution", fmt.Sprintf("Execute human input step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("human input step %d execution failed: %w", i+1, err)
			}

			hcpo.GetLogger().Info(fmt.Sprintf("✅ Human input step %d completed successfully: %s", i+1, step.GetTitle()))

			// Track execution result in memory for use by subsequent steps
			// Extract the response from the saved JSON file to create an execution result summary
			// Get the context output path to read the saved response
			contextOutput := step.GetContextOutput().String()
			if contextOutput == "" {
				contextOutput = fmt.Sprintf("step-%d.json", i+1)
			}
			resolvedContextOutput := ResolveVariables(contextOutput, hcpo.variableValues)

			// Read the saved response file to get the actual response
			runWorkspacePath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
			executionWorkspacePath := fmt.Sprintf("%s/execution", runWorkspacePath)
			stepPath := fmt.Sprintf("step-%d", i+1)
			stepExecutionPath := getExecutionFolderPath(executionWorkspacePath, step.GetID(), stepPath)
			responseFilePath := filepath.Join(stepExecutionPath, resolvedContextOutput)

			var executionResult string
			responseContent, err := hcpo.ReadWorkspaceFile(ctx, responseFilePath)
			if err == nil {
				// Parse JSON to extract response
				var responseData map[string]interface{}
				if err := json.Unmarshal([]byte(responseContent), &responseData); err == nil {
					if response, ok := responseData["response"].(string); ok {
						executionResult = response
					} else {
						executionResult = fmt.Sprintf("Human input step completed: %s", step.GetTitle())
					}
				} else {
					executionResult = fmt.Sprintf("Human input step completed: %s", step.GetTitle())
				}
			} else {
				// Fallback if file can't be read
				executionResult = fmt.Sprintf("Human input step completed: %s", step.GetTitle())
			}

			// Ensure slice is large enough (pad with empty strings if needed)
			for len(previousExecutionResults) <= i {
				previousExecutionResults = append(previousExecutionResults, "")
			}
			previousExecutionResults[i] = executionResult
			hcpo.GetLogger().Info(fmt.Sprintf("💾 Stored execution result for human input step %d (will be used by subsequent steps): %s", i+1, executionResult))
			if lastOutcome != nil {
				*lastOutcome = LastExecutedStepOutcome{StepIndex: i, StepID: step.GetID(), ExecutionResult: executionResult, Found: true}
			}

			// Check if we're in single step mode and should stop
			if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
				hcpo.SetRunSingleStepMode(false, -1) // Reset mode
				break
			}

			// Determine the next step from the human response route computed during execution.
			humanInputStep, ok := step.(*HumanInputPlanStep)
			if !ok {
				return fmt.Errorf("step %d is not a HumanInputPlanStep", i+1)
			}
			// Use SelectedNextStepID if computed, otherwise fallback to NextStepID
			nextStepID := humanInputStep.SelectedNextStepID
			if nextStepID == "" {
				nextStepID = humanInputStep.NextStepID
			}

			// Handle next step navigation. This now goes through the same shared
			// helper as routing/todo_task — human_input jumps previously skipped
			// progress cleanup and execution-folder archival entirely, which let
			// converged steps read stale artifacts from a prior pass. The repeat
			// cap is generous: each pass blocks on a human response, so loops are
			// self-limiting and the guard is only a failsafe.
			outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), nextStepID, breakdownSteps, progress, &i, &startFromStep, maxHumanJumpRepeats)
			if navErr != nil {
				return navErr
			}
			if outcome == "end" {
				hcpo.GetLogger().Info(fmt.Sprintf("🏁 Human input step %d specified 'end' - terminating workflow", i+1))
				break
			}
			// "jump" repointed i; "none" falls through to the next sequential step.
			continue
		}

		// Execute crew step by invoking its Crew trigger and waiting for the
		// final response.
		if isCrewStep(step) {
			if ctx != nil && ctx.Err() != nil {
				hcpo.GetLogger().Info(fmt.Sprintf("[STOP] refusing to start step %d of %d (%q) — run is canceled: %v",
					i+1, len(breakdownSteps), step.GetTitle(), ctx.Err()))
				return fmt.Errorf("workflow halted before step %d: %w", i+1, ctx.Err())
			}
			previousContextFiles = make([]string, 0)
			for prevIdx := 0; prevIdx < i; prevIdx++ {
				if prevIdx < len(breakdownSteps) {
					contextOutput := breakdownSteps[prevIdx].GetContextOutput().String()
					if contextOutput != "" {
						resolvedOutput := ResolveVariables(contextOutput, hcpo.variableValues)
						previousContextFiles = append(previousContextFiles, resolvedOutput)
					}
				}
			}

			crewResult, _, err := hcpo.executeCrewStep(ctx, step, i, progress, previousContextFiles, execCtx, breakdownSteps)
			if err != nil {
				if ctx != nil && ctx.Err() != nil {
					hcpo.GetLogger().Info(fmt.Sprintf("[STOP] crew step %d (%q) interrupted: %v", i+1, step.GetTitle(), ctx.Err()))
					return fmt.Errorf("workflow halted during crew step %d: %w", i+1, ctx.Err())
				}
				hcpo.GetLogger().Error(fmt.Sprintf("❌ Crew step %d execution failed: %v", i+1, err), nil)
				hcpo.EmitOrchestratorAgentError(ctx, "workflow", "crew-step-execution", fmt.Sprintf("Execute crew step: %s", step.GetTitle()), err.Error(), i, iteration)
				return fmt.Errorf("crew step %d execution failed: %w", i+1, err)
			}
			hcpo.GetLogger().Info(fmt.Sprintf("✅ Crew step %d completed successfully: %s", i+1, step.GetTitle()))

			// Track execution result in memory for use by subsequent steps.
			executionResult := crewResult.FinalResponse
			for len(previousExecutionResults) <= i {
				previousExecutionResults = append(previousExecutionResults, "")
			}
			previousExecutionResults[i] = executionResult
			hcpo.GetLogger().Info(fmt.Sprintf("💾 Stored execution result for crew step %d (will be used by subsequent steps)", i+1))
			if lastOutcome != nil {
				*lastOutcome = LastExecutedStepOutcome{StepIndex: i, StepID: step.GetID(), ExecutionResult: executionResult, Found: true}
			}

			// Check if we're in single step mode and should stop
			if hcpo.runSingleStepOnly && i == hcpo.singleStepTarget {
				hcpo.GetLogger().Info(fmt.Sprintf("🎯 Single step mode: completed target step %d, stopping execution", i+1))
				break
			}

			crewStep, ok := step.(*CrewPlanStep)
			if !ok {
				return fmt.Errorf("step %d is not a CrewPlanStep", i+1)
			}
			outcome, navErr := hcpo.navigateToNextStepID(ctx, step.GetID(), crewStep.NextStepID, breakdownSteps, progress, &i, &startFromStep, maxLLMJumpRepeats)
			if navErr != nil {
				return navErr
			}
			if outcome == "end" {
				hcpo.GetLogger().Info(fmt.Sprintf("🏁 Crew step %d specified 'end' - terminating workflow", i+1))
				break
			}
			// "jump" repointed i; "none" falls through to the next sequential step.
			continue
		}

		// Execute regular step using executeSingleStep
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
		// Same contract as the message_sequence queue: a canceled run must not
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
			false,                    // isSubAgent = false (regular step)
			previousExecutionResults, // Execution outputs from previous steps
			nil,                      // orchestrationRoutes - nil for regular steps (not sub-agents)
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
		if regularStep, ok := step.(*RegularPlanStep); ok {
			outcome, navErr := hcpo.navigateAfterRegularStep(ctx, regularStep, breakdownSteps, progress, &i, &startFromStep)
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

func (hcpo *StepBasedWorkflowOrchestrator) navigateAfterRegularStep(
	ctx context.Context,
	step *RegularPlanStep,
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

// readLearningHistory reads learning history from the learnings folder
// Returns a file-path reference string (not full content) and any error.
// readGlobalLearningHistory reads the global workflow-level learning history.
// Returns a file-path reference string (not full content) and any error.
func (hcpo *StepBasedWorkflowOrchestrator) readGlobalLearningHistory(
	ctx context.Context,
) (formattedLearningHistory string, err error) {
	hcpo.GetLogger().Info("🔀 Reading global learning history")

	globalLearningsPath := hcpo.getLearningsBasePath() + "/" + GlobalLearningID
	globalSkillPath := globalLearningsPath + "/SKILL.md"
	globalSkillBody, err := hcpo.ReadWorkspaceFile(ctx, globalSkillPath)
	if err != nil || strings.TrimSpace(globalSkillBody) == "" {
		hcpo.GetLogger().Info(fmt.Sprintf("⏭️ No global learning skill found: %s", globalSkillPath))
		return "", nil
	}

	absLearningsPath := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), globalLearningsPath)
	formattedLearningHistory = fmt.Sprintf(
		"📚 **Workflow skill available** at `%s/`.\n"+
			"Read `SKILL.md` first, then only the relevant linked file under `references/`, `scripts/`, `code/`, or `assets/`. "+
			"Treat this prior-run know-how as advisory when it conflicts with the current step.",
		absLearningsPath)
	hcpo.GetLogger().Info("✅ Found global workflow skill (compact path reference only)")

	return formattedLearningHistory, nil
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
