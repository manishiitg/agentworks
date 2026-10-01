package step_based_workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
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
	Learnings     string // Formatted learning history section
	PreviousSteps string // Previous steps context section
}

// BuildManagedWorkflowDBGuidance is the one agent-facing contract for the
// managed workflow database. Agentic steps and background agents must receive
// the same call shapes; saved scripted code has a separate $DB_PATH contract.
func BuildManagedWorkflowDBGuidance(access string) string {
	if strings.EqualFold(strings.TrimSpace(access), DBAccessRead) {
		return guidance.StepSystemPromptTemplate("managed-db-read")
	}
	return guidance.StepSystemPromptTemplate("managed-db-write")
}

// BuildCodeExecutionSection returns the code execution mode instructions.
// isCodeExecution: agent uses code execution mode (HTTP API calls via shell)
// workspacePath: absolute workspace path for code examples
func BuildCodeExecutionSection(isCodeExecution bool, workspacePath string) string {
	if isCodeExecution {
		return prompt.GetCodeExecutionInstructions(workspacePath) + `

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

// BuildLearningsSection returns the formatted learning history section for the system prompt.
// learningHistory: the formatted learning content (empty string means no learnings)
// keepLearningFull: whether full learning content is included (vs paths-only)
func BuildLearningsSection(learningHistory string, keepLearningFull bool) string {
	if learningHistory == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Learning History (Secondary Guidance)\n")
	sb.WriteString(learningHistory)
	sb.WriteString("\n\n")
	sb.WriteString("- **Workflows**: Use validated sequences from learnings, but adapt args to this specific step.\n")
	sb.WriteString("- **Patterns**: Use tool hints/error recovery patterns from learnings.\n")
	sb.WriteString("- **Conflict**: If learning conflicts with step requirement, the step wins.\n")
	if !keepLearningFull {
		sb.WriteString("- **Note**: These learnings are incomplete. Rely primarily on the step description and your own capabilities.\n")
	}

	return sb.String()
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

// BuildMainPyAuthoringRules returns the canonical rules that any agent writing or
// patching a step's main.py MUST follow. Shared by:
//   - the execution agent in scripted mode (via GetScriptedModeInstructions)
//   - review_step_code (detects drift from these rules)
//   - the parent Pulse Fixer (applies reviewed fixes)
//
// The workshop chat agent prompt does NOT call this anymore — it gets a short
// cheat sheet and loads the full rules on demand via
// read_skill(skills=[{"name":"builder-reference","path":"references/code-authoring.md"}]) when it actually needs to patch.
//
// Source of truth lives in cmd/server/guidance/templates/system/code-authoring.md.
// This wrapper is the inline fallback for API agents and non-execution review
// surfaces; coding CLI execution agents read the same document from the
// projected builder-reference skill instead.
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
	sb.WriteString("Read the attached `agent-browser` skill's **Selector Discipline** section before browser authoring. It is the shared contract for builder chats, steps, scripts, and learnings.\n\n")
	sb.WriteString("Start with a snapshot and use current refs for live actions. Saved main.py may resolve fresh refs at runtime or use verified durable locators; never hardcode a previous snapshot's ref. Use a scoped read-only eval only when the snapshot is insufficient. Verify the intended target and the action's outcome.\n\n")
	sb.WriteString("- **Site-access resilience**: if a headless `open` returns \"Permission Denied\", a blank page, or a native-alert freeze, switch the workflow to CDP mode against an existing Chrome and document the precondition in learnings. Register a dialog handler before interacting if the page shows native alerts.\n")
	sb.WriteString("- Wait by polling snapshots in a loop checking for expected content / expected widget state (e.g. disabled→enabled). NOT `time.sleep(N)` for UI state (use short sleeps 1-2s only between polls).\n")
	sb.WriteString("- On failure (element missing, navigation stuck), print **both** the current snapshot AND the last probe result (if any) so the fix loop sees both views.\n")
	sb.WriteString("- Call `get_api_spec` to discover exact parameter schemas — don't guess parameter names.\n")
	sb.WriteString("\n")
	return sb.String()
}

// BuildBrowserLearningRules is the single selector-persistence contract used by
// both the active direct-learning continuation and the legacy learning agent.
// Keep selector mechanics in the attached agent-browser skill; this block only
// describes the durable HOW knowledge that may be saved across runs.
func BuildBrowserLearningRules() string {
	var sb strings.Builder
	sb.WriteString("## Browser automation learnings (required when this step used agent_browser)\n\n")
	sb.WriteString("Save reusable browser HOW in `references/site-profile.md`, `references/selectors.md`, or a linked topic. Snapshots are runtime evidence, not configuration.\n\n")
	sb.WriteString("1. **Never persist snapshot refs.** Do not store `@e1`, `e68`, or generated `ref` values. Save the semantic recipe that resolves a fresh ref from current page state; refresh after navigation, DOM updates, tab changes, or when freshness is uncertain.\n")
	sb.WriteString("2. **Record the observed stable-hook inventory when useful.** Do not run a full DOM probe merely to complete learnings. Include the framework only if known and whether the inspected region exposes `data-testid`/`data-test`, hand-written `id` or `name`, `aria-label`, labels/placeholders, and stable roles/names. Explicitly list generated ID/class patterns to avoid.\n")
	sb.WriteString("3. **Record semantic action recipes, not a raw selector dump.** For each important action save, in a compact form, the action name and purpose, page/state precondition, primary verified locator or fresh-snapshot resolution recipe, enclosing row/card scope, one or two fallbacks, expected postcondition, and timing/auth/modal quirks (e.g. `login.fill_user_id` → primary `{by: id, value: panAdhaarUserId}`, fallback `{by: placeholder, value: User ID}`, postcondition Continue enabled).\n")
	sb.WriteString("4. **Follow the agent-browser skill selector contract:** role + accessible name or label, verified test attributes, hand-written semantic `id`/`name`, and `aria-label` are locator candidates, not guarantees of stability. Record state changes such as Like becoming Unlike. Structural CSS/XPath is a fragile last resort. Store classes only when verified hand-written and stable across runs; never store generated framework/build classes or long class chains.\n")
	sb.WriteString("5. **Capture behavior that DOM inspection cannot explain.** Preserve login/CDP requirements, redirects, disabled-until-valid controls, portal/popover behavior, confirmation dialogs, OTP/captcha branches, polling conditions, and known false controls.\n")
	sb.WriteString("6. **Keep confidence honest.** Save only locators actually used or verified in this run. Mark unverified fallbacks as candidates. If a saved locator failed, replace or qualify it and retain the failure signature in the known-bad section.\n")
	sb.WriteString("7. **Do not save sensitive values.** Selector recipes may describe field identity, but never persist entered credentials, account identifiers, tokens, cookies, or user data.\n")
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

// BuildPythonBestPractices returns a "Python Best Practices" section for code execution agents.
// varMappingLines lists {{VAR}} → SECRET_VAR mappings (may be empty).
// hasInputArgs: whether the step has positional input file args (sys.argv).
// This is the single source of truth for Python code patterns so all generated scripts are consistent.
func BuildPythonBestPractices(varMappingLines []string, hasInputArgs bool) string {
	var sb strings.Builder
	sb.WriteString("\n## Python Best Practices\n\n")
	sb.WriteString("Use these exact patterns for consistency across all scripts.\n\n")

	// Env vars / secrets
	sb.WriteString("### Accessing secrets and workflow variables\n")
	sb.WriteString("```python\n")
	sb.WriteString("import os, sys\n\n")
	sb.WriteString("# Always use os.environ['KEY'] — never os.environ.get('KEY', 'default')\n")
	sb.WriteString("# Missing var = KeyError (fail loudly, never silently fall back to a hardcoded value)\n\n")
	sb.WriteString("# Workflow variables → VAR_<NAME>  (non-secret config: user IDs, sheet IDs, etc.)\n")
	if len(varMappingLines) > 0 {
		for _, line := range varMappingLines {
			// line format: "{{VAR}} → os.environ['VAR_VAR']"
			parts := strings.SplitN(line, " → ", 2)
			if len(parts) == 2 {
				varName := strings.Trim(parts[0], "{}")
				sb.WriteString(fmt.Sprintf("%s = os.environ['VAR_%s']\n", strings.ToLower(varName), varName))
			}
		}
	} else {
		sb.WriteString("my_var = os.environ['VAR_MY_VAR']\n")
	}
	sb.WriteString("\n# Real secrets → SECRET_<NAME>  (passwords, API keys, tokens)\n")
	sb.WriteString("my_password = os.environ['SECRET_MY_PASSWORD']\n")
	sb.WriteString("\n# Special vars always available:\n")
	sb.WriteString("output_dir    = os.environ['STEP_OUTPUT_DIR']      # write all output files here\n")
	sb.WriteString("execution_dir = os.environ['STEP_EXECUTION_DIR']  # parent folder for sibling-step reads only (fallback only — prefer sys.argv for input data)\n")
	sb.WriteString("db_path       = os.environ['DB_PATH']             # ABSOLUTE path to the workflow db/db.sqlite — ALWAYS use this for sqlite, never relative 'db/db.sqlite' (the step's CWD is not the workflow root)\n")
	sb.WriteString("mcp_url       = os.environ['MCP_API_URL']\n")
	sb.WriteString("mcp_token     = os.environ['MCP_API_TOKEN']\n")
	sb.WriteString("group_name    = os.environ.get('VAR_GROUP_NAME', '')  # current group name (e.g., 'production'); empty if no group\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**sqlite `unable to open database file`?** First verify that `DB_PATH` exists, is absolute, and points to the workflow db, then pass it directly to `sqlite3.connect(os.environ['DB_PATH'])`. Never use relative `db/db.sqlite`, generate `.sql` files, copy the db to `/tmp`, or silently switch to the `sqlite3` CLI as a workaround. If the absolute path exists but Python still cannot open it, report a Runloop runtime/folder-guard failure with the exact path and error; do not describe Python sqlite as generally sandbox-blocked.\n\n")

	// Input files
	if hasInputArgs {
		sb.WriteString("### Reading input files (positional args)\n")
		sb.WriteString("```python\n")
		sb.WriteString("import sys, json\n\n")
		sb.WriteString("input_file = sys.argv[1]          # first context_dependency path\n")
		sb.WriteString("# input_file2 = sys.argv[2]       # second, if any\n")
		sb.WriteString("with open(input_file) as f:\n")
		sb.WriteString("    data = json.load(f)            # or f.read() for plain text\n")
		sb.WriteString("```\n\n")
	}

	// MCP tool call
	sb.WriteString("### Calling an MCP tool\n")
	sb.WriteString("```python\n")
	sb.WriteString("import requests, os, json, time\n\n")
	sb.WriteString("VERBOSE = os.environ.get('SCRIPT_VERBOSE', '') == '1'\n\n")
	sb.WriteString("def call_mcp(server, tool, args, retries=3, backoff=2):\n")
	sb.WriteString("    \"\"\"Call an MCP tool via HTTP. Retries on broken pipe / connection errors.\"\"\"\n")
	sb.WriteString("    url = os.environ['MCP_API_URL'] + f'/tools/mcp/{server}/{tool}'\n")
	sb.WriteString("    headers = {\n")
	sb.WriteString("        'Authorization': f'Bearer {os.environ[\"MCP_API_TOKEN\"]}',\n")
	sb.WriteString("        'Content-Type': 'application/json',\n")
	sb.WriteString("    }\n")
	sb.WriteString("    if VERBOSE: print(f'[MCP] >> {server}/{tool} args={json.dumps(args)[:500]}')\n")
	sb.WriteString("    last_err = None\n")
	sb.WriteString("    for attempt in range(retries):\n")
	sb.WriteString("        try:\n")
	sb.WriteString("            resp = requests.post(url, json=args, headers=headers, timeout=120)\n")
	sb.WriteString("            resp.raise_for_status()\n")
	sb.WriteString("            result = resp.json()\n")
	sb.WriteString("            if not result.get('success'):\n")
	sb.WriteString("                err = result.get('error', '')\n")
	sb.WriteString("                if VERBOSE: print(f'[MCP] !! {server}/{tool} FAILED: {err[:1000]}')\n")
	sb.WriteString("                # Broken pipe from Go's MCP connection — retry, the server will reconnect\n")
	sb.WriteString("                if 'broken pipe' in err.lower() or 'connection reset' in err.lower() or 'transport closed' in err.lower():\n")
	sb.WriteString("                    last_err = RuntimeError(f'MCP broken pipe: {err}')\n")
	sb.WriteString("                    if attempt < retries - 1:\n")
	sb.WriteString("                        time.sleep(backoff * (attempt + 1))\n")
	sb.WriteString("                        continue\n")
	sb.WriteString("                raise RuntimeError(f'MCP error: {err}')\n")
	sb.WriteString("            res = result['result']\n")
	sb.WriteString("            if VERBOSE: print(f'[MCP] << {server}/{tool} OK ({len(str(res))} chars): {str(res)[:500]}')\n")
	sb.WriteString("            return res\n")
	sb.WriteString("        except (requests.exceptions.ConnectionError, requests.exceptions.Timeout) as e:\n")
	sb.WriteString("            if VERBOSE: print(f'[MCP] !! {server}/{tool} attempt {attempt+1} error: {e}')\n")
	sb.WriteString("            last_err = e\n")
	sb.WriteString("            if attempt < retries - 1:\n")
	sb.WriteString("                time.sleep(backoff * (attempt + 1))\n")
	sb.WriteString("    raise last_err\n")
	sb.WriteString("```\n\n")

	// Writing output files
	sb.WriteString("### Writing output files\n")
	sb.WriteString("```python\n")
	sb.WriteString("import os, json\n\n")
	sb.WriteString("output_dir = os.environ['STEP_OUTPUT_DIR']\n")
	sb.WriteString("os.makedirs(output_dir, exist_ok=True)\n\n")
	sb.WriteString("# JSON output:\n")
	sb.WriteString("with open(os.path.join(output_dir, 'result.json'), 'w') as f:\n")
	sb.WriteString("    json.dump(data, f, indent=2)\n\n")
	sb.WriteString("# Text output:\n")
	sb.WriteString("with open(os.path.join(output_dir, 'output.txt'), 'w') as f:\n")
	sb.WriteString("    f.write(text)\n")
	sb.WriteString("```\n\n")

	// Error diagnostics guidance
	sb.WriteString("### Error diagnostics (critical for fix loop)\n")
	sb.WriteString("For scripted Playwright suites, read builder-reference references/playwright-scripted.md. Use direct Python Playwright and validate with execute_step(fast_path_only=true).\n")
	sb.WriteString("When your script fails, the **only** feedback the system sees is stdout + stderr.\n")
	sb.WriteString("Files written to disk are **not** automatically read back. So:\n")
	sb.WriteString("- **Always `print()` diagnostic context before raising/exiting on failure** — e.g., current page snapshot, API response body, intermediate state, what you expected vs. what you got.\n")
	sb.WriteString("- Never write debug info only to a file (the system won't read it). Print it to stdout first, then optionally save to a file.\n")
	sb.WriteString("- Include enough context that a future fix attempt can pinpoint the root cause without re-running the script.\n")
	sb.WriteString("```python\n")
	sb.WriteString("# BAD — debug info only in a file, invisible to the fix loop:\n")
	sb.WriteString("with open('debug.txt', 'w') as f: f.write(snapshot)\n")
	sb.WriteString("raise RuntimeError('Failed — check debug.txt')  # fix loop can't read debug.txt!\n\n")
	sb.WriteString("# GOOD — print diagnostic context so the fix loop can see it:\n")
	sb.WriteString("print(f'[DIAG] Expected: dashboard page, Got: {current_state}')\n")
	sb.WriteString("print(f'[DIAG] Page content (first 2000 chars):\\n{snapshot[:2000]}')\n")
	sb.WriteString("raise RuntimeError('Login failed — not on dashboard')\n")
	sb.WriteString("```\n")

	return sb.String()
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
		return filepath.Join(docsRoot, path)
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
			result[i] = filepath.Join(docsRoot, p)
		}
	}
	return result
}
