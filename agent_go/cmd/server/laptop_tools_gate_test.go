package server

import (
	"sort"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// Laptop commands run as the person, with no sandbox, so only the Crew's main owner may ever be given the laptop tools. A person
// using the Crew in Run mode, even when the owner has a folder shared and commands on, gets none of them.
func TestOnlyTheCrewOwnerGetsLaptopTools(t *testing.T) {
	api := tokenTestSetup(t)
	// The owner's saved choice: a Read & write folder with terminal commands on.
	saveTestLaptopSettings(t, "laptop", []localfiles.FolderSelection{{ID: "project", Path: "/tmp/project", Access: "write"}})
	target := &laptopConnectionTarget{DeviceID: "laptop", ResourceID: "project"}
	names := func(reg *recordingRegistrar) []string {
		out := []string{}
		for name := range reg.tools {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}
	files := func(claims *UserClaims, readOnly bool) []string {
		reg := &recordingRegistrar{}
		if err := api.registerLaptopTools(reg, newProductToolGate(nil), claims, readOnly, "session", "Crew/local-test", target); err != nil {
			t.Fatal(err)
		}
		return names(reg)
	}
	setup := func(claims *UserClaims, readOnly bool) []string {
		reg := &recordingRegistrar{}
		if err := api.registerLaptopAccessChatTool(reg, newProductToolGate(nil), claims, readOnly, "session", "Crew/local-test"); err != nil {
			t.Fatal(err)
		}
		return names(reg)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	runner := &UserClaims{UserID: "runner", Username: "runner"}

	if got := files(owner, false); len(got) < 4 {
		t.Fatalf("the owner's own chat should get the list, write, copy and command tools, got %v", got)
	}
	if got := setup(owner, false); len(got) != 1 {
		t.Fatalf("the owner's own chat should get the laptop setup tool, got %v", got)
	}
	for name, got := range map[string][]string{
		"a Run person's chat (read-only)":                        files(runner, true),
		"a Run person's chat marked as editable by mistake":      files(runner, false),
		"the owner in a read-only turn":                          files(owner, true),
		"laptop setup in a Run person's chat":                    setup(runner, true),
		"laptop setup for the owner in a read-only turn":         setup(owner, true),
		"a connector or schedule caller without a website login": files(&UserClaims{UserID: "owner", Provider: "bot_route"}, false),
	} {
		if len(got) != 0 {
			t.Fatalf("%s got laptop tools: %v", name, got)
		}
	}
}
