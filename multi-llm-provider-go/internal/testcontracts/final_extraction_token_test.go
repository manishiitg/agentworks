package testcontracts

import "testing"

// A live run's random token must not change the review fingerprint shape, but
// the surrounding extracted text still must.
func TestLiveRunTokenPatternNormalisesOnlyRunTokens(t *testing.T) {
	a := liveRunTokenPattern.ReplaceAllString("Claude final LIVE_CLAUDE_FINAL_7a664ad1ac\nfirst LIVE_CLAUDE_FINAL_7a664ad1ac", "<RUN_TOKEN>")
	b := liveRunTokenPattern.ReplaceAllString("Claude final LIVE_CLAUDE_FINAL_db689818da\nfirst LIVE_CLAUDE_FINAL_db689818da", "<RUN_TOKEN>")
	if a != b {
		t.Fatalf("run tokens changed the shape: %q vs %q", a, b)
	}
	for _, fixed := range []string{"Here's the full summary:\n- done\n- verified", "Here is the final answer:\n- alpha\n- beta"} {
		if got := liveRunTokenPattern.ReplaceAllString(fixed, "<RUN_TOKEN>"); got != fixed {
			t.Fatalf("fixture text altered: %q", got)
		}
	}
}
