package server

import (
	"strings"
	"testing"
)

// A hosted chat's name is made from the title and the product's permanent id, so
// two products with one title never collide and a rename changes only the slug.
func TestHostedChatNamesAreStableAndDistinct(t *testing.T) {
	a := hostedChat{Kind: "crew", ProductID: "crew-aaa", Title: "SDE Team!", Chat: "builder"}
	b := hostedChat{Kind: "crew", ProductID: "crew-bbb", Title: "SDE Team!", Chat: "builder"}
	if a.Address() == b.Address() {
		t.Fatal("two products with one title share a name")
	}
	if got := a.Address(); !strings.HasPrefix(got, "crew-sde-team-") || !strings.HasSuffix(got, "-builder") {
		t.Fatal("address shape", got)
	}
	renamed := hostedChat{Kind: "crew", ProductID: "crew-aaa", Title: "Support", Chat: "builder"}
	if hostedID4(a.ProductID) != hostedID4(renamed.ProductID) {
		t.Fatal("a rename changed the id part")
	}
	if got := (hostedChat{Kind: "code", ProductID: "p1", Title: "Site redesign", Chat: "main", ChatName: "Main chat"}).Display(); got != "Code · Site redesign · Main chat" {
		t.Fatal("display", got)
	}
	if hostedSlug("  !!  ") != "untitled" {
		t.Fatal("an unusable title has no fallback")
	}
}
