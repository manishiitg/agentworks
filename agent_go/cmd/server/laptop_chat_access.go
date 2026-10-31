package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// registerLaptopAccessChatTool gives a Crew's Builder chat the same laptop setup the Local laptop panel and the MCP tool have:
// find the connected computers (with the install and connect commands when none is), browse its folders, save the folders and
// the commands switch, or remove access. It is offered whether or not a computer is already chosen, so first-time setup can be
// done from chat. Like the panel, it is a Crew owner's interactive turn only: a person using a shared Crew, a
// schedule or a connector gets nothing, and nothing here widens what those settings allow.
func (api *StreamingAPI) registerLaptopAccessChatTool(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string) error {
	if !websiteDeviceClaims(claims) || readOnly || !userAccessForClaims(claims).CanEdit || strings.TrimSpace(root) == "" {
		return nil
	}
	check := func(ctx context.Context) (*UserClaims, error) {
		return api.interactiveLaptopOwner(ctx, claims, sessionID, root)
	}
	gate.Declare("manage_laptop_access")
	return registrar.RegisterCustomToolWithTimeout("manage_laptop_access",
		"Connect your own computer to this project, the same as the Local laptop panel. action=devices lists your connected computers (when none is connected it also returns the install and connect commands for this server); get shows the saved computer, folders and whether terminal commands are on; browse lists the folder names on a computer to pick from; set saves the complete choice: device_id, folders (each Read or Read & write; Read lets the agent list and copy files, Read & write also write files) and commands (true gives the agent a full terminal on that computer, exactly as if you typed in your own terminal: your files, keys and logins, not limited to the folders; only turn it on when the person asks for it); remove disconnects the project. set replaces the whole folder list, so include every folder to keep. The new access applies from the next message. Nothing is shared automatically; change it only when the person asks.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":    map[string]interface{}{"type": "string", "enum": []string{"get", "devices", "browse", "set", "remove"}},
				"device_id": map[string]interface{}{"type": "string", "description": "A connected computer from action=devices; required for browse, and for set with folders"},
				"path":      map[string]interface{}{"type": "string", "description": "Folder to browse on the computer; omit to start from its home folder"},
				"folders": map[string]interface{}{"type": "array", "maxItems": 32, "description": "The complete selection for this project; omitted folders lose access. Keep a saved folder's id when changing it; omit the id for a new one",
					"items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
						"id":     map[string]interface{}{"type": "string"},
						"path":   map[string]interface{}{"type": "string", "description": "Absolute folder path returned by browse"},
						"access": map[string]interface{}{"type": "string", "enum": []string{"read", "write"}},
					}, "required": []string{"path", "access"}}},
				"commands": map[string]interface{}{"type": "boolean", "description": "Allow a full terminal on the computer (needs a Read & write folder to start in); omit to keep the saved choice"},
			},
			"required": []string{"action"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			caller, err := check(ctx)
			if err != nil {
				return "", err
			}
			request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
			recorder := httptest.NewRecorder()
			api.laptopAccessAction(recorder, request, caller, root, args)
			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code >= 400 {
				return "", errors.New(laptopActionErrorText(body))
			}
			return body, nil
		}, 60*time.Second, "workspace_advanced")
}

// laptopActionErrorText is the plain message inside an error response, or the response itself when it has no message.
func laptopActionErrorText(body string) string {
	var parsed struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && len(parsed.Error) > 0 {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(parsed.Error, &nested) == nil && nested.Message != "" {
			return nested.Message
		}
		var text string
		if json.Unmarshal(parsed.Error, &text) == nil && text != "" {
			return text
		}
	}
	if body == "" {
		return "Laptop settings could not be changed."
	}
	return body
}

// listIntArg reads a whole-number tool argument that may arrive as a float or an int; 0 when it is absent, negative or not a
// number.
func listIntArg(args map[string]interface{}, key string) int {
	switch value := args[key].(type) {
	case float64:
		if value > 0 && value == float64(int(value)) {
			return int(value)
		}
	case int:
		if value > 0 {
			return value
		}
	case int64:
		if value > 0 {
			return int(value)
		}
	}
	return 0
}

// Shared by laptop tools and explicit paired-project sync; no sender or schedule inherits it.
func (api *StreamingAPI) interactiveLaptopOwner(ctx context.Context, claims *UserClaims, sessionID, root string) (*UserClaims, error) {
	caller := GetUserFromContext(ctx)
	if caller == nil {
		caller = claims
	}
	if !websiteDeviceClaims(caller) || caller.UserID != claims.UserID {
		return nil, errors.New("Laptop settings belong to your project's Builder chat")
	}
	current, err := accessTokenClaims(accesstokens.Token{UserID: caller.UserID, Username: caller.Username, Email: caller.Email, Provider: caller.Provider})
	if err != nil || !userAccessForClaims(current).CanEdit {
		return nil, errors.New("Your account no longer has edit access")
	}
	api.lastQueryMu.RLock()
	req := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if req.userID != caller.UserID || api.isSyntheticTurn(sessionID) || !laptopBuilderTurn(req, laptopProfileForRequest(req)) {
		return nil, errors.New("Laptop settings need your own interactive chat turn")
	}
	if !laptopProjectAllowed(ctx, caller, req.SelectedFolder) || !workspacePathsMatchForUser(caller.UserID, req.SelectedFolder, root) {
		return nil, errors.New("Only a project Builder can change its laptop settings")
	}
	return caller, nil
}
