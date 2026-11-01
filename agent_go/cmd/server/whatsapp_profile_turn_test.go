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

// Only a profile that declared the whatsapp capability in its product.yaml
// can be a pairing's default destination.
func TestProfileTakesWhatsAppFollowsTheDeclaredCapability(t *testing.T) {
	for _, tc := range []struct {
		requirement agentprofiles.CapabilityRequirement
		want        bool
	}{
		{"", false},
		{agentprofiles.CapabilityDisabled, false},
		{agentprofiles.CapabilityPreferred, true},
		{agentprofiles.CapabilityRequired, true},
	} {
		if got := profileTakesWhatsApp(whatsappTestProfile(tc.requirement, "Chats/ExampleApp")); got != tc.want {
			t.Errorf("whatsapp=%q: takes WhatsApp = %v, want %v", tc.requirement, got, tc.want)
		}
	}
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

// the conversation exactly as the app's first turn would.
func TestWhatsAppEngineFollowsTheConversationBinding(t *testing.T) {
	profile := whatsappTestProfile(agentprofiles.CapabilityPreferred, "Chats/ExampleApp")
	profile.Runtime.ProviderOptions = []agentprofiles.ProviderOption{
		{ID: "claude-code", Provider: "claude-code", ModelID: "claude-fable-5-1", Default: true},
		{ID: "codex-cli", Provider: "codex-cli", ModelID: "gpt-6-astra"},
	}
	if option, ok := whatsappEngineFor(profile, ProductConversationRecord{}); !ok || option.ID != "claude-code" {
		t.Fatalf("unbound conversation engine = (%q, %v), want the default claude-code", option.ID, ok)
	}
	if option, ok := whatsappEngineFor(profile, ProductConversationRecord{Provider: "codex-cli"}); !ok || option.ID != "codex-cli" {
		t.Fatalf("codex-bound conversation engine = (%q, %v), want codex-cli", option.ID, ok)
	}
	profile.Runtime.ProviderOptions = nil
	if _, ok := whatsappEngineFor(profile, ProductConversationRecord{}); ok {
		t.Fatal("a profile with no provider options offered an engine")
	}
}
