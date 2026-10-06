package browser

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// GetToolDefinition returns the tool definition for the agent_browser tool
func GetToolDefinition() llmtypes.Tool {
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "agent_browser",
			Description: "Execute browser automation commands using agent-browser CLI. A paired Chrome extension may be selected for this account/workspace; call status first. For effective_mode=extension, omit --cdp and use ordinary commands on shared tabs. Extension tabs stay in the background unless active=true is explicitly requested; upload/download transfer and teaching are unavailable. Extension record start <workspace-path.webm|.mp4> [url] [--fps 1-60] captures the existing shared tab as-is; record stop publishes it. Stop before selecting or creating another tab. Extension recording needs ffmpeg and has no microphone/desktop audio. Connectivity check: call command='status' with no args; it needs no tab and snapshot must not be used as a CDP probe because snapshot is page-specific. Call status before first browser use to query cdp_supported, the live effective mode, and reachable authorized CDP endpoints. When cdp_supported=false, CDP is disabled by deployment policy: use headless without --cdp and do not probe or configure ports. CDP availability is never inferred from saved chat state. Supports navigation, interaction, screenshots, video recording, console/errors, network inspection/HAR, and data extraction. In CDP mode, record start creates a fresh temporary context; the backend returns AGENTWORKS_RECORDING_CONTEXT, requires a fresh snapshot, pins actions to the recorded tab, and restores the original tab on stop. Managed headless Chrome includes synthetic microphone/camera and automatic media permissions; this is test audio, not real speech. Verify media and app behavior through this tool instead of switching to a standalone browser harness. Builder command capture with args=[start|stop|status] bundles video, HAR, console and errors through the live Browser UI recording service in headless mode. Use it when the user asks to record/reproduce with evidence; the workspace destination is automatic. Start requires a running browser. Check status after timeouts; stop returns files, directory, validation and any partial errors. Only present a capture as verified when validation is passed; never substitute footage from an earlier run. Never close the shared browser after recording. It also exposes version-matched documentation through command='skills'. Multiple configured CDP ports are only for specialized multi-login testing inside one workflow. IMPORTANT: Load the core agent-browser skill before first use, then follow the snapshot → interact → re-snapshot workflow.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Command to execute. Call status first; it is a backend-only live check and takes no args. Then call skills with args=['get','core'] in headless mode or with the status-returned --cdp endpoint in CDP mode. Use ['get','core','--full'] only when the overview lacks an exact command, and ['list'] to discover specialized version-matched skills. Browser commands include open, snapshot, click, fill, type, press, screenshot, wait, get, scroll, select, hover, upload, download, eval, console, errors, network (requests/route/HAR), record (start/stop WebM), capture (Builder-only bundled start/stop/status, headless only, automatic workspace destination), trace, profiler, back, forward, reload, close. Use reset only to force-kill a genuinely broken browser session and clear all state. In shared CDP mode, do NOT reset for a missing-tab error; use command='tab' to get the selected tab hint, select a known tab, or create a stable labeled tab, then retry open with URL-only args.",
					},
					"args": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Arguments for the command. Do not include the command name inside args. status takes no args. Headless mode has no tab requirement. When live status reports CDP, EVERY later call, including skills documentation calls, must begin with ['--cdp', '<status-authorized-endpoint>']; the backend rechecks and rejects unavailable or unconfigured ports. When multiple profiles are reachable, choose the endpoint for the intended login/account on every call and keep separate labeled tabs per profile. A skills documentation call does not need a tab. For browser actions, command='tab' with no additional args lists real tN ids and URLs. Reuse an owned label or exact URL match; otherwise request a stable tab with ['--cdp','<endpoint>','new','--label','<label>','<url>']. The backend atomically rechecks reuse, returns the real tN and actual URL, and refuses creation when it cannot list tabs safely. All other CDP page actions must include the tab inline; the backend verifies the real tN before every action and switches only when needed, avoiding repeated Chrome foreground activation. CDP record start/restart creates a temporary context: obey AGENTWORKS_RECORDING_CONTEXT, discard old refs, snapshot immediately, and do not switch tabs until stop restores the original. CDP open remains URL-only after the --cdp prefix because it uses that selected tab. Upload sources and explicit download destinations must be workspace-relative authorized paths; the backend stages them across persistent-daemon sandbox boundaries.",
					},
					"active": map[string]interface{}{
						"type":        "boolean",
						"description": "Chrome extension only: default false. Keep browser actions and tab selection in the background. Set true explicitly to allow this command to bring its shared tab forward, for example command=tab with the intended tab in args. Permission ends after this call; do not enable routinely.",
					},
					"session": map[string]interface{}{
						"type":        "string",
						"description": "Compatibility session label; use default. Managed headless browsing uses one persistent browser per workflow, shared by its authorized users, builder, runs and groups; unrelated workflows use separate browsers. Different names do not create browsers. Tabs are optional: reuse the current tab or select/create one as needed. Coordinate parallel work and take a fresh snapshot before interacting. Never close/reset the browser merely because your step finished.",
					},
				},
				"required": []string{"command", "session"},
			}),
		},
	}
}
