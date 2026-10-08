package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type localDeviceConnection struct {
	hello     localfiles.Hello
	owner     string
	claims    *UserClaims
	conn      *websocket.Conn
	done      chan struct{}
	ready     chan struct{}
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan localfiles.Response
	slots     chan struct{}
}

type codeLocalFileTarget struct {
	DeviceID   string `json:"device_id"`
	ResourceID string `json:"resource_id"`
}

func (target *codeLocalFileTarget) valid() bool {
	return target != nil && localfiles.ValidID(target.DeviceID) && localfiles.ValidID(target.ResourceID)
}
func resolveCodeChatMode(mode string, target *codeLocalFileTarget) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if target != nil {
			return "local", nil
		}
		return "server", nil
	}
	if mode != "server" && mode != "local" {
		return "", fmt.Errorf("Code chat mode must be server or local")
	}
	if mode == "server" && target != nil {
		return "", fmt.Errorf("a local folder requires Local chat mode")
	}
	return mode, nil
}

func codeLocalModeTurn(req QueryRequest, profile *resolvedAgentProfile) bool {
	return (req.CodeChatMode == "local" || (req.CodeChatMode == "" && req.CodeLocalFiles != nil)) && profile != nil && profile.Definition.ID == "code" &&
		req.BotPlatform == "" && (strings.TrimSpace(req.TriggeredBy) == "" || req.TriggeredBy == "interactive") &&
		req.ParentSessionID == "" && req.SessionKind == "" && !req.IsAutoNotification && !req.PulseLifecycleTurn
}

func codeLocalFileTurn(req QueryRequest, profile *resolvedAgentProfile) bool {
	return req.CodeLocalFiles != nil && codeLocalModeTurn(req, profile)
}

// Narrow this turn's resolved copy; the server project and its existing
// schedules/connections remain unchanged. Offline bindings keep the policy.
func restrictCodeLocalFeatures(profile *agentprofiles.Profile) (map[string]bool, []string) {
	if profile == nil || profile.ID != "code" {
		return nil, nil
	}
	blockedFeatures := map[string]bool{"dashboard": true, "database": true, "schedules": true, "triggers": true, "bots": true, "mcp": true, "skills": true, "secrets": true, "background-work": true, "browser": true, "terminal": true, "attached-folders": true, "workflow-references": true, "knowledgebase": true}
	// Include Slack variants already narrowed out by Code's DM-only option,
	// so another registrar cannot redeclare a broader channel capability.
	blockedTools := map[string]bool{
		"get_report_link": true, "slack": true, "send_slack_message": true,
		"manage_my_mcp_servers": true, "read_skill": true, "execute_shell_command": true, "get_file_link": true, "diff_patch_workspace_file": true, "read_image": true, "delegate": true,
		"create_slack_bot_route": true, "update_slack_bot_route_permission": true, "remove_slack_bot_route": true,
	}
	blockedSkills, retainedSkills := map[string]bool{}, map[string]bool{}
	features := make([]agentprofiles.ResolvedFeature, 0, len(profile.ResolvedFeatures))
	for _, feature := range profile.ResolvedFeatures {
		if blockedFeatures[feature.ID] {
			for _, tool := range feature.Tools {
				blockedTools[tool] = true
			}
			for _, skill := range feature.Skills {
				if strings.HasPrefix(skill, "code-") {
					blockedSkills[skill] = true
				}
			}
			continue
		}
		features = append(features, feature)
		for _, skill := range feature.Skills {
			retainedSkills[skill] = true
		}
	}
	for skill := range retainedSkills {
		delete(blockedSkills, skill)
	}
	profile.ResolvedFeatures = features
	profile.Features = append([]agentprofiles.FeatureBinding(nil), profile.Features...)
	disabled := false
	for i := range profile.Features {
		if blockedFeatures[profile.Features[i].ID] {
			profile.Features[i].Enabled = &disabled
		}
	}
	for _, skill := range profile.Skills {
		blockedSkills[skill] = true
	}
	profile.Skills = nil
	// Models and UI controls remain available without their skill bundles.
	for i := range profile.ResolvedFeatures {
		profile.ResolvedFeatures[i].Skills = nil
		if profile.ResolvedFeatures[i].ID == "models" {
			profile.ResolvedFeatures[i].PromptExtension = "Model selection is available in the website's Models panel."
		}
		if profile.ResolvedFeatures[i].ID == "workspace-ui" {
			profile.ResolvedFeatures[i].PromptExtension = "The right side offers Local CLI connection, Costs and Models. Use list_ui_capabilities before requesting a view."
		}
	}
	profile.ToolPolicy.Enabled = withoutCodeLocalValues(profile.ToolPolicy.Enabled, blockedTools)
	names := make([]string, 0, len(blockedTools))
	for name := range blockedTools {
		names = append(names, name)
	}
	sort.Strings(names)
	profile.Runtime.Capabilities.WhatsApp = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.Secrets = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.MCPSelection = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.SkillSelection = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.WorkflowReferences = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.Browser = agentprofiles.CapabilityDisabled
	profile.Runtime.Capabilities.RawTerminal = agentprofiles.CapabilityDisabled
	profile.Runtime.AgentTools.Mode = "mcp_only"
	return blockedSkills, names
}

func withoutCodeLocalValues(values []string, blocked map[string]bool) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if !blocked[value] {
			kept = append(kept, value)
		}
	}
	return kept
}

func codeLocalFilesInstructions(target *codeLocalFileTarget) string {
	if target == nil {
		return "\nThis Code chat is in Local mode, with no folder selected yet. Only conversation and the Local CLI connection, Costs and Models views are available. Ask the user to connect their CLI and select a folder before file work. MCP connections, skills, secrets, background agents and server file/terminal/browser tools are disabled. Never substitute server files.\n"
	}
	return fmt.Sprintf("\nThis Code session's file access is connected to the user's computer: device_id=%q, resource_id=%q. Use list_local_files, read_local_file and write_local_file for the connected files, and execute_local_shell_command for laptop commands when granted with --shell; paths are relative to this folder. Read first for the revision and preserve request_id for identical write retries. The same chat, agent, model, project settings and server runtime remain in use. Dashboard/database, schedules/webhook triggers and built-in Slack, WhatsApp and Gmail/Google integrations are unavailable in this local mode; do not substitute shell commands, other connections or background work for those disabled features. Only this folder's local file and granted shell tools, conversation, UI controls, Costs and Models are available. MCP connections, reusable skills, project/Vault secrets, background delegation and server terminal/browser tools are disabled for this turn. Do not attempt to load or use saved connections, skills or secrets. The right side shows CLI connection controls instead of a file browser. If the device is offline, ordinary conversation can continue but local file and shell actions fail; report the connection issue and never substitute server files or copy the project to the server.\n", target.DeviceID, target.ResourceID)
}
func (api *StreamingAPI) validateCodeLocalFiles(claims *UserClaims, target *codeLocalFileTarget) error {
	if !websiteDeviceClaims(claims) || !target.valid() {
		return fmt.Errorf("local Code files require your website login and a valid folder selection")
	}
	// A selection hint grants no authority. File dispatch validates live device
	// ownership, authorization and grants; offline devices must not block chat.
	return nil
}

func (api *StreamingAPI) codeLocalFilePolicyKey(claims *UserClaims, target *codeLocalFileTarget, readOnly bool) string {
	for _, hello := range api.localDeviceList(claims) {
		if hello.DeviceID != target.DeviceID {
			continue
		}
		for _, resource := range hello.Resources {
			if resource.ID == target.ResourceID {
				data, _ := json.Marshal(struct {
					Resource localfiles.Resource `json:"resource"`
					ReadOnly bool                `json:"read_only"`
				}{resource, readOnly})
				return string(data)
			}
		}
	}
	return "offline"
}

func (device *localDeviceConnection) authorized(ctx context.Context) bool {
	t := device.claims.AccessToken
	if t == nil || !t.Allows("devices:connect") || !t.ExpiresAt.After(time.Now()) {
		return false
	}
	var current accesstokens.Token
	if strings.HasPrefix(t.ID, "oauth-") {
		store, err := openMCPOAuthStore()
		if err != nil {
			return false
		}
		defer store.Close()
		grant, err := store.ActiveFamily(ctx, strings.TrimPrefix(t.ID, "oauth-"))
		if err != nil {
			return false
		}
		current = mcpOAuthTokenForGrant(grant)
	} else {
		store, err := openAccessTokens()
		if err != nil {
			return false
		}
		defer store.Close()
		current, err = store.Active(ctx, t.ID, time.Now())
		if err != nil {
			return false
		}
	}
	if !current.Allows("devices:connect") || current.UserID != device.owner {
		return false
	}
	_, err := accessTokenClaims(current)
	return err == nil
}
func (api *StreamingAPI) handleLocalDeviceConnect(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken == nil || !claims.AccessToken.Allows("devices:connect") {
		externalError(w, 403, "insufficient_scope", "An explicitly approved devices:connect connection is required.")
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(localfiles.MaxMessageBytes)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var hello localfiles.Hello
	if err = conn.ReadJSON(&hello); err != nil || hello.Validate() != nil {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid executor handshake"), time.Now().Add(time.Second))
		return
	}
	device := &localDeviceConnection{hello: hello, owner: claims.UserID, claims: claims, conn: conn, done: make(chan struct{}), ready: make(chan struct{}), pending: map[string]chan localfiles.Response{}, slots: make(chan struct{}, 8)}
	if !device.authorized(r.Context()) {
		return
	}
	key := claims.UserID + "/" + hello.DeviceID
	if _, loaded := api.localDevices.LoadOrStore(key, device); loaded {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "device is already connected"), time.Now().Add(time.Second))
		return
	}
	defer func() { api.localDevices.CompareAndDelete(key, device); close(device.done) }()
	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err = conn.WriteJSON(map[string]bool{"connected": true}); err != nil {
		return
	}
	close(device.ready)
	conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-device.done:
				return
			case <-r.Context().Done():
				conn.Close()
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				active := device.authorized(ctx)
				cancel()
				if !active {
					conn.Close()
					return
				}
				device.writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				device.writeMu.Unlock()
				if err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	for {
		var response localfiles.Response
		if err = conn.ReadJSON(&response); err != nil {
			return
		}
		device.pendingMu.Lock()
		reply := device.pending[response.ID]
		device.pendingMu.Unlock()
		if reply != nil {
			select {
			case reply <- response:
			default:
			}
		}
	}
}
func websiteDeviceClaims(claims *UserClaims) bool {
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil || claims.Scope != "" || claims.ExecutionPrincipal != nil || claims.BotRouteGrant != "" {
		return false
	}
	switch claims.Provider {
	case "bot_route", "bot_owner", slackDMProvider:
		return false
	}
	return true
}
func (api *StreamingAPI) localDeviceList(claims *UserClaims) []localfiles.Hello {
	devices := []localfiles.Hello{}
	if !websiteDeviceClaims(claims) {
		return devices
	}
	if _, err := accessTokenClaims(accesstokens.Token{UserID: claims.UserID, Username: claims.Username, Email: claims.Email, Provider: claims.Provider}); err != nil {
		return devices
	}
	api.localDevices.Range(func(_, value any) bool {
		device := value.(*localDeviceConnection)
		if device.owner == claims.UserID {
			select {
			case <-device.ready:
				devices = append(devices, device.hello)
			default:
			}
		}
		return true
	})
	return devices
}
func (api *StreamingAPI) localDeviceCall(ctx context.Context, claims *UserClaims, deviceID string, request localfiles.Request) (localfiles.Response, error) {
	if !websiteDeviceClaims(claims) {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Local devices require their owner's website login"}
	}
	// Resolve the account live, including read-only status and disabled accounts.
	current, err := accessTokenClaims(accesstokens.Token{UserID: claims.UserID, Username: claims.Username, Email: claims.Email, Provider: claims.Provider})
	if err != nil {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Account is unavailable"}
	}
	if (request.Operation == "write" || request.Operation == "shell") && !userAccessForClaims(current).CanEdit {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Account is read-only"}
	}
	value, ok := api.localDevices.Load(claims.UserID + "/" + deviceID)
	if !ok {
		return localfiles.Response{}, &wf.FileError{Status: 404, Message: "Device is offline or unavailable"}
	}
	device := value.(*localDeviceConnection)
	// The acknowledgment must precede every request on the wire. A device may
	// reserve its ID before the acknowledgment has finished writing.
	select {
	case <-device.ready:
	case <-device.done:
		return localfiles.Response{}, &wf.FileError{Status: 503, Message: "Device disconnected during handshake"}
	case <-ctx.Done():
		return localfiles.Response{}, &wf.FileError{Status: 504, Message: "Device handshake timed out"}
	}
	if !device.authorized(ctx) {
		device.conn.Close()
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Device connection permission expired or was revoked"}
	}
	p, err := wf.CleanRelative(request.Path)
	if err != nil || wf.Private(p) {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Private or invalid file path"}
	}
	request.Path = p
	var resource *localfiles.Resource
	for i := range device.hello.Resources {
		if device.hello.Resources[i].ID == request.ResourceID {
			resource = &device.hello.Resources[i]
			break
		}
	}
	shell := request.Operation == "shell"
	write := request.Operation == "write"
	if resource == nil || !resource.Guard.Allows(p, write) || write && (!resource.Writable || wf.ProtectedWrite(p)) {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "File is outside device grants or protected"}
	}
	if shell {
		if !resource.Shell || !resource.Writable || !resource.Guard.Allows(p, true) {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Shell is not granted in this folder"}
		}
		if err := request.ValidateShell(); err != nil {
			return localfiles.Response{}, &wf.FileError{Status: 400, Message: err.Error()}
		}
	}
	if request.Operation != "read" && request.Operation != "list" && !write && !shell {
		return localfiles.Response{}, &wf.FileError{Status: 400, Message: "Unsupported device operation"}
	}
	if write && (request.RequestID == "" || request.ExpectedRevision == "" || len(request.RequestID) > 128 || len(request.Content) > wf.MaxFileBytes) {
		return localfiles.Response{}, &wf.FileError{Status: 400, Message: "Writes require a revision, request_id and bounded text"}
	}
	select {
	case device.slots <- struct{}{}:
		defer func() { <-device.slots }()
	default:
		return localfiles.Response{}, &wf.FileError{Status: 429, Message: "Device is busy"}
	}
	request.Identity = wf.EditIdentity{UserID: current.UserID, Username: current.Username, ConnectionID: device.claims.AccessToken.ID, Source: "server_local_executor", DeviceID: device.hello.DeviceID}
	request.ID = uuid.NewString()
	if shell {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(request.ShellTimeout()+10)*time.Second)
		defer cancel()
	}
	unknown := "write outcome may be unknown, reconcile with the same request_id"
	if shell {
		unknown = "command outcome may be unknown; reconnect and check the same request_id, never rerun automatically"
	}
	reply := make(chan localfiles.Response, 1)
	device.pendingMu.Lock()
	device.pending[request.ID] = reply
	device.pendingMu.Unlock()
	defer func() { device.pendingMu.Lock(); delete(device.pending, request.ID); device.pendingMu.Unlock() }()
	device.writeMu.Lock()
	device.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = device.conn.WriteJSON(request)
	device.writeMu.Unlock()
	if err != nil {
		device.conn.Close()
		return localfiles.Response{}, &wf.FileError{Status: 503, Message: "Device disconnected; " + unknown}
	}
	select {
	case response := <-reply:
		if response.Status != 200 {
			return response, &wf.FileError{Status: response.Status, Message: response.Error, Code: response.Code}
		}
		return response, nil
	case <-device.done:
		return localfiles.Response{}, &wf.FileError{Status: 503, Message: "Device disconnected; " + unknown}
	case <-ctx.Done():
		if shell {
			device.writeMu.Lock()
			device.conn.SetWriteDeadline(time.Now().Add(time.Second))
			cancelErr := device.conn.WriteJSON(localfiles.Request{ID: request.ID, Operation: "cancel"})
			device.writeMu.Unlock()
			if cancelErr != nil {
				device.conn.Close()
			}
		}
		return localfiles.Response{}, &wf.FileError{Status: 504, Message: "Device request timed out; " + unknown}
	}
}
func (api *StreamingAPI) handleLocalDevices(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !websiteDeviceClaims(claims) {
		externalError(w, 403, "forbidden", "Use your website login to access local devices.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"devices": api.localDeviceList(claims)})
}
func (api *StreamingAPI) registerLocalDeviceTools(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, target *codeLocalFileTarget) error {
	if !websiteDeviceClaims(claims) || !target.valid() {
		return nil
	}
	selected := *target
	writable, shellGranted := false, false
	for _, hello := range api.localDeviceList(claims) {
		if hello.DeviceID == selected.DeviceID {
			for _, resource := range hello.Resources {
				if resource.ID == selected.ResourceID {
					writable = resource.Writable
					shellGranted = resource.Shell
				}
			}
		}
	}
	for _, name := range []string{"list_local_devices", "list_local_files", "read_local_file", "write_local_file", "execute_local_shell_command"} {
		tool := name
		if tool == "write_local_file" && (readOnly || !writable || !userAccessForClaims(claims).CanEdit) {
			continue
		}
		if tool == "execute_local_shell_command" && (readOnly || !shellGranted || !writable || !userAccessForClaims(claims).CanEdit) {
			continue
		}
		props := map[string]interface{}{}
		required := []string{}
		description := "List your connected laptop executors and named folders. Files stay local; the agent and LLM run on this server."
		if tool != "list_local_devices" {
			description = "Access a file in an explicitly shared local folder. Paths are relative to the folder alias. Offline devices fail without a server-file fallback."
			for _, field := range []string{"device_id", "resource_id", "path"} {
				props[field] = map[string]interface{}{"type": "string"}
				required = append(required, field)
			}
		}
		description += fmt.Sprintf(" This Code uses only device_id=%q, resource_id=%q.", selected.DeviceID, selected.ResourceID)
		if tool == "write_local_file" {
			description += " Read first for expected_revision (missing for new files). Each write needs a unique request_id; identical retries return its receipt. Plans, databases and private files are protected."
			for _, field := range []string{"content", "expected_revision", "request_id"} {
				props[field] = map[string]interface{}{"type": "string"}
				required = append(required, field)
			}
		}
		if tool == "execute_local_shell_command" {
			description = "Run a sandboxed shell command on the selected laptop, not this server. path is the relative working directory (use . for the root). Use for builds, tests, git and other project commands. Explicit blocked/read-only paths remain enforced. Commands have broader project-file authority than raw file edits. Output is bounded; timeout_seconds defaults to 60 and is at most 300. Use a unique request_id; identical retries return a saved result, and an uncertain outcome must never be rerun automatically."
			props["command"] = map[string]interface{}{"type": "string", "minLength": 1, "maxLength": localfiles.MaxShellCommandBytes}
			props["request_id"] = map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128}
			props["timeout_seconds"] = map[string]interface{}{"type": "integer", "minimum": 1, "maximum": localfiles.MaxShellTimeout}
			required = append(required, "command", "request_id")
		}
		// A live local grant is the explicit capability declaration. External token
		// sessions cannot receive it, and the gate's deny overlays still apply.
		gate.Declare(tool)
		registrationTimeout := 40 * time.Second
		if tool == "execute_local_shell_command" {
			registrationTimeout = time.Duration(localfiles.MaxShellTimeout+15) * time.Second
		}
		err := registrar.RegisterCustomToolWithTimeout(tool, description, map[string]interface{}{"type": "object", "properties": props, "required": required, "additionalProperties": false}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			caller := GetUserFromContext(ctx)
			if caller == nil {
				caller = claims
			}
			if !websiteDeviceClaims(caller) || caller.UserID != claims.UserID {
				return "", errors.New("local device owner mismatch")
			}
			var result any
			if tool == "list_local_devices" {
				devices := []localfiles.Hello{}
				for _, hello := range api.localDeviceList(caller) {
					if hello.DeviceID != selected.DeviceID {
						continue
					}
					for _, resource := range hello.Resources {
						if resource.ID == selected.ResourceID {
							hello.Resources = []localfiles.Resource{resource}
							devices = append(devices, hello)
							break
						}
					}
				}
				result = map[string]any{"devices": devices}
			} else {
				if externalArg(args, "device_id") != selected.DeviceID || externalArg(args, "resource_id") != selected.ResourceID {
					return "", errors.New("file target differs from this Code's selected computer folder")
				}
				operation := map[string]string{"list_local_files": "list", "read_local_file": "read", "write_local_file": "write", "execute_local_shell_command": "shell"}[tool]
				request := localfiles.Request{ResourceID: externalArg(args, "resource_id"), Operation: operation, Path: externalArg(args, "path"), Content: externalArg(args, "content"), ExpectedRevision: externalArg(args, "expected_revision"), RequestID: externalArg(args, "request_id")}
				timeout := 35 * time.Second
				if operation == "shell" {
					request.Command = externalArg(args, "command")
					if raw, ok := args["timeout_seconds"]; ok {
						number, ok := raw.(float64)
						if !ok {
							if integer, isInt := raw.(int); isInt {
								number, ok = float64(integer), true
							}
						}
						if !ok || number < 1 || number > localfiles.MaxShellTimeout || number != float64(int(number)) {
							return "", errors.New("timeout_seconds must be an integer between 1 and 300")
						}
						request.TimeoutSeconds = int(number)
					}
					if err := request.ValidateShell(); err != nil {
						return "", err
					}
					timeout = time.Duration(request.ShellTimeout()+10) * time.Second
				}
				operationCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				response, err := api.localDeviceCall(operationCtx, caller, externalArg(args, "device_id"), request)
				if err != nil {
					return "", err
				}
				result = response
			}
			data, err := json.Marshal(result)
			return string(data), err
		}, registrationTimeout, "local_files")
		if err != nil {
			return fmt.Errorf("register %s: %w", tool, err)
		}
	}
	return nil
}
