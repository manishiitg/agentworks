package server

import "testing"

// WELCOME.md: starters come from its front matter (at most six, a missing message sends the label) and the rest is the page.
func TestParseProjectWelcomeStartersAndBody(t *testing.T) {
	text := "---\nstarters:\n  - label: Make a promo\n    note: From a short brief\n    message: Make a 30-second promo for\n  - label: Say hello\n  - note: no label, skipped\n" +
		"  - label: c\n  - label: d\n  - label: e\n  - label: f\n  - label: g\n---\n# Welcome\n\n![How to](media/how.mp4)\n"
	got := parseProjectWelcome(text)
	if got.Body != "# Welcome\n\n![How to](media/how.mp4)" {
		t.Fatalf("body = %q", got.Body)
	}
	if len(got.Starters) != 6 || got.Starters[0].Message != "Make a 30-second promo for" || got.Starters[0].Note != "From a short brief" || got.Starters[1].Message != "Say hello" || got.Starters[5].Label != "f" {
		t.Fatalf("starters = %+v", got.Starters)
	}
	if plain := parseProjectWelcome("Just text\n"); plain.Body != "Just text" || len(plain.Starters) != 0 {
		t.Fatalf("no front matter = %+v", plain)
	}
}
