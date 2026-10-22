package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// Crew owners share the project's saved laptop connection and folder grants.
// A person's MCP connection acts as that person; product, device-only, Brain and
// attached-agent credentials do not become a personal laptop connection.
func personalLaptopSettingsClaims(c *UserClaims) bool {
	if c == nil || c.UserID == "" || c.Scope != "" || c.ExecutionPrincipal != nil || c.BotRouteGrant != "" || !userAccessForClaims(c).CanEdit {
		return false
	}
	if websiteDeviceClaims(c) {
		return true
	}
	t := c.AccessToken
	deviceOnly := t != nil && len(t.Scopes) == 1 && t.Scopes[0] == "devices:connect"
	return t != nil && t.ProjectKind == "" && t.ProjectIdentity == "" && t.BoundAgentID == "" && t.BoundAgentName == "" && t.KnowledgebaseIdentityID == "" && !deviceOnly && c.Provider != "bot_route" && c.Provider != "bot_owner" && c.Provider != slackDMProvider
}

// Capture the same tool callback used by the website, with an MCP-request
// owner check instead of a website chat-turn check. No synthetic chat is made.
type laptopToolCallRegistrar struct {
	name    string
	execute func(context.Context, map[string]interface{}) (string, error)
	timeout time.Duration
}

func (c *laptopToolCallRegistrar) RegisterCustomTool(name, description string, schema map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	return c.RegisterCustomToolWithTimeout(name, description, schema, execute, 180*time.Second, category)
}
func (c *laptopToolCallRegistrar) RegisterCustomToolWithTimeout(name, _ string, _ map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, _ string) error {
	if name == c.name {
		c.execute, c.timeout = execute, timeout
	}
	return nil
}

func (api *StreamingAPI) externalLaptopToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	c := GetUserFromContext(r.Context())
	if !personalLaptopSettingsClaims(c) {
		externalError(w, 403, "forbidden", "Use your own Builder connection and choose laptop folders with manage_laptop_access first.")
		return
	}
	crew, _, ok := api.resolveExternalWorkspace(r.Context(), c, externalArg(args, "workspace_id"))
	if !ok {
		externalError(w, 403, "forbidden", "Choose a Crew you own. Only Crew owners can use its connected laptop.")
		return
	}
	root := agentProfileRuntimeWorkspace(c.UserID, crew.Binding.WorkspacePath)
	if !crewProjectOwnedByCaller(c.UserID, root) {
		externalError(w, 403, "forbidden", "Only Crew owners can use its connected laptop tools.")
		return
	}
	settings, err := readLaptopSettings(c.UserID, root)
	if err != nil || settings.DeviceID == "" || len(settings.Folders) == 0 {
		externalError(w, 400, "laptop_unavailable", "Choose your computer and folders with manage_laptop_access, or open Connections → Local laptop in your Crew.")
		return
	}
	check := func(ctx context.Context) (*UserClaims, error) {
		if !personalLaptopSettingsClaims(c) || !crewProjectOwnedByCaller(c.UserID, root) {
			return nil, fmt.Errorf("Laptop tools require your own Crew and your own connected computer")
		}
		identity := accesstokens.Token{UserID: c.UserID, Username: c.Username, Email: c.Email, Provider: c.Provider}
		live, err := accessTokenClaims(identity)
		if err != nil || !userAccessForClaims(live).CanEdit {
			return nil, fmt.Errorf("Your account no longer has Builder access")
		}
		current, err := readLaptopSettings(c.UserID, root)
		if err != nil || current.DeviceID != settings.DeviceID || len(current.Folders) == 0 {
			return nil, fmt.Errorf("Laptop access was removed from this Crew")
		}
		return c, nil
	}
	if _, err := check(r.Context()); err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return
	}
	registrar := &laptopToolCallRegistrar{name: name}
	if err := api.registerGrantedLaptopTools(registrar, newProductToolGate(nil), "", root, settings, check); err != nil {
		externalError(w, 500, "tool_unavailable", "Laptop tool could not be prepared.")
		return
	}
	if registrar.execute == nil {
		externalError(w, 403, "folder_access", "Commands need the Run terminal commands switch on and a Read & write folder. Change them with manage_laptop_access or in Crew Settings.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), registrar.timeout)
	defer cancel()
	result, err := registrar.execute(ctx, args)
	if err != nil {
		externalError(w, 400, "laptop_operation_failed", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, result)
}

// MCP settings may browse names and validate choices, never dispatch file or
// command operations. Those retain the live interactive-turn checks.
func (api *StreamingAPI) laptopSettingsDeviceCall(ctx context.Context, c *UserClaims, device string, request localfiles.Request) (localfiles.Response, error) {
	if !personalLaptopSettingsClaims(c) || request.Operation != "browse_folders" && request.Operation != "validate_folders" {
		return localfiles.Response{}, fmt.Errorf("Use your own Builder connection to choose laptop folders")
	}
	return api.localDeviceCallForOwner(ctx, c, device, request)
}

// The laptop tools that act on files or run commands, as opposed to manage_laptop_access which only chooses folders.
func isExternalLaptopFileTool(name string) bool {
	switch name {
	case "execute_local_shell_command", "copy_local_file_to_crew", "list_local_files", "write_local_file":
		return true
	}
	return false
}

func externalLaptopAccessDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_laptop_access", "Manage the shared laptop connection and folders of a Crew you build. get returns the saved access and setup steps; devices lists your computers plus the Crew's selected computer; browse lists folder names; set saves the complete folder selection with Read or Read & write; remove disconnects the Crew. Uses the same settings as Connections → Local laptop. Install and sign in to AgentWorks on that computer once, then keep it running. Folders cover files (list_local_files, copy_local_file_to_crew, and write_local_file for Read & write); commands (execute_local_shell_command) need the separate commands switch.", true, false, map[string]any{
		"action":       map[string]any{"type": "string", "enum": []any{"get", "devices", "browse", "set", "remove"}},
		"workspace_id": externalString("Your Crew's stable workspace_id from workspace action=list."),
		"device_id":    externalString("Your connected computer from action=devices; required for browse or set with folders."),
		"path":         externalString("Folder to browse on your computer; omit to start from its home folder."),
		"commands":     map[string]any{"type": "boolean", "description": "Allow terminal commands (execute_local_shell_command) on the computer. Folder access covers files only. Needs a Read & write folder to run. Omit to keep the saved choice."},
		"folders": map[string]any{"type": "array", "maxItems": 32, "description": "The complete selection for this Crew. Omitted previous folders lose access. Read permits listing and copying files into Crew; write also permits write_local_file (files only; commands are the separate commands switch).", "items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
			"id":     externalString("Keep the saved folder id when changing its access; omit for a new folder."),
			"path":   externalString("Absolute local folder path returned by browse."),
			"access": map[string]any{"type": "string", "enum": []any{"read", "write"}},
		}, "required": []any{"path", "access"}}},
	}, "action", "workspace_id")
	add("execute_local_shell_command", "Run a command on the connected laptop for a Crew you build. Requires commands turned on and a Read & write folder saved by manage_laptop_access. Commands start in that folder and run under your computer's existing permissions. Normal Crew shell stays on the server; server environment and secrets are never forwarded. A timed-out/disconnected call may have run: do not retry it blindly.", true, false, map[string]any{
		"workspace_id": externalString("Your Crew's workspace_id from workspace action=list."), "folder": externalString("Granted folder ID from manage_laptop_access action=get; optional when only one is granted."), "command": externalString("Command to run on your laptop."), "timeout": externalInteger(1, 300),
	}, "workspace_id", "command")
	add("list_local_files", "List one folder on the connected laptop for a Crew you build, using a granted Read or Read & write folder. path is relative to that folder; omit it for the folder itself. One level, in name order, a page at a time (200 by default; the reply gives total, truncated and next_offset); find a file in a big folder with query. An error is not an empty folder.", true, false, map[string]any{
		"workspace_id": externalString("Your Crew's workspace_id from workspace action=list."), "folder": externalString("Granted folder ID from manage_laptop_access action=get; optional when only one is granted."), "path": externalString("Folder inside the granted folder; omit for the folder itself."),
		"query": externalString("Only names containing this text (not case-sensitive), for finding a file in a big folder."),
		"limit": map[string]any{"type": "integer", "description": "Entries per page; default 200, at most 1000."}, "offset": map[string]any{"type": "integer", "description": "Where the page starts, from next_offset of the previous page."},
	}, "workspace_id")
	add("write_local_file", "Create or replace one text file in a Read & write folder on the connected laptop for a Crew you build. Replaces the whole file; text only, at most 11 MiB.", true, false, map[string]any{
		"workspace_id": externalString("Your Crew's workspace_id from workspace action=list."), "folder": externalString("Granted folder ID from manage_laptop_access action=get; optional when only one is writable."), "path": externalString("File path relative to the granted folder."), "content": externalString("The complete new text of the file."),
	}, "workspace_id", "path", "content")
	add("copy_local_file_to_crew", "Copy one local file into a Crew you own, using your granted Read or Read & write folder. local_path is relative to that laptop folder; crew_path is relative to the Crew on the server. Text or binary, at most 11 MiB; bytes go directly to the normal guarded Crew writer. Replaces the destination; no sync or server-to-laptop copy. File content is not returned through the model.", true, false, map[string]any{
		"workspace_id": externalString("Your Crew's workspace_id from workspace action=list."), "folder": externalString("Granted folder ID from manage_laptop_access action=get; optional when only one is granted."), "local_path": externalString("File relative to the granted laptop folder."), "crew_path": externalString("Editable destination relative to this Crew."),
	}, "workspace_id", "local_path", "crew_path")
}

func (api *StreamingAPI) externalLaptopAccessCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	if !personalLaptopSettingsClaims(c) {
		externalError(w, 403, "forbidden", "Use your own Builder connection to manage laptop access, or open Connections → Local laptop in your Crew.")
		return
	}
	crew, _, ok := api.resolveExternalWorkspace(r.Context(), c, externalArg(args, "workspace_id"))
	if !ok {
		externalError(w, 403, "forbidden", "Choose a Crew you own. Only Crew owners can use its connected laptop.")
		return
	}
	root := agentProfileRuntimeWorkspace(c.UserID, crew.Binding.WorkspacePath)
	if !crewProjectOwnedByCaller(c.UserID, root) {
		externalError(w, 403, "forbidden", "Choose a Crew you own. Only Crew owners can use its connected laptop.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.laptopAccessAction(w, r, c, root, args)
}

// laptopSetupSteps are the commands to put the AgentWorks program on a computer and connect it, built from this server's own
// public address; shown only while no computer is connected, so a chat can walk the person through first-time setup.
func laptopSetupSteps(noneConnected bool) map[string]any {
	base := strings.TrimRight(effectiveShareBaseURL(), "/")
	if !noneConnected || base == "" {
		return nil
	}
	return map[string]any{
		"mac_or_linux_install": "curl -fsSL '" + base + "/api/downloads/cli/install-agentworks.sh' | sh -s -- --server '" + base + "' --no-login",
		"windows_install":      "& ([scriptblock]::Create((Invoke-RestMethod '" + base + "/api/downloads/cli/install-agentworks.ps1'))) -Server '" + base + "' -NoLogin",
		"connect":              "agentworks start --server '" + base + "'",
		"steps":                "Run the install command in a terminal on that computer, then the connect command and sign in with the same account in the browser window it opens. Leave it running; then call devices again.",
	}
}

// laptopAccessAction runs one manage_laptop_access action for the Crew at root as the person c. The MCP tool and the Crew chat
// tool share it, so both apply the same owner-only checks and the same saved settings.
func (api *StreamingAPI) laptopAccessAction(w http.ResponseWriter, r *http.Request, c *UserClaims, root string, args map[string]any) {
	switch externalArg(args, "action") {
	case "devices":
		devices := api.localDeviceListForCrew(c, root)
		externalJSON(w, map[string]any{"devices": devices, "setup": laptopSetupSteps(len(devices) == 0), "next": "Choose your computer, browse its folders, then save Read or Read & write access. If none is connected, open Connections → Local laptop in your Crew for the download and sign-in steps. Install once on that computer, sign in and keep AgentWorks running (agentworks start for CLI users). The same app also connects AgentBridge."})
	case "get":
		settings, err := readLaptopSettings(c.UserID, root)
		if err != nil {
			externalError(w, 500, "settings_unavailable", "Laptop settings could not be read.")
			return
		}
		externalJSON(w, map[string]any{"settings": settings, "next": "Use devices, browse and set to choose laptop folders. For the first connection, open Connections → Local laptop in your Crew for the download/sign-in steps. Install once on that computer and keep AgentWorks running (agentworks start for CLI users); no folder is granted automatically. Normal Crew work stays on the server. Each folder is Read (list and copy files into the Crew) or Read & write (also write files). Terminal commands are a separate switch (commands) and also need a Read & write folder; they run under your computer's existing permissions."})
	case "browse":
		ctx, cancel := contextWithLaptopTimeout(r)
		defer cancel()
		response, err := api.laptopSettingsDeviceCall(ctx, c, externalArg(args, "device_id"), localfiles.Request{CrewID: root, Operation: "browse_folders", Path: externalArg(args, "path")})
		if err != nil {
			externalError(w, 400, "folder_unavailable", err.Error())
			return
		}
		externalJSON(w, response.Directory)
	case "set", "remove":
		settings := crewLaptopSettings{Folders: []localfiles.FolderSelection{}}
		if externalArg(args, "action") == "set" {
			if _, present := args["folders"]; !present || remarshalExternalArgs(args["folders"], &settings.Folders) != nil {
				externalError(w, 400, "invalid_arguments", "Send the complete folders selection, including an empty list to remove all folder access.")
				return
			}
			settings.DeviceID = externalArg(args, "device_id")
			if value, present := args["commands"].(bool); present {
				settings.Commands = value
			} else if previous, err := readLaptopSettings(c.UserID, root); err == nil {
				settings.Commands = previous.Commands
			}
		}
		data, _ := json.Marshal(settings)
		request := r.Clone(r.Context())
		request.Method = http.MethodPut
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.ContentLength = int64(len(data))
		api.serveCrewLaptopSettings(w, request, root)
	default:
		externalError(w, 400, "invalid_arguments", "Choose get, devices, browse, set or remove.")
	}
}
