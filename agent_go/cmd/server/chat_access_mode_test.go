package server

import (
	"context"
	"strings"
	"testing"
)

// Owner, 26 Nov: access is the project's, mode is the chat's. No mode adds reach; a chat still saved as full_access
// runs as Normal, and Auto-review runs only on a personal Mac with a program that has a reviewer.
func TestChatModeEffectiveNeverAddsReach(t *testing.T) {
	for _, tc := range []struct {
		saved, provider string
		mac             bool
		want            string
	}{
		{chatAccessLegacyFull, "codex-cli", true, chatAccessProject},
		{chatAccessLegacyFull, "claude-code", true, chatAccessProject},
		{chatAccessAutoReview, "claude-code", true, chatAccessAutoReview},
		{chatAccessAutoReview, "cursor-cli", true, chatAccessAutoReview},
		{chatAccessAutoReview, "codex-cli", true, chatAccessProject},
		{chatAccessAutoReview, "claude-code", false, chatAccessProject},
		{chatAccessProject, "claude-code", true, chatAccessProject},
	} {
		if got := chatAccessEffective(tc.saved, tc.provider, tc.mac); got != tc.want {
			t.Errorf("effective(%q, %s, mac=%t) = %q, want %q", tc.saved, tc.provider, tc.mac, got, tc.want)
		}
	}
	for _, in := range []string{"normal", "project", "full_access"} {
		if got, err := normalizeChatAccessMode(in); err != nil || got != chatAccessProject {
			t.Errorf("normalize(%q) = %q, %v; want Normal", in, got, err)
		}
	}
}

func TestChatModeToolCanTurnAutoReviewOnButNeverOff(t *testing.T) {
	args, err := chatContextArgs(map[string]any{"access_mode": "normal"})
	if err != nil || !args.fromTool || args.AccessMode == nil || *args.AccessMode != "normal" || !args.changesSwitch() {
		t.Fatalf("args = %+v, %v", args, err)
	}
	tab := chatContextTab{}
	tab.Identity.AccessMode = chatAccessAutoReview
	for _, mode := range []string{"normal", "full_access"} {
		if _, err := setChatAccessMode(context.Background(), "owner", tab, mode, true); err == nil || !strings.Contains(err.Error(), "only the person") {
			t.Fatalf("a tool turned Auto-review off with %q: %v", mode, err)
		}
	}
	if !(chatAccessRank(chatAccessAutoReview) < chatAccessRank(chatAccessProject)) {
		t.Fatal("Auto-review must rank below Normal, so a tool cannot drop the reviewer")
	}
	if _, err := chatContextArgs(map[string]any{"access_mode": true}); err == nil {
		t.Fatal("a non-text access_mode was accepted")
	}
	if _, err := normalizeChatAccessMode("everything"); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

func TestChatModeViewOffersNormalAndAutoReviewOnly(t *testing.T) {
	view := chatAccessViewFor(chatAccessAutoReview, "codex-cli", true)
	if view.Mode != chatAccessAutoReview || view.Effective != "project" || len(view.Options) != 2 || view.Options[1].Available || view.Options[1].Why == "" {
		t.Fatalf("codex view = %+v", view)
	}
	view = chatAccessViewFor(chatAccessLegacyFull, "claude-code", true)
	if view.Mode != "project" || view.Effective != "project" || !view.Options[1].Available {
		t.Fatalf("legacy full access view = %+v", view)
	}
	view = chatAccessViewFor("", "claude-code", false)
	if !view.OnServer || view.Options[1].Available || view.Mode != "project" {
		t.Fatalf("server view = %+v", view)
	}
}
