package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const computerUseDescription = "Use apps on the owner's local computer from this Crew. action=status checks setup; install prepares the pinned agent-desktop runtime; permissions opens the OS permission guide; enable/disable changes this Crew's access only when the person asks; stop turns it off and cancels the current command (an already-sent click may finish). Before first use, guide returns the compiled desktop skill, then guide command=platform returns OS guidance; guide command=<CLI command> returns its help. run passes command and args as separate literal arguments, without a shell. Snapshot the app, act on its qualified refs, then inspect again. Use compact skeletons for dense apps. Set headed=true only when the person permits moving focus/physical input. Never retry an uncertain action automatically. Screenshots return a private local image path for your native image reader; never copy these into shared files. Hosted servers, shared users, schedules and file-only laptop connections cannot use this tool. The owner's phone/web messages may use it through the existing local Crew relay."

func computerUseProperties() map[string]any {
	return map[string]any{
		"action":  map[string]any{"type": "string", "enum": []string{"status", "install", "permissions", "enable", "disable", "stop", "guide", "run"}},
		"command": map[string]any{"type": "string", "description": "agent-desktop subcommand; for guide use platform or a command name"},
		"args":    map[string]any{"type": "array", "maxItems": 64, "items": map[string]any{"type": "string"}, "description": "Literal CLI arguments; AgentWorks manages the session and screenshot destinations"},
		"headed":  map[string]any{"type": "boolean", "description": "Allow physical input only when the person asks for it"},
	}
}
func decodeComputerUse(args map[string]any) (computerUseInput, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return computerUseInput{}, err
	}
	var in computerUseInput
	err = json.Unmarshal(data, &in)
	return in, err
}
func (api *StreamingAPI) registerComputerUseTool(registrar definitionToolRegistrar, gate *productToolGate, c *UserClaims, readOnly bool, session, root string) error {
	if readOnly || !computerUseAllowed(c) || !websiteDeviceClaims(c) || !crewProjectOwnedByCaller(c.UserID, root) {
		return nil
	}
	gate.Declare("computer_use")
	return registrar.RegisterCustomToolWithTimeout("computer_use", computerUseDescription, map[string]any{"type": "object", "properties": computerUseProperties(), "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, c, session, root)
		if err != nil {
			return "", err
		}
		in, err := decodeComputerUse(args)
		if err != nil {
			return "", err
		}
		out, err := api.computerUseAction(ctx, caller, root, session, in)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(out)
		return string(data), err
	}, 3*time.Minute, "workspace_advanced")
}

func externalComputerUseDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	if !localProductInstallation() || !IsLocalMode() || IsMultiUserMode() {
		return
	}
	props := computerUseProperties()
	props["crew_id"] = externalString("Your local Crew ID from workspace action=list")
	props["session"] = map[string]any{"type": "string", "maxLength": 128, "description": "A stable name for this coding chat, to keep its desktop references separate"}
	add("computer_use", computerUseDescription, true, false, props, "crew_id", "action", "session")
}
func (api *StreamingAPI) externalComputerUse(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	if !computerUseAllowed(c) || c.AccessToken != nil && !c.AccessToken.Allows("crews:write") {
		externalError(w, 403, "local_computer_required", "Use your own local Crew and personal connection for Computer use")
		return
	}
	root, ok := api.externalLaptopRoot(w, r, map[string]any{"crew_id": externalArg(args, "crew_id")})
	if !ok {
		return
	}
	if !isCrewProjectPath(root) {
		externalError(w, 403, "crew_required", "Computer use needs your own local Crew")
		return
	}
	session := externalArg(args, "session")
	if session == "" || len(session) > 128 {
		externalError(w, 400, "session_required", "Give this coding chat a stable session name")
		return
	}
	in, err := decodeComputerUse(args)
	if err == nil {
		var out any
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		connection := "website"
		if c.AccessToken != nil {
			connection = c.AccessToken.ID
		}
		out, err = api.computerUseAction(ctx, c, root, "mcp:"+connection+":"+session, in)
		if err == nil {
			w.Header().Set("Cache-Control", "no-store")
			externalJSON(w, out)
			return
		}
	}
	externalError(w, 400, "computer_use_unavailable", err.Error())
}
