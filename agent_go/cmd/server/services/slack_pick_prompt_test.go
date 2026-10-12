package services

import (
	"strings"
	"testing"
)

// The "who should answer" question names each target the way the product does and says what it is for.
func TestSlackPickPromptShowsNamesAndDescriptions(t *testing.T) {
	prompt := slackPickPrompt([]SlackTarget{
		{Slug: "release-checks", Label: "Release checks", Description: "Checks each release before it goes out."},
		{Slug: "support", Label: "Support"},
	}, "")
	for _, want := range []string{"*Release checks* (`release-checks`): Checks each release", "*Support* (`support`)"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "(`support`):") {
		t.Fatalf("a target without a description must not get a colon:\n%s", prompt)
	}
}
