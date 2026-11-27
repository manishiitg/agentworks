package server

import (
	"context"
	"fmt"
	"strings"

	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// buildSkillCallbacks creates SkillCallbacks that bridge the workshop tools
// to the workspace skills API. Returns nil-safe callbacks.
func (api *StreamingAPI) buildSkillCallbacks() *todo_creation_human.SkillCallbacks {
	return api.buildSkillCallbacksForProduct("")
}

func skillSelectionHintForContext(ctx context.Context, productID, skillNames string) string {
	if root, _ := ctx.Value(externalCrewMCPPlaceKey{}).(string); root != "" && productID == "work" {
		return "Use settings with this crew_id to inspect and select installed skills for future project turns, or use Setup > Skills in the app."
	}
	return skillSelectionHint(productID, skillNames)
}

func skillSelectionHint(productID, skillNames string) string {

	if strings.EqualFold(strings.TrimSpace(productID), "work") {
		return fmt.Sprintf("%s can be loaded immediately with read_skill. To attach it automatically on future project turns, call skills(action=\"select\", skill=\"<folder-name>\"); project persists that selection in workflow.json and also shows it in Setup > Skills.", skillNames)
	}
	return fmt.Sprintf("Use update_workflow_config to add %s to the workflow's selected skills.", skillNames)
}

// projectSkillCallbacks points install, import and uninstall at one project's
// skills folder; listing and uninstall use that same workspace inventory.
func projectSkillCallbacks(base *todo_creation_human.SkillCallbacks, projectSkillsDir, productID string) *todo_creation_human.SkillCallbacks {
	wsURL := getWorkspaceAPIURL()
	scoped := *base
	workspacePath := strings.TrimSuffix(projectSkillsDir, "/skills")
	scoped.ListSkills = func(ctx context.Context) (string, error) {
		inventory, err := skills.DiscoverSkillsIn(wsURL, workspacePath)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "## Skills in this workspace (%d)\n", len(inventory.Skills))
		brainVersions := brainSkillCurrentVersions(ctx, inventory.Skills)
		for _, skill := range inventory.Skills {
			fmt.Fprintf(&sb, "- **%s** (folder %s, path %s): %s", skill.Frontmatter.Name, skill.FolderName, skill.FilePath, skill.Frontmatter.Description)
			sb.WriteString(brainSkillUpdateNote(skill, brainVersions))
			if len(skill.UsedBy) > 0 {
				fmt.Fprintf(&sb, " — %s", strings.Join(skill.UsedBy, ", "))
			}
			sb.WriteString("\n")
		}
		return sb.String(), nil
	}
	scoped.InstallSkill = func(ctx context.Context, source string) (string, error) {
		if folder, fromBrain := brainSkillFolder(source); fromBrain {
			return installBrainSkill(ctx, wsURL, workspacePath, folder, productID)
		}
		result, err := skills.ImportToWorkspaceDir(ctx, wsURL, source, projectSkillsDir)
		if err != nil {
			return "", fmt.Errorf("failed to install skill: %w", err)
		}
		if len(result.InstalledSkills) == 0 {
			return "", fmt.Errorf("no skills were installed; check the source")
		}
		return fmt.Sprintf("Installed into this workspace's private skills/ folder: %s. %s", strings.Join(result.InstalledSkills, ", "), skillSelectionHintForContext(ctx, productID, "These skills")), nil
	}
	scoped.ImportSkill = func(ctx context.Context, githubURL, token string) (string, error) {
		resp, err := skills.ImportGitHubSkillInto(wsURL, githubURL, token, projectSkillsDir)
		if err != nil {
			return "", fmt.Errorf("failed to import skill: %w", err)
		}
		if !resp.Success {
			return "", fmt.Errorf("failed to import skill: %s", resp.Error)
		}
		return fmt.Sprintf("Imported skill **%s** into this workspace's private skills/ folder. %s", resp.SkillName, skillSelectionHintForContext(ctx, productID, "It")), nil
	}
	scoped.DeleteSkill = func(ctx context.Context, folderName string) error {
		return skills.UninstallSkillIn(ctx, wsURL, workspacePath, folderName)
	}
	return &scoped
}

func (api *StreamingAPI) buildSkillCallbacksForProduct(productID string) *todo_creation_human.SkillCallbacks {
	return &todo_creation_human.SkillCallbacks{
		ListSkills: func(ctx context.Context) (string, error) { return "", fmt.Errorf("choose a workspace to list skills") },
		ImportSkill: func(ctx context.Context, githubURL, token string) (string, error) {
			return "", fmt.Errorf("choose a workspace to import skills")
		},
		DeleteSkill: func(ctx context.Context, folderName string) error {
			return fmt.Errorf("choose a workspace to uninstall skills")
		},
		SearchSkills: func(ctx context.Context, query string) (string, error) {
			// Company skills (Brain) first, then the public registry.
			company := brainSkillSearch(ctx, query)
			results, err := skills.FindSkills(ctx, query)
			if err != nil {
				if company != "" {
					return company, nil
				}
				return "", fmt.Errorf("failed to search skills: %w", err)
			}
			if len(results) == 0 {
				if company != "" {
					return company, nil
				}
				return "No skills found matching your query.", nil
			}
			var sb strings.Builder
			sb.WriteString(company)
			sb.WriteString(fmt.Sprintf("## Public registry results (%d found)\n\n", len(results)))
			sb.WriteString("Install with: `install_skill` tool using the source value.\n\n")
			for _, r := range results {
				sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s\n", r.Skill, r.Source, r.Installs))
			}
			return sb.String(), nil
		},
		InstallSkill: func(ctx context.Context, source string) (string, error) {
			return "", fmt.Errorf("choose a workspace to install skills")
		},
	}
}

func (api *StreamingAPI) registerMultiAgentSkillTools(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool, productID ...string) error {
	activeProductID := ""
	if len(productID) > 0 {
		activeProductID = productID[0]
	}
	return api.registerMultiAgentSkillToolsIn(registrar, disabled, activeProductID, "")
}

// registerMultiAgentSkillToolsIn registers the skill tools. A non-empty
// projectSkillsDir (a private Code's docs-relative skills folder) makes
// list, install, import and uninstall act on that workspace only.
func (api *StreamingAPI) registerMultiAgentSkillToolsIn(registrar interface {
	RegisterCustomTool(string, string, map[string]interface{}, func(context.Context, map[string]interface{}) (string, error), string) error
}, disabled func(string) bool, activeProductID, projectSkillsDir string) error {
	skillFuncs := api.buildSkillCallbacksForProduct(activeProductID)
	if skillFuncs == nil {
		return fmt.Errorf("skill callbacks unavailable")
	}
	if projectSkillsDir = strings.Trim(strings.TrimSpace(projectSkillsDir), "/"); projectSkillsDir != "" {
		skillFuncs = projectSkillCallbacks(skillFuncs, projectSkillsDir, activeProductID)
	}

	registerTool := func(name, description string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error)) error {
		if disabled != nil && disabled(name) {
			return nil
		}
		return registrar.RegisterCustomTool(name, description, params, exec, "skill_tools")
	}

	if err := api.registerNativeSkillTools(registrar, disabled, activeProductID, projectSkillsDir); err != nil {
		return err
	}

	if err := registerTool(
		"list_skills",
		"List skills available in the workspace. Use this to inspect installed skills before selecting them in chat settings or reading their files directly.",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			return skillFuncs.ListSkills(ctx)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"import_skill",
		"Import a skill from GitHub into the workspace. Imported skills become available for future chats and can also be read directly from the skills folder.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"github_url": map[string]interface{}{
					"type":        "string",
					"description": "GitHub URL of the skill to import, either a repo URL or a direct path to a skill folder.",
				},
				"token": map[string]interface{}{
					"type":        "string",
					"description": "Optional GitHub personal access token for private repositories.",
				},
			},
			"required": []string{"github_url"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			githubURL, _ := args["github_url"].(string)
			if strings.TrimSpace(githubURL) == "" {
				return "github_url is required.", nil
			}
			token, _ := args["token"].(string)
			return skillFuncs.ImportSkill(ctx, githubURL, token)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"uninstall_skill",
		"Remove an installed skill from the workspace. Use list_skills first to confirm the folder name.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"folder_name": map[string]interface{}{
					"type":        "string",
					"description": "The skill folder name returned by list_skills.",
				},
			},
			"required": []string{"folder_name"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			folderName, _ := args["folder_name"].(string)
			if strings.TrimSpace(folderName) == "" {
				return "folder_name is required.", nil
			}
			if err := skillFuncs.DeleteSkill(ctx, folderName); err != nil {
				return "", fmt.Errorf("failed to uninstall skill %q: %w", folderName, err)
			}
			return fmt.Sprintf("Successfully uninstalled skill %q from workspace.", folderName), nil
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"search_skills",
		"Search the public skills registry. <!-- product:knowledgebase -->Also search company skills in Brain (your company's shared skills).<!-- /product --> Use install_skill with a returned source value to install one into this workspace.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search terms such as 'browser automation', 'social media', or 'data analysis'.",
				},
			},
			"required": []string{"query"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			if strings.TrimSpace(query) == "" {
				return "query is required.", nil
			}
			return skillFuncs.SearchSkills(ctx, query)
		},
	); err != nil {
		return err
	}

	if err := registerTool(
		"install_skill",
		"Install a public registry skill into this workspace with owner/repo@skill-name. <!-- product:knowledgebase -->For a company skill in Brain, use source brain:<folder> (for example brain:Company/Skills/release-notes).<!-- /product --> Use search_skills first to find valid sources.",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"source": map[string]interface{}{
					"type":        "string",
					"description": "Skill source: owner/repo@skill-name for the public registry. <!-- product:knowledgebase -->Use brain:<folder> for a company skill in Brain.<!-- /product -->",
				},
			},
			"required": []string{"source"},
		},
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			source, _ := args["source"].(string)
			if strings.TrimSpace(source) == "" {
				return "source is required (e.g. 'owner/repo@skill-name').", nil
			}
			return skillFuncs.InstallSkill(ctx, source)
		},
	); err != nil {
		return err
	}

	return nil
}
