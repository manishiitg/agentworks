package server

import "testing"

func TestProductProjectManifestDisplayTitlePrefersIdentityName(t *testing.T) {
	m := productProjectManifest{Title: "gptlive1"}
	if got := m.displayTitle(); got != "gptlive1" {
		t.Fatalf("without identity: got %q", got)
	}
	m.Identity.Name = "SDE"
	if got := m.displayTitle(); got != "SDE" {
		t.Fatalf("with identity: got %q, want SDE", got)
	}
}
