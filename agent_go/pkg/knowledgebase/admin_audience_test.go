package knowledgebase

import (
	"context"
	"testing"
)

// An administrator's own Crew saw only the one folder the admin had an explicit grant on, because the output-audience
// check ignored the admin's implicit Owner role (RTS 2026-10-06). An admin in the audience never narrows a project.
func TestAdminInAudienceDoesNotNarrowProjectBrain(t *testing.T) {
	s, admin, _ := fixture(t, false)
	folder(t, s, admin, "", "Company")
	create(t, s, admin, "Company", "about.md", "hello", "about")
	project := admin
	project.BindingPolicy = &BindingPolicy{Audience: []string{"admin"}, WriteAll: true}
	if _, err := s.Call(context.Background(), project, "read_knowledgebase", map[string]any{"path": "Company/about.md"}); err == nil {
		t.Fatal("without the admin flag the audience check should still use explicit grants only")
	}
	project.BindingPolicy.Admins = []string{"admin"}
	if _, err := s.Call(context.Background(), project, "read_knowledgebase", map[string]any{"path": "Company/about.md"}); err != nil {
		t.Fatalf("an admin's project must read a folder the admin can read: %v", err)
	}
}
