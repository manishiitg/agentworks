package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// The Builder and external MCP use the same account-scoped WhatsApp handlers
// as the website. The project access check precedes account access; the handler
// derives the phone owner from the current authenticated request.
func (api *StreamingAPI) externalWhatsAppCall(w http.ResponseWriter, r *http.Request, action string, target externalMessagingTarget) {
	if _, err := personalConnectionItemScope(r, target.path, true, "WhatsApp", "WhatsApp"); err != nil {
		externalError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	if api.whatsappManager == nil {
		externalError(w, http.StatusServiceUnavailable, "whatsapp_unavailable", "WhatsApp is not enabled on this server.")
		return
	}
	method, route, handler := http.MethodGet, "status", whatsappStatusHandler(api.whatsappManager)
	switch action {
	case "whatsapp_status":
	case "whatsapp_routing":
		route, handler = "routing", whatsappGetRoutingHandler(api.whatsappManager)
	case "whatsapp_unpair":
		method, route, handler = http.MethodDelete, "session", whatsappUnpairHandler(api.whatsappManager)
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Choose whatsapp_status, whatsapp_routing or whatsapp_unpair.")
		return
	}
	request := httptest.NewRequest(method, "/api/whatsapp/"+route, nil).WithContext(r.Context())
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	body := strings.TrimSpace(recorder.Body.String())
	if recorder.Code >= 400 {
		externalError(w, recorder.Code, "whatsapp_failed", laptopActionErrorText(body))
		return
	}
	externalScheduleRespond(w, recorder.Code, []byte(body))
}
