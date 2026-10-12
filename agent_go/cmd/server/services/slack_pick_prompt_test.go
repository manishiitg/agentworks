package services

import (
	"strings"
	"testing"
)

// The "who should answer" question has one row per target: its name, what it is for, the slug people can type, and one
// Pick button that carries the target.
func TestSlackPickRowsShowNamesAndDescriptions(t *testing.T) {
	ref := SlackTargetRef{WorkspacePath: "Workflow/checks"}
	blocks := slackPickBlocks([]SlackTarget{
		{Ref: ref, Slug: "release-checks", Label: "Release checks", Description: "Checks each release before it goes out."},
		{Ref: SlackTargetRef{WorkspacePath: "Workflow/help"}, Slug: "support", Label: "Support"},
	})
	if len(blocks) != 2 || blocks[0].Type != "section" || len(blocks[0].Buttons) != 1 {
		t.Fatalf("want one section row with one button per target, got %+v", blocks)
	}
	if want := "*Release checks*\nChecks each release before it goes out.\n`release-checks`"; blocks[0].Text != want {
		t.Fatalf("row text = %q, want %q", blocks[0].Text, want)
	}
	if blocks[1].Text != "*Support*\n`support`" {
		t.Fatalf("a target without a description has no empty line: %q", blocks[1].Text)
	}
	if blocks[0].Buttons[0].Value != ref.Key() || !strings.HasPrefix(blocks[0].Buttons[0].ActionID, slackPickActionPrefix) {
		t.Fatalf("the button must carry the target and the pick action: %+v", blocks[0].Buttons[0])
	}
}
