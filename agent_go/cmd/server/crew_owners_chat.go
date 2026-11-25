package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"
)

// registerCrewOwnersChatTool gives a server project's own chat the crew_owners tool, so the chat can say who its
// co-owners and Users are and change them as the Access panel does. It was only an MCP tool, so a project chat asked
// about its Users found nothing and said it could not see them. Same code as MCP (externalCrewOwnersCall); a User is
// refused by the setup lock (crew_setup_lock.go lists crew_owners).
func (api *StreamingAPI) registerCrewOwnersChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, root string) error {
	if claims == nil || claims.UserID == "" || readOnly {
		return nil
	}
	rec, ok := crewOwnerRecordAt(root)
	if !ok || !crewLivesOnServer(rec.ProjectID) {
		return nil
	}
	projectID := rec.ProjectID
	var description string
	var props map[string]any
	externalCrewOwnersDefinition(func(_ string, d string, _ bool, _ bool, p map[string]any, _ ...string) {
		description, props = d, p
	})
	delete(props, "crew_id")
	gate.Declare(externalCrewOwnersTool)
	return reg.RegisterCustomToolWithTimeout(externalCrewOwnersTool, description+" This project only.", map[string]any{"type": "object", "properties": props, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		call := map[string]any{}
		for k, v := range args {
			call[k] = v
		}
		call["crew_id"] = projectID
		body, _ := json.Marshal(call)
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, claims))
		w := httptest.NewRecorder()
		api.externalCrewOwnersCall(w, r, call)
		if w.Code >= 300 {
			return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
		}
		return w.Body.String(), nil
	}, time.Minute, "workspace_advanced")
}
