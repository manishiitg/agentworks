package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func whatsappTestProfile(capability agentprofiles.CapabilityRequirement, root string) agentprofiles.Profile {
	profile := routeTestProfile("example-app", true, "")
	profile.Name = "ExampleApp"
	profile.Runtime.Capabilities.WhatsApp = capability
	profile.Runtime.Workspace.Root = root
	return profile
}

// A product's attachment folder is kept inside its own fixed workspace root,
// where its folder guard lets the profile read what arrives.
func TestWhatsAppUploadFolderStaysInsideTheProfileWorkspace(t *testing.T) {
	profile := whatsappTestProfile(agentprofiles.CapabilityPreferred, "Chats/ExampleApp")
	for _, tc := range []struct {
		requested string
		want      string
		wantErr   bool
	}{
		{"", "", false},
		{"inbox", "Chats/ExampleApp/inbox", false},
		{"/inbox/", "Chats/ExampleApp/inbox", false},
		{"Chats/ExampleApp/inbox", "Chats/ExampleApp/inbox", false},
		{"Chats/ExampleApp", "Chats/ExampleApp", false},
		{"../Other", "", true},
		{"inbox/../../Other", "", true},
		{"..", "", true},
	} {
		got, err := whatsappUploadFolderFor(profile, tc.requested)
		if tc.wantErr {
			if err == nil {
				t.Errorf("requested %q: got %q, want an error", tc.requested, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("requested %q: got (%q, %v), want %q", tc.requested, got, err, tc.want)
		}
	}
	noRoot := whatsappTestProfile(agentprofiles.CapabilityPreferred, "")
	if _, err := whatsappUploadFolderFor(noRoot, "inbox"); err == nil {
		t.Fatal("a profile with no fixed root accepted an upload folder, want an error")
	}
	// A keyed profile (one conversation per project) keeps attachments under
	// its projects root — the activity folder a routed @child turn names.
	keyed := whatsappTestProfile("", "")
	keyed.Runtime.Workspace.ProjectsRoot = "Chats/ExampleApp/activities"
	if got, err := whatsappUploadFolderFor(keyed, "Chats/ExampleApp/activities/fractions/attempts"); err != nil || got != "Chats/ExampleApp/activities/fractions/attempts" {
		t.Fatalf("keyed profile folder = (%q, %v), want the activity's attempts folder", got, err)
	}
	if _, err := whatsappUploadFolderFor(keyed, "Chats/ExampleApp/inbox"); err != nil {
		t.Fatalf("keyed profile folder outside its projects root = %v", err)
	} else if got, _ := whatsappUploadFolderFor(keyed, "Chats/ExampleApp/inbox"); got != "Chats/ExampleApp/activities/Chats/ExampleApp/inbox" {
		t.Fatalf("a folder outside the projects root resolved to %q, want it forced under the root", got)
	}
}
