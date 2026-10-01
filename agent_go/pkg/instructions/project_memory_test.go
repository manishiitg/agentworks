package instructions

import (
	"strings"
	"testing"
)

func TestProjectMemoryKeepsPolicyUpfrontAndProceduresModeScoped(t *testing.T) {
	writer := ProjectMemorySkill(false)
	reader := ProjectMemorySkill(true)
	for _, mode := range []bool{false, true} {
		prompt := ProjectMemoryPrompt(mode)
		for _, rule := range []string{"MEMORY.md", "one durable memory store", "Skills hold procedures", "Never retain secrets", "explicit user request", "project-memory skill"} {
			if !strings.Contains(prompt, rule) {
				t.Fatalf("mode=%v lost upfront rule %s", mode, rule)
			}
		}
		if strings.Contains(prompt, "YYYY-MM-DD") || strings.Contains(prompt, "**Summary:**") {
			t.Fatal("format tutorial belongs in the skill")
		}
	}
	for _, procedure := range []string{"## YYYY-MM-DD", "**Summary:**", "merge an existing topic", "Replace or remove stale entries", "forget this", "Never save guesses"} {
		if !strings.Contains(writer.Content, procedure) {
			t.Fatalf("memory writer lost %s", procedure)
		}
	}
	if strings.Contains(reader.Content, "**Summary:**") || strings.Contains(reader.Content, "Save stable") || !strings.Contains(reader.Content, "Do not create or update") {
		t.Fatal("reader received memory authoring instructions")
	}
	writer.Content = "changed"
	if strings.Contains(ProjectMemorySkill(false).Content, "changed") {
		t.Fatal("sessions share a mutable memory skill")
	}
}
