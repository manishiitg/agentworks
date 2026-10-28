package commands

import "testing"

func TestValidateFolderNameRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../other", "folder/name", "folder name"} {
		if err := validateFolderName(name); err == nil {
			t.Fatalf("validateFolderName(%q) accepted an unsafe command folder", name)
		}
	}
	for _, name := range []string{"quick-review", "review_code", "status2"} {
		if err := validateFolderName(name); err != nil {
			t.Fatalf("validateFolderName(%q): %v", name, err)
		}
	}
}

// Any Lucide icon name is accepted for a command; an unknown name is refused
// so the reply card never silently draws a fallback icon.
func TestValidateCommandContentChecksLucideIconName(t *testing.T) {
	content := func(icon string) string {
		return "---\nname: ticket\ndescription: \"Update ticket\"\nicon: \"" + icon + "\"\n---\n\nDo it.\n"
	}
	for _, icon := range []string{"ticket", "git-branch", ""} {
		if _, _, err := ValidateCommandContent(content(icon)); err != nil {
			t.Fatalf("icon %q refused: %v", icon, err)
		}
	}
	if _, _, err := ValidateCommandContent(content("lightning-bolt-x")); err == nil {
		t.Fatal("unknown icon name was accepted")
	}
}
