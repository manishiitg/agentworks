package productguide

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// The merged display must work for either entitled origin without advertising
// another product or reverting to the old Code-versus-Crew choice.
func TestCombinedCrewGuideProjection(t *testing.T) {
	for _, origins := range []string{"work", "code", "work,code", ""} {
		t.Run("origins="+origins, func(t *testing.T) {
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
			t.Setenv("AGENT_PRODUCTS", "")
			t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work,code,knowledgebase")
			selection := productpolicy.Selection{Allowed: func(product string) bool {
				return product != "" && productpolicy.Contains(origins, product)
			}}
			guide := Guide(selection)
			wantCrew := origins != ""
			if got := strings.Count(guide, "| Crew |"); got != map[bool]int{true: 1, false: 0}[wantCrew] {
				t.Fatalf("expected one Crew entry for an entitled origin, got %d", got)
			}
			for _, body := range []string{guide, Skill(selection), Overview(selection)} {
				if strings.Contains(body, "<!--") || strings.Contains(body, "| Code |") || strings.Contains(body, "### Code") || strings.Contains(body, "Code has one owner") {
					t.Fatal("guide still advertises a separate Code product or has unresolved conditions")
				}
			}
			if strings.Contains(Skill(selection), "- Crew:") != wantCrew || strings.Contains(guide, "### Where did Code go?") != wantCrew {
				t.Fatal("Crew routing/FAQ does not follow the entitled origins")
			}
		})
	}
}
