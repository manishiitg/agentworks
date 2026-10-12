package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	if !builderProductSelection(claims).Has("knowledgebase") {
		return nil, fmt.Errorf("Brain is unavailable in this installation or to this account")
	}
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
	version, _ := got["version"].(string)
	if edited, err := installedBrainSkillEdited(workspaceAPIURL, workspacePath, name); err == nil && edited {
		return "", fmt.Errorf("the installed copy of %s was edited in this workspace, so installing %s again would overwrite those edits; uninstall_skill %s first if you want the Brain version", name, folder, name)
	}
	files[brainSkillMarkerFile] = brainSkillMarker(folder, version, files)
	if err := skills.InstallSkillFilesIn(workspaceAPIURL, workspacePath, name, files); err != nil {
		return "", fmt.Errorf("install Brain skill %s: %w", folder, err)
	}
	return fmt.Sprintf("Installed Brain skill **%s** (version %s, %d files) into this workspace's skills/ folder as `%s`. Re-install it to take a newer version. %s", folder, version, len(files), name, skillSelectionHintForContext(ctx, productID, "It")), nil
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

// brainSkillMarkerFile records, beside an installed Brain skill, where it came from: the Brain folder, the version
// installed and a fingerprint of each file, so list_skills can say "update available" and "edited here".
const brainSkillMarkerFile = ".brain-source.json"

func brainSkillMarker(folder, version string, files map[string][]byte) []byte {
	sums := map[string]string{}
	for rel, data := range files {
		if rel == brainSkillMarkerFile {
			continue
		}
		sum := sha256.Sum256(data)
		sums[rel] = hex.EncodeToString(sum[:])
	}
	out, _ := json.Marshal(map[string]any{"folder": folder, "version": version, "files": sums})
	return out
}

// installedBrainSkillEdited reports whether an installed copy of name came from Brain and was changed since.
func installedBrainSkillEdited(workspaceAPIURL, workspacePath, name string) (bool, error) {
	inventory, err := skills.DiscoverSkillsIn(workspaceAPIURL, workspacePath)
	if err != nil {
		return false, err
	}
	for _, skill := range inventory.Skills {
		if skill.FolderName == name && skill.BrainSource != "" {
			return skill.BrainEdited, nil
		}
	}
	return false, nil
}

type brainSkillRecord struct {
	Folder  string `json:"folder"`
	Version string `json:"version"`
}

// brainSkillCurrentVersions maps a Brain skill folder to its current version, but only when some installed skill came
// from Brain (one Brain call, none otherwise or when Brain is unavailable to the caller).
func brainSkillCurrentVersions(ctx context.Context, installed []skills.Skill) map[string]string {
	hasBrain := false
	for _, skill := range installed {
		hasBrain = hasBrain || skill.BrainSource != ""
	}
	if !hasBrain {
		return nil
	}
	out, err := brainSkillCall(ctx, map[string]any{"action": "list"})
	if err != nil {
		return nil
	}
	versions := map[string]string{}
	list, _ := out["skills"].([]any)
	for _, item := range list {
		s, _ := item.(map[string]any)
		folder, _ := s["folder_path"].(string)
		version, _ := s["version"].(string)
		versions[folder] = version
	}
	return versions
}

// brainSkillUpdateNote is the text list_skills appends to an installed skill that came from Brain.
func brainSkillUpdateNote(skill skills.Skill, current map[string]string) string {
	if skill.BrainSource == "" {
		return ""
	}
	var rec brainSkillRecord
	if json.Unmarshal([]byte(skill.BrainSource), &rec) != nil || rec.Folder == "" {
		return ""
	}
	note := fmt.Sprintf(" [Brain skill brain:%s, installed %s", rec.Folder, shortSkillVersion(rec.Version))
	switch latest, known := current[rec.Folder]; {
	case current == nil:
	case !known:
		note += "; no longer in Brain or not visible to you"
	case latest != rec.Version:
		note += fmt.Sprintf("; UPDATE AVAILABLE (Brain has %s): install_skill source brain:%s", shortSkillVersion(latest), rec.Folder)
	default:
		note += "; up to date"
	}
	if skill.BrainEdited {
		note += "; edited in this workspace, so an update needs uninstall_skill first"
	}
	return note + "]"
}

func shortSkillVersion(v string) string {
	v = strings.TrimPrefix(v, "v_")
	if len(v) > 8 {
		return v[:8]
	}
	return v
}
