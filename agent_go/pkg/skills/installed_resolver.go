package skills

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// InstalledSkillFile mirrors mcpagent's read_skill fallback payload without
// importing it, so this package stays free of agent-runtime dependencies.
type InstalledSkillFile struct {
	Content        string
	Description    string
	AvailableFiles []string
}

// NewInstalledSkillReader returns a resolver for skills that are installed in a
// workspace but not attached to the agent.
//
// Progressive disclosure attaches a router skill and leaves the specialists on
// disk; reaching one previously meant the agent had to know a filesystem path
// and shell out, which loses read_skill's per-call limits and behaves
// differently on each provider. This grants no new access — the session's
// folder guard already exposes skills/ — it only lets the agent ask by name.
func NewInstalledSkillReader(workspaceAPIURL, workspacePath string) func(string, string) (InstalledSkillFile, error) {
	return func(skillName, relPath string) (InstalledSkillFile, error) {
		// The name is joined into a workspace path just like relPath below, so it
		// gets the same host-boundary check. read_skill supplies it directly, so
		// it is model-controlled input.
		name, err := ValidateSkillName(skillName)
		if err != nil {
			return InstalledSkillFile{}, err
		}
		// The caller normalises the path, but this is a host boundary: re-check
		// rather than trust that the only caller always will.
		clean := strings.TrimSpace(relPath)
		if clean == "" {
			clean = SkillFileName
		}
		if path.IsAbs(clean) || strings.Contains(clean, "..") || strings.ContainsRune(clean, '\x00') {
			return InstalledSkillFile{}, fmt.Errorf("skill path must be a safe relative path: %q", relPath)
		}

		if clean == SkillFileName {
			skill, err := GetSkillIn(workspaceAPIURL, workspacePath, name)
			if err != nil {
				return InstalledSkillFile{}, err
			}
			return InstalledSkillFile{
				Content:        skill.Content,
				Description:    skill.Frontmatter.Description,
				AvailableFiles: installedSkillFileNames(workspaceAPIURL, workspacePath, name),
			}, nil
		}
		for _, file := range loadSkillSupportingFilesIn(workspaceAPIURL, workspacePath, name) {
			if file.RelPath == clean {
				if !utf8.Valid(file.Content) {
					return InstalledSkillFile{}, fmt.Errorf("cannot read binary skill file as text")
				}
				return InstalledSkillFile{Content: string(file.Content), AvailableFiles: installedSkillFileNames(workspaceAPIURL, workspacePath, name)}, nil
			}
		}

		return InstalledSkillFile{}, fmt.Errorf("file %q is not part of installed skill %q", clean, name)
	}
}

// installedSkillFileNames lists what else the agent could read. Best-effort:
// an empty list is a weaker result, not a failed read.
func installedSkillFileNames(workspaceAPIURL, workspacePath, name string) []string {
	names := []string{SkillFileName}
	for _, file := range loadSkillSupportingFilesIn(workspaceAPIURL, workspacePath, name) {
		names = append(names, file.RelPath)
	}
	return names
}
