package skills

import (
	"path"
	"strings"
	"testing"
)

// A skill name is joined into a workspace path (skills/<name>/<relPath>), and
// path.Join collapses "..", so an unvalidated name escapes skills/ exactly the
// way an unvalidated relPath does. NewInstalledSkillReader already rejected the
// relPath side; these pin the same rule for the name it is joined with.
func TestValidateSkillNameRejectsAnythingButOneSegment(t *testing.T) {
	rejected := []struct {
		name  string
		input string
	}{
		{"parent traversal", "../secrets"},
		{"nested traversal", "../../etc/passwd"},
		{"traversal mid-path", "video-creation/../../../etc"},
		{"trailing traversal", "video-creation/.."},
		{"absolute", "/etc/passwd"},
		{"absolute workspace", "/_users/alice/skills/x"},
		{"foreign user hop", "_users/alice/../bob"},
		{"embedded separator", "a/b"},
		{"backslash separator", `a\b`},
		{"NUL byte", "video\x00creation"},
		{"dot", "."},
		{"dotdot", ".."},
		{"empty", ""},
		{"whitespace only", "   "},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSkillName(tc.input); err == nil {
				t.Fatalf("ValidateSkillName(%q) was accepted; it must be rejected", tc.input)
			}
		})
	}
}

func TestValidateSkillNameAcceptsRealSkillNames(t *testing.T) {
	accepted := []string{
		"video-creation",
		"hyperframes-quality",
		"video_editing",
		"Skill.With.Dots",
		"  padded-name  ", // trimmed, not rejected
	}
	for _, input := range accepted {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			got, err := ValidateSkillName(input)
			if err != nil {
				t.Fatalf("ValidateSkillName(%q) = %v, want accepted", input, err)
			}
			if got != strings.TrimSpace(input) {
				t.Fatalf("ValidateSkillName(%q) = %q, want the trimmed name", input, got)
			}
		})
	}
}

// Demonstrates why the check is required rather than cosmetic: without it the
// join silently produces a path outside skills/, which is what the workspace
// read would then have been asked for.
func TestUnvalidatedSkillNameWouldEscapeTheSkillsRoot(t *testing.T) {
	const base = "Chats/Example Product/projects/launch/skills"
	escaped := path.Join(base, "../../../../etc", "SKILL.md")
	if strings.HasPrefix(escaped, base) {
		t.Fatalf("expected path.Join to collapse .. out of the skills root, got %q", escaped)
	}

	if _, err := ValidateSkillName("../../../../etc"); err == nil {
		t.Fatal("the name that produces that escape must be rejected")
	}
}
