package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/gogconfig"
)

// SessionShellConfig delegates to common.SessionShellConfig.
// Kept for backward compatibility — new code should use common.SessionShellConfig directly.
type SessionShellConfig = common.SessionShellConfig

// SetSessionWorkingDir delegates to common.SetSessionWorkingDir.
func SetSessionWorkingDir(sessionID, dir string) {
	common.SetSessionWorkingDir(sessionID, dir)
}

// SetSessionFolderGuard delegates to common.SetSessionFolderGuard.
func SetSessionFolderGuard(sessionID string, readPaths, writePaths []string) {
	common.SetSessionFolderGuard(sessionID, readPaths, writePaths)
}

// SetSessionFolderGuardBlockedPaths delegates to the common hard-deny policy.
func SetSessionFolderGuardBlockedPaths(sessionID string, blockedPaths []string) {
	common.SetSessionFolderGuardBlockedPaths(sessionID, blockedPaths)
}

// SetSessionFolderGuardBlockedWritePaths delegates to the common version.
// BlockedWritePaths are the authoritative write-only deny list — they flow
// through to the isolator's FolderGuardConfig.BlockedWritePaths and are enforced
// at kernel sandbox level (sandbox-exec `(deny file-write*)` on macOS,
// read-only bind-mount on Linux). Reads are intentionally still permitted so
// the agent can inspect files (e.g. read plan.json) without the ability to
// modify them — distinct from BlockedPaths which denies reads as well.
// SetSessionSandbox delegates to common.SetSessionSandbox.
func SetSessionSandbox(sessionID string, strictAllowlist, denyNetwork bool) {
	common.SetSessionSandbox(sessionID, strictAllowlist, denyNetwork)
}

func SetSessionFolderGuardBlockedWritePaths(sessionID string, blockedWritePaths []string) {
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, blockedWritePaths)
}

// ClearSessionShellConfig delegates to common.ClearSessionShellConfig.
func ClearSessionShellConfig(sessionID string) {
	common.ClearSessionShellConfig(sessionID)
}

// GetSessionShellConfig delegates to common.GetSessionShellConfig.
func GetSessionShellConfig(sessionID string) *SessionShellConfig {
	return common.GetSessionShellConfig(sessionID)
}

type ExecuteShellCommandParams struct {
	Command          string             `json:"command"`
	WorkingDirectory string             `json:"working_directory,omitempty"`
	Timeout          *int               `json:"timeout,omitempty"`
	UseShell         *bool              `json:"use_shell,omitempty"`
	FolderGuard      *FolderGuardConfig `json:"folder_guard,omitempty"` // Set internally — never from LLM args
	ExtraEnv         map[string]string  `json:"extra_env,omitempty"`
	// DBReadSnapshot asks the trusted workspace service to replace DB_PATH with
	// a consistent, standalone snapshot for this shell process. It is set by
	// the workflow harness for read-only scripted steps and is not exposed in
	// the execute_shell_command tool schema.
	DBReadSnapshot bool `json:"db_read_snapshot,omitempty"`
	// BridgeSessionID, set only by trusted Go callers (never decodable from
	// tool arguments), is the session the command's tool calls act as when it
	// differs from the shell's own session: a scripted step's script calls the
	// bridge as its step session while the shell runs under the group session.
	BridgeSessionID string `json:"-"`
}

var shellCommandSecretReplacements = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)AIza[0-9A-Za-z_-]{20,}`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)sk-api-[0-9A-Za-z_-]{20,}`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)sk_[0-9A-Za-z]{20,}`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)sk-[0-9A-Za-z_-]{20,}`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)(Bearer\s+)[0-9A-Za-z._~+/=-]{20,}`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|authorization)\s*[:=]\s*["']?)[^"'\\\s,}]{8,}`), `${1}[REDACTED]`},
}

func redactShellCommandForLog(command string) string {
	redacted := command
	for _, replacement := range shellCommandSecretReplacements {
		redacted = replacement.pattern.ReplaceAllString(redacted, replacement.replacement)
	}
	return redacted
}

// gitPushRe matches a `git ... push` invocation in the executable part of a
// command (heredoc/quoted data is stripped before this runs).
var gitPushRe = regexp.MustCompile(`(?m)\bgit\b[^\n;&|]*\bpush\b`)

// gitBundleCreateRe extracts the output path from ordinary `git bundle create`
// commands. A bundle is a snapshot of the repository; writing it back inside
// that same repository and then tracking it recursively embeds the previous
// repository in every later snapshot (PLAT-147).
var gitBundleCreateRe = regexp.MustCompile(`(?m)\bgit\b[^\n;&|]*\bbundle\s+create\s+("[^"]+"|'[^']+'|[^\s;&|]+)`)

// isGitPushCommand reports whether cmd runs `git push` as an executable (not as
// quoted/heredoc data — e.g. echo "git push" or a script that merely mentions it).
func isGitPushCommand(cmd string) bool {
	return gitPushRe.MatchString(stripShellDataRegions(cmd))
}

func gitBundleCreateDestination(cmd string) (string, bool) {
	match := gitBundleCreateRe.FindStringSubmatch(cmd)
	if len(match) != 2 {
		return "", false
	}
	destination := strings.Trim(strings.TrimSpace(match[1]), `"'`)
	return destination, destination != ""
}

func canonicalProspectivePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

// isWorkflowSecretPath identifies the workflow's own secret files. Kept in sync
// with backup-strategy.md: secrets.json, workflow_secrets/, *.key, *.pem,
// .env*, credentials*, *.token.
func isWorkflowSecretPath(p string) bool {
	p = strings.TrimSpace(strings.Trim(p, `"`))
	if p == "" {
		return false
	}
	slash := filepath.ToSlash(p)
	base := filepath.Base(slash)
	switch {
	case base == "secrets.json":
		return true
	case slash == "workflow_secrets" || strings.HasPrefix(slash, "workflow_secrets/") || strings.Contains(slash, "/workflow_secrets/"):
		return true
	case strings.HasSuffix(base, ".key"), strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".token"):
		return true
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case base == "credentials" || strings.HasPrefix(base, "credentials.") || strings.HasPrefix(base, "credentials_"):
		return true
	}
	return false
}

// parseGitHubOwnerRepo extracts owner/repo from a GitHub remote URL (https, ssh,
// or scp form). ok is false for non-GitHub remotes.
func parseGitHubOwnerRepo(remoteURL string) (owner, repo string, ok bool) {
	m := regexp.MustCompile(`github\.com[/:]+([^/]+)/(.+?)(?:\.git)?/?\s*$`).FindStringSubmatch(strings.TrimSpace(remoteURL))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], m[1] != "" && m[2] != ""
}

// githubRepoIsPublic returns true ONLY when the repo is positively confirmed
// public via the unauthenticated GitHub API (200 + "private":false). A private
// repo returns 404 unauthenticated; any error/ambiguity returns false. This is
// deliberately "confirm public, else allow" so backups are never blocked on
// uncertainty — only an unmistakable public repo blocks a secret push.
func githubRepoIsPublic(ctx context.Context, owner, repo string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false // 404 (private/missing) or other → not confirmed public → allow
	}
	var body struct {
		Private *bool `json:"private"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || body.Private == nil {
		return false
	}
	return !*body.Private
}

// gitOutputInDir runs a read-only git command in workingDir via the same bridge
// and returns trimmed stdout, or "" on any failure (callers fail open).
func (c *Client) gitOutputInDir(ctx context.Context, workingDir, command string) string {
	res, err := c.ExecuteShellCommand(ctx, ExecuteShellCommandParams{Command: command, WorkingDirectory: workingDir})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// blockSecretPushToPublicRepo refuses a `git push` that would carry the
// workflow's own secret files to a positively-confirmed public GitHub repo.
// Everything else — no secrets in the push, non-GitHub remote, or a private /
// indeterminate repo — is allowed, so full backups (incl. secrets to a private
// repo) are never blocked. Returns an error only to block.
func (c *Client) blockSecretPushToPublicRepo(ctx context.Context, params ExecuteShellCommandParams) error {
	if !isGitPushCommand(params.Command) {
		return nil
	}
	dir := params.WorkingDirectory
	// Files this push would carry: unpushed commits if an upstream exists, else
	// the whole tracked set (first push).
	listed := c.gitOutputInDir(ctx, dir, "git diff --name-only @{upstream}..HEAD 2>/dev/null || git ls-files")
	if listed == "" {
		return nil
	}
	var secrets []string
	for _, f := range strings.Split(listed, "\n") {
		if isWorkflowSecretPath(f) {
			secrets = append(secrets, strings.TrimSpace(f))
		}
	}
	if len(secrets) == 0 {
		return nil // nothing sensitive in the push
	}
	owner, repo, ok := parseGitHubOwnerRepo(c.gitOutputInDir(ctx, dir, "git remote get-url origin 2>/dev/null"))
	if !ok {
		return nil // non-GitHub remote → can't be a public-GitHub leak → allow
	}
	if !githubRepoIsPublic(ctx, owner, repo) {
		return nil // private or not-confirmed-public → allow full backup
	}
	return fmt.Errorf(
		"access denied: refusing to push workflow secret file(s) to the PUBLIC GitHub repo %s/%s: %s. "+
			"Secrets may only be backed up to a PRIVATE repo. Either make the repo private (gh repo edit %s/%s --visibility private), "+
			"unstage the secret files (git rm --cached <file>), or use the workflow secret tools / a large-file backend. "+
			"Non-secret content can still be pushed.",
		owner, repo, strings.Join(secrets, ", "), owner, repo,
	)
}

// blockRecursiveGitBundle rejects repository snapshots whose destination is
// ambiguous or lives inside the source repository. Requiring an absolute,
// external destination makes the containment decision explicit and prevents a
// tracked bundle from recursively inflating .git. Plain local Git commits are
// the supported zero-configuration checkpoint and do not need a bundle.
func (c *Client) blockRecursiveGitBundle(ctx context.Context, params ExecuteShellCommandParams) error {
	destination, ok := gitBundleCreateDestination(params.Command)
	if !ok {
		return nil
	}
	if strings.ContainsAny(destination, "$`") || !filepath.IsAbs(destination) {
		return fmt.Errorf(
			"access denied: git bundle output must be an absolute path outside the source repository; refusing ambiguous destination %q. "+
				"Never write or track a backup bundle inside the workflow. Use the local Git commit as the local-only checkpoint, or configure an off-device destination",
			destination,
		)
	}
	repoRoot := c.gitOutputInDir(ctx, params.WorkingDirectory, "git rev-parse --show-toplevel 2>/dev/null")
	if repoRoot == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return nil
	}
	target, err := canonicalProspectivePath(destination)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(root, target)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"access denied: refusing git bundle destination %q because it is inside source repository %q. "+
				"A tracked in-repo bundle recursively embeds earlier repository history and can grow without bound",
			destination, root,
		)
	}
	return nil
}

// ExecuteShellCommand executes a shell command using the REST API: POST /api/execute
func (c *Client) ExecuteShellCommand(ctx context.Context, params ExecuteShellCommandParams) (ShellCommandResult, error) {
	// One consistent view of the client env for this request; SetExtraEnv may
	// run concurrently (a secret created by a tool call in the same turn).
	clientEnv := c.extraEnvSnapshot()
	// Debug: log ExtraEnv keys, MCP_API_URL value, and client pointer for identity tracking
	if len(clientEnv) > 0 {
		keys := make([]string, 0, len(clientEnv))
		for k := range clientEnv {
			keys = append(keys, k)
		}
		log.Printf("[SHELL_DEBUG] Client=%p ExtraEnv keys: %v (count=%d) MCP_API_URL=%s MCP_SESSION_ID=%s", c, keys, len(clientEnv), clientEnv["MCP_API_URL"], clientEnv["MCP_SESSION_ID"])
	} else {
		log.Printf("[SHELL_DEBUG] Client=%p ExtraEnv is EMPTY", c)
	}
	// Always use shell execution - removed from tool definition to simplify LLM interface
	useShell := true
	params.UseShell = &useShell

	// Override default shell timeout: send timeout=0 to signal "no timeout" to the
	// Docker /api/execute endpoint. The default 60s is too short for long-running
	// operations like sub-agent calls via curl (which can take up to 30 minutes).
	// This matches direct tool call behavior where Timeout: 0 = runs indefinitely.
	if params.Timeout == nil {
		noTimeout := 0
		params.Timeout = &noTimeout
	}

	// Look up per-session shell config (working dir + folder guard)
	sessionID := c.sessionIDFromContext(ctx)
	sessionCfg := GetSessionShellConfig(sessionID)
	sessionEnv := common.GetSessionShellEnv(sessionID)
	// Block agent-browser browser-driving CLI calls via shell — catches direct calls,
	// bash -c wrapping, piping, etc. The agent_browser tool handles CDP URL
	// resolution, session tracking, and folder guard. Calling agent-browser directly
	// via shell bypasses all of this. Read-only built-in skill commands are allowed
	// so agents can load version-matched usage docs from the installed CLI.
	//
	// We strip heredoc bodies and quoted string literals before the substring check so that
	// commands embedding the literal text "agent-browser" as data (e.g. a python3 <<PYEOF
	// script rewriting plan descriptions that mention the CLI, or `echo "use agent-browser"`)
	// don't trip the guard. What remains after stripping is the executable part of the
	// command, where a real invocation would appear.
	cmdTrimmed := strings.TrimSpace(params.Command)
	if containsAgentBrowserInvocation(cmdTrimmed) {
		log.Printf("[SHELL] Blocked agent-browser CLI call. Command: %s", redactShellCommandForLog(params.Command))

		return ShellCommandResult{
			Stderr:   "ERROR: Do not call agent-browser directly via execute_shell_command for browser actions. Use the agent_browser tool instead.\n\nAllowed shell exception for docs only:\n  agent-browser skills list\n  agent-browser skills get core\n  agent-browser skills get core --full\n\nFor direct tool call mode:\n  agent_browser(command=\"open\", args=[\"https://example.com\"], session=\"default\")\n\nFor code execution mode (MCP bridge):\n  Call get_api_spec(tool_name=\"agent_browser\") to get the HTTP API spec,\n  then POST to the agent_browser endpoint via HTTP.\n\nThe agent_browser tool handles CDP connection, session management, and folder sandboxing automatically. Calling agent-browser CLI directly for browser actions bypasses CDP URL resolution and will fail inside Docker.",
			ExitCode: 1,
		}, nil
	}

	if rawCDP := detectRawChromeCDPAccess(params.Command); rawCDP != "" {
		log.Printf("[SHELL] Blocked raw Chrome CDP access via shell (%s). Command: %s", rawCDP, redactShellCommandForLog(params.Command))
		return ShellCommandResult{
			Stderr:   fmt.Sprintf("ERROR: Raw Chrome CDP access via execute_shell_command is blocked (%s).\n\nUse the agent_browser tool instead so shared-CDP tab selection and locking are enforced.\n\nAllowed replacements:\n  - Selected tab hint: agent_browser(command=\"tab\", args=[], session=\"<session>\")\n  - Select/create a tab: agent_browser(command=\"tab\", args=[\"<tab-id-or-label>\"], session=\"<session>\") or agent_browser(command=\"tab\", args=[\"new\", \"--label\", \"<label>\", \"https://example.com\"], session=\"<session>\")\n  - Navigate: agent_browser(command=\"open\", args=[\"https://example.com\"], session=\"<session>\")\n  - Run JavaScript: agent_browser(command=\"eval\", args=[\"tab\", \"<tab-id-or-label>\", \"document.title\"], session=\"<session>\")\n\nDo not use ws://localhost:9222/devtools/page, http://localhost:9222/json/*, websocket.create_connection(...9222...), or direct CDP Target/Runtime calls from shell. Those bypass the shared browser lock and can interfere with other running workflows.", rawCDP),
			ExitCode: 1,
		}, nil
	}

	redactedCommandForLog := redactShellCommandForLog(params.Command)

	// Set default working directory:
	// Priority: param > session config > client field > ExtraEnv. Non-workflow
	// callers may still leave it empty and use workspace root; workflow steps
	// fail closed below because root is never a valid implicit step cwd.
	if params.WorkingDirectory == "" {
		if sessionCfg != nil && sessionCfg.WorkingDir != "" {
			params.WorkingDirectory = sessionCfg.WorkingDir
		} else if c.DefaultWorkingDir != "" {
			params.WorkingDirectory = c.DefaultWorkingDir
		} else if dir, ok := clientEnv["_DEFAULT_WORKING_DIR"]; ok && dir != "" {
			params.WorkingDirectory = dir
		}
	}
	if params.WorkingDirectory == "" && isWorkflowStepShellRequest(clientEnv, sessionEnv, params.ExtraEnv) {
		log.Printf("[SHELL] Refusing workflow-step shell request with no cwd: session=%s cmd=%s", sessionID, redactedCommandForLog)
		return ShellCommandResult{}, fmt.Errorf(
			"WORKFLOW STEP CONFIG ERROR: execute_shell_command has no working directory for session %q; refusing workspace-root fallback",
			sessionID,
		)
	}

	// Populate folder guard configuration for the Isolator.
	// params.FolderGuard is never set by callers — it's always nil on entry.
	// Priority: session config > context keys > client-level fallback.
	if sessionConfigHasFolderGuard(sessionCfg) {
		// Session config: set by SetSessionFolderGuard() — highest priority.
		// Covers CLI/Gemini providers that bypass the Go folder guard context wrappers.
		// BlockedWritePaths flow through as the write-only deny list enforced by the
		// isolator at kernel-sandbox level (reads still permitted), independent of
		// WritePaths overlap.
		readPaths := sessionCfg.WritePaths
		if len(sessionCfg.ReadPaths) > 0 {
			readPaths = deduplicateStrings(append(sessionCfg.ReadPaths, sessionCfg.WritePaths...))
		}
		params.FolderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        sessionCfg.WritePaths,
			ReadPaths:         readPaths,
			BlockedPaths:      sessionCfg.BlockedPaths,
			BlockedWritePaths: sessionCfg.BlockedWritePaths,
			StrictAllowlist:   sessionCfg.StrictAllowlist,
			DenyNetwork:       sessionCfg.DenyNetwork,
		}
		log.Printf("[FOLDER_GUARD_RESOLVE] SessionConfig: session=%s WritePaths=%v ReadPaths=%v BlockedWritePaths=%v cmd=%s", sessionID, sessionCfg.WritePaths, readPaths, sessionCfg.BlockedWritePaths, redactedCommandForLog)
	} else if allowedWrites, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string); ok {
		// Context System 1: chat/plan/prototype mode
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := allowedWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = deduplicateStrings(append(ctxReads, allowedWrites...))
		}
		params.FolderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        allowedWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      contextPathList(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: contextPathList(ctx, common.FolderGuardBlockedWritePathsKey),
		}
		log.Printf("[FOLDER_GUARD_RESOLVE] System1 (chat/plan/prototype): URL=%s WritePaths=%v ReadPaths=%v cmd=%s", c.BaseURL, allowedWrites, readPaths, redactedCommandForLog)
	} else if ctxWrites, ok := ctx.Value(common.FolderGuardWritePathsKey).([]string); ok {
		// Context System 2: workflow orchestrator
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := ctxWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = deduplicateStrings(append(ctxReads, ctxWrites...))
		}
		params.FolderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        ctxWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      contextPathList(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: contextPathList(ctx, common.FolderGuardBlockedWritePathsKey),
		}
		log.Printf("[FOLDER_GUARD_RESOLVE] System2 (workflow): URL=%s WritePaths=%v ReadPaths=%v cmd=%s", c.BaseURL, ctxWrites, readPaths, redactedCommandForLog)
	} else if c.FolderGuard != nil && c.FolderGuard.Enabled {
		// Client-level fallback — no session config, no context keys.
		params.FolderGuard = cloneFolderGuard(c.FolderGuard)
		log.Printf("[FOLDER_GUARD_RESOLVE] FALLBACK to client-level guard: URL=%s ReadPaths=%v WritePaths=%v BlockedPaths=%v cmd=%s",
			c.BaseURL, c.FolderGuard.ReadPaths, c.FolderGuard.WritePaths, c.FolderGuard.BlockedPaths, redactedCommandForLog)
	} else {
		log.Printf("[FOLDER_GUARD_RESOLVE] NO folder guard at all: URL=%s cmd=%s", c.BaseURL, redactedCommandForLog)
	}
	if params.FolderGuard != nil && params.FolderGuard.Enabled &&
		len(params.FolderGuard.ReadPaths) == 0 && len(params.FolderGuard.WritePaths) == 0 {
		return ShellCommandResult{}, fmt.Errorf("ACCESS DENIED: execute_shell_command has no granted workspace paths")
	}
	if params.FolderGuard != nil {
		params.FolderGuard.BrowserSession = common.SandboxBrowserSession(sessionID)
	}

	// Block absolute host paths when folder guard is active.
	// Docker: VirtioFS auto-mounts /Users/ into containers, so absolute paths bypass sandbox.
	// Native: sandbox-exec profile only restricts workspace-docs subpaths, not the wider
	// filesystem, so this check is needed here too as defense-in-depth.
	if params.FolderGuard != nil && params.FolderGuard.Enabled {
		if err := blockAbsoluteHostPaths(params.Command, params.FolderGuard); err != nil {
			log.Printf("[FOLDER_GUARD] Blocked shell command with absolute host path: %s", redactedCommandForLog)
			return ShellCommandResult{}, err
		}
	}

	// Hard guard: never push the workflow's own secret files to a confirmed
	// public GitHub repo. Private / unknown / non-GitHub remotes are allowed so
	// full backups (incl. secrets to a private repo) are never blocked.
	if err := c.blockSecretPushToPublicRepo(ctx, params); err != nil {
		log.Printf("[SECRET_PUSH_GUARD] Blocked secret push to public repo: %s", redactedCommandForLog)
		return ShellCommandResult{}, err
	}
	if err := c.blockRecursiveGitBundle(ctx, params); err != nil {
		log.Printf("[BACKUP_CONTAINMENT_GUARD] Blocked recursive git bundle: %s", redactedCommandForLog)
		return ShellCommandResult{}, err
	}

	// Build an isolated environment for this request. Parallel Pulse reviewers
	// share a Client, so aliasing the client env here would let concurrent requests
	// write RUNLOOP_* and MCP shortcut variables into the same map.
	params.ExtraEnv = mergeShellCommandEnv(
		clientEnv,
		sessionEnv,
		params.ExtraEnv,
	)
	bridgeSessionID := strings.TrimSpace(params.BridgeSessionID)
	if bridgeSessionID == "" {
		bridgeSessionID = sessionID
	}
	bindShellBridgeSession(params.ExtraEnv, bridgeSessionID)
	// PLAT-280: a session granted direct database access (a scripted step)
	// must reach its command with DB_PATH. Agentic steps are denied raw
	// database access by design (the file is on their blocked list) and use
	// query_workflow_db / mutate_workflow_db instead, so their missing DB_PATH
	// is not an anomaly. Checked on the final environment. Log-only.
	if shellMissingGrantedDBPath(params.ExtraEnv, sessionCfg) {
		log.Printf("[PLAT-280] execute_shell_command: session %q was granted direct DB access (WORKFLOW_DB_ACCESS=%q) but its command has no DB_PATH",
			sessionID, params.ExtraEnv["WORKFLOW_DB_ACCESS"])
	}
	// Script-repair shells use the ordinary execute_shell_command executor, so
	// infer the same snapshot behavior from their trusted session capability.
	// The saved-script fast path sets DBReadSnapshot explicitly because it does
	// not run through a session-scoped agent executor.
	if !params.DBReadSnapshot &&
		strings.TrimSpace(sessionEnv["WORKFLOW_DB_ACCESS"]) == "read" &&
		strings.TrimSpace(params.ExtraEnv["DB_PATH"]) != "" {
		params.DBReadSnapshot = true
	}
	populateRunloopProcessEnv(params.ExtraEnv, sessionID, params.WorkingDirectory, params.Command)
	common.PopulateMCPBridgeShortEnv(params.ExtraEnv)

	path := "/api/execute"
	// Use a no-timeout HTTP client for shell execution. Shell commands can run
	// much longer than normal file operations, especially when they wrap a
	// call_sub_agent HTTP call that blocks until the sub-agent completes.
	respBody, err := c.requestWithTimeout(ctx, "POST", path, params, 0)
	if err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusRequestTimeout {
			// The workspace returns a structured 408 only after cmd.Start
			// succeeded and the running process exceeded its deadline. Preserve
			// partial output so callers do not misclassify it as a startup refusal.
			result := parseShellResponse(statusErr.body)
			result.TimedOut = true
			if result.ExitCode == 0 {
				result.ExitCode = -1
			}
			if result.Error == "" {
				result.Error = "command execution timed out"
			}
			result.Stderr += shellWorkingDirectoryHint(result, params.WorkingDirectory)
			return result, nil
		}
		return ShellCommandResult{}, err
	}

	// Parse the shell response into a typed struct.
	// Infrastructure errors (network, validation) are returned as Go errors above.
	// Command failures (non-zero exit code) are returned in the struct so callers
	// can inspect stdout/stderr to understand what went wrong.
	result := parseShellResponse(respBody)
	// Debug: log result size for call_sub_agent diagnostics
	if result.Stdout != "" && len(result.Stdout) < 200 && strings.Contains(params.Command, "call_sub_agent") {
		log.Printf("[SHELL_RESULT_DEBUG] call_sub_agent via shell: stdout_len=%d raw_resp_len=%d", len(result.Stdout), len(respBody))
	}
	result.Stderr += shellWorkingDirectoryHint(result, params.WorkingDirectory)
	return result, nil
}

// shellWorkingDirectoryHint tells a failed command where it actually ran.
//
// The cwd is per-session — SetSessionWorkingDir points a workshop tool-agent at
// its workflow folder and a step at its run execution folder — but the agent is
// only ever shown workspace-rooted paths like "Workflow/build-in-public/...".
// Nothing reported the difference, so an agent that prefixed its own workflow
// path got `cd: Workflow/build-in-public: No such file or directory` from inside
// Workflow/build-in-public, with no way to tell that from a missing folder. On
// 2026-08-04 that cost six calls across the Pulse reviewer, the Pulse fixer, and
// a dev step.
//
// Only failures carry the hint, so a working command pays nothing for it.
func shellWorkingDirectoryHint(result ShellCommandResult, workingDirectory string) string {
	workingDirectory = strings.TrimSpace(workingDirectory)
	if result.ExitCode == 0 || workingDirectory == "" {
		return ""
	}
	hint := fmt.Sprintf("\n[cwd] This command ran in %q. Paths are resolved against it, not the workspace docs root — do not prefix them with that directory again.", workingDirectory)
	if strings.HasSuffix(result.Stderr, "\n") {
		return strings.TrimPrefix(hint, "\n")
	}
	return hint
}

// isWorkflowStepShellRequest identifies shell calls owned by a workflow step
// before RUNLOOP_* inference runs. STEP_OUTPUT_DIR is registered on every
// agentic/scripted step session; RUNLOOP_STEP_ID covers callers that supply the
// ownership fields directly.
func isWorkflowStepShellRequest(envs ...map[string]string) bool {
	for _, env := range envs {
		if strings.TrimSpace(env["STEP_OUTPUT_DIR"]) != "" || strings.TrimSpace(env["RUNLOOP_STEP_ID"]) != "" {
			return true
		}
	}
	return false
}

// shellMissingGrantedDBPath reports a command that should have DB_PATH but
// does not: its session has workflow DB access and raw access to the
// database file is not blocked (only scripted steps get that), yet the final
// environment carries no DB_PATH.
func shellMissingGrantedDBPath(env map[string]string, cfg *common.SessionShellConfig) bool {
	access := strings.TrimSpace(env["WORKFLOW_DB_ACCESS"])
	// "none" is a product without platform stores (a Relay): no DB_PATH is the
	// intended state, not a missing grant.
	if access == "" || strings.EqualFold(access, "none") || strings.TrimSpace(env["DB_PATH"]) != "" {
		return false
	}
	if cfg == nil {
		return false
	}
	for _, blocked := range cfg.BlockedPaths {
		if strings.HasSuffix(filepath.ToSlash(strings.TrimSpace(blocked)), "/db/db.sqlite") {
			return false // agentic: raw database access withheld on purpose
		}
	}
	return true
}

// bindShellBridgeSession makes a shell's tool-call credentials those of the
// session it runs as, and only that one. The bridge token is per session
// (common/bridge_token.go), so it is derived last, after every env layer is
// merged, and only from the trusted session: sessionIDFromContext (the
// request context the token-bound bridge route or step wrapper set, else the
// client's own env), never from the command's extra_env, which the model
// controls. A model naming another session in extra_env still gets this
// shell's session and token; a shell with no trusted session gets no token.
func bindShellBridgeSession(env map[string]string, sessionID string) {
	if env == nil || !common.BridgeTokensEnabled() {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		for _, key := range []string{"MCP_SESSION_ID", "MCP_API_TOKEN", "MCP_AUTH"} {
			delete(env, key)
		}
		return
	}
	env["MCP_SESSION_ID"] = sessionID
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MCP_API_URL")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(env["MCP_API_URL"]), "/")
		if i := strings.Index(base, "/s/"); i >= 0 {
			base = base[:i]
		}
	}
	if base != "" {
		env["MCP_API_URL"] = base + "/s/" + sessionID
	}
	common.PopulateMCPBridgeShortEnv(env)
}

// mergeShellCommandEnv returns a new map for each request. Values are applied
// from lowest to highest priority: client defaults, session values, then
// explicit per-call values.
func mergeShellCommandEnv(clientEnv, sessionEnv, callEnv map[string]string) map[string]string {
	merged := make(map[string]string, len(clientEnv)+len(sessionEnv)+len(callEnv))
	for key, value := range clientEnv {
		merged[key] = value
	}
	for key, value := range sessionEnv {
		merged[key] = value
	}
	for key, value := range callEnv {
		merged[key] = value
	}
	return merged
}

func populateRunloopProcessEnv(env map[string]string, sessionID, workingDir, command string) {
	if env == nil {
		return
	}
	setDefault := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
	setDefault("RUNLOOP_SESSION_ID", sessionID)

	for _, candidate := range []string{
		env["STEP_OUTPUT_DIR"],
		env["STEP_EXECUTION_DIR"],
		workingDir,
		command,
	} {
		owner := inferRunloopOwnerFromPath(candidate)
		setDefault("RUNLOOP_WORKFLOW_ID", owner.WorkflowID)
		setDefault("RUNLOOP_RUN_ID", owner.RunID)
		setDefault("RUNLOOP_STEP_ID", owner.StepID)
		if env["RUNLOOP_WORKFLOW_ID"] != "" && env["RUNLOOP_RUN_ID"] != "" && env["RUNLOOP_STEP_ID"] != "" {
			break
		}
	}
	if env["RUNLOOP_WORKFLOW_ID"] != "" {
		setDefault("RUNLOOP_OWNER", "workflow")
	}
}

type runloopProcessOwner struct {
	WorkflowID string
	RunID      string
	StepID     string
}

func inferRunloopOwnerFromPath(raw string) runloopProcessOwner {
	slash := filepath.ToSlash(strings.TrimSpace(raw))
	if slash == "" {
		return runloopProcessOwner{}
	}
	parts := strings.Split(slash, "/")
	workflowIdx := -1
	for i, part := range parts {
		if part == "Workflow" {
			workflowIdx = i
			break
		}
	}
	if workflowIdx < 0 || workflowIdx+1 >= len(parts) {
		return runloopProcessOwner{}
	}
	owner := runloopProcessOwner{WorkflowID: parts[workflowIdx+1]}
	runsIdx := -1
	for i := workflowIdx + 2; i < len(parts); i++ {
		if parts[i] == "runs" {
			runsIdx = i
			break
		}
	}
	if runsIdx < 0 {
		return owner
	}
	executionIdx := -1
	for i := runsIdx + 1; i < len(parts); i++ {
		if parts[i] == "execution" {
			executionIdx = i
			break
		}
	}
	if executionIdx < 0 {
		return owner
	}
	if executionIdx > runsIdx+1 {
		owner.RunID = strings.Join(parts[runsIdx+1:executionIdx], "/")
	}
	if executionIdx+1 < len(parts) {
		owner.StepID = parts[executionIdx+1]
	}
	return owner
}

// containsAgentBrowserInvocation reports whether cmd appears to invoke the agent-browser
// CLI as an executable, rather than merely mentioning the string "agent-browser" inside
// data regions (heredoc bodies, quoted string literals).
//
// It strips heredoc bodies (<<EOF / <<'EOF' / <<"EOF" / <<-EOF) and single/double-quoted
// string literals, then does a substring check on what remains. This is a guardrail for
// a well-intentioned LLM, not a security boundary — adversarial constructs like
// $(printf agent)-browser are out of scope.
func containsAgentBrowserInvocation(cmd string) bool {
	executableText := strings.TrimSpace(stripShellDataRegions(cmd))
	if isAgentBrowserSkillsDocsCommand(executableText) {
		return false
	}
	return strings.Contains(executableText, "agent-browser")
}

// isAgentBrowserSkillsDocsCommand allows read-only agent-browser skill discovery.
// Browser-driving commands still must go through the managed agent_browser tool.
func isAgentBrowserSkillsDocsCommand(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) < 3 || fields[0] != "agent-browser" || fields[1] != "skills" {
		return false
	}
	switch fields[2] {
	case "list":
		return len(fields) == 3
	case "get":
		if len(fields) < 4 || strings.HasPrefix(fields[3], "-") {
			return false
		}
		for _, flag := range fields[4:] {
			if flag != "--full" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// detectRawChromeCDPAccess reports shell commands/scripts that talk directly to
// Chrome's remote debugging endpoint.
//
// Keep this narrow. Workflow steps often write plan/docs/scripts that mention CDP
// URLs as plain data. Those should pass. We only inspect executable shell text
// plus heredocs fed directly to an interpreter, and we require a CDP endpoint (or
// CDP URL variable) to appear together with code that actually opens or drives it.
func detectRawChromeCDPAccess(cmd string) string {
	if reason := detectRawChromeCDPInExecutableText(stripShellHeredocBodies(cmd)); reason != "" {
		return reason
	}
	for _, body := range executableHeredocBodies(cmd) {
		if reason := detectRawChromeCDPInExecutableText(body); reason != "" {
			return reason
		}
	}
	return ""
}

func detectRawChromeCDPInExecutableText(text string) string {
	lower := strings.ToLower(text)
	hasDevToolsPage := strings.Contains(lower, "ws://localhost:9222/devtools/page") ||
		strings.Contains(lower, "ws://127.0.0.1:9222/devtools/page") ||
		strings.Contains(lower, "ws://host.docker.internal:9222/devtools/page") ||
		strings.Contains(lower, "/devtools/page/")
	hasJSONEndpoint := strings.Contains(lower, "http://localhost:9222/json/") ||
		strings.Contains(lower, "http://127.0.0.1:9222/json/") ||
		strings.Contains(lower, "http://host.docker.internal:9222/json/") ||
		strings.Contains(lower, "localhost:9222/json/") ||
		strings.Contains(lower, "127.0.0.1:9222/json/") ||
		strings.Contains(lower, "host.docker.internal:9222/json/") ||
		strings.Contains(lower, "/json/list") ||
		strings.Contains(lower, "/json/version") ||
		strings.Contains(lower, "/json/new") ||
		strings.Contains(lower, "/json/close")
	hasCDPVariable := strings.Contains(lower, "var_twitter_cdp_url") ||
		strings.Contains(lower, "twitter_cdp_url") ||
		strings.Contains(lower, "cdp_url")
	hasCDPEndpointOrVariable := strings.Contains(lower, ":9222") ||
		strings.Contains(lower, "/devtools/page") ||
		strings.Contains(lower, "/json/list") ||
		strings.Contains(lower, "/json/version") ||
		strings.Contains(lower, "/json/new") ||
		strings.Contains(lower, "/json/close") ||
		hasCDPVariable

	hasWSOpen := strings.Contains(lower, "websocket.create_connection") ||
		strings.Contains(lower, "create_connection(") ||
		strings.Contains(lower, "new websocket(") ||
		strings.Contains(lower, "websocat ") ||
		strings.Contains(lower, "wscat ")
	if hasWSOpen && (hasDevToolsPage || hasCDPVariable) {
		return "raw CDP WebSocket connection"
	}

	hasHTTPFetch := strings.Contains(lower, "curl ") ||
		strings.Contains(lower, "curl\t") ||
		strings.Contains(lower, "wget ") ||
		strings.Contains(lower, "urlopen(") ||
		strings.Contains(lower, "requests.get(") ||
		strings.Contains(lower, "requests.post(") ||
		strings.Contains(lower, "httpx.get(") ||
		strings.Contains(lower, "httpx.post(") ||
		strings.Contains(lower, "fetch(") ||
		strings.Contains(lower, "axios.get(") ||
		strings.Contains(lower, "axios.post(") ||
		strings.Contains(lower, "http.get(") ||
		strings.Contains(lower, "https.get(")
	if hasHTTPFetch && (hasJSONEndpoint || hasCDPVariable) {
		return "Chrome /json target endpoint"
	}

	hasCDPMethod := strings.Contains(lower, "target.createtarget") ||
		strings.Contains(lower, "target.closetarget") ||
		strings.Contains(lower, "runtime.evaluate") ||
		strings.Contains(lower, "page.navigate") ||
		strings.Contains(lower, "input.dispatchmouseevent") ||
		strings.Contains(lower, "input.dispatchkeyevent")
	if hasCDPMethod && hasCDPEndpointOrVariable {
		return "Chrome CDP method call"
	}
	return ""
}

var executableHeredocCommandRe = regexp.MustCompile(`(?i)(^|[;&|]\s*)(python3?|node|ruby|perl|php|bash|sh|zsh|deno|tsx|npx\s+(?:tsx|ts-node))\b`)

func executableHeredocBodies(cmd string) []string {
	var bodies []string
	i := 0
	n := len(cmd)
	for i < n {
		if i+2 <= n && cmd[i] == '<' && cmd[i+1] == '<' {
			if loc := heredocStartRe.FindStringSubmatchIndex(cmd[i:]); loc != nil && loc[0] == 0 {
				lineStart := strings.LastIndex(cmd[:i], "\n") + 1
				prefix := strings.TrimSpace(cmd[lineStart:i])
				fullEnd := i + loc[1]
				var delim string
				for g := 1; g <= 3; g++ {
					if loc[2*g] >= 0 {
						delim = cmd[i+loc[2*g] : i+loc[2*g+1]]
						break
					}
				}

				i = fullEnd
				for i < n && cmd[i] != '\n' {
					i++
				}
				if i < n {
					i++
				}
				bodyStart := i
				bodyEnd := i
				for i < n {
					lineStart := i
					for i < n && cmd[i] != '\n' {
						i++
					}
					line := cmd[lineStart:i]
					if strings.TrimSpace(line) == delim {
						bodyEnd = lineStart
						if i < n {
							i++
						}
						break
					}
					if i < n {
						i++
					}
					bodyEnd = i
				}
				if executableHeredocCommandRe.MatchString(prefix) {
					bodies = append(bodies, cmd[bodyStart:bodyEnd])
				}
				continue
			}
		}
		i++
	}
	return bodies
}

// heredocStartRe matches the start of a heredoc redirection and captures the delimiter.
// Handles <<WORD, <<-WORD, <<'WORD', <<"WORD" (the <<- form strips leading tabs but the
// delimiter word itself is the same).
var heredocStartRe = regexp.MustCompile(`<<-?\s*(?:'([^']+)'|"([^"]+)"|([A-Za-z_][A-Za-z0-9_]*))`)

// stripShellDataRegions removes heredoc bodies and single/double-quoted string literals
// from a shell command, leaving the executable skeleton for substring inspection.
func stripShellDataRegions(cmd string) string {
	var out strings.Builder
	out.Grow(len(cmd))

	i := 0
	n := len(cmd)
	for i < n {
		// Heredoc: <<EOF ... \nEOF\n — strip body through matching delimiter line.
		if i+2 <= n && cmd[i] == '<' && cmd[i+1] == '<' {
			if loc := heredocStartRe.FindStringSubmatchIndex(cmd[i:]); loc != nil && loc[0] == 0 {
				full := cmd[i : i+loc[1]]
				var delim string
				for g := 1; g <= 3; g++ {
					if loc[2*g] >= 0 {
						delim = cmd[i+loc[2*g] : i+loc[2*g+1]]
						break
					}
				}
				out.WriteString(full)
				i += loc[1]
				// Advance to end of the current line (the heredoc body starts on the next line).
				for i < n && cmd[i] != '\n' {
					out.WriteByte(cmd[i])
					i++
				}
				if i < n {
					out.WriteByte('\n')
					i++
				}
				// Consume body up to a line whose trimmed content equals the delimiter.
				for i < n {
					lineStart := i
					for i < n && cmd[i] != '\n' {
						i++
					}
					line := cmd[lineStart:i]
					if i < n {
						i++ // consume newline
					}
					if strings.TrimSpace(line) == delim {
						// Preserve the delimiter line so later logic can still see command structure.
						out.WriteString(line)
						out.WriteByte('\n')
						break
					}
					// Body line — drop.
				}
				continue
			}
		}

		ch := cmd[i]
		// Single-quoted string: everything until the next single quote is literal.
		if ch == '\'' {
			out.WriteByte('\'')
			i++
			for i < n && cmd[i] != '\'' {
				i++
			}
			if i < n {
				out.WriteByte('\'')
				i++
			}
			continue
		}
		// Double-quoted string: strip body but keep the quotes. We don't try to honor
		// $(...) / `...` expansions inside — the guard is a guardrail, not a parser.
		if ch == '"' {
			out.WriteByte('"')
			i++
			for i < n && cmd[i] != '"' {
				if cmd[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i < n {
				out.WriteByte('"')
				i++
			}
			continue
		}
		out.WriteByte(ch)
		i++
	}
	return out.String()
}

// parseShellResponse parses the workspace API shell response into a typed ShellCommandResult.
func parseShellResponse(respBody []byte) ShellCommandResult {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return ShellCommandResult{Stdout: string(respBody)}
	}

	var result ShellCommandResult

	// Extract fields from the "data" envelope
	if data, ok := resp["data"]; ok {
		var dataFields struct {
			Stdout          string  `json:"stdout"`
			Stderr          string  `json:"stderr"`
			ExitCode        int     `json:"exit_code"`
			ExecutionTimeMs float64 `json:"execution_time_ms"`
		}
		if json.Unmarshal(data, &dataFields) == nil {
			result.Stdout = dataFields.Stdout
			result.Stderr = dataFields.Stderr
			result.ExitCode = dataFields.ExitCode
			result.ExecutionTimeMs = dataFields.ExecutionTimeMs
		}
	}

	// Include error if present
	if errVal, ok := resp["error"]; ok {
		var errStr string
		if json.Unmarshal(errVal, &errStr) == nil && errStr != "" {
			result.Error = errStr
		}
	}

	// Unwrap MCP API response from stdout to reduce JSON nesting for the LLM.
	// When the shell command calls an MCP/virtual tool via HTTP (e.g. call_sub_agent),
	// stdout contains: {"success":true,"result":"{...actual JSON...}"}
	// This unwraps it so the LLM sees the actual result directly.
	if result.ExitCode == 0 && result.Error == "" {
		stdout := strings.TrimSpace(result.Stdout)
		if unwrapped := tryUnwrapMCPAPIResponse(stdout); unwrapped != "" {
			result.Stdout = unwrapped
		}
	}

	return result
}

// tryUnwrapMCPAPIResponse attempts to unwrap a nested MCP API response.
// Input format: {"success":true,"result":"{...escaped JSON...}"}
// Returns the inner result string (which may itself be JSON) or "" if not applicable.
func tryUnwrapMCPAPIResponse(stdout string) string {
	if !strings.HasPrefix(stdout, "{") {
		return ""
	}

	var apiResp struct {
		Success bool   `json:"success"`
		Result  string `json:"result"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &apiResp); err != nil {
		return ""
	}

	// Only unwrap if it matches the MCP API response shape (has success + result/error)
	if !apiResp.Success && apiResp.Error != "" {
		// Retain the failure signal and the tool's actionable diagnostic body.
		// A generic success=false envelope must not erase missing scopes,
		// validation errors, or other reasons the agent needs to explain.
		if apiResp.Result != "" {
			return fmt.Sprintf("ERROR: %s\n\n%s", apiResp.Error, apiResp.Result)
		}
		return fmt.Sprintf("ERROR: %s", apiResp.Error)
	}

	if apiResp.Result == "" {
		return ""
	}

	// If the result is itself valid JSON, return it as-is (pretty for the LLM)
	var jsonCheck json.RawMessage
	if json.Unmarshal([]byte(apiResp.Result), &jsonCheck) == nil {
		return apiResp.Result
	}

	// Plain text result
	return apiResp.Result
}

// deduplicateStrings removes duplicate entries from a string slice while preserving order.
func deduplicateStrings(input []string) []string {
	seen := make(map[string]bool, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// blockAbsoluteHostPaths rejects commands containing absolute paths that
// reference the host filesystem outside the workspace.
//
// In Docker, VirtioFS auto-mounts /Users/ into containers — an LLM-generated
// command like "cat /Users/foo/secret.txt" would bypass workspace isolation.
// This function blocks /Users/, /home/, /root/ to prevent that.
//
// On desktop Mac the workspace root itself is also under /Users/, so absolute
// paths under workspace-docs must be allowed through. The workspace API
// receives the same FolderGuardConfig and enforces read/write restrictions in
// the sandbox layer; doing a second path-level rejection here turns expected
// sandbox denials into provider-level tool-call errors that agents cannot catch.
func blockAbsoluteHostPaths(command string, guard *FolderGuardConfig) error {
	if !strings.Contains(command, "/") || guard == nil {
		return nil
	}

	candidates := extractAbsoluteShellPaths(command)
	if len(candidates) == 0 {
		return nil
	}

	blockedDirs := []string{
		"/users",
		"/home",
		"/root",
	}

	for _, candidate := range candidates {
		if _, ok := normalizeAbsoluteWorkspacePath(candidate); ok {
			continue
		}
		if isAllowedAbsoluteHostPath(candidate, guard) {
			continue
		}

		candidateLower := strings.ToLower(candidate)
		for _, dir := range blockedDirs {
			if candidateLower == dir || strings.HasPrefix(candidateLower, dir+"/") {
				if hint := codingCLIToolResultSpillDenial(candidate); hint != "" {
					return errors.New(hint)
				}
				roots := workspaceDocsRoots()
				suggestion := closestWorkspaceRootHint(candidate, roots)
				return fmt.Errorf(
					"access denied: shell command references absolute host path (%s) that is not under any allowed workspace root. "+
						"Allowed workspace roots: %s. "+
						"%s"+
						"Use workspace-relative paths (e.g. 'Workflow/myproject/file.txt') or absolute paths under one of the allowed roots.",
					candidate, strings.Join(roots, ", "), suggestion,
				)
			}
		}
	}
	return nil
}

// codingCLIToolResultSpillDenial recognizes the one host path an agent is
// actively instructed to read, and replaces a useless denial with the only
// recovery that works.
//
// When a tool result exceeds a coding CLI's token cap the CLI truncates it,
// writes the full text under its own project directory
// (~/.claude/projects/<slug>/<session>/tool-results/…), and tells the agent to
// re-read it with offset and limit. That directory is outside every workspace
// root, so the guard denies it — and the generic denial then says "use
// workspace-relative paths", which cannot work: the file does not exist in the
// workspace and never will.
//
// Observed on 2026-08-04: a 103,685-character shell result spilled at 09:05:53
// and the read of that exact file was denied at 09:05:59. Three of the four host
// path denials that day were this same dead end. Granting read access is not the
// answer — the spill holds output this tool produced, so re-running the command
// with narrower output costs one call and keeps the boundary intact.
//
// Returning "" means this is not a spill path and the generic denial applies.
func codingCLIToolResultSpillDenial(candidate string) string {
	slashed := filepath.ToSlash(candidate)
	if !strings.Contains(slashed, "/tool-results/") && !strings.HasSuffix(slashed, "/tool-results") {
		return ""
	}
	if !strings.Contains(slashed, "/.claude/projects/") && !strings.Contains(slashed, "/.codex/") && !strings.Contains(slashed, "/.cursor/") {
		return ""
	}
	return fmt.Sprintf(
		"access denied: %s is a truncated tool-result spill written by your coding CLI, outside every workspace root. "+
			"It cannot be read, and no workspace-relative path points at it — do not look for one. "+
			"The output was too large for one result, so re-run the original command producing less: "+
			"filter with grep, take a slice with head/tail or sed -n '<start>,<end>p', select fields with jq/awk, "+
			"or redirect the full output into a file under your working directory and read it back in pieces.",
		candidate,
	)
}

func isAllowedAbsoluteHostPath(candidate string, guard *FolderGuardConfig) bool {
	if guard == nil {
		return false
	}
	cleanCandidate := filepath.Clean(candidate)
	// This host-managed CLI store is a separate runtime grant, not a new
	// workspace write permission. The execution host grants its own resolved
	// store independently, so caller-provided environment cannot widen access.
	if home := gogconfig.TerminalHome(guard.StrictAllowlist); home != "" && isPathUnder(cleanCandidate, filepath.Clean(home)) {
		return true
	}
	for _, allowed := range guard.WritePaths {
		if !filepath.IsAbs(allowed) {
			continue
		}
		if isPathUnder(cleanCandidate, filepath.Clean(allowed)) {
			return true
		}
	}
	for _, allowed := range guard.ReadPaths {
		if !filepath.IsAbs(allowed) {
			continue
		}
		if !isPathUnder(cleanCandidate, filepath.Clean(allowed)) {
			continue
		}
		for _, blockedWrite := range guard.BlockedWritePaths {
			if filepath.IsAbs(blockedWrite) && isPathUnder(cleanCandidate, filepath.Clean(blockedWrite)) {
				return true
			}
		}
	}
	return false
}

// closestWorkspaceRootHint returns a "Did you mean" hint when the rejected
// path's tail (everything after the first /Workflow, /skills, /Chats, etc.)
// could plausibly fit under one of the allowed workspace roots. Returns ""
// when no useful suggestion can be made — the caller passes through whatever
// it gets and the message reads cleanly either way.
func closestWorkspaceRootHint(rejected string, roots []string) string {
	clean := filepath.Clean(rejected)
	// Look for the first known top-level workspace folder name in the rejected
	// path; if found, the tail is what we'd append to a real root.
	knownTops := []string{"/Workflow/", "/skills/", "/Chats/", "/Downloads/", "/subagents/", "/memory/"}
	for _, top := range knownTops {
		if idx := strings.Index(clean, top); idx >= 0 {
			tail := clean[idx:] // e.g. "/Workflow/linkedin/..."
			for _, root := range roots {
				if root == "" {
					continue
				}
				return fmt.Sprintf("Did you mean: %s%s? ", filepath.Clean(root), tail)
			}
		}
	}
	return ""
}

func normalizeAbsoluteWorkspacePath(inputPath string) (string, bool) {
	cleanPath := filepath.Clean(inputPath)
	for _, root := range workspaceDocsRoots() {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if cleanPath == root {
			return ".", true
		}
		prefix := root + string(filepath.Separator)
		if strings.HasPrefix(cleanPath, prefix) {
			return strings.TrimPrefix(cleanPath, prefix), true
		}
	}
	return "", false
}

func workspaceDocsRoots() []string {
	roots := make([]string, 0, 8)
	if envRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")); envRoot != "" {
		roots = append(roots, envRoot)
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "workspace-docs")
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				roots = append(roots, candidate)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	roots = append(roots, "/app/workspace-docs", "/workspace-docs")
	return deduplicateStrings(roots)
}

func extractAbsoluteShellPaths(command string) []string {
	sanitized := stripShellHeredocBodies(command)
	seen := make(map[string]struct{})
	var paths []string

	addPath := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.TrimRight(raw, ",)")
		if raw == "" || !filepath.IsAbs(raw) {
			return
		}
		clean := filepath.Clean(raw)
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}

	for i := 0; i < len(sanitized); {
		switch sanitized[i] {
		case '\'':
			start := i + 1
			i++
			for i < len(sanitized) && sanitized[i] != '\'' {
				i++
			}
			if start < len(sanitized) {
				addPath(sanitized[start:i])
			}
			if i < len(sanitized) {
				i++
			}
		case '"':
			start := i + 1
			i++
			for i < len(sanitized) && sanitized[i] != '"' {
				if sanitized[i] == '\\' && i+1 < len(sanitized) {
					i += 2
					continue
				}
				i++
			}
			if start < len(sanitized) {
				addPath(sanitized[start:i])
			}
			if i < len(sanitized) {
				i++
			}
		default:
			if sanitized[i] == '/' && (i == 0 || isShellPathBoundary(sanitized[i-1])) {
				start := i
				i++
				for i < len(sanitized) && !isShellPathTerminator(sanitized[i]) {
					i++
				}
				addPath(sanitized[start:i])
				continue
			}
			i++
		}
	}

	return paths
}

func isShellPathBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '=', '(', ':', '<', '>':
		return true
	default:
		return false
	}
}

func isShellPathTerminator(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\'', '"', '|', '&', ';', '<', '>', ')':
		return true
	default:
		return false
	}
}

func stripShellHeredocBodies(cmd string) string {
	var out strings.Builder
	out.Grow(len(cmd))

	i := 0
	n := len(cmd)
	for i < n {
		if i+2 <= n && cmd[i] == '<' && cmd[i+1] == '<' {
			if loc := heredocStartRe.FindStringSubmatchIndex(cmd[i:]); loc != nil && loc[0] == 0 {
				full := cmd[i : i+loc[1]]
				var delim string
				for g := 1; g <= 3; g++ {
					if loc[2*g] >= 0 {
						delim = cmd[i+loc[2*g] : i+loc[2*g+1]]
						break
					}
				}
				out.WriteString(full)
				i += loc[1]
				for i < n && cmd[i] != '\n' {
					out.WriteByte(cmd[i])
					i++
				}
				if i < n {
					out.WriteByte('\n')
					i++
				}
				for i < n {
					lineStart := i
					for i < n && cmd[i] != '\n' {
						i++
					}
					line := cmd[lineStart:i]
					if i < n {
						i++
					}
					if strings.TrimSpace(line) == delim {
						out.WriteString(line)
						out.WriteByte('\n')
						break
					}
				}
				continue
			}
		}
		out.WriteByte(cmd[i])
		i++
	}
	return out.String()
}
