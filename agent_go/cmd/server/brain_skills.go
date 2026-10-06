package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// Company skills live in Brain (PLAT-576). A platform agent finds them with search_skills and installs one into its
// own workspace with install_skill(source="brain:<folder>"): the files are read through the caller's Brain access
// (and its project's Brain mode) and copied into the workspace's skills/ folder, where they work like any other skill
// of that workflow, Crew or Code project (PLAT-581). Re-install to take a newer version.
const brainSkillSourcePrefix = "brain:"

func brainSkillFolder(source string) (string, bool) {
	if !strings.HasPrefix(source, brainSkillSourcePrefix) {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(strings.TrimPrefix(source, brainSkillSourcePrefix)), "/"), true
}

func brainSkillCall(ctx context.Context, args map[string]any) (map[string]any, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return nil, fmt.Errorf("Brain skills need a signed-in caller")
	}
	raw, err := knowledgebaseExecute(ctx, claims.UserID, false, knowledgebase.ToolSkills, args)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// installBrainSkill copies one Brain skill into a workspace's skills/ folder.
func installBrainSkill(ctx context.Context, workspaceAPIURL, workspacePath, folder, productID string) (string, error) {
	if folder == "" {
		return "", fmt.Errorf("name the Brain skill folder, for example brain:Company/Skills/release-notes")
	}
	got, err := brainSkillCall(ctx, map[string]any{"action": "get", "folder_path": folder})
	if err != nil {
		return "", fmt.Errorf("read Brain skill %s: %w", folder, err)
	}
	files := map[string][]byte{}
	list, _ := got["files"].([]any)
	for _, item := range list {
		f, _ := item.(map[string]any)
		rel, _ := f["path"].(string)
		if encoded, ok := f["content_base64"].(string); ok {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return "", fmt.Errorf("Brain skill file %s is not valid base64", rel)
			}
			files[rel] = data
		} else {
			text, _ := f["content"].(string)
			files[rel] = []byte(text)
		}
	}
	name := path.Base(folder)
	if err := skills.InstallSkillFilesIn(workspaceAPIURL, workspacePath, name, files); err != nil {
		return "", fmt.Errorf("install Brain skill %s: %w", folder, err)
	}
	version, _ := got["version"].(string)
	return fmt.Sprintf("Installed Brain skill **%s** (version %s, %d files) into this workspace's skills/ folder as `%s`. Re-install it to take a newer version. %s", folder, version, len(files), name, skillSelectionHint(productID, "It")), nil
}

// brainSkillSearch lists company skills matching query for search_skills; empty when Brain is unavailable here.
func brainSkillSearch(ctx context.Context, query string) string {
	out, err := brainSkillCall(ctx, map[string]any{"action": "list", "query": query})
	if err != nil {
		return ""
	}
	list, _ := out["skills"].([]any)
	if len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Company skills in Brain (%d)\n\nInstall with `install_skill` using source `brain:<folder>`.\n\n", len(list))
	for _, item := range list {
		s, _ := item.(map[string]any)
		fmt.Fprintf(&sb, "- **%v** (brain:%v) — %v\n", s["name"], s["folder_path"], s["description"])
	}
	return sb.String() + "\n"
}
