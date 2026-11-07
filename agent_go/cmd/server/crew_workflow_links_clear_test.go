package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Crew no longer links workflows (owner, 7 Nov): the one-off takes only "Workflow/..." out of a Crew's saved links
// and its Crew chats' links, keeps Crew links and every standalone workflow's own links, copies first, and runs once.
func TestClearCrewWorkflowLinksKeepsCrewLinksAndStandaloneWorkflowLinks(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	write := func(rel, content string) string {
		file := filepath.Join(docs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	crew := write("_system/crew-data/c1/workflow.json", `{"id":"c1","revision":7,"workflow_context_paths":["Workflow/old","Crew/other"]}`)
	nested := write("Crew/c2/product.json", `{"id":"c2","capabilities":{"workflow_context_paths":["Workflow/old"],"keep":true}}`)
	standalone := write("Workflow/wf/workflow.json", `{"id":"wf","workflow_context_paths":["Workflow/other"]}`)
	registry := write(productConversationRegistryPath("u1"), `{"entries":{"work/c1:chat:a":{"profile_id":"work","workflow_context_paths":["Workflow/old","Crew/other"]},"goal/g":{"profile_id":"goal","workflow_context_paths":["Workflow/keep"]}},"previous":{"work/c1:chat:a":[{"profile_id":"work","workflow_context_paths":["Workflow/old"]}]}}`)
	if err := os.MkdirAll(filepath.Join(docs, "_users", "u1"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := clearCrewWorkflowLinks(); err != nil {
		t.Fatal(err)
	}
	read := func(file string) string {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(raw)), "")
	}
	if got := read(crew); strings.Contains(got, "Workflow/") || !strings.Contains(got, `"Crew/other"`) || !strings.Contains(got, `"revision":7`) {
		t.Fatalf("Crew manifest = %s; want only the workflow link gone", got)
	}
	if got := read(nested); strings.Contains(got, "Workflow/") || !strings.Contains(got, `"keep":true`) {
		t.Fatalf("nested capabilities = %s", got)
	}
	if got := read(standalone); !strings.Contains(got, "Workflow/other") {
		t.Fatalf("a standalone workflow's own link was removed: %s", got)
	}
	if got := read(registry); strings.Count(got, "Workflow/") != 1 || !strings.Contains(got, "Workflow/keep") || !strings.Contains(got, `"Crew/other"`) {
		t.Fatalf("registry = %s; want only the Crew chats' workflow links gone", got)
	}
	if _, err := os.Stat(crew + crewWorkflowLinksClearBackup); err != nil {
		t.Fatalf("no copy of the original: %v", err)
	}
	if _, err := os.Stat(filepath.Join(docs, filepath.FromSlash(crewWorkflowLinksClearMarker))); err != nil {
		t.Fatalf("no done marker: %v", err)
	}
	// Done once: a link written afterwards is not touched by a later start.
	if err := os.WriteFile(crew, []byte(`{"id":"c1","workflow_context_paths":["Workflow/new"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearCrewWorkflowLinks(); err != nil {
		t.Fatal(err)
	}
	if got := read(crew); !strings.Contains(got, "Workflow/new") {
		t.Fatalf("the one-off ran twice: %s", got)
	}
}
