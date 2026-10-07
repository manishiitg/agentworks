package browser

import (
	"reflect"
	"testing"
)

// RTS 2026-10-07: a 30 fps .webm take fell behind the encoder on a 2-vCPU
// server and was lost, so a take without --fps records at 15 fps.
func TestRecordStartDefaultsTo15FPS(t *testing.T) {
	got := withDefaultRecordingFPS([]string{"start", "evidence/take.webm"})
	if want := []string{"start", "evidence/take.webm", "--fps", "15"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	explicit := []string{"start", "evidence/take.mp4", "--fps", "30"}
	if got := withDefaultRecordingFPS(explicit); !reflect.DeepEqual(got, explicit) {
		t.Fatalf("explicit fps changed: %v", got)
	}
}
