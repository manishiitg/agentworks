package instructions

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// cdpHost returns the hostname to use in CDP instructions.
// In native mode, use localhost. In Docker mode, use host.docker.internal.
func cdpHost() string {
	if common.IsNativeWorkspace() {
		return "localhost"
	}
	return "host.docker.internal"
}

// BrowserConfig holds configured browser intent for prompt generation.
// Auto-mode reachability is queried live through agent_browser status.
type BrowserConfig struct {
	HasAgentBrowser bool
	CdpPort         int   // Primary port (legacy compatibility).
	CdpPorts        []int // Authorized independent Chrome profiles for this run.
	Mode            string
}

// BuildBrowserRuntimeInstructions returns only run-specific browser state for
// coding CLI agents. Static command syntax, safety rules, tab ownership, and
// selector guidance are projected through the agent-browser and
// builder-reference skills; repeating those docs in CLAUDE.md/AGENTS.md can
// push the CLI over its system-prompt limit.
func BuildBrowserRuntimeInstructions(cfg BrowserConfig) string {
	if !cfg.HasAgentBrowser {
		return ""
	}
	ports := append([]int{cfg.CdpPort}, cfg.CdpPorts...)
	endpoints := browser.ConfiguredCDPEndpoints(ports)
	if len(endpoints) == 0 && (cfg.Mode == "auto" || cfg.Mode == "cdp") {
		endpoints = browser.ConfiguredCDPEndpoints([]int{9222})
	}

	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		if len(endpoints) > 0 {
			mode = "cdp"
		} else {
			mode = "headless"
		}
	}

	var sb strings.Builder
	sb.WriteString("## Browser Runtime Configuration\n")
	fmt.Fprintf(&sb, "- Configured mode: `%s`. This is configuration, not cached reachability.\n", mode)
	if len(endpoints) > 0 {
		fmt.Fprintf(&sb, "- Authorized CDP endpoint(s): `%s`.\n", strings.Join(endpoints, "`, `"))
	}
	if mode == "auto" {
		sb.WriteString("- Before the first action and after an availability error, call `agent_browser` status. Follow its live `effective_mode`; never infer CDP reachability from this prompt or probe Chrome through shell.\n")
	}
	sb.WriteString("- For repeatable browser tests, read `builder-reference/references/playwright-scripted.md`; use @agentworks/playwright for JS/TS or agentworks-playwright for Python when available. Its live view is watch-only; this runtime configuration concerns managed agent-browser.\n")
	sb.WriteString("- Before interactive browser work, read the projected `agent-browser` skill when attached; otherwise read `builder-reference/references/browser-usage.md`. Those references contain the managed HTTP bridge, current skill-loading, tab ownership, cleanup, and safety contracts.\n")
	return sb.String()
}

// BuildBrowserInstructions returns the complete browser system prompt
// (upload + mode-specific) for the given config, or "" if no browser tool is active.
func BuildBrowserInstructions(cfg BrowserConfig) string {
	if !cfg.HasAgentBrowser {
		return ""
	}

	result := "\nFor repeatable browser tests, read builder-reference/references/playwright-scripted.md; use @agentworks/playwright for JS/TS or agentworks-playwright for Python when available. Its test sessions are watch-only. The following configuration, media, and capture rules concern managed agent-browser.\n\n## Bundled Browser Recording\nWhen requested, use agent_browser(command=\"capture\", args=[\"start\"], session=\"main\") in managed headless mode after opening/selecting the page. This Builder command starts video, HAR, console and error capture through the same backend as the Browser view. Use args=[\"status\"] before starting and after uncertain responses. Stop with args=[\"stop\"] and report the returned directory, files, validation and errors. A failed validation means the capture is not verified evidence; never substitute a previous run’s footage. Stop a capture you started even when reproduction fails; never stop someone else's active recording. Do not mix capture with separate record/HAR commands or close/reset the browser. The output path is assigned from current workflow permissions. CDP uses the separate native recording commands.\n"

	// Use Mode as primary decision, fall back to legacy CdpPort/Has* flags.
	if cfg.Mode == "auto" {
		result += "\n" + GetAutoBrowserInstructions(cfg.CdpPort, cfg.CdpPorts...)
	}
	isCdp := cfg.Mode == "cdp" || (cfg.Mode == "" && (cfg.CdpPort > 0 || len(cfg.CdpPorts) > 0))
	if cfg.Mode == "auto" {
		// Added above; availability is deliberately not resolved here.
	} else if isCdp {
		result += "\n" + GetCdpBrowserInstructions(cfg.CdpPort, cfg.CdpPorts...)
	} else {
		result += "\n" + GetHeadlessBrowserInstructions()
	}

	// Add session limits — applies to all browser types
	result += "\n\n## Managed Browser Ownership\nOne persistent headless browser per workflow is shared by its authorized users, builder, runs, steps and groups. Unrelated workflows use separate browsers; non-workflow chats retain the user's personal browser. Session labels do not create separate browsers. Tabs are optional: reuse the current/existing tab or create one when useful. Browser commands are serialized; coordinate parallel tasks and re-snapshot before interaction. Preserve the browser and sign-ins when a step finishes. During capture, wait for another run’s recording to stop. In CDP mode keep the existing owned-tab and cleanup rules.\n"

	return result
}

// GetAutoBrowserInstructions describes configured candidates without claiming
// whether CDP is currently available. agent_browser status is authoritative.
func GetAutoBrowserInstructions(cdpPort int, additionalPorts ...int) string {
	ports := append([]int{cdpPort}, additionalPorts...)
	if len(browser.ConfiguredCDPEndpoints(ports)) == 0 {
		ports = []int{9222}
	}
	endpoints := browser.ConfiguredCDPEndpoints(ports)
	return fmt.Sprintf(`## Browser Automation (Auto — Live Mode)

Browser configuration is `+"`auto`"+`. The configured CDP candidate endpoint(s) are `+"`%s`"+`, but reachability is live state and is never taken from saved conversation or prompt state.

Before the first browser action, and after any availability error, call:

`+"```python"+`
status = agent_browser(command="status", args=[], session="default")
`+"```"+`

- If `+"`effective_mode`"+` is `+"`cdp`"+`, prefix every later call with one endpoint from `+"`authorized_endpoints`"+` and follow shared-tab rules.
- If it is `+"`headless`"+`, call without `+"`--cdp`"+`.
- `+"`status`"+` is the connectivity check and needs no tab and no `+"`--cdp`"+` argument. Never use `+"`snapshot`"+` as a reachability probe; it reads a specific page.
- Never probe Chrome's `+"`/json/version`"+` endpoint through shell.
- The backend rechecks auto-mode availability on every browser action.
`, strings.Join(endpoints, "`, `"))
}

// GetBrowserUploadInstructions returns system prompt instructions for browser file upload.
// Appended to the agent's system prompt when browser access is active.
// agent_browser resolves workspace-relative paths through guarded backend staging.
func GetBrowserUploadInstructions() string {
	return `

## Browser File Upload

When a website has a file upload input (e.g. file picker, drag-and-drop zone), use these tools to upload workspace files:

### Using agent_browser
1. In CDP mode, list/select a tab and include it inline on each page action: agent_browser(command="snapshot", args=["tab", "t1", "-i"])
2. Upload using the ref. In CDP mode include the tab inline: agent_browser(command="upload", args=["tab", "t1", "@ref", "Chats/report.pdf"])

### Path Rules
- Always use **workspace-relative paths** (e.g. "Chats/report.pdf", "Chats/output.csv")
- Paths are securely staged from the workspace root for the persistent browser daemon — do NOT construct absolute paths yourself
- Files in "Chats/" are created during the conversation
- If you need to create a file first, save it to "Chats/" using execute_shell_command, then upload it
`
}

// GetCdpModeInstructions returns instructions specific to CDP mode (connected to user's real Chrome).
func GetCdpModeInstructions() string {
	host := cdpHost()
	return fmt.Sprintf(`
## Browser Mode: CDP (Connected to User's Chrome)

You are controlling the **user's real Chrome browser** via Chrome DevTools Protocol (CDP).

**Key behaviors:**
- The user can **see everything you do** in their browser ��� actions are visible in real-time
- The browser may have **existing cookies, login sessions, and tabs** — you can leverage authenticated sessions without re-logging in
- Never call the top-level `+"`close`"+` command in CDP mode; it can disrupt the user's real Chrome session
- Leave workflow-created labeled tabs open at normal completion. The backend closes only those owned tabs after one hour and preserves pre-existing user tabs
- Use `+"`tab close <owned-label>`"+` only for explicit immediate cleanup or to replace one of the workflow's own labeled tabs; never close a pre-existing user tab
- **Take screenshots** to show the user what you see, since they can also verify visually
- Sessions **persist across tool calls** — you don't need to re-open pages between interactions
- If a site requires login and the user is already logged in, just navigate directly to the target page

**Connection behavior (important):**
- CDP endpoint is already configured by the backend from the selected port.
- Do **NOT** ask the user for a websocket debugger URL or run "curl localhost:9222/json/version".
- When you describe or troubleshoot the endpoint, use %s:<port>.

**Best practices:**
- Start with a **snapshot** to see the current page state before taking any action
- Use **session="default"** unless you need multiple isolated sessions
- Be careful with form submissions and purchases — this is the user's real browser with real accounts
`, host)
}

// GetHeadlessModeInstructions returns instructions specific to headless browser mode.
func GetHeadlessModeInstructions() string {
	return `
## Browser Mode: Headless (Container Browser)

You are controlling a **headless Chromium browser** running inside a container.

**Key behaviors:**
- Managed headless browsing uses one persistent browser per workflow, shared by its authorized users, runs and steps. Unrelated workflows have separate browsers. Session labels do not create browsers.
- Tabs are optional. Inspect existing tabs; reuse one or create one when useful. Coordinate parallel work, take fresh snapshots, and preserve the browser/sign-ins when a step finishes.
- Users can watch and interact in the workflow Browser view. Use workspace view tools to show that view when appropriate.
- Verify login from page content; never assume a session is authenticated.
- Managed headless Chrome has a synthetic microphone/camera and automatic media permissions. This is test audio, not user speech. Verify getUserMedia and the application result through agent_browser; do not switch to a separate Playwright harness when a media flow fails.

**Best practices:**
- Take screenshots at key moments so the user can verify progress
- Handle login flows explicitly (fill credentials, handle 2FA via human_feedback if needed)
- Use **session="default"** unless you need parallel browser instances
`
}

// GetAgentBrowserQuickStartInstructions returns inline instructions for using the agent-browser tool.
// Appended to the agent's system prompt when browser access (agent-browser skill) is enabled.
func GetAgentBrowserQuickStartInstructions() string {
	return fmt.Sprintf(`## Browser Automation (Quick Start)

Call agent_browser via HTTP API:

` + "```python\nimport requests, os\nBROWSER = os.environ[\"MCP_API_URL\"] + \"/tools/mcp/workspace_browser/agent_browser\"\nHEADERS = {\"Authorization\": f\"Bearer {os.environ['MCP_API_TOKEN']}\", \"Content-Type\": \"application/json\"}\n\ndef browser(command, args=None, session=\"default\"):\n    resp = requests.post(BROWSER, json={\"command\": command, \"args\": args or [], \"session\": session}, headers=HEADERS, timeout=120)\n    resp.raise_for_status()\n    return resp.json().get(\"result\", \"\")\n\n# Basic workflow\nbrowser(\"open\", [\"https://example.com\"])\nsnap = browser(\"snapshot\", [\"-i\"])   # see interactive elements with refs like @e1\n# Example only: use the ref for the intended control in the actual snapshot.\nbrowser(\"click\", [\"@e1\"])\nsnap = browser(\"snapshot\", [\"-i\"])   # refresh after page changes\n# Verify the expected result and resolve the next target from this fresh snapshot.\nbrowser(\"screenshot\", [\"page.png\"])\n\n# Only for a broken ISOLATED session; never reset shared Chrome without an explicit request:\nbrowser(\"reset\")                      # force-kills daemon, clears session state\nbrowser(\"open\", [\"https://example.com\"])  # fresh start\n```" + `

Key commands: skills (version-matched docs), open, snapshot, click, fill, type, press, screenshot, wait, get, scroll, select, hover, upload, download, close, eval, back, forward, reload, reset.

Before the first browser action, load the version-matched core skill with agent_browser(command="skills", args=["get", "core"], session="default"). Use args=["list"] to discover specialized skills and load only those relevant to the task.`)
}

// GetCdpBrowserInstructions returns a single merged section for CDP mode (agent_browser + CDP behaviors).
func GetCdpBrowserInstructions(cdpPort int, additionalPorts ...int) string {
	ports := append([]int{cdpPort}, additionalPorts...)
	seen := make(map[int]bool, len(ports))
	endpoints := make([]string, 0, len(ports))
	for _, port := range ports {
		if port <= 0 || seen[port] {
			continue
		}
		seen[port] = true
		endpoints = append(endpoints, browser.ConfiguredCDPEndpoint(port))
	}
	if len(endpoints) == 0 {
		endpoints = []string{browser.ConfiguredCDPEndpoint(9222)}
	}
	cdpURL := endpoints[0]
	profileGuidance := "This run authorizes one CDP browser: `" + cdpURL + "`."
	if len(endpoints) > 1 {
		profileGuidance = "This run explicitly authorizes multiple independently-profiled CDP browsers: `" + strings.Join(endpoints, "`, `") + "`. Choose the endpoint for the intended login/account on every call. Keep a distinct labeled tab per account. Multiple ports are for multi-login testing inside this workflow, not for ordinary workflow concurrency."
	}
	return fmt.Sprintf(`## Browser Automation (CDP — Connected to User's Chrome)

You have the `+"`agent_browser`"+` tool controlling the **user's real Chrome browser** via Chrome DevTools Protocol.

For a connectivity-only check, call `+"`agent_browser(command=\"status\", args=[])`"+`. It needs no tab and no `+"`--cdp`"+` argument. Never use `+"`snapshot`"+` to test CDP reachability because snapshot reads one specific page.

After status, include `+"`--cdp %[1]s`"+` in every browser/documentation call.

%[2]s

In shared CDP mode, choose an explicit real `+"`tN`"+` tab before browsing, then keep using that tab. `+"`browser(\"tab\", [])`"+` refreshes real tab ids and query-free URLs for up to 15 seconds. Reuse the workflow's existing owned tab or an exact target-URL match. If neither exists, request one stable labeled tab; the backend atomically rechecks reuse and returns its real `+"`tN`"+` plus actual URL. If listing is unavailable, creation is refused instead of risking a duplicate. Important: `+"`open`"+` is URL-only; do not pass `+"`[\"tab\", \"t1\", url]`"+` to `+"`open`"+`. Use the `+"`tab`"+` command to choose/create the tab first, then call `+"`open`"+` with only the URL:

`+"```python\nimport requests, os\nBROWSER = os.environ[\"MCP_API_URL\"] + \"/tools/mcp/workspace_browser/agent_browser\"\nHEADERS = {\"Authorization\": f\"Bearer {os.environ['MCP_API_TOKEN']}\", \"Content-Type\": \"application/json\"}\n\ndef browser(command, args=None, session=\"default\"):\n    tool_args = [] if command == \"status\" else [\"--cdp\", \"%[1]s\"] + (args or [])\n    resp = requests.post(BROWSER, json={\"command\": command, \"args\": tool_args, \"session\": session}, headers=HEADERS, timeout=120)\n    resp.raise_for_status()\n    return resp.json().get(\"result\", \"\")\n\n# Connectivity only: no tab and no --cdp.\nstatus = browser(\"status\")\nprint(status)\n\n# Try the real tab list. If it times out, use the selected-tab fallback.\nselected = browser(\"tab\", [])\nprint(selected)\n# browser(\"tab\", [\"existing-tab-or-label\"])  # only if you already know it\n# browser(\"tab\", [\"new\", \"--label\", \"my-workflow-tab\", \"https://example.com\"])\nbrowser(\"open\", [\"https://example.com\"])\n\n# Use the chosen tab inline on page actions after open. The tab token is removed before the action runs.\nsnap = browser(\"snapshot\", [\"tab\", \"t1\", \"-i\"])\n# Example only: use the ref for the intended control in the actual snapshot.\nbrowser(\"click\", [\"tab\", \"t1\", \"@e1\"])\nsnap = browser(\"snapshot\", [\"tab\", \"t1\", \"-i\"])  # refresh after page changes\n# Verify the expected result and resolve the next target from this fresh snapshot.\n```"+`

Key commands: skills (version-matched docs), open, snapshot, click, fill, type, press, screenshot, wait, get, scroll, select, hover, upload, download, eval, network, console, errors, record, trace, profiler, back, forward, reload, close, reset.

Before the first browser action, load the core skill with `+"`browser(\"skills\", [\"get\", \"core\"])`"+`. Documentation calls do not require a selected tab but still carry the configured `+"`--cdp`"+` prefix. Use `+"`browser(\"skills\", [\"list\"])`"+` to discover specialized skills and load only those relevant to the task (for example `+"`dogfood`"+` for exploratory QA).

### CDP-Specific Behaviors
- The user can **see everything you do** in their browser — actions are visible in real-time
- CDP controls a visible, real Chrome window. The backend preserves OS focus while continuing actions in an already-active workflow tab; creating or switching tabs and explicit human interaction can still bring Chrome forward. Do not switch tabs unnecessarily. For unattended schedules or background work, prefer headless mode or a dedicated automation Chrome/profile/port instead of the user's primary Chrome.
- The browser may have **existing cookies, login sessions, and tabs** — leverage authenticated sessions without re-logging in
- `+"`open`"+` must use URL-only args: `+"`browser(\"open\", [\"https://target.example\"])`"+`. Do not call `+"`open`"+` with `+"`[\"tab\", \"t1\", url]`"+`.
- For snapshot, click, fill, eval, wait, screenshot, and other page-action commands after open, include `+"`[\"tab\", \"<tab-id-or-label>\", ...]`"+` or `+"`[\"--tab\", \"<tab-id-or-label>\", ...]`"+`.
- Do not include the command name inside args. Wrong: `+"`browser(\"wait\", [\"tab\", \"t1\", \"wait\", \"6s\"])`"+`. Right: `+"`browser(\"wait\", [\"tab\", \"t1\", \"6000\"])`"+`.
- Call `+"`browser(\"tab\", [])`"+` to get real `+"`tN`"+` ids plus query-free URLs. Reuse the workflow's owned label first or an exact target-URL match.
- If no tab matches, request one stable labeled tab: `+"`browser(\"tab\", [\"new\", \"--label\", \"<workflow-label>\", \"https://target.example\"])`"+`. The backend atomically rechecks exact-URL reuse and returns the real `+"`tN`"+` plus actual URL; creation is refused if listing is unavailable.
- If a command fails with `+"`CDP shared-browser mode requires selecting or creating a tab`"+`, do not treat that as CDP unavailable and do not call reset. Select a known tab or create a labeled tab, then retry the URL-only open.
- Do not create throwaway tabs for routine navigation. Keep a workflow's labeled tab open and navigate within it across steps/runs.
- Never call the top-level `+"`close`"+` command in CDP mode; it can disrupt the user's real Chrome session
- At normal completion, leave workflow-created labeled tabs open for review. The backend automatically closes only their registered real `+"`tN`"+` ids one hour after the final run releases its browser lease; pre-existing or exact-URL-reused user tabs are preserved
- Use `+"`browser(\"tab\", [\"close\", \"<owned-label>\"])`"+` only when the user explicitly requests immediate cleanup or the workflow must replace one of its own labeled tabs. Never close a pre-existing user tab
- Sessions **persist across tool calls** — you don't need to re-open pages between interactions
- If a site requires login and the user is already logged in, navigate directly to the target page
- Native Chrome downloads may land in the host Downloads folder. When the prompt grants that host path, it may be used to read, stage, or retrieve browser files. Prefer the workspace/run Downloads folder for run-owned artifacts, and do not modify unrelated host files.
- Python/browser code must call this HTTP `+"`agent_browser`"+` tool. Do not connect directly to raw CDP for actions.

### QA, Network Debugging, and Video Evidence
- Keep diagnostics inside `+"`agent_browser`"+` so tab selection and the shared-CDP lock remain enforced.
- Inspect requests with `+"`browser(\"network\", [\"tab\", \"<label>\", \"requests\"])`"+`. HAR files can contain credentials and response bodies; create/share one only when requested and review it before sharing.
- Read page console output with `+"`browser(\"console\", [\"tab\", \"<label>\"])`"+` and page errors with `+"`browser(\"errors\", [\"tab\", \"<label>\"])`"+`.
- For automatic QA video evidence, start `+"`browser(\"record\", [\"tab\", \"<label>\", \"start\", \"qa-run.webm\"])`"+` before the reproduction and always call `+"`browser(\"record\", [\"tab\", \"<label>\", \"stop\"])`"+` afterward, including when a step fails. Upstream recording creates a fresh temporary browser context. AgentWorks detects its real `+"`tN`"+`, pins this workflow's later actions to it, and returns an `+"`AGENTWORKS_RECORDING_CONTEXT`"+` notice. Immediately discard refs from the original tab and take the required fresh snapshot; do not select/create another tab while recording. After stop, the temporary tab is closed and the original tab is restored, so snapshot again before continuing. Videos may capture sensitive visible data; do not record unless the user/workflow requested it.
- Prefer `+"`agent_browser eval`"+` with an inline tab for complex JavaScript. Raw direct-CDP connections bypass the shared-tab lock and are not allowed for normal browser work.

For an exact command or flag not covered by the core overview, load `+"`browser(\"skills\", [\"get\", \"core\", \"--full\"])`"+`.`, cdpURL, profileGuidance)
}

// GetHeadlessBrowserInstructions returns a single merged section for headless mode (agent_browser + headless behaviors).
func GetHeadlessBrowserInstructions() string {
	return fmt.Sprintf(`## Browser Automation (Headless Container Browser)

You have the ` + "`agent_browser`" + ` tool controlling a **headless Chromium browser** inside a container.

Call agent_browser via HTTP API:

` + "```python\nimport requests, os\nBROWSER = os.environ[\"MCP_API_URL\"] + \"/tools/mcp/workspace_browser/agent_browser\"\nHEADERS = {\"Authorization\": f\"Bearer {os.environ['MCP_API_TOKEN']}\", \"Content-Type\": \"application/json\"}\n\ndef browser(command, args=None, session=\"default\"):\n    resp = requests.post(BROWSER, json={\"command\": command, \"args\": args or [], \"session\": session}, headers=HEADERS, timeout=120)\n    resp.raise_for_status()\n    return resp.json().get(\"result\", \"\")\n\nbrowser(\"open\", [\"https://example.com\"])\nsnap = browser(\"snapshot\", [\"-i\"])   # see interactive elements with refs like @e1\n# Example only: use the ref for the intended control in the actual snapshot.\nbrowser(\"click\", [\"@e1\"])\nsnap = browser(\"snapshot\", [\"-i\"])   # refresh after page changes\n# Verify the expected result and resolve the next target from this fresh snapshot.\n\n# Only for a broken ISOLATED session; never reset shared Chrome without an explicit request:\nbrowser(\"reset\")                      # force-kills daemon, clears headless state\nbrowser(\"open\", [\"https://example.com\"])  # fresh start\n```" + `

Key commands: skills (version-matched docs), open, snapshot, click, fill, type, press, screenshot, wait, get, scroll, select, hover, upload, download, close, eval, back, forward, reload, reset.

Before the first browser action, load the core skill with agent_browser(command="skills", args=["get", "core"], session="default"). Use args=["list"] to discover specialized skills and load only those relevant to the task (for example dogfood for exploratory QA).

### Headless-Specific Behaviors
- Managed headless browsing uses one persistent browser per workflow, shared by its authorized users, runs and steps. Unrelated workflows have separate browsers. Session labels do not create browsers.
- Tabs are optional. Inspect existing tabs; reuse one or create one when useful. Coordinate parallel work, take fresh snapshots, and preserve the browser/sign-ins when a step finishes.
- Users can watch and interact in the workflow Browser view. Use workspace view tools to show that view when appropriate.
- Verify login from page content; never assume a session is authenticated.
- Managed headless Chrome has a synthetic microphone/camera and automatic media permissions. This is test audio, not user speech. Verify getUserMedia and the application result through agent_browser; do not switch to a separate Playwright harness when a media flow fails.
- Handle login flows explicitly (fill credentials, handle 2FA via human_feedback if needed)

For an exact command or flag not covered by the core overview, load agent_browser(command="skills", args=["get", "core", "--full"], session="default").`)
}
