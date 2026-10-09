package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workshop "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspacepathpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// Live report data (window.report.run).
//
// A Workflow, Relay, Crew or Code Dashboard can run one of
// its own scripts under code/ and use
// the JSON it prints. The script runs in the same sandbox as a scripted step:
// read access to the workflow, a read-only DB snapshot, the workflow's
// selected secrets and MCP servers, and one writable cache folder. Anyone who
// can see the workflow can run it -- it uses the project's selection under
// the viewer's live MCP/Vault permissions. There is no server cache;
// a script that talks to a slow or rate-limited service caches for itself in
// REPORT_CACHE_DIR.

const (
	reportRunTimeout     = 60 * time.Second
	reportRunMaxOutput   = 2 << 20 // bytes of stdout a report may receive
	reportRunMaxArgs     = 16 << 10
	reportRunConcurrency = 4
	reportRunCacheFolder = ".report-cache"
)

var reportRunSlots = make(chan struct{}, reportRunConcurrency)

// reportRunScope is what the MCP bridge needs to authorize a report run's
// tool calls after the HTTP request that started the script has moved on.
type reportRunScope struct {
	workspacePath string
	userID        string
}

// reportRunSelection is what a report script may use: the workspace's own
// selected MCP servers/tools, secrets and variables.
type reportRunSelection struct {
	servers       []string
	tools         []string
	secrets       []string
	globalSecrets *[]string
	variables     bool
}

// isReportRunWorkflowRoot: exactly Workflow/<name>, never a folder inside one.
func isReportRunWorkflowRoot(workspacePath string) bool {
	segments := strings.Split(strings.Trim(workspacePath, "/"), "/")
	return len(segments) == 2 && segments[0] == "Workflow" && segments[1] != "" && !strings.HasPrefix(segments[1], ".")
}

// reportRunSelectionFor reads the selection without migrating or writing
// anything: a reader's refresh must never touch the owner's manifest.
func reportRunSelectionFor(ctx context.Context, workspacePath string) (reportRunSelection, error) {
	if product, _, isProject := projectProductForPath(workspacePath); isProject {
		servers, _, err := productSelectedServers(ctx, product.ProfileID, workspacePath)
		if err != nil {
			return reportRunSelection{}, err
		}
		secrets, _, err := productSelectedSecrets(ctx, product.ProfileID, workspacePath)
		if err != nil {
			return reportRunSelection{}, err
		}
		globals, err := productSelectedGlobalSecrets(ctx, product.ProfileID, workspacePath)
		if err != nil {
			return reportRunSelection{}, err
		}
		raw, _, err := readProjectRuntimeManifest(ctx, product.ProfileID, workspacePath)
		if err != nil {
			return reportRunSelection{}, err
		}
		var manifest struct {
			Capabilities WorkflowCapabilities `json:"capabilities"`
		}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
				return reportRunSelection{}, err
			}
		}
		return reportRunSelection{servers: servers, tools: manifest.Capabilities.SelectedTools, secrets: secrets, globalSecrets: globals}, nil
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return reportRunSelection{}, fmt.Errorf("workflow.json is missing or unreadable")
	}
	caps := manifest.Capabilities
	return reportRunSelection{servers: caps.SelectedServers, tools: caps.SelectedTools, secrets: caps.SelectedSecrets, globalSecrets: caps.SelectedGlobalSecretNames, variables: true}, nil
}

// reportRunScriptPath accepts only a script in the workflow's code/ folder.
func reportRunScriptPath(relative string) (string, bool) {
	slashed := strings.ReplaceAll(strings.TrimSpace(relative), "\\", "/")
	normalized := strings.TrimPrefix(path.Clean("/"+slashed), "/")
	if strings.Contains(slashed, "..") || !strings.HasPrefix(normalized, "code/") || strings.Contains(normalized, "/.") {
		return "", false
	}
	switch path.Ext(normalized) {
	case ".py", ".js", ".mjs":
		return normalized, true
	}
	return "", false
}

func reportRunCommand(absScript string) string {
	interpreter := "python3"
	if ext := filepath.Ext(absScript); ext == ".js" || ext == ".mjs" {
		interpreter = "node"
	}
	return interpreter + " '" + strings.ReplaceAll(absScript, "'", `'"'"'`) + "'"
}

// POST /api/workflow/report-preview/run {workspace, path, args}
// Lives under the report-preview prefix so the headless preview page (whose
// token reaches only that prefix) renders live reports the same way.
func (api *StreamingAPI) handleReportRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string          `json:"workspace"`
		Path      string          `json:"path"`
		Args      json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, reportRunMaxArgs+4096)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	workspacePath, err := reportPreviewWorkspace(r, claims, body.Workspace)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clean, valid := cleanWorkspaceReadPath(workspacePath, false)
	if !valid {
		http.Error(w, "a Workflow, Relay, Crew or Code project root is required", http.StatusBadRequest)
		return
	}
	crew, isCrew := resolveCrewPath(r.Context(), claims.UserID, clean)
	ref := workspaceref.MustParse(clean)
	root, project, projectRoot := ref.ProjectRoot()
	isCode := projectRoot && root == workspaceref.CodeProjectsRoot && !strings.HasPrefix(project, ".")
	if (isCrew && crew.Rest != "") || (!isCrew && !isCode && !isReportRunWorkflowRoot(clean)) {
		http.Error(w, "a Workflow, Relay, Crew or Code project root is required", http.StatusBadRequest)
		return
	}
	if isCrew {
		workspacePath = crew.Root
	} else if isCode {
		owner := ref.Owner()
		if owner == "" {
			owner = sanitizeUserIDForPath(claims.UserID)
		}
		// Code dashboards follow ordinary Code ownership, including for admins.
		// Read-only administrative inspection never authorizes script execution.
		if codeRoleFor(r.Context(), claims.UserID, owner, project) != codeRoleOwner {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
		workspacePath = workspaceref.PhysicalPathOf(owner, ref.Logical())
		if resolveProjectOwner(r.Context(), workspacePath) != owner {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
	}
	script, ok := reportRunScriptPath(body.Path)
	if !ok {
		http.Error(w, "path must be a .py, .js or .mjs script under code/", http.StatusBadRequest)
		return
	}
	// Project capabilities are selected by the owner; live viewer permissions
	// still govern every secret and MCP call made by the script.
	if isCrew {
		if crewAccessFor(claims, crew) == crewAccessNone {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
	} else if !isCode && !requireWorkflowVisible(w, r, workspacePath) {
		return
	}
	if claims.PreviewConnectionID != "" {
		if err := reportPreviewReadable(r, claims, workspacePath); err != nil {
			writeWorkflowPermissionDenied(w, "read")
			return
		}
	}
	if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(script, false) {
		http.Error(w, "outside connection read grants", http.StatusForbidden)
		return
	}
	script, err = api.resolveDashboardDocument(r, workspacePath, script)
	if err != nil {
		dashboardError(w, err)
		return
	}
	args := "{}"
	if trimmed := bytes.TrimSpace(body.Args); len(trimmed) > 0 && string(trimmed) != "null" {
		if len(trimmed) > reportRunMaxArgs || !json.Valid(trimmed) {
			http.Error(w, "args must be JSON of at most 16 KB", http.StatusBadRequest)
			return
		}
		args = string(trimmed)
	}

	docsRoot := workshop.GetPromptDocsRoot()
	if isCode {
		docs, err := os.OpenRoot(docsRoot)
		if err != nil {
			http.Error(w, "workspace unavailable", http.StatusNotFound)
			return
		}
		code, err := openProjectDir(docs, productOwnerFromPath(workspacePath), root, project, "code")
		_ = docs.Close()
		if err != nil {
			status := http.StatusBadRequest
			if os.IsNotExist(err) {
				status = http.StatusNotFound
			}
			http.Error(w, "Code script directory is missing or unsafe", status)
			return
		}
		defer code.Close()
	}
	codeRoot := filepath.Join(docsRoot, workspacePath, "code")
	absScript := filepath.Join(docsRoot, workspacePath, script)
	// A symlink must not lead a report outside its own code/ folder.
	if resolved, err := filepath.EvalSymlinks(absScript); err != nil {
		writeReportRunResult(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("script %s not found", script)})
		return
	} else if realCode, err := filepath.EvalSymlinks(codeRoot); err != nil || !strings.HasPrefix(resolved, realCode+string(filepath.Separator)) {
		http.Error(w, "script must stay inside code/", http.StatusBadRequest)
		return
	}

	select {
	case reportRunSlots <- struct{}{}:
		defer func() { <-reportRunSlots }()
	case <-r.Context().Done():
		return
	}

	result := api.runReportScript(r.Context(), claims.UserID, workspacePath, script, absScript, args)
	status := http.StatusOK
	if result["success"] != true {
		status = http.StatusUnprocessableEntity
	}
	writeReportRunResult(w, status, result)
}

func writeReportRunResult(w http.ResponseWriter, status int, result map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func (api *StreamingAPI) runReportScript(ctx context.Context, userID, workspacePath, script, absScript, args string) map[string]any {
	started := time.Now()
	selection, err := reportRunSelectionFor(ctx, workspacePath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	docsRoot := workshop.GetPromptDocsRoot()
	cacheRel := filepath.ToSlash(filepath.Join(workspacePath, reportRunCacheFolder))
	if _, err := workspacepathpolicy.Materialize(docsRoot, []workspacepathpolicy.Grant{{
		Path: cacheRel, Lifecycle: workspacepathpolicy.PlatformManaged, Kind: workspacepathpolicy.Directory, Mode: 0o700,
	}}); err != nil {
		return map[string]any{"success": false, "error": "could not prepare the report cache folder"}
	}

	sessionID := "report-run-" + uuid.NewString()
	api.reportRunSessions.Store(sessionID, reportRunScope{workspacePath: workspacePath, userID: userID})
	defer api.reportRunSessions.Delete(sessionID)

	scopedSecrets := api.loadSelectedSecrets(ctx, userID, workspacePath, selection.secrets)
	if err := validateVaultSecretSelection(ctx, userID, scopedSecrets, &selection.secrets); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if err := validateVaultSecretSelection(ctx, userID, scopedSecrets, selection.globalSecrets); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	env := api.reportRunEnv(ctx, userID, workspacePath, selection, sessionID)
	secretValues := make([]string, 0, len(env))
	for key, value := range env {
		if strings.HasPrefix(key, "SECRET_") && len(value) >= 6 {
			secretValues = append(secretValues, value)
		}
	}
	env["REPORT_ARGS"] = args
	env["REPORT_CACHE_DIR"] = filepath.Join(docsRoot, cacheRel)
	// The workspace service copies the DB into STEP_OUTPUT_DIR/.runtime and
	// points DB_PATH at that snapshot, so a report never writes the store.
	env["STEP_OUTPUT_DIR"] = env["REPORT_CACHE_DIR"]
	env["DB_PATH"] = filepath.Join(docsRoot, workspacePath, "db", "db.sqlite")
	_, dbErr := os.Stat(env["DB_PATH"])
	if dbErr != nil {
		delete(env, "DB_PATH")
	}
	env["WORKFLOW_CODE_ROOT"] = filepath.Join(docsRoot, workspacePath, "code")
	deps := filepath.Join(docsRoot, workspacePath, ".sandbox-cache", "python-packages")
	env["WORKFLOW_CODE_DEPS"] = deps
	env["PYTHONPATH"] = env["WORKFLOW_CODE_ROOT"] + string(filepath.ListSeparator) + deps
	env["PYTHONDONTWRITEBYTECODE"] = "1"

	timeoutSeconds := int(reportRunTimeout / time.Second)
	runCtx, cancel := context.WithTimeout(ctx, reportRunTimeout+15*time.Second)
	defer cancel()
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	out, execErr := client.ExecuteShellCommand(runCtx, workspace.ExecuteShellCommandParams{
		Command:          reportRunCommand(absScript),
		WorkingDirectory: filepath.ToSlash(filepath.Join(workspacePath, "code")),
		Timeout:          &timeoutSeconds,
		ExtraEnv:         env,
		// The script's tool calls act as this run's bridge session.
		BridgeSessionID: sessionID,
		DBReadSnapshot:  dbErr == nil,
		FolderGuard: &workspace.FolderGuardConfig{
			Enabled:    true,
			ReadPaths:  []string{workspacePath},
			WritePaths: []string{cacheRel},
		},
	})
	elapsed := time.Since(started).Milliseconds()
	log.Printf("[REPORT_RUN] user=%s workflow=%s script=%s exit=%d ms=%d err=%v", userID, workspacePath, script, out.ExitCode, elapsed, execErr)

	redact := func(text string) string {
		for _, secret := range secretValues {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
		return text
	}
	failure := func(message string) map[string]any {
		stderr := strings.TrimSpace(redact(out.Stderr))
		if len(stderr) > 4000 {
			stderr = "…" + stderr[len(stderr)-4000:]
		}
		return map[string]any{"success": false, "error": redact(message), "stderr": stderr, "duration_ms": elapsed}
	}
	switch {
	case execErr != nil:
		return failure(fmt.Sprintf("script did not run: %v", execErr))
	case out.TimedOut:
		return failure(fmt.Sprintf("script timed out after %s", reportRunTimeout))
	case out.CommandFailed():
		message := fmt.Sprintf("script exited with code %d", out.ExitCode)
		if out.Error != "" {
			message += ": " + out.Error
		}
		return failure(message)
	case len(out.Stdout) > reportRunMaxOutput:
		return failure(fmt.Sprintf("script printed %d bytes; the limit is %d -- aggregate before printing", len(out.Stdout), reportRunMaxOutput))
	}
	stdout := strings.TrimSpace(out.Stdout)
	var data any
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		snippet := stdout
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return failure(fmt.Sprintf("script must print one JSON value on stdout (log to stderr); got: %s", snippet))
	}
	return map[string]any{"success": true, "data": data, "duration_ms": elapsed}
}

// reportRunEnv gives the script what a scheduled run of this workflow would
// have: selected workflow + global secrets, the unambiguous variable values,
// and an MCP bridge session scoped to the workflow's selected servers.
func (api *StreamingAPI) reportRunEnv(ctx context.Context, userID, workspacePath string, selection reportRunSelection, sessionID string) map[string]string {
	env := map[string]string{}
	secrets := api.mergeGlobalSecretsFor(ctx, userID, api.loadSelectedSecrets(ctx, userID, workspacePath, selection.secrets), selection.globalSecrets)
	for _, secret := range secrets {
		env["SECRET_"+secret.Name] = secret.Value
	}
	if selection.variables { // workflows only; crews have no variables
		api.addReportRunVariables(ctx, workspacePath, env)
	}
	if base := strings.TrimRight(os.Getenv("MCP_API_URL"), "/"); base != "" {
		env["MCP_API_URL"] = base + "/s/" + sessionID
		env["MCP_SESSION_ID"] = sessionID
		common.PopulateMCPBridgeShortEnv(env) // the session's own token
	}
	return env
}

func (api *StreamingAPI) addReportRunVariables(ctx context.Context, workspacePath string, env map[string]string) {
	content, found, err := readFileFromWorkspace(ctx, filepath.ToSlash(filepath.Join(workspacePath, "variables/variables.json")))
	if err != nil || !found || strings.TrimSpace(content) == "" {
		return
	}
	var variables workshop.VariablesManifest
	if json.Unmarshal([]byte(content), &variables) != nil {
		return
	}
	values, _, ok := workshop.ResolveWorkshopVariableValues(&variables, nil)
	if !ok {
		values = workshop.MergeGroupWithDefaults(&variables, nil)
	}
	for name, value := range values {
		env["VAR_"+name] = value
	}
}

// resolveReportRunMCPServer scopes a report run's bridge calls to the
// workflow's selected servers and tools. ok=false means sessionID is not a
// report run.
func (api *StreamingAPI) resolveReportRunMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, bool, error) {
	cached, found := api.reportRunSessions.Load(sessionID)
	if !found {
		if strings.HasPrefix(sessionID, "report-run-") {
			return nil, true, fmt.Errorf("this report run has finished")
		}
		return nil, false, nil
	}
	scope := cached.(reportRunScope)
	selection, err := reportRunSelectionFor(ctx, scope.workspacePath)
	if err != nil {
		return nil, true, fmt.Errorf("report MCP scope is unavailable")
	}
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, true, fmt.Errorf("load current MCP configuration: %w", err)
	}
	resolved, err := api.resolveScopedGovernedMCP(ctx, catalog, runtimeMCPServers(selection.servers), selection.tools, scope.userID, server, tool)
	return resolved, true, err
}
