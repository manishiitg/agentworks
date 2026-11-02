//go:build cgo && !novoice

package voicestt

import "testing"

// The recognizer's own endpoint rule is trailing-silence-duration only — a
// loud non-speech sound can reset that timer without anyone having spoken,
// or it can fire while VAD's own speech-probability model says the person is
// still actively talking. endOfUtteranceConfirmed is the AND-gate that keeps
// the recognizer's endpoint from being trusted alone once VAD disagrees.
func TestEndOfUtteranceConfirmed(t *testing.T) {
	cases := []struct {
		name               string
		recognizerEndpoint bool
		hasVAD             bool
		vadDetectsSpeech   bool
		want               bool
	}{
		{"recognizer says not done", false, true, false, false},
		{"recognizer says not done even if VAD also silent", false, true, true, false},
		{"no VAD installed: recognizer alone decides, done", true, false, false, true},
		{"no VAD installed: recognizer alone decides, not done", false, false, true, false},
		{"VAD confirms silence: genuinely done", true, true, false, true},
		{"VAD says speech still active: suppress the premature endpoint", true, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := endOfUtteranceConfirmed(tc.recognizerEndpoint, tc.hasVAD, tc.vadDetectsSpeech)
			if got != tc.want {
				t.Fatalf("endOfUtteranceConfirmed(recognizerEndpoint=%v, hasVAD=%v, vadDetectsSpeech=%v) = %v, want %v",
					tc.recognizerEndpoint, tc.hasVAD, tc.vadDetectsSpeech, got, tc.want)
			}
		})
	}
}
