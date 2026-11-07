package guidance

import (
	"strings"
	"testing"
)

func TestBrowserGuidanceTreatsDisabledCDPAsDeploymentPolicy(t *testing.T) {
	doc, err := RenderReferenceKindForTest("browser-usage", "workshop")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\"cdp_supported\": false",
		"deployment policy",
		"Automatic is not a",
		"probe CDP on a",
		"skills get core",
		"they do not require a shared tab",
		`read_skill(skills=[{"name":"builder-reference","path":"references/browser-usage.md"}])`,
		"Documentation reads do not",
		"In extension mode the prefix is empty",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("browser guidance missing %q", want)
		}
	}
}
