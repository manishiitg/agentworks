package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/voicestt"
)

// AgentWorks' own composer has no agent profile. It must be admitted exactly
// when this build carries the engine, and a product profile must never be
// admitted merely because no profile registry is wired.
func TestVoicePermittedWithoutProfileFollowsBuild(t *testing.T) {
	api := &StreamingAPI{}
	if got := api.voicePermitted("", "user-1"); got != voicestt.Available {
		t.Fatalf("voicePermitted(\"\") = %v, want voicestt.Available (%v)", got, voicestt.Available)
	}
	if api.voicePermitted("example-product", "user-1") {
		t.Fatal("a product profile must not be admitted without a profile registry to consult")
	}
}

// Unload is gated exactly like warm: a product that never declared voice
// cannot poke the engine, and without a registry no profile is admitted.
func TestVoiceUnloadRefusedForUndeclaredProfile(t *testing.T) {
	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodPost, "/api/voice/unload?profile_id=example-product", nil)
	rec := httptest.NewRecorder()
	api.handleVoiceUnload(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("expected a JSON error body, got %q (%v)", rec.Body.String(), err)
	}
}

// The composer decides whether to mount a mic from this one field, so it must
// always be present and must never claim availability a stub build cannot
// honor.
func TestCapabilitiesAdvertiseVoice(t *testing.T) {
	api := &StreamingAPI{}
	rec := httptest.NewRecorder()
	api.handleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))

	var body struct {
		Voice *struct {
			Available bool `json:"available"`
			Ready     bool `json:"ready"`
		} `json:"voice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.Voice == nil {
		t.Fatal("capabilities response has no voice block")
	}
	if body.Voice.Available != voicestt.Available {
		t.Fatalf("voice.available = %v, want %v", body.Voice.Available, voicestt.Available)
	}
	if body.Voice.Ready && !body.Voice.Available {
		t.Fatal("voice.ready must not be true when the engine is unavailable")
	}
}

// Behind a reverse proxy the browser is always same-origin with the public
// host, which no CORS allow-list can predict. Such an upgrade must pass; a
// genuinely foreign origin must still be refused.
func TestVoiceUpgraderAdmitsSameOrigin(t *testing.T) {
	api := &StreamingAPI{}
	same := httptest.NewRequest(http.MethodGet, "https://video.example.com/api/voice/stream", nil)
	same.Host = "video.example.com"
	same.Header.Set("Origin", "https://video.example.com")
	if !api.voiceUpgraderFor().CheckOrigin(same) {
		t.Fatal("same-origin upgrade was refused")
	}
	foreign := httptest.NewRequest(http.MethodGet, "https://video.example.com/api/voice/stream", nil)
	foreign.Host = "video.example.com"
	foreign.Header.Set("Origin", "https://evil.example.net")
	if api.voiceUpgraderFor().CheckOrigin(foreign) {
		t.Fatal("cross-site upgrade was admitted")
	}
}
