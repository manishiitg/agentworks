package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestChatAccessEffectiveNeverGivesMoreThanTheHostAndProgramAllow(t *testing.T) {
	for _, tc := range []struct {
		saved, provider string
		mac             bool
		want            string
	}{
		{chatAccessFull, "codex-cli", true, chatAccessFull},
		{chatAccessFull, "codex-cli", false, chatAccessProject},
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
}

func TestWithChatAccessGrantAddsHomeOnlyForWholeMacModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := []workflowtypes.WorkflowFolderGrant{{Alias: "docs", Path: filepath.Join(home, "docs"), Access: workflowtypes.FolderAccessReadOnly}}
	if got := withChatAccessGrant(project, chatAccessProject); len(got) != 1 {
		t.Fatalf("This project added grants: %+v", got)
	}
	for _, mode := range []string{chatAccessAutoReview, chatAccessFull} {
		got := withChatAccessGrant(project, mode)
		if len(got) != 2 || got[1].Path != home || !got[1].CanWrite() || got[1].Alias != chatAccessWholeMacAlias {
			t.Fatalf("%s grants = %+v", mode, got)
		}
		if len(project) != 1 {
			t.Fatal("the project's own grant list changed")
		}
	}
	attached := append(project, workflowtypes.WorkflowFolderGrant{Alias: "mac", Path: home + string(os.PathSeparator), Access: workflowtypes.FolderAccessReadWrite})
	if got := withChatAccessGrant(attached, chatAccessFull); len(got) != 2 {
		t.Fatalf("a project that already has the whole Mac got a second grant: %+v", got)
	}
	if !chatAccessHasWholeMac(attached) || chatAccessHasWholeMac(project) {
		t.Fatal("whole-Mac detection is wrong")
	}
}

func TestChatAccessToolCanLowerButNeverRaise(t *testing.T) {
	args, err := chatContextArgs(map[string]any{"access_mode": "full_access"})
	if err != nil || !args.fromTool || args.AccessMode == nil || *args.AccessMode != "full_access" || !args.changesSwitch() {
		t.Fatalf("args = %+v, %v", args, err)
	}
	tab := chatContextTab{}
	tab.Identity.AccessMode = chatAccessProject
	if _, err := setChatAccessMode(context.Background(), "owner", tab, "full_access", true); err == nil || !strings.Contains(err.Error(), "only the person") {
		t.Fatalf("a tool raised access: %v", err)
	}
	if !(chatAccessRank(chatAccessProject) < chatAccessRank(chatAccessAutoReview) && chatAccessRank(chatAccessAutoReview) < chatAccessRank(chatAccessFull)) {
		t.Fatal("modes are not ordered by access, so a tool could raise by picking a lower-ranked name")
	}
	if _, err := chatContextArgs(map[string]any{"access_mode": true}); err == nil {
		t.Fatal("a non-text access_mode was accepted")
	}
	if _, err := normalizeChatAccessMode("everything"); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

func TestChatAccessViewOffersAutoReviewOnlyWhereItWorks(t *testing.T) {
	view := chatAccessViewFor(chatAccessAutoReview, "codex-cli", true)
	if view.Mode != chatAccessAutoReview || view.Effective != "project" || view.Options[1].Available || view.Options[1].Why == "" || !view.Options[2].Available {
		t.Fatalf("codex view = %+v", view)
	}
	view = chatAccessViewFor("", "claude-code", false)
	if !view.OnServer || view.Options[1].Available || view.Options[2].Available || view.Mode != "project" {
		t.Fatalf("server view = %+v", view)
	}
}
