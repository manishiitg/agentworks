package server

import "testing"

// #2980: default Main and imported/generated titles filled the agent list;
// saved existence, a teammate's view and a running provider are not owner views.
func TestHostedDirectoryRequiresNameOrOwnerView(t *testing.T) {
	tab := crewOwnerChat{OwnerID: "owner", Record: ProductConversationRecord{SessionID: "view-test", Title: "Claude Code (example)", ConversationKey: "project:chat:view-test", ProfileID: "work", IsMainChat: true}}
	if hostedDirectoryTab(tab) {
		t.Fatal("generated title listed as an explicit agent")
	}
	other := observeHostedChatView("teammate", hostedBindingForRecord(tab.Record))
	defer other()
	if hostedDirectoryTab(tab) {
		t.Fatal("teammate invented owner's presence")
	}
	closeFirst := observeHostedChatView("owner", hostedBindingForRecord(tab.Record))
	closeSecond := observeHostedChatView("owner", hostedBindingForRecord(tab.Record))
	if !hostedDirectoryTab(tab) {
		t.Fatal("open owner chat absent")
	}
	closeFirst()
	closeFirst()
	if !hostedDirectoryTab(tab) {
		t.Fatal("one closed stream removed another live view")
	}
	closeSecond()
	if hostedDirectoryTab(tab) {
		t.Fatal("closed unnamed Main remained listed")
	}
	tab.Identity.Name = "Planner"
	if !hostedDirectoryTab(tab) {
		t.Fatal("named idle agent absent")
	}
	tab.Identity.GeneratedName = true
	if hostedDirectoryTab(tab) {
		t.Fatal("generated import name counted as explicit")
	}
	tab.Identity.GeneratedName = false
	tab.Identity.Name = "Main"
	if hostedDirectoryTab(tab) {
		t.Fatal("default Main remained a separate agent")
	}
}
