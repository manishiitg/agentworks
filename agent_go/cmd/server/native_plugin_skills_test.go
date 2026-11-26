package server

import (
	"path/filepath"
	"testing"
)

// The picker lists only skills Claude itself loads: turned-on plugins, project
// plugins only in their own project, and no skills marked not user-invocable.
func TestClaudePluginSkillsListOnlyWhatClaudeLoads(t *testing.T) {
	home, project, other := t.TempDir(), t.TempDir(), t.TempDir()
	plugin := func(name string) string {
		dir := filepath.Join(home, "plugins", "cache", name, "1.0.0")
		recognitionWrite(t, filepath.Join(dir, "skills", "map", "SKILL.md"), "---\nname: map\ndescription: Map the code\n---\nBody.")
		recognitionWrite(t, filepath.Join(dir, "skills", "inner", "SKILL.md"), "---\nname: inner\ndescription: Internal\nuser-invocable: false\n---\nBody.")
		return dir
	}
	recognitionWrite(t, filepath.Join(home, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"on@market":[{"scope":"user","installPath":"`+plugin("on")+`"}],
		"off@market":[{"scope":"user","installPath":"`+plugin("off")+`"}],
		"proj@market":[{"scope":"project","projectPath":"`+project+`","installPath":"`+plugin("proj")+`"}]}}`)
	recognitionWrite(t, filepath.Join(home, "settings.json"), `{"enabledPlugins":{"on@market":true,"off@market":false}}`)
	recognitionWrite(t, filepath.Join(project, ".claude", "settings.json"), `{"enabledPlugins":{"proj@market":true}}`)

	names := func(dir string) []string {
		var out []string
		for _, s := range claudePluginSkills(home, dir) {
			out = append(out, s.Name)
		}
		return out
	}
	if got := names(project); len(got) != 2 || got[0] != "on:map" || got[1] != "proj:map" {
		t.Fatalf("in its project = %v, want [on:map proj:map]", got)
	}
	if got := names(other); len(got) != 1 || got[0] != "on:map" {
		t.Fatalf("elsewhere = %v, want [on:map]", got)
	}
}
