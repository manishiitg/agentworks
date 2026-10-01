package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

const cdpTabListTimeout = 15 * time.Second

// EnvAgentBrowserCDPEnabled controls whether this deployment permits agents to
// connect to an operator-owned Chrome instance over CDP. It defaults to true
// for backwards compatibility with desktop/local installations. Public and
// remote server deployments set it to false explicitly.
const EnvAgentBrowserCDPEnabled = "AGENT_BROWSER_CDP_ENABLED"

// CDPEnabled reports the deployment-level CDP capability. Workflow settings
// can narrow browser access further, but cannot override a disabled deployment
// capability.
func CDPEnabled() bool {
	raw, ok := os.LookupEnv(EnvAgentBrowserCDPEnabled)
	if !ok || strings.TrimSpace(raw) == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

// A coding CLI can accept much less than mcpagent's own bridge result limit.
// Snapshot output above this threshold must fail explicitly rather than being
// silently narrowed or truncated: the agent, not the runtime, chooses which
// evidence surface to inspect next.
const maxInlineSnapshotOutputRunes = 24000

// fullSnapshotFlag is an AgentWorks-level opt-in, not an agent-browser flag. It
// is stripped from the argument list before the CLI runs. It exists so the
// agent -- not the runtime -- decides when an oversized tree is worth its
// context cost, which is the same principle the cap itself was written to
// protect.
const fullSnapshotFlag = "--full-snapshot"

// execCmd is an alias so tests can swap it out; defaults to exec.Command.
var execCmd = exec.Command

// ExecutorOption is a functional option for configuring an Executor
type ExecutorOption func(*Executor)

// WithBrowserRuntimeConfig gives an executor a live, thread-safe source of
// configured browser intent. In auto mode the executor checks CDP reachability
// at every status/action call instead of caching a resolved mode in a prompt or
// chat runtime.
func WithBrowserRuntimeConfig(config *BrowserRuntimeConfig) ExecutorOption {
	return func(e *Executor) {
		e.RuntimeConfig = config
	}
}

// WithCdpPort configures the executor to connect to an existing Chrome via CDP on the given port
func WithCdpPort(port int) ExecutorOption {
	return func(e *Executor) {
		e.CdpPort = port
		e.CdpPorts = normalizeCDPPorts([]int{port})
	}
}

// WithCdpPorts authorizes a small, explicit set of independent Chrome CDP
// browsers for one run. Each port is expected to use a different Chrome
// --user-data-dir so it represents a separate login/profile. The agent must
// still name one authorized endpoint with --cdp on every tool call.
func WithCdpPorts(ports ...int) ExecutorOption {
	return func(e *Executor) {
		e.CdpPorts = normalizeCDPPorts(ports)
		if len(e.CdpPorts) > 0 {
			e.CdpPort = e.CdpPorts[0]
		} else {
			e.CdpPort = 0
		}
	}
}

// OversizedOutputSpiller persists content too large to return inline to a
// location the calling session can read back, returning the workspace-relative
// path it landed at. It must apply the same "only if this session actually has
// tool_output_folder read access" check the caller already verified — this
// type does not itself carry authority to write anywhere.
type OversizedOutputSpiller func(ctx context.Context, content string) (relPath string, err error)

// Executor handles the execution of browser tool commands
type Executor struct {
	Client         *Client
	CdpPort        int   // Legacy primary port; first entry in CdpPorts when configured.
	CdpPorts       []int // Explicitly authorized CDP browsers. Empty means headless.
	RuntimeConfig  *BrowserRuntimeConfig
	SpillOversized OversizedOutputSpiller // Optional; nil means "no recoverable spill available for this session."
}

// WithOversizedOutputSpiller wires a session-scoped write path for content that
// would otherwise be silently discarded when a snapshot exceeds the inline cap.
// The caller (not this package) is responsible for confirming, via the session's
// own already-granted folder guard, that the target directory is actually
// readable back by that session -- see workspace_browser_tools.go.
func WithOversizedOutputSpiller(spiller OversizedOutputSpiller) ExecutorOption {
	return func(e *Executor) {
		e.SpillOversized = spiller
	}
}

// NewExecutor creates a new browser executor
func NewExecutor(client *Client, opts ...ExecutorOption) *Executor {
	e := &Executor{
		Client: client,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func normalizeCDPPorts(ports []int) []int {
	seen := make(map[int]bool, len(ports))
	normalized := make([]int, 0, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		normalized = append(normalized, port)
	}
	return normalized
}

func (e *Executor) configuredCDPPorts() []int {
	if e.RuntimeConfig != nil {
		_, ports := e.RuntimeConfig.Snapshot()
		return normalizeCDPPorts(ports)
	}
	ports := append([]int{}, e.CdpPorts...)
	if e.CdpPort > 0 {
		ports = append([]int{e.CdpPort}, ports...)
	}
	return normalizeCDPPorts(ports)
}

func (e *Executor) configuredBrowserMode() string {
	if e.RuntimeConfig != nil {
		mode, _ := e.RuntimeConfig.Snapshot()
		return mode
	}
	if len(e.configuredCDPPorts()) > 0 {
		return "cdp"
	}
	return "headless"
}

type browserRuntimeStatus struct {
	ConfiguredMode      string         `json:"configured_mode"`
	EffectiveMode       string         `json:"effective_mode"`
	CDPSupported        bool           `json:"cdp_supported"`
	CDPDisabledReason   string         `json:"cdp_disabled_reason,omitempty"`
	ConfiguredCDPPorts  []int          `json:"configured_cdp_ports,omitempty"`
	ReachableCDPPorts   []int          `json:"reachable_cdp_ports,omitempty"`
	AuthorizedEndpoints []string       `json:"authorized_endpoints,omitempty"`
	HeadlessAvailable   bool           `json:"headless_available"`
	CheckErrors         map[int]string `json:"check_errors,omitempty"`
	Instruction         string         `json:"instruction"`
}

func (e *Executor) liveBrowserStatus(ctx context.Context) browserRuntimeStatus {
	mode := strings.ToLower(strings.TrimSpace(e.configuredBrowserMode()))
	ports := e.configuredCDPPorts()
	cdpEnabled := CDPEnabled()
	status := browserRuntimeStatus{
		ConfiguredMode:     mode,
		ConfiguredCDPPorts: append([]int(nil), ports...),
		HeadlessAvailable:  mode == "auto" || mode == "headless",
		CDPSupported:       cdpEnabled,
	}
	if !cdpEnabled {
		status.CDPDisabledReason = "CDP is disabled for this server deployment; managed headless Chromium remains available."
	}

	switch mode {
	case "auto", "cdp":
		if !cdpEnabled {
			break
		}
		status.CheckErrors = make(map[int]string)
		type result struct {
			index     int
			port      int
			connected bool
			message   string
			err       error
		}
		results := make(chan result, len(ports))
		for index, port := range ports {
			go func(index, port int) {
				connected, message, err := e.Client.CheckCDP(ctx, port)
				results <- result{index: index, port: port, connected: connected, message: message, err: err}
			}(index, port)
		}
		ordered := make([]result, len(ports))
		for range ports {
			item := <-results
			ordered[item.index] = item
		}
		for _, item := range ordered {
			if item.connected {
				status.ReachableCDPPorts = append(status.ReachableCDPPorts, item.port)
				continue
			}
			if item.err != nil {
				status.CheckErrors[item.port] = item.err.Error()
			} else if item.message != "" {
				status.CheckErrors[item.port] = item.message
			}
		}
		if len(status.CheckErrors) == 0 {
			status.CheckErrors = nil
		}
	}

	switch mode {
	case "auto":
		if !cdpEnabled {
			status.EffectiveMode = "headless"
			status.Instruction = "CDP is disabled for this server deployment. Call agent_browser without --cdp; Automatic mode uses managed headless Chromium here."
			break
		}
		if len(status.ReachableCDPPorts) > 0 {
			status.EffectiveMode = "cdp"
			status.AuthorizedEndpoints = ConfiguredCDPEndpoints(status.ReachableCDPPorts)
			status.Instruction = "Use one authorized --cdp endpoint on every subsequent agent_browser call."
		} else {
			status.EffectiveMode = "headless"
			status.Instruction = "CDP is not currently reachable; call agent_browser without --cdp. Call status again if Chrome becomes available."
		}
	case "cdp":
		if !cdpEnabled {
			status.EffectiveMode = "unavailable"
			status.Instruction = "CDP is disabled for this server deployment. Change browser_mode to auto, headless, or none; --cdp cannot override this policy."
			break
		}
		if len(status.ReachableCDPPorts) > 0 {
			status.EffectiveMode = "cdp"
			status.AuthorizedEndpoints = ConfiguredCDPEndpoints(status.ReachableCDPPorts)
			status.Instruction = "Use one authorized --cdp endpoint on every subsequent agent_browser call."
		} else {
			status.EffectiveMode = "unavailable"
			status.Instruction = "Configured CDP is not currently reachable. Start the configured Chrome profile and call status again."
		}
	case "headless":
		status.EffectiveMode = "headless"
		status.Instruction = "Call agent_browser without --cdp."
	case "none":
		status.EffectiveMode = "none"
		status.HeadlessAvailable = false
		status.Instruction = "Browser access is disabled for this run."
	default:
		status.EffectiveMode = mode
		status.Instruction = "Browser configuration is invalid; update the workflow browser_mode."
	}
	if status.EffectiveMode == "headless" {
		status.Instruction += " Managed browsing is persistent per workflow and shared by its authorized users, runs and steps. Unrelated workflows use separate browsers. Session labels do not create browsers. Reuse an existing tab or open a new tab only when useful; preserve tabs and sign-ins at step completion."
	}
	return status
}

func isBrowserOpenCommand(command string) bool {
	return command == "open" || command == "goto" || command == "navigate"
}

// isBrowserDocumentationCommand reports commands that only read the
// version-matched documentation bundled with the installed agent-browser CLI.
// They do not connect to, inspect, or mutate a browser page, so CDP tab
// ownership and the shared select/action lock do not apply.
func isBrowserDocumentationCommand(command string) bool {
	return command == "skills"
}

func cdpExclusiveFeatureAction(command string, args []string) (feature, action string) {
	command = strings.ToLower(strings.TrimSpace(command))
	if len(args) == 0 {
		return "", ""
	}
	switch command {
	case "record":
		return "video recording", strings.ToLower(strings.TrimSpace(args[0]))
	case "trace":
		return "DevTools trace", strings.ToLower(strings.TrimSpace(args[0]))
	case "profiler":
		return "DevTools profile", strings.ToLower(strings.TrimSpace(args[0]))
	case "network":
		if len(args) >= 2 && strings.EqualFold(strings.TrimSpace(args[0]), "har") {
			return "HAR capture", strings.ToLower(strings.TrimSpace(args[1]))
		}
	}
	return "", ""
}

func isCDPRecordingTransition(command, action string) bool {
	return command == "record" && (action == "start" || action == "restart")
}

func canRunBeforeRecordingSnapshot(command, action string) bool {
	if command == "snapshot" {
		return true
	}
	return command == "record" && (action == "stop" || action == "restart")
}

type cdpArgInfo struct {
	found bool
	url   string
	port  int
}

// extractFullSnapshotArg pulls the AgentWorks-only --full-snapshot flag out of
// the agent-browser argument list. Returns whether it was present and the args
// with every occurrence removed.
func extractFullSnapshotArg(args []string) (bool, []string) {
	found := false
	cleaned := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) == fullSnapshotFlag {
			found = true
			continue
		}
		cleaned = append(cleaned, arg)
	}
	return found, cleaned
}

func extractCDPArg(args []string) (cdpArgInfo, []string, error) {
	var info cdpArgInfo
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] != "--cdp" {
			cleaned = append(cleaned, args[i])
			continue
		}
		if info.found {
			return cdpArgInfo{}, nil, fmt.Errorf("--cdp may only be provided once")
		}
		if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
			return cdpArgInfo{}, nil, fmt.Errorf("--cdp requires a non-empty URL")
		}
		cdpURL := strings.TrimSpace(args[i+1])
		i++
		info = cdpArgInfo{found: true, url: cdpURL, port: parseCDPPort(cdpURL)}
	}
	return info, cleaned, nil
}

func parseCDPPort(cdpURL string) int {
	if port, err := strconv.Atoi(strings.TrimSpace(cdpURL)); err == nil && port >= 1 && port <= 65535 {
		return port
	}
	parsed, err := url.Parse(cdpURL)
	if err == nil && parsed.Port() != "" {
		if port, convErr := strconv.Atoi(parsed.Port()); convErr == nil {
			return port
		}
	}
	if strings.Contains(cdpURL, ":9222") {
		return 9222
	}
	return 0
}

func resolveCDPInvocation(configuredPorts []int, cdpArg cdpArgInfo) (int, string, error) {
	configuredPorts = normalizeCDPPorts(configuredPorts)
	if len(configuredPorts) > 0 {
		if !cdpArg.found {
			return 0, "", fmt.Errorf(
				"CDP mode requires an explicit --cdp endpoint on every agent_browser call. This run authorizes port(s) %v; add [\"--cdp\", %q] to args",
				configuredPorts,
				resolveCdpURL(configuredPorts[0]),
			)
		}
		for _, configuredPort := range configuredPorts {
			if cdpArg.port == configuredPort {
				return configuredPort, resolveCdpURL(configuredPort), nil
			}
		}
		endpoints := make([]string, 0, len(configuredPorts))
		for _, configuredPort := range configuredPorts {
			endpoints = append(endpoints, resolveCdpURL(configuredPort))
		}
		return 0, "", fmt.Errorf(
			"CDP_CONFIGURATION_MISMATCH: this run authorizes CDP port(s) %v, but the tool call requested %q. Use one of the configured endpoints: %v",
			configuredPorts,
			cdpArg.url,
			endpoints,
		)
	}

	if cdpArg.found {
		return 0, "", fmt.Errorf(
			"CDP_NOT_CONFIGURED: this browser tool is configured for headless mode and cannot switch to the model-supplied endpoint %q. Select CDP mode for the run first",
			cdpArg.url,
		)
	}
	return 0, "", nil
}

// HandleAgentBrowser executes the agent-browser CLI command
func (e *Executor) HandleAgentBrowser(ctx context.Context, args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}
	command = strings.ToLower(strings.TrimSpace(command))
	// Status is a backend-owned live capability query. It never launches
	// agent-browser and intentionally does not require or accept a --cdp value.
	if command == "status" {
		statusJSON, err := json.MarshalIndent(e.liveBrowserStatus(ctx), "", "  ")
		if err != nil {
			return "", err
		}
		return string(statusJSON), nil
	}
	argsArray := stringArgs(args["args"])
	// --full-snapshot is an AgentWorks-level flag, not an agent-browser one: strip
	// it before the CLI ever sees it, or agent-browser rejects it as an unknown
	// subcommand. It is the agent's explicit opt-in to receive an oversized
	// snapshot inline, accepting the context cost, instead of the spill+grep path.
	fullSnapshotRequested, argsArray := extractFullSnapshotArg(argsArray)
	cdpArg, argsWithoutCDP, cdpArgErr := extractCDPArg(argsArray)
	if cdpArgErr != nil {
		return "", cdpArgErr
	}
	argsWithoutCDP = stripRedundantTabCommandArg(command, argsWithoutCDP)

	// Build command arguments
	var cmdArgs []string

	// The agent must declare CDP explicitly so its tool trace remains mode-aware,
	// while the executor remains authoritative for the actual endpoint. This keeps
	// a headless run from switching itself to arbitrary CDP hosts/ports and rejects
	// prompt/config drift instead of silently connecting somewhere unexpected.
	configuredMode := strings.ToLower(strings.TrimSpace(e.configuredBrowserMode()))
	configuredPorts := e.configuredCDPPorts()
	if configuredMode == "none" {
		return "", fmt.Errorf("BROWSER_DISABLED: browser access is disabled for this run")
	}
	if !CDPEnabled() {
		if configuredMode == "cdp" || cdpArg.found {
			return "", fmt.Errorf("CDP_DISABLED: CDP is disabled for this server deployment; use browser_mode=auto or headless and call agent_browser without --cdp")
		}
		// On server deployments Automatic is deliberately headless-only. Do not
		// probe configured ports or accidentally grant host-browser access.
		if configuredMode == "auto" {
			configuredPorts = nil
		}
	}
	if configuredMode == "auto" && CDPEnabled() {
		status := e.liveBrowserStatus(ctx)
		if status.EffectiveMode == "cdp" {
			configuredPorts = status.ReachableCDPPorts
		} else {
			configuredPorts = nil
			if cdpArg.found {
				return "", fmt.Errorf("CDP_UNAVAILABLE: no configured CDP endpoint is currently reachable; call agent_browser(command=\"status\", session=%q) and follow its live effective_mode", args["session"])
			}
		}
	}
	cdpPort, cdpURL, cdpResolveErr := resolveCDPInvocation(configuredPorts, cdpArg)
	if cdpResolveErr != nil {
		return "", cdpResolveErr
	}
	isCdpMode := cdpPort > 0
	if command == "capture" && isCdpMode {
		return "", fmt.Errorf("CAPTURE_UNSUPPORTED: bundled capture requires the managed headless browser; CDP supports the separate record, network HAR, console and errors commands")
	}

	log.Printf("[BROWSER] mode=%s command=%s session=%s", map[bool]string{true: "cdp", false: "headless"}[isCdpMode], command, args["session"])

	// Add session flag (required)
	session, ok := args["session"].(string)
	if !ok || session == "" {
		return "", fmt.Errorf("session is required")
	}

	// Get agent-level and workflow-level session IDs from context.
	// agentSessionID identifies the workflow/browser owner.
	// workflowSessionID = always the parent/root workflow session ID.
	agentSessionID := ""
	if sid, ok := ctx.Value(common.ChatSessionIDKey).(string); ok {
		agentSessionID = sid
	}
	workflowSessionID := ""
	if wid, ok := ctx.Value(common.WorkflowSessionIDKey).(string); ok {
		workflowSessionID = wid
	}
	// Fallback: if no workflow session set, use agent session (non-workflow/chat mode)
	if workflowSessionID == "" {
		workflowSessionID = agentSessionID
	}
	// The conversation that owns its own tab in a shared managed browser
	// (session_tabs.go). CDP keeps its own per-port owner (cdpOwnerID).
	sessionTabOwner := sessionTabOwnerID(agentSessionID, workflowSessionID)
	if isCdpMode {
		// Auto mode may become CDP after the surrounding request was assembled.
		// Grant the host Downloads read path at the moment CDP is actually used,
		// rather than relying on a stale request-time effective mode.
		if agentSessionID != "" {
			common.GrantSessionCDPHostDownloadsReadWrite(agentSessionID, "cdp")
		}
		if workflowSessionID != "" && workflowSessionID != agentSessionID {
			common.GrantSessionCDPHostDownloadsReadWrite(workflowSessionID, "cdp")
		}
	}

	if isCdpMode {
		sharedSession := sharedCDPSessionName(cdpPort)
		if session != sharedSession {
			log.Printf("[BROWSER] CDP: remapped session %q -> %q for shared browser port %d", session, sharedSession, cdpPort)
			session = sharedSession
		}
	} else {
		ownerSessionID := agentSessionID
		if cfg := common.GetSessionShellConfig(ownerSessionID); cfg == nil || cfg.BrowserSessionNamespace == "" {
			ownerSessionID = workflowSessionID
		}
		resolvedSession := common.ResolveBrowserSessionID(ownerSessionID, session)
		if SharedBrowserEnabled() && !IsUserBrowserSession(resolvedSession) {
			resolvedSession = SharedSessionName
		}
		if resolvedSession != "" && resolvedSession != session {
			log.Printf("[BROWSER] remapped session %q -> %q for agent=%q", session, resolvedSession, agentSessionID)
			session = resolvedSession
		}
	}

	if !isCdpMode {
		cmdArgs = append(cmdArgs, HeadlessLaunchArgsForSession(session)...)
	}

	log.Printf("[BROWSER] session=%q agent=%q workflow=%q command=%q", session, agentSessionID, workflowSessionID, command)

	if !isCdpMode {
		release, err := AcquireBrowserAutomation(ctx, session)
		if err != nil {
			return "", err
		}
		defer release()
		workspace := ""
		for _, id := range []string{agentSessionID, workflowSessionID} {
			if cfg := common.GetSessionShellConfig(id); captureWorkspace(common.SessionUserIDFromContext(ctx), cfg) != "" {
				workspace = captureWorkspace(common.SessionUserIDFromContext(ctx), cfg)
				break
			}
		}
		if !(command == "capture" && len(argsWithoutCDP) == 1 && argsWithoutCDP[0] == "status") {
			if conflict := GetSessionTracker().CaptureConflict(session, workflowSessionID, workspace, command); conflict != "" {
				return "", fmt.Errorf("BROWSER_RECORDING_BUSY: %s", conflict)
			}
		}
	}

	// Track headless browser sessions to prevent unbounded growth.
	// CDP mode connects to the user's real browser, so it is tracked separately
	// via the per-port owner registry (cdp_registry.go) instead of this tracker.
	isHeadless := !isCdpMode
	tracker := GetSessionTracker()

	if isHeadless && session != SharedSessionName {
		isOpenCommand := isBrowserOpenCommand(command) || (command == "tab" && len(argsWithoutCDP) > 0 && argsWithoutCDP[0] == "new")

		if isOpenCommand {
			// Auto-evict helper: kills runtime + removes files + unregisters from tracker.
			// Used for both per-agent and global eviction so the new session can proceed
			// without the LLM needing to close things manually.
			autoEvict := func(evictSession string) {
				release, ok := TryTakeBrowserControl(evictSession)
				if !ok {
					return
				}
				defer release()
				if tracker.Recording(evictSession) {
					return
				}
				log.Printf("[BROWSER_TRACKER] Auto-evicting session %q to free slot for %q", evictSession, session)
				killSessionRuntimeFully(evictSession)
				tracker.Remove(evictSession)
			}

			// Per-agent limit: if this agent already has too many sessions, evict its
			// oldest one so the new open can proceed. Mirrors the global-evict behavior.
			if agentSessionID != "" && tracker.CountForAgent(agentSessionID) >= MaxBrowserSessionsPerAgent {
				oldest := tracker.GetOldestSessionForAgent(agentSessionID)
				if oldest != "" && oldest != session {
					autoEvict(oldest)
				}
			}

			// Per-workflow limit: evict oldest workflow session if needed.
			if workflowSessionID != "" && tracker.CountForWorkflow(workflowSessionID) >= MaxBrowserSessionsPerWorkflow {
				// Find oldest workflow session that isn't the one being opened.
				wfSessions := tracker.SessionsForWorkflow(workflowSessionID)
				var oldestWf string
				for _, s := range wfSessions {
					if s != session {
						oldestWf = s
						break
					}
				}
				if oldestWf != "" {
					autoEvict(oldestWf)
				}
			}

			// Check global limit — auto-evict oldest if needed
			if !tracker.HasSession(session) && tracker.Count() >= MaxBrowserSessionsGlobal {
				oldest := tracker.GetOldestSession()
				if oldest != "" && oldest != session {
					log.Printf("[BROWSER_TRACKER] Global limit (%d) reached, auto-closing oldest: %q",
						MaxBrowserSessionsGlobal, oldest)
					autoEvict(oldest)
				}
				if tracker.Count() >= MaxBrowserSessionsGlobal {
					return "", fmt.Errorf("BROWSER_CAPACITY: all browser slots are busy or recording; retry after a slot is released")
				}
			}

			log.Printf("[BROWSER_TRACKER] Opening browser: browser=%q agent=%q workflow=%q (agent_count=%d, wf_count=%d, global=%d)",
				session, agentSessionID, workflowSessionID,
				tracker.CountForAgent(agentSessionID), tracker.CountForWorkflow(workflowSessionID), tracker.Count())
		}

		// Track the session: only register new sessions on open commands.
		// Non-open commands only update lastUsed if already tracked (prevents
		// non-open commands from bypassing limits by pre-registering sessions).
		if isOpenCommand {
			tracker.Touch(session, agentSessionID, workflowSessionID)
		} else {
			tracker.TouchExisting(session)
		}

		// Log every command for debugging
		log.Printf("[BROWSER_TRACKER] Command: browser=%q agent=%q cmd=%q (agent_count=%d, wf_count=%d, global=%d)",
			session, agentSessionID, command,
			tracker.CountForAgent(agentSessionID), tracker.CountForWorkflow(workflowSessionID), tracker.Count())

		// If closing, remove from tracker
		if (command == "close" || command == "quit" || command == "exit") && !SessionHasOtherTabOwners(session, sessionTabOwner) {
			log.Printf("[BROWSER_TRACKER] Closing browser: browser=%q agent=%q", session, agentSessionID)
			defer tracker.Remove(session)
		}
	}

	cmdArgs = append(cmdArgs, "--session", session)

	// Add the command
	cmdArgs = append(cmdArgs, command)

	openTabArg := ""
	commandInputArgs := argsWithoutCDP
	if isBrowserOpenCommand(command) {
		if tab, cleaned, ok, stripErr := normalizeOpenCommandArgs(command, argsWithoutCDP); stripErr != nil {
			return "", stripErr
		} else if ok {
			openTabArg = tab
			commandInputArgs = cleaned
			log.Printf("[BROWSER] stripped inline tab %q from %q args; agent-browser open receives URL-only args", tab, command)
		}
	}
	tabArgs := commandInputArgs
	commandArgs := commandInputArgs
	inlineCDPTab := ""
	if isCdpMode {
		var inlineErr error
		inlineCDPTab, commandArgs, inlineErr = extractInlineCDPTab(tabArgs)
		if inlineErr != nil {
			return "", inlineErr
		}
		if inlineCDPTab == "" && openTabArg != "" {
			inlineCDPTab = openTabArg
		}
	}
	commandArgs = normalizeAgentBrowserCommandArgs(command, commandArgs)
	// Recover the unambiguous bare tN form before any command-specific planning
	// or subprocess argument construction. Previously this happened in the CDP
	// selection block below, after commandArgs had already been copied into
	// cmdArgs. The executor would therefore select t2 correctly but still invoke
	// agent-browser with `click t2 e64`, where t2 was misread as the element ref.
	if isCdpMode && inlineCDPTab == "" && command != "tab" && !isBrowserDocumentationCommand(command) {
		if recovered, cleaned := unmarkedCDPTabArg(commandArgs); recovered != "" {
			log.Printf("[BROWSER] CDP: recovered unmarked tab %q from %q args; treat it as --tab %s", recovered, command, recovered)
			inlineCDPTab = recovered
			commandArgs = cleaned
		}
	}
	if isBrowserOpenCommand(command) && len(commandArgs) > 0 {
		log.Printf("[BROWSER] navigation: browser=%q agent=%q workflow=%q command=%q tab=%q target=%q",
			session, agentSessionID, workflowSessionID, command, inlineCDPTab, displayCDPTabURL(commandArgs[0]))
	}
	if command == "tab" && len(commandArgs) > 0 && commandArgs[0] == "new" {
		newTabRequest, newTabErr := parseNewCDPTabRequest(commandArgs)
		if newTabErr != nil {
			return "", newTabErr
		}
		commandArgs = canonicalNewCDPTabArgs(newTabRequest)
		tabArgs = commandArgs
	}

	// Determine timeout
	timeout := getTimeoutForCommand(command)

	// Resolve folder guard and working directory — same priority as execute_shell_command.
	// This ensures agent_browser has identical sandboxing to shell commands.
	var folderGuard *FolderGuardConfig
	workingDir := "."

	// Look up per-session config (working dir + folder guard)
	var sessionCfg *common.SessionShellConfig
	if agentSessionID != "" {
		sessionCfg = common.GetSessionShellConfig(agentSessionID)
	}
	// Fallback: try workflow session if agent session didn't have config
	if sessionCfg == nil && workflowSessionID != "" && workflowSessionID != agentSessionID {
		sessionCfg = common.GetSessionShellConfig(workflowSessionID)
	}

	// Working directory priority: browser downloads path > session config > default
	if downloadsPath, ok := ctx.Value(common.BrowserDownloadsPathKey).(string); ok && downloadsPath != "" {
		workingDir = downloadsPath
	} else if sessionCfg != nil && sessionCfg.WorkingDir != "" {
		workingDir = sessionCfg.WorkingDir
	}

	// Folder guard priority: session config > context system 1 > context system 2
	if sessionCfg != nil && (sessionCfg.FolderGuardSet || len(sessionCfg.ReadPaths) > 0 || len(sessionCfg.WritePaths) > 0 ||
		len(sessionCfg.BlockedPaths) > 0 || len(sessionCfg.BlockedWritePaths) > 0) {
		readPaths := sessionCfg.WritePaths
		if len(sessionCfg.ReadPaths) > 0 {
			readPaths = common.DeduplicateStrings(append(sessionCfg.ReadPaths, sessionCfg.WritePaths...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        sessionCfg.WritePaths,
			ReadPaths:         readPaths,
			BlockedPaths:      sessionCfg.BlockedPaths,
			BlockedWritePaths: sessionCfg.BlockedWritePaths,
		}
	} else if allowedWrites, ok := ctx.Value(common.FolderGuardAllowedWriteFolderKey).([]string); ok {
		// Context System 1: chat/plan/prototype mode
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := allowedWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = common.DeduplicateStrings(append(ctxReads, allowedWrites...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        allowedWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      browserContextGuardPaths(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: browserContextGuardPaths(ctx, common.FolderGuardBlockedWritePathsKey),
		}
	} else if ctxWrites, ok := ctx.Value(common.FolderGuardWritePathsKey).([]string); ok {
		// Context System 2: workflow orchestrator
		ctxReads, hasCtxReads := ctx.Value(common.FolderGuardReadPathsKey).([]string)
		readPaths := ctxWrites
		if hasCtxReads && len(ctxReads) > 0 {
			readPaths = common.DeduplicateStrings(append(ctxReads, ctxWrites...))
		}
		folderGuard = &FolderGuardConfig{
			Enabled:           true,
			WritePaths:        ctxWrites,
			ReadPaths:         readPaths,
			BlockedPaths:      browserContextGuardPaths(ctx, common.FolderGuardBlockedPathsKey),
			BlockedWritePaths: browserContextGuardPaths(ctx, common.FolderGuardBlockedWritePathsKey),
		}
	}
	if folderGuard != nil && len(folderGuard.ReadPaths) == 0 && len(folderGuard.WritePaths) == 0 {
		return "", fmt.Errorf("ACCESS DENIED: agent_browser has no granted workspace paths")
	}
	if folderGuard != nil {
		folderGuard.BrowserSession = common.SandboxBrowserSession(agentSessionID)
		if folderGuard.BrowserSession == "" && workflowSessionID != "" {
			folderGuard.BrowserSession = common.SandboxBrowserSession(workflowSessionID)
		}
	}

	// Execute via client
	opts := &ExecuteOptions{
		Timeout:          timeout,
		FolderGuard:      folderGuard,
		WorkingDirectory: workingDir,
	}
	if command == "capture" {
		return e.handleCapture(ctx, session, argsWithoutCDP, sessionCfg, folderGuard)
	}

	// A persistent agent-browser daemon can outlive the sandbox that launched
	// it. Broker named outputs through managed staging, and broker upload inputs
	// into a temporary path the persistent daemon can read. workspace-api checks
	// both directions against this request's current FolderGuard.
	// Unguarded local calls keep their existing direct-path behavior.
	var artifactPlan *browserArtifactPlan
	var uploadPlan *browserUploadPlan
	commandOpts := opts
	if folderGuard != nil && folderGuard.Enabled {
		artifactOwner := cdpOwnerID(workflowSessionID, agentSessionID, session, cdpSharedConnectionIdentityFor(isCdpMode, cdpPort))
		cloned := *opts
		optionsChanged := false
		var artifactErr error
		artifactPlan, artifactErr = prepareBrowserArtifact(command, commandArgs, artifactOwner, session)
		if artifactErr != nil {
			return "", artifactErr
		}
		if artifactPlan != nil {
			commandArgs = artifactPlan.RewrittenArgs
			cloned.ArtifactTransfer = artifactPlan.Transfer
			optionsChanged = true
		}
		var uploadErr error
		uploadPlan, uploadErr = prepareBrowserUploads(command, commandArgs)
		if uploadErr != nil {
			return "", uploadErr
		}
		if uploadPlan != nil {
			commandArgs = uploadPlan.RewrittenArgs
			cloned.UploadTransfers = append([]UploadTransfer(nil), uploadPlan.Transfers...)
			optionsChanged = true
			defer cleanupBrowserUploadPlan(uploadPlan)
		}
		if optionsChanged {
			commandOpts = &cloned
		}
	}

	for _, argStr := range commandArgs {
		cmdArgs = append(cmdArgs, argStr)
	}
	if isCdpMode {
		cmdArgs = append(cmdArgs, "--cdp", cdpURL)
		log.Printf("[BROWSER] CDP: using %s", cdpURL)
	}

	// Always add --json for machine-readable output
	cmdArgs = append(cmdArgs, "--json")

	cdpOwner := ""
	cdpSelectedTabForCommand := ""
	var recordingTabsBefore []cdpTabInfo
	recordingOriginalTab := ""
	recordingPreviousTab := ""
	if isCdpMode && !isBrowserDocumentationCommand(command) {
		cdpOwner = cdpOwnerID(workflowSessionID, agentSessionID, session, cdpSharedConnectionIdentityFor(isCdpMode, cdpPort))
		// Keep delayed ownership cleanup aware of direct Builder/browser calls as
		// well as orchestrated workflow runs. A surrounding run lease keeps the
		// owner active; without one, this renews cleanup to one hour after the
		// latest CDP command.
		AcquireCDPTabOwnerLease(cdpOwner, []int{cdpPort})
		defer ReleaseCDPTabOwnerLease(cdpOwner, []int{cdpPort}, e.Client, DefaultCDPTabCleanupDelay)
		unlock, err := acquireSharedCDPLock(ctx, cdpPort)
		if err != nil {
			return "", err
		}
		defer unlock()
		touchCDPOwner(cdpPort, cdpOwner)

		if command == "reset" {
			// Reset kills the shared daemon and wipes every owner's tab state —
			// only allowed when no other workflow is actively using this port.
			if err := guardCDPReset(cdpPort, cdpOwner); err != nil {
				return "", err
			}
			clearCDPTabSelectionsForPort(cdpPort)
		} else if command == "tab" {
			if _, _, err := parseTabSelection(tabArgs); err != nil {
				return "", err
			}
			if _, recording := getCDPRecordingHandoff(cdpPort, cdpOwner); recording && !isTabListRequest(tabArgs) {
				return "", fmt.Errorf("RECORDING_CONTEXT_ACTIVE: tab selection/creation is blocked while this workflow is recording. Stop the recording first; normal page actions are automatically routed to the recording tab")
			}
			if len(tabArgs) > 0 && tabArgs[0] == "new" {
				newTabRequest, parseErr := parseNewCDPTabRequest(tabArgs)
				if parseErr != nil {
					return "", parseErr
				}
				if reusedOutput, reused, reuseErr := e.reuseCDPTabForNew(ctx, session, cdpURL, opts, cdpPort, cdpOwner, newTabRequest); reuseErr != nil {
					return "", reuseErr
				} else if reused {
					return reusedOutput, nil
				}
				if err := guardCDPTabCreation(cdpPort, cdpOwner); err != nil {
					return "", err
				}
			}
			if isTabListRequest(tabArgs) {
				return e.listCDPTabsForUser(ctx, session, cdpURL, opts, cdpPort, cdpOwner), nil
			}
		} else {
			tabForCommand := inlineCDPTab
			if tabForCommand == "" && isBrowserOpenCommand(command) {
				tabForCommand = getCDPTabSelection(cdpPort, cdpOwner)
			}
			if handoff, recording := getCDPRecordingHandoff(cdpPort, cdpOwner); recording {
				_, action := cdpExclusiveFeatureAction(command, commandArgs)
				if handoff.NeedsSnapshot && !canRunBeforeRecordingSnapshot(command, action) {
					return "", fmt.Errorf("RECORDING_CONTEXT_CHANGED: video recording is active in fresh tab %q, but it has not been snapshotted yet. Discard refs from the original tab %q and call snapshot now; subsequent actions will be routed to the recording tab automatically", handoff.RecordingTab, handoff.OriginalTab)
				}
				if tabForCommand != "" && tabForCommand != handoff.RecordingTab {
					log.Printf("[BROWSER] CDP recording handoff: routing %q from requested tab %q to recording tab %q", command, tabForCommand, handoff.RecordingTab)
				}
				tabForCommand = handoff.RecordingTab
			}
			if tabForCommand == "" {
				if isBrowserOpenCommand(command) {
					return "", fmt.Errorf("CDP shared-browser mode requires selecting or creating a tab before %q.\n\n%s\n\nUse agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"<tab-id-or-label>\"]) if you already know the tab id/label, or agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"new\", \"--label\", \"<label>\", \"<url>\"]) to create one. Then call agent_browser(command=\"open\", args=[\"--cdp\", %q, \"https://example.com\"]).", command, selectedCDPTabMessage(cdpPort, cdpOwner), cdpURL, cdpURL, cdpURL)
				}
				return "", missingCDPPageActionTabError(cdpPort, command, commandArgs, selectedCDPTabMessage(cdpPort, cdpOwner))
			}
			selectedTab, err := e.selectCDPTabForCommand(ctx, session, cdpURL, opts, cdpPort, cdpOwner, tabForCommand, command)
			if err != nil {
				return "", err
			}
			setCDPActiveTab(cdpPort, selectedTab)
			cdpSelectedTabForCommand = selectedTab
			log.Printf("[BROWSER] CDP: verified target tab %q before %q", selectedTab, command)
		}
	}
	// Managed headless browser shared by several conversations (a Crew's or a
	// workflow's): each conversation gets and keeps its own tab, selected before
	// every command while AcquireBrowserAutomation above is held.
	sessionTabNote := ""
	if !isCdpMode {
		routed, routeErr := e.routeSessionOwnerTab(ctx, session, sessionTabOwner, command, commandArgs, opts)
		if routeErr != nil {
			return "", routeErr
		}
		if routed.handled {
			return routed.output, nil
		}
		sessionTabNote = routed.note
	}

	exclusiveFeature, exclusiveAction := cdpExclusiveFeatureAction(command, commandArgs)
	if isCdpMode && exclusiveFeature == "video recording" && exclusiveAction == "start" {
		if handoff, recording := getCDPRecordingHandoff(cdpPort, cdpOwner); recording {
			return "", fmt.Errorf("RECORDING_ALREADY_ACTIVE: this workflow is already recording in tab %q. Call record stop, or record restart to begin a new capture", handoff.RecordingTab)
		}
	}
	exclusiveFeatureClaimed := false
	if isCdpMode && exclusiveFeature != "" {
		switch exclusiveAction {
		case "start", "restart":
			var claimErr error
			exclusiveFeatureClaimed, claimErr = claimCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
			if claimErr != nil {
				return "", claimErr
			}
		case "stop":
			if err := guardCDPExclusiveFeatureStop(cdpPort, cdpOwner, exclusiveFeature); err != nil {
				return "", err
			}
		}
	}
	if isCdpMode && isCDPRecordingTransition(command, exclusiveAction) {
		if current, recording := getCDPRecordingHandoff(cdpPort, cdpOwner); recording {
			recordingOriginalTab = current.OriginalTab
			recordingPreviousTab = current.RecordingTab
		} else {
			recordingOriginalTab = cdpSelectedTabForCommand
		}
		listed, listErr := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout))
		if listErr != nil {
			if exclusiveFeatureClaimed {
				releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
			}
			return "", fmt.Errorf("cannot safely start CDP recording because the current tab set could not be captured: %w", listErr)
		}
		var parseErr error
		recordingTabsBefore, parseErr = parseCDPTabs(listed)
		if parseErr != nil {
			if exclusiveFeatureClaimed {
				releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
			}
			return "", fmt.Errorf("cannot safely start CDP recording because the current tab set was invalid: %w", parseErr)
		}
	}

	// reset: force-kill the daemon and wipe session files without touching the
	// agent-browser binary. Works even when CDP is dead. Use this when open/close
	// keep failing — it gives a completely clean slate for the next open.
	if command == "reset" {
		log.Printf("[BROWSER] Reset requested for session %q — killing daemon and clearing state", session)
		if isCdpMode {
			resetCDPSessionRuntime(session)
			clearCDPTabSelectionsForPort(cdpPort)
			clearCDPExclusiveFeaturesForPort(cdpPort)
		} else {
			killSessionRuntimeFully(session)
		}
		if isHeadless {
			tracker.Remove(session)
		}
		return `{"success":true,"message":"session reset — ready for fresh open"}`, nil
	}

	if isHeadless && session != SharedSessionName && !isBrowserOpenCommand(command) {
		// Baseline reading for whatever command is about to run against an
		// already-open session, so a "dead session" failure a moment later can
		// be compared against what was actually alive right before the attempt.
		logPreKillDiagnostics(session)
	}
	output, err := e.Client.ExecuteCommand(ctx, cmdArgs, commandOpts)
	if err != nil && isHeadless && strings.Contains(err.Error(), "timed out after") {
		// The daemon is stuck behind the page: capture evidence straight from
		// Chrome so the caller learns whether the page is busy or blocked.
		if summary := captureHangDiagnostics(session, command); summary != "" {
			err = fmt.Errorf("%w. Diagnosis: %s", err, summary)
		}
	}

	// After a successful open/navigate, record Chrome's PID so killSessionRuntime can
	// kill it reliably even if Chrome has been reparented (daemon auto-relaunch race).
	isOpenCommand := isBrowserOpenCommand(command) || (command == "tab" && len(argsWithoutCDP) > 0 && argsWithoutCDP[0] == "new")
	if err == nil && isOpenCommand {
		captureChromePID(session)
	}
	if err == nil && isHeadless {
		tracker.RecordAction(session, command, argsWithoutCDP)
	}

	// isDeadSession returns true for errors that mean the browser session no longer
	// exists and we need to start fresh. Error strings come from agent-browser/Chrome:
	//   "CDP response channel closed"   — Chrome crashed while connected
	//   "No such file or directory"     — daemon socket was deleted (e.g. after a reset
	//                                     that ran concurrently with this command)
	//   "ProcessSingleton"              — Chrome refused to launch because a stale
	//                                     SingletonLock/SingletonSocket/SingletonCookie
	//                                     was left behind by a previous crash of THIS
	//                                     session's own Chrome (the old process is
	//                                     already gone, only its lock files remain)
	isDeadSession := func(e error) bool {
		if e == nil {
			return false
		}
		msg := e.Error()
		return strings.Contains(msg, "CDP response channel closed") ||
			strings.Contains(msg, "No such file or directory") ||
			strings.Contains(msg, "ProcessSingleton")
	}

	// Auto-recover from dead sessions — Chrome crashed or the daemon socket was removed.
	// Kill the daemon process first (so it releases its port/socket), then
	// remove session files so the retry starts a completely fresh runtime + Chrome.
	if isDeadSession(err) {
		canRecover := true
		if isCdpMode {
			if recoveryErr := guardCDPAutomaticRecovery(cdpPort, cdpOwner); recoveryErr != nil {
				canRecover = false
				log.Printf("[BROWSER] Dead shared CDP session detected for %q, but automatic reset is unsafe: %v", session, recoveryErr)
			}
		}
		if canRecover {
			log.Printf("[BROWSER] Dead session detected for %q (%v), killing stale runtime and retrying", session, err)
			logPreKillDiagnostics(session)
			killSessionRuntimeFully(session)
			if isCdpMode {
				clearCDPActiveTabForPort(cdpPort)
				clearCDPExclusiveFeaturesForPort(cdpPort)
			}
			// If this was a close command, there's nothing left to close — declare success.
			if command == "close" || command == "quit" || command == "exit" {
				log.Printf("[BROWSER] Session %q already dead, treating close as success", session)
				return `{"success":true,"message":"session already closed"}`, nil
			}
			// Brief pause so the OS can reclaim memory from the killed Chrome/daemon
			// before we launch a new Chrome. Without this, the retry often hits OOM
			// immediately because the killed process hasn't fully released its pages yet.
			time.Sleep(2 * time.Second)
			if isCdpMode && command != "tab" && command != "reset" {
				tabForRetry := inlineCDPTab
				if tabForRetry == "" && isBrowserOpenCommand(command) {
					tabForRetry = getCDPTabSelection(cdpPort, cdpOwner)
				}
				if tabForRetry != "" {
					selectedTab, selectErr := e.selectCDPTabForCommand(ctx, session, cdpURL, opts, cdpPort, cdpOwner, tabForRetry, command)
					if selectErr != nil {
						if exclusiveFeatureClaimed {
							releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
						}
						return "", selectErr
					}
					setCDPActiveTab(cdpPort, selectedTab)
					log.Printf("[BROWSER] CDP: re-verified target tab %q before retrying %q", selectedTab, command)
				}
			}
			output, err = e.Client.ExecuteCommand(ctx, cmdArgs, commandOpts)
			// On successful retry, record Chrome PID for the fresh session.
			if err == nil && isOpenCommand {
				captureChromePID(session)
			}
			// A ProcessSingleton failure on the retry itself (not just the
			// original error) means 2s wasn't enough for the OS to finish
			// tearing down the just-killed Chrome/daemon before this profile's
			// singleton files were recreated -- confirmed live on SparkQuill:
			// the automatic retry failed with the identical error, but a
			// manual retry moments later against the same profile succeeded
			// immediately. One bounded extra attempt with a longer pause
			// covers that timing gap instead of surfacing a transient race as
			// a hard failure the user has to retry by hand.
			if err != nil && strings.Contains(err.Error(), "ProcessSingleton") {
				log.Printf("[BROWSER] Retry for %q still hit ProcessSingleton, giving the OS more time and trying once more", session)
				logPreKillDiagnostics(session)
				killSessionRuntimeFully(session)
				time.Sleep(5 * time.Second)
				output, err = e.Client.ExecuteCommand(ctx, cmdArgs, commandOpts)
				if err == nil && isOpenCommand {
					captureChromePID(session)
				}
			}
		}
	}

	if err != nil && isCdpMode && isCommandTimeoutError(err) && shouldRetryCDPTimeout(command) {
		log.Printf("[BROWSER] CDP command timed out for session %q (%v), retrying once", session, err)
		if !isBrowserDocumentationCommand(command) {
			if recoveryErr := guardCDPAutomaticRecovery(cdpPort, cdpOwner); recoveryErr == nil {
				resetCDPSessionRuntime(session)
				clearCDPActiveTabForPort(cdpPort)
				clearCDPExclusiveFeaturesForPort(cdpPort)
			} else {
				log.Printf("[BROWSER] Retrying timed-out read without resetting shared CDP runtime: %v", recoveryErr)
			}
		}
		time.Sleep(500 * time.Millisecond)
		output, err = e.Client.ExecuteCommand(ctx, cmdArgs, commandOpts)
	} else if err != nil && isCdpMode && isCommandTimeoutError(err) {
		log.Printf("[BROWSER] CDP command %q timed out for session %q (%v); not retrying a potentially side-effecting or non-idempotent operation", command, session, err)
		err = fmt.Errorf("CDP_COMMAND_OUTCOME_UNKNOWN: %q timed out and was not automatically retried because it may have changed browser or application state. Inspect the current tab with snapshot/get before deciding whether to retry: %w", command, err)
	}

	if err != nil && isCdpMode && isCDPRuntimeStartupError(err) {
		log.Printf("[BROWSER] CDP runtime startup failed for session %q (%v), retrying once", session, err)
		if recoveryErr := guardCDPAutomaticRecovery(cdpPort, cdpOwner); recoveryErr == nil {
			resetCDPSessionRuntime(session)
			clearCDPActiveTabForPort(cdpPort)
			clearCDPExclusiveFeaturesForPort(cdpPort)
		} else {
			log.Printf("[BROWSER] Retrying startup without resetting shared CDP runtime: %v", recoveryErr)
		}
		time.Sleep(500 * time.Millisecond)
		output, err = e.Client.ExecuteCommand(ctx, cmdArgs, commandOpts)
	}

	// If the final error is a dead session on an open command, remove it from the
	// tracker immediately. Otherwise it sits tracked-but-dead and blocks the next
	// open attempt with LIMIT EXCEEDED until the LLM remembers to call close.
	if err != nil && isOpenCommand && isDeadSession(err) && isHeadless {
		log.Printf("[BROWSER_TRACKER] open failed with dead session for %q — removing from tracker so next open isn't blocked", session)
		tracker.Remove(session)
	}

	if err != nil {
		if artifactPlan != nil && artifactPlan.CleanupOnError && artifactPlan.StagedPath != "" {
			_ = os.Remove(artifactPlan.StagedPath)
		}
		if exclusiveFeatureClaimed && !isCommandTimeoutError(err) {
			releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
		}
		if isCdpMode && isCDPUnavailableError(err) {
			return "", cdpUnavailableError(cdpPort, err)
		}
		return "", err
	}

	recordingContextNote := ""
	if isCdpMode && isCDPRecordingTransition(command, exclusiveAction) {
		listed, listErr := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout))
		var recordingTab string
		if listErr == nil {
			if afterTabs, parseErr := parseCDPTabs(listed); parseErr == nil {
				recordingTab, listErr = findCDPRecordingTab(recordingTabsBefore, afterTabs, recordingOriginalTab)
			} else {
				listErr = parseErr
			}
		}
		if listErr != nil || recordingTab == "" {
			// Do not leave a capture running when the adapter cannot guarantee that
			// subsequent actions will reach it. This stop is best-effort; the
			// caller receives a hard error instead of false video evidence.
			_, _ = e.Client.ExecuteCommand(ctx, []string{"--session", session, "record", "stop", "--cdp", cdpURL, "--json"}, opts)
			if previous, ok := clearCDPRecordingHandoff(cdpPort, cdpOwner); ok {
				clearCDPTabStateForOwner(cdpPort, cdpOwner, previous.RecordingTab)
			}
			releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
			if artifactPlan != nil && artifactPlan.StagedPath != "" {
				_ = os.Remove(artifactPlan.StagedPath)
			}
			return "", fmt.Errorf("CDP_RECORDING_HANDOFF_FAILED: recording was stopped because its fresh tab could not be identified safely: %w", listErr)
		}
		handoff := cdpRecordingHandoff{
			OriginalTab:   recordingOriginalTab,
			RecordingTab:  recordingTab,
			NeedsSnapshot: true,
		}
		setCDPRecordingHandoff(cdpPort, cdpOwner, handoff)
		markCDPTabOwned(cdpPort, cdpOwner, "__recording__"+recordingTab, recordingTab)
		if recordingPreviousTab != "" && recordingPreviousTab != recordingTab {
			if closeOutput, closeErr := e.Client.ExecuteCommand(ctx, []string{"--session", session, "tab", "close", recordingPreviousTab, "--cdp", cdpURL, "--json"}, opts); closeErr != nil {
				log.Printf("[BROWSER] CDP recording restart: could not close replaced temporary tab %q: %v (%s)", recordingPreviousTab, closeErr, strings.TrimSpace(closeOutput))
			} else {
				clearCDPTabStateForOwner(cdpPort, cdpOwner, recordingPreviousTab)
			}
		}
		recordingContextNote = fmt.Sprintf("AGENTWORKS_RECORDING_CONTEXT: recording is active in fresh tab %q (original tab %q). Discard every old element ref and call snapshot now. Until record stop, AgentWorks automatically routes this workflow's page actions to %q; do not select or create another tab.", recordingTab, recordingOriginalTab, recordingTab)
		log.Printf("[BROWSER] CDP recording handoff: owner=%q original=%q recording=%q", cdpOwner, recordingOriginalTab, recordingTab)
	}
	if isCdpMode && command == "record" && exclusiveAction == "stop" {
		if handoff, recording := clearCDPRecordingHandoff(cdpPort, cdpOwner); recording {
			closeOutput, closeErr := e.Client.ExecuteCommand(ctx, []string{"--session", session, "tab", "close", handoff.RecordingTab, "--cdp", cdpURL, "--json"}, opts)
			if closeErr == nil {
				clearCDPTabStateForOwner(cdpPort, cdpOwner, handoff.RecordingTab)
			} else {
				log.Printf("[BROWSER] CDP recording handoff: could not close temporary tab %q after stop: %v (%s)", handoff.RecordingTab, closeErr, strings.TrimSpace(closeOutput))
			}
			if handoff.OriginalTab != "" {
				if restored, restoreErr := e.selectCDPTabForCommand(ctx, session, cdpURL, opts, cdpPort, cdpOwner, handoff.OriginalTab, "record stop restore"); restoreErr == nil {
					setCDPTabSelection(cdpPort, cdpOwner, restored)
					setCDPActiveTab(cdpPort, restored)
					recordingContextNote = fmt.Sprintf("AGENTWORKS_RECORDING_CONTEXT: recording stopped, temporary tab %q was closed, and original tab %q was restored. The recording context is gone; take a fresh snapshot before continuing.", handoff.RecordingTab, restored)
				} else {
					clearCDPTabSelection(cdpPort, cdpOwner)
					recordingContextNote = fmt.Sprintf("AGENTWORKS_RECORDING_CONTEXT: recording stopped and temporary tab %q was closed, but original tab %q is no longer available. Select or create a tab before continuing.", handoff.RecordingTab, handoff.OriginalTab)
				}
			}
		}
	}

	if artifactPlan != nil {
		if artifactPlan.StoreLeaseOnSuccess {
			if previous, ok := getBrowserArtifactLease(artifactPlan.LeaseKey); ok && previous.Transfer != nil && previous.Transfer.SourcePath != artifactPlan.StagedPath {
				_ = os.Remove(previous.Transfer.SourcePath)
			}
			setBrowserArtifactLease(artifactPlan.LeaseKey, browserArtifactLease{
				Transfer:      artifactPlan.Transfer,
				RequestedPath: artifactPlan.RequestedPath,
			})
		}
		if artifactPlan.DeleteLeaseOnSuccess {
			deleteBrowserArtifactLease(artifactPlan.LeaseKey)
		}
		output = rewriteBrowserArtifactOutput(output, artifactPlan)
	}

	if isCdpMode && command == "tab" {
		if tab, clear, parseErr := parseTabSelection(tabArgs); parseErr == nil {
			if clear {
				clearCDPTabStateForOwner(cdpPort, cdpOwner, tab)
				log.Printf("[BROWSER] CDP: cleared tab %q for owner=%q port=%d", tab, cdpOwner, cdpPort)
			} else if tab != "" {
				activeTab := tab
				if tabID := findCDPTabID(output, tab); tabID != "" {
					setCDPTabAlias(cdpPort, cdpOwner, tab, tabID)
					activeTab = tabID
				}
				if len(tabArgs) > 0 && tabArgs[0] == "new" && !isCDPTabID(activeTab) {
					if refreshed, refreshErr := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout)); refreshErr == nil {
						if tabID := findCDPTabID(refreshed, tab); tabID != "" {
							setCDPTabAlias(cdpPort, cdpOwner, tab, tabID)
							activeTab = tabID
						}
					}
				}
				setCDPTabSelection(cdpPort, cdpOwner, activeTab)
				if len(tabArgs) > 0 && tabArgs[0] == "new" {
					if isCDPTabID(activeTab) {
						markCDPTabOwned(cdpPort, cdpOwner, tab, activeTab)
						log.Printf("[BROWSER] CDP: registered workflow-created tab %q (%s) for delayed cleanup owner=%q port=%d", tab, activeTab, cdpOwner, cdpPort)
					} else {
						log.Printf("[BROWSER] CDP: tab %q was created but its real tN id could not be resolved; skipping ownership registration", tab)
					}
				}
				setCDPActiveTab(cdpPort, activeTab)
				log.Printf("[BROWSER] CDP: selected tab %q for owner=%q port=%d", activeTab, cdpOwner, cdpPort)
			}
		}
		message := selectedCDPTabMessage(cdpPort, cdpOwner)
		if len(tabArgs) > 0 && tabArgs[0] == "new" {
			if request, requestErr := parseNewCDPTabRequest(tabArgs); requestErr == nil {
				if created, found := findCreatedCDPTab(output, request.Label); found {
					message = formatCDPTabIdentity("Created CDP tab", created) + "\n" + message
				} else {
					message = fmt.Sprintf("Created CDP tab label=%q, but agent-browser did not return its real tab id; refusing to treat the label as a durable tab identity.\n%s", request.Label, message)
				}
			}
		}
		return message, nil
	}
	if isCdpMode && exclusiveFeature != "" && exclusiveAction == "stop" {
		releaseCDPExclusiveFeature(cdpPort, cdpOwner, exclusiveFeature)
	}
	if isBrowserDocumentationCommand(command) {
		return formatAgentBrowserSkillsOutput(output), nil
	}
	if command == "snapshot" {
		if handled, err := e.handleOversizedSnapshot(ctx, &output, fullSnapshotRequested); err != nil {
			return "", err
		} else if handled {
			return output, nil
		}
		if isCdpMode {
			if handoff, recording := getCDPRecordingHandoff(cdpPort, cdpOwner); recording && handoff.NeedsSnapshot {
				markCDPRecordingSnapshotReady(cdpPort, cdpOwner)
				recordingContextNote = fmt.Sprintf("AGENTWORKS_RECORDING_CONTEXT: fresh snapshot accepted for recording tab %q. Subsequent page actions are now automatically routed to this recorded context.", handoff.RecordingTab)
			}
		}
	}
	if recordingContextNote != "" {
		output = strings.TrimSpace(output) + "\n\n" + recordingContextNote
	}
	if sessionTabNote != "" {
		output = strings.TrimSpace(output) + "\n\n" + sessionTabNote
	}

	return output, nil
}

// handleOversizedSnapshot applies the inline size cap to a snapshot result.
//
// It used to return an error and DISCARD the output, which cost the step a full
// round trip and left it choosing a narrower --selector/--depth without ever
// having seen the tree. Measured live 2026-08-17 (confida-login): 4 snapshots at
// ~30.4k runes against the 24k cap, each one a blind retry.
//
// The original rationale is kept intact -- a snapshot must never be SILENTLY
// narrowed, because a truncated accessibility tree reads exactly like a page
// where the element is absent, and "not found" is the wrong conclusion to hand a
// QA step. So the head is returned with a loud, unmissable incompleteness banner
// rather than quietly. The agent still chooses what to inspect next; it just now
// chooses with evidence instead of without.
//
// Returns handled=true when it has produced the final result for this call.
func (e *Executor) handleOversizedSnapshot(ctx context.Context, output *string, fullSnapshotRequested bool) (bool, error) {
	runeCount := len([]rune(*output))
	if runeCount <= maxInlineSnapshotOutputRunes {
		return false, nil
	}
	if fullSnapshotRequested {
		// Explicit opt-in: hand back the whole tree. Anything past the bridge's
		// own 128KB result cap is truncated and persisted to tool_output_folder
		// by mcpbridge, which message_sequence steps can now read (see
		// setupMessageSequenceFolderGuard).
		*output = fmt.Sprintf("SNAPSHOT_FULL (%d runes, %d bytes) -- returned inline because %s was requested.\n\n%s",
			runeCount, len(*output), fullSnapshotFlag, *output)
		return true, nil
	}
	full := *output
	head := string([]rune(full)[:maxInlineSnapshotOutputRunes])

	// PLAT-200: the default truncated path used to just discard everything past
	// the head -- unlike the --full-snapshot path above, nothing spilled it
	// anywhere, so the only way to recover the rest was a second, full-cost
	// snapshot call. If this session's caller has confirmed (via its own
	// granted folder guard, not a path this package invents) that it can write
	// somewhere the session can also read back, persist the full tree there so
	// the agent gets a real recoverable path instead of only a re-run option.
	spilledPath := ""
	if e.SpillOversized != nil {
		if p, err := e.SpillOversized(ctx, full); err == nil && strings.TrimSpace(p) != "" {
			spilledPath = strings.TrimSpace(p)
		}
	}
	recoveryLine := fmt.Sprintf("  - agent_browser(command=\"snapshot\", args=[..., %q])            -- this exact snapshot in full, inline (re-runs the command)", fullSnapshotFlag)
	if spilledPath != "" {
		// execute_shell_command is the registered tool that can read the session's
		// already-granted tool_output_folder. Do not advertise the test-only
		// read_workspace_file helper here: an unavailable recovery command turns a
		// successful spill back into an unrecoverable snapshot.
		recoveryLine = fmt.Sprintf("  - execute_shell_command(command=\"sed -n '1,240p' %q\") -- read this saved tree in chunks; use later line ranges as needed (no re-run)", spilledPath)
	}
	*output = fmt.Sprintf(`SNAPSHOT_TRUNCATED: showing the first %d of %d runes (%d bytes).

*** THIS TREE IS INCOMPLETE. An element missing from the text below may still exist on the page -- do NOT record it as absent, and do not assert a negative from this output. ***

To get what you need, pick one -- but choose based on WHY the tree below is
large, not by trying options in order. --depth only shrinks a tree that is
large because it is deeply NESTED; on a WIDE/FLAT page (many sibling rows,
cards, or list items at a similar shallow depth -- look at the indentation
below), raising or lowering --depth can return a byte-identical result and
waste the retry, since the size comes from sibling count, not nesting:
  - agent_browser(command="snapshot", args=[..., "--selector", "<css>"])  -- scope to the region you actually need (cheapest, works for wide OR deep trees)
  - agent_browser(command="snapshot", args=[..., "--compact"])           -- strip empty structural wrappers (helps on noisy markup)
  - agent_browser(command="snapshot", args=[..., "--interactive"])       -- only clickable/typeable elements (helps when you only need to act, not read)
  - agent_browser(command="snapshot", args=[..., "--depth", "<n>"])      -- a shallower tree (only helps if the head below looks deeply nested, not wide)
%s

%s`, maxInlineSnapshotOutputRunes, runeCount, len(*output), recoveryLine, head)
	return true, nil
}

func formatAgentBrowserSkillsOutput(output string) string {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return raw
	}

	var payload struct {
		Success bool `json:"success"`
		Data    []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Content     string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || !payload.Success || len(payload.Data) == 0 {
		return raw
	}

	const adapterNote = "Builder adapter note: this is version-matched upstream documentation from the installed agent-browser CLI. Treat its `agent-browser ...` shell examples as logical commands and invoke them through the managed `agent_browser` tool. In CDP mode, preserve the configured `--cdp` prefix on every call."
	sections := make([]string, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if content := strings.TrimSpace(entry.Content); content != "" {
			sections = append(sections, content)
			continue
		}
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		line := "- `" + name + "`"
		if description := strings.TrimSpace(entry.Description); description != "" {
			line += " — " + description
		}
		sections = append(sections, line)
	}
	if len(sections) == 0 {
		return raw
	}
	return adapterNote + "\n\n" + strings.Join(sections, "\n\n")
}

func browserContextGuardPaths(ctx context.Context, key common.ContextKey) []string {
	paths, _ := ctx.Value(key).([]string)
	return append([]string(nil), paths...)
}

// listCDPTabs runs the bare `tab` listing form only -- no tab id/selector
// argument -- so it is always a side-effect-free read, unlike `tab <id>`
// (switches focus) or `tab new` (creates a tab). The top-level command
// dispatcher's shouldRetryCDPTimeout retry set deliberately excludes "tab"
// because that name also covers those side-effecting forms; every caller of
// this internal helper also bypasses that dispatcher entirely, so a listing
// timeout here got zero retries even though five independent Pulse findings
// converged on this exact call, after this exact endpoint, coming back
// reachable moments earlier from a fresh Stage-0 connection test. Retry once
// here specifically, sleeping 500ms then reissuing the identical command --
// only the sleep-then-retry-once *mechanic* mirrors the dispatcher's own
// timeout retry (executor.go:978-990); this retry deliberately skips the
// dispatcher's session-recovery steps (guardCDPAutomaticRecovery /
// resetCDPSessionRuntime / clearCDPActiveTabForPort /
// clearCDPExclusiveFeaturesForPort), since a bare listing timeout does not
// by itself indicate a dead session worth resetting, and does not touch
// shouldRetryCDPTimeout's shared allowlist.
func (e *Executor) listCDPTabs(ctx context.Context, session, cdpURL string, opts *ExecuteOptions) (string, error) {
	args := cdpTabTarget(session, cdpURL).command("tab")
	output, err := e.Client.ExecuteCommand(ctx, args, opts)
	if err != nil && isGenuineCDPListingTimeout(err) {
		log.Printf("[BROWSER] CDP tab list timed out for session %q, retrying once: %v", session, err)
		time.Sleep(500 * time.Millisecond)
		output, err = e.Client.ExecuteCommand(ctx, args, opts)
	}
	return output, err
}

func (e *Executor) listCDPTabsForUser(ctx context.Context, session, cdpURL string, opts *ExecuteOptions, port int, ownerID string) string {
	output, err := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout))
	if err != nil {
		log.Printf("[BROWSER] CDP tab list failed after %s; falling back to selected tab hint: %v", cdpTabListTimeout, err)
		return fallbackCDPTabListMessage(port, ownerID, err)
	}
	return formatCDPTabListForPrompt(output)
}

func executeOptionsWithTimeout(opts *ExecuteOptions, timeout time.Duration) *ExecuteOptions {
	if opts == nil {
		return &ExecuteOptions{Timeout: timeout}
	}
	cloned := *opts
	cloned.Timeout = timeout
	return &cloned
}

func (e *Executor) selectCDPTab(ctx context.Context, session, tab, cdpURL string, opts *ExecuteOptions) (string, error) {
	return e.Client.ExecuteCommand(ctx, cdpTabTarget(session, cdpURL).command("tab", tab), opts)
}

func (e *Executor) selectCDPTabForCommand(ctx context.Context, session, cdpURL string, opts *ExecuteOptions, port int, ownerID, tab, command string) (string, error) {
	requestedTab := tab
	if aliasTabID := getCDPTabAlias(port, ownerID, tab); aliasTabID != "" {
		tab = aliasTabID
	}

	// Selecting a tab through agent-browser can bring the visible CDP Chrome
	// window to the foreground on macOS. Inspect the real browser state first;
	// when the requested tab is already active, keep using it without issuing a
	// redundant tab switch. The surrounding per-port lock prevents another
	// AgentWorks workflow from switching tabs between this check and the action.
	if listed, listErr := e.listCDPTabs(ctx, session, cdpURL, executeOptionsWithTimeout(opts, cdpTabListTimeout)); listErr == nil {
		if tabs, parseErr := parseCDPTabs(listed); parseErr == nil {
			if current, found := findCDPTabByRef(tabs, tab); found && current.Active {
				resolvedTab := strings.TrimSpace(current.TabID)
				if resolvedTab == "" {
					resolvedTab = tab
				}
				setCDPTabAlias(port, ownerID, requestedTab, resolvedTab)
				setCDPTabSelection(port, ownerID, resolvedTab)
				setCDPActiveTab(port, resolvedTab)
				log.Printf("[BROWSER] CDP: tab %q already active before %q; preserving OS focus", resolvedTab, command)
				return resolvedTab, nil
			}
		} else {
			log.Printf("[BROWSER] CDP: could not parse tab state before %q; falling back to explicit selection: %v", command, parseErr)
		}
	} else {
		log.Printf("[BROWSER] CDP: could not inspect tab state before %q; falling back to explicit selection: %v", command, listErr)
	}

	output, err := e.selectCDPTab(ctx, session, tab, cdpURL, opts)
	if err == nil {
		if tabID := findCDPTabID(output, tab); tabID != "" {
			setCDPTabAlias(port, ownerID, requestedTab, tabID)
			return tabID, nil
		}
		return tab, nil
	}
	return "", e.cdpTabSelectionError(ctx, session, cdpURL, opts, port, requestedTab, command, err)
}

func (e *Executor) cdpTabSelectionError(ctx context.Context, session, cdpURL string, opts *ExecuteOptions, port int, tab, command string, selectErr error) error {
	configuredURL := resolveCdpURL(port)
	return fmt.Errorf("failed to select CDP tab %q before %q: %w\n\nThe tab label/id %q is not currently selectable. Do not retry page actions with this label. Select a known tab with agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"<tab-id-or-label>\"]) or create a labeled tab with agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"new\", \"--label\", %q, \"<url>\"]).", tab, command, selectErr, tab, configuredURL, configuredURL, tab)
}

// unmarkedCDPTabArg looks for a tab id that was passed as a bare positional
// (["--cdp", url, "t7"]) instead of being marked with tab/--tab. Only the
// strict tN id form counts, and only outside a flag's value slot, so e.g.
// type --text t7 is never mistaken for a tab selection.
func unmarkedCDPTabArg(args []string) (tab string, rest []string) {
	for i, arg := range args {
		if !isCDPTabID(arg) {
			continue
		}
		if i > 0 && strings.HasPrefix(args[i-1], "-") {
			continue
		}
		rest = append(append([]string{}, args[:i]...), args[i+1:]...)
		return strings.TrimSpace(arg), rest
	}
	return "", args
}

func missingCDPPageActionTabError(port int, command string, commandArgs []string, tabHint string) error {
	// A tN-shaped positional never reaches here -- the caller recovers it as
	// the tab selection -- so by this point no tab was supplied in any form.
	normalizedArgs := normalizeAgentBrowserCommandArgs(command, commandArgs)
	cdpPrefix := []string{"--cdp", resolveCdpURL(port)}
	tabRetryArgs := append(append(append([]string{}, cdpPrefix...), "tab", "<tab-id-or-label>"), normalizedArgs...)
	flagRetryArgs := append(append(append([]string{}, cdpPrefix...), "--tab", "<tab-id-or-label>"), normalizedArgs...)
	return fmt.Errorf("CDP shared-browser mode requires every page action to include a tab before %q.\n\nIf the goal is only to check CDP reachability, do not use %q: it is a page action. Call agent_browser(command=\"status\", args=[], session=\"<same-session>\") instead. status needs no tab and no --cdp argument.\n\n%s\n\nRetry the same page command with the tab inline:\nagent_browser(command=%q, args=%s)\nor:\nagent_browser(command=%q, args=%s)\n\nDo not put the command name inside args. For wait, use milliseconds or native wait options after the CDP+tab prefix.\n\nIf no tab is selected yet, create a labeled tab with agent_browser(command=\"tab\", args=[\"--cdp\", %q, \"new\", \"--label\", \"<label>\", \"<url>\"]).", command, command, tabHint, command, jsonStringSlice(tabRetryArgs), command, jsonStringSlice(flagRetryArgs), resolveCdpURL(port))
}

func jsonStringSlice(args []string) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	s := string(raw)
	s = strings.ReplaceAll(s, "\\u003c", "<")
	s = strings.ReplaceAll(s, "\\u003e", ">")
	s = strings.ReplaceAll(s, "\\u0026", "&")
	return s
}

// resolveCdpURL builds the CDP URL for agent-browser.
// CDP_HOST env var overrides auto-detection.
// In native mode, agent-browser runs on the host so localhost reaches Chrome directly.
// In Docker mode, agent-browser runs inside the workspace container so we use
// host.docker.internal (client.go resolves it to an IP for Chrome compatibility).
func resolveCdpURL(port int) string {
	if host := os.Getenv("CDP_HOST"); host != "" {
		return fmt.Sprintf("http://%s:%d", host, port)
	}
	if common.IsNativeWorkspace() {
		return fmt.Sprintf("http://localhost:%d", port)
	}
	return fmt.Sprintf("http://host.docker.internal:%d", port)
}

// ConfiguredCDPEndpoint returns the environment-correct endpoint that agents
// must declare with --cdp for a backend-configured port.
func ConfiguredCDPEndpoint(port int) string {
	if port <= 0 {
		port = 9222
	}
	return resolveCdpURL(port)
}

// ConfiguredCDPEndpoints returns environment-correct endpoints in stable,
// de-duplicated order for prompt generation and observability.
func ConfiguredCDPEndpoints(ports []int) []string {
	ports = normalizeCDPPorts(ports)
	endpoints := make([]string, 0, len(ports))
	for _, port := range ports {
		endpoints = append(endpoints, resolveCdpURL(port))
	}
	return endpoints
}

// getTimeoutForCommand returns an appropriate timeout based on the command type
func getTimeoutForCommand(command string) time.Duration {
	switch command {
	case "open", "goto", "navigate":
		return 60 * time.Second // Navigation can take longer
	case "screenshot", "pdf":
		return 60 * time.Second // Screenshots/PDFs can take longer
	case "wait":
		return 120 * time.Second // Wait operations may have longer timeouts
	case "close", "quit", "exit":
		return 10 * time.Second // Close should be quick
	default:
		return 30 * time.Second // Default timeout for most operations
	}
}

func isCommandTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "command timed out") ||
		strings.Contains(msg, "command execution timed out") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "exceeded timeout")
}

// isGenuineCDPListingTimeout is isCommandTimeoutError narrowed for
// listCDPTabs's retry: isCommandTimeoutError also matches "context
// canceled", which fires when the *caller's* context was canceled (e.g. the
// parent request was aborted), not when the CDP endpoint was genuinely
// slow. Retrying a canceled-context failure after a 500ms sleep is not a
// recovery attempt -- the reissued command will almost certainly fail
// immediately for the identical reason, so the sleep is pure waste on that
// path. Only retry the shapes that indicate the request actually ran out of
// time against a live endpoint.
func isGenuineCDPListingTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "command timed out") ||
		strings.Contains(msg, "command execution timed out") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "exceeded timeout")
}

func shouldRetryCDPTimeout(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "skills", "snapshot", "get", "is", "screenshot", "console", "errors":
		return true
	default:
		return false
	}
}

func isCDPUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "econnrefused") ||
		strings.Contains(msg, "failed to connect") ||
		strings.Contains(msg, "cannot connect") ||
		strings.Contains(msg, "could not connect") ||
		strings.Contains(msg, "cdp endpoint") && strings.Contains(msg, "unavailable")
}

func cdpUnavailableError(port int, cause error) error {
	profileDir := "$HOME/.chrome-cdp-profile"
	if port != 9222 {
		profileDir = fmt.Sprintf("$HOME/.chrome-cdp-profile-%d", port)
	}
	launch := fmt.Sprintf(`/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --remote-debugging-port=%d --user-data-dir="%s" --no-first-run --no-default-browser-check`, port, profileDir)
	return fmt.Errorf("CDP_UNAVAILABLE: %s\nport: %d\nlaunch_command: %s\nstatus_command: agent_browser(command=\"status\", session=\"default\")\ncause: %w", "the configured Chrome CDP browser is not reachable; do not probe Chrome directly from shell", port, launch, cause)
}

func isCDPRuntimeStartupError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "auto-launch failed") ||
		strings.Contains(msg, "invalid cdp url") ||
		strings.Contains(msg, "empty host")
}

// sessionDirs returns the directories where agent-browser stores session files.
// sessionDirs returns every directory that might hold agent-browser's own
// per-session bookkeeping files (.pid, .chrome-pid, .sock, ...), in the same
// order agent-browser itself would resolve them.
//
// When AGENT_BROWSER_NAMESPACE is set (every fixed-workspace product with a
// persistent shared profile sets this, to isolate daemon sockets/state from
// other products on the same shared host -- see browserconfig.SharedProfile),
// agent-browser puts these files under a namespaces/$NAMESPACE/run/
// subdirectory instead of flat under $HOME/.agent-browser or
// /tmp/.agent-browser. The base directory for that subtree depends on
// whether XDG_RUNTIME_DIR is visible to the process that actually spawns
// agent-browser: the real production path -- execute_shell_command's
// Landlock-sandboxed subprocess, invoked from sparkquill-workspace -- does
// not propagate it even though sparkquill-workspace's own environment has it
// set, so agent-browser falls back to /tmp there. Confirmed directly:
// running the identical command through /api/execute (the real path) wrote
// to /tmp/.agent-browser/namespaces/$NS/run/, not
// $XDG_RUNTIME_DIR/agent-browser/namespaces/$NS/run/, even though
// XDG_RUNTIME_DIR was set on sparkquill-workspace.service itself. Both are
// checked, in that order, since an interactive shell (which does get
// XDG_RUNTIME_DIR from the login session) uses the XDG one.
//
// Before this fix, this function checked neither namespaced location at
// all: captureChromePID could never find a daemon PID to read, so it never
// recorded Chrome's PID; and killSessionRuntime's force-kill fallback
// (steps 2-4, the path specifically meant to catch an already-hung or
// already-crashed daemon that can't gracefully close itself) silently
// found nothing to kill every single time, on every namespaced deployment,
// since the namespace was introduced. gracefulCloseSession still worked
// (it shells out to agent-browser's own `close` command, which resolves
// its own namespace correctly), which is exactly why this went unnoticed:
// the happy path recovered fine, and only the fallback -- the one case
// that matters most, a daemon too dead to close itself -- was silently
// broken.
func sessionDirs() []string {
	dirs := []string{}
	if ns := strings.TrimSpace(os.Getenv("AGENT_BROWSER_NAMESPACE")); ns != "" {
		if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
			dirs = append(dirs, filepath.Join(runtimeDir, "agent-browser", "namespaces", ns, "run"))
		}
		dirs = append(dirs, filepath.Join("/tmp/.agent-browser", "namespaces", ns, "run"))
	}
	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		dirs = append(dirs, filepath.Join(homeDir, ".agent-browser"))
	}
	tmpDir := "/tmp/.agent-browser"
	if homeDir == "" || tmpDir != filepath.Join(homeDir, ".agent-browser") {
		dirs = append(dirs, tmpDir)
	}
	// Each managed browser keeps its sockets in its own folder
	// (browserconfig.SocketDirForSession); the host side must find them all.
	if owners, err := filepath.Glob(filepath.Join(browserconfig.SocketRoot, "o", "*")); err == nil {
		dirs = append(dirs, owners...)
	}
	// A daemon started from a coding CLI's sandbox keeps them under the shared tmp folder (see
	// browserconfig.SandboxSocketDir).
	dirs = append(dirs, browserconfig.SandboxSocketDirs()...)
	return dirs
}

// captureChromePID records Chrome's PID into a .chrome-pid file right after a
// successful open command — when Chrome is guaranteed alive and still a child of
// the daemon. This lets killSessionRuntime use the stored PID later, avoiding the
// PPID timing race where Chrome has already been reparented or re-launched.
//
// Topology (verified against agent-browser v0.26): each session spawns its own
// daemon (PPID=1) which spawns exactly one Chrome child. Daemons and Chromes
// are not shared across sessions, so kids[0] is always this session's Chrome.
func captureChromePID(session string) {
	for _, dir := range sessionDirs() {
		pidFile := filepath.Join(dir, session+".pid")
		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		daemonPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil {
			continue
		}
		kids := childPIDs(daemonPID)
		if len(kids) == 0 {
			continue
		}
		// One daemon = one Chrome child (see function doc).
		chromePID := kids[0]
		chromePIDFile := filepath.Join(dir, session+".chrome-pid")
		if err := os.WriteFile(chromePIDFile, []byte(strconv.Itoa(chromePID)), 0600); err == nil {
			log.Printf("[BROWSER] Recorded Chrome PID %d for session %q", chromePID, session)
		}
		return
	}
}

// killChromePID kills Chrome's process group using the given PID.
// Chrome's PGID equals its own PID, so killing -PGID takes the entire Chrome tree
// (GPU, renderer, network helpers) in one shot.
func killChromePID(chromePID int, session string) {
	if err := syscall.Kill(-chromePID, syscall.SIGKILL); err != nil {
		// Fallback: kill just the process if group kill fails
		if p, err2 := os.FindProcess(chromePID); err2 == nil {
			p.Signal(syscall.SIGKILL) //nolint:errcheck
		}
		log.Printf("[BROWSER] Killed Chrome PID %d (group kill failed: %v) for session %q", chromePID, err, session)
	} else {
		log.Printf("[BROWSER] Killed Chrome process group PGID=%d for session %q", chromePID, session)
	}
}

// logPreKillDiagnostics captures the exact state we're about to destroy,
// right before killSessionRuntimeFully runs, so a live incident can show
// whether Chrome was actually already dead or whether recovery is about to
// kill a session that was still working fine. Added while chasing a
// recurring SparkQuill failure where `open` succeeds but the very next
// command (`snapshot`) immediately reports the session dead -- this answers
// the open question of whether that's a real Chrome crash or a false
// positive from whatever decides the session needs a fresh launch.
func logPreKillDiagnostics(session string) {
	var daemonPID, chromePID int
	var daemonAlive, chromeAlive bool
	for _, dir := range sessionDirs() {
		if daemonPID == 0 {
			if b, err := os.ReadFile(filepath.Join(dir, session+".pid")); err == nil {
				if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
					daemonPID = p
					daemonAlive = isProcessAlive(p)
				}
			}
		}
		if chromePID == 0 {
			if b, err := os.ReadFile(filepath.Join(dir, session+".chrome-pid")); err == nil {
				if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
					chromePID = p
					chromeAlive = isProcessAlive(p)
				}
			}
		}
	}

	lockState := "no-shared-profile"
	if profile := ProfilePathForSession(session); profile != "" {
		var present []string
		for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
			path := filepath.Join(profile, name)
			if info, err := os.Lstat(path); err == nil {
				age := time.Since(info.ModTime()).Round(time.Millisecond)
				present = append(present, fmt.Sprintf("%s(age=%s)", name, age))
			}
		}
		if len(present) == 0 {
			lockState = "none-present"
		} else {
			lockState = strings.Join(present, ",")
		}
	}

	// Independent of the stored PID files above, which can go stale if the
	// daemon respawned Chrome under a new PID (a known race — see the
	// comment on killSessionRuntime's step 2). This catches that case.
	liveMatches := "?"
	if out, err := runCommand("pgrep", "-af", session); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if strings.TrimSpace(out) == "" {
			liveMatches = "none"
		} else {
			liveMatches = strconv.Itoa(len(lines))
		}
	}

	log.Printf("[BROWSER_DIAG] state snapshot for %q: daemonPID=%d daemonAlive=%v chromePID=%d chromeAlive=%v lockFiles=[%s] liveProcessesMatchingSession=%s",
		session, daemonPID, daemonAlive, chromePID, chromeAlive, lockState, liveMatches)
}

// killSessionRuntime closes the agent-browser session and kills all associated
// processes. Each session owns its own daemon + Chrome (verified 1:1:1), so
// killing this session's daemon does not affect any other session.
//
// Always runs both graceful close AND force kill:
//   - Graceful: daemon closes Chrome and exits cleanly (the happy path).
//   - Force kill: covers (a) daemon already dead but Chrome still alive as an
//     orphan (PPID=1), and (b) graceful close returning success even when the
//     daemon is dead, which means we cannot trust its return code.
func killSessionRuntime(session string) {
	if SharedBrowserEnabled() && session == SharedSessionName {
		return
	}
	// Step 1: ask the daemon to close gracefully — kills Chrome and exits cleanly
	// when the daemon is alive. NOTE: agent-browser close returns success even
	// when the daemon is already dead, so we cannot rely on this alone.
	gracefulCloseSession(session)

	// Steps 2-4 below are the force-kill safety net. They read this session's
	// daemon PID from <session>.pid and kill the daemon's process tree.
	for _, dir := range sessionDirs() {
		pidFile := filepath.Join(dir, session+".pid")
		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		daemonPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil {
			continue
		}

		// Step 2: kill live children of the daemon first (catches daemon-restarted
		// Chrome with a new PID that differs from the stored .chrome-pid).
		// Safe even though childPIDs is unfiltered: per verified 1:1:1 topology
		// the daemon has at most one Chrome child, belonging to this session.
		for _, chromePID := range childPIDs(daemonPID) {
			killChromePID(chromePID, session)
		}

		// Step 3: kill the stored chrome-pid (original Chrome, may already be dead
		// but kills its PGID which covers any helpers in the same process group).
		chromePIDFile := filepath.Join(dir, session+".chrome-pid")
		if chromePIDBytes, err := os.ReadFile(chromePIDFile); err == nil {
			if chromePID, err := strconv.Atoi(strings.TrimSpace(string(chromePIDBytes))); err == nil && chromePID > 0 {
				killChromePID(chromePID, session)
			}
		}

		// Step 4: kill the daemon itself. Safe because daemons are per-session.
		proc, err := os.FindProcess(daemonPID)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGKILL); err != nil {
			log.Printf("[BROWSER] Could not kill runtime PID %d for session %q: %v", daemonPID, session, err)
		} else {
			log.Printf("[BROWSER] Killed stale runtime PID %d for session %q", daemonPID, session)
		}
	}

	// Step 5: kill any orphaned Chrome processes (PPID=1, agent-browser user-data-dir)
	// that slipped through — daemon may have restarted Chrome right before dying.
	killOrphanedChrome(session)
}

// resetCDPSessionRuntime kills only the agent-browser daemon for a shared CDP
// session and removes its state files. It intentionally does not call
// agent-browser close and does not kill Chrome children, because shared CDP mode
// attaches to the user's existing Chrome instead of owning a browser process.
func resetCDPSessionRuntime(session string) {
	for _, dir := range sessionDirs() {
		pidFile := filepath.Join(dir, session+".pid")
		pidBytes, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		daemonPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil || daemonPID <= 0 {
			continue
		}
		proc, err := os.FindProcess(daemonPID)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGKILL); err != nil {
			log.Printf("[BROWSER] Could not kill CDP runtime PID %d for session %q: %v", daemonPID, session, err)
		} else {
			log.Printf("[BROWSER] Killed CDP runtime PID %d for session %q", daemonPID, session)
		}
	}
	removeSessionFiles(session)
}

// gracefulCloseSession asks the agent-browser daemon to close the session cleanly.
// Returns true if the daemon acknowledged and shut down within the timeout.
// The daemon stops its Chrome restart loop and kills Chrome itself — no orphans.
func gracefulCloseSession(session string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agent-browser", "--session", session, "close", "--json")
	// Reach the daemon in the folder it actually runs in: a managed browser's
	// own folder (browserconfig.SocketDirForSession), or the shared one for
	// browsers started before per-owner folders.
	for _, dir := range sessionDirs() {
		if _, err := os.Stat(filepath.Join(dir, session+".pid")); err == nil {
			env := make([]string, 0, len(os.Environ())+1)
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "AGENT_BROWSER_SOCKET_DIR=") {
					env = append(env, kv)
				}
			}
			cmd.Env = append(env, "AGENT_BROWSER_SOCKET_DIR="+dir)
			break
		}
	}
	if err := cmd.Run(); err != nil {
		log.Printf("[BROWSER] Graceful close failed for %q (%v) — falling back to force kill", session, err)
		return false
	}
	log.Printf("[BROWSER] Gracefully closed session %q", session)
	return true
}

// killOrphanedChrome kills Chrome for Testing processes that are reparented to PID 1
// (daemon was killed but Chrome survived). This handles the case where the daemon
// auto-restarted Chrome after the original Chrome crashed — the restarted Chrome gets
// a new PID that was never captured in .chrome-pid, so it slips past the normal kill.
//
// The pgrep pattern is global (no session filter) because agent-browser uses
// random UUIDs in its user-data-dir path, not session names — there is no way
// to scope by session from the cmdline alone. This is safe because the PPID=1
// filter only matches Chromes whose daemon is already dead: a live session's
// Chrome has PPID = daemon, so it won't match. In other words, anything this
// function kills is by definition already disowned and leaking.
//
// The session argument is logging context only — the kill is not session-scoped.
//
// Note: v0.24.1+ of agent-browser puts Chrome in its own process group and uses
// PR_SET_PDEATHSIG on Linux, so orphans are much rarer now. This remains a safety
// net for crash scenarios and for macOS (no PR_SET_PDEATHSIG equivalent).
func killOrphanedChrome(session string) {
	// Use pgrep to find all Chrome for Testing processes whose cmdline contains
	// the agent-browser temp user-data-dir marker AND whose parent is PID 1.
	out, err := runCommand("pgrep", "-f", "agent-browser-chrome-")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	killed := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		// Check if PPID is 1 (orphaned — daemon already dead)
		ppidOut, err := runCommand("ps", "-p", strconv.Itoa(pid), "-o", "ppid=")
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(strings.TrimSpace(ppidOut))
		if err != nil || ppid != 1 {
			continue
		}
		// Orphaned agent-browser Chrome — safe to kill
		killChromePID(pid, session+"/orphan")
		killed++
	}
	if killed > 0 {
		log.Printf("[BROWSER] Killed %d orphaned Chrome process(es) for session %q", killed, session)
	}
}

// childPIDs returns the direct child PIDs of the given parent PID by scanning /proc (Linux)
// or using sysctl (macOS). Returns an empty slice if none are found or on error.
func childPIDs(parentPID int) []int {
	// Use ps to find direct children — works on both macOS and Linux.
	out, err := runCommand("ps", "-o", "pid=", "--ppid", strconv.Itoa(parentPID))
	if err != nil || strings.TrimSpace(out) == "" {
		// macOS ps uses different flags
		out, err = runCommand("pgrep", "-P", strconv.Itoa(parentPID))
		if err != nil || strings.TrimSpace(out) == "" {
			return nil
		}
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// runCommand executes a command and returns its stdout output.
func runCommand(name string, args ...string) (string, error) {
	cmd := execCmd(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

// killSessionRuntimeFully is the complete teardown sequence for a session
// whose runtime is being stopped: kill the daemon/Chrome processes, remove
// agent-browser's own bookkeeping files, and clear any stale Chrome
// ProcessSingleton lock left in the profile directory.
//
// Use this instead of calling killSessionRuntime/removeSessionFiles directly.
// Before this helper existed, three separate call sites each reimplemented
// the first two steps but not the third -- the idle reaper, auto-eviction
// (freeing a session slot for a new open), and KillAllTrackedSessions (every
// server SIGTERM, i.e. every deploy). Confirmed live on SparkQuill: any one
// of those killing a session with an active Chrome left its SingletonLock/
// SingletonSocket/SingletonCookie behind, and the next launch attempt against
// that persistent profile -- sometimes tens of minutes later, on the very
// first try -- failed with "Failed to create a ProcessSingleton for your
// profile directory" even though nothing was actually still running.
func killSessionRuntimeFully(session string) {
	clearSessionTabScope(session)
	killSessionRuntime(session)
	removeSessionFiles(session)
	removeStaleChromeSingletonLock(session)
}

// removeSessionFiles removes agent-browser session state files (.pid, .sock, etc.)
// so the next command starts a fresh runtime + Chrome instead of connecting to a
// dead one. Call killSessionRuntime first to stop the daemon before removing its files.
func removeSessionFiles(session string) {
	if SharedBrowserEnabled() && session == SharedSessionName {
		return
	}
	for _, dir := range sessionDirs() {
		removed := false
		for _, ext := range []string{".pid", ".chrome-pid", ".sock", ".stream", ".engine", ".version"} {
			f := filepath.Join(dir, session+ext)
			if err := os.Remove(f); err == nil {
				removed = true
			}
		}
		if removed {
			log.Printf("[BROWSER] Removed stale session files for %q in %s", session, dir)
		}
	}
}

// removeStaleChromeSingletonLock clears Chrome's own ProcessSingleton lock
// files (SingletonLock/SingletonSocket/SingletonCookie) from a persistent
// shared profile after this session's runtime has just been killed. Chrome
// does not always clean these up on an unclean exit, and their mere presence
// makes the NEXT launch attempt refuse to start at all -- "Failed to create
// ProcessSingleton for your profile directory ... Aborting now to avoid
// profile corruption" -- even though the old process is provably gone (we
// just killed it above). removeSessionFiles only cleans agent-browser's own
// daemon bookkeeping files (.pid/.sock/...), never Chrome's, so every single
// auto-recovery retry after a crash was guaranteed to hit this immediately.
// Confirmed live on SparkQuill: a crashed session's every recovery attempt
// failed with exactly this error until the lock was removed by hand.
func removeStaleChromeSingletonLock(session string) {
	profile := ProfilePathForSession(session)
	if profile == "" {
		return
	}
	removed := false
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if err := os.Remove(filepath.Join(profile, name)); err == nil {
			removed = true
		}
	}
	if removed {
		log.Printf("[BROWSER] Removed stale Chrome singleton lock for %q in %s", session, profile)
	}
}
