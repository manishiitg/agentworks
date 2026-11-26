package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type nativeSkillRegistrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}

// The existing Skills tool gets the same project discovery, preview/import and
// invocation inventory as the screen. The captured project root is not input;
// every call still crosses the HTTP handlers' current owner/token/file checks.
func (api *StreamingAPI) registerNativeSkillTools(reg nativeSkillRegistrar, disabled func(string) bool, product, projectSkillsDir string) error {
	if product != "work" || projectSkillsDir == "" {
		return nil
	}
	root := strings.TrimSuffix(projectSkillsDir, "/skills")
	for _, operation := range []struct {
		name, description string
		write, invocation bool
	}{
		{"discover_native_skills", "Discover native skills for this own project without copying. Pass provider; optional id previews its instructions and source. Only server-admitted sources are read.", false, false},
		{"import_native_skill", "Explicitly copy one id returned by native_discover into this project's existing skills folder. Originals stay unchanged; does not select or execute it.", true, false},
		{"list_invocable_skills", "List this exact current chat's prepared native skills for explicit invocation. Pass session_id and provider; requested is not proof executed. Unsupported programs say so.", false, true},
	} {
		if disabled != nil && disabled(operation.name) {
			continue
		}
		properties := map[string]interface{}{"provider": map[string]any{"type": "string", "description": "Selected program, for example claude-code or codex-cli."}}
		required := []string{"provider"}
		if operation.invocation {
			properties["session_id"] = map[string]any{"type": "string", "description": "Exact current own chat session from sessions list."}
			required = append(required, "session_id")
		} else {
			properties["id"] = map[string]any{"type": "string", "description": "Opaque source ID from native_discover; omit to list."}
			if operation.write {
				required = append(required, "id")
			}
		}
		err := reg.RegisterCustomTool(operation.name, operation.description, map[string]any{"type": "object", "properties": properties, "required": required}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://internal.invalid", nil)
			if err != nil {
				return "", err
			}
			query := url.Values{"workspace_path": {root}, "provider": {externalArg(args, "provider")}}
			handler := api.nativeSkillsRoute(operation.write)
			method, endpoint := http.MethodGet, "/api/skills/discover"
			var body any
			if operation.invocation {
				query.Set("session_id", externalArg(args, "session_id"))
				handler = api.ownNativeSkillRoute(false, api.nativeSkillCatalogHandler)
				endpoint = "/api/skills/invocation"
			} else if operation.write {
				method = http.MethodPost
				body = map[string]string{"id": externalArg(args, "id")}
			} else {
				query.Set("id", externalArg(args, "id"))
			}
			status, response := scheduleHandler(r, handler, method, endpoint, nil, query, body)
			if status >= 400 {
				return "", fmt.Errorf("%s", strings.TrimSpace(string(response)))
			}
			return string(response), nil
		}, "skill_tools")
		if err != nil {
			return err
		}
	}
	return nil
}
