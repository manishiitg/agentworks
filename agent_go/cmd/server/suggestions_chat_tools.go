package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"
)

// registerCrewNeedsYouChatTools gives a Crew's Builder chat what the Suggestions panel and Needs you have: see what waits on the
// owner in this Crew (suggestions from people using it, decisions, live questions), answer one, or dismiss it with a reason. They
// run the same code as the MCP tools, narrowed to this Crew, so answering still needs write access to it (people using a shared
// Crew can submit suggestions but never decide them); nothing is registered in a read-only chat.
func (api *StreamingAPI) registerCrewNeedsYouChatTools(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, root string) error {
	if claims == nil || claims.UserID == "" || readOnly || !userAccessForClaims(claims).CanEdit || strings.TrimSpace(root) == "" {
		return nil
	}
	label := filepath.Base(root)
	run := func(name string) func(context.Context, map[string]interface{}) (string, error) {
		return func(ctx context.Context, args map[string]interface{}) (string, error) {
			caller := GetUserFromContext(ctx)
			if caller == nil {
				caller = claims
			}
			if caller.UserID != claims.UserID {
				return "", errors.New("These items belong to the Crew owner's own chat")
			}
			request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
			recorder := httptest.NewRecorder()
			places := []needsYouPlace{{Kind: "crew", ID: label, Label: label, path: root}}
			api.needsYouRespond(recorder, request, name, args, caller, places, true)
			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code >= 400 {
				return "", errors.New(laptopActionErrorText(body))
			}
			return body, nil
		}
	}
	for _, tool := range []struct {
		name, description string
		properties        map[string]interface{}
		required          []string
	}{
		{"list_needs_you", "List what waits on you in this Crew, the same as the Suggestions panel and Needs you: suggestions sent by people using the Crew, open decisions (with the recommendation when there is one) and live questions. Urgent first. Answer one with answer_needs_you or dismiss it with dismiss_needs_you.", map[string]interface{}{}, nil},
		{"answer_needs_you", "Answer or dismiss one item from list_needs_you for this Crew. Pass option (an option id or title) or answer (free text), or dismiss=true for a decision or suggestion. A live question goes back to the paused chat or run. For an apply_decision item, send the returned apply_message to the Crew's Builder chat. Needs write access to this Crew.", map[string]interface{}{
			"id":      map[string]interface{}{"type": "string", "description": "Item id from list_needs_you"},
			"option":  map[string]interface{}{"type": "string", "description": "Chosen option id or title, or your own choice when free text is allowed"},
			"answer":  map[string]interface{}{"type": "string", "maxLength": 8000, "description": "Free-text answer or note"},
			"dismiss": map[string]interface{}{"type": "boolean", "description": "Dismiss a decision or suggestion without answering"},
		}, []string{"id"}},
		{"dismiss_needs_you", "Dismiss an open decision or suggestion in this Crew with a reason; it records who dismissed it and why, and does not approve or apply it. Live questions cannot be dismissed.", map[string]interface{}{
			"id":     map[string]interface{}{"type": "string", "description": "Item id from list_needs_you"},
			"reason": map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 8000, "description": "Why it no longer needs an answer, for example it was superseded"},
		}, []string{"id", "reason"}},
	} {
		gate.Declare(tool.name)
		schema := map[string]interface{}{"type": "object", "properties": tool.properties}
		if len(tool.required) > 0 {
			schema["required"] = tool.required
		}
		if err := registrar.RegisterCustomToolWithTimeout(tool.name, tool.description, schema, run(tool.name), 60*time.Second, "workspace_advanced"); err != nil {
			return err
		}
	}
	return nil
}
