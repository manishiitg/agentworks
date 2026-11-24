package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

// Personal profile changes have no target-user field. Image references pass
// through the very same Crew read admission as read_crew_file, including token,
// publication, private-path and folder fences. No arbitrary host path or URL.
func (api *StreamingAPI) externalUserAvatar(w http.ResponseWriter, r *http.Request, args map[string]any) {
	var req struct {
		Operation string      `json:"operation"`
		Icon      string      `json:"icon"`
		Color     string      `json:"color"`
		Crop      *avatarCrop `json:"crop"`
		ImageFile *struct {
			CrewID  string `json:"crew_id"`
			Path    string `json:"path"`
			AgentID string `json:"agent_id,omitempty"`
		} `json:"image_file"`
	}
	data, _ := json.Marshal(args)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		http.Error(w, "Use the offered profile picture fields.", 400)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || userAccessForClaims(claims).Disabled || claims.Scope != "" || (claims.AccessToken != nil && claims.AccessToken.FileGuard != nil) {
		http.Error(w, "Use your own account to change your profile picture.", 403)
		return
	}
	input := userAvatarRequest{Operation: req.Operation, Icon: req.Icon, Color: req.Color, Crop: req.Crop}
	if req.ImageFile != nil {
		if req.Operation != "set" || req.Icon != "" || req.ImageFile.CrewID == "" || req.ImageFile.Path == "" {
			http.Error(w, "Choose an admitted project image for set, or an icon, not both.", 400)
			return
		}
		response := httptest.NewRecorder()
		api.externalCrewCall(response, r, "read_crew_file", map[string]any{"crew_id": req.ImageFile.CrewID, "path": req.ImageFile.Path, "agent_id": req.ImageFile.AgentID})
		var file struct {
			Encoding string `json:"encoding"`
			Content  string `json:"content_base64"`
			Size     int64  `json:"size"`
		}
		if response.Code >= 400 || json.Unmarshal(response.Body.Bytes(), &file) != nil || file.Encoding != "base64" || file.Size > avatarInputBytes || len(file.Content) > ((avatarInputBytes+2)/3)*4 {
			http.Error(w, "Choose a PNG, JPEG or WebP image under 5 MB in a project you can read.", 400)
			return
		}
		var err error
		input.Image, err = base64.StdEncoding.DecodeString(file.Content)
		if err != nil {
			http.Error(w, "This image could not be read.", 400)
			return
		}
	}
	api.userAvatar(w, r, input)
}
func userAvatarToolDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("profile_picture", "Read, set or remove your own profile picture. operation=get|set|remove. set uses an offered icon and colour, or image_file with the project ID and relative path of an attached/admitted PNG, JPEG or WebP (under 5 MB). Never send image bytes, a host path or URL. Photos become small square JPEGs; optional crop has zoom 1..8 and x/y 0..1. This changes only your authenticated account, never somebody else's picture.", true, false, map[string]any{
		"operation":  map[string]any{"type": "string", "enum": []any{"get", "set", "remove"}},
		"icon":       map[string]any{"type": "string", "enum": []any{"person", "rocket", "star", "leaf", "bot"}},
		"color":      map[string]any{"type": "string", "enum": []any{"blue", "purple", "green", "orange"}},
		"image_file": map[string]any{"type": "object", "additionalProperties": false, "required": []any{"crew_id", "path"}, "properties": map[string]any{"crew_id": externalString("Project containing the image."), "path": externalString("Relative admitted file path, including an uploaded chat attachment."), "agent_id": externalString("Publication agent context, when needed for this file.")}},
		"crop":       map[string]any{"type": "object", "additionalProperties": false, "required": []any{"zoom", "x", "y"}, "properties": map[string]any{"zoom": map[string]any{"type": "number", "minimum": 1, "maximum": 8}, "x": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "y": map[string]any{"type": "number", "minimum": 0, "maximum": 1}}},
	})
}
