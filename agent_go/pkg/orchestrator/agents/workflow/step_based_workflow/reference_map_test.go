package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-561: a removed producer step stays named in a consumer's Inputs, an
// eval and a guide. The map reports every one, and an edit to the producer
// lists its dependents; neither blocks anything.
func TestReferenceMapReportsChangesNotCarriedThrough(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	root := filepath.Join(docs, "Workflow", "wf")
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("planning/plan.json", `{"steps":[
	 {"type":"regular","id":"find-jobs","description":"Find jobs.","context_output":"jobs.json",
	  "validation_schema":{"files":[{"file_name":"jobs.json"},{"file_name":"details.json"}]}},
	 {"type":"regular","id":"save-jobs","description":"## Inputs\n- scores from `+"`score-jobs`"+`, `+"`details.json`"+`, `+"`ranking.json`"+`\n- Guide: `+"`learnings/_global/references/save.md`"+`",
	  "context_dependencies":["jobs.json","details.json"],"context_output":"saved.json"}]}`)
	write("planning/changelog/changelog-1.json", `{"entries":[{"tool":"delete_plan_steps","step_ids":["score-jobs"]}]}`)
	write("evaluation/evaluation_plan.json", `{"steps":[{"id":"eval-save","description":"Read $VAR_TARGET_RUN_PATH/score-jobs/scores.json and $VAR_TARGET_RUN_PATH/save-jobs/saved.json."}]}`)
	write("knowledgebase/notes/flow.md", "save-jobs reads jobs.json from find-jobs; see code/save-jobs/main.py.")

	report, err := CollectReferenceMap("Workflow/wf")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, issue := range report.Issues {
		found[issue.Severity+" "+issue.Kind+" "+issue.Source+" "+issue.Ref] = true
	}
	for _, want := range []string{
		"break retired_step step:save-jobs score-jobs",            // Inputs naming a removed producer
		"break dependency_not_staged step:save-jobs details.json", // written, but not a context_output
		"break missing_input step:save-jobs ranking.json",         // nothing produces it
		"break missing_file step:save-jobs learnings/_global/references/save.md",
		"break retired_step eval:eval-save score-jobs", // eval asserting a removed step's file
		"break missing_file file:knowledgebase/notes/flow.md code/save-jobs/main.py",
	} {
		if !found[want] {
			t.Errorf("missing %q in %+v", want, report.Issues)
		}
	}
	if found["info output_unconsumed step:save-jobs saved.json"] {
		t.Errorf("saved.json is read by eval-save and must not be unconsumed")
	}

	notes := ReferenceNotesForSteps("Workflow/wf", []string{"find-jobs"}, nil)
	for _, want := range []string{"save-jobs (jobs.json)", "save-jobs (details.json)", "notes/guides naming it or its outputs: knowledgebase/notes/flow.md"} {
		if !strings.Contains(notes, want) {
			t.Errorf("edit note missing %q:\n%s", want, notes)
		}
	}

	// The plan-edit wrapper appends the note only when the plan changed.
	edit := withReferenceMapNotes("Workflow/wf", func(context.Context, map[string]interface{}) (string, error) { return "unchanged", nil })
	if out, _ := edit(context.Background(), map[string]interface{}{"existing_step_id": "find-jobs"}); out != "unchanged" {
		t.Errorf("no-op edit got a note: %q", out)
	}
}

// A dependency may name an earlier step's id, which resolves to that step's
// context_output files. Only a step that outputs nothing is a break.
func TestReferenceMapAcceptsStepIDDependencies(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "planning"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `{"steps":[
	 {"type":"regular","id":"step-a","description":"A.","context_output":"a.json"},
	 {"type":"regular","id":"step-quiet","description":"Q."},
	 {"type":"regular","id":"step-c","description":"C.","context_dependencies":["step-a","step-quiet"],"context_output":"c.json"}]}`
	if err := os.WriteFile(filepath.Join(root, "planning", "plan.json"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReferenceMap(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, issue := range report.Issues {
		got[issue.Kind+" "+issue.Ref] = true
	}
	if got["dependency_unproduced step-a"] || !got["dependency_step_without_output step-quiet"] {
		t.Fatalf("step id dependencies: %v", got)
	}
}
