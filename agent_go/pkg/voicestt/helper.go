package voicestt

import (
	"context"
	"encoding/binary"
	"math"
	"net/http"

	"github.com/gorilla/websocket"
)

const helperTokenHeader = "X-Voice-Token"

// Service is the runtime voice-recognition interface used by agent-server.
// It is satisfied both by an in-process *Manager (Linux / direct cgo builds)
// and by *HelperClient (packaged macOS builds where sherpa-onnx runs inside a
// small standalone voice-helper binary).
type Service interface {
	Available() bool
	Status() Status
	Warm()
	Unload() bool
	Transcribe(ctx context.Context, samples []float32) (string, error)
	ServeStream(upgrader *websocket.Upgrader, w http.ResponseWriter, r *http.Request)
	Close() error
}

// Available reports whether this in-process Manager was compiled with the
// native speech engine.
func (*Manager) Available() bool { return Available }

// ServeStream satisfies Service by delegating to the package-level WebSocket handler.
func (m *Manager) ServeStream(upgrader *websocket.Upgrader, w http.ResponseWriter, r *http.Request) {
	ServeStream(m, upgrader, w, r)
}

// Close releases any loaded in-process engine resources.
func (m *Manager) Close() error {
	m.Unload()
	return nil
}

type helperReadyMessage struct {
	Port int `json:"port"`
}

type helperUnloadResponse struct {
	Released bool   `json:"released"`
	Status   Status `json:"status"`
}

type helperTranscribeResponse struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func encodeFloat32LE(samples []float32) []byte {
	buf := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(buf[i*4:i*4+4], math.Float32bits(s))
	}
	return buf
}

func decodeFloat32LE(raw []byte) []float32 {
	n := len(raw) / 4
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4]))
	}
	return samples
}
