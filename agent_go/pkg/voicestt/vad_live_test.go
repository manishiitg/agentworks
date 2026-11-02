//go:build cgo && !novoice

package voicestt

import (
	"os"
	"testing"
)

// TestVADWiringLive is a live integration smoke test against a real model
// directory — set VOICESTT_TEST_MODEL_DIR to run it (same opt-in convention
// as TestEngineTranscribesRealAudioLive). It does not assert on VAD's own
// speech-detection accuracy (that is Silero's own validated model, not this
// package's code) — it verifies the wiring itself: the VAD model loads, a
// Stream gets a live (non-nil) detector, and feeding real silence through
// AcceptWaveform does not crash and is not misreported as speech.
func TestVADWiringLive(t *testing.T) {
	modelDir := os.Getenv("VOICESTT_TEST_MODEL_DIR")
	if modelDir == "" {
		t.Skip("set VOICESTT_TEST_MODEL_DIR to run this live test")
	}
	if !VADModelInstalled(modelDir) {
		t.Skipf("no %s in %s — copy it in to exercise VAD, or this Engine falls back to recognizer-only endpointing (still correct, just not what this test checks)", VADModelFileName, modelDir)
	}

	engine, err := NewEngine(modelDir)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	stream := engine.NewStream()
	defer stream.Close()
	if stream.vad == nil {
		t.Fatal("VADModelInstalled was true but Stream.vad is nil — engine did not pick up the model")
	}

	// One second of real digital silence, fed in small chunks like a live mic
	// would deliver it.
	chunk := SampleRate / 10
	silence := make([]float32, SampleRate)
	for i := 0; i < len(silence); i += chunk {
		end := i + chunk
		if end > len(silence) {
			end = len(silence)
		}
		res := stream.AcceptWaveform(silence[i:end])
		if res.EndOfUtterance {
			t.Fatalf("silence alone reported EndOfUtterance at sample %d — the recognizer's own endpoint rule needs trailing silence after actual speech, not silence from the start", i)
		}
	}
	if stream.vad.IsSpeech() {
		t.Fatal("VAD reported speech active during one full second of real silence")
	}
	t.Log("VAD wiring live check passed: model loaded, Stream got a live detector, silence behaved as silence")
}
