package terminals

import "testing"

// TestProviderLabelRecognizesMuse pins a real gap found live 2026-09-11:
// providerLabel's metadata switch had no muse-cli case, so a muse terminal's
// provider label rendered as the raw "muse-cli" string instead of a
// prettified name like the other four coding CLIs, and its pane-header
// content sniff never matched muse's real boot banner ("Muse Code 1.1.1").
func TestProviderLabelRecognizesMuse(t *testing.T) {
	if got := providerLabel("", map[string]interface{}{"provider": "muse-cli"}); got != "Muse" {
		t.Fatalf("providerLabel(metadata provider=muse-cli) = %q, want %q", got, "Muse")
	}
	if got := providerLabel("  Muse Code 1.1.1\n\n  Model set to muse-spark-1.3-contributor\n", nil); got != "Muse" {
		t.Fatalf("providerLabel(pane header) = %q, want %q", got, "Muse")
	}
}

func TestProviderLabelRecognizesAgy(t *testing.T) {
	if got := providerLabel("", map[string]interface{}{"provider": "agy-cli"}); got != "Antigravity CLI" {
		t.Fatalf("providerLabel(metadata provider=agy-cli) = %q", got)
	}
	if got := providerLabel("Antigravity CLI\n> ", nil); got != "Antigravity CLI" {
		t.Fatalf("providerLabel(pane header) = %q", got)
	}
}
