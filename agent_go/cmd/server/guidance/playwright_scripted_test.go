package guidance

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPlaywrightScriptedGuidancePinsIsolationAndFailureEvidence(t *testing.T) {
	doc, err := renderFromRegistry("playwright-scripted", tmplData{}, referenceKinds)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"fresh browser process and fresh context for every case",
		"Do not add a `shared_browser` flag",
		"wall-clock watchdog",
		"SHA-256 hash `main.py`",
		"did run; never report that the workspace “refused to start” it",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("Playwright guidance missing %q", want)
		}
	}
}

// Exercise the attachment path, not just the source markdown: the former
// multi-agent omission left Example Product unable to read the named reference.
func TestPlaywrightReferenceIsDiscoverableAndCanonicalAcrossChatSurfaces(t *testing.T) {
	canonical, err := renderReferenceKind("playwright-scripted", tmplData{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"multi-agent", "workshop", "run"} {
		t.Run(mode, func(t *testing.T) {
			found := false
			err := AttachReferenceSurface(mode, func(skill *llmtypes.Skill) error {
				if skill.Name != "builder-reference" {
					return nil
				}
				found = true
				if !strings.Contains(skill.Description, "playwright-scripted") ||
					!strings.Contains(skill.Content, referenceKinds["playwright-scripted"].Description) {
					t.Fatal("skill discovery/index lost the canonical Playwright description")
				}
				if got := materializedFileContent(t, skill, "references/playwright-scripted.md"); got != canonical {
					t.Fatal("attached Playwright reference drifted from canonical guidance")
				}
				return nil
			})
			if err != nil || !found {
				t.Fatalf("Playwright reference attachment failed: %v (found=%v)", err, found)
			}
		})
	}
}

func TestPlaywrightExecutionReferenceFollowsAvailableCapabilities(t *testing.T) {
	canonical, err := renderReferenceKind("playwright-scripted", tmplData{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		signals StepExecutionSignals
		want    bool
	}{
		{"browser routing", StepExecutionSignals{ToolNames: []string{"agent_browser"}}, true},
		{"shell tests without managed browser", StepExecutionSignals{ToolNames: []string{"execute_shell_command"}}, true},
		{"saved Python harness", StepExecutionSignals{ScriptedStep: true}, true},
		{"database only", StepExecutionSignals{ToolNames: []string{"query_workflow_db"}}, false},
		{"no tools", StepExecutionSignals{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skill := MaterializeStepExecutionReferenceSkill(tc.signals)
			found := false
			if skill != nil {
				for _, file := range skill.SupportingFiles {
					if file.RelPath == "references/playwright-scripted.md" {
						found = true
						if string(file.Content) != canonical {
							t.Fatal("step has a stale copy of the reference")
						}
					}
				}
			}
			if found != tc.want {
				t.Fatalf("reference present=%v, want=%v", found, tc.want)
			}
		})
	}
}
