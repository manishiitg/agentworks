package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"log"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
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

type laptopConnectionTarget struct {
	DeviceID   string `json:"device_id"`
	ResourceID string `json:"resource_id,omitempty"`
}

func (target *laptopConnectionTarget) valid() bool {
	return target != nil && localfiles.ValidID(target.DeviceID)
}

// A connection is extra authority for one person's live Builder turn. It never
// follows the Crew into Run callers, schedules, child agents or synthetic turns.
func laptopConnectionTurn(req QueryRequest, profile *resolvedAgentProfile) bool {
	return req.LaptopConnection != nil && profile != nil && isProjectProfileID(profile.Definition.ID) &&
		!req.PinRunMode && req.BotPlatform == "" && (strings.TrimSpace(req.TriggeredBy) == "" || req.TriggeredBy == "interactive") &&
		req.ParentSessionID == "" && req.SessionKind == "" && !req.IsAutoNotification && !req.PulseLifecycleTurn
}

func laptopConnectionInstructions() string {
	return "\nThis Crew runs on the server. Ordinary shell, files, skills, connections, secrets, functions and background work use the server. The owner may allow laptop folders in Crew Settings. Read grants provide copy_local_file_to_crew; Read & write also enables execute_local_shell_command. Each tool lists the allowed folders and their IDs. Use the local shell only when work needs that connected computer; paths are relative to its granted folder. Copy local files explicitly into the Crew when needed; there is no automatic sync. Local output and copied files reach this server and the model and may be saved in chat history. Never put server secrets, SECRET_* values, AGENTWORKS_* environment values or login data into a laptop command. If the laptop is not connected, explain that the local action is unavailable and continue server work; never retry an uncertain command blindly.\n"
}

// Retained tool callbacks are reused between turns. Check the live turn and
// binding on every call, rather than trusting their original registration.
func (api *StreamingAPI) laptopTurnAllowed(sessionID, owner string, target laptopConnectionTarget) bool {
	api.lastQueryMu.RLock()
	req, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok || req.userID != owner || req.LaptopConnection == nil || *req.LaptopConnection != target || api.isSyntheticTurn(sessionID) {
		return false
	}
	return laptopConnectionTurn(req, &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: req.AgentProfileID}})
}

func (api *StreamingAPI) validateLaptopConnection(claims *UserClaims, target *laptopConnectionTarget) error {
	if !websiteDeviceClaims(claims) || !target.valid() {
		return fmt.Errorf("A laptop connection requires your website login and a valid folder selection")
	}
	// A selection hint grants no authority. File dispatch validates live device
	// ownership, authorization and grants; offline devices must not block chat.
	return nil
}

func (api *StreamingAPI) laptopConnectionPolicyKey(claims *UserClaims, target *laptopConnectionTarget, readOnly bool, root string) string {
	settings, err := readLaptopSettings(claims.UserID, root)
	if err != nil {
		return "unavailable"
	}
	data, _ := json.Marshal([]any{settings, readOnly})
	return string(data)
}

func (device *localDeviceConnection) authorized(ctx context.Context) bool {
	t := device.claims.AccessToken
	if t == nil || !t.Allows("devices:connect") {
		return false
	}
	// The socket authenticated once, with an access token that lasts an hour, and the CLI never renews it on a live socket.
	// An OAuth connection therefore lives as long as its grant does (sign-out, revocation or a lost role closes it at the next
	// check, within 25 s); cutting it at the access token's expiry dropped every Local connection once an hour (PLAT-826).
	oauth := strings.HasPrefix(t.ID, "oauth-")
	if !oauth && !t.ExpiresAt.After(time.Now()) {
		return false
	}
	var current accesstokens.Token
	if oauth {
		store, err := openMCPOAuthStore()
		if err != nil {
			return false
		}
		defer store.Close()
		grant, err := store.LiveFamily(ctx, strings.TrimPrefix(t.ID, "oauth-"))
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
	owner, agentID, deviceID := "", "", ""
	if claims != nil {
		owner = claims.UserID
		if claims.AccessToken != nil {
			agentID = claims.AccessToken.BoundAgentID
		}
	}
	upgraded := false
	connectionEvent := func(status int, check string) {
		httpStatus := status
		if upgraded {
			httpStatus = http.StatusSwitchingProtocols
		}
		log.Printf("[LOCAL-DEVICE] http_status=%d status=%d check=%s user=%q agent=%q device=%q", httpStatus, status, check, owner, agentID, deviceID)
	}
	if claims == nil || claims.AccessToken == nil || !claims.AccessToken.Allows("devices:connect") {
		connectionEvent(http.StatusForbidden, "device_consent")
		externalError(w, 403, "insufficient_scope", "This connection does not have permission to share local folders. Open Settings → Laptop connection in the app and follow its setup to approve access.")
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, Error: func(w http.ResponseWriter, r *http.Request, status int, _ error) {
		connectionEvent(status, "websocket_upgrade")
		externalError(w, status, "executor_upgrade_failed", "The local folder connection could not be opened. Check that your network allows live connections, or contact your administrator.")
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	upgraded = true
	defer conn.Close()
	conn.SetReadLimit(localfiles.MaxMessageBytes)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var hello localfiles.Hello
	if err = conn.ReadJSON(&hello); err != nil || hello.Validate() != nil {
		connectionEvent(http.StatusBadRequest, "executor_handshake")
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid executor handshake"), time.Now().Add(time.Second))
		return
	}
	deviceID = hello.DeviceID
	device := &localDeviceConnection{hello: hello, owner: claims.UserID, claims: claims, conn: conn, done: make(chan struct{}), ready: make(chan struct{}), pending: map[string]chan localfiles.Response{}, slots: make(chan struct{}, 8)}
	if !device.authorized(r.Context()) {
		connectionEvent(http.StatusForbidden, "live_device_authorization")
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "device connection is no longer authorized"), time.Now().Add(time.Second))
		return
	}
	key := claims.UserID + "/" + hello.DeviceID
	if _, loaded := api.localDevices.LoadOrStore(key, device); loaded {
		connectionEvent(http.StatusConflict, "device_already_connected")
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "device is already connected"), time.Now().Add(time.Second))
		return
	}
	defer func() { api.localDevices.CompareAndDelete(key, device); close(device.done) }()
	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err = conn.WriteJSON(map[string]bool{"connected": true}); err != nil {
		connectionEvent(http.StatusBadGateway, "connection_ack")
		return
	}
	connectionEvent(http.StatusSwitchingProtocols, "connected")
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
					connectionEvent(http.StatusForbidden, "live_device_authorization")
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
	if (request.Operation == "write" || request.Operation == "shell" || request.Operation == "patch") && !userAccessForClaims(current).CanEdit {
		return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Account is read-only"}
	}
	value, ok := api.localDevices.Load(claims.UserID + "/" + deviceID)
	if !ok {
		return localfiles.Response{}, &wf.FileError{Status: 404, Message: "Your laptop is not connected. Start AgentWorks on that computer to use laptop tools; server work remains available."}
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
	patch := request.Operation == "patch"
	shell := request.Operation == "shell"
	write := request.Operation == "write"
	var resource *localfiles.Resource
	control := request.Operation == "browse_folders" || request.Operation == "validate_folders"
	if !device.hello.SettingsManaged {
		return localfiles.Response{}, &wf.FileError{Status: 409, Message: "Update the CLI, run agentworks stop then agentworks start, and choose folders in Crew Settings."}
	}
	if !control {
		// Callers supply only the folder ID; authority is loaded from server-owned Crew settings now.
		folders, err := currentLaptopFolder(claims.UserID, request.CrewID, deviceID, request.ResourceID, shell || write || patch)
		if err != nil {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: err.Error()}
		}
		request.Folders = folders
		if request.ResourceID == "" && len(folders) == 1 {
			request.ResourceID = folders[0].ID
		}
		for _, folder := range folders {
			if folder.ID == request.ResourceID {
				guard := wf.FolderGuard{ReadPaths: []string{"."}}
				if folder.Access == "write" {
					guard.WritePaths = []string{"."}
				}
				resource = &localfiles.Resource{ID: folder.ID, Writable: folder.Access == "write", Shell: folder.Access == "write", Patch: folder.Access == "write", Guard: guard}
			}
		}
		if resource == nil {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Folder is not allowed for this Crew"}
		}
	}
	if control {
		if !userAccessForClaims(current).CanEdit {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Your account cannot manage laptop access"}
		}
	} else if patch {
		// All diff targets, including absolute local paths and multi-file patches,
		// are checked independently by the file-owning CLI before any write.
		if (!resource.Writable && !resource.Downloads) || !resource.Patch || request.RequestID == "" || len(request.RequestID) > 128 || len(request.Content) > wf.MaxFileBytes {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Patch requires a writable folder, current CLI and bounded diff"}
		}
	} else {
		p, err := wf.CleanRelative(request.Path)
		if err != nil || wf.Private(p) {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Private or invalid file path"}
		}
		request.Path = p
		if !resource.Guard.Allows(p, write) || write && (!resource.Writable || wf.ProtectedWrite(p)) {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "File is outside device grants or protected"}
		}
		if shell {
			if !resource.Shell || !resource.Guard.Allows(p, resource.Writable) {
				return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Shell requires a current CLI and an allowed working directory"}
			}
			if err := request.ValidateShell(); err != nil {
				return localfiles.Response{}, &wf.FileError{Status: 400, Message: err.Error()}
			}
		}
		if request.Operation == "publish_snapshot" && (!resource.Publication || !userAccessForClaims(current).CanEdit) {
			return localfiles.Response{}, &wf.FileError{Status: 403, Message: "Update and reconnect the CLI, then publish from a project you may build."}
		}
		if request.Operation != "copy_read" && request.Operation != "read" && request.Operation != "list" && request.Operation != "publish_snapshot" && !write && !shell {
			return localfiles.Response{}, &wf.FileError{Status: 400, Message: "Unsupported device operation"}
		}
		if write && (request.RequestID == "" || request.ExpectedRevision == "" || len(request.RequestID) > 128 || len(request.Content) > wf.MaxFileBytes) {
			return localfiles.Response{}, &wf.FileError{Status: 400, Message: "Writes require a revision, request_id and bounded text"}
		}
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
	if patch {
		unknown = "patch outcome may be unknown; reconnect and inspect files before issuing another patch"
	}
	if shell {
		unknown = "command outcome may be unknown; reconnect and inspect local files before issuing another command"
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
		if shell || patch {
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

// Reuse the bridge's canonical names and schemas; only the execution target changes.
func (api *StreamingAPI) registerLaptopTools(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string, target *laptopConnectionTarget) error {
	if !websiteDeviceClaims(claims) || !target.valid() || readOnly || !userAccessForClaims(claims).CanEdit {
		return nil
	}
	selected := *target
	settings, err := readLaptopSettings(claims.UserID, root)
	if err != nil || settings.DeviceID != selected.DeviceID || len(settings.Folders) == 0 {
		return nil
	}
	folderInfo, _ := json.Marshal(settings.Folders)
	allowedFolders := " Allowed local folders (use the ID in folder): " + string(folderInfo)
	check := func(ctx context.Context) (*UserClaims, error) {
		if !api.laptopTurnAllowed(sessionID, claims.UserID, selected) {
			log.Printf("[LAPTOP_TOOL] session=%s caller=%s operation=deny reason=current_interactive_owner_turn", sessionID, claims.UserID)
			return nil, errors.New("Laptop tools require your current interactive Builder turn and selected folder")
		}
		caller := GetUserFromContext(ctx)
		if caller == nil {
			caller = claims
		}
		if !websiteDeviceClaims(caller) || caller.UserID != claims.UserID {
			return nil, errors.New("Laptop connection owner mismatch")
		}
		current, err := accessTokenClaims(accesstokens.Token{UserID: caller.UserID, Username: caller.Username, Email: caller.Email, Provider: caller.Provider})
		if err != nil || !userAccessForClaims(current).CanEdit {
			return nil, errors.New("Your account no longer has edit access")
		}
		api.lastQueryMu.RLock()
		req := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		if !crewProjectOwnedByCallerStrict(caller.UserID, agentProfileRuntimeWorkspace(caller.UserID, req.SelectedFolder)) || !workspacePathsMatchForUser(caller.UserID, req.SelectedFolder, root) {
			return nil, errors.New("Laptop tools require your own Crew's Builder chat")
		}
		settings, err := readLaptopSettings(caller.UserID, root)
		if err != nil || settings.DeviceID != selected.DeviceID || len(settings.Folders) == 0 {
			return nil, errors.New("Laptop access was removed from this Crew")
		}
		return caller, nil
	}
	gate.Declare("copy_local_file_to_crew")
	hasWrite := false
	for _, folder := range settings.Folders {
		if folder.Access == "write" {
			hasWrite = true
		}
	}
	if hasWrite {
		gate.Declare("execute_local_shell_command")
		if err := registrar.RegisterCustomToolWithTimeout("execute_local_shell_command",
			"Run a command on your connected computer in its selected shared folder. Ordinary execute_shell_command runs on the server. Use this tool for local files, programs, builds, tests and git. Commands require Read & write for that folder and run as you on this computer, with the existing operating-system permissions. Output reaches the server/model. No server environment or secrets are forwarded. Paths start in the granted folder. timeout defaults to 60 seconds, maximum 300. If the laptop is disconnected, server work remains available."+allowedFolders,
			map[string]interface{}{"type": "object", "properties": map[string]interface{}{"folder": map[string]interface{}{"type": "string", "description": "Folder ID from your Crew Laptop access settings; optional when only one is allowed"}, "command": map[string]interface{}{"type": "string"}, "timeout": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 300}}, "required": []string{"command"}},
			func(ctx context.Context, args map[string]interface{}) (string, error) {
				caller, err := check(ctx)
				if err != nil {
					return "", err
				}
				request := localfiles.Request{CrewID: root, ResourceID: externalArg(args, "folder"), RequestID: uuid.NewString(), Operation: "shell", Path: ".", Command: externalArg(args, "command")}
				if raw, ok := args["timeout"]; ok {
					value, ok := raw.(float64)
					if !ok {
						if n, yes := raw.(int); yes {
							value, ok = float64(n), true
						}
					}
					if !ok || value < 1 || value > localfiles.MaxShellTimeout || value != float64(int(value)) {
						return "", errors.New("timeout must be an integer from 1 to 300")
					}
					request.TimeoutSeconds = int(value)
				}
				if err := request.ValidateShell(); err != nil {
					return "", err
				}
				opCtx, cancel := context.WithTimeout(ctx, time.Duration(request.ShellTimeout()+10)*time.Second)
				defer cancel()
				response, err := api.localDeviceCall(opCtx, caller, selected.DeviceID, request)
				if err != nil {
					return "", err
				}
				if response.Shell == nil {
					return "", errors.New("Laptop returned no command result")
				}
				result := response.Shell
				log.Printf("[LAPTOP_TOOL] session=%s owner=%s operation=shell request=%s exit=%d", sessionID, caller.UserID, request.RequestID, result.ExitCode)
				return workspace.MarshalShellResultForAgent(workspace.ShellCommandResult{Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, TimedOut: result.TimedOut})
			}, time.Duration(localfiles.MaxShellTimeout+15)*time.Second, "workspace_advanced"); err != nil {
			return err
		}
	}
	return registrar.RegisterCustomToolWithTimeout("copy_local_file_to_crew",
		"Copy one file from your connected computer's shared folder into this server Crew. local_path and crew_path are relative to their respective project folders. Supports text and binary files up to 11 MiB. Copies bytes directly, without sending file content through the model. Existing destination files are replaced through the Crew's normal guarded writer. Private files and protected Crew paths are excluded. No automatic sync; the original laptop file is unchanged."+allowedFolders,
		map[string]interface{}{"type": "object", "properties": map[string]interface{}{"folder": map[string]interface{}{"type": "string", "description": "Folder ID from your Crew Laptop access settings; optional when only one is allowed"}, "local_path": map[string]interface{}{"type": "string"}, "crew_path": map[string]interface{}{"type": "string"}}, "required": []string{"local_path", "crew_path"}},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			caller, err := check(ctx)
			if err != nil {
				return "", err
			}
			destination, err := wf.CleanRelative(externalArg(args, "crew_path"))
			if err != nil || destination == "." || wf.ProtectedWrite(destination) {
				return "", errors.New("Choose an editable file path inside this Crew")
			}
			if _, err := crewSpecFilePath(root, destination); err != nil {
				return "", err
			}
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
			client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(caller.UserID))
			if err := client.ValidatePathWithContext(ctx, path.Join(root, destination), true); err != nil {
				return "", err
			}
			if reason := publishedWriteRefusal(ctx, path.Join(root, destination)); reason != "" {
				return "", errors.New(reason)
			}
			current, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "read", Path: destination})
			if err != nil {
				return "", err
			}
			expected := wf.MissingRevision
			if current.Exists {
				expected = current.Revision
			}
			id := uuid.NewString()
			response, err := api.localDeviceCall(ctx, caller, selected.DeviceID, localfiles.Request{CrewID: root, ResourceID: externalArg(args, "folder"), Operation: "copy_read", Path: externalArg(args, "local_path"), RequestID: id})
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "unsupported operation") {
					return "", errors.New("Update AgentWorks on your laptop, restart it and choose the connected folder again to copy files")
				}
				return "", err
			}
			if response.Bytes == nil || len(response.Bytes) > wf.MaxBinaryFileBytes {
				return "", errors.New("Laptop did not return a file of at most 11 MiB; update its CLI and reconnect")
			}
			// Recheck the current turn after transfer, before the server write.
			if _, err := check(ctx); err != nil {
				return "", err
			}
			request := wf.WriteRequest{Root: root, Path: destination, ContentBase64: base64.StdEncoding.EncodeToString(response.Bytes), ExpectedRevision: expected, RequestID: id, Actor: caller.UserID, Identity: wf.EditIdentity{UserID: caller.UserID, Username: caller.Username, Source: "laptop_copy"}}
			recorder := httptest.NewRecorder()
			externalPostSharedFileWrite(recorder, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), request)
			if recorder.Code != http.StatusOK {
				return "", fmt.Errorf("Crew file copy rejected: %s", strings.TrimSpace(recorder.Body.String()))
			}
			log.Printf("[LAPTOP_TOOL] session=%s owner=%s operation=copy_in request=%s destination=%q bytes=%d", sessionID, caller.UserID, id, destination, len(response.Bytes))
			return recorder.Body.String(), nil
		}, 180*time.Second, "workspace_advanced")
}
