package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// The app Builder and external MCP call the same authorized service. The app
// tools are bound to their project and cannot retarget another workspace.
func (api *StreamingAPI) registerDashboardTools(reg definitionToolRegistrar, userID, root string) error {
	var definitions []externalTool
	dashboardToolDefinitions(func(name, description string, write, scoped bool, props map[string]any, required ...string) {
		if name == "get_report_link" {
			return
		}
		delete(props, "workspace")
		delete(props, "workflow_id")
		delete(props, "crew_id")
		keys := []any{}
		for _, key := range required {
			if key != "workspace" {
				keys = append(keys, key)
			}
		}
		definitions = append(definitions, externalTool{Name: name, Description: description, InputSchema: map[string]any{"type": "object", "properties": props, "required": keys, "additionalProperties": false}})
	})
	for _, def := range definitions {
		execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
			payload := map[string]any{"workspace": root, "action": dashboardActions[def.Name]}
			for key, value := range args {
				payload[key] = value
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return "", err
			}
			claims := GetUserFromContext(ctx)
			if claims == nil {
				claims = &UserClaims{UserID: userID, Username: userID}
				ctx = context.WithValue(ctx, UserContextKey, claims)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/dashboards", strings.NewReader(string(data))).WithContext(ctx)
			response := httptest.NewRecorder()
			api.handleDashboards(response, request)
			if response.Code != http.StatusOK {
				return "", fmt.Errorf("dashboard operation: %s", response.Body.String())
			}
			return response.Body.String(), nil
		}
		if err := reg.RegisterCustomToolWithTimeout(def.Name, def.Description, def.InputSchema, execute, 3*time.Minute, "dashboard"); err != nil {
			return err
		}
	}
	return nil
}
