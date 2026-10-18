package agentworksclient

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// installedSkillDir is the skill folder name written by the install flow.
//
//go:embed skills/agentworks/SKILL.md
var installedSkillFile embed.FS

//go:embed skills/publish/SKILL.md
var publicationSkillFile embed.FS

const EntrySkillVersion = "1.0.7"

// EntrySkillFiles is shared by CLI installation and hosted skill packages.
func EntrySkillFiles() map[string]string {
	selection := productpolicy.Selection{}
	content := EntrySkill()
	return map[string]string{
		"SKILL.md":                 content,
		"references/products.md":   productguide.Guide(selection),
		"references/cli-setup.md":  productguide.CLISetup(selection),
		"references/versions.json": productguide.VersionsForSkill(selection, "agentworks", EntrySkillVersion, content),
	}
}

// InstallSkill writes the bundled AgentWorks skill into dir/agentworks/.
// It refuses to overwrite an existing installation without force so a user's
// customized copy is never clobbered silently.
func InstallSkill(dir string, force bool) (string, error) {
	target := filepath.Join(dir, "agentworks", "SKILL.md")
	files := map[string]string{}
	for relative, content := range EntrySkillFiles() {
		files[filepath.Join("agentworks", relative)] = content
	}
	publication, err := publicationSkillFile.ReadFile("skills/publish/SKILL.md")
	if err != nil {
		return "", err
	}
	files[filepath.Join("publish", "SKILL.md")] = string(publication)
	for relative := range files {
		path := filepath.Join(dir, filepath.FromSlash(relative))
		if _, err := os.Lstat(path); err == nil && !force {
			return "", fmt.Errorf("skill already installed at %s (use force to overwrite)", path)
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	for relative, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return target, nil
}

// EntrySkill is the same small pointer installed locally and served remotely.
func EntrySkill() string {
	content, err := installedSkillFile.ReadFile("skills/agentworks/SKILL.md")
	if err != nil {
		panic(err)
	}
	return strings.Replace(string(content), "# AgentWorks\n", "# AgentWorks\n\n"+productguide.Overview(productpolicy.Selection{})+"\n\nRead [the product guide](references/products.md) for abilities, roles and examples, or fetch `help action=topic topic=product-guide` from the current server. Guide source version: "+productguide.GuideVersion+".\n", 1)
}
