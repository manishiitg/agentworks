package mcpagent

import "testing"

func TestCanonicalAppToolCategories(t *testing.T) {
	filter := NewToolFilter(nil, nil, nil, []string{"workspace", "human_tools", "delegation_tools"}, nil)
	for _, category := range []string{"human_tools", "delegation_tools"} {
		if !filter.IsSystemCategory(category) {
			t.Fatalf("%q must be a system category", category)
		}
	}
	if filter.IsSystemCategory("human") {
		t.Fatal("legacy ambiguous category human must not remain a system category")
	}
	for input, want := range map[string]string{
		"workspace_tools":  "workspace",
		"human_tools":      "human_tools",
		"delegation_tools": "delegation_tools",
	} {
		if got := filter.GetToolCategory(input); got != want {
			t.Fatalf("GetToolCategory(%q) = %q, want %q", input, got, want)
		}
	}
}
