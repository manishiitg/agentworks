package llmtypes

import "testing"

func TestDeclaredRunAsCoversTheFolderAndEverythingUnderIt(t *testing.T) {
	ResetDeclaredRunAsForTest()
	t.Cleanup(ResetDeclaredRunAsForTest)
	DeclareRunAs("/docs/Crew/c1", RunAs{Declared: true, User: "a", Slot: "slot01"})
	DeclareRunAs("/docs/Workflow/g", RunAs{Declared: true})
	DeclareRunAs("/docs/ignored", RunAs{User: "a"}) // not declared: never recorded

	for hint, want := range map[string]string{
		"/docs/Crew/c1":              "a",
		"/docs/Crew/c1/code/x.go":    "a",
		"/docs/Crew/c1/../c1/code":   "a",
		"/docs/Workflow/g/runs/it-0": "",
		"/docs/Crew/c10":             "-",
		"/docs":                      "-",
		"/docs/ignored":              "-",
		"":                           "-",
	} {
		got, ok := DeclaredRunAs(hint)
		switch {
		case want == "-" && ok:
			t.Errorf("%q must have no declaration, got %+v", hint, got)
		case want != "-" && (!ok || got.User != want):
			t.Errorf("%q = %+v ok=%v, want user %q", hint, got, ok, want)
		}
	}
	// The longest folder wins.
	DeclareRunAs("/docs/Crew/c1/sub", RunAs{Declared: true, User: "b"})
	if got, _ := DeclaredRunAs("/docs/Crew/c1/sub/x"); got.User != "b" {
		t.Errorf("nested declaration lost: %+v", got)
	}
}

func TestPolicyDeclaresItsRunAsForRootAndPrivateHome(t *testing.T) {
	ResetDeclaredRunAsForTest()
	t.Cleanup(ResetDeclaredRunAsForTest)
	policy := CLISecurityPolicy{PrivateHome: "/home/x/.cli", RunAs: RunAs{Declared: true, User: "a", Root: "/docs/Crew/c1"}}
	policy.DeclareRunAs()
	for _, hint := range []string{"/docs/Crew/c1/code", "/home/x/.cli/tmp"} {
		if got, ok := DeclaredRunAs(hint); !ok || got.User != "a" {
			t.Errorf("%q not covered: %+v %v", hint, got, ok)
		}
	}
	var none *CLISecurityPolicy
	none.DeclareRunAs() // nil-safe
	if clone := policy.Clone(); clone.RunAs != policy.RunAs {
		t.Errorf("Clone lost RunAs: %+v", clone.RunAs)
	}
}
