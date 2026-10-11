package agentworksclient

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// installedSkillDir is the skill folder name written by the install flow.
//
//go:embed skills/agentworks/SKILL.md
var installedSkillFile embed.FS

// InstallSkill writes the bundled AgentWorks skill into dir/agentworks/.
// It refuses to overwrite an existing installation without force so a user's
// customized copy is never clobbered silently.
func InstallSkill(dir string, force bool) (string, error) {
	content, err := installedSkillFile.ReadFile("skills/agentworks/SKILL.md")
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "agentworks", "SKILL.md")
	if _, err := os.Stat(target); err == nil && !force {
		return "", fmt.Errorf("skill already installed at %s (use force to overwrite)", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// EntrySkill is the same small pointer installed locally and served remotely.
func EntrySkill() string {
	content, err := installedSkillFile.ReadFile("skills/agentworks/SKILL.md")
	if err != nil {
		panic(err)
	}
	return string(content)
}
