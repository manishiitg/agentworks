package guidance

import (
	"strings"
	"testing"
)

// PLAT-436: no Builder guidance may still describe a run repairing or rewriting
// a scripted step; only the Builder's own execute_step does.
func TestBuilderGuidanceNoLongerPromisesRunTimeScriptRepair(t *testing.T) {
	for _, kind := range []string{"scripted", "optimize-playbook", "code-authoring", "workflow-tools"} {
		rendered, err := renderFromRegistry(kind, tmplData{}, allKinds)
		if err != nil {
			rendered, err = renderFromRegistry(kind, tmplData{}, referenceKinds)
		}
		if err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		text := strings.ToLower(rendered)
		for _, stale := range []string{
			"autofix sees it",
			"fix loop cannot rewrite",
			"execution agent will never replace it after a failure",
			"can trigger the fix loop to rewrite a script",
			"keep `lock_code=false` so the repair loop",
			"execution-agent rewrites on failure",
		} {
			if strings.Contains(text, stale) {
				t.Errorf("%s guidance still promises run-time script repair: %q", kind, stale)
			}
		}
	}
}
