package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/voicestt"
)

// The shared streaming STT engine (pkg/voicestt), one Service per process.
// AgentWorks' composer and every product that declares
// agentprofiles.RuntimeCapabilities.Voice run this same engine and model;
// this file only decides who may reach it.
//
// Packaged macOS builds pass AGENTWORKS_VOICE_HELPER_BIN so a single
// novoice agent-server binary can delegate speech recognition to a small
// standalone voice-helper binary (avoiding shipping two full agent-server
// binaries for macOS compatibility). Linux and direct cgo builds leave the
// env var unset and use an in-process *voicestt.Manager.
//
// Loaded lazily (or by the startup warm in server.go) so a deployment that
// never uses voice never pays the download or the resident memory.
var (
	voiceServiceMu sync.Mutex
	voiceService   voicestt.Service
)

func currentVoiceService() voicestt.Service {
	voiceServiceMu.Lock()
	defer voiceServiceMu.Unlock()
	if voiceService != nil {
		return voiceService
	}
	if bin := strings.TrimSpace(os.Getenv("AGENTWORKS_VOICE_HELPER_BIN")); bin != "" {
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			voiceService = voicestt.NewHelperClient(bin, voicestt.DefaultModelDir())
			return voiceService
		}
		log.Printf("[VOICE] AGENTWORKS_VOICE_HELPER_BIN=%q is unavailable; falling back to in-process voice", bin)
	}
	voiceService = voicestt.NewManager(voicestt.DefaultModelDir())
	return voiceService
}

func closeVoiceService() {
	voiceServiceMu.Lock()
	svc := voiceService
	voiceService = nil
	voiceServiceMu.Unlock()
	if svc != nil {
		_ = svc.Close()
	}
}

// voicePermitted reports whether a caller may use the shared voice engine.
//
// With a profile id, the product decides: it must have opted into the
// capability in its product.yaml. Products never get a mic control (or this
// endpoint's functionality) merely by existing — same gate as Browser/Secrets
// in agent_profile_runtime.go.
//
// Without a profile id the caller is AgentWorks' own composer, which is the
// platform shell rather than a product and has no product.yaml to declare
// anything in. It gets dictation whenever this runtime carries the engine
// (in-process or via voice-helper), the same fact /api/capabilities advertises
// so the frontend only mounts the mic where it can work. This is not a
// privilege boundary: the endpoint is already behind AuthMiddleware and only
// ever transcribes the authenticated caller's own audio back to them.
func (api *StreamingAPI) voicePermitted(profileID, userID string) bool {
	if profileID == "" {
		return currentVoiceService().Available()
	}
	if api.agentProfiles == nil {
		return false
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return false
	}
	req := profile.Runtime.Capabilities.Voice
	return req == agentprofiles.CapabilityRequired || req == agentprofiles.CapabilityPreferred || req == agentprofiles.CapabilityOptional
}

// CheckOrigin must be set explicitly: gorilla/websocket's DEFAULT Upgrader
// rejects any request whose Origin differs from Host, which is exactly what a
// browser sends when the frontend (Vite dev server, port 52733) and this API
// server (port 19743) are on different ports — the ordinary case for this
// codebase's dev setup, and true of a separately-deployed frontend generally.
// Caught live: the browser's mic button silently hung for 60+ seconds (model
// load time) and then failed with "request origin not allowed by
// Upgrader.CheckOrigin", because this was left as the zero-value Upgrader.
// Reuses the same CORS allow-list check the terminal live-attach websocket
// upgrader uses (checkLiveAttachOrigin).
var voiceUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 4 * 1024,
}

func (api *StreamingAPI) voiceUpgraderFor() *websocket.Upgrader {
	up := voiceUpgrader
	up.CheckOrigin = func(r *http.Request) bool {
		ok := api.checkLiveAttachOrigin(r)
		if !ok {
			// The one refusal a browser cannot explain itself: it just sees a
			// failed socket. Name the origin and host so the mismatch is
			// obvious in the log (caught live behind the EC2 gateway).
			log.Printf("[VOICE] refused websocket origin %q for host %q (not in CORS allow-list and not same-origin)", r.Header.Get("Origin"), r.Host)
		}
		return ok
	}
	return &up
}

// handleVoiceStream is GET /api/voice/stream?profile_id=...&token=... — the
// shared dictation WebSocket (voicestt.ServeStream) behind this server's
// authorization. profile_id gates functionality per
// agentprofiles.RuntimeCapabilities.Voice; token is the same query-param JWT
// fallback AuthMiddleware and the terminal live-attach websocket already use,
// because browsers cannot set a custom Authorization header on a WebSocket
// upgrade request.
func (api *StreamingAPI) handleVoiceStream(w http.ResponseWriter, r *http.Request) {
	currentUserID := GetUserIDFromContext(r.Context())
	profileID := r.URL.Query().Get("profile_id")
	svc := currentVoiceService()
	st := svc.Status()
	log.Printf("[VOICE] stream requested: user=%s profile=%q origin=%q host=%s engine(available=%v installed=%v ready=%v downloading=%v)",
		currentUserID, profileID, r.Header.Get("Origin"), r.Host, st.Available, st.Installed, st.Ready, st.Downloading)
	if !api.voicePermitted(profileID, currentUserID) {
		log.Printf("[VOICE] stream refused: profile %q has not declared the voice capability (or the profile could not be resolved for user %s)", profileID, currentUserID)
		http.Error(w, `{"error":"voice capability not enabled for this profile"}`, http.StatusForbidden)
		return
	}
	if !svc.Available() {
		log.Printf("[VOICE] stream refused: this runtime has no native speech engine or helper")
	}
	svc.ServeStream(api.voiceUpgraderFor(), w, r)
}

// handleVoiceStatus is GET /api/voice/status — the engine's live state
// (voicestt.Status): installed, downloading with byte progress, loading,
// ready. The composer polls it while a first-time download runs.
func (api *StreamingAPI) handleVoiceStatus(w http.ResponseWriter, r *http.Request) {
	st := currentVoiceService().Status()
	// Only while something is in flight: a ready engine is polled at most
	// once per mic click, a download once per second, and the latter is the
	// one worth a trail.
	if st.Downloading || st.Loading {
		log.Printf("[VOICE] status: downloading=%v %d/%d bytes loading=%v", st.Downloading, st.GotBytes, st.TotalBytes, st.Loading)
	}
	writeVoiceJSON(w, http.StatusOK, st)
}

// handleVoiceWarm is POST /api/voice/warm?profile_id=... — the explicit
// "set up voice" step: starts the model download (first run) and load in the
// background and returns the status immediately. Gated like the stream, so a
// product that never declared voice cannot trigger a 690MB download either.
func (api *StreamingAPI) handleVoiceWarm(w http.ResponseWriter, r *http.Request) {
	profileID := r.URL.Query().Get("profile_id")
	userID := GetUserIDFromContext(r.Context())
	if !api.voicePermitted(profileID, userID) {
		log.Printf("[VOICE] warm refused: user=%s profile=%q has not declared the voice capability", userID, profileID)
		writeVoiceJSON(w, http.StatusForbidden, map[string]string{"error": "voice capability not enabled for this profile"})
		return
	}
	svc := currentVoiceService()
	before := svc.Status()
	svc.Warm()
	after := svc.Status()
	log.Printf("[VOICE] warm requested: user=%s profile=%q installed=%v ready=%v -> downloading=%v loading=%v", userID, profileID, before.Installed, before.Ready, after.Downloading, after.Loading)
	writeVoiceJSON(w, http.StatusAccepted, after)
}

// handleVoiceUnload is POST /api/voice/unload?profile_id=... — releases the
// loaded speech model (~1 GB resident) without touching the downloaded files,
// so a desktop shell can free it when its window is hidden and warm it again
// on show. Gated like warm; a no-op when nothing is loaded.
func (api *StreamingAPI) handleVoiceUnload(w http.ResponseWriter, r *http.Request) {
	profileID := r.URL.Query().Get("profile_id")
	userID := GetUserIDFromContext(r.Context())
	if !api.voicePermitted(profileID, userID) {
		log.Printf("[VOICE] unload refused: user=%s profile=%q has not declared the voice capability", userID, profileID)
		writeVoiceJSON(w, http.StatusForbidden, map[string]string{"error": "voice capability not enabled for this profile"})
		return
	}
	svc := currentVoiceService()
	released := svc.Unload()
	log.Printf("[VOICE] unload requested: user=%s profile=%q released=%v", userID, profileID, released)
	writeVoiceJSON(w, http.StatusOK, svc.Status())
}

func writeVoiceJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
