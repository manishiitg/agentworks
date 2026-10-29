package voicestt

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// StreamMessage is one server->client update on the dictation WebSocket.
// Kept minimal and JSON rather than reusing any product's event envelope:
// this is a narrow-purpose transport (audio in, text out).
type StreamMessage struct {
	Type           string `json:"type"` // "partial" | "final" | "error"
	Text           string `json:"text"`
	EndOfUtterance bool   `json:"end_of_utterance,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ServeStream runs one dictation session over an already-authorized HTTP
// request: it upgrades to a WebSocket, accepts raw PCM16 mono 16kHz audio as
// binary frames, and streams back partial/final transcripts as JSON text
// frames. A text frame {"action":"finish"} flushes the final transcript
// without closing — the push-to-talk release. Closing the socket flushes too.
//
// The agent server serves exactly this for AgentWorks and for every product
// on it; the caller only decides who may reach it and how origins are checked.
func ServeStream(m *Manager, upgrader *websocket.Upgrader, w http.ResponseWriter, r *http.Request) {
	session, err := m.NewSession(r.Context())
	if err != nil {
		log.Printf("[VOICE] session refused: engine unavailable: %v", err)
		http.Error(w, `{"error":"voice engine unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	log.Printf("[VOICE] session opened (active=%d)", m.Status().ActiveStreams)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		session.Close()
		log.Printf("[VOICE] upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	defer session.Close()

	// Chunk-arrival telemetry: without it, "no text appeared" is
	// indistinguishable between "no audio ever arrived" (mic/permission/
	// silence problem, entirely a frontend concern) and "audio arrived but the
	// model heard nothing to transcribe" (a real STT problem). Logging RMS, not
	// just a byte count, is what separates near-silence (a muted/wrong input
	// device) from real speech reaching the engine.
	var audioChunks, totalSamples int
	opened := time.Now()
	send := func(msg StreamMessage) {
		if writeErr := conn.WriteJSON(msg); writeErr != nil {
			log.Printf("[VOICE] write failed: %v", writeErr)
		}
	}
	for {
		msgType, data, readErr := conn.ReadMessage()
		if readErr != nil {
			if websocket.IsUnexpectedCloseError(readErr, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[VOICE] read failed: %v", readErr)
			}
			result := session.Finish()
			if result.Text != "" {
				send(StreamMessage{Type: "final", Text: result.Text, EndOfUtterance: true})
			}
			// Audio far shorter than the session means the browser dropped samples.
			log.Printf("[VOICE] session ended: chunks=%d audio_seconds=%.1f wall_seconds=%.1f final_text=%q",
				audioChunks, float64(totalSamples)/float64(SampleRate), time.Since(opened).Seconds(), result.Text)
			return
		}
		switch msgType {
		case websocket.BinaryMessage:
			samples := DecodePCM16(data)
			audioChunks++
			totalSamples += len(samples)
			if audioChunks == 1 {
				log.Printf("[VOICE] first audio chunk received: %d samples, rms=%.4f", len(samples), RMS(samples))
			} else if audioChunks%50 == 0 {
				log.Printf("[VOICE] %d chunks received, %.1fs of audio, last-chunk rms=%.4f",
					audioChunks, float64(totalSamples)/float64(SampleRate), RMS(samples))
			}
			result := session.Accept(samples)
			if result.Text == "" {
				continue
			}
			if result.EndOfUtterance {
				send(StreamMessage{Type: "final", Text: result.Text, EndOfUtterance: true})
			} else {
				send(StreamMessage{Type: "partial", Text: result.Text})
			}
		case websocket.TextMessage:
			var ctrl struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Action == "finish" {
				result := session.Finish()
				// Always answer a finish, even with empty text, so a client
				// waiting for the committed transcript before tearing down
				// never has to guess whether one is still coming.
				send(StreamMessage{Type: "final", Text: result.Text, EndOfUtterance: true})
			}
		}
	}
}

// DecodePCM16 converts little-endian 16-bit PCM bytes (what an AudioWorklet
// capture sends) to the float32 samples the engine expects. An odd trailing
// byte is ignored.
func DecodePCM16(data []byte) []float32 {
	n := len(data) / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2])) //nolint:gosec // G115: intentional PCM16 bit reinterpretation.
		samples[i] = float32(v) / 32768.0
	}
	return samples
}

// RMS is a cheap, decisive signal-vs-silence check: real speech at a
// reasonable mic level sits well above ~0.01; a muted input, a wrong/virtual
// device, or a permission grant that returns a synthetic empty stream all read
// near zero.
func RMS(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}
