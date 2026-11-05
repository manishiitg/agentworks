package server

import "testing"

// Showcase, 5 Nov: a teammate working on the website showed green from their server copy's sync. The dot is "active
// now": the website's live connection or their Mac, nothing else.
func TestTeamActiveIsWebsiteOpenOrMacOnly(t *testing.T) {
	if on, label := teamActive("member-never-seen", false); on || label != "Not active" {
		t.Fatalf("a person never seen is active: %v %q", on, label)
	}
	noteWebOpen("member-web", 1)
	if on, label := teamActive("member-web", false); !on || label != "Active now on the web" {
		t.Fatalf("website open is not active: %v %q", on, label)
	}
	noteWebOpen("member-web", -1) // closed just now: still active for a few minutes
	if on, _ := teamActive("member-web", false); !on {
		t.Fatal("a website closed a moment ago is not active")
	}
	if on, label := teamActive("member-mac", true); !on || label != "Mac on" {
		t.Fatalf("a connected Mac is not active: %v %q", on, label)
	}
}
