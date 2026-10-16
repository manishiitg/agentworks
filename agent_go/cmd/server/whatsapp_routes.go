package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// WhatsAppDefaultProfileResolver decides whether profileID may take a user's
// unrouted WhatsApp messages (it must declare the whatsapp runtime
// capability) and where its attachments go: the requested folder resolved
// inside the profile's own workspace, or "" for the per-user chat uploads.
// nil means this server offers no default profiles.
type WhatsAppDefaultProfileResolver func(ctx context.Context, userID, profileID, uploadFolder string) (resolvedUploadFolder string, err error)

// WhatsAppRoutes wires up per-user HTTP endpoints for pairing and status of
// the WhatsApp bot connector.
func WhatsAppRoutes(router *mux.Router, manager *services.WhatsAppServiceManager, defaults WhatsAppDefaultProfileResolver) {
	if manager == nil {
		return
	}
	waRouter := router.PathPrefix("/api/whatsapp").Subrouter()
	waRouter.HandleFunc("/pair", whatsappPairHandler(manager, defaults)).Methods("GET")
	waRouter.HandleFunc("/status", whatsappStatusHandler(manager)).Methods("GET")
	waRouter.HandleFunc("/session", whatsappUnpairHandler(manager)).Methods("DELETE", "OPTIONS")
	waRouter.HandleFunc("/routing", whatsappGetRoutingHandler(manager)).Methods("GET")
	waRouter.HandleFunc("/routing", whatsappPutRoutingHandler(manager)).Methods("PUT", "OPTIONS")
	waRouter.HandleFunc("/default-profile", whatsappPutDefaultProfileHandler(manager, defaults)).Methods("PUT", "OPTIONS")
	waRouter.HandleFunc("/device-label", whatsappPutDeviceLabelHandler(manager)).Methods("PUT", "OPTIONS")
	log.Printf("[WHATSAPP] Registered routes: GET /api/whatsapp/pair, GET /api/whatsapp/status, DELETE /api/whatsapp/session, GET|PUT /api/whatsapp/routing, PUT /api/whatsapp/default-profile, PUT /api/whatsapp/device-label")
}

// applyWhatsAppDefaultProfile makes profileID the pairing's default
// destination for the authenticated owner. Writes the error response itself
// and reports whether it succeeded.
func applyWhatsAppDefaultProfile(w http.ResponseWriter, r *http.Request, svc *services.WhatsAppService, user *UserClaims, defaults WhatsAppDefaultProfileResolver, profileID, uploadFolder string) bool {
	profileID = strings.TrimSpace(profileID)
	resolvedFolder := ""
	if profileID != "" {
		if defaults == nil {
			http.Error(w, "default profiles are not available on this server", http.StatusBadRequest)
			return false
		}
		folder, err := defaults(r.Context(), user.UserID, profileID, uploadFolder)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return false
		}
		resolvedFolder = folder
	}
	if err := svc.SetDefaultProfile(user.UserID, profileID, resolvedFolder); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	return true
}

// whatsappPutDefaultProfileHandler sets (or, with an empty profile_id,
// clears) the profile whose conversation takes this pairing's unrouted
// messages. Body: {"profile_id": "...", "upload_folder": "..."}.
func whatsappPutDefaultProfileHandler(manager *services.WhatsAppServiceManager, defaults WhatsAppDefaultProfileResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		svc, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		var body struct {
			ProfileID    string `json:"profile_id"`
			UploadFolder string `json:"upload_folder"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if !applyWhatsAppDefaultProfile(w, r, svc, user, defaults, body.ProfileID, body.UploadFolder) {
			return
		}
		profileID, folder := svc.DefaultProfile()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"default_profile_id":    profileID,
			"default_upload_folder": folder,
		})
	}
}

func whatsappPutDeviceLabelHandler(manager *services.WhatsAppServiceManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Slot  string `json:"slot"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Slot) != "" {
			http.Error(w, services.ErrWhatsAppOnePhone.Error(), http.StatusConflict)
			return
		}
		if err := manager.SetDeviceLabel(r.Context(), user.UserID, body.Label); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		devices, err := manager.Devices(r.Context(), user.UserID)
		if err != nil {
			http.Error(w, "whatsapp devices unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"devices": devices,
		})
	}
}

func whatsappServiceForRequest(r *http.Request, manager *services.WhatsAppServiceManager) (*services.WhatsAppService, *UserClaims, error) {
	user := GetUserFromContext(r.Context())
	if user == nil || user.UserID == "" {
		return nil, nil, context.Canceled
	}
	svc, err := manager.ServiceForUser(r.Context(), user.UserID, user.Email, user.Username)
	return svc, user, err
}

// whatsappPairHandler serves the current pairing QR code as a PNG image.
// Query params:
//
//	size — pixel dimension (default 384, clamped to [128, 1024])
//	profile_id — also make this profile the pairing's default destination
//	             (see WhatsAppDefaultProfileResolver); upload_folder says
//	             where its attachments go. A product's pairing screen passes
//	             its own profile so the scan and the routing are one step.
//
// Auth behavior: requesting the QR claims ownership of the pairing for the
// authenticated user. Every incoming WhatsApp message will then route to
// that user's per-user chat history / memory. If the pairing is already
// bound to a different user, we return 409 so the UI can prompt "unpair
// first". Unauthenticated calls are allowed (for dev / local), but in that
// case no owner binding is created and incoming messages get rejected by
// the bot manager until someone claims ownership via an authed request.
//
// Returns 404 when there's no active QR (paired already, or StartListening
// hasn't been called yet).
func whatsappPairHandler(manager *services.WhatsAppServiceManager, defaults WhatsAppDefaultProfileResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		primary, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		// One person, one WhatsApp: the account links a single phone. Asking
		// for another ("next" once paired, or a named extra slot) is refused.
		svc := primary
		switch device := strings.TrimSpace(r.URL.Query().Get("device")); device {
		case "", "primary":
		case "next":
			if primary.IsPaired() {
				http.Error(w, services.ErrWhatsAppOnePhone.Error(), http.StatusConflict)
				return
			}
		default:
			http.Error(w, services.ErrWhatsAppOnePhone.Error(), http.StatusConflict)
			return
		}
		if err := svc.ClaimOwnership(user.UserID, user.Email, user.Username); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if profileID := strings.TrimSpace(r.URL.Query().Get("profile_id")); profileID != "" {
			if !applyWhatsAppDefaultProfile(w, r, primary, user, defaults, profileID, r.URL.Query().Get("upload_folder")) {
				return
			}
		}
		if err := svc.EnsurePairingQR(context.Background()); err != nil {
			http.Error(w, "pairing QR unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		size := 384
		if s := r.URL.Query().Get("size"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				if n < 128 {
					n = 128
				} else if n > 1024 {
					n = 1024
				}
				size = n
			}
		}
		// A new attempt issues its first code about a second after it starts.
		// Wait for it, so the screen shows the code instead of an error at
		// every restart (the attempt ends every ~2.5 minutes).
		var png []byte
		deadline := time.Now().Add(8 * time.Second)
		for {
			png, err = svc.GetQRImagePNG(size)
			if err != nil {
				http.Error(w, "qr encode failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if png != nil || svc.IsPaired() || time.Now().After(deadline) || r.Context().Err() != nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if png == nil {
			http.Error(w, "no pairing QR available — already paired, or service not started", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(png)
	}
}

// whatsappUnpairHandler drops the current pairing: disconnects the client,
// deletes the session DB, and restarts the service so a fresh QR is
// available immediately from GET /api/whatsapp/pair. Idempotent — calling
// twice in a row is safe.
func whatsappUnpairHandler(manager *services.WhatsAppServiceManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		svc, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		// The account has one phone. ?jid=<number> must name it; any other
		// ?device is an extra phone, which no longer exists.
		if device := strings.TrimSpace(r.URL.Query().Get("device")); device != "" && device != "primary" {
			http.Error(w, "no linked phone with that name", http.StatusNotFound)
			return
		}
		if jid := strings.TrimSpace(r.URL.Query().Get("jid")); jid != "" {
			own := svc.OwnJID()
			if own.IsEmpty() || (own.String() != jid && own.User != strings.SplitN(jid, "@", 2)[0]) {
				http.Error(w, "no linked phone with that number", http.StatusNotFound)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := manager.UnpairDevice(ctx, user.UserID); err != nil {
			http.Error(w, "unpair failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

// whatsappGetRoutingHandler returns the current slug → workflow mapping
// used for in-message @<slug> routing. Response shape:
//
//	{"routing": {"<slug>": {"workflow_id":"…", "workspace_path":"…", "workshop_mode":"…"}, ...}}
func whatsappGetRoutingHandler(manager *services.WhatsAppServiceManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Ensure every workflow and Crew has a default @<name> route so the UI
		// and an unrouted WhatsApp chat can show usable slugs immediately after
		// pairing. Throttled
		// internally.
		unlockRouting := manager.LockAccountRouting(user.UserID)
		svc.EnsureDefaultWhatsAppRoutes(r.Context())
		after := svc.GetRouting()
		unlockRouting()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"routing": after,
		})
	}
}

// whatsappPutRoutingHandler replaces the entire routing map. Clients should
// PUT the full desired state; partial edits are handled client-side. Request
// body matches the GET response shape. Returns 400 on invalid slug names.
func whatsappPutRoutingHandler(manager *services.WhatsAppServiceManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Routing services.WhatsAppRouting `json:"routing"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		// Held so the throttled GET-time default-route sync can't interleave
		// with this write.
		unlockRouting := manager.LockAccountRouting(user.UserID)
		defer unlockRouting()
		if err := svc.SetRouting(body.Routing); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"routing": svc.GetRouting(),
		})
	}
}

// whatsappStatusHandler reports connector lifecycle state as JSON:
//
//	enabled   — StartListening has been called
//	paired    — a WhatsApp device identity is stored (scan was completed)
//	connected — whatsmeow is live on the WS right now
//	own_jid   — paired account's JID ("" when unpaired)
//	qr_expires_at — RFC3339 timestamp when the current QR expires (omitted
//	                when no QR is active)
func whatsappStatusHandler(manager *services.WhatsAppServiceManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, user, err := whatsappServiceForRequest(r, manager)
		if err != nil {
			http.Error(w, "whatsapp service unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if svc.IsEnabled() && !svc.IsPaired() {
			if err := svc.EnsurePairingQR(context.Background()); err != nil {
				log.Printf("[WHATSAPP] refresh pairing QR failed: %v", err)
			}
		}
		// After pairing completes, auto-provision default workflow slugs so
		// a WhatsApp message can immediately target @<automation-name> without
		// the operator having to open the routing screen first. Throttled
		// internally.
		svc.EnsureDefaultWhatsAppRoutes(r.Context())
		// The account's phone (one WhatsApp per account).
		var devices []services.WhatsAppDevice
		if listed, err := manager.Devices(r.Context(), user.UserID); err == nil {
			devices = listed
		}
		resp := map[string]interface{}{
			"enabled":   svc.IsEnabled(),
			"paired":    svc.IsPaired(),
			"connected": svc.IsConnected(),
			"own_jid":   svc.OwnJID().String(),
		}
		if active, started, lastErr, lastMsg, lastAt := svc.PairingInfo(); true {
			resp["pairing_active"] = active
			if !started.IsZero() {
				resp["pairing_started_at"] = started.UTC().Format(time.RFC3339)
			}
			if lastErr != "" {
				resp["pairing_error"] = lastErr
			}
			if lastMsg != "" {
				resp["pairing_message"] = lastMsg
			}
			if !lastAt.IsZero() {
				resp["pairing_last_at"] = lastAt.UTC().Format(time.RFC3339)
			}
		}
		access := svc.GetAccessState()
		resp["link_code"] = access.LinkCode
		if !access.LinkCodeExpires.IsZero() {
			resp["link_code_expires_at"] = access.LinkCodeExpires.UTC().Format(time.RFC3339)
		}
		resp["bound_chat_count"] = len(access.BoundChats)
		if code, expires := svc.GetQR(); code != "" && !expires.IsZero() {
			resp["qr_available"] = true
			resp["qr_expires_at"] = expires.UTC().Format(time.RFC3339)
		} else {
			resp["qr_available"] = false
		}
		// Owner binding — who owns this pairing and gets the incoming chats.
		// Nil means the pairing is unclaimed (rare edge case where someone
		// paired without hitting the authed pair endpoint). Surfaced so the
		// UI can show "Paired by <email>" and warn when the current user is
		// viewing someone else's pairing.
		if owner := svc.GetOwner(); owner != nil {
			resp["owner_user_id"] = owner.UserID
			resp["owner_email"] = owner.Email
			resp["owner_username"] = owner.Username
			resp["owner_paired_at"] = owner.PairedAt.UTC().Format(time.RFC3339)
			resp["default_profile_id"] = owner.DefaultProfileID
			resp["default_upload_folder"] = owner.DefaultUploadFolder
		}
		if devices != nil {
			resp["devices"] = devices
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
