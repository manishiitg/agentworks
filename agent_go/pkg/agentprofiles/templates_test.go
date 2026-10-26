package agentprofiles

import (
	"testing"
	"testing/fstest"
)

func TestRenderPromptTemplateExpandsKnownVariablesOnly(t *testing.T) {
	got := RenderPromptTemplate("Hello {{NAME}}, it is {{TIME}}. {{UNKNOWN}} stays.", map[string]string{
		"NAME": "Example Product",
		"TIME": "10:00",
	})
	want := "Hello Example Product, it is 10:00. {{UNKNOWN}} stays."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveCommandPromptsFillsInFromFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"commands/a.md": &fstest.MapFile{Data: []byte("  Do the thing.  \n")},
	}
	commands := []CommandBinding{{Name: "a", File: "commands/a.md"}}

	if err := ResolveCommandPrompts(fsys, commands); err != nil {
		t.Fatal(err)
	}
	if commands[0].Prompt != "Do the thing." {
		t.Fatalf("prompt = %q, want trimmed file contents", commands[0].Prompt)
	}
}

// A command that reaches the menu and submits nothing is worse than one that
// was never offered, so a missing or empty prompt fails product load rather
// than silently dropping the command.
func TestResolveCommandPromptsRejectsMissingOrEmptyFiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fsys     fstest.MapFS
		commands []CommandBinding
	}{
		{
			name:     "no file declared",
			fsys:     fstest.MapFS{},
			commands: []CommandBinding{{Name: "a"}},
		},
		{
			name:     "file does not exist",
			fsys:     fstest.MapFS{},
			commands: []CommandBinding{{Name: "a", File: "commands/missing.md"}},
		},
		{
			name:     "file is blank",
			fsys:     fstest.MapFS{"commands/a.md": &fstest.MapFile{Data: []byte("   \n")}},
			commands: []CommandBinding{{Name: "a", File: "commands/a.md"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ResolveCommandPrompts(tc.fsys, tc.commands); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
