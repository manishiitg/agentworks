package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"
)

// registerWhatsAppChatTool gives a Crew's Builder chat the WhatsApp card's controls that do not need a phone in hand: see whether
// the person's WhatsApp is paired and connected, see which slugs route to which Crews and Goals, and unpair. It runs the card's own
// handlers as the person, so it is their own pairing only. Pairing itself is the QR scan on the person's phone, which stays in the
// app (owner decision 2026-10-09: a paired phone acts as its owner, so pairing stays behind a browser sign-in); pair_help only says
// where. Offered only where the server has the WhatsApp connector, and never in a read-only chat.
func (api *StreamingAPI) registerWhatsAppChatTool(registrar definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool) error {
	if api == nil || api.whatsappManager == nil || claims == nil || claims.UserID == "" || readOnly || !userAccessForClaims(claims).CanEdit {
		return nil
	}
	gate.Declare("manage_whatsapp")
	return registrar.RegisterCustomToolWithTimeout("manage_whatsapp",
		"Your own WhatsApp for AgentWorks, as in the WhatsApp card. action=status: whether it is paired and connected, and the linked phone. routing: which @slug sends a message to which Crew or Goal. unpair: disconnect your WhatsApp (ask the person first; they scan the QR again to pair). pair_help: where to pair, because the QR scan is done in the app on the person's phone and is never done in chat. Only change things when the person asks.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{"type": "string", "enum": []string{"status", "routing", "unpair", "pair_help"}},
			},
			"required": []string{"action"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			caller := GetUserFromContext(ctx)
			if caller == nil {
				caller = claims
			}
			if caller.UserID != claims.UserID {
				return "", errors.New("WhatsApp belongs to the person's own chat")
			}
			action := externalArg(args, "action")
			method, handler := http.MethodGet, http.HandlerFunc(nil)
			switch action {
			case "status":
				handler = whatsappStatusHandler(api.whatsappManager)
			case "routing":
				handler = whatsappGetRoutingHandler(api.whatsappManager)
			case "unpair":
				method, handler = http.MethodDelete, whatsappUnpairHandler(api.whatsappManager)
			case "pair_help":
				return `{"steps":"Open this Crew's Integrations, then WhatsApp, and scan the QR code with your phone (WhatsApp > Settings > Linked devices > Link a device). You can open that screen from chat with perform_ui_action.","note":"Pairing is done in the app, never in chat: a paired phone acts as you until it is unpaired."}`, nil
			default:
				return "", errors.New("Choose status, routing, unpair or pair_help.")
			}
			request := httptest.NewRequest(method, "/api/whatsapp/"+action, nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			body := strings.TrimSpace(recorder.Body.String())
			if recorder.Code >= 400 {
				return "", errors.New(laptopActionErrorText(body))
			}
			if body == "" {
				return `{"ok":true}`, nil
			}
			return body, nil
		}, 30*time.Second, "workspace_advanced")
}
