package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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
	docsRoot := GetPromptDocsRoot()
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
			absPath = filepath.Join(docsRoot, entryRelPath)
		} else {
			absPath = filepath.Join(docsRoot, workspacePath, entryRelPath)
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
	ScriptVersion  int            `json:"script_version"`             // incremented each time the LLM rewrites the script
	EntryPointHash string         `json:"entry_point_hash,omitempty"` // canonical entry-point revision; helpers remain shared source
	CreatedAt      string         `json:"created_at"`
	LastRunAt      string         `json:"last_run_at"`
	TotalRuns      int            `json:"total_runs"`
	SuccessfulRuns map[string]int `json:"successful_runs"` // canonical key is "scripted"; legacy aliases are normalized on read
	FailedRuns     int            `json:"failed_runs"`
	RelearnCount   int            `json:"relearn_count"` // how many times the LLM had to rewrite

	// Rich run tracking (added for debugging & optimization decisions)
	RecentRuns    []RunRecord          `json:"recent_runs,omitempty"`     // last N runs with full details (capped at 10)
	GroupStats    map[string]GroupStat `json:"group_stats,omitempty"`     // per-group success/failure counts
	DurationStats *DurationStats       `json:"duration_stats,omitempty"`  // execution time statistics
	LastFailure   *LastFailureInfo     `json:"last_failure,omitempty"`    // details of most recent failure
	CurrentStreak *StreakInfo          `json:"current_streak,omitempty"`  // consecutive success/failure streak
	LockCodeStats *LockCodeStats       `json:"lock_code_stats,omitempty"` // failures while main.py is locked

	// TODO: Auto lock_code tracking — auto-unlock when main.py changes (hash mismatch),
	// auto-lock after N consecutive successes across M+ groups.
}

// LockCodeStats tracks how the locked main.py is performing since lock_code was set.
// Distinct from FailedRuns/CurrentStreak (which span the script's whole lifetime): a
// stable script that ran successfully for months before being locked, then started
// failing after a site change, would show 0 ConsecutiveFailures at first — the counter
// only ticks while lock_code=true, so the builder gets a clean signal of "is the frozen
// script still working?" without having to diff against an unknown lock-time baseline.
type LockCodeStats struct {
	LockedSince         string `json:"locked_since,omitempty"`        // first observed run while lock_code=true
	Failures            int    `json:"failures"`                      // runs failed while locked
	Successes           int    `json:"successes"`                     // runs succeeded while locked
	ConsecutiveFailures int    `json:"consecutive_failures"`          // current run of locked failures; resets on success or unlock
	LastLockedFailure   string `json:"last_locked_failure,omitempty"` // timestamp of most recent locked failure
	NeedsReview         bool   `json:"needs_review,omitempty"`        // derived: ConsecutiveFailures >= 3 → builder should consider unlocking + patching
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

// ErrScriptedHarnessRejection means the workspace API refused the execute
// request, so main.py never started and produced no output at all.
//
// This is deliberately a distinct failure kind rather than "the script failed",
// because the two demand opposite responses. A failed script is the relearn
// path's whole purpose: the step agent is handed the source plus the error and
// told to "fix the bug and rewrite it", and its rewrite is then saved back as
// the canonical script. Feed a harness rejection into that path and the agent
// is asked to explain an error its code did not cause, against a script it
// cannot observe running. It complies, because the prompt presumes the script
// is at fault and offers no branch for "the error is not yours".
//
// That is not hypothetical. One workflow concluded DB_PATH was missing; another
// concluded Python's sqlite3 could not open the database and wrote a permanent
// learning to shell out to the sqlite3 CLI instead. Neither script had executed
// a line, and both rewrites were persisted over working code.
//
// So a rejection aborts the step instead. It is infrastructure being down, and
// it surfaces through the ordinary step-failure notification the operator
// already watches — not as a workflow concern, which Pulse Gate reads to choose
// reviewers that could do nothing about it.
var ErrScriptedHarnessRejection = errors.New("workspace refused to start the script")

// ErrScriptedHarnessTimeout means main.py started but the outer workspace
// harness killed it after its command deadline. This is not a startup refusal
// and must not enter the code-repair loop.
var ErrScriptedHarnessTimeout = errors.New("script exceeded the workspace harness timeout")

// ScriptedTerminalRefusalExitCode is the reserved exit code a scripted step's
// main.py uses to signal a DELIBERATE, terminal refusal to proceed -- e.g. a
// fail-closed data-safety guard that detected an unsafe write and correctly
// aborted -- as distinct from an ordinary bug (any other non-zero exit code).
//
// Without this, decideScriptedFastPath could not tell the two apart: "the
// fallback is the feature" (see its own doc comment) treats every non-zero
// exit identically, handing the LLM the script and its error to relearn from.
// That is correct for a genuine bug, but actively dangerous for a deliberate
// safety refusal -- confirmed live on HDFC-Personal-Accounts: a BR5-01
// fail-closed guard refused to overwrite bank balance history it could not
// verify was current, sys.exit(1)'d with an explicit ABORT message, and the
// LLM fallback read that refusal, agreed it was correct, and then performed
// the exact write and database mutation the script had refused to do.
//
// A script opts into this by returning sys.exit(2) specifically; any other
// non-zero code (including the still-conventional sys.exit(1)) keeps today's
// relearn-fallback behavior unchanged. Code 2 alone is not proof a safety
// guard fired: scripts and command-line parsers may use it for other errors.
// Keep the stop behavior, and describe intent only when explicitly reported.
const ScriptedTerminalRefusalExitCode = 2

// ScriptedFastPathResult is returned by tryRunSavedScriptedScript.
type ScriptedFastPathResult struct {
	RanScript       bool   // true if a saved script was found and attempted
	Success         bool   // true if script ran and validation passed
	ExitCode        int    // actual Python exit code (0 = success, -1 = not-run/API error)
	Output          string // combined stdout from script
	Error           string // stderr / validation error (used as relearn context for LLM)
	ExecutionError  string // raw script execution failure, if any
	ValidationError string // output/pre-validation failure, if any
	FailureReason   string // "execution_error", "validation_error", or "execution_and_validation_error"
	ExistingScript  string // old script content (for LLM relearn prompt)
	// HarnessFailure means the script never started (ErrScriptedHarnessRejection).
	// Mutually exclusive with RanScript: nothing was executed, so there is no
	// exit code, no output, and nothing for the LLM to repair.
	HarnessFailure bool
	HarnessError   string
	// HarnessTimeout means the process started and may have emitted partial
	// output, but the outer workspace deadline killed it. It is terminal for
	// this attempt and must not be presented to the LLM as a code bug.
	HarnessTimeout bool
	TimeoutError   string
	// TerminalRefusal means the script ran and exited
	// ScriptedTerminalRefusalExitCode. This stops repair conservatively; intent
	// is unverified without explicit refusal details. Like HarnessFailure,
	// this must never reach the LLM relearn path --
	// handing an agent "here is a refusal, fix it" is exactly how the refusal
	// gets overridden instead of respected.
	TerminalRefusal       bool
	TerminalRefusalReason string
}

// ScriptedFastPathDecision is what the saved-script attempt means for the rest
// of the step: either the script did the whole job, or the LLM takes over and
// needs the script (and any error) as repair context.
type ScriptedFastPathDecision struct {
	// FastPathDone means the saved script ran AND validated — the LLM is
	// skipped entirely for this step (the 0-token path).
	FastPathDone bool
	// PriorScript is the saved script handed to the LLM so it repairs/adapts
	// the existing code instead of rewriting from scratch.
	PriorScript string
	// PriorError is the failure the saved script produced. Non-empty ONLY when
	// a script actually ran and failed — an untried script is a reuse case, not
	// a failure, and must not be presented to the model as one.
	PriorError string
	// HarnessFailure aborts the step: the runtime refused to start the script,
	// so neither the fast path nor the LLM fallback has anything to work with.
	HarnessFailure bool
	HarnessError   string
	HarnessTimeout bool
	TimeoutError   string
	// TerminalRefusal aborts the step: the script deliberately exited
	// ScriptedTerminalRefusalExitCode rather than proceed. Unlike PriorError,
	// this must never be handed to the LLM as something to fix -- see
	// ScriptedTerminalRefusalExitCode's doc comment for why.
	TerminalRefusal       bool
	TerminalRefusalReason string
}

// decideScriptedFastPath maps a saved-script attempt onto the step's next move.
//
// Extracted as a pure function because the fallback is the feature: when a
// scripted step's main.py fails, the run must NOT fail — it must fall back to
// the LLM carrying the broken script and its error so the model can fix it.
// Inline, that behavior was three easily-transposed branches with no test
// coverage at all.
func decideScriptedFastPath(result *ScriptedFastPathResult) ScriptedFastPathDecision {
	if result == nil {
		return ScriptedFastPathDecision{}
	}
	switch {
	case result.HarnessTimeout:
		return ScriptedFastPathDecision{HarnessTimeout: true, TimeoutError: result.TimeoutError}
	case result.HarnessFailure:
		// The script never started. Handing the LLM a script and an error it did
		// not cause is how working code gets rewritten — abort instead.
		return ScriptedFastPathDecision{HarnessFailure: true, HarnessError: result.HarnessError}
	case result.TerminalRefusal:
		// The script deliberately refused to proceed. Handing this to the LLM
		// as "here is an error, fix it" is how the refusal gets overridden
		// instead of respected — abort instead, exactly like HarnessFailure.
		return ScriptedFastPathDecision{TerminalRefusal: true, TerminalRefusalReason: result.TerminalRefusalReason}
	case result.RanScript && result.Success:
		// Saved script executed and validated — skip the LLM entirely.
		return ScriptedFastPathDecision{FastPathDone: true}
	case result.RanScript:
		// Ran but failed: hand the LLM the script AND the error to relearn from.
		priorError := result.Error
		if result.Output != "" && !strings.Contains(priorError, result.Output) {
			priorError += "\n\nScript stdout/stderr:\n" + result.Output
		}
		return ScriptedFastPathDecision{
			PriorScript: result.ExistingScript,
			PriorError:  priorError,
		}
	case result.ExistingScript != "":
		// A saved script exists but was never executed. Reuse/update path —
		// deliberately no PriorError, since nothing failed.
		return ScriptedFastPathDecision{PriorScript: result.ExistingScript}
	default:
		// No saved script at all — the LLM writes one from scratch.
		return ScriptedFastPathDecision{}
	}
}

type learnCodeSelfRunInfo struct {
	Output   string
	ExitCode int
}

func parseExecuteShellCommandArgs(argsJSON string) string {
	if strings.TrimSpace(argsJSON) == "" {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Command)
}

func parseExecuteShellCommandResult(result string) (stdout, stderr string, exitCode int, ok bool) {
	if strings.TrimSpace(result) == "" {
		return "", "", 0, false
	}
	var parsed struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		return "", "", 0, false
	}
	return parsed.Stdout, parsed.Stderr, parsed.ExitCode, true
}

func isReadOnlyMainPyCommand(command, mainPyAbsPath string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" || !strings.Contains(cmd, mainPyAbsPath) {
		return false
	}
	readPrefixes := []string{
		"cat " + shellQuotePath(mainPyAbsPath),
		"cat " + mainPyAbsPath,
		"sed -n ",
		"nl -ba ",
		"ls ",
		"stat ",
		"wc ",
		"head ",
		"tail ",
	}
	for _, prefix := range readPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return true
		}
	}
	return false
}

func detectSuccessfulLLMScriptedSelfRun(history []llmtypes.MessageContent, mainPyAbsPath string) *learnCodeSelfRunInfo {
	type shellCall struct {
		command string
		index   int
	}
	pendingShellCalls := map[string]shellCall{}
	lastSuccessfulRun := -1
	lastSuccessfulOutput := ""
	lastSuccessfulExitCode := 0
	lastMainPyMutation := -1
	callIndex := 0

	for _, msg := range history {
		switch msg.Role {
		case llmtypes.ChatMessageTypeAI:
			for _, part := range msg.Parts {
				toolCall, ok := part.(llmtypes.ToolCall)
				if !ok || toolCall.FunctionCall == nil {
					continue
				}
				callIndex++
				switch toolCall.FunctionCall.Name {
				case "execute_shell_command", "mcp_api-bridge_execute_shell_command":
					command := parseExecuteShellCommandArgs(toolCall.FunctionCall.Arguments)
					pendingShellCalls[toolCall.ID] = shellCall{command: command, index: callIndex}
					if strings.Contains(command, mainPyAbsPath) &&
						!strings.Contains(command, "python3 "+shellQuotePath(mainPyAbsPath)) &&
						!strings.Contains(command, "python3 "+mainPyAbsPath) &&
						!isReadOnlyMainPyCommand(command, mainPyAbsPath) {
						lastMainPyMutation = callIndex
					}
				case "diff_patch_workspace_file", "mcp_api-bridge_diff_patch_workspace_file":
					// A multi-file "*** Begin Patch" names main.py inside the diff.
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &args); err == nil {
						for _, target := range workspace.DiffPatchTargetPaths(args) {
							if strings.TrimSpace(target) == mainPyAbsPath {
								lastMainPyMutation = callIndex
							}
						}
					}
				}
			}
		case llmtypes.ChatMessageTypeTool:
			for _, part := range msg.Parts {
				toolResp, ok := part.(llmtypes.ToolCallResponse)
				if !ok {
					continue
				}
				call, exists := pendingShellCalls[toolResp.ToolCallID]
				if !exists {
					continue
				}
				stdout, stderr, exitCode, ok := parseExecuteShellCommandResult(toolResp.Content)
				if !ok {
					continue
				}
				if exitCode == 0 &&
					(strings.Contains(call.command, "python3 "+shellQuotePath(mainPyAbsPath)) ||
						strings.Contains(call.command, "python3 "+mainPyAbsPath)) {
					lastSuccessfulRun = call.index
					lastSuccessfulExitCode = exitCode
					lastSuccessfulOutput = strings.TrimSpace(stdout)
					if strings.TrimSpace(stderr) != "" {
						if lastSuccessfulOutput != "" {
							lastSuccessfulOutput += "\n"
						}
						lastSuccessfulOutput += strings.TrimSpace(stderr)
					}
				}
			}
		}
	}

	if lastSuccessfulRun == -1 || lastMainPyMutation > lastSuccessfulRun {
		return nil
	}
	return &learnCodeSelfRunInfo{
		Output:   lastSuccessfulOutput,
		ExitCode: lastSuccessfulExitCode,
	}
}

// extractLastMainPyRunOutput returns the output from the last execution of main.py
// in the conversation history, regardless of exit code. This is used to provide
// execution context to repair agents that start with a fresh conversation.
func extractLastMainPyRunOutput(history []llmtypes.MessageContent, mainPyAbsPath string) (output string, exitCode int, found bool) {
	type shellCall struct {
		command string
	}
	pendingShellCalls := map[string]shellCall{}
	var lastOutput string
	var lastExitCode int
	lastFound := false

	for _, msg := range history {
		switch msg.Role {
		case llmtypes.ChatMessageTypeAI:
			for _, part := range msg.Parts {
				toolCall, ok := part.(llmtypes.ToolCall)
				if !ok || toolCall.FunctionCall == nil {
					continue
				}
				switch toolCall.FunctionCall.Name {
				case "execute_shell_command", "mcp_api-bridge_execute_shell_command":
					command := parseExecuteShellCommandArgs(toolCall.FunctionCall.Arguments)
					pendingShellCalls[toolCall.ID] = shellCall{command: command}
				}
			}
		case llmtypes.ChatMessageTypeTool:
			for _, part := range msg.Parts {
				toolResp, ok := part.(llmtypes.ToolCallResponse)
				if !ok {
					continue
				}
				call, exists := pendingShellCalls[toolResp.ToolCallID]
				if !exists {
					continue
				}
				if strings.Contains(call.command, "python3 "+shellQuotePath(mainPyAbsPath)) ||
					strings.Contains(call.command, "python3 "+mainPyAbsPath) {
					stdout, stderr, ec, ok := parseExecuteShellCommandResult(toolResp.Content)
					if !ok {
						continue
					}
					combined := strings.TrimSpace(stdout)
					if strings.TrimSpace(stderr) != "" {
						if combined != "" {
							combined += "\n"
						}
						combined += strings.TrimSpace(stderr)
					}
					lastOutput = combined
					lastExitCode = ec
					lastFound = true
				}
			}
		}
	}

	return lastOutput, lastExitCode, lastFound
}

// reviewMainPyScript performs static analysis on a main.py script to catch common
// anti-patterns that would cause failures when the script is reused across groups.
// declaredEnvVars is the list of env var names actually available to the script (e.g. VAR_USER_ID, SECRET_PASSWORD).
// If nil, the VAR_*/SECRET_* check is skipped (can't distinguish declared vs invented vars).
// Returns a list of human-readable issues. Empty list means the script looks clean.
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
	relPath := hcpo.scriptedSourceDir(stepID) + "/script_metadata.json"
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
		RelearnCount   int    `json:"relearn_count"`
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
		RelearnCount:   legacy.RelearnCount,
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

func scriptArtifactsChanged(before, after map[string]string) bool {
	if len(before) != len(after) {
		return true
	}
	for name, oldContent := range before {
		if newContent, ok := after[name]; !ok || newContent != oldContent {
			return true
		}
	}
	return false
}

// writeScriptedMetadataAPI writes script_metadata.json via the workspace API.
func (hcpo *StepBasedWorkflowOrchestrator) writeScriptedMetadataAPI(ctx context.Context, stepID string, meta ScriptedMetadata) error {
	relPath := hcpo.scriptedSourceDir(stepID) + "/script_metadata.json"
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

// buildScriptedVarMappingForPrompt returns a newline-joined list showing how workflow
// variables map to env vars, e.g. "{{TARGET_USER}} → os.environ['SECRET_TARGET_USER']".
// Returns "" when scripted mode is disabled or there are no variables.
func buildScriptedVarMappingForPrompt(isScriptedMode bool, manifest *VariablesManifest) string {
	if !isScriptedMode || manifest == nil || len(manifest.Variables) == 0 {
		return ""
	}
	lines := make([]string, 0, len(manifest.Variables))
	for _, v := range manifest.Variables {
		lines = append(lines, fmt.Sprintf("{{%s}} → os.environ['VAR_%s']", v.Name, v.Name))
	}
	return strings.Join(lines, "\n")
}

// buildScriptedEnvVarNamesForPrompt returns newline-joined env var names for templateVars.
// Returns "" when scripted mode is disabled.
func buildScriptedEnvVarNamesForPrompt(isScriptedMode bool, workspaceEnvRef map[string]string) string {
	if !isScriptedMode {
		return ""
	}
	// Every name the runtime actually injects must appear here. execScriptedScript
	// sets STEP_OUTPUT_DIR, STEP_EXECUTION_DIR and DB_PATH alongside the workspace
	// env, but this list used to omit the last two — so a script author was told the
	// database path did not exist while the runtime was handing it over.
	//
	// That mismatch is expensive, not cosmetic. An author reading this list finds no
	// DB_PATH, concludes the db is unreachable, and works around it: one workflow
	// removed the required-env read and derived the path from STEP_EXECUTION_DIR,
	// another shelled out to the sqlite3 CLI and wrote that into its permanent
	// learnings. Both workarounds are explicitly forbidden by the stores contract,
	// which insists steps use $DB_PATH and report an open failure as a runtime bug
	// rather than routing around it. The framework was contradicting itself.
	fixed := []string{"STEP_OUTPUT_DIR", "STEP_EXECUTION_DIR", "DB_PATH", "RUN_FOLDER", "MCP_API_URL"}
	seen := make(map[string]bool, len(fixed)+len(workspaceEnvRef))
	for _, n := range fixed {
		seen[n] = true
	}
	names := append([]string{}, fixed...)
	for k := range workspaceEnvRef {
		if seen[k] {
			continue // already declared above; never list a name twice
		}
		seen[k] = true
		names = append(names, k)
	}
	sort.Strings(names[len(fixed):]) // sort only the SECRET_*/VAR_* tail
	return strings.Join(names, "\n")
}

// buildScriptedInputArgs returns absolute paths to context_dependency files as positional args.
// These become: python3 main.py <input1> <input2> ...
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
	learningsAccess := resolveExecutionLearningsAccess(stepConfig, step)

	readPaths, writePaths := hcpo.setupExecutionFolderGuard(stepPath, step.GetID(), kbAccess, learningsAccess, resolveDBAccess(stepConfig), stepConfig)
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
	readPaths = common.DeduplicateStrings(append(readPaths, writePaths...))
	if err := hcpo.materializeWorkflowGuardPaths(readPaths, writePaths); err != nil {
		return nil, err
	}

	return &workspace.FolderGuardConfig{
		Enabled:           true,
		ReadPaths:         readPaths,
		WritePaths:        writePaths,
		BlockedWritePaths: readOnlyPaths,
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
	docsRoot := GetPromptDocsRoot() // /app/workspace-docs

	// Convert absolute workDir to workspace-relative for WorkingDirectory field.
	// The workspace API prepends docsRoot, so passing the absolute path would double it.
	workDirRel := strings.TrimPrefix(workDirAbsPath, docsRoot+"/")
	if workDirRel == workDirAbsPath {
		// Didn't start with docsRoot — fall back to empty (workspace root)
		workDirRel = ""
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
			if k == "STEP_OUTPUT_DIR" || k == "STEP_EXECUTION_DIR" {
				// Never let the shared workspace env override the per-step folders.
				// These are execution-specific and can leak across concurrently
				// running steps if read from the shared env map.
				continue
			}
			extraEnv[k] = v
		}
		hcpo.UnlockWorkspaceEnv()
	}

	// DB_PATH: the scripted fast path execs python3 directly — it does NOT go
	// through the agent's execute_shell_command wrapper (injectStepEnvIntoShellExecutor)
	// that injects DB_PATH. Without it a saved main.py doing os.environ['DB_PATH']
	// fails with "DB_PATH unset and no root found". Set it here to the same absolute
	// path the agent path uses. Set AFTER the workspace-env merge so it always wins.
	extraEnv["DB_PATH"] = filepath.Join(docsRoot, hcpo.GetWorkspacePath(), DBFolderName, "db.sqlite")
	extraEnv = hcpo.codeRuntimeEnv(extraEnv)

	// RUN_FOLDER: the workspace-relative "iteration-N/<group>" segment under
	// runs/, e.g. "iteration-85/confida-staging". PLAT-185: a script that needs
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
	// Use timeout=0 so the parent workflow/sub-agent context controls cancellation.
	// Long-running scripted steps can legitimately exceed a fixed shell timeout.
	timeout := 0
	useShell := true
	reqParams := workspace.ExecuteShellCommandParams{
		Command:          command,
		WorkingDirectory: workDirRel,
		Timeout:          &timeout,
		UseShell:         &useShell,
		FolderGuard:      guard,
		ExtraEnv:         extraEnv,
		DBReadSnapshot:   dbAccess == DBAccessRead,
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
	// The script's bridge session comes from the orchestrator's own workspace
	// env (trusted), so pass it as the context session: the shell binds its
	// bridge token only to that, never to a session named in ExtraEnv.
	if sid := strings.TrimSpace(extraEnv["MCP_SESSION_ID"]); sid != "" {
		if existing, _ := ctx.Value(common.ChatSessionIDKey).(string); strings.TrimSpace(existing) == "" {
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, sid)
		}
	}
	result, err := hcpo.WorkspaceClient.ExecuteShellCommand(ctx, reqParams)
	if err != nil {
		// Transport failure or a non-2xx status: the request never became a
		// process, so this is the harness failing, not the script.
		return "", -1, fmt.Errorf("%w: %w", ErrScriptedHarnessRejection, err)
	}

	combined := result.Stdout
	if result.Stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += result.Stderr
	}
	exitCode = result.ExitCode
	if result.TimedOut {
		return combined, exitCode, fmt.Errorf("%w: %s", ErrScriptedHarnessTimeout, result.Error)
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
//   - No saved script               → RanScript=false (fall through to LLM)
//   - Script ran + validation passed → RanScript=true, Success=true
//   - Script failed                  → RanScript=true, Success=false (fall through to LLM for relearn)
func (hcpo *StepBasedWorkflowOrchestrator) tryRunSavedScriptedScript(
	ctx context.Context,
	step PlanStepInterface,
	stepIndex int,
	stepPath string,
	allSteps []PlanStepInterface,
	stepExecutionRelPath string, // workspace-relative, e.g. "Workflow/X/runs/iter-1/execution/step-2"
	executionWorkspacePath string, // workspace-relative execution root
	dbAccess string,
) *ScriptedFastPathResult {
	docsRoot := GetPromptDocsRoot()
	stepID := step.GetID()

	if !hcpo.hasValidLearnedScriptAPI(ctx, stepID) {
		scriptRelPath := hcpo.scriptedSourceDir(stepID) + "/main.py"
		existingScript, _ := hcpo.ReadWorkspaceFile(ctx, scriptRelPath)
		hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] No saved script for step %d (%s) — LLM will generate from scratch", stepIndex+1, stepID))
		return &ScriptedFastPathResult{RanScript: false, ExistingScript: existingScript}
	}

	hcpo.GetLogger().Info(fmt.Sprintf("🐍 [scripted] Executing saved script for step %d (%s) — 0 LLM tokens", stepIndex+1, stepID))

	// Read existing script content for relearn context (if script fails).
	learnDirRelPath := hcpo.scriptedSourceDir(stepID)
	scriptRelPath := learnDirRelPath + "/main.py"
	existingScript, _ := hcpo.ReadWorkspaceFile(ctx, scriptRelPath)

	stepExecutionAbsPath := filepath.Join(docsRoot, stepExecutionRelPath)
	codeDirRelPath := hcpo.scriptedWorkingDir(stepID, stepExecutionRelPath)
	codeDirAbsPath := filepath.Join(docsRoot, codeDirRelPath)
	mainPyAbsPath := filepath.Join(codeDirAbsPath, "main.py")

	inputArgs := hcpo.buildScriptedInputArgs(ctx, step, stepIndex, stepPath, allSteps, executionWorkspacePath, docsRoot, hcpo.variableValues)

	// Saved-script fast path bypasses execution-agent startup, so prime browser-capable
	// MCP server configs here to preserve the same stateful MCP session and runtime overrides.
	// behavior as a normal workflow run.
	// Without this, the first browser tool call can fall through to default mcpcache
	// creation and lose run-specific settings like Downloads/output-dir.

	// Clean previous output files before running saved script — fresh slate for this execution.
	// Preserve code/ — it may contain an LLM-fixed main.py from a previous attempt.
	hcpo.cleanStepOutputDir(ctx, stepExecutionRelPath, "code")

	// Always copy saved script from learnings/ to execution/code/.
	// learnings/{step-id}/main.py is the source of truth — execution/code/ is a disposable
	// working copy. If the user or workflow builder patches learnings, the next run must
	// pick up the change. LLM relearn fixes are saved back to learnings/ via
	// saveScriptedScriptToLearnings, so always copying from learnings is safe.
	if !hcpo.usesCodeTree() {
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
		hcpo.GetLogger().Warn(fmt.Sprintf("🔍 [review-code] Saved script for step %d (%s) has %d issue(s) — skipping fast path, falling back to LLM for fix", stepIndex+1, stepID, len(codeIssues)))
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

	// Whether main.py was locked at run time — drives LockCodeStats updates so the
	// builder can spot a frozen-but-broken script via consecutive_failures / needs_review.
	fastPathAgentCfgs := getAgentConfigs(step)
	isLocked := fastPathAgentCfgs != nil && fastPathAgentCfgs.LockCode != nil && *fastPathAgentCfgs.LockCode

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

	// A refused request is not a run. Return before pre-validation and before
	// updateScriptedRunStats: recording it as a failure would inflate
	// current_streak and lock_code_stats.consecutive_failures, which is what
	// flips needs_review and argues for unlocking a script that never ran.
	if errors.Is(execErr, ErrScriptedHarnessRejection) {
		hcpo.GetLogger().Error(fmt.Sprintf(
			"🚨 [scripted] Workspace refused to start main.py for step %d (%s) — the script did not run",
			stepIndex+1, stepID), execErr)
		return &ScriptedFastPathResult{
			HarnessFailure: true,
			HarnessError:   execErr.Error(),
			ExitCode:       -1,
			ExistingScript: existingScript,
		}
	}

	// Preserve the reserved-code stop before validation/statistics and repair.
	// Exit code 2 alone cannot prove why the script stopped; do not describe
	// it as a successful safety guard without explicit refusal evidence.
	if execErr == nil && exitCode == ScriptedTerminalRefusalExitCode {
		hcpo.GetLogger().Warn(fmt.Sprintf(
			"🛑 [scripted] Step %d (%s) main.py stopped with exit code %d — automatic repair withheld; inspect output for the reason",
			stepIndex+1, stepID, ScriptedTerminalRefusalExitCode))
		return &ScriptedFastPathResult{
			RanScript:             true,
			ExitCode:              exitCode,
			Output:                output,
			TerminalRefusal:       true,
			TerminalRefusalReason: strings.TrimSpace(output),
			ExistingScript:        existingScript,
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
		hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(false, failureReason, errMsg), isLocked)
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
		hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(false, "validation_error", errMsg), isLocked)
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
	hcpo.updateScriptedRunStats(ctx, stepID, buildRunRecord(true, "", ""), isLocked)
	return &ScriptedFastPathResult{RanScript: true, Success: true, Output: output}
}

// saveScriptedScriptToLearnings copies all files from the step's code/ subfolder to learnings/{step-id}/.
// This preserves main.py plus any helper modules the LLM wrote alongside it.
// Uses workspace API (not os.*) — Go server may not have direct filesystem access to Docker workspace.
func (hcpo *StepBasedWorkflowOrchestrator) saveScriptedScriptToLearnings(
	step PlanStepInterface,
	stepExecutionAbsPath string,
) {
	// Canonical code was repaired in place. Never copy stale execution code back.
	if hcpo.usesCodeTree() {
		return
	}
	stepID := step.GetID()
	ctx := context.Background()

	// Check if main.py exists in the code/ subfolder via workspace API.
	// stepExecutionAbsPath = /app/workspace-docs/{workspace}/{runs}/execution/step-N
	// Convert to relative path for workspace API: strip docsRoot + workspacePath prefix.
	docsRoot := GetPromptDocsRoot()
	workspacePath := hcpo.GetWorkspacePath()
	wsPrefix := filepath.Join(docsRoot, workspacePath) + "/"
	stepExecRelPath := strings.TrimPrefix(stepExecutionAbsPath, wsPrefix)
	if stepExecRelPath == stepExecutionAbsPath {
		// Couldn't strip — fall back to relative path extraction from absolute
		stepExecRelPath = strings.TrimPrefix(stepExecutionAbsPath, docsRoot+"/")
		stepExecRelPath = strings.TrimPrefix(stepExecRelPath, workspacePath+"/")
	}
	codeRelPath := stepExecRelPath + "/code"
	mainPyRelPath := codeRelPath + "/main.py"

	if _, err := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] main.py not found in code/ dir (%s) — skipping save", mainPyRelPath))
		return
	}

	learnDirRelPath := hcpo.scriptedSourceDir(stepID)

	// Snapshot old file contents from learnings/ before overwriting — used for diff debugging.
	oldFileContents := map[string]string{}
	if oldFiles, listErr := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, learnDirRelPath); listErr == nil {
		for _, f := range oldFiles {
			fn := filepath.Base(f)
			if fn == "" || fn == "." || fn == "script_metadata.json" || fn == ".learning_metadata.json" || fn == "diffs" || strings.HasSuffix(fn, ".md") {
				continue
			}
			if content, readErr := hcpo.ReadWorkspaceFile(ctx, learnDirRelPath+"/"+fn); readErr == nil {
				oldFileContents[fn] = content
			}
		}
	}

	// Delete all existing files AND folders in learnings/{step-id}/ before copying new ones.
	// Files use DELETE /api/documents/; subdirectories use DELETE /api/folders/.
	// We try folder delete first (handles "code/" subdir), then fall back to file delete.
	if existingFiles, listErr := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, learnDirRelPath); listErr == nil {
		for _, f := range existingFiles {
			fileName := filepath.Base(f)
			if fileName == "" || fileName == "." || fileName == "script_metadata.json" || fileName == ".learning_metadata.json" || fileName == "diffs" {
				continue // keep metadata and diffs/; refresh script files only
			}
			entryRelPath := learnDirRelPath + "/" + fileName
			absPath := filepath.Join(GetPromptDocsRoot(), workspacePath, entryRelPath)
			// File delete first; fall back to folder delete for subdirectories (e.g. code/).
			if fileDelErr := hcpo.DeleteWorkspaceFile(ctx, entryRelPath); fileDelErr != nil {
				if folderErr := deleteFolderViaAPI(ctx, absPath); folderErr != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to delete %s from learnings: file=%v folder=%v", fileName, fileDelErr, folderErr))
				}
			}
		}
	}

	// List files in code/ folder and copy each to learnings/{step-id}/
	newFileContents := map[string]string{}
	files, listErr := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, codeRelPath)
	if listErr != nil {
		// ListWorkspaceFiles failed — try to copy just main.py
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to list code/ dir (%s): %v — copying main.py only", codeRelPath, listErr))
		content, readErr := hcpo.ReadWorkspaceFile(ctx, mainPyRelPath)
		if readErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to read main.py: %v", readErr))
			return
		}
		if writeErr := hcpo.WriteWorkspaceFile(ctx, learnDirRelPath+"/main.py", content); writeErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to write main.py to learnings: %v", writeErr))
			return
		}
		newFileContents["main.py"] = content
	} else {
		for _, f := range files {
			if f == "" {
				continue
			}
			// f may be a relative path within codeRelPath or the full path — normalize
			fileName := filepath.Base(f)
			if fileName == "" || fileName == "." {
				continue
			}
			srcRelPath := codeRelPath + "/" + fileName
			content, readErr := hcpo.ReadWorkspaceFile(ctx, srcRelPath)
			if readErr != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to read %s: %v", fileName, readErr))
				continue
			}
			if writeErr := hcpo.WriteWorkspaceFile(ctx, learnDirRelPath+"/"+fileName, content); writeErr != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to write %s to learnings: %v", fileName, writeErr))
				continue
			}
			newFileContents[fileName] = content
		}
	}

	oldMeta := hcpo.readScriptedMetadataAPI(ctx, stepID)
	contentChanged := scriptArtifactsChanged(oldFileContents, newFileContents)
	// Start from old metadata to preserve rich run history (RecentRuns, GroupStats, etc.)
	var newMeta ScriptedMetadata
	if oldMeta != nil {
		newMeta = *oldMeta
		if contentChanged {
			newMeta.ScriptVersion = oldMeta.ScriptVersion + 1
			newMeta.RelearnCount = oldMeta.RelearnCount + 1
		}
	} else {
		newMeta.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		newMeta.ScriptVersion = 1
	}
	newMeta.StepID = stepID
	newMeta.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	if err := hcpo.writeScriptedMetadataAPI(ctx, stepID, newMeta); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to write script_metadata.json: %v", err))
	} else {
		if contentChanged || oldMeta == nil {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] Saved changed main.py to learnings for step (%s) — version %d", stepID, newMeta.ScriptVersion))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("✅ [scripted] main.py for step (%s) was byte-identical — keeping version %d", stepID, newMeta.ScriptVersion))
		}
	}

	// Save diffs between old and new script files for debugging.
	if len(oldFileContents) > 0 {
		diffsDirRelPath := learnDirRelPath + "/diffs"
		if mkErr := createFolderViaAPI(ctx, diffsDirRelPath, workspacePath); mkErr != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to create diffs/ dir: %v", mkErr))
		} else {
			for fileName, oldContent := range oldFileContents {
				newContent, readErr := hcpo.ReadWorkspaceFile(ctx, learnDirRelPath+"/"+fileName)
				if readErr != nil {
					continue
				}
				if oldContent == newContent {
					continue // no changes
				}
				diff := generateSimpleDiff(fileName, oldContent, newContent)
				diffFileName := fmt.Sprintf("%s.v%d.diff", fileName, newMeta.ScriptVersion)
				if writeErr := hcpo.WriteWorkspaceFile(ctx, diffsDirRelPath+"/"+diffFileName, diff); writeErr != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ [scripted] Failed to write diff %s: %v", diffFileName, writeErr))
				} else {
					hcpo.GetLogger().Info(fmt.Sprintf("📝 [scripted] Saved diff for %s (v%d → v%d)", fileName, newMeta.ScriptVersion-1, newMeta.ScriptVersion))
				}
			}
		}
	}
}

// generateSimpleDiff produces a unified-style diff between old and new file contents for debugging.
func generateSimpleDiff(fileName, oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", fileName, fileName))

	// Simple line-by-line diff: show removed and added lines.
	// Not a true unified diff algorithm, but sufficient for debugging.
	oldSet := make(map[string]int)
	for _, l := range oldLines {
		oldSet[l]++
	}
	newSet := make(map[string]int)
	for _, l := range newLines {
		newSet[l]++
	}

	// Show lines only in old (removed)
	for _, l := range oldLines {
		if newSet[l] > 0 {
			newSet[l]--
		} else {
			sb.WriteString(fmt.Sprintf("- %s\n", l))
		}
	}

	// Reset newSet
	newSet = make(map[string]int)
	for _, l := range newLines {
		newSet[l]++
	}
	oldSet2 := make(map[string]int)
	for _, l := range oldLines {
		oldSet2[l]++
	}

	// Show lines only in new (added)
	for _, l := range newLines {
		if oldSet2[l] > 0 {
			oldSet2[l]--
		} else {
			sb.WriteString(fmt.Sprintf("+ %s\n", l))
		}
	}

	sb.WriteString(fmt.Sprintf("\n@@ old: %d lines, new: %d lines @@\n", len(oldLines), len(newLines)))
	return sb.String()
}

// updateScriptedRunStats increments run counters and appends rich run data to script_metadata.json.
// isLocked indicates whether the step's lock_code was true at run time — when true, the run is
// also reflected in LockCodeStats so the builder can spot a frozen-but-broken script quickly.
func (hcpo *StepBasedWorkflowOrchestrator) updateScriptedRunStats(ctx context.Context, stepID string, record RunRecord, isLocked bool) {
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
				if meta.EntryPointHash != "" {
					meta.RelearnCount++
				}
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

	// Locked-script stats: only ticks while lock_code=true. Gives the builder a quick
	// "is the frozen script still working?" read without diffing TotalRuns against an
	// unknown lock-time baseline.
	if isLocked {
		if meta.LockCodeStats == nil {
			meta.LockCodeStats = &LockCodeStats{LockedSince: record.Timestamp}
		}
		if record.Success {
			meta.LockCodeStats.Successes++
			meta.LockCodeStats.ConsecutiveFailures = 0
			meta.LockCodeStats.NeedsReview = false
		} else {
			meta.LockCodeStats.Failures++
			meta.LockCodeStats.ConsecutiveFailures++
			meta.LockCodeStats.LastLockedFailure = record.Timestamp
			if meta.LockCodeStats.ConsecutiveFailures >= 3 {
				meta.LockCodeStats.NeedsReview = true
			}
		}
	} else if meta.LockCodeStats != nil {
		// Lock was cleared since last run — keep history but stop the consecutive counter
		// so an old "needs_review" flag doesn't keep firing after the user already unlocked.
		meta.LockCodeStats.ConsecutiveFailures = 0
		meta.LockCodeStats.NeedsReview = false
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

// GetScriptedModeInstructions returns system prompt additions for the persistent
// scripted-code path used by both agentic and legacy scripted steps.
// codeDirAbsPath is the LLM's working directory (execution/step-x/code/).
// stepOutputAbsPath is the step output folder (execution/step-x/) — STEP_OUTPUT_DIR env var points here.
// inputArgPaths are the absolute paths passed as sys.argv[1], sys.argv[2], ...
// envVarNames are the env var names available to the script (SECRET_*, MCP_API_URL, STEP_OUTPUT_DIR).
func GetScriptedModeInstructions(codeDirAbsPath, stepOutputAbsPath string, isRelearn bool, priorScript, priorError string, inputArgPaths []string, envVarNames []string, varMappingLines []string, validationSchemaJSON string, hasBrowser, isCodeLocked, useProjectedReferenceSkills bool, directSource ...bool) string {
	canonical := len(directSource) > 0 && directSource[0]
	var sb strings.Builder
	sb.WriteString("\n\n## Code Execution Mode\n\n")
	if isCodeLocked {
		sb.WriteString(fmt.Sprintf("You are in **code execution mode**. A saved `main.py` exists for this step but is **locked** (`lock_code=true`) — it is the source of truth and the controller will not save any rewrite you produce. The previous run executed that locked script (`%s/main.py`) and it failed validation, so this turn is a recovery turn.\n\n", codeDirAbsPath))
		sb.WriteString("**Do NOT rewrite or replace `main.py` this turn.** Instead, complete the task by calling MCP tools directly via the API step by step:\n")
		sb.WriteString("1. Read the existing `main.py` and the run-folder failure artifacts (`step_*_status.json`, screenshots, downloaded files, `scripted_fast_path.json`) to understand *why* the saved script failed.\n")
		sb.WriteString("2. Decide whether the failure is environmental (bad creds, MFA, captcha, expired session, target-site change) or a real bug in the script.\n")
		sb.WriteString(fmt.Sprintf("3. If environmental, drive the task to completion *interactively* — write outputs directly to `%s` (available as `os.environ['STEP_OUTPUT_DIR']`) using snapshots and direct tool calls so this run produces valid outputs. Do not edit the script.\n", stepOutputAbsPath))
		sb.WriteString("4. If you find a real script bug, surface it clearly in your reply so the orchestrator can clear `lock_code` and have you patch the script on a future run. Do not patch it now.\n\n")
	} else {
		if canonical {
			sb.WriteString("You are in **code execution mode**. Write or repair the canonical reusable Python solution (`main.py`). The controller executes it with the step's runtime after your turn and returns failures for repair. Do not substitute hand-made outputs for a passing script.\n\n")
		} else {
			sb.WriteString("You are in **code execution mode**. Your primary goal is to write a reusable Python solution (`main.py`). If you are unable to write a working main.py, you may fall back to calling MCP tools directly via the API to complete the task — but always prefer writing main.py since it becomes a saved script for future runs.\n\n")
		}
		sb.WriteString("**Your working directory (write all code files here):**\n")
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", codeDirAbsPath))
		sb.WriteString("**This run's working-directory rules:**\n")
		sb.WriteString(fmt.Sprintf("- Write `main.py` (the entry point) to `%s/main.py`\n", codeDirAbsPath))
		sb.WriteString(fmt.Sprintf("- You may also write helper modules (e.g. `utils.py`) to `%s/` — they will be available since the script runs with that as cwd\n", codeDirAbsPath))
		sb.WriteString(fmt.Sprintf("- Write all **output files** to `%s` (available as `os.environ['STEP_OUTPUT_DIR']`)\n", stepOutputAbsPath))
		sb.WriteString("- Before writing main.py, you may call tools via the API to inspect the current state (e.g. take a browser snapshot, check page content, read files) to understand the system before coding your solution\n")
		if !canonical {
			sb.WriteString(fmt.Sprintf("- To test, just run: `python3 '%s/main.py'` — **all env vars listed below are pre-injected; do NOT manually `export` any values**\n", codeDirAbsPath))
		}
		sb.WriteString("- After your turn, the system will run main.py and give you the error output if it fails — you'll get multiple fix attempts\n")
		sb.WriteString("- A passing `main.py` becomes the saved script for this step and will be tried first on future runs before calling the LLM again\n\n")
	}

	// Shared authoring rules — single source of truth used by this prompt, the workshop
	// builder prompt and review_step_code so all agents that write or
	// review main.py agree on what "compliant" means. Skipped when the script is locked:
	// the LLM isn't authoring or patching main.py this turn, so authoring rules would
	// only re-anchor it on writing code.
	if !isCodeLocked && !useProjectedReferenceSkills {
		sb.WriteString(BuildMainPyAuthoringRules())
	}

	// Browser authoring rules are no longer emitted here — the execution_only
	// template now injects them directly via {{.BrowserAuthoringRules}} (populated
	// from HasBrowserAccess) so both learn-code and pure code-exec browser steps
	// see the refs/selectors/probe guidance. Emitting them a second time here
	// would duplicate ~60 lines. Keep the execution-mode call-examples here since
	// they're learn-code-specific (they demonstrate the call_mcp Python wrapper
	// the learn-code prompt teaches).
	if hasBrowser && !useProjectedReferenceSkills {
		sb.WriteString("**Browser automation — execution-mode tool call examples (Python):**\n")
		sb.WriteString("- CDP mode: select/create a tab first with `command='tab'`. `command='open'` is URL-only, e.g. `args=['https://example.com']`; do not pass `['tab', 't1', url]` to open. After open, every page action must include an inline tab, e.g. `args=['tab', 't1', '-i']` for snapshot or `args=['tab', 't1', ref_parsed_from_snapshot]` for click.\n")
		sb.WriteString("- Snapshot: `call_mcp('workspace_browser', 'agent_browser', {'command': 'snapshot', 'args': ['tab', 't1', '-i'], 'session': 'main'})`\n")
		sb.WriteString("- Click with a runtime-parsed ref (never persist a literal snapshot ref): `call_mcp('workspace_browser', 'agent_browser', {'command': 'click', 'args': ['tab', 't1', ref_parsed_from_snapshot], 'session': 'main'})`\n")
		sb.WriteString("- Click with a durable selector: `call_mcp('workspace_browser', 'agent_browser', {'command': 'click', 'args': ['tab', 't1', '[aria-label=\"Sign in\"]'], 'session': 'main'})`\n")
		sb.WriteString("- Always `get_api_spec` first to see exact parameter schemas. Do NOT guess parameter names.\n\n")
	}

	// Show workflow variable → env var mapping so LLM knows exactly how to access each one
	if len(varMappingLines) > 0 {
		sb.WriteString("**Workflow variable → env var** (`VAR_*` = config values, `SECRET_*` = real credentials):\n")
		for _, line := range varMappingLines {
			sb.WriteString(fmt.Sprintf("- `%s`\n", line))
		}
		sb.WriteString("\n")
	}

	// Show exact positional arguments
	if len(inputArgPaths) > 0 {
		sb.WriteString("**Script invocation** (exact call the controller will make):\n```\n")
		sb.WriteString(fmt.Sprintf("python3 %s/main.py", codeDirAbsPath))
		for _, arg := range inputArgPaths {
			sb.WriteString(fmt.Sprintf(" \\\n    '%s'", arg))
		}
		sb.WriteString("\n```\n\n")
		sb.WriteString("**Positional arguments** (sys.argv):\n")
		for i, arg := range inputArgPaths {
			sb.WriteString(fmt.Sprintf("- `sys.argv[%d]` = `%s`\n", i+1, arg))
		}
		sb.WriteString("\n")
		sb.WriteString("**CRITICAL**: Read ALL input data from `sys.argv` — these are the declared dependencies from upstream steps. Do NOT construct paths to sibling step folders manually (e.g. do NOT build paths like `execution/login-step/output.json`). The controller resolves the correct paths per group/iteration and passes them as positional arguments. If you need data that isn't in sys.argv, add it as a context_dependency in the plan — don't hardcode paths.\n\n")
	} else {
		sb.WriteString("- No positional arguments — script takes no inputs\n\n")
	}

	// Show available env vars
	if len(envVarNames) > 0 {
		sb.WriteString("**Available environment variables**:\n")
		// Always show STEP_OUTPUT_DIR first, then sort the rest
		shown := map[string]bool{}
		sb.WriteString(fmt.Sprintf("- `STEP_OUTPUT_DIR` = `%s`  (write output files here)\n", stepOutputAbsPath))
		shown["STEP_OUTPUT_DIR"] = true
		stepExecutionDir := filepath.Dir(stepOutputAbsPath)
		sb.WriteString(fmt.Sprintf("- `STEP_EXECUTION_DIR` = `%s`  (parent execution folder — only use as fallback when data is not available via sys.argv)\n", stepExecutionDir))
		shown["STEP_EXECUTION_DIR"] = true
		sorted := make([]string, 0, len(envVarNames))
		for _, name := range envVarNames {
			if !shown[name] {
				sorted = append(sorted, name)
			}
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			sb.WriteString(fmt.Sprintf("- `%s`\n", name))
		}
		sb.WriteString("\n")
	}

	// Show validation schema — tells the LLM exactly what output files to produce
	if validationSchemaJSON != "" {
		sb.WriteString("**Output requirements (validation schema)** — your script MUST produce these files:\n")
		sb.WriteString("```json\n")
		sb.WriteString(validationSchemaJSON)
		sb.WriteString("\n```\n")
		sb.WriteString("Write each required file to `STEP_OUTPUT_DIR` with the exact filename and structure shown.\n\n")
	}

	// NOTE: Prior script context (failed script + error, or existing script for update)
	// is now included in the USER MESSAGE via BuildScriptedPriorContext(), not in the
	// system prompt. This ensures the LLM sees it with higher salience and it survives
	// repair agent transitions where the system prompt is regenerated.
	return sb.String()
}

// BuildScriptedPriorContext generates the prior script context section for inclusion
// in the user message. Returns empty string if no prior context is needed.
// scriptMetadataPath is optional — when non-empty and there's a failure, the LLM is
// told to read it to see run history, failure streaks, and per-group stats.
func BuildScriptedPriorContext(priorScript, priorError, scriptMetadataPath string, isCodeLocked bool) string {
	if priorScript == "" {
		return ""
	}
	var sb strings.Builder
	if priorError != "" {
		sb.WriteString("\n### Previous Script (Failed)\n\n")
		if isCodeLocked {
			sb.WriteString("The saved script ran and its output failed validation. The script is locked (`lock_code=true`) — do NOT rewrite it. Read it to understand what it does, then drive the task to completion interactively (tool calls, browser snapshots) so this run produces valid outputs. If you find a real bug in the script, surface it in your reply for the orchestrator to clear the lock and patch on a future run.\n\n")
		} else {
			sb.WriteString("The previously saved script failed. Read the current main.py in your working directory, fix the bug, and rewrite it.\n\n")
		}
		sb.WriteString("**Error:**\n```\n")
		sb.WriteString(priorError)
		sb.WriteString("\n```\n")
		if scriptMetadataPath != "" {
			sb.WriteString("\n### Script Run History\n\n")
			sb.WriteString(fmt.Sprintf("Read `%s` to understand failure patterns — check `recent_runs` for repeated errors, `group_stats` for which groups fail, `current_streak` for consecutive failures, `lock_code_stats` for how the locked script has been doing since `lock_code` was set (look at `consecutive_failures` and `needs_review` — if true, the lock is likely wrong and the script needs unlocking + patching), and `last_failure` for the most recent error details.\n", scriptMetadataPath))
		}
	} else {
		sb.WriteString("\n### Existing Script (Update Required)\n\n")
		if isCodeLocked {
			sb.WriteString("A saved script exists at your working directory's main.py and is locked. Read it for context, but do NOT modify it. Use it as the reference for what this step should produce, and complete the task interactively for this run.\n")
		} else {
			sb.WriteString("A saved script already exists at your working directory's main.py. Read it, then adapt and improve it to match the current step description above. ")
			sb.WriteString("Keep all working logic intact unless the current task explicitly requires a change.\n")
		}
	}
	return sb.String()
}
