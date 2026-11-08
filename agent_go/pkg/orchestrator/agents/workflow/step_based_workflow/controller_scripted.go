package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	toolcatalog "github.com/manishiitg/coding-agent-loop/agent_go/pkg/platformtools/catalog"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/agent/codeexec"
)

// cleanStepOutputDir deletes all output files/folders in the step execution directory.
// preserveDirs lists subdirectory names to skip (e.g., "code" when main.py is inside it).
// Called before the controller re-runs main.py so pre-validation tests fresh output only.
func (hcpo *StepBasedWorkflowOrchestrator) cleanStepOutputDir(ctx context.Context, stepExecutionRelPath string, preserveDirs ...string) {
	entries, listErr := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, stepExecutionRelPath)
	if listErr != nil {
		return // folder doesn't exist yet — nothing to clean
	}
	preserveSet := make(map[string]bool, len(preserveDirs))
	for _, d := range preserveDirs {
		preserveSet[d] = true
	}
	workspacePath := hcpo.GetWorkspacePath()
	for _, entry := range entries {
		name := filepath.Base(entry)
		if name == "" || name == "." || preserveSet[name] {
			continue
		}
		entryRelPath := stepExecutionRelPath + "/" + name
		// Build absolute path for folder-delete API, accounting for the fact that
		// stepExecutionRelPath may already include the workspacePath prefix.
		var absPath string
		if strings.HasPrefix(entryRelPath, workspacePath+"/") || entryRelPath == workspacePath {
			absPath = workflowAbsPath(entryRelPath)
		} else {
			absPath = workflowAbsPath(workspacePath, entryRelPath)
		}
		// Try file delete first; if it fails (directory entries return error from /api/documents/),
		// fall back to folder delete via /api/folders/.
		fileDelErr := hcpo.DeleteWorkspaceFile(ctx, entryRelPath)
		if fileDelErr != nil {
			if folderErr := deleteFolderViaAPI(ctx, absPath); folderErr != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] cleanStepOutputDir: failed to delete %s: file=%v folder=%v", name, fileDelErr, folderErr))
			}
		}
	}
}

// ScriptedMetadata persists script provenance and runtime statistics.
type ScriptedMetadata struct {
	StepID         string         `json:"step_id"`
	ScriptVersion  int            `json:"script_version"`             // incremented when the saved entry point changes
	EntryPointHash string         `json:"entry_point_hash,omitempty"` // canonical entry-point revision; helpers remain shared source
	CreatedAt      string         `json:"created_at"`
	LastRunAt      string         `json:"last_run_at"`
	TotalRuns      int            `json:"total_runs"`
	SuccessfulRuns map[string]int `json:"successful_runs"` // canonical key is "scripted"; legacy aliases are normalized on read
	FailedRuns     int            `json:"failed_runs"`

	// Rich run tracking (added for debugging & optimization decisions)
	RecentRuns    []RunRecord          `json:"recent_runs,omitempty"`    // last N runs with full details (capped at 10)
	GroupStats    map[string]GroupStat `json:"group_stats,omitempty"`    // per-group success/failure counts
	DurationStats *DurationStats       `json:"duration_stats,omitempty"` // execution time statistics
	LastFailure   *LastFailureInfo     `json:"last_failure,omitempty"`   // details of most recent failure
	CurrentStreak *StreakInfo          `json:"current_streak,omitempty"` // consecutive success/failure streak

}

// RunRecord captures details of a single script execution.
type RunRecord struct {
	Timestamp     string `json:"timestamp"`
	Success       bool   `json:"success"`
	ExitCode      int    `json:"exit_code"`
	DurationMs    int64  `json:"duration_ms"`
	GroupName     string `json:"group_name,omitempty"`
	RunFolder     string `json:"run_folder,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"` // "execution_error", "validation_error", or "execution_and_validation_error"
	ErrorSummary  string `json:"error_summary,omitempty"`  // full error message
}

// GroupStat tracks per-group run statistics.
type GroupStat struct {
	Runs      int `json:"runs"`
	Successes int `json:"successes"`
	Failures  int `json:"failures"`
}

// DurationStats tracks execution time across runs.
type DurationStats struct {
	AvgMs  int64 `json:"avg_ms"`
	MinMs  int64 `json:"min_ms"`
	MaxMs  int64 `json:"max_ms"`
	LastMs int64 `json:"last_ms"`
	Count  int   `json:"count"` // number of runs included in the stats
}

// LastFailureInfo captures the most recent failure for quick debugging.
type LastFailureInfo struct {
	Timestamp    string `json:"timestamp"`
	Reason       string `json:"reason"`
	ErrorSnippet string `json:"error_snippet"` // first ~2000 chars
	GroupName    string `json:"group_name,omitempty"`
	ExitCode     int    `json:"exit_code"`
}

// StreakInfo tracks consecutive success/failure runs.
type StreakInfo struct {
	Type  string `json:"type"` // "success" or "failure"
	Count int    `json:"count"`
}

var ErrScriptedHarnessRejection = errors.New("workspace script invocation failed")

// ErrScriptedHarnessTimeout means main.py started but the outer workspace
// harness killed it after its command deadline. This is not a startup refusal
// and must not enter the code-repair loop.
var ErrScriptedHarnessTimeout = errors.New("script exceeded the workspace harness timeout")

// ScriptedFastPathResult is returned by tryRunSavedScriptedScript.
type ScriptedFastPathResult struct {
	LogFile         string
	LogError        string
	OutputBytes     int
	OutputTruncated bool
	LogComplete     bool
	RanScript       bool   // true if a saved script was found and attempted
	Success         bool   // true if script ran and validation passed
	ExitCode        int    // actual Python exit code (0 = success, -1 = not-run/API error)
	Output          string // combined stdout from script
	Error           string // execution or validation error
	ExecutionError  string // raw script execution failure, if any
	ValidationError string // output/pre-validation failure, if any
	FailureReason   string // "execution_error", "validation_error", or "execution_and_validation_error"
	ExistingScript  string // source used for this invocation
	// HarnessFailure means no reliable execution result was returned. A lost
	// request may have started the script; this is not evidence of a code defect
	// and must never enter the automatic repair path.
	HarnessFailure bool
	HarnessError   string
	// HarnessTimeout means the process started and may have emitted partial
	// output, but the outer workspace deadline killed it. It is terminal for
	// this attempt and must not be presented to the LLM as a code bug.
	HarnessTimeout bool
	TimeoutError   string
}

func reviewMainPyScript(script string, declaredEnvVars ...string) []string {
	var issues []string
	lines := strings.Split(script, "\n")

	// Build set of declared env vars for quick lookup
	declaredSet := map[string]bool{}
	for _, v := range declaredEnvVars {
		declaredSet[v] = true
	}

	// Precompile patterns
	reHardcodedRunPath := regexp.MustCompile(`['"]/?app/workspace-docs/.*/runs/iteration-\d+/[^/'"]*/execution`)
	reHardcodedWorkspacePath := regexp.MustCompile(`['"]/?app/workspace-docs/[^'"]{20,}['"]`)
	reEnvGetWithDefault := regexp.MustCompile(`os\.environ\.get\(\s*['"][^'"]+['"]\s*,\s*['"][^'"]+['"]\s*\)`)
	reEnvGetVarName := regexp.MustCompile(`os\.environ\.get\(\s*['"]((?:VAR_|SECRET_)[^'"]+)['"]\s*,\s*['"][^'"]+['"]\s*\)`)
	reSiblingStepPath := regexp.MustCompile(`/execution/[a-z][\w-]+/`)
	reStepOutputDirUsed := regexp.MustCompile(`os\.environ\[['"]STEP_OUTPUT_DIR['"]\]|STEP_OUTPUT_DIR`)
	reStepExecDirUsed := regexp.MustCompile(`os\.environ\[['"]STEP_EXECUTION_DIR['"]\]|STEP_EXECUTION_DIR`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		lineNum := i + 1

		// Check 1: Hardcoded execution paths with group names
		if reHardcodedRunPath.MatchString(line) {
			issues = append(issues, fmt.Sprintf(
				"Line %d: Hardcoded execution path with group name. Use `os.environ['STEP_EXECUTION_DIR']` instead. Found: %s",
				lineNum, strings.TrimSpace(line)))
		}

		// Check 2: os.environ.get with hardcoded fallback for VAR_*/SECRET_* — only flag if the var is actually declared
		if matches := reEnvGetVarName.FindStringSubmatch(line); len(matches) > 1 {
			varName := matches[1]
			if len(declaredSet) > 0 && declaredSet[varName] {
				// Declared variable used with fallback — should use os.environ['KEY'] instead
				issues = append(issues, fmt.Sprintf(
					"Line %d: Using os.environ.get() with hardcoded fallback for declared variable %s. Use `os.environ['%s']` (no default) so missing vars fail loudly. Found: %s",
					lineNum, varName, varName, strings.TrimSpace(line)))
			}
			// If not in declaredSet, the script invented this var — don't flag it
		}

		// Check 3: os.environ.get with any hardcoded default (weaker signal, only flag for known env vars)
		if !reEnvGetVarName.MatchString(line) && reEnvGetWithDefault.MatchString(line) {
			// Check if it's for a known step env var
			if strings.Contains(line, "STEP_OUTPUT_DIR") || strings.Contains(line, "STEP_EXECUTION_DIR") ||
				strings.Contains(line, "MCP_API_URL") || strings.Contains(line, "MCP_API_TOKEN") {
				issues = append(issues, fmt.Sprintf(
					"Line %d: Using os.environ.get() with fallback for a required env var. Use `os.environ['KEY']` instead. Found: %s",
					lineNum, strings.TrimSpace(line)))
			}
		}
	}

	// Check 4: Script references sibling step folders but doesn't use STEP_EXECUTION_DIR
	hasSiblingRef := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if reSiblingStepPath.MatchString(line) && !strings.Contains(line, "STEP_OUTPUT_DIR") && !strings.Contains(line, "STEP_EXECUTION_DIR") {
			hasSiblingRef = true
			break
		}
	}
	if hasSiblingRef && !reStepExecDirUsed.MatchString(script) {
		issues = append(issues, "Script references sibling step folders via hardcoded paths. Use `os.environ['STEP_EXECUTION_DIR']` to build paths to other steps (e.g., `os.path.join(os.environ['STEP_EXECUTION_DIR'], 'other-step/output.json')`).")
	}

	// Check 5: Long hardcoded workspace paths (even if not matching the run pattern)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if reHardcodedWorkspacePath.MatchString(line) && !reHardcodedRunPath.MatchString(line) {
			// Only flag if not using an env var on the same line
			if !reStepOutputDirUsed.MatchString(line) && !reStepExecDirUsed.MatchString(line) {
				issues = append(issues, fmt.Sprintf(
					"Line %d: Long hardcoded workspace path detected. Derive paths from environment variables (STEP_OUTPUT_DIR, STEP_EXECUTION_DIR) instead. Found: %s",
					i+1, strings.TrimSpace(line)))
			}
		}
	}

	// Check 6: Script writes output but doesn't use STEP_OUTPUT_DIR
	reOpenWrite := regexp.MustCompile(`open\s*\(\s*['"][^'"]+['"].*['"]w['"]`)
	hasWriteWithoutEnv := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if reOpenWrite.MatchString(line) && !reStepOutputDirUsed.MatchString(line) && !strings.Contains(line, "output_dir") && !strings.Contains(line, "OUTPUT_DIR") {
			hasWriteWithoutEnv = true
			break
		}
	}
	if hasWriteWithoutEnv && !reStepOutputDirUsed.MatchString(script) {
		issues = append(issues, "Script writes files but never references STEP_OUTPUT_DIR. Output files must be written to `os.environ['STEP_OUTPUT_DIR']`.")
	}

	// Check 7: Script has sys.argv references but builds sibling paths manually instead
	reSysArgv := regexp.MustCompile(`sys\.argv\[`)
	reManualSiblingPath := regexp.MustCompile(`os\.path\.join\s*\(.*execution.*,\s*['"][a-z][\w-]+`)
	if reSysArgv.MatchString(script) && reManualSiblingPath.MatchString(script) {
		issues = append(issues, "Script receives input via sys.argv but also constructs manual paths to sibling step folders. Read ALL input data from sys.argv — do not build sibling paths manually. If you need additional input, add it as a context_dependency in the plan.")
	}

	// Check 8: Script writes to knowledgebase/ or learnings/ — these are system-managed folders,
	// not scratch space. Scripts that write caches here are taking shortcuts that break on reuse.
	reWriteToSystemDir := regexp.MustCompile(`(?i)['"]/?(?:app/workspace-docs/)?[^'"]*(?:knowledgebase|learnings)/[^'"]*['"]`)
	reOpenWriteMode := regexp.MustCompile(`open\s*\(.*['"]w`)
	reMkdir := regexp.MustCompile(`(?:os\.makedirs|os\.mkdir|pathlib\.Path.*mkdir)`)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if reWriteToSystemDir.MatchString(line) {
			if reOpenWriteMode.MatchString(line) || reMkdir.MatchString(line) || strings.Contains(line, "shutil.copy") || strings.Contains(line, "shutil.move") {
				issues = append(issues, fmt.Sprintf(
					"Line %d: Script writes to knowledgebase/ or learnings/ directory. These are system-managed — do NOT use them as cache or scratch space. Write all output to `os.environ['STEP_OUTPUT_DIR']` instead. Found: %s",
					i+1, strings.TrimSpace(line)))
			}
		}
	}

	// Check 9: Script creates its own cache/shortcut paths outside STEP_OUTPUT_DIR
	// Detects patterns like CACHE_DIR = '/app/...', cache_path = '...knowledgebase...'
	reCacheVar := regexp.MustCompile(`(?i)(?:cache|cached|shortcut|precomputed|saved_results?)\s*[=:]`)
	reCacheWithHardcodedPath := regexp.MustCompile(`(?i)(?:cache|cached|shortcut|precomputed|saved_results?)\s*=\s*['"/]`)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if reCacheVar.MatchString(line) && reCacheWithHardcodedPath.MatchString(line) {
			// Only flag if it's not using STEP_OUTPUT_DIR
			if !reStepOutputDirUsed.MatchString(line) && !reStepExecDirUsed.MatchString(line) {
				issues = append(issues, fmt.Sprintf(
					"Line %d: Script defines a cache/shortcut path with a hardcoded value. This bypasses the actual work the step should perform. If caching is needed, use `os.environ['STEP_OUTPUT_DIR']`. Found: %s",
					i+1, strings.TrimSpace(line)))
			}
		}
	}

	// Check 10: Ephemeral browser refs saved into main.py. Refs like @e1, e68, "ref": "abc123"
	// only exist within a single snapshot — they change every session, so baking them into
	// main.py guarantees the replay will fail. Fire only when the script looks browser-ish.
	reBrowserSignal := regexp.MustCompile(`browser_(?:click|type|snapshot|evaluate|navigate|select|fill|hover)|agent[_-]browser`)
	if reBrowserSignal.MatchString(script) {
		reEphemeralRef := regexp.MustCompile(`['"]@e\d+['"]|['"]\bref['"]\s*:\s*['"][a-zA-Z0-9]{4,}['"]`)
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if reEphemeralRef.MatchString(line) {
				issues = append(issues, fmt.Sprintf(
					"Line %d: Ephemeral browser ref (e.g. @e1, {\"ref\": \"abc123\"}) saved into main.py. Refs change every snapshot/session — the replay will fail. Replace with a durable locator: data-testid > hand-written #id > aria-label > role+name > get_by_label/placeholder/text. Found: %s",
					i+1, strings.TrimSpace(line)))
			}
		}
	}

	return issues
}

// getScriptedDirRelPath returns the learnings subdirectory (relative to workspace root).
func getScriptedDirRelPath(stepID string) string {
	return fmt.Sprintf("learnings/%s", stepID)
}

// readScriptedMetadataAPI reads script_metadata.json via the workspace API.
// Returns nil if the file is missing or cannot be parsed.
func (hcpo *StepBasedWorkflowOrchestrator) readScriptedMetadataAPI(ctx context.Context, stepID string) *ScriptedMetadata {
	relPath := hcpo.scriptedMetadataPath(stepID)
	data, err := hcpo.ReadWorkspaceFile(ctx, relPath)
	if err != nil {
		return nil
	}
	var meta ScriptedMetadata
	if err := json.Unmarshal([]byte(data), &meta); err == nil {
		normalizeScriptedSuccessCounts(&meta)
		return &meta
	}
	// Legacy format: successful_runs was a plain int, not a map.
	var legacy struct {
		StepID         string `json:"step_id"`
		ScriptVersion  int    `json:"script_version"`
		CreatedAt      string `json:"created_at"`
		LastRunAt      string `json:"last_run_at"`
		TotalRuns      int    `json:"total_runs"`
		SuccessfulRuns int    `json:"successful_runs"`
		FailedRuns     int    `json:"failed_runs"`
	}
	if err := json.Unmarshal([]byte(data), &legacy); err != nil {
		return nil
	}
	return &ScriptedMetadata{
		StepID:         legacy.StepID,
		ScriptVersion:  legacy.ScriptVersion,
		CreatedAt:      legacy.CreatedAt,
		LastRunAt:      legacy.LastRunAt,
		TotalRuns:      legacy.TotalRuns,
		SuccessfulRuns: map[string]int{"scripted": legacy.SuccessfulRuns},
		FailedRuns:     legacy.FailedRuns,
	}
}

func normalizeScriptedSuccessCounts(meta *ScriptedMetadata) {
	if meta == nil {
		return
	}
	// TotalRuns/FailedRuns are updated atomically with every saved-script
	// attempt and therefore avoid double-counting files that contain both the
	// old "agentic" mirror and an older "scripted" bucket.
	successes := meta.TotalRuns - meta.FailedRuns
	if successes < 0 {
		successes = 0
	}
	if meta.TotalRuns == 0 {
		for _, key := range []string{"scripted", "agentic", "learn_code", "code_exec"} {
			if meta.SuccessfulRuns[key] > successes {
				successes = meta.SuccessfulRuns[key]
			}
		}
	}
	meta.SuccessfulRuns = map[string]int{"scripted": successes}
}

func (hcpo *StepBasedWorkflowOrchestrator) writeScriptedMetadataAPI(ctx context.Context, stepID string, meta ScriptedMetadata) error {
	relPath := hcpo.scriptedMetadataPath(stepID)
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return hcpo.WriteWorkspaceFile(ctx, relPath, string(data))
}

// hasValidLearnedScriptAPI returns true if main.py exists via workspace API.
func (hcpo *StepBasedWorkflowOrchestrator) hasValidLearnedScriptAPI(ctx context.Context, stepID string) bool {
	scriptRelPath := hcpo.scriptedSourceDir(stepID) + "/main.py"
	_, err := hcpo.ReadWorkspaceFile(ctx, scriptRelPath)
	return err == nil
}

func (hcpo *StepBasedWorkflowOrchestrator) buildScriptedInputArgs(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	allSteps []PlanStepInterface,
	executionWorkspacePath string,
	docsRoot string,
	variableValues map[string]string,
) []string {
	deps := step.GetContextDependencies()
	if len(deps) == 0 {
		return nil
	}
	resolved := ResolveVariablesArray(deps, variableValues)
	return hcpo.resolveDependencyPathsWithWorkspace(ctx, resolved, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, variableValues)
}

// shellQuotePath wraps an absolute path in single quotes for safe shell embedding.
func shellQuotePath(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func (hcpo *StepBasedWorkflowOrchestrator) resolveScriptedShellGuard(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	stepExecutionRelPath string,
	includeCodeDir bool,
) (*workspace.FolderGuardConfig, error) {
	stepConfig := getAgentConfigs(step)
	kbAccess := resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase())

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(stepPath, step.GetID(), kbAccess, hcpo.resolveDBAccess(stepConfig), stepConfig)
	readPaths = common.DeduplicateStrings(appendDescriptionReferenceReadPaths(readPaths, hcpo.GetWorkspacePath(), step.GetDescription()))
	// Sequence batches use an invocation-specific output folder while retaining
	// the saved script's identity and store permissions. Never grant the script's
	// ordinary execution folder instead of the actual invocation directory.
	if len(writePaths) > 0 && stepExecutionRelPath != "" {
		writePaths[0] = stepExecutionRelPath
	}
	_, _, readOnlyPaths, _ := appendWorkflowFolderAccess(hcpo.GetWorkspacePath(), nil, nil, kbAccessAllowsRead(resolveKnowledgebaseAccess(getAgentConfigs(step), hcpo.UseKnowledgebase())))
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.TriggerContextFile != "" {
		readOnlyPaths = append(readOnlyPaths, opts.TriggerContextFile)
	}
	if opts := hcpo.GetExecutionOptions(); opts != nil && opts.WebhookInputFile != "" {
		readOnlyPaths = append(readOnlyPaths, opts.WebhookInputFile)
	}
	if includeCodeDir && len(writePaths) > 0 {
		writePaths = append(writePaths, writePaths[0]+"/code")
	}
	if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil {
		// Source is authored through the Crew editor, never by a running copy.
		// Protect the directory as well as existing files, including new names.
		readOnlyPaths = append(readOnlyPaths, saved.crewScript.Root)
		readOnlyPaths = append(readOnlyPaths, stepExecutionRelPath+"/code", hcpo.GetWorkspacePath()+"/runs/source-blobs")
	}
	readPaths = common.DeduplicateStrings(append(readPaths, writePaths...))
	if stateRoot := hcpo.pythonHelperStateRoot(); stateRoot != "" {
		readPaths = append(readPaths, stateRoot)
		readOnlyPaths = append(readOnlyPaths, stateRoot)
	}
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, err
	}

	return &workspace.FolderGuardConfig{
		Enabled:           true,
		ReadPaths:         readPaths,
		WritePaths:        writePaths,
		BlockedWritePaths: readOnlyPaths,
		BlockedPaths:      hcpo.executionStoreBlockedPaths(),
	}, nil
}

// Only inspect unconditional top-level imports/config reads. Conditional imports
// and runtime configuration belong to the script; do not execute modules here.
const scriptedBundlePreflight = `import ast, importlib.util, os, pathlib, sys
root = pathlib.Path(sys.argv[1]).parent
sys.path.insert(0, str(root))
problems = []
for p in sorted(root.glob('*.py')):
    tree = ast.parse(p.read_bytes(), filename=str(p))
    compile(tree, str(p), 'exec')
    for node in tree.body:
        names = []
        if isinstance(node, ast.Import):
            names = [a.name.split('.')[0] for a in node.names]
        elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
            names = [node.module.split('.')[0]]
        for name in names:
            if importlib.util.find_spec(name) is None:
                problems.append(f'{p.name}: missing module {name}')
        if isinstance(node, (ast.Assign, ast.AnnAssign)):
            for ref in ast.walk(node):
                if isinstance(ref, ast.Subscript) and isinstance(ref.value, ast.Attribute) and isinstance(ref.value.value, ast.Name) and ref.value.value.id == 'os' and ref.value.attr == 'environ' and isinstance(ref.slice, ast.Constant) and isinstance(ref.slice.value, str):
                    if ref.slice.value not in os.environ:
                        problems.append(f'{p.name}: missing environment variable {ref.slice.value}')
if problems:
    print('[preflight] ' + '\n[preflight] '.join(problems), file=sys.stderr)
    sys.exit(1)
print('[preflight] Python bundle syntax, top-level imports and configuration OK', flush=True)
`

// A saved script must use its step's tools, independently of the caller's
// chat tools. In particular a pinned Run chat has no database tools to lend
// its group session. Give each invocation its own registry so parallel helpers
// cannot replace one another's access or leave broader tools on a cached group.
func (hcpo *StepBasedWorkflowOrchestrator) configureScriptBridgeSession(ctx context.Context, step PlanStepInterface, guard *workspace.FolderGuardConfig, dbAccess string, env map[string]string, codeDir, outputDir string) func() {
	ctx, finishActivities := context.WithCancel(ctx)
	sessionID := fmt.Sprintf("script-step-%s-%d", step.GetID(), time.Now().UnixNano())
	if hcpo.httpSessionID != "" {
		mcpagent.RegisterHTTPSession(hcpo.httpSessionID, sessionID)
	}
	hcpo.configureSubAgentSessionGuard(sessionID, "scripted", step.GetID(), guard.ReadPaths, guard.WritePaths)
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, guard.BlockedWritePaths)
	common.SetSessionFolderGuardBlockedPaths(sessionID, guard.BlockedPaths)
	configureWorkflowDBSession(sessionID, hcpo.GetWorkspacePath(), dbAccess, false)
	if parent := common.GetSessionShellConfig(hcpo.GetMCPSessionID()); parent != nil {
		hcpo.bindWorkshopBrowserSession(sessionID, parent.BrowserSessionID)
	}
	bridgeConfig := stepConfigWithDescribedHumanTools(getAgentConfigs(step), step)
	if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil && len(saved.crewScript.ToolSelections) > 0 {
		var copy AgentConfigs
		if bridgeConfig != nil {
			copy = *bridgeConfig
		}
		copy.EnabledCustomTools = append(append([]string{}, copy.EnabledCustomTools...), saved.crewScript.ToolSelections...)
		bridgeConfig = &copy
	}
	_, executors := hcpo.prepareOrchestratorCustomTools(bridgeConfig, dbAccess)
	bridgeTools := make(map[string]func(context.Context, map[string]interface{}) (string, error), len(executors))
	allow := make(map[string]bool, len(executors))
	for name, executor := range executors {
		if fn, ok := executor.(func(context.Context, map[string]interface{}) (string, error)); ok {
			bridgeTools[name] = fn
			allow[name] = true
		}
	}
	// Saved scripts and the built-in helpers call the public family names (database, metrics, ...).
	for family, fn := range toolcatalog.ScriptFamilies(bridgeTools) {
		if _, taken := bridgeTools[family]; !taken {
			bridgeTools[family] = fn
			allow[family] = true
		}
	}
	ctx = context.WithValue(ctx, pythonScriptSessionKey{}, sessionID)
	bridgeTools["python_automation"] = hcpo.pythonAutomationHandler(ctx, step, codeDir, outputDir)
	allow["python_automation"] = true
	env["WORKFLOW_RUN_DIR"] = workflowAbsPath(hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder)
	codeexec.InitRegistryForSession(sessionID, bridgeTools, hcpo.GetLogger())
	codeexec.SetSessionToolAllowList(sessionID, allow)
	env["MCP_SESSION_ID"] = sessionID
	if url := buildSessionScopedMCPAPIURL(sessionID); url != "" {
		env["MCP_API_URL"] = url
	}
	common.PopulateMCPBridgeShortEnv(env)
	registerStepSessionShellEnv(sessionID, env["STEP_OUTPUT_DIR"], env["STEP_EXECUTION_DIR"], env["DB_PATH"], env["RUN_FOLDER"], env)
	configureWorkshopToolAgentBridgeSession(sessionID)
	hcpo.GetLogger().Info(fmt.Sprintf("[SCRIPT_BRIDGE] Registered step tools session=%s step=%s db_access=%s tools=%d", sessionID, step.GetID(), dbAccess, len(bridgeTools)))
	return func() {
		finishActivities()
		codeexec.CleanupSession(sessionID)
		common.ClearSessionShellConfig(sessionID)
		mcpagent.CloseSession(sessionID)
	}
}

// execScriptedScript runs python3 <mainPy> <args...> via the workspace shell API.
// Uses workspace.Client (same path as the LLM agent's execute_shell_command tool) so that
// folder guard, ExtraEnv (SECRET_*, MCP_API_URL), and path handling all work consistently.
//
// workDirAbsPath  — absolute path to the script's working directory (code/ or learnings/{id}/)
// stepOutputAbsPath — absolute path for STEP_OUTPUT_DIR env var (execution/step-N/)
// stepExecutionRelPath — workspace-relative path to the execution folder (for FolderGuard write paths)
func (hcpo *StepBasedWorkflowOrchestrator) execScriptedScript(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	mainPyAbsPath string,
	inputArgs []string,
	stepOutputAbsPath string,
	workDirAbsPath string,
	stepExecutionRelPath string,
	dbAccess string,
) (output string, exitCode int, execErr error) {
	var definition *CrewScriptDefinition
	if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil {
		definition = &saved.crewScript.Definition
	}
	timeout, err := definition.executionTimeoutSeconds()
	if err != nil {
		return "", -1, fmt.Errorf("%w: %w", ErrScriptedHarnessRejection, err)
	}
	// Convert absolute workDir to workspace-relative for WorkingDirectory field.
	// The workspace API prepends docsRoot, so passing the absolute path would double it.
	workDirRel := workflowAPIPath(hcpo.GetWorkspacePath(), workDirAbsPath)
	if workDirRel == workDirAbsPath {
		// Didn't start with docsRoot — fall back to empty (workspace root)
		workDirRel = hcpo.scriptedWorkingDir(step.GetID(), stepExecutionRelPath)
	}

	if hcpo.crewAutomationCodeRoot != "" && strings.HasPrefix(workDirAbsPath, hcpo.crewAutomationCodeRoot+"/") {
		workDirRel = hcpo.scriptedWorkingDir(step.GetID(), stepExecutionRelPath)
	}

	// Build command: STEP_OUTPUT_DIR is passed via ExtraEnv so no inline env prefix needed.
	// Use absolute paths for main.py and args — /app/workspace-docs/... is valid inside Docker.
	command := scriptedCommand(mainPyAbsPath, inputArgs)

	includeCodeDir := workDirRel == stepExecutionRelPath+"/code"
	guard, guardErr := hcpo.resolveScriptedShellGuard(ctx, step, stepIndex, stepPath, stepExecutionRelPath, includeCodeDir)
	if guardErr != nil {
		return "", -1, guardErr
	}

	// ExtraEnv: merge workspace env (SECRET_*, MCP_API_URL) with STEP_OUTPUT_DIR and STEP_EXECUTION_DIR.
	stepExecutionAbsPath := stepExecutionScopeAbsPath(stepOutputAbsPath)
	extraEnv := map[string]string{
		"STEP_OUTPUT_DIR":         stepOutputAbsPath,
		"SCRIPT_SOURCE_DIR":       workDirAbsPath,
		"STEP_EXECUTION_DIR":      stepExecutionAbsPath,
		"PYTHONDONTWRITEBYTECODE": "1",
		"SCRIPT_VERBOSE":          "1", // Enable verbose logging in scripts — stdout is only read on failure
	}
	_, _, _, folderEnv := appendWorkflowFolderAccess(hcpo.GetWorkspacePath(), nil, nil, kbAccessAllowsRead(resolveKnowledgebaseAccess(getAgentConfigs(step), hcpo.UseKnowledgebase())))
	for key, value := range folderEnv {
		extraEnv[key] = value
	}
	if envRef := hcpo.GetWorkspaceEnvRef(); envRef != nil {
		hcpo.LockWorkspaceEnv()
		for k, v := range envRef {
			if k == "STEP_OUTPUT_DIR" || k == "STEP_EXECUTION_DIR" || k == "SCRIPT_SOURCE_DIR" {
				// Never let the shared workspace env override the per-step folders.
				// These are execution-specific and can leak across concurrently
				// running steps if read from the shared env map.
				continue
			}
			extraEnv[k] = v
		}
		hcpo.UnlockWorkspaceEnv()
	}
	declared := []string{}
	if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil {
		declared = saved.crewScript.Definition.Secrets
	}
	names, _ := json.Marshal(declared)
	extraEnv["SCRIPT_REQUIRED_SECRETS_JSON"] = string(names)

	// DB_PATH: the scripted fast path execs python3 directly — it does NOT go
	// through the agent's execute_shell_command wrapper (injectStepEnvIntoShellExecutor)
	// that injects DB_PATH. Without it a saved main.py doing os.environ['DB_PATH']
	// fails with "DB_PATH unset and no root found". Set it here to the same absolute
	// path the agent path uses. Set AFTER the workspace-env merge so it always wins.
	if dbAccess != DBAccessNone {
		extraEnv["DB_PATH"] = workflowAbsPath(hcpo.GetWorkspacePath(), DBFolderName, "db.sqlite")
	}
	extraEnv = hcpo.codeRuntimeEnv(extraEnv)
	if saved, ok := step.(*ScriptedPlanStep); ok && saved.crewScript != nil {
		useExecutedScriptSource(extraEnv, workDirAbsPath)
	}

	// RUN_FOLDER: the workspace-relative "iteration-N/<group>" segment under
	// runs/, e.g. "iteration-85/customer-staging". PLAT-185: a script that needs
	// to read a SIBLING step's runs/.../logs/<other-step>/... had no reliable
	// way to know the current run's own folder name, so it either had to
	// hardcode a guess (wrong the moment the platform advances past that
	// iteration) or derive it independently and risk not matching what
	// setupExecutionFolderGuard actually granted. hcpo.selectedRunFolder is the
	// orchestrator's own value for this — the same one that already builds
	// runWorkspacePath for both the write scope and the additional_read_paths
	// grant — so exposing it directly removes the guess entirely. Empty when no
	// run folder is selected (setupExecutionFolderGuard falls back to the bare
	// workspace root in that case too); scripts must treat "" as "not
	// run-scoped," not assume a folder exists.
	extraEnv["RUN_FOLDER"] = hcpo.selectedRunFolder
	// Orchestrator routes may add instructions at dispatch time. The saved-script
	// fast path never renders the cloned step description, so expose that dynamic
	// contract explicitly in this child process. This is context-local and cannot
	// leak between concurrently running todo routes.
	extraEnv = appendScriptedDelegationEnv(ctx, extraEnv)
	releaseBridge := hcpo.configureScriptBridgeSession(ctx, step, guard, dbAccess, extraEnv, workDirAbsPath, stepExecutionRelPath)
	defer releaseBridge()
	if len(scriptedParameterDefinitions(step)) > 0 {
		if _, present := extraEnv[ScriptedParametersEnv]; !present {
			extraEnv[ScriptedParametersEnv] = "{}"
		}
	}

	envKeys := make([]string, 0, len(extraEnv))
	for k := range extraEnv {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	hcpo.GetLogger().Info(fmt.Sprintf(
		"🐍 [scripted_code] Direct script MCP preflight | script=%s | workdir=%s | step_output=%s | read_paths=%v | write_paths=%v | MCP_SESSION_ID=%s | MCP_API_URL=%s | env_keys=%v",
		mainPyAbsPath,
		workDirRel,
		stepOutputAbsPath,
		guard.ReadPaths,
		guard.WritePaths,
		extraEnv["MCP_SESSION_ID"],
		extraEnv["MCP_API_URL"],
		envKeys,
	))

	// Build request using the same ExecuteShellCommandParams struct the LLM agent uses,
	// so the workspace API receives the same JSON shape and applies the same logic.
	// Send the saved script's explicit whole-process limit. The workspace maps
	// zero to one hour; it does not mean unlimited. Caller cancellation still
	// travels on the same request context.
	// The script's DB writes go through the bridge as its MCP session, so that
	// session must carry the step's DB grant.
	grantScriptBridgeSessionDB(extraEnv["MCP_SESSION_ID"], hcpo.GetWorkspacePath(), dbAccess)
	// A test run's script (PLAT-562): its bridge calls are guarded, its DB is
	// the copy and it cannot write outside the test run folder.

	useShell := true
	reqParams := workspace.ExecuteShellCommandParams{
		Command:          command,
		WorkingDirectory: workDirRel,
		Timeout:          &timeout,
		UseShell:         &useShell,
		FolderGuard:      guard,
		ExtraEnv:         extraEnv,
		DBReadSnapshot:   dbAccess == DBAccessRead,
		// The bridge and shell use this step session and its existing grants.
		BridgeSessionID: strings.TrimSpace(extraEnv["MCP_SESSION_ID"]),
	}
	logReceipt, _ := ctx.Value(scriptLogKey{}).(*scriptLogReceipt)
	if logReceipt != nil {
		reqParams.LogFile = logReceipt.apiPath
	}

	// Dispatch through the same client the agent's own execute_shell_command uses.
	//
	// This used to build and send its own request, which is how the two diverged:
	// workspace.Client attaches X-Workspace-Token and X-User-ID, and this path
	// attached neither, so /api/execute — the one token-protected route — rejected
	// every scripted run. The agent could not see it. Its self-test went through
	// the client and passed; only the controller's run failed, so the agent was
	// told its working script was broken and duly "fixed" it.
	//
	// Keeping one door means a test run and a real run cannot disagree again.
	if hcpo.WorkspaceClient == nil {
		return "", -1, fmt.Errorf("%w: no workspace client configured", ErrScriptedHarnessRejection)
	}

	// Freeze the source at dispatch; later edits/retries must not rewrite this
	// execution's evidence. A transport refusal is recorded as not started.
	snapshotStart := time.Now()
	scriptRel := workflowAPIPath(hcpo.GetWorkspacePath(), mainPyAbsPath)
	scriptSource, _ := hcpo.ReadWorkspaceFile(ctx, scriptRel)
	hcpo.recordInspectionEvents(ctx,
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: snapshotStart, Type: "script_path", Text: scriptRel},
		orchevents.BackgroundAgentTranscriptEvent{Timestamp: snapshotStart, Type: "script_source", Text: scriptSource})
	stdout, stderr := "", ""
	defer func() {
		status := "completed"
		if execErr != nil || exitCode != 0 {
			status = "failed"
		}
		if errors.Is(execErr, ErrScriptedHarnessRejection) {
			status = "not_started"
			var requestErr *url.Error
			if errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) || errors.As(execErr, &requestErr) {
				status = "interrupted"
			}
		}
		hcpo.recordInspectionEvents(ctx, scriptInspectionEnd(snapshotStart, stdout, stderr, exitCode, status, execErr)...)
	}()
	// PLAT-861-343: the client resolves policy by session, overriding request
	// guards. Use the registered step session rather than the parent function's
	// whole-project guard; identity, cancellation and inspection stay on ctx.
	shellCtx := ctx
	if sessionID := strings.TrimSpace(extraEnv["MCP_SESSION_ID"]); sessionID != "" {
		shellCtx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	}
	if logReceipt != nil {
		logReceipt.dispatched = true
	}
	result, err := hcpo.WorkspaceClient.ExecuteShellCommand(shellCtx, reqParams)
	if logReceipt != nil {
		if result.LogFile != "" {
			logReceipt.LogFile = result.LogFile
		}
		logReceipt.LogError = result.LogError
		logReceipt.LogComplete = result.LogComplete
		if result.LogFile == "" && logReceipt.LogError == "" {
			logReceipt.LogError = "workspace did not confirm a full log; inspect the saved run before retrying"
		}
	}
	if err != nil {
		// A transport failure can arrive after the process starts. Preserve the
		// infrastructure error without claiming it produced no side effects.
		return "", -1, fmt.Errorf("%w: %w", ErrScriptedHarnessRejection, err)
	}

	stdout = scriptOutputPreview(result.Stdout, result.LogFile)
	stderr = scriptOutputPreview(result.Stderr, result.LogFile)
	combined := result.Stdout
	if result.Stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += result.Stderr
	}
	preview := scriptOutputPreview(combined, result.LogFile)
	if logReceipt != nil {
		logReceipt.OutputBytes = len(combined)
		logReceipt.OutputTruncated = preview != combined
	}
	combined = preview
	exitCode = result.ExitCode
	if result.TimedOut {
		return combined, exitCode, fmt.Errorf("%w: script reached its %d-second limit: %s", ErrScriptedHarnessTimeout, timeout, result.Error)
	}
	hcpo.GetLogger().Info(fmt.Sprintf(
		"🐍 [scripted_code] Direct script shell result | exit_code=%d | api_error=%q | script=%s",
		exitCode,
		result.Error,
		mainPyAbsPath,
	))
	if result.Error != "" && exitCode == 0 {
		// The API reported a failure but no process ever ran (exit 0 with an
		// error is the shape of a rejected request, not a failed script).
		exitCode = -1
		execErr = fmt.Errorf("%w: %s", ErrScriptedHarnessRejection, result.Error)
	}
	return combined, exitCode, execErr
}

// tryRunSavedScriptedScript checks for a saved main.py and runs it:
//   - No saved script               → RanScript=false; caller reports missing source
//   - Script ran + validation passed → RanScript=true, Success=true
//   - Script failed                  → RanScript=true, Success=false; caller reports the error
func (hcpo *StepBasedWorkflowOrchestrator) tryRunSavedScriptedScript(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	allSteps []PlanStepInterface,
	stepExecutionRelPath string, // workspace-relative, e.g. "Workflow/X/runs/iter-1/execution/step-2"
	executionWorkspacePath string, // workspace-relative execution root
	dbAccess string,
) (finalResult *ScriptedFastPathResult) {
	ctx, logReceipt := hcpo.prepareScriptLog(ctx, stepExecutionRelPath)
	defer func() { hcpo.finishScriptLog(ctx, logReceipt, finalResult) }()
	docsRoot := GetPromptDocsRoot()
	stepID := step.GetID()

	shared, hasShared := step.(*ScriptedPlanStep)
	hasShared = hasShared && shared.crewScript != nil
	if hasShared {
		if err := hcpo.validateScriptSecrets(&shared.crewScript.Definition); err != nil {
			return &ScriptedFastPathResult{RanScript: true, ExitCode: -1, Error: err.Error()}
		}
	}
	if !hasShared && !hcpo.hasValidLearnedScriptAPI(ctx, stepID) {
		scriptRelPath := hcpo.scriptedSourceDir(stepID) + "/main.py"
		existingScript, _ := hcpo.ReadWorkspaceFile(ctx, scriptRelPath)
		hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] No saved script for step %d (%s) — source must be created in Crew chat", stepIndex+1, stepID))
		return &ScriptedFastPathResult{RanScript: false, ExistingScript: existingScript}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Executing saved script for step %d (%s) — 0 LLM tokens", stepIndex+1, stepID))

	// Read the source for static checks and this invocation.
	learnDirRelPath := hcpo.scriptedSourceDir(stepID)
	scriptRelPath := learnDirRelPath + "/main.py"
	existingScript, _ := hcpo.ReadWorkspaceFile(ctx, scriptRelPath)

	stepExecutionAbsPath := workflowAbsPath(stepExecutionRelPath)
	codeDirRelPath := hcpo.scriptedWorkingDir(stepID, stepExecutionRelPath)
	codeDirAbsPath := workflowAbsPath(codeDirRelPath)
	if hcpo.crewAutomationCodeRoot != "" && hcpo.usesCodeTree() {
		codeDirAbsPath = hcpo.scriptedSourceAbsolute(stepID)
	}
	mainPyAbsPath := filepath.Join(codeDirAbsPath, "main.py")
	if hasShared {
		parameters, err := hcpo.crewScriptParameters(ctx, shared, stepIndex, stepPath, allSteps, executionWorkspacePath)
		if err != nil {
			return &ScriptedFastPathResult{RanScript: true, Error: err.Error(), ExitCode: -1}
		}
		ctx = withScriptedDelegationContext(ctx, stepID, "", "", parameters)
		codeDirRelPath = stepExecutionRelPath + "/code"
		codeDirAbsPath = workflowAbsPath(codeDirRelPath)
		mainPyAbsPath = filepath.Join(codeDirAbsPath, "main.py")
		if err := createFolderViaAPI(ctx, codeDirRelPath); err != nil {
			return &ScriptedFastPathResult{RanScript: true, Error: err.Error(), ExitCode: -1}
		}
		files := shared.crewScript.Files
		if files == nil {
			return &ScriptedFastPathResult{RanScript: true, Error: "saved script source was not resolved", ExitCode: -1}
		}
		if err := hcpo.materializeScriptBundle(ctx, codeDirRelPath, files); err != nil {
			return &ScriptedFastPathResult{RanScript: true, Error: err.Error(), ExitCode: -1}
		}
		existingScript = string(files["main.py"])
		if existingScript == "" {
			return &ScriptedFastPathResult{RanScript: true, Error: "saved script main.py is unavailable", ExitCode: -1}
		}
	}

	inputArgs := hcpo.buildScriptedInputArgs(ctx, step, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, hcpo.variableValues)

	// Saved-script fast path bypasses execution-agent startup, so prime browser-capable
	// MCP server configs here to preserve the same stateful MCP session and runtime overrides.
	// behavior as a normal workflow run.
	// Without this, the first browser tool call can fall through to default mcpcache
	// creation and lose run-specific settings like Downloads/output-dir.

	// Clean previous output files before running saved script — fresh slate for this execution.
	// Preserve code/ containing the saved-source bundle.
	hcpo.cleanStepOutputDir(ctx, stepExecutionRelPath, "code")
	if hasShared {
		// Direct chat and workflow invocations record the same resolved inputs and
		// executed definition beside their own copied source bundle.
		call, _ := scriptedDelegationFromContext(ctx)
		inputs, _ := json.MarshalIndent(call.Parameters, "", "  ")
		definition, _ := json.MarshalIndent(shared.crewScript.Definition, "", "  ")
		files := map[string][]byte{"code/script.json": definition}
		if hcpo.directCrewScript == nil {
			files["inputs.json"] = inputs
		}
		for file, content := range files {
			if err := hcpo.WriteWorkspaceFile(ctx, stepExecutionRelPath+"/"+file, string(content)); err != nil {
				return &ScriptedFastPathResult{RanScript: true, Error: err.Error(), ExitCode: -1}
			}
		}
		// Hash the actual frozen bytes, including the resolved script.json,
		// before the program or any of its agents can read the run metadata.
		if err := hcpo.recordScriptSourceSnapshot(ctx, shared.crewScript.Definition.ID, codeDirRelPath); err != nil {
			return &ScriptedFastPathResult{RanScript: true, Error: err.Error(), ExitCode: -1}
		}
	}

	// Always copy saved script from learnings/ to execution/code/.
	// learnings/{step-id}/main.py is the source of truth — execution/code/ is a disposable
	// working copy. If the user or workflow builder patches learnings, the next run must
	// pick up that explicit edit; execution never copies source back.
	if !hasShared && !hcpo.usesCodeTree() {
		if mkErr := createFolderViaAPI(ctx, codeDirRelPath); mkErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to create code/ dir for saved script copy: %v", mkErr))
		}
		learnFiles, listErr := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, learnDirRelPath)
		if listErr != nil {
			// Fallback: copy just main.py
			if writeErr := hcpo.WriteWorkspaceFile(ctx, codeDirRelPath+"/main.py", existingScript); writeErr != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to copy main.py to code/: %v", writeErr))
			}
		} else {
			for _, f := range learnFiles {
				fileName := filepath.Base(f)
				if fileName == "" || fileName == "." || fileName == "script_metadata.json" || fileName == ".learning_metadata.json" || strings.HasSuffix(fileName, ".md") {
					continue // skip metadata and legacy note files; only copy script artifacts
				}
				content, readErr := hcpo.ReadWorkspaceFile(ctx, learnDirRelPath+"/"+fileName)
				if readErr != nil {
					continue
				}
				if writeErr := hcpo.WriteWorkspaceFile(ctx, codeDirRelPath+"/"+fileName, content); writeErr != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to copy %s to code/: %v", fileName, writeErr))
				}
			}
		}
		hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Copied saved script from learnings/%s/ to %s/", stepID, codeDirRelPath))
	}

	// Static code review before execution — catch anti-patterns that would fail on reuse
	codeIssues := reviewMainPyScript(existingScript)
	if len(codeIssues) > 0 {
		hcpo.GetLogger().Warn(fmt.Sprintf("🔍 [review-code] Saved script for step %d (%s) has %d issue(s) — script execution refused", stepIndex+1, stepID, len(codeIssues)))
		var issueReport strings.Builder
		issueReport.WriteString("Static code review found issues that must be fixed:\n")
		for idx, issue := range codeIssues {
			issueReport.WriteString(fmt.Sprintf("%d. %s\n", idx+1, issue))
		}
		return &ScriptedFastPathResult{
			RanScript:      true,
			Success:        false,
			ExitCode:       -1,
			Error:          issueReport.String(),
			ExistingScript: existingScript,
			FailureReason:  "execution_error",
		}
	} else {
		hcpo.GetLogger().Info(fmt.Sprintf("✅ [review-code] Saved script for step %d (%s) passed static analysis", stepIndex+1, stepID))
	}

	// Run from execution/code/ — same location the LLM writes to
	scriptStartTime := time.Now()
	output, exitCode, execErr := hcpo.execScriptedScript(ctx, step, stepIndex, stepPath, mainPyAbsPath, inputArgs, stepExecutionAbsPath, codeDirAbsPath, stepExecutionRelPath, dbAccess)
	scriptDurationMs := time.Since(scriptStartTime).Milliseconds()

	// Helper to build a RunRecord with common fields pre-filled
	buildRunRecord := func(success bool, failureReason, errSummary string) RunRecord {
		return RunRecord{
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Success:       success,
			ExitCode:      exitCode,
			DurationMs:    scriptDurationMs,
			GroupName:     hcpo.currentGroupName,
			RunFolder:     hcpo.selectedRunFolder,
			FailureReason: failureReason,
			ErrorSummary:  errSummary,
		}
	}

	// A harness timeout is a run, but not evidence that the workflow code is
	// faulty. Preserve its partial output and abort before validation/statistics
	// or the LLM repair path can rewrite the script.
	if errors.Is(execErr, ErrScriptedHarnessTimeout) {
		hcpo.GetLogger().Error(fmt.Sprintf(
			"⏱️ [scripted] main.py started for step %d (%s) but exceeded the workspace harness timeout",
			stepIndex+1, stepID), execErr)
		return &ScriptedFastPathResult{
			RanScript:      true,
			HarnessTimeout: true,
			TimeoutError:   execErr.Error(),
			ExitCode:       exitCode,
			Output:         output,
			ExistingScript: existingScript,
		}
	}

	// Missing execution evidence is not a script defect. Return before
	// validation/statistics/repair; an interrupted request may have done work.
	if errors.Is(execErr, ErrScriptedHarnessRejection) {
		hcpo.GetLogger().Error(fmt.Sprintf(
			"[scripted] Workspace request failed for step %d (%s); execution may have started, outcome unknown",
			stepIndex+1, stepID), execErr)
		return &ScriptedFastPathResult{
			HarnessFailure: true,
			HarnessError:   execErr.Error(),
			ExitCode:       -1,
			ExistingScript: existingScript,
		}
	}

	if execErr != nil || exitCode != 0 {
		var execErrMsg string
		if execErr != nil {
			execErrMsg = fmt.Sprintf("execution error: %v\n%s", execErr, output)
		} else {
			execErrMsg = fmt.Sprintf("exit code %d:\n%s", exitCode, output)
		}
		// Collect output diagnostics, but valid artifacts cannot override a failed process.
		preValResults, _ := RunPreValidation(ctx, getValidationSchema(step), stepExecutionRelPath, hcpo.BaseOrchestrator)
		if preValResults != nil {
			hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, false, preValResults)
			if hcpo.selectedRunFolder != "" {
				preValLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
				SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValLogPath, step.GetID(), stepPath, preValResults, getValidationSchema(step), hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
					PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "saved-script", ExecutionAttempt: 1, ValidationAttempt: 1})
			}
		}
		validationErrMsg := ""
		failureReason := "execution_error"
		if preValResults != nil && !preValResults.OverallPass {
			validationErrMsg = formatWorkspaceResults(preValResults)
			failureReason = "execution_and_validation_error"
		}
		errMsg := execErrMsg
		if validationErrMsg != "" {
			errMsg = fmt.Sprintf("%s\n\n%s", execErrMsg, validationErrMsg)
		}
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Script failed for step %d: %s", stepIndex+1, errMsg))
		hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(false, failureReason, errMsg))
		return &ScriptedFastPathResult{
			RanScript:       true,
			Success:         false,
			ExitCode:        exitCode,
			Output:          output,
			Error:           errMsg,
			ExecutionError:  execErrMsg,
			ValidationError: validationErrMsg,
			FailureReason:   failureReason,
			ExistingScript:  existingScript,
		}
	}

	// Script exited 0 — run pre-validation to confirm output structure
	preValResults, preValErr := RunPreValidation(ctx, getValidationSchema(step), stepExecutionRelPath, hcpo.BaseOrchestrator)
	if preValErr != nil {
		return &ScriptedFastPathResult{
			RanScript: true, ExitCode: 0, Output: output,
			Error:           fmt.Sprintf("output validation could not run: %v", preValErr),
			ValidationError: preValErr.Error(), FailureReason: "validation_error",
			ExistingScript: existingScript,
		}
	}
	if preValResults != nil {
		hcpo.emitPreValidationCompletedEvent(ctx, step, stepIndex, stepPath, false, preValResults)
		if hcpo.selectedRunFolder != "" {
			preValLogPath := fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
			SavePreValidationLog(ctx, hcpo.BaseOrchestrator, preValLogPath, step.GetID(), stepPath, preValResults, getValidationSchema(step), hcpo.GetWorkspacePath(), hcpo.selectedRunFolder, hcpo.currentGroupName,
				PreValidationAttempt{ExecutionMode: "scripted", ValidationPhase: "saved-script", ExecutionAttempt: 1, ValidationAttempt: 1})
		}
	}
	if preValResults != nil && !preValResults.OverallPass {
		errMsg := formatWorkspaceResults(preValResults)
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Script ran but output validation failed for step %d", stepIndex+1))
		hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(false, "validation_error", errMsg))
		return &ScriptedFastPathResult{
			RanScript:       true,
			Success:         false,
			ExitCode:        0,
			Output:          output,
			Error:           errMsg,
			ValidationError: errMsg,
			FailureReason:   "validation_error",
			ExistingScript:  existingScript,
		}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] Script executed and validated for step %d (%s) — 0 LLM tokens used", stepIndex+1, stepID))
	hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(true, "", ""))
	return &ScriptedFastPathResult{RanScript: true, Success: true, Output: output}
}

// updateScriptedRunStats increments run counters and appends rich run data to script_metadata.json.
func (hcpo *StepBasedWorkflowOrchestrator) updateScriptedRunStats(ctx context.Context, stepID string, record RunRecord) {
	meta := hcpo.readScriptedMetadataAPI(ctx, stepID)
	if meta == nil {
		if !hcpo.usesCodeTree() {
			return
		}
		meta = &ScriptedMetadata{StepID: stepID, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	if hcpo.usesCodeTree() {
		if source, err := hcpo.ReadWorkspaceFile(ctx, hcpo.scriptedSourceDir(stepID)+"/main.py"); err == nil {
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
			if hash != meta.EntryPointHash {
				meta.ScriptVersion++
				meta.EntryPointHash = hash
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	meta.TotalRuns++
	meta.LastRunAt = now
	if record.Success {
		if meta.SuccessfulRuns == nil {
			meta.SuccessfulRuns = map[string]int{}
		}
		meta.SuccessfulRuns["scripted"]++
	} else {
		meta.FailedRuns++
	}

	// Fill in timestamp if not set
	if record.Timestamp == "" {
		record.Timestamp = now
	}

	// Append to recent runs (cap at 10)
	meta.RecentRuns = append(meta.RecentRuns, record)
	if len(meta.RecentRuns) > 10 {
		meta.RecentRuns = meta.RecentRuns[len(meta.RecentRuns)-10:]
	}

	// Update per-group stats
	if record.GroupName != "" {
		if meta.GroupStats == nil {
			meta.GroupStats = map[string]GroupStat{}
		}
		gs := meta.GroupStats[record.GroupName]
		gs.Runs++
		if record.Success {
			gs.Successes++
		} else {
			gs.Failures++
		}
		meta.GroupStats[record.GroupName] = gs
	}

	// Update duration stats
	if record.DurationMs > 0 {
		if meta.DurationStats == nil {
			meta.DurationStats = &DurationStats{MinMs: record.DurationMs}
		}
		ds := meta.DurationStats
		ds.LastMs = record.DurationMs
		ds.Count++
		if record.DurationMs < ds.MinMs || ds.MinMs == 0 {
			ds.MinMs = record.DurationMs
		}
		if record.DurationMs > ds.MaxMs {
			ds.MaxMs = record.DurationMs
		}
		// Running average
		ds.AvgMs = ((ds.AvgMs * int64(ds.Count-1)) + record.DurationMs) / int64(ds.Count)
	}

	// Update last failure
	if !record.Success {
		meta.LastFailure = &LastFailureInfo{
			Timestamp:    record.Timestamp,
			Reason:       record.FailureReason,
			ErrorSnippet: truncateStr(record.ErrorSummary, 2000),
			GroupName:    record.GroupName,
			ExitCode:     record.ExitCode,
		}
	}

	// Update streak
	if meta.CurrentStreak == nil {
		meta.CurrentStreak = &StreakInfo{}
	}
	if record.Success {
		if meta.CurrentStreak.Type == "success" {
			meta.CurrentStreak.Count++
		} else {
			meta.CurrentStreak = &StreakInfo{Type: "success", Count: 1}
		}
	} else {
		if meta.CurrentStreak.Type == "failure" {
			meta.CurrentStreak.Count++
		} else {
			meta.CurrentStreak = &StreakInfo{Type: "failure", Count: 1}
		}
	}

	_ = hcpo.writeScriptedMetadataAPI(ctx, stepID, *meta)
}

// truncateStr returns at most maxLen characters from s.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// saveScriptedFastPathLog writes a JSON execution log to the standard logs directory
// so that debug_step and direct file inspection can see the fast-path script run.
func (hcpo *StepBasedWorkflowOrchestrator) saveScriptedFastPathLog(
	ctx context.Context,
	stepIndex int,
	stepID string,
	stepPath string,
	scriptPath string,
	result *ScriptedFastPathResult,
) {
	if result == nil {
		return
	}
	logDir := getExecutionFolderPathForLogs(func() string {
		if hcpo.selectedRunFolder != "" {
			return fmt.Sprintf("%s/runs/%s", hcpo.GetWorkspacePath(), hcpo.selectedRunFolder)
		}
		return hcpo.GetWorkspacePath()
	}(), stepID, stepPath)

	logData := map[string]interface{}{
		"log_file":         result.LogFile,
		"log_error":        result.LogError,
		"log_complete":     result.LogComplete,
		"output_bytes":     result.OutputBytes,
		"output_truncated": result.OutputTruncated,
		"step_index":       stepIndex + 1,
		"step_path":        stepPath,
		"mode":             "scripted_fast_path",
		"script_path":      scriptPath,
		"exit_code":        result.ExitCode,
		"success":          result.Success,
		"output":           result.Output,
		"error":            result.Error,
		"execution_error":  result.ExecutionError,
		"validation_error": result.ValidationError,
		"failure_reason":   result.FailureReason,
		"timestamp":        time.Now().Format(time.RFC3339),
	}
	if delegation, ok := scriptedDelegationFromContext(ctx); ok && len(delegation.Parameters) > 0 {
		// Record names for diagnosis without copying potentially sensitive runtime
		// values into durable logs. Secrets belong in SECRET_* variables, never in
		// script parameters, but this remains deliberately fail-safe.
		names := make([]string, 0, len(delegation.Parameters))
		for name := range delegation.Parameters {
			names = append(names, name)
		}
		sort.Strings(names)
		logData["script_parameter_names"] = names
	}
	if logJSON, err := json.MarshalIndent(logData, "", "  "); err == nil {
		// Preserve every attempt; the legacy name remains a latest-result alias.
		attemptPath := fmt.Sprintf("%s/scripted-%d.json", logDir, time.Now().UnixNano())
		if err := hcpo.WriteWorkspaceFile(context.Background(), attemptPath, string(logJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("Failed to preserve scripted attempt: %v", err))
		}
		logPath := logDir + "/scripted_fast_path.json"
		if err := hcpo.WriteWorkspaceFile(context.Background(), logPath, string(logJSON)); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to save fast path log: %v", err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Saved fast path execution log: %s", logPath))
		}
	}
}
