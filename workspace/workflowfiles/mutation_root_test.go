package workflowfiles

import "testing"

// The root a managed edit is grouped under keeps the owner's "_users/<owner>/" in front (PLAT-838 moved the parsing onto workspaceref).
func TestMutationRootGroupsByProjectAndKeepsTheOwnerPrefix(t *testing.T) {
	for file, want := range map[string]string{
		"/d/_users/u1/Workflow/w1/plan.json":         "/d/_users/u1/Workflow/w1",
		"/d/_users/u1/Chats/Work/projects/p/a/b.txt": "/d/_users/u1/Chats/Work/projects/p",
		"/d/Crew/c-1/x/y":                            "/d/Crew/c-1",
		"/d/Workflow/w2/steps/s.json":                "/d/Workflow/w2",
		"/d/_users/u1":                               "/d/_users",
		"/d/skills/foo/SKILL.md":                     "/d/skills",
	} {
		got, err := MutationRoot("/d", file)
		if err != nil || got != want {
			t.Errorf("MutationRoot(%q) = %q, %v; want %q", file, got, err, want)
		}
	}
}
