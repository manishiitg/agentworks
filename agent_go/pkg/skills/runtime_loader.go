package skills

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// LoadAttachable resolves a list of selected skill folder names to
// `*llmtypes.Skill` values suitable for `mcpagent.AgentDefinition.Skills`.
//
// For each folder name the loader:
//  1. Reads workspace/skills/<name>/SKILL.md, parsing the YAML
//     frontmatter and body.
//  2. Walks the skill folder and pulls any supporting files
//     (scripts/, references/, assets/ etc.) so adapters that project
//     skills to disk get the full bundle, not just SKILL.md.
//
// Skills that fail to load (missing folder, parse error, network
// failure) are logged and skipped; the loader returns the skills it
// could resolve rather than failing the whole attach. This matches the
// behavior of the old buildSkillPrompt path which silently fell back to
// a one-line stub for unreadable skills.
//
// LoadGlobalSkill returns a small "pointer" skill that directs the
// agent to read the workflow's accumulated learnings bundle at
// learnings/_global/ (SKILL.md + references/ + any scripts/, assets/)
// from the workflow folder, instead of copying the bundle into the
// projected skills directory.
//
// Why a pointer instead of a full bundle copy: the global learnings
// can be large and grows over time, with multiple references/ files.
// Copying the tree into .agents/skills/workflow-learnings/ on every
// session launch duplicates the content and risks drift if the workflow
// updates _global/ mid-session. The pointer skill stays tiny (one
// short SKILL.md), and the agent reads the authoritative files
// from the workflow folder when it needs them.
//
// The skill is named "workflow-learnings" (not the on-disk folder name
// "_global") so listings and description-based matching carry meaning;
// the learnings folder itself keeps the _global name.
//
// Returns nil when the workflow has no _global/SKILL.md yet — no
// point attaching a pointer to a file that doesn't exist.
//
// workflowPath is workspace-relative (e.g. "Workflow/HDFC-Personal-Accounts").
func LoadGlobalSkill(workspaceAPIURL, workflowPath string) *llmtypes.Skill {
	if strings.TrimSpace(workflowPath) == "" {
		return nil
	}
	client := NewWorkspaceAPIClient(workspaceAPIURL)
	skillPath := path.Join(workflowPath, "learnings", "_global", "SKILL.md")
	if content, err := client.ReadFile(skillPath); err != nil || strings.TrimSpace(content) == "" {
		return nil
	}
	body := fmt.Sprintf(`This skill is a pointer to the workflow's accumulated execution know-how.

When you need to recall how a step worked in this workflow — selectors, API quirks, timing patterns, conventions established by prior runs — read the authoritative files in the workflow folder:

- %s/learnings/_global/SKILL.md (the main guide)
- %s/learnings/_global/references/ (per-topic detail files, if any)
- %s/learnings/_global/scripts/ and assets/ (if any)

These files are written by step agents during successful runs and shared across the workflow. They live in the workflow folder rather than this skills directory so they remain the single source of truth as the workflow learns more.

If a referenced file does not exist, the workflow has not accumulated that piece of knowledge yet — proceed with general best practices for that area.
`, workflowPath, workflowPath, workflowPath)
	return &llmtypes.Skill{
		Name:        "workflow-learnings",
		Description: "Pointer to the workflow's accumulated learnings (selectors, timings, API quirks, conventions). Read learnings/_global/ in the workflow folder for the full content.",
		Content:     body,
		Source:      llmtypes.SkillSource{Origin: "global-learnings"},
	}
}

func LoadAttachable(workspaceAPIURL string, selectedSkills []string) []*llmtypes.Skill {
	return LoadAttachableIn(workspaceAPIURL, "", selectedSkills)
}

// WithAgentBrowserCapability makes the product-owned browser skill part of an
// agent's identity whenever that agent is actually given the managed browser
// tool. Browser capability and its operating contract are one unit: callers
// must not require every workflow or step to select the built-in skill again.
//
// A fresh slice is returned so request and persisted workflow configuration
// remain unchanged. The built-in skill is appended only when absent.
func WithAgentBrowserCapability(selectedSkills []string, hasAgentBrowser bool) []string {
	if !hasAgentBrowser {
		return selectedSkills
	}
	for _, name := range selectedSkills {
		if strings.EqualFold(strings.TrimSpace(name), "agent-browser") {
			return selectedSkills
		}
	}
	out := append([]string(nil), selectedSkills...)
	return append(out, "agent-browser")
}

// LoadAttachableIn resolves each skill against the given workspace first, then
// the user-level skills folder. LoadGlobalSkill already took a workspace path;
// attachment did not, so a workspace's own skills/ folder could never be found.
func LoadAttachableIn(workspaceAPIURL, workspacePath string, selectedSkills []string) []*llmtypes.Skill {
	if len(selectedSkills) == 0 {
		return nil
	}
	out := make([]*llmtypes.Skill, 0, len(selectedSkills))
	// Deduplicate on the resolved name, which is what the agent definition is
	// keyed by: two selections can land on one name without repeating a folder
	// (a folder whose SKILL.md frontmatter renames it to another folder's
	// name), and a caller may simply pass the same name twice. Attaching both
	// fails the whole turn at finalization, so drop the repeat here instead.
	// Trimming matches the validator, which compares trimmed names.
	seen := make(map[string]struct{}, len(selectedSkills))
	for _, folderName := range selectedSkills {
		skill, err := loadOneAttachable(workspaceAPIURL, workspacePath, folderName)
		if err != nil {
			log.Printf("[SKILLS] Failed to load %s: %v (skipping)", folderName, err)
			continue
		}
		name := strings.TrimSpace(skill.Name)
		if _, duplicate := seen[name]; duplicate {
			log.Printf("[SKILLS] Skipping %s: skill %q is already attached", folderName, name)
			continue
		}
		seen[name] = struct{}{}
		out = append(out, skill)
	}
	return out
}

func loadOneAttachable(workspaceAPIURL, workspacePath, folderName string) (*llmtypes.Skill, error) {
	if skill := builtinAttachableSkill(folderName); skill != nil {
		return skill, nil
	}

	parsed, err := GetSkillIn(workspaceAPIURL, workspacePath, folderName)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}
	skill := &llmtypes.Skill{
		Name:                   folderName,
		Description:            parsed.Frontmatter.Description,
		Content:                lazySkillBody(parsed.FilePath, folderName, parsed.Content),
		DisableModelInvocation: parsed.Frontmatter.DisableModelInvocation,
		Source: llmtypes.SkillSource{
			Origin:    "imported",
			SourceURL: parsed.SourceURL,
		},
	}
	if parsed.Frontmatter.Name != "" && parsed.Frontmatter.Name != folderName {
		// Preserve the author-declared name only when it differs from
		// the folder; otherwise the writer would emit the folder name
		// anyway. Mismatches usually indicate a stale frontmatter.
		skill.Name = parsed.Frontmatter.Name
	}

	// Walk the skill folder for supporting files. Failures here are
	// non-fatal — SKILL.md alone is still a valid attach.
	skill.SupportingFiles = loadSkillSupportingFilesIn(workspaceAPIURL, workspacePath, folderName)
	return skill, nil
}

// Imported skill bodies above lazySkillBodyLineThreshold lines are injected
// as an excerpt plus a pointer instead of in full. Reference-heavy imported
// skills run 300-800 lines (~1.5-3k tokens each), paid on every run of every
// step that selects them whether or not the depth is needed. This mirrors the
// LoadGlobalSkill pointer pattern: the prompt carries the quick-start head,
// and the agent reads the authoritative on-disk SKILL.md (read access comes
// from BuildSkillFolderGuardPaths) only when it needs more. Small skills are
// injected whole — an excerpt of a skill that fits anyway just loses detail.
const (
	lazySkillBodyLineThreshold = 150
	lazySkillExcerptLines      = 60
)

// lazySkillBody returns the prompt-injected body for an imported skill:
// the full body when small, or the first lazySkillExcerptLines lines plus a
// read-the-rest pointer when large. filePath is the workspace-relative
// SKILL.md path (e.g. "skills/ffmpeg/SKILL.md").
func lazySkillBody(filePath, folderName, body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) <= lazySkillBodyLineThreshold {
		return body
	}
	if strings.TrimSpace(filePath) == "" {
		filePath = path.Join("skills", folderName, "SKILL.md")
	}
	excerpt := strings.TrimRight(strings.Join(lines[:lazySkillExcerptLines], "\n"), "\n")
	return excerpt + fmt.Sprintf(
		"\n\n---\n**This is an excerpt (%d of %d lines).** Before relying on any detail not shown above, read the full skill at `%s` (workspace-relative). Supporting files (references/, scripts/, assets/) live next to it under `%s/`.\n",
		lazySkillExcerptLines, len(lines), filePath, path.Dir(filePath))
}

// loadSkillSupportingFilesIn reads supporting files from the resolved workspace
// skill folder. The service validates paths and preserves binary assets.
func loadSkillSupportingFilesIn(workspaceAPIURL, workspacePath, folderName string) []llmtypes.SkillFile {
	var result struct {
		Files []llmtypes.SkillFile `json:"files"`
	}
	if err := workspaceSkillOperation(context.Background(), workspaceAPIURL, "files", workspacePath, folderName, &result); err != nil {
		log.Printf("[SKILLS] Could not load skill %q files from %s: %v", folderName, workspacePath, err)
		return nil
	}
	return result.Files
}
