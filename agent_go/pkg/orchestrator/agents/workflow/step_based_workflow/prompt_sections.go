package step_based_workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	prompt "github.com/manishiitg/mcpagent/agent/prompt"
)

// BuildStepFilesListing enumerates files in a single step-associated folder (step output
// artifacts, execution logs, etc.) and returns a markdown listing with per-file byte
// sizes. The listing is meant to be inlined into an agent's user message so the agent can
// pick targets without a blind `ls` call.
//
// Layout is flat: hidden files and subdirectories are skipped (every per-step folder in
// this codebase is flat by convention). Returns a terse placeholder when the folder is
// missing or empty — callers typically have fallback language in their prompts for that.
func BuildStepFilesListing(folderPath string) string {
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return fmt.Sprintf("_Folder not readable at `%s` (%v)._", folderPath, err)
	}
	type fileEntry struct {
		name string
		size int64
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			files = append(files, fileEntry{name: e.Name(), size: -1})
			continue
		}
		files = append(files, fileEntry{name: e.Name(), size: info.Size()})
	}
	if len(files) == 0 {
		return fmt.Sprintf("_Folder `%s` is empty — no files to read._", folderPath)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Files in `%s` (sizes in bytes):\n", folderPath))
	for _, f := range files {
		if f.size < 0 {
			sb.WriteString(fmt.Sprintf("- `%s` (size unknown)\n", f.name))
			continue
		}
		sb.WriteString(fmt.Sprintf("- `%s` (%d bytes)\n", f.name, f.size))
	}
	return sb.String()
}

// PromptSections holds pre-built prompt sections that can be injected into any agent's
// system prompt. All agent types (execution, todo task) should
// use these common builders for consistency.
type PromptSections struct {
	CodeExecution string // Code execution instructions
	PreviousSteps string // Previous steps context section
}

// BuildManagedWorkflowDBGuidance is the one agent-facing contract for the
// managed workflow database. Agentic steps and background agents must receive
// the same call shapes; saved scripted code has a separate $DB_PATH contract.
func BuildManagedWorkflowDBGuidance(access string) string {
	// A product without platform stores (a Relay) has no database to describe.
	if strings.EqualFold(strings.TrimSpace(access), DBAccessNone) {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(access), DBAccessRead) {
		return guidance.StepSystemPromptTemplate("managed-db-read")
	}
	return guidance.StepSystemPromptTemplate("managed-db-write")
}

// BuildCodeExecutionSection returns the code execution mode instructions.
// isCodeExecution: agent uses code execution mode (HTTP API calls via shell)
func BuildCodeExecutionSection(isCodeExecution bool) string {
	if isCodeExecution {
		return prompt.GetCodeExecutionInstructions() + `

## Workflow execution environment

When supplied, $STEP_OUTPUT_DIR is the existing primary artifact directory; do
not mkdir it. $STEP_EXECUTION_DIR is its parent and is only a fallback for
sibling-step inputs when positional inputs were not supplied. Reference config
through $VAR_<NAME>, never hardcode it. Read credentials from $SECRET_<NAME>,
never print or save them. Missing variables must fail loudly (bash ${VAR_NAME:?missing}
or Python os.environ['VAR_NAME']); only $VAR_GROUP_NAME may be empty or absent.
`
	}
	return ""
}

// BuildPreviousStepsSection returns the previous steps context section for the system prompt.
// previousStepsSummary: the formatted summary from buildPreviousStepsSummary()
func BuildPreviousStepsSection(previousStepsSummary string) string {
	if previousStepsSummary == "" {
		return ""
	}
	return previousStepsSummary
}

// BuildVariablesSection returns the variables section for the system prompt.
// variableNames: formatted variable names (empty if no variables)
// variableValues: formatted variable values (empty if no values)
func BuildVariablesSection(variableNames string, variableValues string) string {
	if variableNames == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Variables\n")
	sb.WriteString(variableNames)
	sb.WriteString("\n")
	if variableValues != "" {
		sb.WriteString(fmt.Sprintf("**Values**: %s\n", variableValues))
	}
	sb.WriteString("\n**Handling**: Step descriptions are already resolved. For code and tool calls, use the resolved values directly.\n")
	return sb.String()
}

func contextOutputMatchesDependency(output string, dep string) bool {
	if strings.TrimSpace(output) == strings.TrimSpace(dep) {
		return true
	}
	for _, part := range strings.Split(output, ",") {
		if strings.TrimSpace(part) == strings.TrimSpace(dep) {
			return true
		}
	}
	return false
}

func contextOutputFileNames(output string) []string {
	parts := strings.Split(output, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func BuildMainPyAuthoringRules() string {
	return guidance.RenderSystemDoc("code-authoring") + "\n"
}

// BuildBrowserAuthoringRules returns the browser-automation-specific main.py rules.
// Append to BuildMainPyAuthoringRules() ONLY when the step has agent-browser available.
// Keep selector mechanics in the attached agent-browser skill. Non-browser
// steps do not need this authoring pointer.
//
// Callers: gate with templateVars["HasBrowserAccess"] == "true" or equivalent signal.
func BuildBrowserAuthoringRules() string {
	var sb strings.Builder
	sb.WriteString("## Browser automation rules (this step has agent_browser)\n\n")
	sb.WriteString("Read the attached `agent-browser` skill's **Selector Discipline** section before browser authoring. It is the shared contract for Crew chats, steps and scripts.\n\n")
	sb.WriteString("Start with a snapshot and use current refs for live actions. Saved main.py may resolve fresh refs at runtime or use verified durable locators; never hardcode a previous snapshot's ref. Use a scoped read-only eval only when the snapshot is insufficient. Verify the intended target and the action's outcome.\n\n")
	sb.WriteString("- **Site-access resilience**: if a headless `open` returns \"Permission Denied\", a blank page, or a native-alert freeze, switch the workflow to CDP mode against an existing Chrome and document the precondition in the task or its selected skill. Register a dialog handler before interacting if the page shows native alerts.\n")
	sb.WriteString("- Wait by polling snapshots in a loop checking for expected content / expected widget state (e.g. disabled→enabled). NOT `time.sleep(N)` for UI state (use short sleeps 1-2s only between polls).\n")
	sb.WriteString("- On failure (element missing, navigation stuck), print **both** the current snapshot AND the last probe result (if any) so the fix loop sees both views.\n")
	sb.WriteString("- Call `get_api_spec` to discover exact parameter schemas — don't guess parameter names.\n")
	sb.WriteString("\n")
	return sb.String()
}

// BrowserAuthoringRulesFromTemplateVars returns BuildBrowserAuthoringRules() when
// templateVars["HasBrowserAccess"] is "true", else "". Use at call sites that don't
// have direct access to the orchestrator (e.g. agent Execute methods that receive
// only templateVars, including review_step_code).
func BrowserAuthoringRulesFromTemplateVars(templateVars map[string]string) string {
	if templateVars["HasBrowserAccess"] == "true" {
		return BuildBrowserAuthoringRules()
	}
	return ""
}

// ResolveDependencyPathCandidates returns candidate absolute paths for a dependency, ordered by
// workflow likelihood. Callers can optionally verify these against the real workspace and pick
// the first existing file.
func ResolveDependencyPathCandidates(
	dep string,
	stepIndex int,
	currentStepPath string,
	allSteps []PlanStepInterface,
	executionWorkspacePath string,
	docsRoot string,
	variableValues map[string]string,
) []string {
	if dep == "" {
		return nil
	}
	toAbs := func(path string) string {
		if path == "" || docsRoot == "" {
			return path
		}
		resolved, _ := fsutil.WorkspacePath(docsRoot, path)
		return resolved
	}
	buildDepAbsPath := func(folderPath string, dep string) string {
		return fmt.Sprintf("%s/%s", toAbs(folderPath), dep)
	}
	currentStepID := ""
	if stepIndex >= 0 && stepIndex < len(allSteps) {
		currentStepID = allSteps[stepIndex].GetID()
	}
	if currentStepPath == "" {
		currentStepPath = fmt.Sprintf("step-%d", stepIndex+1)
	}

	if filepath.IsAbs(dep) || strings.Contains(dep, "/") {
		return []string{dep}
	}

	candidates := make([]string, 0, 3)
	appendCandidate := func(candidate string) {
		if candidate == "" {
			return
		}
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		candidates = append(candidates, candidate)
	}

	for j := 0; j < stepIndex && j < len(allSteps); j++ {
		prevOutput := ResolveVariables(allSteps[j].GetContextOutput().String(), variableValues)
		if contextOutputMatchesDependency(prevOutput, dep) {
			prevStepPath := fmt.Sprintf("step-%d", j+1)
			prevStepExecPath := getExecutionFolderPath(executionWorkspacePath, allSteps[j].GetID(), prevStepPath)
			appendCandidate(buildDepAbsPath(prevStepExecPath, dep))
		} else if strings.TrimSpace(allSteps[j].GetID()) == strings.TrimSpace(dep) {
			// Compatibility for plans that accidentally stored the producer step
			// ID instead of its context_output filename. Resolve the declared
			// output so execution remains safe; Plan Drift separately flags the
			// plan for canonical cleanup.
			prevStepPath := fmt.Sprintf("step-%d", j+1)
			prevStepExecPath := getExecutionFolderPath(executionWorkspacePath, allSteps[j].GetID(), prevStepPath)
			for _, outputName := range contextOutputFileNames(prevOutput) {
				appendCandidate(buildDepAbsPath(prevStepExecPath, outputName))
			}
		}
	}

	if parentStepPath := nestedArtifactParentRoot(currentStepPath); parentStepPath != "" {
		parentStepExecPath := getExecutionFolderPath(executionWorkspacePath, "", parentStepPath)
		appendCandidate(buildDepAbsPath(parentStepExecPath, dep))
	}

	currentStepExecPath := getExecutionFolderPath(executionWorkspacePath, currentStepID, currentStepPath)
	appendCandidate(buildDepAbsPath(currentStepExecPath, dep))

	if len(candidates) == 0 {
		return []string{dep}
	}
	return candidates
}

// ResolveDependencyPaths maps dependency filenames to the most likely absolute path based on the
// workflow plan. This is the common logic used by both execution and todo task agents to show
// full paths instead of bare filenames.
func ResolveDependencyPaths(
	deps []string,
	stepIndex int,
	currentStepPath string,
	allSteps []PlanStepInterface,
	executionWorkspacePath string,
	docsRoot string,
	variableValues map[string]string,
) []string {
	if len(deps) == 0 {
		return nil
	}

	fullPathDeps := make([]string, 0, len(deps))
	for _, dep := range deps {
		candidates := ResolveDependencyPathCandidates(dep, stepIndex, currentStepPath, allSteps, executionWorkspacePath, docsRoot, variableValues)
		if len(candidates) == 0 {
			fullPathDeps = append(fullPathDeps, dep)
			continue
		}
		fullPathDeps = append(fullPathDeps, candidates[0])
	}
	return fullPathDeps
}

// GetPromptDocsRoot returns the workspace docs root path for use in prompts.
// This path is passed to LLM agents so they generate correct absolute paths in
// shell commands (jq, cat, ls, etc.) that execute inside the workspace server.
//
// Deployment modes:
//   - Docker (default):  not set → returns "/app/workspace-docs" (volume mount inside container)
//   - Desktop DMG (Mac): set by desktop/main.js → "~/Library/Application Support/Runloop/workspace-docs"
//     (workspace-server runs as a native binary, no Docker)
//   - run_server_with_logging.sh: NOT set, because workspace still runs in Docker
//
// ~30 callers across the workflow engine use this; change the env var, not callers.
func GetPromptDocsRoot() string {
	if p := os.Getenv("WORKSPACE_DOCS_PATH"); p != "" {
		return filepath.Clean(p)
	}
	return "/app/workspace-docs"
}

// toAbsPaths converts a slice of workspace-relative paths to absolute paths by prepending docsRoot.
func toAbsPaths(docsRoot string, paths []string) []string {
	result := make([]string, len(paths))
	for i, p := range paths {
		if p == "" || docsRoot == "" || filepath.IsAbs(p) {
			result[i] = p
		} else {
			result[i], _ = fsutil.WorkspacePath(docsRoot, p)
		}
	}
	return result
}
